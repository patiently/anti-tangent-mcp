# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0] - 2026-05-07

### Added
- Initial release. MCP server (`anti-tangent-mcp`) exposing three tools that
  review implementing-subagent work at the start, middle, and end of a task:
  - `validate_task_spec` — checks structural completeness, AC quality, and
    unstated assumptions before coding begins.
  - `check_progress` — flags scope drift, untouched ACs, and unaddressed
    prior findings during implementation.
  - `validate_completion` — walks every AC and non-goal in a final review.
- Multi-provider reviewer support: Anthropic Messages API (tool_use),
  OpenAI Chat Completions (json_schema), Google Gemini generateContent
  (responseSchema). Per-hook model defaults overridable per call.
- In-memory session store with configurable TTL (default 4h).
- Cross-platform binaries via GoReleaser (linux/darwin/windows × amd64/arm64).
- Distroless static container image published to ghcr.io.
- GitHub Actions CI (changelog enforcement, `go test -race`) and release
  workflow (commit-tag-driven semver bump, tag, GoReleaser, GHCR push).
- `validate_plan` MCP tool — plan-level handoff gate that reviews an entire implementation plan in one call and proposes ready-to-paste structured-header blocks (Goal / Acceptance criteria / Non-goals / Context) for tasks that lack them. Replaces the per-task plan-handoff loop.
- `ANTI_TANGENT_PLAN_MODEL` env var — overrides the model used by `validate_plan`. Defaults to `ANTI_TANGENT_PRE_MODEL`.
