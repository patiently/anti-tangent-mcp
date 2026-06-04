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

const baseCSS = `:root{color-scheme:dark}
*{box-sizing:border-box}
body{font:16px/1.6 system-ui,Segoe UI,Roboto,sans-serif;margin:0;background:#16181d;color:#e6e7eb}
.topbar{position:sticky;top:0;z-index:10;display:flex;flex-wrap:wrap;gap:.5rem 1rem;align-items:center;padding:.55rem 1rem;background:#1f232b;border-bottom:1px solid #2c313c}
.topbar .brand{font-weight:700;color:#e6e7eb;text-decoration:none;white-space:nowrap}
.topbar form.search{flex:1}
.topbar input{width:100%;font-size:1rem;padding:.45rem .6rem;border-radius:6px;border:1px solid #3a4150;background:#0f1115;color:#e6e7eb}
.topbar nav{display:flex;gap:.2rem}
.topbar nav a{padding:.45rem .7rem;border-radius:6px;color:#cdd3df;text-decoration:none}
.topbar nav a:hover{background:#2c313c}
main.md{max-width:50rem;margin:1.5rem auto;padding:0 1rem}
a{color:#6cb6ff}
h1,h2,h3{line-height:1.25}
main.md input[type=text],main.md input:not([type]){font-size:1rem;padding:.5rem;border-radius:6px;border:1px solid #3a4150;background:#0f1115;color:#e6e7eb;width:70%}
main.md button{font-size:1rem;padding:.5rem .9rem;border-radius:6px;border:1px solid #3a4150;background:#2c313c;color:#e6e7eb;cursor:pointer}
pre{background:#0f1115;padding:.75rem;border-radius:8px;overflow:auto;border:1px solid #2c313c}
pre.mermaid{background:transparent;border:0}
code{font-family:ui-monospace,Menlo,monospace}
:not(pre)>code{background:#262b34;padding:.1rem .3rem;border-radius:4px}
table{border-collapse:collapse}td,th{border:1px solid #2c313c;padding:.3rem .6rem}
ul.cards{list-style:none;padding:0}
ul.cards li{padding:.6rem .8rem;margin:.45rem 0;background:#1f232b;border:1px solid #2c313c;border-radius:8px}
ul.cards li a{font-weight:600}
.tag{font-size:.72rem;color:#9aa4b2;border:1px solid #3a4150;border-radius:999px;padding:.05rem .5rem;margin-left:.4rem}
.snippet{color:#9aa4b2;font-size:.9rem;margin-top:.3rem;white-space:pre-wrap}
.muted{color:#9aa4b2}`

// topbar is the sticky header shown on every UI page: a brand link, a search box
// (epics & stories), and navigation. Links are same-origin; the gtb_session
// cookie authenticates them, so no token is threaded through.
const topbar = `<header class="topbar">` +
	`<a class="brand" href="/ui/search">🛠 gnome-topbar</a>` +
	`<form class="search" method="GET" action="/ui/search/results">` +
	`<input name="q" placeholder="Search the knowledge base…" aria-label="Search the knowledge base"></form>` +
	`<nav><a href="/ui/howtos">Howtos</a><a href="/ui/gotchas">Gotchas</a><a href="/ui/modules">Modules</a><a href="/ui/features">Features</a><a href="/ui/decisions">Decisions</a><a href="/ui/notes">Notes</a><a href="/ui/new-todo">New todo</a></nav>` +
	`</header>`

// pageShell wraps body HTML in a dark-themed document with the sticky topbar and
// loads mermaid from the cached /assets route (mermaid.min.js sets a global, so a
// plain <script src> works).
func pageShell(title, bodyHTML string) string {
	return `<!doctype html><html lang="en"><head><meta charset="utf-8">` +
		`<meta name="viewport" content="width=device-width,initial-scale=1">` +
		`<title>` + html.EscapeString(title) + `</title>` +
		`<style>` + baseCSS + `</style></head><body>` + topbar +
		`<main class="md">` + bodyHTML +
		`</main><script src="/assets/mermaid.min.js"></script>` +
		`<script>mermaid.initialize({startOnLoad:true,theme:"dark"});</script></body></html>`
}
