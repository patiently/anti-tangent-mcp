# anti-tangent-mcp

An advisory MCP server that fights agent drift. Implementing subagents call three lifecycle tools — `validate_task_spec`, `check_progress`, `validate_completion` — and a reviewer LLM (Anthropic, OpenAI, or Google) returns structured findings against the task's acceptance criteria.

The reviewer is intentionally a different model than the implementer, so reviews are not blind to the implementer's blind spots.

## Install

```bash
go install github.com/patiently/anti-tangent-mcp/cmd/anti-tangent-mcp@latest
```

Or grab a pre-built binary from the [releases page](https://github.com/patiently/anti-tangent-mcp/releases). Or pull the container image:

```bash
docker pull ghcr.io/patiently/anti-tangent-mcp:latest
```

### One-shot install via paste-in prompt

If your host is Claude Code or opencode, you can paste the matching prompt below into a fresh session and the agent will fetch the latest release binary, register the MCP, download `INTEGRATION.md`, and wire it into your user-level instructions so every future session sees the protocol. The prompts resolve "latest" from the GitHub API, so they don't need to be edited each release.

These prompts target Linux and macOS. Windows users should follow the manual install above and adapt the steps to their host's MCP config format.

Pick the host you use:

#### Claude Code

````text
Install the latest anti-tangent-mcp release on this Linux/macOS machine and
wire it into Claude Code. Do all of the following, reporting progress after
each step and stopping on any error. NEVER echo the value of any API key in
your reports — redact to `***` whenever a step would otherwise print one.

1. Detect this machine's OS and arch (`uname -s`, `uname -m`). Map to one of
   Linux_x86_64, Linux_arm64, Darwin_x86_64, Darwin_arm64. If the host is
   Windows, stop and tell me to use the manual install instead.
2. Look up the latest release tag from
   https://api.github.com/repos/patiently/anti-tangent-mcp/releases/latest
   (`tag_name` is shaped like `v0.5.0`; strip the leading `v` to get VERSION).
3. Download `anti-tangent-mcp_${VERSION}_${OS}_${ARCH}.tar.gz` from the release
   assets and extract the `anti-tangent-mcp` binary to
   `~/.local/bin/anti-tangent-mcp`. `mkdir -p ~/.local/bin` first; `chmod +x`
   after extraction. Run `~/.local/bin/anti-tangent-mcp --version` and confirm
   it prints VERSION.
4. If `~/.local/bin` is not on my PATH, tell me the one-line export to add to
   my shell profile (do not edit my dotfiles yourself). Continue with the
   absolute path regardless.
5. Register the MCP server at user scope so it applies across all projects:
       claude mcp add anti-tangent ~/.local/bin/anti-tangent-mcp -s user
   Verify with `claude mcp list`.
6. The server needs at least one reviewer key. Ask me which of
   ANTHROPIC_API_KEY / OPENAI_API_KEY / GOOGLE_API_KEY to use. If I want
   Claude Code to inherit it from my shell env, leave the registration as-is
   and remind me to export the variable from my shell profile. Otherwise
   re-register with `-e KEY=VALUE` so the key lives in Claude Code's MCP
   config instead of my shell env. Do not print the value back in any
   subsequent report.
7. Download `INTEGRATION.md` for the installed version to
   `~/.claude/anti-tangent.md` (overwrite if present — it is a mirror, not a
   user file):
       https://raw.githubusercontent.com/patiently/anti-tangent-mcp/v${VERSION}/INTEGRATION.md
8. Wire that file into `~/.claude/CLAUDE.md` so every Claude Code session
   sees the protocol. Claude Code's `@`-import does NOT expand `~` — use the
   literal absolute path (e.g. `@/Users/me/.claude/anti-tangent.md` on macOS,
   `@/home/me/.claude/anti-tangent.md` on Linux):
   - If `~/.claude/CLAUDE.md` exists, append the import line under an
     `## Active integrations` heading (create the heading if absent). Skip if
     the same import is already present.
   - If it doesn't exist, create it with that heading and one import line.

Report: installed version, binary path, `claude mcp list` output (with any
key values redacted), and the final contents of `~/.claude/CLAUDE.md`.
````

#### opencode

````text
Install the latest anti-tangent-mcp release on this Linux/macOS machine and
wire it into opencode. Do all of the following, reporting progress after each
step and stopping on any error. NEVER echo the value of any API key in your
reports — redact to `***` whenever a step would otherwise print one.

1. Detect OS+arch (`uname -s`, `uname -m`). Map to one of Linux_x86_64,
   Linux_arm64, Darwin_x86_64, Darwin_arm64. If the host is Windows, stop
   and tell me to use the manual install instead.
2. Look up the latest release tag from
   https://api.github.com/repos/patiently/anti-tangent-mcp/releases/latest;
   strip the leading `v` from `tag_name` to get VERSION.
3. Download `anti-tangent-mcp_${VERSION}_${OS}_${ARCH}.tar.gz`, extract
   `anti-tangent-mcp` to `~/.local/bin/anti-tangent-mcp` (`mkdir -p
   ~/.local/bin` first, `chmod +x` after), and verify
   `~/.local/bin/anti-tangent-mcp --version` prints VERSION.
4. opencode requires an ABSOLUTE path in MCP `command` and does NOT
   auto-inherit shell env into MCP servers. It does, however, support
   `{env:NAME}` substitution at config-load time (and `{file:path}` for keys
   stored in a separate file). Ask me:
   (a) which of ANTHROPIC_API_KEY / OPENAI_API_KEY / GOOGLE_API_KEY to use,
   AND
   (b) how to wire the value, defaulting to env substitution:
       - `{env:NAME}` (default, recommended): the key stays in my shell
         profile and the JSON only references it by name. Remind me to
         export the variable.
       - `{file:/abs/path}`: the key lives in a separate file. Ask for the
         path and confirm it's outside the opencode config dir if I want it
         git-ignored.
       - Literal value: only if I explicitly opt in. Hold the value in
         memory; never echo it back.
5. `mkdir -p ~/.config/opencode`, then open `~/.config/opencode/opencode.json`.
   If it doesn't exist, create it as
   `{ "$schema": "https://opencode.ai/config.json" }`. Then add or merge an
   `mcp.anti-tangent` entry, preserving any other MCP servers already
   configured. Use the absolute resolved binary path (no `~`) and the chosen
   substitution form for the API key (env substitution shown — replace the
   value with `{file:/abs/path}` or a literal if I picked one of those):
       {
         "mcp": {
           "anti-tangent": {
             "type": "local",
             "command": ["/abs/path/to/anti-tangent-mcp"],
             "environment": {
               "<PROVIDER>_API_KEY": "{env:<PROVIDER>_API_KEY}"
             }
           }
         }
       }
6. Download `INTEGRATION.md` for the installed version to
   `~/.config/opencode/anti-tangent.md` (overwrite if present):
       https://raw.githubusercontent.com/patiently/anti-tangent-mcp/v${VERSION}/INTEGRATION.md
7. Wire that file into opencode's top-level `instructions` array in the same
   `~/.config/opencode/opencode.json` you edited in step 5 — opencode loads
   files listed there automatically; it does NOT process `@`-imports inside
   `AGENTS.md`. Use the literal absolute path (no `~`):
       {
         "instructions": ["/abs/path/to/.config/opencode/anti-tangent.md"]
       }
   If an `instructions` array is already present, append the path only if
   not already listed. Do not duplicate entries.
8. Tell me to restart opencode so the new MCP entry and `instructions` are
   loaded.

Report: installed version, binary path, and the final `opencode.json` with
every `*_API_KEY` value redacted to `***` before printing.
````

## Configure

Set at least one provider key. The defaults route every hook through Anthropic; override per hook with env vars.

```dotenv
ANTHROPIC_API_KEY=sk-ant-...
OPENAI_API_KEY=sk-...
GOOGLE_API_KEY=...

# Optional per-hook model defaults:
ANTI_TANGENT_PRE_MODEL=anthropic:claude-sonnet-4-6
ANTI_TANGENT_MID_MODEL=anthropic:claude-haiku-4-5-20251001
ANTI_TANGENT_POST_MODEL=anthropic:claude-opus-4-7
ANTI_TANGENT_PLAN_MODEL=anthropic:claude-sonnet-4-6   # validate_plan; defaults to ANTI_TANGENT_PRE_MODEL

# Optional tunables:
ANTI_TANGENT_SESSION_TTL=4h
ANTI_TANGENT_MAX_PAYLOAD_BYTES=204800
ANTI_TANGENT_REQUEST_TIMEOUT=180s
ANTI_TANGENT_LOG_LEVEL=info

# Output budgets + chunking (v0.1.4+):
ANTI_TANGENT_PER_TASK_MAX_TOKENS=4096    # output cap for the per-task hooks (validate_task_spec / check_progress / validate_completion); raise if a stateful hook returns a truncation finding
ANTI_TANGENT_PLAN_MAX_TOKENS=4096        # output cap per reviewer call in validate_plan (single-call and per-chunk); raise if plan validation returns a truncation finding
ANTI_TANGENT_PLAN_TASKS_PER_CHUNK=8      # plans above this task count are reviewed via the chunked path; also the per-chunk size
ANTI_TANGENT_MAX_TOKENS_CEILING=16384    # cap on per-call max_tokens_override; over-ceiling values are clamped and emit a minor clamp finding (v0.3.0+)

# Project knowledge (v0.6.0+ optional integration):
ANTI_TANGENT_KB_STORE=                   # empty (default; bm_commands omitted) or "basic-memory" (only accepted non-empty value); gates bm_commands emission on prime/extract
ANTI_TANGENT_PRIME_MODEL=                # prime_project_knowledge; falls back to ANTI_TANGENT_PLAN_MODEL → ANTI_TANGENT_PRE_MODEL when unset
ANTI_TANGENT_EXTRACT_MODEL=              # extract_project_knowledge; falls back to ANTI_TANGENT_PLAN_MODEL → ANTI_TANGENT_PRE_MODEL when unset
ANTI_TANGENT_PRIME_MAX_TOKENS=4096       # output cap for prime_project_knowledge; raise if a prime call returns a truncation finding
ANTI_TANGENT_EXTRACT_MAX_TOKENS=8192     # output cap for extract_project_knowledge; raise if an extract call returns a truncation finding
```

### Picking a reviewer model

The reviewer LLM should not be the same model as the implementer. Same model + same training data ≈ same blind spots, which defeats the point.

| If your implementer is… | Set `ANTI_TANGENT_*_MODEL` to… |
|---|---|
| Anthropic Claude (Sonnet/Opus) | `openai:gpt-5` and/or `google:gemini-3.1-pro-preview` |
| OpenAI GPT-5 family | `anthropic:claude-sonnet-4-6` and/or `google:gemini-3.1-pro-preview` |
| Google Gemini | `anthropic:claude-sonnet-4-6` and/or `openai:gpt-5` |

The mid-hook (`check_progress`) is called more often — a fast/cheap tier there is fine. The plan-level hook (`validate_plan`) reasons over the whole plan in one shot — give it a strong tier. `ANTI_TANGENT_PLAN_MODEL` falls back to `ANTI_TANGENT_PRE_MODEL` if unset.

### Smoke test

Launch your MCP host with debug logging on and confirm all six tools — `validate_plan`, `validate_task_spec`, `check_progress`, `validate_completion`, `prime_project_knowledge`, `extract_project_knowledge` — appear in the discovered tool catalog. Server-side configuration errors print to stderr at startup.

### Large plans (chunking)

`validate_plan` automatically chunks plans with more than `ANTI_TANGENT_PLAN_TASKS_PER_CHUNK` tasks (default 8). Internally the server makes one Pass-1 reviewer call for cross-cutting `plan_findings` plus `ceil(n/N)` per-task chunks, each carrying the full plan as context. The merged `PlanResult` is identical in shape to the single-call path — callers see no difference. Operators with very dense per-task content (long `**Goal:**` / `**Acceptance criteria:**` blocks) can lower `ANTI_TANGENT_PLAN_TASKS_PER_CHUNK` to reduce per-chunk output size, or raise `ANTI_TANGENT_PLAN_MAX_TOKENS` if their reviewer model supports it. Worst-case wall-clock for a 25-task plan is ~5 sequential calls.

### Per-call tool args (v0.3.0+)

All six tools accept an optional `max_tokens_override` non-negative int — replaces the configured default (`PerTaskMaxTokens`, `PlanMaxTokens`, `PrimeMaxTokens`, or `ExtractMaxTokens`) for this call only. Zero or unset uses the configured default; positive values up to `ANTI_TANGENT_MAX_TOKENS_CEILING` are used directly; over-ceiling values are clamped to the ceiling and emit a `minor` clamp finding. Negative values are rejected at the handler boundary with `max_tokens_override must be ≥ 0`. Use when you know one specific call needs a larger reviewer budget without changing global config.

`validate_plan` additionally accepts an optional `mode` arg of `"quick"` or `"thorough"` (default `"thorough"`). `"quick"` instructs the reviewer to surface only the most-severe findings (at most 3 per scope) — useful for small ASAP plans where you don't want round-after-round of stylistic refinement.

`validate_plan` scales its default output budget by task count when no `max_tokens_override` is supplied. If reviewer output still truncates before any usable analysis, the response is a `warn` with a `major` truncation finding and retry guidance naming `max_tokens_override`, `ANTI_TANGENT_PLAN_MAX_TOKENS`, and `ANTI_TANGENT_MAX_TOKENS_CEILING`.

Task-level `unverifiable_codebase_claim` findings are rolled into one plan-level checklist. If that checklist is the only remaining finding category, `plan_verdict` is `pass` and `plan_quality` lands at `actionable` (or stays at `rigorous` when the reviewer already emitted that); callers should still pre-flight the references before dispatch.

As of v0.4.0, `validate_plan` task results may include `lightweight_eligible` and `lightweight_reason`. These are advisory controller hints for trivial mechanical tasks that may use the lightweight protocol; they do not change the server-side lifecycle hooks.

Identical passing `validate_plan` calls are cached in memory for 3 minutes. The cache identity includes the rendered prompt, model, mode, and token budget; cache hits return `review_ms: 0` and preserve the original `next_action` behind a `[cached <=3m]` prefix.

### `validate_task_spec` arguments

In addition to the existing `task_title` / `goal` / `acceptance_criteria` / `non_goals` / `context` fields:

- `pinned_by` (optional, v0.3.3+): existing tests, docs, commands, or static checks that pin referenced behavior. The reviewer treats these as caller-supplied anchors, not independently verified codebase facts.
- `controller_verified_references` (optional, v0.4.0+): paths, symbols, line anchors, commands, or adjacent patterns that the controller already verified before dispatch. The reviewer treats these as caller-supplied attestations and suppresses matching `unverifiable_codebase_claim` findings only by deterministic substring match; contradictions, missing acceptance criteria, and ambiguity still surface.
- `harness_shape_attestation` (optional, v0.5.2+): list of `{harness, path, assertions[]}` objects declaring caller-attested shape facts about test harnesses or fixtures. Pairs with the new `attestation_contradiction` finding category, which the reviewer emits only when an acceptance criterion explicitly contradicts an attested assertion.
- `phase` (optional, v0.3.3+): `pre` (default) or `post`. Use `post` only for post-hoc/session-recovery reviews; normal protocol still calls this at task start.

`validate_task_spec` rolls task-level `unverifiable_codebase_claim` findings into a single `codebase_reference_checklist` finding so implementers get one consistent checklist shape instead of raw text-only-reference findings.

### Lightweight protocol mode (v0.3.1+)

For trivial tasks (doc-only edits, mechanical relocations, dependency bumps), the full anti-tangent dispatch protocol is overhead-heavy. As of v0.3.1 the project ships a lightweight dispatch template at [`examples/lightweight-dispatch.md`](examples/lightweight-dispatch.md) that skips `validate_task_spec` and `check_progress`, keeping only `validate_completion` as a sanity gate. See `INTEGRATION.md`'s "Lightweight protocol mode" section for when to use it.

### Companion tool: CodeScene MCP (optional)

Anti-tangent's reviewer is intentionally text-only: it reasons over plan text and submitted evidence, not the codebase. That bounds what it can catch (see "Scope and limits" in INTEGRATION.md). For the codebase-grounded blind spot — Code Health regressions, complexity creep, low cohesion in actually-modified files — the recommended companion is [CodeScene MCP](https://github.com/codescene-oss/codescene-mcp-server), open-sourced by CodeScene (https://codescene.com).

When CodeScene MCP is configured in your host alongside anti-tangent, dispatched implementers are instructed to call:

- `pre_commit_code_health_safeguard` mid-task — deterministic Code Health check on uncommitted/staged files. Fast, cheap, and complementary to anti-tangent's optional `check_progress`.
- `analyze_change_set` before reporting DONE — full branch-vs-base Code Health analysis. Cite the delta and any findings in the DONE summary alongside anti-tangent's `summary_block`.

The pairing is **advisory** on the anti-tangent side: anti-tangent never enforces CodeScene findings, and the call is skipped silently when CodeScene MCP isn't configured. See INTEGRATION.md's "CodeScene MCP companion" section for the dispatch-clause integration details.

## Use with Claude Code (`.mcp.json`)

```json
{
  "mcpServers": {
    "anti-tangent": {
      "command": "anti-tangent-mcp",
      "env": {
        "ANTHROPIC_API_KEY": "sk-ant-..."
      }
    }
  }
}
```

## Use with opencode (`~/.config/opencode/opencode.json`)

```json
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "anti-tangent": {
      "type": "local",
      "command": ["/absolute/path/to/anti-tangent-mcp"],
      "environment": {
        "GOOGLE_API_KEY": "...",
        "ANTI_TANGENT_PRE_MODEL":  "google:gemini-3.1-pro-preview",
        "ANTI_TANGENT_MID_MODEL":  "google:gemini-3.1-flash-lite",
        "ANTI_TANGENT_POST_MODEL": "google:gemini-3.1-pro-preview",
        "ANTI_TANGENT_PLAN_MODEL": "google:gemini-3.1-pro-preview"
      }
    }
  }
}
```

opencode requires `command` as an array and uses `environment` (not `env`). The binary path must be absolute — `$PATH` is not consulted. Restart opencode after editing.

## Supported reviewer models

Set `ANTI_TANGENT_*_MODEL` (or pass `model` per call) using `provider:model-id`. The server validates against this allowlist at startup and rejects unknown IDs with a clear error.

| Provider | Model id | Tier |
|---|---|---|
| `anthropic` | `claude-opus-4-7` | heavy |
| `anthropic` | `claude-sonnet-4-6` | balanced |
| `anthropic` | `claude-haiku-4-5-20251001` | fast |
| `openai` | `gpt-5` | heavy |
| `openai` | `gpt-5-mini` | balanced |
| `openai` | `gpt-5-nano` | fast |
| `openai` | `gpt-5.5` | heavy (rolling snapshot) |
| `openai` | `gpt-5.5-2026-04-23` | heavy (pinned) |
| `openai` | `gpt-5.4-mini` | balanced (rolling snapshot) |
| `openai` | `gpt-5.4-mini-2026-03-17` | balanced (pinned) |
| `google` | `gemini-3.1-pro-preview` | heavy |
| `google` | `gemini-3.1-flash-lite` | fast |
| `google` | `gemini-2.5-pro` | heavy |
| `google` | `gemini-2.5-flash` | fast |

Adding a new model is a one-line change in [`internal/providers/reviewer.go`](internal/providers/reviewer.go) — open a PR.

## The 6 tools

- `validate_plan` — call once at plan-handoff time. Reviews an entire implementation plan and proposes ready-to-paste structured headers (Goal / AC / Non-goals / Context) for tasks that lack them. Returns per-task findings.
- `validate_task_spec` — call once before coding. Returns findings on missing goals, weak acceptance criteria, unstated assumptions. Returns a `session_id` you thread through the next two calls.
- `check_progress` — call at checkpoints during implementation. Catches scope drift, untouched ACs, and unaddressed prior findings.
- `validate_completion` — call before claiming done. Walks every AC and non-goal explicitly.
- `prime_project_knowledge` (v0.6.0+, optional) — stateless. Given a task spec and a Basic-Memory-style `kb_index`, returns prioritized note picks for the implementer to read before starting. Emits paste-ready `bm_commands` when `ANTI_TANGENT_KB_STORE=basic-memory`.
- `extract_project_knowledge` (v0.6.0+, optional) — stateless. Given one or more `validate_completion` envelopes, returns structured create/update/supersede proposals for the project knowledge base. Same env gate for `bm_commands`.

The latter three return the same envelope; `validate_plan` returns a richer `PlanResult` with per-task analysis (see [INTEGRATION.md](INTEGRATION.md) §5.5):

```json
{
  "session_id": "uuid",
  "verdict": "pass",
  "findings": [
    {
      "severity": "major",
      "category": "missing_acceptance_criterion",
      "criterion": "<verbatim AC>",
      "evidence": "<code or spec text>",
      "suggestion": "<concrete next action>"
    }
  ],
  "next_action": "one sentence",
  "model_used": "anthropic:claude-sonnet-4-6",
  "review_ms": 2341,
  "session_expires_at": "2026-05-12T18:30:00Z",
  "session_ttl_remaining_seconds": 14399
}
```

`session_expires_at` and `session_ttl_remaining_seconds` are included in stateful-hook responses (v0.2.0+). If a stateful hook returns a `category: other` finding with `criterion: reviewer_response`, the reviewer response was cut off at the token budget — raise `ANTI_TANGENT_PER_TASK_MAX_TOKENS` and retry.

`validate_completion` (v0.2.0+) accepts `final_diff` as an alternative or supplement to `final_files`. Pass a unified diff when the changed files are too large to inline. At least one of `final_files`, `final_diff`, or `test_evidence` must be non-empty — summary-only requests are rejected. Timeout errors (default 180s, configurable via `ANTI_TANGENT_REQUEST_TIMEOUT`) include the configured timeout value and the env-var name for self-diagnosis.

## Project knowledge (optional)

On epic-scale projects with multiple agents and authors, implementers drift away from decisions already taken and modules already shaped. v0.6.0 adds an optional knowledge-base loop alongside the review loop: `prime_project_knowledge` recommends notes to attach before a task starts, `extract_project_knowledge` proposes new notes from a completion envelope, and `validate_task_spec` / `validate_plan` accept an optional `project_knowledge` string the reviewer treats as authoritative grounding (same posture as `pinned_by`). The knowledge itself lives in [Basic Memory](https://github.com/basicmachines-co/basic-memory) (recommended) or any markdown-backed store — anti-tangent never reads or writes that store directly.

- Design: [`docs/superpowers/specs/2026-05-18-project-knowledge-design.md`](docs/superpowers/specs/2026-05-18-project-knowledge-design.md)
- Integration playbook: [INTEGRATION.md, "Project knowledge (optional)"](INTEGRATION.md#project-knowledge-optional)
- Shared-VM setup: [`docs/team-setup/basic-memory-shared-vm.md`](docs/team-setup/basic-memory-shared-vm.md)
- Note templates: [`examples/project-knowledge/`](examples/project-knowledge/)

## Integration

For wiring this MCP into your LLM-driven implementation workflow (superpowers, hone-ai, vanilla Claude Code, or any harness with MCP support), see [`INTEGRATION.md`](INTEGRATION.md).

## Design

Authoritative design: [`docs/superpowers/specs/2026-05-07-anti-tangent-mcp-design.md`](docs/superpowers/specs/2026-05-07-anti-tangent-mcp-design.md).

## License

MIT — see [`LICENSE`](LICENSE) for full text.
