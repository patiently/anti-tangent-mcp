package tray

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/claudestats"
)

// utilWarnPct is the utilization at/above which a window is flagged ⚠ (close to
// the plan limit).
const utilWarnPct = 80.0

// humanUntil renders a duration as a compact "in …" reset label.
func humanUntil(d time.Duration) string {
	if d <= 0 {
		return "now"
	}
	if d < time.Hour {
		return fmt.Sprintf("in %dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		h := int(d.Hours())
		m := int(d.Minutes()) % 60
		if m == 0 {
			return fmt.Sprintf("in %dh", h)
		}
		return fmt.Sprintf("in %dh%dm", h, m)
	}
	return fmt.Sprintf("in %dd", int(d.Hours())/24)
}

// claudeClock renders an absolute reset time in local time: a clock for resets
// under 24h out, else a date.
func claudeClock(t, now time.Time) string {
	lt := t.Local()
	if t.Sub(now) < 24*time.Hour {
		return lt.Format("15:04")
	}
	return lt.Format("Jan 2")
}

func usd(f float64) string { return fmt.Sprintf("$%.2f", f) }

// humanTokens renders a token count compactly: raw under 1k, "<n>k" under 1M,
// else "<n>M", with one decimal trimmed of a trailing ".0".
func humanTokens(n int64) string {
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

// accountsWithWindows returns the account keys (sorted) that carry at least one
// rate-limit window, i.e. ones the inline summary can render.
func accountsWithWindows(cs claudestats.Stats) []string {
	var keys []string
	for k, a := range cs.Accounts {
		if a.Limits != nil && (a.Limits.FiveHour.HasData() || a.Limits.SevenDay.HasData()) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

// utilPct renders a window's utilization as "26%" (or "91% ⚠" past the warn
// threshold), or "" when utilization is unknown. Shared by the inline and
// detail builders so the threshold/glyph/format live in one place; callers
// supply their own leading separator.
func utilPct(w *claudestats.Window) string {
	if w.Utilization == nil {
		return ""
	}
	s := fmt.Sprintf("%.0f%%", *w.Utilization)
	if *w.Utilization >= utilWarnPct {
		s += " ⚠"
	}
	return s
}

// windowInlineSuffix renders " <util>% [⚠] [· extra] · resets in …" for a
// window, omitting whichever fields are nil.
func windowInlineSuffix(w *claudestats.Window, now time.Time, extra string) string {
	var b strings.Builder
	if p := utilPct(w); p != "" {
		b.WriteString(" " + p)
	}
	if extra != "" {
		fmt.Fprintf(&b, " · %s", extra)
	}
	if w.ResetsAt != nil {
		fmt.Fprintf(&b, " · resets %s", humanUntil(w.ResetsAt.Sub(now)))
	}
	return b.String()
}

// claudeInlineLabels builds the always-visible inline rows: per account with
// limit data, a 5h row and a 7d row (the latter carrying the week cost). Returns
// nil when stats are absent or no account has limit windows. Account name is
// prefixed only when more than one account has windows.
func claudeInlineLabels(cs claudestats.Stats, now time.Time) []string {
	if !cs.Present {
		return nil
	}
	keys := accountsWithWindows(cs)
	multi := len(keys) > 1
	var rows []string
	for _, k := range keys {
		a := cs.Accounts[k]
		prefix := "🤖 "
		if multi {
			prefix = "🤖 " + k + " "
		}
		if w := a.Limits.FiveHour; w.HasData() {
			rows = append(rows, prefix+"5h"+windowInlineSuffix(w, now, ""))
		}
		if w := a.Limits.SevenDay; w.HasData() {
			extra := ""
			if a.Week != nil {
				extra = usd(a.Week.CostUSD)
			}
			rows = append(rows, prefix+"7d"+windowInlineSuffix(w, now, extra))
		}
	}
	if cs.Stale(now) {
		rows = append([]string{"🤖 ⚠ Claude stats stale (" + humanAge(now.Sub(cs.GeneratedAt)) + ")"}, rows...)
	}
	return rows
}

// claudeUsageRows builds the per-account detail rows for the "Claude usage"
// submenu: an account header followed by indented window, usage, active-block,
// and error rows. Returns nil when stats are absent.
func claudeUsageRows(cs claudestats.Stats, now time.Time) []string {
	if !cs.Present {
		return nil
	}
	keys := make([]string, 0, len(cs.Accounts))
	for k := range cs.Accounts {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var rows []string
	for _, k := range keys {
		a := cs.Accounts[k]
		rows = append(rows, accountHeader(k, a))
		if a.Limits != nil {
			if a.Limits.Error != nil {
				rows = append(rows, "· ⚠ limits unavailable ("+*a.Limits.Error+")")
			} else {
				if w := a.Limits.FiveHour; w.HasData() {
					rows = append(rows, "· "+windowDetail("5h", w, now))
				}
				if w := a.Limits.SevenDay; w.HasData() {
					rows = append(rows, "· "+windowDetail("7d", w, now))
				}
			}
		}
		if a.Week != nil {
			rows = append(rows, "· week  "+usageDetail(a.Week))
		}
		if a.Today != nil {
			rows = append(rows, "· today "+usageDetail(a.Today))
		}
		if a.Month != nil {
			rows = append(rows, "· month "+usageDetail(a.Month))
		}
		if b := a.ActiveBlock; b != nil && b.IsActive {
			rows = append(rows, "· "+activeBlockDetail(b))
		}
		if a.Error != nil {
			rows = append(rows, "· ⚠ ccusage error: "+*a.Error)
		}
	}
	return rows
}

func accountHeader(key string, a claudestats.Account) string {
	if a.DisplayName != "" {
		return a.DisplayName
	}
	return key
}

// windowDetail renders the submenu form: "5h  4% · resets 11:46 (in 2h41m)".
func windowDetail(name string, w *claudestats.Window, now time.Time) string {
	var b strings.Builder
	b.WriteString(name)
	if p := utilPct(w); p != "" {
		b.WriteString("  " + p)
	}
	if w.ResetsAt != nil {
		// A reset already in the past (stale snapshot / just-rolled window) would
		// print a misleading past wall-clock; collapse it to "resets now".
		if d := w.ResetsAt.Sub(now); d <= 0 {
			b.WriteString(" · resets now")
		} else {
			fmt.Fprintf(&b, " · resets %s (%s)", claudeClock(*w.ResetsAt, now), humanUntil(d))
		}
	}
	return b.String()
}

func usageDetail(u *claudestats.Usage) string {
	return fmt.Sprintf("%s · %s tok", usd(u.CostUSD), humanTokens(u.TotalTokens))
}

func activeBlockDetail(b *claudestats.ActiveBlock) string {
	s := "block " + usd(b.CostUSD)
	if b.ProjectedCostUSD > 0 {
		s += " → " + usd(b.ProjectedCostUSD) + " proj"
	}
	if b.RemainingMinutes > 0 {
		s += fmt.Sprintf(" · %dm left", b.RemainingMinutes)
	}
	return s
}
