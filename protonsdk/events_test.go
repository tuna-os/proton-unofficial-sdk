package protonsdk

import (
	"context"
	"slices"
	"testing"

	proton "github.com/ProtonMail/go-proton-api"
)

func stringsOf(t *testing.T, value any) []string {
	t.Helper()
	rows, ok := value.([]any)
	if !ok {
		t.Fatalf("not a list: %#v", value)
	}
	out := make([]string, len(rows))
	for i, row := range rows {
		out[i] = row.(string)
	}
	return out
}

func TestEventsNameChangedFoldersAndMessages(t *testing.T) {
	f := newFixture(t)
	f.user("alice")
	alice := f.account("alice")
	kept := alice.deliver(t, "kept", "one", true)
	doomed := alice.deliver(t, "doomed", "two", true)
	moved := alice.deliver(t, "moved", "three", true)
	session := loginAs(t, "alice")
	since := result(t, call(t, map[string]any{"op": "sync", "session": session, "limit": 10}))["event_id"].(string)

	quiet := result(t, call(t, map[string]any{"op": "events", "session": session, "since": since}))
	if quiet["refresh"] != false || quiet["folders_changed"] != false || len(stringsOf(t, quiet["mail_changed"])) != 0 ||
		len(stringsOf(t, quiet["updated"])) != 0 || len(stringsOf(t, quiet["deleted"])) != 0 || quiet["session"] == nil {
		t.Fatalf("nothing happened, yet events = %v", quiet)
	}

	ctx := context.Background()
	if err := alice.client.MarkMessagesRead(ctx, kept); err != nil {
		t.Fatal(err)
	}
	if err := alice.client.DeleteMessage(ctx, doomed); err != nil {
		t.Fatal(err)
	}
	if err := alice.client.LabelMessages(ctx, []string{moved}, proton.ArchiveLabel); err != nil {
		t.Fatal(err)
	}
	if _, err := alice.client.CreateLabel(ctx, proton.CreateLabelReq{Name: "Receipts", Color: "#7272a7", Type: proton.LabelTypeFolder}); err != nil {
		t.Fatal(err)
	}

	got := result(t, call(t, map[string]any{"op": "events", "session": session, "since": since}))
	if got["refresh"] != false || got["more"] != false || got["folders_changed"] != true {
		t.Fatalf("events = %v", got)
	}
	if got["event_id"] == since {
		t.Fatal("event id did not advance")
	}
	updated, deleted, folders := stringsOf(t, got["updated"]), stringsOf(t, got["deleted"]), stringsOf(t, got["mail_changed"])
	if !slices.Contains(updated, kept) || !slices.Contains(updated, moved) || slices.Contains(updated, doomed) {
		t.Fatalf("updated = %v", updated)
	}
	if !slices.Equal(deleted, []string{doomed}) {
		t.Fatalf("deleted = %v", deleted)
	}
	if !slices.Contains(folders, proton.InboxLabel) || !slices.Contains(folders, proton.ArchiveLabel) || !slices.IsSorted(folders) {
		t.Fatalf("mail_changed = %v", folders)
	}

	again := result(t, call(t, map[string]any{"op": "events", "session": session, "since": got["event_id"]}))
	if len(stringsOf(t, again["updated"])) != 0 || again["folders_changed"] != false {
		t.Fatalf("a poll from the returned id must be quiet: %v", again)
	}

	stale := result(t, call(t, map[string]any{"op": "events", "session": session, "since": "abc"}))
	if stale["refresh"] != true || stale["event_id"] == "" || stale["event_id"] == "abc" {
		t.Fatalf("an unknown event id must ask for a refresh: %v", stale)
	}
}

func TestEventsValidateBeforeAnyNetwork(t *testing.T) {
	session := `{"login":"a","user_id":"u","uid":"x","access_token":"t","refresh_token":"r","key_pass":""}`
	cases := map[string]struct{ raw, want string }{
		"no since":     {`{"version":1,"op":"events","session":` + session + `}`, "invalid_request"},
		"no session":   {`{"version":1,"op":"events","since":"abc"}`, "session_invalid"},
		"path since":   {`{"version":1,"op":"events","session":` + session + `,"since":"../x"}`, "invalid_id"},
		"unknown key":  {`{"version":1,"op":"events","session":` + session + `,"since":"a","limit":1}`, "invalid_request"},
		"number since": {`{"version":1,"op":"events","session":` + session + `,"since":1}`, "invalid_request"},
	}
	for name, tc := range cases {
		if got := codeOf(t, tc.raw); got != tc.want {
			t.Errorf("%s code = %q, want %q", name, got, tc.want)
		}
	}
}

func TestRequestReturnsTheEnvelopeWithoutNewline(t *testing.T) {
	got := Request(`{"version":2,"op":"login"}`)
	if got != `{"version":1,"error":{"code":"unsupported_version"}}` {
		t.Fatalf("Request = %q", got)
	}
	if got := Request(""); got != `{"version":1,"error":{"code":"invalid_request"}}` {
		t.Fatalf("empty Request = %q", got)
	}
}
