package stats

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// TestWriteFileAtomicConcurrentSamePath pins that concurrent writers to the same
// target path never fail and always publish one complete payload. The old fixed
// "<path>.tmp" implementation could fail Rename (temp already moved by a peer) or
// publish a torn payload; ANTI_TANGENT_STATS_DIR is shared across processes, so
// the helper must tolerate concurrent writers to the same path.
func TestWriteFileAtomicConcurrentSamePath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rollup.json")

	const writers = 16
	payloads := make([][]byte, writers)
	for i := range payloads {
		// Same length so a torn read is still detectable by content, not size.
		payloads[i] = []byte("payload-" + string(rune('A'+i)) + "----")
	}

	var wg sync.WaitGroup
	errs := make([]error, writers)
	wg.Add(writers)
	for i := 0; i < writers; i++ {
		go func(i int) {
			defer wg.Done()
			errs[i] = writeFileAtomic(path, payloads[i], 0o644)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("writer %d: writeFileAtomic returned error: %v", i, err)
		}
	}

	// The published file must be exactly one of the written payloads (never a
	// torn/partial mix), and no stray temp files must linger.
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read published file: %v", err)
	}
	matched := false
	for _, p := range payloads {
		if string(got) == string(p) {
			matched = true
			break
		}
	}
	if !matched {
		t.Fatalf("published content %q is not any complete written payload", got)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, e := range entries {
		if e.Name() != "rollup.json" {
			t.Errorf("unexpected leftover file in dir: %q", e.Name())
		}
	}
}
