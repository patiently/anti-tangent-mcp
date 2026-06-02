package bm

import (
	"regexp"
	"strings"
	"time"
)

type TodoItem struct {
	Text    string     `json:"text"`
	Due     *time.Time `json:"due,omitempty"`
	Overdue bool       `json:"overdue"`
}

var (
	openRe = regexp.MustCompile(`^- \[ \]\s+(.*)$`)
	doneRe = regexp.MustCompile(`^- \[[xX]\]\s+(.*)$`)
	dateRe = regexp.MustCompile(`^\[(\d{4}-\d{2}-\d{2})\]\s*(.*)$`)
)

// ParseTodos returns open items under "## Active" and the subset that is due
// today or overdue (relative to today's local date).
func ParseTodos(md string, today time.Time) (active, due []TodoItem) {
	day := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, today.Location())
	inActive := false
	for _, raw := range strings.Split(md, "\n") {
		line := strings.TrimRight(raw, " \t")
		if strings.HasPrefix(line, "## ") {
			inActive = strings.EqualFold(strings.TrimSpace(line[3:]), "Active")
			continue
		}
		if !inActive {
			continue
		}
		if doneRe.MatchString(line) {
			continue
		}
		m := openRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		item := TodoItem{Text: strings.TrimSpace(m[1])}
		if dm := dateRe.FindStringSubmatch(item.Text); dm != nil {
			if d, err := time.ParseInLocation("2006-01-02", dm[1], today.Location()); err == nil {
				item.Due = &d
				item.Text = strings.TrimSpace(dm[2])
			}
		}
		active = append(active, item)
		if item.Due != nil && !item.Due.After(day) {
			item.Overdue = item.Due.Before(day)
			due = append(due, item)
		}
	}
	return active, due
}
