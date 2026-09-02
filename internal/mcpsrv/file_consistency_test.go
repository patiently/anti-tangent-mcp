package mcpsrv

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/planparser"
	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

func tasksFrom(bodies ...string) []planparser.RawTask {
	out := make([]planparser.RawTask, 0, len(bodies))
	for _, b := range bodies {
		out = append(out, planparser.RawTask{Body: b})
	}
	return out
}

func TestCheckFileConsistency_OrderTier_LaterCreateIsAContradiction(t *testing.T) {
	f := checkFileConsistency(tasksFrom(
		"**Files:**\n- Modify: `a.go`\n",
		"**Files:**\n- Create: `a.go`\n",
	), "")
	require.NotNil(t, f)
	assert.Equal(t, verdict.SeverityMajor, f.Severity)
	assert.Equal(t, "task_order_contradiction", f.Criterion)
	assert.Contains(t, f.Evidence, "Task 1")
	assert.Contains(t, f.Evidence, "Task 2")
	assert.Contains(t, f.Evidence, "a.go")
}

func TestCheckFileConsistency_OrderTier_EarlierCreateIsFine(t *testing.T) {
	f := checkFileConsistency(tasksFrom(
		"**Files:**\n- Create: `a.go`\n",
		"**Files:**\n- Modify: `a.go`\n",
	), "")
	assert.Nil(t, f)
}

func TestCheckFileConsistency_NoRepoRoot_SkipsDiskTier(t *testing.T) {
	// a.go is created by no task and (without repo_root) cannot be checked
	// against disk — so no finding.
	f := checkFileConsistency(tasksFrom("**Files:**\n- Modify: `a.go`\n"), "")
	assert.Nil(t, f)
}

func TestCheckFileConsistency_DiskTier_MissingTargetIsAContradiction(t *testing.T) {
	dir := t.TempDir()
	f := checkFileConsistency(tasksFrom("**Files:**\n- Modify: `a.go`\n"), dir)
	require.NotNil(t, f)
	assert.Contains(t, f.Evidence, "does not exist")
}

func TestCheckFileConsistency_DiskTier_ExistingTargetIsFine(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.go"), []byte("x"), 0o600))
	f := checkFileConsistency(tasksFrom("**Files:**\n- Modify: `a.go`\n"), dir)
	assert.Nil(t, f)
}

// THE regression guard. The naive version of this check reported 10
// contradictions on a real plan and all 10 were false positives — 9 of them
// because an earlier task had already been implemented in the worktree, so
// its Create: target existed. A resumed plan run is a legitimate state and
// must never be flagged. See design §6.2.
func TestCheckFileConsistency_AlreadyImplementedWorktreeIsNotAFinding(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.go"), []byte("x"), 0o600))
	f := checkFileConsistency(tasksFrom(
		"**Files:**\n- Create: `a.go`\n",
		"**Files:**\n- Modify: `a.go`\n",
	), dir)
	assert.Nil(t, f, "a Create: target that already exists is not a contradiction")
}

func TestCheckFileConsistency_PathEscapingRepoRootIsIgnored(t *testing.T) {
	dir := t.TempDir()
	f := checkFileConsistency(tasksFrom("**Files:**\n- Modify: `../../etc/passwd`\n"), dir)
	assert.Nil(t, f, "a path outside repo_root is ignored, not stat'd")
}

func TestCheckFileConsistency_NoFilesSections(t *testing.T) {
	assert.Nil(t, checkFileConsistency(tasksFrom("**Goal:** g\n"), t.TempDir()))
}

// TestCheckFileConsistency_OrderTier_EdgeCases covers two off-by-one shapes
// that a check like this breaks on: a path created by two different tasks
// (createdBy must key on the FIRST task, not the last), and a task that
// creates and modifies the same path in its own **Files:** section (the
// "created > taskNum" comparison is strict, so equal must not be a finding).
func TestCheckFileConsistency_OrderTier_EdgeCases(t *testing.T) {
	cases := []struct {
		name  string
		tasks []string
	}{
		{
			name: "first Create wins when a path is created by two different tasks",
			tasks: []string{
				"**Files:**\n- Create: `a.go`\n",
				"**Files:**\n- Modify: `a.go`\n",
				"**Files:**\n- Create: `a.go`\n",
			},
		},
		{
			name: "a Modify in the same task that creates the path is fine",
			tasks: []string{
				"**Files:**\n- Create: `a.go`\n- Modify: `a.go`\n",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := checkFileConsistency(tasksFrom(tc.tasks...), "")
			assert.Nil(t, f)
		})
	}
}

// TestCheckFileConsistency_OrderTier_UsesTitleNumberNotPosition is the
// regression test for the review finding that task_order_contradiction named
// tasks by slice position instead of the plan's own "### Task N:" number.
// Here the plan's own numbers (3, 7) diverge from slice position (1, 2); the
// finding must name Task 3 and Task 7, never Task 1 or Task 2.
//
// This pins MESSAGE WORDING only. Comparison correctness is pinned separately
// by TestCheckFileConsistency_OrderTier_ComparesSlicePositionNotTitleNumber —
// the ordering decision moved to slice index, while naming stayed on the
// plan's own numbers.
func TestCheckFileConsistency_OrderTier_UsesTitleNumberNotPosition(t *testing.T) {
	tasks := []planparser.RawTask{
		{Title: "Task 3: Modify a", Body: "**Files:**\n- Modify: `a.go`\n"},
		{Title: "Task 7: Create a", Body: "**Files:**\n- Create: `a.go`\n"},
	}
	f := checkFileConsistency(tasks, "")
	require.NotNil(t, f)
	assert.Contains(t, f.Evidence, "Task 3")
	assert.Contains(t, f.Evidence, "Task 7")
	assert.NotContains(t, f.Evidence, "Task 1 ")
	assert.NotContains(t, f.Evidence, "Task 2 ")
}

// TestCheckFileConsistency_OrderTier_FallsBackToPositionWhenTitleUnparseable
// covers the fallback half of the same fix: when Title is empty or does not
// match the "Task N:" shape, the finding must still name a task — via the
// 1-based slice position — rather than silently dropping the check.
func TestCheckFileConsistency_OrderTier_FallsBackToPositionWhenTitleUnparseable(t *testing.T) {
	tasks := []planparser.RawTask{
		{Title: "not a task heading", Body: "**Files:**\n- Modify: `a.go`\n"},
		{Title: "", Body: "**Files:**\n- Create: `a.go`\n"},
	}
	f := checkFileConsistency(tasks, "")
	require.NotNil(t, f)
	assert.Contains(t, f.Evidence, "Task 1")
	assert.Contains(t, f.Evidence, "Task 2")
}

// TestCheckFileConsistency_LineAnchoredModifyTarget is the end-to-end guard
// for the false-positive class the line-anchor strip closes (see
// planparser.stripLineAnchor). Plans anchor Modify: bullets to the lines
// being edited; before the strip the disk tier stat'd `a.go:15-23`, a path
// that can never exist, and reported a major finding about a file sitting
// right there on disk. The order tier missed the same plans for the mirror
// reason: the anchored string never matched its unanchored Create: twin.
func TestCheckFileConsistency_LineAnchoredModifyTarget(t *testing.T) {
	t.Run("disk tier does not false-positive on an existing file", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "a.go"), []byte("x"), 0o600))
		f := checkFileConsistency(tasksFrom("**Files:**\n- Modify: `a.go:15-23`\n"), dir)
		assert.Nil(t, f, "a.go exists; the line anchor must not be stat'd as part of the path")
	})

	t.Run("order tier still matches the unanchored Create twin", func(t *testing.T) {
		f := checkFileConsistency(tasksFrom(
			"**Files:**\n- Modify: `a.go:15-23`\n",
			"**Files:**\n- Create: `a.go`\n",
		), "")
		require.NotNil(t, f, "the anchored Modify: must match the later Create: of the same path")
		assert.Contains(t, f.Evidence, "`a.go`")
		assert.NotContains(t, f.Evidence, "15-23")
	})

	t.Run("order tier accepts an earlier Create of the anchored path", func(t *testing.T) {
		f := checkFileConsistency(tasksFrom(
			"**Files:**\n- Create: `a.go`\n",
			"**Files:**\n- Modify: `a.go:15-23`\n",
		), "")
		assert.Nil(t, f)
	})
}

// TestCheckFileConsistency_OrderTier_ComparesSlicePositionNotTitleNumber is
// the regression test for both directions of comparing the plan's declared
// "Task N:" numbers instead of the order the tasks actually execute in.
// Tasks run in slice order; nothing forces the headings to be ascending
// 1..N, and two corpus plans already restart numbering mid-file for phases.
func TestCheckFileConsistency_OrderTier_ComparesSlicePositionNotTitleNumber(t *testing.T) {
	t.Run("phase-restarting numbers do not manufacture a finding", func(t *testing.T) {
		// Correctly ordered: the creating task runs first. Its heading
		// number (5) is larger only because phase 2 restarts at 1.
		tasks := []planparser.RawTask{
			{Title: "Task 5: Add the file", Body: "**Files:**\n- Create: `a.go`\n"},
			{Title: "Task 1: Phase 2 — extend it", Body: "**Files:**\n- Modify: `a.go`\n"},
		}
		assert.Nil(t, checkFileConsistency(tasks, ""),
			"the Create: task runs first; a restarted heading number is not a contradiction")
	})

	t.Run("descending numbers still catch a real ordering bug", func(t *testing.T) {
		// Genuinely out of order: the modifying task runs first. The
		// declared numbers descend, so a title-number comparison misses it.
		tasks := []planparser.RawTask{
			{Title: "Task 9: Extend the file", Body: "**Files:**\n- Modify: `a.go`\n"},
			{Title: "Task 2: Add the file", Body: "**Files:**\n- Create: `a.go`\n"},
		}
		f := checkFileConsistency(tasks, "")
		require.NotNil(t, f, "Task 9 runs before Task 2 here; that is a real contradiction")
		assert.Contains(t, f.Evidence, "Task 9 modifies `a.go`, which is not created until Task 2",
			"the message still names the tasks by the plan's own numbers")
	})

	t.Run("repeated numbers are ordered by position", func(t *testing.T) {
		// Both headings say "Task 1". Only slice order can decide.
		tasks := []planparser.RawTask{
			{Title: "Task 1: Extend the file", Body: "**Files:**\n- Modify: `a.go`\n"},
			{Title: "Task 1: Add the file", Body: "**Files:**\n- Create: `a.go`\n"},
		}
		f := checkFileConsistency(tasks, "")
		require.NotNil(t, f, "equal declared numbers must not suppress a positional contradiction")
	})
}

// The disk tier deliberately reports ONLY a genuine fs.ErrNotExist as a
// finding: a permission error, an ENOTDIR, or an I/O error means the check
// could not LOOK, not that the file is missing. Narrowing to ErrNotExist was
// right, but it left the could-not-look branch emitting nothing at all — an
// unreadable repo_root made the whole disk tier inert while the response
// stayed byte-identical to a clean run. The operator gets a stderr line.
func TestCheckFileConsistency_UnstattableTargetWarnsInsteadOfSilence(t *testing.T) {
	root := t.TempDir()
	// A regular file where the plan expects a directory: stat("<root>/blocker/x.go")
	// returns ENOTDIR, which is NOT fs.ErrNotExist.
	require.NoError(t, os.WriteFile(filepath.Join(root, "blocker"), []byte("x"), 0o600))

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	defer slog.SetDefault(prev)

	f := checkFileConsistency(tasksFrom("**Files:**\n- Modify: `blocker/x.go`\n"), root)

	assert.Nil(t, f,
		"a path the check could not stat must not be reported as \"does not exist\"")
	assert.Contains(t, buf.String(), "could not stat",
		"the could-not-look branch must not be silent")
	assert.Contains(t, buf.String(), filepath.Join(root, "blocker", "x.go"),
		"the warning must name the path it could not stat")
}

// The stat warning is ONE line per call, not one per unstattable path. It is
// emitted from inside `for _, p := range r.Modify` inside `for i, r := range
// refs`, so a per-path line was bounded only by how many Modify: bullets a
// 1MB plan can carry — an unreadable repo_root could bury the operator's
// stderr under hundreds of lines for a single call.
func TestCheckFileConsistency_ManyUnstattableTargetsWarnOnce(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "blocker"), []byte("x"), 0o600))

	body := "**Files:**\n"
	for i := 0; i < 12; i++ {
		body += fmt.Sprintf("- Modify: `blocker/x%d.go`\n", i)
	}

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	defer slog.SetDefault(prev)

	f := checkFileConsistency(tasksFrom(body), root)
	assert.Nil(t, f, "none of these is a genuine not-exists")

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	require.Len(t, lines, 1,
		"12 unstattable Modify: targets must produce ONE warning, got %d lines:\n%s", len(lines), buf.String())

	var rec map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &rec))
	assert.EqualValues(t, 12, rec["unstattable_paths"],
		"the single warning must carry the count it is standing in for")
	assert.Equal(t, filepath.Join(root, "blocker", "x0.go"), rec["first_path"],
		"the single warning must name the first path, so the operator has something to look at")
	assert.NotEmpty(t, rec["first_err"])
}

// The mirror: a target the check CAN stat produces no warning at all, so the
// new stderr line cannot become noise on every healthy call.
func TestCheckFileConsistency_StattableTargetEmitsNoWarning(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "present.go"), []byte("x"), 0o600))

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	defer slog.SetDefault(prev)

	// One target that exists, one that genuinely does not.
	f := checkFileConsistency(tasksFrom("**Files:**\n- Modify: `present.go`\n- Modify: `absent.go`\n"), root)

	require.NotNil(t, f, "the genuinely-missing target must still be a finding")
	assert.Contains(t, f.Evidence, "absent.go")
	assert.NotContains(t, f.Evidence, "present.go")
	assert.Empty(t, buf.String(), "neither a hit nor a genuine not-exists may emit a warning")
}

// CodeRabbit PR #63: resolveUnderRoot is lexical only, and os.Stat follows
// symlinks — so a link that lives inside the repo but points out of it made
// the disk tier report on a path outside the repository entirely. The check
// is documented to answer "does this file exist in the repo"; an in-root
// symlink must not widen that to the whole filesystem.
func TestCheckFileConsistency_DiskTier_InRootSymlinkToOutsideIsIgnored(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "passwd"), []byte("x\n"), 0o600))
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "link")))

	// The target exists, but only outside the root. Before the containment
	// re-check this produced NO finding — which is how the caller learned
	// the external file was there.
	f := checkFileConsistency(tasksFrom("**Files:**\n- Modify: `link/passwd`\n"), root)
	assert.Nil(t, f, "an escaping symlink must be skipped, not stat'd")

	// And the mirror: a name that does not exist behind the same escaping
	// link must not be reported as a missing repo file either.
	f = checkFileConsistency(tasksFrom("**Files:**\n- Modify: `link/absent`\n"), root)
	assert.Nil(t, f)
}

// A symlink that stays inside the root is an ordinary repo file and must
// still satisfy the disk tier — the containment check must not turn every
// in-repo link into a false "does not exist".
func TestCheckFileConsistency_DiskTier_InRootSymlinkStayingInsideIsFine(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "real.go"), []byte("package a\n"), 0o600))
	require.NoError(t, os.Symlink(filepath.Join(root, "real.go"), filepath.Join(root, "alias.go")))

	f := checkFileConsistency(tasksFrom("**Files:**\n- Modify: `alias.go`\n"), root)
	assert.Nil(t, f)
}

// Lstat, not Stat: a dangling link is still a file the repository contains,
// and calling it missing would be a false positive on every broken symlink
// in the tree.
func TestCheckFileConsistency_DiskTier_DanglingInRootSymlinkIsNotMissing(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Symlink(filepath.Join(root, "gone.go"), filepath.Join(root, "dangling.go")))

	f := checkFileConsistency(tasksFrom("**Files:**\n- Modify: `dangling.go`\n"), root)
	assert.Nil(t, f)
}

// CodeRabbit PR #63: "./a.go" and "a.go" are the same file, so a later
// Create: of one must contradict an earlier Modify: of the other. With no
// repo_root the disk tier never runs, so the order tier is the only thing
// that can catch it — which is exactly when the split identity was silent.
func TestCheckFileConsistency_OrderTier_CanonicalisesEquivalentSpellings(t *testing.T) {
	for _, tc := range []struct{ modify, create string }{
		{"./a.go", "a.go"},
		{"a.go", "./a.go"},
		{"dir/../a.go", "a.go"},
		{"./dir/b.go", "dir/b.go"},
	} {
		f := checkFileConsistency(tasksFrom(
			"**Files:**\n- Modify: `"+tc.modify+"`\n",
			"**Files:**\n- Create: `"+tc.create+"`\n",
		), "")
		require.NotNil(t, f, "Modify %q / Create %q should contradict", tc.modify, tc.create)
		assert.Equal(t, "task_order_contradiction", f.Criterion)
	}
}
