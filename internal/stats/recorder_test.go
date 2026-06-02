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
	r.runCompaction = func(now time.Time) {
		mu.Lock()
		calls++
		mu.Unlock()
		started <- struct{}{}
		<-release // hold the single in-flight slot open
	}

	r.Record(Event{Ts: time.Now().UTC(), Tool: "validate_task_spec"})
	<-started // first compaction is now running and holding the slot
	r.Record(Event{Ts: time.Now().UTC(), Tool: "validate_task_spec"}) // should NOT launch a second
	close(release)
	time.Sleep(20 * time.Millisecond) // let any erroneous second launch run

	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("compaction ran %d times, want 1 (single-flight)", calls)
	}
}
