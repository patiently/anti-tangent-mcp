package bm

import (
	"context"
	"strings"
	"time"
)

// AppendTodo inserts "- [ ] <text>" at the end of the "## Active" section of
// <username>/todo/main, matching bm-scribe:add-todo (insert before "## Done").
// The todo note must already exist with an "## Active" and "## Done" section.
func (c *Client) AppendTodo(ctx context.Context, username, text string) error {
	_, err := c.caller.CallTool(ctx, "edit_note", map[string]any{
		"identifier": username + "/todo/main",
		"operation":  "insert_before_section",
		"section":    "## Done",
		"content":    "- [ ] " + text + "\n",
		"project":    c.project,
	})
	return err
}

// MarkTodoDone flips a single bullet from "- [ ]" to "- [x]" and appends a
// done-date, matching bm-scribe:tick-todo. rawLine is the exact source bullet
// (TodoItem.Raw); today supplies the stamp (injected for testability).
func (c *Client) MarkTodoDone(ctx context.Context, username, rawLine string, today time.Time) error {
	replacement := strings.Replace(rawLine, "- [ ]", "- [x]", 1) + " — done " + today.Format("2006-01-02")
	_, err := c.caller.CallTool(ctx, "edit_note", map[string]any{
		"identifier":   username + "/todo/main",
		"operation":    "find_replace",
		"find_text":    rawLine,
		"replace_text": replacement,
		"project":      c.project,
	})
	return err
}
