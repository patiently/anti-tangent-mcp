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
