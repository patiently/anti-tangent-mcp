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
