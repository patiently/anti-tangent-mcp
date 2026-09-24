package atruns

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/bm"
	"github.com/patiently/anti-tangent-mcp/scorecard"
)

// fakeCaller implements bm.Caller for these tests: it records every call and
// serves write_note/search_notes/read_note from fixed responses, matching
// the pattern the bm package's own tests use (see internal/bm/write_test.go).
type fakeCall struct {
	name string
	args map[string]any
}

type fakeCaller struct {
	calls  []fakeCall
	search string
	notes  map[string]string
}

func (f *fakeCaller) CallTool(_ context.Context, name string, args map[string]any) (string, error) {
	f.calls = append(f.calls, fakeCall{name, args})
	switch name {
	case "search_notes":
		return f.search, nil
	case "read_note":
		id, _ := args["identifier"].(string)
		return f.notes[id], nil
	default:
		return "", nil
	}
}

func (f *fakeCaller) writeCalls() []fakeCall {
	var out []fakeCall
	for _, c := range f.calls {
		if c.name == "write_note" {
			out = append(out, c)
		}
	}
	return out
}

func TestShareEnabledOnlyForExactlyOne(t *testing.T) {
	for v, want := range map[string]bool{"1": true, "": false, "0": false, "true": false, " 1": false} {
		if got := ShareEnabled(func(string) string { return v }); got != want {
			t.Fatalf("%q -> %v", v, got)
		}
	}
}

func TestNoteRoundTripStampsPublisher(t *testing.T) {
	body, err := NoteBody("alice", []scorecard.RunLine{{RunHash: "r_1", Publisher: "mallory"}}, []scorecard.OutcomeLine{{RunHash: "r_1", Source: "final_review"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(body, "task_title") || strings.Contains(body, "plan_run_id") {
		t.Fatalf("note leaks content: %s", body)
	}
	ls, ocs, err := ParseNote(body)
	if err != nil || ls[0].Publisher != "alice" || ocs[0].Publisher != "alice" {
		t.Fatalf("parse: %+v %+v %v", ls, ocs, err)
	}
	if _, _, err := ParseNote("no block here"); err == nil {
		t.Fatal("a note without a json block must not parse")
	}
}

func TestNoteBodyNormalisesCategories(t *testing.T) {
	long := "  The Handler Swallows The Error And Returns 200 To The Caller Anyway  "
	in := []scorecard.OutcomeLine{{RunHash: "r_1", Source: "final_review", Findings: []scorecard.OutcomeFinding{{TaskIndex: 1, Severity: "major", Category: long}}}}
	body, err := NoteBody("alice", nil, in)
	if err != nil {
		t.Fatal(err)
	}
	want := scorecard.NormalizeCategory(long)
	if !strings.Contains(body, `"category": "`+want+`"`) || strings.Contains(body, "Anyway") {
		t.Fatalf("category not normalised: %s", body)
	}
	if in[0].Findings[0].Category != long {
		t.Fatal("NoteBody must not mutate its input")
	}
}

func TestNoteBodyClampsModelStrings(t *testing.T) {
	longModel := strings.Repeat("m", 150)
	controlModel := "anthropic:claude\tsonnet-5\nextra"
	in := []scorecard.OutcomeLine{{
		RunHash:       "r_1",
		Source:        "final_review",
		ReviewerModel: longModel,
		ImplementerModels: []scorecard.ImplementerModel{
			{TaskIndex: 1, Model: controlModel},
		},
	}}
	body, err := NoteBody("alice", nil, in)
	if err != nil {
		t.Fatal(err)
	}
	wantReviewer := scorecard.ClampModelString(longModel)
	wantImpl := scorecard.ClampModelString(controlModel)
	if len(wantReviewer) >= len(longModel) {
		t.Fatalf("test setup: clamp did not shorten reviewer model, got %q", wantReviewer)
	}
	if !strings.Contains(body, `"reviewer_model": "`+wantReviewer+`"`) {
		t.Fatalf("reviewer_model not clamped: %s", body)
	}
	if !strings.Contains(body, `"model": "`+wantImpl+`"`) {
		t.Fatalf("implementer_models[].model not clamped: %s", body)
	}

	// NoteBody must not mutate its input.
	if in[0].ReviewerModel != longModel {
		t.Fatal("NoteBody must not mutate ReviewerModel on its input")
	}
	if in[0].ImplementerModels[0].Model != controlModel {
		t.Fatal("NoteBody must not mutate ImplementerModels on its input")
	}
}

func TestPublishSkipsUnchangedAndRunsWithoutOutcomes(t *testing.T) {
	fc := &fakeCaller{}
	statePath := filepath.Join(t.TempDir(), "published.json")
	p := &Publisher{Client: bm.New(fc, "team"), Project: "team", Username: "alice", StatePath: statePath}

	d := Data{
		Lines: []scorecard.RunLine{
			{RunHash: "r_1", Header: true},
			{RunHash: "r_2", Header: true},
		},
		Outcomes: []scorecard.OutcomeLine{
			{RunHash: "r_1", Source: "final_review"},
		},
	}

	wrote, err := p.Publish(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	if wrote != 1 {
		t.Fatalf("first publish wrote = %d, want 1", wrote)
	}
	writes := fc.writeCalls()
	if len(writes) != 1 {
		t.Fatalf("write_note calls = %d, want 1", len(writes))
	}
	if writes[0].args["directory"] != noteDirectory || writes[0].args["title"] != "r_1" {
		t.Fatalf("write args = %+v", writes[0].args)
	}

	fc.calls = nil
	wrote, err = p.Publish(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	if wrote != 0 {
		t.Fatalf("second publish (unchanged) wrote = %d, want 0", wrote)
	}
	if len(fc.writeCalls()) != 0 {
		t.Fatalf("unexpected write_note on unchanged publish: %+v", fc.writeCalls())
	}

	// Changing the outcome must trigger a republish.
	d.Outcomes[0].Findings = []scorecard.OutcomeFinding{{TaskIndex: 1, Severity: "major", Category: "x"}}
	fc.calls = nil
	wrote, err = p.Publish(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	if wrote != 1 {
		t.Fatalf("third publish (changed) wrote = %d, want 1", wrote)
	}
}

func TestPoolSkipsBadNotes(t *testing.T) {
	validBody, err := NoteBody("alice", []scorecard.RunLine{{RunHash: "r_1"}}, []scorecard.OutcomeLine{{RunHash: "r_1", Source: "final_review"}})
	if err != nil {
		t.Fatal(err)
	}
	fc := &fakeCaller{
		search: `{"results":[
		  {"title":"r_1","type":"entity","permalink":"anti-tangent/runs/r_1/main","metadata":{"note_type":"at_run"}},
		  {"title":"r_2","type":"entity","permalink":"anti-tangent/runs/r_2/main","metadata":{"note_type":"at_run"}}
		],"has_more":false}`,
		notes: map[string]string{
			"anti-tangent/runs/r_1/main": validBody,
			"anti-tangent/runs/r_2/main": "garbage, no json block here",
		},
	}
	c := bm.New(fc, "team")
	d, skipped, err := Pool(context.Background(), c, "team")
	if err != nil {
		t.Fatal(err)
	}
	if skipped != 1 {
		t.Fatalf("skipped = %d, want 1", skipped)
	}
	if !d.Present || len(d.Lines) != 1 || d.Lines[0].RunHash != "r_1" || d.Lines[0].Publisher != "alice" {
		t.Fatalf("d = %+v", d)
	}
	if len(d.Outcomes) != 1 || d.Outcomes[0].Publisher != "alice" {
		t.Fatalf("outcomes = %+v", d.Outcomes)
	}
}
