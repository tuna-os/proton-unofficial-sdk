package protonsdk

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	proton "github.com/ProtonMail/go-proton-api"
	"github.com/bradenaw/juniper/stream"
)

// incoming is one message to import, with the headers search looks at.
type incoming struct {
	from, to, subject string
	date              time.Time
	isUnread          bool
}

// importAll imports messages into the account's inbox in one batch and
// returns their ids in order.
func (a account) importAll(t *testing.T, messages ...incoming) []string {
	t.Helper()
	requests := make([]proton.ImportReq, len(messages))
	for i, message := range messages {
		literal := "From: " + message.from + "\r\nTo: " + message.to + "\r\nSubject: " + message.subject +
			"\r\nMessage-ID: <search-" + fmt.Sprint(i) + "-" + fmt.Sprint(message.date.UnixNano()) + "@example.com>" +
			"\r\nDate: " + message.date.Format(time.RFC1123Z) +
			"\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nbody\r\n"
		requests[i] = proton.ImportReq{
			Metadata: proton.ImportMetadata{
				AddressID: a.address.ID,
				LabelIDs:  []string{proton.InboxLabel},
				Unread:    proton.Bool(message.isUnread),
				Flags:     proton.MessageFlagReceived,
			},
			Message: []byte(literal),
		}
	}
	ctx := context.Background()
	results, err := a.client.ImportMessages(ctx, a.addrKR, 4, 4, requests...)
	if err != nil {
		t.Fatal(err)
	}
	imported, err := stream.Collect(ctx, results)
	if err != nil || len(imported) != len(messages) {
		t.Fatalf("import failed: %v %+v", err, imported)
	}
	ids := make([]string, len(imported))
	for i, result := range imported {
		if result.Code != proton.SuccessCode {
			t.Fatalf("import %d failed: %+v", i, result)
		}
		ids[i] = result.MessageID
	}
	return ids
}

// searchAll runs one search request to the end, following next_cursor, and
// returns every hit's id sorted, with the number of calls it took.
func searchAll(t *testing.T, request map[string]any) ([]string, int) {
	t.Helper()
	var found []string
	calls := 0
	for {
		calls++
		got := result(t, call(t, request))
		if got["session"] == nil {
			t.Fatalf("search reply carries no session: %v", got)
		}
		found = append(found, ids(got["messages"].([]any))...)
		next, ok := got["next_cursor"].(string)
		if !ok {
			break
		}
		request["cursor"] = next
	}
	slices.Sort(found)
	return found, calls
}

func sorted(ids ...string) []string {
	return slices.Sorted(slices.Values(ids))
}

// stampTimes sets each listed message's Time, which go-proton-api's test
// server leaves at zero, the way the live API fills it from the message.
func (f *fixture) stampTimes(times map[string]time.Time) {
	f.setIntercept(func(w http.ResponseWriter, r *http.Request, forward func(http.ResponseWriter)) bool {
		if r.URL.Path != "/mail/v4/messages" || r.Header.Get("X-HTTP-Method-Override") != http.MethodGet {
			return false
		}
		recorder := httptest.NewRecorder()
		forward(recorder)
		var listing map[string]any
		if err := json.Unmarshal(recorder.Body.Bytes(), &listing); err != nil {
			f.t.Errorf("listing reply is not JSON: %v", err)
			return false
		}
		messages, _ := listing["Messages"].([]any)
		for _, message := range messages {
			row := message.(map[string]any)
			if when, ok := times[row["ID"].(string)]; ok {
				row["Time"] = when.Unix()
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(recorder.Code)
		_ = json.NewEncoder(w).Encode(listing)
		return true
	})
}

func TestSearchMatchesMetadataCriteria(t *testing.T) {
	f := newFixture(t)
	userID := f.user("alice")
	alice := f.account("alice")
	ctx := context.Background()
	day := func(month time.Month, d int) time.Time { return time.Date(2026, month, d, 12, 0, 0, 0, time.UTC) }
	me := alice.address.Email
	got := alice.importAll(t,
		incoming{"Ada Lovelace <ada@example.com>", me, "Quarterly invoice", day(time.November, 1), true},
		incoming{"Bob <bob@example.org>", "Carol <carol@example.net>, " + me, "Lunch", day(time.November, 3), false},
		incoming{"ada@example.com", me, "Re: lunch plans", day(time.October, 1), false},
		incoming{"Dan <dan@example.com>", me, "Tagged", day(time.September, 1), false},
	)
	invoice, lunch, plans, tagged := got[0], got[1], got[2], got[3]
	if err := alice.client.LabelMessages(ctx, []string{plans}, proton.StarredLabel); err != nil {
		t.Fatal(err)
	}
	label, err := f.srv.CreateLabel(userID, "Important", "", proton.LabelTypeLabel)
	if err != nil {
		t.Fatal(err)
	}
	if err := alice.client.LabelMessages(ctx, []string{tagged}, label); err != nil {
		t.Fatal(err)
	}
	f.stampTimes(map[string]time.Time{
		invoice: day(time.November, 1), lunch: day(time.November, 3), plans: day(time.October, 1), tagged: day(time.September, 1),
	})
	session := loginAs(t, "alice")
	november := day(time.November, 1).UnixMilli()

	for _, test := range []struct {
		name     string
		criteria map[string]any
		want     []string
	}{
		{"sender name or address, any case", map[string]any{"sender": "ADA"}, sorted(invoice, plans)},
		{"recipient among To", map[string]any{"recipient": "carol"}, sorted(lunch)},
		{"keyword in the subject", map[string]any{"keyword": "invoice"}, sorted(invoice)},
		{"keyword in the sender", map[string]any{"keyword": "bob@"}, sorted(lunch)},
		{"subject only", map[string]any{"subject": "LUNCH"}, sorted(lunch, plans)},
		{"after, inclusive", map[string]any{"after_ms": november}, sorted(invoice, lunch)},
		{"before, exclusive", map[string]any{"before_ms": day(time.November, 1).UnixMilli()}, sorted(plans, tagged)},
		{"a date range", map[string]any{"after_ms": day(time.October, 1).UnixMilli(), "before_ms": november}, sorted(plans)},
		{"unread", map[string]any{"is_unread": true}, sorted(invoice)},
		{"read", map[string]any{"is_unread": false}, sorted(lunch, plans, tagged)},
		{"starred", map[string]any{"is_starred": true}, sorted(plans)},
		{"in a label", map[string]any{"folder": label, "sender": "example.com"}, sorted(tagged)},
		{"in the inbox", map[string]any{"folder": proton.InboxLabel, "subject": "lunch"}, sorted(lunch, plans)},
		{"every criterion at once", map[string]any{"sender": "ada", "is_starred": true, "is_unread": false}, sorted(plans)},
		{"nothing matches", map[string]any{"keyword": "nowhere"}, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := map[string]any{"op": "search", "session": session, "limit": 150}
			for key, value := range test.criteria {
				request[key] = value
			}
			found, calls := searchAll(t, request)
			if !slices.Equal(found, test.want) || calls != 1 {
				t.Fatalf("search %v = %v in %d calls, want %v", test.criteria, found, calls, test.want)
			}
		})
	}
}

func TestSearchPagesWithACursor(t *testing.T) {
	f := newFixture(t)
	f.user("alice")
	alice := f.account("alice")
	now := time.Date(2026, time.November, 2, 10, 0, 0, 0, time.UTC)
	messages := make([]incoming, 0, 160)
	for i := range 160 {
		subject := fmt.Sprintf("noise %d", i)
		if i%40 == 0 {
			subject = fmt.Sprintf("needle %d", i)
		}
		messages = append(messages, incoming{"Sender <sender@example.com>", alice.address.Email, subject, now, false})
	}
	imported := alice.importAll(t, messages...)
	var want []string
	for i, id := range imported {
		if i%40 == 0 {
			want = append(want, id)
		}
	}
	slices.Sort(want)
	session := loginAs(t, "alice")

	found, calls := searchAll(t, map[string]any{"op": "search", "session": session, "keyword": "needle", "limit": 1})
	if !slices.Equal(found, want) || calls < len(want) {
		t.Fatalf("paged search = %v in %d calls, want %v", found, calls, want)
	}

	// One page per call: the 160 messages take two pages, so the walk
	// hands back a cursor in between even with no hit yet.
	previous := searchPages
	searchPages = 1
	t.Cleanup(func() { searchPages = previous })
	found, calls = searchAll(t, map[string]any{"op": "search", "session": session, "keyword": "needle", "limit": 150})
	if !slices.Equal(found, want) || calls != 2 {
		t.Fatalf("budgeted search = %v in %d calls, want %v in 2", found, calls, want)
	}
}

func TestSearchSendsTheCriteriaToTheServer(t *testing.T) {
	f := newFixture(t)
	f.user("alice")
	alice := f.account("alice")
	alice.deliver(t, "hello", "body", true)
	session := loginAs(t, "alice")
	var (
		mu     sync.Mutex
		bodies []map[string]any
	)
	f.setIntercept(func(w http.ResponseWriter, r *http.Request, forward func(http.ResponseWriter)) bool {
		if r.Method == http.MethodPost && r.URL.Path == "/mail/v4/messages" && r.Header.Get("X-HTTP-Method-Override") == http.MethodGet {
			raw, _ := io.ReadAll(r.Body)
			var body map[string]any
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Errorf("listing body is not JSON: %v", err)
			}
			mu.Lock()
			bodies = append(bodies, body)
			mu.Unlock()
		}
		return false
	})

	result(t, call(t, map[string]any{
		"op": "search", "session": session, "limit": 10, "folder": proton.InboxLabel,
		"keyword": " invoice ", "subject": "ignored when there is a keyword", "sender": "ada@example.com",
		"recipient": "carol", "after_ms": 1_700_000_000_500, "before_ms": 1_800_000_000_001,
		"is_unread": true, "is_starred": false,
	}))
	result(t, call(t, map[string]any{"op": "search", "session": session, "limit": 10, "subject": "Quarterly"}))

	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 {
		t.Fatalf("listings = %v", bodies)
	}
	want := map[string]any{
		"LabelID": proton.InboxLabel, "Desc": float64(1), "Keyword": "invoice", "From": "ada@example.com",
		"Recipients": "carol", "Begin": float64(1_700_000_000), "End": float64(1_800_000_001),
		"Unread": float64(1), "Starred": float64(0),
	}
	for key, value := range want {
		if bodies[0][key] != value {
			t.Errorf("%s = %v, want %v (body %v)", key, bodies[0][key], value, bodies[0])
		}
	}
	if bodies[1]["Keyword"] != "Quarterly" || bodies[1]["LabelID"] != proton.AllMailLabel {
		t.Errorf("a subject search sends %v", bodies[1])
	}
	for _, absent := range []string{"From", "Recipients", "Begin", "End", "Unread", "Starred"} {
		if _, ok := bodies[1][absent]; ok {
			t.Errorf("%s sent without its criterion: %v", absent, bodies[1])
		}
	}
}

func TestSearchRefusesBadRequests(t *testing.T) {
	f := newFixture(t)
	f.user("alice")
	session := loginAs(t, "alice")
	for _, test := range []struct {
		request map[string]any
		code    string
	}{
		{map[string]any{"limit": 10}, "invalid_request"},
		{map[string]any{"limit": 10, "folder": proton.InboxLabel}, "invalid_request"},
		{map[string]any{"keyword": "x"}, "invalid_request"},
		{map[string]any{"limit": 10, "keyword": "  "}, "invalid_request"},
		{map[string]any{"limit": 10, "sender": "a\nb"}, "invalid_request"},
		{map[string]any{"limit": 10, "subject": strings.Repeat("x", 257)}, "invalid_request"},
		{map[string]any{"limit": 10, "after_ms": 5, "before_ms": 5}, "invalid_request"},
		{map[string]any{"limit": 10, "after_ms": -1}, "invalid_request"},
		{map[string]any{"limit": 10, "after_ms": 1.5}, "invalid_request"},
		{map[string]any{"limit": 10, "keyword": "x", "body": "x"}, "invalid_request"},
		{map[string]any{"limit": 0, "keyword": "x"}, "invalid_page"},
		{map[string]any{"limit": 151, "keyword": "x"}, "invalid_page"},
		{map[string]any{"limit": 10, "keyword": "x", "cursor": "a/b"}, "invalid_id"},
		{map[string]any{"limit": 10, "keyword": "x", "folder": "../x"}, "invalid_id"},
		{map[string]any{"limit": 10, "keyword": "x", "folder": "15"}, "folder_not_found"},
		{map[string]any{"limit": 10, "keyword": "x", "folder": "nosuchlabel"}, "folder_not_found"},
	} {
		request := map[string]any{"op": "search", "session": session}
		for key, value := range test.request {
			request[key] = value
		}
		if code := errorCode(t, call(t, request)); code != test.code {
			t.Errorf("search %v = %s, want %s", test.request, code, test.code)
		}
	}
	if code := errorCode(t, call(t, map[string]any{"op": "search", "limit": 10, "keyword": "x"})); code != "session_invalid" {
		t.Errorf("search without a session = %s", code)
	}
}
