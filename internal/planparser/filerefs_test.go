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

// Regression test for panic on empty tail after colon.
func TestFileRefs_EmptyPathAfterColon(t *testing.T) {
	// A bullet with only whitespace after the colon should not panic.
	refs := FileRefs("**Files:**\n- Modify:   \n")
	assert.Empty(t, refs.Create)
	assert.Empty(t, refs.Modify)
	assert.Empty(t, refs.Delete)
}

// Regression test for unterminated backtick.
func TestFileRefs_UnterminatedBacktick(t *testing.T) {
	// A path with an opening backtick but no closing backtick should
	// extract the content after the opening backtick as the first token.
	refs := FileRefs("**Files:**\n- Modify: `abc.go\n")
	assert.Equal(t, []string{"abc.go"}, refs.Modify, "unterminated backtick should be stripped")
}

// Regression test for bare path first-token behavior.
func TestFileRefs_BarePathFirstToken(t *testing.T) {
	// A bare path with multiple whitespace-delimited tokens should
	// take only the first token.
	refs := FileRefs("**Files:**\n- Delete: foo bar.go\n")
	assert.Equal(t, []string{"foo"}, refs.Delete, "bare path should be first whitespace-delimited token")
}

// Plans anchor Modify: bullets to the lines being edited — superpowers'
// task-format reference asks for "line ranges for modifications", and this
// repo's own plans write "- Modify: `internal/verdict/parser.go:57-70`".
// The anchor is not part of the path: leaving it on makes the disk tier stat
// a path that can never exist, and stops the anchored form from matching its
// unanchored Create: twin.
func TestFileRefs_StripsTrailingLineAnchor(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"single line, backticked", "**Files:**\n- Modify: `a/b.go:57`\n", "a/b.go"},
		{"line range, backticked", "**Files:**\n- Modify: `a/b.go:57-70`\n", "a/b.go"},
		{"comma pair, backticked", "**Files:**\n- Modify: `a/b.go:57,70`\n", "a/b.go"},
		{"bare path", "**Files:**\n- Modify: a/b.go:15-23\n", "a/b.go"},
		{"anchor plus parenthetical", "**Files:**\n- Modify: `a/b.go:15-23` (the render func)\n", "a/b.go"},
		// line:column is what an editor and most greps emit. A single
		// non-repeating anchor group stripped only the trailing ":12",
		// leaving the phantom path "a/b.go:57" — which stats as missing and
		// never matches its unanchored Create: twin, i.e. exactly the bug
		// the anchor strip exists to prevent, one colon deeper.
		{"line and column", "**Files:**\n- Modify: `a/b.go:57:12`\n", "a/b.go"},
		{"range then column", "**Files:**\n- Modify: `a/b.go:57-70:12`\n", "a/b.go"},
		{"bare line and column", "**Files:**\n- Modify: a/b.go:57:12\n", "a/b.go"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, []string{tc.want}, FileRefs(tc.body).Modify)
		})
	}
}

// The anchor strip must not eat anything that merely contains a colon. A
// Windows drive letter has no digits after its colon and is not at the end
// of the string; a URL scheme is followed by slashes; a filename that ends
// in digits has no colon before them.
func TestFileRefs_LineAnchorStripLeavesOtherColonsAlone(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"windows drive letter", "**Files:**\n- Modify: `C:\\repo\\a.go`\n", `C:\repo\a.go`},
		{"url", "**Files:**\n- Modify: `https://example.com/a.go`\n", "https://example.com/a.go"},
		{"filename ending in digits", "**Files:**\n- Modify: `migrations/0042.sql`\n", "migrations/0042.sql"},
		{"colon then non-digits", "**Files:**\n- Modify: `a/b.go:head`\n", "a/b.go:head"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, []string{tc.want}, FileRefs(tc.body).Modify)
		})
	}
}

// A bullet whose path is nothing but an anchor names no file, so it must
// drop out rather than register ":15" as a path.
func TestFileRefs_AnchorOnlyPathIsDropped(t *testing.T) {
	refs := FileRefs("**Files:**\n- Modify: `:15-23`\n")
	assert.Empty(t, refs.Modify)
}
