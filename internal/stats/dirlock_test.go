package stats

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// sharedRecorder opens a second Recorder on r's directory, standing in for
// another server process: it shares the files but not r.mu.
func sharedRecorder(t *testing.T, r *Recorder) *Recorder {
	t.Helper()
	o, err := New(Options{Dir: r.dir, SummaryInterval: 24 * time.Hour, SummaryThreshold: 1000, RetentionDays: 30, Logger: slog.Default()})
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func seededWith(models ...string) firstSeen {
	fs := firstSeen{Seeded: true, Models: map[string]time.Time{}}
	for _, m := range models {
		fs.Models[m] = time.Time{}
	}
	return fs
}

// TestFirstSeenWritersAcrossProcessesKeepEveryEntry pauses writer A between
// its reread and its rename, then lets writer B run. Without a cross-process
// lock B rereads and renames first and A's later rename drops B's entry.
func TestFirstSeenWritersAcrossProcessesKeepEveryEntry(t *testing.T) {
	a := newTestRecorder(t, 1000)
	b := sharedRecorder(t, a)
	aReread, releaseA, bBusy := make(chan struct{}), make(chan struct{}), make(chan struct{})
	a.firstSeenHook = func(stage string) {
		if stage == "reread" {
			close(aReread)
			<-releaseA
		}
	}
	var busyOnce sync.Once
	b.firstSeenHook = func(stage string) {
		if stage == "busy" {
			busyOnce.Do(func() { close(bBusy) })
		}
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); a.persistFirstSeen(seededWith("m")) }()
	<-aReread
	bDone := make(chan struct{})
	go func() { defer close(bDone); b.persistFirstSeen(seededWith("n")) }()
	select {
	case <-bBusy:
	case <-bDone:
	}
	close(releaseA)
	wg.Wait()
	<-bDone
	fs, err := readFirstSeen(a.dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{"m", "n"} {
		if _, ok := fs.Models[m]; !ok {
			t.Fatalf("entry %q lost: %+v", m, fs.Models)
		}
	}
}

func TestFirstSeenHeldLockFallsBackWithOneWarning(t *testing.T) {
	r := newTestRecorder(t, 1000)
	var buf bytes.Buffer
	r.logger = slog.New(slog.NewTextHandler(&buf, nil))
	r.firstSeenLockWait = 50 * time.Millisecond
	lock := filepath.Join(r.dir, firstSeenFile+".lock")
	if err := os.WriteFile(lock, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	r.persistFirstSeen(seededWith("m"))
	fs, err := readFirstSeen(r.dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := fs.Models["m"]; !ok {
		t.Fatalf("fallback did not write: %+v", fs.Models)
	}
	assertWarnings(t, &buf, "first_seen lock unavailable", 1)
	if _, err := os.Stat(lock); err != nil {
		t.Fatalf("another holder's lock was removed: %v", err)
	}
}

func TestFirstSeenStaleLockIsTakenOver(t *testing.T) {
	r := newTestRecorder(t, 1000)
	var buf bytes.Buffer
	r.logger = slog.New(slog.NewTextHandler(&buf, nil))
	r.firstSeenLockWait = 50 * time.Millisecond
	lock := filepath.Join(r.dir, firstSeenFile+".lock")
	if err := os.WriteFile(lock, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * firstSeenLockStale)
	if err := os.Chtimes(lock, old, old); err != nil {
		t.Fatal(err)
	}
	r.persistFirstSeen(seededWith("m"))
	assertWarnings(t, &buf, "first_seen lock unavailable", 0)
	if _, err := os.Stat(lock); !os.IsNotExist(err) {
		t.Fatalf("lock left behind: %v", err)
	}
}
