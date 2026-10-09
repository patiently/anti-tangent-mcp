# anti-tangent protocol — for plan authors

Read [`core.md`](core.md) first. This part covers the task-block format that maps onto
`validate_task_spec` inputs. If you will also dispatch the plan, read
[`controller.md`](controller.md).

## 3. For plan authors — the anti-tangent-friendly task format

Give each task a small structured header block. The implementing subagent passes these fields verbatim into `validate_task_spec`; the reviewer uses them to decide whether the spec is implementable as written.

### 3.1 The required shape

```markdown
### Task N: <one-line title>

**Goal:** <one sentence: what success looks like>

**Acceptance criteria:**
- <testable criterion 1>
- <testable criterion 2>

**Non-goals:** *(optional but recommended)*
- <thing this task explicitly does NOT cover>

**Context:** *(optional)*
<relevant background, constraints, or links a fresh implementer needs>

<… your existing plan structure: Files / Steps / Code / etc. …>
```

This is the shape superpowers' `writing-plans` skill emits. It writes
`**Acceptance Criteria:**` with a capital C; the parser matches case-insensitively, so either
casing works.

`writing-plans` does **not** emit `**Non-goals:**`. Plans from it arrive with no scope bound,
and the reviewer will say so. Add non-goals by hand, or set a repo-level instruction that does.

The existing "Files:" / "Steps:" structure that superpowers, hone-ai, and most CLAUDE.md plans already use lives below the header block. The header is additive.

### 3.2 Worked example

```markdown
### Task 4: Add /healthz endpoint

**Goal:** Expose a liveness probe for the HTTP server.

**Acceptance criteria:**
- `GET /healthz` returns HTTP 200 with body `ok`.
- p95 latency under 50 ms at 100 RPS on a warm process.
- Endpoint is registered in `cmd/api/router.go` and covered by a handler test.

**Non-goals:**
- Database health (covered separately by `/healthz/deep`).
- Authentication on the endpoint.

**Context:**
The service is a Gin app on port 8080. The probe is consumed by the
Kubernetes liveness check defined in `deploy/k8s/api.yaml`.
```

### 3.3 What `validate_task_spec` actually checks

- **Structural completeness.** Is the goal stated? Are there acceptance criteria? Are non-goals declared where they help bound scope?
- **Acceptance-criterion quality.** Is each AC testable, specific, and unambiguous? For any vague AC, the reviewer suggests a concrete rewrite.
- **Implicit assumptions.** Each assumption a fresh implementer would have to make becomes a finding, so the spec author can either pin it down or explicitly mark it as implementer's discretion.

### 3.5 Anti-pattern: keep implementation steps OUT of the AC list

Acceptance criteria describe *what done looks like*, not *how to get there*. Implementation steps belong in the "Steps:" / "Files:" portion of the task, where they always lived. Mixing them produces brittle ACs that the reviewer flags as either redundant or hyper-specific.

### 3.6 Normative test bodies (binding test code in plans)

When a task pastes verbatim test code the implementer must land as written, wrap each test body in a fenced block immediately under a literal `**NORMATIVE TEST BODIES (verbatim):**` header. `validate_plan` extracts each fence server-side and threads the list into the per-task `validate_task_spec` `normative_test_bodies` input; the reviewer treats each entry as binding scope. Adjacent fences extract as separate entries. Bodies > 4000 Unicode code points are server-truncated with a `// truncated` marker; for legitimately longer bodies, paraphrase or excerpt and prefix with `// excerpt:` so the reviewer treats it as partial coverage.

### 3.7 `.trimIndent()` raw-string caveat

When a plan snippet is wrapped in `.trimIndent()` (or any equivalent raw-string trim), multi-line source phrases render newlines exactly where they sit in the markdown — anti-tangent reads the source, not the rendered output. Keep example strings on a single source line, and phrase ACs against the rendered string (e.g. "output contains `please decline politely`"), not against source layout.

### 3.8 Harness shape attestations

`harness_shape_attestation` is a structured optional input on `validate_task_spec`. Each entry is `{harness: string, path: string, assertions: []string}`. Use it when ACs depend on a test harness's stated capabilities (or non-capabilities). The reviewer treats each attestation as authoritative caller-attested context (no independent verification) and flags ACs that EXPLICITLY contradict an entry — e.g. an AC asking for behavior a `does not …` assertion forbids, or asserting a state directly contradicting a positive assertion — as `attestation_contradiction` findings. Absence of a capability is NOT a contradiction; do not list things to forbid them.

### 3.9 `**Files:**` bullet syntax (what the Create/Modify check parses)

`validate_plan` runs a deterministic, reviewer-free check that a task's `Modify:` targets can
exist when that task runs. It reads ONE structure and nothing else: a literal `**Files:**` line,
followed by bullets naming a verb and a path.

```markdown
**Files:**
- Create: `internal/planparser/filerefs.go`
- Modify: `internal/mcpsrv/handlers.go`
- Modify: `internal/verdict/parser.go:57-70`
- Create/Modify: `internal/prompts/templates/plan.tmpl`
- Delete: `internal/legacy/shim.go`
- Modify: internal/config/config.go (the roots parsing)
```

Rules the parser actually applies:

- The heading must be a line of its own reading `**Files:**` (case-insensitive).
- Bullets may use `-` or `*`. A space after the marker is OPTIONAL for a recognized verb —
  `-Create: path` parses. The space governs the UNRECOGNIZED-VERB fallback: a bullet whose
  verb is not Create/Modify/Delete is skipped and collection continues only when it starts
  `-` or `*` followed by one space; without it, collection stops at that line.
- The verb is `Create`, `Modify`, or `Delete`, case-insensitive. Two verbs may be joined with
  `/` (`Create/Modify:`) for a file one task creates and another edits; both are recorded.
- A bullet may list several paths, not just one. Backtick-quoted spans are separated only by
  whitespace, a comma, semicolon, `&`, `+`, or `and`, and the list reads up to the first span
  followed by anything else — so a code span used in prose (`` `Foo`, `## Configure` ``) is not
  read as another path. Unquoted, the list is comma-separated single words after the first word.
- A trailing parenthetical (`(the roots parsing)`) is dropped, and so is a trailing line anchor
  — `:57`, `:57-70`, `:57,70`, a comma-separated list of either (`:60,166,174,419`,
  `:57-70,90-95`), and repeated forms like `:57:12` — so anchoring a `Modify:` to the lines you
  are editing is safe. The list may have a space after each comma and may end in `, …`
  (`:6-22, 29`).
- Not checked: a pattern (`*`, `?`, `{`), a slash-free name after a path that has a directory
  (read as a sibling shorthand of it), and a later item on the bullet that is not a file name (a
  symbol, a bare word).
- Paths are repo-relative. Collection stops at the first line that is neither a bullet nor
  blank, so a following `**Steps:**` section is never harvested.

The section is OPTIONAL. A task without it yields no file operations and no findings — the check
guards plans that opt into the structure, it does not demand that they do. The `json:metadata`
fence's `files` array is a flat list with no verb, so it cannot drive this check; the bullets are
the only source. A `task_order_contradiction` a controller has checked and found wrong is waived
with a `controller_rulings` entry on its `id`, like any other finding.

**State the comment policy, or point at it.** A plan is the one artifact every implementing
subagent reads, and an implementer working without this plugin loaded has no comment policy
otherwise. Either restate the policy in the plan's constraints section, or carry the canonical pointer line,
byte for byte:

```
Comments: anti-tangent-protocol implementer.md §4.4
```

`validate_plan` emits a plan-level `major` (`criterion: comment_policy_absent`) when a plan carries
neither. Equivalent wordings are accepted, but this is the line to paste.

### 3.10 Lean by default

`validate_plan` flags a plan that mandates over-building, as one `quality` / `over_building`
finding per task plus one plan-level finding for a pattern that spans tasks, always `minor`,
each instance tagged:

- `reuse:` a task re-writes a helper an attached file or Project knowledge already provides.
- `stdlib:` a dependency, or a hand-written utility, for what the standard library covers.
- `native:` application code for what the platform provides (a DB constraint, `<input type="date">`, CSS).
- `yagni:` an interface with one implementation, a factory for one product, a config value no
  task varies, a layer with one caller — judged across the whole plan.
- `delete:` scaffolding for a phase this plan does not deliver.
- `shrink:` fenced code a shorter form replaces; test fences are exempt.

**Justify deliberate structure in `Context:`, not in the dispatch conversation.** The same check
runs at task start (`validate_task_spec`) and on the built code (`validate_completion`), and only
`Context:` reaches them: "Task 9 adds the S3 backend", "chosen for the TZ edge cases the stdlib
mishandles". A controller ruling waives the finding at plan level only.

**Attach what a task might duplicate.** A helper the reviewer can see in `context_paths` is a
`reuse` finding; one it cannot see is silence, not approval.

An acceptance criterion that names an interface is an implementation step in disguise (§3.5):
state the outcome and let the implementer pick the leanest structure that delivers it.

### 3.11 Agent-network plans and experiment tasks

Needs anti-tangent-mcp 0.28.0 or later. An agent-network plan builds model behaviour: replies an LLM writes, judged by evals. Declare it,
or the reviewer pushes that work into code — a regex where a prompt change belonged, a "100% of
runs" criterion no model can meet.

- `**Plan kind:** agent-network` above the first task heading. The reviewer then reports a
  criterion demanding that model behaviour hold in every run (`determinism_demand`), accepts a
  rate report as evidence, counts `rigidity:` under over-building, and checks the fix ladder.
- `**Kind:** experiment` on a task that changes model behaviour and is measured, then kept or
  reverted. The default is `build`. An experiment is never lightweight.
- `**Rung:** oracle | variance | facts | tool | commitment | prompt | owner` — the fix-ladder
  rung the task works at. An experiment without one draws `rung_missing`. A `commitment` task
  (a code gate) whose `Context:` does not rule out the lower rungs is a `fix_ladder` finding.

`validate_plan` returns `task_kind`, `rung` and `plan_kind` per task; pass them on
`validate_task_spec` and a lightweight `validate_completion` (a session carries them on). The plan run's values win when the call attaches to it (`kind_conflict` notes a
different one), and an unknown value draws `unknown_kind`. A plan with no `**Plan kind:**`
header may use `Kind:` and `Rung:` labels of its own: their unknown values draw nothing.

Pass your project's boundary rules — what code may do with reply and user text — as
`boundary_rules` on `validate_plan`; a per-task call attached to the run inherits them, and
rules it sends itself replace them for that task, noted as `kind_conflict`.
anti-tangent ships none. A spec or change that does what a rule forbids is `boundary_violation`,
major (minor at `check_progress`, which cannot see what the task added). Settle it by moving the
work or with a controller ruling. With rules, or on an experiment, `validate_completion` needs a
diff when it sends `final_files` or records the change as kept, or it returns `diff_required`.
A build task with rules that sends no diff and no files draws `boundary_unchecked`: nothing was
checked against the rules.
`boundary_rules_missing` and `plan_kind_missing` say one of the two declarations is missing.
These notes are minor and never move a verdict.

```json
"boundary_rules": [
  "Code checks structured data only. It never inspects reply or user words: no regex or phrase matching.",
  "The LLM writes every reply. The only code-written text is verbatim legal text."
]
```

An experiment's criteria state the protocol, never a guaranteed outcome:

```markdown
### Task 6: Ask for a missing ZIP

**Kind:** experiment
**Rung:** prompt

**Goal:** The bot asks for a missing ZIP code more often.

**Acceptance criteria:**
- `evals/core/zip-missing.yaml` measured at n=10 before and after; baseline 5/10 (interval 0.24–0.76).
- Keep the change if the after-rate is at least 9/10 and the regression suite shows no eval
  outside its baseline interval; otherwise revert.
- The result, kept or reverted, is recorded in `rate_digest`.

**Context:** A revert that follows the rule meets this task. The ZIP field already reaches the
model, so the facts and tool rungs are ruled out. `pinned_by`: `evals/suite.yaml`.
```

At completion, send the scoreboard as `test_evidence_path` and the counts as `rate_digest`; a
kept change also sends its diff.

### Write-time comment guard (if `anti-tangent-guard` is installed)

The comment policy (`implementer.md` §4.4) can be enforced, not just stated. If the
`anti-tangent-guard` plugin is installed, its `PreToolUse` hook on `Edit`/`Write` refuses — `exit
2`, before the write ever lands — a write that adds a comment matching one of a small set of
mechanical tells: an issue, pull-request or task reference, or a version reference narrating when
something changed. A tracker key (`ABC-1234:`) is matched only where the project sets
`ANTI_TANGENT_TICKET_PATTERN` to its own key shape; there is deliberately no default, so an
unconfigured project gets no tracker tell at all. Both hooks honour it: the pattern is compiled
once and the close-time scan reads the same tell set this write-time one does. Prose narration ("previously", "no longer", "this replaced") cannot be matched
without false positives, so that half of the policy is reviewer-led instead — `post.tmpl` catches
it at completion time — and a clean write-time pass is not proof the whole of §4.4 was followed. A
refused `Edit`/`Write` is not a bug in your call; it is the policy holding. Rewrite the flagged
comment and retry the same edit.

The hook fires per tool call regardless of which session issued it, so it reaches a dispatched
subagent's own `Edit`/`Write` calls the same as the controller's. It does not see a `Bash`-written
file (a heredoc, `sed -i`) at all, so writing the same comment through `Bash` bypasses this layer
entirely — a comment that reaches disk that way is caught, if at all, only by `post.tmpl`'s
reviewer rule or the completion guard's close-time scan. See the guard plugin's README for the
full limitations list.

Kill switch: `ANTI_TANGENT_COMMENT_GUARD=0`. This is a Claude Code plugin hook, not the MCP
server — the server itself stays advisory and never blocks (root `CLAUDE.md`, "What This Repo Is
Not").
