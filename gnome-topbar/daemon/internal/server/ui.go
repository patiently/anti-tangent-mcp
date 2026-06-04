package server

import (
	"crypto/subtle"
	"fmt"
	"html"
	"net/http"

	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/bm"
)

const sessionCookie = "gtb_session"

func registerUI(mux *http.ServeMux, p Provider, token string) {
	// Public OSS library — no secret, no auth. Cached aggressively.
	mux.HandleFunc("/assets/mermaid.min.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		_, _ = w.Write(mermaidJS)
	})

	mux.HandleFunc("/ui/search", uiAuth(token, func(w http.ResponseWriter, r *http.Request) {
		writeHTML(w, pageShell("gnome-topbar", `<h1>gnome-topbar</h1>`+
			`<form method="GET" action="/ui/search/results">`+
			`<input type="text" name="q" autofocus placeholder="Search epics, stories &amp; gotchas…"> <button>Search</button></form>`+
			`<p class="muted">Or browse:</p>`+
			`<ul class="cards">`+
			`<li><a href="/ui/howtos">📓 Howtos</a></li>`+
			`<li><a href="/ui/gotchas">⚠️ Gotchas</a></li>`+
			`<li><a href="/ui/notes">🗒 My notes</a></li>`+
			`<li><a href="/ui/new-todo">➕ New todo</a></li>`+
			`</ul>`))
	}))

	mux.HandleFunc("/ui/search/results", uiAuth(token, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		res, err := p.Search(r.Context(), q)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		writeHTML(w, listPage("Results for "+q, res, "No epics, stories, or gotchas matched."))
	}))

	mux.HandleFunc("/ui/howtos", uiAuth(token, func(w http.ResponseWriter, r *http.Request) {
		res, err := p.ListHowtos(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		writeHTML(w, listPage("Howtos", res, "No howtos found."))
	}))

	mux.HandleFunc("/ui/gotchas", uiAuth(token, func(w http.ResponseWriter, r *http.Request) {
		res, err := p.ListGotchas(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		writeHTML(w, listPage("Gotchas", res, "No gotchas found."))
	}))

	mux.HandleFunc("/ui/notes", uiAuth(token, func(w http.ResponseWriter, r *http.Request) {
		res, err := p.ListMyNotes(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		writeHTML(w, listPage("My notes", res, "No personal notes yet."))
	}))

	mux.HandleFunc("/ui/note", uiAuth(token, func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("id")
		md, err := p.ReadNote(r.Context(), id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		out, err := renderNoteHTML(id, md)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeHTML(w, out)
	}))

	mux.HandleFunc("/ui/new-todo", uiAuth(token, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			if err := r.ParseForm(); err != nil {
				http.Error(w, "bad form", http.StatusBadRequest)
				return
			}
			text := r.FormValue("text")
			if text == "" {
				http.Error(w, "empty todo", http.StatusBadRequest)
				return
			}
			if err := p.AppendTodo(r.Context(), text); err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
			writeHTML(w, pageShell("Added", `<h1>Added</h1><p>`+html.EscapeString(text)+
				`</p><p><a href="/ui/new-todo">Add another</a></p>`))
			return
		}
		writeHTML(w, pageShell("New todo", `<h1>New todo</h1>`+
			`<form method="POST" action="/ui/new-todo">`+
			`<input type="text" name="text" autofocus placeholder="what needs doing"> `+
			`<button>Add</button></form>`))
	}))
}

// listPage renders a titled list of Basic Memory notes as cards linking to the
// note view. emptyMsg is shown when there are no results.
func listPage(title string, res []bm.SearchResult, emptyMsg string) string {
	body := `<h1>` + html.EscapeString(title) + `</h1>`
	if len(res) == 0 {
		body += `<p class="muted">` + html.EscapeString(emptyMsg) + `</p>`
	}
	body += `<ul class="cards">`
	for _, rr := range res {
		body += `<li><a href="` + noteHref(rr.Permalink) + `">` + html.EscapeString(rr.Title) + `</a>`
		if rr.Type != "" {
			body += `<span class="tag">` + html.EscapeString(rr.Type) + `</span>`
		}
		if rr.Snippet != "" {
			body += `<div class="snippet">` + html.EscapeString(rr.Snippet) + `</div>`
		}
		body += `</li>`
	}
	body += `</ul>`
	return pageShell(title, body)
}

// uiAuth allows a request bearing a valid ?t= token OR a valid session cookie.
// On a token hit it plants the cookie so in-page navigation needs no token.
func uiAuth(token string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		viaToken := tokenOK(token, r.URL.Query().Get("t"))
		if !viaToken && !cookieOK(token, r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if viaToken {
			http.SetCookie(w, &http.Cookie{
				Name: sessionCookie, Value: token, Path: "/ui",
				HttpOnly: true, SameSite: http.SameSiteStrictMode,
			})
		}
		next(w, r)
	}
}

func cookieOK(token string, r *http.Request) bool {
	c, err := r.Cookie(sessionCookie)
	return err == nil && tokenOK(token, c.Value)
}

func tokenOK(want, got string) bool {
	return subtle.ConstantTimeCompare([]byte(want), []byte(got)) == 1
}

func writeHTML(w http.ResponseWriter, s string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Belt-and-suspenders: never leak the ?t= token (which rides the first-hit
	// URL) via a Referer header should an external resource ever be added.
	w.Header().Set("Referrer-Policy", "no-referrer")
	_, _ = fmt.Fprint(w, s)
}
