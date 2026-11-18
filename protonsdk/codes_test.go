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

// The Kotlin wrapper keeps its own copy of the contract: ProtonErrorCode
// lists the codes by hand and ProtonAccount/ProtonClient name each op as a
// string. Nothing in the Gradle build reads the README, so these tests hold
// that copy to it from here, where go test runs on every pull request.
const kotlinSources = "../android/proton-sdk-android/src/main/kotlin/org/tunaos/proton"

func readKotlin(t *testing.T, name string) string {
	t.Helper()
	text, err := os.ReadFile(filepath.Join(kotlinSources, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(text)
}

// ProtonErrorCode carries every documented code and nothing else. A code
// missing from it reaches an Android caller as UNKNOWN and is thrown as
// ProtonException.Failed instead of the class that says what to do about
// it. not_attempted is the one per-id outcome code that never fills an
// envelope, so it is documented beside the batch ops, not in the table.
func TestKotlinErrorCodesMatchTheReadme(t *testing.T) {
	documented := documentedCodes(t)
	if len(documented) < 30 {
		t.Fatalf("README table parsed as %v", documented)
	}
	expected := append(slices.Clone(documented), "not_attempted")
	source := readKotlin(t, "ProtonErrors.kt")
	body, _, ok := strings.Cut(source, "public companion object")
	if !ok || !strings.Contains(body, "enum class ProtonErrorCode") {
		t.Fatal("ProtonErrors.kt has no ProtonErrorCode enum")
	}
	entry := regexp.MustCompile(`(?m)^\s+[A-Z_]+\("([a-z_]*)"\),$`)
	var mirrored []string
	for _, match := range entry.FindAllStringSubmatch(body, -1) {
		if match[1] != "" { // UNKNOWN
			mirrored = append(mirrored, match[1])
		}
	}
	for _, code := range expected {
		if !slices.Contains(mirrored, code) {
			t.Errorf("%s is documented but missing from the Kotlin ProtonErrorCode", code)
		}
	}
	for _, code := range mirrored {
		if !slices.Contains(expected, code) {
			t.Errorf("the Kotlin ProtonErrorCode has %s, which the README does not document", code)
		}
	}
}

// The wrapper sends exactly the ops dispatch accepts: a misspelt op fails
// only at runtime as invalid_request, and an op the wrapper never sends is
// one an Android caller cannot reach.
func TestKotlinWrapperSendsEveryOperation(t *testing.T) {
	op := regexp.MustCompile(`\bcall\("([a-z_]+)"\)|put\("op", "([a-z_]+)"\)`)
	sent := map[string]bool{}
	for _, name := range []string{"ProtonAccount.kt", "ProtonClient.kt"} {
		for _, match := range op.FindAllStringSubmatch(readKotlin(t, name), -1) {
			sent[match[1]+match[2]] = true
		}
	}
	if !sent["login"] || !sent["revoke"] {
		t.Fatalf("the scan missed login or revoke, found %v: tighten the pattern", sent)
	}
	for name := range sent {
		if _, ok := operations[name]; !ok {
			t.Errorf("the Kotlin wrapper sends %s, which dispatch does not accept", name)
		}
	}
	for name := range operations {
		if !sent[name] {
			t.Errorf("%s is dispatched but the Kotlin wrapper never sends it", name)
		}
	}
}
