package server

import "strings"
import "testing"

func TestRenderNoteHTML_StripsFrontmatterAndRendersHeading(t *testing.T) {
	md := "---\ntitle: X\npermalink: a/b/main\n---\n\n# Hello\n\nbody text\n"
	out, err := renderNoteHTML("a/b/main", md)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "<h1>Hello</h1>") {
		t.Error("heading not rendered")
	}
	if strings.Contains(out, "permalink: a/b/main") {
		t.Error("frontmatter leaked into rendered HTML")
	}
	if !strings.Contains(out, `<script src="/assets/mermaid.min.js">`) {
		t.Error("mermaid asset script tag missing")
	}
	if !strings.Contains(out, "mermaid.initialize") {
		t.Error("mermaid bootstrap missing")
	}
}

func TestRenderNoteHTML_MermaidFenceBecomesDiv(t *testing.T) {
	md := "# D\n\n```mermaid\ngraph TD; A-->B;\n```\n"
	out, err := renderNoteHTML("d", md)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `<pre class="mermaid">`) {
		t.Error("mermaid fence not rewritten to <pre class=\"mermaid\">")
	}
	if strings.Contains(out, `class="language-mermaid"`) {
		t.Error("raw goldmark mermaid code block survived")
	}
}

func TestRenderNoteHTML_WikilinkBecomesNoteLink(t *testing.T) {
	md := "See [[proj/decisions/0001-x/main]] and [[proj/modules/m/main|the module]].\n"
	out, err := renderNoteHTML("d", md)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `href="/ui/note?id=proj%2Fdecisions%2F0001-x%2Fmain"`) {
		t.Errorf("wikilink not rewritten to note link; out=%s", out)
	}
	if !strings.Contains(out, ">the module</a>") {
		t.Error("piped wikilink label not used")
	}
}

func TestRenderNoteHTML_MemoryLinkRewritten(t *testing.T) {
	md := "[other](memory://proj/stories/S-1/main)\n"
	out, err := renderNoteHTML("d", md)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `href="/ui/note?id=proj%2Fstories%2FS-1%2Fmain"`) {
		t.Errorf("memory:// link not rewritten; out=%s", out)
	}
}

func TestRenderNoteHTML_MultipleMermaidFences(t *testing.T) {
	md := "# D\n\n```mermaid\ngraph TD; A-->B;\n```\n\ntext\n\n```mermaid\nsequenceDiagram\nA->>B: hi\n```\n"
	out, err := renderNoteHTML("d", md)
	if err != nil {
		t.Fatal(err)
	}
	if c := strings.Count(out, `<pre class="mermaid">`); c != 2 {
		t.Errorf("expected 2 mermaid blocks, got %d", c)
	}
	if strings.Contains(out, `class="language-mermaid"`) {
		t.Error("a raw mermaid code block survived")
	}
}

func TestRenderNoteHTML_NoFrontmatter(t *testing.T) {
	out, err := renderNoteHTML("d", "# Heading\n\nplain body, no frontmatter\n")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "<h1>Heading</h1>") {
		t.Error("heading not rendered when note has no frontmatter")
	}
	if !strings.Contains(out, "plain body, no frontmatter") {
		t.Error("body dropped when note has no frontmatter")
	}
}

func TestRenderNoteHTML_TitleEscaped(t *testing.T) {
	out, err := renderNoteHTML("a<b>&c", "# x\n")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "<title>a&lt;b&gt;&amp;c</title>") {
		t.Errorf("title not HTML-escaped; out=%s", out)
	}
}
