package planrun

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// RunTotals is the aggregate line of a plan-run report.
type RunTotals struct {
	Tasks            int     `json:"tasks"`
	Completed        int     `json:"completed"`
	Pass             int     `json:"pass"`
	Warn             int     `json:"warn"`
	Fail             int     `json:"fail"`
	Incomplete       int     `json:"incomplete"`
	CodesceneRan     int     `json:"codescene_ran"`
	CodesceneSkipped int     `json:"codescene_skipped"`
	CodesceneMissing int     `json:"codescene_missing"`
	NetPP            float64 `json:"net_pp"`
}

// Totals aggregates a run's rows. Incomplete counts rows the plan created a
// session for but which never reported completion; TaskCount minus the row
// count is a separate thing — tasks never dispatched at all.
func Totals(r *Run) RunTotals {
	t := RunTotals{Tasks: r.TaskCount}
	for _, row := range r.Rows {
		switch row.PostVerdict {
		case "pass":
			t.Pass++
			t.Completed++
		case "warn":
			t.Warn++
			t.Completed++
		case "fail":
			t.Fail++
			t.Completed++
		default:
			t.Incomplete++
		}
		switch row.CodesceneState {
		case StateRan:
			t.CodesceneRan++
		case StateSkipped:
			t.CodesceneSkipped++
		default:
			t.CodesceneMissing++
		}
		if row.Codescene != nil && row.Codescene.Ran {
			t.NetPP += row.Codescene.NetPP
		}
	}
	return t
}

// codesceneCell renders the CodeScene column for one row.
func codesceneCell(row TaskRow) string {
	switch row.CodesceneState {
	case StateRan:
		if row.Codescene == nil {
			return "ran"
		}
		return fmt.Sprintf("%-7s %+.1fpp%s", row.Codescene.QualityGate, row.Codescene.NetPP,
			topCategories(row.Codescene.CategoryCounts))
	case StateSkipped:
		reason := "no reason given"
		if row.Codescene != nil && strings.TrimSpace(row.Codescene.SkipReason) != "" {
			reason = strings.TrimSpace(row.Codescene.SkipReason)
		}
		return "skipped (" + reason + ")"
	default:
		return "not run"
	}
}

// topCategories renders the highest-count CodeScene categories for the report
// column, deterministically. Distinct wrapping from TopCategories (double
// leading space, no trailing period context) because this is a table cell,
// not inline finding prose.
func topCategories(counts map[string]int) string {
	s := topCategoriesList(counts, 2)
	if s == "" {
		return ""
	}
	return "  (" + s + ")"
}

// TopCategories renders up to max CodeScene categories, highest count first,
// ties broken alphabetically so output is stable, as " (a x1, b x2)" suitable
// for direct interpolation into finding text. Empty string when counts is
// empty. This is the exported counterpart of topCategories: the two callers
// (this package's report column, and mcpsrv's CodeScene regression finding)
// want different max values and different wrapping, so the ranking logic is
// shared via topCategoriesList and each caller wraps it to taste.
func TopCategories(counts map[string]int, max int) string {
	s := topCategoriesList(counts, max)
	if s == "" {
		return ""
	}
	return " (" + s + ")"
}

// topCategoriesList is the shared ranking core: up to max categories, highest
// count first, ties broken alphabetically, comma-joined with no wrapping.
// Empty when counts is empty.
func topCategoriesList(counts map[string]int, max int) string {
	if len(counts) == 0 {
		return ""
	}
	type kv struct {
		k string
		v int
	}
	pairs := make([]kv, 0, len(counts))
	for k, v := range counts {
		pairs = append(pairs, kv{k, v})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].v != pairs[j].v {
			return pairs[i].v > pairs[j].v
		}
		return pairs[i].k < pairs[j].k
	})
	if len(pairs) > max {
		pairs = pairs[:max]
	}
	parts := make([]string, len(pairs))
	for i, p := range pairs {
		parts[i] = fmt.Sprintf("%s x%d", p.k, p.v)
	}
	return strings.Join(parts, ", ")
}

// Render produces the paste-ready plan-run report.
func Render(r *Run) string {
	t := Totals(r)
	var b strings.Builder
	b.WriteString("anti-tangent plan run report\n")
	fmt.Fprintf(&b, "  plan_run_id:  %s\n", r.ID)
	fmt.Fprintf(&b, "  plan:         %s / %s\n", r.PlanVerdict, r.PlanQuality)
	fmt.Fprintf(&b, "  tasks: %d of %d completed   pass %d | warn %d | fail %d\n\n",
		t.Completed, t.Tasks, t.Pass, t.Warn, t.Fail)

	// Width and truncation are computed in runes, not bytes: a byte-based
	// slice would split a multi-byte UTF-8 title (an en-dash, an accented
	// character, ...) mid-codepoint, producing garbled output. This mirrors
	// the rune-based truncate() convention in internal/mcpsrv/summary.go.
	width := 4
	for _, row := range r.Rows {
		if n := utf8.RuneCountInString(row.TaskTitle); n > width {
			width = n
		}
	}
	if width > 40 {
		width = 40
	}

	fmt.Fprintf(&b, "  #  %-*s  %-10s %s\n", width, "Task", "AT", "CodeScene")
	for _, row := range r.Rows {
		title := row.TaskTitle
		if n := utf8.RuneCountInString(title); n > width {
			runes := []rune(title)
			title = string(runes[:width-1]) + "…"
		}
		at := row.PostVerdict
		if at == "" {
			at = "incomplete"
		}
		fmt.Fprintf(&b, "  %-2d %-*s  %-10s %s\n", row.Index, width, title, at, codesceneCell(row))
	}

	fmt.Fprintf(&b, "\n  codescene: %d run, %d skipped, %d missing\n",
		t.CodesceneRan, t.CodesceneSkipped, t.CodesceneMissing)
	fmt.Fprintf(&b, "  net problem points across run: %+.1f\n", t.NetPP)
	if n := r.TaskCount - len(r.Rows); n > 0 {
		fmt.Fprintf(&b, "  %d task(s) in the plan were never dispatched\n", n)
	}
	return b.String()
}
