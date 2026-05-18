package planparser

import (
	"strings"
)

const (
	normativeHeader               = "**NORMATIVE TEST BODIES (verbatim):**"
	normativeMaxEntries           = 20
	normativeMaxChars             = 4000
	normativeTruncationMarker     = "\n// truncated"
	normativeTruncationMarkerRuns = 13 // len("\n// truncated") in runes (ASCII)
)

// ExtractNormativeTestBodies scans body for the literal NORMATIVE TEST BODIES
// header line and returns the immediately-following fenced code blocks (one
// entry per block) in source order. If the header is followed by non-fenced
// text, the paragraph up to the next blank line is returned as a single
// entry.
//
// The header must appear on its own line (matched after CRLF normalization),
// not embedded within other text. Whitespace-only separator lines between
// adjacent fenced blocks are skipped.
//
// Per-entry truncation: entries longer than normativeMaxChars runes are
// truncated to (normativeMaxChars - normativeTruncationMarkerRuns) runes and
// the literal "\n// truncated" marker is appended. Entry-count cap: at most
// normativeMaxEntries entries; later entries are dropped silently.
//
// Returns nil when no header is present, when the header is at the very end
// of the body with nothing following, when only whitespace follows the
// header, or when body is empty.
func ExtractNormativeTestBodies(body string) []string {
	if body == "" {
		return nil
	}
	rest, found := findHeaderRemainder(body)
	if !found {
		return nil
	}
	rest = trimLeadingBlankLines(rest)
	if rest == "" {
		return nil
	}

	// firstIsFenced records whether the first entry was a fenced block.
	// After at least one fenced entry, only adjacent fenced blocks chain;
	// any non-fenced content stops extraction (no paragraph fallback applies
	// after a fence). Paragraph fallback is allowed only as the very first
	// entry — and in that case it yields exactly one entry.
	var out []string
	sawFenced := false
	for len(out) < normativeMaxEntries {
		rest = trimLeadingBlankLines(rest)
		if rest == "" {
			break
		}
		// After the first fenced entry, refuse to fall back to paragraph
		// parsing: only adjacent fenced blocks chain.
		if sawFenced && !startsWithFenceOpener(rest) {
			break
		}
		entry, remainder, isFenced, ok := readNormativeEntry(rest)
		if !ok {
			break
		}
		out = append(out, capNormativeEntry(entry))
		rest = remainder
		if isFenced {
			sawFenced = true
			continue
		}
		// Paragraph fallback yields exactly one entry then stops.
		break
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// findHeaderRemainder locates the first line whose content (after stripping a
// trailing \r) equals the normative header verbatim, and returns the body
// that follows that line. Returns ("", false) if no such line exists.
func findHeaderRemainder(body string) (string, bool) {
	// Treat string start and every '\n' as a line boundary. A header line is
	// one whose content (excluding the terminating \n and a trailing \r) is
	// exactly the header text.
	pos := 0
	for pos <= len(body) {
		// Identify the next line's [start,end) within body.
		start := pos
		end := strings.IndexByte(body[start:], '\n')
		var lineEnd, nextStart int
		if end < 0 {
			lineEnd = len(body)
			nextStart = len(body)
		} else {
			lineEnd = start + end
			nextStart = lineEnd + 1
		}
		line := body[start:lineEnd]
		// Tolerate CRLF.
		line = strings.TrimSuffix(line, "\r")
		if line == normativeHeader {
			return body[nextStart:], true
		}
		if end < 0 {
			break
		}
		pos = nextStart
	}
	return "", false
}

// trimLeadingBlankLines removes leading lines that are empty or contain only
// whitespace, so that whitespace-only separators between adjacent fenced
// blocks don't fall through into paragraph parsing.
func trimLeadingBlankLines(s string) string {
	for len(s) > 0 {
		nl := strings.IndexByte(s, '\n')
		var line string
		var rest string
		if nl < 0 {
			line = s
			rest = ""
		} else {
			line = s[:nl]
			rest = s[nl+1:]
		}
		if strings.TrimSpace(line) != "" {
			return s
		}
		if nl < 0 {
			return ""
		}
		s = rest
	}
	return s
}

// readNormativeEntry reads one entry from the head of `rest`. If `rest` begins
// with a fenced code block (``` optionally followed by a language tag on its
// own line), it returns the inner text up to the closing fence and
// isFenced=true. Otherwise it returns the paragraph up to the next blank line
// and isFenced=false. Returns ok=false when no entry could be read (e.g. an
// unterminated fence with no closing line, or empty paragraph content).
func readNormativeEntry(rest string) (entry, remainder string, isFenced, ok bool) {
	nl := strings.IndexByte(rest, '\n')
	if nl < 0 {
		// Single trailing line (no terminating newline). Treat as paragraph.
		trimmed := strings.TrimSpace(rest)
		if trimmed == "" {
			return "", "", false, false
		}
		return trimmed, "", false, true
	}
	firstLine := strings.TrimRight(rest[:nl], "\r")
	if isFenceOpener(firstLine) {
		// Find closing fence: a line whose content (after CRLF normalization
		// and trimming trailing spaces/tabs) is exactly ```.
		inner := rest[nl+1:]
		closeRel, closeLineLen, found := findClosingFence(inner)
		if !found {
			// Unterminated fence — bail rather than swallowing the rest of
			// the body. Plans must close their fences.
			return "", "", false, false
		}
		entry = strings.TrimRight(inner[:closeRel], "\n")
		closeEnd := closeRel + closeLineLen
		if closeEnd < len(inner) && inner[closeEnd] == '\n' {
			closeEnd++
		}
		return entry, inner[closeEnd:], true, true
	}

	// Paragraph case: collect consecutive non-blank lines (line-based, so a
	// whitespace-only or CRLF-only blank line terminates the paragraph).
	para, remainder, hasContent := readParagraphLines(rest)
	if !hasContent {
		return "", "", false, false
	}
	return para, remainder, false, true
}

// readParagraphLines scans s line-by-line, accumulating non-blank lines into
// the paragraph until it hits a blank line (one whose TrimSpace is empty) or
// the end of input. The blank-line terminator is consumed; subsequent text is
// returned as remainder. hasContent is false if no non-blank line was found.
func readParagraphLines(s string) (para, remainder string, hasContent bool) {
	var lines []string
	pos := 0
	for pos <= len(s) {
		start := pos
		end := strings.IndexByte(s[start:], '\n')
		var lineEnd, nextStart int
		if end < 0 {
			lineEnd = len(s)
			nextStart = len(s)
		} else {
			lineEnd = start + end
			nextStart = lineEnd + 1
		}
		line := strings.TrimSuffix(s[start:lineEnd], "\r")
		if strings.TrimSpace(line) == "" {
			// Blank line terminates the paragraph. Consume the blank line
			// (and only the blank line) — leave subsequent content for the
			// caller. trimLeadingBlankLines on the next loop iteration will
			// skip any additional blank lines.
			if len(lines) == 0 {
				// No content yet; treat as no-entry. Caller will see ok=false.
				return "", "", false
			}
			return strings.Join(lines, "\n"), s[nextStart:], true
		}
		lines = append(lines, line)
		if end < 0 {
			break
		}
		pos = nextStart
	}
	if len(lines) == 0 {
		return "", "", false
	}
	return strings.Join(lines, "\n"), "", true
}

// isFenceOpener reports whether line is a fenced-code-block opener: a line
// that begins with ``` (after the caller has stripped any trailing \r),
// optionally followed by a language tag. Leading whitespace is not allowed.
func isFenceOpener(line string) bool {
	return strings.HasPrefix(line, "```")
}

// startsWithFenceOpener reports whether the first line of s (after CRLF
// normalization) is a fenced-code-block opener. Does not skip leading blank
// lines — the caller is responsible for trimming.
func startsWithFenceOpener(s string) bool {
	nl := strings.IndexByte(s, '\n')
	var firstLine string
	if nl < 0 {
		firstLine = s
	} else {
		firstLine = s[:nl]
	}
	return isFenceOpener(strings.TrimRight(firstLine, "\r"))
}

// findClosingFence locates the next line in s that is exactly ``` (the
// closing fence), after CRLF normalization and trimming trailing horizontal
// whitespace. Returns the byte offset of the line start, the length of the
// closing-line content (excluding the terminating \n), and found=true.
func findClosingFence(s string) (offset, lineLen int, found bool) {
	pos := 0
	for pos < len(s) {
		nl := strings.IndexByte(s[pos:], '\n')
		var line string
		var lineEnd int
		if nl < 0 {
			line = s[pos:]
			lineEnd = len(s)
		} else {
			line = s[pos : pos+nl]
			lineEnd = pos + nl
		}
		normalized := strings.TrimRight(strings.TrimSuffix(line, "\r"), " \t")
		if normalized == "```" {
			return pos, lineEnd - pos, true
		}
		if nl < 0 {
			break
		}
		pos = lineEnd + 1
	}
	return 0, 0, false
}

// capNormativeEntry truncates entry to normativeMaxChars runes if necessary,
// appending the truncation marker so the reviewer can see the body was clipped.
func capNormativeEntry(entry string) string {
	runes := []rune(entry)
	if len(runes) <= normativeMaxChars {
		return entry
	}
	cut := normativeMaxChars - normativeTruncationMarkerRuns
	return string(runes[:cut]) + normativeTruncationMarker
}
