package state

import (
	"crypto/sha1"
	"encoding/hex"
	"strconv"
	"time"

	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/atstats"
	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/bm"
	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/github"
)

type SourceStatus struct {
	OK         bool       `json:"ok"`
	Error      string     `json:"error,omitempty"`
	StaleSince *time.Time `json:"stale_since,omitempty"`
}

type Event struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"` // "review_request" | "todo_due"
	Title string `json:"title"`
	URL   string `json:"url,omitempty"`
	Body  string `json:"body,omitempty"`
}

type Snapshot struct {
	NowWorking bm.NowWorking `json:"now_working"`
	PRs        struct {
		Authored        []github.PR `json:"authored"`
		ReviewRequested []github.PR `json:"review_requested"`
	} `json:"prs"`
	Todos struct {
		Active []bm.TodoItem `json:"active"`
		Due    []bm.TodoItem `json:"due"`
	} `json:"todos"`
	Sources       map[string]SourceStatus `json:"sources"`
	AntiTangent   atstats.Stats           `json:"anti_tangent"`
	UnackedEvents []Event                 `json:"unacked_events"`
	GeneratedAt   time.Time               `json:"generated_at"`
}

// ComputeEvents returns events not yet acked in the store. It does not mark
// them seen (the panel acks after showing).
func ComputeEvents(s *Snapshot, store *Store) []Event {
	var out []Event
	for _, pr := range s.PRs.ReviewRequested {
		id := reviewID(pr)
		if store.IsNew(id) {
			out = append(out, Event{ID: id, Kind: "review_request",
				Title: pr.Repo + " #" + strconv.Itoa(pr.Number), URL: pr.URL, Body: pr.Title})
		}
	}
	for _, td := range s.Todos.Due {
		id := todoID(td)
		if store.IsNew(id) {
			out = append(out, Event{ID: id, Kind: "todo_due", Title: "Todo due", Body: td.Text})
		}
	}
	return out
}

func reviewID(pr github.PR) string { return "pr:" + pr.Repo + "#" + strconv.Itoa(pr.Number) }

func todoID(td bm.TodoItem) string {
	d := "nodate"
	if td.Due != nil {
		d = td.Due.Format("2006-01-02")
	}
	h := sha1.Sum([]byte(td.Text))
	return "todo:" + d + ":" + hex.EncodeToString(h[:])[:8]
}
