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
