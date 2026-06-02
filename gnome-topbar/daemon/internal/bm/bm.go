// Package bm reads Basic Memory notes/search via an MCP Caller and parses the
// returned markdown into the daemon's domain types.
package bm

import "context"

// Caller is the subset of an MCP client this package needs.
type Caller interface {
	CallTool(ctx context.Context, name string, args map[string]any) (string, error)
}

type Client struct {
	caller  Caller
	project string
}

func New(caller Caller, project string) *Client {
	return &Client{caller: caller, project: project}
}

// ReadNote returns the raw markdown (including frontmatter) of a note.
func (c *Client) ReadNote(ctx context.Context, identifier string) (string, error) {
	return c.caller.CallTool(ctx, "read_note", map[string]any{
		"identifier": identifier,
		"project":    c.project,
	})
}
