// Package tray renders the daemon snapshot as a StatusNotifierItem tray menu.
// BuildMenu is a pure function (no DBus) so it can be unit-tested; tray.go maps
// its output onto fyne.io/systray items.
package tray

import (
	"fmt"
	"strings"
	"time"

	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/state"
)

type RowKind int

const (
	RowHeader    RowKind = iota // section / now-working header (disabled)
	RowPR                       // a PR (clickable; has URL)
	RowTodo                     // a todo (disabled)
	RowStat                     // a stats line (disabled)
	RowSourceErr                // a "source error" line (disabled)
	RowSeparator
	RowAction // Refresh / Quit
)

type Row struct {
	Kind     RowKind
	Label    string
	URL      string // RowPR only
	Disabled bool
}

// BuildMenu turns a snapshot into logical menu rows. `now` drives the
// currently-working-on age, injected for testability.
func BuildMenu(s state.Snapshot, now time.Time) []Row {
	var rows []Row
	add := func(k RowKind, label string) { rows = append(rows, Row{Kind: k, Label: label, Disabled: k != RowPR && k != RowAction}) }

	// currently-working-on
	nw := s.NowWorking
	if nw.NotFound || strings.TrimSpace(nw.Body) == "" {
		add(RowHeader, "🛠 Currently working on — (not set up)")
	} else {
		age := ""
		if nw.HasUpdated {
			age = " (⟳ " + humanAge(now.Sub(nw.Updated)) + ")"
		}
		add(RowHeader, "🛠 "+oneLine(nw.Body, 80)+age)
	}
	add(RowSeparator, "")

	// PRs
	add(RowHeader, fmt.Sprintf("🔵 Review requested (%d)", len(s.PRs.ReviewRequested)))
	for _, pr := range s.PRs.ReviewRequested {
		rows = append(rows, Row{Kind: RowPR, Label: prLabel(pr.Repo, pr.Number, pr.Title), URL: pr.URL})
	}
	add(RowHeader, fmt.Sprintf("🟣 My open PRs (%d)", len(s.PRs.Authored)))
	for _, pr := range s.PRs.Authored {
		rows = append(rows, Row{Kind: RowPR, Label: prLabel(pr.Repo, pr.Number, pr.Title), URL: pr.URL})
	}
	add(RowSeparator, "")

	// todos
	add(RowHeader, fmt.Sprintf("✅ Due / overdue (%d)", len(s.Todos.Due)))
	for _, td := range s.Todos.Due {
		add(RowTodo, "⚠ "+oneLine(td.Text, 80))
	}
	add(RowHeader, fmt.Sprintf("Active (%d)", len(s.Todos.Active)))
	for _, td := range s.Todos.Active {
		add(RowTodo, "  "+oneLine(td.Text, 80))
	}

	// per-source errors
	for name, st := range s.Sources {
		if !st.OK {
			add(RowSourceErr, "⚠ "+name+": "+oneLine(st.Error, 60))
		}
	}

	// anti-tangent stats (only when present)
	if at := s.AntiTangent; at.Present {
		add(RowSeparator, "")
		add(RowStat, fmt.Sprintf("🛡 anti-tangent — %d calls · %.0f%%/%.0f%%/%.0f%% · top %s · p95 %dms",
			at.TotalCalls, at.PassPct, at.WarnPct, at.FailPct, dash(at.TopCategory), at.ReviewMSP95))
		if cs := at.CodeScene; cs != nil {
			add(RowStat, fmt.Sprintf("📊 CodeScene — score %.1f (%s) · %dr/%di",
				cs.LatestScore, dash(cs.LatestTrend), cs.Regressions, cs.Improvements))
		}
	}

	// actions
	add(RowSeparator, "")
	rows = append(rows, Row{Kind: RowAction, Label: "↻ Refresh"})
	rows = append(rows, Row{Kind: RowAction, Label: "Quit"})
	return rows
}

func prLabel(repo string, num int, title string) string {
	return fmt.Sprintf("%s #%d  %s", repo, num, oneLine(title, 60))
}

func oneLine(s string, max int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if len(s) > max {
		return s[:max-1] + "…"
	}
	return s
}

func dash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func humanAge(d time.Duration) string {
	if d < time.Minute {
		return "just now"
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}
