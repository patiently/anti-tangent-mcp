# Task-order false positive (0.25.0 Part 4) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `validate_plan`'s deterministic Create/Modify check stops reporting existing files as missing when their line anchor has spaces after its commas, checks every path of a Files bullet instead of only the first, and can be waived by a controller ruling like any other finding.

**Architecture:** two independent changes. In `internal/planparser/filerefs.go`, `lineAnchorRe` admits whitespace after each comma and a trailing `, …`, and `cleanRefPath` (one path per bullet) becomes `refPaths` (the bullet's leading path list), collected through a small `refPathList` that drops patterns, sibling shorthands and repeats. In `internal/mcpsrv/review_error.go`, `applyPreLadder` appends the file-consistency finding before `waivePlanFindings` runs, so a ruling on its id moves it into `waived_findings`.

**Tech Stack:** Go (stdlib `regexp`, `slices`; `testify`).

**Spec:** `docs/superpowers/specs/2026-09-22-field-report-0.24.0-improvements-design.md`, Part 4 (§4.1–§4.4). The brief is `.superpowers/HANDOVER-part4.md`.

## Global Constraints

- **Branch:** all work is on `bugfix/task-order-false-positive`, cut from Part 3's tip `451ed70`. Its pull request targets `feature/lean-coherence-replay-gated` while PR #92 is open, then `version/0.25.0` — never `main`. Do not bump `VERSION`.
- **Tests:** `go test -race ./...` passes and `gofmt -l .` prints nothing before every commit. Unit tests never touch the network.
- **No anti-tangent gating.** This plan fixes `validate_plan`'s own check. Implementers do NOT call `validate_task_spec`, `check_progress` or `validate_completion`, and the controller does not call `validate_plan` on this plan. The gate is the per-task review and the final whole-branch review.
- **CodeScene, every task, via its MCP tools only** (load them with ToolSearch: `select:mcp__codescene__pre_commit_code_health_safeguard,mcp__codescene__analyze_change_set`; never through Bash or npx): run `mcp__codescene__pre_commit_code_health_safeguard` on the staged change before each commit and fix what it reports without changing behaviour this plan specifies; before reporting DONE run `mcp__codescene__analyze_change_set` with `base_ref: "version/0.25.0"` and report its quality gate and net problem points. The branch already carries Part 3's +1.09 problem points; judge this task's own delta. A degradation you judge unavoidable is argued in the report, not assumed.
- **Comments** (project CLAUDE.md): a comment explains non-obvious behaviour or a hazard; never change history — no version, issue or task references, no "previously", "no longer", "now". When you touch a comment that breaks this rule, rewrite it.
- **Public repository:** no consumer-project names, paths or code anywhere. Every path in tests and comments is synthetic.
- **CHANGELOG:** append to the existing `## [0.25.0] - 2026-09-22` entry under `### Fixed`, at the end of that subsection. Never open a second entry.
- **Protocol:** this plan does not edit `docs/protocol/`.
- **Commit trailer:** end every commit message with a `Co-Authored-By:` line naming the model that wrote the commit, then `Claude-Session: https://claude.ai/code/session_016CAvPWaWTqQ87m5DZ1oYor`.

**User decisions (already made):**
- Part 4 ships inside 0.25.0 (Patrick, 2026-09-23).
- A controller ruling waives `task_order_contradiction` like any other id — option (a) (Patrick, 2026-09-23). Accepted cost: a ruling also covers a genuine violation a later round adds to the same finding; the waived entry keeps its evidence.
- anti-tangent is off for this work; CodeScene stays on.

**Rulings made for this plan (controller's, each with its cost if wrong):**
1. *A multi-path bullet's list ends at the first span followed by anything but a separator* (commas, semicolons, `&`, `+`, whitespace, `and`). Reading every backtick span would turn prose code spans (`` `Foo` ``, `` `## Configure` ``) into missing files. Cost if wrong: a list written with another separator (`/`, `or`) is read as its first path only, which is what happened before.
2. *Patterns (`*`, `?`, `{`) are dropped, first path included.* Run over this repo's own plans, the change reads 37 multi-path bullets; `pre_*.golden` and `{pre,plan}*.golden` among them would stat as missing. Cost if wrong: a pattern bullet's files are not checked, as a glob never could be.
3. *A slash-free path after a path with a directory is dropped* (sibling shorthand, three instances in this repo's plans). Cost if wrong: a root file listed after a nested one (`` `internal/a.go`, `README.md` ``) is not checked — it never was.
4. *The ellipsis form (`, …` / `, ...`) is part of the anchor.* It is in the field report's plan bullets. Cost if wrong: none; a path cannot end in `, …`.
5. *No README or protocol text.* The `controller_rulings` schema text already promises what the code will do; no document says deterministic findings are exempt. Cost if wrong: none found by grep.

---

### Task 1: Every path on a Files bullet is read, and a spaced line list is an anchor

**Goal:** `planparser.FileRefs` strips `:6-22, 29` and `:1-2, 5, …` anchors and returns every path in a Files bullet's leading path list, dropping patterns, sibling shorthands and repeats.

**Files:**
- Modify: `internal/planparser/filerefs.go`
- Modify: `internal/planparser/filerefs_test.go`
- Modify: `internal/mcpsrv/file_consistency_test.go`
- Modify: `CHANGELOG.md`

**Acceptance Criteria:**
- [ ] `stripLineAnchor` reduces `a.go:6-22, 29`, `a.go:51-54, 748-753, 1394`, `a.go:1-2, 5, …`, `a.go:1-2, 5, ...` and `a.go:14-15,  64` to `a.go`, and every case in `TestLineAnchorStripsCommaLists` and `TestFileRefs_LineAnchorStripLeavesOtherColonsAlone` still passes unchanged.
- [ ] `FileRefs` returns every path of a backticked list whose spans are separated only by commas, semicolons, `&`, `+`, whitespace or `and`, and stops at the first span followed by anything else (`` - Modify: `README.md` — add a row under `## Configure` `` yields `README.md` only).
- [ ] With no backtick on the bullet, `FileRefs` returns the comma-separated pieces: the first piece's first word, then each later piece that is a single word, skipping a piece that is only line numbers or an ellipsis and stopping at the first multi-word piece. `TestFileRefs_BarePathFirstToken` still passes.
- [ ] A path containing `*`, `?` or `{` is dropped; a slash-free path after a path with a directory on the same bullet is dropped (also when that earlier path was itself dropped as a pattern); a path repeated on one bullet is returned once; `` `go.mod`, `go.sum` `` returns both.
- [ ] A bullet's verbs apply to every path on it (`Create/Modify:` with two paths puts both in `Create` and `Modify`).
- [ ] `checkFileConsistency` with `repo_root` returns nil for `` - Modify: `a.go:6-22, 29`, `b.go:1-2, 5, …` `` when both files exist, and reports `` Task 1 modifies `b.go`, which is not created until Task 2 `` for `` - Modify: `a.go`, `b.go:10-12, 40` `` followed by a task that creates `b.go`.
- [ ] `cleanRefPath` no longer exists; no comment in `filerefs.go` describes one path per bullet.
- [ ] One bullet appended under `### Fixed` in `## [0.25.0]`.

**Verify:** `go test -race -count=1 ./internal/planparser/ ./internal/mcpsrv/ -run 'FileRefs|LineAnchor|CheckFileConsistency'` → `ok` for both packages

**Steps:**

- [ ] **Step 1: Write the failing parser tests.** Append to `internal/planparser/filerefs_test.go`:

```go
// A line list is written with and without a space after each comma, and may
// end in an ellipsis that elides the rest of it. Left on the path, the suffix
// is statted as part of the file name and the file reported missing.
func TestLineAnchorStripsSpacedAndElidedLists(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"a.go:6-22, 29", "a.go"},
		{"a.go:51-54, 748-753, 1394", "a.go"},
		{"a.go:1-2, 5, …", "a.go"},
		{"a.go:1-2, 5, ...", "a.go"},
		{"a.go:14-15,  64", "a.go"},
	} {
		if got := stripLineAnchor(tc.in); got != tc.want {
			t.Errorf("stripLineAnchor(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// A Files bullet can list several paths. Each must be read, or a Modify: of a
// file a later task creates goes unreported whenever it is not first on its
// line. Prose after the list must not be read as more paths.
func TestFileRefs_EveryPathOnABullet(t *testing.T) {
	cases := []struct {
		name   string
		bullet string
		want   []string
	}{
		{"backticked list with spaced anchors", "- Modify: `a/x.go:6-22, 29`, `b/y.go:1-6`, `c/z.go:1-18, 165-222`",
			[]string{"a/x.go", "b/y.go", "c/z.go"}},
		{"and and semicolon separators", "- Modify: `a.go` and `b.go`; `c.go`", []string{"a.go", "b.go", "c.go"}},
		{"prose after the first span", "- Modify: `README.md` — add a row under `## Configure`", []string{"README.md"}},
		{"code span in a sentence", "- Modify: `a.go` to call `Helper` from `b.go`", []string{"a.go"}},
		{"unquoted list", "- Modify: a.go, b.go", []string{"a.go", "b.go"}},
		{"unquoted anchor continuation", "- Modify: a.go:6-22, 29, b.go", []string{"a.go", "b.go"}},
		{"unquoted prose ends the list", "- Modify: a.go — edit it, then b.go", []string{"a.go"}},
		{"repeated path counts once", "- Modify: `a.go:1-3`, `a.go:9`", []string{"a.go"}},
		{"glob dropped", "- Modify: `testdata/pre_*.golden`, `testdata/b.golden`", []string{"testdata/b.golden"}},
		{"brace pattern dropped", "- Modify: `tmpl/{pre,post}.tmpl`", nil},
		{"sibling shorthand dropped", "- Modify: `testdata/pre.golden`, `post.golden`", []string{"testdata/pre.golden"}},
		{"sibling after a dropped pattern", "- Modify: `testdata/*.golden`, `post.golden`", nil},
		{"slash-free list kept", "- Modify: `go.mod`, `go.sum`", []string{"go.mod", "go.sum"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := FileRefs("**Files:**\n" + tc.bullet + "\n").Modify
			if len(tc.want) == 0 {
				assert.Empty(t, got)
				return
			}
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestFileRefs_VerbsApplyToEveryPath(t *testing.T) {
	refs := FileRefs("**Files:**\n- Create/Modify: `a.go`, `b.go`\n")
	assert.Equal(t, []string{"a.go", "b.go"}, refs.Create)
	assert.Equal(t, []string{"a.go", "b.go"}, refs.Modify)
}
```

- [ ] **Step 2: Run them and watch them fail.**

Run: `go test -count=1 ./internal/planparser/ -run 'SpacedAndElided|EveryPathOnABullet|VerbsApplyToEveryPath'`
Expected: FAIL — `a.go:6-22, 29` is returned unstripped, and the multi-path cases return one path.

- [ ] **Step 3: Implement.** In `internal/planparser/filerefs.go`:

(a) Add `"slices"` to the imports.

(b) Replace the last paragraph of `lineAnchorRe`'s comment and the regex itself with:

```go
	// An anchor is a comma-separated LIST of lines-or-ranges, not a single
	// line or a single pair: "a.md:60,166,174,419" and "a.md:27-30,40-50"
	// are both ordinary plan bullets, written with or without a space after
	// each comma, and a list may end in ", …" or ", ..." that elides the
	// rest. Any list shape the pattern does not accept leaves the digits
	// attached to the path, which the disk tier stats verbatim and reports as
	// a file that does not exist.
	lineAnchorRe = regexp.MustCompile(`(?::\d+(?:-\d+)?(?:,\s*\d+(?:-\d+)?)*(?:,\s*(?:…|\.\.\.))?)+$`)
	// pathListSepRe matches what may stand between two backticked paths of
	// one bullet's list. Anything else after a span — a dash, a verb, an
	// opening parenthesis — starts prose, whose code spans (`Foo`,
	// `## Configure`) are not paths.
	pathListSepRe = regexp.MustCompile(`^(?:[\s,;&+]|\band\b)*$`)
	// anchorContinuationRe matches an unquoted comma piece that only carries
	// more lines of the previous path's anchor: the " 29" of "a.go:6-22, 29".
	anchorContinuationRe = regexp.MustCompile(`^(?:\d+(?:-\d+)?|…|\.\.\.)$`)
```

(c) In `FileRefs`, replace

```go
		path := cleanRefPath(m[2])
		if path == "" {
			continue
		}
		for _, verb := range strings.Split(strings.ToLower(m[1]), "/") {
			switch verb {
			case "create":
				refs.Create = append(refs.Create, path)
			case "modify":
				refs.Modify = append(refs.Modify, path)
			case "delete":
				refs.Delete = append(refs.Delete, path)
			}
		}
```

with

```go
		for _, path := range refPaths(m[2]) {
			for _, verb := range strings.Split(strings.ToLower(m[1]), "/") {
				switch verb {
				case "create":
					refs.Create = append(refs.Create, path)
				case "modify":
					refs.Modify = append(refs.Modify, path)
				case "delete":
					refs.Delete = append(refs.Delete, path)
				}
			}
		}
```

(d) Replace the whole of `cleanRefPath` (doc comment included) with:

```go
// refPaths takes the paths out of a bullet's tail, after removing a trailing
// parenthetical annotation: the backticked list when the tail has a backtick,
// else the comma-separated unquoted list.
func refPaths(tail string) []string {
	tail = strings.TrimSpace(trailingParenRe.ReplaceAllString(strings.TrimSpace(tail), ""))
	if i := strings.Index(tail, "`"); i >= 0 {
		return backtickPaths(tail[i+1:])
	}
	return barePaths(tail)
}

// backtickPaths reads the spans of a backticked list, rest starting just after
// the first opening backtick. The list ends at the first span followed by
// anything pathListSepRe does not accept. An unterminated span contributes its
// first word.
func backtickPaths(rest string) []string {
	var l refPathList
	for {
		j := strings.Index(rest, "`")
		if j < 0 {
			if fields := strings.Fields(rest); len(fields) > 0 {
				l.add(fields[0])
			}
			return l.paths
		}
		l.add(rest[:j])
		rest = rest[j+1:]
		k := strings.Index(rest, "`")
		if k < 0 || !pathListSepRe.MatchString(rest[:k]) {
			return l.paths
		}
		rest = rest[k+1:]
	}
}

// barePaths reads an unquoted list. The first piece contributes its first
// word, and ends the list if prose follows that word; a later piece counts
// only as a single word, so "a.go — edit it, then b.go" is not read as a
// path named "then".
func barePaths(tail string) []string {
	var l refPathList
	for i, piece := range strings.Split(tail, ",") {
		fields := strings.Fields(piece)
		if i == 0 {
			if len(fields) == 0 {
				return nil
			}
			l.add(fields[0])
			if len(fields) > 1 {
				return l.paths
			}
			continue
		}
		if len(fields) != 1 {
			return l.paths
		}
		if anchorContinuationRe.MatchString(fields[0]) {
			continue
		}
		l.add(fields[0])
	}
	return l.paths
}

// refPathList collects one bullet's paths, cleaned by stripLineAnchor and
// canonRefPath. It drops three shapes that would otherwise be statted as
// files that do not exist:
//
//   - a pattern (`*`, `?`, `{`), which names a set of files, not one;
//   - a slash-free path after a path with a directory, which plans write as
//     shorthand for a sibling of that path (`testdata/a.golden`,
//     `b.golden`); a bare name could as well be a root file, and the text
//     cannot tell which, so it is not checked at all;
//   - a repeat, which would report the same file twice.
//
// hasDir counts dropped paths too: a shorthand after a directory pattern is
// still a sibling of it.
type refPathList struct {
	paths  []string
	hasDir bool
}

func (l *refPathList) add(raw string) {
	p := canonRefPath(stripLineAnchor(strings.TrimSpace(raw)))
	hasDir := strings.Contains(p, "/")
	sibling := l.hasDir && !hasDir
	l.hasDir = l.hasDir || hasDir
	if p == "" || sibling || strings.ContainsAny(p, "*?{") || slices.Contains(l.paths, p) {
		return
	}
	l.paths = append(l.paths, p)
}
```

(e) In `stripLineAnchor`'s doc comment, replace `":N-M", ":N,M", or repeated` with `":N-M", ":N,M", ":N, M", or repeated`, and rejoin its oddly-wrapped lines so the comment reads as one paragraph.

- [ ] **Step 4: Run the parser tests.**

Run: `go test -race -count=1 ./internal/planparser/`
Expected: `ok` — the new tests and every existing one.

- [ ] **Step 5: Write the end-to-end tests.** Append to `internal/mcpsrv/file_consistency_test.go`:

```go
// A Files bullet can list several paths, each anchored with a spaced line
// list. The disk tier must find every one of them on disk, and the order tier
// must see a later path on the line, not just the first.
func TestCheckFileConsistency_MultiPathBullets(t *testing.T) {
	t.Run("existing files with spaced anchors draw no finding", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "a.go"), []byte("x"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "b.go"), []byte("x"), 0o600))
		f := checkFileConsistency(tasksFrom("**Files:**\n- Modify: `a.go:6-22, 29`, `b.go:1-2, 5, …`\n"), dir)
		assert.Nil(t, f)
	})

	t.Run("a later path created by a later task is reported", func(t *testing.T) {
		f := checkFileConsistency(tasksFrom(
			"**Files:**\n- Modify: `a.go`, `b.go:10-12, 40`\n",
			"**Files:**\n- Create: `b.go`\n",
		), "")
		require.NotNil(t, f)
		assert.Contains(t, f.Evidence, "Task 1 modifies `b.go`, which is not created until Task 2")
	})
}
```

- [ ] **Step 6: Run them.**

Run: `go test -race -count=1 ./internal/mcpsrv/ -run CheckFileConsistency`
Expected: `ok`.

- [ ] **Step 7: CHANGELOG.** Append at the end of `### Fixed` in `## [0.25.0] - 2026-09-22`:

```markdown
- `validate_plan`'s Create/Modify check strips a line anchor written with spaces after its commas
  (`a.go:6-22, 29`) or ending in `, …`, and reads every path a Files bullet lists rather than the
  first. It reported such a file as missing when it existed, and never looked at the other paths on
  the line, so a Modify: of a file only a later task creates went unreported. Patterns
  (`testdata/*.golden`) and a bare file name after a nested path, which plans write as shorthand for
  a sibling, are not checked.
```

- [ ] **Step 8: Full suite, format, CodeScene, commit.**

Run: `go test -race ./... && gofmt -l .` → all `ok`, no gofmt output. Stage the four files, run `mcp__codescene__pre_commit_code_health_safeguard`, then:

```bash
git commit -m "fix(planparser): read every path on a Files bullet and spaced line lists

<body>

Co-Authored-By: <model>
Claude-Session: https://claude.ai/code/session_016CAvPWaWTqQ87m5DZ1oYor"
```

Then `mcp__codescene__analyze_change_set` with `base_ref: "version/0.25.0"`; report its gate and net problem points.

```json:metadata
{"files": ["internal/planparser/filerefs.go", "internal/planparser/filerefs_test.go", "internal/mcpsrv/file_consistency_test.go", "CHANGELOG.md"], "verifyCommand": "go test -race -count=1 ./internal/planparser/ ./internal/mcpsrv/ -run 'FileRefs|LineAnchor|CheckFileConsistency'", "acceptanceCriteria": ["spaced and elided anchors strip; existing anchor cases unchanged", "backticked list read up to the first non-separator", "unquoted list: first word, single-word pieces, anchor continuations skipped", "patterns, sibling shorthands and repeats dropped; go.mod, go.sum kept", "verbs apply to every path", "checkFileConsistency: no finding on existing multi-path; later-created second path reported", "cleanRefPath removed", "one CHANGELOG Fixed bullet"], "modelTier": "standard"}
```

---

### Task 2: A controller ruling waives the task-order finding

**Goal:** a `validate_plan` `controller_rulings` entry naming the `task_order_contradiction` finding's id moves it into `waived_findings`, with its evidence, and the verdict is computed without it.

**Files:**
- Modify: `internal/mcpsrv/review_error.go`
- Modify: `internal/mcpsrv/plan_normalize.go`
- Modify: `internal/verdict/plan.go`
- Modify: `internal/mcpsrv/handlers_plan_rulings_test.go`
- Modify: `CHANGELOG.md`

**Acceptance Criteria:**
- [ ] In `applyPreLadder`, the `c.FileConsistency` append precedes `waivePlanFindings`, and `prependPlanClamp` still follows it; the comment above them says why each sits where it does.
- [ ] `TestValidatePlan_RulingWaivesTheFileConsistencyFinding` passes: round 1 reports the finding with id `verdict.Fingerprint(verdict.CategoryOther, planScopeKey, "task_order_contradiction")`; round 2, on a plan whose violation names a different file, with a ruling on that id, has no such finding in `plan_findings`, one `waived_findings` entry with that id, the ruling text and the new evidence, `plan_verdict` `pass`, and a `waived: <id>` line in `summary_block`.
- [ ] `TestValidatePlan_FileConsistencyFindingReachesTheEnvelope` and every existing ruling test still pass.
- [ ] `waivePlanFindings`'s doc comment and the two `WaivedFindings` field comments in `internal/verdict/plan.go` no longer say "reviewer findings".
- [ ] One bullet appended under `### Fixed` in `## [0.25.0]`.

**Verify:** `go test -race -count=1 ./internal/mcpsrv/ -run 'Ruling|FileConsistency'` → `ok`

**Steps:**

- [ ] **Step 1: Write the failing test.** Append to `internal/mcpsrv/handlers_plan_rulings_test.go` (add `"fmt"` to its imports):

```go
// The deterministic Create/Modify finding is a plan-level finding like any
// other: a ruling on its id waives it. The id is a fingerprint of category,
// scope and criterion, not evidence, so the id one round reports still
// matches after the plan is edited and the evidence changes.
func TestValidatePlan_RulingWaivesTheFileConsistencyFinding(t *testing.T) {
	plan := func(file string) string {
		return "# Plan\n\n" +
			"### Task 1: t1\n\n**Goal:** g\n\n**Acceptance criteria:**\n- ac\n\n**Files:**\n- Modify: `" + file + "`\n\n" +
			"### Task 2: t2\n\n**Goal:** g\n\n**Acceptance criteria:**\n- ac\n\n**Files:**\n- Create: `" + file + "`\n\n"
	}
	id := verdict.Fingerprint(verdict.CategoryOther, planScopeKey, "task_order_contradiction")

	sr := &scriptedReviewer{responses: []providers.Response{singlePlanResp(t, 2), singlePlanResp(t, 2)}}
	h := &handlers{deps: newDepsWithScripted(t, sr, 8)}

	_, first, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: plan("a.go")})
	require.NoError(t, err)
	var reported bool
	for _, f := range first.PlanFindings {
		if f.Criterion == "task_order_contradiction" {
			reported = true
			assert.Equal(t, id, f.ID)
		}
	}
	require.True(t, reported, "round 1 must report the ordering violation")

	_, second, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{
		PlanText:          plan("b.go"),
		ControllerRulings: []ControllerRulingArg{{FindingID: id, Ruling: "Ordered on purpose"}},
	})
	require.NoError(t, err)
	assert.False(t, hasCriterion(second.PlanFindings, "task_order_contradiction"))
	require.Len(t, second.WaivedFindings, 1)
	w := second.WaivedFindings[0]
	assert.Equal(t, id, w.ID)
	assert.Equal(t, "Ordered on purpose", w.Ruling)
	assert.Contains(t, w.Evidence, "`b.go`")
	assert.Equal(t, verdict.VerdictPass, second.PlanVerdict)
	assert.Contains(t, second.SummaryBlock, fmt.Sprintf("waived: %s", id))
}
```

- [ ] **Step 2: Run it and watch it fail.**

Run: `go test -count=1 ./internal/mcpsrv/ -run TestValidatePlan_RulingWaivesTheFileConsistencyFinding`
Expected: FAIL — the finding is still in `plan_findings` and `waived_findings` is empty.

- [ ] **Step 3: Implement.** In `internal/mcpsrv/review_error.go`, `applyPreLadder`, replace

```go
	// Before the file-consistency finding and the clamp join the list, so only
	// reviewer findings are waived.
	waivePlanFindings(pr, c.Rulings, c.Tasks)
	if c.FileConsistency != nil {
		pr.PlanFindings = append(pr.PlanFindings, *c.FileConsistency)
	}
	*pr = prependPlanClamp(*pr, c.Clamp)
```

with

```go
	// The file-consistency finding joins before the waiver, so a ruling reaches
	// it: it is the plan-level finding a controller can most often prove wrong
	// by listing a directory, and an unwaivable major would hold the verdict
	// down every round. The clamp joins after: it reports this call's token
	// budget, not the plan, so there is nothing to rule on.
	if c.FileConsistency != nil {
		pr.PlanFindings = append(pr.PlanFindings, *c.FileConsistency)
	}
	waivePlanFindings(pr, c.Rulings, c.Tasks)
	*pr = prependPlanClamp(*pr, c.Clamp)
```

In `internal/mcpsrv/plan_normalize.go`, the first line of `waivePlanFindings`'s doc comment: `// waivePlanFindings moves every reviewer finding a ruling covers into` → `// waivePlanFindings moves every finding a ruling covers into`.

In `internal/verdict/plan.go`: `// WaivedFindings holds the plan-level reviewer findings a controller ruling` → `// WaivedFindings holds the plan-level findings a controller ruling`, and `// WaivedFindings holds this task's reviewer findings a controller ruling` → `// WaivedFindings holds this task's findings a controller ruling`.

- [ ] **Step 4: Run the ruling and consistency tests.**

Run: `go test -race -count=1 ./internal/mcpsrv/ -run 'Ruling|FileConsistency'`
Expected: `ok`.

- [ ] **Step 5: CHANGELOG.** Append at the end of `### Fixed` in `## [0.25.0] - 2026-09-22`:

```markdown
- A `validate_plan` `controller_rulings` entry waives the deterministic `task_order_contradiction`
  finding, which then appears under `waived_findings` with its evidence, as the argument's
  description says. The finding could not be waived, so a false positive held the verdict down every
  round. A ruling covers every violation the finding lists, including one a later round adds.
```

- [ ] **Step 6: Full suite, format, CodeScene, commit.**

Run: `go test -race ./... && gofmt -l .` → all `ok`, no gofmt output. Stage the five files, run `mcp__codescene__pre_commit_code_health_safeguard`, then commit as `fix(validate_plan): let a controller ruling waive the task-order finding` with the trailer from Global Constraints. Then `mcp__codescene__analyze_change_set` with `base_ref: "version/0.25.0"`; report its gate and net problem points.

```json:metadata
{"files": ["internal/mcpsrv/review_error.go", "internal/mcpsrv/plan_normalize.go", "internal/verdict/plan.go", "internal/mcpsrv/handlers_plan_rulings_test.go", "CHANGELOG.md"], "verifyCommand": "go test -race -count=1 ./internal/mcpsrv/ -run 'Ruling|FileConsistency'", "acceptanceCriteria": ["FileConsistency appended before waivePlanFindings; clamp after; comment explains both", "RulingWaivesTheFileConsistencyFinding passes: stable id, waived entry with ruling and new evidence, verdict pass, waived: line", "existing ruling and file-consistency tests pass", "comments no longer say reviewer findings", "one CHANGELOG Fixed bullet"], "modelTier": "mechanical"}
```
