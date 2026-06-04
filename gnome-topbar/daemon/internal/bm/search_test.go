package bm

import (
	"context"
	"testing"
)

func TestSearchKnowledgeParsesResults(t *testing.T) {
	payload := `{"results":[
	  {"title":"YN epic","type":"entity","permalink":"monorepo/epics/X/main","content":"do the thing","metadata":{"note_type":"epic"}},
	  {"title":"YN story","type":"entity","permalink":"monorepo/stories/Y/main","content":"a story body","metadata":{"note_type":"story"}}
	]}`
	fc := &fakeCaller{ret: payload}
	c := New(fc, "main")
	res, err := c.SearchKnowledge(context.Background(), "thing")
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 {
		t.Fatalf("results=%d want 2", len(res))
	}
	if res[0].Title != "YN epic" || res[0].Type != "epic" || res[0].Permalink != "monorepo/epics/X/main" {
		t.Fatalf("bad result[0]: %+v", res[0])
	}
	if fc.last.name != "search_notes" {
		t.Fatalf("called %q", fc.last.name)
	}
	types, _ := fc.last.args["note_types"].([]string)
	if len(types) != 6 || types[0] != "epic" || types[2] != "gotcha" || types[3] != "module" || types[5] != "decision" {
		t.Fatalf("note_types=%v", fc.last.args["note_types"])
	}
}

func TestListHowtosParses(t *testing.T) {
	payload := `{"results":[{"title":"Runbook A","type":"entity","permalink":"monorepo/howtos/a/main","content":"body","metadata":{"note_type":"howto"}}]}`
	fc := &fakeCaller{ret: payload}
	c := New(fc, "main")
	res, err := c.ListHowtos(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].Title != "Runbook A" || res[0].Permalink != "monorepo/howtos/a/main" {
		t.Fatalf("bad: %+v", res)
	}
	if types, _ := fc.last.args["note_types"].([]string); len(types) != 1 || types[0] != "howto" {
		t.Fatalf("note_types=%v", fc.last.args["note_types"])
	}
}

func TestListMyNotesFiltersByNamespace(t *testing.T) {
	payload := `{"results":[
	  {"title":"Mine","type":"entity","permalink":"alice/notes/mine/main","metadata":{"note_type":"personal_note"}},
	  {"title":"Theirs","type":"entity","permalink":"bob/notes/theirs/main","metadata":{"note_type":"personal_note"}}
	]}`
	fc := &fakeCaller{ret: payload}
	c := New(fc, "main")
	res, err := c.ListMyNotes(context.Background(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].Title != "Mine" {
		t.Fatalf("want only alice's note, got %+v", res)
	}
}

// pagingCaller returns a different payload per call, simulating BM pagination.
type pagingCaller struct {
	pages []string
	calls int
}

func (p *pagingCaller) CallTool(_ context.Context, _ string, _ map[string]any) (string, error) {
	i := p.calls
	if i >= len(p.pages) {
		i = len(p.pages) - 1
	}
	p.calls++
	return p.pages[i], nil
}

func TestListHowtosPaginatesAcrossPages(t *testing.T) {
	page1 := `{"results":[{"title":"A","permalink":"p/howtos/a/main","metadata":{"note_type":"howto"}}],"has_more":true}`
	page2 := `{"results":[{"title":"B","permalink":"p/howtos/b/main","metadata":{"note_type":"howto"}}],"has_more":false}`
	pc := &pagingCaller{pages: []string{page1, page2}}
	c := New(pc, "main")
	res, err := c.ListHowtos(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 {
		t.Fatalf("want 2 results across pages, got %d (%+v)", len(res), res)
	}
	if pc.calls != 2 {
		t.Fatalf("want 2 page fetches (stop on has_more=false), got %d", pc.calls)
	}
}
