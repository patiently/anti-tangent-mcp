# anti-tangent-mcp — Design

**Date:** 2026-05-07
**Status:** Draft — pending spec review

## Purpose

An MCP server that prevents implementing subagents from drifting away from their assigned tasks. It exposes three lifecycle tools the subagent calls before, during, and after implementing work. Each call sends the task spec and the relevant code/spec context to a reviewer LLM (different from the implementing agent) and returns precise, structured feedback.

The server is **advisory**: it returns verdicts and findings, never blocks. Enforcement is the responsibility of the orchestrator and the implementing subagent's system prompt.

## Goals

- Catch unclear, incomplete, or untestable task specs **before** implementation begins.
- Catch scope drift, untouched acceptance criteria, and unaddressed prior findings **during** implementation.
- Validate the final implementation against every acceptance criterion **before** completion is claimed.
- Allow the reviewer LLM to be a different model — and ideally a different provider — from the implementing agent, so reviews are not blind to the implementer's blind spots.
- Be trivial to install and configure: a single binary, env-var configuration, no persistent storage.

## Non-goals

- No automated correction or code generation by the reviewer.
- No persistent storage, no DB, no migrations.
- No metrics endpoint, no OTel exporter (v1).
- No plugin system for custom reviewers (v1).
- No language-specific code analysis — the reviewer is an LLM, not a static analyzer.
- No queueing, no concurrency across sessions beyond what a `sync.RWMutex`-protected map provides.

## Architecture

A single Go binary, internally organized by layer.

```
┌─────────────────────────────────────────────────────────────┐
│                    anti-tangent-mcp (Go binary)             │
│                                                             │
│  ┌──────────────┐                                           │
│  │   mcp/       │  stdio transport, modelcontextprotocol/   │
│  │              │  go-sdk; registers 3 tools                │
│  └──────┬───────┘                                           │
│         │                                                   │
│         ▼                                                   │
│  ┌──────────────┐    ┌──────────────┐    ┌──────────────┐  │
│  │  session/    │◄──►│  prompts/    │───►│  verdict/    │  │
│  │  in-memory   │    │  templates:  │    │ JSON schema, │  │
│  │  store; UUID │    │  pre/mid/post│    │ parser, types│  │
│  └──────────────┘    └──────┬───────┘    └──────────────┘  │
│                             │                               │
│                             ▼                               │
│                     ┌──────────────┐    ┌──────────────┐    │
│                     │ providers/   │    │  config/     │    │
│                     │              │    │  env vars,   │    │
│                     │ anthropic    │    │  defaults    │    │
│                     │ openai       │    └──────────────┘    │
│                     │ google       │                        │
│                     └──────┬───────┘                        │
└────────────────────────────┼────────────────────────────────┘
                             │ HTTPS
                             ▼
                  Anthropic / OpenAI / Google APIs
```

### Request flow (mid_check example)

1. Subagent calls `mid_check` with `session_id`, `working_on`, `changed_files`.
2. `mcp/` handler resolves the session from `session/`, fetches the locked spec.
3. `prompts/` builds the drift-check prompt: spec + delta + finding history.
4. `providers/` dispatches to the configured provider for this hook.
5. `verdict/` parses the JSON response into `{verdict, findings[], next_action}`.
6. `session/` appends a `Checkpoint` to the session history.
7. Handler returns the verdict envelope to the subagent.

### Runtime properties

- Single binary, no external runtime dependencies beyond outbound HTTPS to the chosen provider.
- No DB, no Redis, no filesystem state.
- Sessions live in memory, expire after a configurable TTL (default 4h).
- If the server restarts, in-flight sessions are lost — re-issuing `validate_task_spec` is cheap (~1-2s, one LLM call).

## The 3 MCP tools

All three tools return a common envelope:

```json
{
  "session_id": "uuid",
  "verdict": "pass" | "warn" | "fail",
  "findings": [
    {
      "severity": "critical" | "major" | "minor",
      "category": "missing_acceptance_criterion" | "scope_drift" | "ambiguous_spec" | "unaddressed_finding" | "quality" | "session_not_found" | "payload_too_large" | "other",
      "criterion": "string — which AC or non-goal this maps to (or 'spec' for pre-hook)",
      "evidence": "string — the specific code or spec text that triggered this finding",
      "suggestion": "string — the concrete next action"
    }
  ],
  "next_action": "string — one sentence telling the subagent what to do next",
  "model_used": "anthropic:claude-sonnet-4-6",
  "review_ms": 2341
}
```

### Tool 1 — `validate_task_spec` (pre-hook)

**Description:** *"Validate that a task specification is clear and implementable BEFORE you start coding. Returns findings on missing/ambiguous goals, weak acceptance criteria, and unstated assumptions. Call this once at the start of every task."*

**Input:**
```json
{
  "task_title": "string, required",
  "goal": "string, required — what success looks like",
  "acceptance_criteria": ["string", "..."],
  "non_goals": ["string", "..."],
  "context": "string, optional — relevant background, constraints, links",
  "model_override": "string, optional — e.g. 'openai:gpt-5'"
}
```

**Side effect:** creates a new session and returns its `session_id` in the envelope. A session is created on every `validate_task_spec` call regardless of the verdict, so the subagent has an ID to thread through subsequent hooks. If the subagent revises the spec and calls `validate_task_spec` again, that returns a *new* session with a new ID; the subagent should discard the old ID and use the new one for `check_progress` / `validate_completion`. The session stores the spec exactly as passed; the spec itself is not mutated after creation.

**Reviewer's job:** evaluate
- structural completeness (goal present, ACs present, non-goals present, context where useful);
- AC quality — are they testable, specific, unambiguous? Suggest rewrites for vague items;
- implicit assumptions a fresh implementer would have to make.

There is no code section in this prompt.

### Tool 2 — `check_progress` (mid-hook)

**Description:** *"Check that your in-progress work is staying aligned with the task spec. Call this at natural checkpoints — after a meaningful chunk of code is written, before moving to a new sub-area, or whenever you're unsure whether you're drifting."*

**Input:**
```json
{
  "session_id": "string, required",
  "working_on": "string, required — one sentence: what you just did or are about to do",
  "changed_files": [
    { "path": "src/foo.go", "content": "full file content OR unified diff" }
  ],
  "questions": ["string", "..."],
  "model_override": "string, optional"
}
```

**Reviewer's job:** focus on drift signals.
- Code that doesn't map to any acceptance criterion → scope drift.
- ACs that look untouched and at risk of being missed.
- Prior findings (from pre-hook or earlier mid-checks) that haven't been addressed.
- Mismatch between `working_on` and what the diff actually contains.

The mid-hook prompt explicitly instructs the reviewer **not** to critique code style or polish — that's noise mid-task.

### Tool 3 — `validate_completion` (post-hook)

**Description:** *"Final validation before declaring a task complete. The reviewer checks the full implementation against every acceptance criterion and non-goal. Treat any `fail` or `warn` findings as work to do before claiming done."*

**Input:**
```json
{
  "session_id": "string, required",
  "summary": "string, required — what you implemented, in your own words",
  "final_files": [
    { "path": "src/foo.go", "content": "full file content" }
  ],
  "test_evidence": "string, optional — output of test runs, build logs, etc.",
  "model_override": "string, optional"
}
```

**Reviewer's job:** rigorous AC-by-AC walkthrough. Each AC gets a pass-with-evidence or a finding citing the gap. Non-goals are checked for accidental violations. If `test_evidence` is provided, it is cross-checked against the AC list.

### Notes on the surface

- File content can be passed as full files **or** unified diffs (`*** Begin Patch` style). The prompt template handles both.
- No `delete_session` tool: sessions auto-expire.
- `model_override` syntax: `<provider>:<model_id>`. Validated at startup against an internal allowlist; same validator runs on per-call overrides.

## Session lifecycle & state

### Session struct (in-memory only)

```go
type Session struct {
    ID             string         // UUIDv4
    CreatedAt      time.Time
    LastAccessed   time.Time      // updated on every hook call; drives TTL
    Spec           TaskSpec       // frozen at pre-hook time
    PreFindings    []Finding
    MidCheckpoints []Checkpoint   // append-only summary log
    PostFindings   []Finding
    ModelDefaults  ModelConfig    // captured at session creation
}

type Checkpoint struct {
    At        time.Time
    WorkingOn string
    FileCount int
    Verdict   string
    Findings  []Finding
}
```

The full diff/file content from each call is **not** stored — only the resulting checkpoint summary. This bounds memory regardless of session length and avoids retaining user code in process memory longer than the request.

### Store

A single `sessionStore` type wrapping `map[string]*Session` behind a `sync.RWMutex`. Adequate for the load profile (one subagent per session, low call frequency).

### Lifecycle

1. **Create** — `validate_task_spec` generates a UUIDv4, builds the session, stores it, returns the ID in the response envelope.
2. **Access** — `check_progress` and `validate_completion` look up by ID. On miss → return a `verdict: "fail"` envelope with a single `session_not_found` finding telling the subagent to call `validate_task_spec` first. On hit → update `LastAccessed`.
3. **Expire** — a background goroutine runs every 5 minutes, evicting sessions where `now - LastAccessed > TTL` (default 4h, env-configurable).
4. **Shutdown** — sessions are dropped on process exit.

### Concurrency

The mutex protects the map. Hook calls within a single session are expected to be sequential (one subagent doing one thing at a time). We do not add per-session locks; if two `check_progress` calls race for the same session, the worst case is interleaved appends to `MidCheckpoints`, which is fine because checkpoints are ordered by `At`.

### Why no persistence

- The MCP server's lifetime is bound to a coding session; host restart → fresh server.
- Crash recovery isn't valuable: re-running `validate_task_spec` is cheap.
- Persistence introduces real surface area (schema migration, file locking on Windows, location decisions) without proportional benefit for v1.

### Failure-mode mapping

| Situation | Response |
|---|---|
| Unknown `session_id` on mid/post | Envelope with `verdict: "fail"`, single `session_not_found` finding, `next_action` = "Call `validate_task_spec` first" |
| Session expired | Same as above (no distinction; cure is identical) |
| Provider call fails (network, 5xx, rate limit) | MCP `isError: true` with provider-error detail. Subagent can retry or escalate. We do **not** fabricate a verdict. |
| Provider returns malformed JSON | One retry with a "respond with valid JSON only" reminder; if still bad → MCP error |
| Payload exceeds size cap | Envelope with `verdict: "fail"`, `payload_too_large` finding, `next_action` instructing the subagent to send a unified diff or split the call |

## Providers & prompt strategy

### The `Reviewer` interface

```go
type Reviewer interface {
    Name() string                 // "anthropic" | "openai" | "google"
    Review(ctx context.Context, req ReviewRequest) (ReviewResponse, error)
}

type ReviewRequest struct {
    Model      string
    System     string
    User       string
    MaxTokens  int
    JSONSchema []byte
}

type ReviewResponse struct {
    RawJSON      []byte
    Model        string
    InputTokens  int
    OutputTokens int
}
```

Each provider is implemented with **plain `net/http`**, not the vendor SDK. This keeps the binary small (~5MB vs ~30MB+ with all three SDKs), makes testing with `httptest.Server` straightforward, and avoids inheriting each SDK's opinions about retries and telemetry.

### Structured-output strategy per provider

| Provider | Mechanism |
|---|---|
| Anthropic | `tool_choice` forced to a single tool whose `input_schema` is the verdict schema |
| OpenAI | `response_format: { type: "json_schema", json_schema: {...} }` (Structured Outputs) |
| Google | `response_mime_type: "application/json"` + `response_schema` on `generationConfig` |

If parsing fails, `verdict/` does **one** retry with an appended *"respond with only the JSON object"* reminder. Beyond that, it's an error to the caller — no fabricated verdicts.

### Per-hook model defaults

```
ANTI_TANGENT_PRE_MODEL=anthropic:claude-sonnet-4-6
ANTI_TANGENT_MID_MODEL=anthropic:claude-haiku-4-5     # cheap, fast, called often
ANTI_TANGENT_POST_MODEL=anthropic:claude-opus-4-7     # most rigorous
```

Each `<provider>:<model_id>` is validated at startup against an internal allowlist (a Go slice in `providers/`). Adding a new model is a one-line change. Per-call `model_override` goes through the same validator.

API keys: `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GOOGLE_API_KEY`. Only providers with a key present are activated; calling a missing-keyed provider returns a clear error.

### Prompt strategy

Each hook has its own embedded prompt template (`//go:embed prompts/templates/*.tmpl`). Common skeleton:

```
SYSTEM: You are an exacting reviewer. You return ONLY a JSON object
matching the provided schema. You give specific, evidence-backed findings.
You never invent facts about code that wasn't shown to you.

USER:
## Task spec
<rendered spec>

## Prior findings (if any, mid/post only)
<rendered finding history>

## What to evaluate
<hook-specific>

## Code under review (mid/post only)
<files / diffs>

## Hook-specific instructions
<the unique instructions for this hook>

Respond with the verdict JSON only.
```

**Hook-specific differences:**

- **`pre`**: evaluate completeness, AC quality (testable, specific, unambiguous), and unstated assumptions. No code section. `criterion` field on each finding is `"spec"` or the AC text being critiqued.
- **`mid`**: focus on drift signals. Avoid critiquing code style.
- **`post`**: walk every AC and every non-goal explicitly. Each finding's `criterion` is the verbatim AC text. Cross-check `test_evidence` against the AC list when provided.

### Token-budget guardrails

The MCP handler enforces a soft cap on the size of `changed_files` / `final_files` content per call. Default 200KB total (`ANTI_TANGENT_MAX_PAYLOAD_BYTES=204800`). On exceed, return `verdict: "fail"` with a `payload_too_large` finding — honest failure beats a silent truncated review.

## Configuration

Env vars only. No config file in v1.

```
# Provider keys (at least one required)
ANTHROPIC_API_KEY=sk-ant-...
OPENAI_API_KEY=sk-...
GOOGLE_API_KEY=...

# Per-hook model defaults (optional)
ANTI_TANGENT_PRE_MODEL=anthropic:claude-sonnet-4-6
ANTI_TANGENT_MID_MODEL=anthropic:claude-haiku-4-5
ANTI_TANGENT_POST_MODEL=anthropic:claude-opus-4-7

# Tunables (optional)
ANTI_TANGENT_SESSION_TTL=4h
ANTI_TANGENT_MAX_PAYLOAD_BYTES=204800
ANTI_TANGENT_REQUEST_TIMEOUT=120s
ANTI_TANGENT_LOG_LEVEL=info
```

Validation runs at startup. If no provider key is set, the server fails fast: *"Set at least one of ANTHROPIC_API_KEY, OPENAI_API_KEY, GOOGLE_API_KEY."*

## Distribution

In priority order:

1. `go install github.com/<user>/anti-tangent-mcp@latest` — primary path.
2. GoReleaser pre-built binaries for linux/amd64, linux/arm64, darwin/amd64, darwin/arm64, windows/amd64. Published to GitHub Releases.
3. Container image (`ghcr.io/<user>/anti-tangent-mcp:latest`).

Sample `.mcp.json` snippet shipped in the README:
```json
{
  "mcpServers": {
    "anti-tangent": {
      "command": "anti-tangent-mcp",
      "env": { "ANTHROPIC_API_KEY": "...", "OPENAI_API_KEY": "..." }
    }
  }
}
```

## Versioning, changelog, and release automation

Modeled on the convention used in the `powow` repo: a `VERSION` file holds the current released version, `CHANGELOG.md` follows [Keep a Changelog](https://keepachangelog.com/en/1.0.0/), and the GitHub Actions release workflow does the bumping, tagging, and artifact publishing.

### Version source of truth

A single `VERSION` file at the repo root contains the current version, e.g.:
```
0.1.0
```
This is the only place the version lives. The Go build embeds it via `-ldflags "-X main.version=$(cat VERSION)"` so `anti-tangent-mcp --version` reports it.

### Branch convention

Feature work happens on branches named `version/X.Y.Z`, where `X.Y.Z` is the version that branch will become when merged. The branch name **must** match a `## [X.Y.Z] - YYYY-MM-DD` entry in `CHANGELOG.md` — CI enforces this on every push to a `version/*` branch.

Other branches (e.g., `dependabot/*`, `docs/*`, anything that isn't shipping a release) skip the changelog check entirely. This keeps the rule cheap and unambiguous.

### Commit-message-driven version bumps

When a `version/X.Y.Z` branch is merged into `main`, the release workflow bumps `VERSION` based on the merge commit message:

| Commit message contains | Bump |
|---|---|
| `[major]` | major: `X+1.0.0` |
| `[minor]` | minor: `X.Y+1.0` |
| neither | patch: `X.Y.Z+1` |

The branch name and the merge bump together must produce the same version that `CHANGELOG.md` declares — release fails if they diverge.

### CHANGELOG.md format

```
# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0] - 2026-05-07

### Added
- Initial release: three MCP tools (`validate_task_spec`, `check_progress`,
  `validate_completion`) backed by Anthropic / OpenAI / Google reviewers.
```

Each release section uses standard Keep-a-Changelog subsections (`### Added`, `### Changed`, `### Fixed`, `### Removed`, `### Deprecated`, `### Security`). The release workflow extracts the body of the matching `## [X.Y.Z]` block and uses it verbatim as the GitHub Release notes.

### CI workflow — `.github/workflows/ci.yml`

Runs on every push and pull request. `workflow_call`-able so the release workflow can reuse it.

Jobs:

1. **`changelog`** — only enforces on `version/X.Y.Z` branches. Greps `CHANGELOG.md` for `^## [X.Y.Z]`. Fails with a clear message if missing.
2. **`build-test`** — sets up Go (`actions/setup-go@v6`, version pinned in `go.mod` toolchain directive), runs `go mod download`, `go build ./...`, `go test -race ./...`. Uses `gotest.tools/gotestsum@latest` for readable output and uploads `test-results.xml` as an artifact. No external services required (unit tests use `httptest`; the e2e `-tags=e2e` job is **not** part of mainline CI).

### Release workflow — `.github/workflows/release.yml`

Trigger: `push` to `main`. Reuses `ci.yml` first; downstream jobs are gated on it passing.

Jobs (in order, each `needs:` the previous):

1. **`ci`** — `uses: ./.github/workflows/ci.yml`. Same checks every PR runs.
2. **`version`** — checks out the repo with full history, computes `new_version` from `VERSION` + the merge commit message tag (`[major]` / `[minor]` / patch default), **validates that `CHANGELOG.md` contains `## [<new_version>]`**, and extracts the changelog body for that version into a `release_notes` step output (via `awk`, same approach as powow). Outputs `current_version`, `new_version`, `release_notes` for downstream jobs.
3. **`tag`** — bot-commits the new `VERSION` with `[skip ci]` to keep the loop closed, then creates and pushes the `vX.Y.Z` tag. Does **not** create a GitHub Release yet — that's GoReleaser's job so we don't fight over the release object.
4. **`goreleaser`** — checks out at the new tag, writes the `release_notes` from the `version` job to a temp file, runs `goreleaser release --release-notes=<file>`. GoReleaser creates the GitHub Release using our extracted changelog as the body (its own changelog generation is disabled in `.goreleaser.yaml`), builds binaries for linux/amd64, linux/arm64, darwin/amd64, darwin/arm64, windows/amd64, and attaches them with checksums. No code-signing in v1.
5. **`docker`** — checks out at the new tag, logs in to `ghcr.io` with `GITHUB_TOKEN`, builds and pushes the image to `ghcr.io/<owner>/anti-tangent-mcp:X.Y.Z` and `:latest` using `docker/build-push-action@v7`.

Permissions: `tag` and `goreleaser` need `contents: write`; `docker` needs `packages: write`. Each job declares only the permissions it needs.

### `.goreleaser.yaml` outline

```yaml
version: 2
project_name: anti-tangent-mcp
builds:
  - main: ./cmd/anti-tangent-mcp
    binary: anti-tangent-mcp
    env: [CGO_ENABLED=0]
    goos: [linux, darwin, windows]
    goarch: [amd64, arm64]
    ldflags: ["-s -w -X main.version={{.Version}}"]
archives:
  - format: tar.gz
    format_overrides:
      - goos: windows
        format: zip
checksum:
  name_template: "checksums.txt"
changelog:
  disable: true   # we supply our own notes via --release-notes
release:
  github:
    owner: <user>
    name: anti-tangent-mcp
```

GoReleaser is the single owner of the GitHub Release object. The `tag` job only pushes the tag; GoReleaser creates the release at that tag and uses our hand-written `CHANGELOG.md` section as the release body (passed via `--release-notes`). Disabling GoReleaser's own changelog generation prevents it from appending an auto-generated commit list to our curated notes.

### Initial release

The first release is `0.1.0`. The repo lands with:
- `VERSION` containing `0.1.0`
- `CHANGELOG.md` with a single `## [0.1.0] - 2026-05-07` entry summarizing the v1 surface
- `.github/workflows/ci.yml` and `release.yml`
- `.goreleaser.yaml`
- `Dockerfile`

Cutting `0.1.0` is just the first time `main` advances past the initial scaffold and triggers the release workflow.

### What's deliberately missing

- No release-please, no semantic-release, no auto-generated changelog. The `[major]`/`[minor]` commit tag is the entire automation surface — easy to read, easy to override, no third-party tool to keep up with.
- No prerelease channel (`alpha`/`rc` tags) in v1. If we need one later, it slots in as a separate workflow keyed on tag pattern.
- No code-signing, no notarization, no Homebrew tap. All deferable; `go install` and the GHCR image are sufficient distribution for v1.

## Logging & observability

Structured JSON logs to stderr (stdout is reserved for MCP stdio traffic). One log line per hook call: `session_id`, `hook`, `model`, `verdict`, `findings_count`, `input_tokens`, `output_tokens`, `review_ms`. `ANTI_TANGENT_LOG_LEVEL=debug` also logs prompts and provider responses.

No metrics endpoint or OTel exporter in v1.

## Error handling philosophy

- **Configuration errors** (missing keys, invalid model spec): fail fast at startup with clear messages.
- **Per-call errors** (network, provider 5xx, rate limit, malformed JSON after retry): return MCP `isError: true` with the underlying cause.
- **Logical errors** (unknown session, oversized payload): return a normal envelope with `verdict: "fail"` and a single structured finding. These are part of normal operation, not exceptional.
- **Never fabricate a verdict.** If the reviewer LLM didn't produce a valid response, the tool surfaces an error rather than inventing a "pass."

## Testing strategy

Three layers, each runnable via `go test`.

1. **Unit — per-package, no network**
   - `verdict/`: parser handles well-formed, malformed, and partial JSON.
   - `session/`: store concurrency (`-race`), TTL eviction, lookup miss/hit.
   - `prompts/`: golden-file tests — given fixed inputs, the rendered prompt is byte-stable.
   - `providers/`: each provider tested against `httptest.Server` returning known fixtures.

2. **Integration — local, mock provider**
   - Spin up the full MCP server with stdio transport, point it at a fake provider returning canned JSON. Drive the 3 tools end-to-end with the official MCP test client. Verifies session threading and the response envelope.

3. **End-to-end — real providers, opt-in**
   - Behind `-tags=e2e`. One scenario per provider: tiny task spec, tiny diff, real review. Catches schema drift in providers' structured-output behavior. Designed to run in CI nightly, not on every PR.

## Project layout

```
anti-tangent-mcp/
├── cmd/anti-tangent-mcp/main.go    # entry point, wiring
├── internal/
│   ├── mcp/                        # tool registration & handlers
│   ├── session/
│   ├── prompts/
│   │   └── templates/{pre,mid,post}.tmpl
│   ├── verdict/
│   ├── providers/
│   │   ├── anthropic.go
│   │   ├── openai.go
│   │   └── google.go
│   └── config/
├── .github/workflows/
│   ├── ci.yml
│   └── release.yml
├── docs/superpowers/specs/         # this design doc
├── .goreleaser.yaml
├── Dockerfile
├── VERSION
├── CHANGELOG.md
├── go.mod
└── README.md
```

`internal/` keeps everything unimportable from the outside — we ship a binary, not a library.

## Out of scope (deferred)

- Plugin architecture for custom reviewers.
- Persistent sessions / SQLite.
- Local-only providers (Ollama, llama.cpp).
- Metrics/OTel.
- Strict-mode enforcement (returning MCP errors on `fail` verdicts).
- A web UI for inspecting session history.
- Built-in static analysis or test execution.
