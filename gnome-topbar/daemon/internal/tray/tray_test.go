package tray

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/state"
)

func TestSelectUnraisedDedups(t *testing.T) {
	raised := map[string]bool{}
	evs := []state.Event{{ID: "a"}, {ID: "b"}}
	if got := selectUnraised(raised, evs); len(got) != 2 {
		t.Fatalf("first call should select both, got %d", len(got))
	}
	if got := selectUnraised(raised, evs); len(got) != 0 {
		t.Fatalf("second call should dedup to 0, got %d", len(got))
	}
	evs = append(evs, state.Event{ID: "c"})
	if got := selectUnraised(raised, evs); len(got) != 1 || got[0].ID != "c" {
		t.Fatalf("only the new event should select, got %+v", got)
	}
}

// TestSelectUnraisedConcurrent proves the dedup is race-free and selects each
// event exactly once when callers serialize on a shared lock — the discipline
// render() follows (it mutates t.raised only under t.mu). Run under -race.
func TestSelectUnraisedConcurrent(t *testing.T) {
	raised := map[string]bool{}
	var mu sync.Mutex
	evs := []state.Event{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	var total int64
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			mu.Lock()
			got := selectUnraised(raised, evs)
			mu.Unlock()
			atomic.AddInt64(&total, int64(len(got)))
		}()
	}
	wg.Wait()
	if total != 3 {
		t.Fatalf("each event must be selected exactly once across goroutines, got %d", total)
	}
}
