package mcpsrv

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

func TestTestEvidenceFindingsStaysQuiet(t *testing.T) {
	for name, evidence := range map[string]string{
		"empty":                   "",
		"gradle-uptodate":         "> Task :app:test UP-TO-DATE\nBUILD SUCCESSFUL in 1s",
		"gradle-cached":           "> Task :app:test FROM-CACHE\nBUILD SUCCESSFUL in 1s",
		"go-cached":               "ok  \tgithub.com/x/y\t(cached)",
		"gradle-mixed":            "> Task :app:compileKotlin UP-TO-DATE\n> Task :app:test\nBUILD SUCCESSFUL in 12s",
		"gradle-nosource-nontest": "> Task :app:processTestResources NO-SOURCE\n> Task :app:test\nBUILD SUCCESSFUL in 12s",
		// Alone, with no executed test task anywhere in the output: an
		// executed line would suppress the finding by itself and the case
		// would pass even if testClasses were wrongly read as a test task.
		"gradle-testclasses-alone": "> Task :app:testClasses NO-SOURCE\nBUILD SUCCESSFUL in 9s",
		"gradle-testclasses":       "> Task :app:testClasses NO-SOURCE\n> Task :app:test\nBUILD SUCCESSFUL in 9s",
		"human-summary":            "all 4 tests pass",
		"go-passing":               "ok  \tgithub.com/x/y\t0.412s",
		"pytest-real":              "collected 4 items\n\n=== 4 passed in 0.10s ===",

		// Every fixture above stays quiet because no no-execution marker
		// ever matches — the executionMarkers suppression loop is never
		// reached, let alone exercised. Each fixture below DOES trip a
		// no-execution marker for real, so staying quiet can only happen
		// through genuine suppression; each is built so a specific
		// executionMarkers entry is the one doing the suppressing.

		// Suppressed by the unannotated-Gradle-test-task regex
		// (`^> Task :...test\s*$`): moduleA's own test task genuinely is
		// NO-SOURCE (its task path ends right at "test", unlike
		// processTestResources above), and would fire alone — moduleB's
		// unannotated test line is what suppresses it.
		"gradle-multimodule-suppressed": "> Task :moduleA:test NO-SOURCE\n> Task :moduleB:test\nBUILD SUCCESSFUL in 12s",
		// Suppressed by the `^ok\s+\S+\s+\d` regex: one package in a
		// `go test ./...` run has no test files, a sibling package passed.
		"go-multipackage-suppressed": "?   \tgithub.com/x/a\t[no test files]\nok  \tgithub.com/x/b\t0.412s",
		// The same shape with the sibling package's result served from the
		// build cache. `go test` caches only a PASSING result, so "(cached)"
		// attests a pass on the current inputs exactly as a fresh duration
		// does — and a cached line carries no duration to match on.
		"go-multipackage-cached-suppressed": "?   \tgithub.com/x/a\t[no test files]\nok  \tgithub.com/x/b\t(cached)",
		// Gradle's two cache annotations on the sibling module's test task.
		// A failing test task re-executes, so a task Gradle marks FROM-CACHE
		// or UP-TO-DATE passed on these inputs; both are as much an executed
		// suite as the unannotated line above.
		"gradle-multimodule-from-cache-suppressed": "> Task :moduleA:test NO-SOURCE\n> Task :moduleB:test FROM-CACHE\nBUILD SUCCESSFUL in 1s",
		"gradle-multimodule-up-to-date-suppressed": "> Task :moduleA:test NO-SOURCE\n> Task :moduleB:test UP-TO-DATE\nBUILD SUCCESSFUL in 1s",
		// Suppressed by "N tests|examples passed|ran|completed", not by the
		// plainer "N passed" regex below it — there is no bare "4 passed"
		// substring here, only "4 tests passed".
		"tests-passed-suppressed": "suite A: no tests ran\nsuite B: 4 tests passed",
		// Suppressed by the plain "N passed" regex: pytest's own summary
		// line carries a count with no "tests"/"examples" word beside it.
		"pytest-multimodule-suppressed": "moduleA: no tests ran\n\n=== 4 passed in 0.10s ===",
		// Trips "N tests completed" too, but that pattern is a strict
		// subset of "N tests|examples passed|ran|completed" above it in the
		// list — every string it matches also matches that broader regex,
		// which is checked first — so it can never be the sole or
		// first-matching reason evidence stays quiet. Kept anyway to prove
		// the combination still suppresses correctly.
		"tests-completed-also-matches-broader-regex": "moduleA: no tests ran\nmoduleB: 4 tests completed",
	} {
		t.Run(name, func(t *testing.T) {
			assert.Empty(t, testEvidenceFindings(evidence))
		})
	}
}

func TestTestEvidenceFindingsFiresOnNoExecution(t *testing.T) {
	for name, evidence := range map[string]string{
		"gradle-no-source": "> Task :app:test NO-SOURCE\nBUILD SUCCESSFUL in 1s",
		"gradle-variant":   "> Task :app:testDebugUnitTest NO-SOURCE\nBUILD SUCCESSFUL in 1s",
		"pytest":           "collected 0 items\n\n=== no tests ran in 0.01s ===",
		"jest":             "No tests found, exiting with code 1",
		"zero-count":       "collected 0 items\n\n=== no tests ran in 0.01s ===\n0 passed",
		"zero-completed":   "> Task :app:test NO-SOURCE\n0 tests completed",
		"go-no-test-files": "?   \tgithub.com/x/y\t[no test files]",
	} {
		t.Run(name, func(t *testing.T) {
			got := testEvidenceFindings(evidence)
			require.Len(t, got, 1)
			assert.Equal(t, verdict.SeverityMajor, got[0].Severity)
			assert.Equal(t, verdict.CategoryInsufficientEvidence, got[0].Category)
			assert.Equal(t, "test_evidence", got[0].Criterion)
		})
	}
}

func TestTestEvidenceFindingIsSubmissionDefectOnly(t *testing.T) {
	f := testEvidenceFindings("> Task :app:test NO-SOURCE")
	require.Len(t, f, 1)
	assert.True(t, isSubmissionDefectOnly(f))
}

func TestNoExecutionEvidenceRoutesToResubmit(t *testing.T) {
	// A lightweight completion (empty session_id) whose only defect is the
	// test evidence: the envelope must tell the implementer to re-submit,
	// not to rework code.
	h := newTestHandlers(t)
	_, env, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		Summary:      "added the parser",
		FinalFiles:   []CompletionFileArg{{Path: "/tmp/x.go", Content: strPtr("package x\n")}},
		TestEvidence: "> Task :app:test NO-SOURCE\nBUILD SUCCESSFUL in 1s",
	})
	require.NoError(t, err)
	assert.True(t, env.SubmissionDefectOnly)
	assert.Contains(t, env.NextAction, "Re-submit with the missing evidence")
}
