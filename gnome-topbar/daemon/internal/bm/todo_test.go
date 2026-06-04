package bm

import (
	"testing"
	"time"
)

const todoFixture = `---
title: alice's todo
type: personal_todo
---

# Todo

## Active

- [ ] [2026-06-01] overdue item
- [ ] [2026-06-02] due today item
- [ ] [2026-06-10] future item
- [ ] no-date item
- [x] [2026-05-01] done item should be ignored

## Done

- [x] archived
`

func TestParseTodos(t *testing.T) {
	today := time.Date(2026, 6, 2, 9, 0, 0, 0, time.UTC)
	active, due := ParseTodos(todoFixture, today)

	if len(active) != 4 {
		t.Fatalf("active=%d want 4: %+v", len(active), active)
	}
	if len(due) != 2 {
		t.Fatalf("due=%d want 2: %+v", len(due), due)
	}
	// due set = overdue + due-today, both open
	texts := map[string]bool{}
	for _, d := range due {
		texts[d.Text] = true
	}
	if !texts["overdue item"] || !texts["due today item"] {
		t.Fatalf("wrong due items: %+v", due)
	}
	// overdue flagged
	for _, d := range due {
		if d.Text == "overdue item" && !d.Overdue {
			t.Fatal("overdue item not flagged Overdue")
		}
	}
	// no-date item present in active, never in due
	for _, d := range due {
		if d.Text == "no-date item" {
			t.Fatal("no-date item must not be due")
		}
	}
}

func TestParseTodosKeepsRawLine(t *testing.T) {
	md := "## Active\n- [ ] [2026-06-04] ship the thing\n- [ ] no date here\n## Done\n- [x] old\n"
	today := time.Date(2026, 6, 4, 9, 0, 0, 0, time.Local)
	active, _ := ParseTodos(md, today)
	if len(active) != 2 {
		t.Fatalf("want 2 active, got %d", len(active))
	}
	if active[0].Raw != "- [ ] [2026-06-04] ship the thing" {
		t.Errorf("raw[0] = %q", active[0].Raw)
	}
	if active[0].Text != "ship the thing" {
		t.Errorf("text[0] = %q (date prefix should be stripped for display)", active[0].Text)
	}
	if active[1].Raw != "- [ ] no date here" {
		t.Errorf("raw[1] = %q", active[1].Raw)
	}
}
