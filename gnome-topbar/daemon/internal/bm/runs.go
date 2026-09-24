package bm

import "context"

// WriteRunNote creates or overwrites the at_run note titled title in
// directory, in project.
func (c *Client) WriteRunNote(ctx context.Context, project, directory, title, content string) error {
	_, err := c.caller.CallTool(ctx, "write_note", map[string]any{
		"title":     title,
		"directory": directory,
		"content":   content,
		"note_type": "at_run",
		"project":   project,
		// Without overwrite, BM's write_note errors when the note exists
		// (or follows its server-side default), and a changed run could
		// never be republished.
		"overwrite": true,
	})
	return err
}

// ListRunNotes returns every at_run note's permalink in project.
func (c *Client) ListRunNotes(ctx context.Context, project string) ([]SearchResult, error) {
	sub := &Client{caller: c.caller, project: project}
	return sub.listAllByTypes(ctx, []string{"at_run"})
}

// ReadRunNote returns an at_run note's raw markdown.
func (c *Client) ReadRunNote(ctx context.Context, project, permalink string) (string, error) {
	return c.caller.CallTool(ctx, "read_note", map[string]any{"identifier": permalink, "project": project})
}
