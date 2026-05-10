# CLAUDE.md

This file provides guidance to Claude Code (and other agents) when working with code in this repository.

## Project Overview

`anti-tangent-mcp` is an advisory MCP server (Go binary) that helps prevent implementing subagents from drifting away from their assigned tasks. It exposes three tools — `validate_task_spec`, `check_progress`, `validate_completion` — that send the task spec and code under review to a reviewer LLM and return structured findings.

The reviewer LLM is intentionally a *different* model from the implementer, so reviews are not blind to the implementer's blind spots.

**Authoritative design:** [`docs/superpowers/specs/2026-05-07-anti-tangent-mcp-design.md`](docs/superpowers/specs/2026-05-07-anti-tangent-mcp-design.md). Anything below is a summary; the spec is the source of truth.

## Common Commands

```bash
# Build
go build ./...

# Run tests with race detector (mainline)
go test -race ./...

# Run e2e tests against real provider APIs (requires keys)
go test -tags=e2e ./...

# Run a single package
go test ./internal/prompts/...

# Update prompt golden files after intentional template changes
go test ./internal/prompts/... -update

# Run the server (it speaks MCP stdio; usually launched by the host)
go run ./cmd/anti-tangent-mcp

# Print version (uses VERSION file via -ldflags)
go run ./cmd/anti-tangent-mcp --version

# Local release artifacts dry-run
goreleaser release --snapshot --clean --skip=publish
```

## Architecture

```text
cmd/anti-tangent-mcp/main.go    # entry: load config, build deps, run MCP server
internal/
  config/      env-driven Config + ModelRef parsing/validation
  verdict/     canonical Result + Finding types, JSON Schema, parser
  session/     TaskSpec, Session, Checkpoint; in-memory store with TTL
  prompts/     embedded pre/mid/post templates; render funcs; golden tests
  providers/   Reviewer interface; allowlist; HTTP clients (anthropic/openai/google)
  mcpsrv/      MCP server: tool registration + 3 handlers + integration test
```

Each package has one responsibility. `cmd/` only wires; logic lives in `internal/`.

## Branch & Version Conventions

Feature work goes on `version/X.Y.Z` branches. The branch name's `X.Y.Z` **must** match a `## [X.Y.Z] - YYYY-MM-DD` entry in `CHANGELOG.md` — CI enforces this.

Pick `X.Y.Z` by the change you're shipping:

- bugfix-only → bump patch (e.g. `0.1.0` → `0.1.1`)
- backward-compatible feature → bump minor (e.g. `0.1.1` → `0.2.0`)
- breaking change → bump major

The merge commit into `main` carries `[major]` or `[minor]` to drive the same bump in the release workflow; default is patch. Branch name and merge bump must agree.

## Changelog Handling

`CHANGELOG.md` follows [Keep a Changelog](https://keepachangelog.com/en/1.0.0/). Use `### Added`, `### Changed`, `### Fixed`, `### Removed`, `### Deprecated`, `### Security` subsections.

Add the entry as you write the code, not at the end. The body of the matching `## [X.Y.Z]` block is what users see on the GitHub Release.

## Adding a Provider or Model

1. Implement the `Reviewer` interface in `internal/providers/<name>.go`. Use plain `net/http`; no vendor SDK.
2. Add the model id to the `allowlist` map in `internal/providers/reviewer.go`.
3. Add an `httptest`-based unit test in `internal/providers/<name>_test.go`.
4. Document the env var (e.g. `<NAME>_API_KEY`) in README and the spec.

## Testing Conventions

- `-race` always on. `go test -race ./...` is the mainline command.
- Unit tests must not hit the network. Use `httptest.Server` for HTTP-shaped tests.
- E2E tests live behind `-tags=e2e`. They are not run on every PR.
- Prompt rendering uses **golden files** in `internal/prompts/testdata/`. Regenerate with `-update` only after intentional template changes; review the diff before committing.

## Logging Conventions

Structured JSON to **stderr only** (stdout is reserved for MCP stdio traffic). One log line per hook call. Set `ANTI_TANGENT_LOG_LEVEL=debug` to also log prompts and provider responses.

## What This Repo Is Not

(Lifted from the spec's non-goals; do not propose features it has already ruled out.)

- No persistent storage. Sessions live in memory and are lost on restart by design.
- No plugin system for custom reviewers.
- No language-specific code analysis. The reviewer is an LLM, not a linter.
- No metrics endpoint, no OTel exporter.
- No automatic correction: the server is advisory, never blocking.
- No queueing. Concurrency is what `sync.RWMutex` gives us.
