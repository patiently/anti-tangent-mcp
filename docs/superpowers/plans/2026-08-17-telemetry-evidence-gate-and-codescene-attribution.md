# Telemetry, Evidence Gate, and CodeScene Attribution Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship v0.15.0 — correct the plan-header adoption metric, make evidence-shaped rejections cheap, let anti-tangent observe and attribute CodeScene runs per task, produce an end-of-plan per-task report, and slice the protocol document by reader role.

**Architecture:** Five independent server-side additions (all default-off) plus one documentation restructure. The restructure lands first because every later documentation edit needs to know which file it is editing, and because the widened skill trigger is only affordable once a false fire costs a role slice rather than the whole protocol. CodeScene results arrive in-band as a structured `codescene` argument on `validate_completion`, keyed to the session; that single primitive makes both "did it run" and per-task attribution possible.

**Tech Stack:** Go 1.x, `github.com/modelcontextprotocol/go-sdk/mcp`, `stretchr/testify`, `net/http` (no vendor SDKs), Go `text/template` with golden-file tests, GitHub Actions.

**User decisions (already made):**
- "One design, one release" — all three bodies of work ship together as v0.15.0.
- CodeScene ingestion: "In-band arg on validate_completion", not hook-file correlation.
- Enforcement: "Env-gated, major, tagged submission defect" — `ANTI_TANGENT_CODESCENE=required`, unset means today's behaviour.
- Plan report: "plan_run_id correlation + report tool", not stateless envelope collection.
- Header flag: "Fix casing and give it a real consumer", not deletion.
- Doc handling: "Split by audience, skill reads only what's needed."
- Source-of-truth shape: "INTEGRATION.md becomes a thin router" over `docs/protocol/` parts — no assembly step, no generated file.

**Spec:** [`docs/superpowers/specs/2026-08-17-telemetry-evidence-gate-and-codescene-attribution-design.md`](../specs/2026-08-17-telemetry-evidence-gate-and-codescene-attribution-design.md)

**Branch:** `version/0.15.0` (already created; spec committed at `2fe7f84`, revised at `b717761`)

---

## Measured slice sizes (correcting the spec's estimates)

The spec's §7.2 table gave approximate sizes. Measured from `INTEGRATION.md` at HEAD:

| Part | Source lines | Bytes |
|---|---|---|
| `core.md` | 1–17, 19–51, 53–69, 454–483 | 9,798 |
| `authoring.md` | 71–140 | 4,212 |
| `implementer.md` | 142–273 | 9,174 |
| `controller.md` | 378–452 | 7,086 |
| `project-knowledge.md` | 275–376 | 9,686 |

Role read totals: implementer 18,972 B (was 39,963), controller 16,884 B, plan author 14,010 B, controller-with-KB 26,570 B. The `< 16,000` per-part cap from spec §7.2 holds with the largest part at 9,798 B.

---

## File Structure

**New Go packages**

- `internal/codescene/codescene.go` — `Digest`, `Verdicts`, `Normalize()`. Leaf package: imports nothing from this repo, so both `internal/stats` and `internal/mcpsrv` can depend on it without a cycle.
- `internal/planrun/planrun.go` — `TaskRow`, `Run`, in-memory `Store` with TTL eviction. Mirrors `internal/session/store.go`'s shape.
- `internal/planrun/ledger.go` — optional JSONL persistence and read-back. Split from the store so the store stays pure in-memory and testable without a filesystem.
- `internal/planrun/report.go` — deterministic rendering of a `Run` into the paste-ready block. Split so rendering can be golden-tested without touching the store.

**New documentation tree**

- `INTEGRATION.md` — shrinks to a ~1 KB router.
- `docs/protocol/{core,authoring,implementer,controller,project-knowledge}.md` — the protocol, sliced by reader role. Source of truth.
- `plugin/anti-tangent-protocol/protocol/*.md` — byte-identical bundle of the above.

**Modified**

- `internal/planparser/planparser.go` — case-insensitive header matching.
- `internal/stats/{event,rollup,codescene}.go` — `plan_headers` telemetry; `CodesceneEvent` embeds `codescene.Digest`.
- `internal/config/config.go` — `Codescene`, `PlanLedger` fields.
- `internal/verdict/verdict.go` — two server-only categories.
- `internal/mcpsrv/{handlers,server,summary}.go` — the `codescene` arg, `submission_defect_only`, `plan_run_id` threading, the seventh tool.
- `internal/mcpsrv/submission_defect.go` — new file; the classification helper kept out of the already-large `handlers.go`.
- `internal/prompts/templates/post.tmpl` + `internal/prompts/prompts.go` — CodeScene block, `insufficient_evidence` instruction.
- `.github/workflows/ci.yml` — per-part cap, directory match, doc guards.
- `plugin/anti-tangent-protocol/{.claude-plugin/plugin.json,README.md,skills/anti-tangent-protocol/SKILL.md}`.

`handlers.go` is already ~1,600 lines. This plan adds to it but pushes every genuinely separable helper (`submission_defect.go`, the planrun wiring) into its own file rather than growing it further.

---

## Task Sequencing

```
1 ─┬─> 2
   └─> 11 ──> 12 ──> 13
3 (independent)
4 ──> 6
5 (independent)
7 ──> 8 ──> 9 ──> 10
5, 6, 8, 9, 10 ─────> 11
```

---

### Task 1: Split the protocol into role slices

**Goal:** `INTEGRATION.md` becomes a router over five role-scoped parts under `docs/protocol/`, bundled byte-identically into the plugin, with CI enforcing a per-part cap and a directory match.

**Files:**
- Create: `docs/protocol/core.md`
- Create: `docs/protocol/authoring.md`
- Create: `docs/protocol/implementer.md`
- Create: `docs/protocol/controller.md`
- Create: `docs/protocol/project-knowledge.md`
- Modify: `INTEGRATION.md` (replace entirely with the router)
- Delete: `plugin/anti-tangent-protocol/INTEGRATION.md`
- Create: `plugin/anti-tangent-protocol/protocol/` (bundle copy of `docs/protocol/`)
- Modify: `.github/workflows/ci.yml:49-73`

**Acceptance Criteria:**
- [ ] Every section identifier that exists in `INTEGRATION.md` at HEAD (`## Scope and limits`, `## 1.`, `### 3.1`–`### 3.8`, `## 4.`, `### 4.2`, `### 4.3`, `## 5.`, `### 5.1`–`### 5.7`, `## 6.`) appears exactly once across `docs/protocol/*.md`, with identical numbering.
- [ ] No prose content is lost: concatenating the five parts yields the same set of non-blank lines as the original file (ordering may differ, separators may differ).
- [ ] `INTEGRATION.md` is under 2,000 bytes and links to all five parts.
- [ ] Every part is under 16,000 bytes.
- [ ] `diff -r docs/protocol plugin/anti-tangent-protocol/protocol` is clean.
- [ ] The CI job fails when a part exceeds the cap and when the bundle drifts (both proven by temporary local runs of the job's shell body).

**Verify:** `bash -c 'for f in docs/protocol/*.md; do b=$(wc -c < "$f"); echo "$f $b"; [ "$b" -lt 16000 ] || exit 1; done && diff -r docs/protocol plugin/anti-tangent-protocol/protocol && [ $(wc -c < INTEGRATION.md) -lt 2000 ] && echo OK'` → prints each part's size then `OK`

**Steps:**

- [ ] **Step 1: Capture a baseline of the current content**

This is the safety net for the "no prose lost" criterion. Run before touching anything:

```bash
cd /home/pgilmore/Development/Patiently/anti-tangent-mcp
grep -v '^\s*$' INTEGRATION.md | sort > /tmp/protocol-baseline.txt
wc -l /tmp/protocol-baseline.txt
```

- [ ] **Step 2: Carve the parts by line range**

The ranges below were measured against HEAD. `---` separator lines (17, 51, 69, 140, 273, 376, 452) are excluded; each part gets its own heading instead.

```bash
mkdir -p docs/protocol

# core.md = intro (1-16) + scope&limits (19-49) + §1 (53-67) + §6 FAQ (454-483)
{ sed -n '1,16p'   INTEGRATION.md
  echo; echo '---'; echo
  sed -n '19,49p'  INTEGRATION.md
  echo; echo '---'; echo
  sed -n '53,67p'  INTEGRATION.md
  echo; echo '---'; echo
  sed -n '454,483p' INTEGRATION.md
} > docs/protocol/core.md

sed -n '71,138p'  INTEGRATION.md > docs/protocol/authoring.md
sed -n '142,271p' INTEGRATION.md > docs/protocol/implementer.md
sed -n '275,374p' INTEGRATION.md > docs/protocol/project-knowledge.md
sed -n '378,450p' INTEGRATION.md > docs/protocol/controller.md
```

- [ ] **Step 3: Add a per-part header and cross-role pointer**

Each part opens with a one-line statement of who it is for and where to go next. This is the mitigation for the "reader on the wrong side of a boundary" risk in spec §10. Prepend to each file:

`docs/protocol/core.md`:

```markdown
# anti-tangent protocol — core

**Everyone reads this part.** It carries the tool surface, what the reviewer can and cannot
catch, when the protocol applies at all, and the FAQ. Then read the part for your role:
[`authoring.md`](authoring.md) (drafting task blocks), [`implementer.md`](implementer.md)
(writing the code), [`controller.md`](controller.md) (dispatching subagents or running a plan
end to end).
```

`docs/protocol/authoring.md`:

```markdown
# anti-tangent protocol — for plan authors

Read [`core.md`](core.md) first. This part covers the task-block format that maps onto
`validate_task_spec` inputs. If you will also dispatch the plan, read
[`controller.md`](controller.md).
```

`docs/protocol/implementer.md`:

```markdown
# anti-tangent protocol — for implementers

Read [`core.md`](core.md) first. This part covers the per-task lifecycle, the dispatch clause,
lightweight mode, and the CodeScene companion. If your task block looks thin or ambiguous, the
format it should have had is in [`authoring.md`](authoring.md).
```

`docs/protocol/controller.md`:

```markdown
# anti-tangent protocol — for controllers

Read [`core.md`](core.md) first. This part covers the plan-handoff gate, the dispatch addendum,
and end-of-run reporting. The clause you paste into each subagent lives in
[`implementer.md`](implementer.md) §4.2. If a project knowledge base is in play, also read
[`project-knowledge.md`](project-knowledge.md).
```

`docs/protocol/project-knowledge.md`:

```markdown
# anti-tangent protocol — project knowledge (optional)

Read [`core.md`](core.md) and [`controller.md`](controller.md) first. This part is
**controller-only** — implementing subagents make no prime/extract calls. Skip it entirely
unless a knowledge base is configured.
```

- [ ] **Step 4: Replace INTEGRATION.md with the router**

```markdown
# Integrating anti-tangent-mcp

`anti-tangent-mcp` is an advisory MCP server that helps prevent implementing-subagent drift
while working on **tasks from a written implementation plan**. The protocol is split by reader
role so an agent loads only what applies to it.

**Install and configure:** see [`README.md`](README.md).

| Part | Who reads it | Covers |
|---|---|---|
| [`docs/protocol/core.md`](docs/protocol/core.md) | everyone | tool surface, scope and limits, §1 when the protocol applies, §6 FAQ and finding categories |
| [`docs/protocol/authoring.md`](docs/protocol/authoring.md) | plan authors | §3 task-block format, normative test bodies, attestations |
| [`docs/protocol/implementer.md`](docs/protocol/implementer.md) | implementing subagents | §4 lifecycle, the paste-in dispatch clause, lightweight mode, CodeScene companion |
| [`docs/protocol/controller.md`](docs/protocol/controller.md) | controllers | §5 plan-handoff gate, dispatch addendum, end-of-run reporting |
| [`docs/protocol/project-knowledge.md`](docs/protocol/project-knowledge.md) | controllers, only with a KB | prime/extract loop, note types, Basic Memory translation |

Section numbers are stable across the split: §1 and §6 are in `core.md`, §3 in `authoring.md`,
§4 in `implementer.md`, §5 in `controller.md`.

The authoritative design is
[`docs/superpowers/specs/2026-05-07-anti-tangent-mcp-design.md`](docs/superpowers/specs/2026-05-07-anti-tangent-mcp-design.md).
```

- [ ] **Step 5: Verify no prose was lost**

```bash
cat docs/protocol/*.md | grep -v '^\s*$' | sort > /tmp/protocol-after.txt
# Expect: only the new per-part headers and router lines as additions, nothing removed.
comm -23 /tmp/protocol-baseline.txt /tmp/protocol-after.txt
```

Expected: empty output. Any line printed here was dropped by the carve and must be restored.

- [ ] **Step 6: Verify section identifiers survived exactly once**

```bash
for s in '## Scope and limits' '## 1\.' '### 3\.1' '### 3\.2' '### 3\.3' '### 3\.5' '### 3\.6' '### 3\.7' '### 3\.8' '## 4\.' '### 4\.2' '### 4\.3' '## 5\.' '### 5\.1' '### 5\.2' '### 5\.3' '### 5\.4' '### 5\.5' '### 5\.6' '### 5\.7' '## 6\.'; do
  n=$(grep -hcE "^$s" docs/protocol/*.md | paste -sd+ | bc)
  [ "$n" = "1" ] || echo "FAIL: '$s' found $n times"
done
echo "section check done"
```

Expected: only `section check done`. (There is no §2 in the source document — that gap is pre-existing and intentional.)

- [ ] **Step 7: Resync the plugin bundle**

```bash
git rm -q plugin/anti-tangent-protocol/INTEGRATION.md
mkdir -p plugin/anti-tangent-protocol/protocol
rm -f plugin/anti-tangent-protocol/protocol/*.md
cp docs/protocol/*.md plugin/anti-tangent-protocol/protocol/
diff -r docs/protocol plugin/anti-tangent-protocol/protocol && echo "bundle in sync"
```

- [ ] **Step 8: Rewrite the CI job**

Replace `.github/workflows/ci.yml:49-73` (job `integration-size`) with:

```yaml
  protocol-docs:
    name: Protocol docs budget & bundle
    runs-on: ubuntu-latest
    steps:
      - name: Checkout code
        uses: actions/checkout@v6

      - name: Verify each protocol part is under the 16,000-byte budget
        run: |
          FAIL=0
          for f in docs/protocol/*.md; do
            BYTES=$(wc -c < "$f")
            echo "$f = $BYTES bytes (budget: strictly < 16000)"
            if [ "$BYTES" -ge 16000 ]; then
              echo "::error file=$f::$f is $BYTES bytes; must be < 16000. This file is Read in full by every agent whose role matches it — the cost is paid once per dispatched subagent, not once per session. Split it or move reference detail into docs/team-setup/."
              FAIL=1
            fi
          done
          exit $FAIL

      - name: Verify INTEGRATION.md is still a router, not a monolith
        run: |
          BYTES=$(wc -c < INTEGRATION.md)
          echo "INTEGRATION.md = $BYTES bytes (budget: strictly < 2000)"
          if [ "$BYTES" -ge 2000 ]; then
            echo "::error file=INTEGRATION.md::INTEGRATION.md is $BYTES bytes; it is an index over docs/protocol/, not a place for protocol text. Put the content in the matching part file."
            exit 1
          fi

      - name: Verify anti-tangent-protocol bundled protocol matches docs/protocol
        run: |
          if ! diff -r docs/protocol plugin/anti-tangent-protocol/protocol; then
            echo "::error file=plugin/anti-tangent-protocol/protocol::Bundled copy has drifted. Resync with: rm -f plugin/anti-tangent-protocol/protocol/*.md && cp docs/protocol/*.md plugin/anti-tangent-protocol/protocol/"
            exit 1
          fi
          echo "✓ bundled plugin copy matches docs/protocol"
```

Then update the `build-test` job's `needs:` at `.github/workflows/ci.yml:78` from `[changelog, integration-size]` to `[changelog, protocol-docs]`.

- [ ] **Step 9: Prove the guards actually fail**

Run the job bodies locally against deliberately broken states, then restore:

```bash
# cap guard
printf '%*s' 16100 '' >> docs/protocol/core.md
for f in docs/protocol/*.md; do b=$(wc -c < "$f"); [ "$b" -lt 16000 ] || echo "correctly caught: $f = $b"; done
git checkout docs/protocol/core.md

# bundle guard
echo "drift" >> plugin/anti-tangent-protocol/protocol/core.md
diff -r docs/protocol plugin/anti-tangent-protocol/protocol >/dev/null || echo "correctly caught bundle drift"
cp docs/protocol/core.md plugin/anti-tangent-protocol/protocol/core.md
diff -r docs/protocol plugin/anti-tangent-protocol/protocol && echo "restored"
```

- [ ] **Step 10: Commit**

```bash
git add -A docs/protocol INTEGRATION.md plugin/anti-tangent-protocol .github/workflows/ci.yml
git commit -m "docs: split protocol into role-scoped parts under docs/protocol/

INTEGRATION.md becomes a router. Section numbers are preserved verbatim so
external references (README, ~/.claude mirrors, §5.1 citations) still resolve
to a findable section. CI gains a per-part byte cap and a directory match in
place of the single-file budget."
```

---

### Task 2: Make the plugin skill role-scoped and reachable at end-of-run

**Goal:** The skill reads `core.md` plus only the part matching the agent's role, and its trigger covers the end-of-plan moment when `plan_run_report` is meant to be called.

**Files:**
- Modify: `plugin/anti-tangent-protocol/skills/anti-tangent-protocol/SKILL.md`
- Modify: `plugin/anti-tangent-protocol/.claude-plugin/plugin.json:3`
- Modify: `plugin/anti-tangent-protocol/README.md`

**Acceptance Criteria:**
- [ ] `SKILL.md`'s frontmatter description includes an end-of-run trigger clause.
- [ ] Step 1 reads `../../protocol/core.md` plus a role-selected part; no path in the file refers to `INTEGRATION.md`.
- [ ] Step 2's controller bullet names `plan_run_report` and the `plan_run_id` it needs.
- [ ] `plugin.json`'s description no longer claims to load "the full INTEGRATION.md".
- [ ] Every relative path referenced in `SKILL.md` resolves to a file that exists.

**Verify:** `bash -c 'cd plugin/anti-tangent-protocol/skills/anti-tangent-protocol && for p in $(grep -oE "\.\./\.\./protocol/[a-z-]+\.md" SKILL.md | sort -u); do test -f "$p" || { echo "MISSING $p"; exit 1; }; done && ! grep -q INTEGRATION.md SKILL.md ../../.claude-plugin/plugin.json && echo OK'` → `OK`

**Steps:**

- [ ] **Step 1: Rewrite SKILL.md**

Replace the whole file:

```markdown
---
name: anti-tangent-protocol:anti-tangent-protocol
description: Use when you are about to implement, or dispatch a subagent to implement, a task that has a Goal / Acceptance-criteria header from an implementation plan — or when a multi-task plan run has just finished and you need to report on it. Loads the anti-tangent-mcp drift-protection protocol (validate_task_spec → check_progress → validate_completion; validate_plan and plan_run_report for controllers).
---

# anti-tangent-protocol

Loads the anti-tangent-mcp integration protocol on demand, sliced by role, so an agent reads
only the part that applies to it.

## When this applies

Either of:

- The current task carries a structured **Goal / Acceptance criteria / (Non-goals) /
  (Context)** header from an implementation plan.
- A multi-task plan run has just finished and you are reporting on it.

For read-only research, Q&A, ad-hoc edits, plan authoring, or code review, the protocol does
not apply — stop here.

## Step 1 — Read the protocol for your role

Paths are relative to this skill file.

Always read `../../protocol/core.md` — tool surface, scope and limits, when the protocol
applies, FAQ.

Then read the part matching what you are about to do:

- **Implementer** (you will write the code for this task): `../../protocol/implementer.md`
- **Controller** (you dispatch subagents, or you are running a plan end to end):
  `../../protocol/controller.md`
- **Plan author** (you are drafting or revising task blocks): `../../protocol/authoring.md`
- Additionally, **only if a project knowledge base is in play**:
  `../../protocol/project-knowledge.md`

Read more than one part when you hold more than one role. Do not read all of them by default —
the project-knowledge part in particular is controller-only, and an implementing subagent makes
no prime/extract calls.

## Step 2 — Follow the clause that fits your role

- **Implementer:** follow the §4 lifecycle — call `validate_task_spec` before editing,
  `validate_completion` before reporting DONE, and paste its `summary_block`. When CodeScene
  MCP is configured, pass the `analyze_change_set` result as the `codescene` argument.
- **Controller:** run the §5.1 `validate_plan` handoff gate before dispatch, capture the
  returned `plan_run_id`, and paste the §4.2 clause into each implementer's prompt. When the
  last task has reported DONE, call `plan_run_report` with that `plan_run_id` and surface the
  table to the user.

Anti-tangent is advisory — it never blocks. Treat `critical` / `major` findings as
blocking-or-explain per the protocol. A response carrying `submission_defect_only: true` is
telling you the submission was incomplete, not that the code is wrong: attach what is missing
and re-submit.
```

- [ ] **Step 2: Correct plugin.json's description**

Replace line 3's value with:

```json
  "description": "On-demand loader for the anti-tangent-mcp drift-protection protocol. A description-triggered skill that loads only the role-scoped part of the protocol an agent actually needs — keeping the always-loaded footprint to one line, and the on-demand read to roughly half of what a single combined document would cost.",
```

- [ ] **Step 3: Update the plugin README**

`Read` `plugin/anti-tangent-protocol/README.md` and replace every reference to the bundled
`INTEGRATION.md` with the `protocol/` directory and its five parts, matching the router table
from Task 1 Step 4.

- [ ] **Step 4: Verify paths resolve**

```bash
cd plugin/anti-tangent-protocol/skills/anti-tangent-protocol
for p in $(grep -oE '\.\./\.\./protocol/[a-z-]+\.md' SKILL.md | sort -u); do
  test -f "$p" && echo "ok $p" || { echo "MISSING $p"; exit 1; }
done
cd - >/dev/null
grep -rn "INTEGRATION.md" plugin/anti-tangent-protocol/ && echo "FAIL: stale reference" || echo "no stale references"
```

- [ ] **Step 5: Commit**

```bash
git add plugin/anti-tangent-protocol
git commit -m "feat(plugin): role-scoped protocol reads and end-of-run trigger

The skill now reads core.md plus only the part matching the agent's role.
The description gains an end-of-run clause: plan_run_report is called after
the last task reports DONE, a moment at which the previous trigger ('about to
implement') was false, so the tool would never have been reached."
```

---

### Task 3: Case-insensitive header matching with real telemetry

**Goal:** `HasStructuredHeader` matches either casing and stops being dead code — `validate_plan` reports header adoption into `rollup.json`.

**Files:**
- Modify: `internal/planparser/planparser.go:104-106`
- Modify: `internal/planparser/planparser_test.go`
- Modify: `internal/stats/event.go:19-32`
- Modify: `internal/stats/rollup.go:17-33`
- Modify: `internal/stats/rollup_test.go`
- Modify: `internal/mcpsrv/handlers.go` (`statParams`, `recordStat`, `ValidatePlan`)
- Modify: `internal/mcpsrv/handlers_stats_test.go`

**Acceptance Criteria:**
- [ ] `**Acceptance Criteria:**` (capital C) and `**acceptance criteria:**` both match.
- [ ] A body with only one of the two markers does not match.
- [ ] `validate_plan` stats events carry `tasks_total` and `tasks_with_header`.
- [ ] `rollup.json` gains a `plan_headers` block with `tasks_total`, `tasks_with_header`, `adoption`; the key is absent entirely when the window has no `validate_plan` events.
- [ ] `adoption` is 0 when `tasks_total` is 0 (no division by zero).

**Verify:** `go test -race ./internal/planparser/... ./internal/stats/... ./internal/mcpsrv/...` → PASS

**Steps:**

- [ ] **Step 1: Write the failing parser tests**

Append to `internal/planparser/planparser_test.go`:

```go
func TestSplitTasks_HasStructuredHeader_CaseInsensitive(t *testing.T) {
	// superpowers writing-plans emits a capital C and grep-enforces it.
	plan := "### Task 1: t\n\n**Goal:** g\n\n**Acceptance Criteria:**\n- ac\n"
	tasks, _ := SplitTasks(plan)
	require.Len(t, tasks, 1)
	assert.True(t, tasks[0].HasStructuredHeader, "capital C must match")
}

func TestSplitTasks_HasStructuredHeader_AllLowercase(t *testing.T) {
	plan := "### Task 1: t\n\n**goal:** g\n\n**acceptance criteria:**\n- ac\n"
	tasks, _ := SplitTasks(plan)
	require.Len(t, tasks, 1)
	assert.True(t, tasks[0].HasStructuredHeader)
}

func TestSplitTasks_HasStructuredHeader_RequiresBothMarkers(t *testing.T) {
	plan := "### Task 1: t\n\n**Acceptance Criteria:**\n- ac\n"
	tasks, _ := SplitTasks(plan)
	require.Len(t, tasks, 1)
	assert.False(t, tasks[0].HasStructuredHeader, "goal marker absent")
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/planparser/ -run HasStructuredHeader -v`
Expected: FAIL — the capital-C and lowercase cases assert True but get False.

- [ ] **Step 3: Make the matcher case-insensitive**

Replace `internal/planparser/planparser.go:104-106`:

```go
// structuredGoalRE and structuredACRE match the two header markers
// case-insensitively. Case matters here: superpowers' writing-plans skill —
// the dominant plan generator — emits "**Acceptance Criteria:**" with a
// capital C and grep-enforces it, so a case-sensitive match scored every
// plan it produced as headerless.
var (
	structuredGoalRE = regexp.MustCompile(`(?i)\*\*Goal:\*\*`)
	structuredACRE   = regexp.MustCompile(`(?i)\*\*Acceptance criteria:\*\*`)
)

func hasStructuredHeader(body string) bool {
	return structuredGoalRE.MatchString(body) && structuredACRE.MatchString(body)
}
```

Update the doc comment at `internal/planparser/planparser.go:18-21`:

```go
	// HasStructuredHeader is true iff the body contains both **Goal:** and
	// **Acceptance criteria:** markers, matched case-insensitively. Reported
	// as plan-header adoption telemetry by validate_plan; never sent to the
	// reviewer.
	HasStructuredHeader bool
```

- [ ] **Step 4: Run to verify they pass**

Run: `go test -race ./internal/planparser/...`
Expected: PASS

- [ ] **Step 5: Write the failing stats rollup test**

Append to `internal/stats/rollup_test.go`:

```go
func TestComputeRollup_PlanHeaders(t *testing.T) {
	now := time.Now().UTC()
	events := []Event{
		{Ts: now, Tool: "validate_plan", TasksTotal: 10, TasksWithHeader: 8},
		{Ts: now, Tool: "validate_plan", TasksTotal: 10, TasksWithHeader: 10},
		{Ts: now, Tool: "validate_task_spec"},
	}
	r := computeRollup(events, now)
	require.NotNil(t, r.PlanHeaders)
	assert.Equal(t, 20, r.PlanHeaders.TasksTotal)
	assert.Equal(t, 18, r.PlanHeaders.TasksWithHeader)
	assert.InDelta(t, 0.9, r.PlanHeaders.Adoption, 0.0001)
}

func TestComputeRollup_PlanHeaders_AbsentWithoutPlanEvents(t *testing.T) {
	now := time.Now().UTC()
	r := computeRollup([]Event{{Ts: now, Tool: "validate_completion"}}, now)
	assert.Nil(t, r.PlanHeaders, "absence must mean no plan events, not zero adoption")

	b, err := json.Marshal(r)
	require.NoError(t, err)
	assert.NotContains(t, string(b), "plan_headers")
}

func TestComputeRollup_PlanHeaders_ZeroTasksNoDivideByZero(t *testing.T) {
	now := time.Now().UTC()
	r := computeRollup([]Event{{Ts: now, Tool: "validate_plan", TasksTotal: 0}}, now)
	require.NotNil(t, r.PlanHeaders)
	assert.Equal(t, 0.0, r.PlanHeaders.Adoption)
}
```

- [ ] **Step 6: Run to verify it fails**

Run: `go test ./internal/stats/ -run PlanHeaders -v`
Expected: FAIL — `Event` has no field `TasksTotal`; compile error.

- [ ] **Step 7: Add the Event fields**

Append to the `Event` struct in `internal/stats/event.go:19-32`:

```go
	// TasksTotal and TasksWithHeader are set only on validate_plan events:
	// how many tasks the plan parsed into, and how many carried a structured
	// Goal / Acceptance criteria header. Plan-header adoption telemetry.
	TasksTotal      int `json:"tasks_total,omitempty"`
	TasksWithHeader int `json:"tasks_with_header,omitempty"`
```

- [ ] **Step 8: Add the rollup block**

Add to `internal/stats/rollup.go`, after the `Rollup` struct:

```go
// PlanHeadersRollup reports structured-header adoption across the window's
// validate_plan calls. Nil (and so absent from rollup.json) when the window
// contains no plan events — absence means "no data", not "zero adoption".
type PlanHeadersRollup struct {
	TasksTotal      int     `json:"tasks_total"`
	TasksWithHeader int     `json:"tasks_with_header"`
	Adoption        float64 `json:"adoption"`
}
```

Add the field to `Rollup` (after `Codescene`):

```go
	PlanHeaders *PlanHeadersRollup `json:"plan_headers,omitempty"`
```

In `computeRollup`, accumulate inside the existing event loop:

```go
		if e.Tool == "validate_plan" {
			planEvents++
			planTasks += e.TasksTotal
			planTasksWithHeader += e.TasksWithHeader
		}
```

declaring `var planEvents, planTasks, planTasksWithHeader int` alongside the existing
`var totalFindings, cached, partial int`, and after the loop, before `return r`:

```go
	if planEvents > 0 {
		ph := &PlanHeadersRollup{TasksTotal: planTasks, TasksWithHeader: planTasksWithHeader}
		if planTasks > 0 {
			ph.Adoption = float64(planTasksWithHeader) / float64(planTasks)
		}
		r.PlanHeaders = ph
	}
```

- [ ] **Step 9: Thread the counts from ValidatePlan**

Add to `statParams` in `internal/mcpsrv/handlers.go:273-283`:

```go
	tasksTotal      int
	tasksWithHeader int
```

Add to the `stats.Event` literal in `recordStat` (`internal/mcpsrv/handlers.go:293-306`):

```go
		TasksTotal:      p.tasksTotal,
		TasksWithHeader: p.tasksWithHeader,
```

In `ValidatePlan`, the plan is already split via `planparser.SplitTasks`. Locate that call, and
immediately after it compute:

```go
	tasksTotal := len(rawTasks)
	tasksWithHeader := 0
	for _, rt := range rawTasks {
		if rt.HasStructuredHeader {
			tasksWithHeader++
		}
	}
```

then pass `tasksTotal: tasksTotal, tasksWithHeader: tasksWithHeader` in every `recordStat`
call inside `ValidatePlan` that occurs *after* the split. Calls that return before the split
(the `plan_text is required` guard, the mode guard, the payload-cap guard) leave them zero,
which is correct — nothing was parsed.

- [ ] **Step 10: Run all three packages**

Run: `go test -race ./internal/planparser/... ./internal/stats/... ./internal/mcpsrv/...`
Expected: PASS

- [ ] **Step 11: Commit**

```bash
git add internal/planparser internal/stats internal/mcpsrv
git commit -m "fix(planparser): match header markers case-insensitively, report adoption

The matcher was case-sensitive and the result was read nowhere outside tests,
so every plan from superpowers writing-plans (capital C, grep-enforced) scored
as headerless and the resulting adoption signal was false. Fixes the casing and
gives the flag its first consumer: validate_plan stats events now carry
tasks_total / tasks_with_header, aggregated into a plan_headers rollup block."
```

---

### Task 4: The shared CodeScene digest type

**Goal:** A leaf `internal/codescene` package owns the digest shape, and `stats.CodesceneEvent` embeds it so the hook's on-disk record and the MCP argument cannot drift apart.

**Files:**
- Create: `internal/codescene/codescene.go`
- Create: `internal/codescene/codescene_test.go`
- Modify: `internal/stats/codescene.go:10-30`
- Modify: `internal/stats/codescene_test.go`

**Acceptance Criteria:**
- [ ] `codescene.Digest` carries `ran`, `skip_reason`, `tool`, `quality_gate`, `files_analyzed`, `verdicts`, `trend`, `net_pp`, `category_counts`.
- [ ] `Normalize()` recomputes `Trend` from `NetPP`: positive → `regression`, negative → `improvement`, zero → `neutral`; and lowercases `QualityGate`.
- [ ] `internal/codescene` imports no other package from this repo.
- [ ] `stats.CodesceneEvent` embeds `codescene.Digest`; a hook-written JSONL line with no `ran` field still unmarshals, and re-marshalling omits `ran`/`skip_reason`.
- [ ] Existing `internal/stats` tests pass unchanged.

**Verify:** `go test -race ./internal/codescene/... ./internal/stats/...` → PASS

**Steps:**

- [ ] **Step 1: Write the failing tests**

Create `internal/codescene/codescene_test.go`:

```go
package codescene

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalize_RecomputesTrendFromNetPP(t *testing.T) {
	// A caller reporting an improvement while submitting positive problem
	// points is corrected, not trusted.
	d := Digest{Ran: true, NetPP: 2.0, Trend: "improvement"}
	d.Normalize()
	assert.Equal(t, "regression", d.Trend)
}

func TestNormalize_NegativeIsImprovement(t *testing.T) {
	d := Digest{Ran: true, NetPP: -1.5}
	d.Normalize()
	assert.Equal(t, "improvement", d.Trend)
}

func TestNormalize_ZeroIsNeutral(t *testing.T) {
	d := Digest{Ran: true, NetPP: 0, Trend: "regression"}
	d.Normalize()
	assert.Equal(t, "neutral", d.Trend)
}

func TestNormalize_LowercasesQualityGate(t *testing.T) {
	d := Digest{Ran: true, QualityGate: "FAILED"}
	d.Normalize()
	assert.Equal(t, "failed", d.QualityGate)
}

func TestDigest_OmitsRanAndSkipReasonWhenZero(t *testing.T) {
	b, err := json.Marshal(Digest{QualityGate: "passed"})
	require.NoError(t, err)
	assert.NotContains(t, string(b), "ran")
	assert.NotContains(t, string(b), "skip_reason")
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/codescene/...`
Expected: FAIL — package does not exist.

- [ ] **Step 3: Create the package**

Create `internal/codescene/codescene.go`:

```go
// Package codescene defines the CodeScene analyze_change_set digest shape
// shared by the validate_completion MCP argument and the stats subsystem's
// on-disk record. It is a leaf package: it imports nothing else from this
// repository, so both internal/stats and internal/mcpsrv can depend on it
// without an import cycle.
//
// anti-tangent never calls CodeScene. It receives a digest a caller computed
// (the same reduction examples/hooks/codescene-log.sh performs) and treats it
// as caller-attested, exactly like pinned_by.
package codescene

import "strings"

// Verdicts is the per-file verdict tally from an analyze_change_set run.
type Verdicts struct {
	Improved int `json:"improved"`
	Degraded int `json:"degraded"`
	Stable   int `json:"stable"`
}

// Digest is one analyze_change_set result reduced to counts and metadata.
// No file paths, no code, no function names — privacy parity with the rest of
// the stats subsystem.
//
// Ran and SkipReason are omitempty and absent from hook-written records; the
// hook has no notion of a deliberate skip, so a record it wrote unmarshals
// with Ran=false and is distinguished from a caller-declared skip by
// SkipReason being empty too.
type Digest struct {
	Ran            bool           `json:"ran,omitempty"`
	SkipReason     string         `json:"skip_reason,omitempty"`
	Tool           string         `json:"tool,omitempty"`
	QualityGate    string         `json:"quality_gate,omitempty"` // passed|failed
	FilesAnalyzed  int            `json:"files_analyzed,omitempty"`
	Verdicts       *Verdicts      `json:"verdicts,omitempty"`
	Trend          string         `json:"trend,omitempty"` // improvement|regression|neutral
	NetPP          float64        `json:"net_pp,omitempty"`
	CategoryCounts map[string]int `json:"category_counts,omitempty"`
}

// Trend values.
const (
	TrendImprovement = "improvement"
	TrendRegression  = "regression"
	TrendNeutral     = "neutral"
)

// Normalize derives Trend from NetPP and lowercases QualityGate. It is the
// only integrity check applied to an inbound digest: it cannot tell whether
// the numbers came from a real CodeScene run, but it does stop a caller
// reporting an improvement alongside positive problem points.
func (d *Digest) Normalize() {
	switch {
	case d.NetPP > 0:
		d.Trend = TrendRegression
	case d.NetPP < 0:
		d.Trend = TrendImprovement
	default:
		d.Trend = TrendNeutral
	}
	d.QualityGate = strings.ToLower(strings.TrimSpace(d.QualityGate))
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test -race ./internal/codescene/...`
Expected: PASS

- [ ] **Step 5: Write the failing stats-embedding test**

Append to `internal/stats/codescene_test.go`:

```go
func TestCodesceneEvent_UnmarshalsHookWrittenRecord(t *testing.T) {
	// Exactly what examples/hooks/codescene-log.sh appends: no "ran" key.
	line := `{"ts":"2026-07-07T13:11:28Z","tool":"analyze_change_set",` +
		`"quality_gate":"failed","files_analyzed":2,` +
		`"verdicts":{"improved":0,"degraded":2,"stable":0},` +
		`"trend":"regression","net_pp":2.0,"category_counts":{"Complex Method":2}}`

	var ev CodesceneEvent
	require.NoError(t, json.Unmarshal([]byte(line), &ev))
	assert.Equal(t, "failed", ev.QualityGate)
	assert.Equal(t, 2, ev.FilesAnalyzed)
	require.NotNil(t, ev.Verdicts)
	assert.Equal(t, 2, ev.Verdicts.Degraded)
	assert.Equal(t, 2.0, ev.NetPP)
	assert.False(t, ev.Ran, "hook records carry no ran field")

	out, err := json.Marshal(ev)
	require.NoError(t, err)
	assert.NotContains(t, string(out), `"ran"`)
}
```

- [ ] **Step 6: Run to verify it fails**

Run: `go test ./internal/stats/ -run HookWrittenRecord -v`
Expected: FAIL — `CodesceneEvent` has no field `Ran`.

- [ ] **Step 7: Embed the digest**

Replace `internal/stats/codescene.go:10-30` with:

```go
// Verdicts is re-exported so existing consumers of stats.Verdicts keep
// compiling; the canonical definition lives in internal/codescene.
type Verdicts = codescene.Verdicts

// CodesceneEvent is the per-run record the hook appends (see
// docs/team-setup/codescene-stats.md). anti-tangent reads this file and, from
// v0.15.0, may also receive the same shape in band as the validate_completion
// `codescene` argument. Counts + metadata only — no file paths.
// analyze_change_set is categorical (verdicts / quality-gate / problem-points),
// not a 1-10 score.
type CodesceneEvent struct {
	Ts time.Time `json:"ts"`
	codescene.Digest
}
```

with `"github.com/patiently/anti-tangent-mcp/internal/codescene"` added to the imports.
The embedded struct promotes `Tool`, `QualityGate`, `FilesAnalyzed`, `Verdicts`, `Trend`,
`NetPP`, and `CategoryCounts`, so `computeCodescene` and `pruneCodescene` compile unchanged.

Note: `Tool` was previously non-omitempty. Confirm the existing
`internal/stats/codescene_test.go` assertions on marshalled output still hold; if a test
asserts a `"tool"` key is present on a zero-value event, update it — the field is now
`omitempty`.

- [ ] **Step 8: Run the stats suite**

Run: `go test -race ./internal/stats/...`
Expected: PASS

- [ ] **Step 9: Verify the leaf-package constraint**

```bash
# `go list -deps` includes the target package itself, so it must be excluded —
# without the second filter this reports "not a leaf" for a genuine leaf.
go list -deps ./internal/codescene \
  | grep patiently \
  | grep -v '^github.com/patiently/anti-tangent-mcp/internal/codescene$' \
  && echo "FAIL: not a leaf" || echo "leaf package confirmed"
```

- [ ] **Step 10: Commit**

```bash
git add internal/codescene internal/stats
git commit -m "feat(codescene): extract the digest shape into a leaf package

stats.CodesceneEvent now embeds codescene.Digest so the hook's on-disk record
and the incoming validate_completion argument are one shape by construction.
Normalize() recomputes trend from net_pp, so a caller cannot report an
improvement while submitting positive problem points."
```

---

### Task 5: Submission-defect classification

**Goal:** `validate_completion` distinguishes findings about the submission from findings about the code, and says so in the envelope the implementer pastes into DONE.

**Files:**
- Create: `internal/mcpsrv/submission_defect.go`
- Create: `internal/mcpsrv/submission_defect_test.go`
- Modify: `internal/mcpsrv/handlers.go:27-38` (`Envelope`)
- Modify: `internal/mcpsrv/handlers.go` (`ValidateCompletion` tail)
- Modify: `internal/mcpsrv/summary.go:23-39` (`formatEnvelopeSummary`)
- Modify: `internal/mcpsrv/summary_test.go`
- Modify: `internal/prompts/templates/post.tmpl`
- Modify: `internal/prompts/testdata/*.golden` (regenerate)

**Acceptance Criteria:**
- [ ] `Envelope` gains `submission_defect_only bool` with `omitempty`.
- [ ] It is true when at least one critical/major finding exists and every one of them is `insufficient_evidence`, `malformed_evidence`, or `codescene_not_run`.
- [ ] It is false when a genuine major finding is mixed in, false when only minor findings exist, and false when there are no findings.
- [ ] When true, `next_action` leads with the re-submit instruction and `summary_block` carries a `submission_defect_only:` line.
- [ ] `post.tmpl` instructs the reviewer to use `insufficient_evidence` for unassessable ACs.
- [ ] Golden files regenerated and the diff reviewed.

**Verify:** `go test -race ./internal/mcpsrv/... ./internal/prompts/...` → PASS

**Steps:**

- [ ] **Step 1: Write the failing classifier tests**

Create `internal/mcpsrv/submission_defect_test.go`:

```go
package mcpsrv

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

func f(sev verdict.Severity, cat verdict.Category) verdict.Finding {
	return verdict.Finding{Severity: sev, Category: cat, Criterion: "c", Evidence: "e", Suggestion: "s"}
}

func TestIsSubmissionDefectOnly_AllEvidenceCategories(t *testing.T) {
	got := isSubmissionDefectOnly([]verdict.Finding{
		f(verdict.SeverityMajor, verdict.CategoryInsufficientEvidence),
		f(verdict.SeverityCritical, verdict.CategoryMalformedEvidence),
		f(verdict.SeverityMinor, verdict.CategoryQuality), // minors are ignored
	})
	assert.True(t, got)
}

func TestIsSubmissionDefectOnly_MixedWithGenuineMajor(t *testing.T) {
	got := isSubmissionDefectOnly([]verdict.Finding{
		f(verdict.SeverityMajor, verdict.CategoryInsufficientEvidence),
		f(verdict.SeverityMajor, verdict.CategoryMissingAC),
	})
	assert.False(t, got, "a real code finding disqualifies the whole envelope")
}

func TestIsSubmissionDefectOnly_MinorOnly(t *testing.T) {
	got := isSubmissionDefectOnly([]verdict.Finding{
		f(verdict.SeverityMinor, verdict.CategoryInsufficientEvidence),
	})
	assert.False(t, got, "minors never blocked DONE, so there is nothing to excuse")
}

func TestIsSubmissionDefectOnly_NoFindings(t *testing.T) {
	assert.False(t, isSubmissionDefectOnly(nil))
}

func TestIsSubmissionDefectOnly_CodesceneNotRun(t *testing.T) {
	got := isSubmissionDefectOnly([]verdict.Finding{
		f(verdict.SeverityMajor, verdict.CategoryCodesceneNotRun),
	})
	assert.True(t, got)
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/mcpsrv/ -run SubmissionDefectOnly -v`
Expected: FAIL — `isSubmissionDefectOnly` undefined; `CategoryCodesceneNotRun` undefined.

- [ ] **Step 3: Add the two server-only categories**

In `internal/verdict/verdict.go`, next to `CategoryMalformedEvidence` (line 56-61):

```go
	// CategoryCodesceneNotRun and CategoryCodesceneSkipped are server-only,
	// like CategoryMalformedEvidence: emitted by the validate_completion
	// CodeScene check when ANTI_TANGENT_CODESCENE=required, never by a
	// reviewer. Both are intentionally absent from validCategory.
	CategoryCodesceneNotRun  Category = "codescene_not_run"
	CategoryCodesceneSkipped Category = "codescene_skipped"
```

Do **not** add them to `validCategory` in `internal/verdict/parser.go:59-68`.

- [ ] **Step 4: Write the classifier**

Create `internal/mcpsrv/submission_defect.go`:

```go
// Package mcpsrv: classification of validate_completion findings into
// "the submission was incomplete" versus "the code is wrong".
package mcpsrv

import "github.com/patiently/anti-tangent-mcp/internal/verdict"

// submissionDefectCategories are the findings that describe what the
// implementer sent rather than what they built. Fixing one means attaching
// more evidence, not changing code.
var submissionDefectCategories = map[verdict.Category]bool{
	verdict.CategoryInsufficientEvidence: true,
	verdict.CategoryMalformedEvidence:    true,
	verdict.CategoryCodesceneNotRun:      true,
}

// resubmitNextAction is prefixed onto next_action when the only blocking
// findings are submission defects.
const resubmitNextAction = "Re-submit with the missing evidence — every blocking finding is " +
	"about the submission, not the code; no rework is implied. Then: "

// isSubmissionDefectOnly reports whether the envelope is blocked solely by
// submission defects. Minor findings are ignored: they never blocked DONE, so
// an envelope carrying only minors has nothing to excuse and returns false.
func isSubmissionDefectOnly(findings []verdict.Finding) bool {
	blocking := 0
	for _, f := range findings {
		if f.Severity != verdict.SeverityCritical && f.Severity != verdict.SeverityMajor {
			continue
		}
		blocking++
		if !submissionDefectCategories[f.Category] {
			return false
		}
	}
	return blocking > 0
}
```

- [ ] **Step 5: Run to verify it passes**

Run: `go test -race ./internal/mcpsrv/ -run SubmissionDefectOnly`
Expected: PASS

- [ ] **Step 6: Add the envelope field and wire it**

Add to `Envelope` (`internal/mcpsrv/handlers.go:27-38`), after `Partial`:

```go
	// SubmissionDefectOnly is set on validate_completion responses whose
	// blocking findings are all about the submission rather than the code.
	// The implementer should attach what is missing and re-submit; no rework
	// is implied. Server-computed; see submission_defect.go.
	SubmissionDefectOnly bool `json:"submission_defect_only,omitempty"`
```

In `ValidateCompletion`, at the point where the final `env` literal is built (after
`result = verdict.FinalizeVerdict(result)` and the session write-back), set it before
`recordStat`:

```go
	if isSubmissionDefectOnly(env.Findings) {
		env.SubmissionDefectOnly = true
		env.NextAction = resubmitNextAction + env.NextAction
	}
```

This must run after `FinalizeVerdict` (which may adjust severities) and after the
CodeScene findings from Task 6 are prepended, so ordering inside the handler matters — place
it immediately before the final `h.recordStat(...)` call.

- [ ] **Step 7: Surface it in the summary block**

In `formatEnvelopeSummary` (`internal/mcpsrv/summary.go:23-39`), after the `partial` line:

```go
	if env.SubmissionDefectOnly {
		b.WriteString("  submission_defect_only: true — re-submit with the missing evidence; no code rework implied\n")
	}
```

Add to `internal/mcpsrv/summary_test.go`:

```go
func TestFormatEnvelopeSummary_SubmissionDefectLine(t *testing.T) {
	got := formatEnvelopeSummary(Envelope{
		SessionID: "s1", Verdict: "fail", NextAction: "n",
		ModelUsed: "m", SubmissionDefectOnly: true,
	})
	assert.Contains(t, got, "submission_defect_only: true")
	assert.Contains(t, got, "no code rework implied")
}
```

- [ ] **Step 8: Teach the reviewer the category**

In `internal/prompts/templates/post.tmpl`, insert after the paragraph ending
`...an AC is not addressed by any of the provided evidence.` (currently line 62):

```
When an acceptance criterion cannot be assessed because the submitted evidence is absent, partial, or does not cover it, emit `category: insufficient_evidence` rather than `missing_acceptance_criterion`. Reserve `missing_acceptance_criterion` for evidence that affirmatively fails or contradicts a criterion. This distinction is load-bearing: `insufficient_evidence` tells the implementer to attach what is missing, while `missing_acceptance_criterion` tells them to change the code.
```

- [ ] **Step 9: Regenerate and review the goldens**

```bash
go test ./internal/prompts/... -update
git diff internal/prompts/testdata/
```

Expected: only the new paragraph appears in post-prompt goldens. Any other change is a mistake
— investigate before continuing.

- [ ] **Step 10: Run the full suite**

Run: `go test -race ./...`
Expected: PASS

- [ ] **Step 11: Commit**

```bash
git add internal/verdict internal/mcpsrv internal/prompts
git commit -m "feat(mcpsrv): distinguish submission defects from code defects

Two-thirds of first-round validate_completion failures in field data were the
reviewer asking for evidence, not reviewing code — yet insufficient_evidence is
major, so the protocol treated a formatting complaint as blocking. The envelope
now carries submission_defect_only when every blocking finding is about what was
submitted, and next_action says to re-submit rather than rework. post.tmpl names
insufficient_evidence so reviewers stop routing evidence gaps through
missing_acceptance_criterion."
```

---

### Task 6: In-band CodeScene results

**Goal:** `validate_completion` accepts a `codescene` digest, threads it to the reviewer, and — when the operator opts in — says so when it is missing.

**Files:**
- Modify: `internal/config/config.go` (`Config`, `Load`)
- Modify: `internal/config/config_test.go`
- Modify: `internal/mcpsrv/handlers.go:767-777` (`ValidateCompletionArgs`), `ValidateCompletion`
- Modify: `internal/mcpsrv/handlers_test.go`
- Modify: `internal/prompts/prompts.go:41-51` (`PostInput`)
- Modify: `internal/prompts/templates/post.tmpl`
- Modify: `internal/prompts/prompts_test.go`, `internal/prompts/testdata/`

**Acceptance Criteria:**
- [ ] `ANTI_TANGENT_CODESCENE` accepts `""` and `required`; any other value fails startup with a message naming the allowed set.
- [ ] With the switch unset: no CodeScene finding is ever emitted, and a supplied digest is still threaded to the reviewer.
- [ ] With `required`: absent block → `codescene_not_run` major; `ran:false` + `skip_reason` → `codescene_skipped` minor; `ran:false` without a reason → `codescene_not_run` major; `ran:true` → no finding.
- [ ] A supplied digest with `trend: regression` produces a `quality` finding at minor naming the net problem points.
- [ ] `Normalize()` is applied before any of the above, so trend-based behaviour uses the corrected value.
- [ ] The CodeScene prompt block appears only when a digest with `ran:true` is supplied; existing goldens are byte-identical.

**Verify:** `go test -race ./internal/config/... ./internal/mcpsrv/... ./internal/prompts/...` → PASS

**Steps:**

- [ ] **Step 1: Write the failing config test**

Append to `internal/config/config_test.go`:

```go
func TestLoad_Codescene(t *testing.T) {
	base := map[string]string{"ANTHROPIC_API_KEY": "k"}
	get := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}

	cfg, err := Load(get(base))
	require.NoError(t, err)
	assert.Equal(t, "", cfg.Codescene, "default is off")

	m := map[string]string{"ANTHROPIC_API_KEY": "k", "ANTI_TANGENT_CODESCENE": "required"}
	cfg, err = Load(get(m))
	require.NoError(t, err)
	assert.Equal(t, "required", cfg.Codescene)

	m = map[string]string{"ANTHROPIC_API_KEY": "k", "ANTI_TANGENT_CODESCENE": "optional"}
	_, err = Load(get(m))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ANTI_TANGENT_CODESCENE")
	assert.Contains(t, err.Error(), `allowed: "", "required"`)
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/config/ -run Codescene -v`
Expected: FAIL — `cfg.Codescene` undefined.

- [ ] **Step 3: Add the config field**

Add to `Config` in `internal/config/config.go`, after `KBStore`:

```go
	// Codescene gates the validate_completion CodeScene adoption check.
	// "" (default) disables it entirely — no findings, today's behaviour.
	// "required" makes a missing `codescene` argument an observable
	// non-adoption signal. Operator-declared rather than agent-declared:
	// if absence were interpreted from the agent's own claim about its host,
	// a forgetful agent and an unconfigured host would look identical.
	Codescene string
```

In `Load`, mirroring the `ANTI_TANGENT_KB_STORE` block at lines 146-152:

```go
	if v := env("ANTI_TANGENT_CODESCENE"); v != "" {
		if v != "required" {
			return Config{}, fmt.Errorf(`ANTI_TANGENT_CODESCENE: unknown value %q (allowed: "", "required")`, v)
		}
		cfg.Codescene = v
	}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test -race ./internal/config/...`
Expected: PASS

- [ ] **Step 5: Write the failing handler tests**

Append to `internal/mcpsrv/handlers_test.go` (using whatever harness the existing completion
tests use to build `handlers` with a stub reviewer — mirror the nearest existing
`TestValidateCompletion_*` for setup):

```go
func TestValidateCompletion_CodesceneRequired_MissingBlock(t *testing.T) {
	h := newTestHandlersWithCodescene(t, "required")
	sess := h.deps.Sessions.Create(session.TaskSpec{Title: "t", Goal: "g"})

	_, env, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		SessionID: sess.ID, Summary: "done", FinalDiff: "diff --git a/x b/x\n+ok\n",
	})
	require.NoError(t, err)

	var found *verdict.Finding
	for i := range env.Findings {
		if env.Findings[i].Category == verdict.CategoryCodesceneNotRun {
			found = &env.Findings[i]
		}
	}
	require.NotNil(t, found, "expected a codescene_not_run finding")
	assert.Equal(t, verdict.SeverityMajor, found.Severity)
	assert.True(t, env.SubmissionDefectOnly)
}

func TestValidateCompletion_CodesceneRequired_DeclaredSkip(t *testing.T) {
	h := newTestHandlersWithCodescene(t, "required")
	sess := h.deps.Sessions.Create(session.TaskSpec{Title: "t", Goal: "g"})

	_, env, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		SessionID: sess.ID, Summary: "done", FinalDiff: "diff --git a/x b/x\n+ok\n",
		Codescene: &codescene.Digest{Ran: false, SkipReason: "docs-only task"},
	})
	require.NoError(t, err)

	for _, f := range env.Findings {
		if f.Category == verdict.CategoryCodesceneSkipped {
			assert.Equal(t, verdict.SeverityMinor, f.Severity)
			assert.Contains(t, f.Evidence, "docs-only task")
			return
		}
	}
	t.Fatal("expected a codescene_skipped finding")
}

func TestValidateCompletion_CodesceneRequired_UndeclaredSkipIsNotRun(t *testing.T) {
	h := newTestHandlersWithCodescene(t, "required")
	sess := h.deps.Sessions.Create(session.TaskSpec{Title: "t", Goal: "g"})

	_, env, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		SessionID: sess.ID, Summary: "done", FinalDiff: "diff --git a/x b/x\n+ok\n",
		Codescene: &codescene.Digest{Ran: false},
	})
	require.NoError(t, err)
	assert.True(t, hasCategory(env.Findings, verdict.CategoryCodesceneNotRun))
}

func TestValidateCompletion_CodesceneOff_NoFinding(t *testing.T) {
	h := newTestHandlersWithCodescene(t, "")
	sess := h.deps.Sessions.Create(session.TaskSpec{Title: "t", Goal: "g"})

	_, env, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		SessionID: sess.ID, Summary: "done", FinalDiff: "diff --git a/x b/x\n+ok\n",
	})
	require.NoError(t, err)
	assert.False(t, hasCategory(env.Findings, verdict.CategoryCodesceneNotRun))
	assert.False(t, hasCategory(env.Findings, verdict.CategoryCodesceneSkipped))
}

func TestValidateCompletion_CodesceneRegressionFinding(t *testing.T) {
	h := newTestHandlersWithCodescene(t, "") // fires regardless of the switch
	sess := h.deps.Sessions.Create(session.TaskSpec{Title: "t", Goal: "g"})

	_, env, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		SessionID: sess.ID, Summary: "done", FinalDiff: "diff --git a/x b/x\n+ok\n",
		Codescene: &codescene.Digest{
			Ran: true, QualityGate: "failed", NetPP: 2.0,
			CategoryCounts: map[string]int{"Complex Method": 2},
		},
	})
	require.NoError(t, err)

	for _, f := range env.Findings {
		if f.Category == verdict.CategoryQuality && strings.Contains(f.Criterion, "code_health") {
			assert.Equal(t, verdict.SeverityMinor, f.Severity)
			assert.Contains(t, f.Evidence, "2")
			return
		}
	}
	t.Fatal("expected a minor code-health regression finding")
}
```

Add the two helpers next to the tests:

```go
func hasCategory(fs []verdict.Finding, c verdict.Category) bool {
	for _, f := range fs {
		if f.Category == c {
			return true
		}
	}
	return false
}
```

and `newTestHandlersWithCodescene(t, mode)` built from the existing test constructor with
`Cfg.Codescene = mode`.

- [ ] **Step 6: Run to verify they fail**

Run: `go test ./internal/mcpsrv/ -run Codescene -v`
Expected: FAIL — `ValidateCompletionArgs` has no field `Codescene`.

- [ ] **Step 7: Add the argument and the check**

Add to `ValidateCompletionArgs` (`internal/mcpsrv/handlers.go:767-777`):

```go
	Codescene *codescene.Digest `json:"codescene,omitempty"`
```

Add to `internal/mcpsrv/submission_defect.go` (it already owns finding classification, and
keeping this out of `handlers.go` holds that file's growth down):

```go
// codesceneFindings returns the findings implied by an inbound digest.
// mode is cfg.Codescene: "" disables the adoption check entirely, "required"
// makes a missing or undeclared-skipped run observable. The regression finding
// fires regardless of mode, because a supplied digest is signal whether or not
// the operator opted into enforcement.
//
// d is normalized by the caller before this runs.
func codesceneFindings(mode string, d *codescene.Digest) []verdict.Finding {
	var out []verdict.Finding

	if mode == "required" {
		switch {
		case d == nil:
			out = append(out, verdict.Finding{
				Severity:  verdict.SeverityMajor,
				Category:  verdict.CategoryCodesceneNotRun,
				Criterion: "codescene_adoption",
				Evidence: "ANTI_TANGENT_CODESCENE=required, but this validate_completion call " +
					"carried no `codescene` argument.",
				Suggestion: "Run CodeScene `analyze_change_set` and re-submit with the result as " +
					"the `codescene` argument, or pass {\"ran\": false, \"skip_reason\": \"…\"} if " +
					"the skip is deliberate. This is a submission defect — no code rework is implied.",
			})
		case !d.Ran && strings.TrimSpace(d.SkipReason) == "":
			out = append(out, verdict.Finding{
				Severity:  verdict.SeverityMajor,
				Category:  verdict.CategoryCodesceneNotRun,
				Criterion: "codescene_adoption",
				Evidence:  "`codescene.ran` is false and no `skip_reason` was given.",
				Suggestion: "State why CodeScene was skipped in `skip_reason`, or run " +
					"`analyze_change_set` and re-submit the result.",
			})
		case !d.Ran:
			out = append(out, verdict.Finding{
				Severity:   verdict.SeverityMinor,
				Category:   verdict.CategoryCodesceneSkipped,
				Criterion:  "codescene_adoption",
				Evidence:   "CodeScene deliberately skipped: " + strings.TrimSpace(d.SkipReason),
				Suggestion: "No action needed if the reason holds; the skip is recorded in the plan-run ledger.",
			})
		}
	}

	if d != nil && d.Ran && d.Trend == codescene.TrendRegression {
		out = append(out, verdict.Finding{
			Severity:  verdict.SeverityMinor,
			Category:  verdict.CategoryQuality,
			Criterion: "code_health_regression",
			Evidence: fmt.Sprintf("CodeScene reports a Code Health regression: net problem points %+.1f%s.",
				d.NetPP, topCategoriesSuffix(d.CategoryCounts)),
			Suggestion: "Consider addressing the flagged functions before reporting DONE. " +
				"Advisory only — anti-tangent never fails a verdict on CodeScene.",
		})
	}
	return out
}

// topCategoriesSuffix renders up to three CodeScene categories, highest count
// first, deterministically ordered so the finding text is stable.
func topCategoriesSuffix(counts map[string]int) string {
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
	if len(pairs) > 3 {
		pairs = pairs[:3]
	}
	parts := make([]string, len(pairs))
	for i, p := range pairs {
		parts[i] = fmt.Sprintf("%s x%d", p.k, p.v)
	}
	return " (" + strings.Join(parts, ", ") + ")"
}
```

Add `fmt`, `sort`, `strings`, and the `codescene` import to that file.

- [ ] **Step 8: Wire it into the handler**

In `ValidateCompletion`, immediately after the at-least-one-evidence guard (around
`internal/mcpsrv/handlers.go:1007`):

```go
	if args.Codescene != nil {
		args.Codescene.Normalize()
	}
```

Then, after `result = verdict.FinalizeVerdict(result)` and before the `env` literal is built,
prepend the CodeScene findings so they precede reviewer findings:

```go
	if cs := codesceneFindings(h.deps.Cfg.Codescene, args.Codescene); len(cs) > 0 {
		result.Findings = append(cs, result.Findings...)
		result = verdict.FinalizeVerdict(result)
	}
```

The second `FinalizeVerdict` re-runs the severity ladder so a newly-added major lifts the
verdict. The Task 5 `isSubmissionDefectOnly` call sits after this — confirm the ordering.

- [ ] **Step 9: Thread the digest to the reviewer**

Add to `PostInput` (`internal/prompts/prompts.go:41-51`):

```go
	Codescene *codescene.Digest
```

Pass it in the `prompts.RenderPost` call inside `ValidateCompletion`:

```go
				Codescene:                      args.Codescene,
```

Add to `internal/prompts/templates/post.tmpl`, immediately before `## What to evaluate`:

```
{{if and .Codescene .Codescene.Ran}}
## CodeScene change-set analysis (codebase-grounded)

Quality gate: {{.Codescene.QualityGate}}
{{if .Codescene.Verdicts}}Files analyzed: {{.Codescene.FilesAnalyzed}} — improved {{.Codescene.Verdicts.Improved}} / degraded {{.Codescene.Verdicts.Degraded}} / stable {{.Codescene.Verdicts.Stable}}
{{end}}Trend: {{.Codescene.Trend}} (net problem points {{printf "%+.1f" .Codescene.NetPP}})

This is deterministic analysis of the actual files, not a claim by the implementer. Treat it as authoritative for Code Health. It does not by itself fail an acceptance criterion — weigh it alongside the evidence.
{{end}}
```

- [ ] **Step 10: Add a prompt golden and confirm the others are untouched**

Add to `internal/prompts/prompts_test.go` a `post_with_codescene` case mirroring the nearest
existing post-render test, with:

```go
		Codescene: &codescene.Digest{
			Ran: true, QualityGate: "failed", FilesAnalyzed: 6,
			Verdicts: &codescene.Verdicts{Improved: 1, Degraded: 2, Stable: 3},
			Trend:    codescene.TrendRegression, NetPP: 2.0,
		},
```

Then:

```bash
go test ./internal/prompts/... -update
git diff --stat internal/prompts/testdata/
```

Expected: exactly one new golden file. If any pre-existing golden changed, the `{{if}}` guard
is wrong — fix it before continuing.

- [ ] **Step 11: Run everything**

Run: `go test -race ./...`
Expected: PASS

- [ ] **Step 12: Commit**

```bash
git add internal/config internal/mcpsrv internal/prompts
git commit -m "feat(mcpsrv): accept CodeScene results in band on validate_completion

anti-tangent could not previously observe whether the CodeScene companion calls
ran — v0.13.0 asked for a prose attestation line precisely because of that. A
structured codescene argument makes a missing run observable when the operator
sets ANTI_TANGENT_CODESCENE=required, and gives the text-only reviewer its first
codebase-grounded input. Advisory throughout: a failed quality gate never fails
a verdict server-side."
```

---

### Task 7: The plan-run store

**Goal:** An in-memory, TTL-evicted store of plan runs and their per-task rows, with no dependency on the MCP layer.

**Files:**
- Create: `internal/planrun/planrun.go`
- Create: `internal/planrun/planrun_test.go`

**Acceptance Criteria:**
- [ ] `Store.Create(planVerdict, planQuality string, taskCount int) *Run` mints an id of the form `pr_` + 12 lowercase hex characters.
- [ ] `Store.AppendRow` / `Store.UpdateRow` add and mutate rows keyed by session id; unknown run or session ids return false rather than panicking.
- [ ] Rows preserve dispatch order.
- [ ] `EvictExpired` drops runs whose `LastAccessed` is older than the TTL and returns the count.
- [ ] Concurrent access is race-free under `-race`.

**Verify:** `go test -race ./internal/planrun/...` → PASS

**Steps:**

- [ ] **Step 1: Write the failing tests**

Create `internal/planrun/planrun_test.go`:

```go
package planrun

import (
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/codescene"
)

func TestCreate_IDShape(t *testing.T) {
	s := NewStore(time.Hour)
	r := s.Create("pass", "rigorous", 5)
	assert.Regexp(t, regexp.MustCompile(`^pr_[0-9a-f]{12}$`), r.ID)
	assert.Equal(t, 5, r.TaskCount)
}

func TestAppendAndUpdateRow_PreservesOrder(t *testing.T) {
	s := NewStore(time.Hour)
	r := s.Create("pass", "actionable", 2)

	require.True(t, s.AppendRow(r.ID, TaskRow{SessionID: "s1", TaskTitle: "first", PreVerdict: "pass"}))
	require.True(t, s.AppendRow(r.ID, TaskRow{SessionID: "s2", TaskTitle: "second", PreVerdict: "warn"}))

	require.True(t, s.UpdateRow(r.ID, "s1", func(row *TaskRow) {
		row.PostVerdict = "pass"
		row.CodesceneState = StateRan
		row.Codescene = &codescene.Digest{Ran: true, QualityGate: "passed", NetPP: -1.5}
	}))

	got, ok := s.Get(r.ID)
	require.True(t, ok)
	require.Len(t, got.Rows, 2)
	assert.Equal(t, "first", got.Rows[0].TaskTitle)
	assert.Equal(t, "second", got.Rows[1].TaskTitle)
	assert.Equal(t, "pass", got.Rows[0].PostVerdict)
	assert.Equal(t, "", got.Rows[1].PostVerdict, "incomplete tasks keep an empty post verdict")
}

func TestUpdateRow_UnknownIDs(t *testing.T) {
	s := NewStore(time.Hour)
	r := s.Create("pass", "rigorous", 1)
	assert.False(t, s.AppendRow("pr_deadbeefdead", TaskRow{SessionID: "x"}))
	assert.False(t, s.UpdateRow(r.ID, "nope", func(*TaskRow) {}))
}

func TestEvictExpired(t *testing.T) {
	s := NewStore(time.Hour)
	r := s.Create("pass", "rigorous", 1)
	s.runs[r.ID].LastAccessed = time.Now().Add(-2 * time.Hour)

	assert.Equal(t, 1, s.EvictExpired(time.Now()))
	_, ok := s.Get(r.ID)
	assert.False(t, ok)
}

func TestConcurrentAppend(t *testing.T) {
	s := NewStore(time.Hour)
	r := s.Create("pass", "rigorous", 50)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s.AppendRow(r.ID, TaskRow{SessionID: string(rune('a' + i%26))})
		}(i)
	}
	wg.Wait()

	got, ok := s.Get(r.ID)
	require.True(t, ok)
	assert.Len(t, got.Rows, 50)
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/planrun/...`
Expected: FAIL — package does not exist.

- [ ] **Step 3: Write the store**

Create `internal/planrun/planrun.go`:

```go
// Package planrun tracks one execution of a multi-task implementation plan:
// the plan-level verdict from validate_plan, and one row per task carrying
// the anti-tangent verdict and the CodeScene result.
//
// State is in memory with TTL eviction, mirroring internal/session — a plan
// run and the sessions belonging to it expire together. Durable persistence
// is optional and lives in ledger.go.
package planrun

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"

	"github.com/patiently/anti-tangent-mcp/internal/codescene"
)

// CodeScene adoption states recorded per task row.
const (
	StateRan     = "ran"
	StateSkipped = "skipped"
	StateMissing = "missing"
)

// TaskRow is one task's outcome within a plan run.
type TaskRow struct {
	SessionID      string            `json:"-"`
	Index          int               `json:"index"`
	TaskTitle      string            `json:"task_title"`
	PreVerdict     string            `json:"pre_verdict"`
	Checkpoints    int               `json:"checkpoints"`
	PostVerdict    string            `json:"post_verdict,omitempty"`
	Severity       map[string]int    `json:"severity,omitempty"`
	SubmissionOnly bool              `json:"submission_defect_only,omitempty"`
	Codescene      *codescene.Digest `json:"codescene,omitempty"`
	CodesceneState string            `json:"codescene_state,omitempty"`
	CompletedAt    time.Time         `json:"completed_at,omitempty"`
}

// Run is one plan execution.
type Run struct {
	ID           string    `json:"plan_run_id"`
	CreatedAt    time.Time `json:"created_at"`
	LastAccessed time.Time `json:"-"`
	PlanVerdict  string    `json:"plan_verdict"`
	PlanQuality  string    `json:"plan_quality"`
	// TaskCount is how many tasks the validated plan contained, so the report
	// can tell a failed task from one that was never dispatched.
	TaskCount int       `json:"task_count"`
	Rows      []TaskRow `json:"rows"`
}

// Store holds plan runs in memory.
type Store struct {
	mu   sync.Mutex
	runs map[string]*Run
	ttl  time.Duration
}

func NewStore(ttl time.Duration) *Store {
	return &Store{runs: map[string]*Run{}, ttl: ttl}
}

func (s *Store) TTL() time.Duration { return s.ttl }

// newID returns "pr_" plus 12 lowercase hex characters.
func newID() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failure is not recoverable and not worth propagating
		// through every call site; a time-derived fallback keeps the server up.
		return "pr_" + hex.EncodeToString([]byte(time.Now().UTC().Format("150405")))[:12]
	}
	return "pr_" + hex.EncodeToString(b[:])
}

func (s *Store) Create(planVerdict, planQuality string, taskCount int) *Run {
	now := time.Now()
	r := &Run{
		ID:           newID(),
		CreatedAt:    now,
		LastAccessed: now,
		PlanVerdict:  planVerdict,
		PlanQuality:  planQuality,
		TaskCount:    taskCount,
	}
	s.mu.Lock()
	s.runs[r.ID] = r
	s.mu.Unlock()
	return r
}

func (s *Store) Get(id string) (*Run, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.runs[id]
	if !ok {
		return nil, false
	}
	r.LastAccessed = time.Now()
	return r, true
}

// AppendRow adds a task row, stamping its Index from the current length.
// Returns false when the run id is unknown or expired.
func (s *Store) AppendRow(runID string, row TaskRow) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.runs[runID]
	if !ok {
		return false
	}
	row.Index = len(r.Rows) + 1
	r.Rows = append(r.Rows, row)
	r.LastAccessed = time.Now()
	return true
}

// UpdateRow applies mutate to the row owned by sessionID. Returns false when
// the run or the row is unknown.
func (s *Store) UpdateRow(runID, sessionID string, mutate func(*TaskRow)) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.runs[runID]
	if !ok {
		return false
	}
	for i := range r.Rows {
		if r.Rows[i].SessionID == sessionID {
			mutate(&r.Rows[i])
			r.LastAccessed = time.Now()
			return true
		}
	}
	return false
}

// EvictExpired drops runs untouched for longer than the TTL.
func (s *Store) EvictExpired(now time.Time) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for id, r := range s.runs {
		if now.Sub(r.LastAccessed) > s.ttl {
			delete(s.runs, id)
			n++
		}
	}
	return n
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test -race ./internal/planrun/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/planrun
git commit -m "feat(planrun): in-memory plan-run store with TTL eviction"
```

---

### Task 8: Mint and thread plan_run_id

**Goal:** `validate_plan` returns a `plan_run_id`; `validate_task_spec` accepts it; the session carries it so completion and progress calls update the right row without new arguments.

**Files:**
- Modify: `internal/verdict/plan.go` (`PlanResult`)
- Modify: `internal/session/session.go:57-66` (`Session`)
- Modify: `internal/session/store.go:27-40` (`Create`)
- Modify: `internal/mcpsrv/server.go` (`Deps`)
- Modify: `internal/mcpsrv/handlers.go` (`ValidateTaskSpecArgs`, `ValidatePlan`, `ValidateTaskSpec`, `CheckProgress`, `ValidateCompletion`)
- Modify: `internal/mcpsrv/summary.go:47-73` (`formatPlanSummary`)
- Modify: `cmd/anti-tangent-mcp/main.go`
- Modify: `internal/mcpsrv/handlers_plan_test.go`, `internal/mcpsrv/handlers_test.go`

**Acceptance Criteria:**
- [ ] A passing `validate_plan` returns a non-empty `plan_run_id`, and it appears in `summary_block`.
- [ ] `plan_run_id` is absent from `internal/verdict/plan_schema.json` — the reviewer never emits it.
- [ ] A cache hit returns the same `plan_run_id` as the original call.
- [ ] `validate_task_spec` with a valid `plan_run_id` appends a row carrying the task title and pre-verdict.
- [ ] `validate_task_spec` with an unknown `plan_run_id` still succeeds — the review is not held hostage to run bookkeeping.
- [ ] `check_progress` increments the row's checkpoint count; `validate_completion` writes post verdict, severity counts, submission flag, and CodeScene state.

**Verify:** `go test -race ./internal/verdict/... ./internal/session/... ./internal/mcpsrv/...` → PASS

**Steps:**

- [ ] **Step 1: Write the failing tests**

Append to `internal/mcpsrv/handlers_plan_test.go`:

```go
func TestValidatePlan_MintsPlanRunID(t *testing.T) {
	h := newTestPlanHandlers(t) // existing helper
	plan := "### Task 1: t\n\n**Goal:** g\n\n**Acceptance criteria:**\n- ac\n"

	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: plan})
	require.NoError(t, err)
	assert.Regexp(t, `^pr_[0-9a-f]{12}$`, pr.PlanRunID)
	assert.Contains(t, pr.SummaryBlock, pr.PlanRunID)

	run, ok := h.deps.PlanRuns.Get(pr.PlanRunID)
	require.True(t, ok)
	assert.Equal(t, 1, run.TaskCount)
}

func TestValidatePlan_SchemaHasNoPlanRunID(t *testing.T) {
	assert.NotContains(t, string(verdict.PlanSchema()), "plan_run_id",
		"plan_run_id is server-set; a reviewer must never be asked to emit it")
}

func TestValidateTaskSpec_UnknownPlanRunIDStillSucceeds(t *testing.T) {
	h := newTestHandlers(t)
	_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "t", Goal: "g", PlanRunID: "pr_000000000000",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, env.SessionID, "run bookkeeping must not block the review")
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/mcpsrv/ -run PlanRunID -v`
Expected: FAIL — `PlanRunID` undefined on `PlanResult`.

- [ ] **Step 3: Add the fields**

`internal/verdict/plan.go`, on `PlanResult` after `Partial`:

```go
	// PlanRunID is server-set, never reviewer-emitted: it is absent from
	// plan_schema.json, exactly like SummaryBlock. Controllers thread it into
	// each validate_task_spec call so plan_run_report can assemble the run.
	PlanRunID string `json:"plan_run_id,omitempty"`
```

`internal/session/session.go`, on `Session`:

```go
	PlanRunID     string
```

`internal/session/store.go`, change `Create` to accept the run id:

```go
func (s *Store) Create(spec TaskSpec, planRunID string) *Session {
	now := time.Now()
	sess := &Session{
		ID:           uuid.NewString(),
		CreatedAt:    now,
		LastAccessed: now,
		Spec:         spec,
		PlanRunID:    planRunID,
	}
	s.mu.Lock()
	s.sessions[sess.ID] = sess
	s.mu.Unlock()
	return sess
}
```

Update every existing `Sessions.Create(spec)` call site to `Sessions.Create(spec, "")` or the
real id. Find them with:

```bash
grep -rn "\.Create(" --include=*.go internal/ | grep -i session
```

`internal/mcpsrv/server.go`, on `Deps`:

```go
	// PlanRuns tracks multi-task plan executions. Never nil; New() fills it in.
	PlanRuns *planrun.Store
```

and in `New`:

```go
	if d.PlanRuns == nil {
		d.PlanRuns = planrun.NewStore(d.Cfg.SessionTTL)
	}
```

`internal/mcpsrv/handlers.go`, on `ValidateTaskSpecArgs`:

```go
	PlanRunID string `json:"plan_run_id,omitempty"`
```

- [ ] **Step 4: Mint the id in ValidatePlan**

In `finalizePlanResult` (the function containing `pr.SummaryBlock = formatPlanSummary(...)` at
`internal/mcpsrv/handlers.go:1542`), the run must be created before the summary is rendered so
the id appears in the block. Because that helper does not hold `h`, mint in `ValidatePlan`
instead: after the plan result is finalized and before `planEnvelopeResultFinalized`, add:

```go
	if pr.PlanRunID == "" {
		run := h.deps.PlanRuns.Create(string(pr.PlanVerdict), string(pr.PlanQuality), len(pr.Tasks))
		pr.PlanRunID = run.ID
		pr.SummaryBlock = formatPlanSummary(pr, modelUsed, ms)
	}
```

The `if` guard is what makes the cache path correct: `planPassCache` stores the finalized
`PlanResult` including `PlanRunID`, so a cache hit re-renders with the original id rather than
minting a second run for the same plan. Confirm `internal/mcpsrv/plan_cache.go:78` re-renders
after restoring, and that the cached `pr` carries `PlanRunID`.

- [ ] **Step 5: Render it in the plan summary**

In `formatPlanSummary` (`internal/mcpsrv/summary.go:47-73`), after the `plan_quality` line:

```go
	if pr.PlanRunID != "" {
		fmt.Fprintf(&b, "  plan_run_id:   %s\n", pr.PlanRunID)
	}
```

- [ ] **Step 6: Append the row in ValidateTaskSpec**

After the session is created and the pre-verdict is known, before building the envelope:

```go
	if args.PlanRunID != "" {
		// Best-effort: an unknown or expired run must not fail the review.
		h.deps.PlanRuns.AppendRow(args.PlanRunID, planrun.TaskRow{
			SessionID:      sess.ID,
			TaskTitle:      args.TaskTitle,
			PreVerdict:     env.Verdict,
			CodesceneState: planrun.StateMissing,
		})
	}
```

- [ ] **Step 7: Update the row from CheckProgress**

After the checkpoint is appended to the session:

```go
	if sess.PlanRunID != "" {
		h.deps.PlanRuns.UpdateRow(sess.PlanRunID, sess.ID, func(row *planrun.TaskRow) {
			row.Checkpoints++
		})
	}
```

- [ ] **Step 8: Update the row from ValidateCompletion**

Immediately before the final `h.recordStat(...)` call, after `SubmissionDefectOnly` is set:

```go
	if !lightweight && sess.PlanRunID != "" {
		sev, _, _ := stats.CountFindings(env.Findings)
		state := planrun.StateMissing
		if args.Codescene != nil {
			if args.Codescene.Ran {
				state = planrun.StateRan
			} else {
				state = planrun.StateSkipped
			}
		}
		h.deps.PlanRuns.UpdateRow(sess.PlanRunID, sess.ID, func(row *planrun.TaskRow) {
			row.PostVerdict = env.Verdict
			row.Severity = sev
			row.SubmissionOnly = env.SubmissionDefectOnly
			row.Codescene = args.Codescene
			row.CodesceneState = state
			row.CompletedAt = time.Now().UTC()
		})
	}
```

- [ ] **Step 9: Evict expired runs alongside sessions**

In `cmd/anti-tangent-mcp/main.go`, find the ticker that calls `Sessions.EvictExpired` and add
the parallel call on the same tick, using the same `now`.

- [ ] **Step 10: Run everything**

Run: `go test -race ./...`
Expected: PASS

- [ ] **Step 11: Commit**

```bash
git add internal/verdict internal/session internal/mcpsrv cmd
git commit -m "feat(mcpsrv): mint plan_run_id and thread it through the task lifecycle

validate_plan mints a server-set plan_run_id (absent from the reviewer schema,
like summary_block); validate_task_spec accepts it and the session carries it,
so check_progress and validate_completion update the right row with no new
arguments. Row bookkeeping is best-effort throughout — an unknown or expired
run never fails a review."
```

---

### Task 9: The plan_run_report tool

**Goal:** A seventh MCP tool that renders a plan run's per-task table deterministically, with no LLM call.

**Files:**
- Create: `internal/planrun/report.go`
- Create: `internal/planrun/report_test.go`
- Modify: `internal/mcpsrv/handlers.go` (tool definition + handler)
- Modify: `internal/mcpsrv/server.go:29-50`
- Modify: `internal/mcpsrv/integration_test.go`

**Acceptance Criteria:**
- [ ] `plan_run_report(plan_run_id)` returns `plan_run_id`, `plan_verdict`, `plan_quality`, `tasks`, `totals`, and `summary_block`.
- [ ] Rendering is deterministic — the same `Run` renders byte-identically every time.
- [ ] Tasks with no `PostVerdict` render as incomplete rather than as failures.
- [ ] Totals count anti-tangent verdicts and CodeScene states (`ran` / `skipped` / `missing`) and sum net problem points.
- [ ] An unknown id returns a result carrying one `session_not_found` finding with `criterion: plan_run_id`.
- [ ] The handler makes no provider call — verified by a nil reviewer registry in the test.

**Verify:** `go test -race ./internal/planrun/... ./internal/mcpsrv/...` → PASS

**Steps:**

- [ ] **Step 1: Write the failing report test**

Create `internal/planrun/report_test.go`:

```go
package planrun

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/codescene"
)

func sampleRun() *Run {
	return &Run{
		ID: "pr_8f21c4a90b3e", CreatedAt: time.Unix(0, 0).UTC(),
		PlanVerdict: "pass", PlanQuality: "rigorous", TaskCount: 3,
		Rows: []TaskRow{
			{Index: 1, TaskTitle: "Add /healthz endpoint", PreVerdict: "pass", PostVerdict: "pass",
				CodesceneState: StateRan,
				Codescene:      &codescene.Digest{Ran: true, QualityGate: "passed", NetPP: -1.5}},
			{Index: 2, TaskTitle: "Retry backoff", PreVerdict: "pass", PostVerdict: "warn",
				CodesceneState: StateSkipped,
				Codescene:      &codescene.Digest{SkipReason: "docs-only task"}},
			{Index: 3, TaskTitle: "Config plumbing", PreVerdict: "warn"},
		},
	}
}

func TestRender_Deterministic(t *testing.T) {
	r := sampleRun()
	assert.Equal(t, Render(r), Render(r))
}

func TestRender_Contents(t *testing.T) {
	got := Render(sampleRun())
	assert.Contains(t, got, "pr_8f21c4a90b3e")
	assert.Contains(t, got, "pass / rigorous")
	assert.Contains(t, got, "Add /healthz endpoint")
	assert.Contains(t, got, "skipped (docs-only task)")
	assert.Contains(t, got, "not run")
	assert.Contains(t, got, "incomplete")
}

func TestTotals(t *testing.T) {
	tot := Totals(sampleRun())
	assert.Equal(t, 1, tot.Pass)
	assert.Equal(t, 1, tot.Warn)
	assert.Equal(t, 0, tot.Fail)
	assert.Equal(t, 1, tot.Incomplete)
	assert.Equal(t, 1, tot.CodesceneRan)
	assert.Equal(t, 1, tot.CodesceneSkipped)
	assert.Equal(t, 1, tot.CodesceneMissing)
	assert.InDelta(t, -1.5, tot.NetPP, 0.0001)
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/planrun/ -run Render -v`
Expected: FAIL — `Render` undefined.

- [ ] **Step 3: Write the renderer**

Create `internal/planrun/report.go`:

```go
package planrun

import (
	"fmt"
	"strings"
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

// topCategories renders the highest-count CodeScene categories, deterministically.
func topCategories(counts map[string]int) string {
	s := topCategoriesList(counts, 2)
	if s == "" {
		return ""
	}
	return "  (" + s + ")"
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

	width := 4
	for _, row := range r.Rows {
		if len(row.TaskTitle) > width {
			width = len(row.TaskTitle)
		}
	}
	if width > 40 {
		width = 40
	}

	fmt.Fprintf(&b, "  #  %-*s  %-10s %s\n", width, "Task", "AT", "CodeScene")
	for _, row := range r.Rows {
		title := row.TaskTitle
		if len(title) > width {
			title = title[:width-1] + "…"
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
```

Move `topCategoriesSuffix` from Task 7's `internal/mcpsrv/submission_defect.go` into
`internal/planrun/report.go` as an exported-in-package `topCategoriesList(counts, max)` helper,
and have `submission_defect.go` call `planrun.TopCategories(counts, 3)`. Export it as:

```go
// TopCategories renders up to max CodeScene categories, highest count first,
// ties broken alphabetically so output is stable.
func TopCategories(counts map[string]int, max int) string
```

and make `topCategoriesList` its unexported body. This removes the duplicate that would
otherwise exist across the two packages.

- [ ] **Step 4: Run to verify it passes**

Run: `go test -race ./internal/planrun/...`
Expected: PASS

- [ ] **Step 5: Add the tool**

In `internal/mcpsrv/handlers.go`:

```go
// PlanRunReportArgs is the input schema for the plan-run report.
type PlanRunReportArgs struct {
	PlanRunID string `json:"plan_run_id" jsonschema:"required"`
}

// PlanRunReportResult is what plan_run_report returns. Deterministic: no
// reviewer call, no provider round-trip, no cost.
type PlanRunReportResult struct {
	PlanRunID    string             `json:"plan_run_id"`
	PlanVerdict  string             `json:"plan_verdict,omitempty"`
	PlanQuality  string             `json:"plan_quality,omitempty"`
	Tasks        []planrun.TaskRow  `json:"tasks"`
	Totals       planrun.RunTotals  `json:"totals"`
	Findings     []verdict.Finding  `json:"findings,omitempty"`
	SummaryBlock string             `json:"summary_block"`
}

func planRunReportTool() *mcp.Tool {
	return &mcp.Tool{
		Name: "plan_run_report",
		Description: "Report the per-task outcome of a finished multi-task plan run: the anti-tangent verdict and the CodeScene result for each task. " +
			"Call once after the last task reports DONE, with the plan_run_id returned by validate_plan. " +
			"Deterministic and free — no reviewer model is called.",
	}
}

func (h *handlers) PlanRunReport(_ context.Context, _ *mcp.CallToolRequest, args PlanRunReportArgs) (*mcp.CallToolResult, PlanRunReportResult, error) {
	if args.PlanRunID == "" {
		return nil, PlanRunReportResult{}, errors.New("plan_run_id is required")
	}

	// Snapshot, not Get: this walks run.Rows after the lock is released, and
	// other subagents under the same plan run may be appending concurrently.
	run, ok := h.deps.PlanRuns.Snapshot(args.PlanRunID)
	if !ok {
		res := PlanRunReportResult{
			PlanRunID: args.PlanRunID,
			Tasks:     []planrun.TaskRow{},
			Findings: []verdict.Finding{{
				Severity:  verdict.SeverityMajor,
				Category:  verdict.CategorySessionMissing,
				Criterion: "plan_run_id",
				Evidence: fmt.Sprintf("No plan run %q is known to this server. Runs expire after %s, "+
					"and in-memory state is lost on restart.", args.PlanRunID, h.deps.PlanRuns.TTL()),
				Suggestion: "Nothing to recover — report from the per-task DONE envelopes instead. " +
					"Set ANTI_TANGENT_STATS_DIR and ANTI_TANGENT_PLAN_LEDGER=1 to persist future runs.",
			}},
			SummaryBlock: "anti-tangent plan run report\n  plan_run_id:  " + args.PlanRunID +
				"\n  (unknown or expired — no rows)\n",
		}
		return planRunReportResult(res)
	}

	res := PlanRunReportResult{
		PlanRunID:    run.ID,
		PlanVerdict:  run.PlanVerdict,
		PlanQuality:  run.PlanQuality,
		Tasks:        run.Rows,
		Totals:       planrun.Totals(run),
		SummaryBlock: planrun.Render(run),
	}
	if res.Tasks == nil {
		res.Tasks = []planrun.TaskRow{}
	}
	return planRunReportResult(res)
}

func planRunReportResult(res PlanRunReportResult) (*mcp.CallToolResult, PlanRunReportResult, error) {
	body, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return nil, PlanRunReportResult{}, err
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(body)}},
	}, res, nil
}
```

Register it in `internal/mcpsrv/server.go`:

```go
	mcp.AddTool(srv, planRunReportTool(), h.PlanRunReport)
```

and update the `New` doc comment at `server.go:29-31` to list seven tools.

- [ ] **Step 6: Add the end-to-end integration test**

In `internal/mcpsrv/integration_test.go`, add a test that runs `validate_plan` → two
`validate_task_spec` (both passing `plan_run_id`) → two `validate_completion` (one with a
`ran:true` digest, one with none) → `plan_run_report`, and asserts the rendered
`summary_block` contains both task titles, one CodeScene result, and one `not run`.

- [ ] **Step 7: Run everything**

Run: `go test -race ./...`
Expected: PASS

- [ ] **Step 8: Commit**

```bash
git add internal/planrun internal/mcpsrv
git commit -m "feat(mcpsrv): add plan_run_report, the seventh tool

Deterministic per-task report over a finished plan run — anti-tangent verdict
and CodeScene result side by side. No reviewer call, so it costs nothing."
```

---

### Task 10: Optional durable plan-run ledger

**Goal:** Plan runs survive a server restart when the operator opts in, with the privacy change made explicit rather than inherited from `ANTI_TANGENT_STATS_DIR`.

**Files:**
- Create: `internal/planrun/ledger.go`
- Create: `internal/planrun/ledger_test.go`
- Modify: `internal/config/config.go`, `internal/config/stats_config_test.go`
- Modify: `internal/mcpsrv/handlers.go` (`ValidateCompletion` row update, `PlanRunReport` fallback)
- Modify: `cmd/anti-tangent-mcp/main.go`

**Acceptance Criteria:**
- [ ] `ANTI_TANGENT_PLAN_LEDGER=1` alone does nothing; it requires `ANTI_TANGENT_STATS_DIR` too.
- [ ] With both set, each completed task appends one line to `plan-runs.jsonl` in the stats dir.
- [ ] `plan_run_report` reconstructs a run from the ledger when the in-memory run is gone.
- [ ] Ledger writes are best-effort — a write failure never changes a `validate_completion` result.
- [ ] A ledger line carries the task title, making the privacy difference from `events.jsonl` real and documented.

**Verify:** `go test -race ./internal/planrun/... ./internal/config/... ./internal/mcpsrv/...` → PASS

**Steps:**

- [ ] **Step 1: Write the failing ledger test**

Create `internal/planrun/ledger_test.go`:

```go
package planrun

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/codescene"
)

func TestLedger_AppendAndLoad(t *testing.T) {
	dir := t.TempDir()
	l := &Ledger{Dir: dir}

	run := &Run{ID: "pr_abc123abc123", PlanVerdict: "pass", PlanQuality: "rigorous", TaskCount: 2}
	require.NoError(t, l.Append(run, TaskRow{
		Index: 1, TaskTitle: "Add endpoint", PostVerdict: "pass",
		CodesceneState: StateRan, CompletedAt: time.Unix(0, 0).UTC(),
		Codescene:      &codescene.Digest{Ran: true, QualityGate: "passed", NetPP: -1},
	}))
	require.NoError(t, l.Append(run, TaskRow{
		Index: 2, TaskTitle: "Wire router", PostVerdict: "warn", CodesceneState: StateMissing,
	}))

	got, ok := l.Load("pr_abc123abc123")
	require.True(t, ok)
	assert.Equal(t, "pass", got.PlanVerdict)
	assert.Equal(t, 2, got.TaskCount)
	require.Len(t, got.Rows, 2)
	assert.Equal(t, "Add endpoint", got.Rows[0].TaskTitle)
	assert.Equal(t, "Wire router", got.Rows[1].TaskTitle)

	_, ok = l.Load("pr_000000000000")
	assert.False(t, ok)

	assert.FileExists(t, filepath.Join(dir, "plan-runs.jsonl"))
}

func TestLedger_DisabledIsNoop(t *testing.T) {
	var l *Ledger
	assert.NoError(t, l.Append(&Run{ID: "x"}, TaskRow{}))
	_, ok := l.Load("x")
	assert.False(t, ok)
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/planrun/ -run Ledger -v`
Expected: FAIL — `Ledger` undefined.

- [ ] **Step 3: Write the ledger**

Create `internal/planrun/ledger.go`:

```go
package planrun

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
)

const ledgerFile = "plan-runs.jsonl"

// ledgerLine is one completed task row, denormalized with its run's header so
// the file can be replayed without a separate index.
//
// PRIVACY: unlike events.jsonl and codescene-events.jsonl, which are
// deliberately content-free, this record carries TaskTitle. That is why it
// requires its own opt-in (ANTI_TANGENT_PLAN_LEDGER=1) on top of
// ANTI_TANGENT_STATS_DIR rather than inheriting the stats opt-in.
type ledgerLine struct {
	PlanRunID   string  `json:"plan_run_id"`
	PlanVerdict string  `json:"plan_verdict,omitempty"`
	PlanQuality string  `json:"plan_quality,omitempty"`
	TaskCount   int     `json:"task_count,omitempty"`
	Row         TaskRow `json:"row"`
}

// Ledger appends completed task rows to plan-runs.jsonl. A nil *Ledger is a
// no-op, so the disabled path is a single nil check.
type Ledger struct {
	Dir string
}

func (l *Ledger) Append(run *Run, row TaskRow) error {
	if l == nil || l.Dir == "" {
		return nil
	}
	b, err := json.Marshal(ledgerLine{
		PlanRunID: run.ID, PlanVerdict: run.PlanVerdict,
		PlanQuality: run.PlanQuality, TaskCount: run.TaskCount, Row: row,
	})
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(l.Dir, ledgerFile), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(b, '\n'))
	return err
}

// Load reconstructs a run from the ledger. Returns false when the ledger is
// disabled, unreadable, or holds no rows for the id.
func (l *Ledger) Load(planRunID string) (*Run, bool) {
	if l == nil || l.Dir == "" {
		return nil, false
	}
	f, err := os.Open(filepath.Join(l.Dir, ledgerFile))
	if err != nil {
		return nil, false
	}
	defer f.Close()

	var run *Run
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var ln ledgerLine
		if err := json.Unmarshal(sc.Bytes(), &ln); err != nil {
			continue // tolerate a torn trailing line
		}
		if ln.PlanRunID != planRunID {
			continue
		}
		if run == nil {
			run = &Run{
				ID: ln.PlanRunID, PlanVerdict: ln.PlanVerdict,
				PlanQuality: ln.PlanQuality, TaskCount: ln.TaskCount,
			}
		}
		run.Rows = append(run.Rows, ln.Row)
	}
	if run == nil {
		return nil, false
	}
	return run, true
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test -race ./internal/planrun/...`
Expected: PASS

- [ ] **Step 5: Add the config field**

In `internal/config/config.go`, on `Config`:

```go
	// PlanLedger enables the durable plan-run ledger. Requires StatsDir; on
	// its own it does nothing. Separate from the stats opt-in because
	// plan-runs.jsonl carries task titles, unlike every other stats artifact.
	PlanLedger bool
```

In `Load`, after the stats block:

```go
	if v := env("ANTI_TANGENT_PLAN_LEDGER"); v == "1" || strings.EqualFold(v, "true") {
		cfg.PlanLedger = true
	}
```

Add to `internal/config/stats_config_test.go`:

```go
func TestLoad_PlanLedgerRequiresStatsDir(t *testing.T) {
	get := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}
	cfg, err := Load(get(map[string]string{
		"ANTHROPIC_API_KEY": "k", "ANTI_TANGENT_PLAN_LEDGER": "1",
	}))
	require.NoError(t, err)
	assert.True(t, cfg.PlanLedger)
	assert.Equal(t, "", cfg.StatsDir, "the ledger is inert without a stats dir")
}
```

- [ ] **Step 6: Wire the ledger**

In `internal/mcpsrv/server.go`, on `Deps`:

```go
	// PlanLedger is nil unless both ANTI_TANGENT_STATS_DIR and
	// ANTI_TANGENT_PLAN_LEDGER are set. All calls are nil-safe.
	PlanLedger *planrun.Ledger
```

In `cmd/anti-tangent-mcp/main.go`, where `Deps` is built:

```go
	var ledger *planrun.Ledger
	if cfg.StatsDir != "" && cfg.PlanLedger {
		ledger = &planrun.Ledger{Dir: cfg.StatsDir}
	}
```

In `ValidateCompletion`'s row update (Task 8 Step 8), after `UpdateRow` succeeds:

```go
		// Snapshot, not Get: this walks run.Rows after the lock is released,
		// and concurrent subagents under the same plan run may be appending.
		if run, ok := h.deps.PlanRuns.Snapshot(sess.PlanRunID); ok {
			for _, row := range run.Rows {
				if row.SessionID == sess.ID {
					if err := h.deps.PlanLedger.Append(run, row); err != nil {
						slog.Warn("plan ledger append failed", "err", err)
					}
					break
				}
			}
		}
```

In `PlanRunReport`, before the unknown-id branch:

```go
	run, ok := h.deps.PlanRuns.Snapshot(args.PlanRunID)
	if !ok {
		run, ok = h.deps.PlanLedger.Load(args.PlanRunID)
	}
	if !ok {
		// ... existing unknown-id result
	}
```

- [ ] **Step 7: Add the fallback test**

In `internal/mcpsrv/handlers_test.go`, build handlers with a `PlanLedger` pointed at
`t.TempDir()`, run a completion that writes a row, then construct a *fresh* `planrun.Store`
(simulating a restart) and assert `PlanRunReport` still returns the row.

- [ ] **Step 8: Run everything**

Run: `go test -race ./...`
Expected: PASS

- [ ] **Step 9: Commit**

```bash
git add internal/planrun internal/config internal/mcpsrv cmd
git commit -m "feat(planrun): optional durable plan-run ledger

Gated behind ANTI_TANGENT_PLAN_LEDGER=1 on top of ANTI_TANGENT_STATS_DIR. The
second opt-in is deliberate: plan-runs.jsonl carries task titles, while every
other stats artifact is content-free, and inheriting the stats opt-in would
change that posture silently."
```

---

### Task 11: Protocol-text edits

**Goal:** The role-scoped parts document everything this release added, using the exact wording agreed in the spec.

**Files:**
- Modify: `docs/protocol/authoring.md`
- Modify: `docs/protocol/implementer.md`
- Modify: `docs/protocol/controller.md`
- Modify: `docs/protocol/core.md`
- Modify: `plugin/anti-tangent-protocol/protocol/*.md` (resync)

**Acceptance Criteria:**
- [ ] §3.1 names superpowers `writing-plans` as the generator and states that either casing of the AC marker works.
- [ ] §3.1 carries the non-goals note.
- [ ] §4.2 step 3 is the replacement text, keyed off `submission_defect_only`.
- [ ] §4.2 step 3b describes the `codescene` argument; the short variant matches.
- [ ] §5.1 gains step 6 covering `plan_run_id` and `plan_run_report`.
- [ ] §6 lists `insufficient_evidence` for `validate_completion` and both CodeScene categories as server-only.
- [ ] `core.md` documents `ANTI_TANGENT_CODESCENE` and `ANTI_TANGENT_PLAN_LEDGER`, and says seven tools.
- [ ] Every part still under 16,000 bytes; bundle resynced.

**Verify:** `bash -c 'for f in docs/protocol/*.md; do b=$(wc -c < "$f"); [ "$b" -lt 16000 ] || { echo "OVER: $f $b"; exit 1; }; done && diff -r docs/protocol plugin/anti-tangent-protocol/protocol && echo OK'` → `OK`

**Steps:**

- [ ] **Step 1: Edit §3.1 in `docs/protocol/authoring.md`**

Immediately after the fenced `### Task N:` template block, add:

```markdown
This is the shape superpowers' `writing-plans` skill emits. It writes
`**Acceptance Criteria:**` with a capital C; the parser matches case-insensitively, so either
casing works.

`writing-plans` does **not** emit `**Non-goals:**`. Plans from it arrive with no scope bound,
and the reviewer will say so. Add non-goals by hand, or set a repo-level instruction that does.
```

- [ ] **Step 2: Replace §4.2 step 3 in `docs/protocol/implementer.md`**

Replace the `**3. Before reporting DONE (REQUIRED).**` paragraph with:

```markdown
**3. Before reporting DONE (REQUIRED).** Call `validate_completion` with
the session_id, your summary, **a complete `final_diff` (or full
`final_files`)**, and test evidence. A complete diff is a **precondition
of the first call**, not something to add after a rejection —
evidence-poor submissions fail most of the time and buy you a formatting
review instead of a code review.
**Copy the `summary_block` field from the response verbatim into your DONE report.**
If the verdict is `fail` or contains `critical`/`major` findings, do
not report DONE — fix the findings and re-validate. **Exception: when the
response carries `submission_defect_only: true`, every blocking finding is
about what you submitted, not about your code. Attach the missing evidence
and re-submit; no rework is implied.**
```

- [ ] **Step 3: Replace §4.2 step 3b in `docs/protocol/implementer.md`**

```markdown
**3b. CodeScene pre-DONE check (REQUIRED when codescene-mcp is
configured in your host).** Call `analyze_change_set` for the full
branch-vs-base Code Health view, then pass the result to
`validate_completion` as the `codescene` argument:
`{"ran": true, "quality_gate": …, "verdicts": {…}, "trend": …, "net_pp": …, "category_counts": {…}}`.
If you deliberately skipped, pass `{"ran": false, "skip_reason": "…"}` instead.
The structured field supersedes the prose status line: it reaches the reviewer as
codebase-grounded evidence and lands in the plan-run report. If codescene-mcp
is not configured, omit the argument.
```

- [ ] **Step 4: Update the short variant in `docs/protocol/implementer.md`**

Replace the CodeScene bullet with:

```markdown
- If CodeScene MCP is configured, `pre_commit_code_health_safeguard` (mid-task) and `analyze_change_set` (pre-DONE) are required; pass the pre-DONE result to `validate_completion` as the `codescene` argument (or `{"ran": false, "skip_reason": "…"}`).
```

and add:

```markdown
- If the response carries `submission_defect_only: true`, attach the missing evidence and re-submit — that is a submission defect, not a code defect.
```

- [ ] **Step 5: Add §5.1 step 6 in `docs/protocol/controller.md`**

After the existing step 5:

```markdown
6. **Capture `plan_run_id`** from the passing `validate_plan` response and pass it as
   `plan_run_id` in each implementing subagent's `validate_task_spec` call — add it to the
   dispatch clause's task-spec field list. After the last task reports DONE, call
   `plan_run_report` with that id and surface the table to the user. The report is
   deterministic and free (no reviewer call). If you re-validate the plan after the 3-minute
   cache window expires, use the id from your **final** passing call.
```

Add `plan_run_id` to the §4.2 task-spec field list in `docs/protocol/implementer.md`:

```markdown
- plan_run_id:          <optional, v0.15.0+; from the controller's validate_plan>
```

- [ ] **Step 6: Update §6 and the tool count in `docs/protocol/core.md`**

Replace the finding-category bullets with:

```markdown
- Spec / lifecycle: `missing_acceptance_criterion`, `scope_drift`, `ambiguous_spec`, `unaddressed_finding`, `quality`, `convention_deviation`, `attestation_contradiction`, `unverifiable_codebase_claim`, `other`.
- Evidence: `insufficient_evidence` — emitted by `validate_completion` when an AC cannot be assessed from the submitted evidence, and by `extract_project_knowledge`. Server-only: `malformed_evidence`, `codescene_not_run`, `codescene_skipped`.
- Operational: `session_not_found`, `payload_too_large`.
- Project-knowledge (v0.6.0+): `kb_gap`, `ambiguous_pick`, `missing_index_entry` (prime); `redundant_proposal`, `contradicts_existing` (extract).
```

Add a FAQ entry:

```markdown
**What is `submission_defect_only: true`?** Every blocking finding on that `validate_completion`
response is about what you submitted — absent evidence, malformed evidence, or a CodeScene run
that did not happen — not about your code. Attach what is missing and call again. No rework is
implied, and the reviewer has not yet been able to review your code.
```

Change "It exposes six tools" to seven in the intro paragraph, naming `plan_run_report`.

- [ ] **Step 7: Document the env vars in `docs/protocol/core.md`**

```markdown
- `ANTI_TANGENT_CODESCENE` — `""` (off). Set to `required` to make a `validate_completion` call
  with no `codescene` argument emit a `codescene_not_run` finding. Prompt-level enforcement
  only; anti-tangent never fails a verdict on CodeScene findings.
- `ANTI_TANGENT_PLAN_LEDGER` — `0` (off). With `ANTI_TANGENT_STATS_DIR` set, `1` persists each
  completed task row to `plan-runs.jsonl` so `plan_run_report` survives a restart. Unlike every
  other stats artifact this file carries task titles, which is why it has its own opt-in.
```

- [ ] **Step 8: Resync and verify**

```bash
rm -f plugin/anti-tangent-protocol/protocol/*.md
cp docs/protocol/*.md plugin/anti-tangent-protocol/protocol/
for f in docs/protocol/*.md; do printf "%s %s\n" "$f" "$(wc -c < "$f")"; done
diff -r docs/protocol plugin/anti-tangent-protocol/protocol && echo "in sync"
```

- [ ] **Step 9: Commit**

```bash
git add docs/protocol plugin/anti-tangent-protocol
git commit -m "docs(protocol): document the v0.15.0 surface

Names writing-plans as the task-format generator and notes the capital-C
tolerance; adds the missing-non-goals warning; rewrites §4.2 step 3 around
submission_defect_only; documents the codescene argument, plan_run_id, and
plan_run_report; corrects the finding-category list."
```

---

### Task 12: Remaining documentation

**Goal:** README, CLAUDE.md, the stats team-setup doc, and CHANGELOG all describe v0.15.0 accurately.

**Files:**
- Modify: `README.md`
- Modify: `CLAUDE.md`
- Modify: `docs/team-setup/codescene-stats.md`
- Modify: `CHANGELOG.md`

**Acceptance Criteria:**
- [ ] README lists `plan_run_report` and both new env vars, and describes the in-band `codescene` argument.
- [ ] Every `INTEGRATION.md#…` deep link in README points at a part file instead.
- [ ] CLAUDE.md's "exposes four tools" becomes seven; the "Editing INTEGRATION.md" section is rewritten for `docs/protocol/` with the new resync command and both CI invariants.
- [ ] `codescene-stats.md` documents `plan-runs.jsonl`, its two opt-ins, and its privacy carve-out.
- [ ] `CHANGELOG.md` has a `## [0.15.0]` heading matching the branch name, with the protocol restructure called out under `### Changed` including the moved anchors.

**Verify:** `bash -c 'grep -q "0.15.0" CHANGELOG.md && ! grep -rn "INTEGRATION.md#" README.md && grep -q plan_run_report README.md CLAUDE.md && echo OK'` → `OK`

**Steps:**

- [ ] **Step 1: Update README**

- Add `plan_run_report` to the tool table with "deterministic, no reviewer call".
- Add to the dotenv block:

```bash
# CodeScene adoption check. "" (default) = off; "required" makes a
# validate_completion call with no `codescene` argument emit a finding.
ANTI_TANGENT_CODESCENE=
# Persist plan-run rows to plan-runs.jsonl (needs ANTI_TANGENT_STATS_DIR).
# NOTE: unlike other stats files, this one contains task titles.
ANTI_TANGENT_PLAN_LEDGER=0
```

- Rewrite the CodeScene section to describe the in-band argument.
- Repoint deep links: `grep -n 'INTEGRATION.md#' README.md` and fix each.

- [ ] **Step 2: Update CLAUDE.md**

Change "exposes four tools" to seven and name `plan_run_report`. Replace the "Editing
INTEGRATION.md" section with:

```markdown
## Editing the protocol

The protocol lives in `docs/protocol/` as five role-scoped parts; `INTEGRATION.md` is a router
over them, and the parts are bundled into the `anti-tangent-protocol` plugin so Claude Code can
load only what a given role needs. Two invariants are CI-enforced:

- Each part must stay **under 16,000 bytes**. The cost is a read per dispatched subagent, not a
  resident context cost — so it is paid once per task on a multi-task plan.
- `plugin/anti-tangent-protocol/protocol/` must be **identical** to `docs/protocol/`.

After editing any part, resync the bundle in the same commit:

```bash
rm -f plugin/anti-tangent-protocol/protocol/*.md
cp docs/protocol/*.md plugin/anti-tangent-protocol/protocol/
```

Section numbers (§1, §3.x, §4.x, §5.x, §6) are stable across the split and are cited
externally — do not renumber.
```

- [ ] **Step 3: Update `docs/team-setup/codescene-stats.md`**

Add a section:

```markdown
## Per-task attribution: `plan-runs.jsonl`

The hook above records CodeScene runs anonymously and in aggregate — it cannot know which task
it was inside. From v0.15.0 the implementer can instead pass the `analyze_change_set` digest to
`validate_completion` as the `codescene` argument, which attributes it to a task exactly.

Set `ANTI_TANGENT_PLAN_LEDGER=1` (with `ANTI_TANGENT_STATS_DIR`) to persist one line per
completed task to `plan-runs.jsonl`, so `plan_run_report` survives a server restart.

**Privacy: this file is different from the others.** `events.jsonl` and
`codescene-events.jsonl` are deliberately content-free — no titles, no paths, no code.
`plan-runs.jsonl` carries **task titles**. That is why it needs its own opt-in instead of
inheriting `ANTI_TANGENT_STATS_DIR`. It is retention-pruned alongside the other stats files.

The two channels do not double-count: the hook writes `codescene-events.jsonl` and feeds the
rollup's `codescene` block; the in-band argument writes `plan-runs.jsonl` and feeds
`plan_run_report`.
```

- [ ] **Step 4: Write the CHANGELOG entry**

Insert above `## [0.14.0]`:

```markdown
## [0.15.0] - 2026-08-DD

### Added
- **`plan_run_report`** (seventh tool) — a deterministic, reviewer-free per-task report over a
  finished plan run, showing the anti-tangent verdict and the CodeScene result side by side.
  `validate_plan` now mints a `plan_run_id` the controller threads into each
  `validate_task_spec` call.
- **In-band CodeScene results.** `validate_completion` accepts a structured `codescene`
  argument (the `analyze_change_set` digest). It reaches the reviewer as codebase-grounded
  evidence — the first input that partially covers anti-tangent's text-only blind spot — and is
  attributed to the task in the plan-run report. `ANTI_TANGENT_CODESCENE=required` makes a
  missing run observable as a `codescene_not_run` finding; unset (the default) changes nothing.
  Still advisory: a failed quality gate never fails a verdict server-side.
- **`submission_defect_only`** on `validate_completion` envelopes. True when every blocking
  finding is about the submission (`insufficient_evidence`, `malformed_evidence`,
  `codescene_not_run`) rather than the code, so an implementer re-submits instead of reworking.
  Field data showed two-thirds of first-round failures were evidence complaints treated as
  blocking code defects.
- **Plan-header adoption telemetry** in `rollup.json` (`plan_headers`), and an optional durable
  plan-run ledger behind `ANTI_TANGENT_PLAN_LEDGER=1`.

### Fixed
- `HasStructuredHeader` matched `**Acceptance criteria:**` case-sensitively and was read
  nowhere outside tests. superpowers' `writing-plans` — the dominant plan generator — emits a
  capital C and grep-enforces it, so every plan it produced scored as headerless and the
  resulting adoption signal was false. The matcher is now case-insensitive and the result is
  reported as telemetry.

### Changed
- **The protocol document is now role-scoped.** `INTEGRATION.md` is a router over
  `docs/protocol/{core,authoring,implementer,controller,project-knowledge}.md`, and the
  `anti-tangent-protocol` skill reads only `core.md` plus the part matching the agent's role.
  An implementing subagent previously read the whole ~40 KB document — including ~13 KB of
  project-knowledge protocol it is structurally forbidden from acting on — once per dispatch;
  it now reads roughly 19 KB. **Deep links to `INTEGRATION.md` anchors no longer resolve**;
  section numbers are unchanged, so `§4.2` is still `§4.2`, now inside `implementer.md`.
- The skill's trigger covers the end of a plan run, so a controller calling `plan_run_report`
  after the last task reports DONE has the protocol loaded.
- `post.tmpl` instructs the reviewer to use `insufficient_evidence` for acceptance criteria it
  cannot assess, instead of `missing_acceptance_criterion`.
```

- [ ] **Step 5: Verify**

```bash
grep -q "0.15.0" CHANGELOG.md && echo "changelog ok"
grep -rn "INTEGRATION.md#" README.md && echo "FAIL: stale anchors" || echo "no stale anchors"
grep -c "plan_run_report" README.md CLAUDE.md
```

- [ ] **Step 6: Commit**

```bash
git add README.md CLAUDE.md docs/team-setup CHANGELOG.md
git commit -m "docs: README, CLAUDE.md, stats guide, and changelog for v0.15.0"
```

---

### Task 13: Documentation guard CI

**Goal:** CI catches the two defects most likely to slip through a documentation restructure — a dead cross-reference and a section identifier that got duplicated or lost.

**Files:**
- Create: `scripts/check-protocol-docs.sh`
- Modify: `.github/workflows/ci.yml` (the `protocol-docs` job from Task 1)

**Acceptance Criteria:**
- [ ] The script fails when a relative markdown link in `INTEGRATION.md`, `README.md`, or `docs/protocol/*.md` points at a nonexistent file.
- [ ] It fails when any tracked section identifier appears zero times or more than once across `docs/protocol/*.md`.
- [ ] It exits 0 on the committed tree.
- [ ] It runs in the `protocol-docs` CI job.
- [ ] Both failure modes are demonstrated locally before the task is closed.

**Verify:** `bash scripts/check-protocol-docs.sh` → exits 0 with `✓ protocol docs OK`

**Steps:**

- [ ] **Step 1: Write the script**

Create `scripts/check-protocol-docs.sh`:

```bash
#!/usr/bin/env bash
# Guards the role-scoped protocol docs. Two failure modes matter here and
# neither is caught by a byte cap: a cross-reference that points nowhere, and
# a section identifier that got duplicated or dropped during a move. Section
# numbers are cited externally (README, ~/.claude mirrors, "§5.1" in prose),
# so losing one is a silent break.
set -uo pipefail
cd "$(dirname "$0")/.."

fail=0

# --- relative link check -------------------------------------------------
while IFS= read -r line; do
  src="${line%%:*}"
  target="${line#*:}"
  dir=$(dirname "$src")
  resolved="$dir/${target%%#*}"
  if [ ! -e "$resolved" ]; then
    echo "::error file=$src::broken relative link -> $target (resolved: $resolved)"
    fail=1
  fi
done < <(
  grep -ohnE '\]\([^)#][^)]*\)' INTEGRATION.md README.md docs/protocol/*.md 2>/dev/null >/dev/null
  for f in INTEGRATION.md README.md docs/protocol/*.md; do
    [ -f "$f" ] || continue
    grep -oE '\]\((\.{0,2}[^):]*\.md[^)]*)\)' "$f" \
      | sed -E 's/^\]\(//; s/\)$//' \
      | while read -r t; do echo "$f:$t"; done
  done
)

# --- section identifier uniqueness ---------------------------------------
sections=(
  '## Scope and limits'
  '## 1\.' '### 3\.1' '### 3\.2' '### 3\.3' '### 3\.5' '### 3\.6' '### 3\.7' '### 3\.8'
  '## 4\.' '### 4\.2' '### 4\.3'
  '## 5\.' '### 5\.1' '### 5\.2' '### 5\.3' '### 5\.4' '### 5\.5' '### 5\.6' '### 5\.7'
  '## 6\.'
)
for s in "${sections[@]}"; do
  n=$(grep -hcE "^$s" docs/protocol/*.md 2>/dev/null | paste -sd+ - | bc)
  n=${n:-0}
  if [ "$n" != "1" ]; then
    echo "::error::section '$s' appears $n times across docs/protocol/ (expected exactly 1)"
    fail=1
  fi
done

[ "$fail" -eq 0 ] && echo "✓ protocol docs OK"
exit "$fail"
```

```bash
chmod +x scripts/check-protocol-docs.sh
```

- [ ] **Step 2: Run it — expect clean**

Run: `bash scripts/check-protocol-docs.sh`
Expected: `✓ protocol docs OK`, exit 0.

- [ ] **Step 3: Prove the link guard fires**

```bash
echo '[dead](nope-does-not-exist.md)' >> docs/protocol/core.md
bash scripts/check-protocol-docs.sh; echo "exit=$?"   # expect exit=1 + broken-link error
git checkout docs/protocol/core.md
```

- [ ] **Step 4: Prove the section guard fires**

```bash
echo '### 4.2 duplicate' >> docs/protocol/core.md
bash scripts/check-protocol-docs.sh; echo "exit=$?"   # expect exit=1 + "appears 2 times"
git checkout docs/protocol/core.md
bash scripts/check-protocol-docs.sh                    # back to clean
```

- [ ] **Step 5: Add it to CI**

Append a step to the `protocol-docs` job:

```yaml
      - name: Protocol cross-reference and section guards
        run: bash scripts/check-protocol-docs.sh
```

- [ ] **Step 6: Full verification**

```bash
go build ./... && go test -race ./... && bash scripts/check-protocol-docs.sh
```

Expected: build clean, all tests PASS, `✓ protocol docs OK`.

- [ ] **Step 7: Commit**

```bash
git add scripts/check-protocol-docs.sh .github/workflows/ci.yml
git commit -m "ci: guard protocol cross-references and section identifiers

A byte cap does not catch the two defects a documentation restructure actually
produces: a link that resolves nowhere, and a section identifier duplicated or
dropped in a move. Section numbers are cited externally, so losing one is a
silent break."
```

---

## Self-Review

**Spec coverage**

| Spec section | Task |
|---|---|
| §3 header telemetry | 3 |
| §4.2 `submission_defect_only` | 5 |
| §4.3 `insufficient_evidence` prompt + docs | 5, 11 |
| §4.4 no byte-threshold gate | — (deliberate omission, recorded) |
| §5.1 shared digest type | 4 |
| §5.2 operator switch | 6 |
| §5.3 the argument | 6 |
| §5.4 behaviour table | 6 |
| §5.5 threading to the reviewer | 6 |
| §5.6 regression finding | 6 |
| §6.1 minting and threading | 8 |
| §6.2 the store | 7 |
| §6.3 the tool + reachability | 9, 2 |
| §6.4 ledger + privacy carve-out | 10 |
| §7.1–7.2 restructure | 1 |
| §7.3 the skill | 2 |
| §7.4 protocol-text edits | 11 |
| §7.5 other documents | 12 |
| §8 testing, incl. doc guards | every task; 13 for the doc guards |
| §9 sequencing, changelog | task order; 12 |

No spec requirement is unassigned.

**Deviations from the spec, recorded**

1. **Slice sizes.** The spec's §7.2 table was estimates; measured values are in this plan's
   opening table. `core.md` is 9,798 B rather than the estimated 6 KB, because the FAQ (3,858 B)
   and scope-and-limits (2,832 B) are larger than assumed. The `< 16,000` cap still holds with
   headroom. Implementer path drops 39,963 → 18,972 B, a 53% cut rather than the ~87% the
   spec's illustrative arithmetic implied.
2. **`topCategories` lives in `internal/planrun`.** The spec sketched the helper inside the
   CodeScene finding code. Both the finding text and the report column need it, so Task 9 Step 3
   exports it once from `planrun` rather than duplicating it.
3. **`Session.Create` signature change.** Not called out in the spec, but threading
   `plan_run_id` onto the session requires it. Task 8 Step 3 names the grep to find all call
   sites.

**Type consistency check**

- `codescene.Digest` — defined Task 4, used Tasks 6 (`ValidateCompletionArgs.Codescene`,
  `PostInput.Codescene`), 7 (`TaskRow.Codescene`), 9, 10. Same name throughout.
- `planrun.TaskRow` / `planrun.Run` / `planrun.Store` — defined Task 7, used 8, 9, 10.
- `StateRan` / `StateSkipped` / `StateMissing` — defined Task 7, used 8, 9.
- `isSubmissionDefectOnly` / `resubmitNextAction` — defined Task 5, used 5, 8.
- `codesceneFindings(mode string, d *codescene.Digest)` — defined Task 6, called Task 6.
- `verdict.CategoryCodesceneNotRun` / `CategoryCodesceneSkipped` — defined Task 5 (needed by
  its classifier test), used Task 6. Ordering is correct: Task 5 precedes Task 6 in the
  dependency graph via the shared file `submission_defect.go`.
- `PlanHeadersRollup` — defined Task 3 only.
- `Ledger.Append` / `Ledger.Load` — defined Task 10, used Task 10.

One ordering constraint that is easy to get wrong and is called out in both places: in
`ValidateCompletion` the CodeScene findings (Task 6 Step 8) must be prepended **before**
`isSubmissionDefectOnly` runs (Task 5 Step 6), or a `codescene_not_run` envelope will not be
flagged as a submission defect.

---

## Handoff note — the validate_plan gate

Tasks 8 and 9 modify `internal/verdict/plan.go` and `validate_plan`'s handler. Per the
standing convention, do **not** use `validate_plan` as the controller-side handoff gate for
this plan: findings from a tool this plan is actively changing are not a trustworthy gate. The
brainstorm-session spec review stands in as the handoff gate. The per-task lifecycle hooks
(`validate_task_spec` → `check_progress` → `validate_completion`) still apply normally.
