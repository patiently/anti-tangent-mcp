package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/bm"
	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/state"
)

type fakeProv struct {
	results   []bm.SearchResult
	note      string
	appended  string
	howtos    []bm.SearchResult
	gotchas   []bm.SearchResult
	modules   []bm.SearchResult
	features  []bm.SearchResult
	decisions []bm.SearchResult
	notes     []bm.SearchResult
}

func (f *fakeProv) Snapshot() state.Snapshot { return state.Snapshot{} }
func (f *fakeProv) Ack([]string)             {}
func (f *fakeProv) Search(context.Context, string) ([]bm.SearchResult, error) {
	return f.results, nil
}
func (f *fakeProv) ReadNote(context.Context, string) (string, error)         { return f.note, nil }
func (f *fakeProv) AppendTodo(_ context.Context, text string) error          { f.appended = text; return nil }
func (f *fakeProv) ListHowtos(context.Context) ([]bm.SearchResult, error)    { return f.howtos, nil }
func (f *fakeProv) ListMyNotes(context.Context) ([]bm.SearchResult, error)   { return f.notes, nil }
func (f *fakeProv) ListGotchas(context.Context) ([]bm.SearchResult, error)   { return f.gotchas, nil }
func (f *fakeProv) ListModules(context.Context) ([]bm.SearchResult, error)   { return f.modules, nil }
func (f *fakeProv) ListFeatures(context.Context) ([]bm.SearchResult, error)  { return f.features, nil }
func (f *fakeProv) ListDecisions(context.Context) ([]bm.SearchResult, error) { return f.decisions, nil }

const tok = "secret-token"

func srv(p Provider) http.Handler { return New(p, tok) }

func TestUITokenHitSetsCookieAndStripsToken(t *testing.T) {
	p := &fakeProv{note: "# Title\n\nhi\n"}
	r := httptest.NewRequest("GET", "/ui/note?id=a/b/main&t="+tok, nil)
	w := httptest.NewRecorder()
	srv(p).ServeHTTP(w, r)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("want 303 redirect on token hit, got %d", w.Code)
	}
	if !strings.Contains(w.Header().Get("Set-Cookie"), "gtb_session=") {
		t.Error("session cookie not set on token hit")
	}
	lu, _ := url.Parse(w.Header().Get("Location"))
	if lu.Query().Has("t") {
		t.Errorf("token not stripped from redirect target: %s", w.Header().Get("Location"))
	}
	if lu.Query().Get("id") != "a/b/main" {
		t.Errorf("redirect dropped/changed id: %s", w.Header().Get("Location"))
	}
}

func TestUIRejectsNoCredential(t *testing.T) {
	r := httptest.NewRequest("GET", "/ui/note?id=x", nil)
	w := httptest.NewRecorder()
	srv(&fakeProv{}).ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
}

func TestUINavigationViaCookie(t *testing.T) {
	p := &fakeProv{note: "# Next\n"}
	r := httptest.NewRequest("GET", "/ui/note?id=next", nil)
	r.AddCookie(&http.Cookie{Name: "gtb_session", Value: tok})
	w := httptest.NewRecorder()
	srv(p).ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("cookie auth failed: %d", w.Code)
	}
	if got := w.Header().Get("Referrer-Policy"); got != "no-referrer" {
		t.Errorf("Referrer-Policy = %q, want no-referrer", got)
	}
	if !strings.Contains(w.Body.String(), "<h1>Next</h1>") {
		t.Error("note not rendered via cookie auth")
	}
}

func TestUISearchResultsLinkToNoteNoToken(t *testing.T) {
	p := &fakeProv{results: []bm.SearchResult{{Title: "Epic One", Type: "epic", Permalink: "proj/epics/E-1/main", Snippet: "s"}}}
	r := httptest.NewRequest("GET", "/ui/search/results?q=one", nil)
	r.AddCookie(&http.Cookie{Name: "gtb_session", Value: tok})
	w := httptest.NewRecorder()
	srv(p).ServeHTTP(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "Epic One") {
		t.Error("result title missing")
	}
	if !strings.Contains(body, `href="/ui/note?id=proj%2Fepics%2FE-1%2Fmain"`) {
		t.Errorf("result link wrong; body=%s", body)
	}
}

func TestUINewTodoPostViaCookie(t *testing.T) {
	p := &fakeProv{}
	form := url.Values{"text": {"do the thing"}}
	r := httptest.NewRequest("POST", "/ui/new-todo", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(&http.Cookie{Name: "gtb_session", Value: tok})
	w := httptest.NewRecorder()
	srv(p).ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("code %d", w.Code)
	}
	if p.appended != "do the thing" {
		t.Errorf("appended = %q", p.appended)
	}
}

func TestUIRejectsWrongCookieValue(t *testing.T) {
	r := httptest.NewRequest("GET", "/ui/note?id=x", nil)
	r.AddCookie(&http.Cookie{Name: "gtb_session", Value: "wrong"})
	w := httptest.NewRecorder()
	srv(&fakeProv{}).ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
}

func TestAssetServedWithoutAuth(t *testing.T) {
	r := httptest.NewRequest("GET", "/assets/mermaid.min.js", nil)
	w := httptest.NewRecorder()
	srv(&fakeProv{}).ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("asset code %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/javascript") {
		t.Errorf("content-type = %q", ct)
	}
}

func TestUIHowtosLists(t *testing.T) {
	p := &fakeProv{howtos: []bm.SearchResult{{Title: "Runbook A", Type: "howto", Permalink: "monorepo/howtos/a/main"}}}
	r := httptest.NewRequest("GET", "/ui/howtos", nil)
	r.AddCookie(&http.Cookie{Name: "gtb_session", Value: tok})
	w := httptest.NewRecorder()
	srv(p).ServeHTTP(w, r)
	body := w.Body.String()
	if w.Code != 200 || !strings.Contains(body, "Runbook A") {
		t.Fatalf("code %d body=%s", w.Code, body)
	}
	if !strings.Contains(body, `href="/ui/note?id=monorepo%2Fhowtos%2Fa%2Fmain"`) {
		t.Errorf("howto not linked to note view; body=%s", body)
	}
}

func TestUINotesLists(t *testing.T) {
	p := &fakeProv{notes: []bm.SearchResult{{Title: "My Note", Type: "personal_note", Permalink: "alice/notes/x/main"}}}
	r := httptest.NewRequest("GET", "/ui/notes", nil)
	r.AddCookie(&http.Cookie{Name: "gtb_session", Value: tok})
	w := httptest.NewRecorder()
	srv(p).ServeHTTP(w, r)
	body := w.Body.String()
	if w.Code != 200 || !strings.Contains(body, "My Note") {
		t.Fatalf("code %d body=%s", w.Code, body)
	}
}

func TestUIGotchasLists(t *testing.T) {
	p := &fakeProv{gotchas: []bm.SearchResult{{Title: "Koog wiring gotcha", Type: "gotcha", Permalink: "monorepo/gotchas/0001-koog/main"}}}
	r := httptest.NewRequest("GET", "/ui/gotchas", nil)
	r.AddCookie(&http.Cookie{Name: "gtb_session", Value: tok})
	w := httptest.NewRecorder()
	srv(p).ServeHTTP(w, r)
	body := w.Body.String()
	if w.Code != 200 || !strings.Contains(body, "Koog wiring gotcha") {
		t.Fatalf("code %d body=%s", w.Code, body)
	}
	if !strings.Contains(body, `href="/ui/note?id=monorepo%2Fgotchas%2F0001-koog%2Fmain"`) {
		t.Errorf("gotcha not linked to note view; body=%s", body)
	}
}

func TestUIPagesAreDarkWithTopbar(t *testing.T) {
	r := httptest.NewRequest("GET", "/ui/search", nil)
	r.AddCookie(&http.Cookie{Name: "gtb_session", Value: tok})
	w := httptest.NewRecorder()
	srv(&fakeProv{}).ServeHTTP(w, r)
	body := w.Body.String()
	if !strings.Contains(body, `class="topbar"`) {
		t.Error("topbar missing")
	}
	if !strings.Contains(body, "background:#16181d") {
		t.Error("dark theme missing")
	}
}

func TestUIBrowsePagesForKnowledgeTypes(t *testing.T) {
	p := &fakeProv{
		modules:   []bm.SearchResult{{Title: "Platform Architecture", Type: "module", Permalink: "monorepo/modules/platform-architecture/main"}},
		features:  []bm.SearchResult{{Title: "Snapshitter override", Type: "feature", Permalink: "monorepo/features/snap/main"}},
		decisions: []bm.SearchResult{{Title: "Text-only reviewer", Type: "decision", Permalink: "monorepo/decisions/0001-x/main"}},
	}
	cases := []struct{ path, want, href string }{
		{"/ui/modules", "Platform Architecture", "/ui/note?id=monorepo%2Fmodules%2Fplatform-architecture%2Fmain"},
		{"/ui/features", "Snapshitter override", "/ui/note?id=monorepo%2Ffeatures%2Fsnap%2Fmain"},
		{"/ui/decisions", "Text-only reviewer", "/ui/note?id=monorepo%2Fdecisions%2F0001-x%2Fmain"},
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", c.path, nil)
		r.AddCookie(&http.Cookie{Name: "gtb_session", Value: tok})
		w := httptest.NewRecorder()
		srv(p).ServeHTTP(w, r)
		body := w.Body.String()
		if w.Code != 200 || !strings.Contains(body, c.want) {
			t.Fatalf("%s: code %d, missing %q", c.path, w.Code, c.want)
		}
		if !strings.Contains(body, `href="`+c.href+`"`) {
			t.Errorf("%s: missing note link %s", c.path, c.href)
		}
	}
}
