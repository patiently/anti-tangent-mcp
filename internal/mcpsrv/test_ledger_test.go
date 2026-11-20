package mcpsrv

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func diffAdding(path string, lines ...string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "diff --git a/%s b/%s\n--- /dev/null\n+++ b/%s\n@@ -0,0 +1,%d @@\n", path, path, path, len(lines))
	for _, l := range lines {
		b.WriteString("+" + l + "\n")
	}
	return b.String()
}

func renameDiff(from, to string, added string) string {
	return fmt.Sprintf("diff --git a/%s b/%s\nsimilarity index 95%%\nrename from %s\nrename to %s\n--- a/%s\n+++ b/%s\n@@ -1,1 +1,2 @@\n package x\n+%s\n",
		from, to, from, to, from, to, added)
}

func TestCountAddedTests(t *testing.T) {
	cases := []struct {
		name  string
		diff  string
		added int
		known bool
	}{
		{"go adds two, TestMain excluded", diffAdding("x_test.go",
			"func TestA(t *testing.T) {", "func TestB(t *testing.T) {", "func TestMain(m *testing.M) {"), 2, true},
		{"go table rows not counted", diffAdding("x_test.go",
			"func TestT(t *testing.T) {", `{"a", 1},`, `{"b", 2},`, `t.Run(c.name, func(t *testing.T) {`), 1, true},
		{"kotlin annotations", diffAdding("FooTest.kt",
			"@Test", "@ParameterizedTest", "@TestFactory", "@ValueSource(ints = [1, 2])"), 3, true},
		{"python", diffAdding("tests/test_x.py", "def test_a():", "@pytest.mark.parametrize('x', [1, 2])", "async def test_b():"), 2, true},
		{"js each counted once", diffAdding("a.test.ts", "it('a', () => {", "test.each([[1], [2]])('b %i', (n) => {"), 2, true},
		{"no test file", diffAdding("main.go", "func main() {"), 0, true},
		{"unknown language", diffAdding("tests/run.bats", `@test "x" {`), 0, false},
		{"rename shows changed hunk only", renameDiff("old_test.go", "new_test.go", "func TestNew(t *testing.T) {"), 1, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			added, known := countAddedTests(c.diff)
			assert.Equal(t, c.known, known)
			if c.known {
				assert.Equal(t, c.added, added)
			}
		})
	}
}

func TestLedgerValidation(t *testing.T) {
	entry := func(s string) TestLedgerEntryArg { return TestLedgerEntryArg{Test: "a::b", Protects: s} }
	many := func(n int) []TestLedgerEntryArg { return make([]TestLedgerEntryArg, n) }
	cases := []struct {
		name     string
		ledger   []TestLedgerEntryArg
		declined []TestDeclinedArg
		wantErr  string
	}{
		{"within bounds", []TestLedgerEntryArg{entry("p")}, []TestDeclinedArg{{Test: "t", Reason: "r"}}, ""},
		{"41 ledger entries", many(41), nil, "test_ledger"},
		{"41 declined entries", nil, make([]TestDeclinedArg, 41), "tests_declined"},
		{"301-rune ledger value", []TestLedgerEntryArg{entry(strings.Repeat("é", 301))}, nil, "test_ledger[0]"},
		{"300-rune value accepted", []TestLedgerEntryArg{entry(strings.Repeat("é", 300))}, nil, ""},
		{"control character in declined", nil, []TestDeclinedArg{{Test: "t", Reason: "a\x07b"}}, "tests_declined[0]"},
		{"newline in ledger", []TestLedgerEntryArg{entry("a\nb")}, nil, "test_ledger[0]"},
		{"over 16 KB total", func() []TestLedgerEntryArg {
			out := make([]TestLedgerEntryArg, 40)
			for i := range out {
				out[i] = TestLedgerEntryArg{Test: strings.Repeat("é", 150), Protects: strings.Repeat("é", 150), FailedWhen: strings.Repeat("é", 150)}
			}
			return out
		}(), nil, "bytes"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateTestLedger(c.ledger, c.declined)
			if c.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, c.wantErr)
		})
	}
}

func TestLedgerAbsentFindingAndSummaryLine(t *testing.T) {
	three := diffAdding("x_test.go", "func TestA(t *testing.T) {", "func TestB(t *testing.T) {", "func TestC(t *testing.T) {")
	two := diffAdding("x_test.go", "func TestA(t *testing.T) {", "func TestB(t *testing.T) {")
	cases := []struct {
		name        string
		acs         []string
		diff        string
		ledger      []TestLedgerEntryArg
		declined    []TestDeclinedArg
		wantFinding bool
		wantLine    string
	}{
		{"above AC count, no ledger", []string{"a", "b"}, three, nil, nil, true, "tests:         +3 for 2 ACs (declined 0)"},
		{"at AC count", []string{"a", "b"}, two, nil, nil, false, "tests:         +2 for 2 ACs (declined 0)"},
		{"above AC count with ledger", []string{"a", "b"}, three, []TestLedgerEntryArg{{Test: "x::TestC", Protects: "p"}}, []TestDeclinedArg{{Test: "t", Reason: "r"}}, false, "tests:         +3 for 2 ACs (declined 1)"},
		{"session without ACs", nil, three, nil, nil, false, "tests:         n/a"},
		{"unknown language", []string{"a"}, diffAdding("tests/run.bats", `@test "x" {`), nil, nil, false, "tests:         n/a"},
		{"no diff", []string{"a"}, "", nil, nil, false, "tests:         n/a"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := &handlers{deps: newDeps(t, &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")})}
			_, task, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
				TaskTitle: "X", Goal: "Y", AcceptanceCriteria: c.acs,
			})
			require.NoError(t, err)
			_, env, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
				SessionID: task.SessionID, Summary: "done", FinalDiff: c.diff, TestEvidence: "ok  \tpkg\t0.1s\n",
				TestLedger: c.ledger, TestsDeclined: c.declined,
			})
			require.NoError(t, err)
			var got int
			for _, f := range env.Findings {
				if f.Criterion == "test_ledger_absent" {
					got++
					assert.Equal(t, "quality", string(f.Category))
					assert.Equal(t, "minor", string(f.Severity))
				}
			}
			if c.wantFinding {
				assert.Equal(t, 1, got)
			} else {
				assert.Zero(t, got)
			}
			assert.Contains(t, env.SummaryBlock, "  "+c.wantLine+"\n")
		})
	}
}
