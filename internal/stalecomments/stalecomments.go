// Package stalecomments finds comment lines that still name a symbol a unified
// diff removes. The match is lexical: it reads no files and never decides
// whether a comment is stale, which is left to the reviewer the hits are shown
// to.
package stalecomments

import (
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
	"unicode"
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

// hunkLine processes a line that is known to be in a hunk. Returns false for
// a line that is none of -, +, context, empty or \ — a header or garbage met
// while the hunk's counts are still outstanding.
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
//
// Known limitation: a hunk header whose counts overrun its actual body — only
// possible in a hand-edited or truncated diff, since git always emits counts
// that match — makes the next file's --- / +++ headers get consumed as hunk
// body lines instead, attributing that file's hunk to the previous path.
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

// unquote decodes a git-quoted path. Git does not quote a path merely for a
// space — it leaves the path bare and terminates it with a tab instead — so
// quoting means the path has a non-ASCII byte, which git escapes octal
// (\NNN per byte, the same syntax Go string literals use) inside the
// surrounding double quotes. strconv.Unquote reverses both the quoting and
// the escapes; a name that is not validly quoted is returned unchanged
// rather than dropped.
func unquote(name string) string {
	if len(name) < 2 {
		return name
	}
	if name[0] != '"' || name[len(name)-1] != '"' {
		return name
	}
	if s, err := strconv.Unquote(name); err == nil {
		return s
	}
	return name
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

// localDeclKeywords are the keywords whose declared name is dropped when the
// name has no uppercase letter: "val result", "var value" and "let mut" are
// almost always locals, while "const MAX_RETRIES" and "val StateRetired"
// name symbols worth watching for staleness.
var localDeclKeywords = map[string]bool{"val": true, "var": true, "let": true, "const": true}

// hasUppercase reports whether s contains at least one uppercase letter.
func hasUppercase(s string) bool {
	for _, r := range s {
		if unicode.IsUpper(r) {
			return true
		}
	}
	return false
}

// declaredNames lists the names a code line declares. A name that is itself a
// declaration keyword starts the next match instead, so "enum class Foo"
// declares Foo, and every label of "case A, B:" is declared. A name after
// val/var/let/const with no uppercase letter is dropped as a local.
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
		if !localDeclKeywords[keyword] || hasUppercase(name) {
			names = append(names, name)
		}
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

// quoteRunes are the characters that open a string literal a code line's
// declaration matching must not look inside.
const quoteRunes = `"'` + "`"

// blankQuotedLiterals replaces the interior of every double-, single- and
// backtick-quoted literal in line with spaces, left to right, honoring a
// backslash escape inside the literal. This is a lexical scan, not a parser
// for any one language: it exists only to keep a string's words (for example
// `"unknown object with id %d"`) out of declaredNames, not to validate quoting.
// An unterminated literal blanks to the end of the line.
func blankQuotedLiterals(line string) string {
	b := []byte(line)
	for i := 0; i < len(b); i++ {
		if !strings.ContainsRune(quoteRunes, rune(b[i])) {
			continue
		}
		j := closingQuote(b, i)
		blankRange(b, i, j)
		i = j
	}
	return string(b)
}

// closingQuote finds the byte in b, after i, that closes the quote at b[i],
// honoring a backslash escape. It returns the last index of b when the
// literal is unterminated, so the caller blanks to the end of the line.
func closingQuote(b []byte, i int) int {
	quote := b[i]
	for j := i + 1; j < len(b); j++ {
		if b[j] == '\\' && j+1 < len(b) {
			j++
			continue
		}
		if b[j] == quote {
			return j
		}
	}
	return len(b) - 1
}

// blankRange overwrites b[i:j+1] with spaces, in place.
func blankRange(b []byte, i, j int) {
	for k := i; k <= j; k++ {
		b[k] = ' '
	}
}

// trailingCommentMarkers open a trailing comment on an otherwise-code line: a
// slash-slash or hash line comment, or a spaced SQL-style "--" comment (the
// surrounding spaces keep a decrement or a flag like "--force" mid-line from
// being mistaken for one).
var trailingCommentMarkers = []string{"//", "#", " -- "}

// stripTrailingComment cuts line at the earliest trailing comment marker, so
// prose after the code — "x := 1 // see handleFooBar below" — is not read
// for declared names. The caller blanks quoted literals first, so a marker
// inside a string cannot trigger this.
func stripTrailingComment(line string) string {
	cut := len(line)
	for _, m := range trailingCommentMarkers {
		if i := strings.Index(line, m); i >= 0 && i < cut {
			cut = i
		}
	}
	return line[:cut]
}

// declaredOnCodeLines returns all names declared by non-comment lines, with
// duplicates. Quoted string literals are blanked and a trailing comment is
// cut first, so a literal's words and a trailing remark are never mistaken
// for a declared name.
func declaredOnCodeLines(lines []string) []string {
	var names []string
	for _, text := range lines {
		if IsComment(text) {
			continue
		}
		names = append(names, declaredNames(stripTrailingComment(blankQuotedLiterals(text)))...)
	}
	return names
}

// proseExtensions are file extensions whose content is prose, not code: a
// removed line there yields words like "each" or "handles", never a symbol
// worth watching for staleness.
var proseExtensions = map[string]bool{".md": true, ".markdown": true, ".txt": true, ".rst": true, ".adoc": true}

// isProseFile reports whether path names a prose file by its extension,
// case-insensitively. A deleted file's Path is empty (the diff's +++ header
// names none), and an empty path is never prose: its removed lines are the
// only evidence of what kind of file it was, so they are still scanned.
func isProseFile(filePath string) bool {
	return filePath != "" && proseExtensions[strings.ToLower(path.Ext(filePath))]
}

// redeclaredNames builds a set of names declared in added code lines,
// skipping a prose file's lines: name collection only, never the hit scan.
func redeclaredNames(files []File) map[string]bool {
	redeclared := map[string]bool{}
	for _, f := range files {
		if isProseFile(f.Path) {
			continue
		}
		for _, n := range declaredOnCodeLines(f.Added) {
			redeclared[n] = true
		}
	}
	return redeclared
}

// removedCodeNames returns all names declared in removed code lines, in file
// and line order with duplicates, skipping a prose file's lines: name
// collection only, never the hit scan.
func removedCodeNames(files []File) []string {
	var names []string
	for _, f := range files {
		if isProseFile(f.Path) {
			continue
		}
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
			hits = append(hits, Hit{Path: clip(src.Path), Line: l.Number, Name: name, Text: clip(strings.TrimSpace(l.Text))})
			if len(hits) == MaxHits {
				return hits
			}
		}
	}
	return hits
}

// clip truncates s to MaxLineRunes. Scan applies it to both a hit's path and
// its text, since a +++ header path is arbitrary text and left unclipped
// could make one hit's rendered line grow unbounded.
func clip(s string) string {
	if utf8.RuneCountInString(s) <= MaxLineRunes {
		return s
	}
	return string([]rune(s)[:MaxLineRunes])
}
