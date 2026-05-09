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

# Optional tunables:
ANTI_TANGENT_SESSION_TTL=4h
ANTI_TANGENT_MAX_PAYLOAD_BYTES=204800
ANTI_TANGENT_REQUEST_TIMEOUT=120s
ANTI_TANGENT_LOG_LEVEL=info
```

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
  "review_ms": 2341
}
```

## Integration

For wiring this MCP into your LLM-driven implementation workflow (superpowers, hone-ai, vanilla Claude Code, or any harness with MCP support), see [`INTEGRATION.md`](INTEGRATION.md).

## Design

Authoritative design: [`docs/superpowers/specs/2026-05-07-anti-tangent-mcp-design.md`](docs/superpowers/specs/2026-05-07-anti-tangent-mcp-design.md).

## License

MIT — see [`LICENSE`](LICENSE) for full text.
