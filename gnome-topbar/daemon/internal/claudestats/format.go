package claudestats

import (
	"fmt"
	"strconv"
	"strings"
)

// UtilWarnPct is the utilization percent at/above which a rate-limit window is
// flagged with a ⚠ marker (and, in the tray icon/bars, filled red). Shared here
// so the tray menu and the /ui/claude web page apply the same threshold.
const UtilWarnPct = 80.0

// HumanTokens renders a token count compactly: raw under 1k, "<n>k" under 1M,
// else "<n>M", trimming a trailing ".0". Shared by the tray and web renderers.
func HumanTokens(n int64) string {
	switch {
	case n < 1000:
		return strconv.FormatInt(n, 10)
	case n < 1_000_000:
		return trimDotZero(fmt.Sprintf("%.1f", float64(n)/1000)) + "k"
	default:
		return trimDotZero(fmt.Sprintf("%.1f", float64(n)/1_000_000)) + "M"
	}
}

func trimDotZero(s string) string { return strings.TrimSuffix(s, ".0") }

// CostTokens renders a usage as "$12.47 · 4.8M tok". Returns "" for a nil usage
// so callers can't panic (matching Window.HasData's nil-safe convention).
func (u *Usage) CostTokens() string {
	if u == nil {
		return ""
	}
	return fmt.Sprintf("$%.2f · %s tok", u.CostUSD, HumanTokens(u.TotalTokens))
}

// UtilLabel renders the window's utilization as "26%", or "91% ⚠" at/above
// UtilWarnPct, or "" when utilization is unknown (or the window is nil). One
// definition so the tray and the web page mark high utilization identically.
func (w *Window) UtilLabel() string {
	if w == nil || w.Utilization == nil {
		return ""
	}
	s := fmt.Sprintf("%.0f%%", *w.Utilization)
	if *w.Utilization >= UtilWarnPct {
		s += " ⚠"
	}
	return s
}
