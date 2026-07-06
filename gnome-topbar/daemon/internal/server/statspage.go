package server

import (
	"fmt"
	"html"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/atstats"
	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/claudestats"
)

func esc(s string) string { return html.EscapeString(s) }

// kvTable renders labelled rows as a two-column table.
func kvTable(title string, rows [][2]string) string {
	if len(rows) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<h2>` + esc(title) + `</h2><table>`)
	for _, r := range rows {
		b.WriteString(`<tr><td>` + esc(r[0]) + `</td><td>` + esc(r[1]) + `</td></tr>`)
	}
	b.WriteString(`</table>`)
	return b.String()
}

// histTable renders a count map sorted by descending count then key.
func histTable(title string, m map[string]int) string {
	if len(m) == 0 {
		return ""
	}
	type kv struct {
		k string
		v int
	}
	items := make([]kv, 0, len(m))
	for k, v := range m {
		items = append(items, kv{k, v})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].v != items[j].v {
			return items[i].v > items[j].v
		}
		return items[i].k < items[j].k
	})
	var b strings.Builder
	b.WriteString(`<h2>` + esc(title) + `</h2><table>`)
	for _, it := range items {
		b.WriteString(`<tr><td>` + esc(it.k) + `</td><td>` + strconv.Itoa(it.v) + `</td></tr>`)
	}
	b.WriteString(`</table>`)
	return b.String()
}

// renderStatsPage renders the anti-tangent rollup aggregates + CodeScene block.
func renderStatsPage(at atstats.Stats) string {
	if !at.Present {
		return pageShell("Stats", `<h1>Stats</h1><p class="muted">No anti-tangent stats yet (rollup.json absent).</p>`)
	}
	var b strings.Builder
	b.WriteString(`<h1>anti-tangent stats</h1>`)
	fmt.Fprintf(&b, `<p class="muted">%d calls · window %s → %s</p>`,
		at.TotalCalls, esc(at.WindowStart.Format("Jan 2 15:04")), esc(at.WindowEnd.Format("Jan 2 15:04")))
	b.WriteString(kvTable("Verdicts", [][2]string{
		{"pass", fmt.Sprintf("%.0f%% (%d)", at.PassPct, at.VerdictCounts["pass"])},
		{"warn", fmt.Sprintf("%.0f%% (%d)", at.WarnPct, at.VerdictCounts["warn"])},
		{"fail", fmt.Sprintf("%.0f%% (%d)", at.FailPct, at.VerdictCounts["fail"])},
	}))
	b.WriteString(kvTable("Throughput", [][2]string{
		{"findings/call", fmt.Sprintf("%.2f", at.FindingsPerCall)},
		{"review p50", fmt.Sprintf("%d ms", at.ReviewMSP50)},
		{"review p95", fmt.Sprintf("%d ms", at.ReviewMSP95)},
		{"cache hit", fmt.Sprintf("%.0f%%", at.CacheHitRate*100)},
		{"partial", fmt.Sprintf("%.0f%%", at.PartialRate*100)},
	}))
	b.WriteString(histTable("Per tool", at.PerTool))
	b.WriteString(histTable("Severity", at.SeverityHistogram))
	b.WriteString(histTable("Categories", at.CategoryHistogram))
	b.WriteString(histTable("Model usage", at.ModelUsage))
	b.WriteString(`<h2>CodeScene</h2>`)
	if cs := at.CodeScene; cs != nil {
		b.WriteString(kvTable("Code Health", [][2]string{
			{"latest score", fmt.Sprintf("%.1f", cs.LatestScore)},
			{"latest delta", fmt.Sprintf("%+.1f (%s)", cs.LatestDelta, esc(cs.LatestTrend))},
			{"score p50", fmt.Sprintf("%.1f", cs.ScoreP50)},
			{"runs", strconv.Itoa(cs.Runs)},
			{"reg / imp / neutral", fmt.Sprintf("%d / %d / %d", cs.Regressions, cs.Improvements, cs.Neutral)},
		}))
		b.WriteString(histTable("CodeScene categories", cs.CategoryHistogram))
	} else {
		b.WriteString(`<p class="muted">No data yet. Append <code>analyze_change_set</code> records to <code>codescene-events.jsonl</code>; see <code>docs/team-setup/codescene-stats.md</code>.</p>`)
	}
	if at.Summary != "" {
		b.WriteString(`<h2>Summary</h2><p>` + esc(at.Summary) + `</p>`)
	}
	return pageShell("Stats", b.String())
}

// humanTokensS renders a token count compactly (local copy; tray has its own).
func humanTokensS(n int64) string {
	switch {
	case n < 1000:
		return strconv.FormatInt(n, 10)
	case n < 1_000_000:
		return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(n)/1000), ".0") + "k"
	default:
		return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(n)/1_000_000), ".0") + "M"
	}
}

func usageStr(u *claudestats.Usage) string {
	return fmt.Sprintf("$%.2f · %s tok", u.CostUSD, humanTokensS(u.TotalTokens))
}

func winStr(w *claudestats.Window, now time.Time) string {
	s := "—"
	if w.Utilization != nil {
		s = fmt.Sprintf("%.0f%%", *w.Utilization)
	}
	if w.ResetsAt != nil && w.ResetsAt.After(now) {
		s += " · resets " + w.ResetsAt.Local().Format("Jan 2 15:04")
	}
	return s
}

// renderClaudePage renders per-account Claude usage + rate limits.
func renderClaudePage(cs claudestats.Stats, now time.Time) string {
	if !cs.Present {
		return pageShell("Claude usage", `<h1>Claude usage</h1><p class="muted">No claude-stats.json present.</p>`)
	}
	var b strings.Builder
	b.WriteString(`<h1>Claude usage</h1>`)
	if cs.Stale(now) {
		b.WriteString(`<p class="muted">⚠ stats stale (generated ` + esc(cs.GeneratedAt.Format("Jan 2 15:04")) + `)</p>`)
	}
	keys := make([]string, 0, len(cs.Accounts))
	for k := range cs.Accounts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		a := cs.Accounts[k]
		name := a.DisplayName
		if name == "" {
			name = k
		}
		b.WriteString(`<h2>` + esc(name) + `</h2>`)
		var urows [][2]string
		if a.Today != nil {
			urows = append(urows, [2]string{"today", usageStr(a.Today)})
		}
		if a.Week != nil {
			urows = append(urows, [2]string{"week", usageStr(a.Week)})
		}
		if a.Month != nil {
			urows = append(urows, [2]string{"month", usageStr(a.Month)})
		}
		b.WriteString(kvTable("Usage", urows))
		if a.Limits != nil {
			if a.Limits.Error != nil {
				b.WriteString(`<p class="muted">⚠ limits unavailable (` + esc(*a.Limits.Error) + `)</p>`)
			} else {
				var lrows [][2]string
				if w := a.Limits.FiveHour; w.HasData() {
					lrows = append(lrows, [2]string{"5h", winStr(w, now)})
				}
				if w := a.Limits.SevenDay; w.HasData() {
					lrows = append(lrows, [2]string{"weekly", winStr(w, now)})
				}
				mnames := make([]string, 0, len(a.Limits.WeeklyModels))
				for mn := range a.Limits.WeeklyModels {
					mnames = append(mnames, mn)
				}
				sort.Strings(mnames)
				for _, mn := range mnames {
					if w := a.Limits.WeeklyModels[mn]; w.HasData() {
						lrows = append(lrows, [2]string{mn, winStr(w, now)})
					}
				}
				b.WriteString(kvTable("Rate limits", lrows))
			}
		}
		if a.Error != nil {
			b.WriteString(`<p class="muted">⚠ ccusage error: ` + esc(*a.Error) + `</p>`)
		}
	}
	return pageShell("Claude usage", b.String())
}
