package stats

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
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
	go func() { defer wg.Done(); _, _ = a.persistFirstSeen(seededWith("m")) }()
	<-aReread
	bDone := make(chan struct{})
	go func() { defer close(bDone); _, _ = b.persistFirstSeen(seededWith("n")) }()
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

// The test holds the lock through its own open of the lock file, standing in
// for another process mid-write: the call must leave first-seen.json alone,
// still signal from what it read, and warn once; the call after release writes.
func TestFirstSeenHeldLockSkipsWriteThenRetries(t *testing.T) {
	r, now := fixedClockRecorder(t)
	recordTask(t, r, "r1", now.Add(-time.Hour), "claude-sonnet-5-5")
	r.WriteScorecard()
	path := filepath.Join(r.dir, firstSeenFile)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lock := path + ".lock"
	release, err := acquireFileLock(lock, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	r.logger = slog.New(slog.NewTextHandler(&buf, nil))
	r.firstSeenLockWait = 50 * time.Millisecond
	recordTask(t, r, "r2", now.Add(-time.Minute), "claude-haiku-5-5")
	sc := r.WriteScorecard()
	release()
	if ids := signalIDs(sc); !slices.Contains(ids, "new_model:claude-haiku-5-5") {
		t.Fatalf("want new_model for haiku from the read view, got %v", ids)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("first-seen.json written without the lock:\nbefore %s\nafter  %s", before, after)
	}
	assertWarnings(t, &buf, "first_seen lock unavailable, write skipped", 1)
	assertWarnings(t, &buf, "level=WARN", 1)
	if _, err := os.Stat(lock); err != nil {
		t.Fatalf("lock file removed on release: %v", err)
	}
	buf.Reset()
	r.WriteScorecard()
	fs, err := readFirstSeen(r.dir)
	if err != nil {
		t.Fatal(err)
	}
	if !fs.Models["claude-haiku-5-5"].Equal(now) {
		t.Fatalf("retry did not write haiku: %+v", fs.Models)
	}
	assertWarnings(t, &buf, "level=WARN", 0)
}

// Another process leaves first-seen.json unreadable (not corrupt) between this
// call's first read and its locked reread. The write is skipped so entries the
// unread file holds are not dropped, revisit is withheld, and the call warns once.
func TestFirstSeenRereadFailureSkipsWriteAndWithholdsRevisit(t *testing.T) {
	r, now := fixedClockRecorder(t)
	recordTask(t, r, "r1", now.Add(-time.Hour), "claude-sonnet-5-5")
	r.WriteScorecard()
	path := filepath.Join(r.dir, firstSeenFile)
	var buf bytes.Buffer
	r.logger = slog.New(slog.NewTextHandler(&buf, nil))
	r.firstSeenHook = func(stage string) {
		if stage == "locked" {
			if err := os.Remove(path); err != nil {
				t.Error(err)
			}
			if err := os.Mkdir(path, 0o755); err != nil {
				t.Error(err)
			}
		}
	}
	if got := r.updateFirstSeen([]string{"claude-haiku-5-5"}, now); got != nil {
		t.Fatalf("want nil first-seen view, got %+v", got)
	}
	if fi, err := os.Stat(path); err != nil || !fi.IsDir() {
		t.Fatalf("first-seen.json was overwritten: %v", err)
	}
	assertWarnings(t, &buf, "reread failed, write skipped", 1)
	assertWarnings(t, &buf, "level=WARN", 1)
}
