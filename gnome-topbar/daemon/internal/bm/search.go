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

// SearchKnowledge runs a Basic Memory full-text search across the project-
// knowledge note types surfaced by the tray search box: epics, stories, gotchas,
// modules, features, and decisions. page_size is raised above Basic Memory's
// default of 10 so an exact ticket-ID match isn't buried beneath the many notes
// that merely mention it in passing.
func (c *Client) SearchKnowledge(ctx context.Context, query string) ([]SearchResult, error) {
	raw, err := c.caller.CallTool(ctx, "search_notes", map[string]any{
		"query":         query,
		"note_types":    []string{"epic", "story", "gotcha", "module", "feature", "decision"},
		"project":       c.project,
		"output_format": "json",
		"page_size":     50,
	})
	if err != nil {
		return nil, err
	}
	return parseSearch(raw)
}

// ListHowtos returns all howto notes (project-knowledge runbooks).
func (c *Client) ListHowtos(ctx context.Context) ([]SearchResult, error) {
	return c.listAllByTypes(ctx, []string{"howto"})
}

// ListGotchas returns all gotcha notes (module-scoped lessons learned).
func (c *Client) ListGotchas(ctx context.Context) ([]SearchResult, error) {
	return c.listAllByTypes(ctx, []string{"gotcha"})
}

// ListModules returns all module notes (coherent capabilities / technical
// surface — e.g. the platform-architecture overview).
func (c *Client) ListModules(ctx context.Context) ([]SearchResult, error) {
	return c.listAllByTypes(ctx, []string{"module"})
}

// ListFeatures returns all feature notes (user-facing capability catalog).
func (c *Client) ListFeatures(ctx context.Context) ([]SearchResult, error) {
	return c.listAllByTypes(ctx, []string{"feature"})
}

// ListDecisions returns all decision notes (ADR-style records).
func (c *Client) ListDecisions(ctx context.Context) ([]SearchResult, error) {
	return c.listAllByTypes(ctx, []string{"decision"})
}

// ListMyNotes returns the caller's personal notes — those under
// "<username>/notes/". It filters by permalink prefix so a shared Basic Memory
// project only ever surfaces the caller's own notes, never another user's.
func (c *Client) ListMyNotes(ctx context.Context, username string) ([]SearchResult, error) {
	all, err := c.listAllByTypes(ctx, []string{"personal_note"})
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

// listAllByTypes returns every note of the given types, following Basic Memory's
// pagination (page + has_more) so results past the first page aren't silently
// dropped. Capped at maxListPages as a runaway guard. Used by the browse pages;
// SearchKnowledge stays single-page on purpose (ranked top results).
func (c *Client) listAllByTypes(ctx context.Context, noteTypes []string) ([]SearchResult, error) {
	const pageSize, maxListPages = 100, 20
	var all []SearchResult
	for page := 1; page <= maxListPages; page++ {
		raw, err := c.caller.CallTool(ctx, "search_notes", map[string]any{
			"note_types":    noteTypes,
			"project":       c.project,
			"output_format": "json",
			"page_size":     pageSize,
			"page":          page,
		})
		if err != nil {
			return nil, err
		}
		res, hasMore, err := parseSearchPage(raw)
		if err != nil {
			return nil, err
		}
		all = append(all, res...)
		if !hasMore {
			break
		}
	}
	return all, nil
}

func parseSearch(raw string) ([]SearchResult, error) {
	res, _, err := parseSearchPage(raw)
	return res, err
}

// parseSearchPage parses one search_notes page, returning its results and the
// envelope's has_more flag so callers can paginate.
func parseSearchPage(raw string) ([]SearchResult, bool, error) {
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
		HasMore bool `json:"has_more"`
	}
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		return nil, false, err
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
	return out, env.HasMore, nil
}

// snippetMaxChars bounds the search-result snippet length.
const snippetMaxChars = 200
