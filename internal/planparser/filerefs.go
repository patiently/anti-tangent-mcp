package planparser

import (
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
	// as line ranges. A single-group pattern stripped only the trailing
	// `:12` from `x.go:57:12`, leaving the phantom path `x.go:57` — which
	// stats as missing and never string-matches its unanchored `Create:`
	// twin, i.e. the exact bug this regex exists to prevent, just one colon
	// deeper. Repeating cannot widen the match past the shapes above: every
	// repetition must still be `:digits`, so `C:\x` and `https://…` are
	// untouched.
	lineAnchorRe = regexp.MustCompile(`(?::\d+(?:[-,]\d+)?)+$`)
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
		path := cleanRefPath(m[2])
		if path == "" {
			continue
		}
		for _, verb := range strings.Split(strings.ToLower(m[1]), "/") {
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
	return refs
}

// cleanRefPath takes the path out of a bullet's tail: backtick-quoted when
// present, else the first whitespace-delimited token, with any trailing
// parenthetical annotation and any trailing line anchor removed.
func cleanRefPath(tail string) string {
	tail = strings.TrimSpace(trailingParenRe.ReplaceAllString(strings.TrimSpace(tail), ""))
	if i := strings.Index(tail, "`"); i >= 0 {
		rest := tail[i+1:]
		if j := strings.Index(rest, "`"); j >= 0 {
			return stripLineAnchor(strings.TrimSpace(rest[:j]))
		}
		// Unterminated backtick: use the part after the opening backtick
		tail = rest
	}
	fields := strings.Fields(tail)
	if len(fields) == 0 {
		return ""
	}
	return stripLineAnchor(strings.TrimSpace(fields[0]))
}

// stripLineAnchor removes a trailing ":N", ":N-M", ":N,M", or repeated
// (":N:C", ":N-M:C") line anchor (see lineAnchorRe). A reference that is
// NOTHING but an anchor collapses to
// the empty string, which FileRefs already skips — an anchor with no path in
// front of it names no file.
func stripLineAnchor(p string) string {
	return lineAnchorRe.ReplaceAllString(p, "")
}
