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
// done-date. rawLine is the exact source bullet (TodoItem.Raw); today supplies
// the stamp (injected for testability).
//
// Note: Basic Memory's edit_note find_replace takes the replacement in `content`
// (the schema's required field), NOT `replace_text` — the latter is the
// bm-scribe:tick-todo skill's *conceptual* arg name, which the live v0.21.1
// server rejects with a validation error.
func (c *Client) MarkTodoDone(ctx context.Context, username, rawLine string, today time.Time) error {
	replacement := strings.Replace(rawLine, "- [ ]", "- [x]", 1) + " — done " + today.Format("2006-01-02")
	_, err := c.caller.CallTool(ctx, "edit_note", map[string]any{
		"identifier": username + "/todo/main",
		"operation":  "find_replace",
		"find_text":  rawLine,
		"content":    replacement,
		"project":    c.project,
	})
	return err
}
