// Package mcpsrv: the deterministic, reviewer-free Create/Modify consistency
// check for validate_plan. No provider call; stats only, never reads file
// contents. See design §6.
package mcpsrv

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
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

	// createdAt[path] = the SLICE INDEX of the first task that creates it.
	//
	// Ordering is decided on slice index, never on the plan's own "Task N:"
	// number, because tasks execute in the order they appear in the file and
	// nothing forces the headings to be 1..N in ascending order. Two real
	// corpus plans restart numbering mid-file (1-5, then 1, 2, then 6, 7)
	// for phases; a plan may also renumber descending or repeat a number
	// after an edit. Comparing declared numbers there produces both failure
	// directions: a bogus blocking finding on a correctly-ordered plan whose
	// numbers restart, and a silent miss on a genuinely out-of-order plan
	// whose numbers happen to descend.
	//
	// taskNumber is still what NAMES the tasks in the message — a finding
	// must name the task the plan's author would recognize (see its doc
	// comment). Comparison and naming are deliberately separate concerns.
	createdAt := map[string]int{}
	for i, r := range refs {
		for _, p := range r.Create {
			if _, seen := createdAt[p]; !seen {
				createdAt[p] = i
			}
		}
	}

	var lines []string
	// Stat failures are COUNTED here and reported in a single Warn after the
	// loops, not logged per path. This sits inside `for _, p := range r.Modify`
	// inside `for i, r := range refs`, so a per-path Warn was bounded only by
	// how many unstattable Modify: bullets a 1MB plan can hold — an
	// unreadable repo_root could emit hundreds of stderr lines for one call,
	// against CLAUDE.md's one-line-per-call convention. One summary line per
	// degraded surface keeps the signal and drops the flood.
	statErrs := 0
	var firstStatErrPath string
	var firstStatErr error
	for i, r := range refs {
		taskNum := taskNumber(tasks, i)
		for _, p := range r.Modify {
			if createdIdx, ok := createdAt[p]; ok {
				if createdIdx > i {
					lines = append(lines, fmt.Sprintf(
						"Task %d modifies `%s`, which is not created until Task %d",
						taskNum, p, taskNumber(tasks, createdIdx)))
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
			// resolveUnderRoot is LEXICAL only, and os.Stat follows
			// symlinks. A link inside the repo pointing out of it
			// ("vendor -> /etc") therefore makes the disk tier answer
			// "does this path exist anywhere on this box" instead of
			// "does this file exist in the repository" - the question it
			// is documented to answer, and the only one a plan reviewer
			// has any business asking.
			//
			// Resolve the PARENT and require it to stay under the root,
			// then Lstat the leaf. Lstat, not Stat, because a symlink
			// that lives in the repo IS a repo file: reporting it missing
			// because its target is absent would be a false positive on
			// every dangling in-repo link. A parent that will not resolve
			// falls through to the os.Stat below, whose error handling
			// already separates "missing" from "could not look".
			leaf := abs
			if rp, rerr := filepath.EvalSymlinks(filepath.Dir(abs)); rerr == nil {
				if !withinRoots(rp, []string{repoRoot}) {
					continue
				}
				leaf = filepath.Join(rp, filepath.Base(abs))
			}
			// ONLY a genuine not-exists is a finding. A permission error, a
			// too-long path, or an I/O error means the check could not look,
			// not that the file is missing — reporting "does not exist" for
			// those states a fact the server did not establish.
			//
			// But "could not look" must not be silent either: an unreadable
			// repo_root (wrong ownership, a mount that went away) makes the
			// whole disk tier inert while the response is byte-identical to
			// a clean run. The operator gets ONE stderr line per call naming
			// how many targets could not be stat'd plus the first path and
			// error; the finding list stays honest.
			_, serr := os.Lstat(leaf)
			switch {
			case errors.Is(serr, fs.ErrNotExist):
				lines = append(lines, fmt.Sprintf(
					"Task %d modifies `%s`, which does not exist and is created by no earlier task", taskNum, p))
			case serr != nil:
				statErrs++
				if firstStatErr == nil {
					firstStatErrPath, firstStatErr = leaf, serr
				}
			}
		}
	}
	if statErrs > 0 {
		slog.Warn("validate_plan: could not stat one or more Modify: targets under repo_root; disk tier skipped for them",
			slog.String("tool", "validate_plan"),
			slog.Int("unstattable_paths", statErrs),
			slog.String("first_path", firstStatErrPath),
			slog.String("first_err", firstStatErr.Error()))
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
