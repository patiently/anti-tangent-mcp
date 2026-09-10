package planrun

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/patiently/anti-tangent-mcp/internal/blocktext"
)

// Continuation indents for the two shapes of line Render emits. Both feed
// blocktext.EscapeContinuationLines, whose "| " sentinel — not the indent —
// is what stops a continuation line reading as block grammar; the indent only
// decides where the folded text sits.
const (
	// reportValueContIndent aligns under the value column of the report's
	// "  <label>: <value>" header lines ("  plan_run_id:  ", "  plan:         ").
	reportValueContIndent = "                "
	// reportCellContIndent aligns under the first column of a task row, whose
	// "  %-2d " prefix is five characters wide.
	reportCellContIndent = "     "
)

// escapeReportValue folds a header-line value; escapeReportCell folds a table
// cell. EVERY caller-reachable string Render puts into its output goes through
// one of them — see the comment on Render.
func escapeReportValue(s string) string {
	return blocktext.EscapeContinuationLines(s, reportValueContIndent)
}

func escapeReportCell(s string) string {
	return blocktext.EscapeContinuationLines(s, reportCellContIndent)
}

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
		// The evidence is what distinguishes a skip a reader can check from
		// one they cannot. Omitting it here would leave the ledger showing
		// only the caller's own sentence.
		if row.Codescene != nil && strings.TrimSpace(row.Codescene.SkipEvidence) != "" {
			return "skipped (" + reason + ": " + strings.TrimSpace(row.Codescene.SkipEvidence) + ")"
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
//
// Its output becomes plan_run_report's summary_block, so it lands in a
// tool_result that plugin/anti-tangent-guard's hook scans line-by-line for
// "anti-tangent envelope" / "tool:" / "verdict:" — the hook does not care
// which tool or which formatter produced a chunk of text. This report's own
// header is deliberately different ("anti-tangent plan run report"), but that
// buys nothing on its own: a newline inside any value it renders would put
// the NEXT physical line at column 0, where it can read as that grammar. So
// every caller-reachable value below is folded through escapeReportValue or
// escapeReportCell first. It is hygiene, not a security boundary — see
// blocktext.EscapeContinuationLines.
func Render(r *Run) string {
	t := Totals(r)
	var b strings.Builder
	b.WriteString("anti-tangent plan run report\n")
	fmt.Fprintf(&b, "  plan_run_id:  %s\n", escapeReportValue(r.ID))
	fmt.Fprintf(&b, "  plan:         %s / %s\n", escapeReportValue(r.PlanVerdict), escapeReportValue(r.PlanQuality))
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
		// Escape AFTER truncation, and escape codesceneCell's COMPOSED output
		// rather than its inputs: one call then covers every free-text field
		// that can reach the cell — SkipReason, QualityGate, and the
		// CategoryCounts map's keys — including any added later.
		fmt.Fprintf(&b, "  %-2d %-*s  %-10s %s\n", row.Index, width,
			escapeReportCell(title), escapeReportCell(at), escapeReportCell(codesceneCell(row)))
	}

	fmt.Fprintf(&b, "\n  codescene: %d run, %d skipped, %d missing\n",
		t.CodesceneRan, t.CodesceneSkipped, t.CodesceneMissing)
	fmt.Fprintf(&b, "  net problem points across run: %+.1f\n", t.NetPP)
	if n := r.TaskCount - len(r.Rows); n > 0 {
		fmt.Fprintf(&b, "  %d task(s) in the plan were never dispatched\n", n)
	} else if len(r.Rows) > r.TaskCount {
		// Reachable when a task is re-dispatched after its first subagent died:
		// validate_task_spec runs again with the same plan_run_id and AppendRow
		// stamps a new row rather than refusing the append (a re-dispatch is
		// legitimate history, not an error). Without this branch the negative
		// n above is silently skipped and "tasks: 4 of 3 completed" prints with
		// no explanation.
		fmt.Fprintf(&b, "  %d rows for %d tasks — includes re-dispatched or duplicate attempts\n", len(r.Rows), r.TaskCount)
	}
	return b.String()
}
