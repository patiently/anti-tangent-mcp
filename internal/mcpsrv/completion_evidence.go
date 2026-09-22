// Package mcpsrv: helpers for the validate_completion evidence prompt's
// "referenced paths missing evidence" advisory. See ValidateCompletion.
package mcpsrv

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// referencedEvidencePathRE matches doc/artifact path tokens that might be named
// in the implementer's summary. The extension list is intentionally narrow:
// source-code extensions (.go, .kt, .py, .ts) are excluded because they almost
// always appear in diffs even when not deliverables — including them would
// produce noisy hints. Doc/config formats are far more likely to be deliverables
// that need explicit evidence.
var referencedEvidencePathRE = regexp.MustCompile(`[A-Za-z0-9_./-]+\.(?:md|txt|json|ya?ml)\b`)

// referencedPathsMissingEvidence returns the deduplicated set of doc/artifact
// paths named in summary that are NOT present in either files (path-suffix
// match — see pathTailMatches) or finalDiff (substring match). The result is
// used by ValidateCompletion to render an advisory note in the post-review
// prompt — it never mutates findings, never rejects the request, and never
// affects the verdict. It only nudges the reviewer to require full evidence
// if a listed path is a deliverable.
func referencedPathsMissingEvidence(summary string, files []FileArg, finalDiff string) []string {
	candidates := referencedEvidencePathRE.FindAllString(summary, -1)
	if len(candidates) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	missing := make([]string, 0, len(candidates))
	for _, path := range candidates {
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		if pathPresentInEvidence(path, files, finalDiff) {
			continue
		}
		missing = append(missing, path)
	}
	if len(missing) == 0 {
		return nil
	}
	return missing
}

// pathPresentInEvidence reports whether path appears as a final_files entry
// (path-suffix match — see pathTailMatches) or anywhere in the final_diff
// text (substring). The final_diff substring match is intentionally
// permissive — diff headers, rename old/new paths, and contextual filename
// mentions all count as "evidence was provided."
func pathPresentInEvidence(path string, files []FileArg, finalDiff string) bool {
	for _, f := range files {
		if pathTailMatches(f.Path, path) {
			return true
		}
	}
	return strings.Contains(finalDiff, path)
}

// pathTailMatches reports whether tail names the same file as candidate:
// either the two are identical, or candidate ends with tail immediately
// after a path separator.
//
// docs/protocol/implementer.md tells implementers to pass ABSOLUTE
// final_files paths, while args.Summary is scanned for the RELATIVE paths
// implementers actually write in prose (e.g. "docs/foo.md") — so exact
// equality alone would never match and this advisory would fire spuriously
// on every doc deliverable submitted the documented way. Comparing by
// suffix fixes that (an absolute f.Path ending in the relative summary
// mention counts as present), but a bare suffix check on its own is too
// loose: "myfoo.md" would satisfy a reference to "foo.md" purely because
// the characters happen to line up at the tail, with no real path
// relationship between the two. Requiring a separator immediately before
// the matched tail closes that gap while still accepting the legitimate
// absolute-vs-relative case.
//
// Both sides are normalized to forward slashes before comparing, and the
// separator check accepts either byte after that — NOT filepath.Separator,
// which is a single OS-specific byte ('/' when this server itself runs on
// Unix). final_files paths are the implementer's own filesystem paths, which
// on a Windows implementer are backslash-separated (e.g. `C:\repo\docs\foo.md`)
// regardless of what OS this server happens to run on; comparing against
// filepath.Separator alone made this advisory fire spuriously on every doc
// deliverable from a Windows caller — the exact false positive this function
// exists to avoid, just triggered from the other side.
func pathTailMatches(candidate, tail string) bool {
	candidate = strings.ReplaceAll(candidate, `\`, "/")
	tail = strings.ReplaceAll(tail, `\`, "/")
	if candidate == tail {
		return true
	}
	if tail == "" || !strings.HasSuffix(candidate, tail) {
		return false
	}
	i := len(candidate) - len(tail)
	return candidate[i-1] == '/'
}

// hunkHeaderRe matches a unified-diff hunk header's old and new ranges. A
// combined diff's "@@@" header does not match, and is not checked: it has one
// range per parent and says nothing this guard can judge.
var hunkHeaderRe = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

// hunkLineSpend maps a hunk body line's leading marker byte to how many of
// the enclosing hunk's declared old/new line counts (from its "@@ " header)
// it accounts for: a context line (' ') accounts for one of each; a removed
// line ('-') for one old line; an added line ('+') for one new line; and a
// "\ No newline at end of file" marker ('\\') for neither. Byte 0 (never a
// real marker) stands in for a completely empty line — some tools strip the
// trailing space from an empty context line, and such a diff is still
// legitimate evidence, so it spends one of each exactly like ' ' does. A
// lookup miss means the declared count was over-stated: the body ran out
// before the header's count did.
var hunkLineSpend = map[byte][2]int{0: {1, 1}, ' ': {1, 1}, '-': {1, 0}, '+': {0, 1}, '\\': {0, 0}}

// diffHunkOrderReason reports why a diff cannot have come from git, or "" when
// nothing says it did not. Within one file section git emits hunks in
// ascending order and merges any that would touch, so a hunk that starts at or
// before the previous hunk's end means two files' hunks were concatenated
// under one header, or a section was assembled by hand. Such a diff reads to
// the reviewer as evidence that contradicts itself.
//
// A new section starts at each "diff --git" line, so the same file appearing
// twice — as git log -p emits it — is judged per section. Line-by-line state
// lives in diffHunkTracker so each function's own branching stays small.
//
// A hunk's own "@@ " header declares how many old/new lines its body has,
// but that declaration is attacker-controlled input, not a fact — exhausting
// it is not proof the hunk actually ended. So while a hunk still owes lines
// against its declared count, every line is spent as payload (per
// hunkLineSpend) regardless of what it looks like: an added line whose own
// content starts with "++ " renders, once the "+" body marker is prepended,
// as a line starting with "+++ ", indistinguishable from a real "+++ "
// header at the prefix-check level, but it is still just an added line
// while the count says so. A line that cannot be spent this way (its marker
// byte is not in hunkLineSpend) while a count is still outstanding means
// the header declared more lines than the body has: over-declared, rejected
// immediately.
//
// Once a hunk's count is exhausted, the very next line is validated before
// being handed to observe: a context line (" ") or a "\ No newline..."
// marker can never be the start of any recognized header, so either one
// arriving right after exhaustion means the header declared fewer lines
// than the body has: under-declared, rejected. "-"/"+" are not judged here,
// since "--- "/"+++ " genuinely start that way too; observe's sawDashHeader
// check catches a disguised "+++ " instead (see its own doc comment), which
// is the one of the four markers this guard exists to protect at all — a
// leaked "diff --git "/"diff --cc "/"@@ " line can never collide with a
// payload marker's first byte either, so those always reach observe as
// themselves.
func diffHunkOrderReason(diff string) string {
	t := &diffHunkTracker{}
	for _, line := range strings.Split(diff, "\n") {
		if max(t.oldRemaining, t.newRemaining) > 0 {
			// Spend this line against the open hunk's declared count (see
			// hunkLineSpend for the empty-line special case — "+ \x00" gives
			// a real line's first byte unchanged and stands in for the
			// empty-line sentinel without a length check). A marker byte
			// hunkLineSpend doesn't have means the header declared more
			// lines than the body has.
			spend, ok := hunkLineSpend[(line + "\x00")[0]]
			if !ok {
				return fmt.Sprintf("%s: hunk %q declared more lines than its body has; %q arrived before the declared count was reached, so this diff was not produced by git", t.path, t.hunkLine, line)
			}
			t.oldRemaining -= spend[0]
			t.newRemaining -= spend[1]
			t.justExhausted = max(t.oldRemaining, t.newRemaining) <= 0
			continue
		}
		exhausted := t.justExhausted
		t.justExhausted = false
		unexplainedPayload := strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\\")
		if exhausted && unexplainedPayload {
			return fmt.Sprintf("%s: hunk %q declared fewer lines than its body has; %q continues the body after the declared count was reached, so this diff was not produced by git", t.path, t.hunkLine, line)
		}
		if reason := t.observe(line); reason != "" {
			return reason
		}
	}
	return ""
}

// diffHunkTracker carries the current file section's path and the previous
// hunk's old/new end positions across the line-by-line scan in
// diffHunkOrderReason. sawGitHeader distinguishes a section a "diff --git"/
// "diff --cc" line opened from a headerless plain-diff section: real git
// never emits a second "--- "/"+++ " pair within one header-opened section,
// so once such a header is seen, a later "+++ " must not reset the tracked
// end positions again — doing so would let a hand-assembled diff defeat the
// whole check by inserting a spurious pair mid-section. sawDashHeader is true
// exactly when the immediately preceding line was "--- ": git always emits
// "--- " right before "+++ ", so a "+++ " line arriving without it just
// preceding is not a real header, whatever it claims to name — see
// diffHunkOrderReason and observe's doc comments for why a declared hunk
// count alone cannot be trusted to tell payload from headers.
// oldRemaining/newRemaining count down the current hunk's old/new lines
// still due; hunkLine holds its "@@ " text, for naming it in a rejection
// when its declaration turns out to be wrong.
type diffHunkTracker struct {
	path                       string
	oldEnd, newEnd             int
	sawGitHeader               bool
	sawDashHeader              bool
	oldRemaining, newRemaining int
	hunkLine                   string
	justExhausted              bool
}

// observe updates the tracker for one diff line once diffHunkOrderReason's
// own loop has established it is not hunk payload, and reports a rejection
// reason when a "@@ " hunk header names one via observeHunk — see
// diffHunkOrderReason's doc comment for why that means git could not have
// emitted this diff. A header the regex cannot parse (e.g. a combined
// diff's "@@@" form) is left unjudged, and the tracker's end positions are
// unchanged.
//
// "diff --git"/"diff --cc" open a section and set sawGitHeader, so a later
// "+++ " within that same section does not reset the end positions again:
// resetting on every "+++ " regardless of section origin would let a
// hand-assembled diff defeat the whole check by inserting a spurious pair
// mid-section, since real git never emits a second "--- "/"+++ " pair inside
// one header-opened section. A plain `diff -u`/`diff -ruN` file pair (no
// "diff --git" line at all) never sets sawGitHeader, so its "+++ " line
// resets normally — what a headerless multi-file diff needs, since
// otherwise a second file's first hunk would be judged against the first
// file's end position. "+++ " also requires sawDashHeader: git always
// emits "--- " immediately before "+++ ", so a "+++ " line that was not
// immediately preceded by one is not a real header no matter what path it
// claims — this is what catches a disguised "+++ " that syntactically
// matches a header shape (diffHunkOrderReason's own exhaustion check only
// catches a line that does not even try to look like one). "+++ "
// additionally carries the section's display path, preferring the
// pre-image path already on the tracker over "/dev/null" (a deleted file's
// post-image) so the rejection names a real file.
func (t *diffHunkTracker) observe(line string) string {
	switch {
	case strings.HasPrefix(line, "diff --git "):
		t.path, t.oldEnd, t.newEnd, t.sawGitHeader, t.sawDashHeader = strings.TrimPrefix(line, "diff --git "), 0, 0, true, false
	case strings.HasPrefix(line, "diff --cc "):
		t.path, t.oldEnd, t.newEnd, t.sawGitHeader, t.sawDashHeader = strings.TrimPrefix(line, "diff --cc "), 0, 0, true, false
	case strings.HasPrefix(line, "--- "):
		t.sawDashHeader = true
	case strings.HasPrefix(line, "+++ "):
		if !t.sawDashHeader {
			return fmt.Sprintf("%s: %q is a file header not immediately preceded by \"--- \"; git always emits the pair together, so this diff was not produced by git", t.path, line)
		}
		t.sawDashHeader = false
		if !t.sawGitHeader {
			t.oldEnd, t.newEnd = 0, 0
		}
		if path := strings.TrimSpace(strings.TrimPrefix(line, "+++ ")); path != "/dev/null" {
			t.path = path
		}
	case strings.HasPrefix(line, "@@ "):
		t.sawDashHeader = false
		return t.observeHunk(line)
	default:
		t.sawDashHeader = false
	}
	return ""
}

// observeHunk parses one "@@ " hunk header's old/new ranges (an omitted
// length is 1, as the unified-diff format defines it) and reports a
// rejection reason when the old or new start falls at or before the
// tracker's current end — see diffHunkOrderReason's doc comment for why
// that means git could not have produced this diff. A header the regex
// cannot parse (e.g. a combined diff's "@@@" form) is left unjudged.
func (t *diffHunkTracker) observeHunk(line string) string {
	m := hunkHeaderRe.FindStringSubmatch(line)
	if m == nil {
		return ""
	}
	parseRange := func(start, length string) (int, int) {
		s, _ := strconv.Atoi(start)
		if length == "" {
			return s, 1
		}
		n, _ := strconv.Atoi(length)
		return s, n
	}
	oldStart, oldLen := parseRange(m[1], m[2])
	newStart, newLen := parseRange(m[3], m[4])
	if oldStart < t.oldEnd || newStart < t.newEnd {
		return fmt.Sprintf("%s: hunk %q starts before the previous hunk ends; git emits a file's hunks in ascending order, so this diff was not produced by git", t.path, line)
	}
	t.oldEnd, t.newEnd = oldStart+oldLen, newStart+newLen
	t.oldRemaining, t.newRemaining = oldLen, newLen
	t.hunkLine = line
	return ""
}
