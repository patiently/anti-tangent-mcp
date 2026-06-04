package server

import (
	"bytes"
	_ "embed"
	"html"
	"net/url"
	"regexp"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

//go:embed assets/mermaid.min.js
var mermaidJS []byte

var (
	frontmatterRe  = regexp.MustCompile(`(?s)\A---\n.*?\n---\n`)
	mermaidFenceRe = regexp.MustCompile(`(?s)<pre><code class="language-mermaid">(.*?)</code></pre>`)
	// [[target]] or [[target|label]] — BM's inter-note wikilink syntax.
	wikilinkRe = regexp.MustCompile(`\[\[([^\]|]+)(?:\|([^\]]+))?\]\]`)
	// [text](memory://<id>) links some BM notes emit.
	memHrefRe = regexp.MustCompile(`href="memory://([^"]+)"`)
)

var gm = goldmark.New(goldmark.WithExtensions(extension.GFM))

// noteHref builds the same-origin note-view URL for a BM identifier (permalink
// or title). Auth rides on the session cookie, so the link carries no token.
func noteHref(id string) string {
	return "/ui/note?id=" + url.QueryEscape(id)
}

// renderNoteHTML converts a BM note's markdown into a standalone HTML page.
// Inter-note links become /ui/note links so clicking navigates to the target:
//   - [[id]] / [[id|label]] wikilinks (pre-processed before goldmark)
//   - [text](memory://id) links (post-processed on the rendered HTML)
//
// ```mermaid fences become <pre class="mermaid"> for client-side rendering. The
// HTML-entity-escaped diagram source survives because mermaid reads textContent
// (the browser un-escapes). Pure: no I/O. (Wikilinks inside fenced code blocks
// are also rewritten — acceptable for v1.)
func renderNoteHTML(title, md string) (string, error) {
	body := frontmatterRe.ReplaceAllString(md, "")
	body = wikilinkRe.ReplaceAllStringFunc(body, func(m string) string {
		g := wikilinkRe.FindStringSubmatch(m)
		target, label := g[1], g[1]
		if g[2] != "" {
			label = g[2]
		}
		return "[" + label + "](" + noteHref(target) + ")"
	})
	var buf bytes.Buffer
	if err := gm.Convert([]byte(body), &buf); err != nil {
		return "", err
	}
	out := buf.String()
	out = mermaidFenceRe.ReplaceAllString(out, `<pre class="mermaid">$1</pre>`)
	out = memHrefRe.ReplaceAllStringFunc(out, func(m string) string {
		g := memHrefRe.FindStringSubmatch(m)
		return `href="` + noteHref(g[1]) + `"`
	})
	return pageShell(title, out), nil
}

const baseCSS = `body{font:16px/1.6 system-ui,Segoe UI,Roboto,sans-serif;max-width:48rem;margin:2rem auto;padding:0 1rem;color:#1a1a1a}
pre{background:#f5f5f5;padding:.75rem;border-radius:6px;overflow:auto}
pre.mermaid{background:transparent}
code{font-family:ui-monospace,Menlo,monospace}
table{border-collapse:collapse}td,th{border:1px solid #ddd;padding:.3rem .6rem}
h1,h2,h3{line-height:1.25}a{color:#0b69c7}`

// pageShell wraps body HTML in a document that loads mermaid from the cached
// /assets route (mermaid.min.js sets a global, so a plain <script src> works).
func pageShell(title, bodyHTML string) string {
	return `<!doctype html><html lang="en"><head><meta charset="utf-8">` +
		`<meta name="viewport" content="width=device-width,initial-scale=1">` +
		`<title>` + html.EscapeString(title) + `</title>` +
		`<style>` + baseCSS + `</style></head><body><main class="md">` +
		bodyHTML +
		`</main><script src="/assets/mermaid.min.js"></script>` +
		`<script>mermaid.initialize({startOnLoad:true});</script></body></html>`
}
