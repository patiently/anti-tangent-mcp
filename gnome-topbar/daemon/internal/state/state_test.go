package state

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/bm"
	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/github"
)

func TestComputeEventsNewOnly(t *testing.T) {
	store, _ := LoadStore(filepath.Join(t.TempDir(), "seen.json"))
	due := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
	snap := &Snapshot{}
	snap.PRs.ReviewRequested = []github.PR{{Repo: "o/r", Number: 7, Title: "Please review", URL: "https://x/7"}}
	snap.Todos.Due = []bm.TodoItem{{Text: "ship it", Due: &due, Overdue: true}}

	ev := ComputeEvents(snap, store)
	if len(ev) != 2 {
		t.Fatalf("events=%d want 2: %+v", len(ev), ev)
	}

	// ack one, recompute -> only the other remains
	for _, e := range ev {
		if e.Kind == "review_request" {
			_ = store.MarkSeen([]string{e.ID})
		}
	}
	ev2 := ComputeEvents(snap, store)
	if len(ev2) != 1 || ev2[0].Kind != "todo_due" {
		t.Fatalf("after ack: %+v", ev2)
	}
}
