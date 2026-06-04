package bm

import (
	"context"
	"encoding/json"
	"strings"
)

type SearchResult struct {
	Title     string `json:"title"`
	Type      string `json:"type"`
	Permalink string `json:"permalink"`
	Snippet   string `json:"snippet"`
}

// SearchEpicsStories runs a Basic Memory search limited to epic/story notes.
func (c *Client) SearchEpicsStories(ctx context.Context, query string) ([]SearchResult, error) {
	raw, err := c.caller.CallTool(ctx, "search_notes", map[string]any{
		"query":         query,
		"note_types":    []string{"epic", "story"},
		"project":       c.project,
		"output_format": "json",
	})
	if err != nil {
		return nil, err
	}
	return parseSearch(raw)
}

// ListHowtos returns all howto notes (project-knowledge runbooks), in the order
// Basic Memory returns them.
func (c *Client) ListHowtos(ctx context.Context) ([]SearchResult, error) {
	raw, err := c.caller.CallTool(ctx, "search_notes", map[string]any{
		"note_types":    []string{"howto"},
		"project":       c.project,
		"output_format": "json",
		"page_size":     100,
	})
	if err != nil {
		return nil, err
	}
	return parseSearch(raw)
}

// ListMyNotes returns the caller's personal notes — those under
// "<username>/notes/". It filters by permalink prefix so a shared Basic Memory
// project only ever surfaces the caller's own notes, never another user's.
func (c *Client) ListMyNotes(ctx context.Context, username string) ([]SearchResult, error) {
	raw, err := c.caller.CallTool(ctx, "search_notes", map[string]any{
		"note_types":    []string{"personal_note"},
		"project":       c.project,
		"output_format": "json",
		"page_size":     100,
	})
	if err != nil {
		return nil, err
	}
	all, err := parseSearch(raw)
	if err != nil {
		return nil, err
	}
	prefix := username + "/notes/"
	out := make([]SearchResult, 0, len(all))
	for _, r := range all {
		if strings.HasPrefix(r.Permalink, prefix) {
			out = append(out, r)
		}
	}
	return out, nil
}

func parseSearch(raw string) ([]SearchResult, error) {
	var env struct {
		Results []struct {
			Title     string `json:"title"`
			Type      string `json:"type"` // server returns "entity" for all; real note type is in metadata.note_type
			Permalink string `json:"permalink"`
			Content   string `json:"content"`
			Snippet   string `json:"snippet"`
			Metadata  struct {
				NoteType string `json:"note_type"`
			} `json:"metadata"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		return nil, err
	}
	out := make([]SearchResult, 0, len(env.Results))
	for _, r := range env.Results {
		// Basic Memory's search_notes returns type:"entity" for every result;
		// the epic/story note type lives in metadata.note_type. Prefer it,
		// falling back to the top-level type only if metadata is absent.
		typ := r.Metadata.NoteType
		if typ == "" {
			typ = r.Type
		}
		snip := r.Snippet
		if snip == "" {
			snip = r.Content
		}
		// cap to a rune count (not bytes, so multi-byte runes aren't split)
		if rs := []rune(snip); len(rs) > snippetMaxChars {
			snip = string(rs[:snippetMaxChars])
		}
		out = append(out, SearchResult{Title: r.Title, Type: typ, Permalink: r.Permalink, Snippet: snip})
	}
	return out, nil
}

// snippetMaxChars bounds the search-result snippet length.
const snippetMaxChars = 200
