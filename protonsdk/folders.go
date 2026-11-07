package protonsdk

import (
	"context"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	proton "github.com/ProtonMail/go-proton-api"
)

// Proton's default label colour, used when the caller names none.
const defaultFolderColor = "#7272a7"

// Proton refuses longer label names.
const maxFolderName = 100

var folderColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

type createFolderParams struct {
	envelopeFields
	sessionRequest
	Name   string  `json:"name"`
	Kind   string  `json:"kind"`
	Parent *string `json:"parent"`
	Color  *string `json:"color"`
}

type renameFolderParams struct {
	envelopeFields
	sessionRequest
	ID   string `json:"id"`
	Name string `json:"name"`
}

type deleteFolderParams struct {
	envelopeFields
	sessionRequest
	ID string `json:"id"`
}

// folderName is name trimmed, or false when it is empty, too long or
// carries control characters.
func folderName(name string) (string, bool) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > maxFolderName || strings.ContainsFunc(name, unicode.IsControl) {
		return "", false
	}
	return name, true
}

// createFolderOp creates a user folder (kind "folder", a place mail is
// moved to) or label (kind "label", a tag), optionally under parent.
func createFolderOp(ctx context.Context, raw []byte) envelope {
	var params createFolderParams
	if !decodeStrict(raw, &params) {
		return failure("invalid_request")
	}
	name, ok := folderName(params.Name)
	if !ok {
		return failure("invalid_request")
	}
	var kind proton.LabelType
	switch params.Kind {
	case "folder":
		kind = proton.LabelTypeFolder
	case "label":
		kind = proton.LabelTypeLabel
	default:
		return failure("invalid_request")
	}
	color := defaultFolderColor
	if params.Color != nil {
		if !folderColor.MatchString(*params.Color) {
			return failure("invalid_request")
		}
		color = *params.Color
	}
	parent := ""
	if params.Parent != nil {
		if !validID(*params.Parent) {
			return failure("invalid_id")
		}
		parent = *params.Parent
	}
	if !params.Session.usable() {
		return failure("session_invalid")
	}
	session := resume(*params.Session)
	defer session.close()
	if parent != "" {
		if _, code := session.userLabel(ctx, parent); code != "" {
			return session.fail(code)
		}
	}
	created, err := session.client.CreateLabel(ctx, proton.CreateLabelReq{Name: name, Color: color, Type: kind, ParentID: parent})
	if err != nil {
		return session.failErr(err)
	}
	return session.folderRow(ctx, created.ID)
}

// renameFolderOp renames a user folder or label, keeping its colour and
// parent.
func renameFolderOp(ctx context.Context, raw []byte) envelope {
	var params renameFolderParams
	if !decodeStrict(raw, &params) {
		return failure("invalid_request")
	}
	name, ok := folderName(params.Name)
	if !ok {
		return failure("invalid_request")
	}
	if !validID(params.ID) {
		return failure("invalid_id")
	}
	if !params.Session.usable() {
		return failure("session_invalid")
	}
	session := resume(*params.Session)
	defer session.close()
	label, code := session.userLabel(ctx, params.ID)
	if code != "" {
		return session.fail(code)
	}
	if _, err := session.client.UpdateLabel(ctx, label.ID, proton.UpdateLabelReq{Name: name, Color: label.Color, ParentID: label.ParentID}); err != nil {
		return session.failErr(err)
	}
	return session.folderRow(ctx, label.ID)
}

// deleteFolderOp deletes a user folder or label. Its mail is not deleted:
// Proton keeps it in All Mail.
func deleteFolderOp(ctx context.Context, raw []byte) envelope {
	var params deleteFolderParams
	if !decodeStrict(raw, &params) {
		return failure("invalid_request")
	}
	if !validID(params.ID) {
		return failure("invalid_id")
	}
	if !params.Session.usable() {
		return failure("session_invalid")
	}
	session := resume(*params.Session)
	defer session.close()
	label, code := session.userLabel(ctx, params.ID)
	if code != "" {
		return session.fail(code)
	}
	if err := session.client.DeleteLabel(ctx, label.ID); err != nil {
		return session.failErr(err)
	}
	return session.ok(map[string]any{})
}

// userLabel finds one of the user's folders or labels. A system folder is
// folder_readonly; anything else not found is folder_not_found.
func (s *apiSession) userLabel(ctx context.Context, id string) (proton.Label, string) {
	if slices.ContainsFunc(systemFolders, func(folder systemFolder) bool { return folder.id == id }) || isSystemLabel(id) {
		return proton.Label{}, "folder_readonly"
	}
	labels, err := s.client.GetLabels(ctx, proton.LabelTypeFolder, proton.LabelTypeLabel)
	if err != nil {
		return proton.Label{}, s.classify(err)
	}
	index := slices.IndexFunc(labels, func(label proton.Label) bool { return label.ID == id })
	if index < 0 {
		return proton.Label{}, "folder_not_found"
	}
	return labels[index], ""
}

// isSystemLabel reports Proton's system label ids, small integers, which
// include views this library does not list (all-drafts, outbox, ...).
func isSystemLabel(id string) bool {
	return id != "" && !strings.ContainsFunc(id, func(r rune) bool { return r < '0' || r > '9' })
}

// folderRow replies with the folder row sync would list for id.
func (s *apiSession) folderRow(ctx context.Context, id string) envelope {
	rows, _, err := s.folders(ctx)
	if err != nil {
		return s.failErr(err)
	}
	for _, row := range rows {
		if row["id"] == id {
			return s.ok(map[string]any{"folder": row})
		}
	}
	return s.fail("folder_not_found")
}
