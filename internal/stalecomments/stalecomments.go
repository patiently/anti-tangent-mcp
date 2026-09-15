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

// ParseDiff splits a unified diff into file sections. Inside a hunk, lines are
// classified by the counts in the hunk header, so a removed line whose body
// starts with -- or ++ is not mistaken for a file header. Text with no hunk
// header yields sections with no lines.
func ParseDiff(diff string) []File {
	var files []File
	var cur *File
	oldLeft, newLeft, newLine := 0, 0, 0
	start := func() {
		files = append(files, File{})
		cur = &files[len(files)-1]
	}
	for _, raw := range strings.Split(diff, "\n") {
		line := strings.TrimSuffix(raw, "\r")
		if oldLeft > 0 || newLeft > 0 {
			switch {
			case strings.HasPrefix(line, "-"):
				cur.Removed = append(cur.Removed, line[1:])
				oldLeft--
				continue
			case strings.HasPrefix(line, "+"):
				cur.Added = append(cur.Added, line[1:])
				cur.Post = append(cur.Post, Line{Number: newLine, Text: line[1:]})
				newLine++
				newLeft--
				continue
			case strings.HasPrefix(line, " "), line == "":
				cur.Post = append(cur.Post, Line{Number: newLine, Text: strings.TrimPrefix(line, " ")})
				newLine++
				oldLeft--
				newLeft--
				continue
			case strings.HasPrefix(line, `\`):
				continue
			}
			oldLeft, newLeft = 0, 0
		}
		switch {
		case strings.HasPrefix(line, "diff --git "):
			start()
		case strings.HasPrefix(line, "--- "):
			if cur == nil || len(cur.Removed)+len(cur.Post) > 0 {
				start()
			}
		case strings.HasPrefix(line, "+++ "):
			if cur == nil {
				start()
			}
			cur.Path = headerPath(strings.TrimPrefix(line, "+++ "))
		default:
			m := hunkHeader.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			if cur == nil {
				start()
			}
			oldLeft, newLeft = hunkCount(m[1]), hunkCount(m[3])
			newLine, _ = strconv.Atoi(m[2])
		}
	}
	return files
}

// hunkCount reads an optional hunk-header count, which defaults to 1.
func hunkCount(s string) int {
	if s == "" {
		return 1
	}
	n, _ := strconv.Atoi(s)
	return n
}

// headerPath is the path a +++ header names, without a trailing timestamp,
// surrounding quotes or the b/ prefix; /dev/null names no file.
func headerPath(name string) string {
	if tab := strings.IndexByte(name, '\t'); tab >= 0 {
		name = name[:tab]
	}
	if len(name) >= 2 && strings.HasPrefix(name, `"`) && strings.HasSuffix(name, `"`) {
		name = name[1 : len(name)-1]
	}
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
		for keyword == "case" {
			more := moreCaseLabels.FindStringSubmatchIndex(rest)
			if more == nil {
				break
			}
			names = append(names, lastSegment(rest[more[2]:more[3]]))
			rest = rest[more[1]:]
		}
	}
}

func lastSegment(dotted string) string {
	return dotted[strings.LastIndexByte(dotted, '.')+1:]
}

// RemovedNames lists the names declared on the diff's removed code lines that
// no added code line declares again, in order of first appearance, skipping
// names shorter than MinNameRunes and stopping at MaxNames.
func RemovedNames(files []File) []string {
	redeclared := map[string]bool{}
	for _, f := range files {
		for _, text := range f.Added {
			if IsComment(text) {
				continue
			}
			for _, n := range declaredNames(text) {
				redeclared[n] = true
			}
		}
	}
	seen := map[string]bool{}
	var names []string
	for _, f := range files {
		for _, text := range f.Removed {
			if IsComment(text) {
				continue
			}
			for _, n := range declaredNames(text) {
				if utf8.RuneCountInString(n) < MinNameRunes || redeclared[n] || seen[n] {
					continue
				}
				seen[n] = true
				names = append(names, n)
				if len(names) == MaxNames {
					return names
				}
			}
		}
	}
	return names
}

// Scan reports the comment lines in sources that contain one of names as a
// whole identifier, in source and line order, each path and line once, and
// stops at MaxHits.
func Scan(names []string, sources []Source) []Hit {
	if len(names) == 0 {
		return nil
	}
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = regexp.QuoteMeta(n)
	}
	matcher := regexp.MustCompile(`(?:^|[^A-Za-z0-9_])(` + strings.Join(quoted, "|") + `)(?:[^A-Za-z0-9_]|$)`)
	seen := map[string]bool{}
	var hits []Hit
	for _, src := range sources {
		for _, l := range src.Lines {
			if !IsComment(l.Text) {
				continue
			}
			m := matcher.FindStringSubmatch(l.Text)
			if m == nil {
				continue
			}
			key := src.Path + "\x00" + strconv.Itoa(l.Number)
			if seen[key] {
				continue
			}
			seen[key] = true
			hits = append(hits, Hit{Path: src.Path, Line: l.Number, Name: m[1], Text: clip(strings.TrimSpace(l.Text))})
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
