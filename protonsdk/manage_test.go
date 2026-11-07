package protonsdk

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	proton "github.com/ProtonMail/go-proton-api"
)

// exists reports whether the account still holds message id.
func (a account) exists(t *testing.T, id string) bool {
	t.Helper()
	metas, err := a.client.GetMessageMetadataPage(context.Background(), 0, pageSize, proton.MessageFilter{ID: []string{id}})
	if err != nil {
		t.Fatal(err)
	}
	return slices.ContainsFunc(metas, func(meta proton.MessageMetadata) bool { return meta.ID == id })
}

func outcomeCodes(t *testing.T, got map[string]any) []any {
	t.Helper()
	rows := got["outcomes"].([]any)
	codes := make([]any, len(rows))
	for i, row := range rows {
		codes[i] = row.(map[string]any)["code"]
	}
	return codes
}

func TestDeleteRemovesMessagesForGood(t *testing.T) {
	f := newFixture(t)
	f.user("alice")
	alice := f.account("alice")
	doomed := alice.deliver(t, "doomed", "one", true)
	kept := alice.deliver(t, "kept", "two", true)
	session := loginAs(t, "alice")

	got := result(t, call(t, map[string]any{"op": "delete", "session": session, "ids": []string{doomed}}))
	if codes := outcomeCodes(t, got); !slices.Equal(codes, []any{nil}) || got["session"] == nil {
		t.Fatalf("delete = %v", got)
	}
	if alice.exists(t, doomed) || !alice.exists(t, kept) {
		t.Fatal("delete removed the wrong messages")
	}
	if empty := result(t, call(t, map[string]any{"op": "delete", "session": session, "ids": []string{}})); len(outcomeCodes(t, empty)) != 0 {
		t.Fatalf("an empty batch = %v", empty)
	}
}

func TestEmptyFolderClearsTrashAndSpamOnly(t *testing.T) {
	f := newFixture(t)
	f.user("alice")
	alice := f.account("alice")
	ctx := context.Background()
	kept := alice.deliver(t, "kept", "one", true)
	var trashed []string
	for _, subject := range []string{"old one", "old two", "old three"} {
		trashed = append(trashed, alice.deliver(t, subject, "gone", true))
	}
	if err := alice.client.LabelMessages(ctx, trashed, proton.TrashLabel); err != nil {
		t.Fatal(err)
	}
	spam := alice.deliver(t, "spam", "buy now", true)
	if err := alice.client.LabelMessages(ctx, []string{spam}, proton.SpamLabel); err != nil {
		t.Fatal(err)
	}
	session := loginAs(t, "alice")

	got := result(t, call(t, map[string]any{"op": "empty_folder", "session": session, "folder": proton.TrashLabel}))
	if got["deleted"] != float64(len(trashed)) || got["more"] != false {
		t.Fatalf("empty Trash = %v", got)
	}
	for _, id := range trashed {
		if alice.exists(t, id) {
			t.Fatalf("%s survived emptying Trash", id)
		}
	}
	if !alice.exists(t, kept) || !alice.exists(t, spam) {
		t.Fatal("emptying Trash touched mail outside it")
	}
	if got := result(t, call(t, map[string]any{"op": "empty_folder", "session": session, "folder": proton.SpamLabel})); got["deleted"] != float64(1) {
		t.Fatalf("empty Spam = %v", got)
	}
	if got := result(t, call(t, map[string]any{"op": "empty_folder", "session": session, "folder": proton.TrashLabel})); got["deleted"] != float64(0) {
		t.Fatalf("empty an empty Trash = %v", got)
	}

	for folder, want := range map[string]string{
		proton.InboxLabel:   "invalid_destination",
		proton.ArchiveLabel: "invalid_destination",
		"a/b":               "invalid_id",
	} {
		if code := errorCode(t, call(t, map[string]any{"op": "empty_folder", "session": session, "folder": folder})); code != want {
			t.Errorf("empty %s code = %q, want %q", folder, code, want)
		}
	}
	if !alice.exists(t, kept) {
		t.Fatal("a refused empty deleted mail")
	}

	// Too little budget left to start a round: nothing is deleted and the
	// caller is told to come back.
	alice.deliver(t, "late", "trash", true)
	short := result(t, callWithin(t, emptyReserve/2, map[string]any{"op": "empty_folder", "session": session, "folder": proton.TrashLabel}))
	if short["deleted"] != float64(0) || short["more"] != true {
		t.Fatalf("empty with no budget = %v", short)
	}
}

func TestLabelTagsWithUserLabelsOnly(t *testing.T) {
	f := newFixture(t)
	f.user("alice")
	alice := f.account("alice")
	ctx := context.Background()
	id := alice.deliver(t, "tag me", "body", true)
	label, err := alice.client.CreateLabel(ctx, proton.CreateLabelReq{Name: "Work", Color: defaultFolderColor, Type: proton.LabelTypeLabel})
	if err != nil {
		t.Fatal(err)
	}
	folder, err := alice.client.CreateLabel(ctx, proton.CreateLabelReq{Name: "Receipts", Color: defaultFolderColor, Type: proton.LabelTypeFolder})
	if err != nil {
		t.Fatal(err)
	}
	session := loginAs(t, "alice")

	got := result(t, call(t, map[string]any{"op": "label", "session": session, "ids": []string{id}, "label": label.ID, "is_labelled": true}))
	if !slices.Equal(outcomeCodes(t, got), []any{nil}) {
		t.Fatalf("label = %v", got)
	}
	if labels := alice.metadata(t, id).LabelIDs; !slices.Contains(labels, label.ID) || !slices.Contains(labels, proton.InboxLabel) {
		t.Fatalf("labels after tagging = %v", labels)
	}
	result(t, call(t, map[string]any{"op": "label", "session": session, "ids": []string{id}, "label": label.ID, "is_labelled": false}))
	if labels := alice.metadata(t, id).LabelIDs; slices.Contains(labels, label.ID) || !slices.Contains(labels, proton.InboxLabel) {
		t.Fatalf("labels after untagging = %v", labels)
	}

	for target, want := range map[string]string{
		folder.ID:           "invalid_destination",
		proton.StarredLabel: "invalid_destination",
		proton.InboxLabel:   "invalid_destination",
		"bm9sYWJlbA==":      "folder_not_found",
		"a/b":               "invalid_id",
	} {
		if code := errorCode(t, call(t, map[string]any{"op": "label", "session": session, "ids": []string{id}, "label": target, "is_labelled": true})); code != want {
			t.Errorf("label with %s code = %q, want %q", target, code, want)
		}
	}
	if code := errorCode(t, call(t, map[string]any{"op": "label", "session": session, "ids": []string{id}, "label": label.ID})); code != "invalid_request" {
		t.Errorf("label without is_labelled code = %q", code)
	}
}

func folderRows(t *testing.T, session map[string]any) map[string]map[string]any {
	t.Helper()
	listed := result(t, call(t, map[string]any{"op": "sync", "session": session, "limit": 1}))
	rows := map[string]map[string]any{}
	for _, row := range listed["folders"].([]any) {
		entry := row.(map[string]any)
		rows[entry["id"].(string)] = entry
	}
	return rows
}

func TestFoldersAreCreatedRenamedAndDeleted(t *testing.T) {
	f := newFixture(t)
	f.user("alice")
	alice := f.account("alice")
	session := loginAs(t, "alice")
	// go-proton-api's test server keeps no colours, so the label writes
	// are checked as they go out.
	var writes []map[string]any
	f.setIntercept(func(_ http.ResponseWriter, r *http.Request, _ func(http.ResponseWriter)) bool {
		if strings.HasPrefix(r.URL.Path, "/core/v4/labels") && (r.Method == http.MethodPost || r.Method == http.MethodPut) {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("label write body: %v", err)
			}
			writes = append(writes, body)
		}
		return false
	})

	created := result(t, call(t, map[string]any{"op": "create_folder", "session": session, "name": "  Projects ", "kind": "folder"}))["folder"].(map[string]any)
	parent := created["id"].(string)
	if created["label"] != "Projects" || created["kind"] != "folder" || created["role"] != "other" || created["parent"] != "" {
		t.Fatalf("created folder = %v", created)
	}
	child := result(t, call(t, map[string]any{"op": "create_folder", "session": session, "name": "2026", "kind": "folder", "parent": parent}))["folder"].(map[string]any)
	if child["parent"] != parent {
		t.Fatalf("child folder = %v", child)
	}
	tag := result(t, call(t, map[string]any{"op": "create_folder", "session": session, "name": "Urgent", "kind": "label", "color": "#cf5858"}))["folder"].(map[string]any)
	if tag["kind"] != "label" {
		t.Fatalf("created label = %v", tag)
	}

	renamed := result(t, call(t, map[string]any{"op": "rename_folder", "session": session, "id": child["id"], "name": "Older"}))["folder"].(map[string]any)
	if renamed["id"] != child["id"] || renamed["label"] != "Older" || renamed["kind"] != "folder" || renamed["parent"] != parent {
		t.Fatalf("renamed folder = %v", renamed)
	}
	if len(writes) != 4 || writes[0]["Color"] != defaultFolderColor || writes[0]["Type"] != float64(proton.LabelTypeFolder) ||
		writes[2]["Color"] != "#cf5858" || writes[2]["Type"] != float64(proton.LabelTypeLabel) ||
		writes[3]["Name"] != "Older" || writes[3]["ParentID"] != parent {
		t.Fatalf("label writes = %v", writes)
	}
	f.setIntercept(nil)

	id := alice.deliver(t, "filed", "body", true)
	result(t, call(t, map[string]any{"op": "move", "session": session, "ids": []string{id}, "destination": child["id"]}))
	result(t, call(t, map[string]any{"op": "delete_folder", "session": session, "id": child["id"]}))
	if rows := folderRows(t, session); rows[child["id"].(string)] != nil || rows[parent] == nil {
		t.Fatalf("folders after delete = %v", rows)
	}
	if !alice.exists(t, id) {
		t.Fatal("deleting a folder deleted its mail")
	}

	for name, tc := range map[string]struct {
		request map[string]any
		want    string
	}{
		"rename Inbox":    {map[string]any{"op": "rename_folder", "id": proton.InboxLabel, "name": "x"}, "folder_readonly"},
		"delete Trash":    {map[string]any{"op": "delete_folder", "id": proton.TrashLabel}, "folder_readonly"},
		"delete a view":   {map[string]any{"op": "delete_folder", "id": proton.AllDraftsLabel}, "folder_readonly"},
		"rename unknown":  {map[string]any{"op": "rename_folder", "id": "bm9mb2xkZXI=", "name": "x"}, "folder_not_found"},
		"unknown parent":  {map[string]any{"op": "create_folder", "name": "x", "kind": "folder", "parent": "bm9mb2xkZXI="}, "folder_not_found"},
		"empty name":      {map[string]any{"op": "create_folder", "name": "  ", "kind": "folder"}, "invalid_request"},
		"control in name": {map[string]any{"op": "rename_folder", "id": parent, "name": "a\nb"}, "invalid_request"},
		"unknown kind":    {map[string]any{"op": "create_folder", "name": "x", "kind": "system"}, "invalid_request"},
		"bad colour":      {map[string]any{"op": "create_folder", "name": "x", "kind": "label", "color": "red"}, "invalid_request"},
		"path id":         {map[string]any{"op": "delete_folder", "id": "../x"}, "invalid_id"},
	} {
		tc.request["session"] = session
		if code := errorCode(t, call(t, tc.request)); code != tc.want {
			t.Errorf("%s code = %q, want %q", name, code, tc.want)
		}
	}
}

func TestSaveDraftWritesAndReplacesDrafts(t *testing.T) {
	f := newFixture(t)
	f.user("alice")
	f.user("bob")
	alice := f.account("alice")
	bob := f.account("bob")
	session := loginAs(t, "alice")
	draft := func(subject string, extra map[string]any) map[string]any {
		request := map[string]any{
			"op": "save_draft", "session": session,
			"sender": alice.address.Email, "sender_name": "Alice",
			"to":      []map[string]any{{"name": "Bob", "address": bob.address.Email}},
			"subject": subject, "body_html": "<p>Not yet</p>",
			"attachments": []map[string]any{{
				"name": "notes.txt", "mime_type": "text/plain", "cid": nil,
				"data": base64.StdEncoding.EncodeToString([]byte("draft notes")),
			}},
		}
		for key, value := range extra {
			request[key] = value
		}
		return call(t, request)
	}

	first := result(t, draft("first try", nil))
	firstID := first["id"].(string)
	if first["replaced"] != false || first["message_id"] == "" || first["session"] == nil {
		t.Fatalf("first draft = %v", first)
	}
	meta := alice.metadata(t, firstID)
	if !meta.IsDraft() || meta.Subject != "first try" || meta.NumAttachments != 1 || !slices.Contains(meta.LabelIDs, proton.DraftsLabel) {
		t.Fatalf("first draft metadata = %+v", meta)
	}
	if f.sendCount() != 0 {
		t.Fatal("saving a draft sent it")
	}

	second := result(t, draft("second try", map[string]any{"replaces": firstID, "message_id": "<draft-1@example.com>"}))
	if second["replaced"] != true || second["id"] == firstID || second["message_id"] != "draft-1@example.com" {
		t.Fatalf("second draft = %v", second)
	}
	if alice.exists(t, firstID) || !alice.exists(t, second["id"].(string)) {
		t.Fatal("the replaced draft is still there, or the new one is not")
	}

	// replaces only ever deletes a draft.
	inbox := alice.deliver(t, "not a draft", "body", true)
	third := result(t, draft("third try", map[string]any{"replaces": inbox}))
	if third["replaced"] != false || !alice.exists(t, inbox) {
		t.Fatalf("replacing a received message = %v", third)
	}

	// A draft may have no recipients yet.
	bare := result(t, call(t, map[string]any{
		"op": "save_draft", "session": session, "sender": alice.address.Email,
		"subject": "", "body_html": "",
	}))
	if !alice.metadata(t, bare["id"].(string)).IsDraft() {
		t.Fatal("a bare draft is not a draft")
	}

	for name, tc := range map[string]struct {
		extra map[string]any
		want  string
	}{
		"bad recipient":   {map[string]any{"to": []map[string]any{{"address": "not an address"}}}, "invalid_recipient"},
		"foreign sender":  {map[string]any{"sender": "mallory@example.com"}, "sender_unknown"},
		"bad replaces":    {map[string]any{"replaces": "a/b"}, "invalid_id"},
		"bad attachment":  {map[string]any{"attachments": []map[string]any{{"name": "x", "mime_type": "text/plain", "cid": nil, "data": "!!"}}}, "attachment_failed"},
		"action sans ids": {map[string]any{"action": "reply"}, "invalid_request"},
		"delivery field":  {map[string]any{"delivery": "secure"}, "invalid_request"},
	} {
		if code := errorCode(t, draft("refused", tc.extra)); code != tc.want {
			t.Errorf("%s code = %q, want %q", name, code, tc.want)
		}
	}
}

func TestNewOperationsValidateBeforeAnyNetwork(t *testing.T) {
	session := `{"login":"a","user_id":"u","uid":"x","access_token":"t","refresh_token":"r","key_pass":""}`
	cases := map[string]struct{ raw, want string }{
		"delete sans ids":       {`{"version":1,"op":"delete","session":` + session + `}`, "invalid_request"},
		"delete bad id":         {`{"version":1,"op":"delete","session":` + session + `,"ids":["a b"]}`, "invalid_id"},
		"delete sans session":   {`{"version":1,"op":"delete","ids":["a"]}`, "session_invalid"},
		"label sans flag":       {`{"version":1,"op":"label","session":` + session + `,"ids":["a"],"label":"l"}`, "invalid_request"},
		"empty sans session":    {`{"version":1,"op":"empty_folder","folder":"3"}`, "session_invalid"},
		"empty unknown key":     {`{"version":1,"op":"empty_folder","session":` + session + `,"folder":"3","all":true}`, "invalid_request"},
		"create sans session":   {`{"version":1,"op":"create_folder","name":"x","kind":"folder"}`, "session_invalid"},
		"draft sans session":    {`{"version":1,"op":"save_draft","sender":"a@b.c","subject":"","body_html":""}`, "session_invalid"},
		"oversized delete":      {`{"version":1,"op":"delete","session":` + session + `,"ids":["a","a","a","a","a","a","a","a","a","a","a","a","a","a","a","a","a","a","a","a","a","a","a","a","a","a","a","a","a","a","a","a","a","a","a","a","a","a","a","a","a","a","a","a","a","a","a","a","a","a","a"]}`, "invalid_batch"},
		"rename folder sans id": {`{"version":1,"op":"rename_folder","session":` + session + `,"id":"","name":"x"}`, "invalid_id"},
	}
	for name, tc := range cases {
		if got := codeOf(t, tc.raw); got != tc.want {
			t.Errorf("%s code = %q, want %q", name, got, tc.want)
		}
	}
	// The reserve leaves a run room for its last round and the reply.
	if emptyReserve >= operationTimeout || emptyReserve < 5*time.Second {
		t.Fatalf("emptyReserve = %v", emptyReserve)
	}
}
