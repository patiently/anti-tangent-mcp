# anti-tangent-mcp v0.19.0 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Recalibrate `validate_task_spec`'s pre-hook prompt so its verdict is actionable, and add a comment-hygiene policy with three enforcement layers — write time, review time, and task close — whose combined coverage is high but explicitly not total (see the coverage holes below).

**Architecture:** Parts 1–2 are prompt-only edits to `pre.tmpl`, ported from wording already proven in `post.tmpl`. Part 3 adds a comment policy with three enforcement layers — a `PreToolUse` hook that prevents, a `post.tmpl` reviewer rule that judges, and a `check-task-complete` scan that is defence in depth. The two deterministic hook layers share one scanner implementation (`comment_scan.py`); the reviewer layer applies the same policy semantically, through prompt instructions, and shares no code.

**Known coverage holes, by design.** `post.tmpl` accepts completion evidence as `final_files`,
`final_diff` or `test_evidence` "in any combination", and two of those combinations are not
covered end-to-end:

- **`final_files`-only** — no diff, so neither close-time layer runs. The scanner needs a diff;
  and the reviewer deliberately does NOT fall back to the summary, because `post.tmpl` states the
  summary is not evidence, so it cannot establish which comments the change added.
- **`test_evidence`-only** — no file content of any kind reaches either layer.
- Combined with a `Bash`-written change (heredoc, `sed -i`), which the `Edit`/`Write` matcher never
  sees, either of the above leaves the change unenforced by **all three** layers.

The deterministic scanner is also narrower than the policy it enforces, and this must be
documented rather than left for someone to discover:

- it reads **full-line comments only** — block-comment interiors, trailing comments, and
  comment-like text inside string literals are out of scope;
- it implements **three tells** (`task-<n>`, `#<digits>`, `v<x.y.z>`), while the policy also bans
  prose narration such as "previously", "no longer" and "this replaced". Those forms are
  reviewer-only by design, because they cannot be pattern-matched without false positives.

So the reviewer layer is the broader of the two, and the scanner is the blocking one. Tasks 9 and
10 must state this asymmetry in the plugin README and the protocol docs.

This is accepted rather than closed: requiring diff evidence purely for comment hygiene would
change `validate_completion`'s contract for every caller, which is out of scope for this release.
The consequence is that the Goal's "at write time, at review time, and at task close" is a
description of the three layers, NOT a claim of unconditional coverage — Tasks 7, 9 and 10 must
state the limitation in the plugin README and the protocol docs rather than implying completeness. Part 4 adds a bounded `criterion` histogram to the stats ledger so Part 3 is measurable.

**Tech Stack:** Go 1.x (`internal/prompts` text/template + golden tests, `internal/stats`, `internal/mcpsrv`), Bash + Python 3 Claude Code hooks, JSON-driven hook eval harness.

**Spec:** `docs/superpowers/specs/2026-09-08-anti-tangent-v0.19.0-design.md`

## Global Constraints

- **The MCP server embeds prompts via `go:embed`.** Editing a `.tmpl` does not change the behaviour of the *running* server until it is rebuilt and the host restarts it. Do not expect a template edit to alter live `validate_task_spec` output mid-plan, and do not "fix" a template because a live call still shows the old wording.
- **`go test -race ./...` must pass before the commit of every task that changes Go code** (Tasks 2, 3, 4, 5). Tasks that touch only hooks, plugin metadata or docs (1, 6, 7, 8, 9, 10) verify with their own `**Verify:**` command instead — running the Go suite there proves nothing about what they changed. Never leave a task with a failing suite either way.
- **Golden files are regenerated with `go test ./internal/prompts/... -update`, and the resulting diff must be read line by line before committing.** For Parts 1–2 the golden diff *is* the release; a rubber-stamped `-update` defeats the whole task.
- **Protocol parts are hard-capped at 16,000 bytes by CI.** `core.md` has 109 bytes of headroom — nothing lands there. `implementer.md` has 1,479.
- **`docs/protocol/` and `plugin/anti-tangent-protocol/protocol/` must be byte-identical**, resynced in the same commit as any protocol edit.
- **Comment-hygiene findings are pinned to `severity: minor`, always.** `quality` is not severity-floored server-side, so an unpinned rule lets two comment nits become a `fail` and hard-block a task close.
- **Every scan looks at ADDED lines only, in source files only.** `.md`/`.yml`/`.yaml`/`.json`/`.txt` are excluded — `CHANGELOG.md` legitimately carries issue and version references and this repo requires a changelog entry in every change.
- **This plan's own commits must satisfy the comment policy it introduces**, from Task 2 onward.
- Branch is `version/0.19.0`; do **not** bump the `VERSION` file (the release workflow does it).

**User decisions (already made):**
- "Keep invariant-why, ban change-why" — a comment may explain a non-obvious invariant or hazard; it may not say why a change was made or cite an issue.
- "Both layers" for enforcement — deterministic pattern scan *and* reviewer judgement, not either alone.
- "On-touch only" — no repo-wide cleanup of the ~135 existing violations.
- "Product + this repo" — the policy ships in the protocol docs and is also stated in this repo's `CLAUDE.md`.
- "Add the PreToolUse as you described" — prevention layer on `Edit`/`Write` is in scope.
- "We should add criterion to the stats.Event" — yes, via a bounded allowlist, never raw criterion text.
- Ladder untouched: `FinalizeVerdict` is not modified in this release.

---

### Task 1: Probe whether PreToolUse fires inside a subagent

**Goal:** Determine, by experiment, whether a `PreToolUse` hook intercepts tool calls issued *inside* a dispatched subagent, because that single fact decides whether the prevention layer is the primary comment gate or only covers the controller's own edits.

> **USER-ORDERED GATE — NON-SKIPPABLE.** This task was requested by the user in the current conversation. It MUST NOT be closed by walking around it, by declaring it "verified inline", or by substituting a cheaper check. Close only after every item in `acceptanceCriteria` has been re-validated independently, with output captured.

**Files:**
- Create: `/tmp/claude-hooks/subagent-probe.log` (scratch, not committed)
- Create: `.claude/settings.local.json` (does not exist yet; temporary probe hook, **deleted again** in the final step)
- Modify: `docs/superpowers/specs/2026-09-08-anti-tangent-v0.19.0-design.md` (record the answer)

**Acceptance Criteria:**
- [ ] A `PreToolUse` matcher on `Write` is registered and demonstrably fires for a write made by the *main* session (control — proves the probe itself works)
- [ ] A subagent is dispatched that writes a file, and the log is inspected for an entry naming that file
- [ ] The spec's "Coverage" section is updated to state the answer as fact, replacing the "unverified" wording
- [ ] `.claude/settings.local.json` is restored to its pre-probe state
- [ ] The captured log content is quoted in the task's completion report

**Verify:** `cat /tmp/claude-hooks/subagent-probe.log` → shows a control entry for the main-session write, and either does or does not show one for the subagent write; `git diff --stat .claude/settings.local.json` → empty

**Steps:**

- [ ] **Step 1: Record the pre-probe state of the settings file**

```bash
mkdir -p /tmp/claude-hooks
rm -f /tmp/claude-hooks/settings.local.json.bak /tmp/claude-hooks/settings.existed
if [[ -f .claude/settings.local.json ]]; then
    cp .claude/settings.local.json /tmp/claude-hooks/settings.local.json.bak
    touch /tmp/claude-hooks/settings.existed
fi
```

Clearing the backup first and recording existence with an explicit marker matters: a leftover
`.bak` from an earlier run would otherwise let Step 7 *create* a settings file that never existed
before the probe.

- [ ] **Step 2: Register the probe hook**

**Activation first.** Claude Code may read hook settings at session start rather than on every
tool call. If the control write in Step 3 does not appear in the log, that is the FIRST thing to
suspect — not evidence about subagents. Before concluding anything, re-read the settings (or start
a fresh session in this directory) and repeat Step 3 until the control fires. Record in the
completion report which of the two was needed, because it tells the next person whether a hook
edit takes effect live.

Add this `PreToolUse` entry to `.claude/settings.local.json` (merging with any existing `hooks` key rather than replacing it):

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Write",
        "hooks": [
          { "type": "command", "command": "cat >> /tmp/claude-hooks/subagent-probe.log" }
        ]
      }
    ]
  }
}
```

- [ ] **Step 3: Control — write a file from the main session**

```bash
: > /tmp/claude-hooks/subagent-probe.log
```

Then use the `Write` tool (not Bash) from this session to create `/tmp/claude-hooks/control.txt` with the content `control`. Then:

```bash
grep -c "control.txt" /tmp/claude-hooks/subagent-probe.log
```

Expected: `1` or more. **If this is 0 the probe is not active, not disproven — see the activation note above, get the control firing, and only then run Step 4.** A zero here means nothing about subagents.

- [ ] **Step 4: Dispatch a subagent that writes a file**

Dispatch a subagent (Agent tool, `general-purpose`, model `haiku`) with exactly this prompt:

> Use the Write tool to create the file `/tmp/claude-hooks/subagent.txt` containing the single word `subagent`. Do not use Bash. Report only whether the write succeeded.

- [ ] **Step 5: Read the answer**

```bash
grep -c "subagent.txt" /tmp/claude-hooks/subagent-probe.log
cat /tmp/claude-hooks/subagent-probe.log
```

- `>= 1` → PreToolUse DOES fire inside subagents. The prevention layer closes the subagent-driven gap and is the primary comment gate.
- `0` → it does NOT. Prevention covers only the closing/controlling session's own edits; the reviewer layer carries subagent work.

- [ ] **Step 6: Record the answer in the spec**

In `docs/superpowers/specs/2026-09-08-anti-tangent-v0.19.0-design.md`, in the "Coverage: what each layer reaches" section, replace the paragraph beginning "**The prevention layer's reach turns on one unverified fact" and the two bullets and the probe paragraph that follow it with a statement of the measured result, quoting the grep counts. Keep it factual: what was run, what came back, what it means for the layer's reach.

- [ ] **Step 7: Restore settings and commit**

```bash
if [[ -f /tmp/claude-hooks/settings.existed ]]; then
    cp /tmp/claude-hooks/settings.local.json.bak .claude/settings.local.json
else
    rm -f .claude/settings.local.json
fi
git status --porcelain .claude/
git add docs/superpowers/specs/2026-09-08-anti-tangent-v0.19.0-design.md
git commit -m "docs(spec): record measured PreToolUse subagent reach"
```

---

### Task 2: Calibrate severity in pre.tmpl

**Goal:** Stop `validate_task_spec` emitting `major` for every enumerable implicit assumption, which is what drives 81% of its non-pass verdicts.

The 81% is measured, not asserted: see the table in the spec's "Revision" section, derived from 191 real `validate_task_spec` events in `~/.local/state/anti-tangent/stats/events.jsonl` (154 of 191 non-passes major-driven). You do not need to reproduce it to do this task.

**Files:**
- Modify: `internal/prompts/templates/pre.tmpl` (the numbered check 3, and the `Severity:` line)
- Modify: `internal/prompts/prompts_test.go`
- Modify: `internal/prompts/testdata/pre_basic.golden`, `internal/prompts/testdata/pre_with_project_knowledge.golden`

**Acceptance Criteria:**
- [ ] Check 3 instructs consolidation and pins assumptions to `minor` unless misimplementation is plausible
- [ ] A calibration paragraph follows the `Severity:` line, reserving `major` and `critical`
- [ ] `TestRenderPre_IncludesTestOnlyGuidance`'s existing assertion on `"one consolidated finding"` still passes
- [ ] A new test asserts both new passages render
- [ ] Both `pre_*.golden` files regenerated and the diff read line by line

**Verify:** `go test ./internal/prompts/... -v -run TestRenderPre` → PASS

**Steps:**

- [ ] **Step 1: Write the failing test**

Add to `internal/prompts/prompts_test.go`:

```go
func TestRenderPre_CalibratesSeverity(t *testing.T) {
	out, err := RenderPre(PreInput{Spec: sampleSpec()})
	require.NoError(t, err)
	assert.Contains(t, out.User, "Consolidate related assumptions into one finding")
	assert.Contains(t, out.User, "prefer `verdict: pass` with `severity: minor`")
	assert.Contains(t, out.User, "Reserve `severity: major` for ambiguity that would cause")
	assert.Contains(t, out.User, "is not, on its own, a major finding")
}
```

- [ ] **Step 2: Run it and watch it fail**

Run: `go test ./internal/prompts/... -run TestRenderPre_CalibratesSeverity -v`
Expected: FAIL — the strings are absent from the rendered prompt.

- [ ] **Step 3: Bound the assumptions clause**

In `internal/prompts/templates/pre.tmpl`, replace this line:

```
3. Implicit assumptions — list any assumptions a fresh implementer would have to make. Each becomes a finding so the spec author can either pin them down or explicitly mark them as implementer's discretion.
```

with:

```
3. Implicit assumptions — list any assumptions a fresh implementer would have to make. Consolidate related assumptions into one finding rather than emitting one finding per assumption. Emit each at `severity: minor` unless a competent implementer could plausibly resolve the assumption the wrong way, in which case it is `major`.
```

- [ ] **Step 4: Add the calibration paragraph**

In the same file, find this line:

```
Severity: critical = spec is unimplementable as written; major = a competent implementer would still misimplement it; minor = nit.
```

and insert immediately after it, separated by a blank line:

```
When the goal is clear and each acceptance criterion is individually testable, prefer `verdict: pass` with `severity: minor` `category: quality` findings for nit-level concerns over a failing verdict. Reserve `severity: major` for ambiguity that would cause a competent implementer to build the wrong thing, and `severity: critical` for a spec that cannot be implemented as written. Wanting an acceptance criterion stated more explicitly is not, on its own, a major finding.
```

- [ ] **Step 5: Run the test again**

Run: `go test ./internal/prompts/... -run TestRenderPre_CalibratesSeverity -v`
Expected: PASS

- [ ] **Step 6: Regenerate goldens and READ the diff**

```bash
go test ./internal/prompts/... -update
git diff internal/prompts/testdata/
```

Read every changed line. Expected: exactly the two passages above appear in `pre_basic.golden` and `pre_with_project_knowledge.golden`, and nothing else changed. If anything else moved, stop and investigate before committing.

- [ ] **Step 7: Full suite and commit**

```bash
go test -race ./... 2>&1 | tail -20
git add internal/prompts/
git commit -m "fix(pre.tmpl): reserve major for ambiguity that causes misimplementation"
```

---

### Task 3: Make Context authoritative in pre.tmpl

**Goal:** Stop the pre-hook reviewer flagging ambiguity that the brief's own `Context:` block already resolves, including when it resolves it in code rather than prose.

**Files:**
- Modify: `internal/prompts/templates/pre.tmpl`
- Modify: `internal/prompts/prompts_test.go`
- Modify: `internal/prompts/testdata/pre_basic.golden`, `internal/prompts/testdata/pre_with_project_knowledge.golden`

**Acceptance Criteria:**
- [ ] `pre.tmpl` instructs that `Context:` resolves under-specification and names both suppressed categories
- [ ] It requires a `major` `ambiguous_spec` finding where an AC and `Context:` are incompatible, so `Context:` cannot silently overrule an AC
- [ ] The instruction explicitly covers `Context:` resolving something in code, not only prose
- [ ] A new test asserts the passage renders
- [ ] Goldens regenerated and diff read

**Verify:** `go test ./internal/prompts/... -v -run TestRenderPre` → PASS

**Steps:**

- [ ] **Step 1: Write the failing test**

Add to `internal/prompts/prompts_test.go`:

```go
func TestRenderPre_ContextIsAuthoritative(t *testing.T) {
	out, err := RenderPre(PreInput{Spec: sampleSpec()})
	require.NoError(t, err)
	assert.Contains(t, out.User, "`Context:` block in the task spec above resolves under-specification")
	assert.Contains(t, out.User, "including when it answers it in code rather than prose")
	assert.Contains(t, out.User, "it does not silently overrule them")
}
```

- [ ] **Step 2: Run it and watch it fail**

Run: `go test ./internal/prompts/... -run TestRenderPre_ContextIsAuthoritative -v`
Expected: FAIL.

- [ ] **Step 3: Add the passage**

In `internal/prompts/templates/pre.tmpl`, immediately BEFORE the line beginning `Severity: critical = spec is unimplementable`, insert this paragraph followed by a blank line:

```
The `Context:` block in the task spec above resolves under-specification. Do not emit `ambiguous_spec` or `missing_acceptance_criterion` for an ambiguity that `Context:` already answers — including when it answers it in code rather than prose.

This applies only where an acceptance criterion is SILENT or VAGUE and `Context:` fills the gap. Where an acceptance criterion and `Context:` state incompatible requirements, that is a contradiction, not an ambiguity: emit it as `ambiguous_spec` at `severity: major`, quoting both sides. `Context:` clarifies acceptance criteria; it does not silently overrule them.
```

- [ ] **Step 4: Run the test again**

Run: `go test ./internal/prompts/... -run TestRenderPre_ContextIsAuthoritative -v`
Expected: PASS

- [ ] **Step 5: Regenerate goldens and READ the diff**

```bash
go test ./internal/prompts/... -update
git diff internal/prompts/testdata/
```

Expected: only the new paragraph appears.

- [ ] **Step 6: Full suite and commit**

```bash
go test -race ./... 2>&1 | tail -20
git add internal/prompts/
git commit -m "fix(pre.tmpl): treat Context as authoritative, as post.tmpl already does"
```

---

### Task 4: Bounded criterion counts in the stats ledger

**Goal:** Make comment-hygiene findings separable from other `quality` findings in `events.jsonl`, without writing any reviewer- or caller-authored free text to disk.

**Files:**
- Modify: `internal/stats/event.go`
- Modify: `internal/mcpsrv/handlers.go:330` and `internal/mcpsrv/handlers.go:1711`
- Test: `internal/stats/event_test.go`

**Acceptance Criteria:**
- [ ] `Event` carries `CriterionCounts map[string]int` with json tag `criterion_counts,omitempty`
- [ ] `CountFindings` returns a criterion histogram built ONLY from an allowlist of sentinels
- [ ] A finding whose criterion is verbatim acceptance-criterion text contributes nothing to the map
- [ ] Both existing `CountFindings` call sites compile with the new arity
- [ ] With no allowlisted criteria present the map is nil, and a marshalled `Event` provably contains no `criterion_counts` key

**Verify:** `go test -race ./internal/stats/... ./internal/mcpsrv/... -v -run 'CountFindings|Criterion'` → PASS

**Steps:**

- [ ] **Step 1: Write the failing test**

Add to `internal/stats/event_test.go`:

```go
func TestCountFindings_CriterionAllowlistOnly(t *testing.T) {
	findings := []verdict.Finding{
		{Severity: verdict.SeverityMinor, Category: verdict.CategoryQuality,
			Criterion: "comment_hygiene", Evidence: "e", Suggestion: "s"},
		{Severity: verdict.SeverityMinor, Category: verdict.CategoryQuality,
			Criterion: "the exporter MUST emit one span per outbound request",
			Evidence: "e", Suggestion: "s"},
	}
	_, _, crit, total := CountFindings(findings)
	require.Equal(t, 2, total)
	assert.Equal(t, 1, crit["comment_hygiene"])
	assert.Len(t, crit, 1,
		"verbatim acceptance-criterion text must never reach the ledger")
}

func TestCountFindings_NoAllowlistedCriteriaYieldsNilMap(t *testing.T) {
	findings := []verdict.Finding{
		{Severity: verdict.SeverityMinor, Category: verdict.CategoryQuality,
			Criterion: "some verbatim AC", Evidence: "e", Suggestion: "s"},
	}
	_, _, crit, _ := CountFindings(findings)
	assert.Nil(t, crit, "nil map keeps criterion_counts out of the JSON via omitempty")

	blob, err := json.Marshal(Event{Tool: "validate_task_spec", CriterionCounts: crit})
	require.NoError(t, err)
	assert.NotContains(t, string(blob), "criterion_counts",
		"omitempty must actually drop the key, not merely leave it null")
}
```

- [ ] **Step 2: Run it and watch it fail**

Run: `go test ./internal/stats/... -run TestCountFindings_Criterion -v`
Expected: FAIL — compile error, `CountFindings` returns 3 values not 4.

- [ ] **Step 3: Add the field to Event**

In `internal/stats/event.go`, add to the `Event` struct immediately after `CategoryCounts`:

```go
	CriterionCounts map[string]int `json:"criterion_counts,omitempty"`
```

- [ ] **Step 4: Add the allowlist and widen CountFindings**

In `internal/stats/event.go`, add above `CountFindings`:

```go
// countedCriteria bounds what the ladder records. Criterion is free-form
// reviewer text — the pre-hook prompt instructs the reviewer to quote verbatim
// acceptance-criterion text into it — so counting every distinct value would
// give the histogram one key per acceptance criterion ever reviewed and write
// specification text into the ledger, which otherwise holds no free text at all.
// Only these server-recognised sentinels are counted; everything else is dropped.
var countedCriteria = map[string]bool{
	"comment_hygiene":              true,
	"noise_cluster":                true,
	"codebase_reference_checklist": true,
	"codebase_convention":          true,
	"exit_contract":                true,
	"spec":                         true,
	"structure":                    true,
	"max_tokens_override":          true,
}
```

Replace `CountFindings` with:

```go
func CountFindings(findings []verdict.Finding) (severity, category, criterion map[string]int, total int) {
	if len(findings) == 0 {
		return nil, nil, nil, 0
	}
	severity = make(map[string]int)
	category = make(map[string]int)
	for _, f := range findings {
		severity[string(f.Severity)]++
		category[string(f.Category)]++
		if countedCriteria[f.Criterion] {
			if criterion == nil {
				criterion = make(map[string]int)
			}
			criterion[f.Criterion]++
		}
	}
	return severity, category, criterion, len(findings)
}
```

Update its doc comment's first line to say it builds severity, category and criterion histograms.

- [ ] **Step 5: Fix both call sites**

`internal/mcpsrv/handlers.go` around line 330, in `recordStat`:

```go
	sev, cat, crit, total := stats.CountFindings(p.findings)
```

and add to the `stats.Event{...}` literal, after `CategoryCounts: cat,`:

```go
		CriterionCounts: crit,
```

`internal/mcpsrv/handlers.go` around line 1711:

```go
		sev, _, _, _ := stats.CountFindings(env.Findings)
```

- [ ] **Step 6: Run the tests**

Run: `go test -race ./internal/stats/... ./internal/mcpsrv/... -run 'CountFindings|Criterion' -v`
Expected: PASS

- [ ] **Step 7: Full suite and commit**

```bash
go test -race ./... 2>&1 | tail -20
git add internal/stats/ internal/mcpsrv/handlers.go
git commit -m "feat(stats): record bounded criterion counts on review events"
```

---

### Task 5: Comment-hygiene rule in post.tmpl

**Goal:** Give `validate_completion`'s reviewer — the only enforcement layer that sees every execution path — a rule for judging comments, pinned to `minor` so it can never fail a task close.

**Files:**
- Modify: `internal/prompts/templates/post.tmpl`
- Modify: `internal/prompts/prompts_test.go`
- Modify: `internal/prompts/testdata/post_basic.golden`, `post_with_codescene.golden`, `post_with_exit_contracts.golden`, `post_with_exit_contracts_inferred.golden`

**Acceptance Criteria:**
- [ ] `post.tmpl` states the policy (behaviour/invariant allowed, change-history banned) and names the banned forms
- [ ] The rule applies only when a diff is present, and says so explicitly for both `final_files`-only and `test_evidence`-only evidence. It must NOT use the summary to decide which code is new — `post.tmpl` already states the summary is not evidence, and a rule that contradicts its own surrounding prompt is worse than a narrower one
- [ ] The rule pins `category: quality`, `criterion: comment_hygiene`, `severity: minor`, and says explicitly that it is never major or critical however many are found
- [ ] A new test asserts the passage renders and that the severity pin is present verbatim
- [ ] All four `post_*.golden` files regenerated and the diff read

**Verify:** `go test ./internal/prompts/... -v -run TestRenderPost` → PASS

**Steps:**

- [ ] **Step 1: Write the failing test**

Add to `internal/prompts/prompts_test.go`:

```go
func TestRenderPost_CommentHygieneIsPinnedMinor(t *testing.T) {
	out, err := RenderPost(samplePostInput())
	require.NoError(t, err)
	assert.Contains(t, out.User, "criterion: comment_hygiene")
	assert.Contains(t, out.User, "always `minor`, never `major` or `critical`")
	assert.Contains(t, out.User, "read correctly to someone who never saw this change")
}
```

If the existing post tests use a different helper than `samplePostInput()`, use whichever helper the neighbouring `TestRenderPost_*` tests already use — do not invent a new fixture.

- [ ] **Step 2: Run it and watch it fail**

Run: `go test ./internal/prompts/... -run TestRenderPost_CommentHygieneIsPinnedMinor -v`
Expected: FAIL.

- [ ] **Step 3: Add the rule**

In `internal/prompts/templates/post.tmpl`, insert immediately BEFORE the final line `Respond with the verdict JSON only.`:

```
### Comment hygiene

Comments added by this change must explain non-trivial behaviour, or a non-obvious invariant or hazard that would bite the next editor. The test is that a comment reads correctly to someone who never saw this change.

A comment is a defect when it narrates change history — an issue, pull-request or task reference; a version reference; "previously", "no longer", "this replaced" — or when it restates what the code plainly does, or describes the code inaccurately.

Judge only comments this change ADDS, and only when the evidence shows which comments those are. When the evidence is a diff, added comments are the `+` lines.

**Apply this policy ONLY when a diff is present.** With `final_files` and no diff you cannot tell an added comment from one that was already there, and the summary does not settle it — as stated above, the summary on its own is not evidence. With `test_evidence` alone there is no comment to read. In both cases emit no comment-hygiene finding at all rather than inferring which code is new.

Emit these as `category: quality`, `criterion: comment_hygiene`, and `severity: minor` — always `minor`, never `major` or `critical`, however many you find. Quote the offending comment in `evidence`, and give the rewritten comment or an explicit removal in `suggestion`.
```

- [ ] **Step 4: Run the test again**

Run: `go test ./internal/prompts/... -run TestRenderPost_CommentHygieneIsPinnedMinor -v`
Expected: PASS

- [ ] **Step 5: Regenerate goldens and READ the diff**

```bash
go test ./internal/prompts/... -update
git diff internal/prompts/testdata/
```

Expected: the same block appears in all four `post_*.golden` files, nothing else changes.

- [ ] **Step 6: Full suite and commit**

```bash
go test -race ./... 2>&1 | tail -20
git add internal/prompts/
git commit -m "feat(post.tmpl): flag change-history comments as minor quality findings"
```

---

### Task 6: Shared comment scanner and the PreToolUse prevention hook

**Goal:** Prevent a violating comment being written at all, via a `PreToolUse` hook on `Edit`/`Write` that shares one scanner implementation with the task-close hook.

**Files:**
- Create: `plugin/anti-tangent-guard/hooks/comment_scan.py`
- Create: `plugin/anti-tangent-guard/hooks/check_comment_write.py`
- Create: `plugin/anti-tangent-guard/hooks/check-comment-write`
- Modify: `plugin/anti-tangent-guard/hooks/hooks.json`
- Modify: `plugin/anti-tangent-guard/evals/guard-evals.json`, `plugin/anti-tangent-guard/evals/run.sh`

**Acceptance Criteria:**
- [ ] `comment_scan.py` exposes a scan over (path, added lines) returning violations, with the extension allowlist and the three tells
- [ ] `Edit` scans only lines in `new_string` absent from `old_string`
- [ ] `Write` to a new file scans all content; `Write` over an existing file scans only lines absent from the on-disk file
- [ ] `.md`, `.yml`, `.yaml`, `.json`, `.txt` never block
- [ ] `ANTI_TANGENT_COMMENT_GUARD=0` disables the hook
- [ ] The hook fails open on every internal error (missing python3, unreadable file, malformed JSON)
- [ ] A newly added DUPLICATE of a comment already present in the file is still scanned (proving `added` is occurrence-aware, not set-based)
- [ ] Every fail-open path named above has its own eval asserting exit 0: malformed JSON, missing body file, unreadable target, absent `python3`
- [ ] Each eval that writes or reads a target file uses a per-case temporary directory, so no case depends on a file another case left behind
- [ ] New evals pass, and `EXPECTED_CASE_COUNT` (33) and the `description` string agree

**Verify:** `bash plugin/anti-tangent-guard/evals/run.sh` → all 33 cases pass

**Steps:**

- [ ] **Step 1: Write the scanner**

Create `plugin/anti-tangent-guard/hooks/comment_scan.py`:

```python
"""Shared comment-hygiene scanner for the anti-tangent-guard hooks.

Scans ADDED comment lines in source files for change-history references.
Behaviour and invariants belong in comments; change history belongs in git.
"""
import os
import re
from collections import Counter

SCAN_EXTS = {
    ".go", ".sh", ".bash", ".py", ".ts", ".tsx", ".js", ".jsx",
    ".rs", ".java", ".kt", ".rb", ".c", ".h", ".cc", ".cpp", ".hpp",
}

# Line-comment openers per extension. Block-comment interiors, trailing
# comments and comment-like text inside string literals are out of scope:
# a partial implementation that half-detects them would produce false
# blocks, which cost more here than a missed violation.
LINE_COMMENT = {
    ".py": ("#",), ".sh": ("#",), ".bash": ("#",), ".rb": ("#",),
}
DEFAULT_COMMENT = ("//",)

TELLS = (
    (re.compile(r"task-\d+", re.I), "a task reference"),
    (re.compile(r"#\d+"), "an issue or pull-request reference"),
    (re.compile(r"\bv\d+\.\d+\.\d+\b"), "a version reference"),
)


def openers(path):
    return LINE_COMMENT.get(os.path.splitext(path)[1].lower(), DEFAULT_COMMENT)


def scannable(path):
    return os.path.splitext(path)[1].lower() in SCAN_EXTS


def violations(path, added_lines):
    """Return [(line, why)] for added comment lines carrying change history."""
    if not scannable(path):
        return []
    opens = openers(path)
    out = []
    for raw in added_lines:
        line = raw.strip()
        if not any(line.startswith(o) for o in opens):
            continue
        for pat, why in TELLS:
            if pat.search(line):
                out.append((line, why))
                break
    return out


def added(old_text, new_text):
    """Lines in new_text beyond the occurrences already present in old_text.

    Occurrence-aware on purpose. A set would discard a NEWLY ADDED duplicate of
    a comment that already appears elsewhere in the file, which is exactly the
    case a writer hits when copying a bad comment to a second site.
    """
    remaining = Counter(old_text.splitlines())
    out = []
    for line in new_text.splitlines():
        if remaining.get(line, 0) > 0:
            remaining[line] -= 1
        else:
            out.append(line)
    return out
```

- [ ] **Step 2: Write the PreToolUse hook as TWO files**

The hook is a thin bash wrapper plus a real Python file. Do **not** try to embed the Python in a
heredoc while also feeding JSON on stdin — `python3 - <<'PY' <<<"$INPUT"` silently makes Python
read the JSON *as its script*, so the scanner never runs and the hook always exits 0. Verified.

Create `plugin/anti-tangent-guard/hooks/check_comment_write.py`:

```python
"""PreToolUse body: refuse an Edit/Write that ADDS a change-history comment.

Reads the hook payload from ATG_INPUT (not stdin, which is reserved for this
file). Exit 0 = allow, 2 = block, anything else = internal error the wrapper
converts to allow.
"""
import json
import os
import sys

sys.path.insert(0, os.path.join(os.environ.get("ATG_ROOT", ""), "hooks"))
from comment_scan import violations, added, scannable  # noqa: E402

data = json.loads(os.environ.get("ATG_INPUT", ""))
inp = data.get("tool_input") or {}
path = inp.get("file_path") or ""

if (data.get("tool_name") or "") not in ("Edit", "Write") or not scannable(path):
    sys.exit(0)

if data["tool_name"] == "Edit":
    lines = added(inp.get("old_string") or "", inp.get("new_string") or "")
else:
    content = inp.get("content") or ""
    try:
        with open(path) as fh:
            lines = added(fh.read(), content)
    except FileNotFoundError:
        lines = content.splitlines()

bad = violations(path, lines)
if not bad:
    sys.exit(0)

print("BLOCKED: this edit adds comment(s) carrying change history.\n", file=sys.stderr)
for line, why in bad[:10]:
    print("  %s\n    -> contains %s" % (line, why), file=sys.stderr)
print(
    "\nComments must explain non-trivial behaviour or a non-obvious invariant, and must read\n"
    "correctly to someone who never saw this change. Issue, task and version references belong\n"
    "in the commit message, not the code. Rewrite the comment and retry.",
    file=sys.stderr,
)
sys.exit(2)
```

Create `plugin/anti-tangent-guard/hooks/check-comment-write` (`chmod +x`):

```bash
#!/usr/bin/env bash
# PreToolUse hook: refuse an Edit/Write that ADDS a comment carrying change
# history. Behaviour and invariants belong in comments; issue, task and version
# references belong in git.
#
# There is deliberately NO `trap ... ERR` here. The Python body signals a block
# with exit 2, and an ERR trap fires on that and rewrites it to 0 — which would
# make this hook incapable of ever blocking. Statuses are handled explicitly
# instead: 2 blocks, 0 allows, anything else is an internal error and allows.
set -uo pipefail

PLUGIN_ROOT="${CLAUDE_PLUGIN_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
TRACE_LOG="${ANTI_TANGENT_GUARD_TRACE_LOG:-/tmp/claude-hooks/anti-tangent-guard.log}"
mkdir -p "$(dirname "$TRACE_LOG")" 2>/dev/null || true
trace() {
    printf '%s | comment-write | %s%s\n' \
        "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "${1:-?}" "${2:+ | $2}" \
        >> "$TRACE_LOG" 2>/dev/null || true
}

[[ "${ANTI_TANGENT_COMMENT_GUARD:-1}" == "0" ]] && { trace "skip" "guard=0"; exit 0; }
command -v python3 >/dev/null 2>&1 || { trace "skip" "no-python3"; exit 0; }
[[ -r "$PLUGIN_ROOT/hooks/check_comment_write.py" ]] || { trace "skip" "no-body"; exit 0; }

ATG_INPUT="$(cat)" ATG_ROOT="$PLUGIN_ROOT" \
    python3 "$PLUGIN_ROOT/hooks/check_comment_write.py"
status=$?
case "$status" in
    0) exit 0 ;;
    2) trace "block" "comment-hygiene"; exit 2 ;;
    *) trace "error" "python-exit=$status"; exit 0 ;;
esac
```

Note what makes this fail open without a trap: an unset `CLAUDE_PLUGIN_ROOT` falls back to the
script's own directory rather than tripping `set -u`; a missing `python3`, a missing body file, a
malformed payload or an unreadable target all land in the `*)` arm and exit 0.

- [ ] **Step 3: Register the hook**

Replace `plugin/anti-tangent-guard/hooks/hooks.json` with:

```json
{
  "hooks": {
    "PostToolUse": [
      {
        "matcher": "TaskUpdate",
        "hooks": [{ "type": "command", "command": "\"${CLAUDE_PLUGIN_ROOT}/hooks/check-task-complete\"" }]
      }
    ],
    "PreToolUse": [
      {
        "matcher": "Edit|Write",
        "hooks": [{ "type": "command", "command": "\"${CLAUDE_PLUGIN_ROOT}/hooks/check-comment-write\"" }]
      }
    ]
  }
}
```

- [ ] **Step 4: Verify the hook by hand before wiring evals**

```bash
export CLAUDE_PLUGIN_ROOT="$PWD/plugin/anti-tangent-guard"
SMOKE=$(mktemp -d); trap 'rm -rf "$SMOKE"' EXIT
echo '{"tool_name":"Write","tool_input":{"file_path":"'"$SMOKE"'/x.go","content":"// fixes #58\npackage x\n"}}' \
  | ./plugin/anti-tangent-guard/hooks/check-comment-write; echo "exit=$?"
```
Expected: exit=2, stderr naming the issue reference.

```bash
echo '{"tool_name":"Write","tool_input":{"file_path":"'"$SMOKE"'/x.md","content":"// fixes #58\n"}}' \
  | ./plugin/anti-tangent-guard/hooks/check-comment-write; echo "exit=$?"
```
Expected: exit=0 — markdown is never scanned.

```bash
echo '{"tool_name":"Write","tool_input":{"file_path":"'"$SMOKE"'/x.go","content":"// POSITIONAL EXTRACTION, NOT FREE SCAN\npackage x\n"}}' \
  | ./plugin/anti-tangent-guard/hooks/check-comment-write; echo "exit=$?"
```
Expected: exit=0 — invariant-why is legitimate.

- [ ] **Step 5: Add evals**

Append these to the `evals` array in `plugin/anti-tangent-guard/evals/guard-evals.json`, continuing ids from 23. Each entry follows the existing shape but drives the new hook, so add `"hook": "check-comment-write"` and teach `run.sh` to honour it (default `check-task-complete` when absent):

```json
{
  "id": 23,
  "name": "comment-write-issue-ref-blocks",
  "hook": "check-comment-write",
  "stdin_raw": "{\"tool_name\":\"Write\",\"tool_input\":{\"file_path\":\"/tmp/atg-eval.go\",\"content\":\"// fixes #58\\npackage x\\n\"}}",
  "expected_exit": 2,
  "expected_stderr_contains": ["adds comment(s) carrying change history", "an issue or pull-request reference"],
  "reason": "an added Go comment citing an issue must block"
},
{
  "id": 24,
  "name": "comment-write-markdown-never-blocks",
  "hook": "check-comment-write",
  "stdin_raw": "{\"tool_name\":\"Write\",\"tool_input\":{\"file_path\":\"/tmp/atg-eval.md\",\"content\":\"// fixes #58 in v0.19.0\\n\"}}",
  "expected_exit": 0,
  "reason": "CHANGELOG.md legitimately carries issue and version refs; markdown is never scanned"
},
{
  "id": 25,
  "name": "comment-write-untouched-comment-does-not-block",
  "hook": "check-comment-write",
  "stdin_raw": "{\"tool_name\":\"Edit\",\"tool_input\":{\"file_path\":\"/tmp/atg-eval.go\",\"old_string\":\"// see #12\\nfunc a() {}\",\"new_string\":\"// see #12\\nfunc a() { return }\"}}",
  "expected_exit": 0,
  "reason": "on-touch means ADDED lines only; an untouched violating comment must not block"
},
{
  "id": 26,
  "name": "comment-write-invariant-why-passes",
  "hook": "check-comment-write",
  "stdin_raw": "{\"tool_name\":\"Write\",\"tool_input\":{\"file_path\":\"/tmp/atg-eval.go\",\"content\":\"// POSITIONAL EXTRACTION, NOT FREE SCAN: a forged line could otherwise win.\\npackage x\\n\"}}",
  "expected_exit": 0,
  "reason": "invariant-why is the kind of comment the policy protects"
},
{
  "id": 27,
  "name": "comment-write-product-grammar-does-not-block",
  "hook": "check-comment-write",
  "stdin_raw": "{\"tool_name\":\"Write\",\"tool_input\":{\"file_path\":\"/tmp/atg-eval.go\",\"content\":\"// matches the leading \\\"Task 4: Add /healthz endpoint\\\" of a RawTask.Title\\npackage x\\n\"}}",
  "expected_exit": 0,
  "reason": "unhyphenated Task N is the product's own input grammar, deliberately not a tell"
},
{
  "id": 28,
  "name": "comment-write-kill-switch",
  "hook": "check-comment-write",
  "env": { "ANTI_TANGENT_COMMENT_GUARD": "0" },
  "stdin_raw": "{\"tool_name\":\"Write\",\"tool_input\":{\"file_path\":\"/tmp/atg-eval.go\",\"content\":\"// fixes #58\\npackage x\\n\"}}",
  "expected_exit": 0,
  "reason": "kill switch disables the hook"
},
{
  "id": 29,
  "name": "comment-write-added-duplicate-is-scanned",
  "hook": "check-comment-write",
  "stdin_raw": "{\"tool_name\":\"Edit\",\"tool_input\":{\"file_path\":\"/tmp/atg-eval.go\",\"old_string\":\"// see #12\\nfunc a() {}\",\"new_string\":\"// see #12\\nfunc a() {}\\n// see #12\\nfunc b() {}\"}}",
  "expected_exit": 2,
  "expected_stderr_contains": ["an issue or pull-request reference"],
  "reason": "a set-based diff discards this newly added duplicate; the occurrence-aware one must catch it"
},
{
  "id": 30,
  "name": "comment-write-malformed-json-fails-open",
  "hook": "check-comment-write",
  "stdin_raw": "not json at all",
  "expected_exit": 0,
  "reason": "a malformed payload must never block work"
},
{
  "id": 31,
  "name": "comment-write-missing-body-fails-open",
  "hook": "check-comment-write",
  "env": { "CLAUDE_PLUGIN_ROOT": "/nonexistent-plugin-root" },
  "stdin_raw": "{\"tool_name\":\"Write\",\"tool_input\":{\"file_path\":\"{{TMPDIR}}/a.go\",\"content\":\"// fixes #58\\n\"}}",
  "expected_exit": 0,
  "reason": "a missing python body must fail open, not block"
},
{
  "id": 32,
  "name": "comment-write-unreadable-target-fails-open",
  "hook": "check-comment-write",
  "stdin_raw": "{\"tool_name\":\"Write\",\"tool_input\":{\"file_path\":\"{{TMPDIR}}\",\"content\":\"// fixes #58\\n\"}}",
  "expected_exit": 0,
  "reason": "file_path pointing at a directory raises on open; must fail open"
},
{
  "id": 33,
  "name": "comment-write-no-python3-fails-open",
  "hook": "check-comment-write",
  "env": { "PATH": "/nonexistent-bin" },
  "stdin_raw": "{\"tool_name\":\"Write\",\"tool_input\":{\"file_path\":\"{{TMPDIR}}/a.go\",\"content\":\"// fixes #58\\n\"}}",
  "expected_exit": 0,
  "reason": "no python3 on PATH must fail open"
}
```

Note case 32 relies on `file_path` being a directory, which `open()` rejects — a portable way to
exercise the unreadable-target arm without chmod games.

- [ ] **Step 5b: Give every case its own temp directory**

Several cases write and read target files, and `Write`-over-an-existing-file deliberately
subtracts what is already on disk. Sharing one path between cases makes outcomes depend on
execution order and on residue from earlier runs. In `run.sh`, before each case:

```bash
case_tmp=$(mktemp -d "${TMPDIR:-/tmp}/atg-eval-XXXXXX")
```

substitute `{{TMPDIR}}` with `$case_tmp` everywhere it can appear. Be concrete rather than
copying the transcript path's approach: `{{TRANSCRIPT}}` is handled by `jq` **setting**
`.input.transcript_path` to a computed value, which is a different operation from replacing a
placeholder wherever it occurs. Use one mechanism for both payload shapes:

```bash
stdin_raw="${stdin_raw//\{\{TMPDIR\}\}/$case_tmp}"
input=$(jq --arg d "$case_tmp" 'walk(if type == "string" then gsub("\\{\\{TMPDIR\\}\\}"; $d) else . end)' <<<"$input")
```

and apply the same `${...//}` expansion to `env` values and to any `expected_file_contains` paths.

`run_case` has several early `return` paths on failure, so cleanup placed at the end of the
function would be skipped on exactly the runs you most want cleaned. Register it per case instead
— append `"$case_tmp"` to a module-level `CASE_TMPDIRS` array and have the existing
`trap cleanup EXIT` (line 38) `rm -rf` every entry. That reuses the cleanup path already proven to
run on every exit.

Update the existing cases 23–29 to use `{{TMPDIR}}/...` paths rather than the fixed
`/tmp/atg-eval.go`.

- [ ] **Step 6: Teach run.sh to dispatch by hook and bump the count**

In `plugin/anti-tangent-guard/evals/run.sh`:
- change `EXPECTED_CASE_COUNT=22` to `EXPECTED_CASE_COUNT=33`
- replace line 19, `HOOK="$PLUGIN_DIR/hooks/check-task-complete"`, with
  `HOOK_DIR="$PLUGIN_DIR/hooks"`, and change every invocation of `"$HOOK"` to `"$case_hook"`,
  where `case_hook="$HOOK_DIR/$hook_name"` is computed per case from:

```bash
hook_name=$(jq -r ".evals[$idx].hook // \"check-task-complete\"" "$EVALS_FILE")
```
then invoke `"$HOOK_DIR/$hook_name"` instead of the hardcoded script.

Also update the `description` string in `guard-evals.json` from `(22 cases)` to `(33 cases)`. `EXPECTED_CASE_COUNT` and this string must agree.

- [ ] **Step 7: Run the evals**

Run: `bash plugin/anti-tangent-guard/evals/run.sh`
Expected: all 33 pass.

- [ ] **Step 8: Commit**

```bash
chmod +x plugin/anti-tangent-guard/hooks/check-comment-write
git add plugin/anti-tangent-guard/
git commit -m "feat(guard): prevent comments carrying change history at write time"
```

---

### Task 7: Comment scan in check-task-complete

**Goal:** Add defence in depth at task close for comments that reached disk without passing through `Edit`/`Write` — a Bash heredoc write, most commonly.

**Files:**
- Modify: `plugin/anti-tangent-guard/hooks/check-task-complete`
- Modify: `plugin/anti-tangent-guard/evals/guard-evals.json`, `plugin/anti-tangent-guard/evals/run.sh`
- Modify: `plugin/anti-tangent-guard/README.md` (the coverage limitations this layer cannot cover)

**Acceptance Criteria:**
- [ ] The transcript walk retains each `validate_completion` call's `input`, not only its index and id
- [ ] Only the LAST `validate_completion` in the task window is scanned
- [ ] Inline `final_diff` is scanned; `final_diff_path` is read with an absolute-path check and a size cap, failing open on any error
- [ ] A `final_files`-only completion (no diff of any kind) passes this layer without a block, and the README records that such closes rely on the reviewer layer alone
- [ ] Only `+`-prefixed lines in scannable files are considered, reusing `comment_scan.py`
- [ ] The block message is textually distinct from the two existing block messages
- [ ] A `trace()` line records the new block reason
- [ ] `ANTI_TANGENT_COMMENT_GUARD=0` skips the comment scan while the completion gate still runs
- [ ] Existing 22 cases still pass
- [ ] Every acceptance criterion above has at least one eval: last-call selection, absolute `final_diff_path`, relative-path fail-open, the size cap, the kill switch, the `trace()` reason, `final_files`-only passing, an excluded extension, and an unchanged context line

**Verify:** `bash plugin/anti-tangent-guard/evals/run.sh` → all 44 cases pass

**Steps:**

- [ ] **Step 1: Retain the call input**

In the embedded Python of `check-task-complete`, the loop currently records only `called_idx` and `call_ids` for `validate_completion`. Add a parallel list capturing the input:

```python
                if name == "mcp__anti-tangent__validate_completion":
                    called_idx.append(idx)
                    completion_inputs.append((idx, inp))
                    tuid = c.get("id")
                    if tuid:
                        call_ids[tuid] = idx
```

Declare `completion_inputs = []` beside the other accumulators.

- [ ] **Step 2: Scan the last in-window call**

After the existing verdict decision and before the final exit, add:

```python
def diff_added_lines(inp):
    """Return {path: [added lines]} from a validate_completion call's evidence."""
    text = inp.get("final_diff") or ""
    if not text:
        p = inp.get("final_diff_path") or ""
        if not p or not os.path.isabs(p):
            return {}
        try:
            with open(p) as fh:
                # Read one byte past the cap rather than trusting a prior stat:
                # the file can grow between getsize() and read().
                text = fh.read(2_000_001)
            if len(text) > 2_000_000:
                return {}
        except Exception:
            return {}
    out, cur = {}, None
    for line in text.splitlines():
        if line.startswith("+++ b/"):
            cur = line[6:].strip()
            out.setdefault(cur, [])
        elif cur and line.startswith("+") and not line.startswith("+++"):
            out[cur].append(line[1:])
    return out
```

Select the last call at or after `scan_from` (the same window bound the verdict extraction uses), run `violations()` from `comment_scan` over each path's added lines, and if any are found print a block message beginning:

```
TASK CLOSED WITH COMMENTS CARRYING CHANGE HISTORY
```

then the offending lines, then the same rewrite guidance the write-time hook prints. Exit 2. Call `trace(task_id, "block", "comment-hygiene")` first.

Import at the top of the embedded python: `import os, sys` (if not already) and the shared scanner via the same `sys.path.insert` trick used in `check-comment-write`.

- [ ] **Step 2b: Guard the kill switch**

The comment scan must be skipped when `ANTI_TANGENT_COMMENT_GUARD=0`, while the completion gate still runs — they are separate switches. Read the env var in the bash preamble and pass it into the python as an argument or environment read, and skip only the comment portion.

- [ ] **Step 2c: Record the limitations in the plugin README**

Add a short subsection to `plugin/anti-tangent-guard/README.md` stating what this layer cannot
see: a `final_files`-only or `test_evidence`-only completion carries no diff, so the close-time
comment scan does not run and those closes rely on the reviewer layer (or, for
`test_evidence`-only, are unenforced). Task 9 rewrites the rest of that README; this subsection is
this task's because it documents this task's behaviour.

- [ ] **Step 2d: Teach the runner to assert file contents**

Case 41 asserts the trace log gained a `comment-hygiene` line, and `run.sh` today checks only exit
status and stderr substrings. Add one optional eval field, `expected_file_contains`, as an object
of `{path: [substrings]}`: after the case runs, for each path (with `{{TMPDIR}}` substituted) fail
if the file is missing or any substring is absent, matching fixed strings via `grep -F`. Without
this, case 41 cannot be expressed and the `trace()` acceptance criterion ships unverified.

- [ ] **Step 3: Add evals**

Append to `guard-evals.json`, ids 34–35 (Task 6 ends at 33 — do not reuse an id, the fixture test keys on it):

```json
{
  "id": 34,
  "name": "close-with-history-comment-blocks",
  "input": {
    "tool_name": "TaskUpdate",
    "tool_input": { "status": "completed", "taskId": "1" },
    "transcript_path": "{{TRANSCRIPT}}"
  },
  "transcript_raw_lines": [
    "{\"type\": \"assistant\", \"message\": {\"content\": [{\"type\": \"tool_use\", \"name\": \"TaskUpdate\", \"input\": {\"taskId\": \"1\", \"status\": \"in_progress\"}}]}}",
    "{\"type\": \"assistant\", \"message\": {\"content\": [{\"type\": \"tool_use\", \"id\": \"t1\", \"name\": \"mcp__anti-tangent__validate_completion\", \"input\": {\"final_diff\": \"+++ b/a.go\\n+// fixes #58\\n\"}}]}}",
    "{\"type\": \"user\", \"message\": {\"content\": [{\"type\": \"tool_result\", \"tool_use_id\": \"t1\", \"content\": \"{\\\"tool\\\":\\\"validate_completion\\\",\\\"session_id\\\":\\\"s\\\",\\\"verdict\\\":\\\"pass\\\"}\"}]}}"
  ],
  "expected_exit": 2,
  "expected_stderr_contains": ["TASK CLOSED WITH COMMENTS CARRYING CHANGE HISTORY"],
  "reason": "a passing verdict must not let a change-history comment through"
},
{
  "id": 35,
  "name": "close-with-clean-comment-allows",
  "input": {
    "tool_name": "TaskUpdate",
    "tool_input": { "status": "completed", "taskId": "1" },
    "transcript_path": "{{TRANSCRIPT}}"
  },
  "transcript_raw_lines": [
    "{\"type\": \"assistant\", \"message\": {\"content\": [{\"type\": \"tool_use\", \"name\": \"TaskUpdate\", \"input\": {\"taskId\": \"1\", \"status\": \"in_progress\"}}]}}",
    "{\"type\": \"assistant\", \"message\": {\"content\": [{\"type\": \"tool_use\", \"id\": \"t1\", \"name\": \"mcp__anti-tangent__validate_completion\", \"input\": {\"final_diff\": \"+++ b/a.go\\n+// callers must hold the lock\\n\"}}]}}",
    "{\"type\": \"user\", \"message\": {\"content\": [{\"type\": \"tool_result\", \"tool_use_id\": \"t1\", \"content\": \"{\\\"tool\\\":\\\"validate_completion\\\",\\\"session_id\\\":\\\"s\\\",\\\"verdict\\\":\\\"pass\\\"}\"}]}}"
  ],
  "expected_exit": 0,
  "reason": "an invariant comment in the diff must not block a clean close"
}
```

Add six more, ids 36–41, so every acceptance criterion of this task has an eval rather than only
the two happy paths:

- **36 `close-last-call-wins`** — two `validate_completion` calls in the window, the FIRST carrying
  a violating `final_diff` and the SECOND a clean one. `expected_exit: 0`. Without last-call
  selection a comment that was fixed can never be closed.
- **37 `close-diff-path-blocks`** — `final_diff_path` set to an absolute path the runner has
  materialised, containing a violating added line. `expected_exit: 2`. This needs `run.sh`
  extended to write a `{{DIFFFILE}}` fixture the way it already writes `{{TRANSCRIPT}}`; do it in
  this step, because `final_diff_path` is the route §4.2 tells implementers to PREFER and would
  otherwise ship with no coverage at all.
- **38 `close-relative-diff-path-fails-open`** — `final_diff_path` set to a relative path.
  `expected_exit: 0`, proving the absolute-path check fails open rather than blocking.
- **39 `close-oversized-diff-path-fails-open`** — an absolute `final_diff_path` whose file exceeds
  the 2,000,000-byte cap. `expected_exit: 0`, proving the cap fails open rather than blocking. The
  cap is an acceptance criterion of this task and would otherwise ship unexercised.
- **40 `close-comment-kill-switch`** — `env: {"ANTI_TANGENT_COMMENT_GUARD": "0"}` with case 34's
  violating diff and a passing `validate_completion` in the transcript. `expected_exit: 0`,
  proving the comment scan is skipped while the completion gate still runs.
- **41 `close-trace-records-comment-hygiene`** — case 34's payload with
  `env: {"ANTI_TANGENT_GUARD_TRACE_LOG": "{{TMPDIR}}/trace.log"}`, `expected_exit: 2`, and an
  assertion that the trace file contains `comment-hygiene`. The `trace()` line is an acceptance
  criterion; without this case nothing proves it fires.

- **42 `close-final-files-only-allows`** — a `validate_completion` whose input carries `final_files`
  and no diff of any kind. `expected_exit: 0`. An acceptance criterion of this task asserts exactly
  this, and without a case it is unproven.
- **43 `close-excluded-extension-allows`** — a diff whose only violating added line is in a `.md`
  file. `expected_exit: 0`, pinning the extension allowlist at close time as well as at write time.
- **44 `close-context-line-does-not-block`** — a diff carrying a violating comment as an unchanged
  context line (no `+` prefix). `expected_exit: 0`, pinning added-lines-only at close time.

Bump `EXPECTED_CASE_COUNT` to 44 and the `description` to `(44 cases)`.

- [ ] **Step 4: Run the evals**

Run: `bash plugin/anti-tangent-guard/evals/run.sh`
Expected: all 44 pass, including the original 22.

- [ ] **Step 5: Commit**

```bash
git add plugin/anti-tangent-guard/
git commit -m "feat(guard): scan the submitted diff for change-history comments at close"
```

---

### Task 8: Zero-false-positive run over HEAD

**Goal:** Prove the tell set does not fire on legitimate comments in this repo, because a false positive in the prevention layer blocks an edit mid-task.

> **USER-ORDERED GATE — NON-SKIPPABLE.** This task was requested by the user in the current conversation. It MUST NOT be closed by walking around it, by declaring it "verified inline", or by substituting a cheaper check. Close only after every item in `acceptanceCriteria` has been re-validated independently, with output captured.

**Files:**
- Create: `/tmp/claude-hooks/fp-raw.tsv`, `/tmp/claude-hooks/fp-class.tsv`, `/tmp/claude-hooks/fp-report.sh` (scratch evidence, not committed)
- Modify: `plugin/anti-tangent-guard/hooks/comment_scan.py` (only if a tell must be narrowed)
- Modify: `docs/superpowers/specs/2026-09-08-anti-tangent-v0.19.0-design.md` (record the result)

**Acceptance Criteria:**
- [ ] The scanner is run over every tracked source file at HEAD, treating every comment line as "added"
- [ ] Every hit is hand-classified as a TRUE positive (genuine change history) or a FALSE positive (legitimate comment)
- [ ] The false-positive count is **zero**, either because none were found or because the offending tell was narrowed or moved to the reviewer layer
- [ ] The scan reads HEAD blobs (`git show HEAD:<path>`), not the working tree, and exits non-zero rather than silently skipping any tracked source file it cannot read
- [ ] The raw scan (`fp-raw.tsv`) and the durable classification (`fp-class.tsv`) are kept in SEPARATE files, joined on a stable `path:line` key
- [ ] `fp-report.sh` performs a one-to-one join and rejects unclassified keys, unknown keys and duplicate classifications before counting; it prints `FALSE POSITIVES: 0` and exits 0, and its output is quoted in the completion report
- [ ] Any narrowing is covered by a new eval case

**Verify:** `bash /tmp/claude-hooks/fp-report.sh` → prints `FALSE POSITIVES: 0` (it refuses to print that unless every hit in the raw scan carries exactly one TRUE/FALSE classification)

**Steps:**

- [ ] **Step 1: Run the scanner over HEAD**

```bash
cd /home/pgilmore/Development/Patiently/anti-tangent-mcp
python3 - > /tmp/claude-hooks/fp-raw.tsv <<'PY'
import subprocess, sys
sys.path.insert(0, "plugin/anti-tangent-guard/hooks")
from comment_scan import violations, scannable

# HEAD blobs, not the working tree: the gate is about what is committed, and a
# dirty worktree would otherwise silently change the result.
ls = subprocess.run(["git", "ls-files", "-z"], capture_output=True, text=True)
if ls.returncode != 0:
    # Without this the script scans an empty list, prints zero hits and "passes".
    print("git ls-files failed: %s" % ls.stderr, file=sys.stderr)
    sys.exit(1)
files = [f for f in ls.stdout.split("\0") if f]

total, failed = 0, 0
for f in files:
    if not scannable(f):
        continue
    blob = subprocess.run(["git", "show", "HEAD:" + f], capture_output=True, text=True)
    if blob.returncode != 0:
        # Never skip silently: an unscannable tracked source file invalidates the run.
        print("UNREADABLE\t%s" % f, file=sys.stderr)
        failed += 1
        continue
    for n, raw in enumerate(blob.stdout.splitlines(), 1):
        for line, why in violations(f, [raw]):
            total += 1
            # key = path:line. violations() is called one source line at a
            # time and returns at most one hit per line (it breaks on the first
            # matching tell), so the line number alone is already unique. Do not
            # append a fake occurrence index that is always 1.
            print("%s:%d\t%s\t%s" % (f, n, why, line.replace("\t", " ")[:200]))
print("TOTAL HITS: %d" % total)
if failed:
    print("UNREADABLE FILES: %d" % failed)
    sys.exit(1)
PY
tail -2 /tmp/claude-hooks/fp-raw.tsv
```

Note the split: `fp-raw.tsv` is the regenerable scanner output and Step 3 overwrites it on every
re-run. `fp-class.tsv` is the durable classification. Keeping them in one file would mean each
re-run silently destroyed the classification work. Re-running the scan after narrowing a tell
changes the key set, so re-classify against the NEW raw file — the report below fails loudly if a
classification refers to a key that no longer exists.

- [ ] **Step 2: Hand-classify every hit**

Read `/tmp/claude-hooks/fp-raw.tsv` in full. Each line begins with a unique key `path:line`. For each hit decide:

- **TRUE positive** — the comment genuinely narrates change history ("added in v0.5.0", "see #25", "task-12b review"). No action; the on-touch rule handles it when that code is next edited.
- **FALSE positive** — the comment is legitimate and the reference is load-bearing. Known families to expect: a comment describing the product's own input grammar; a version string stating a wire-compatibility contract ("reads the v0.10.0 stats output"); a test fixture describing a criterion string such as `"AC #1"`.

Write the classification to `/tmp/claude-hooks/fp-class.tsv`, one line per hit, as
`<key><TAB><TRUE|FALSE><TAB><reason>` — where `<key>` is copied verbatim from column 1 of the raw
file. Keying on `path:line` rather than on the comment text is what makes the join
below exact: two identical comments in one file, or a comment containing a tab, cannot collide.

- [ ] **Step 3: Drive false positives to zero**

For each false-positive family, choose ONE:
- **Narrow the tell** in `comment_scan.py` (e.g. require an issue reference to be adjacent to a word like `see`/`fixes`/`issue`/`PR`), or
- **Move the tell to the reviewer layer** — delete it from `TELLS` and confirm `post.tmpl`'s rule already covers that form in prose.

Re-run Step 1 after each change. Do not proceed while any false positive remains.

- [ ] **Step 3b: Compute the counts mechanically**

Create `/tmp/claude-hooks/fp-report.sh` and run it. It refuses to report success unless every raw
hit is classified, so the gate cannot be satisfied by a partial pass:

```bash
cat > /tmp/claude-hooks/fp-report.sh <<'SH'
set -u
RAW=/tmp/claude-hooks/fp-raw.tsv
CLS=/tmp/claude-hooks/fp-class.tsv
python3 - "$RAW" "$CLS" <<'PY'
import collections, sys
raw_p, cls_p = sys.argv[1], sys.argv[2]
raw = [l.split("\t")[0] for l in open(raw_p).read().splitlines()
       if l and not l.startswith(("TOTAL HITS:", "UNREADABLE"))]
cls = collections.defaultdict(list)
for l in open(cls_p).read().splitlines():
    if not l.strip():
        continue
    parts = l.split("\t")
    if len(parts) < 2 or parts[1] not in ("TRUE", "FALSE"):
        print("MALFORMED: %s" % l, file=sys.stderr); sys.exit(1)
    cls[parts[0]].append(parts[1])

# One-to-one join. Counting lines is not enough: duplicate classifications for
# one hit would mask another hit having none.
dup_raw = [k for k, n in collections.Counter(raw).items() if n > 1]
if dup_raw:
    print("DUPLICATE RAW KEYS: %s" % dup_raw[:5], file=sys.stderr)
    sys.exit(1)

missing = [k for k in raw if k not in cls]
unknown = [k for k in cls if k not in set(raw)]
dupes = [k for k, v in cls.items() if len(v) > 1]
for label, ks in (("UNCLASSIFIED", missing), ("UNKNOWN KEY", unknown), ("DUPLICATE", dupes)):
    if ks:
        print("%s: %d -> %s" % (label, len(ks), ks[:5]), file=sys.stderr)
if missing or unknown or dupes:
    sys.exit(1)

fp = sum(1 for k in raw if cls[k][0] == "FALSE")
print("TOTAL HITS: %d" % len(raw))
print("TRUE POSITIVES: %d" % (len(raw) - fp))
print("FALSE POSITIVES: %d" % fp)
sys.exit(0 if fp == 0 else 1)
PY
SH
bash /tmp/claude-hooks/fp-report.sh
```

Expected on completion: `FALSE POSITIVES: 0` and exit 0. While it exits non-zero, the gate is not
met — go back to Step 3.

- [ ] **Step 4: Cover any narrowing with an eval**

If `TELLS` changed, add an eval case to `guard-evals.json` pinning the now-allowed shape at `expected_exit: 0`, bump `EXPECTED_CASE_COUNT` and the description string, and re-run `bash plugin/anti-tangent-guard/evals/run.sh`.

- [ ] **Step 5: Record the result in the spec**

In the spec's Part 3, under "Acceptance criterion for the pattern set", replace the forward-looking paragraph with the measured outcome: total hits, true positives, false positives, and what was narrowed.

- [ ] **Step 6: Commit**

```bash
go test -race ./... 2>&1 | tail -5
bash plugin/anti-tangent-guard/evals/run.sh | tail -5
git add plugin/anti-tangent-guard/ docs/superpowers/specs/2026-09-08-anti-tangent-v0.19.0-design.md
git commit -m "test(guard): drive comment-scan false positives to zero against HEAD"
```

---

### Task 9: Guard plugin metadata, README and marketplace

**Goal:** Make the plugin's own description honest now that it is no longer a completion-gate-only plugin, and bump the versions the release convention requires.

**Files:**
- Modify: `plugin/anti-tangent-guard/.claude-plugin/plugin.json`
- Modify: `plugin/anti-tangent-guard/README.md`
- Modify: `.claude-plugin/marketplace.json`

**Acceptance Criteria:**
- [ ] `plugin.json` version goes `0.1.0` → `0.2.0` and its `description` names both hooks
- [ ] The guard's entry in `.claude-plugin/marketplace.json` mirrors that version and description
- [ ] The marketplace catalog's own top-level `version` is bumped
- [ ] Every statement that the hook blocks in "exactly two cases" is corrected to three
- [ ] The README documents `ANTI_TANGENT_COMMENT_GUARD` alongside `ANTI_TANGENT_COMPLETION_GUARD`, and states four limitations: Bash writes bypass the matcher; `PostToolUse` detects rather than prevents; the scanner reads full-line comments only; and it implements three pattern tells while the reviewer layer covers prose narration the patterns cannot catch

**Verify:** `bash` the Step 4 block → prints `clean`, then `0.2.0` twice, then `ok` for each description and README assertion.

`docs/protocol/controller.md` also carries a stale "two cases" statement. That file belongs to Task 10 and is corrected there — do NOT edit it here, and do not include it in this task's grep.

**Steps:**

- [ ] **Step 1: Bump and rewrite plugin.json**

Set `"version": "0.2.0"` and replace `description` with:

```
Two hooks enforcing anti-tangent-mcp's conventions. A PostToolUse hook on TaskUpdate mandates the validate_completion gate at task close and refuses a close whose submitted diff adds comments carrying change history. A PreToolUse hook on Edit/Write refuses such a comment before it is written. Kill switches: ANTI_TANGENT_COMPLETION_GUARD=0, ANTI_TANGENT_COMMENT_GUARD=0.
```

- [ ] **Step 2: Mirror into the marketplace catalog**

In `.claude-plugin/marketplace.json`, update the `anti-tangent-guard` entry's `version` to `0.2.0` and copy the same `description`. Bump the catalog's own top-level `version` by one minor.

- [ ] **Step 3: Update the README**

- Correct every "exactly two cases" / "two cases" statement to three, naming the new one.
- Add a section for the write-time hook: what it matches, what it scans (added lines, source extensions), and its kill switch.
- State both limitations plainly: `Bash` writes (heredoc, `sed -i`) bypass the `Edit`/`Write` matcher, and `PostToolUse` fires after the state change so the close-time scan detects rather than prevents.

- [ ] **Step 4: Verify no stale claims**

```bash
grep -rn "two cases" plugin/anti-tangent-guard/README.md || echo "clean"   # broad on purpose: review EVERY match
jq -r '.version' plugin/anti-tangent-guard/.claude-plugin/plugin.json
jq -r '.plugins[] | select(.name=="anti-tangent-guard") | .version' .claude-plugin/marketplace.json
# descriptions must match between plugin.json and the marketplace entry
a=$(jq -r '.description' plugin/anti-tangent-guard/.claude-plugin/plugin.json)
b=$(jq -r '.plugins[] | select(.name=="anti-tangent-guard") | .description' .claude-plugin/marketplace.json)
[[ "$a" == "$b" ]] && echo "ok descriptions match" || echo "MISMATCH"
# catalog version: assert the EXACT expected value, not merely "changed".
# Record the pre-edit value in Step 2, compute the one-minor bump, and pin it
# here as <EXPECTED> — inequality alone would accept a patch or major bump.
[[ "$(jq -r '.version' .claude-plugin/marketplace.json)" == "<EXPECTED>" ]] && echo "ok catalog version" || echo "CATALOG VERSION WRONG"
# README must document both switches and both limitations
for n in ANTI_TANGENT_COMMENT_GUARD ANTI_TANGENT_COMPLETION_GUARD Bash PostToolUse; do
  grep -q "$n" plugin/anti-tangent-guard/README.md && echo "ok $n" || echo "MISSING $n"
done
```
Expected: `clean`, then `0.2.0` twice, `ok descriptions match`, the bumped catalog version, and `ok` for all four README greps.

- [ ] **Step 5: Commit**

```bash
git add plugin/anti-tangent-guard/ .claude-plugin/marketplace.json
git commit -m "chore(guard): 0.2.0 — two hooks, and say so everywhere"
```

---

### Task 10: Protocol docs, CLAUDE.md, and bundle resync

**Goal:** Document the pre-gate's terminal state and the comment policy where implementers and controllers actually read them, within the CI byte cap.

**Files:**
- Modify: `docs/protocol/implementer.md`
- Modify: `docs/protocol/controller.md`
- Modify: `CLAUDE.md`
- Modify: `plugin/anti-tangent-protocol/protocol/` (existing bundle; resynced by copy, never hand-edited)
- Modify (conditional): `docs/protocol/authoring.md` — only on the Step 5 fallback, if `implementer.md` will not fit

**Acceptance Criteria:**
- [ ] `implementer.md` §4.2 states the stopping rule for a `warn` carrying only minor findings
- [ ] The comment policy — what a comment may say, what it may not, and the on-touch removal rule — is stated in `implementer.md`, OR (if the byte cap forbids it) stated in full in `authoring.md` with an explicit one-line pointer to it left in `implementer.md`. Both shapes satisfy this criterion; a pointer with no policy behind it does not.
- [ ] `docs/protocol/controller.md`'s statement that the guard blocks in "exactly two cases" is corrected to three
- [ ] `controller.md` carries the matching stopping-rule sentence
- [ ] `CLAUDE.md` states the comment policy for this repo
- [ ] The protocol docs state that the blocking scanner is narrower than the policy — full-line comments and three tells — so a clean hook run is not proof of compliance
- [ ] Each of the four doc assertions in Step 7 prints its `ok` line
- [ ] Every protocol part is under 16,000 bytes, `core.md` unchanged
- [ ] `docs/protocol/` and `plugin/anti-tangent-protocol/protocol/` are byte-identical

**Verify:** `bash scripts/check-protocol-docs.sh` → pass; `diff -r docs/protocol plugin/anti-tangent-protocol/protocol` → no output

**Steps:**

- [ ] **Step 1: Add the stopping rule to §4.2**

In `docs/protocol/implementer.md`, in the numbered step 1 of the pasteable clause, after the existing "Read the findings list…" bullet, add:

```
- A `warn` carrying only `minor` findings is a legitimate place to proceed. The
  verdict is derived from the finding mix, so a well-specified task can sit at
  `warn` indefinitely; iterate while findings are still being resolved, and
  proceed once the remaining ones are prose-level. Do not keep re-validating a
  spec whose findings have stopped changing.
```

- [ ] **Step 2: Add the comment policy**

Append to `docs/protocol/implementer.md` a new section (use the next free number so `scripts/check-protocol-docs.sh`'s tracked-section list is not disturbed — verify with `grep -n '^### 4\.' docs/protocol/implementer.md`):

```
### 4.x Comments

Comments explain non-trivial behaviour, or a non-obvious invariant or hazard
that would bite the next editor. The test: the comment reads correctly to
someone who never saw the change that introduced it.

Comments do NOT carry change history — no issue, pull-request or task
references, no version references, no "previously" / "no longer" / "this
replaced". Git holds that, and a comment repeating it goes stale on the next
change.

When you touch code whose comments break these rules, remove or rewrite them as
part of your task. There is no separate cleanup pass.
```

- [ ] **Step 3: Add the controller sentence**

In `docs/protocol/controller.md`, next to the existing `plan_quality` convergence paragraph, add:

```
The same reading applies one level down: a `validate_task_spec` `warn` whose
findings are all `minor` is a proceed signal, not a defect. Do not send an
implementer back to re-validate a spec whose findings have stopped moving.
```

Then, in the same file, correct the statement that the `anti-tangent-guard` hook blocks in
"exactly two cases" — it is three now. Find it with `grep -n "two cases" docs/protocol/controller.md`
and name the third: a task close whose submitted diff adds comments carrying change history.
(Task 9 corrects the same claim in the plugin's own README; this file is this task's.)

- [ ] **Step 4: Add the policy to this repo's CLAUDE.md**

Add a `## Comments` section to `CLAUDE.md` carrying the same three paragraphs as Step 2, prefaced with one line noting that the `anti-tangent-guard` plugin enforces this at write time and at task close.

- [ ] **Step 5: Check the byte cap BEFORE resyncing**

```bash
wc -c docs/protocol/*.md
```
Every file must be under 16000. If `implementer.md` is at or over, move the Step-2 comment policy into `docs/protocol/authoring.md` (which has ample room) and leave a one-line pointer to it in `implementer.md`.

- [ ] **Step 6: Resync the bundle**

```bash
rm -f plugin/anti-tangent-protocol/protocol/*.md
cp docs/protocol/*.md plugin/anti-tangent-protocol/protocol/
diff -r docs/protocol plugin/anti-tangent-protocol/protocol && echo "in sync"
```

- [ ] **Step 7: Run the doc checks and commit**

```bash
bash scripts/check-protocol-docs.sh

# core.md must be untouched, and nothing may reach the cap
git diff --exit-code -- docs/protocol/core.md && echo "ok core.md unchanged"
awk 'END{}' /dev/null; for f in docs/protocol/*.md; do
  n=$(wc -c < "$f"); [[ "$n" -lt 16000 ]] || { echo "OVER CAP: $f ($n)"; exit 1; }
done; echo "ok every part under 16000"

# the stopping rule reached both audiences
grep -q "have stopped changing" docs/protocol/implementer.md && echo "ok implementer stopping rule"
grep -q "have stopped moving" docs/protocol/controller.md && echo "ok controller stopping rule"

# the comment policy is stated SOMEWHERE authoritative, and CLAUDE.md carries it
# -i is load-bearing: the mandated prose is "Comments do NOT carry change history",
# and a case-sensitive lowercase pattern can never match it.
if grep -qi "not carry change history" docs/protocol/implementer.md; then
  echo "ok policy in implementer.md"
else
  # fallback shape: full policy in authoring.md AND an explicit pointer left behind
  grep -qi "not carry change history" docs/protocol/authoring.md && \
    grep -q "authoring.md" docs/protocol/implementer.md && \
    echo "ok policy in authoring.md with pointer from implementer.md"
fi
grep -q "change history" CLAUDE.md && echo "ok policy in CLAUDE.md"

# no stale block-count claim survives
grep -rn "exactly two cases" docs/protocol/ || echo "ok no stale two-cases claim"

diff -r docs/protocol plugin/anti-tangent-protocol/protocol && echo "ok bundle in sync"
git add docs/protocol/ plugin/anti-tangent-protocol/protocol/ CLAUDE.md
git commit -m "docs(protocol): pre-gate stopping rule and the comment policy"
```

Every `echo` above must fire. Run the block under `set -e` (or append `|| exit 1` to each
assertion) so a missing `ok` stops the sequence mechanically rather than relying on someone
reading the output — otherwise the `git commit` at the end runs regardless.

---

## Self-Review

**Spec coverage:**

| Spec section | Task |
| --- | --- |
| Part 1 — severity calibration (1a, 1b) | 2 |
| Part 2 — `Context:` authority | 3 |
| Part 3 — policy statement | 10 |
| Part 3 — prevention layer | 6 |
| Part 3 — reviewer layer | 5 |
| Part 3 — finish-hook layer | 7 |
| Part 3 — zero-FP acceptance criterion | 8 |
| Part 3 — coverage probe | 1 |
| Part 3 — bookkeeping (README, plugin.json, marketplace, eval counts) | 6, 7, 9 |
| Part 4 — bounded criterion counts | 4 |
| Part 5 — docs, byte cap, bundle resync | 10 |
| Compatibility — no `VERSION` bump | Global Constraints |

**Placeholder scan:** every code step carries real content. Task 7 Step 2 describes the integration in prose plus the two functions it needs, because the surrounding python must be read in place — the executor has the exact accumulator name, the exact window bound, and the exact message prefix.

**Type consistency:** `CountFindings` returns 4 values everywhere after Task 4 (both call sites fixed in the same task). `comment_scan.py` exposes `violations(path, added_lines)`, `added(old, new)` and `scannable(path)` — Task 6 defines them, Tasks 7 and 8 consume exactly those names. The criterion sentinel is `comment_hygiene` in Task 4's allowlist, Task 5's prompt, and Task 7's evals.
