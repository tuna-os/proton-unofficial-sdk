package protonsdk

import (
	"context"
	"slices"

	proton "github.com/ProtonMail/go-proton-api"
)

type saveDraftParams struct {
	envelopeFields
	sessionRequest
	Sender      string           `json:"sender"`
	SenderName  string           `json:"sender_name"`
	To          []mailAddress    `json:"to"`
	Cc          []mailAddress    `json:"cc"`
	Bcc         []mailAddress    `json:"bcc"`
	ReplyTos    []mailAddress    `json:"reply_tos"`
	Subject     string           `json:"subject"`
	BodyHTML    string           `json:"body_html"`
	Attachments []sendAttachment `json:"attachments"`
	MessageID   *string          `json:"message_id"`
	InReplyTo   *string          `json:"in_reply_to"`
	References  *string          `json:"references"`
	Action      *string          `json:"action"`
	// Replaces is the id of the draft this one supersedes, deleted once the
	// new one is written.
	Replaces *string `json:"replaces"`
}

// saveDraftOp writes a draft to the Drafts folder, encrypted to the
// sender's key like every Proton draft. Saving again writes a new draft and
// deletes the one it replaces, as IMAP clients save drafts: replacing the
// attachments of an existing Proton draft would take a request per
// attachment and could leave a half-updated draft behind.
func saveDraftOp(ctx context.Context, raw []byte) envelope {
	var params saveDraftParams
	if !decodeStrict(raw, &params) {
		return failure("invalid_request")
	}
	if slices.ContainsFunc(recipientList(params.To, params.Cc, params.Bcc), func(a string) bool { return !validAddress(a) }) ||
		slices.ContainsFunc(params.ReplyTos, func(a mailAddress) bool { return !validAddress(a.Address) }) {
		return failure("invalid_recipient")
	}
	if !validAddress(params.Sender) {
		return failure("sender_unknown")
	}
	if params.Replaces != nil && !validID(*params.Replaces) {
		return failure("invalid_id")
	}
	externalID, ok := messageID(params.MessageID, params.Sender)
	if !ok {
		return failure("invalid_request")
	}
	thread, ok := threadingOf(params.InReplyTo, params.References, params.Action)
	if !ok {
		return failure("invalid_request")
	}
	staged, ok := stageAttachments(params.Attachments)
	if !ok {
		return failure("attachment_failed")
	}
	if !params.Session.usable() {
		return failure("session_invalid")
	}
	session := resume(*params.Session)
	defer session.close()
	return session.saveDraft(ctx, params, externalID, staged, thread)
}

func (s *apiSession) saveDraft(ctx context.Context, params saveDraftParams, externalID string, staged []stagedAttachment, thread threading) envelope {
	_, sender, addrKR, code := s.senderKey(ctx, params.Sender, "request_failed")
	if code != "" {
		return s.fail(code)
	}
	parentID, code := s.findParent(ctx, thread, sender.ID)
	if code == "send_failed" {
		code = "request_failed"
	}
	if code != "" {
		return s.fail(code)
	}
	action := proton.ForwardAction
	if parentID != "" {
		action = thread.action
	}
	content := draftContent{
		senderName: params.SenderName,
		subject:    params.Subject,
		bodyHTML:   params.BodyHTML,
		externalID: externalID,
		to:         params.To,
		cc:         params.Cc,
		bcc:        params.Bcc,
		replyTos:   params.ReplyTos,
	}
	draft, _, code := s.writeDraft(ctx, addrKR, sender, content, parentID, action, staged, s.classify)
	if code != "" {
		return s.fail(code)
	}
	replaced := false
	if params.Replaces != nil {
		replaced = s.deleteDraft(ctx, *params.Replaces)
	}
	return s.ok(map[string]any{
		"id":         draft.ID,
		"message_id": externalID,
		"replaced":   replaced,
	})
}

// deleteDraft deletes id if it is a draft, and reports whether it did. The
// new draft is already written, so a failure here costs a duplicate draft,
// not the user's text; anything that is not a draft is left alone.
func (s *apiSession) deleteDraft(ctx context.Context, id string) bool {
	metas, err := s.client.GetMessageMetadataPage(ctx, 0, 1, proton.MessageFilter{ID: []string{id}})
	if err != nil || len(metas) != 1 || metas[0].ID != id || !metas[0].IsDraft() {
		return false
	}
	return s.client.DeleteMessage(ctx, id) == nil
}
