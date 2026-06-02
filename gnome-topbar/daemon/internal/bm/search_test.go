package bm

import (
	"context"
	"testing"
)

func TestSearchEpicsStoriesParsesResults(t *testing.T) {
	payload := `{"results":[
	  {"title":"YN epic","type":"epic","permalink":"monorepo/epics/X/main","content":"do the thing"},
	  {"title":"YN story","type":"story","permalink":"monorepo/stories/Y/main","content":"a story body"}
	]}`
	fc := &fakeCaller{ret: payload}
	c := New(fc, "main")
	res, err := c.SearchEpicsStories(context.Background(), "thing")
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
	if len(types) != 2 || types[0] != "epic" || types[1] != "story" {
		t.Fatalf("note_types=%v", fc.last.args["note_types"])
	}
}
