package planparser

import (
	"strings"
	"testing"
)

func TestExtractNormativeTestBodies_SimpleFenced(t *testing.T) {
	body := "### Task 1: thing\n\n**Goal:** thing\n\n**NORMATIVE TEST BODIES (verbatim):**\n\n```kotlin\n@Test fun t() { assertThat(x).isEqualTo(y) }\n```\n"
	got := ExtractNormativeTestBodies(body)
	if len(got) != 1 {
		t.Fatalf("want 1 entry, got %d (%v)", len(got), got)
	}
	if got[0] != "@Test fun t() { assertThat(x).isEqualTo(y) }" {
		t.Errorf("entry = %q", got[0])
	}
}

func TestExtractNormativeTestBodies_AdjacentFenced(t *testing.T) {
	body := "**NORMATIVE TEST BODIES (verbatim):**\n\n```kotlin\nbody1\n```\n\n```kotlin\nbody2\n```\n"
	got := ExtractNormativeTestBodies(body)
	if len(got) != 2 {
		t.Fatalf("want 2 entries, got %d (%v)", len(got), got)
	}
	if got[0] != "body1" || got[1] != "body2" {
		t.Errorf("entries = %v", got)
	}
}

func TestExtractNormativeTestBodies_ParagraphFallback(t *testing.T) {
	body := "**NORMATIVE TEST BODIES (verbatim):**\n\ndef test_x(): assert phrase in OUTPUT\n\nnext paragraph is not part of the body.\n"
	got := ExtractNormativeTestBodies(body)
	if len(got) != 1 {
		t.Fatalf("want 1 entry, got %d (%v)", len(got), got)
	}
	if got[0] != "def test_x(): assert phrase in OUTPUT" {
		t.Errorf("entry = %q", got[0])
	}
}

func TestExtractNormativeTestBodies_TruncationMarker(t *testing.T) {
	// 5000-char body must truncate to 3987 chars + "\n// truncated" suffix.
	big := strings.Repeat("x", 5000)
	body := "**NORMATIVE TEST BODIES (verbatim):**\n\n```\n" + big + "\n```\n"
	got := ExtractNormativeTestBodies(body)
	if len(got) != 1 {
		t.Fatalf("want 1 entry, got %d", len(got))
	}
	if !strings.HasSuffix(got[0], "\n// truncated") {
		t.Errorf("entry missing truncation marker; suffix = %q", got[0][len(got[0])-20:])
	}
	if want := 3987 + len("\n// truncated"); len([]rune(got[0])) != want {
		t.Errorf("entry rune count = %d, want %d", len([]rune(got[0])), want)
	}
}

func TestExtractNormativeTestBodies_EntryCountCap(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("**NORMATIVE TEST BODIES (verbatim):**\n\n")
	for i := 0; i < 25; i++ {
		sb.WriteString("```\nbody" + string(rune('a'+i)) + "\n```\n\n")
	}
	got := ExtractNormativeTestBodies(sb.String())
	if len(got) != 20 {
		t.Errorf("want 20 entries (cap), got %d", len(got))
	}
}

func TestExtractNormativeTestBodies_NoHeader(t *testing.T) {
	body := "### Task 1: thing\n\nNo normative bodies here.\n\n```kotlin\nnot extracted\n```\n"
	got := ExtractNormativeTestBodies(body)
	if got != nil {
		t.Errorf("want nil, got %v", got)
	}
}

func TestExtractNormativeTestBodies_HeaderNoContent(t *testing.T) {
	body := "**NORMATIVE TEST BODIES (verbatim):**\n\n"
	got := ExtractNormativeTestBodies(body)
	if got != nil {
		t.Errorf("want nil, got %v", got)
	}
}

func TestExtractNormativeTestBodies_Empty(t *testing.T) {
	if got := ExtractNormativeTestBodies(""); got != nil {
		t.Errorf("want nil, got %v", got)
	}
}

// TestExtractNormativeTestBodies_HeaderMustBeOwnLine guards against treating
// the header as a substring within other prose. Only a stand-alone line
// matching the header text exactly should activate extraction.
func TestExtractNormativeTestBodies_HeaderMustBeOwnLine(t *testing.T) {
	body := "prefix **NORMATIVE TEST BODIES (verbatim):** suffix\n\n```kotlin\nshould not extract\n```\n"
	if got := ExtractNormativeTestBodies(body); got != nil {
		t.Errorf("want nil for header embedded in prose, got %v", got)
	}
}

// TestExtractNormativeTestBodies_HeaderCRLF verifies CRLF line endings are
// tolerated for the header line itself.
func TestExtractNormativeTestBodies_HeaderCRLF(t *testing.T) {
	body := "**NORMATIVE TEST BODIES (verbatim):**\r\n\r\n```\nbody\n```\n"
	got := ExtractNormativeTestBodies(body)
	if len(got) != 1 || got[0] != "body" {
		t.Errorf("want [body], got %v", got)
	}
}

// TestExtractNormativeTestBodies_WhitespaceSeparatorBetweenFences verifies
// that adjacent fenced blocks separated by a whitespace-only line (containing
// spaces or tabs, not just newlines) are still extracted as two entries.
func TestExtractNormativeTestBodies_WhitespaceSeparatorBetweenFences(t *testing.T) {
	body := "**NORMATIVE TEST BODIES (verbatim):**\n\n```\nbody1\n```\n   \n```\nbody2\n```\n"
	got := ExtractNormativeTestBodies(body)
	if len(got) != 2 {
		t.Fatalf("want 2 entries, got %d (%v)", len(got), got)
	}
	if got[0] != "body1" || got[1] != "body2" {
		t.Errorf("entries = %v", got)
	}
}

// TestExtractNormativeTestBodies_HeaderFollowedByWhitespaceOnly guards
// against emitting an empty-string entry when only whitespace lines (no
// fenced or paragraph content) follow the header.
func TestExtractNormativeTestBodies_HeaderFollowedByWhitespaceOnly(t *testing.T) {
	body := "**NORMATIVE TEST BODIES (verbatim):**\n   \n\t\n"
	if got := ExtractNormativeTestBodies(body); got != nil {
		t.Errorf("want nil, got %v", got)
	}
}

// TestExtractNormativeTestBodies_FenceFollowedByProseStopsExtraction
// verifies that after at least one fenced entry, non-fenced content (prose)
// does NOT trigger the paragraph fallback. The AC limits extraction after a
// fence to ADJACENT fenced blocks only.
func TestExtractNormativeTestBodies_FenceFollowedByProseStopsExtraction(t *testing.T) {
	body := "**NORMATIVE TEST BODIES (verbatim):**\n\n```kotlin\nbody1\n```\n\nThis is ordinary prose that follows the fence.\n\nSecond prose paragraph.\n"
	got := ExtractNormativeTestBodies(body)
	if len(got) != 1 {
		t.Fatalf("want 1 entry (only the fenced block), got %d (%v)", len(got), got)
	}
	if got[0] != "body1" {
		t.Errorf("entry = %q", got[0])
	}
}

// TestExtractNormativeTestBodies_ParagraphTerminatedByWhitespaceLine
// verifies the paragraph fallback terminates on a blank line containing
// only whitespace (not strictly a literal \n\n).
func TestExtractNormativeTestBodies_ParagraphTerminatedByWhitespaceLine(t *testing.T) {
	body := "**NORMATIVE TEST BODIES (verbatim):**\n\nparagraph line\n   \nshould not be part of entry\n"
	got := ExtractNormativeTestBodies(body)
	if len(got) != 1 {
		t.Fatalf("want 1 entry, got %d (%v)", len(got), got)
	}
	if got[0] != "paragraph line" {
		t.Errorf("entry = %q (expected paragraph to stop at whitespace-only blank line)", got[0])
	}
}

// TestExtractNormativeTestBodies_ParagraphTerminatedByCRLFBlankLine
// verifies the paragraph fallback handles CRLF blank line terminators.
func TestExtractNormativeTestBodies_ParagraphTerminatedByCRLFBlankLine(t *testing.T) {
	body := "**NORMATIVE TEST BODIES (verbatim):**\r\n\r\nparagraph line\r\n\r\nnot part of entry\r\n"
	got := ExtractNormativeTestBodies(body)
	if len(got) != 1 {
		t.Fatalf("want 1 entry, got %d (%v)", len(got), got)
	}
	if got[0] != "paragraph line" {
		t.Errorf("entry = %q (expected paragraph to stop at CRLF blank line)", got[0])
	}
}

// TestExtractNormativeTestBodies_FenceClosingMustBeOnOwnLine ensures the
// closing fence is a line consisting of exactly ``` (allowing trailing
// whitespace), not just any line beginning with ```. A line opening another
// fenced block (e.g. ```bash) must NOT be treated as the closer.
func TestExtractNormativeTestBodies_FenceClosingMustBeOnOwnLine(t *testing.T) {
	// The middle line "```bash" should NOT close the first fence — it would
	// start a nested fence opener, which means the first fence is
	// unterminated within this body. The implementation must NOT slice the
	// body as if "```bash" were the closer.
	body := "**NORMATIVE TEST BODIES (verbatim):**\n\n```\nfirst line\n```bash\nstill inside fence\n```\n"
	got := ExtractNormativeTestBodies(body)
	// We accept either one entry whose content runs until the real closer
	// (i.e. multi-line including "still inside fence") OR nil (unterminated
	// fence treated as bail-out). The forbidden behaviors are: (a) emitting
	// an entry that ends at the "```bash" line, or (b) emitting more than
	// one entry (a single fenced block must produce at most one entry).
	if len(got) > 1 {
		t.Fatalf("a single fenced block must yield at most one entry, got %d (%v)", len(got), got)
	}
	if len(got) == 1 {
		if !strings.Contains(got[0], "still inside fence") {
			t.Errorf("closing fence incorrectly matched a ```bash opener; entry = %q", got[0])
		}
	}
}

func TestExtractNormativeTestBodies_EmptyFencedBody(t *testing.T) {
	// An empty fenced block (open ``` immediately followed by close ```)
	// must not emit an empty-string entry — the result should be nil.
	body := "**NORMATIVE TEST BODIES (verbatim):**\n\n```\n```\n"
	got := ExtractNormativeTestBodies(body)
	if got != nil {
		t.Errorf("want nil for empty fenced body, got %v", got)
	}
}
