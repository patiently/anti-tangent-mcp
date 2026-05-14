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
```

### Large plans (chunking)

`validate_plan` automatically chunks plans with more than `ANTI_TANGENT_PLAN_TASKS_PER_CHUNK` tasks (default 8). Internally the server makes one Pass-1 reviewer call for cross-cutting `plan_findings` plus `ceil(n/N)` per-task chunks, each carrying the full plan as context. The merged `PlanResult` is identical in shape to the single-call path — callers see no difference. Operators with very dense per-task content (long `**Goal:**` / `**Acceptance criteria:**` blocks) can lower `ANTI_TANGENT_PLAN_TASKS_PER_CHUNK` to reduce per-chunk output size, or raise `ANTI_TANGENT_PLAN_MAX_TOKENS` if their reviewer model supports it. Worst-case wall-clock for a 25-task plan is ~5 sequential calls.

### Per-call tool args (v0.3.0+)

All four tools accept an optional `max_tokens_override` non-negative int — replaces the configured default (`PerTaskMaxTokens` or `PlanMaxTokens`) for this call only. Zero or unset uses the configured default; positive values up to `ANTI_TANGENT_MAX_TOKENS_CEILING` are used directly; over-ceiling values are clamped to the ceiling and emit a `minor` clamp finding. Negative values are rejected at the handler boundary with `max_tokens_override must be ≥ 0`. Use when you know one specific call needs a larger reviewer budget without changing global config.

`validate_plan` additionally accepts an optional `mode` arg of `"quick"` or `"thorough"` (default `"thorough"`). `"quick"` instructs the reviewer to surface only the most-severe findings (at most 3 per scope) — useful for small ASAP plans where you don't want round-after-round of stylistic refinement.

`validate_plan` scales its default output budget by task count when no `max_tokens_override` is supplied. If reviewer output still truncates before any usable analysis, the response is a `warn` with a `major` truncation finding and retry guidance naming `max_tokens_override`, `ANTI_TANGENT_PLAN_MAX_TOKENS`, and `ANTI_TANGENT_MAX_TOKENS_CEILING`.

Task-level `unverifiable_codebase_claim` findings are rolled into one plan-level checklist. If that checklist is the only remaining finding category, `plan_verdict` is `pass` and `plan_quality` lands at `actionable` (or stays at `rigorous` when the reviewer already emitted that); callers should still pre-flight the references before dispatch.

### `validate_task_spec` arguments (v0.3.3+)

In addition to the existing `task_title` / `goal` / `acceptance_criteria` / `non_goals` / `context` fields:

- `pinned_by` (optional): existing tests, docs, commands, or static checks that pin referenced behavior. The reviewer treats these as caller-supplied anchors, not independently verified codebase facts.
- `phase` (optional): `pre` (default) or `post`. Use `post` only for post-hoc/session-recovery reviews; normal protocol still calls this at task start.

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

## The 4 tools

- `validate_plan` — call once at plan-handoff time. Reviews an entire implementation plan and proposes ready-to-paste structured headers (Goal / AC / Non-goals / Context) for tasks that lack them. Returns per-task findings.
- `validate_task_spec` — call once before coding. Returns findings on missing goals, weak acceptance criteria, unstated assumptions. Returns a `session_id` you thread through the next two calls.
- `check_progress` — call at checkpoints during implementation. Catches scope drift, untouched ACs, and unaddressed prior findings.
- `validate_completion` — call before claiming done. Walks every AC and non-goal explicitly.

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

## Integration

For wiring this MCP into your LLM-driven implementation workflow (superpowers, hone-ai, vanilla Claude Code, or any harness with MCP support), see [`INTEGRATION.md`](INTEGRATION.md).

## Design

Authoritative design: [`docs/superpowers/specs/2026-05-07-anti-tangent-mcp-design.md`](docs/superpowers/specs/2026-05-07-anti-tangent-mcp-design.md).

## License

MIT — see [`LICENSE`](LICENSE) for full text.
