package stalecomments

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const goDiff = `diff --git a/internal/state/sweep.go b/internal/state/sweep.go
index 1111111..2222222 100644
--- a/internal/state/sweep.go
+++ b/internal/state/sweep.go
@@ -10,9 +10,5 @@ import "time"
 // handleRetired drains retired entries before the sweep.
 func (s *Sweeper) Run() {
 	switch s.state {
-	case StateRetired, StateArchived:
-		s.handleRetired()
-	}
+	}
 }
-func (s *Sweeper) handleRetired() {
-}
`

func TestParseDiff_ClassifiesHunkLinesAndNumbersPostChangeLines(t *testing.T) {
	files := ParseDiff(goDiff)
	require.Len(t, files, 1)
	f := files[0]
	assert.Equal(t, "internal/state/sweep.go", f.Path)
	assert.Equal(t, []string{"\tcase StateRetired, StateArchived:", "\t\ts.handleRetired()", "\t}", "func (s *Sweeper) handleRetired() {", "}"}, f.Removed)
	assert.Equal(t, []string{"\t}"}, f.Added)
	assert.Equal(t, []Line{
		{Number: 10, Text: "// handleRetired drains retired entries before the sweep."},
		{Number: 11, Text: "func (s *Sweeper) Run() {"},
		{Number: 12, Text: "\tswitch s.state {"},
		{Number: 13, Text: "\t}"},
		{Number: 14, Text: "}"},
	}, f.Post)
}

func TestParseDiff_RemovedLineStartingWithDashesIsNotAHeader(t *testing.T) {
	diff := "--- a/schema.sql\n+++ b/schema.sql\n@@ -1,2 +1,1 @@\n--- legacy_status is kept for old clients\n-++ not a header either\n+-- current\n"
	files := ParseDiff(diff)
	require.Len(t, files, 1)
	assert.Equal(t, "schema.sql", files[0].Path)
	assert.Equal(t, []string{"-- legacy_status is kept for old clients", "++ not a header either"}, files[0].Removed)
	assert.Equal(t, []string{"-- current"}, files[0].Added)
}

func TestParseDiff_DeletedFileHasNoPathAndHeaderlessTextHasNoLines(t *testing.T) {
	files := ParseDiff("diff --git a/old.py b/old.py\ndeleted file mode 100644\n--- a/old.py\n+++ /dev/null\n@@ -1,2 +0,0 @@\n-def legacy_sweep(self):\n-    pass\n")
	require.Len(t, files, 1)
	assert.Equal(t, "", files[0].Path)
	assert.Equal(t, []string{"def legacy_sweep(self):", "    pass"}, files[0].Removed)

	assert.Empty(t, RemovedNames(ParseDiff("-func handleRetired() {\n+func other() {\n")))
}

func TestParseDiff_SeparatesFilesAndStripsQuotesAndTimestamps(t *testing.T) {
	diff := "--- a/one.kt\t2026-09-15\n+++ \"b/dir with space/one.kt\"\t2026-09-15\n@@ -1 +1 @@\n-fun oldName() {}\n+fun newName() {}\n" +
		"--- a/two.ts\n+++ b/two.ts\n@@ -3 +3 @@\n-export function legacyHandler() {}\n+export function handler() {}\n"
	files := ParseDiff(diff)
	require.Len(t, files, 2)
	assert.Equal(t, "dir with space/one.kt", files[0].Path)
	assert.Equal(t, "two.ts", files[1].Path)
	assert.Equal(t, []Line{{Number: 3, Text: "export function handler() {}"}}, files[1].Post)
}

func TestRemovedNames_AcrossLanguages(t *testing.T) {
	diff := `--- a/a.go
+++ b/a.go
@@ -1,4 +1,1 @@
-func (s *Sweeper) handleRetired() {
-	case pkg.StateRetired, .StateArchived:
-type LegacyQueue struct {
 package a
--- a/b.kt
+++ b/b.kt
@@ -1,4 +1,2 @@
-enum class LegacyState { A, B }
-fun String.toLegacyLabel(): String = this
-fun retireAccount(id: Long) {}
-val id = 1
+fun retireAccount(id: Long, reason: String) {}
+// fun ghostName() is gone
--- a/c.py
+++ b/c.py
@@ -1,2 +1,0 @@
-async def legacy_sweep(self):
-# def commentedOut():
--- a/d.ts
+++ b/d.ts
@@ -1,2 +1,0 @@
-export const MAX_RETRIES = 3
-export function oldHandler<T>(x: T) {}
`
	assert.Equal(t, []string{
		"handleRetired", "StateRetired", "StateArchived", "LegacyQueue",
		"LegacyState", "toLegacyLabel",
		"legacy_sweep",
		"MAX_RETRIES", "oldHandler",
	}, RemovedNames(ParseDiff(diff)))
}

func TestRemovedNames_StopsAtMaxNames(t *testing.T) {
	var b strings.Builder
	b.WriteString("--- a/x.go\n+++ b/x.go\n@@ -1,40 +0,0 @@\n")
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&b, "-func removedName%02d() {}\n", i)
	}
	names := RemovedNames(ParseDiff(b.String()))
	require.Len(t, names, MaxNames)
	assert.Equal(t, "removedName00", names[0])
	assert.Equal(t, "removedName29", names[MaxNames-1])
}

func TestIsComment(t *testing.T) {
	for _, s := range []string{"// a", "  # a", " * a", "/* a", "<!-- a", "-- a", "\t//a"} {
		assert.True(t, IsComment(s), "%q", s)
	}
	for _, s := range []string{"x := 1 // a", "func a()", "", "  "} {
		assert.False(t, IsComment(s), "%q", s)
	}
}

func TestScan_MatchesWholeIdentifiersInCommentsOnly(t *testing.T) {
	sources := []Source{{Path: "a.go", Lines: []Line{
		{Number: 1, Text: "// handleRetired drains the queue."},
		{Number: 2, Text: "// handleRetiredLater is a different symbol."},
		{Number: 3, Text: "s.handleRetired() // code, not a comment line"},
		{Number: 4, Text: "\t * see handleRetired()."},
	}}}
	hits := Scan([]string{"handleRetired"}, sources)
	require.Len(t, hits, 2)
	assert.Equal(t, Hit{Path: "a.go", Line: 1, Name: "handleRetired", Text: "// handleRetired drains the queue."}, hits[0])
	assert.Equal(t, "a.go:4: * see handleRetired().", hits[1].String())
}

func TestScan_DeduplicatesAndCaps(t *testing.T) {
	var lines []Line
	for i := 1; i <= 30; i++ {
		lines = append(lines, Line{Number: i, Text: "# legacy_sweep " + strings.Repeat("x", 300)})
	}
	sources := []Source{{Path: "c.py", Lines: lines[:1]}, {Path: "c.py", Lines: lines}}
	hits := Scan([]string{"legacy_sweep"}, sources)
	require.Len(t, hits, MaxHits)
	assert.Equal(t, 1, hits[0].Line)
	assert.Equal(t, 2, hits[1].Line)
	assert.Equal(t, MaxLineRunes, len([]rune(hits[0].Text)))
	assert.Empty(t, Scan(nil, sources))
}

func TestFileLines(t *testing.T) {
	assert.Equal(t, []Line{{Number: 1, Text: "a"}, {Number: 2, Text: "b"}}, FileLines("a\r\nb\n"))
	assert.Nil(t, FileLines(""))
}
