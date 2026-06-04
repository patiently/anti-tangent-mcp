// Package server exposes the daemon snapshot over a loopback, bearer-protected
// JSON API consumed by the GNOME extension.
package server

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/bm"
	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/state"
)

type Provider interface {
	Snapshot() state.Snapshot
	Search(ctx context.Context, q string) ([]bm.SearchResult, error)
	Ack(ids []string)
	ReadNote(ctx context.Context, identifier string) (string, error)
	AppendTodo(ctx context.Context, text string) error
	ListHowtos(ctx context.Context) ([]bm.SearchResult, error)
	ListMyNotes(ctx context.Context) ([]bm.SearchResult, error)
}

func New(p Provider, token string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/state", auth(token, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, p.Snapshot())
	}))
	mux.HandleFunc("/search", auth(token, func(w http.ResponseWriter, r *http.Request) {
		res, err := p.Search(r.Context(), r.URL.Query().Get("q"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		writeJSON(w, map[string]any{"results": res})
	}))
	mux.HandleFunc("/ack", auth(token, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			EventIDs []string `json:"event_ids"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		p.Ack(body.EventIDs)
		writeJSON(w, map[string]any{"acked": len(body.EventIDs)})
	}))
	registerUI(mux, p, token)
	return mux
}

func auth(token string, next http.HandlerFunc) http.HandlerFunc {
	want := "Bearer " + token
	return func(w http.ResponseWriter, r *http.Request) {
		if !tokenOK(want, r.Header.Get("Authorization")) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
