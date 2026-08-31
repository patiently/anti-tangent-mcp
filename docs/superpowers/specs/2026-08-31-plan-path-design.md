# File-path inputs for `validate_plan` / `validate_completion` + reviewer-side prompt caching — design

**Status:** approved design (brainstorming output), pre-implementation
**Date:** 2026-08-31
**Issue:** [#60](https://github.com/patiently/anti-tangent-mcp/issues/60)
**Release vehicle:** `version/0.16.0` (backward-compatible minor; current released is 0.15.0) with a `## [0.16.0]` CHANGELOG entry. The `plan_text` removal lands later in `1.0.0`.

---

## 1. Summary

`validate_plan` is structurally unsubmittable for large plans. `plan_text` is a required
literal string, so the calling agent must **emit** the entire plan markdown inside its own
tool-call arguments. Once the plan exceeds the caller's max-output-tokens setting, the call
fails before it ever reaches the server:

```
API Error: Claude's response exceeded the 64000 output token maximum.
```

This design adds `plan_path`, makes it the documented input, deprecates `plan_text`, extends
the same remedy to `validate_completion`'s evidence inputs, and adds reviewer-side prompt
caching so the chunked path stops paying full price for the same plan prefix on every call.

It deliberately does **not** attach the source files a plan references. That is a larger change
to what the reviewer is, and it is deferred to 0.17.0 for the measured reasons in §9.1.

### 1.1 Why

Measured against v0.15.0, on the real plan that triggered the report:

| property | value |
|---|---|
| size | **170,158 bytes** (1,513 lines) |
| `planparser.SplitTasks` | **9 tasks**, 8/9 with structured headers |
| JSON-escaped tool argument | 173,559 bytes ≈ **51K output tokens** |
| `MaxPayloadBytes` (default 204800) | **not exceeded** |

The server would have accepted this plan. The only binding constraint is the caller's
output-token ceiling, and ~51K of argument plus the tool-call wrapper, a thinking block, and
surrounding text lands at or through 64K. No client-side workaround exists: chunking,
retrying, and subagent delegation all still have to materialize the argument in a single model
response.

The cost is paid **per round**. Every re-validation after a plan edit re-emits the full 51K,
which is what makes an iterative plan-handoff gate untenable rather than merely expensive.

Two things that are *not* the cause, recorded so they don't get "fixed":

- **Not the heading level.** The field report attributed it to `` no `### Task N:` headings
  detected ``. That error came from an *abbreviated* plan the agent synthesized after the full
  submission failed — not from the real document. `taskHeadingRe` has accepted
  `` #{2,4} Task \d+: `` since 52cd262 (shipped in v0.15.0).
- **Not `MaxPayloadBytes`.** 170KB is under the 204800 default. Raising it fixes nothing.

### 1.2 Goals

- A caller can submit a plan of any practical size without emitting its content.
- The reviewer reads the plan **from disk**, so it cannot be handed a summarized or paraphrased
  substitute that differs from what implementing subagents will receive.
- The chunked path stops re-billing the full plan on every reviewer call.
- The controller can demonstrate *which* document cleared the gate.
- Operators who do not want server-side file reads can restrict or refuse them.

### 1.3 Non-goals

- **File attachment via provider Files APIs.** See §8.1 — it does not reduce input tokens, and it
  would force a per-provider upload abstraction plus file lifecycle management across three
  vendors to buy an HTTP-bandwidth saving on a 170KB payload.
- **Path inputs for the remaining five tools.** `validate_completion` is in scope (§2.1)
  because it shares the resolver and the failure mode. `check_progress`'s `changed_files` is
  the next candidate but is not covered here.
- **Attaching source files the plan references** so the reviewer can see the codebase. Deferred
  to 0.17.0 with its own design; §9.1 records the measurements and the reason.
- **A `project_knowledge_path`.** KB context is small in practice; YAGNI until it isn't.
- **Reducing what the chunked path *sends*.** Each chunk prompt deliberately carries the whole
  plan so the reviewer can reason about cross-task dependencies and infer `exit_contracts`.
  §7 makes that cheap; it does not make it smaller.

---

## 2. Input contracts

`ValidatePlanArgs` gains one field; `plan_text` loses its `required` marker:

```go
type ValidatePlanArgs struct {
    PlanText string `json:"plan_text,omitempty"`   // was jsonschema:"required"; deprecated
    PlanPath string `json:"plan_path,omitempty"`   // new — absolute path
    ...
}
```

Exactly one must be set:

| input | behavior |
|---|---|
| neither | transport error `plan_text or plan_path is required` |
| both | transport error `plan_text and plan_path are mutually exclusive` |
| `plan_path` | resolved per §3, then the existing pipeline runs unchanged |
| `plan_text` | works, plus one `minor` / `other` plan finding (criterion `input`) pointing at `plan_path` |

Both errors are transport errors, matching the existing `plan_text is required` convention.

**Absolute paths only.** A relative path would resolve against the server's working directory,
which for a host-spawned stdio server is not reliably the project root — and with git worktrees
it is routinely a *different checkout*, so a relative path could silently review the wrong copy
of the plan. Rejecting outright beats reviewing the wrong document.

`plan_text` remains fully functional in 0.16.0 for plans not on disk (composed in-conversation,
or a host whose filesystem the server cannot see). It is removed in 1.0.0 (§9).

### 2.1 `validate_completion` path inputs

`validate_completion` has the identical structural flaw: `final_diff` and
`final_files[].content` are literal strings the caller must emit, so a large diff hits the same
output-token ceiling. It reuses the §3 resolver, so the marginal cost of including it here is
small and the argument for it is the same.

**`final_files` needs no new field.** `FileArg` is already `{Path, Content}`, and `Content` is
not marked required. Omitting it becomes the instruction to read `Path` from disk:

```go
type FileArg struct {
    Path    string `json:"path"`
    Content string `json:"content,omitempty"` // omitted -> server reads Path
}
```

| `path` | `content` | behavior |
|---|---|---|
| set | set | today's behavior, byte-for-byte unchanged |
| set | omitted | server reads the file per §3 |
| empty | either | existing empty-path rejection, unchanged |

This is backward compatible and adds no schema surface. `totalBytes(files)` sums resolved
content, so the existing cumulative payload check keeps working unchanged.

**`final_diff` gains `final_diff_path`**, mutually exclusive with `final_diff` on the same
terms as §2. A caller runs `git diff > /tmp/x.diff` for effectively nothing rather than
emitting 50K tokens of diff.

**Cap:** completion inputs keep the shared `MaxPayloadBytes` (200KB). They did **not** gain the
headroom `validate_plan` did — §4's reasoning applies only to plans — and `PlanMaxPayloadBytes`
must not leak into this path.

**Evidence-shape guard interaction, important:** `checkEvidenceShape` scans `final_diff` and
every `final_files[].content` for truncation markers (`(truncated)`, `// snip`, bare `...`
lines). It must run **after** resolution, on the resolved content — otherwise a caller could
bypass the guard entirely by passing a path to a file containing elided content. This is the
one place where path inputs could silently weaken an existing check, so it gets its own test.

---

## 3. Path resolution and safety

New `internal/mcpsrv/file_source.go`. One resolver, shared by both tools, called before any
existing stage in each handler and returning a string plus its provenance.

For `ValidatePlan` this means **every downstream stage is untouched** — the payload cap,
`planparser.SplitTasks`, `adaptivePlanMaxTokens`, `renderPlanReview`, and `planPassCacheKey`
all keep operating on text. For `ValidateCompletion` the same holds for `totalBytes`,
`checkEvidenceShape` (§2.1), and the prompt render.

The file is named `file_source.go` rather than `plan_source.go` precisely because
`validate_completion` shares it; a plan-specific name would invite a second, divergent copy the
first time someone adds paths to `check_progress`.

Order of checks:

1. Reject relative paths.
2. `filepath.EvalSymlinks`.
3. Check the resolved path against `PlanRoots`, if configured.
4. `os.Stat` for a regular file (reject directories, devices, sockets).
5. Read at most `PlanMaxPayloadBytes+1` bytes.

Symlink resolution happens **before** the roots check so a symlink cannot hop outside an
allowlisted root. Reading one byte past the cap means an accidentally-pointed-at multi-gigabyte
file is rejected without being loaded into memory.

Failure mapping:

| condition | result |
|---|---|
| relative path | transport error naming the path |
| missing / unreadable / not a regular file | transport error |
| outside every configured root | transport error naming the roots |
| content over the cap | `tooLargePlanResult` envelope (§4) |

### 3.1 `ANTI_TANGENT_PLAN_ROOTS`

Absolute paths joined with the OS path-list separator (`filepath.SplitList`: `:` on Unix, `;` on
Windows). **Empty (the default) means unrestricted.** Prefix matching is
on cleaned, symlink-resolved paths with a separator boundary, so `/home/foo` does not authorize
`/home/foobar`.

### 3.2 Trust model

Reads are unrestricted by default. The justification is that anti-tangent-mcp is **stdio-only**:
the host spawns it as a child process, so it shares the caller's container, mounts, and uid —
and the calling agent already holds unrestricted file read. The server therefore acquires no
capability the caller lacks.

This is stated rather than assumed because it is the honest cost of §8's reversal. A
prompt-injected agent could point `plan_path` at a private key and the server would ship it to
a third-party reviewer API. The same agent could already `Read` that file and paste it into
`plan_text` — but `plan_path` is a *shorter and less visible* route to the same outcome.
`ANTI_TANGENT_PLAN_ROOTS` exists for operators who do not accept the equivalence argument. The
README states this explicitly rather than leaving it implicit.

---

## 4. Payload cap split

`Config` gains `PlanMaxPayloadBytes` (default `1048576`), from
`ANTI_TANGENT_PLAN_MAX_PAYLOAD_BYTES`. It replaces `MaxPayloadBytes` in `ValidatePlan`'s cap
check **only**; the other six tools keep the shared 200KB.

Rationale: the 64K client ceiling was masking the payload cap. `MaxPayloadBytes` never fires on
plans today because no client can emit that much. Once paths land, a 250KB plan becomes
submittable and the existing cap would start rejecting real work immediately. `validate_plan`
just gained roughly 10x headroom; `validate_completion` did not, and its evidence inputs are
still protected by the client ceiling.

1MB ≈ 270K tokens, which the chunked path sends on every call — beyond a 200K-token reviewer.
So the cap remains a real guard, not a formality.

The existing `tooLargePlanResult` envelope is reused: it already reports the plan and
`project_knowledge` byte counts separately, which is exactly the diagnostic a caller needs.

One wording change is required. Its evidence string is currently

```go
fmt.Sprintf("payload %d bytes > cap %d (plan_text: %d, project_knowledge: %d)", ...)
```

The `plan_text:` label is wrong when the content came from `plan_path` — it would name an
argument the caller never passed. The label becomes `plan:`, which is accurate for both inputs.
`NextAction` changes from "Reduce plan_text size and retry." to "Reduce plan size and retry."
for the same reason. Both are covered by existing tests that assert on the evidence string
(`handlers_plan_test.go` asserts `Contains(evidence, "plan_text:")`), so those assertions move
with the label.

---

## 5. Provenance

`formatPlanSummary`'s three scalar parameters become a struct:

```go
type planSummaryMeta struct {
    ModelUsed string
    ReviewMS  int64
    Source    string // "" when plan_text was used
}
```

threaded through `finalizePlanResult` and `planEnvelopeResultFinalized`. This follows the
pattern already documented on `renderPlanReviewInputs` — *"carrying these on a struct keeps the
helper signature narrow (1 arg vs. 5) and matches CodeScene's max arguments = 4 threshold."*

One line lands in `SummaryBlock`, after `plan_run_id`, only when `plan_path` was used:

```
  source:        /abs/path/plan.md (170158 B, sha256 4f2a9c1e…)
```

No `verdict.PlanResult` field, so `plan_schema.json` and every existing consumer are untouched.

**Cache interaction.** `planPassCacheKey` hashes *content*, so two different paths holding
identical plans share a cache entry. Provenance is therefore passed into `planPassCache.lookup`
and rendered from the **current** call — never stored in the entry — otherwise a cache hit
would echo an earlier caller's path. This is the one non-obvious consequence of the feature.

---

## 6. Deprecating `plan_text`

0.16.0 accepts `plan_text` and prepends one finding:

```go
verdict.Finding{
    Severity:   verdict.SeverityMinor,
    Category:   verdict.CategoryOther,
    Criterion:  "input",
    Evidence:   "plan_text was supplied; it is deprecated and will be removed in 1.0.0",
    Suggestion: "pass plan_path with the absolute path to the plan file instead",
}
```

`minor` so it never changes a verdict — the server is advisory, and a deprecation is not a plan
defect. It is prepended the same way `prependPlanClamp` prepends clamp findings, so it survives
every early-exit path.

---

## 7. Reviewer-side prompt caching

The chunked path issues one findings-only call plus one call per chunk, each carrying the full
plan. For the 9-task plan above that is 3 calls at ~47K input tokens.

`plan_findings_only.tmpl` and `plan_tasks_chunk.tmpl` are **byte-identical through line 26** —
reviewer ground rules, optional project knowledge, `## Plan under review`, and `{{.PlanText}}` —
and diverge at line 27 on `## What to evaluate`. That is already a textbook stable-prefix /
varying-suffix boundary; no template restructuring is required.

Changes:

- `prompts.Output` splits `User` into `UserPrefix` (through the plan) and `UserSuffix` (the
  per-call instructions), at the existing boundary. On the single-call path and for every
  non-plan tool, `UserPrefix` is empty and `UserSuffix` carries the whole prompt, so those
  renderers are unaffected.
- `providers.Request` gains `CachePrefix string`. When set, the Anthropic client emits two user
  content blocks with `cache_control: {"type": "ephemeral"}` on the first, instead of today's
  single string. The OpenAI and Google clients concatenate prefix + suffix and behave exactly
  as now.
- `providers.Response` gains `CacheReadInputTokens` and `CacheCreationInputTokens` so hit rate
  is observable rather than assumed.

Expected effect for a 3-call round over a ~47K-token shared head:

| | input tokens billed |
|---|---|
| today | 3 × 47K = **141K** |
| with breakpoint | 58.7K write (1.25×) + 2 × 4.7K read (0.1×) = **68K** |

Roughly a **52% reduction** per chunked round.

**Only on the chunked path.** A plan at or below `PlanTasksPerChunk` renders to
`rendered.Single` — one call, where a breakpoint is a 1.25× write premium against zero reads, a
pure surcharge. `CachePrefix` stays empty there.

The malformed-JSON retry appends `RetryHint()` to the **suffix**, so a retry reads the cached
prefix instead of invalidating it.

### 7.1 The split must be reflected in `planPassCacheKey`

`renderedPlanReview.cachePrompts()` currently returns `planCachePrompt{System, User}` for each
rendered prompt, and `planPassCacheKey` hashes that alongside the plan text, mode, model, and
token budget. Splitting `Output.User` without updating `cachePrompts()` would leave the key
covering only whichever half stayed in `User`.

The practical risk is narrower than it first looks — `planPassCacheKey` already hashes
`PlanText` as its own field, so two different plans cannot collide regardless. But the
*purpose* of hashing the rendered prompts is to invalidate the cache when a template changes,
and a half-covered key would silently stop doing that: an edit to a template's per-call
instructions (everything after line 27, which is where all the per-call divergence lives) would
no longer invalidate entries, and a passing result rendered by the old template could be served
for up to 3 minutes after the change.

So `planCachePrompt` gains the split explicitly:

```go
type planCachePrompt struct {
    System     string `json:"system"`
    UserPrefix string `json:"user_prefix"`
    UserSuffix string `json:"user_suffix"`
}
```

This changes the hash input, so `planPassCacheVersion` bumps to `plan-pass-cache-v2` to drop
pre-upgrade entries rather than reinterpret them.

---

## 8. Rejected alternative reversal: `plan_text_from_file`

`docs/superpowers/specs/2026-05-14-review-noise-and-evidence-context-design.md` §Out of scope
ruled this out:

> `plan_text_from_file`. MCP hosts vary in filesystem access; direct server-side file reads
> would change the deployment/security model.

**This design overturns that call.** Both premises are addressed:

- *"MCP hosts vary in filesystem access."* The server is stdio-only, so the host spawns it as a
  child process — same container, same mounts, same uid. The variance is real only for a
  transport that does not exist. If an HTTP transport is ever added, `plan_path` must be
  refused on it; that is a hard requirement on any such future work.
- *"Would change the deployment/security model."* It does. §3.2 states the change explicitly
  and `ANTI_TANGENT_PLAN_ROOTS` gives operators a lever. The change is accepted rather than
  denied.

What is new since May: the tool is **structurally unusable** on exactly the plans it is most
valuable for. That was not known when the original call was made.

That spec gets a pointer on the line so the record is not silently contradicted.

### 8.1 Provider file attachment — rejected

Uploading the plan via the Anthropic Files API and referencing it as
`{"type":"document","source":{"type":"file","file_id":…}}` does **not** reduce input token
cost. The Files API is a transport and storage convenience — its documented purpose is
uploading a file "across multiple requests without re-uploading". The reviewer still reads the
plan, and everything the model reads is billed as input tokens.

It would also require a per-provider upload abstraction across three vendors with unrelated
file APIs, plus lifecycle management to avoid orphaning uploads on their servers — all to save
HTTP bandwidth on a 170KB payload. §7 achieves the actual goal (the plan not being re-billed on
every call) through the mechanism that genuinely does it.

---

## 9. Docs + release sequence

**0.16.0:**

- **`docs/protocol/controller.md`** §5.1 currently reads *"call `validate_plan` once with the
  full plan markdown"* — the instruction that produced this failure. It becomes
  prefer-`plan_path`-when-the-plan-is-on-disk. Resync `plugin/anti-tangent-protocol/protocol/`
  in the same commit and re-check the 16,000-byte-per-part CI limit.
- **`2026-05-14-…-design.md`**: pointer on the `plan_text_from_file` line to §8 here.
- **`2026-05-07-anti-tangent-mcp-design.md`**: note in Architecture that `validate_plan` reads
  from disk when given `plan_path`.
- **README**: `ANTI_TANGENT_PLAN_ROOTS` and `ANTI_TANGENT_PLAN_MAX_PAYLOAD_BYTES` in the dotenv
  block; the §3.2 trust model as prose.
- **CHANGELOG**: `## [0.16.0]` with `### Added` (`plan_path`, the two env vars, caching) and
  `### Deprecated` (`plan_text`).

**1.0.0** (separate release): remove `plan_text`, make `plan_path` required, drop the
deprecation finding. Not designed here beyond the commitment.

`docs/protocol/implementer.md` also changes, since `validate_completion` is implementer-facing:
prefer omitting `content` and prefer `final_diff_path`.

### 9.1 Why source-file attachment is deferred to 0.17.0

Attaching the files a plan references is the natural next step — it would attack the
`unverifiable_codebase_claim` noise that issues #8, #10, and #16 have all tried to tune. It is
deferred because it is a different kind of change, and the measurements say so.

Measured on the plan from §1.1, whose `json:metadata` fences carry machine-readable
`"files": [...]` arrays (so extraction would be deterministic, not heuristic):

| | |
|---|---|
| unique referenced paths | 31 (24 exist; 7 are to-be-created) |
| total bytes of the 24 | **402,740 ≈ 100,685 tokens** |
| the plan itself | ~46K tokens |
| per reviewer call | ~147K tokens — **the code is 2.2× the plan** |

Three findings that shape the deferred design:

1. **Cost.** Three chunked calls at ~147K tokens is ~441K input tokens per round: roughly
   **$0.55 → $1.46 per round** uncached at `PlanModel`'s default rate, or ~$0.30 → ~$0.70 with
   §7's caching. `docs/protocol/controller.md` sells this gate at "~$0.01–$0.02" per call — a
   claim already stale for a 170KB plan and off by ~50× with attachments. Attachment must be
   opt-in with its own byte cap, never automatic.

2. **Whole-file attachment spends its budget badly.** The three largest referenced files —
   `Configuration.kt` (70KB), `DatabaseModule.kt` (57KB), `SmsFacade.kt` (47KB) — are 43% of
   the corpus and are barely touched by the plan. Selection is therefore
   **caller-supplied `context_paths`**, not server-side extraction from the plan: the caller
   knows what is relevant, and explicit paths need no repo-root inference to resolve the
   plan's repo-relative entries against.

3. **Partial visibility is the real hazard, and it is a prompt problem, not a plumbing one.**
   Every plan template opens with *"You have access ONLY to the plan markdown… Treat such
   identifiers as black-box references"* and then mandates `unverifiable_codebase_claim`. With
   some files attached that instruction is wrong for those and right for the rest, and a
   reviewer holding 24 files will reason from absence about everything else. The ground rules
   must enumerate exactly what is attached and restate that all else stays black-box. That is
   a rewrite of the tool's core review posture and needs its own dogfooding.

A related deterministic check was evaluated and **found not to pay for itself yet**:
cross-checking `Create:` / `Modify:` bullets against disk. Naively it reports 10 contradictions
on this plan, all false positives — 9 because an earlier task was already implemented in the
worktree, 1 because a later task modifies what an earlier task creates. Made order-aware (a
`Modify` target must exist on disk *or* be created by a lower-numbered task) it reports **0**.
Worth building as a cheap guard in 0.17.0; worthless in its naive form.

---

## 10. Testing (`go test -race`, no network)

Table-driven, in `internal/mcpsrv/file_source_test.go`:

- relative path rejected; missing file; directory; non-regular file
- symlink escaping a configured root rejected; symlink *into* a root accepted
- `/home/foo` root does not authorize `/home/foobar`
- file over `PlanMaxPayloadBytes` → `tooLargePlanResult`, not a slurp
- both args set; neither arg set
- roots unset → any absolute path accepted

Handler-level:

- `plan_path` and an equivalent `plan_text` produce identical `PlanResult` values apart from
  the source line and the deprecation finding
- `plan_text` emits exactly one `minor` / `other` finding with criterion `input`, and never
  changes the verdict
- summary golden covering the `source:` line
- a template edit confined to the per-call suffix changes the cache key (guards §7.1)

`validate_completion` (§2.1):

- `final_files` entry with `content` omitted is read from disk; with `content` set, byte-for-byte
  today's behavior
- `final_diff_path` and `final_diff` are mutually exclusive
- resolved content over the shared `MaxPayloadBytes` is rejected — `PlanMaxPayloadBytes` does
  not apply on this path
- **`checkEvidenceShape` runs on resolved content**: a file whose contents contain `// snip` or
  a bare `...` line is rejected exactly as inline evidence would be. This is the regression that
  matters most; without it, path inputs become a bypass for the truncation guard.

Caching (`internal/providers`, `httptest`):

- chunked render sets `CachePrefix`; single render leaves it empty
- Anthropic client emits two content blocks with `cache_control` on the first when
  `CachePrefix` is set, one block when it is not
- OpenAI and Google clients produce byte-identical requests to today
- e2e (`-tags=e2e`): second chunk call reports `cache_read_input_tokens > 0`

---

## 11. Acceptance ("done")

- A 170KB plan validates via `plan_path` with the caller emitting ~60 tokens of argument.
- `plan_text` still works and reports its own deprecation without changing any verdict.
- With `ANTI_TANGENT_PLAN_ROOTS` set, a path outside every root is refused; unset, any absolute
  path is read.
- A plan over `PlanMaxPayloadBytes` returns the too-large envelope without being read into
  memory.
- The summary block names the resolved path, byte count, and hash — including on a cache hit,
  where it names the *current* call's path.
- A chunked round reports `cache_read_input_tokens > 0` on calls after the first; a single-call
  round writes no cache entry.
- `validate_completion` accepts `final_files` entries without `content` and a `final_diff_path`,
  and `checkEvidenceShape` still rejects truncated evidence when it arrives via a path.
- `go test -race ./...` passes; protocol parts stay under 16,000 bytes and
  `plugin/anti-tangent-protocol/protocol/` is byte-identical to `docs/protocol/`.
