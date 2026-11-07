package protonsdk

import (
	"context"
	"errors"
	"net/http"
	"slices"

	proton "github.com/ProtonMail/go-proton-api"
)

// Event pages: GetEvent already chains up to 50 events per call.
const maxEventRounds = 10

type eventsParams struct {
	envelopeFields
	sessionRequest
	Since *string `json:"since"`
}

// eventsOp is the poll: what changed since an event id, by folder and by
// message id, without listing or re-reading anything. Proton offers no push
// channel to third-party clients (its apps' push goes through Proton's own
// FCM/APNs registrations), so a caller watches an account by calling this
// on a timer and syncing the folders it names.
func eventsOp(ctx context.Context, raw []byte) envelope {
	var params eventsParams
	if !decodeStrict(raw, &params) || params.Since == nil {
		return failure("invalid_request")
	}
	if !params.Session.usable() {
		return failure("session_invalid")
	}
	if !validID(*params.Since) {
		return failure("invalid_id")
	}
	session := resume(*params.Session)
	defer session.close()
	return session.events(ctx, *params.Since)
}

func (s *apiSession) events(ctx context.Context, since string) envelope {
	var log messageLog
	folders := map[string]bool{}
	foldersChanged := false
	eventID, refresh, more, err := s.walkEvents(ctx, since, func(event proton.Event) {
		if len(event.Labels) > 0 {
			foldersChanged = true
		}
		for _, item := range event.Messages {
			log.add(item)
			if item.Action == proton.EventDelete {
				continue
			}
			for _, label := range item.Message.LabelIDs {
				if validID(label) {
					folders[label] = true
				}
			}
		}
	})
	if err != nil {
		return s.failErr(err)
	}
	if refresh {
		return s.ok(map[string]any{
			"event_id": eventID, "refresh": true, "more": false, "folders_changed": true,
			"mail_changed": []string{}, "updated": []string{}, "deleted": []string{},
		})
	}
	deleted, updated := log.split()
	changed := make([]string, 0, len(folders))
	for label := range folders {
		changed = append(changed, label)
	}
	slices.Sort(changed)
	return s.ok(map[string]any{
		"event_id": eventID, "refresh": false, "more": more, "folders_changed": foldersChanged,
		"mail_changed": changed, "updated": orEmpty(updated), "deleted": orEmpty(deleted),
	})
}

func orEmpty(ids []string) []string {
	if ids == nil {
		return []string{}
	}
	return ids
}

// walkEvents replays the event log from since, at most maxEventRounds pages,
// handing every event to visit. more reports that the log goes on past the
// returned event id. A refresh the server demands, or an event id it no
// longer knows, comes back as refresh with the event id to resync from.
func (s *apiSession) walkEvents(ctx context.Context, since string, visit func(proton.Event)) (eventID string, refresh, more bool, err error) {
	eventID = since
	for range maxEventRounds {
		events, pending, err := s.client.GetEvent(ctx, eventID)
		if err != nil {
			if !staleEvent(err) || s.wasDeauthed() {
				return "", false, false, err
			}
			latest, err := s.client.GetLatestEventID(ctx)
			if err != nil {
				return "", false, false, err
			}
			return latest, true, false, nil
		}
		for _, event := range events {
			if event.EventID != "" {
				eventID = event.EventID
			}
			if event.Refresh&proton.RefreshMail != 0 {
				refresh = true
			}
			visit(event)
		}
		more = pending
		if !pending {
			break
		}
	}
	if refresh {
		more = false
	}
	return eventID, refresh, more, nil
}

// messageLog folds message events into the ids deleted and the ids created
// or changed, in first-seen order; the last action on an id wins.
type messageLog struct {
	order   []string
	deleted map[string]bool
}

func (l *messageLog) add(item proton.MessageEvent) {
	if !validID(item.ID) {
		return
	}
	if l.deleted == nil {
		l.deleted = map[string]bool{}
	}
	if _, seen := l.deleted[item.ID]; !seen {
		l.order = append(l.order, item.ID)
	}
	l.deleted[item.ID] = item.Action == proton.EventDelete
}

func (l *messageLog) split() (deleted, changed []string) {
	for _, id := range l.order {
		if l.deleted[id] {
			deleted = append(deleted, id)
		} else {
			changed = append(changed, id)
		}
	}
	return deleted, changed
}

// staleEvent reports whether the server refused the event id itself (too
// old, unknown) rather than failing the request: a client error that is not
// about authentication or throttling.
func staleEvent(err error) bool {
	var apiErr *proton.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	switch apiErr.Status {
	case http.StatusBadRequest, http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity:
		return true
	}
	return false
}
