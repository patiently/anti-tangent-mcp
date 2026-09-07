# I/O delegation + completion guard — v0.18.0 design

**Date:** 2026-09-07
**Status:** draft — all decisions reviewed (§12); awaiting spec sign-off
**Ships in:** `version/0.18.0` (minor)

Two independent features share a release because both extend anti-tangent through
Claude Code *plugins* rather than through the server's advisory review loop.

- **A — completion guard.** A `PostToolUse` hook that refuses a silent task close
  when `validate_completion` never ran, or ran and failed.
- **B — I/O delegation.** Two new MCP tools (`bulk_read`, `code_write`) that route
  I/O-heavy implementer work to a cheap worker model, plus the hooks that make the
  implementer actually use them. Adapted from Spotify's `shunt`.

Feature B's origin: [shunt](https://github.com/spotify/portal-ai-plugins/tree/main/plugins/shunt)
(Apache-2.0) and Dimitri Mazmanov, ["Portal by Spotify cut my Claude Code token usage by 90%"](https://engineering.atspotify.com/2026/9/portal-by-spotify-cut-my-claude-code-token-usage-by-90),
Spotify Engineering, 2026-09-03.

## 1. Goals

1. Make `validate_completion` observably enforced at task close, configurably.
2. Keep the file corpus of a large read, and generated boilerplate, out of the
   implementer's context.
3. Add both without weakening the server's advisory contract or its filesystem
   trust model.

## 2. Non-goals

Carried from the origin design, plus two of our own:

- **No delegated edits.** Worker answers carry no reliable line anchors. An edit
  still requires a targeted `Read` by the implementer.
- **No delegated reasoning** — debugging, architecture, safety-critical code.
- **No delegation below the line threshold.** Round-trip latency exceeds the saving.
- **No dependency on Spotify Portal / AiKA.** See §3.4.
- **No change to the existing seven tools or their envelope.**
- **No blocking behaviour in the server.** Every block in this release lives in a
  plugin hook. See §3.1.

## 3. Decisions

Each of these was an open fork during brainstorming; recording the answer with its
reasoning so the plan does not re-litigate them.

### 3.1 Blocking lives in plugins, never in the server

`CLAUDE.md` states a standing non-goal: *"No automatic correction: the server is
advisory, never blocking."* Feature A blocks. The invariant survives because the
block is a **hook** shipped in a plugin the operator installs, and the MCP server
itself gains no blocking behaviour whatsoever. This distinction is load-bearing and
must be stated in `CLAUDE.md` and the README, or the next reader will reasonably
conclude the non-goal was quietly abandoned.

### 3.2 Free-text worker output rides a one-field JSON schema

All three provider clients unconditionally `json.Unmarshal(req.JSONSchema, &schema)`
and force structured output — Anthropic via `tool_choice`, Google via
`responseMimeType: application/json` + `responseSchema`, OpenAI via `response_format`.
There is no text path.

Rather than add one, worker calls pass a single-field schema: `{"answer": string}`
for `bulk_read`, `{"code": string}` for `code_write`.

- **Zero provider changes**, so no new failure modes in code three tools already depend on.
- **Reuses `ErrResponseTruncated`** unchanged.
- **Truncation fails closed**, which is the deciding argument. A response cut at
  `max_tokens` mid-string will not parse, so `code_write` refuses to write rather than
  writing half a file. The plain-text alternative would require reading `stop_reason` /
  `finish_reason` / `finishReason` — spelled differently by all three providers — to get
  the same safety.

Cost: JSON-escaping code is a few percent of token overhead. Accepted.

Note: the Anthropic client hardcodes the forced tool name `submit_review`. Worker calls
inherit it. It is an opaque identifier and works correctly; leaving it keeps the provider
diff at exactly zero. It will read oddly in debug logs, which is acceptable.

### 3.3 Worker model falls back to `MidModel`

`ANTI_TANGENT_WORKER_MODEL` overrides; unset, it resolves to `cfg.MidModel`. This is the
established idiom in `config.go` — `StatsModel` already chains to `MidModel`, and
`PrimeModel`/`ExtractModel` chain to `PlanModel`. `MidModel` defaults to
`claude-haiku-4-5-20251001`, already the cheap tier, so the tools work zero-config on a
cheap model.

The reviewer≠implementer principle does **not** apply to the worker — same-model is fine
here, cheap is the only criterion. Known trade-off: an operator who raised
`ANTI_TANGENT_MID_MODEL` to something expensive makes worker calls expensive too. Documented
in the README, not defended against in code.

### 3.4 shunt is ported, not depended on

Claude Code *does* support inter-plugin dependencies (`plugin.json` has a `dependencies`
array with semver constraints; Claude Code auto-enables them transitively). Depending on
shunt was nonetheless rejected on evidence:

shunt is not a standalone tool. Its README requires the `portal` plugin, the Portal CLI
binary, `/portal:setup` authentication against **a Spotify Portal instance**, and two AiKA
modes (`bulk-reader`, `code-writer`) that must already exist on that instance. It ships
`scripts/bulk-read`, `scripts/code-write` and `scripts/lib/aika.sh`; its hooks redirect into
those, which call Portal CLI, which calls AiKA. Five of its seven environment variables
(`SHUNT_PORTAL_INSTANCE`, `PORTAL_CLI_BIN`, `SHUNT_BULK_READER_MODE_ID`,
`SHUNT_CODE_WRITER_MODE_ID`, `SHUNT_MAX_PAYLOAD_BYTES`) are Portal-shaped. There is no
supported seam to point it at an arbitrary MCP tool.

The contrast with CodeScene is instructive and worth stating, because the two look
superficially alike: CodeScene MCP is standalone — you run it against your own repo with
your own token, and anti-tangent composes with it purely at the protocol level while
shipping none of its code. shunt is a client for Spotify-internal infrastructure.

What we take is small and specific: the two hooks' decision logic, the 350-line threshold,
the pass-through cases, and the eval fixtures. What we discard is `scripts/`, `aika.sh`, and
the Portal env vars. That large discarded fraction is itself the argument against depending
on it.

### 3.5 The guard blocks on absence *and* on failure

Blocking only on absence would miss the sharper failure: an agent runs the gate, reads a
`fail`, and closes the task anyway. The protocol already says *"If the verdict is `fail` or
contains `critical`/`major` findings, do not report DONE."* The guard enforces the sentence
that already exists.

Accepted risk: this couples the hook to `summary_block`'s rendered text. Mitigated by §8.3 —
a Go test pins the exact substrings the hook greps for, so server and hook cannot drift
silently.

### 3.6 `code_write` refuses to overwrite by default

New `overwrite` field, default `false`, implemented by `O_EXCL` — the same `openat` flag set
that closes the symlink-swap window, so it costs nothing extra. The server has never written
to disk before; a cheap worker model driving a file-writing tool should not clobber a
hand-written file by default, especially when the response returns only a line count and the
caller never sees what was replaced.

## 4. Part A — `plugin/anti-tangent-guard/`

```text
plugin/anti-tangent-guard/
├── .claude-plugin/plugin.json
├── README.md
├── hooks/
│   ├── hooks.json          # PostToolUse matcher: TaskUpdate
│   └── check-task-complete
└── evals/
    ├── run.sh
    └── guard-evals.json
```

### 4.1 Trigger and window

`PostToolUse`, matcher `TaskUpdate`. Exits 0 immediately unless
`tool_input.status == "completed"`.

The scan window is the transcript slice from the most recent `TaskUpdate` setting this
`taskId` to `in_progress`, to now — the same technique as superpowers'
`post-task-complete-revalidate.sh`, and for the same reason: an unscoped scan would credit a
*different* task's validation. Falls back to the whole transcript when no `in_progress` is
found (an agent that skipped straight to `completed`).

### 4.2 Pass signals

Passes if **either** appears in the window:

1. A `tool_use` block named `mcp__anti-tangent__validate_completion`. This is the
   `executing-plans` case, where the controller ran the task itself.
2. The literal marker `anti-tangent envelope` together with `session_id:` in a
   `tool_result`. This is the `subagent-driven-development` case: the call happened inside a
   subagent transcript the controller cannot see, but the protocol already requires the
   implementer to *"Copy the `summary_block` field from the response verbatim into your DONE
   report"*, so the marker arrives in the controller's transcript as tool output.

Covering both flows with one hook is the reason `PostToolUse`/`TaskUpdate` was chosen over
`SubagentStop`.

### 4.3 Fail detection

When signal 2 matched, parse `verdict:` from the same summary block. A verdict of `fail`
blocks with a message distinct from the absence message — the remediation differs (fix the
findings vs. run the gate at all).

Where several summary blocks appear in one window (a re-validation after fixes), the **last**
one wins. That is the whole point of re-validating.

### 4.4 Blocking, configuration, failure posture

- Block = `exit 2` with the explanation on **stderr**, verified in this environment as the
  mechanism that returns text to the agent.
- `ANTI_TANGENT_COMPLETION_GUARD=0` disables at runtime. Installing the plugin is the opt-in;
  absent that variable it is active.
- **Fail open, always.** Any unexpected error — unparseable transcript, missing
  `transcript_path`, absent `jq`/`python3` — exits 0. A guard that blocks work because it
  broke is worse than the drift it prevents.
- Trace log at `${ANTI_TANGENT_GUARD_TRACE_LOG:-/tmp/claude-hooks/anti-tangent-guard.log}`,
  one line per decision point.

No lightweight-mode exemption exists and none is needed: lightweight mode explicitly keeps
`validate_completion` as its sanity gate, so the rule is unconditional. Recorded here so the
absence reads as a decision rather than an oversight.

### 4.5 Dependencies

`python3` + `jq`, matching the hook it mirrors. The transcript walk (nested content blocks,
`tool_result` inner arrays, 1-based task-id reconstruction) is impractical in jq alone. This
differs from the shunt hooks, which stay jq-only per upstream; both plugin READMEs state
their own dependency line.

## 5. Part B1 — server tools

New files, deliberately not added to `handlers.go` (already 117KB):

- `internal/mcpsrv/worker_handlers.go` — both handlers
- `internal/mcpsrv/worker_call.go` — shared worker invocation + response parsing
- `internal/mcpsrv/file_target.go` — `resolveWriteTarget`
- `internal/prompts/worker_bulk_read.tmpl`, `worker_code_write.tmpl` + goldens

### 5.1 `bulk_read`

| Field | Type | Notes |
|---|---|---|
| `question` | string | required |
| `paths` | string[] | required, absolute, 1–50 entries |
| `model` | string | optional `provider:model-id` override |
| `max_tokens_override` | int ≥ 0 | existing semantics |

Files are read server-side through the **existing** `resolveFileInput`, so `ANTI_TANGENT_PLAN_ROOTS`
containment, symlink-resolution-before-check, `O_NOFOLLOW`, and control/format-character
refusal all apply unchanged. Each file is rendered as `<file path="…">…</file>`.

The system instruction asks for structured bullets only, each led by an exact name, type, or
line number, and nothing the caller did not ask for. Paraphrased rather than copied from
upstream, so no attribution attaches to the string itself.

Response: `answer`, `model_used`, `review_ms`, `input_tokens`, `output_tokens`, `files_read`,
`bytes_read`.

### 5.2 `code_write`

| Field | Type | Notes |
|---|---|---|
| `spec` | string | required |
| `reference_path` | string | required, absolute — the file whose patterns to match |
| `target_path` | string | optional, absolute; if set, write to disk and return no code |
| `overwrite` | bool | **new**; default false |
| `model` | string | optional override |
| `max_tokens_override` | int ≥ 0 | |

`reference_path` is required and its absence is rejected with a message saying why:
context-free generated code fits nothing. Markdown fences are stripped before writing — only
when the entire body is fence-wrapped, never mid-body.

With `target_path`: returns `written`, `lines_written`, `model_used`, `review_ms`,
`input_tokens`, `output_tokens` — never the code. Without it, `code` replaces
`written`/`lines_written`.

### 5.3 `resolveWriteTarget` — why `resolveFileInput` cannot be reused

`resolveFileInput` calls `EvalSymlinks` on the **full** path, which fails outright when the
file does not exist yet — the normal case for `code_write`. The write path therefore:

1. Rejects empty / non-absolute paths.
2. Splits parent and leaf; `EvalSymlinks` the **parent**, which must already exist.
   `code_write` never creates directories.
3. Runs the existing `rejectControlChars` on `filepath.Join(parentResolved, leaf)`.
4. `withinRoots(parentResolved, roots)`.
5. Opens with `O_WRONLY|O_CREAT|O_NOFOLLOW`, plus `O_EXCL` when `overwrite` is false and
   `O_TRUNC` when it is true.

`O_NOFOLLOW` on the leaf means an existing **symlink** at `target_path` is refused even with
`overwrite: true` — writing through a symlink is exactly how a write escapes the roots
allowlist. As with reads, Windows has no `O_NOFOLLOW` equivalent and the final-component race
is not closed there; the README's existing caveat is extended to cover writes.

`lines_written` counts `\n` plus one for a non-empty final line.

### 5.4 Configuration

Server-side, in `internal/config`:

- `ANTI_TANGENT_WORKER_MODEL` — `provider:model-id`; unset → `cfg.MidModel` (§3.3).
- `ANTI_TANGENT_WORKER_MAX_TOKENS` — default 4096, clamped by `MaxTokensCeiling` exactly as
  `StatsMaxTokens` is.

Worker calls reuse `ANTI_TANGENT_REQUEST_TIMEOUT`, `ANTI_TANGENT_MAX_PAYLOAD_BYTES` and
`ANTI_TANGENT_PLAN_ROOTS` unchanged. Payloads over the cap are refused with the existing
structured too-large envelope, never truncated.

One upstream constraint we explicitly do **not** inherit: shunt caps requests at
`SHUNT_MAX_PAYLOAD_BYTES` (400KB, 120KB on Linux) because its input travels through
`argv` to the Portal CLI and must fit inside `ARG_MAX` or die with `E2BIG`. Our transport
is MCP stdio into a Go process, so that limit has no analogue here and
`ANTI_TANGENT_MAX_PAYLOAD_BYTES` governs for its own reasons. Worth recording, because a
reader comparing the two configurations will otherwise assume the omission is an oversight.

**Not** server config: `ANTI_TANGENT_SHUNT_MIN_LINES` is read only by the hook scripts and
never enters `internal/config`. Worth stating because every other `ANTI_TANGENT_*` variable in
this repo is server configuration, and a reader will assume this one is too.

### 5.5 Stats

Strictly additive. `rollup.json`'s JSON tags are a documented load-bearing contract with the
gnome-topbar consumer, which reads existing keys by exact name.

- `stats.Event` gains `input_tokens` / `output_tokens`, both `omitempty`.
- `stats.Rollup` gains `Worker *WorkerRollup` (`omitempty`) carrying per-tool call counts and
  summed input/output tokens — the basis for "tokens kept out of the implementer's context".

Accepted cosmetic wart: `Event.FindingsTotal` is not `omitempty`, so worker rows serialise a
meaningless `"findings_total":0`. Changing that tag would alter existing rows' shape, which is
not worth it.

## 6. Part B2 — `plugin/anti-tangent-shunt/`

```text
plugin/anti-tangent-shunt/
├── .claude-plugin/plugin.json
├── README.md
├── hooks/
│   ├── hooks.json           # PreToolUse matchers: Read, Bash
│   ├── check-file-size      # adapted from spotify/portal-ai-plugins (Apache-2.0)
│   └── check-bash-read      # adapted from spotify/portal-ai-plugins (Apache-2.0)
├── skills/
│   ├── bulk-reader/SKILL.md
│   └── code-writer/SKILL.md
└── evals/
    ├── run.sh
    ├── hook-evals.json
    └── bash-hook-evals.json
```

Both hooks block with `exit 2` + stderr — the same convention as Part A, so all three new
hooks in this release behave identically when they refuse.

### 6.1 `check-file-size` (PreToolUse → `Read`)

| Condition | Action | Rationale |
|---|---|---|
| `offset` or `limit` set | pass | A targeted read. Load-bearing for the no-delegated-edits non-goal, not a convenience. |
| File does not exist | pass | Let `Read` emit its own error; a hook inventing one is worse. |
| Lines < `ANTI_TANGENT_SHUNT_MIN_LINES` (350) | pass | Round-trip latency exceeds the saving. |
| Otherwise | **block** | stderr names `mcp__anti-tangent__bulk_read` and gives one example invocation. |

### 6.2 `check-bash-read` (PreToolUse → `Bash`)

Blocks `cat` / `head` / `tail` / `less` / `more` reading a whole large file. Passes pipes,
redirections, non-read commands, and `head`/`tail` carrying a line-count flag — the flag *is*
the limit, making those already-targeted reads.

**This hook carries the release's highest risk of user-visible harm.** A false positive on
`check-file-size` costs a redundant delegation; a false positive here blocks a legitimate
shell command. Two mitigations are requirements, not preferences:

- **Any parse ambiguity defaults to allow.**
- **The upstream fixture set is ported rather than reinvented** — those cases encode edge
  conditions we would otherwise discover in production.

Upstream's `evals/run.sh` reports 51 tests. Only some are portable, verified against the
real README:

| Upstream file | Cases | Ported? |
|---|---|---|
| `hook-evals.json` (Read hook) | 17 | **yes** |
| `bash-hook-evals.json` (Bash hook) | 17 | **yes** |
| `transport-evals.sh` (`aika.sh` vs a stubbed CLI) | 17 | no — Portal transport we do not have |
| `evals.json` (end-to-end skill) | 3 | no — requires Portal auth |
| `benchmarks.json` | 4 scenarios | as methodology (§6.6) |

So "34 hook cases" is 17 + 17, and the remaining 20 are Portal-specific. Our suite adds the
guard-hook fixtures on top.

### 6.3 Skills

The block message says *what* to call; the skills say *how*, and carry weight the hooks cannot:

- `bulk-reader/SKILL.md` — send **a question, not "summarise this file"**; and when the goal
  is an edit, follow the answer with a targeted `Read` of the region it names. This is the
  no-delegated-edits non-goal turned into guidance delivered at the moment it is needed.
- `code-writer/SKILL.md` — always pass `reference_path`; prefer `target_path` so generated
  code never enters context; `overwrite` is deliberate, not routine.

### 6.4 Evals and CI

Both plugins' eval suites run in a new blocking `hook-evals` job in `ci.yml`. The hooks are
pure decision functions over JSON on stdin — no provider keys, no network — making this the
one part of 0.18.0 that is cheap to test exhaustively. Requires `bash` + `jq` (+ `python3` for
the guard).

### 6.5 Attribution

1. **`THIRD_PARTY_NOTICES.md`** at repo root: states the repo is MIT except as noted; names
   **shunt**, Copyright Spotify AB, Apache-2.0; lists the derived files
   (`hooks/check-file-size`, `hooks/check-bash-read`, the eval fixtures); reproduces the full
   Apache-2.0 text, and upstream's `NOTICE` contents if it ships one (to be confirmed when the
   files are fetched).
2. Each derived file keeps its original header plus:
   `Modified 2026-09 for anti-tangent-mcp: replaced Portal/AiKA delegation target with the anti-tangent MCP tools.`
3. **README "Acknowledgements"** crediting shunt and the blog post.
4. **CHANGELOG** entry cites the same two links.
5. Worker instruction strings are paraphrased, not copied, so no notice attaches to them.
6. **The same applies to the "What is never delegated" list.** Upstream's README carries a
   four-bullet "What doesn't get delegated" section (debugging, editing, small files,
   architectural decisions). That is Apache-2.0 *documentation prose*; §7 reproduces the
   substance in `core.md` in our own words rather than copying it, so the protocol docs stay
   MIT-clean. Copying it verbatim would extend the Apache-2.0 obligation into `docs/protocol/`
   and from there into the bundled plugin copy.

CI asserts `THIRD_PARTY_NOTICES.md` exists and names both "Spotify AB" and "Apache". See
§10 for a deviation note on how this is implemented.

### 6.6 Benchmarks (in scope for 0.18.0)

Upstream publishes this table, measured against a 162K-line Java monorepo:

| Scenario | Lines | Without shunt | With shunt | Savings |
|---|---|---|---|---|
| Single large file | 4,014 | 33,684 tokens | 5,737 tokens | 82% |
| Source + test pair | 7,408 | 75,990 tokens | 4,148 tokens | 94% |
| Multi-file cross-service | 1,281 | 16,221 tokens | 821 tokens | 94% |
| Code-write | 3,667 | 40,614 tokens + generation | 833 lines to disk | — |

with a headline of **mean bulk-read savings 90%** across the first three.

*Provenance:* this table was verified against the raw bytes of upstream's README, not a
summary of it. The accompanying blog post is **not** reachable from this environment
(`403 CONNECT tunnel failed`) and has not been read; it is cited as a URL only. These
remain upstream's self-reported figures, on Java, through Portal/AiKA — which is the whole
reason we measure our own.

We reproduce all four against a Go corpus and publish our own table in
`plugin/anti-tangent-shunt/README.md`. The point is not to confirm their number but to
state an honest one for this stack: adopting the pattern on the strength of someone
else's benchmark, on a different language and a different delegation path, would be
recommending it on borrowed evidence.

**Measurement.** For each scenario, two quantities:

- *Without* — tokens the file(s) would occupy in the implementer's context, i.e. the
  token count of the content a direct `Read` would return.
- *With* — tokens the implementer actually receives: the `bulk_read` `answer` plus call
  overhead.

Savings is the ratio. The worker's own consumption is reported **separately** from the
new `input_tokens` / `output_tokens` stats fields, and must not be netted off: the claim
is about implementer context, and conflating the two would overstate it. Scenario 4 has
no savings ratio for the same reason upstream omits one — the comparison is against
generation the implementer would otherwise have emitted as output tokens, which is a
different unit.

**Corpus.** A public Go repository of comparable size (~160K lines) rather than this one,
which is far too small for the multi-file scenario to be meaningful. The specific repo,
the four file selections, and the commit SHA are pinned in the README so the table is
reproducible rather than anecdotal. *Repo choice is proposed at plan time, not fixed here.*

**Cost.** Real provider spend against the configured worker model. Runs once, by hand, at
implementation time — not in CI, which has no keys and must stay free.

## 7. Documentation changes

- **`docs/protocol/core.md`** — a shared "What is never delegated" list, so the review loop
  and the I/O loop cannot contradict each other. **Budget risk:** core.md is 14,460 bytes of a
  CI-enforced 16,000, leaving ~1,540. The list must fit or something gets trimmed to make room.
- **`docs/protocol/implementer.md`** — a "Large reads" clause: when a hook blocks, call
  `bulk_read` with a question; for an edit, follow up with a targeted `Read`. 14,017 bytes,
  ~1,983 of headroom. Also tight.
- **`docs/protocol/controller.md`** — the completion guard, what it blocks and how to disable
  it. Ample headroom at 10,643.
- New headings are **unnumbered**. `scripts/check-protocol-docs.sh` enforces that each tracked
  `§` identifier appears exactly once; unnumbered headings need no registry change and cannot
  disturb externally-cited numbering.
- **Bundle resync is mandatory in the same commit:**
  `rm -f plugin/anti-tangent-protocol/protocol/*.md && cp docs/protocol/*.md plugin/anti-tangent-protocol/protocol/`
- **README** — new env vars, the two new tools, Acknowledgements, and a rewrite of the
  filesystem trust-model paragraph. It currently argues from *read*: "the calling agent already
  has unrestricted file read... acquires no capability the caller lacks." The symmetry still
  holds, since the caller has `Write`/`Edit`, but the sentence must say so, and
  `ANTI_TANGENT_PLAN_ROOTS` becomes load-bearing for writes in a way it never was for reads.
- **`CLAUDE.md`** — "seven tools" → nine; new files in the architecture tree; §3.1's
  plugin-vs-server distinction under "What This Repo Is Not".
- **`.claude-plugin/marketplace.json`** — two new plugin entries; version 0.8.0 → 0.9.0.
- **`CHANGELOG.md`** — `## [0.18.0]` entry (CI-enforced against the branch name).
- **`VERSION` is not touched.** The release workflow bumps it; pre-bumping on a version branch
  breaks the release workflow's changelog validation.

## 8. Testing

### 8.1 Go

- `worker_call` response parsing: valid, truncated (`ErrResponseTruncated`), malformed.
- `bulk_read`: roots refusal, control-char refusal, too-large envelope, path count bounds.
- `code_write`: missing `reference_path`; `overwrite` false against an existing file; symlink
  at leaf refused under both `overwrite` values; parent outside roots; fence stripping;
  `lines_written` arithmetic.
- `resolveWriteTarget` gets its own table test mirroring `file_source_test.go`.
- Config: both new variables, the `MidModel` fallback chain, ceiling clamping.
- Prompts: golden files for both templates.
- Stats: new fields serialise; existing `rollup.json` keys unchanged.
- All under `-race`; no network — `httptest.Server` only.

### 8.2 Hook evals

JSON fixtures driven through each hook's stdin. Ported upstream cases plus our own for the
guard: absent validation, direct tool call, pasted summary block, `verdict: fail`, multiple
blocks (last wins), missing transcript, `GUARD=0`, non-`completed` status.

### 8.3 The cross-boundary test that matters

A Go test in `internal/mcpsrv` asserting `formatEnvelopeSummary` emits the exact substrings
`anti-tangent envelope`, `session_id:` and `verdict:` that the guard hook greps for. Without
it, a future summary-format change silently disarms the guard — the failure mode §3.5 accepts
risk on, converted into a build break.

### 8.4 Attribution test

A Go test asserting `THIRD_PARTY_NOTICES.md` exists and contains both `Spotify AB` and
`Apache`, reading it by relative path from its package directory. Implemented literally as
a test rather than a CI grep so it fails on a developer's machine under the
`go test -race ./...` everyone already runs — attribution is a legal obligation, and
catching its accidental removal before CI is worth the mildly odd artifact of a Go test
asserting on a repo-root markdown file.

## 9. Risks

| Risk | Mitigation |
|---|---|
| `check-bash-read` false positives block legitimate commands | Default-allow on ambiguity; port upstream fixtures; blocking CI job |
| Guard coupled to `summary_block` text | §8.3 pins the contract in a Go test |
| `core.md` / `implementer.md` byte budgets | Trim to fit; a sixth protocol part is the fallback but costs a CI registry entry |
| Worker context window: haiku is 200K, `bulk_read` allows 50 files | Existing payload cap applies; README guidance on model choice |
| Two new plugins in one release is a large surface | Both are opt-in installs; neither affects existing users who install neither |
| Blocking hooks read as abandoning the advisory non-goal | §3.1 stated explicitly in CLAUDE.md and README |
| Benchmarks are open-ended and cost real spend | Corpus and file selections pinned in the README; run once by hand, never in CI |

## 10. Relationship to the origin design

**Genuine deviations**, stated so review can catch anything unintended:

1. **`overwrite` added** to `code_write` (§3.6) — a field the origin design does not have.

**Open questions the origin design left, now resolved:**

2. **Worker default** → `MidModel` (§3.3).
3. **Benchmarks** → in scope for this release, all four scenarios (§6.6).
4. **`code_write` hook enforcement** → deferred, which is what the origin design proposed.
   Now confirmed against upstream, whose own "Known limitations" states: *"No enforcement for
   code-writer — only bulk-reader has hook enforcement. Code-writer relies on Claude
   recognizing when to use it via the skill description."* So this matches shipped upstream
   behaviour, not merely an untested intention. Revisit once `bulk_read` has been measured.

**Conformance worth noting:** AC #9 is implemented literally as a Go test (§8.4). An
earlier draft of this spec proposed a CI grep instead; that was rejected at review.

## 11. Acceptance criteria

1. `bulk_read` and `code_write` appear in the tool catalog alongside the existing seven.
2. `bulk_read` reads server-side under the same `resolveFileInput` rules as
   `validate_completion`, returning the answer plus `model_used`, `review_ms`, and token counts.
3. `code_write` rejects a call with no `reference_path`, explaining why. With `target_path` it
   writes and returns only a line count and token counts. Writes outside
   `ANTI_TANGENT_PLAN_ROOTS` are refused, as is an existing target without `overwrite: true`,
   as is a symlink at the leaf.
4. Markdown fences are stripped before writing.
5. Payloads above the cap are refused with the structured too-large envelope, never truncated.
6. `plugin/anti-tangent-shunt/` ships two PreToolUse hooks and two skills. The `Read` hook
   blocks full-file reads above `ANTI_TANGENT_SHUNT_MIN_LINES` (350) and names
   `mcp__anti-tangent__bulk_read`; targeted reads, sub-threshold files, and nonexistent files
   pass. The `Bash` hook blocks `cat`/`head`/`tail`/`less`/`more` on large files and passes
   pipes, redirections, and non-read commands.
7. `plugin/anti-tangent-guard/` ships one PostToolUse hook that blocks a `completed` close when
   neither pass signal appears in the window, and when the last summary block reads
   `verdict: fail`. `ANTI_TANGENT_COMPLETION_GUARD=0` disables it. It fails open on every error.
8. Hook evals run without provider keys and gate CI.
9. `docs/protocol/core.md` carries a "What is never delegated" list shared by both loops.
10. `THIRD_PARTY_NOTICES.md` exists, names Spotify AB and Apache-2.0, and a Go test asserts it.
11. `plugin/anti-tangent-shunt/README.md` carries a reproduced benchmark table covering all
    four scenarios against a pinned Go corpus, reporting implementer-context savings with
    worker consumption stated separately.
12. `go test -race ./...` passes; every protocol part stays under 16,000 bytes; the bundled
    plugin copy matches `docs/protocol/`.

## 12. Decision provenance

Every decision in this document has now been explicitly reviewed. Recorded so a later
reader can tell author decisions from implementer ones.

**Chosen by the author:** §4.1 hook event · §3.1/§4 guard packaging · §3.3 worker default ·
§3.4 port-vs-depend · §3.5 guard strictness · §3.6 overwrite · §6.4 evals in CI ·
§3.2 JSON-wrapper worker output · §5.3 parent must exist · §6.6 benchmarks in scope ·
§8.4 attribution as a Go test.

Two of these overturned an implementer assumption: benchmarks were initially deferred, and
the attribution check was initially proposed as a CI grep.

**Implementer calls, stated as assumptions rather than raised as forks:** `python3`+`jq`
for the guard hook (§4.5), last-summary-block-wins (§4.3), unnumbered protocol headings
(§7), the additive stats shape (§5.5), and the README trust-model rewrite (§7). Any of
these can still be reopened at spec review.
