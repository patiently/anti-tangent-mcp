# Integrating anti-tangent-mcp

`anti-tangent-mcp` is an advisory MCP server that helps prevent implementing-subagent drift by reviewing the task spec and the in-progress work at three lifecycle points (pre / mid / post). The reviewer LLM is intentionally a different model from the implementer, so reviews are not blind to the implementer's blind spots. See [`README.md`](README.md) for the tool surface and [`docs/superpowers/specs/2026-05-07-anti-tangent-mcp-design.md`](docs/superpowers/specs/2026-05-07-anti-tangent-mcp-design.md) for the authoritative design.

This document tells you how to wire the server into your workflow. It has two real audiences:

- **Plan authors** — get a recommended task format that maps directly to `validate_task_spec` inputs (one-time read while drafting).
- **Implementing subagents** — get a paste-in lifecycle clause that mandates pre + post calls, recommends mid calls, and tells them how to handle findings.

There is also a short addendum for **controllers** who orchestrate subagent dispatches (e.g. superpowers' `subagent-driven-development`, hone-ai's equivalent, or hand-rolled loops).

The integration is **system-agnostic**: it works with superpowers, hone-ai, vanilla Claude Code with a project-level `CLAUDE.md`, Cursor, or any harness that supports MCP servers. It ships as a single markdown document; you paste the relevant chunks where they need to go.

> **When does anti-tangent-mcp earn its keep?** Its value compounds when (a) tasks are specced before being implemented, (b) the implementer is an LLM that can drift, and (c) the implementer LLM differs from the reviewer LLM. Without all three, anti-tangent is just extra latency.

---

## 1. Setup

### 1.1 Install the binary

Pick one:

```bash
# Option A: build from source
go install github.com/patiently/anti-tangent-mcp@latest
```

```bash
# Option B: download a prebuilt binary
# https://github.com/patiently/anti-tangent-mcp/releases
```

```bash
# Option C: container image
docker pull ghcr.io/patiently/anti-tangent-mcp:latest
```

### 1.2 Register the MCP server with your harness

Claude Code (`.mcp.json`):

```json
{
  "mcpServers": {
    "anti-tangent": {
      "command": "anti-tangent-mcp",
      "env": {
        "ANTHROPIC_API_KEY": "sk-ant-...",
        "OPENAI_API_KEY":    "sk-..."
      }
    }
  }
}
```

Other harnesses (Cursor, Continue, Zed, custom) accept the same `command` + `env` shape — adapt to their config file.

### 1.3 Provider keys

Set at least one of `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GOOGLE_API_KEY`. Only providers with a key set are activated; calling a missing-keyed provider returns a clear error from the tool.

### 1.4 Pick a model split (the load-bearing decision)

The reviewer LLM should not be the same model as the implementer. Same model + same training data ≈ same blind spots, which defeats the point. Recommended starter config:

| If your implementer is… | Set `ANTI_TANGENT_*_MODEL` to… |
|---|---|
| Anthropic Claude (Sonnet/Opus) | `openai:gpt-5` and/or `google:gemini-2.5-pro` |
| OpenAI GPT-5 family | `anthropic:claude-sonnet-4-6` and/or `google:gemini-2.5-pro` |
| Google Gemini | `anthropic:claude-sonnet-4-6` and/or `openai:gpt-5` |

The mid-hook (`check_progress`) is called more often, so use a cheaper fast model:

```dotenv
ANTI_TANGENT_PRE_MODEL=openai:gpt-5
ANTI_TANGENT_MID_MODEL=openai:gpt-5-mini
ANTI_TANGENT_POST_MODEL=openai:gpt-5
```

Or mix providers across hooks if you have multiple keys.

### 1.5 Smoke test

Launch your harness with debug logging on and confirm the three tools (`validate_task_spec`, `check_progress`, `validate_completion`) appear in the discovered tool catalog. If the server fails at startup, it prints the configuration error to stderr.

---

<!-- Sections 2–5 added in subsequent commits -->
