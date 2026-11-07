package protonsdk

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	proton "github.com/ProtonMail/go-proton-api"
	"github.com/go-resty/resty/v2"
)

// One search call reads at most this many metadata pages before it hands
// back a cursor, so a criterion the server cannot narrow by still finishes
// inside the operation budget. A variable so the tests can shrink it.
var searchPages = 20

// The longest text criterion, in characters.
const maxCriterion = 256

type searchParams struct {
	envelopeFields
	sessionRequest
	Folder    *string `json:"folder"`
	Keyword   *string `json:"keyword"`
	Subject   *string `json:"subject"`
	Sender    *string `json:"sender"`
	Recipient *string `json:"recipient"`
	AfterMs   *int64  `json:"after_ms"`
	BeforeMs  *int64  `json:"before_ms"`
	IsUnread  *bool   `json:"is_unread"`
	IsStarred *bool   `json:"is_starred"`
	Limit     *int    `json:"limit"`
	Cursor    *string `json:"cursor"`
}

// searchQuery is a validated search. Empty text and nil fields match
// anything.
type searchQuery struct {
	folder                              string
	keyword, subject, sender, recipient string
	afterMs, beforeMs                   *int64
	isUnread, isStarred                 *bool
}

func searchOp(ctx context.Context, raw []byte) envelope {
	var params searchParams
	if !decodeStrict(raw, &params) || params.Limit == nil {
		return failure("invalid_request")
	}
	query, ok := params.query()
	if !ok {
		return failure("invalid_request")
	}
	if !params.Session.usable() {
		return failure("session_invalid")
	}
	if limit := *params.Limit; limit <= 0 || limit > pageSize {
		return failure("invalid_page")
	}
	cursor := ""
	if params.Cursor != nil {
		cursor = *params.Cursor
		if !validID(cursor) {
			return failure("invalid_id")
		}
	}
	if !validID(query.folder) {
		return failure("invalid_id")
	}
	session := resume(*params.Session)
	defer session.close()
	return session.search(ctx, query, *params.Limit, cursor)
}

// query validates the criteria. A search needs at least one besides the
// folder: without one it is a listing, which is sync's.
func (p searchParams) query() (searchQuery, bool) {
	query := searchQuery{
		folder:  proton.AllMailLabel,
		afterMs: p.AfterMs, beforeMs: p.BeforeMs,
		isUnread: p.IsUnread, isStarred: p.IsStarred,
	}
	if p.Folder != nil {
		query.folder = *p.Folder
	}
	texts := []struct {
		value  *string
		target *string
	}{
		{p.Keyword, &query.keyword},
		{p.Subject, &query.subject},
		{p.Sender, &query.sender},
		{p.Recipient, &query.recipient},
	}
	for _, text := range texts {
		if text.value == nil {
			continue
		}
		value, ok := criterion(*text.value)
		if !ok {
			return searchQuery{}, false
		}
		*text.target = value
	}
	switch {
	case query.afterMs != nil && *query.afterMs < 0,
		query.beforeMs != nil && *query.beforeMs <= 0,
		query.afterMs != nil && query.beforeMs != nil && *query.afterMs >= *query.beforeMs:
		return searchQuery{}, false
	}
	isEmpty := query.keyword == "" && query.subject == "" && query.sender == "" && query.recipient == "" &&
		query.afterMs == nil && query.beforeMs == nil && query.isUnread == nil && query.isStarred == nil
	return query, !isEmpty
}

// criterion trims a text criterion and refuses an empty, oversize or
// control-character one.
func criterion(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" || utf8.RuneCountInString(value) > maxCriterion || strings.ContainsFunc(value, unicode.IsControl) {
		return "", false
	}
	return value, true
}

// search walks the folder newest-first from cursor, with the server
// narrowing what it can, and keeps the messages whose metadata matches
// every criterion. It stops at limit hits, at the end of the folder, or
// after searchPages pages; next_cursor is the last message examined, so the
// next call resumes exactly there.
func (s *apiSession) search(ctx context.Context, query searchQuery, limit int, cursor string) envelope {
	if code := s.checkFolder(ctx, query.folder); code != "" {
		return s.fail(code)
	}
	s.addSearchFields(query.apiFields())
	hits := []map[string]any{}
	filter := proton.MessageFilter{LabelID: query.folder, Desc: true, EndID: cursor}
	isEnd := false
	for pages := 0; len(hits) < limit && !isEnd && pages < searchPages; pages++ {
		page, err := s.client.GetMessageMetadataPage(ctx, 0, pageSize, filter)
		if err != nil {
			return s.failErr(err)
		}
		isEnd = len(page) < pageSize
		// EndID is inclusive: the cursor itself comes back first.
		if filter.EndID != "" && len(page) > 0 && page[0].ID == filter.EndID {
			page = page[1:]
		}
		for i, meta := range page {
			if query.matches(meta) {
				hits = append(hits, headerRow(meta))
			}
			filter.EndID = meta.ID
			if len(hits) == limit {
				isEnd = isEnd && i == len(page)-1
				break
			}
		}
	}
	result := map[string]any{"messages": hits, "next_cursor": nil}
	if !isEnd {
		result["next_cursor"] = filter.EndID
	}
	return s.ok(result)
}

// checkFolder accepts the folders sync lists: the system folders and the
// user's own folders and labels.
func (s *apiSession) checkFolder(ctx context.Context, id string) string {
	if slices.ContainsFunc(systemFolders, func(folder systemFolder) bool { return folder.id == id }) {
		return ""
	}
	if isSystemLabel(id) {
		return "folder_not_found"
	}
	_, code := s.userLabel(ctx, id)
	return code
}

// apiFields are the search parameters Proton's message listing takes
// (WebClients' queryMessageMetadata) that go-proton-api's MessageFilter
// does not carry. Begin and End are whole seconds, widened outwards; the
// exact bounds are re-checked on each hit. The subject is sent as the
// keyword when there is none, since Keyword covers the subject and Proton's
// own clients never send its Subject parameter.
func (q searchQuery) apiFields() map[string]any {
	fields := map[string]any{}
	switch {
	case q.keyword != "":
		fields["Keyword"] = q.keyword
	case q.subject != "":
		fields["Keyword"] = q.subject
	}
	if q.sender != "" {
		fields["From"] = q.sender
	}
	if q.recipient != "" {
		fields["Recipients"] = q.recipient
	}
	if q.afterMs != nil {
		fields["Begin"] = *q.afterMs / 1000
	}
	if q.beforeMs != nil {
		fields["End"] = (*q.beforeMs + 999) / 1000
	}
	if q.isUnread != nil {
		fields["Unread"] = proton.Bool(*q.isUnread)
	}
	if q.isStarred != nil {
		fields["Starred"] = proton.Bool(*q.isStarred)
	}
	return fields
}

// addSearchFields adds fields to this session's message listings. The
// session is this one operation's, so every listing it makes is the search.
func (s *apiSession) addSearchFields(fields map[string]any) {
	if len(fields) == 0 {
		return
	}
	s.client.AddPreRequestHook(func(_ *resty.Client, r *resty.Request) error {
		if r.Method != http.MethodPost || r.URL != "/mail/v4/messages" ||
			r.Header.Get("X-HTTP-Method-Override") != http.MethodGet {
			return nil
		}
		encoded, err := json.Marshal(r.Body)
		if err != nil {
			return err
		}
		var body map[string]any
		if err := json.Unmarshal(encoded, &body); err != nil {
			return err
		}
		maps.Copy(body, fields)
		r.SetBody(body)
		return nil
	})
}

// matches re-checks every criterion on the metadata, so a hit means the
// same whatever the server did with the fields: text is a case-insensitive
// substring, the date range is [after_ms, before_ms).
func (q searchQuery) matches(meta proton.MessageMetadata) bool {
	sender := []*mailAddress{}
	if meta.Sender != nil {
		sender = append(sender, &mailAddress{Name: meta.Sender.Name, Address: meta.Sender.Address})
	}
	recipients := append(append(toMail(meta.ToList), toMail(meta.CCList)...), toMail(meta.BCCList)...)
	dateMs := meta.Time * 1000
	switch {
	case q.subject != "" && !containsFold(meta.Subject, q.subject),
		q.sender != "" && !mentions(sender, q.sender),
		q.recipient != "" && !mentions(recipients, q.recipient),
		q.keyword != "" && !containsFold(meta.Subject, q.keyword) && !mentions(sender, q.keyword) && !mentions(recipients, q.keyword),
		q.afterMs != nil && dateMs < *q.afterMs,
		q.beforeMs != nil && dateMs >= *q.beforeMs,
		q.isUnread != nil && bool(meta.Unread) != *q.isUnread,
		q.isStarred != nil && meta.Starred() != *q.isStarred:
		return false
	}
	return true
}

// mentions reports whether any mailbox's name or address contains text.
func mentions(mailboxes []*mailAddress, text string) bool {
	for _, mailbox := range mailboxes {
		if containsFold(mailbox.Name, text) || containsFold(mailbox.Address, text) {
			return true
		}
	}
	return false
}

func containsFold(haystack, needle string) bool {
	return strings.Contains(strings.ToLower(haystack), strings.ToLower(needle))
}
