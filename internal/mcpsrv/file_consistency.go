// Package mcpsrv: the deterministic, reviewer-free Create/Modify consistency
// check for validate_plan. No provider call; stats only, never reads file
// contents. See design §6.
package mcpsrv

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/patiently/anti-tangent-mcp/internal/planparser"
	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

// taskNumberHeadingRe matches the leading "Task N:" of a
// planparser.RawTask.Title (e.g. "Task 4: Add /healthz endpoint").
var taskNumberHeadingRe = regexp.MustCompile(`^Task (\d+):`)

// taskNumber returns the plan's own declared number for tasks[i] — parsed
// from its Title — falling back to the 1-based slice position when Title is
// empty or does not match the "Task N:" shape. Findings must name the task
// the plan's author would recognize: on a plan whose "### Task N:" headings
// are not exactly 1..N (a deletion, an insertion, a plan numbered from 0),
// the slice position and the plan's own number diverge, and a finding that
// names the position is a finding that names the wrong task.
func taskNumber(tasks []planparser.RawTask, i int) int {
	if m := taskNumberHeadingRe.FindStringSubmatch(tasks[i].Title); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil {
			return n
		}
	}
	return i + 1
}

// checkFileConsistency reports Modify: targets that cannot exist when their
// task runs. Returns nil when the plan is consistent — which is the expected
// outcome on a well-formed plan; this is a guard, not a source of findings.
//
// ONLY the Modify direction is checked. The mirror check — flagging a
// Create: target that already exists on disk — was measured at 9 false
// positives out of 10 findings on a real plan, every one of them because an
// earlier task had already been implemented in the worktree. A resumed or
// partially-executed plan run is a legitimate state, so that direction is
// deliberately absent. See design §6.2.
//
// Two tiers:
//
//   - Order tier (always): a Modify: target whose only Create: comes from a
//     LATER task. Provable from the plan text alone.
//   - Disk tier (repoRoot != ""): a Modify: target that exists on neither
//     disk nor any earlier task's Create: list.
//
// repoRoot == "" skips the disk tier rather than erroring: the check is free
// and optional, and refusing to review a plan for want of an optional
// argument would be a regression.
func checkFileConsistency(tasks []planparser.RawTask, repoRoot string) *verdict.Finding {
	refs := make([]planparser.TaskFileRefs, len(tasks))
	for i, t := range tasks {
		refs[i] = planparser.FileRefs(t.Body)
	}

	// createdBy[path] = the plan's own declared number of the FIRST task
	// that creates it (see taskNumber).
	createdBy := map[string]int{}
	for i, r := range refs {
		for _, p := range r.Create {
			if _, seen := createdBy[p]; !seen {
				createdBy[p] = taskNumber(tasks, i)
			}
		}
	}

	var lines []string
	for i, r := range refs {
		taskNum := taskNumber(tasks, i)
		for _, p := range r.Modify {
			if created, ok := createdBy[p]; ok {
				if created > taskNum {
					lines = append(lines, fmt.Sprintf(
						"Task %d modifies `%s`, which is not created until Task %d", taskNum, p, created))
				}
				continue
			}
			if repoRoot == "" {
				continue
			}
			abs, ok := resolveUnderRoot(repoRoot, p)
			if !ok {
				continue
			}
			if _, err := os.Stat(abs); err != nil {
				lines = append(lines, fmt.Sprintf(
					"Task %d modifies `%s`, which does not exist and is created by no earlier task", taskNum, p))
			}
		}
	}
	if len(lines) == 0 {
		return nil
	}
	return &verdict.Finding{
		Severity:   verdict.SeverityMajor,
		Category:   verdict.CategoryOther,
		Criterion:  "task_order_contradiction",
		Evidence:   strings.Join(lines, "\n"),
		Suggestion: "Reorder the tasks, or add the missing Create: bullet to an earlier task.",
	}
}

// resolveUnderRoot joins a repo-relative plan path to root and reports
// whether the result stays inside root. A bullet reading "../../etc/passwd"
// is ignored rather than stat'd — the check has no business touching
// anything outside the repository it was pointed at.
func resolveUnderRoot(root, rel string) (string, bool) {
	if filepath.IsAbs(rel) {
		return "", false
	}
	abs := filepath.Clean(filepath.Join(root, rel))
	r, err := filepath.Rel(filepath.Clean(root), abs)
	if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return "", false
	}
	return abs, true
}
