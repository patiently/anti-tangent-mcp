package mcpsrv

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/patiently/anti-tangent-mcp/internal/prompts"
	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

// TestLedgerEntryArg is one test the implementer added beyond the first for a
// criterion, with what it protects that the others cannot.
type TestLedgerEntryArg struct {
	Test       string `json:"test" jsonschema:"The test, as path::name."`
	Protects   string `json:"protects" jsonschema:"The acceptance criterion or caller-visible behaviour it protects that the task's other tests cannot."`
	FailedWhen string `json:"failed_when,omitempty" jsonschema:"How you saw it fail with the behaviour broken. Not verified."`
}

// TestDeclinedArg is a test the implementer considered and did not write.
type TestDeclinedArg struct {
	Test   string `json:"test" jsonschema:"What you considered testing."`
	Reason string `json:"reason" jsonschema:"Why you did not write it."`
}

func toPromptTestLedger(in []TestLedgerEntryArg) []prompts.TestLedgerEntry {
	out := make([]prompts.TestLedgerEntry, len(in))
	for i, e := range in {
		out[i] = prompts.TestLedgerEntry{Test: e.Test, Protects: e.Protects, FailedWhen: e.FailedWhen}
	}
	return out
}

func toPromptTestsDeclined(in []TestDeclinedArg) []prompts.TestDeclined {
	out := make([]prompts.TestDeclined, len(in))
	for i, d := range in {
		out[i] = prompts.TestDeclined{Test: d.Test, Reason: d.Reason}
	}
	return out
}

const (
	maxLedgerEntries = 40
	maxLedgerRunes   = 300
	maxLedgerBytes   = 16 * 1024
)

// testPathRe matches the files a diff adding tests is recognised by. A file
// matching it whose language has no declaration rule makes the count unknown.
var testPathRe = regexp.MustCompile(`(_test\.go|Tests?\.kt|Spec\.kt|Tests?\.java|IT\.java|_test\.py|\.test\.[^/]+|\.spec\.[^/]+|\.bats)$|(^|/)test_[^/]*\.py$|(^|/)(__tests__|tests|src/test)/`)

// testDataRe matches fixtures, snapshots and resources kept beside tests. They
// declare no test, so they neither count nor make the count unknown.
var testDataRe = regexp.MustCompile(`\.(json|ya?ml|md|snap|txt|golden|xml|csv|properties|sql)$`)

// uncountedSourceRe matches source files that can declare a test anywhere, not
// only under a test path (Rust's inline #[test], for one), in a language with no
// declaration rule, and Kotest specs, whose declarations are DSL calls in too
// many styles to count. An added line in one makes the count unknown.
var uncountedSourceRe = regexp.MustCompile(`\.(rs|rb|cs|swift|scala|php|c|cc|cpp|h|hpp)$|Spec\.kts?$`)

// testDeclRules maps a file type to the line shape that declares one test.
// exclude, when set, rejects a declaration whose first capture group equals it.
var testDeclRules = []struct {
	file    *regexp.Regexp
	decl    *regexp.Regexp
	exclude string
}{
	{regexp.MustCompile(`\.go$`), regexp.MustCompile(`^func Test(\w*)\(`), "Main"},
	{regexp.MustCompile(`\.(kt|kts|java)$`), regexp.MustCompile(`^\s*@(Test|ParameterizedTest|TestFactory)\b`), ""},
	{regexp.MustCompile(`\.py$`), regexp.MustCompile(`^\s*(async\s+)?def test_\w*\(`), ""},
	{regexp.MustCompile(`\.[cm]?[jt]sx?$`), regexp.MustCompile(`^\s*(it|test)(\.each\b.*)?\s*[(\x60]`), ""},
}

// countAddedTests counts the test declarations a unified diff adds. Table rows
// and parametrised cases are not counted, so one table-driven test counts once.
// known is false when an added line sits in a file that may declare tests in a
// language with no declaration rule: such a task has no comparable count.
func countAddedTests(diff string) (added int, known bool) {
	var path string
	for _, line := range strings.Split(diff, "\n") {
		if p, ok := strings.CutPrefix(line, "+++ "); ok {
			path = strings.TrimPrefix(p, "b/")
			continue
		}
		body, isAdd := strings.CutPrefix(line, "+")
		if !isAdd || !mayDeclareTests(path) {
			continue
		}
		isTest, ruled := declaresTest(path, body)
		if !ruled {
			return 0, false
		}
		if isTest {
			added++
		}
	}
	return added, true
}

// mayDeclareTests reports whether an added line in path is read for test
// declarations. A file it admits that no rule covers makes the count unknown.
func mayDeclareTests(path string) bool {
	if uncountedSourceRe.MatchString(path) {
		return true
	}
	return testPathRe.MatchString(path) && !testDataRe.MatchString(path)
}

// declaresTest reports whether an added line declares a test, and whether any
// rule covers the file's language at all.
func declaresTest(path, body string) (isTest, ruled bool) {
	if uncountedSourceRe.MatchString(path) {
		return false, false
	}
	for _, r := range testDeclRules {
		if !r.file.MatchString(path) {
			continue
		}
		m := r.decl.FindStringSubmatch(body)
		return m != nil && (r.exclude == "" || m[1] != r.exclude), true
	}
	return false, false
}

// ledgerValue is one string of the caller-attested lists, labelled for errors.
type ledgerValue struct {
	field string
	index int
	text  string
}

func ledgerValues(ledger []TestLedgerEntryArg, declined []TestDeclinedArg) []ledgerValue {
	var out []ledgerValue
	for i, e := range ledger {
		for _, s := range []string{e.Test, e.Protects, e.FailedWhen} {
			out = append(out, ledgerValue{"test_ledger", i, s})
		}
	}
	for i, e := range declined {
		for _, s := range []string{e.Test, e.Reason} {
			out = append(out, ledgerValue{"tests_declined", i, s})
		}
	}
	return out
}

// validateTestLedger bounds the two caller-attested lists.
func validateTestLedger(ledger []TestLedgerEntryArg, declined []TestDeclinedArg) error {
	if len(ledger) > maxLedgerEntries {
		return fmt.Errorf("test_ledger has %d entries; at most %d are accepted", len(ledger), maxLedgerEntries)
	}
	if len(declined) > maxLedgerEntries {
		return fmt.Errorf("tests_declined has %d entries; at most %d are accepted", len(declined), maxLedgerEntries)
	}
	total := 0
	for _, v := range ledgerValues(ledger, declined) {
		total += len(v.text)
		if utf8.RuneCountInString(v.text) > maxLedgerRunes || strings.IndexFunc(v.text, unicode.IsControl) >= 0 {
			return fmt.Errorf("%s[%d]: each value must be at most %d characters with no control character", v.field, v.index, maxLedgerRunes)
		}
	}
	if total > maxLedgerBytes {
		return fmt.Errorf("test_ledger and tests_declined total %d bytes; at most %d are accepted", total, maxLedgerBytes)
	}
	return nil
}

// TestTally is the summary block's tests: line. Added is nil when the count is
// not comparable: no diff, a test language with no rule, or no acceptance
// criteria.
type TestTally struct {
	Added    *int `json:"added,omitempty"`
	ACs      int  `json:"acs"`
	Declined int  `json:"declined"`
	Ledger   int  `json:"ledger"`
}

func testTally(diff string, acs int, ledger, declined int) TestTally {
	t := TestTally{ACs: acs, Declined: declined, Ledger: ledger}
	if diff == "" || acs == 0 {
		return t
	}
	if n, known := countAddedTests(diff); known {
		t.Added = &n
	}
	return t
}

func (t TestTally) line() string {
	if t.Added == nil {
		return "n/a"
	}
	return fmt.Sprintf("+%d for %d ACs (declined %d)", *t.Added, t.ACs, t.Declined)
}

// ledgerAbsentFindings asks for a ledger when a task adds more tests than it
// has acceptance criteria and sent none.
func ledgerAbsentFindings(t TestTally) []verdict.Finding {
	if t.Added == nil || t.Ledger > 0 {
		return nil
	}
	if *t.Added <= t.ACs {
		return nil
	}
	return []verdict.Finding{{
		Severity:  verdict.SeverityMinor,
		Category:  verdict.CategoryQuality,
		Criterion: "test_ledger_absent",
		Evidence:  fmt.Sprintf("The diff adds %d test declarations for %d acceptance criteria and no test_ledger was sent.", *t.Added, t.ACs),
		Suggestion: "Send test_ledger with one entry per test beyond the first for a criterion, saying what it protects " +
			"that the others cannot, and tests_declined for tests you considered and did not write.",
	}}
}
