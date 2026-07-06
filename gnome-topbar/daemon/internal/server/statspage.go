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

// heading renders "<hN>text</hN>" with the text escaped, so callers can nest a
// table at the correct outline depth (h2 for a top-level section, h3 for a
// sub-section under an existing h2) instead of a hardcoded level.
func heading(level int, text string) string {
	return fmt.Sprintf("<h%d>%s</h%d>", level, esc(text), level)
}

// kvTable renders labelled rows as a two-column table under a level-N heading.
// The label column is a <th scope="row"> for screen-reader row association, and
// the table is wrapped in an overflow-x container so a wide value scrolls inside
// the table rather than pushing the page body.
func kvTable(level int, title string, rows [][2]string) string {
	if len(rows) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(heading(level, title) + `<div class="tbl"><table>`)
	for _, r := range rows {
		b.WriteString(`<tr><th scope="row">` + esc(r[0]) + `</th><td>` + esc(r[1]) + `</td></tr>`)
	}
	b.WriteString(`</table></div>`)
	return b.String()
}

// histTable renders a count map (sorted by descending count then key) under a
// level-N heading, with the same th/overflow treatment as kvTable.
func histTable(level int, title string, m map[string]int) string {
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
	rows := make([][2]string, len(items))
	for i, it := range items {
		rows[i] = [2]string{it.k, strconv.Itoa(it.v)}
	}
	return kvTable(level, title, rows)
}

// renderStatsPage renders the anti-tangent rollup aggregates + CodeScene block.
func renderStatsPage(at atstats.Stats) string {
	if !at.Present {
		return pageShell("Stats", `<h1>Stats</h1><p class="muted">No anti-tangent stats yet (rollup.json absent).</p>`)
	}
	var b strings.Builder
	b.WriteString(`<h1>anti-tangent stats</h1>`)
	// The window timestamps come from a Go time layout (no HTML-special chars), so
	// they don't need escaping.
	fmt.Fprintf(&b, `<p class="muted">%d calls · window %s → %s</p>`,
		at.TotalCalls, at.WindowStart.Format("Jan 2 15:04"), at.WindowEnd.Format("Jan 2 15:04"))
	b.WriteString(kvTable(2, "Verdicts", [][2]string{
		{"pass", fmt.Sprintf("%.0f%% (%d)", at.PassPct, at.VerdictCounts["pass"])},
		{"warn", fmt.Sprintf("%.0f%% (%d)", at.WarnPct, at.VerdictCounts["warn"])},
		{"fail", fmt.Sprintf("%.0f%% (%d)", at.FailPct, at.VerdictCounts["fail"])},
	}))
	b.WriteString(kvTable(2, "Throughput", [][2]string{
		{"findings/call", fmt.Sprintf("%.2f", at.FindingsPerCall)},
		{"review p50", fmt.Sprintf("%d ms", at.ReviewMSP50)},
		{"review p95", fmt.Sprintf("%d ms", at.ReviewMSP95)},
		{"cache hit", fmt.Sprintf("%.0f%%", at.CacheHitRate*100)},
		{"partial", fmt.Sprintf("%.0f%%", at.PartialRate*100)},
	}))
	b.WriteString(histTable(2, "Per tool", at.PerTool))
	b.WriteString(histTable(2, "Severity", at.SeverityHistogram))
	b.WriteString(histTable(2, "Categories", at.CategoryHistogram))
	b.WriteString(histTable(2, "Model usage", at.ModelUsage))
	b.WriteString(`<h2>CodeScene</h2>`)
	if cs := at.CodeScene; cs != nil {
		b.WriteString(kvTable(3, "Code Health", [][2]string{
			{"latest score", fmt.Sprintf("%.1f", cs.LatestScore)},
			{"latest delta", fmt.Sprintf("%+.1f (%s)", cs.LatestDelta, cs.LatestTrend)},
			{"score p50", fmt.Sprintf("%.1f", cs.ScoreP50)},
			{"runs", strconv.Itoa(cs.Runs)},
			{"reg / imp / neutral", fmt.Sprintf("%d / %d / %d", cs.Regressions, cs.Improvements, cs.Neutral)},
		}))
		b.WriteString(histTable(3, "CodeScene categories", cs.CategoryHistogram))
	} else {
		b.WriteString(`<p class="muted">No data yet. Append <code>analyze_change_set</code> records to <code>codescene-events.jsonl</code>; see <code>docs/team-setup/codescene-stats.md</code>.</p>`)
	}
	if at.Summary != "" {
		// .snippet preserves newlines (white-space:pre-wrap) so the multi-line
		// summary.md excerpt doesn't collapse into one run-on paragraph.
		b.WriteString(`<h2>Summary</h2><p class="snippet">` + esc(at.Summary) + `</p>`)
	}
	return pageShell("Stats", b.String())
}

// winStr renders a rate-limit window as "<util>% [⚠] · resets <clock>" — the util
// label (incl. the ⚠ at/above claudestats.UtilWarnPct) matches the tray. A reset
// already in the past collapses to "resets now" rather than a misleading wall-clock.
func winStr(w *claudestats.Window, now time.Time) string {
	s := w.UtilLabel()
	if s == "" {
		s = "—"
	}
	if w.ResetsAt != nil {
		if w.ResetsAt.After(now) {
			s += " · resets " + w.ResetsAt.Local().Format("Jan 2 15:04")
		} else {
			s += " · resets now"
		}
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
		b.WriteString(`<p class="muted">⚠ stats stale (generated ` + cs.GeneratedAt.Format("Jan 2 15:04") + `)</p>`)
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
			urows = append(urows, [2]string{"today", a.Today.CostTokens()})
		}
		if a.Week != nil {
			urows = append(urows, [2]string{"week", a.Week.CostTokens()})
		}
		if a.Month != nil {
			urows = append(urows, [2]string{"month", a.Month.CostTokens()})
		}
		b.WriteString(kvTable(3, "Usage", urows))
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
				b.WriteString(kvTable(3, "Rate limits", lrows))
			}
		}
		if a.Error != nil {
			b.WriteString(`<p class="muted">⚠ ccusage error: ` + esc(*a.Error) + `</p>`)
		}
	}
	return pageShell("Claude usage", b.String())
}
