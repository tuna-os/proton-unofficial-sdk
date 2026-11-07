package protonsdk

import (
	"context"
	"slices"
	"time"

	proton "github.com/ProtonMail/go-proton-api"
)

// The Tuta helper's batch cap: larger batches are refused rather than
// half-applied, so callers chunk first.
const batchLimit = 50

type markReadParams struct {
	envelopeFields
	sessionRequest
	IDs    []string `json:"ids"`
	IsRead *bool    `json:"is_read"`
}

type moveParams struct {
	envelopeFields
	sessionRequest
	IDs         []string `json:"ids"`
	Destination string   `json:"destination"`
}

type starParams struct {
	envelopeFields
	sessionRequest
	IDs       []string `json:"ids"`
	IsStarred *bool    `json:"is_starred"`
}

type deleteParams struct {
	envelopeFields
	sessionRequest
	IDs []string `json:"ids"`
}

type labelParams struct {
	envelopeFields
	sessionRequest
	IDs        []string `json:"ids"`
	Label      string   `json:"label"`
	IsLabelled *bool    `json:"is_labelled"`
}

type emptyFolderParams struct {
	envelopeFields
	sessionRequest
	Folder string `json:"folder"`
}

// Folders empty_folder may clear: what Proton's own clients offer to empty.
var emptiableFolders = []string{proton.TrashLabel, proton.SpamLabel}

// empty_folder stops starting new rounds this close to the deadline and
// reports more, rather than running into the budget mid-delete.
const emptyReserve = 15 * time.Second

// checkBatch validates ids before anything is sent: the batch cap, then
// every id's shape (ids are spliced into requests).
func checkBatch(ids []string) string {
	if ids == nil {
		return "invalid_request"
	}
	if len(ids) > batchLimit {
		return "invalid_batch"
	}
	for _, id := range ids {
		if !validID(id) {
			return "invalid_id"
		}
	}
	return ""
}

func markReadOp(ctx context.Context, raw []byte) envelope {
	var params markReadParams
	if !decodeStrict(raw, &params) || params.IsRead == nil {
		return failure("invalid_request")
	}
	if code := checkBatch(params.IDs); code != "" {
		return failure(code)
	}
	if !params.Session.usable() {
		return failure("session_invalid")
	}
	session := resume(*params.Session)
	defer session.close()
	isRead := *params.IsRead
	return session.batch(ctx, params.IDs, func(ctx context.Context, ids ...string) error {
		if isRead {
			return session.client.MarkMessagesRead(ctx, ids...)
		}
		return session.client.MarkMessagesUnread(ctx, ids...)
	})
}

func moveOp(ctx context.Context, raw []byte) envelope {
	var params moveParams
	if !decodeStrict(raw, &params) {
		return failure("invalid_request")
	}
	if code := checkBatch(params.IDs); code != "" {
		return failure(code)
	}
	if !validID(params.Destination) {
		return failure("invalid_id")
	}
	if !params.Session.usable() {
		return failure("session_invalid")
	}
	session := resume(*params.Session)
	defer session.close()
	if code := session.checkDestination(ctx, params.Destination); code != "" {
		return session.fail(code)
	}
	return session.batch(ctx, params.IDs, func(ctx context.Context, ids ...string) error {
		// Labelling with a folder (an exclusive label) is Proton's move: the
		// server drops the message's previous folder.
		return session.client.LabelMessages(ctx, ids, params.Destination)
	})
}

// starOp adds or removes the Starred system label. Starring is a label, not
// a folder, so the message stays where it is.
func starOp(ctx context.Context, raw []byte) envelope {
	var params starParams
	if !decodeStrict(raw, &params) || params.IsStarred == nil {
		return failure("invalid_request")
	}
	if code := checkBatch(params.IDs); code != "" {
		return failure(code)
	}
	if !params.Session.usable() {
		return failure("session_invalid")
	}
	session := resume(*params.Session)
	defer session.close()
	isStarred := *params.IsStarred
	return session.batch(ctx, params.IDs, func(ctx context.Context, ids ...string) error {
		if isStarred {
			return session.client.LabelMessages(ctx, ids, proton.StarredLabel)
		}
		return session.client.UnlabelMessages(ctx, ids, proton.StarredLabel)
	})
}

// deleteOp deletes messages permanently, wherever they are. Proton's own
// clients only offer it in Trash, Spam and Drafts; the caller decides.
func deleteOp(ctx context.Context, raw []byte) envelope {
	var params deleteParams
	if !decodeStrict(raw, &params) {
		return failure("invalid_request")
	}
	if code := checkBatch(params.IDs); code != "" {
		return failure(code)
	}
	if !params.Session.usable() {
		return failure("session_invalid")
	}
	session := resume(*params.Session)
	defer session.close()
	return session.batch(ctx, params.IDs, session.client.DeleteMessage)
}

// labelOp adds or removes a user label (a tag). Folders are `move`'s and
// Starred is `star`'s, so neither is accepted here.
func labelOp(ctx context.Context, raw []byte) envelope {
	var params labelParams
	if !decodeStrict(raw, &params) || params.IsLabelled == nil {
		return failure("invalid_request")
	}
	if code := checkBatch(params.IDs); code != "" {
		return failure(code)
	}
	if !validID(params.Label) {
		return failure("invalid_id")
	}
	if !params.Session.usable() {
		return failure("session_invalid")
	}
	session := resume(*params.Session)
	defer session.close()
	if code := session.checkLabel(ctx, params.Label); code != "" {
		return session.fail(code)
	}
	isLabelled := *params.IsLabelled
	return session.batch(ctx, params.IDs, func(ctx context.Context, ids ...string) error {
		if isLabelled {
			return session.client.LabelMessages(ctx, ids, params.Label)
		}
		return session.client.UnlabelMessages(ctx, ids, params.Label)
	})
}

// emptyFolderOp deletes everything in Trash or Spam permanently, a page at
// a time, newest first. go-proton-api has no call for the API's own empty
// endpoint, and listing then deleting works the same on the real API and
// the test server. A folder too large for one run's budget comes back with
// more: call again.
func emptyFolderOp(ctx context.Context, raw []byte) envelope {
	var params emptyFolderParams
	if !decodeStrict(raw, &params) {
		return failure("invalid_request")
	}
	if !validID(params.Folder) {
		return failure("invalid_id")
	}
	if !slices.Contains(emptiableFolders, params.Folder) {
		return failure("invalid_destination")
	}
	if !params.Session.usable() {
		return failure("session_invalid")
	}
	session := resume(*params.Session)
	defer session.close()
	return session.emptyFolder(ctx, params.Folder)
}

func (s *apiSession) emptyFolder(ctx context.Context, folder string) envelope {
	deleted := map[string]bool{}
	for {
		if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < emptyReserve {
			return s.ok(map[string]any{"deleted": len(deleted), "more": true})
		}
		page, err := s.client.GetMessageMetadataPage(ctx, 0, pageSize, proton.MessageFilter{LabelID: folder, Desc: true})
		if err != nil {
			return s.failErr(err)
		}
		ids := make([]string, 0, len(page))
		for _, meta := range page {
			if !validID(meta.ID) {
				continue
			}
			// A message that survived its own delete means the server
			// refused it: stop rather than loop on it.
			if deleted[meta.ID] {
				return s.fail("request_failed")
			}
			ids = append(ids, meta.ID)
		}
		if len(ids) == 0 {
			return s.ok(map[string]any{"deleted": len(deleted), "more": false})
		}
		if err := s.client.DeleteMessage(ctx, ids...); err != nil {
			return s.failErr(err)
		}
		for _, id := range ids {
			deleted[id] = true
		}
	}
}

// checkDestination accepts the system folders mail can be filed into and
// the user's own folders. A label is not a place (adding one would leave the
// message where it is), nor are views like All Mail, Sent or Starred.
func (s *apiSession) checkDestination(ctx context.Context, destination string) string {
	for _, folder := range systemFolders {
		if folder.id == destination {
			if folder.movable {
				return ""
			}
			return "invalid_destination"
		}
	}
	labels, err := s.client.GetLabels(ctx, proton.LabelTypeFolder, proton.LabelTypeLabel)
	if err != nil {
		return s.classify(err)
	}
	index := slices.IndexFunc(labels, func(label proton.Label) bool { return label.ID == destination })
	switch {
	case index < 0:
		return "folder_not_found"
	case labels[index].Type != proton.LabelTypeFolder:
		return "invalid_destination"
	}
	return ""
}

// checkLabel accepts the user's own labels: the targets of `label`.
func (s *apiSession) checkLabel(ctx context.Context, label string) string {
	if slices.ContainsFunc(systemFolders, func(folder systemFolder) bool { return folder.id == label }) {
		return "invalid_destination"
	}
	labels, err := s.client.GetLabels(ctx, proton.LabelTypeFolder, proton.LabelTypeLabel)
	if err != nil {
		return s.classify(err)
	}
	index := slices.IndexFunc(labels, func(item proton.Label) bool { return item.ID == label })
	switch {
	case index < 0:
		return "folder_not_found"
	case labels[index].Type != proton.LabelTypeLabel:
		return "invalid_destination"
	}
	return ""
}

// batch applies one mutation to ids and reports per-id outcomes in input
// order, the Tuta helper's shape: {"id", "code"} with a null code for an
// applied id. A failure that says nothing about the ids (revoked session,
// outage, timeout) fails the whole request. A failure the server reported
// against the batch is replayed one id at a time — every mutation here is
// idempotent — so each id gets its own outcome; an outage mid-replay marks
// that id and leaves the rest not_attempted.
func (s *apiSession) batch(ctx context.Context, ids []string, apply func(ctx context.Context, ids ...string) error) envelope {
	rows := make([]map[string]any, len(ids))
	for i, id := range ids {
		rows[i] = map[string]any{"id": id, "code": nil}
	}
	if len(ids) == 0 {
		return s.ok(map[string]any{"outcomes": rows})
	}
	err := apply(ctx, ids...)
	if err == nil {
		return s.ok(map[string]any{"outcomes": rows})
	}
	if code := s.classify(err); code != "request_failed" {
		return s.fail(code)
	}
	for i, id := range ids {
		err := apply(ctx, id)
		if err == nil {
			continue
		}
		code := s.classify(err)
		rows[i]["code"] = code
		if code != "request_failed" {
			for _, row := range rows[i+1:] {
				row["code"] = "not_attempted"
			}
			break
		}
	}
	return s.ok(map[string]any{"outcomes": rows})
}
