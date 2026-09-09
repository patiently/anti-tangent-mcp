# anti-tangent-mcp v0.19.0 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Recalibrate `validate_task_spec`'s pre-hook prompt so its verdict is actionable, and add a comment-hygiene policy enforced at write time, at review time, and at task close.

**Architecture:** Parts 1–2 are prompt-only edits to `pre.tmpl`, ported from wording already proven in `post.tmpl`. Part 3 adds a comment policy with three enforcement layers — a `PreToolUse` hook that prevents, a `post.tmpl` reviewer rule that judges, and a `check-task-complete` scan that is defence in depth — sharing one scanner implementation. Part 4 adds a bounded `criterion` histogram to the stats ledger so Part 3 is measurable.

**Tech Stack:** Go 1.x (`internal/prompts` text/template + golden tests, `internal/stats`, `internal/mcpsrv`), Bash + Python 3 Claude Code hooks, JSON-driven hook eval harness.

**Spec:** `docs/superpowers/specs/2026-09-08-anti-tangent-v0.19.0-design.md`

## Global Constraints

- **The MCP server embeds prompts via `go:embed`.** Editing a `.tmpl` does not change the behaviour of the *running* server until it is rebuilt and the host restarts it. Do not expect a template edit to alter live `validate_task_spec` output mid-plan, and do not "fix" a template because a live call still shows the old wording.
- **`go test -race ./...` must pass at the end of every task.** Never leave a task with a failing suite.
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
- Modify: `.claude/settings.local.json` (temporary probe hook; **reverted** in the final step)
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
cp .claude/settings.local.json /tmp/claude-hooks/settings.local.json.bak 2>/dev/null || echo "no settings.local.json yet"
```

- [ ] **Step 2: Register the probe hook**

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

Expected: `1` or more. **If this is 0, the probe is broken — stop and fix the probe before drawing any conclusion about subagents.**

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
cp /tmp/claude-hooks/settings.local.json.bak .claude/settings.local.json 2>/dev/null || rm -f .claude/settings.local.json
git diff --stat .claude/settings.local.json
git add docs/superpowers/specs/2026-09-08-anti-tangent-v0.19.0-design.md
git commit -m "docs(spec): record measured PreToolUse subagent reach"
```

---

### Task 2: Calibrate severity in pre.tmpl

**Goal:** Stop `validate_task_spec` emitting `major` for every enumerable implicit assumption, which is what drives 81% of its non-pass verdicts.

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
- [ ] `pre.tmpl` instructs that `Context:` is authoritative and names both suppressed categories
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
	assert.Contains(t, out.User, "`Context:` block in the task spec above is authoritative")
	assert.Contains(t, out.User, "including when it resolves it in code rather than prose")
	assert.Contains(t, out.User, "ambiguous_spec")
}
```

- [ ] **Step 2: Run it and watch it fail**

Run: `go test ./internal/prompts/... -run TestRenderPre_ContextIsAuthoritative -v`
Expected: FAIL.

- [ ] **Step 3: Add the passage**

In `internal/prompts/templates/pre.tmpl`, immediately BEFORE the line beginning `Severity: critical = spec is unimplementable`, insert this paragraph followed by a blank line:

```
The `Context:` block in the task spec above is authoritative. Do not emit `ambiguous_spec` or `missing_acceptance_criterion` for an ambiguity that `Context:` already resolves — including when it resolves it in code rather than prose. If an acceptance criterion reads one way literally but `Context:` explicitly anticipates or approves a different reading, treat `Context:` as the disambiguator rather than emitting a finding.
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
- [ ] With no allowlisted criteria present the map is nil, so `omitempty` keeps the key out of the JSON

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

A comment is a defect when it narrates change history — an issue, pull-request or task reference; a version reference; "previously", "no longer", "this replaced" — or when it restates what the code plainly does, or describes the code inaccurately. Judge only comments the diff ADDS; an untouched comment is out of scope.

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
- [ ] New evals pass and `EXPECTED_CASE_COUNT` matches the new total

**Verify:** `bash plugin/anti-tangent-guard/evals/run.sh` → all cases pass

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
    """Lines present in new_text that are not present in old_text."""
    old = set(old_text.splitlines())
    return [l for l in new_text.splitlines() if l not in old]
```

- [ ] **Step 2: Write the PreToolUse hook**

Create `plugin/anti-tangent-guard/hooks/check-comment-write` (make it executable with `chmod +x`):

```bash
#!/usr/bin/env bash
# PreToolUse hook: refuse an Edit/Write that ADDS a comment carrying change
# history. Behaviour and invariants belong in comments; issue, task and
# version references belong in git.
#
# Fails open on every error: a broken hook must never block work.
set -uo pipefail

TRACE_LOG="${ANTI_TANGENT_GUARD_TRACE_LOG:-/tmp/claude-hooks/anti-tangent-guard.log}"
mkdir -p "$(dirname "$TRACE_LOG")" 2>/dev/null || true
trace() {
    printf '%s | comment-write | %s%s\n' \
        "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "${1:-?}" "${2:+ | $2}" \
        >> "$TRACE_LOG" 2>/dev/null || true
}

[[ "${ANTI_TANGENT_COMMENT_GUARD:-1}" == "0" ]] && { trace "skip" "guard=0"; exit 0; }
trap 'trace "error" "trap-ERR"; exit 0' ERR
command -v python3 >/dev/null 2>&1 || { trace "skip" "no-python3"; exit 0; }

INPUT=$(cat)
python3 - "$CLAUDE_PLUGIN_ROOT" <<'PY' <<<"$INPUT"
import json, os, sys
sys.path.insert(0, os.path.join(sys.argv[1], "hooks"))
try:
    from comment_scan import violations, added, scannable
except Exception:
    sys.exit(0)

try:
    data = json.load(sys.stdin)
except Exception:
    sys.exit(0)

tool = data.get("tool_name") or ""
inp = data.get("tool_input") or {}
path = inp.get("file_path") or ""
if tool not in ("Edit", "Write") or not path or not scannable(path):
    sys.exit(0)

if tool == "Edit":
    lines = added(inp.get("old_string") or "", inp.get("new_string") or "")
else:
    content = inp.get("content") or ""
    try:
        with open(path) as fh:
            lines = added(fh.read(), content)
    except FileNotFoundError:
        lines = content.splitlines()
    except Exception:
        sys.exit(0)

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
PY
```

Note the heredoc order: `<<'PY' <<<"$INPUT"` gives the script on fd for python and the JSON on stdin.

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
echo '{"tool_name":"Write","tool_input":{"file_path":"/tmp/x.go","content":"// fixes #58\npackage x\n"}}' \
  | ./plugin/anti-tangent-guard/hooks/check-comment-write; echo "exit=$?"
```
Expected: exit=2, stderr naming the issue reference.

```bash
echo '{"tool_name":"Write","tool_input":{"file_path":"/tmp/x.md","content":"// fixes #58\n"}}' \
  | ./plugin/anti-tangent-guard/hooks/check-comment-write; echo "exit=$?"
```
Expected: exit=0 — markdown is never scanned.

```bash
echo '{"tool_name":"Write","tool_input":{"file_path":"/tmp/x.go","content":"// POSITIONAL EXTRACTION, NOT FREE SCAN\npackage x\n"}}' \
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
}
```

- [ ] **Step 6: Teach run.sh to dispatch by hook and bump the count**

In `plugin/anti-tangent-guard/evals/run.sh`:
- change `EXPECTED_CASE_COUNT=22` to `EXPECTED_CASE_COUNT=28`
- where the runner invokes the hook, read `.evals[$idx].hook` and default to `check-task-complete`:

```bash
hook_name=$(jq -r ".evals[$idx].hook // \"check-task-complete\"" "$EVALS_FILE")
```
then invoke `"$HOOK_DIR/$hook_name"` instead of the hardcoded script.

Also update the `description` string in `guard-evals.json` from `(22 cases)` to `(28 cases)`.

- [ ] **Step 7: Run the evals**

Run: `bash plugin/anti-tangent-guard/evals/run.sh`
Expected: all 28 pass.

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

**Acceptance Criteria:**
- [ ] The transcript walk retains each `validate_completion` call's `input`, not only its index and id
- [ ] Only the LAST `validate_completion` in the task window is scanned
- [ ] Inline `final_diff` is scanned; `final_diff_path` is read with an absolute-path check and a size cap, failing open on any error
- [ ] Only `+`-prefixed lines in scannable files are considered, reusing `comment_scan.py`
- [ ] The block message is textually distinct from the two existing block messages
- [ ] A `trace()` line records the new block reason
- [ ] Existing 22 cases still pass

**Verify:** `bash plugin/anti-tangent-guard/evals/run.sh` → all cases pass

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
            if os.path.getsize(p) > 2_000_000:
                return {}
            with open(p) as fh:
                text = fh.read()
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

- [ ] **Step 3: Add evals**

Append to `guard-evals.json`, ids 29–30:

```json
{
  "id": 29,
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
  "id": 30,
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

Bump `EXPECTED_CASE_COUNT` to 30 and the `description` to `(30 cases)`.

- [ ] **Step 4: Run the evals**

Run: `bash plugin/anti-tangent-guard/evals/run.sh`
Expected: all 30 pass, including the original 22.

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
- Create: `/tmp/claude-hooks/fp-run.txt` (scratch evidence, not committed)
- Modify: `plugin/anti-tangent-guard/hooks/comment_scan.py` (only if a tell must be narrowed)
- Modify: `docs/superpowers/specs/2026-09-08-anti-tangent-v0.19.0-design.md` (record the result)

**Acceptance Criteria:**
- [ ] The scanner is run over every tracked source file at HEAD, treating every comment line as "added"
- [ ] Every hit is hand-classified as a TRUE positive (genuine change history) or a FALSE positive (legitimate comment)
- [ ] The false-positive count is **zero**, either because none were found or because the offending tell was narrowed or moved to the reviewer layer
- [ ] The classification table and final counts are captured in `/tmp/claude-hooks/fp-run.txt` and quoted in the completion report
- [ ] Any narrowing is covered by a new eval case

**Verify:** `python3 - <<'PY' ... PY` (the harness in Step 1) → prints `FALSE POSITIVES: 0`

**Steps:**

- [ ] **Step 1: Run the scanner over HEAD**

```bash
cd /home/pgilmore/Development/Patiently/anti-tangent-mcp
python3 - > /tmp/claude-hooks/fp-run.txt <<'PY'
import subprocess, sys, os
sys.path.insert(0, "plugin/anti-tangent-guard/hooks")
from comment_scan import violations, scannable
files = subprocess.run(["git","ls-files"], capture_output=True, text=True).stdout.split()
total = 0
for f in files:
    if not scannable(f) or not os.path.exists(f):
        continue
    try:
        lines = open(f, errors="replace").read().splitlines()
    except Exception:
        continue
    for line, why in violations(f, lines):
        total += 1
        print("%s\t%s\t%s" % (f, why, line[:120]))
print("TOTAL HITS: %d" % total)
PY
tail -1 /tmp/claude-hooks/fp-run.txt
```

- [ ] **Step 2: Hand-classify every hit**

Read `/tmp/claude-hooks/fp-run.txt` in full. For each hit decide:

- **TRUE positive** — the comment genuinely narrates change history ("added in v0.5.0", "see #25", "task-12b review"). No action; the on-touch rule handles it when that code is next edited.
- **FALSE positive** — the comment is legitimate and the reference is load-bearing. Known families to expect: a comment describing the product's own input grammar; a version string stating a wire-compatibility contract ("reads the v0.10.0 stats output"); a test fixture describing a criterion string such as `"AC #1"`.

Append the classification to `/tmp/claude-hooks/fp-run.txt` as a two-column table with a one-line reason per FALSE positive.

- [ ] **Step 3: Drive false positives to zero**

For each false-positive family, choose ONE:
- **Narrow the tell** in `comment_scan.py` (e.g. require an issue reference to be adjacent to a word like `see`/`fixes`/`issue`/`PR`), or
- **Move the tell to the reviewer layer** — delete it from `TELLS` and confirm `post.tmpl`'s rule already covers that form in prose.

Re-run Step 1 after each change. Do not proceed while any false positive remains.

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
- [ ] The README documents `ANTI_TANGENT_COMMENT_GUARD` alongside `ANTI_TANGENT_COMPLETION_GUARD`, and states the Bash-write and `PostToolUse`-timing limitations

**Verify:** `grep -rn "exactly two cases\|two cases" plugin/anti-tangent-guard/README.md docs/protocol/controller.md` → no stale matches; `jq -r '.version' plugin/anti-tangent-guard/.claude-plugin/plugin.json` → `0.2.0`

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
grep -rn "exactly two cases\|blocks in two" plugin/anti-tangent-guard/README.md || echo "clean"
jq -r '.version' plugin/anti-tangent-guard/.claude-plugin/plugin.json
jq -r '.plugins[] | select(.name=="anti-tangent-guard") | .version' .claude-plugin/marketplace.json
```
Expected: `clean`, then `0.2.0` twice.

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
- Modify: `plugin/anti-tangent-protocol/protocol/*.md` (resync, not hand-edited)

**Acceptance Criteria:**
- [ ] `implementer.md` §4.2 states the stopping rule for a `warn` carrying only minor findings
- [ ] `implementer.md` states the comment policy: what a comment may say, what it may not, and the on-touch removal rule
- [ ] `controller.md` carries the matching stopping-rule sentence
- [ ] `CLAUDE.md` states the comment policy for this repo
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
go test -race ./... 2>&1 | tail -5
git add docs/protocol/ plugin/anti-tangent-protocol/protocol/ CLAUDE.md
git commit -m "docs(protocol): pre-gate stopping rule and the comment policy"
```

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
