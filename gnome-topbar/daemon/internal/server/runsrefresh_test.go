package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func postRefresh(t *testing.T, fp *runsFake, scope string) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{"scope": {scope}}
	r := httptest.NewRequest("POST", "/ui/runs/refresh", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(&http.Cookie{Name: "gtb_session", Value: tok})
	w := httptest.NewRecorder()
	New(fp, tok).ServeHTTP(w, r)
	return w
}

func TestRunsRefreshPostRefreshesAndRedirectsToScope(t *testing.T) {
	fp := &runsFake{}
	w := postRefresh(t, fp, "user:al ice")
	if !fp.refreshed {
		t.Fatal("POST did not refresh the team runs")
	}
	if w.Code != http.StatusSeeOther {
		t.Fatalf("code = %d, want 303", w.Code)
	}
	if got := w.Header().Get("Location"); got != "/ui/runs?scope=user:al+ice" {
		t.Errorf("Location = %q", got)
	}
}

func TestRunsRefreshFallsBackToTeamScope(t *testing.T) {
	for _, scope := range []string{"", "mine", "https://evil.example"} {
		w := postRefresh(t, &runsFake{}, scope)
		if got := w.Header().Get("Location"); got != "/ui/runs?scope=team" {
			t.Errorf("scope %q: Location = %q, want the team scope", scope, got)
		}
	}
}

func TestRunsRefreshRejectsGet(t *testing.T) {
	fp := &runsFake{}
	r := httptest.NewRequest("GET", "/ui/runs/refresh", nil)
	r.AddCookie(&http.Cookie{Name: "gtb_session", Value: tok})
	w := httptest.NewRecorder()
	New(fp, tok).ServeHTTP(w, r)
	if w.Code != http.StatusMethodNotAllowed || fp.refreshed {
		t.Fatalf("GET: code=%d refreshed=%v, want 405 and no refresh", w.Code, fp.refreshed)
	}
}

func TestRunsRefreshRequiresAuth(t *testing.T) {
	fp := &runsFake{}
	r := httptest.NewRequest("POST", "/ui/runs/refresh", nil)
	w := httptest.NewRecorder()
	New(fp, tok).ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized || fp.refreshed {
		t.Fatalf("unauthenticated POST: code=%d refreshed=%v", w.Code, fp.refreshed)
	}
}

func TestRunsPageShowsTeamFreshnessAndRefreshButton(t *testing.T) {
	team := renderRunsPage(RunsView{Scope: "team", Present: true, TeamAsOf: t0.Add(90 * 60 * 1e9)})
	for _, want := range []string{"Team data as of 2026-09-01 01:30", `action="/ui/runs/refresh"`, `method="POST"`, `name="scope" value="team"`, "Refresh now"} {
		if !strings.Contains(team, want) {
			t.Errorf("team scope lacks %q", want)
		}
	}
	empty := renderRunsPage(RunsView{Scope: "team"})
	if !strings.Contains(empty, "Refresh now") || !strings.Contains(empty, "not pulled yet") {
		t.Errorf("an empty team scope must still offer the refresh button and say it has not pulled")
	}
	if mine := renderRunsPage(RunsView{Scope: "mine", Present: true}); strings.Contains(mine, "Refresh now") {
		t.Error("the mine scope reads local files and needs no refresh button")
	}
}

func TestRunsPageShowsConfiguredPullInterval(t *testing.T) {
	if out := renderRunsPage(RunsView{Scope: "team"}); !strings.Contains(out, "not refreshing automatically") {
		t.Errorf("with no interval configured the page must not claim a pull cadence")
	}
	for minutes, want := range map[int]string{60: "pulled every 60 minutes", 15: "pulled every 15 minutes"} {
		if out := renderRunsPage(RunsView{Scope: "team", TeamRefreshMinutes: minutes}); !strings.Contains(out, want) {
			t.Errorf("interval %d: page lacks %q", minutes, want)
		}
	}
}
