package planparser

import (
	"path"
	"regexp"
	"strings"
)

// TaskFileRefs is one task's declared file operations, taken from the
// **Files:** bullet list. The json:metadata fence's "files" array is a FLAT
// list with no Create/Modify distinction, so it cannot drive an order-aware
// consistency check — the bullets are the only source of the verb.
type TaskFileRefs struct {
	Create []string
	Modify []string
	Delete []string
}

var (
	// filesHeadingRe matches the **Files:** section heading, case-insensitively.
	filesHeadingRe = regexp.MustCompile(`(?i)^\s*\*\*files:\*\*\s*$`)
	// fileBulletRe matches "- Create: path", "* modify: `path`",
	// "- Create/Modify: path". The verb group may name two verbs joined by
	// "/", which the plan template uses for a file that is created by one
	// task and edited by another.
	fileBulletRe = regexp.MustCompile(`(?i)^\s*[-*]\s*((?:create|modify|delete)(?:/(?:create|modify|delete))*)\s*:\s*(.+)$`)
	// trailingParenRe strips a trailing "(lines 10-20)"-style annotation.
	trailingParenRe = regexp.MustCompile(`\s*\([^)]*\)\s*$`)
	// lineAnchorRe strips a trailing line anchor — ":57", ":57-70", ":57,70"
	// — from a path reference. Plans routinely anchor a Modify: bullet to
	// the lines being edited (superpowers' task-format reference asks for
	// "line ranges for modifications", and this repo's own plans use it),
	// and the anchored string is not a path: statting `parser.go:57-70`
	// reports a file that "does not exist" when parser.go plainly does, and
	// the anchored form never string-matches its unanchored Create: twin.
	//
	// Anchored to a trailing `:digits` group only, which is what keeps the
	// two shapes that must survive intact: a Windows drive letter (`C:\x`)
	// has no digits after the colon and is not at the end of the string,
	// and a URL scheme (`https://…`) is followed by slashes, not digits.
	//
	// The group REPEATS (`(?:…)+$`) because editors emit line:column as well
	// as line ranges. Stripping only the last group would leave `x.go:57` of
	// `x.go:57:12` — a phantom path that stats as missing and never
	// string-matches its unanchored `Create:` twin, the same failure one
	// colon deeper. Repeating cannot widen the match past the shapes above:
	// every repetition must still be `:digits`, so `C:\x` and `https://…`
	// are untouched.
	//
	// An anchor is a comma-separated LIST of lines-or-ranges, not a single
	// line or a single pair: "a.md:60,166,174,419" and "a.md:27-30,40-50"
	// are both ordinary plan bullets, written with or without a space after
	// each comma, and a list may end in ", …" or ", ..." that elides the
	// rest. Any list shape the pattern does not accept leaves the digits
	// attached to the path, which the disk tier stats verbatim and reports as
	// a file that does not exist.
	lineAnchorRe = regexp.MustCompile(`(?::\d+(?:-\d+)?(?:,\s*\d+(?:-\d+)?)*(?:,\s*(?:…|\.\.\.))?)+$`)
	// pathListSepRe matches what may stand between two backticked paths of
	// one bullet's list. Anything else after a span — a dash, a verb, an
	// opening parenthesis — starts prose, whose code spans (`Foo`,
	// `## Configure`) are not paths.
	pathListSepRe = regexp.MustCompile(`^(?:[\s,;&+]|\band\b)*$`)
	// anchorContinuationRe matches an unquoted comma piece that only carries
	// more lines of the previous path's anchor: the " 29" of "a.go:6-22, 29".
	anchorContinuationRe = regexp.MustCompile(`^(?:\d+(?:-\d+)?|…|\.\.\.)$`)
)

// FileRefs extracts a task body's declared file operations.
//
// Collection starts at the **Files:** heading and stops at the first line
// that is neither a bullet nor blank — so a later "**Steps:**" section whose
// bullets happen to read like file operations is not harvested. A body with
// no **Files:** section yields three empty lists and is not a finding
// anywhere: the consistency check guards plans that opt into the structure,
// it does not demand that they do.
func FileRefs(body string) TaskFileRefs {
	var refs TaskFileRefs
	inSection := false
	for _, line := range strings.Split(body, "\n") {
		if !inSection {
			if filesHeadingRe.MatchString(line) {
				inSection = true
			}
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		m := fileBulletRe.FindStringSubmatch(line)
		if m == nil {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "- ") ||
				strings.HasPrefix(trimmed, "* ") {
				// A bullet with an unrecognized verb (e.g. "- Test: ...") — skip it
				// but stay in the section. Bullets must have a space after the marker
				// to distinguish them from markdown formatting like **section:**.
				continue
			}
			break
		}
		for _, path := range refPaths(m[2]) {
			applyVerbs(&refs, m[1], path)
		}
	}
	return refs
}

// applyVerbs appends path to refs under each verb verbGroup names, so a
// "Create/Modify:" bullet puts the same path in both lists.
func applyVerbs(refs *TaskFileRefs, verbGroup, path string) {
	for _, verb := range strings.Split(strings.ToLower(verbGroup), "/") {
		switch verb {
		case "create":
			refs.Create = append(refs.Create, path)
		case "modify":
			refs.Modify = append(refs.Modify, path)
		case "delete":
			refs.Delete = append(refs.Delete, path)
		}
	}
}

// refPaths takes the paths out of a bullet's tail, after removing a trailing
// parenthetical annotation: the backticked list when the tail has a backtick,
// else the comma-separated unquoted list.
func refPaths(tail string) []string {
	tail = strings.TrimSpace(trailingParenRe.ReplaceAllString(strings.TrimSpace(tail), ""))
	if i := strings.Index(tail, "`"); i >= 0 {
		return backtickPaths(tail[i+1:])
	}
	return barePaths(tail)
}

// backtickPaths reads the spans of a backticked list, rest starting just after
// the first opening backtick. The list ends at the first span followed by
// anything pathListSepRe does not accept. An unterminated span contributes its
// first word.
func backtickPaths(rest string) []string {
	var l refPathList
	for {
		j := strings.Index(rest, "`")
		if j < 0 {
			if fields := strings.Fields(rest); len(fields) > 0 {
				l.add(fields[0])
			}
			return l.paths
		}
		l.add(rest[:j])
		rest = rest[j+1:]
		k := strings.Index(rest, "`")
		if k < 0 || !pathListSepRe.MatchString(rest[:k]) {
			return l.paths
		}
		rest = rest[k+1:]
	}
}

// barePaths reads an unquoted list. The first piece contributes its first
// word, and ends the list if prose follows that word; a later piece counts
// only as a single word, so "a.go — edit it, then b.go" is not read as a
// path named "then". An unquoted brace pattern such as "{pre,post}.tmpl" gets
// cut apart by the same commas that separate list items, so a piece that
// opens a "{" without closing it starts a brace group that runs through the
// piece that closes it; the whole group is dropped as one pattern rather than
// read as two path-shaped fragments.
func barePaths(tail string) []string {
	var l refPathList
	pieces := strings.Split(tail, ",")
	for i := 0; i < len(pieces); i++ {
		fields := strings.Fields(pieces[i])
		if i == 0 {
			if len(fields) == 0 {
				return nil
			}
			if next, ok := l.tryBraceGroup(pieces, i, fields); ok {
				i = next
				continue
			}
			l.add(fields[0])
			if len(fields) > 1 {
				return l.paths
			}
			continue
		}
		next, stop := l.addLaterBarePiece(pieces, i, fields)
		if stop {
			return l.paths
		}
		i = next
	}
	return l.paths
}

// addLaterBarePiece handles one comma piece after the bullet's first: it
// must be a single word — an anchor continuation, the start of a brace
// group, or a path — or the rest of the bullet is prose and the list ends.
// It reports the index of the last piece it consumed (beyond i itself for a
// brace group) and whether the caller must stop scanning.
func (l *refPathList) addLaterBarePiece(pieces []string, i int, fields []string) (int, bool) {
	if len(fields) != 1 {
		return i, true
	}
	if anchorContinuationRe.MatchString(fields[0]) {
		return i, false
	}
	if next, ok := l.tryBraceGroup(pieces, i, fields); ok {
		return next, false
	}
	l.add(fields[0])
	return i, false
}

// tryBraceGroup reports whether fields is exactly the single word that opens
// an unquoted brace group at piece i and, if so, drops the whole group and
// returns the index of the last piece it consumed.
func (l *refPathList) tryBraceGroup(pieces []string, i int, fields []string) (int, bool) {
	if len(fields) != 1 || !hasUnclosedBrace(fields[0]) {
		return i, false
	}
	return l.dropBraceGroup(pieces, i), true
}

// hasUnclosedBrace reports whether s opens a "{" that it does not also
// close — the shape strings.Split(tail, ",") leaves a piece in once it has
// cut an unquoted brace pattern apart at the pattern's own commas.
func hasUnclosedBrace(s string) bool {
	return strings.Contains(s, "{") && !strings.Contains(s, "}")
}

// refPathList collects one bullet's paths, cleaned by stripLineAnchor and
// canonRefPath. It drops these shapes rather than let them through as a file
// that does not exist or was never a file name at all:
//
//   - a pattern (`*`, `?`, `{`), which names a set of files, not one;
//   - a slash-free path after a path with a directory, which plans write as
//     shorthand for a sibling of that path (`testdata/a.golden`,
//     `b.golden`); a bare name could as well be a root file, and the text
//     cannot tell which, so it is not checked at all;
//   - a candidate with whitespace or a `(`/`)` inside it, or the word `etc`
//     or `etc.` — prose, not a path;
//   - a candidate after the bullet's first path that has neither `/` nor
//     `.` — a bare word such as `Run` or `twice` is not a file name, while
//     `go.sum` after `go.mod` keeps working because it has a `.`;
//   - a repeat, which would report the same file twice.
//
// hasDir counts dropped paths too: a shorthand after a directory pattern is
// still a sibling of it. "First path" is tracked the same way — a candidate
// add() dropped still counts as the bullet's first, so a dropped symbol
// before the real path does not let the next bare word through.
type refPathList struct {
	paths   []string
	hasDir  bool
	started bool
	// seen mirrors paths as a set so skip's duplicate check stays O(1) per
	// candidate. A single bullet's comma-separated list can hold many paths
	// within the plan's 1 MB cap, and scanning paths with slices.Contains for
	// every candidate would make that bullet's cost quadratic.
	seen map[string]bool
}

func (l *refPathList) add(raw string) {
	first := !l.started
	l.started = true
	p := canonRefPath(stripLineAnchor(strings.TrimSpace(raw)))
	hasDir := strings.Contains(p, "/")
	sibling := l.hasDir && !hasDir
	l.hasDir = l.hasDir || hasDir
	if l.skip(p, sibling, first) {
		return
	}
	if l.seen == nil {
		l.seen = make(map[string]bool)
	}
	l.seen[p] = true
	l.paths = append(l.paths, p)
}

// skip reports whether p must be dropped rather than added: empty, a sibling
// shorthand, a pattern, a symbol or stray word rather than a file name, or
// already present.
func (l *refPathList) skip(p string, sibling, first bool) bool {
	if p == "" || sibling {
		return true
	}
	if isProseWord(p) {
		return true
	}
	if !first && !strings.ContainsAny(p, "/.") {
		return true
	}
	return strings.ContainsAny(p, "*?{") || l.seen[p]
}

// dropBraceGroup consumes pieces[start:] through the piece that closes the
// brace pattern opened at pieces[start] — or through the end of pieces, if it
// is never closed — and returns the index of the last piece consumed, so the
// caller's loop resumes just after it.
//
// The rejoined group is passed to add() like any other candidate, so it goes
// through the same hasDir bookkeeping as a dropped pattern: a bare word later
// on the same bullet, after a brace group whose text names a directory, is
// read as a sibling shorthand of it and dropped too, the same as it would be
// after a dropped glob.
func (l *refPathList) dropBraceGroup(pieces []string, start int) int {
	end := start
	for end < len(pieces)-1 && !strings.Contains(pieces[end], "}") {
		end++
	}
	l.add(strings.Join(pieces[start:end+1], ","))
	return end
}

// isProseWord reports whether p reads as prose rather than a file name: it
// has whitespace or a `(`/`)` inside it, or is the word `etc`/`etc.`.
func isProseWord(p string) bool {
	if len(strings.Fields(p)) != 1 || strings.ContainsAny(p, "()") {
		return true
	}
	return p == "etc" || p == "etc."
}

// stripLineAnchor removes a trailing ":N", ":N-M", ":N,M", ":N, M", or
// repeated (":N:C", ":N-M:C") line anchor (see lineAnchorRe). A reference
// that is NOTHING but an anchor collapses to the empty string, which
// refPathList drops — an anchor with no path in front of it names no
// file.
func stripLineAnchor(p string) string {
	return lineAnchorRe.ReplaceAllString(p, "")
}

// canonRefPath collapses a repo-relative plan path to ONE identity, so that
// "Modify: ./a.go" in one task and "Create: a.go" in another are the same
// key in the order-aware check rather than two unrelated files - a miss that
// is invisible when repo_root is absent, because the disk tier that would
// otherwise catch it never runs.
//
// path.Clean, not filepath.Clean: plan bullets are markdown and always
// slash-separated, and filepath.Clean would rewrite them with backslashes on
// Windows, splitting the identity by GOOS instead of unifying it.
// "../x" is preserved as-is and resolveUnderRoot still refuses it.
//
// Only genuine repo-relative paths are touched. A URL is left alone because
// path.Clean collapses its "//" into "/" - "https://example.com/a.go" comes
// back "https:/example.com/a.go", corrupting a value the parser is
// documented to pass through (TestFileRefs_LineAnchorStripLeavesOtherColonsAlone
// pins it). Absolute paths are left alone too: resolveUnderRoot refuses them
// outright, so there is nothing to unify and no reason to rewrite them.
func canonRefPath(p string) string {
	if p == "" || path.IsAbs(p) || strings.Contains(p, "://") {
		return p
	}
	return path.Clean(p)
}
