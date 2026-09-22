package planrun

import (
	"fmt"
	"sort"
	"strconv"
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

// RunTotals is the aggregate line of a plan-run report. Completed, Pass,
// Warn, Fail and Incomplete count rows that name a plan task; a row that
// names none counts only in Unmatched.
type RunTotals struct {
	Tasks            int     `json:"tasks"`
	Completed        int     `json:"completed"`
	Pass             int     `json:"pass"`
	Warn             int     `json:"warn"`
	Fail             int     `json:"fail"`
	Incomplete       int     `json:"incomplete"`
	NeverDispatched  int     `json:"never_dispatched"`
	Unmatched        int     `json:"unmatched"`
	CodesceneRan     int     `json:"codescene_ran"`
	CodesceneSkipped int     `json:"codescene_skipped"`
	CodesceneMissing int     `json:"codescene_missing"`
	NetPP            float64 `json:"net_pp"`
	Waived           int     `json:"waived"`
	Escalated        int     `json:"escalated"`
}

// Totals aggregates a run's rows. The CodeScene counts cover completed rows
// only: a task still open has not had its chance to run the analysis. NetPP
// is the branch delta rather than a sum over tasks; see branchNetPP.
func Totals(r *Run) RunTotals {
	t := RunTotals{Tasks: r.TaskCount}
	for _, row := range r.Rows {
		t.Waived += row.Waived
		if row.Escalated {
			t.Escalated++
		}
		if row.PostVerdict != "" {
			countCodescene(&t, row)
		}
		if row.Unmatched {
			t.Unmatched++
			continue
		}
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
	}
	t.NeverDispatched = len(neverDispatched(r))
	t.NetPP = branchNetPP(r.Rows)
	return t
}

func countCodescene(t *RunTotals, row TaskRow) {
	switch row.CodesceneState {
	case StateRan:
		t.CodesceneRan++
	case StateSkipped:
		t.CodesceneSkipped++
	default:
		t.CodesceneMissing++
	}
}

// branchNetPP sums, over each distinct base ref, the net problem points of
// the most recently completed row whose analysis ran against it. Each task is
// asked for a branch-versus-base analysis, so rows sharing a base ref report
// the same cumulative change and adding them would count it once per task.
// Rows that named no base ref form one group. Keys are summed in sorted order
// so the float result does not depend on map iteration.
func branchNetPP(rows []TaskRow) float64 {
	latest := map[string]TaskRow{}
	for _, row := range rows {
		if row.Codescene == nil || !row.Codescene.Ran {
			continue
		}
		key := row.Codescene.BaseRef
		if cur, ok := latest[key]; !ok || !row.CompletedAt.Before(cur.CompletedAt) {
			latest[key] = row
		}
	}
	keys := make([]string, 0, len(latest))
	for k := range latest {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sum float64
	for _, k := range keys {
		sum += latest[k].Codescene.NetPP
	}
	return sum
}

// neverDispatched lists, by Index, the plan tasks no row names.
func neverDispatched(r *Run) []int {
	has := matchedTaskIndexes(r.Rows)
	var out []int
	for i := 1; i <= r.TaskCount; i++ {
		if !has[i] {
			out = append(out, i)
		}
	}
	return out
}

// matchedTaskIndexes returns the Index of every row that names a plan task,
// i.e. every row that is not Unmatched.
func matchedTaskIndexes(rows []TaskRow) map[int]bool {
	has := map[int]bool{}
	for _, row := range rows {
		if !row.Unmatched {
			has[row.Index] = true
		}
	}
	return has
}

// verdictCell renders the AT column: the post-task verdict, marked when a
// lightweight call recorded it, or "open" with the pre-task verdict for a
// task that has not completed.
func verdictCell(row TaskRow) string {
	switch {
	case row.PostVerdict != "" && row.Lite:
		return row.PostVerdict + " (lite)"
	case row.PostVerdict != "":
		return row.PostVerdict
	case row.PreVerdict != "":
		return "open (pre: " + row.PreVerdict + ")"
	default:
		return "open"
	}
}

func triesCell(row TaskRow) string {
	if row.Attempts == 0 {
		return "-"
	}
	return strconv.Itoa(row.Attempts)
}

// codesceneCell renders the CodeScene column for one row.
func codesceneCell(row TaskRow) string {
	switch row.CodesceneState {
	case StateRan:
		if row.Codescene == nil {
			return "ran"
		}
		return flattenReportCell(fmt.Sprintf("%-7s %+.1fpp%s", row.Codescene.QualityGate,
			row.Codescene.NetPP, topCategories(row.Codescene.CategoryCounts)))
	case StateSkipped:
		reason := "no reason given"
		if row.Codescene != nil && strings.TrimSpace(row.Codescene.SkipReason) != "" {
			reason = strings.TrimSpace(row.Codescene.SkipReason)
		}
		// The evidence is what distinguishes a skip a reader can check from
		// one they cannot. Omitting it here would leave the ledger showing
		// only the caller's own sentence — but it arrives capped at 2,000
		// runes, several screens of one cell in a table whose other columns
		// are 40 wide, so the cell carries the head of it and the ledger
		// keeps the whole.
		if row.Codescene != nil && strings.TrimSpace(row.Codescene.SkipEvidence) != "" {
			// Flattened BEFORE the cap so the cap counts runes the reader
			// actually sees: a line break left in would spend none of the
			// budget and buy a whole extra row instead.
			ev := fitRunes(oneLine(strings.TrimSpace(row.Codescene.SkipEvidence)), reportCellEvidenceRunes)
			return flattenReportCell("skipped (" + reason + ": " + ev + ")")
		}
		return flattenReportCell("skipped (" + reason + ")")
	default:
		return "not run"
	}
}

// rulingsCell renders how controller rulings shaped a task: how many findings
// they waived on its last validate_completion, and whether any of its calls
// escalated.
func rulingsCell(row TaskRow) string {
	switch {
	case row.Waived > 0 && row.Escalated:
		return fmt.Sprintf("%d waived, escalated", row.Waived)
	case row.Waived > 0:
		return fmt.Sprintf("%d waived", row.Waived)
	case row.Escalated:
		return "escalated"
	default:
		return "-"
	}
}

// oneLine replaces every line break in s with a single space. Idempotent, so
// a value that has already been through it can pass through
// flattenReportCell again unchanged.
func oneLine(s string) string {
	return strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ").Replace(s)
}

// flattenReportCell makes one composed table cell printable as one row.
//
// Every free-text field that can reach a cell is caller-supplied and several
// carry a tool's own output verbatim — a CodeScene skip reason, the failing
// tool's error text, a category key. Two characters in that text decide
// whether the row survives: a line break puts the remainder at column 0 of
// the next physical line, and a "|" is the first character of the sentinel
// EscapeContinuationLines writes at the head of a folded line, so an unmarked
// one lets tool text read as a fold this renderer inserted. Line breaks
// become spaces; a pipe is marked "\|".
//
// Applied to the COMPOSED cell rather than to its inputs, for the reason
// given on Render's escape calls: one call covers every field that can reach
// the cell, including any added later. It runs AFTER the display cap, so a
// mark it adds can never be the half of a sequence the cut discards.
//
// This is display hygiene, not a reversible encoding: a pipe that was already
// written "\|" comes out "\\|", and nothing reads a cell back.
func flattenReportCell(s string) string {
	return strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ", "|", `\|`).Replace(s)
}

// reportCellEvidenceRunes is how much of a skip evidence a table cell shows.
const reportCellEvidenceRunes = 200

// fitRunes shortens s so the RESULT is at most n runes, spending the last of
// them on an ellipsis so a reader can tell a cut cell from a complete short
// value: n-1 runes of s plus the marker. internal/codescene has a
// truncateRunes whose bound counts only the RETAINED runes and appends the
// marker on top, so a cap moved between the two shifts by one rune.
func fitRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
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

	fmt.Fprintf(&b, "  #  %-*s  %-16s %-5s %-20s %s\n", width, "Task", "AT", "Tries", "Rulings", "CodeScene")
	for _, row := range r.Rows {
		title := row.TaskTitle
		if n := utf8.RuneCountInString(title); n > width {
			runes := []rune(title)
			title = string(runes[:width-1]) + "…"
		}
		// Escape AFTER truncation, and escape codesceneCell's COMPOSED output
		// rather than its inputs: one call then covers every free-text field
		// that can reach the cell — SkipReason, QualityGate, and the
		// CategoryCounts map's keys — including any added later.
		fmt.Fprintf(&b, "  %-2d %-*s  %-16s %-5s %-20s %s\n", row.Index, width,
			escapeReportCell(title), escapeReportCell(verdictCell(row)), triesCell(row),
			rulingsCell(row), escapeReportCell(codesceneCell(row)))
	}

	fmt.Fprintf(&b, "\n  codescene: %d run, %d skipped, %d missing\n",
		t.CodesceneRan, t.CodesceneSkipped, t.CodesceneMissing)
	fmt.Fprintf(&b, "  rulings: %d findings waived, %d tasks escalated\n", t.Waived, t.Escalated)
	fmt.Fprintf(&b, "  branch net problem points (latest per base ref): %+.1f\n", t.NetPP)
	if missing := neverDispatched(r); len(missing) > 0 {
		renderNeverDispatched(&b, r, missing)
	}
	if t.Unmatched > 0 {
		fmt.Fprintf(&b, "  unmatched: %d row(s) named no plan task by task_index or title\n", t.Unmatched)
	}
	return b.String()
}

// renderNeverDispatched writes missing's plan tasks to b, one per line,
// labeled by the plan's heading for that task or "task N" when the plan
// carries none (a run created before headings were tracked).
func renderNeverDispatched(b *strings.Builder, r *Run, missing []int) {
	fmt.Fprintf(b, "  never dispatched: %d\n", len(missing))
	for _, idx := range missing {
		label := r.taskTitle(idx)
		if strings.TrimSpace(label) == "" {
			label = fmt.Sprintf("task %d", idx)
		}
		fmt.Fprintf(b, "    %-2d %s\n", idx, escapeReportCell(label))
	}
}
