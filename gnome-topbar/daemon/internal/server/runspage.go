package server

import (
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/patiently/anti-tangent-mcp/scorecard"
)

// runsHandler serves /ui/runs: the overview for ?scope= (default "mine"), or
// a single run's detail when ?run= is present.
func runsHandler(p Provider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		scope := q.Get("scope")
		if scope == "" {
			scope = "mine"
		}
		v := p.RunsView(scope)
		if hash := q.Get("run"); hash != "" {
			writeHTML(w, renderRunDetail(v, hash, q.Get("publisher")))
			return
		}
		writeHTML(w, renderRunsPage(v))
	}
}

var configuredRoles = []string{"plan", "pre", "mid", "post", "worker"}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// scopeHref renders a scope value for a query string, keeping a literal
// "user:" prefix (it is never itself part of a publisher name) and escaping
// only the publisher name that follows it.
func scopeHref(scope string) string {
	if name, ok := strings.CutPrefix(scope, "user:"); ok {
		return "user:" + url.QueryEscape(name)
	}
	return url.QueryEscape(scope)
}

func fmtRate(r scorecard.Rate) string {
	if r.N == 0 {
		return "—"
	}
	return fmt.Sprintf("%.0f%% (%.0f–%.0f%%, n=%d)", r.Value*100, r.Lo*100, r.Hi*100, r.N)
}

// dataTablePlain renders a header row and cell rows as a full HTML table
// (unlike kvTable's label/value pairs), with the same overflow-x wrapper.
// Every cell goes through esc.
func dataTablePlain(headers []string, rows [][]string) string {
	if len(rows) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<div class="tbl"><table><tr>`)
	for _, h := range headers {
		b.WriteString(`<th>` + esc(h) + `</th>`)
	}
	b.WriteString(`</tr>`)
	for _, r := range rows {
		b.WriteString(`<tr>`)
		for _, c := range r {
			b.WriteString(`<td>` + esc(c) + `</td>`)
		}
		b.WriteString(`</tr>`)
	}
	b.WriteString(`</table></div>`)
	return b.String()
}

// scopeNav links Mine, Team and one entry per known publisher; the active
// scope renders as plain text rather than a link.
func scopeNav(v RunsView) string {
	type item struct{ label, scope string }
	items := []item{{"Mine", "mine"}, {"Team", "team"}}
	for _, pub := range v.Publishers {
		items = append(items, item{pub, "user:" + pub})
	}
	parts := make([]string, 0, len(items))
	for _, it := range items {
		if it.scope == v.Scope {
			parts = append(parts, "<strong>"+esc(it.label)+"</strong>")
		} else {
			parts = append(parts, `<a href="/ui/runs?scope=`+scopeHref(it.scope)+`">`+esc(it.label)+`</a>`)
		}
	}
	return `<p class="muted">` + strings.Join(parts, " · ") + `</p>`
}

// nonCanonicalVerdicts returns m's keys not in seen, sorted — the verdicts
// verdictMix doesn't already know a fixed display order for.
func nonCanonicalVerdicts(m map[string]int, seen map[string]bool) []string {
	var extra []string
	for k := range m {
		if !seen[k] {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)
	return extra
}

// verdictMix renders a verdict-count map as "pass 3 · warn 1", canonical
// verdicts first (in pass/warn/fail order) and anything else after,
// alphabetically, so the string is deterministic across renders.
func verdictMix(m map[string]int) string {
	if len(m) == 0 {
		return "—"
	}
	order := []string{"pass", "warn", "fail"}
	seen := make(map[string]bool, len(order))
	parts := make([]string, 0, len(m))
	for _, k := range order {
		if n, ok := m[k]; ok {
			parts = append(parts, fmt.Sprintf("%s %d", k, n))
			seen[k] = true
		}
	}
	for _, k := range nonCanonicalVerdicts(m, seen) {
		parts = append(parts, fmt.Sprintf("%s %d", k, m[k]))
	}
	return strings.Join(parts, " · ")
}

func findOutcome(outs []scorecard.ToolModelOutcome, src string) (escRate, unconfRate scorecard.Rate) {
	for _, o := range outs {
		if o.Source == src {
			return o.EscapeRate, o.UnconfirmedFlagRate
		}
	}
	return scorecard.Rate{}, scorecard.Rate{}
}

func toolModelHeaders() []string {
	headers := []string{"Model", "Calls", "Runs", "Verdicts", "Findings/call", "p50 ms", "p95 ms", "Partial"}
	for _, src := range scorecard.Sources {
		headers = append(headers, src+" escape", src+" unconfirmed")
	}
	return headers
}

func toolModelRowCells(r scorecard.ToolModelRow) []string {
	row := []string{
		r.Model,
		strconv.Itoa(r.Calls),
		strconv.Itoa(r.Runs),
		verdictMix(r.VerdictCounts),
		fmt.Sprintf("%.2f", r.FindingsPerCall),
		strconv.FormatInt(r.MSP50, 10),
		strconv.FormatInt(r.MSP95, 10),
		fmt.Sprintf("%.0f%%", r.PartialRate*100),
	}
	for _, src := range scorecard.Sources {
		escRate, unconfRate := findOutcome(r.Outcomes, src)
		row = append(row, fmtRate(escRate), fmtRate(unconfRate))
	}
	return row
}

func rowsForTool(rows []scorecard.ToolModelRow, tool string) []scorecard.ToolModelRow {
	var out []scorecard.ToolModelRow
	for _, r := range rows {
		if r.Tool == tool {
			out = append(out, r)
		}
	}
	return out
}

// overviewTables renders one table per tool in scorecard.ToolOrder that has
// rows, each row a model's call volume, verdict mix, latency and (per
// outcome source) escape/unconfirmed-flag rates.
func overviewTables(rows []scorecard.ToolModelRow) string {
	var b strings.Builder
	for _, tool := range scorecard.ToolOrder {
		trows := rowsForTool(rows, tool)
		if len(trows) == 0 {
			continue
		}
		body := make([][]string, 0, len(trows))
		for _, r := range trows {
			body = append(body, toolModelRowCells(r))
		}
		b.WriteString(heading(3, tool))
		b.WriteString(dataTablePlain(toolModelHeaders(), body))
	}
	return b.String()
}

func distinctPublishers(rows []scorecard.ToolModelRow) []string {
	seen := map[string]bool{}
	var pubs []string
	for _, r := range rows {
		if seen[r.Publisher] {
			continue
		}
		seen[r.Publisher] = true
		pubs = append(pubs, r.Publisher)
	}
	sort.Strings(pubs)
	return pubs
}

func rowsForPublisher(rows []scorecard.ToolModelRow, pub string) []scorecard.ToolModelRow {
	var out []scorecard.ToolModelRow
	for _, r := range rows {
		if r.Publisher == pub {
			out = append(out, r)
		}
	}
	return out
}

// byUserOverview groups a byPublisher tool×model view under one <h3> per
// publisher, reusing overviewTables for each publisher's slice.
func byUserOverview(rows []scorecard.ToolModelRow) string {
	var b strings.Builder
	for _, pub := range distinctPublishers(rows) {
		b.WriteString(heading(3, pub))
		b.WriteString(overviewTables(rowsForPublisher(rows, pub)))
	}
	return b.String()
}

// reviewModelTable renders the regression-by-review-model cohorts.
func reviewModelTable(groups []scorecard.Group) string {
	headers := []string{"Source", "Review model", "Runs", "Tasks", "Escape", "Minor escape", "Unconfirmed", "Waive", "Caught & fixed", "Regression", "Baseline"}
	rows := make([][]string, 0, len(groups))
	for _, g := range groups {
		baseline := "—"
		if g.Baseline != nil {
			baseline = orDash(g.Baseline.ReviewModel)
		}
		rows = append(rows, []string{
			g.Source,
			g.Key.ReviewModel,
			strconv.Itoa(g.Runs),
			strconv.Itoa(g.Tasks),
			fmtRate(g.EscapeRate),
			fmtRate(g.MinorEscapeRate),
			fmtRate(g.UnconfirmedFlagRate),
			fmtRate(g.WaiveRate),
			strconv.Itoa(g.CaughtAndFixed),
			g.Regression,
			baseline,
		})
	}
	return dataTablePlain(headers, rows)
}

// modelSetStrip renders one line per distinct configured model set, in
// plan/pre/mid/post/worker order, with the run count that used it.
func modelSetStrip(sets []scorecard.ModelSet) string {
	if len(sets) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<ul class="cards">`)
	for _, s := range sets {
		parts := make([]string, 0, len(configuredRoles))
		for _, role := range configuredRoles {
			parts = append(parts, role+"="+orDash(s.Models[role]))
		}
		line := strings.Join(parts, " · ") + fmt.Sprintf(" — %d runs", s.Runs)
		b.WriteString(`<li>` + esc(line) + `</li>`)
	}
	b.WriteString(`</ul>`)
	return b.String()
}

// compactModels renders only the configured roles a run actually carries,
// role=model joined by " · " — a shorter form than modelSetStrip's fixed
// five-role line, meant for a runs-list cell.
func compactModels(m map[string]string) string {
	var parts []string
	for _, role := range configuredRoles {
		if v, ok := m[role]; ok && v != "" {
			parts = append(parts, role+"="+v)
		}
	}
	if len(parts) == 0 {
		return "—"
	}
	return strings.Join(parts, " · ")
}

// escapeCounts renders a run's per-source escape counts, "final_review 1 ·
// review_now 0", in scorecard.Sources order.
func escapeCounts(m map[string]int) string {
	var parts []string
	for _, src := range scorecard.Sources {
		if n, ok := m[src]; ok {
			parts = append(parts, fmt.Sprintf("%s %d", src, n))
		}
	}
	if len(parts) == 0 {
		return "—"
	}
	return strings.Join(parts, " · ")
}

// runsList renders the runs table. The first cell (Publisher outside "mine"
// scope, Latest otherwise) links to the run's detail view.
func runsList(v RunsView) string {
	showPublisher := v.Scope != "mine"
	var b strings.Builder
	b.WriteString(`<div class="tbl"><table><tr>`)
	if showPublisher {
		b.WriteString(`<th>Publisher</th>`)
	}
	b.WriteString(`<th>Latest</th><th>Plan</th><th>Tasks</th><th>Models</th><th>Implementers</th><th>Reviewed by</th><th>Escapes</th></tr>`)
	for _, r := range v.Runs {
		href := "/ui/runs?scope=" + scopeHref(v.Scope) + "&run=" + url.QueryEscape(r.RunHash)
		if r.Publisher != "" {
			href += "&publisher=" + url.QueryEscape(r.Publisher)
		}
		href = esc(href)
		b.WriteString(`<tr>`)
		if showPublisher {
			b.WriteString(`<td><a href="` + href + `">` + esc(r.Publisher) + `</a></td>`)
			b.WriteString(`<td>` + esc(r.Latest.Format("Jan 2 15:04")) + `</td>`)
		} else {
			b.WriteString(`<td><a href="` + href + `">` + esc(r.Latest.Format("Jan 2 15:04")) + `</a></td>`)
		}
		b.WriteString(`<td>` + esc(orDash(r.PlanVerdict)) + `</td>`)
		b.WriteString(`<td>` + strconv.Itoa(r.TaskCount) + `</td>`)
		b.WriteString(`<td>` + esc(compactModels(r.ConfiguredModels)) + `</td>`)
		b.WriteString(`<td>` + esc(orDash(strings.Join(r.ImplementerModels, ", "))) + `</td>`)
		b.WriteString(`<td>` + esc(orDash(strings.Join(r.Sources, ", "))) + `</td>`)
		b.WriteString(`<td>` + esc(escapeCounts(r.Escapes)) + `</td>`)
		b.WriteString(`</tr>`)
	}
	b.WriteString(`</table></div>`)
	return b.String()
}

// teamFreshness says when the team records were last pulled and offers a
// POST that pulls them now. It is a form, not a link, so that a crawler or a
// prefetch following links can never trigger a Basic Memory pull.
func teamFreshness(v RunsView) string {
	asOf := "not pulled yet"
	if !v.TeamAsOf.IsZero() {
		asOf = "as of " + v.TeamAsOf.Format("2006-01-02 15:04")
	}
	return `<form method="POST" action="/ui/runs/refresh" class="muted">Team data ` + esc(asOf) +
		` (pulled hourly) <input type="hidden" name="scope" value="` + esc(v.Scope) + `">` +
		`<button type="submit">Refresh now</button></form>`
}

// teamStatus is the freshness line and refresh button for the team and user
// scopes, plus the last pull's error if it failed.
func teamStatus(v RunsView) string {
	var b strings.Builder
	if v.Scope != "mine" {
		b.WriteString(teamFreshness(v))
	}
	if v.TeamError != "" {
		b.WriteString(`<p class="muted">Team records unavailable: ` + esc(v.TeamError) + `</p>`)
	}
	return b.String()
}

// runsRefreshHandler pulls the team records now and sends the browser back to
// the scope it came from. The scope is only echoed into the redirect when it
// is a team or user scope, so the form cannot be used to redirect elsewhere.
func runsRefreshHandler(p Provider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		p.RefreshTeamRuns(r.Context())
		scope := r.FormValue("scope")
		if scope != "team" && !strings.HasPrefix(scope, "user:") {
			scope = "team"
		}
		http.Redirect(w, r, "/ui/runs?scope="+scopeHref(scope), http.StatusSeeOther)
	}
}

func renderRunsPage(v RunsView) string {
	var b strings.Builder
	b.WriteString(`<h1>anti-tangent runs</h1>`)
	b.WriteString(scopeNav(v))
	b.WriteString(teamStatus(v))
	if !v.Present {
		if v.Scope == "mine" {
			b.WriteString(`<p class="muted">No run records yet (runs.jsonl absent — set ANTI_TANGENT_STATS_DIR on the server).</p>`)
		} else {
			b.WriteString(`<p class="muted">No shared run records in Basic Memory yet. A developer shares by running the daemon with ANTI_TANGENT_SHARE_STATS=1.</p>`)
		}
		return pageShell("Runs", b.String())
	}
	b.WriteString(heading(2, "Overview — anti-tangent tool × validator model"))
	b.WriteString(overviewTables(v.Scorecard.ByToolModel))
	b.WriteString(`<p class="muted">Escape columns slice tasks by the model that reviewed them in this tool. A task has one outcome and up to four reviewing models, so a row points at a model; it does not prove the model caused the miss.</p>`)
	if v.Scope == "team" && v.Scorecard.ByPublisher != nil {
		b.WriteString(heading(2, "By user"))
		b.WriteString(byUserOverview(v.Scorecard.ByPublisher.ByToolModel))
	}
	b.WriteString(heading(2, "Regression by review model"))
	b.WriteString(reviewModelTable(v.Scorecard.ByReviewModel))
	b.WriteString(fmt.Sprintf(`<p class="muted">regression stays insufficient_data until a model and its baseline each have %d runs.</p>`, v.Scorecard.MinRuns))
	b.WriteString(heading(2, "Configured model sets"))
	b.WriteString(modelSetStrip(v.Scorecard.ModelSets))
	b.WriteString(heading(2, "Runs"))
	b.WriteString(runsList(v))
	if v.Skipped > 0 {
		b.WriteString(fmt.Sprintf(`<p class="muted">%d unreadable records skipped.</p>`, v.Skipped))
	}
	return pageShell("Runs", b.String())
}

// runHeader picks the run's latest header line (ties favour the later line,
// matching scorecard's own assemble).
func runHeader(lines []scorecard.RunLine) *scorecard.RunLine {
	var h *scorecard.RunLine
	for i := range lines {
		l := lines[i]
		if !l.Header {
			continue
		}
		if h == nil || !l.Ts.Before(h.Ts) {
			cp := l
			h = &cp
		}
	}
	return h
}

// runTasks folds a run's snapshot lines into the latest snapshot per task
// index, matching scorecard's own assemble.
func runTasks(lines []scorecard.RunLine) map[int]scorecard.TaskSnapshot {
	out := map[int]scorecard.TaskSnapshot{}
	ts := map[int]scorecard.RunLine{}
	for _, l := range lines {
		if l.Header || l.Task == nil {
			continue
		}
		idx := l.Task.Index
		if prev, ok := ts[idx]; !ok || !l.Ts.Before(prev.Ts) {
			ts[idx] = l
			out[idx] = *l.Task
		}
	}
	return out
}

// runOutcomes picks the latest outcome per source, matching scorecard's own
// assemble.
func runOutcomes(outcomes []scorecard.OutcomeLine) map[string]scorecard.OutcomeLine {
	out := map[string]scorecard.OutcomeLine{}
	for _, o := range outcomes {
		if !scorecard.ValidSource(o.Source) {
			continue
		}
		if prev, ok := out[o.Source]; !ok || !o.Ts.Before(prev.Ts) {
			out[o.Source] = o
		}
	}
	return out
}

// findImplementerModel returns the first model assigned to index.
func findImplementerModel(models []scorecard.ImplementerModel, index int) (string, bool) {
	for _, m := range models {
		if m.TaskIndex == index && m.Model != "" {
			return m.Model, true
		}
	}
	return "", false
}

func implementerModelFor(bySource map[string]scorecard.OutcomeLine, index int) string {
	for _, src := range scorecard.Sources {
		o, ok := bySource[src]
		if !ok {
			continue
		}
		if m, found := findImplementerModel(o.ImplementerModels, index); found {
			return m
		}
	}
	return ""
}

func headerRows(h *scorecard.RunLine) [][2]string {
	models := map[string]string{}
	version := ""
	var call *scorecard.ToolCall
	if h != nil {
		models = h.ConfiguredModels
		version = h.ServerVersion
		call = h.PlanCall
	}
	rows := make([][2]string, 0, len(configuredRoles)+2)
	for _, role := range configuredRoles {
		rows = append(rows, [2]string{role, orDash(models[role])})
	}
	rows = append(rows, [2]string{"Server version", orDash(version)})
	planCall := "—"
	if call != nil {
		planCall = fmt.Sprintf("%s · %s · %d findings · %d ms", call.Model, call.Verdict, call.Findings, call.MS)
	}
	rows = append(rows, [2]string{"validate_plan", planCall})
	return rows
}

func severityLine(m map[string]int) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s %d", k, m[k]))
	}
	if len(parts) == 0 {
		return "—"
	}
	return strings.Join(parts, " · ")
}

// isEscapeSeverity reports whether a finding's severity counts as an escape
// (critical or major; minor never does).
func isEscapeSeverity(severity string) bool { return severity == "critical" || severity == "major" }

// findingsList renders one source's findings for a task index as a list of
// "severity · category", marking an escape when the task passed but the
// finding is critical or major.
func findingsList(findings []scorecard.OutcomeFinding, index int, taskPassed bool) string {
	var items []string
	for _, f := range findings {
		if f.TaskIndex != index {
			continue
		}
		line := esc(f.Severity) + " · " + esc(f.Category)
		if taskPassed && isEscapeSeverity(f.Severity) {
			line += " <strong>escape</strong>"
		}
		items = append(items, "<li>"+line+"</li>")
	}
	if len(items) == 0 {
		return `<p class="muted">No findings.</p>`
	}
	return "<ul>" + strings.Join(items, "") + "</ul>"
}

func filterLinesByHash(lines []scorecard.RunLine, hash string) []scorecard.RunLine {
	var out []scorecard.RunLine
	for _, l := range lines {
		if l.RunHash == hash {
			out = append(out, l)
		}
	}
	return out
}

func filterLinesByPublisher(lines []scorecard.RunLine, publisher string) []scorecard.RunLine {
	var out []scorecard.RunLine
	for _, l := range lines {
		if l.Publisher == publisher {
			out = append(out, l)
		}
	}
	return out
}

func filterLines(lines []scorecard.RunLine, hash, publisher string) []scorecard.RunLine {
	out := filterLinesByHash(lines, hash)
	if publisher != "" {
		out = filterLinesByPublisher(out, publisher)
	}
	return out
}

func filterOutcomesByHash(outcomes []scorecard.OutcomeLine, hash string) []scorecard.OutcomeLine {
	var out []scorecard.OutcomeLine
	for _, o := range outcomes {
		if o.RunHash == hash {
			out = append(out, o)
		}
	}
	return out
}

func filterOutcomesByPublisher(outcomes []scorecard.OutcomeLine, publisher string) []scorecard.OutcomeLine {
	var out []scorecard.OutcomeLine
	for _, o := range outcomes {
		if o.Publisher == publisher {
			out = append(out, o)
		}
	}
	return out
}

func filterOutcomes(outcomes []scorecard.OutcomeLine, hash, publisher string) []scorecard.OutcomeLine {
	out := filterOutcomesByHash(outcomes, hash)
	if publisher != "" {
		out = filterOutcomesByPublisher(out, publisher)
	}
	return out
}

func sortedTaskIndexes(tasks map[int]scorecard.TaskSnapshot) []int {
	idxs := make([]int, 0, len(tasks))
	for idx := range tasks {
		idxs = append(idxs, idx)
	}
	sort.Ints(idxs)
	return idxs
}

// taskTitle names a task heading, adding its local title only in "mine"
// scope (the only scope atruns.Data.Titles is ever populated in).
func taskTitle(v RunsView, hash string, idx int) string {
	title := fmt.Sprintf("Task %d", idx)
	if v.Scope != "mine" {
		return title
	}
	t, ok := v.Data.Titles[hash][idx]
	if !ok || t == "" {
		return title
	}
	return title + ": " + t
}

// taskSection renders one task's heading, call log, verdict/severity summary
// and per-source findings.
func taskSection(v RunsView, hash string, idx int, snap scorecard.TaskSnapshot, bySource map[string]scorecard.OutcomeLine) string {
	var b strings.Builder
	b.WriteString(heading(3, taskTitle(v, hash, idx)))
	callHeaders := []string{"Tool", "Model", "Verdict", "Findings", "ms"}
	callRows := make([][]string, 0, len(snap.Calls))
	for _, c := range snap.Calls {
		callRows = append(callRows, []string{c.Tool, c.Model, c.Verdict, strconv.Itoa(c.Findings), strconv.FormatInt(c.MS, 10)})
	}
	b.WriteString(dataTablePlain(callHeaders, callRows))
	b.WriteString(kvTable(4, "Task detail", [][2]string{
		{"Pre verdict", orDash(snap.PreVerdict)},
		{"Post verdict", orDash(snap.PostVerdict)},
		{"Severity", severityLine(snap.Severity)},
		{"Waived", strconv.Itoa(snap.Waived)},
		{"Attempts", strconv.Itoa(snap.Attempts)},
		{"Implementer model", orDash(implementerModelFor(bySource, idx))},
	}))
	taskPassed := snap.PostVerdict == "pass"
	for _, src := range scorecard.Sources {
		o, ok := bySource[src]
		if !ok {
			continue
		}
		b.WriteString(heading(4, src))
		b.WriteString(findingsList(o.Findings, idx, taskPassed))
	}
	return b.String()
}

// hasFindingAt reports whether any finding in o targets task index.
func hasFindingAt(o scorecard.OutcomeLine, index int) bool {
	for _, f := range o.Findings {
		if f.TaskIndex == index {
			return true
		}
	}
	return false
}

// unattributedSection renders task_index:0 findings per source, under one
// heading shared by the whole run.
func unattributedSection(bySource map[string]scorecard.OutcomeLine) string {
	var b strings.Builder
	b.WriteString(heading(3, "Not attributed to a task"))
	any := false
	for _, src := range scorecard.Sources {
		o, ok := bySource[src]
		if !ok || !hasFindingAt(o, 0) {
			continue
		}
		any = true
		b.WriteString(heading(4, src))
		b.WriteString(findingsList(o.Findings, 0, false))
	}
	if !any {
		b.WriteString(`<p class="muted">None.</p>`)
	}
	return b.String()
}

func renderRunDetail(v RunsView, hash, publisher string) string {
	lines := filterLines(v.Data.Lines, hash, publisher)
	if len(lines) == 0 {
		return pageShell("Run detail", `<h1>Run detail</h1><p class="muted">No such run in this scope.</p>`)
	}
	outcomes := filterOutcomes(v.Data.Outcomes, hash, publisher)

	header := runHeader(lines)
	tasks := runTasks(lines)
	bySource := runOutcomes(outcomes)

	var b strings.Builder
	b.WriteString(`<h1>Run detail</h1>`)
	b.WriteString(`<p><a href="/ui/runs?scope=` + scopeHref(v.Scope) + `">← back to runs</a></p>`)
	b.WriteString(kvTable(2, "Run header", headerRows(header)))
	for _, idx := range sortedTaskIndexes(tasks) {
		b.WriteString(taskSection(v, hash, idx, tasks[idx], bySource))
	}
	b.WriteString(unattributedSection(bySource))

	return pageShell("Run detail", b.String())
}
