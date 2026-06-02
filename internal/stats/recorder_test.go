package stats

import (
	"log/slog"
	"sync"
	"testing"
	"time"
)

func newTestRecorder(t *testing.T, threshold int) *Recorder {
	t.Helper()
	r, err := New(Options{
		Dir:              t.TempDir(),
		Reviewer:         nil,
		SummaryInterval:  24 * time.Hour,
		SummaryThreshold: threshold,
		RetentionDays:    30,
		Logger:           slog.Default(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return r
}

func TestNilRecorderIsNoOp(t *testing.T) {
	var r *Recorder
	r.Record(Event{Tool: "validate_task_spec"}) // must not panic
	if got := r.HashSession("abc"); got != "" {
		t.Errorf("nil HashSession = %q, want empty", got)
	}
}

func TestRecordAppends(t *testing.T) {
	r := newTestRecorder(t, 1000) // threshold high so no compaction fires
	for i := 0; i < 3; i++ {
		r.Record(Event{Ts: time.Now().UTC(), Tool: "validate_task_spec", Verdict: "pass"})
	}
	events, err := readEvents(r.dir)
	if err != nil {
		t.Fatalf("readEvents: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("appended %d events, want 3", len(events))
	}
	if r.state.EventsSinceSummary != 3 {
		t.Errorf("EventsSinceSummary = %d, want 3", r.state.EventsSinceSummary)
	}
}

func TestHashSessionStableAndSalted(t *testing.T) {
	r := newTestRecorder(t, 1000)
	a := r.HashSession("session-1")
	if a == "" || a == "session-1" {
		t.Fatalf("hash = %q (must be non-empty and not the raw id)", a)
	}
	if a != r.HashSession("session-1") {
		t.Error("hash not stable for same id")
	}
	if a == r.HashSession("session-2") {
		t.Error("different ids hashed equal")
	}
	if r.HashSession("") != "" {
		t.Error("empty id should hash to empty")
	}
}

func TestRecordSingleFlightCompaction(t *testing.T) {
	r := newTestRecorder(t, 1) // every record is "due"

	var mu sync.Mutex
	calls := 0
	started := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan struct{}, 1) // buffered: at most one send expected
	r.runCompaction = func(now time.Time) {
		mu.Lock()
		calls++
		mu.Unlock()
		started <- struct{}{}
		<-release // hold the single in-flight slot open
		finished <- struct{}{}
	}

	r.Record(Event{Ts: time.Now().UTC(), Tool: "validate_task_spec"})
	<-started                                                         // first compaction is now running and holding the slot
	r.Record(Event{Ts: time.Now().UTC(), Tool: "validate_task_spec"}) // should NOT launch a second
	close(release)
	<-finished // wait for the in-flight goroutine to complete before asserting

	// Assert no second value is pending (single-flight guarantee).
	select {
	case <-finished:
		t.Fatal("second compaction finished — single-flight not enforced")
	default:
	}

	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("compaction ran %d times, want 1 (single-flight)", calls)
	}
}

// TestPruneEvents mirrors TestPruneCodescene: write one old and one fresh event,
// prune with an intermediate cutoff, assert only the fresh event survives.
func TestPruneEvents(t *testing.T) {
	dir := t.TempDir()
	base := time.Unix(1700000000, 0).UTC()
	old := Event{Ts: base.Add(-48 * time.Hour), Tool: "validate_task_spec"}
	fresh := Event{Ts: base, Tool: "validate_task_spec"}
	if err := appendJSONL(dir, eventsFile, old); err != nil {
		t.Fatal(err)
	}
	if err := appendJSONL(dir, eventsFile, fresh); err != nil {
		t.Fatal(err)
	}
	if err := pruneEvents(dir, base.Add(-24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, err := readEvents(dir)
	if err != nil {
		t.Fatalf("readEvents after prune: %v", err)
	}
	if len(got) != 1 || !got[0].Ts.Equal(base) {
		t.Fatalf("after prune got %d events (ts=%v), want 1 (the fresh one at %v)", len(got), func() interface{} {
			if len(got) > 0 {
				return got[0].Ts
			}
			return nil
		}(), base)
	}
}

// TestCompactDecrementsCounterAndStampsFreshTime verifies that compact() uses a
// fresh clock reading (not the trigger-time now) for LastSummaryAt, and
// decrements EventsSinceSummary by the count that was in flight rather than
// zeroing it — so events appended during the LLM window stay counted.
func TestCompactDecrementsCounterAndStampsFreshTime(t *testing.T) {
	r, err := New(Options{
		Dir:              t.TempDir(),
		Reviewer:         nil,
		SummaryInterval:  24 * time.Hour,
		SummaryThreshold: 1000,
		RetentionDays:    30,
		Logger:           slog.Default(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	triggerNow := time.Unix(1700000000, 0).UTC()
	completedAt := time.Unix(1700005000, 0).UTC() // clearly after triggerNow
	r.clock = func() time.Time { return completedAt }

	// Seed three events (simulates events already in-flight at compact time).
	r.state.EventsSinceSummary = 3
	for i := 0; i < 3; i++ {
		if err := appendJSONL(r.dir, eventsFile, Event{Ts: triggerNow, Tool: "validate_task_spec"}); err != nil {
			t.Fatalf("appendJSONL: %v", err)
		}
	}

	// Call compact synchronously (not via Record's goroutine).
	r.compact(triggerNow)

	if !r.state.LastSummaryAt.Equal(completedAt) {
		t.Errorf("LastSummaryAt = %v, want completedAt %v (fresh clock, not triggerNow %v)",
			r.state.LastSummaryAt, completedAt, triggerNow)
	}
	// 3 in-flight events - 3 processed = 0.
	if r.state.EventsSinceSummary != 0 {
		t.Errorf("EventsSinceSummary = %d after compact, want 0 (3 - 3)", r.state.EventsSinceSummary)
	}
}
