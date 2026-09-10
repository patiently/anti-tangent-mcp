// Package mcpsrv: the deterministic, reviewer-free check that submitted test
// evidence describes a run that actually executed tests.
package mcpsrv

import (
	"regexp"

	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

// noExecutionMarkers match output stating that no test ran and none passed.
//
// The distinction that governs this list is whether the marker attests a
// prior PASS. Gradle marks a task UP-TO-DATE or FROM-CACHE only after a
// successful execution on unchanged inputs — a failing test task re-executes
// — and `go test` caches only passing results. Those markers therefore say
// the tests pass on the current inputs; they carry no counts, but neither
// does a plain successful Gradle run, so treating them as missing evidence
// would penalise the cached run and accept an equally count-free executed
// one. NO-SOURCE and its equivalents below make the opposite statement:
// there was nothing to run.
var noExecutionMarkers = []*regexp.Regexp{
	// NO-SOURCE is scoped to a TEST task. Gradle reports it for any task with
	// no inputs, and a healthy build routinely prints it for
	// processTestResources or a resource task while the test task beside it
	// runs a full suite; matching NO-SOURCE anywhere would reject that build.
	// The pattern requires a task segment ending in "test"/"Test", so
	// :app:test, :shared:jvmTest and :app:testDebugUnitTest match while
	// :app:processTestResources does not.
	regexp.MustCompile(`(?m)^.*:[A-Za-z0-9_.-]*[Tt]est\s+NO-SOURCE\s*$`),
	regexp.MustCompile(`(?i)\bno tests ran\b`),
	regexp.MustCompile(`(?i)\bno tests found\b`),
	regexp.MustCompile(`\[no test files\]`),
}

// executionMarkers match output showing that at least one test did run, and
// suppress the finding when they appear alongside a no-execution marker. A
// multi-module build legitimately reports NO-SOURCE for a module with no
// tests while another module runs its suite, and that submission is fine.
//
// A cached result counts as a run here, for the reason set out on
// noExecutionMarkers above: both tools cache only a PASS, so the cached line
// attests the same thing the fresh one does. Cached spellings need their own
// patterns because the executed spellings match on what the cache replaces —
// a duration for `go test`, a bare task line for Gradle.
var executionMarkers = []*regexp.Regexp{
	regexp.MustCompile(`(?m)^ok\s+\S+\s+(?:\d|\(cached\))`),
	// The count must be POSITIVE. `\d+` also matches zero, so evidence
	// pairing a no-execution marker with "0 tests ran" or "0 passed" would
	// suppress the very finding that evidence calls for.
	regexp.MustCompile(`(?i)\b[1-9]\d* (?:tests?|examples?) (?:passed|ran|completed)\b`),
	regexp.MustCompile(`(?i)\b[1-9]\d* passed\b`),
	// gotestsum's non-standard formats print no per-package "ok" line and no
	// per-test counts; their summary is "DONE 42 tests in 1.234s". Without
	// this, output from a runner configured that way carries no recognised
	// execution marker at all, so one package with no test files in it would
	// draw the finding on its own. The count must be positive for the same
	// reason as above.
	regexp.MustCompile(`(?m)^DONE [1-9]\d* tests?\b`),
	// A Gradle test-task line carrying no status at all is an EXECUTED task:
	// Gradle annotates skipped work (UP-TO-DATE, FROM-CACHE, NO-SOURCE,
	// SKIPPED) and leaves a task it actually ran unannotated. Without this, a
	// build whose test task ran and whose resource task was NO-SOURCE has no
	// recognised execution marker at all, because a plain successful Gradle
	// run prints no per-test counts.
	regexp.MustCompile(`(?m)^> Task :[A-Za-z0-9_.:-]*[Tt]est\s*$`),
	// The same Gradle test task served from the cache. The annotation the
	// pattern above relies on being ABSENT is present here, so that pattern
	// cannot reach these two lines.
	regexp.MustCompile(`(?m)^> Task :[A-Za-z0-9_.:-]*[Tt]est\s+(?:FROM-CACHE|UP-TO-DATE)\s*$`),
}

// testEvidenceFindings reports evidence whose own text says no test executed.
// It never inspects a summary the implementer wrote themselves: a marker is a
// verbatim string from a build tool, and prose carrying none draws nothing.
func testEvidenceFindings(evidence string) []verdict.Finding {
	if evidence == "" {
		return nil
	}
	matched := false
	for _, re := range noExecutionMarkers {
		if re.MatchString(evidence) {
			matched = true
			break
		}
	}
	if !matched {
		return nil
	}
	for _, re := range executionMarkers {
		if re.MatchString(evidence) {
			return nil
		}
	}
	return []verdict.Finding{{
		Severity:  verdict.SeverityMajor,
		Category:  verdict.CategoryInsufficientEvidence,
		Criterion: "test_evidence",
		Evidence: "The submitted `test_evidence` states that no test executed — it contains a " +
			"no-source / no-tests-found marker and nothing showing a suite running.",
		Suggestion: "Point the run at a target that has tests and re-submit its output, or attach " +
			"JUnit XML `tests=`/`failures=` counts. This is a submission defect — no code rework " +
			"is implied.",
	}}
}
