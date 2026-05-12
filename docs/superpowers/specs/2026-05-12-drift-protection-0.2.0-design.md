# 0.2.0 drift-protection fixes — design

**Status:** approved 2026-05-12
**Target version:** 0.2.0
**Tracking issue:** [#6](https://github.com/patiently/anti-tangent-mcp/issues/6)
**Branch:** `version/0.2.0`

## Background

Field execution of a 10-task subagent-driven plan against `anti-tangent-mcp` 0.1.4 surfaced four classes of issue across ~30 MCP calls. Two are bugs in code we shipped; two are reviewer-prompt and ergonomics problems that surfaced as friction during real use. Issue #6 captures the report (anonymized). This spec proposes the 0.2.0 fix set.

The reviewer model in `anti-tangent-mcp` is intentionally a different provider than the implementer. That property amplifies the cost of any "the reviewer over-fires on shape" bug: the implementer can't simply re-prompt around it. So the prompt-rewrite work in §B below is high-leverage even though the code change is small.

## Scope

In scope for 0.2.0 (all four buckets from #6):

- **A.** Chunker identity reconciliation regression (introduced 0.1.4).
- **B.** `validate_completion` reviewer prompt rewrite: evidence-shape tolerance, `Context:`-as-authoritative, bias toward `pass` with quality findings.
- **B (schema).** New optional `final_diff` field on `validate_completion`.
- **C.** `model_override` UX: allowlist enumeration in errors; default request timeout 120s → 180s with the configured value surfaced in timeout errors; truncation detection with a structured finding.
- **D.** Session TTL surfaced in the envelope; `payload_too_large` error gains a diff suggestion.

Out of scope for 0.2.0:

- No new providers or models beyond what's already in the allowlist.
- No persistent session store (`What This Repo Is Not`).
- No automatic prompt-rewriting of inbound task specs.
- No `validate_plan` schema change (we keep returning the same `PlanResult` shape — only the chunker's internal title-comparison logic and the chunker prompt change).

## Bump rationale

Minor (`0.1.4` → `0.2.0`). The minor bump signals the breaking change on `validate_completion`: callers that today send summary-only requests now receive a hard error rejecting the request (`validate_completion: at least one of final_files, final_diff, or test_evidence must be non-empty`). Per Keep a Changelog and pre-1.0 semver, minor is the right level for this kind of API tightening; patch is reserved for additive / bugfix-only releases.

Other changes in this release would have been patch-compatible on their own:

- New optional request field (`final_diff`).
- New optional envelope fields (`session_expires_at`, `session_ttl_remaining_seconds`).
- Allowlist error messages are more informative (the validation rule itself is unchanged).
- Default timeout bump (120s → 180s) is widening, not narrowing.
- Reviewer-prompt rewrites are more permissive on what counts as gradable evidence, while keeping coverage of every AC.

The merge commit into `main` must carry `[minor]` to drive the same bump in the release workflow (per `CLAUDE.md`'s branch convention; the branch name and merge bump must agree).

## A. Chunker identity reconciliation

### Problem

`validateChunkIdentity` in `internal/mcpsrv/handlers.go` requires byte-exact match (after `TrimSpace`) between the reviewer's returned `task_title` and the parsed input heading (which has the form `Task N: Title`). OpenAI strips the `Task N:` prefix when echoing, failing reconciliation. The 0.1.4 chunker added this validation; the bug is the first non-trivial use surfaced it.

### Fix

**Code (`internal/mcpsrv/handlers.go`):**

Add a package-level regex and normalize both sides before comparison:

```go
var taskPrefixRe = regexp.MustCompile(`^Task \d+:\s*`)

func normalizeTaskTitle(s string) string {
    return taskPrefixRe.ReplaceAllString(strings.TrimSpace(s), "")
}
```

`validateChunkIdentity` normalizes both `got` and `want` via `normalizeTaskTitle` before comparison. The duplicate-check map uses the normalized form. The mismatch error message keeps the *original* (un-normalized) strings so debugging stays clear when the reviewer emits something neither side recognizes.

**Prompt (`internal/prompts/templates/plan_tasks_chunk.tmpl`):**

In the closing instruction (currently: *"with `task_title` matching the heading text verbatim"*), change to:

> *"`task_title` must be the heading text verbatim, **including the `Task N:` prefix**."*

Belt + suspenders. If the reviewer obeys, the normalizer is a no-op. If not, the normalizer catches it.

### Tests

- `validateChunkIdentity_PrefixStripped`: feed a `TasksOnly` whose tasks have prefix-stripped titles; assert no error.
- `validateChunkIdentity_WrongTitle`: feed a `TasksOnly` whose titles don't match (even after normalization); assert the existing mismatch error fires and the error message includes both original strings.
- `validateChunkIdentity_AllowsLegitimateDuplicateNormalizedTitles`: when the chunk legitimately contains multiple tasks that normalize to the same string (e.g. `Task 1: Add tests` and `Task 2: Add tests`), per-position identity matching plus an expected-count tolerance on the dedupe map keep the chunk valid. Reviewer-side over-duplication (returning the same normalized title more times than expected) is still caught by the per-position mismatch error.
- Golden file regen for `plan_tasks_chunk.tmpl` (`go test ./internal/prompts/... -update`). Diff reviewed in PR.

## B. `validate_completion` evidence shape

### Problem

Three reviewer-side over-fire patterns from #6:

1. Reviewer demands full file contents in `final_files`; treats missing content as a `missing_acceptance_criterion` finding even when the implementer has cited paths, test names, and test output in the `summary` and `test_evidence` fields.
2. Reviewer treats the `summary` text as the *artifact under review* rather than a description of it — including grading inline "reviewer notes:" annotations as if they were source.
3. Reviewer applies ACs literally even when the `Context:` block of the task spec explicitly disambiguates or anticipates a deviation.

Combined: the reviewer over-fires on payload shape rather than substance, and `Context:` is currently advisory at best.

### Schema fix

**File:** `internal/mcpsrv/handlers.go`.

Add an optional field to the `validate_completion` request struct:

```go
type validateCompletionRequest struct {
    SessionID    string          `json:"session_id"     jsonschema:"required"`
    Summary      string          `json:"summary"        jsonschema:"required"`
    FinalFiles   []FileSnapshot  `json:"final_files,omitempty"`
    FinalDiff    string          `json:"final_diff,omitempty"`  // NEW
    TestEvidence string          `json:"test_evidence,omitempty"`
}
```

Minimum-evidence check: at least one of `final_files`, `final_diff`, or `test_evidence` must be non-empty. **This is a NEW requirement in 0.2.0** — current code (0.1.4) accepts summary-only completions; 0.2.0 rejects them. See "Backward compatibility" immediately below.

Payload cap (200KB) applies to the sum of `final_files` content + `len(final_diff)`. The error string in the over-cap case gains the diff-suggestion language (§D).

The same field is **not** added to `check_progress`. Mid-flight changes are usually small and the current `changed_files` shape handles them; we revisit if real use produces friction.

#### Backward compatibility

Tightening the minimum-evidence check is a behavioral break for any caller that today sends `validate_completion` with `summary` only. Such requests now return a hard error:

```text
validate_completion: at least one of final_files, final_diff, or test_evidence must be non-empty
```

Intentional: the reviewer prompt below grades against concrete evidence (files / diff / test output). Summary text is the implementer's description, not evidence; treating it as evidence is exactly the over-firing pattern documented in #6 §3. Migration is short — the smallest fix for a summary-only caller is to put the test command output in `test_evidence`.

Bump implication: minor (`0.1.4` → `0.2.0`). The minor bump signals the API break to consumers; the CHANGELOG entry also marks it under `### Changed` with a `(breaking)` prefix.

### Prompt rewrite

**File:** `internal/prompts/templates/post.tmpl`.

Four targeted additions, no overall restructure of the template:

1. **Context-as-authoritative paragraph** — inserted into the `## What to evaluate` section, immediately after the "Walk every acceptance criterion" instruction:

   > *"The `Context:` block in the task spec above is authoritative. If an AC reads one way literally but `Context:` explicitly anticipates or approves a deviation (e.g. a framework constraint, an upstream design decision, or an in-flight refactor), treat `Context:` as the disambiguator. Do not emit a finding solely because an AC's literal phrasing conflicts with a deviation that `Context:` permits."*

2. **Grade-evidence paragraph** — same section, immediately after (1):

   > *"Evidence for completion comes from `final_files` (full file contents), `final_diff` (a unified diff), and `test_evidence` (test command output). The `summary` is the implementer's description of what was done — cross-reference it against the evidence, but **the summary on its own is not evidence**; the request schema requires at least one of the three evidence fields to be non-empty. Grade whatever evidence is provided, in any combination. Do **not** emit a `missing_acceptance_criterion` finding solely because `final_files` is missing when `final_diff` or `test_evidence` already covers the same AC. **Do** emit one if (a) the evidence affirmatively contradicts an AC, (b) the evidence is internally inconsistent with the summary's claims, or (c) an AC is not addressed by any of the provided evidence."*

3. **New `## Final diff` section** — renders between the existing `## Final implementation` and `## Test evidence` sections when `FinalDiff` is non-empty. Same 4-backtick fencing and prompt-injection-safety language as the existing `## Final implementation` block. The template fragment (4-backtick fences match the existing pattern in `post.tmpl`; not reproduced here verbatim to avoid markdown-nesting confusion) renders:

   - A `## Final diff` heading.
   - The same untrusted-content disclaimer paragraph used by `## Final implementation`, adjusted to "unified diff" wording.
   - A 4-backtick fenced block whose body is `{{.FinalDiff}}`.

   Implementation: add a conditional `{{if .FinalDiff}} ... {{end}}` clause in `post.tmpl` mirroring the existing `{{if .TestEvidence}}` block lower in the file.

4. **Bias paragraph** — inserted immediately before the closing `Respond with the verdict JSON only.` line:

   > *"When the provided evidence addresses every AC and the implementer's narrative is internally consistent with it, prefer `verdict: pass` with a `category: quality` finding for nit-level concerns over `verdict: fail`. Reserve `severity: critical` and `severity: major` for evidence that **affirmatively contradicts** an AC, OR for an AC that is left unaddressed by any of the provided evidence. The bias toward `pass` applies only when every AC has been addressed — not when evidence is absent for an AC."*

### Risk: under-firing real defects

The bias paragraph is the highest-stakes change. The 0.2.0 wording is deliberately tight to minimize under-fire:

- The schema-level minimum-evidence check (above) is the first line of defense: a request with no `final_files`, no `final_diff`, and no `test_evidence` is rejected before it ever reaches the reviewer. So the reviewer is never asked to grade a summary-only payload.
- Paragraph (2) lists three explicit emit triggers: affirmative contradiction, internal inconsistency with the summary's claims, **and** an AC unaddressed by any provided evidence. The third trigger means "absence of evidence for an AC" still earns a `missing_acceptance_criterion` finding.
- The bias paragraph applies **only when every AC has been addressed** by the provided evidence. If even one AC is unaddressed, the bias does not apply and the reviewer is expected to emit `severity: major`.
- The reviewer still walks every AC and emits findings; the change is in severity / verdict mapping for nit-level concerns, not in coverage.
- E2E test changes (below) should sanity-check that planted defects still surface as findings.

### Tests

- Unit: golden file regen for `post.tmpl`. Diff reviewed by hand before commit.
- Unit: `validate_completion` request schema validation accepts the new `final_diff`; minimum-evidence check rejects all-empty.
- Unit: prompt rendering when `FinalDiff` is set produces the new `## Final diff` section.
- Unit: prompt rendering when `FinalDiff` is empty produces output bit-identical to the post-rewrite template without the new section (no stray whitespace).
- Integration (`mcpsrv`): one new test case feeding a `final_diff`-only completion through the handler; assert the reviewer sees the diff in the rendered prompt.
- E2E (gated, optional): one new gated test confirming a planted contradiction-of-AC still produces a `major` finding under the rewritten prompt.

## C. `model_override` UX

### Fix 1: allowlist enumeration

**File:** `internal/providers/reviewer.go` — `ValidateModel`.

When the model isn't in the per-provider map, sort the map's keys deterministically and include them in the error string:

```text
model "gpt-4o" not in allowlist for provider "openai" (allowed: gpt-5, gpt-5-mini, gpt-5-nano, gpt-5.4-mini, gpt-5.4-mini-2026-03-17, gpt-5.5, gpt-5.5-2026-04-23)
```

The unknown-provider error gets a parallel treatment (it already lists providers; just confirm it's still accurate).

Unit test asserts the listed models match the map keys for at least one provider.

### Fix 2: timeout default + surfacing

**File:** `internal/config/config.go`.

Change `RequestTimeout`'s default from `120 * time.Second` to `180 * time.Second`. Env-var override (`ANTI_TANGENT_REQUEST_TIMEOUT`) and validation unchanged.

**Files:** `internal/providers/{anthropic,openai,google}.go`.

Provider structs gain a `timeout time.Duration` field set in constructors. Each provider's outbound HTTP call wraps the `context.DeadlineExceeded` error with the configured timeout:

```go
if errors.Is(err, context.DeadlineExceeded) {
    return Response{}, fmt.Errorf("openai: request timeout %s exceeded (set ANTI_TANGENT_REQUEST_TIMEOUT to raise): %w", p.timeout, err)
}
```

The original error is wrapped (`%w`) so callers can still `errors.Is` against `context.DeadlineExceeded`.

Unit test (per provider, `httptest`-based): server delays past a tight timeout; assert the error contains both the timeout duration and the env-var name, and that `errors.Is(err, context.DeadlineExceeded)` is true.

### Fix 3: truncation detection

**File:** `internal/providers/openai.go` (primary), `anthropic.go`, `google.go`.

Extend each provider response struct to deserialize the finish reason; the existing structs do not yet include it. Add detection:

- OpenAI: `choices[0].finish_reason == "length"`.
- Anthropic: `stop_reason == "max_tokens"`.
- Google: `candidates[0].finishReason == "MAX_TOKENS"`.

When detected, return a sentinel error `ErrResponseTruncated` exported from `internal/providers`:

```go
var ErrResponseTruncated = errors.New("reviewer response truncated at max_tokens limit")
```

**Handler mapping** (`internal/mcpsrv/handlers.go`): every handler that calls a reviewer adds:

```go
if errors.Is(err, providers.ErrResponseTruncated) {
    // surface as a structured finding rather than an opaque error
    return Envelope{
        Verdict: verdict.Warn,
        Findings: []verdict.Finding{{
            Category: verdict.CategoryOther,
            Severity: verdict.SeverityMajor,
            Evidence: "reviewer response truncated at max_tokens limit",
            Suggestion: "raise ANTI_TANGENT_PER_TASK_MAX_TOKENS (per-task hooks) or ANTI_TANGENT_PLAN_MAX_TOKENS (validate_plan) and retry",
        }},
        NextAction: "Retry with a higher max-tokens cap.",
        // session / timing fields unchanged
    }, nil
}
```

For `validate_plan` (which has its own error path through the chunker), the truncation maps onto an equivalent `plan_findings` entry rather than failing the whole call.

Unit tests (one per provider): `httptest` server returns a truncated response with the appropriate finish-reason; assert `ErrResponseTruncated` is returned. Integration test: handler converts `ErrResponseTruncated` into a `verdict: warn` envelope with the suggested-action text.

## D. Small ergonomics

### Session TTL in envelope

**File:** `internal/mcpsrv/handlers.go` — `Envelope` struct.

Add two optional fields:

```go
type Envelope struct {
    // ... existing fields ...
    SessionExpiresAt           *time.Time `json:"session_expires_at,omitempty"`
    SessionTTLRemainingSeconds *int       `json:"session_ttl_remaining_seconds,omitempty"`
}
```

The session store uses sliding idle TTL: each successful `Get`, checkpoint append, or findings update refreshes `LastAccessed`. Compute the expiry surfaced in responses as `sess.LastAccessed.Add(h.deps.Sessions.TTL())` after the handler has performed any operation that refreshes `LastAccessed`. `_remaining_seconds` uses `int(time.Until(expiresAt).Seconds())`, clamped to 0.

Pointer types so they only render in JSON when the session is known. Populated for the three stateful tools (`validate_task_spec`, `check_progress`, `validate_completion`); left nil for `validate_plan`.

Both fields rendered with omitempty so a `validate_plan` envelope is bit-identical to 0.1.4 (modulo any other 0.2.0 additions).

Unit test: envelope serialization with a known session timestamp matches a golden JSON snippet.

### `payload_too_large` error suggestion

**File:** `internal/mcpsrv/handlers.go` — where the 200KB cap is checked.

For `validate_completion`, append (with a leading " — " separator) "try sending a unified diff via final_diff, or splitting the call into smaller chunks".

For `check_progress`, append (same separator) "try sending a smaller changed_files set, or splitting the checkpoint into smaller chunks".

Payload accounting remains `len(path) + len(content)` for file snapshots and additionally includes `len(final_diff)` for `validate_completion`.

Unit test: trigger the cap with a synthetic >200KB payload; assert the error contains the suggestion text.

## Testing summary

| Layer | New / changed |
|---|---|
| Unit (handlers) | chunker prefix normalization (3 cases); minimum-evidence check accepts `final_diff`-only; `payload_too_large` suggestion text |
| Unit (providers) | allowlist error lists models; timeout error includes duration + env-var; `ErrResponseTruncated` per provider |
| Unit (prompts) | `post.tmpl` golden regen; `plan_tasks_chunk.tmpl` golden regen; `## Final diff` rendering on/off |
| Unit (verdict) | envelope JSON with / without `session_*` fields |
| Integration (mcpsrv) | `final_diff`-only completion flows end-to-end; truncation maps to warn-envelope |
| E2E (gated, optional) | planted contradiction-of-AC still earns major under rewritten prompt |

`-race` stays on. Network-free unit tests via `httptest`. Golden files reviewed in PR diff before commit.

## CHANGELOG entries (0.2.0)

```markdown
### Added
- `validate_completion` accepts a new optional `final_diff` field for delta-style evidence when full file contents would exceed the 200KB payload cap or aren't practical to paste.
- `Envelope` adds optional `session_expires_at` (RFC3339) and `session_ttl_remaining_seconds` so implementers can see when a session is approaching the 4h TTL.
- `ValidateModel` errors now enumerate the allowlist for the relevant provider, e.g. `model "gpt-4o" not in allowlist for provider "openai" (allowed: gpt-5, gpt-5-mini, ...)`.
- Reviewer-response truncation (`finish_reason: length` for OpenAI, equivalents for Anthropic/Google) is detected and surfaced as a structured `category: other` finding suggesting an `ANTI_TANGENT_*_MAX_TOKENS` bump, instead of the opaque `decode: EOF` error.

### Changed
- **(breaking)** `validate_completion` now requires at least one of `final_files`, `final_diff`, or `test_evidence` to be non-empty. Summary-only completion requests are rejected with `validate_completion: at least one of final_files, final_diff, or test_evidence must be non-empty`. Migration: include test command output in `test_evidence` (smallest path), a unified diff in `final_diff`, or full files in `final_files`. Rationale: the reviewer prompt rewrite below grades against concrete evidence; summary text alone produced the reviewer over-firing pattern in #6 §3.
- Default `ANTI_TANGENT_REQUEST_TIMEOUT` raised from 120s to 180s; reasoning-heavy models (e.g. `openai:gpt-5`) consistently need more than 120s on dense plan inputs.
- Timeout errors now include the configured value and env-var name, e.g. `openai: request timeout 180s exceeded (set ANTI_TANGENT_REQUEST_TIMEOUT to raise)`.
- `validate_completion` reviewer prompt (`post.tmpl`) rewritten to (a) treat `final_files` / `final_diff` / `test_evidence` as the evidence under review (not the `summary`), (b) treat the task spec's `Context:` block as authoritative when it disambiguates an AC's literal phrasing, (c) bias toward `verdict: pass` with a `category: quality` finding for nit-level concerns when every AC is addressed, reserving `severity: major`/`critical` for affirmative contradictions or for an AC left unaddressed by any evidence.
- `validate_plan` chunker prompt asks the reviewer to echo the `Task N:` prefix verbatim.
- `payload_too_large` errors include tool-specific suggestions: `validate_completion` suggests `final_diff` or split; `check_progress` suggests a smaller `changed_files` set or split.

### Fixed
- `validate_plan` chunker identity reconciliation no longer fails when the reviewer strips the `Task N:` prefix from echoed task titles. Both sides are now normalized via a regex before comparison; the prompt is also tightened. Regression from 0.1.4. (Fixes #6.)
```

## Risks and mitigations

| Risk | Mitigation |
|---|---|
| Bias paragraph in `post.tmpl` under-fires real defects | Paired with internal-consistency check on citations; gated E2E test confirms planted contradiction still earns `major`. |
| Golden-file churn obscures the prompt-rewrite diff in PR | Split golden regen into its own commit so the prompt diff and the golden diff are reviewed separately. |
| `final_diff` plus full `final_files` exceeds 200KB cap | Combined check already applies; suggestion text in the error guides toward one-or-the-other. |
| Timeout bump masks a real provider performance regression | Surfacing the configured timeout in the error means anyone seeing "timeout 180s exceeded" knows to investigate, not silently retry. |
| Truncation finding turns transient response issues into noise | Severity is `major`, not `critical`; suggestion text is specific; only fires on the actual `finish_reason: length` signal, not on generic decode errors. |

## Non-goals (won't fix in 0.2.0)

- Listing the allowed models in the `model_override` tool description directly (the error-side enumeration is sufficient; doc churn isn't worth a release on its own).
- Per-model timeout overrides (one knob is enough until we see real friction).
- Soft-retry on malformed JSON (truncation detection covers the dominant cause; a generic "decode failed, retry once" path adds latency to legitimate parse errors).
- `final_diff` field on `check_progress` (revisit if mid-flight payload-cap friction appears).
- Schema change to `task_title` reconciliation (e.g. `task_index` integer). The prefix-normalization fix is sufficient and avoids any wire-format churn.
