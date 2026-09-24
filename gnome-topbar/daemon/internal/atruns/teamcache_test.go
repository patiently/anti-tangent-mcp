package atruns

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/bm"
	"github.com/patiently/anti-tangent-mcp/scorecard"
)

func listing(hashes ...string) string {
	items := make([]string, len(hashes))
	for i, h := range hashes {
		items[i] = fmt.Sprintf(`{"title":%q,"type":"entity","permalink":"anti-tangent/runs/%s/main","metadata":{"note_type":"at_run"}}`, h, h)
	}
	return `{"results":[` + strings.Join(items, ",") + `],"has_more":false}`
}

func runNote(t *testing.T, publisher, hash, source string) string {
	t.Helper()
	body, err := NoteBody(publisher, []scorecard.RunLine{{RunHash: hash}}, []scorecard.OutcomeLine{{RunHash: hash, Source: source}})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func (f *fakeCaller) reads() int {
	n := 0
	for _, c := range f.calls {
		if c.name == "read_note" {
			n++
		}
	}
	return n
}

type clock struct{ now time.Time }

func (c *clock) Now() time.Time { return c.now }

func newTeamCache(fc *fakeCaller, path string, clk *clock) *TeamCache {
	return &TeamCache{
		Client:    bm.New(fc, "team"),
		Project:   "team",
		Path:      path,
		Interval:  time.Hour,
		FullEvery: 24 * time.Hour,
		Now:       clk.Now,
	}
}

func mustRefresh(t *testing.T, c *TeamCache, full bool) Data {
	t.Helper()
	d, _, err := c.Refresh(context.Background(), full)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// runHashes lists d's run hashes, sorted and comma-joined.
func runHashes(d Data) string {
	hs := make([]string, 0, len(d.Lines))
	for _, l := range d.Lines {
		hs = append(hs, l.RunHash)
	}
	sort.Strings(hs)
	return strings.Join(hs, ",")
}

var t0 = time.Date(2026, 9, 24, 9, 0, 0, 0, time.UTC)

func TestTeamCacheReadsOnlyNewNotesAndDropsDeleted(t *testing.T) {
	fc := &fakeCaller{
		search: listing("r_1", "r_2"),
		notes: map[string]string{
			"anti-tangent/runs/r_1/main": runNote(t, "alice", "r_1", "final_review"),
			"anti-tangent/runs/r_2/main": runNote(t, "bob", "r_2", "final_review"),
		},
	}
	clk := &clock{now: t0}
	c := newTeamCache(fc, filepath.Join(t.TempDir(), "team-runs.json"), clk)

	d := mustRefresh(t, c, false)
	if len(d.Lines) != 2 || fc.reads() != 2 {
		t.Fatalf("first pull: lines=%d reads=%d", len(d.Lines), fc.reads())
	}

	fc.search = listing("r_2", "r_3")
	fc.notes["anti-tangent/runs/r_3/main"] = runNote(t, "carol", "r_3", "review_now")
	clk.now = t0.Add(time.Hour)
	d = mustRefresh(t, c, false)
	if fc.reads() != 3 {
		t.Fatalf("incremental pull re-read cached notes: reads=%d, want 3", fc.reads())
	}
	if got := runHashes(d); got != "r_2,r_3" {
		t.Fatalf("after incremental pull the cache holds %s, want r_2,r_3", got)
	}
}

func TestTeamCacheFullRefreshAfterFullEveryAndWhenForced(t *testing.T) {
	fc := &fakeCaller{
		search: listing("r_1"),
		notes:  map[string]string{"anti-tangent/runs/r_1/main": runNote(t, "alice", "r_1", "final_review")},
	}
	clk := &clock{now: t0}
	c := newTeamCache(fc, filepath.Join(t.TempDir(), "team-runs.json"), clk)
	if _, _, err := c.Refresh(context.Background(), false); err != nil {
		t.Fatal(err)
	}

	fc.notes["anti-tangent/runs/r_1/main"] = runNote(t, "alice", "r_1", "review_now")
	clk.now = t0.Add(2 * time.Hour)
	d, _, _ := c.Refresh(context.Background(), false)
	if fc.reads() != 1 || d.Outcomes[0].Source != "final_review" {
		t.Fatalf("an hourly pull must not re-read a cached note: reads=%d source=%s", fc.reads(), d.Outcomes[0].Source)
	}

	d, _, _ = c.Refresh(context.Background(), true)
	if fc.reads() != 2 || d.Outcomes[0].Source != "review_now" {
		t.Fatalf("a forced pull must re-read every note: reads=%d source=%s", fc.reads(), d.Outcomes[0].Source)
	}

	clk.now = t0.Add(26 * time.Hour)
	fc.notes["anti-tangent/runs/r_1/main"] = runNote(t, "alice", "r_1", "final_review")
	d, _, _ = c.Refresh(context.Background(), false)
	if fc.reads() != 3 || d.Outcomes[0].Source != "final_review" {
		t.Fatalf("a pull past FullEvery must re-read every note: reads=%d source=%s", fc.reads(), d.Outcomes[0].Source)
	}
}

func TestTeamCacheLoadServesDiskWithoutCallingBM(t *testing.T) {
	path := filepath.Join(t.TempDir(), "team-runs.json")
	fc := &fakeCaller{
		search: listing("r_1"),
		notes:  map[string]string{"anti-tangent/runs/r_1/main": runNote(t, "alice", "r_1", "final_review")},
	}
	clk := &clock{now: t0}
	if _, _, err := newTeamCache(fc, path, clk).Refresh(context.Background(), false); err != nil {
		t.Fatal(err)
	}

	fresh := &fakeCaller{}
	clk.now = t0.Add(10 * time.Minute)
	c := newTeamCache(fresh, path, clk)
	d, asOf, _ := c.Load()
	if len(fresh.calls) != 0 {
		t.Fatalf("Load called BM: %+v", fresh.calls)
	}
	if runHashes(d) != "r_1" || d.Lines[0].Publisher != "alice" {
		t.Fatalf("loaded = %+v", d)
	}
	if !asOf.Equal(t0) {
		t.Fatalf("asOf = %v, want %v", asOf, t0)
	}
	if c.Due() {
		t.Fatal("a cache pulled 10 minutes ago must not be due")
	}
	clk.now = t0.Add(time.Hour)
	if !c.Due() {
		t.Fatal("a cache pulled an hour ago must be due")
	}
}

func TestTeamCacheIgnoresCacheFromAnotherProject(t *testing.T) {
	path := filepath.Join(t.TempDir(), "team-runs.json")
	fc := &fakeCaller{
		search: listing("r_1"),
		notes:  map[string]string{"anti-tangent/runs/r_1/main": runNote(t, "alice", "r_1", "final_review")},
	}
	clk := &clock{now: t0}
	if _, _, err := newTeamCache(fc, path, clk).Refresh(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	other := newTeamCache(&fakeCaller{}, path, clk)
	other.Project = "elsewhere"
	if d, _, _ := other.Load(); d.Present || !other.Due() {
		t.Fatalf("a cache for another project must be ignored: %+v", d)
	}
}

type failingCaller struct{}

func (failingCaller) CallTool(context.Context, string, map[string]any) (string, error) {
	return "", errors.New("bm down")
}

func TestTeamCacheKeepsDataWhenBMFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "team-runs.json")
	fc := &fakeCaller{
		search: listing("r_1"),
		notes:  map[string]string{"anti-tangent/runs/r_1/main": runNote(t, "alice", "r_1", "final_review")},
	}
	clk := &clock{now: t0}
	c := newTeamCache(fc, path, clk)
	if _, _, err := c.Refresh(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	c.Client = bm.New(failingCaller{}, "team")
	clk.now = t0.Add(2 * time.Hour)
	if _, _, err := c.Refresh(context.Background(), false); err == nil {
		t.Fatal("a failed listing must return its error")
	}
	d, asOf, _ := c.Snapshot()
	if len(d.Lines) != 1 || !asOf.Equal(t0) {
		t.Fatalf("a failed pull must keep the last good data and its time: %+v %v", d, asOf)
	}
	if !c.Due() {
		t.Fatal("a failed pull must leave the cache due, so the next tick retries")
	}
}

func TestTeamCacheCountsUnparseableNotes(t *testing.T) {
	fc := &fakeCaller{
		search: listing("r_1", "r_2"),
		notes: map[string]string{
			"anti-tangent/runs/r_1/main": runNote(t, "alice", "r_1", "final_review"),
			"anti-tangent/runs/r_2/main": "garbage",
		},
	}
	c := newTeamCache(fc, filepath.Join(t.TempDir(), "team-runs.json"), &clock{now: t0})
	d, skipped, err := c.Refresh(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if skipped != 1 || runHashes(d) != "r_1" {
		t.Fatalf("d=%+v skipped=%d", d, skipped)
	}
}

// flakyReadCaller fails every read_note while failReads is set.
type flakyReadCaller struct {
	*fakeCaller
	failReads bool
}

func (f *flakyReadCaller) CallTool(ctx context.Context, name string, args map[string]any) (string, error) {
	if name == "read_note" && f.failReads {
		f.fakeCaller.calls = append(f.fakeCaller.calls, fakeCall{name, args})
		return "", errors.New("transient")
	}
	return f.fakeCaller.CallTool(ctx, name, args)
}

func TestTeamCacheRetriesANewNoteWhoseReadFailed(t *testing.T) {
	fc := &flakyReadCaller{fakeCaller: &fakeCaller{
		search: listing("r_1"),
		notes:  map[string]string{"anti-tangent/runs/r_1/main": runNote(t, "alice", "r_1", "final_review")},
	}, failReads: true}
	clk := &clock{now: t0}
	c := &TeamCache{Client: bm.New(fc, "team"), Project: "team", Path: filepath.Join(t.TempDir(), "team-runs.json"),
		Interval: time.Hour, FullEvery: 24 * time.Hour, Now: clk.Now}

	d, skipped, err := c.Refresh(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if d.Present || skipped != 0 {
		t.Fatalf("a failed read is not an unparseable note: d=%+v skipped=%d", d, skipped)
	}
	if fc.reads() != 1 {
		t.Fatalf("the first pull must attempt the new note once: reads=%d", fc.reads())
	}
	fc.failReads = false
	clk.now = t0.Add(time.Hour)
	if d = mustRefresh(t, c, false); runHashes(d) != "r_1" {
		t.Fatalf("the next incremental pull must retry the note: %+v", d)
	}
	if fc.reads() != 2 {
		t.Fatalf("the retry must read the note exactly once more: reads=%d", fc.reads())
	}
}
