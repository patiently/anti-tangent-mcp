package bm

import (
	"context"
	"encoding/json"
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
		if len(snip) > 200 {
			snip = snip[:200]
		}
		out = append(out, SearchResult{Title: r.Title, Type: typ, Permalink: r.Permalink, Snippet: snip})
	}
	return out, nil
}
