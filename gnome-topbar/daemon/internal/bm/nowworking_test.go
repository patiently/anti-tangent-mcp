package bm

import (
	"testing"
	"time"
)

func TestParseNowWorking(t *testing.T) {
	md := `---
title: Currently working on
type: note
updated: 2026-06-02T08:30:00Z
---

Wiring the gnome-topbar daemon; next step is the state aggregator.
`
	nw := ParseNowWorking(md)
	if !nw.HasUpdated {
		t.Fatal("expected HasUpdated")
	}
	want := time.Date(2026, 6, 2, 8, 30, 0, 0, time.UTC)
	if !nw.Updated.Equal(want) {
		t.Fatalf("updated=%v want %v", nw.Updated, want)
	}
	if nw.Body != "Wiring the gnome-topbar daemon; next step is the state aggregator." {
		t.Fatalf("body=%q", nw.Body)
	}
}

func TestParseNowWorkingNoFrontmatter(t *testing.T) {
	nw := ParseNowWorking("just a line\n")
	if nw.HasUpdated {
		t.Fatal("did not expect HasUpdated")
	}
	if nw.Body != "just a line" {
		t.Fatalf("body=%q", nw.Body)
	}
}

func TestParseNowWorkingDetectsNoteNotFound(t *testing.T) {
	md := "\n# Note Not Found in main: \"alice/notes/currently-working-on/main\"\n\nI couldn't find an exact match...\n"
	nw := ParseNowWorking(md)
	if !nw.NotFound {
		t.Fatalf("expected NotFound=true, got %+v", nw)
	}
}

func TestParseNowWorkingRealNoteIsNotFlagged(t *testing.T) {
	md := "---\ntitle: x\nupdated: 2026-06-02T08:00:00Z\n---\n\nWiring the tray.\n"
	nw := ParseNowWorking(md)
	if nw.NotFound {
		t.Fatalf("real note wrongly flagged NotFound: %+v", nw)
	}
	if nw.Body != "Wiring the tray." {
		t.Fatalf("body=%q", nw.Body)
	}
}
