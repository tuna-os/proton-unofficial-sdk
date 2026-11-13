package protonsdk

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// documentedCodes is the README's error-code table: the vocabulary the
// Kotlin wrapper's ProtonErrorCode mirrors.
func documentedCodes(t *testing.T) []string {
	t.Helper()
	readme, err := os.ReadFile("../README.md")
	if err != nil {
		t.Fatal(err)
	}
	_, table, ok := strings.Cut(string(readme), "\n## Error codes\n")
	if !ok {
		t.Fatal("README has no Error codes section")
	}
	table, _, _ = strings.Cut(table, "\n## ")
	row := regexp.MustCompile("(?m)^\\| `([a-z_]+)` \\|")
	var codes []string
	for _, match := range row.FindAllStringSubmatch(table, -1) {
		codes = append(codes, match[1])
	}
	return codes
}

// Every code the library can put in an envelope is in the README's table.
// The scan is textual: a code is a literal handed to failure/fail, returned
// as a code, or assigned to code.
func TestEveryErrorCodeIsDocumented(t *testing.T) {
	documented := documentedCodes(t)
	if len(documented) < 30 || slices.Contains(documented, "code") {
		t.Fatalf("README table parsed as %v", documented)
	}
	sources, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	literal := regexp.MustCompile(`(?:failure|fail)\("([a-z_]+)"\)|return (?:[^"\n]*, )?"([a-z_]+)"\s*$|code = "([a-z_]+)"`)
	found := map[string]bool{}
	for _, source := range sources {
		if strings.HasSuffix(source, "_test.go") {
			continue
		}
		text, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(text), "\n") {
			for _, match := range literal.FindAllStringSubmatch(line, -1) {
				found[match[1]+match[2]+match[3]] = true
			}
		}
	}
	for _, code := range []string{"folder_readonly", "invalid_destination", "send_uncertain"} {
		if !found[code] {
			t.Errorf("the scan missed %s: tighten the pattern", code)
		}
	}
	for code := range found {
		if !slices.Contains(documented, code) {
			t.Errorf("%s is produced but not in the README's Error codes table", code)
		}
	}
}

// documentedOperations is the op names the README's Operations section
// gives a heading to; one heading may name several ops.
func documentedOperations(t *testing.T) []string {
	t.Helper()
	readme, err := os.ReadFile("../README.md")
	if err != nil {
		t.Fatal(err)
	}
	_, section, ok := strings.Cut(string(readme), "\n## Operations\n")
	if !ok {
		t.Fatal("README has no Operations section")
	}
	section, _, _ = strings.Cut(section, "\n## ")
	heading := regexp.MustCompile("(?m)^### (.+)$")
	name := regexp.MustCompile("`([a-z_]+)`")
	var ops []string
	for _, match := range heading.FindAllStringSubmatch(section, -1) {
		for _, op := range name.FindAllStringSubmatch(match[1], -1) {
			ops = append(ops, op[1])
		}
	}
	return ops
}

// The ops dispatch accepts are exactly the ops the README documents: a new
// op ships documented, and a documented op is never one dispatch refuses.
func TestEveryOperationIsDocumented(t *testing.T) {
	documented := documentedOperations(t)
	if len(documented) < 10 {
		t.Fatalf("README Operations parsed as %v", documented)
	}
	for _, op := range documented {
		if _, ok := operations[op]; !ok {
			t.Errorf("README documents %s, which dispatch does not accept", op)
		}
	}
	for op := range operations {
		if !slices.Contains(documented, op) {
			t.Errorf("%s is dispatched but has no heading under the README's Operations", op)
		}
	}
}
