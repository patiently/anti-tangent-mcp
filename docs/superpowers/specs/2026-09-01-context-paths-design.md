# Caller-attached source files for `validate_plan` (`context_paths`) + order-aware Create/Modify check — design

**Status:** approved design (brainstorming output), pre-implementation
**Date:** 2026-09-01
**Issue:** [#61](https://github.com/patiently/anti-tangent-mcp/issues/61)
**Release vehicle:** `version/0.17.0` (backward-compatible minor; branched off `version/0.16.0`,
whose PR #62 is still open against `main`) with a `## [0.17.0]` CHANGELOG entry.

---

## 1. Summary

`unverifiable_codebase_claim` exists purely because the reviewer is text-only. Tuning its noise
has taken three passes — #8 (reviewer hallucinates findings about symbols it cannot see), #10
(`validate_plan` iteration UX), #16 (evidence context). This design attacks the cause instead
of the symptom: let the caller attach the actual source files, so the reviewer can *check* a
codebase claim rather than shrug at it.

Two changes ship together:

1. **`context_paths`** — a caller-curated list of absolute paths that the server reads and
   renders into the plan-review prompt, with a rewritten reviewer posture that says exactly
   which files are attached and that everything else stays black-box.
2. **An order-aware Create/Modify consistency check** — deterministic, reviewer-free, no
   provider call. Evaluated and costed in the 0.16.0 spec §9.1; built here in the only form
   that does not flood the caller with false positives.

The second is independent of the first and could ship alone. They are bundled because they
share a release, a docs pass, and the same "make the gate less noisy" goal.

### 1.1 Why

Measured on the same real 9-task, 170,158-byte plan as the 0.16.0 spec, whose `json:metadata`
fences carry machine-readable `"files": [...]` arrays:

| | |
|---|---|
| unique referenced paths | 31 (24 exist; 7 are to-be-created) |
| total bytes of the 24 existing | **402,740 ≈ 100,685 tokens** |
| the plan itself | ~46K tokens |
| per reviewer call | ~147K tokens — **the referenced code is 2.2× the plan** |

Three consequences constrain everything below.

**Cost.** Three chunked calls at ~147K input tokens is ~441K tokens per round: roughly **$1.31
per round** uncached at the default `PlanModel` rate (`anthropic:claude-sonnet-4-6`, $3/MTok
input), against the ~$0.01–$0.02 per call `docs/protocol/controller.md` currently advertises.
Attachment is therefore **opt-in, per call, with its own caps** — never automatic, never
inferred from the plan.

**Whole-file attachment spends its budget badly.** The three largest referenced files (70KB,
57KB, 47KB) are 43% of the corpus and are barely touched by the plan. Selection is
**caller-supplied**, not server-extracted: the caller knows what is relevant, and explicit
absolute paths need no repo-root inference to resolve the plan's repo-relative entries against.

**Partial visibility is the real hazard, and it is a prompt problem.** Every plan template
opens with *"You have access ONLY to the plan markdown… Treat such identifiers as black-box
references"* and then mandates `unverifiable_codebase_claim`. With some files attached that
instruction is wrong for those files and right for everything else, and a reviewer holding two
dozen files will reason from *absence* about every symbol they reference. §4 is a rewrite of
the tool's core review posture, not a plumbing change.

### 1.2 Goals

- A caller can attach the specific files a plan's claims depend on, and the reviewer verifies
  those claims instead of emitting `unverifiable_codebase_claim` for each one.
- A plan claim that an attached file **contradicts** becomes a hard, non-suppressible finding —
  the highest-value output `validate_plan` can produce.
- A reviewer holding a partial view of the codebase never mistakes "not in the attached set"
  for "not in the codebase."
- Attachment cost is bounded, visible, and refused loudly rather than silently truncated.
- A plan whose `Modify:` targets cannot exist when the task runs is caught for free.

### 1.3 Non-goals

- **Server-side extraction of paths from the plan's `json:metadata`.** Deferred until the cost
  story is dogfooded; see §9.1. The caller curates.
- **Line ranges or per-file truncation.** Rejected in §9.2 — partial files reintroduce exactly
  the absence-reasoning hazard that §4 exists to close, and they do it for the one category
  (§5) where a false positive is most damaging.
- **A second cache breakpoint separating files from plan.** Costed and deferred; see §7.1.
- **Attachment for `validate_task_spec` / `validate_completion` / the other four tools.**
  `validate_plan` only. The completion hooks already receive the code under review.
- **Blocking non-Anthropic reviewers.** OpenAI and Google clients have no prompt caching in
  this codebase, so attachment is full freight on every call for them. Documented (§8.3), not
  refused — the server stays advisory.

---

## 2. Input contracts

Two new optional fields on `ValidatePlanArgs`. Both are additive; a caller that supplies
neither gets byte-identical behavior to 0.16.0, including byte-identical rendered prompts (§4.4).

```go
type ValidatePlanArgs struct {
    PlanText          string   `json:"plan_text,omitempty"`
    PlanPath          string   `json:"plan_path,omitempty"`
    ContextPaths      []string `json:"context_paths,omitempty"` // NEW
    RepoRoot          string   `json:"repo_root,omitempty"`     // NEW
    ProjectKnowledge  string   `json:"project_knowledge,omitempty"`
    ModelOverride     string   `json:"model_override,omitempty"`
    MaxTokensOverride int      `json:"max_tokens_override,omitempty"`
    Mode              string   `json:"mode,omitempty"`
}
```

### 2.1 `context_paths`

Absolute paths to regular files, read **whole**. Order is preserved in the rendered prompt so
a caller can put the most relevant file first. Duplicates (after symlink resolution) are
de-duplicated, keeping the first occurrence, and the duplicate is not billed twice.

The tool description gains a sentence that states the cost posture in the same breath as the
capability, because the description is the only thing many callers read:

> `context_paths`: absolute paths to source files the plan makes claims about. The server reads
> them and the reviewer verifies claims against them instead of emitting
> `unverifiable_codebase_claim`. Attach only files the plan actually touches — this is the most
> expensive input the tool takes.

### 2.2 `repo_root`

Absolute path to the repository the plan's `Files:` bullets are relative to. Its **only**
consumer is the disk tier of §6. Absent, §6 degrades to its order-only tier rather than
erroring — the check is a free guard, and refusing to review a plan because a caller did not
supply an optional path would be a regression.

---

## 3. Resolution, caps, and refusal semantics

### 3.1 Resolution reuses `resolveFileInput` unchanged

Every path in `context_paths`, and `repo_root` itself, goes through the existing
`resolveFileInput` (`internal/mcpsrv/file_source.go`), which already provides, in this order:

1. non-empty and absolute check,
2. `filepath.EvalSymlinks`,
3. `withinRoots` against `ANTI_TANGENT_PLAN_ROOTS` (so a symlink cannot hop outside a root),
4. `openNoFollow` (real `O_NOFOLLOW` on Unix),
5. re-stat **from the open handle**, regular-file check,
6. capped `io.LimitReader` read that holds regardless of what the stat reported,
7. sha256 of the bytes actually read.

No new filesystem code is written, and no new sandbox is invented: `context_paths` inherits
`ANTI_TANGENT_PLAN_ROOTS` exactly as `plan_path` does. An operator who has already restricted
plan reads has restricted attachment reads by the same act.

`repo_root` is resolved with the same roots check but is stat'd as a **directory**, so it needs
a small sibling (`resolveDirInput`) rather than `resolveFileInput` itself.

### 3.2 Three caps, all enforced before any provider call

| Cap | Env var | Default | Applies to |
|---|---|---|---|
| per-file bytes | `ANTI_TANGENT_CONTEXT_MAX_FILE_BYTES` | 131072 (128 KiB) | each resolved file |
| total set bytes | `ANTI_TANGENT_CONTEXT_MAX_PAYLOAD_BYTES` | 524288 (512 KiB) | sum of resolved files |
| file count | *(none — fixed constant)* | 50 | `len(context_paths)` |

Defaults are chosen against the measured corpus: the largest real referenced file was 70KB
(128 KiB gives headroom while refusing a pathological multi-megabyte file), and 512 KiB
comfortably holds the full 402,740-byte 24-file corpus. The count cap is a fixed constant, not
an env var, mirroring the existing 50-entry cap on `pinned_by` — it exists to stop a
degenerate 500-entry list from producing an unreadable prompt, not to be tuned.

The count cap is validated **before any read**, so a pathological list costs one comparison.

### 3.3 Which failures are envelopes and which are transport errors

The existing convention is followed exactly, because callers already branch on it:

| Condition | Result | Rationale |
|---|---|---|
| any cap exceeded | `tooLargePlanResult` **envelope** | content-too-large is an envelope today |
| path missing / unreadable / not a regular file | **transport error** | bad input is an error today |
| `repo_root` supplied but unresolvable, or not a directory | **transport error** | same |

A path in `context_paths` that does not exist is a **caller bug**, not a to-be-created file:
7 of the 31 referenced paths in the measured plan are to-be-created, and attaching one is a
mistake the caller should hear about. Silently skipping it would hand the reviewer a shorter
attached set than the caller believes it sent — and under §4's posture, a file the reviewer
believes is attached-and-absent is precisely how a false `contradicted_codebase_claim` gets
manufactured. Loud failure is the safe direction.

The too-large envelope message names the offending path (per-file), or the running total and
the cap (set-level), and names `ANTI_TANGENT_CONTEXT_MAX_FILE_BYTES` /
`ANTI_TANGENT_CONTEXT_MAX_PAYLOAD_BYTES` so the message is actionable without reading docs.
This requires widening `tooLargePlanResult(total, planBytes, pkBytes, limit)` with a
`contextBytes` parameter; its existing call sites pass 0.

### 3.4 Telemetry

`recordStat`'s `payloadBytes` includes the resolved context bytes on every `validate_plan`
path that reaches it. Without this the stats subsystem would under-report the most expensive
calls the server makes by a factor of ~3, which would make the opt-in stats output actively
misleading about cost.

---

## 4. Rendering and reviewer posture

### 4.1 Placement

Attached files render **after** the Project-knowledge section and **before**
`## Plan under review`. Two reasons, both load-bearing:

- The block lands inside the region that `planSuffixIndex` already treats as the cacheable
  prefix (§7), so no splitting logic changes.
- `planSuffixIndex` finds the **last** line-anchored `## What to evaluate`. Attached file
  content is interpolated *before* the real heading, so a source file that happens to contain
  a line reading `## What to evaluate` still cannot shrink the cacheable prefix. This invariant
  is why files go before the plan rather than after it, and it gets an explicit test (§11).

### 4.2 Delimiters, not code fences

Attached content is **not** wrapped in a markdown code fence. Go source contains backticks
(raw string literals), and this repository's own templates contain fences, so any fence length
chosen can be broken by a legitimate attachment. Explicit delimiters instead:

```text
--- BEGIN FILE: /abs/path/internal/config/config.go (4211 bytes, sha256 9f2ab41c…) ---
<verbatim file content>
--- END FILE: /abs/path/internal/config/config.go ---
```

The byte count and short hash are the same provenance a caller already sees on the summary's
`source:` line for `plan_path`, reusing `fileSource.String()`'s formatting. They also give the
reviewer a way to say *which* file its evidence came from without paraphrasing.

### 4.3 The ground rules become a shared partial

The ground-rules paragraph is currently **duplicated verbatim across all three plan
templates** — `plan.tmpl`, `plan_findings_only.tmpl`, `plan_tasks_chunk.tmpl`. The posture
rewrite below would triplicate it and then drift on the next edit, so it is extracted to
`internal/prompts/templates/plan_rules.tmpl` and included by all three.

`render()` changes from parsing one template file to parsing the whole `templates/*.tmpl` set
and executing the named one. Parsing eight small templates per call is negligible next to the
HTTP round-trip it precedes, and it removes the need to enumerate partials per call site. The
partial is named without a leading underscore deliberately: `go:embed`'s exclusion rules around
`_`-prefixed names are subtle enough not to depend on.

This is a targeted improvement to code the change already touches, not an unrelated refactor.

### 4.4 The no-attachment render is byte-identical to today

When `context_paths` is empty, `plan_rules.tmpl` emits exactly the current text and no
attachment section. All existing golden files in `internal/prompts/testdata/` are unchanged,
which keeps the reviewable surface of this change to the *new* behavior. A golden diff on a
no-attachment case is a regression, not an expected update.

### 4.5 The rewritten posture

With `context_paths` non-empty, the ground rules gain a block that states, in this order:

1. **What is attached, by path.** The list is enumerated explicitly, not summarized as a count.
2. **Attached files are complete.** For a listed file, absence *is* evidence: if a symbol is
   not in it, it is not in that file.
3. **Everything else remains black-box.** Absence from the attached set is **not** evidence of
   absence from the codebase — the caller attached only what they judged relevant, and the
   overwhelming majority of the repository is not here.
4. **`unverifiable_codebase_claim` is now scoped** to claims about unattached code. Do not emit
   it for a claim the attached files settle.
5. **`contradicted_codebase_claim` (§5) is scoped** to claims an attached file *refutes*. Never
   emit it about a symbol, file, or path that is not in the attached set.

Rules 3 and 5 are the same guard stated from two directions, deliberately. The failure mode
this feature introduces — a reviewer confidently reporting that `Foo.Bar` does not exist
because it was not among the twelve attached files — is worse than the noise it removes, and
it is worth the redundancy in the prompt.

---

## 5. Verdict semantics: `contradicted_codebase_claim`

### 5.1 The new category

```go
// CategoryContradictedCodebaseClaim is emitted by the reviewer when a plan
// statement is refuted by the contents of a file supplied via context_paths.
// Distinct from unverifiable_codebase_claim: an attached file is ground truth
// read from disk by the server, so a contradiction is a hard finding, not
// "can't verify." Intentionally NOT in applySeverityFloor's list — the
// reviewer's chosen severity (typically major) is preserved. Only valid when
// context_paths supplied the file the evidence cites.
CategoryContradictedCodebaseClaim Category = "contradicted_codebase_claim"
```

Added to `validCategory` and to the enum in the plan schemas
(`PlanSchema`, `PlanFindingsOnlySchema`, `TasksOnlySchema`). Deliberately
**absent** from `applySeverityFloor`, exactly mirroring the documented rationale for
`CategoryAttestationContradiction`: caller-attested facts make a contradiction hard, and a file
read from disk is stronger evidence than an attestation.

`attestation_contradiction` is not reused. It denotes a conflict with a caller-*asserted*
harness shape; this denotes a conflict with server-*read* bytes. Collapsing them would leave a
consumer unable to tell which kind of evidence backed a finding, which is the one thing a
reviewer-emitted contradiction needs to be actionable.

### 5.2 The two suppression paths that must not swallow it

Both live in `internal/mcpsrv/plan_normalize.go` and both currently key on
`CategoryUnverifiableCodebaseClaim`, so both are already correct by construction. They get
explicit tests anyway, because silently rolling a hard contradiction into a
"go grep it yourself" checklist is the single failure mode that would make this feature worse
than not shipping it:

- `splitTaskUnverifiable` must leave `contradicted_codebase_claim` attached to its task and
  out of the plan-level `codebase_reference_checklist` rollup.
- `allPlanFindingsAreMinorUnverifiable` must return false when any such finding is present, so
  `calibratePlanVerdictForUnverifiableOnly` cannot force `plan_verdict` to `pass`.

### 5.3 Effect on the checklist rollup

Nothing else changes. On a call with attachments the rollup simply gets smaller, because the
reviewer emits `unverifiable_codebase_claim` only for unattached code. That shrinkage is the
measurable success signal for this feature and is what dogfooding should look at first.

---

## 6. Order-aware Create/Modify consistency check

Deterministic, server-side, no provider call. Runs on every `validate_plan` invocation,
including the early-exit paths that never reach a reviewer.

### 6.1 Where the data comes from

Per-task `**Files:**` bullets of the form:

```text
**Files:**
- Create: `internal/planparser/filerefs.go`
- Modify: `internal/mcpsrv/handlers.go` (lines 1651-1875)
```

The `json:metadata` fence's `"files"` array is a **flat list with no Create/Modify
distinction**, so it cannot drive this check. The bullets are the only source. Parsing lives in
a new `internal/planparser/filerefs.go`: match `^\s*[-*]\s*(Create|Modify|Delete)\s*:\s*`,
take the path from backticks when present (else the first whitespace-delimited token), and
strip a trailing parenthetical such as `(lines 1651-1875)`.

Tasks that carry no `**Files:**` section contribute nothing and are not findings — the check is
a guard on plans that opt into the structure, not a demand that they do.

### 6.2 Only one of the two directions is worth shipping

The 0.16.0 spec §9.1 measured a naive disk cross-check at **10 contradictions, all false
positives**. Decomposed:

- **9 were `Create:` targets that already exist on disk**, because an earlier task had already
  been implemented in the worktree. A resumed or partially-executed plan run is a *legitimate*
  state, so this direction is inherently false-positive-prone and is **dropped entirely**.
- **1 was a `Modify:` target absent from disk** because an earlier task creates it. This is the
  direction worth having, once it is order-aware.

Made order-aware, the same plan reports **0**. The check earns nothing on a well-formed plan;
that is the point of a guard.

### 6.3 Two tiers

**Order tier** — always runs, needs no filesystem access:

> Task *N* lists `Modify: X`, and the only task listing `Create: X` is task *M* where *M > N*.

A pure ordering contradiction, provable from the plan text alone.

**Disk tier** — runs only when `repo_root` is supplied:

> Task *N* lists `Modify: X`; `X` does not exist under `repo_root`, and no task *M < N* lists
> `Create: X`.

Paths are joined to `repo_root` and must remain inside it after cleaning (a `Modify:
../../etc/passwd` bullet is ignored, not stat'd). The check reads no file contents — it stats
only.

### 6.4 Output shape

One plan-level finding, following the existing `codebase_reference_checklist` rollup shape so
consumers need no new rendering:

```text
severity:   major
category:   other
criterion:  task_order_contradiction
evidence:   Task 3 modifies `internal/verdict/verdict.go`, created by Task 7
            Task 5 modifies `internal/prompts/templates/plan_rules.tmpl`, which does not exist
suggestion: Reorder the tasks, or add the missing Create: bullet to an earlier task.
```

`major`, because an implementer dispatched against it will fail. `category: other` rather than
a new category: this is server-emitted and deterministic, and the existing server-only
categories (`malformed_evidence`, `codescene_not_run`) are deliberately excluded from
`validCategory`, so introducing a second reviewer-invisible category here would add schema
surface for no consumer benefit. The `criterion` carries the identity.

Being `major` and not `unverifiable_codebase_claim`, it correctly blocks
`calibratePlanVerdictForUnverifiableOnly` from force-passing the plan.

---

## 7. Caching and the plan cache key

### 7.1 One breakpoint; the two-breakpoint split is deferred

`providers.Request.CachePrefix` stays a single string and `splitPlanPrompt` /
`planSuffixIndex` are untouched. Because attached files render inside the existing prefix
(§4.1), the chunked path caches `[ground rules][project knowledge][files][plan]` as one unit:
chunk 1 writes it, chunks 2..K read it at ~0.1×. That captures **100% of the within-round
saving**, which is the whole of what 0.16.0 delivers today.

The alternative — a second breakpoint splitting `[files]` from `[plan]` so the file corpus
survives a plan edit between rounds — was costed and deferred. Giving the files block a
1-hour TTL and the plan block the default 5-minute one, four re-validation rounds over the
measured corpus come to roughly $2.79 with the split against $4.12 without it; but it costs a `providers.Request` interface change across
three clients, per-block TTL plumbing, and replacing the string-search prompt split with
explicit template segments — and it is *more* expensive than the single breakpoint on a
one-shot validate-once run, because a 1-hour cache write is 2× and nothing reads it back.
Split it out as its own issue once attachment has been dogfooded and the real round cadence is
known. Note for that issue: blocks with a longer TTL must appear **before** shorter-TTL blocks
in a request, so `[files]1h [plan]5m` is the only legal ordering — which is what §4.1 already
renders.

The single-call path (`plan.tmpl`, task count ≤ `PlanTasksPerChunk`) continues to set no
breakpoint at all. One call has no in-round reads, and a breakpoint there would be a write
premium against zero reads — the same reasoning 0.16.0 recorded, unchanged by attachment
under a single-breakpoint design.

### 7.2 The cache key must include file content

`planPassCacheKey` gains the ordered list of resolved `(path, sha256)` pairs. Hashes, not
contents: the key is itself hashed, so carrying 400KB of bytes into it buys nothing.

This is a correctness requirement, not an optimization. `planPassCache` has a 3-minute TTL and
keys on plan content; without the attached-file hashes, a caller who fixes the source file a
finding complained about and immediately re-validates gets the **stale pre-fix review** back
from the in-process cache, with no indication that anything was reused. That is precisely the
edit-and-re-validate loop this feature is meant to make fast.

`fileSource` already carries the sha256, so no extra hashing pass is needed.

### 7.3 Provenance in the summary

`planSummaryMeta` gains a `ContextFiles []fileSource`. The summary block renders a compact
list under the existing `source:` line:

```text
  source:        /repo/docs/plans/2026-09-01-thing.md (170158 B, sha256 3c1af09b…)
  context:       12 files, 214,880 B
                 - /repo/internal/config/config.go (11,204 B)
                 …
```

Same rationale as 0.16.0's `source:` line: a controller showing a human why the gate passed
must be able to show *what the reviewer could see*. Carried per-call in `planSummaryMeta`, not
stored on the cache entry, for the reason already documented there — a cache entry shared by
two callers must never echo the other caller's paths.

---

## 8. Configuration

### 8.1 New env vars

| Var | Default | Meaning |
|---|---|---|
| `ANTI_TANGENT_CONTEXT_MAX_FILE_BYTES` | 131072 | per-attached-file byte cap |
| `ANTI_TANGENT_CONTEXT_MAX_PAYLOAD_BYTES` | 524288 | total attached-set byte cap |

Both validated as positive integers at load, following the existing pattern for every other
byte cap in `internal/config/config.go`.

### 8.2 Reused, not duplicated

`ANTI_TANGENT_PLAN_ROOTS` governs `context_paths` and `repo_root`. No second allowlist: a
second sandbox var would be a way for an operator to lock one door and leave the other open.

`ANTI_TANGENT_PLAN_MAX_PAYLOAD_BYTES` continues to govern plan + project-knowledge only.
Attachment gets its own budget so an oversized attachment set is refused with a distinct,
actionable message rather than being blamed on the plan.

### 8.3 The non-Anthropic caveat, documented

`internal/providers/openai.go` and `google.go` concatenate `CachePrefix` onto `User` — no
provider-side caching exists for them. A 100K-token attachment is therefore full price on every
call of every round for a caller whose `ANTI_TANGENT_PLAN_MODEL` is not Anthropic. The README
says so plainly next to the new env vars. Not refused: the server is advisory, and the operator
who set that model can weigh it.

---

## 9. Rejected alternatives

### 9.1 Server-side extraction from `json:metadata`

The measured plan's fences carry machine-readable `"files": [...]` arrays, so extraction would
be deterministic rather than heuristic. Rejected for now on two grounds: the caller knows which
of the 31 referenced paths actually matter (the three largest were 43% of the corpus and barely
touched), and repo-relative entries would need repo-root inference that explicit absolute
paths make unnecessary. Revisit as a convenience once §1.1's cost story is understood in
practice — it is additive to this design, not a competing one.

### 9.2 Line ranges or truncating oversized files

Rejected. Both hand the reviewer a *partial* file while §4.5 tells it that attached files are
complete and that absence within them is evidence. A reviewer holding lines 120–260 of
`config.go` would emit a hard `contradicted_codebase_claim` about a field defined on line 40 —
converting this feature's headline capability into its headline failure mode. Supporting them
safely would require a three-state posture (attached / partial / absent) that the reviewer has
to get right on every symbol.

Whole files, refuse when oversized. The caller drops the 70KB file they were not really
reviewing anyway, which §1.1 says is the right outcome regardless.

### 9.3 Reusing `attestation_contradiction` for §5

Cheaper — no schema change, and the no-severity-floor behavior is already right. Rejected
because it erases the distinction between a caller's asserted harness shape and bytes the
server read from disk, leaving consumers unable to tell how strong a contradiction's evidence
is. That distinction is the entire justification for the category being hard-severity.

### 9.4 Naive Create/Modify disk cross-check

Rejected on measurement: 10 findings, 10 false positives, 9 of them from the legitimate
"earlier task already implemented in the worktree" state. Shipping it would train callers to
ignore the check. See §6.2.

---

## 10. Docs + release sequence

1. **`docs/protocol/controller.md`** — the "~$0.01–$0.02" per-call figure is already stale for
   a 170KB plan and off by ~50× with attachments. Replace with real numbers, and document
   `context_paths` as opt-in and expensive, with the "attach only what the plan touches"
   guidance. Currently 8,340 of the CI-enforced 16,000-byte budget; ample headroom.
2. **`docs/protocol/core.md`** — document `contradicted_codebase_claim` alongside
   `unverifiable_codebase_claim`. Currently 13,023 bytes; **~3 KB of headroom**, so this
   addition must be tight, and the byte check is part of the acceptance criteria.
3. **Resync the plugin bundle in the same commit** — CI enforces
   `plugin/anti-tangent-protocol/protocol/` being identical to `docs/protocol/`:
   ```bash
   rm -f plugin/anti-tangent-protocol/protocol/*.md
   cp docs/protocol/*.md plugin/anti-tangent-protocol/protocol/
   ```
4. **README** — the two new env vars, the reused `ANTI_TANGENT_PLAN_ROOTS`, and §8.3's
   non-Anthropic caveat.
5. **`CHANGELOG.md`** — a `## [0.17.0] - 2026-09-01` entry, written as the code is written,
   not at the end. Branch name and entry must agree or CI fails.
6. **`VERSION` is not touched on the branch.** The release workflow bumps it; pre-bumping
   causes the release workflow's changelog validation to fail.

Section numbers in the protocol parts (§1, §3.x, §4.x, §5.x, §6) are cited externally and must
not be renumbered.

---

## 11. Testing (`go test -race`, no network)

Unit tests only; no test reaches the network. Provider-shaped tests use `httptest.Server`.

**Resolution and caps** (`internal/mcpsrv`)
- per-file cap breach → too-large envelope naming the path and the env var
- set cap breach → too-large envelope naming the running total and the env var
- 51-entry `context_paths` → refused before any read (assert no file was opened)
- missing / non-regular / relative path → transport error, not an envelope
- a path outside `ANTI_TANGENT_PLAN_ROOTS` → refused, including via a symlink that resolves out
- duplicate paths de-duplicated after symlink resolution, counted once against the set cap
- `repo_root` that is a file, not a directory → transport error

**Rendering** (`internal/prompts`)
- golden: no attachment → byte-identical to the current golden files (regression guard for §4.4)
- golden: two attachments → delimiters, byte counts, short hashes, enumerated paths in rules
- an attached file containing a line reading `## What to evaluate` does not shrink the
  cacheable prefix (`planSuffixIndex` still finds the real heading) — §4.1's invariant
- an attached file containing backticks and a fence renders intact (§4.2)

**Verdict semantics** (`internal/mcpsrv`, `internal/verdict`)
- `contradicted_codebase_claim` parses, is accepted by `validCategory`, and is **not** floored
  to minor by `applySeverityFloor`
- a task-level `contradicted_codebase_claim` is not absorbed into the
  `codebase_reference_checklist` rollup
- a plan whose only findings are one minor `unverifiable_codebase_claim` plus one
  `contradicted_codebase_claim` is **not** force-passed

**Create/Modify check** (`internal/planparser`, `internal/mcpsrv`)
- order tier: Task 3 modifies what Task 7 creates → one finding
- order tier: Task 7 modifies what Task 3 creates → no finding
- disk tier: `Modify:` target absent from `repo_root` and created by no earlier task → finding
- disk tier: the already-implemented-worktree case (`Create:` target that exists on disk)
  produces **no** finding — the explicit §6.2 regression guard
- no `repo_root` → disk tier silently skipped, order tier still runs
- a task with no `**Files:**` section contributes nothing
- a `Modify:` bullet escaping `repo_root` via `..` is ignored, not stat'd

**Caching**
- two calls whose plans are identical but whose attached file *content* differs produce
  different `planPassCacheKey` values (§7.2)
- the chunked path's `CachePrefix` contains the attachment block

---

## 12. Acceptance ("done")

- `go build ./...` and `go test -race ./...` pass.
- A `validate_plan` call with no `context_paths` produces byte-identical prompts and golden
  files to 0.16.0.
- A call with `context_paths` renders the enumerated attachment block, and the reviewer's
  ground rules scope `unverifiable_codebase_claim` to unattached code.
- A demonstrably false plan claim about an attached file returns
  `contradicted_codebase_claim`, at the reviewer's severity, neither rolled up nor force-passed.
- Each cap refuses in its documented shape (envelope vs. transport error) with an actionable
  message.
- The order-aware check reports zero findings on a consistent plan and catches both tiers'
  contradictions on a seeded inconsistent one.
- `docs/protocol/` edits keep every part under 16,000 bytes, `INTEGRATION.md` under 2,000, and
  `plugin/anti-tangent-protocol/protocol/` is byte-identical to `docs/protocol/` in the same
  commit.
- `CHANGELOG.md` has a `## [0.17.0]` entry matching the branch name; `VERSION` is unmodified.
