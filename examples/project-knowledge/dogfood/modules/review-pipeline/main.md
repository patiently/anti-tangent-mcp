---
permalink: anti-tangent-mcp/modules/review-pipeline/main
type: module
title: Review pipeline — plan/task/completion validation surface
status: stable
last_changed_in: 0.6.2
relates_features: [anti-tangent-mcp/features/validate-plan/main, anti-tangent-mcp/features/validate-task-spec/main, anti-tangent-mcp/features/check-progress/main, anti-tangent-mcp/features/validate-completion/main]
shaped_by_decisions: [anti-tangent-mcp/decisions/0001-text-only-reviewer/main]
tags: [module, core-surface]
---

## Purpose

The review pipeline is anti-tangent's core capability. It takes plan text, task specs, or completion envelopes from a caller; runs a reviewer LLM against them with a structured prompt; parses the reviewer's JSON output into a verdict envelope; and returns the envelope to the caller. The four user-facing tools (`validate_plan`, `validate_task_spec`, `check_progress`, `validate_completion`) are different entry points into the same pipeline.

This is the **user-facing surface** of anti-tangent. The Go-package breakdown is incidental — the pipeline spans `internal/mcpsrv` (handler wiring), `internal/verdict` (verdict types, schemas, parsing), `internal/prompts` (embedded prompt templates + render functions), and `internal/providers` (per-provider HTTP clients to Anthropic / OpenAI / Google). None of these packages do useful work alone; together they implement the review pipeline.

## Invariants

- **Stateless except for sessions.** Each per-task hook creates / continues a `Session` (TTL-bounded, in-memory only). Sessions tie the lifecycle hooks together; the rest of the pipeline is pure request → reviewer → response.
- **Reviewer is text-only** ([decisions/0001-text-only-reviewer](anti-tangent-mcp/decisions/0001-text-only-reviewer/main)). The pipeline never grants the reviewer codebase access.
- **Strict OpenAI schema compatibility.** The four reviewer-output schemas (`schema.json`, `plan_schema.json`, `tasks_only_schema.json`, `plan_findings_only_schema.json`, plus v0.6.0's `prime_schema.json` and `extract_schema.json`) all pass the strict-mode invariants in `schema_invariants_test.go`. Any new property must appear in `required`; no freeform objects; `additionalProperties: false` everywhere.
- **`stdout` is reserved for MCP stdio traffic.** All logging goes to stderr via structured `slog`. Any incidental `fmt.Println` to stdout would break the MCP transport.

## Conventions

- Reviewer output is always parsed via `verdict.Parse*` helpers; the parser owns enum validation, severity-floor enforcement, partial-recovery, and field-presence checks. Handlers don't reimplement these.
- Per-hook handler files (`internal/mcpsrv/{validate_*,prime_handler,extract_handler}.go`) all follow the same ordering: payload-size guard → model resolve → render prompt → call reviewer → parse output → finalize verdict → return envelope.
- New reviewer-output finding categories require updates in five places: `verdict.Category` constants, `validCategory` switch, all four canonical schemas' enum entries, plus `prime_schema.json` and `extract_schema.json` if applicable. The `schema_invariants_test.go::TestReviewerSchemas_CategoryEnumsAreInLockstep` catches lockstep failures.
- Embedded prompt templates live in `internal/prompts/templates/`; the package's `Render*` functions are the only entry points (no direct template execution from handler code).

## Touch-points

- `internal/mcpsrv/server.go` — MCP server entrypoint; registers all 6 tools.
- `internal/mcpsrv/handlers.go` — shared helpers (`review`, `effectiveMaxTokens`, `envelopeResult`, etc.).
- `internal/mcpsrv/{prime,extract}_handler.go` — v0.6.0 stateless handlers.
- `internal/verdict/` — verdict types, JSON schemas, parsers, severity-floor helpers.
- `internal/prompts/prompts.go` + `internal/prompts/templates/*.tmpl` — prompt rendering.
- `internal/providers/{anthropic,openai,google}.go` — provider HTTP clients.
