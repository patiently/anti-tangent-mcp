package planparser

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFileRefs_Basic(t *testing.T) {
	body := "### Task 1: t\n\n**Files:**\n" +
		"- Create: `internal/a/a.go`\n" +
		"- Modify: `internal/b/b.go` (lines 10-20)\n" +
		"- Delete: internal/c/c.go\n\n" +
		"**Acceptance Criteria:**\n- [ ] x\n"

	refs := FileRefs(body)
	assert.Equal(t, []string{"internal/a/a.go"}, refs.Create)
	assert.Equal(t, []string{"internal/b/b.go"}, refs.Modify)
	assert.Equal(t, []string{"internal/c/c.go"}, refs.Delete)
}

func TestFileRefs_CreateOrModifyCountsAsBoth(t *testing.T) {
	refs := FileRefs("**Files:**\n- Create/Modify: `x.go`\n")
	assert.Equal(t, []string{"x.go"}, refs.Create)
	assert.Equal(t, []string{"x.go"}, refs.Modify)
}

func TestFileRefs_CaseAndBulletTolerance(t *testing.T) {
	refs := FileRefs("**Files:**\n* modify: `x.go`\n-   MODIFY:   `y.go`\n")
	assert.Equal(t, []string{"x.go", "y.go"}, refs.Modify)
}

func TestFileRefs_NoFilesSection(t *testing.T) {
	refs := FileRefs("### Task 1: t\n\n**Goal:** g\n\n- Modify: `x.go`\n")
	assert.Empty(t, refs.Create)
	assert.Empty(t, refs.Modify)
	assert.Empty(t, refs.Delete)
}

func TestFileRefs_StopsAtSectionEnd(t *testing.T) {
	body := "**Files:**\n- Modify: `a.go`\n\n**Steps:**\n- Modify: `b.go`\n"
	refs := FileRefs(body)
	assert.Equal(t, []string{"a.go"}, refs.Modify, "bullets under a later section are not file refs")
}

func TestFileRefs_TestPrefixIgnored(t *testing.T) {
	refs := FileRefs("**Files:**\n- Test: `a_test.go`\n- Modify: `a.go`\n")
	assert.Equal(t, []string{"a.go"}, refs.Modify)
	assert.Empty(t, refs.Create)
}
