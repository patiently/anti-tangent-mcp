package mcpsrv

import (
	"os"
	"path/filepath"
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
