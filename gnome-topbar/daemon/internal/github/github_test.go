package github

import (
	"encoding/json"
	"testing"
)

type fakeRest struct {
	byPath map[string]string
	calls  []string
}

func (f *fakeRest) Get(path string, resp any) error {
	f.calls = append(f.calls, path)
	body := f.byPath[path]
	return json.Unmarshal([]byte(body), resp)
}

func TestFetchAuthoredMapsFields(t *testing.T) {
	const path = "search/issues?q=is%3Aopen+is%3Apr+author%3A%40me"
	fr := &fakeRest{byPath: map[string]string{
		path: `{"items":[{"number":40,"title":"Fix thing","html_url":"https://github.com/o/r/pull/40","repository_url":"https://api.github.com/repos/o/r","updated_at":"2026-02-26T16:22:36Z","user":{"login":"me"}}]}`,
	}}
	s := New(fr)
	prs, err := s.FetchAuthored()
	if err != nil {
		t.Fatal(err)
	}
	if len(prs) != 1 {
		t.Fatalf("prs=%d", len(prs))
	}
	p := prs[0]
	if p.Repo != "o/r" || p.Number != 40 || p.Title != "Fix thing" || p.Author != "me" {
		t.Fatalf("bad mapping: %+v", p)
	}
	if p.URL != "https://github.com/o/r/pull/40" {
		t.Fatalf("bad url: %s", p.URL)
	}
}

func TestFetchReviewRequestedUsesRightQuery(t *testing.T) {
	const path = "search/issues?q=is%3Aopen+is%3Apr+review-requested%3A%40me"
	fr := &fakeRest{byPath: map[string]string{path: `{"items":[]}`}}
	s := New(fr)
	if _, err := s.FetchReviewRequested(); err != nil {
		t.Fatal(err)
	}
	if len(fr.calls) != 1 || fr.calls[0] != path {
		t.Fatalf("calls=%v", fr.calls)
	}
}
