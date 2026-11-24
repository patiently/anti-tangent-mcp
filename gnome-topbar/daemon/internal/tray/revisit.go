package tray

import (
	"fmt"
	"math"

	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/state"
	"github.com/patiently/anti-tangent-mcp/scorecard"
)

func revisitTitle(n, gated int) string {
	switch {
	case n == 0 && gated == 0:
		return ""
	case n == 0:
		return fmt.Sprintf("🔁 Cycle review: none due · %d too small to judge", gated)
	case gated == 0:
		return fmt.Sprintf("🔁 Cycle review due (%d)", n)
	default:
		return fmt.Sprintf("🔁 Cycle review due (%d) · %d too small to judge", n, gated)
	}
}

func revisitLabel(s scorecard.Signal) string {
	if s.Kind == scorecard.RevisitNewModel || s.Cohort == nil {
		return "new model: " + s.Model
	}
	arrow := "↑"
	if s.Direction == "down" {
		arrow = "↓"
	}
	l := fmt.Sprintf("%s %s %s · %s / %s", s.Kind, arrow, s.Cohort.Kind, s.Cohort.ImplementerModel, s.Cohort.ReviewModel)
	if s.Source != "" {
		l += " · " + s.Source
	}
	if s.Recent != nil && s.Baseline != nil {
		l += fmt.Sprintf(" — %d%% vs %d%%", pct(s.Recent.Value), pct(s.Baseline.Value))
	}
	return l
}

func pct(v float64) int { return int(math.Round(v * 100)) }

// revisitMenu is the cycle-review submenu for a snapshot: shown only when the
// server's revisit state is known and there is something to say.
func revisitMenu(snap state.Snapshot) (title string, show bool, ids, labels []string) {
	title = revisitTitle(len(snap.Revisit), snap.RevisitGated)
	if !snap.RevisitKnown || title == "" {
		return "", false, nil, nil
	}
	for _, s := range snap.Revisit {
		ids = append(ids, s.ID)
		labels = append(labels, revisitLabel(s))
	}
	return title, true, ids, labels
}

// AckRevisitIDs is the seen-store key a revisit ack is filed under.
func AckRevisitIDs(id string) []string { return []string{"revisit:" + id} }
