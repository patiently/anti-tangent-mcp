// Package tray renders the daemon snapshot as a StatusNotifierItem tray menu.
// The label builders here are pure (no DBus) and unit-tested; tray.go assembles
// the fyne.io/systray item tree (sections + collapsible submenus) from the
// snapshot using them.
package tray

import (
	"fmt"
	"strings"
	"time"

	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/atstats"
	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/bm"
)

const (
	labelWidth   = 80 // default max label width (now-working, todos, error reasons)
	labelWidthPR = 60 // PR titles: the "owner/repo #N  " prefix takes the rest
)

// nowWorkingLabel renders the currently-working-on header. `now` drives the age
// (injected for testability).
func nowWorkingLabel(nw bm.NowWorking, now time.Time) string {
	if nw.NotFound || strings.TrimSpace(nw.Body) == "" {
		return "🛠 Currently working on — (not set up)"
	}
	age := ""
	if nw.HasUpdated {
		age = " (⟳ " + humanAge(now.Sub(nw.Updated)) + ")"
	}
	return "🛠 " + oneLine(nw.Body, labelWidth) + age
}

func antiTangentLabel(at atstats.Stats) string {
	return fmt.Sprintf("🛡 anti-tangent — %d calls · %.0f%%/%.0f%%/%.0f%% · top %s · p95 %dms",
		at.TotalCalls, at.PassPct, at.WarnPct, at.FailPct, dash(at.TopCategory), at.ReviewMSP95)
}

func codeSceneLabel(cs *atstats.CodeSceneStats) string {
	return fmt.Sprintf("📊 CodeScene — score %.1f (%s) · %dr/%di",
		cs.LatestScore, dash(cs.LatestTrend), cs.Regressions, cs.Improvements)
}

func prLabel(repo string, num int, title string) string {
	return fmt.Sprintf("%s #%d  %s", repo, num, oneLine(title, labelWidthPR))
}

// oneLine flattens to a single line and truncates to max runes (not bytes, so
// it never splits a multi-byte rune and produces invalid UTF-8).
func oneLine(s string, max int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if r := []rune(s); len(r) > max {
		return string(r[:max-1]) + "…"
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
