# Telemetry, the evidence gate, and per-task CodeScene attribution (v0.15.0)

**Status:** approved design, not yet implemented
**Date:** 2026-08-17
**Target release:** `0.15.0` (minor — new tool, new optional args, no breaking change)

## 1. Why

Three inputs converge on one release.

**Field feedback.** `feedback-2026-08-telemetry-and-evidence-gate.md`, derived from aggregate
analysis of 316 protocol calls and 1,099 findings, reports two defects. Both were re-verified
against HEAD at v0.14.0 and both are still live:

1. `HasStructuredHeader` is case-sensitive, unread, and therefore generates a false
   plan-adoption signal. Confirmed: declared at `internal/planparser/planparser.go:21`,
   assigned at `:64`, matched case-sensitively at `:105`, and read nowhere outside
   `planparser_test.go`. The dominant plan generator — superpowers `writing-plans` 6.3.0 —
   emits `**Acceptance Criteria:**` with a capital C (`SKILL.md:106`) and hard-enforces it with
   a `grep -c` self-check (`SKILL.md:299`), so every plan it produces scores `false` by
   construction. The reporting team spent an investigation disproving their own metric.
2. Two-thirds of first-round `validate_completion` failures are the reviewer asking for more
   evidence rather than reviewing code. `insufficient_evidence` is classified major, and §4.2
   step 3 tells implementers that any major finding blocks DONE — so a submission-formatting
   complaint demands a full re-review even when no code needs changing.

**Two operational asks.** CodeScene must actually run when a task implementation is verified
(only when CodeScene MCP is active), and a completed plan should yield per-task stats carrying
both the anti-tangent verdict and the CodeScene verdict.

**The connective tissue.** Both asks land on the same missing primitive. CodeScene has been
"REQUIRED when configured" at prompt level since v0.12.0, with a DONE-report attestation line
added in v0.13.0, but the v0.13.0 changelog states the problem plainly: *anti-tangent
structurally cannot observe whether the companion calls ran.* The stats subsystem has a
`codescene` block, but it is window-aggregate and anonymous, fed by a PostToolUse hook
(`examples/hooks/codescene-log.sh`) that has no idea which task it is inside — a gap already
filed upstream as `docs/feedback/codescene/per-task-attribution-ledger.md`. Once CodeScene
results enter anti-tangent **in band, keyed to a session**, "did it run" becomes observable and
per-task attribution falls out for free.

And feedback finding 2 turns out to be the same shape of problem as a missing CodeScene run:
a defect in the *submission*, not in the *code*. That observation is the spine of this design.

## 2. Goals and non-goals

**Goals**

- Correct the header-adoption signal and give it a real consumer.
- Make evidence-shaped rejections cheap to recover from without weakening the code review.
- Let anti-tangent observe whether CodeScene ran for a task, when the operator declares
  CodeScene is in play.
- Produce a deterministic per-task report at the end of a plan run, carrying both verdicts.
- Land the feedback's two smaller documentation notes.

**Non-goals** (existing project non-goals this design does not breach)

- No enforcement. Every new signal is a finding or a report; the server stays advisory and
  never blocks. A failed CodeScene quality gate never fails a verdict server-side.
- No code dependency on the CodeScene MCP server. anti-tangent receives a digest; it never
  calls CodeScene, and never learns whether CodeScene MCP exists except by operator config.
- No persistent storage by default. Plan-run state is in memory with the session TTL. The
  optional durable ledger lives under the already-carved-out stats subsystem and carries its
  own opt-in.
- No new LLM calls. `plan_run_report` is pure formatting.

## 3. Component 1 — header telemetry

**Change.** Match both markers case-insensitively, and consume the result.

`internal/planparser/planparser.go`:

```go
var structuredGoalRE = regexp.MustCompile(`(?i)\*\*Goal:\*\*`)
var structuredACRE   = regexp.MustCompile(`(?i)\*\*Acceptance criteria:\*\*`)

func hasStructuredHeader(body string) bool {
	return structuredGoalRE.MatchString(body) && structuredACRE.MatchString(body)
}
```

The doc comment on `RawTask.HasStructuredHeader` (`:18-21`) currently ends "Used for telemetry
only; not sent to the reviewer." The second clause stays true; the first becomes true for the
first time.

**Consumer.** `ValidatePlan` already parses the plan into `[]RawTask`. It counts them and
threads two numbers into the stat it already records:

- `stats.Event` gains `TasksTotal int \`json:"tasks_total,omitempty"\`` and
  `TasksWithHeader int \`json:"tasks_with_header,omitempty"\``. Both are only ever set on
  `validate_plan` events.
- `stats.Rollup` gains `PlanHeaders *PlanHeadersRollup \`json:"plan_headers,omitempty"\`` with
  `tasks_total`, `tasks_with_header`, and `adoption` (a float in `[0,1]`, `tasks_with_header /
  tasks_total`). The block is nil — and so absent from `rollup.json` — when the window contains
  no `validate_plan` events. When it is present but `tasks_total` is 0 (plans that parsed to
  zero tasks), `adoption` is 0.

Both additions are optional and `omitempty`, so the load-bearing cross-component contract
documented at `internal/stats/rollup.go:9-16` — the gnome-topbar consumer reads exact
snake_case keys — is extended, never altered. `computeRollup` returns a nil `PlanHeaders` when
the window contains no `validate_plan` events, matching how `Codescene` already signals
absence.

**Why not delete the field.** Deleting closes the finding but discards the question. The
reporting team wanted plan-header adoption as a number; they were failed by a wrong number, not
by a useless one. This gives them a correct one.

## 4. Component 2 — submission defects

### 4.1 The concept

A finding about the submission is not a finding about the code. `insufficient_evidence`,
`malformed_evidence`, and the new `codescene_not_run` all mean *you have not given me enough to
review*; none of them means *your code is wrong*. Today all three are indistinguishable from a
genuine defect at the point where the implementer decides whether to rework.

### 4.2 Where the flag lives

**Not on `Finding`.** `Finding` appears in all six reviewer-output JSON schemas, and
`internal/verdict/schema_invariants_test.go` asserts `required == properties` on every schema
object for OpenAI strict mode. Adding a field to `Finding` would force every reviewer to emit
it on every finding. Rejected.

**On the envelope, computed server-side.** `Envelope` (`internal/mcpsrv/handlers.go:27-38`)
gains:

```go
SubmissionDefectOnly bool `json:"submission_defect_only,omitempty"`
```

Set on `validate_completion` responses when the envelope has at least one critical or major
finding **and every** critical/major finding has a category in
`{insufficient_evidence, malformed_evidence, codescene_not_run}`. Minor findings are ignored by
the test — they never blocked DONE anyway.

When set, the server rewrites `next_action` to lead with the re-submit instruction, and
`formatEnvelopeSummary` (`internal/mcpsrv/summary.go:23`) emits a line:

```
  submission_defect_only: true — re-submit with the missing evidence; no code rework implied
```

The `summary_block` is what implementers paste verbatim into DONE reports (§4.2 step 3), so the
distinction reaches the controller without anyone having to reason about categories.

### 4.3 Making `insufficient_evidence` legal and consistent

`INTEGRATION.md:460` documents `insufficient_evidence` as an extract-only category, but
`validCategory` (`internal/verdict/parser.go:64`) accepts it from every tool and reviewers
demonstrably emit it on `validate_completion`. The category list in §6 is corrected to say so,
and `post.tmpl` names the category explicitly so reviewers stop routing evidence gaps through
`missing_acceptance_criterion`:

> When an acceptance criterion cannot be assessed because the submitted evidence is absent,
> partial, or does not cover it, emit `category: insufficient_evidence` rather than
> `missing_acceptance_criterion`. Reserve `missing_acceptance_criterion` for evidence that
> affirmatively fails or contradicts a criterion.

This makes the histogram in `rollup.json` mean what it says, and makes the
`SubmissionDefectOnly` computation reliable rather than dependent on reviewer word choice.

### 4.4 What is deliberately not built

No byte-threshold evidence gate. The corpus correlation is real (<2 KB → 86% fail) but a
threshold would misfire on genuinely one-line tasks, and the failure mode of a false rejection
is worse than the round-trip it saves. The prompt fix in §7 plus a cheap re-submit loop reaches
the same outcome without a heuristic.

## 5. Component 3 — CodeScene in band

### 5.1 The shared digest type

New leaf package `internal/codescene`:

```go
package codescene

type Verdicts struct {
	Improved int `json:"improved"`
	Degraded int `json:"degraded"`
	Stable   int `json:"stable"`
}

// Digest is one analyze_change_set result, reduced to counts and metadata.
// Field-for-field the shape examples/hooks/codescene-log.sh already computes.
type Digest struct {
	Ran            bool           `json:"ran"`
	SkipReason     string         `json:"skip_reason,omitempty"`
	Tool           string         `json:"tool,omitempty"`
	QualityGate    string         `json:"quality_gate,omitempty"` // passed|failed
	FilesAnalyzed  int            `json:"files_analyzed,omitempty"`
	Verdicts       *Verdicts      `json:"verdicts,omitempty"`
	Trend          string         `json:"trend,omitempty"` // improvement|regression|neutral
	NetPP          float64        `json:"net_pp,omitempty"`
	CategoryCounts map[string]int `json:"category_counts,omitempty"`
}

// Normalize recomputes Trend from NetPP and lowercases QualityGate, so a
// caller cannot report an improvement while submitting positive problem points.
func (d *Digest) Normalize()
```

`internal/codescene` imports nothing from this repo. `internal/stats` and `internal/mcpsrv`
both import it; `stats.CodesceneEvent` (`internal/stats/codescene.go:21-30`) is refactored to
embed `codescene.Digest` so the hook's on-disk record and the MCP argument stay one shape by
construction. The JSONL wire format is unchanged — `ran` and `skip_reason` are `omitempty` and
absent from hook-written records, and readers already tolerate absent fields.

`Normalize()` is called on every inbound digest. It is the only integrity check: anti-tangent
does not attempt to verify that the numbers came from a real CodeScene run, and the design does
not pretend otherwise.

### 5.2 The operator switch

`ANTI_TANGENT_CODESCENE` ∈ `""` (default) | `required`. Any other non-empty value is rejected
at startup with an error naming the allowed set — the same pattern as `ANTI_TANGENT_KB_STORE`
(`internal/config/config.go:146-152`). Unset means today's behaviour exactly: no new findings,
no new envelope fields.

The switch is operator-declared rather than agent-declared on purpose. If absence of the
`codescene` argument were interpreted by the agent's own claim about its host, a forgetful
agent and an unconfigured host would look identical — which is the exact blind spot v0.13.0
tried and failed to close with a prose attestation line.

### 5.3 The argument

`ValidateCompletionArgs` (`internal/mcpsrv/handlers.go:767-777`) gains:

```go
Codescene *codescene.Digest `json:"codescene,omitempty"`
```

### 5.4 Behaviour

Two new categories in `internal/verdict/verdict.go`, both **server-only** — absent from
`validCategory`, exactly as `CategoryMalformedEvidence` is today
(`internal/verdict/verdict.go:56-61`), so no reviewer can synthesise them:

```go
CategoryCodesceneNotRun  Category = "codescene_not_run"
CategoryCodesceneSkipped Category = "codescene_skipped"
```

When `ANTI_TANGENT_CODESCENE=required`, `validate_completion` prepends at most one finding:

| Submitted | Finding | Severity | Submission defect |
|---|---|---|---|
| no `codescene` block | `codescene_not_run` | major | yes |
| `ran: false` with `skip_reason` | `codescene_skipped` | minor | n/a (minor) |
| `ran: false`, no `skip_reason` | `codescene_not_run` | major | yes |
| `ran: true` | none | — | — |

An undeclared skip is treated as a non-run because the two are behaviourally identical from the
controller's seat, and because v0.13.0 already established that a stated reason is the price of
skipping.

When `ANTI_TANGENT_CODESCENE` is unset, no finding is ever emitted for a missing block — but a
digest that *is* supplied is still threaded and still recorded. Opting out of enforcement does
not opt out of attribution.

### 5.5 Threading to the reviewer

Independent of the switch, a supplied digest renders into `post.tmpl` before the evaluation
section:

```
## CodeScene change-set analysis (codebase-grounded)

Quality gate: failed
Files analyzed: 6   improved 1 / degraded 2 / stable 3
Trend: regression (net problem points +2.0)
Findings by category: Complex Method x2, Bumpy Road Ahead x1

This is deterministic analysis of the actual files, not a claim by the
implementer. Treat it as authoritative for Code Health. It does not by itself
fail an acceptance criterion — weigh it alongside the evidence.
```

This is codebase-grounded signal the text-only reviewer cannot otherwise produce, and it is the
first thing in this server that partially closes the "Structurally cannot catch" list in
INTEGRATION.md's `## Scope and limits`. Golden files under `internal/prompts/testdata/` gain a
post-with-codescene case; the existing goldens are unaffected because the block is conditional.

### 5.6 Regression visibility

When `Trend == "regression"`, the server appends a deterministic `quality` finding at **minor**
naming the net problem points and the top categories.

This is a judgment call and is called out as one. The argument for it: without it, a Code Health
regression reaches the envelope only if the reviewer chooses to mention it, so the signal that
motivated wiring CodeScene in at all would remain discretionary. The argument against: it is the
server forming an opinion about code, which it otherwise never does. Minor severity is the
compromise — it lands in the envelope, the `summary_block`, and the per-task ledger, and it
never blocks DONE. If this proves noisy it can be dropped without touching anything else in the
design.

## 6. Component 4 — plan runs and the report

### 6.1 Minting and threading the id

`validate_plan` mints `plan_run_id` — `"pr_"` plus 12 hex characters from `crypto/rand` — and
returns it as a **server-set** field on `PlanResult`:

```go
PlanRunID string `json:"plan_run_id,omitempty"`
```

Same treatment as the existing `SummaryBlock` field: set by the handler, absent from
`plan_schema.json`, never something a reviewer emits. It also appears in `formatPlanSummary`'s
output so a controller reading the paste block sees it.

`ValidateTaskSpecArgs` gains `PlanRunID string \`json:"plan_run_id,omitempty"\``. From there the
session carries it (`session.Session` gains a `PlanRunID` field), so `check_progress` and
`validate_completion` need no new argument — they update the row through the session they
already look up.

**Cache interaction.** Passing plan validations are cached for 3 minutes; a cache hit returns
the same `plan_run_id`, which is correct — same plan text, same run. A re-validation after the
cache expires mints a new id. Documented rule: the controller uses the id from its final
passing `validate_plan` call, which is the one §5.1 step 4 already tells it to make.

### 6.2 The store

New package `internal/planrun`, structured like `internal/session`: an in-memory store with TTL
eviction, TTL equal to the session TTL so a run and its sessions expire together.

```go
type TaskRow struct {
	Index          int
	TaskTitle      string
	PreVerdict     string             // from validate_task_spec
	Checkpoints    int                // count of check_progress calls
	PostVerdict    string             // "" until validate_completion lands
	Severity       map[string]int     // critical/major/minor at completion
	SubmissionOnly bool
	Codescene      *codescene.Digest  // nil when none was supplied
	CodesceneState string             // ran | skipped | missing
	CompletedAt    time.Time
}

type Run struct {
	ID          string
	CreatedAt   time.Time
	PlanVerdict string
	PlanQuality string
	TaskCount   int   // tasks in the validated plan, for "never started" accounting
	Rows        []TaskRow
}
```

`TaskCount` is captured at mint time from the parsed plan, so the report can distinguish a task
that failed from a task that was never dispatched.

### 6.3 The tool

`plan_run_report(plan_run_id)` — the seventh tool. Deterministic, **no LLM call, no provider
round-trip, no cost**. Returns the rows, the totals, and a paste-ready `summary_block`:

```
anti-tangent plan run report
  plan_run_id:  pr_8f21c4a90b3e
  plan:         pass / rigorous
  tasks: 5 of 5 completed   pass 3 | warn 1 | fail 1

  #  Task                     AT      CodeScene
  1  Add /healthz endpoint    pass    passed  -1.5pp
  2  Wire router              pass    passed  -0.5pp
  3  Retry backoff            warn    failed  +2.0pp  (Complex Method x2)
  4  Metrics counter          pass    skipped (docs-only task)
  5  Config plumbing          fail    not run

  codescene: 3 run, 1 skipped, 1 missing
  net problem points across run: 0.0
```

An unknown or expired `plan_run_id` returns a result carrying a single finding with the
existing `session_not_found` category and `criterion: plan_run_id`, rather than a new category
— the failure mode and the remedy (the state expired; re-run is the only recovery) are the
same.

### 6.4 Durability and the privacy carve-out

In-memory state is lost on restart. When both `ANTI_TANGENT_STATS_DIR` **and**
`ANTI_TANGENT_PLAN_LEDGER=1` are set, each completed row is appended to `plan-runs.jsonl` in
the stats dir, and `plan_run_report` falls back to reading that file when the in-memory run is
gone.

The second opt-in is not redundant. Every existing stats artifact is deliberately content-free
— `internal/stats/event.go:19-21` states that `Event` holds no finding text, no plan or spec
content, and only a salted session digest; the CodeScene record carries "no file paths, no
code, no function names, no session id". `plan-runs.jsonl` carries **task titles**. That is a
change in privacy posture, and silently applying it to everyone who already set
`ANTI_TANGENT_STATS_DIR` would be wrong. The ledger is documented in
`docs/team-setup/codescene-stats.md` (renamed in scope to cover both) as the one stats artifact
that is not content-free.

## 7. Component 5 — documentation

### 7.1 The budget constraint

`INTEGRATION.md` is 39,963 bytes against a CI-enforced `< 40,000`
(`.github/workflows/ci.yml:51-65`) — 37 bytes of headroom. This design adds to §3.1, §4.2
twice, §5.1, §6, and the environment-variable list.

Room is made by relocating the most version-churny, least protocol-shaped content, which is
what the CI failure message itself advises ("details live in the conventions doc / design
specs"). Moving into the existing `docs/team-setup/project-knowledge-conventions.md`, which
INTEGRATION.md §"Eight note types in three groups" already links to:

- "Applying bm_commands to BM v0.21.1" including the arg-mapping table, the `edit_note`
  operation hints, and the permalink-slug three-step pattern (~2.5 KB).
- The eight-note-types table and the "v0.7.0 canonical layout" paragraph (~1.5 KB).

Each leaves a one-line pointer. Net headroom after relocation: roughly 4 KB, comfortably more
than this release consumes. The 40,000-byte cap is left intact.

`plugin/anti-tangent-protocol/INTEGRATION.md` must be resynced byte-for-byte in the same commit
(`cp INTEGRATION.md plugin/anti-tangent-protocol/INTEGRATION.md`); CI enforces the match at
`.github/workflows/ci.yml:67-73`.

### 7.2 INTEGRATION.md edits

**§3.1** names the generator and the casing tolerance:

> This is the shape superpowers' `writing-plans` skill emits (it writes
> `**Acceptance Criteria:**` with a capital C; the parser matches case-insensitively, so either
> casing works).

**§3.1, adjacent** — the second smaller note from the feedback:

> `writing-plans` does not emit `**Non-goals:**`. Plans from it arrive with no scope bound, and
> the reviewer will say so. Add non-goals by hand, or set a repo-level instruction that does.

**§4.2 step 3** is replaced with the feedback's suggested text, adapted to the computed flag
rather than asking the implementer to classify findings themselves:

> **3. Before reporting DONE (REQUIRED).** Call `validate_completion` with the session_id, your
> summary, **a complete `final_diff` (or full `final_files`)**, and test evidence. A complete
> diff is a **precondition of the first call**, not something to add after a rejection —
> evidence-poor submissions fail most of the time and buy you a formatting review instead of a
> code review. Copy the `summary_block` field from the response verbatim into your DONE report.
>
> If the verdict is `fail` or contains `critical`/`major` findings, do not report DONE — fix the
> findings and re-validate. **Exception: when the response carries
> `submission_defect_only: true`, the blocking findings are all about what you submitted, not
> about your code. Attach the missing evidence and re-submit; no rework is implied.**

**§4.2 step 3b** gains the argument:

> Pass the result to `validate_completion` as the `codescene` argument (`ran`, `quality_gate`,
> `verdicts`, `trend`, `net_pp`, `category_counts`), or `{"ran": false, "skip_reason": "…"}` if
> you deliberately skipped. The prose status line is superseded by the structured field.

The short variant in §4.2 gets the same substitution in one line.

**§5.1** gains a step 6:

> 6. Capture `plan_run_id` from the passing `validate_plan` response and pass it as
>    `plan_run_id` in each implementing subagent's `validate_task_spec` call (add it to the
>    dispatch clause's task-spec field list). After the last task reports DONE, call
>    `plan_run_report` with that id and surface the table to the user.

**§6** corrects the category list so `insufficient_evidence` is documented for
`validate_completion` as well as extract, and adds `codescene_not_run` / `codescene_skipped` to
the operational (server-only) group alongside `malformed_evidence`.

**Environment variables** gains `ANTI_TANGENT_CODESCENE` and `ANTI_TANGENT_PLAN_LEDGER`.

**The opening paragraph** says "It exposes six tools"; that becomes seven, with
`plan_run_report` named alongside the plan-handoff gate.

### 7.3 Other documents

- `README.md` — the new tool in the tool table, both new env vars in the dotenv block, and the
  CodeScene section updated to describe the in-band argument.
- `CLAUDE.md` — the Project Overview says "exposes four tools", which was already stale at six;
  correct it to seven and name `plan_run_report`.
- `CHANGELOG.md` — a `## [0.15.0] - <date>` entry written as the code lands, not at the end.
- `docs/team-setup/codescene-stats.md` — document `plan-runs.jsonl`, its two opt-ins, and its
  privacy carve-out.

## 8. Testing

Per repo convention: `-race` always, no network in unit tests, `httptest` for HTTP shapes,
golden files for prompts regenerated with `-update` only after intentional template changes.

- `internal/planparser` — both casings of each marker, mixed case, one marker present only.
- `internal/stats` — `plan_headers` present and absent; `adoption` arithmetic; a rollup
  round-trip asserting the new keys are snake_case and that absent blocks marshal away
  entirely.
- `internal/codescene` — `Normalize()` corrects an inverted trend, handles zero net_pp as
  neutral, lowercases the gate; JSONL round-trip of a hook-written record (no `ran` field)
  through the embedded struct.
- `internal/mcpsrv` — the four-row behaviour table in §5.4 under both switch settings;
  `SubmissionDefectOnly` true for evidence-only failures, false when one genuine major finding
  is mixed in, false when only minor findings exist; the regression finding fires at minor and
  only on regression.
- `internal/planrun` — TTL eviction, ordering, rows for tasks that never completed, unknown-id
  report, ledger fallback after the in-memory run is dropped.
- `internal/prompts` — one new golden covering the CodeScene block; confirm the existing
  goldens are byte-identical (proof the block is genuinely conditional).
- `internal/mcpsrv` integration test — a full plan run end to end: `validate_plan` →
  two `validate_task_spec` → two `validate_completion` (one with a digest, one without) →
  `plan_run_report`, asserting the rendered table.

## 9. Rollout and compatibility

Everything is additive and default-off:

- `ANTI_TANGENT_CODESCENE` unset → no new findings, no `codescene` handling beyond recording a
  digest if one is volunteered.
- `ANTI_TANGENT_PLAN_LEDGER` unset → nothing written to disk beyond today's stats files.
- `plan_run_id` omitted → sessions behave exactly as now; `plan_run_report` simply has nothing
  to report.
- `submission_defect_only` is `omitempty` and absent whenever false.
- New rollup keys are `omitempty` pointers; the gnome-topbar consumer sees new optional keys and
  is otherwise untouched.

Branch `version/0.15.0`, with a matching `## [0.15.0]` CHANGELOG heading (CI-enforced). The
merge commit into `main` carries `[minor]`. Per repo convention `VERSION` is **not** bumped on
the branch — the release workflow does it.

## 10. Risks

**The regression finding may be noisy.** §5.6 is the one place the server forms an opinion about
code. It is minor-severity and isolated to a single code path so it can be removed cleanly.

**The digest is caller-attested.** `Normalize()` fixes internal inconsistency but nothing proves
a digest came from a real CodeScene run. This is the same trust model as `pinned_by` and
`controller_verified_references`, and the design does not claim otherwise. What changes is that
a *missing* run is now observable, which is the actual reported failure mode — under-adoption,
not fabrication.

**Plan-run state is process-local.** Sessions already are. A server restart mid-plan loses the
run unless the ledger opt-in is on; the report then covers only rows written after the restart.
Documented, not solved.

**INTEGRATION.md will fill up again.** This release buys roughly 4 KB. The next one that needs
space should consider the core/appendix split rather than another trim.
