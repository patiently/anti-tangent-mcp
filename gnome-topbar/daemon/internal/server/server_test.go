package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/bm"
	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/state"
)

type fakeProvider struct{ acked []string }

func (f *fakeProvider) Snapshot() state.Snapshot {
	s := state.Snapshot{Sources: map[string]state.SourceStatus{"github": {OK: true}}}
	s.Todos.Active = []bm.TodoItem{{Text: "x"}}
	return s
}
func (f *fakeProvider) Search(ctx context.Context, q string) ([]bm.SearchResult, error) {
	return []bm.SearchResult{{Title: "T", Type: "epic"}}, nil
}
func (f *fakeProvider) Ack(ids []string) { f.acked = append(f.acked, ids...) }

func newTestServer() (*httptest.Server, *fakeProvider) {
	fp := &fakeProvider{}
	h := New(fp, "secret")
	return httptest.NewServer(h), fp
}

func TestStateRequiresBearer(t *testing.T) {
	srv, _ := newTestServer()
	defer srv.Close()
	resp, _ := http.Get(srv.URL + "/state")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status=%d want 401", resp.StatusCode)
	}
}

func TestStateReturnsSnapshot(t *testing.T) {
	srv, _ := newTestServer()
	defer srv.Close()
	req, _ := http.NewRequest("GET", srv.URL+"/state", nil)
	req.Header.Set("Authorization", "Bearer secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("err=%v status=%v", err, resp.StatusCode)
	}
	var snap state.Snapshot
	_ = json.NewDecoder(resp.Body).Decode(&snap)
	if len(snap.Todos.Active) != 1 {
		t.Fatalf("snapshot not returned: %+v", snap)
	}
}

func TestAck(t *testing.T) {
	srv, fp := newTestServer()
	defer srv.Close()
	req, _ := http.NewRequest("POST", srv.URL+"/ack", strings.NewReader(`{"event_ids":["a","b"]}`))
	req.Header.Set("Authorization", "Bearer secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("err=%v status=%v", err, resp.StatusCode)
	}
	if len(fp.acked) != 2 {
		t.Fatalf("acked=%v", fp.acked)
	}
}
