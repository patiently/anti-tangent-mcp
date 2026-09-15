// Package stalecomments finds comment lines that still name a symbol a unified
// diff removes. The match is lexical: it reads no files and never decides
// whether a comment is stale, which is left to the reviewer the hits are shown
// to.
package stalecomments

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	// MaxNames caps how many removed names one diff contributes.
	MaxNames = 30
	// MaxHits caps how many comment lines one scan reports.
	MaxHits = 20
	// MaxLineRunes caps the quoted text of one hit.
	MaxLineRunes = 200
	// MinNameRunes is the shortest removed name kept. Shorter names match too
	// many unrelated words in prose.
	MinNameRunes = 4
)

// Line is one line of post-change content with its 1-based line number.
type Line struct {
	Number int
	Text   string
}

// File is one file section of a unified diff.
type File struct {
	// Path is the post-change path from the +++ header, without its b/
	// prefix, and empty when the file is deleted.
	Path string
	// Removed and Added are the bodies of the section's - and + lines.
	Removed []string
	Added   []string
	// Post is every context and added line, numbered in the post-change file.
	Post []Line
}

// Source is post-change content to scan for comments.
type Source struct {
	Path  string
	Lines []Line
}

// Hit is a comment line that contains a removed name.
type Hit struct {
	Path string
	Line int
	Name string
	Text string
}

func (h Hit) String() string {
	return fmt.Sprintf("%s:%d: %s", h.Path, h.Line, h.Text)
}

var hunkHeader = regexp.MustCompile(`^@@ -\d+(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

// diffParser is the mutable state used to parse a unified diff.
type diffParser struct {
	files                     []File
	cur                       *File
	oldLeft, newLeft, newLine int
}

func (p *diffParser) start() {
	p.files = append(p.files, File{})
	p.cur = &p.files[len(p.files)-1]
}

func (p *diffParser) inHunk() bool {
	return p.oldLeft > 0 || p.newLeft > 0
}

// hunkLine processes a line that is known to be in a hunk. Returns false if
// the line is not a hunk body line (e.g., context marker but out of sync).
func (p *diffParser) hunkLine(line string) bool {
	switch {
	case strings.HasPrefix(line, "-"):
		p.cur.Removed = append(p.cur.Removed, line[1:])
		p.oldLeft--
		return true
	case strings.HasPrefix(line, "+"):
		p.cur.Added = append(p.cur.Added, line[1:])
		p.cur.Post = append(p.cur.Post, Line{Number: p.newLine, Text: line[1:]})
		p.newLine++
		p.newLeft--
		return true
	case strings.HasPrefix(line, " "), line == "":
		p.cur.Post = append(p.cur.Post, Line{Number: p.newLine, Text: strings.TrimPrefix(line, " ")})
		p.newLine++
		p.oldLeft--
		p.newLeft--
		return true
	case strings.HasPrefix(line, `\`):
		return true
	}
	return false
}

// fileHeader processes a file-level header line (diff, ---, +++). Reports
// whether the line was handled.
func (p *diffParser) fileHeader(line string) bool {
	switch {
	case strings.HasPrefix(line, "diff --git "):
		p.start()
		return true
	case strings.HasPrefix(line, "--- "):
		if p.cur == nil || len(p.cur.Removed)+len(p.cur.Post) > 0 {
			p.start()
		}
		return true
	case strings.HasPrefix(line, "+++ "):
		if p.cur == nil {
			p.start()
		}
		p.cur.Path = headerPath(strings.TrimPrefix(line, "+++ "))
		return true
	}
	return false
}

// hunkStart processes a hunk header line (@@ -...).
func (p *diffParser) hunkStart(line string) {
	m := hunkHeader.FindStringSubmatch(line)
	if m == nil {
		return
	}
	if p.cur == nil {
		p.start()
	}
	p.oldLeft, p.newLeft = hunkCount(m[1]), hunkCount(m[3])
	p.newLine, _ = strconv.Atoi(m[2])
}

func (p *diffParser) headerLine(line string) {
	if p.fileHeader(line) {
		return
	}
	p.hunkStart(line)
}

// ParseDiff splits a unified diff into file sections. Inside a hunk, lines are
// classified by the counts in the hunk header, so a removed line whose body
// starts with -- or ++ is not mistaken for a file header. Text with no hunk
// header yields sections with no lines.
func ParseDiff(diff string) []File {
	p := &diffParser{}
	for _, raw := range strings.Split(diff, "\n") {
		line := strings.TrimSuffix(raw, "\r")
		if p.inHunk() {
			if !p.hunkLine(line) {
				p.oldLeft, p.newLeft = 0, 0
				p.headerLine(line)
			}
		} else {
			p.headerLine(line)
		}
	}
	return p.files
}

// hunkCount reads an optional hunk-header count, which defaults to 1.
func hunkCount(s string) int {
	if s == "" {
		return 1
	}
	n, _ := strconv.Atoi(s)
	return n
}

// unquote removes one pair of surrounding double quotes, which git adds around
// a path containing spaces or non-ASCII bytes.
func unquote(name string) string {
	inner, ok := strings.CutPrefix(name, `"`)
	if !ok {
		return name
	}
	inner, ok = strings.CutSuffix(inner, `"`)
	if !ok {
		return name
	}
	return inner
}

// headerPath is the path a +++ header names, without a trailing timestamp,
// surrounding quotes or the b/ prefix; /dev/null names no file.
func headerPath(name string) string {
	if tab := strings.IndexByte(name, '\t'); tab >= 0 {
		name = name[:tab]
	}
	name = unquote(name)
	if name == "/dev/null" {
		return ""
	}
	return strings.TrimPrefix(name, "b/")
}

// FileLines numbers every line of content from 1.
func FileLines(content string) []Line {
	content = strings.TrimSuffix(content, "\n")
	if content == "" {
		return nil
	}
	parts := strings.Split(content, "\n")
	lines := make([]Line, len(parts))
	for i, p := range parts {
		lines[i] = Line{Number: i + 1, Text: strings.TrimSuffix(p, "\r")}
	}
	return lines
}

var commentPrefixes = []string{"//", "#", "*", "/*", "<!--", "--"}

// IsComment reports whether a line reads as a comment: after leading
// whitespace it starts with //, #, *, /*, <!-- or --.
func IsComment(text string) bool {
	t := strings.TrimSpace(text)
	for _, p := range commentPrefixes {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return false
}

const identifier = `[A-Za-z_][A-Za-z0-9_]*`

var declKeywords = []string{
	"func", "fun", "fn", "function", "def", "class", "object", "interface", "trait",
	"struct", "enum", "type", "val", "var", "let", "const", "case",
}

var isDeclKeyword = func() map[string]bool {
	m := make(map[string]bool, len(declKeywords))
	for _, k := range declKeywords {
		m[k] = true
	}
	return m
}()

// declaration matches a keyword and the name it declares. The optional groups
// skip a Go receiver, a generic parameter list, a leading dot (a Swift case)
// and a dotted qualifier (a Kotlin extension receiver or a qualified case
// label), so the capture is the declared name with its qualifier.
var declaration = regexp.MustCompile(`\b(` + strings.Join(declKeywords, "|") + `)\s+(?:\([^)]*\)\s*)?(?:<[^>]*>\s*)?\.?((?:` + identifier + `\.)*` + identifier + `)`)

// moreCaseLabels matches one further label of a comma-separated case list.
var moreCaseLabels = regexp.MustCompile(`^\s*,\s*\.?((?:` + identifier + `\.)*` + identifier + `)`)

// caseLabels returns additional case labels from a comma-separated case list
// and the remaining text after them.
func caseLabels(rest string) ([]string, string) {
	var names []string
	for {
		more := moreCaseLabels.FindStringSubmatchIndex(rest)
		if more == nil {
			break
		}
		names = append(names, lastSegment(rest[more[2]:more[3]]))
		rest = rest[more[1]:]
	}
	return names, rest
}

// declaredNames lists the names a code line declares. A name that is itself a
// declaration keyword starts the next match instead, so "enum class Foo"
// declares Foo, and every label of "case A, B:" is declared.
func declaredNames(line string) []string {
	var names []string
	for rest := line; ; {
		m := declaration.FindStringSubmatchIndex(rest)
		if m == nil {
			return names
		}
		keyword, name := rest[m[2]:m[3]], lastSegment(rest[m[4]:m[5]])
		if isDeclKeyword[name] {
			rest = rest[m[4]:]
			continue
		}
		names = append(names, name)
		rest = rest[m[1]:]
		if keyword == "case" {
			moreNames, r := caseLabels(rest)
			names = append(names, moreNames...)
			rest = r
		}
	}
}

func lastSegment(dotted string) string {
	return dotted[strings.LastIndexByte(dotted, '.')+1:]
}

// declaredOnCodeLines returns all names declared by non-comment lines, with
// duplicates.
func declaredOnCodeLines(lines []string) []string {
	var names []string
	for _, text := range lines {
		if IsComment(text) {
			continue
		}
		names = append(names, declaredNames(text)...)
	}
	return names
}

// redeclaredNames builds a set of names declared in added code lines.
func redeclaredNames(files []File) map[string]bool {
	redeclared := map[string]bool{}
	for _, f := range files {
		for _, n := range declaredOnCodeLines(f.Added) {
			redeclared[n] = true
		}
	}
	return redeclared
}

// removedCodeNames returns all names declared in removed code lines, in file
// and line order with duplicates.
func removedCodeNames(files []File) []string {
	var names []string
	for _, f := range files {
		names = append(names, declaredOnCodeLines(f.Removed)...)
	}
	return names
}

// RemovedNames lists the names declared on the diff's removed code lines that
// no added code line declares again, in order of first appearance, skipping
// names shorter than MinNameRunes and stopping at MaxNames.
func RemovedNames(files []File) []string {
	redeclared := redeclaredNames(files)
	removed := removedCodeNames(files)
	seen := map[string]bool{}
	var names []string
	for _, n := range removed {
		if utf8.RuneCountInString(n) < MinNameRunes {
			continue
		}
		if redeclared[n] {
			continue
		}
		if seen[n] {
			continue
		}
		seen[n] = true
		names = append(names, n)
		if len(names) == MaxNames {
			return names
		}
	}
	return names
}

// nameMatcher builds a regex that matches any of the given names as whole
// identifiers.
func nameMatcher(names []string) *regexp.Regexp {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = regexp.QuoteMeta(n)
	}
	return regexp.MustCompile(`(?:^|[^A-Za-z0-9_])(` + strings.Join(quoted, "|") + `)(?:[^A-Za-z0-9_]|$)`)
}

// matchLine checks if a line is a comment that contains a name match and
// returns the matched name, or empty string if no match.
func matchLine(matcher *regexp.Regexp, text string) string {
	if !IsComment(text) {
		return ""
	}
	m := matcher.FindStringSubmatch(text)
	if m == nil {
		return ""
	}
	return m[1]
}

// Scan reports the comment lines in sources that contain one of names as a
// whole identifier, in source and line order, each path and line once, and
// stops at MaxHits.
func Scan(names []string, sources []Source) []Hit {
	if len(names) == 0 {
		return nil
	}
	matcher := nameMatcher(names)
	seen := map[string]bool{}
	var hits []Hit
	for _, src := range sources {
		for _, l := range src.Lines {
			name := matchLine(matcher, l.Text)
			if name == "" {
				continue
			}
			key := src.Path + "\x00" + strconv.Itoa(l.Number)
			if seen[key] {
				continue
			}
			seen[key] = true
			hits = append(hits, Hit{Path: src.Path, Line: l.Number, Name: name, Text: clip(strings.TrimSpace(l.Text))})
			if len(hits) == MaxHits {
				return hits
			}
		}
	}
	return hits
}

func clip(s string) string {
	if utf8.RuneCountInString(s) <= MaxLineRunes {
		return s
	}
	return string([]rune(s)[:MaxLineRunes])
}
