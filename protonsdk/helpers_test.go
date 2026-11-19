package protonsdk

import (
	"net/mail"
	"strings"
	"testing"
)

func TestFolderNameTrimsAndBoundsByRunes(t *testing.T) {
	tests := map[string]struct {
		in   string
		want string
		ok   bool
	}{
		"trimmed":             {"  Work  ", "Work", true},
		"empty":               {"", "", false},
		"blank":               {" \t ", "", false},
		"control inside":      {"a\tb", "", false},
		"newline inside":      {"a\nb", "", false},
		"at the limit":        {strings.Repeat("a", maxFolderName), strings.Repeat("a", maxFolderName), true},
		"over the limit":      {strings.Repeat("a", maxFolderName+1), "", false},
		"limit counts runes":  {strings.Repeat("é", maxFolderName), strings.Repeat("é", maxFolderName), true},
		"over limit in runes": {strings.Repeat("é", maxFolderName+1), "", false},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got, ok := folderName(tc.in)
			if got != tc.want || ok != tc.ok {
				t.Fatalf("folderName(%q) = (%q, %v), want (%q, %v)", tc.in, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestIsSystemLabelAcceptsOnlyDigitIDs(t *testing.T) {
	for _, id := range []string{"0", "1", "15", "0042"} {
		if !isSystemLabel(id) {
			t.Errorf("isSystemLabel(%q) = false, want true", id)
		}
	}
	for _, id := range []string{"", "a", "1a", "-1", "1 ", "abc123==", "١"} {
		if isSystemLabel(id) {
			t.Errorf("isSystemLabel(%q) = true, want false", id)
		}
	}
}

func TestMailAddressConversionSkipsNilAndKeepsOrder(t *testing.T) {
	rows := toMail([]*mail.Address{
		{Name: "Alice", Address: "alice@example.org"},
		nil,
		{Address: "bob@example.org"},
	})
	if len(rows) != 2 || rows[0].Name != "Alice" || rows[0].Address != "alice@example.org" ||
		rows[1].Name != "" || rows[1].Address != "bob@example.org" {
		t.Fatalf("toMail = %+v", rows)
	}
	if rows := toMail(nil); rows == nil || len(rows) != 0 {
		t.Fatalf("toMail(nil) = %#v, want an empty, non-nil list so it encodes as []", rows)
	}

	back := fromMail([]mailAddress{{Name: "Alice", Address: "alice@example.org"}, {Address: "bob@example.org"}})
	if len(back) != 2 || back[0].Name != "Alice" || back[0].Address != "alice@example.org" ||
		back[1].Name != "" || back[1].Address != "bob@example.org" {
		t.Fatalf("fromMail = %+v", back)
	}
	if got := fromMail(nil); len(got) != 0 {
		t.Fatalf("fromMail(nil) = %+v, want empty", got)
	}
}

func TestCollapseBlankLinesKeepsOneBlankAndTrimsLines(t *testing.T) {
	tests := map[string]struct{ in, want string }{
		"runs collapse":     {"a\n\n\n\nb", "a\n\nb"},
		"whitespace blanks": {"a\n  \n\t\nb", "a\n\nb"},
		"lines are trimmed": {"  x  \n\ty", "x\ny"},
		"single blank kept": {"a\n\nb", "a\n\nb"},
		"no blanks":         {"a\nb", "a\nb"},
		"leading blanks":    {"\n\na", "\na"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			if got := collapseBlankLines(tc.in); got != tc.want {
				t.Fatalf("collapseBlankLines(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestHTMLToTextKeepsListsAndSkipsHiddenContent(t *testing.T) {
	got := htmlToText("<head><title>t</title></head><ul><li>one</li><li>two</li></ul><p>a</p><br><br><br><p>b</p><style>x{}</style>")
	want := "one\ntwo\na\n\nb\n"
	if got != want {
		t.Fatalf("htmlToText = %q, want %q", got, want)
	}
}
