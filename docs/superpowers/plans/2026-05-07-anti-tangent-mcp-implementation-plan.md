# anti-tangent-mcp Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build an advisory MCP server in Go that validates task specs and reviews implementing-subagent work at three lifecycle points (pre/mid/post), routing reviews through Anthropic/OpenAI/Google as configured.

**Architecture:** Single Go binary, layered modular monolith. `mcp/` exposes three tools over MCP stdio; `session/` stores per-task context in-memory; `prompts/` renders hook-specific templates; `providers/` ships hand-rolled HTTP clients for the three vendors; `verdict/` defines the JSON schema and parses provider responses.

**Tech Stack:** Go 1.24, `github.com/modelcontextprotocol/go-sdk`, `github.com/google/uuid`, `github.com/stretchr/testify` (tests only), `log/slog` (stdlib JSON logging). GoReleaser + GoReleaser-config-driven Docker builds. GitHub Actions for CI/release.

**Authoritative spec:** `docs/superpowers/specs/2026-05-07-anti-tangent-mcp-design.md` — read this first. The plan implements that spec; if any divergence is needed during execution, update the spec, not just the code.

---

## File map

```
anti-tangent-mcp/
├── cmd/anti-tangent-mcp/
│   └── main.go                          # entry: load config, build deps, run MCP server
├── internal/
│   ├── config/
│   │   ├── config.go                    # Config struct + Load() from env
│   │   └── config_test.go
│   ├── verdict/
│   │   ├── verdict.go                   # Verdict, Finding, Result types + JSON schema
│   │   ├── parser.go                    # parse RawJSON → Result, with retry hint
│   │   └── verdict_test.go
│   ├── session/
│   │   ├── session.go                   # TaskSpec, Session, Checkpoint types
│   │   ├── store.go                     # in-memory store with TTL eviction
│   │   └── store_test.go
│   ├── prompts/
│   │   ├── prompts.go                   # Render funcs, embedded templates
│   │   ├── templates/
│   │   │   ├── pre.tmpl
│   │   │   ├── mid.tmpl
│   │   │   └── post.tmpl
│   │   ├── testdata/                    # golden files
│   │   └── prompts_test.go
│   ├── providers/
│   │   ├── reviewer.go                  # Reviewer interface, Request/Response, allowlist
│   │   ├── reviewer_test.go             # parses ModelRef, validates allowlist
│   │   ├── anthropic.go
│   │   ├── anthropic_test.go
│   │   ├── openai.go
│   │   ├── openai_test.go
│   │   ├── google.go
│   │   └── google_test.go
│   └── mcpsrv/
│       ├── server.go                    # NewServer, register the 3 tools
│       ├── handlers.go                  # the 3 tool handlers
│       └── handlers_test.go             # uses fake Reviewer
├── .github/workflows/
│   ├── ci.yml
│   └── release.yml
├── docs/superpowers/specs/2026-05-07-anti-tangent-mcp-design.md   (already exists)
├── docs/superpowers/plans/2026-05-07-anti-tangent-mcp-implementation-plan.md  (this file)
├── .goreleaser.yaml
├── .gitignore
├── Dockerfile
├── VERSION
├── CHANGELOG.md
├── CLAUDE.md
├── README.md
├── go.mod
└── go.sum
```

The MCP package is named `mcpsrv` (not `mcp`) to avoid colliding with imports of the SDK's `mcp` package.

---

## Task 1: Bootstrap repo

**Files:**
- Create: `go.mod`
- Create: `.gitignore`
- Create: `VERSION`
- Create: `CHANGELOG.md`
- Create: `README.md` (placeholder, filled in Task 25)
- Create: `CLAUDE.md` (placeholder, filled in Task 26)

- [ ] **Step 1: Initialize Go module**

Run from repo root:
```bash
go mod init github.com/patiently/anti-tangent-mcp
go mod edit -go=1.24
```

- [ ] **Step 2: Create `.gitignore`**

```
# Build artifacts
/anti-tangent-mcp
/dist/
*.exe

# Test outputs
*.test
*.out
coverage.*

# Editor / OS
.idea/
.vscode/
.DS_Store

# Local env
.env
.env.local

# GoReleaser snapshot
/dist
```

- [ ] **Step 3: Create `VERSION`**

```
0.1.0
```

(No trailing newline-discipline matters; a single trailing `\n` is fine.)

- [ ] **Step 4: Create `CHANGELOG.md` skeleton**

```markdown
# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0] - 2026-05-07

### Added
- Initial release scaffold. Tools, providers, and release workflow are
  filled in by subsequent tasks; this entry is rewritten in Task 27 once
  the implementation is complete.
```

- [ ] **Step 5: Create stub `README.md` and `CLAUDE.md`**

```markdown
<!-- README.md -->
# anti-tangent-mcp

Advisory MCP server that reviews task specs and implementing-subagent work at
three lifecycle points (pre/mid/post). See
[`docs/superpowers/specs/2026-05-07-anti-tangent-mcp-design.md`](docs/superpowers/specs/2026-05-07-anti-tangent-mcp-design.md).

Detailed README written in Task 25.
```

```markdown
<!-- CLAUDE.md -->
# CLAUDE.md

Filled in by Task 26.
```

- [ ] **Step 6: Commit the scaffold**

```bash
git add go.mod .gitignore VERSION CHANGELOG.md README.md CLAUDE.md
git commit -m "chore: bootstrap repo (go.mod, VERSION, CHANGELOG skeleton)"
```

---

## Task 2: Config package

**Files:**
- Create: `internal/config/config.go`
- Create: `internal/config/config_test.go`

The config package holds env-driven configuration. It exposes one `ModelRef` type used across `providers/` and `mcpsrv/`, plus a `Load(env func(string) string) (Config, error)` that's easy to test (we inject a fake env lookup).

- [ ] **Step 1: Write the failing test**

Create `internal/config/config_test.go`:
```go
package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoad_Defaults(t *testing.T) {
	cfg, err := Load(env(map[string]string{
		"ANTHROPIC_API_KEY": "sk-ant-test",
	}))
	require.NoError(t, err)
	assert.Equal(t, "sk-ant-test", cfg.AnthropicKey)
	assert.Equal(t, ModelRef{Provider: "anthropic", Model: "claude-sonnet-4-6"}, cfg.PreModel)
	assert.Equal(t, ModelRef{Provider: "anthropic", Model: "claude-haiku-4-5"}, cfg.MidModel)
	assert.Equal(t, ModelRef{Provider: "anthropic", Model: "claude-opus-4-7"}, cfg.PostModel)
	assert.Equal(t, 4*time.Hour, cfg.SessionTTL)
	assert.Equal(t, 204800, cfg.MaxPayloadBytes)
	assert.Equal(t, 120*time.Second, cfg.RequestTimeout)
}

func TestLoad_Overrides(t *testing.T) {
	cfg, err := Load(env(map[string]string{
		"OPENAI_API_KEY":               "sk-test",
		"ANTI_TANGENT_PRE_MODEL":       "openai:gpt-5",
		"ANTI_TANGENT_SESSION_TTL":     "30m",
		"ANTI_TANGENT_MAX_PAYLOAD_BYTES": "1024",
	}))
	require.NoError(t, err)
	assert.Equal(t, ModelRef{Provider: "openai", Model: "gpt-5"}, cfg.PreModel)
	assert.Equal(t, 30*time.Minute, cfg.SessionTTL)
	assert.Equal(t, 1024, cfg.MaxPayloadBytes)
}

func TestLoad_NoKeys(t *testing.T) {
	_, err := Load(env(map[string]string{}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one of")
}

func TestLoad_BadModelRef(t *testing.T) {
	_, err := Load(env(map[string]string{
		"ANTHROPIC_API_KEY":      "x",
		"ANTI_TANGENT_PRE_MODEL": "no-colon",
	}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected provider:model")
}

func TestParseModelRef(t *testing.T) {
	mr, err := ParseModelRef("anthropic:claude-opus-4-7")
	require.NoError(t, err)
	assert.Equal(t, ModelRef{Provider: "anthropic", Model: "claude-opus-4-7"}, mr)
	assert.Equal(t, "anthropic:claude-opus-4-7", mr.String())

	_, err = ParseModelRef("bad")
	require.Error(t, err)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/...`
Expected: FAIL — `config` package does not exist yet.

- [ ] **Step 3: Implement `config.go`**

Create `internal/config/config.go`:
```go
// Package config loads runtime configuration from environment variables.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	AnthropicKey    string
	OpenAIKey       string
	GoogleKey       string
	PreModel        ModelRef
	MidModel        ModelRef
	PostModel       ModelRef
	SessionTTL      time.Duration
	MaxPayloadBytes int
	RequestTimeout  time.Duration
	LogLevel        slog.Level
}

type ModelRef struct {
	Provider string
	Model    string
}

func (m ModelRef) String() string { return m.Provider + ":" + m.Model }

func ParseModelRef(s string) (ModelRef, error) {
	provider, model, ok := strings.Cut(s, ":")
	if !ok || provider == "" || model == "" {
		return ModelRef{}, fmt.Errorf("invalid model ref %q: expected provider:model", s)
	}
	return ModelRef{Provider: provider, Model: model}, nil
}

// Load reads configuration from the given env lookup function.
// Pass os.Getenv in production; pass a map-backed function in tests.
func Load(env func(string) string) (Config, error) {
	cfg := Config{
		AnthropicKey:    env("ANTHROPIC_API_KEY"),
		OpenAIKey:       env("OPENAI_API_KEY"),
		GoogleKey:       env("GOOGLE_API_KEY"),
		SessionTTL:      4 * time.Hour,
		MaxPayloadBytes: 204800,
		RequestTimeout:  120 * time.Second,
		LogLevel:        slog.LevelInfo,
	}

	if cfg.AnthropicKey == "" && cfg.OpenAIKey == "" && cfg.GoogleKey == "" {
		return Config{}, errors.New("at least one of ANTHROPIC_API_KEY, OPENAI_API_KEY, GOOGLE_API_KEY must be set")
	}

	defaults := map[*ModelRef][2]string{
		&cfg.PreModel:  {"ANTI_TANGENT_PRE_MODEL", "anthropic:claude-sonnet-4-6"},
		&cfg.MidModel:  {"ANTI_TANGENT_MID_MODEL", "anthropic:claude-haiku-4-5"},
		&cfg.PostModel: {"ANTI_TANGENT_POST_MODEL", "anthropic:claude-opus-4-7"},
	}
	for ptr, spec := range defaults {
		val := env(spec[0])
		if val == "" {
			val = spec[1]
		}
		mr, err := ParseModelRef(val)
		if err != nil {
			return Config{}, fmt.Errorf("%s: %w", spec[0], err)
		}
		*ptr = mr
	}

	if v := env("ANTI_TANGENT_SESSION_TTL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, fmt.Errorf("ANTI_TANGENT_SESSION_TTL: %w", err)
		}
		cfg.SessionTTL = d
	}
	if v := env("ANTI_TANGENT_MAX_PAYLOAD_BYTES"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("ANTI_TANGENT_MAX_PAYLOAD_BYTES: %w", err)
		}
		cfg.MaxPayloadBytes = n
	}
	if v := env("ANTI_TANGENT_REQUEST_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, fmt.Errorf("ANTI_TANGENT_REQUEST_TIMEOUT: %w", err)
		}
		cfg.RequestTimeout = d
	}
	if v := env("ANTI_TANGENT_LOG_LEVEL"); v != "" {
		switch strings.ToLower(v) {
		case "debug":
			cfg.LogLevel = slog.LevelDebug
		case "info":
			cfg.LogLevel = slog.LevelInfo
		case "warn":
			cfg.LogLevel = slog.LevelWarn
		case "error":
			cfg.LogLevel = slog.LevelError
		default:
			return Config{}, fmt.Errorf("ANTI_TANGENT_LOG_LEVEL: unknown level %q", v)
		}
	}

	return cfg, nil
}
```

- [ ] **Step 4: Add testify dependency**

```bash
go get github.com/stretchr/testify
```

- [ ] **Step 5: Run tests, confirm pass**

Run: `go test ./internal/config/... -race -v`
Expected: All four tests PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/config/ go.mod go.sum
git commit -m "feat(config): env-driven Config with model-ref parsing"
```

---

## Task 3: Verdict types and JSON schema

**Files:**
- Create: `internal/verdict/verdict.go`
- Create: `internal/verdict/verdict_test.go`

This package owns the canonical shape of reviewer output. Providers will be told to produce JSON matching `Schema()`.

- [ ] **Step 1: Write the failing test**

Create `internal/verdict/verdict_test.go`:
```go
package verdict

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSchema_IsValidJSON(t *testing.T) {
	var schema map[string]any
	require.NoError(t, json.Unmarshal(Schema(), &schema))
	assert.Equal(t, "object", schema["type"])
	props, ok := schema["properties"].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, props, "verdict")
	assert.Contains(t, props, "findings")
	assert.Contains(t, props, "next_action")
}

func TestVerdictConstants(t *testing.T) {
	assert.Equal(t, Verdict("pass"), VerdictPass)
	assert.Equal(t, Verdict("warn"), VerdictWarn)
	assert.Equal(t, Verdict("fail"), VerdictFail)
}

func TestResult_RoundTripsJSON(t *testing.T) {
	r := Result{
		Verdict: VerdictWarn,
		Findings: []Finding{{
			Severity:   SeverityMajor,
			Category:   CategoryScopeDrift,
			Criterion:  "AC #2: must reject empty payloads",
			Evidence:   "handler.go: no length check",
			Suggestion: "Add `if len(body) == 0 { return errEmpty }` at line 42",
		}},
		NextAction: "Add the length check and re-run check_progress.",
	}
	b, err := json.Marshal(r)
	require.NoError(t, err)
	var back Result
	require.NoError(t, json.Unmarshal(b, &back))
	assert.Equal(t, r, back)
}
```

- [ ] **Step 2: Run, confirm fail**

Run: `go test ./internal/verdict/...`
Expected: FAIL (package doesn't exist).

- [ ] **Step 3: Implement `verdict.go`**

Create `internal/verdict/verdict.go`:
```go
// Package verdict defines the canonical shape of reviewer output and the
// JSON schema used to constrain provider responses.
package verdict

import _ "embed"

type Verdict string

const (
	VerdictPass Verdict = "pass"
	VerdictWarn Verdict = "warn"
	VerdictFail Verdict = "fail"
)

type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityMajor    Severity = "major"
	SeverityMinor    Severity = "minor"
)

type Category string

const (
	CategoryMissingAC      Category = "missing_acceptance_criterion"
	CategoryScopeDrift     Category = "scope_drift"
	CategoryAmbiguousSpec  Category = "ambiguous_spec"
	CategoryUnaddressed    Category = "unaddressed_finding"
	CategoryQuality        Category = "quality"
	CategorySessionMissing Category = "session_not_found"
	CategoryTooLarge       Category = "payload_too_large"
	CategoryOther          Category = "other"
)

type Finding struct {
	Severity   Severity `json:"severity"`
	Category   Category `json:"category"`
	Criterion  string   `json:"criterion"`
	Evidence   string   `json:"evidence"`
	Suggestion string   `json:"suggestion"`
}

type Result struct {
	Verdict    Verdict   `json:"verdict"`
	Findings   []Finding `json:"findings"`
	NextAction string    `json:"next_action"`
}

//go:embed schema.json
var schema []byte

// Schema returns the JSON Schema (draft-07-compatible subset) describing Result.
// Providers are instructed to produce output matching this shape.
func Schema() []byte { return schema }
```

- [ ] **Step 4: Create `internal/verdict/schema.json`**

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "title": "Result",
  "type": "object",
  "required": ["verdict", "findings", "next_action"],
  "additionalProperties": false,
  "properties": {
    "verdict": {
      "type": "string",
      "enum": ["pass", "warn", "fail"]
    },
    "findings": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["severity", "category", "criterion", "evidence", "suggestion"],
        "additionalProperties": false,
        "properties": {
          "severity": { "type": "string", "enum": ["critical", "major", "minor"] },
          "category": {
            "type": "string",
            "enum": [
              "missing_acceptance_criterion",
              "scope_drift",
              "ambiguous_spec",
              "unaddressed_finding",
              "quality",
              "session_not_found",
              "payload_too_large",
              "other"
            ]
          },
          "criterion":  { "type": "string" },
          "evidence":   { "type": "string" },
          "suggestion": { "type": "string" }
        }
      }
    },
    "next_action": { "type": "string" }
  }
}
```

- [ ] **Step 5: Run, confirm pass**

Run: `go test ./internal/verdict/... -race -v`
Expected: All three tests PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/verdict/
git commit -m "feat(verdict): canonical Result type + JSON schema"
```

---

## Task 4: Verdict parser

**Files:**
- Create: `internal/verdict/parser.go`
- Modify: `internal/verdict/verdict_test.go` (append parser tests)

The parser turns provider raw JSON into a `Result`, and exposes a `RetryHint` we use when the response doesn't validate.

- [ ] **Step 1: Append failing tests**

Add to `internal/verdict/verdict_test.go`:
```go
func TestParse_ValidJSON(t *testing.T) {
	in := []byte(`{"verdict":"pass","findings":[],"next_action":"ship it"}`)
	r, err := Parse(in)
	require.NoError(t, err)
	assert.Equal(t, VerdictPass, r.Verdict)
	assert.Equal(t, "ship it", r.NextAction)
}

func TestParse_InvalidEnum(t *testing.T) {
	in := []byte(`{"verdict":"maybe","findings":[],"next_action":""}`)
	_, err := Parse(in)
	require.Error(t, err)
}

func TestParse_MalformedJSON(t *testing.T) {
	_, err := Parse([]byte(`{not json`))
	require.Error(t, err)
}

func TestParse_StripsCodeFences(t *testing.T) {
	// Some providers wrap the JSON in ```json fences despite instructions.
	in := []byte("```json\n{\"verdict\":\"pass\",\"findings\":[],\"next_action\":\"ok\"}\n```")
	r, err := Parse(in)
	require.NoError(t, err)
	assert.Equal(t, VerdictPass, r.Verdict)
}

func TestRetryHint(t *testing.T) {
	hint := RetryHint()
	assert.Contains(t, hint, "JSON")
}
```

- [ ] **Step 2: Run, confirm fail**

Run: `go test ./internal/verdict/...`
Expected: FAIL — `Parse` and `RetryHint` are undefined.

- [ ] **Step 3: Implement `parser.go`**

Create `internal/verdict/parser.go`:
```go
package verdict

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// Parse decodes provider output into a Result and validates enum fields.
// It tolerates a `\`\`\`json ... \`\`\`` wrapper and surrounding whitespace.
func Parse(raw []byte) (Result, error) {
	body := stripFences(bytes.TrimSpace(raw))
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	var r Result
	if err := dec.Decode(&r); err != nil {
		return Result{}, fmt.Errorf("decode result: %w", err)
	}
	switch r.Verdict {
	case VerdictPass, VerdictWarn, VerdictFail:
	default:
		return Result{}, fmt.Errorf("invalid verdict %q", r.Verdict)
	}
	for i, f := range r.Findings {
		switch f.Severity {
		case SeverityCritical, SeverityMajor, SeverityMinor:
		default:
			return Result{}, fmt.Errorf("finding[%d]: invalid severity %q", i, f.Severity)
		}
		if !validCategory(f.Category) {
			return Result{}, fmt.Errorf("finding[%d]: invalid category %q", i, f.Category)
		}
	}
	return r, nil
}

func validCategory(c Category) bool {
	switch c {
	case CategoryMissingAC, CategoryScopeDrift, CategoryAmbiguousSpec,
		CategoryUnaddressed, CategoryQuality, CategorySessionMissing,
		CategoryTooLarge, CategoryOther:
		return true
	}
	return false
}

func stripFences(b []byte) []byte {
	s := string(b)
	if !strings.HasPrefix(s, "```") {
		return b
	}
	// strip leading fence (with optional language tag)
	if nl := strings.IndexByte(s, '\n'); nl != -1 {
		s = s[nl+1:]
	}
	s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	return []byte(strings.TrimSpace(s))
}

// RetryHint is the system-side instruction we append when reissuing
// the prompt after a failed parse.
func RetryHint() string {
	return "Your previous response was not valid JSON matching the schema. " +
		"Respond with ONLY the JSON object, no commentary, no code fences."
}
```

- [ ] **Step 4: Run, confirm pass**

Run: `go test ./internal/verdict/... -race -v`
Expected: All tests PASS (originals + new ones).

- [ ] **Step 5: Commit**

```bash
git add internal/verdict/
git commit -m "feat(verdict): tolerant JSON parser + retry hint"
```

---

## Task 5: Session types and store

**Files:**
- Create: `internal/session/session.go`
- Create: `internal/session/store.go`
- Create: `internal/session/store_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/session/store_test.go`:
```go
package session

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

func TestStore_CreateAndGet(t *testing.T) {
	s := NewStore(1 * time.Hour)
	sess := s.Create(TaskSpec{Title: "t", Goal: "g"})
	require.NotEmpty(t, sess.ID)

	got, ok := s.Get(sess.ID)
	require.True(t, ok)
	assert.Equal(t, sess.ID, got.ID)
	assert.Equal(t, "t", got.Spec.Title)
}

func TestStore_GetUnknown(t *testing.T) {
	s := NewStore(1 * time.Hour)
	_, ok := s.Get("nope")
	assert.False(t, ok)
}

func TestStore_AppendCheckpoint(t *testing.T) {
	s := NewStore(1 * time.Hour)
	sess := s.Create(TaskSpec{Title: "t"})

	cp := Checkpoint{
		At:        time.Now(),
		WorkingOn: "writing handler",
		FileCount: 3,
		Verdict:   verdict.VerdictPass,
	}
	require.True(t, s.AppendCheckpoint(sess.ID, cp))

	got, _ := s.Get(sess.ID)
	require.Len(t, got.Checkpoints, 1)
	assert.Equal(t, "writing handler", got.Checkpoints[0].WorkingOn)
}

func TestStore_AppendCheckpointUnknown(t *testing.T) {
	s := NewStore(1 * time.Hour)
	assert.False(t, s.AppendCheckpoint("nope", Checkpoint{}))
}

func TestStore_TTL_Eviction(t *testing.T) {
	s := NewStore(50 * time.Millisecond)
	sess := s.Create(TaskSpec{Title: "t"})

	// Force LastAccessed into the past by directly mutating (test-only).
	s.mu.Lock()
	s.sessions[sess.ID].LastAccessed = time.Now().Add(-1 * time.Hour)
	s.mu.Unlock()

	evicted := s.EvictExpired(time.Now())
	assert.Equal(t, 1, evicted)

	_, ok := s.Get(sess.ID)
	assert.False(t, ok)
}

func TestStore_GetUpdatesLastAccessed(t *testing.T) {
	s := NewStore(1 * time.Hour)
	sess := s.Create(TaskSpec{Title: "t"})
	first := sess.LastAccessed

	time.Sleep(2 * time.Millisecond)
	got, _ := s.Get(sess.ID)
	assert.True(t, got.LastAccessed.After(first))
}
```

- [ ] **Step 2: Run, confirm fail**

Run: `go test ./internal/session/...`
Expected: FAIL — package doesn't exist.

- [ ] **Step 3: Implement `session.go`**

Create `internal/session/session.go`:
```go
// Package session defines the per-task session structures and an in-memory
// store with TTL eviction.
package session

import (
	"time"

	"github.com/patiently/anti-tangent-mcp/internal/config"
	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

type TaskSpec struct {
	Title              string   `json:"title"`
	Goal               string   `json:"goal"`
	AcceptanceCriteria []string `json:"acceptance_criteria,omitempty"`
	NonGoals           []string `json:"non_goals,omitempty"`
	Context            string   `json:"context,omitempty"`
}

type ModelDefaults struct {
	Pre, Mid, Post config.ModelRef
}

type Checkpoint struct {
	At        time.Time         `json:"at"`
	WorkingOn string            `json:"working_on"`
	FileCount int               `json:"file_count"`
	Verdict   verdict.Verdict   `json:"verdict"`
	Findings  []verdict.Finding `json:"findings,omitempty"`
}

type Session struct {
	ID            string
	CreatedAt     time.Time
	LastAccessed  time.Time
	Spec          TaskSpec
	PreFindings   []verdict.Finding
	Checkpoints   []Checkpoint
	PostFindings  []verdict.Finding
	ModelDefaults ModelDefaults
}
```

- [ ] **Step 4: Implement `store.go`**

Create `internal/session/store.go`:
```go
package session

import (
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

type Store struct {
	mu       sync.RWMutex
	sessions map[string]*Session
	ttl      time.Duration
}

func NewStore(ttl time.Duration) *Store {
	return &Store{
		sessions: make(map[string]*Session),
		ttl:      ttl,
	}
}

func (s *Store) TTL() time.Duration { return s.ttl }

func (s *Store) Create(spec TaskSpec) *Session {
	now := time.Now()
	sess := &Session{
		ID:           uuid.NewString(),
		CreatedAt:    now,
		LastAccessed: now,
		Spec:         spec,
	}
	s.mu.Lock()
	s.sessions[sess.ID] = sess
	s.mu.Unlock()
	return sess
}

func (s *Store) Get(id string) (*Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[id]
	if !ok {
		return nil, false
	}
	sess.LastAccessed = time.Now()
	return sess, true
}

func (s *Store) AppendCheckpoint(id string, cp Checkpoint) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[id]
	if !ok {
		return false
	}
	sess.Checkpoints = append(sess.Checkpoints, cp)
	sess.LastAccessed = time.Now()
	return true
}

func (s *Store) SetPreFindings(id string, findings []verdict.Finding) bool {
	return s.setFindings(id, findings, true)
}

func (s *Store) SetPostFindings(id string, findings []verdict.Finding) bool {
	return s.setFindings(id, findings, false)
}

func (s *Store) setFindings(id string, findings []verdict.Finding, pre bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[id]
	if !ok {
		return false
	}
	if pre {
		sess.PreFindings = findings
	} else {
		sess.PostFindings = findings
	}
	sess.LastAccessed = time.Now()
	return true
}

// EvictExpired removes sessions whose LastAccessed is older than now - ttl.
// Returns the number of sessions evicted. Intended to be called periodically
// from a background goroutine.
func (s *Store) EvictExpired(now time.Time) int {
	cutoff := now.Add(-s.ttl)
	s.mu.Lock()
	defer s.mu.Unlock()
	evicted := 0
	for id, sess := range s.sessions {
		if sess.LastAccessed.Before(cutoff) {
			delete(s.sessions, id)
			evicted++
		}
	}
	return evicted
}
```

- [ ] **Step 5: Add uuid dependency**

```bash
go get github.com/google/uuid
```

- [ ] **Step 6: Run tests, confirm pass**

Run: `go test ./internal/session/... -race -v`
Expected: All tests PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/session/ go.mod go.sum
git commit -m "feat(session): in-memory session store with TTL eviction"
```

---

## Task 6: Prompt templates and rendering

**Files:**
- Create: `internal/prompts/prompts.go`
- Create: `internal/prompts/templates/pre.tmpl`
- Create: `internal/prompts/templates/mid.tmpl`
- Create: `internal/prompts/templates/post.tmpl`
- Create: `internal/prompts/prompts_test.go`
- Create: `internal/prompts/testdata/pre_basic.golden`
- Create: `internal/prompts/testdata/mid_basic.golden`
- Create: `internal/prompts/testdata/post_basic.golden`

The `prompts` package renders system + user strings for each hook from typed inputs. Templates are embedded with `//go:embed`. Tests are golden-file based: render → compare to `testdata/*.golden`.

- [ ] **Step 1: Write the failing test**

Create `internal/prompts/prompts_test.go`:
```go
package prompts

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/session"
	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

var update = flag.Bool("update", false, "update golden files")

func sampleSpec() session.TaskSpec {
	return session.TaskSpec{
		Title: "Add /healthz endpoint",
		Goal:  "Liveness probe for the HTTP server",
		AcceptanceCriteria: []string{
			"Returns 200 OK with body \"ok\"",
			"Responds in under 50ms p95",
		},
		NonGoals: []string{"Database health (covered separately)"},
		Context:  "Service is a Gin app on port 8080.",
	}
}

func golden(t *testing.T, name string, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *update {
		require.NoError(t, os.WriteFile(path, []byte(got), 0o644))
		return
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, string(want), got)
}

func TestRenderPre(t *testing.T) {
	out, err := RenderPre(PreInput{Spec: sampleSpec()})
	require.NoError(t, err)
	golden(t, "pre_basic", out.System+"\n---USER---\n"+out.User)
}

func TestRenderMid(t *testing.T) {
	out, err := RenderMid(MidInput{
		Spec:        sampleSpec(),
		PriorFindings: []verdict.Finding{{
			Severity:   verdict.SeverityMajor,
			Category:   verdict.CategoryAmbiguousSpec,
			Criterion:  "AC #2",
			Evidence:   "\"under 50ms\" — at what load?",
			Suggestion: "Pin the load profile (RPS).",
		}},
		WorkingOn: "writing the handler",
		Files: []File{{
			Path:    "handlers/health.go",
			Content: "package handlers\nfunc Health(c *gin.Context) { c.String(200, \"ok\") }\n",
		}},
		Questions: []string{"Should we expose this on a separate port?"},
	})
	require.NoError(t, err)
	golden(t, "mid_basic", out.System+"\n---USER---\n"+out.User)
}

func TestRenderPost(t *testing.T) {
	out, err := RenderPost(PostInput{
		Spec:    sampleSpec(),
		Summary: "Added Gin handler at /healthz returning \"ok\".",
		Files: []File{{
			Path:    "handlers/health.go",
			Content: "package handlers\nfunc Health(c *gin.Context) { c.String(200, \"ok\") }\n",
		}},
		TestEvidence: "PASS: TestHealthReturns200",
	})
	require.NoError(t, err)
	golden(t, "post_basic", out.System+"\n---USER---\n"+out.User)
}
```

- [ ] **Step 2: Run, confirm fail**

Run: `go test ./internal/prompts/...`
Expected: FAIL — package doesn't exist.

- [ ] **Step 3: Implement `prompts.go`**

Create `internal/prompts/prompts.go`:
```go
// Package prompts renders hook-specific prompts for the reviewer LLM.
package prompts

import (
	"bytes"
	"embed"
	"fmt"
	"text/template"

	"github.com/patiently/anti-tangent-mcp/internal/session"
	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

//go:embed templates/*.tmpl
var templatesFS embed.FS

type Output struct {
	System string
	User   string
}

type File struct {
	Path    string
	Content string
}

type PreInput struct {
	Spec session.TaskSpec
}

type MidInput struct {
	Spec          session.TaskSpec
	PriorFindings []verdict.Finding
	WorkingOn     string
	Files         []File
	Questions     []string
}

type PostInput struct {
	Spec         session.TaskSpec
	Summary      string
	Files        []File
	TestEvidence string
}

const systemPrompt = `You are an exacting reviewer. You return ONLY a JSON object matching the provided schema. You give specific, evidence-backed findings. You never invent facts about code that wasn't shown to you.`

func RenderPre(in PreInput) (Output, error) {
	body, err := render("pre.tmpl", in)
	if err != nil {
		return Output{}, err
	}
	return Output{System: systemPrompt, User: body}, nil
}

func RenderMid(in MidInput) (Output, error) {
	body, err := render("mid.tmpl", in)
	if err != nil {
		return Output{}, err
	}
	return Output{System: systemPrompt, User: body}, nil
}

func RenderPost(in PostInput) (Output, error) {
	body, err := render("post.tmpl", in)
	if err != nil {
		return Output{}, err
	}
	return Output{System: systemPrompt, User: body}, nil
}

func render(name string, data any) (string, error) {
	tmpl, err := template.New("").ParseFS(templatesFS, "templates/"+name)
	if err != nil {
		return "", fmt.Errorf("parse %s: %w", name, err)
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		return "", fmt.Errorf("execute %s: %w", name, err)
	}
	return buf.String(), nil
}
```

- [ ] **Step 4: Create `templates/pre.tmpl`**

```
## Task spec

Title: {{.Spec.Title}}
Goal: {{.Spec.Goal}}

Acceptance criteria:
{{range .Spec.AcceptanceCriteria}}- {{.}}
{{end}}
Non-goals:
{{range .Spec.NonGoals}}- {{.}}
{{end}}{{if .Spec.Context}}
Context:
{{.Spec.Context}}
{{end}}
## What to evaluate

You are evaluating the SPEC ITSELF, not any code. The implementer has not started yet.

Check three things:

1. Structural completeness — is the goal clear? Are there acceptance criteria? Are non-goals declared where they help bound scope?
2. Acceptance-criterion quality — is each AC testable, specific, and unambiguous? For any vague AC, propose a concrete rewrite in the suggestion.
3. Implicit assumptions — list any assumptions a fresh implementer would have to make. Each becomes a finding so the spec author can either pin them down or explicitly mark them as implementer's discretion.

Severity: critical = spec is unimplementable as written; major = a competent implementer would still misimplement it; minor = nit.

Use criterion = "spec" for structural findings, or quote the verbatim AC text for AC-quality findings.

Respond with the verdict JSON only.
```

- [ ] **Step 5: Create `templates/mid.tmpl`**

```
## Task spec

Title: {{.Spec.Title}}
Goal: {{.Spec.Goal}}

Acceptance criteria:
{{range .Spec.AcceptanceCriteria}}- {{.}}
{{end}}
Non-goals:
{{range .Spec.NonGoals}}- {{.}}
{{end}}{{if .Spec.Context}}
Context:
{{.Spec.Context}}
{{end}}{{if .PriorFindings}}
## Prior findings (must be addressed or explicitly justified)
{{range .PriorFindings}}
- [{{.Severity}}/{{.Category}}] criterion: {{.Criterion}}
  evidence:   {{.Evidence}}
  suggestion: {{.Suggestion}}
{{end}}{{end}}
## What to evaluate

You are checking IN-PROGRESS work for drift. Focus on:

- Code that does not map to any acceptance criterion → scope_drift.
- Acceptance criteria that look untouched and at risk of being missed → missing_acceptance_criterion.
- Prior findings (above) that have not been addressed → unaddressed_finding.
- Mismatch between "Working on" and what the diff actually changes.

DO NOT critique code style or polish at this stage. Style is noise mid-task.

## Working on

{{.WorkingOn}}
{{if .Questions}}
## Open questions from the implementer
{{range .Questions}}- {{.}}
{{end}}{{end}}
## Code under review
{{range .Files}}
=== {{.Path}} ===
{{.Content}}
{{end}}
Respond with the verdict JSON only.
```

- [ ] **Step 6: Create `templates/post.tmpl`**

```
## Task spec

Title: {{.Spec.Title}}
Goal: {{.Spec.Goal}}

Acceptance criteria:
{{range .Spec.AcceptanceCriteria}}- {{.}}
{{end}}
Non-goals:
{{range .Spec.NonGoals}}- {{.}}
{{end}}{{if .Spec.Context}}
Context:
{{.Spec.Context}}
{{end}}
## What to evaluate

This is the FINAL review. Walk every acceptance criterion explicitly. For each AC, either:

- find a passing implementation and cite it (no finding required), OR
- emit a finding whose criterion is the verbatim AC text.

Then walk the non-goals: emit a finding for any accidental violation.

If test evidence was provided, cross-check it against the AC list — does it actually exercise each AC?

## Implementer's summary

{{.Summary}}

## Final implementation
{{range .Files}}
=== {{.Path}} ===
{{.Content}}
{{end}}{{if .TestEvidence}}
## Test evidence

{{.TestEvidence}}
{{end}}
Respond with the verdict JSON only.
```

- [ ] **Step 7: Generate golden files**

Run: `go test ./internal/prompts/... -update`
Expected: PASS (golden files written).

Inspect the generated files in `internal/prompts/testdata/` and confirm they look reasonable (no obvious template-rendering bugs).

- [ ] **Step 8: Run without -update, confirm pass**

Run: `go test ./internal/prompts/... -race -v`
Expected: All three tests PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/prompts/
git commit -m "feat(prompts): pre/mid/post templates with golden tests"
```

---

## Task 7: Reviewer interface and model allowlist

**Files:**
- Create: `internal/providers/reviewer.go`
- Create: `internal/providers/reviewer_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/providers/reviewer_test.go`:
```go
package providers

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/config"
)

func TestValidateModel_KnownAnthropic(t *testing.T) {
	require.NoError(t, ValidateModel(config.ModelRef{Provider: "anthropic", Model: "claude-sonnet-4-6"}))
	require.NoError(t, ValidateModel(config.ModelRef{Provider: "anthropic", Model: "claude-opus-4-7"}))
	require.NoError(t, ValidateModel(config.ModelRef{Provider: "anthropic", Model: "claude-haiku-4-5"}))
}

func TestValidateModel_KnownOpenAI(t *testing.T) {
	require.NoError(t, ValidateModel(config.ModelRef{Provider: "openai", Model: "gpt-5"}))
}

func TestValidateModel_KnownGoogle(t *testing.T) {
	require.NoError(t, ValidateModel(config.ModelRef{Provider: "google", Model: "gemini-2.5-pro"}))
	require.NoError(t, ValidateModel(config.ModelRef{Provider: "google", Model: "gemini-2.5-flash"}))
}

func TestValidateModel_UnknownProvider(t *testing.T) {
	err := ValidateModel(config.ModelRef{Provider: "openrouter", Model: "anything"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown provider")
}

func TestValidateModel_UnknownModel(t *testing.T) {
	err := ValidateModel(config.ModelRef{Provider: "anthropic", Model: "claude-2"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not in allowlist")
}
```

- [ ] **Step 2: Run, confirm fail**

Run: `go test ./internal/providers/...`
Expected: FAIL.

- [ ] **Step 3: Implement `reviewer.go`**

Create `internal/providers/reviewer.go`:
```go
// Package providers ships HTTP clients for the supported reviewer LLMs.
package providers

import (
	"context"
	"fmt"

	"github.com/patiently/anti-tangent-mcp/internal/config"
)

type Reviewer interface {
	Name() string
	Review(ctx context.Context, req Request) (Response, error)
}

type Request struct {
	Model      string
	System     string
	User       string
	MaxTokens  int
	JSONSchema []byte
}

type Response struct {
	RawJSON      []byte
	Model        string
	InputTokens  int
	OutputTokens int
}

// allowlist holds the known model IDs per provider. Adding a new model is a
// one-line change here; the validator runs at startup and on per-call overrides.
var allowlist = map[string]map[string]bool{
	"anthropic": {
		"claude-opus-4-7":   true,
		"claude-sonnet-4-6": true,
		"claude-haiku-4-5":  true,
	},
	"openai": {
		"gpt-5":      true,
		"gpt-5-mini": true,
		"gpt-5-nano": true,
	},
	"google": {
		"gemini-2.5-pro":   true,
		"gemini-2.5-flash": true,
	},
}

func ValidateModel(mr config.ModelRef) error {
	models, ok := allowlist[mr.Provider]
	if !ok {
		return fmt.Errorf("unknown provider %q (supported: anthropic, openai, google)", mr.Provider)
	}
	if !models[mr.Model] {
		return fmt.Errorf("model %q not in allowlist for provider %q", mr.Model, mr.Provider)
	}
	return nil
}

// Registry maps provider name to a constructed Reviewer instance.
type Registry map[string]Reviewer

func (r Registry) Get(provider string) (Reviewer, error) {
	rv, ok := r[provider]
	if !ok {
		return nil, fmt.Errorf("provider %q not configured (likely missing API key)", provider)
	}
	return rv, nil
}
```

- [ ] **Step 4: Run, confirm pass**

Run: `go test ./internal/providers/... -race -v`
Expected: All five tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/providers/
git commit -m "feat(providers): Reviewer interface, model allowlist, registry"
```

---

## Task 8: Anthropic reviewer

**Files:**
- Create: `internal/providers/anthropic.go`
- Create: `internal/providers/anthropic_test.go`

Anthropic's structured-output mechanism: define a single tool whose `input_schema` is our verdict schema, set `tool_choice` to force its use, and read the tool input back as the JSON.

- [ ] **Step 1: Write the failing test**

Create `internal/providers/anthropic_test.go`:
```go
package providers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAnthropic_Review_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "/v1/messages", r.URL.Path)
		assert.Equal(t, "test-key", r.Header.Get("x-api-key"))
		assert.Equal(t, "2023-06-01", r.Header.Get("anthropic-version"))

		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		require.NoError(t, json.Unmarshal(body, &req))
		assert.Equal(t, "claude-sonnet-4-6", req["model"])

		// Anthropic returns tool_use content blocks; we shape one here.
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "msg_x",
			"model": "claude-sonnet-4-6",
			"content": [{
				"type": "tool_use",
				"id": "tu_1",
				"name": "submit_review",
				"input": {"verdict":"pass","findings":[],"next_action":"ship"}
			}],
			"usage": {"input_tokens": 10, "output_tokens": 7}
		}`))
	}))
	defer srv.Close()

	rv := NewAnthropic("test-key", srv.URL, 5*time.Second)
	resp, err := rv.Review(context.Background(), Request{
		Model:      "claude-sonnet-4-6",
		System:     "be exact",
		User:       "review this",
		MaxTokens:  1024,
		JSONSchema: []byte(`{"type":"object"}`),
	})
	require.NoError(t, err)
	assert.Equal(t, "claude-sonnet-4-6", resp.Model)
	assert.Equal(t, 10, resp.InputTokens)
	assert.Equal(t, 7, resp.OutputTokens)
	assert.JSONEq(t, `{"verdict":"pass","findings":[],"next_action":"ship"}`, string(resp.RawJSON))
}

func TestAnthropic_Review_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"rate limited"}}`, 429)
	}))
	defer srv.Close()

	rv := NewAnthropic("k", srv.URL, 5*time.Second)
	_, err := rv.Review(context.Background(), Request{Model: "claude-sonnet-4-6"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "429")
}

func TestAnthropic_Review_NoToolUse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"content": [{"type":"text","text":"sorry I can't"}],
			"usage": {"input_tokens":1,"output_tokens":1}
		}`))
	}))
	defer srv.Close()

	rv := NewAnthropic("k", srv.URL, 5*time.Second)
	_, err := rv.Review(context.Background(), Request{Model: "claude-sonnet-4-6"})
	require.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "tool_use")
}
```

- [ ] **Step 2: Run, confirm fail**

Run: `go test ./internal/providers/... -run TestAnthropic`
Expected: FAIL — `NewAnthropic` undefined.

- [ ] **Step 3: Implement `anthropic.go`**

Create `internal/providers/anthropic.go`:
```go
package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type anthropicReviewer struct {
	apiKey  string
	baseURL string
	client  *http.Client
}

func NewAnthropic(apiKey, baseURL string, timeout time.Duration) Reviewer {
	if baseURL == "" {
		baseURL = "https://api.anthropic.com"
	}
	return &anthropicReviewer{
		apiKey:  apiKey,
		baseURL: baseURL,
		client:  &http.Client{Timeout: timeout},
	}
}

func (r *anthropicReviewer) Name() string { return "anthropic" }

func (r *anthropicReviewer) Review(ctx context.Context, req Request) (Response, error) {
	var schema map[string]any
	if err := json.Unmarshal(req.JSONSchema, &schema); err != nil {
		return Response{}, fmt.Errorf("anthropic: invalid schema: %w", err)
	}

	body := map[string]any{
		"model":      req.Model,
		"max_tokens": req.MaxTokens,
		"system":     req.System,
		"messages": []map[string]any{
			{"role": "user", "content": req.User},
		},
		"tools": []map[string]any{{
			"name":         "submit_review",
			"description":  "Submit the structured review verdict.",
			"input_schema": schema,
		}},
		"tool_choice": map[string]any{"type": "tool", "name": "submit_review"},
	}

	buf, _ := json.Marshal(body)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", r.baseURL+"/v1/messages", bytes.NewReader(buf))
	if err != nil {
		return Response{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", r.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	resp, err := r.client.Do(httpReq)
	if err != nil {
		return Response{}, fmt.Errorf("anthropic: %w", err)
	}
	defer resp.Body.Close()

	respBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return Response{}, fmt.Errorf("anthropic: HTTP %d: %s", resp.StatusCode, string(respBytes))
	}

	var parsed struct {
		Model   string `json:"model"`
		Content []struct {
			Type  string          `json:"type"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(respBytes, &parsed); err != nil {
		return Response{}, fmt.Errorf("anthropic: decode response: %w", err)
	}

	for _, c := range parsed.Content {
		if c.Type == "tool_use" && len(c.Input) > 0 {
			return Response{
				RawJSON:      []byte(c.Input),
				Model:        parsed.Model,
				InputTokens:  parsed.Usage.InputTokens,
				OutputTokens: parsed.Usage.OutputTokens,
			}, nil
		}
	}
	return Response{}, fmt.Errorf("anthropic: no tool_use block in response")
}
```

- [ ] **Step 4: Run, confirm pass**

Run: `go test ./internal/providers/... -run TestAnthropic -race -v`
Expected: All three Anthropic tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/providers/anthropic.go internal/providers/anthropic_test.go
git commit -m "feat(providers): Anthropic reviewer via Messages API tool_use"
```

---

## Task 9: OpenAI reviewer

**Files:**
- Create: `internal/providers/openai.go`
- Create: `internal/providers/openai_test.go`

OpenAI uses the Responses API with `text.format = json_schema` to force structured output. (We use `/v1/chat/completions` because it's the most stable, but with the `response_format: json_schema` option.)

- [ ] **Step 1: Write the failing test**

Create `internal/providers/openai_test.go`:
```go
package providers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenAI_Review_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "/v1/chat/completions", r.URL.Path)
		assert.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))

		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		require.NoError(t, json.Unmarshal(body, &req))
		assert.Equal(t, "gpt-5", req["model"])

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"model": "gpt-5",
			"choices": [{
				"message": {"role":"assistant","content":"{\"verdict\":\"pass\",\"findings\":[],\"next_action\":\"ok\"}"}
			}],
			"usage": {"prompt_tokens": 12, "completion_tokens": 8}
		}`))
	}))
	defer srv.Close()

	rv := NewOpenAI("test-key", srv.URL, 5*time.Second)
	resp, err := rv.Review(context.Background(), Request{
		Model:      "gpt-5",
		System:     "sys",
		User:       "usr",
		MaxTokens:  1024,
		JSONSchema: []byte(`{"type":"object","properties":{"verdict":{"type":"string"}}}`),
	})
	require.NoError(t, err)
	assert.Equal(t, "gpt-5", resp.Model)
	assert.Equal(t, 12, resp.InputTokens)
	assert.Equal(t, 8, resp.OutputTokens)
	assert.JSONEq(t, `{"verdict":"pass","findings":[],"next_action":"ok"}`, string(resp.RawJSON))
}

func TestOpenAI_Review_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"nope"}`, 401)
	}))
	defer srv.Close()
	rv := NewOpenAI("k", srv.URL, 5*time.Second)
	_, err := rv.Review(context.Background(), Request{Model: "gpt-5"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "401")
}
```

- [ ] **Step 2: Run, confirm fail**

Run: `go test ./internal/providers/... -run TestOpenAI`
Expected: FAIL.

- [ ] **Step 3: Implement `openai.go`**

Create `internal/providers/openai.go`:
```go
package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type openaiReviewer struct {
	apiKey  string
	baseURL string
	client  *http.Client
}

func NewOpenAI(apiKey, baseURL string, timeout time.Duration) Reviewer {
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}
	return &openaiReviewer{
		apiKey:  apiKey,
		baseURL: baseURL,
		client:  &http.Client{Timeout: timeout},
	}
}

func (r *openaiReviewer) Name() string { return "openai" }

func (r *openaiReviewer) Review(ctx context.Context, req Request) (Response, error) {
	var schema map[string]any
	if err := json.Unmarshal(req.JSONSchema, &schema); err != nil {
		return Response{}, fmt.Errorf("openai: invalid schema: %w", err)
	}

	body := map[string]any{
		"model": req.Model,
		"messages": []map[string]string{
			{"role": "system", "content": req.System},
			{"role": "user", "content": req.User},
		},
		"max_tokens": req.MaxTokens,
		"response_format": map[string]any{
			"type": "json_schema",
			"json_schema": map[string]any{
				"name":   "review",
				"strict": true,
				"schema": schema,
			},
		},
	}

	buf, _ := json.Marshal(body)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", r.baseURL+"/v1/chat/completions", bytes.NewReader(buf))
	if err != nil {
		return Response{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+r.apiKey)

	resp, err := r.client.Do(httpReq)
	if err != nil {
		return Response{}, fmt.Errorf("openai: %w", err)
	}
	defer resp.Body.Close()

	respBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return Response{}, fmt.Errorf("openai: HTTP %d: %s", resp.StatusCode, string(respBytes))
	}

	var parsed struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(respBytes, &parsed); err != nil {
		return Response{}, fmt.Errorf("openai: decode response: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return Response{}, fmt.Errorf("openai: no choices in response")
	}

	return Response{
		RawJSON:      []byte(parsed.Choices[0].Message.Content),
		Model:        parsed.Model,
		InputTokens:  parsed.Usage.PromptTokens,
		OutputTokens: parsed.Usage.CompletionTokens,
	}, nil
}
```

- [ ] **Step 4: Run, confirm pass**

Run: `go test ./internal/providers/... -run TestOpenAI -race -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/providers/openai.go internal/providers/openai_test.go
git commit -m "feat(providers): OpenAI reviewer via chat/completions JSON Schema"
```

---

## Task 10: Google reviewer

**Files:**
- Create: `internal/providers/google.go`
- Create: `internal/providers/google_test.go`

Google's Gemini API uses `generateContent` with `generationConfig.response_mime_type = "application/json"` and `generationConfig.response_schema = ...`.

- [ ] **Step 1: Write the failing test**

Create `internal/providers/google_test.go`:
```go
package providers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGoogle_Review_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.True(t, strings.HasPrefix(r.URL.Path, "/v1beta/models/gemini-2.5-pro:generateContent"))
		assert.Equal(t, "test-key", r.URL.Query().Get("key"))

		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		require.NoError(t, json.Unmarshal(body, &req))

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"candidates": [{
				"content": {"parts": [{"text": "{\"verdict\":\"pass\",\"findings\":[],\"next_action\":\"ok\"}"}]}
			}],
			"usageMetadata": {"promptTokenCount": 5, "candidatesTokenCount": 4},
			"modelVersion": "gemini-2.5-pro"
		}`))
	}))
	defer srv.Close()

	rv := NewGoogle("test-key", srv.URL, 5*time.Second)
	resp, err := rv.Review(context.Background(), Request{
		Model:      "gemini-2.5-pro",
		System:     "sys",
		User:       "usr",
		MaxTokens:  1024,
		JSONSchema: []byte(`{"type":"object"}`),
	})
	require.NoError(t, err)
	assert.Equal(t, "gemini-2.5-pro", resp.Model)
	assert.Equal(t, 5, resp.InputTokens)
	assert.Equal(t, 4, resp.OutputTokens)
	assert.JSONEq(t, `{"verdict":"pass","findings":[],"next_action":"ok"}`, string(resp.RawJSON))
}

func TestGoogle_Review_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"bad"}}`, 400)
	}))
	defer srv.Close()
	rv := NewGoogle("k", srv.URL, 5*time.Second)
	_, err := rv.Review(context.Background(), Request{Model: "gemini-2.5-pro"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "400")
}
```

- [ ] **Step 2: Run, confirm fail**

Run: `go test ./internal/providers/... -run TestGoogle`
Expected: FAIL.

- [ ] **Step 3: Implement `google.go`**

Create `internal/providers/google.go`:
```go
package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

type googleReviewer struct {
	apiKey  string
	baseURL string
	client  *http.Client
}

func NewGoogle(apiKey, baseURL string, timeout time.Duration) Reviewer {
	if baseURL == "" {
		baseURL = "https://generativelanguage.googleapis.com"
	}
	return &googleReviewer{
		apiKey:  apiKey,
		baseURL: baseURL,
		client:  &http.Client{Timeout: timeout},
	}
}

func (r *googleReviewer) Name() string { return "google" }

func (r *googleReviewer) Review(ctx context.Context, req Request) (Response, error) {
	var schema map[string]any
	if err := json.Unmarshal(req.JSONSchema, &schema); err != nil {
		return Response{}, fmt.Errorf("google: invalid schema: %w", err)
	}

	body := map[string]any{
		"systemInstruction": map[string]any{
			"parts": []map[string]string{{"text": req.System}},
		},
		"contents": []map[string]any{{
			"role":  "user",
			"parts": []map[string]string{{"text": req.User}},
		}},
		"generationConfig": map[string]any{
			"maxOutputTokens":   req.MaxTokens,
			"responseMimeType":  "application/json",
			"responseSchema":    schema,
		},
	}

	buf, _ := json.Marshal(body)
	endpoint := fmt.Sprintf("%s/v1beta/models/%s:generateContent?key=%s",
		r.baseURL, url.PathEscape(req.Model), url.QueryEscape(r.apiKey))

	httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(buf))
	if err != nil {
		return Response{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := r.client.Do(httpReq)
	if err != nil {
		return Response{}, fmt.Errorf("google: %w", err)
	}
	defer resp.Body.Close()

	respBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return Response{}, fmt.Errorf("google: HTTP %d: %s", resp.StatusCode, string(respBytes))
	}

	var parsed struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
		UsageMetadata struct {
			PromptTokenCount     int `json:"promptTokenCount"`
			CandidatesTokenCount int `json:"candidatesTokenCount"`
		} `json:"usageMetadata"`
		ModelVersion string `json:"modelVersion"`
	}
	if err := json.Unmarshal(respBytes, &parsed); err != nil {
		return Response{}, fmt.Errorf("google: decode response: %w", err)
	}
	if len(parsed.Candidates) == 0 || len(parsed.Candidates[0].Content.Parts) == 0 {
		return Response{}, fmt.Errorf("google: empty candidates in response")
	}

	return Response{
		RawJSON:      []byte(parsed.Candidates[0].Content.Parts[0].Text),
		Model:        parsed.ModelVersion,
		InputTokens:  parsed.UsageMetadata.PromptTokenCount,
		OutputTokens: parsed.UsageMetadata.CandidatesTokenCount,
	}, nil
}
```

- [ ] **Step 4: Run, confirm pass**

Run: `go test ./internal/providers/... -race -v`
Expected: All provider tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/providers/google.go internal/providers/google_test.go
git commit -m "feat(providers): Google reviewer via Gemini generateContent"
```

---

## Task 11: MCP server bootstrap

**Files:**
- Create: `internal/mcpsrv/server.go`
- Create: `internal/mcpsrv/handlers.go` (stubs only; filled in Tasks 12–14)

This task creates the server skeleton + minimum-viable handler stubs so the repo builds cleanly. The three handler methods are replaced (not appended to) in Tasks 12–14.

- [ ] **Step 1: Add MCP SDK dependency**

```bash
go get github.com/modelcontextprotocol/go-sdk@latest
```

- [ ] **Step 2: Implement `server.go`**

Create `internal/mcpsrv/server.go`:
```go
// Package mcpsrv wires the MCP server: tool registration, request validation,
// and dispatch to the verdict pipeline.
package mcpsrv

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/patiently/anti-tangent-mcp/internal/config"
	"github.com/patiently/anti-tangent-mcp/internal/providers"
	"github.com/patiently/anti-tangent-mcp/internal/session"
)

type Deps struct {
	Cfg      config.Config
	Sessions *session.Store
	Reviews  providers.Registry
}

func New(d Deps) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{
		Name:    "anti-tangent-mcp",
		Version: "0.1.0",
	}, nil)

	h := &handlers{deps: d}
	mcp.AddTool(srv, validateTaskSpecTool(), h.ValidateTaskSpec)
	mcp.AddTool(srv, checkProgressTool(), h.CheckProgress)
	mcp.AddTool(srv, validateCompletionTool(), h.ValidateCompletion)

	return srv
}
```

- [ ] **Step 3: Implement minimal `handlers.go` stubs**

Create `internal/mcpsrv/handlers.go`:
```go
package mcpsrv

import (
	"context"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type handlers struct {
	deps Deps
}

func validateTaskSpecTool() *mcp.Tool  { return &mcp.Tool{Name: "validate_task_spec"} }
func checkProgressTool() *mcp.Tool     { return &mcp.Tool{Name: "check_progress"} }
func validateCompletionTool() *mcp.Tool { return &mcp.Tool{Name: "validate_completion"} }

type ValidateTaskSpecArgs struct{}
type CheckProgressArgs struct{}
type ValidateCompletionArgs struct{}

func (h *handlers) ValidateTaskSpec(_ context.Context, _ *mcp.CallToolRequest, _ ValidateTaskSpecArgs) (*mcp.CallToolResult, error) {
	return nil, errors.New("not implemented (Task 12)")
}
func (h *handlers) CheckProgress(_ context.Context, _ *mcp.CallToolRequest, _ CheckProgressArgs) (*mcp.CallToolResult, error) {
	return nil, errors.New("not implemented (Task 13)")
}
func (h *handlers) ValidateCompletion(_ context.Context, _ *mcp.CallToolRequest, _ ValidateCompletionArgs) (*mcp.CallToolResult, error) {
	return nil, errors.New("not implemented (Task 14)")
}
```

- [ ] **Step 4: Confirm the repo builds**

Run: `go build ./...`
Expected: No errors. Tests for other packages still pass: `go test -race ./...`.

- [ ] **Step 5: Commit**

```bash
git add internal/mcpsrv/ go.mod go.sum
git commit -m "feat(mcpsrv): server skeleton with three tool registrations (stubs)"
```

---

## Task 12: `validate_task_spec` handler

**Files:**
- Modify: `internal/mcpsrv/handlers.go` (replace the stub file from Task 11)
- Create: `internal/mcpsrv/handlers_test.go`

Handlers share input validation, model resolution, and reviewer dispatch. We put the shared scaffolding here too so the next two handlers can drop in cleanly.

- [ ] **Step 1: Write the failing test**

Create `internal/mcpsrv/handlers_test.go`:
```go
package mcpsrv

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/config"
	"github.com/patiently/anti-tangent-mcp/internal/providers"
	"github.com/patiently/anti-tangent-mcp/internal/session"
)

type fakeReviewer struct {
	name string
	resp providers.Response
	err  error
}

func (f *fakeReviewer) Name() string { return f.name }
func (f *fakeReviewer) Review(ctx context.Context, _ providers.Request) (providers.Response, error) {
	if f.err != nil {
		return providers.Response{}, f.err
	}
	return f.resp, nil
}

func passResp(model string) providers.Response {
	return providers.Response{
		RawJSON:      []byte(`{"verdict":"pass","findings":[],"next_action":"go"}`),
		Model:        model,
		InputTokens:  3, OutputTokens: 2,
	}
}

func newDeps(t *testing.T, rv *fakeReviewer) Deps {
	cfg, err := config.Load(func(k string) string {
		switch k {
		case "ANTHROPIC_API_KEY":
			return "k"
		}
		return ""
	})
	require.NoError(t, err)
	return Deps{
		Cfg:      cfg,
		Sessions: session.NewStore(1 * time.Hour),
		Reviews:  providers.Registry{"anthropic": rv},
	}
}

func TestValidateTaskSpec_HappyPath(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	out, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle:          "X",
		Goal:               "Y",
		AcceptanceCriteria: []string{"AC1"},
	})
	require.NoError(t, err)
	require.NotNil(t, out)
	require.Len(t, out.Content, 1)

	var env Envelope
	require.NoError(t, json.Unmarshal([]byte(out.Content[0].(*mcpTextContent).Text), &env))
	assert.Equal(t, "pass", env.Verdict)
	assert.NotEmpty(t, env.SessionID)
	assert.Equal(t, "anthropic:claude-sonnet-4-6", env.ModelUsed)

	// Session was actually created.
	_, ok := d.Sessions.Get(env.SessionID)
	assert.True(t, ok)
}

func TestValidateTaskSpec_ProviderError(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", err: errors.New("boom")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}
	_, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "X", Goal: "Y",
	})
	require.Error(t, err)
}

func TestValidateTaskSpec_MissingFields(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	h := &handlers{deps: newDeps(t, rv)}
	_, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{Goal: "Y"})
	require.Error(t, err)
}
```

- [ ] **Step 2: Run, confirm fail**

Run: `go test ./internal/mcpsrv/...`
Expected: FAIL — package incomplete.

- [ ] **Step 3: Replace `handlers.go` with the full implementation**

Overwrite `internal/mcpsrv/handlers.go` (which currently contains the Task 11 stubs) with:
```go
package mcpsrv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/patiently/anti-tangent-mcp/internal/config"
	"github.com/patiently/anti-tangent-mcp/internal/prompts"
	"github.com/patiently/anti-tangent-mcp/internal/providers"
	"github.com/patiently/anti-tangent-mcp/internal/session"
	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

// Envelope is the JSON returned to the subagent for every hook.
type Envelope struct {
	SessionID  string            `json:"session_id"`
	Verdict    string            `json:"verdict"`
	Findings   []verdict.Finding `json:"findings"`
	NextAction string            `json:"next_action"`
	ModelUsed  string            `json:"model_used"`
	ReviewMS   int64             `json:"review_ms"`
}

// ValidateTaskSpecArgs is the input schema for the pre-hook.
type ValidateTaskSpecArgs struct {
	TaskTitle          string   `json:"task_title"           jsonschema:"required"`
	Goal               string   `json:"goal"                 jsonschema:"required"`
	AcceptanceCriteria []string `json:"acceptance_criteria,omitempty"`
	NonGoals           []string `json:"non_goals,omitempty"`
	Context            string   `json:"context,omitempty"`
	ModelOverride      string   `json:"model_override,omitempty"`
}

type handlers struct {
	deps Deps
}

func validateTaskSpecTool() *mcp.Tool {
	return &mcp.Tool{
		Name: "validate_task_spec",
		Description: "Validate that a task specification is clear and implementable BEFORE you start coding. " +
			"Returns findings on missing/ambiguous goals, weak acceptance criteria, and unstated assumptions. " +
			"Call this once at the start of every task.",
	}
}

func (h *handlers) ValidateTaskSpec(ctx context.Context, _ *mcp.CallToolRequest, args ValidateTaskSpecArgs) (*mcp.CallToolResult, error) {
	if args.TaskTitle == "" || args.Goal == "" {
		return nil, errors.New("task_title and goal are required")
	}

	model, err := h.resolveModel(args.ModelOverride, h.deps.Cfg.PreModel)
	if err != nil {
		return nil, err
	}

	spec := session.TaskSpec{
		Title:              args.TaskTitle,
		Goal:               args.Goal,
		AcceptanceCriteria: args.AcceptanceCriteria,
		NonGoals:           args.NonGoals,
		Context:            args.Context,
	}
	sess := h.deps.Sessions.Create(spec)

	rendered, err := prompts.RenderPre(prompts.PreInput{Spec: spec})
	if err != nil {
		return nil, fmt.Errorf("render pre prompt: %w", err)
	}

	result, modelUsed, ms, err := h.review(ctx, model, rendered)
	if err != nil {
		return nil, err
	}

	h.deps.Sessions.SetPreFindings(sess.ID, result.Findings)

	return envelopeResult(Envelope{
		SessionID:  sess.ID,
		Verdict:    string(result.Verdict),
		Findings:   result.Findings,
		NextAction: result.NextAction,
		ModelUsed:  modelUsed,
		ReviewMS:   ms,
	})
}

// review runs a single reviewer call with one parse-retry on malformed JSON.
func (h *handlers) review(ctx context.Context, model config.ModelRef, p prompts.Output) (verdict.Result, string, int64, error) {
	rv, err := h.deps.Reviews.Get(model.Provider)
	if err != nil {
		return verdict.Result{}, "", 0, err
	}
	start := time.Now()

	req := providers.Request{
		Model:      model.Model,
		System:     p.System,
		User:       p.User,
		MaxTokens:  4096,
		JSONSchema: verdict.Schema(),
	}
	resp, err := rv.Review(ctx, req)
	if err != nil {
		return verdict.Result{}, "", 0, err
	}
	r, err := verdict.Parse(resp.RawJSON)
	if err != nil {
		// One retry with explicit reminder.
		req.User = p.User + "\n\n" + verdict.RetryHint()
		resp, err = rv.Review(ctx, req)
		if err != nil {
			return verdict.Result{}, "", 0, err
		}
		r, err = verdict.Parse(resp.RawJSON)
		if err != nil {
			return verdict.Result{}, "", 0, fmt.Errorf("provider response failed schema after retry: %w", err)
		}
	}

	modelUsed := model.Provider + ":" + resp.Model
	if resp.Model == "" {
		modelUsed = model.String()
	}
	return r, modelUsed, time.Since(start).Milliseconds(), nil
}

func (h *handlers) resolveModel(override string, fallback config.ModelRef) (config.ModelRef, error) {
	if override == "" {
		return fallback, nil
	}
	mr, err := config.ParseModelRef(override)
	if err != nil {
		return config.ModelRef{}, err
	}
	if err := providers.ValidateModel(mr); err != nil {
		return config.ModelRef{}, err
	}
	return mr, nil
}

func envelopeResult(env Envelope) (*mcp.CallToolResult, error) {
	body, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return nil, err
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(body)}},
	}, nil
}

// stubs for tools defined in later tasks; let the build pass.
func checkProgressTool() *mcp.Tool      { return &mcp.Tool{Name: "check_progress"} }
func validateCompletionTool() *mcp.Tool { return &mcp.Tool{Name: "validate_completion"} }

type CheckProgressArgs struct{}
type ValidateCompletionArgs struct{}

func (h *handlers) CheckProgress(_ context.Context, _ *mcp.CallToolRequest, _ CheckProgressArgs) (*mcp.CallToolResult, error) {
	return nil, errors.New("not implemented yet (Task 13)")
}
func (h *handlers) ValidateCompletion(_ context.Context, _ *mcp.CallToolRequest, _ ValidateCompletionArgs) (*mcp.CallToolResult, error) {
	return nil, errors.New("not implemented yet (Task 14)")
}

// mcpTextContent alias for tests; the SDK's TextContent satisfies mcp.Content.
type mcpTextContent = mcp.TextContent
```

- [ ] **Step 4: Run, confirm pass**

Run: `go test ./internal/mcpsrv/... -race -v -run TestValidateTaskSpec`
Expected: All three tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/mcpsrv/
git commit -m "feat(mcpsrv): validate_task_spec handler with reviewer dispatch"
```

---

## Task 13: `check_progress` handler

**Files:**
- Modify: `internal/mcpsrv/handlers.go` (replace `CheckProgress` stub)
- Modify: `internal/mcpsrv/handlers_test.go` (append tests)

- [ ] **Step 1: Append failing tests**

Add to `internal/mcpsrv/handlers_test.go`:
```go
func TestCheckProgress_HappyPath(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-haiku-4-5")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	// Pre-create a session so check_progress has something to thread.
	pre, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "T", Goal: "G", AcceptanceCriteria: []string{"AC"},
	})
	require.NoError(t, err)
	var env Envelope
	require.NoError(t, json.Unmarshal([]byte(pre.Content[0].(*mcpTextContent).Text), &env))

	out, err := h.CheckProgress(context.Background(), nil, CheckProgressArgs{
		SessionID: env.SessionID,
		WorkingOn: "writing handler",
		ChangedFiles: []FileArg{{Path: "h.go", Content: "package h\n"}},
	})
	require.NoError(t, err)

	var env2 Envelope
	require.NoError(t, json.Unmarshal([]byte(out.Content[0].(*mcpTextContent).Text), &env2))
	assert.Equal(t, env.SessionID, env2.SessionID)
	assert.Equal(t, "pass", env2.Verdict)

	// A checkpoint was appended.
	got, _ := d.Sessions.Get(env.SessionID)
	require.Len(t, got.Checkpoints, 1)
}

func TestCheckProgress_UnknownSession(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-haiku-4-5")}
	h := &handlers{deps: newDeps(t, rv)}
	out, err := h.CheckProgress(context.Background(), nil, CheckProgressArgs{
		SessionID: "does-not-exist", WorkingOn: "x",
	})
	require.NoError(t, err)
	var env Envelope
	require.NoError(t, json.Unmarshal([]byte(out.Content[0].(*mcpTextContent).Text), &env))
	assert.Equal(t, "fail", env.Verdict)
	require.Len(t, env.Findings, 1)
	assert.Equal(t, "session_not_found", string(env.Findings[0].Category))
}

func TestCheckProgress_PayloadTooLarge(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-haiku-4-5")}
	d := newDeps(t, rv)
	d.Cfg.MaxPayloadBytes = 10
	h := &handlers{deps: d}

	pre, _ := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{TaskTitle: "T", Goal: "G"})
	var env Envelope
	_ = json.Unmarshal([]byte(pre.Content[0].(*mcpTextContent).Text), &env)

	out, err := h.CheckProgress(context.Background(), nil, CheckProgressArgs{
		SessionID:    env.SessionID,
		WorkingOn:    "x",
		ChangedFiles: []FileArg{{Path: "f", Content: "this is way too much"}},
	})
	require.NoError(t, err)
	var env2 Envelope
	_ = json.Unmarshal([]byte(out.Content[0].(*mcpTextContent).Text), &env2)
	assert.Equal(t, "fail", env2.Verdict)
	assert.Equal(t, "payload_too_large", string(env2.Findings[0].Category))
}
```

- [ ] **Step 2: Replace `CheckProgress` stub with full implementation**

In `internal/mcpsrv/handlers.go`, replace the stub block (the `checkProgressTool`, `CheckProgressArgs`, and `CheckProgress` stub from Task 12) with:

```go
func checkProgressTool() *mcp.Tool {
	return &mcp.Tool{
		Name: "check_progress",
		Description: "Check that your in-progress work is staying aligned with the task spec. " +
			"Call this at natural checkpoints — after a meaningful chunk of code is written, " +
			"before moving to a new sub-area, or whenever you're unsure whether you're drifting.",
	}
}

type FileArg struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type CheckProgressArgs struct {
	SessionID     string    `json:"session_id"     jsonschema:"required"`
	WorkingOn     string    `json:"working_on"     jsonschema:"required"`
	ChangedFiles  []FileArg `json:"changed_files,omitempty"`
	Questions     []string  `json:"questions,omitempty"`
	ModelOverride string    `json:"model_override,omitempty"`
}

func (h *handlers) CheckProgress(ctx context.Context, _ *mcp.CallToolRequest, args CheckProgressArgs) (*mcp.CallToolResult, error) {
	if args.SessionID == "" || args.WorkingOn == "" {
		return nil, errors.New("session_id and working_on are required")
	}

	sess, ok := h.deps.Sessions.Get(args.SessionID)
	if !ok {
		return envelopeResult(notFoundEnvelope(args.SessionID, h.deps.Cfg.MidModel))
	}

	if size := totalBytes(args.ChangedFiles); size > h.deps.Cfg.MaxPayloadBytes {
		return envelopeResult(tooLargeEnvelope(sess.ID, h.deps.Cfg.MidModel, size, h.deps.Cfg.MaxPayloadBytes))
	}

	model, err := h.resolveModel(args.ModelOverride, h.deps.Cfg.MidModel)
	if err != nil {
		return nil, err
	}

	rendered, err := prompts.RenderMid(prompts.MidInput{
		Spec:          sess.Spec,
		PriorFindings: priorFindings(sess),
		WorkingOn:     args.WorkingOn,
		Files:         toPromptFiles(args.ChangedFiles),
		Questions:     args.Questions,
	})
	if err != nil {
		return nil, fmt.Errorf("render mid prompt: %w", err)
	}

	result, modelUsed, ms, err := h.review(ctx, model, rendered)
	if err != nil {
		return nil, err
	}

	h.deps.Sessions.AppendCheckpoint(sess.ID, session.Checkpoint{
		At:        time.Now(),
		WorkingOn: args.WorkingOn,
		FileCount: len(args.ChangedFiles),
		Verdict:   result.Verdict,
		Findings:  result.Findings,
	})

	return envelopeResult(Envelope{
		SessionID:  sess.ID,
		Verdict:    string(result.Verdict),
		Findings:   result.Findings,
		NextAction: result.NextAction,
		ModelUsed:  modelUsed,
		ReviewMS:   ms,
	})
}

func totalBytes(files []FileArg) int {
	n := 0
	for _, f := range files {
		n += len(f.Content) + len(f.Path)
	}
	return n
}

func toPromptFiles(files []FileArg) []prompts.File {
	out := make([]prompts.File, len(files))
	for i, f := range files {
		out[i] = prompts.File{Path: f.Path, Content: f.Content}
	}
	return out
}

func priorFindings(s *session.Session) []verdict.Finding {
	out := append([]verdict.Finding{}, s.PreFindings...)
	for _, cp := range s.Checkpoints {
		out = append(out, cp.Findings...)
	}
	return out
}

func notFoundEnvelope(id string, model config.ModelRef) Envelope {
	return Envelope{
		SessionID: id,
		Verdict:   string(verdict.VerdictFail),
		Findings: []verdict.Finding{{
			Severity:   verdict.SeverityCritical,
			Category:   verdict.CategorySessionMissing,
			Criterion:  "session",
			Evidence:   "session_id " + id + " not found or expired",
			Suggestion: "Call validate_task_spec first and use the returned session_id.",
		}},
		NextAction: "Call validate_task_spec first.",
		ModelUsed:  model.String(),
	}
}

func tooLargeEnvelope(id string, model config.ModelRef, size, cap int) Envelope {
	return Envelope{
		SessionID: id,
		Verdict:   string(verdict.VerdictFail),
		Findings: []verdict.Finding{{
			Severity:   verdict.SeverityMajor,
			Category:   verdict.CategoryTooLarge,
			Criterion:  "payload",
			Evidence:   fmt.Sprintf("payload %d bytes exceeds cap %d", size, cap),
			Suggestion: "Send a unified diff instead of full files, or split the call.",
		}},
		NextAction: "Reduce the payload and retry.",
		ModelUsed:  model.String(),
	}
}
```

- [ ] **Step 3: Run, confirm pass**

Run: `go test ./internal/mcpsrv/... -race -v -run TestCheckProgress`
Expected: All three new tests PASS; existing pre-hook tests still PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/mcpsrv/
git commit -m "feat(mcpsrv): check_progress handler with payload guard"
```

---

## Task 14: `validate_completion` handler

**Files:**
- Modify: `internal/mcpsrv/handlers.go` (replace `ValidateCompletion` stub)
- Modify: `internal/mcpsrv/handlers_test.go` (append tests)

- [ ] **Step 1: Append failing tests**

Add to `internal/mcpsrv/handlers_test.go`:
```go
func TestValidateCompletion_HappyPath(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-opus-4-7")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}

	pre, _ := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "T", Goal: "G", AcceptanceCriteria: []string{"AC"},
	})
	var env Envelope
	_ = json.Unmarshal([]byte(pre.Content[0].(*mcpTextContent).Text), &env)

	out, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		SessionID:  env.SessionID,
		Summary:    "implemented X",
		FinalFiles: []FileArg{{Path: "f.go", Content: "package f\n"}},
	})
	require.NoError(t, err)
	var env2 Envelope
	_ = json.Unmarshal([]byte(out.Content[0].(*mcpTextContent).Text), &env2)
	assert.Equal(t, env.SessionID, env2.SessionID)
	assert.Equal(t, "pass", env2.Verdict)

	got, _ := d.Sessions.Get(env.SessionID)
	assert.NotNil(t, got.PostFindings)
}

func TestValidateCompletion_UnknownSession(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-opus-4-7")}
	h := &handlers{deps: newDeps(t, rv)}
	out, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		SessionID: "missing", Summary: "x",
	})
	require.NoError(t, err)
	var env Envelope
	_ = json.Unmarshal([]byte(out.Content[0].(*mcpTextContent).Text), &env)
	assert.Equal(t, "fail", env.Verdict)
	assert.Equal(t, "session_not_found", string(env.Findings[0].Category))
}
```

- [ ] **Step 2: Replace `ValidateCompletion` stub**

In `internal/mcpsrv/handlers.go`, replace the `validateCompletionTool`, `ValidateCompletionArgs`, and `ValidateCompletion` stub block with:

```go
func validateCompletionTool() *mcp.Tool {
	return &mcp.Tool{
		Name: "validate_completion",
		Description: "Final validation before declaring a task complete. " +
			"The reviewer checks the full implementation against every acceptance criterion " +
			"and non-goal. Treat any `fail` or `warn` findings as work to do before claiming done.",
	}
}

type ValidateCompletionArgs struct {
	SessionID     string    `json:"session_id"  jsonschema:"required"`
	Summary       string    `json:"summary"     jsonschema:"required"`
	FinalFiles    []FileArg `json:"final_files,omitempty"`
	TestEvidence  string    `json:"test_evidence,omitempty"`
	ModelOverride string    `json:"model_override,omitempty"`
}

func (h *handlers) ValidateCompletion(ctx context.Context, _ *mcp.CallToolRequest, args ValidateCompletionArgs) (*mcp.CallToolResult, error) {
	if args.SessionID == "" || args.Summary == "" {
		return nil, errors.New("session_id and summary are required")
	}

	sess, ok := h.deps.Sessions.Get(args.SessionID)
	if !ok {
		return envelopeResult(notFoundEnvelope(args.SessionID, h.deps.Cfg.PostModel))
	}

	if size := totalBytes(args.FinalFiles); size > h.deps.Cfg.MaxPayloadBytes {
		return envelopeResult(tooLargeEnvelope(sess.ID, h.deps.Cfg.PostModel, size, h.deps.Cfg.MaxPayloadBytes))
	}

	model, err := h.resolveModel(args.ModelOverride, h.deps.Cfg.PostModel)
	if err != nil {
		return nil, err
	}

	rendered, err := prompts.RenderPost(prompts.PostInput{
		Spec:         sess.Spec,
		Summary:      args.Summary,
		Files:        toPromptFiles(args.FinalFiles),
		TestEvidence: args.TestEvidence,
	})
	if err != nil {
		return nil, fmt.Errorf("render post prompt: %w", err)
	}

	result, modelUsed, ms, err := h.review(ctx, model, rendered)
	if err != nil {
		return nil, err
	}

	h.deps.Sessions.SetPostFindings(sess.ID, result.Findings)

	return envelopeResult(Envelope{
		SessionID:  sess.ID,
		Verdict:    string(result.Verdict),
		Findings:   result.Findings,
		NextAction: result.NextAction,
		ModelUsed:  modelUsed,
		ReviewMS:   ms,
	})
}
```

- [ ] **Step 3: Run, confirm pass**

Run: `go test ./internal/mcpsrv/... -race -v`
Expected: All handler tests PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/mcpsrv/
git commit -m "feat(mcpsrv): validate_completion handler"
```

---

## Task 15: `main.go` wiring + logging + TTL goroutine

**Files:**
- Create: `cmd/anti-tangent-mcp/main.go`

- [ ] **Step 1: Implement `main.go`**

Create `cmd/anti-tangent-mcp/main.go`:
```go
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/patiently/anti-tangent-mcp/internal/config"
	"github.com/patiently/anti-tangent-mcp/internal/mcpsrv"
	"github.com/patiently/anti-tangent-mcp/internal/providers"
	"github.com/patiently/anti-tangent-mcp/internal/session"
)

// version is set at build time via -ldflags "-X main.version=$(cat VERSION)".
var version = "dev"

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "-v" || os.Args[1] == "--version") {
		fmt.Println(version)
		return
	}

	cfg, err := config.Load(os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config error:", err)
		os.Exit(1)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(logger)
	logger.Info("starting", "version", version,
		"pre_model", cfg.PreModel.String(),
		"mid_model", cfg.MidModel.String(),
		"post_model", cfg.PostModel.String(),
		"session_ttl", cfg.SessionTTL.String())

	if err := providers.ValidateModel(cfg.PreModel); err != nil {
		fail(logger, "pre model invalid", err)
	}
	if err := providers.ValidateModel(cfg.MidModel); err != nil {
		fail(logger, "mid model invalid", err)
	}
	if err := providers.ValidateModel(cfg.PostModel); err != nil {
		fail(logger, "post model invalid", err)
	}

	registry := providers.Registry{}
	if cfg.AnthropicKey != "" {
		registry["anthropic"] = providers.NewAnthropic(cfg.AnthropicKey, "", cfg.RequestTimeout)
	}
	if cfg.OpenAIKey != "" {
		registry["openai"] = providers.NewOpenAI(cfg.OpenAIKey, "", cfg.RequestTimeout)
	}
	if cfg.GoogleKey != "" {
		registry["google"] = providers.NewGoogle(cfg.GoogleKey, "", cfg.RequestTimeout)
	}

	store := session.NewStore(cfg.SessionTTL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go evictLoop(ctx, store, 5*time.Minute, logger)

	srv := mcpsrv.New(mcpsrv.Deps{
		Cfg:      cfg,
		Sessions: store,
		Reviews:  registry,
	})

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		logger.Info("shutting down")
		cancel()
	}()

	if err := srv.Run(ctx, &mcp.StdioTransport{}); err != nil {
		logger.Error("mcp run failed", "err", err)
		os.Exit(1)
	}
}

func evictLoop(ctx context.Context, store *session.Store, every time.Duration, logger *slog.Logger) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			n := store.EvictExpired(time.Now())
			if n > 0 {
				logger.Info("evicted sessions", "count", n)
			}
		}
	}
}

func fail(logger *slog.Logger, msg string, err error) {
	logger.Error(msg, "err", err)
	os.Exit(1)
}
```

- [ ] **Step 2: Confirm the binary builds**

Run: `go build ./...`
Expected: No errors.

- [ ] **Step 3: Confirm `--version` works**

Run: `go run ./cmd/anti-tangent-mcp --version`
Expected: prints `dev` (because we haven't passed `-ldflags` yet).

- [ ] **Step 4: Commit**

```bash
git add cmd/
git commit -m "feat(cmd): main.go wires config, providers, session store, MCP stdio"
```

---

## Task 16: End-to-end integration test with mock provider

**Files:**
- Create: `internal/mcpsrv/integration_test.go`

This test exercises the full server through the MCP go-sdk's in-process client/server transport. It validates that session_id threads correctly across all three tools and the response envelope is well-formed.

- [ ] **Step 1: Write the failing test**

Create `internal/mcpsrv/integration_test.go`:
```go
package mcpsrv

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/config"
	"github.com/patiently/anti-tangent-mcp/internal/providers"
	"github.com/patiently/anti-tangent-mcp/internal/session"
)

func TestIntegration_FullLifecycle(t *testing.T) {
	cfg, err := config.Load(func(k string) string {
		if k == "ANTHROPIC_API_KEY" {
			return "k"
		}
		return ""
	})
	require.NoError(t, err)

	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	deps := Deps{
		Cfg:      cfg,
		Sessions: session.NewStore(1 * time.Hour),
		Reviews:  providers.Registry{"anthropic": rv},
	}
	srv := New(deps)

	ct, st := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() { _ = srv.Run(ctx, st) }()

	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	require.NoError(t, err)
	defer cs.Close()

	// 1. validate_task_spec
	pre := callTool(t, ctx, cs, "validate_task_spec", map[string]any{
		"task_title": "X", "goal": "Y", "acceptance_criteria": []string{"AC1"},
	})
	assert.Equal(t, "pass", pre.Verdict)
	require.NotEmpty(t, pre.SessionID)

	// 2. check_progress
	mid := callTool(t, ctx, cs, "check_progress", map[string]any{
		"session_id":     pre.SessionID,
		"working_on":     "writing handler",
		"changed_files":  []map[string]string{{"path": "h.go", "content": "package h\n"}},
	})
	assert.Equal(t, pre.SessionID, mid.SessionID)

	// 3. validate_completion
	post := callTool(t, ctx, cs, "validate_completion", map[string]any{
		"session_id":  pre.SessionID,
		"summary":     "done",
		"final_files": []map[string]string{{"path": "h.go", "content": "package h\n"}},
	})
	assert.Equal(t, pre.SessionID, post.SessionID)
	assert.Equal(t, "pass", post.Verdict)
}

func callTool(t *testing.T, ctx context.Context, cs *mcp.ClientSession, name string, args map[string]any) Envelope {
	t.Helper()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	require.NoError(t, err)
	require.False(t, res.IsError, "tool returned error: %v", res.Content)
	require.Len(t, res.Content, 1)
	tc, ok := res.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	var env Envelope
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &env))
	return env
}
```

- [ ] **Step 2: Run, confirm pass**

Run: `go test ./internal/mcpsrv/... -race -v -run TestIntegration_FullLifecycle`
Expected: PASS.

If the SDK's API differs (`NewInMemoryTransports`, `mcp.AddTool` signatures), reconcile by checking `pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp`. The test asserts the public contract; adjust call sites only.

- [ ] **Step 3: Commit**

```bash
git add internal/mcpsrv/integration_test.go
git commit -m "test(mcpsrv): full-lifecycle integration test through MCP transport"
```

---

## Task 17: Dockerfile

**Files:**
- Create: `Dockerfile`

- [ ] **Step 1: Write the Dockerfile**

```dockerfile
# syntax=docker/dockerfile:1.7

FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -o /out/anti-tangent-mcp ./cmd/anti-tangent-mcp

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/anti-tangent-mcp /anti-tangent-mcp
ENTRYPOINT ["/anti-tangent-mcp"]
```

- [ ] **Step 2: Test the build locally**

Run: `docker build -t anti-tangent-mcp:test --build-arg VERSION=$(cat VERSION) .`
Expected: build succeeds; `docker run --rm anti-tangent-mcp:test --version` prints the VERSION contents.

- [ ] **Step 3: Commit**

```bash
git add Dockerfile
git commit -m "build: add Dockerfile (distroless static)"
```

---

## Task 18: GoReleaser config

**Files:**
- Create: `.goreleaser.yaml`

- [ ] **Step 1: Write `.goreleaser.yaml`**

```yaml
version: 2
project_name: anti-tangent-mcp

before:
  hooks:
    - go mod tidy

builds:
  - id: anti-tangent-mcp
    main: ./cmd/anti-tangent-mcp
    binary: anti-tangent-mcp
    env:
      - CGO_ENABLED=0
    goos: [linux, darwin, windows]
    goarch: [amd64, arm64]
    ldflags:
      - -s -w -X main.version={{.Version}}

archives:
  - formats: [tar.gz]
    name_template: >-
      {{ .ProjectName }}_{{ .Version }}_
      {{- title .Os }}_
      {{- if eq .Arch "amd64" }}x86_64
      {{- else if eq .Arch "386" }}i386
      {{- else }}{{ .Arch }}{{ end }}
    format_overrides:
      - goos: windows
        formats: [zip]

checksum:
  name_template: "checksums.txt"

changelog:
  disable: true

release:
  github:
    owner: patiently
    name: anti-tangent-mcp
```

- [ ] **Step 2: Local snapshot dry-run**

```bash
go install github.com/goreleaser/goreleaser/v2@latest
goreleaser release --snapshot --clean --skip=publish
```

Expected: `dist/` populated with tar.gz/zip archives for the five platform combos and a `checksums.txt`. Inspect one archive to confirm the binary is inside.

- [ ] **Step 3: Commit**

```bash
git add .goreleaser.yaml
git commit -m "build: add .goreleaser.yaml for cross-platform release artifacts"
```

---

## Task 19: CI workflow

**Files:**
- Create: `.github/workflows/ci.yml`

- [ ] **Step 1: Write `ci.yml`**

```yaml
name: CI

on:
  push:
    branches: ['**']
  pull_request:
    branches: ['**']
  workflow_call:

jobs:
  changelog:
    name: Changelog entry
    runs-on: ubuntu-latest
    steps:
      - name: Checkout code
        uses: actions/checkout@v6

      - name: Verify CHANGELOG.md entry for branch version
        run: |
          BRANCH="${{ github.head_ref || github.ref_name }}"
          echo "Branch: $BRANCH"

          if [[ ! "$BRANCH" =~ ^version/([0-9]+\.[0-9]+\.[0-9]+)$ ]]; then
            echo "Branch does not match version/X.Y.Z pattern — skipping changelog check"
            exit 0
          fi

          VERSION="${BASH_REMATCH[1]}"
          echo "Detected version: $VERSION"

          if ! grep -q "^## \[$VERSION\]" CHANGELOG.md; then
            echo "::error file=CHANGELOG.md::Missing changelog entry for version $VERSION"
            echo ""
            echo "Add an entry to CHANGELOG.md like:"
            echo ""
            echo "  ## [$VERSION] - YYYY-MM-DD"
            echo ""
            echo "  ### Added"
            echo "  - your changes here"
            echo ""
            exit 1
          fi

          echo "✓ Found changelog entry for $VERSION"

  build-test:
    name: Build & Test (Go)
    needs: changelog
    runs-on: ubuntu-latest
    steps:
      - name: Checkout code
        uses: actions/checkout@v6

      - name: Set up Go
        uses: actions/setup-go@v6
        with:
          go-version: '1.24'

      - name: Download dependencies
        run: go mod download

      - name: Vet
        run: go vet ./...

      - name: Build
        run: go build ./...

      - name: Install gotestsum
        run: go install gotest.tools/gotestsum@latest

      - name: Test
        run: gotestsum --format testdox --junitfile test-results.xml -- -race -count=1 ./...

      - name: Upload test results
        if: always()
        uses: actions/upload-artifact@v7
        with:
          name: test-results
          path: test-results.xml
```

- [ ] **Step 2: Commit**

```bash
git add .github/workflows/ci.yml
git commit -m "ci: GitHub Actions CI (changelog enforcement + go test -race)"
```

---

## Task 20: Release workflow

**Files:**
- Create: `.github/workflows/release.yml`

- [ ] **Step 1: Write `release.yml`**

```yaml
name: Release

on:
  push:
    branches: [main]

env:
  REGISTRY: ghcr.io

jobs:
  ci:
    name: CI
    uses: ./.github/workflows/ci.yml

  version:
    name: Calculate Version
    runs-on: ubuntu-latest
    needs: ci
    outputs:
      new_version: ${{ steps.version.outputs.new_version }}
      release_notes: ${{ steps.notes.outputs.notes }}
    steps:
      - name: Checkout code
        uses: actions/checkout@v6
        with:
          fetch-depth: 0

      - name: Calculate new version
        id: version
        run: |
          CURRENT=$(cat VERSION)
          IFS='.' read -r MAJOR MINOR PATCH <<< "$CURRENT"
          MSG=$(git log -1 --pretty=%B)

          if [[ "$MSG" == *"[major]"* ]]; then
            MAJOR=$((MAJOR + 1)); MINOR=0; PATCH=0
          elif [[ "$MSG" == *"[minor]"* ]]; then
            MINOR=$((MINOR + 1)); PATCH=0
          else
            PATCH=$((PATCH + 1))
          fi

          NEW="${MAJOR}.${MINOR}.${PATCH}"
          echo "new_version=$NEW" >> $GITHUB_OUTPUT
          echo "Bumping: $CURRENT -> $NEW"

      - name: Validate changelog
        run: |
          NEW="${{ steps.version.outputs.new_version }}"
          if ! grep -q "^## \[$NEW\]" CHANGELOG.md; then
            echo "ERROR: CHANGELOG.md must contain '## [$NEW]'"
            exit 1
          fi

      - name: Extract release notes
        id: notes
        run: |
          NEW="${{ steps.version.outputs.new_version }}"
          NOTES=$(awk "/^## \\[${NEW}\\]/{found=1; next} /^## \\[[0-9]+\\.[0-9]+\\.[0-9]+\\]/{if(found) exit} found{print}" CHANGELOG.md)
          {
            echo "notes<<EOF_NOTES"
            echo "$NOTES"
            echo "EOF_NOTES"
          } >> $GITHUB_OUTPUT

  tag:
    name: Bump VERSION and tag
    runs-on: ubuntu-latest
    needs: version
    permissions:
      contents: write
    steps:
      - name: Checkout code
        uses: actions/checkout@v6
        with:
          token: ${{ secrets.GITHUB_TOKEN }}

      - name: Configure git
        run: |
          git config user.name "github-actions[bot]"
          git config user.email "github-actions[bot]@users.noreply.github.com"

      - name: Update VERSION file
        run: |
          echo "${{ needs.version.outputs.new_version }}" > VERSION
          git add VERSION
          git commit -m "chore: release v${{ needs.version.outputs.new_version }} [skip ci]"
          git push

      - name: Create and push tag
        run: |
          git tag "v${{ needs.version.outputs.new_version }}"
          git push origin "v${{ needs.version.outputs.new_version }}"

  goreleaser:
    name: GoReleaser
    runs-on: ubuntu-latest
    needs: [version, tag]
    permissions:
      contents: write
    steps:
      - name: Checkout at tag
        uses: actions/checkout@v6
        with:
          fetch-depth: 0
          ref: v${{ needs.version.outputs.new_version }}

      - name: Set up Go
        uses: actions/setup-go@v6
        with:
          go-version: '1.24'

      - name: Write release notes
        run: |
          cat > .release-notes.md <<'EOF_NOTES'
          ${{ needs.version.outputs.release_notes }}
          EOF_NOTES

      - name: Run GoReleaser
        uses: goreleaser/goreleaser-action@v6
        with:
          version: latest
          args: release --clean --release-notes=.release-notes.md
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}

  docker:
    name: Build and push Docker image
    runs-on: ubuntu-latest
    needs: [version, tag, goreleaser]
    permissions:
      contents: read
      packages: write
    steps:
      - name: Checkout at tag
        uses: actions/checkout@v6
        with:
          ref: v${{ needs.version.outputs.new_version }}

      - name: Log in to Container Registry
        uses: docker/login-action@v4
        with:
          registry: ${{ env.REGISTRY }}
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}

      - name: Build and push image
        uses: docker/build-push-action@v7
        with:
          context: .
          file: Dockerfile
          push: true
          build-args: |
            VERSION=${{ needs.version.outputs.new_version }}
          tags: |
            ${{ env.REGISTRY }}/${{ github.repository_owner }}/anti-tangent-mcp:${{ needs.version.outputs.new_version }}
            ${{ env.REGISTRY }}/${{ github.repository_owner }}/anti-tangent-mcp:latest
```

- [ ] **Step 2: Commit**

```bash
git add .github/workflows/release.yml
git commit -m "ci: release workflow (version bump, tag, GoReleaser, GHCR)"
```

---

## Task 21: README

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Replace `README.md` content**

```markdown
# anti-tangent-mcp

An advisory MCP server that fights agent drift. Implementing subagents call three lifecycle tools — `validate_task_spec`, `check_progress`, `validate_completion` — and a reviewer LLM (Anthropic, OpenAI, or Google) returns structured findings against the task's acceptance criteria.

The reviewer is intentionally a different model than the implementer, so reviews are not blind to the implementer's blind spots.

## Install

```bash
go install github.com/patiently/anti-tangent-mcp@latest
```

Or grab a pre-built binary from the [releases page](https://github.com/patiently/anti-tangent-mcp/releases). Or pull the container image:

```bash
docker pull ghcr.io/patiently/anti-tangent-mcp:latest
```

## Configure

Set at least one provider key. The defaults route every hook through Anthropic; override per hook with env vars.

```
ANTHROPIC_API_KEY=sk-ant-...
OPENAI_API_KEY=sk-...
GOOGLE_API_KEY=...

# Optional per-hook model defaults:
ANTI_TANGENT_PRE_MODEL=anthropic:claude-sonnet-4-6
ANTI_TANGENT_MID_MODEL=anthropic:claude-haiku-4-5
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

## The 3 tools

- `validate_task_spec` — call once before coding. Returns findings on missing goals, weak acceptance criteria, unstated assumptions. Returns a `session_id` you thread through the next two calls.
- `check_progress` — call at checkpoints during implementation. Catches scope drift, untouched ACs, and unaddressed prior findings.
- `validate_completion` — call before claiming done. Walks every AC and non-goal explicitly.

All return the same envelope:

```json
{
  "session_id": "uuid",
  "verdict": "pass | warn | fail",
  "findings": [{ "severity", "category", "criterion", "evidence", "suggestion" }],
  "next_action": "one sentence",
  "model_used": "anthropic:claude-sonnet-4-6",
  "review_ms": 2341
}
```

## Design

Authoritative design: [`docs/superpowers/specs/2026-05-07-anti-tangent-mcp-design.md`](docs/superpowers/specs/2026-05-07-anti-tangent-mcp-design.md).

## License

MIT (or the user's choice — substitute when adding a `LICENSE` file).
```

- [ ] **Step 2: Commit**

```bash
git add README.md
git commit -m "docs: write README with install, config, and usage"
```

---

## Task 22: CLAUDE.md

**Files:**
- Modify: `CLAUDE.md`

- [ ] **Step 1: Replace `CLAUDE.md` content**

```markdown
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

```
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
```

- [ ] **Step 2: Commit**

```bash
git add CLAUDE.md
git commit -m "docs: write CLAUDE.md with conventions and architecture summary"
```

---

## Task 23: Initial CHANGELOG entry and final readiness

**Files:**
- Modify: `CHANGELOG.md`

- [ ] **Step 1: Replace the placeholder entry with the real one**

Edit `CHANGELOG.md` so the `[0.1.0]` block reads:

```markdown
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
```

- [ ] **Step 2: Run the full test suite one more time**

Run: `go test -race ./...`
Expected: All tests PASS.

- [ ] **Step 3: Run `go vet` and confirm clean**

Run: `go vet ./...`
Expected: No output.

- [ ] **Step 4: Confirm the binary builds with VERSION embedded**

Run: `go build -ldflags "-X main.version=$(cat VERSION)" -o /tmp/atm ./cmd/anti-tangent-mcp && /tmp/atm --version`
Expected: prints `0.1.0`.

- [ ] **Step 5: Commit**

```bash
git add CHANGELOG.md
git commit -m "docs(changelog): finalize 0.1.0 entry"
```

The repo is now ready for its first release. Pushing to `main` (or merging a PR into `main`) will trigger `release.yml`, which bumps `VERSION` to `0.1.1` (patch default) — but the CHANGELOG only contains `0.1.0`, so the release will fail until you write the next CHANGELOG entry. That's correct behavior: it forces a real entry per release.

To cut `0.1.0` itself as the very first release, push the merge commit with `[skip ci]` removed and ensure `VERSION` already contains `0.1.0` — the workflow will compute `0.1.1` and fail. To bootstrap, manually create the `v0.1.0` git tag and push it, then write CHANGELOG entries normally going forward. (This bootstrap step is a one-time event and is acceptable to do manually.)

---

## Self-Review Notes

Spec coverage check (each spec section → task):

- Architecture & request flow → Tasks 11–15
- 3 MCP tools (envelope, inputs, outputs) → Tasks 12, 13, 14
- Session lifecycle & state, TTL eviction → Tasks 5, 15
- Failure-mode mapping (session_not_found, payload_too_large) → Tasks 13, 14
- Reviewer interface → Task 7
- Per-provider structured-output strategy → Tasks 8, 9, 10
- Per-hook model defaults & overrides → Tasks 2, 12
- Prompt strategy (system + user, hook-specific) → Task 6
- Token-budget guardrails → Task 13 (`tooLargeEnvelope`)
- Configuration (env vars only) → Task 2
- Distribution (go install / GoReleaser / Docker) → Tasks 17, 18
- Logging & observability → Task 15
- Error handling philosophy → Tasks 12–14 (per-call), 2 (config), 4 (parse retry)
- Testing strategy (unit / integration / e2e) → Tasks 2–14 (unit), 16 (integration); e2e is documented in CLAUDE.md as `-tags=e2e` but no e2e test files are added in v1
- Project layout → matches the file map at the top of this plan
- Versioning, changelog, release automation → Tasks 1, 19, 20, 23
- CLAUDE.md → Task 22
- Initial release → Task 23
