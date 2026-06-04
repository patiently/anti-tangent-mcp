package server

import (
	"crypto/subtle"
	"fmt"
	"html"
	"net/http"
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
		writeHTML(w, pageShell("Search", `<h1>Search epics &amp; stories</h1>`+
			`<form method="GET" action="/ui/search/results">`+
			`<input name="q" autofocus placeholder="query" style="font-size:1rem;padding:.4rem;width:70%">`+
			`<button style="font-size:1rem;padding:.4rem .8rem">Search</button></form>`))
	}))

	mux.HandleFunc("/ui/search/results", uiAuth(token, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		res, err := p.Search(r.Context(), q)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		body := `<h1>Results for ` + html.EscapeString(q) + `</h1>`
		if len(res) == 0 {
			body += `<p>No epics or stories matched.</p>`
		}
		body += `<ul>`
		for _, rr := range res {
			body += `<li><a href="` + noteHref(rr.Permalink) + `">` + html.EscapeString(rr.Title) + `</a> ` +
				`<small>(` + html.EscapeString(rr.Type) + `)</small><br><small>` +
				html.EscapeString(rr.Snippet) + `</small></li>`
		}
		body += `</ul><p><a href="/ui/search">New search</a></p>`
		writeHTML(w, pageShell("Results", body))
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
			`<input name="text" autofocus placeholder="what needs doing" style="font-size:1rem;padding:.4rem;width:70%">`+
			`<button style="font-size:1rem;padding:.4rem .8rem">Add</button></form>`))
	}))
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
	_, _ = fmt.Fprint(w, s)
}
