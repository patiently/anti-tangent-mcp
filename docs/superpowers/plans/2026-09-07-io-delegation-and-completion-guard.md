# I/O Delegation + Completion Guard (v0.18.0) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship two MCP tools that route I/O-heavy implementer work to a cheap worker model, and two Claude Code plugins whose hooks make implementers actually use them and refuse to let a task close STAND when it skipped `validate_completion`.

**Architecture:** The server gains `bulk_read` / `code_write`, which reuse the existing provider layer by passing a one-field JSON schema (`{"answer"}` / `{"code"}`) so free-text output rides the structured-output path unchanged and truncation fails closed. Reads reuse `resolveFileInput`; writes get a new `resolveWriteTarget` because the read path symlink-resolves a full path that does not exist yet. Three new hooks live in two plugins, never in the server — the server stays advisory.

**Tech Stack:** Go 1.25, `github.com/modelcontextprotocol/go-sdk/mcp`, `text/template` + `embed`, `net/http` (no vendor SDKs), bash + jq (+ python3 for the guard hook).

**Spec:** `docs/superpowers/specs/2026-09-07-io-delegation-and-completion-guard-design.md`

## Global Constraints

- **Branch `version/0.18.0` already exists** with the spec and the `## [0.18.0] - 2026-09-07` CHANGELOG entry committed. Do **not** create it, and do **not** touch `VERSION` — the release workflow bumps it, and pre-bumping breaks the release workflow's changelog validation.
- **`ANTI_TANGENT_SHUNT_MIN_LINES` (default 350) is read by hook scripts ONLY.** It must never enter `internal/config`.
- **Every protocol part must stay strictly under 16,000 bytes**, and `INTEGRATION.md` under 2,000. `core.md` is at 14,460 and `implementer.md` at 14,017 — both tight. CI enforces this.
- **After editing any `docs/protocol/*.md`, resync the bundle in the SAME commit:** `rm -f plugin/anti-tangent-protocol/protocol/*.md && cp docs/protocol/*.md plugin/anti-tangent-protocol/protocol/`. CI byte-compares them.
- **New protocol headings are UNNUMBERED.** `scripts/check-protocol-docs.sh` asserts each tracked `§` appears exactly once; numbering a new section requires a registry edit and disturbs externally-cited numbers.
- **`rollup.json` JSON tags are a load-bearing cross-component contract** with the gnome-topbar consumer, which reads them by exact name. Stats changes are **additive only** — never rename or drop a key.
- **Never copy Apache-2.0 prose into `docs/protocol/`.** Upstream's "What doesn't get delegated" wording must be restated in our own words, or the obligation propagates into the bundled plugin copy.
- **All hooks fail open.** Any unexpected error exits 0. A hook that blocks because it broke is worse than the drift it prevents.
- `go test -race ./...` must pass. Unit tests never hit the network — `httptest.Server` only.

**The generated-code contract (binding on Tasks 6, 11 and 14 — state it the same way in all three).** Code produced by `code_write` with a `target_path` is **verified, not inspected**. The implementer owns choosing the reference file and proving the result works — by running the task's tests and build — and does NOT have to read the generated code. Requiring inspection would defeat the entire purpose, since the saving is precisely that the code never enters the implementer's context. Use the word *verification*; do not write "review" of generated code anywhere, because that reads as inspection and contradicts the tool's design.

## Pre-flight verification (run 2026-09-07, before dispatch)

`validate_plan` has no codebase access, so it flags every reference to existing
code as `unverifiable_codebase_claim` and asks for a pre-flight. This is that
pre-flight. Each claim below was checked against the working tree at commit
`8a79b52`; an implementer hitting a mismatch should stop, not improvise.

| Claim | Verified |
|---|---|
| Branch `version/0.18.0`, `## [0.18.0]` in CHANGELOG, `VERSION` untouched | yes |
| `docs/protocol/core.md` = 14,460 B; `implementer.md` = 14,017 B | exact |
| `marketplace.json` version `0.8.0`, 2 plugins | exact |
| `LICENSE` is MIT | yes |
| `golden(t, name, got)` at `internal/prompts/prompts_test.go:38` | exact |
| `effectiveMaxTokens` `handlers.go:664`; `resolveModel` `handlers.go:263` | exact |
| `resolveFileInput` `file_source.go:101`; `rejectControlChars` `:207`; `withinRoots` `:242` | exact |
| `openNoFollow` `file_source_unix.go:35`; that file's tag is `//go:build !windows` | exact |
| `formatEnvelopeSummary` `summary.go:23`; `computeRollup` `rollup.go:47` | exact |
| All three provider clients call `json.Unmarshal(req.JSONSchema, …)` | 1 each |
| `Recorder.Record` opens `if r == nil { return }` | yes |
| All 14 pre-existing `rollup.json` keys present exactly once | yes |
| `ci.yml` has a `protocol-docs` job; `build-test` is `needs: [changelog, protocol-docs]` (line 92) | exact |

Two defects this pre-flight found, which the reviewer structurally could not:

1. An unscoped `git grep` for stale catalog counts matches **11 files**, six of
   them historical plans and specs under `docs/superpowers/` — including this
   plan and its own spec, which quote the phrase while instructing its removal.
   Task 15's check is therefore scoped to the live surface; editing the
   historical record to satisfy a grep would be the wrong fix.
2. `docs/protocol/core.md:9` says "seven tools" and **no task updated it**.
   Assigned to Task 14, which is the task that edits that file.

**User decisions (already made):**
- Hook event is `PostToolUse` on `TaskUpdate` with `status=completed` — chosen over `SubagentStop` because it covers both `executing-plans` and `subagent-driven-development` via two pass signals.
- The guard ships as its own plugin, **active on install**; `ANTI_TANGENT_COMPLETION_GUARD=0` disables it.
- The guard blocks on **absence AND on `verdict: fail`**.
- `ANTI_TANGENT_WORKER_MODEL` falls back to `MidModel`.
- shunt is **ported, not depended on** — it requires a Spotify Portal instance. Apache-2.0 headers preserved, `THIRD_PARTY_NOTICES.md` shipped, 34 hook fixtures ported.
- Worker free-text output uses a **one-field JSON schema wrapper**, not a plain-text provider branch.
- `code_write` gains **`overwrite` (default false)**; the parent directory **must already exist** (no `MkdirAll`).
- Hook evals run as a **blocking CI job**.
- **Benchmarks are in scope**: all four scenarios reproduced against a pinned Go corpus.

---

### Task 1: Worker configuration

**Goal:** `config.Config` carries a worker model that falls back to `MidModel`, and a worker token cap clamped by the ceiling.

**Files:**
- Modify: `internal/config/config.go`
- Modify: `cmd/anti-tangent-mcp/main.go` (startup validation of the worker model)
- Test: `internal/config/config_test.go`

**Acceptance Criteria:**
- [ ] `ANTI_TANGENT_WORKER_MODEL` unset resolves `WorkerModel` to the resolved `MidModel`
- [ ] `ANTI_TANGENT_WORKER_MODEL=google:gemini-2.5-flash` overrides it
- [ ] A malformed value returns an error naming `ANTI_TANGENT_WORKER_MODEL`
- [ ] `WorkerMaxTokens` defaults to 4096 and is clamped to `MaxTokensCeiling`
- [ ] A non-positive `ANTI_TANGENT_WORKER_MAX_TOKENS` is a startup error
- [ ] A syntactically valid but non-allowlisted worker model (e.g. `anthropic:not-a-model`) fails `providers.ValidateModel`, covered by a test — Step 7 adds that call, so it needs an assertion

**Verify:** `go test -race ./internal/config/... -run Worker -v` → PASS

**Steps:**

- [ ] **Step 1: Write the failing tests**

Append to `internal/config/config_test.go`:

```go
func TestWorkerModelDefaultsToMidModel(t *testing.T) {
	env := map[string]string{"ANTHROPIC_API_KEY": "k"}
	cfg, err := Load(func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.WorkerModel != cfg.MidModel {
		t.Errorf("WorkerModel = %v, want MidModel %v", cfg.WorkerModel, cfg.MidModel)
	}
}

func TestWorkerModelOverride(t *testing.T) {
	env := map[string]string{
		"ANTHROPIC_API_KEY":        "k",
		"ANTI_TANGENT_WORKER_MODEL": "google:gemini-2.5-flash",
	}
	cfg, err := Load(func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.WorkerModel.String() != "google:gemini-2.5-flash" {
		t.Errorf("WorkerModel = %q", cfg.WorkerModel.String())
	}
}

func TestWorkerModelMalformed(t *testing.T) {
	env := map[string]string{
		"ANTHROPIC_API_KEY":        "k",
		"ANTI_TANGENT_WORKER_MODEL": "no-colon",
	}
	_, err := Load(func(k string) string { return env[k] })
	if err == nil || !strings.Contains(err.Error(), "ANTI_TANGENT_WORKER_MODEL") {
		t.Fatalf("want error naming the var, got %v", err)
	}
}

func TestWorkerMaxTokensDefaultAndClamp(t *testing.T) {
	env := map[string]string{"ANTHROPIC_API_KEY": "k"}
	cfg, err := Load(func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.WorkerMaxTokens != 4096 {
		t.Errorf("default WorkerMaxTokens = %d, want 4096", cfg.WorkerMaxTokens)
	}

	env["ANTI_TANGENT_WORKER_MAX_TOKENS"] = "999999"
	env["ANTI_TANGENT_MAX_TOKENS_CEILING"] = "8192"
	cfg, err = Load(func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.WorkerMaxTokens != 8192 {
		t.Errorf("clamped WorkerMaxTokens = %d, want 8192", cfg.WorkerMaxTokens)
	}
}

func TestWorkerMaxTokensNonPositive(t *testing.T) {
	env := map[string]string{
		"ANTHROPIC_API_KEY":              "k",
		"ANTI_TANGENT_WORKER_MAX_TOKENS": "0",
	}
	_, err := Load(func(k string) string { return env[k] })
	if err == nil || !strings.Contains(err.Error(), "ANTI_TANGENT_WORKER_MAX_TOKENS") {
		t.Fatalf("want error naming the var, got %v", err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/config/... -run Worker -v`
Expected: FAIL — `cfg.WorkerModel undefined`, `cfg.WorkerMaxTokens undefined`

- [ ] **Step 3: Add the fields**

In `internal/config/config.go`, add to the `Config` struct next to `ExtractModel` / `ExtractMaxTokens`:

```go
	// WorkerModel drives bulk_read / code_write. Resolution is explicit env
	// override -> MidModel. The reviewer != implementer principle does NOT
	// apply to the worker — same-model is fine, cheap is the only criterion —
	// so this chains to the cheap tier rather than to PlanModel like the
	// review tools do. Mirrors StatsModel's fallback exactly.
	WorkerModel ModelRef
	// WorkerMaxTokens caps worker output. Clamped by MaxTokensCeiling.
	WorkerMaxTokens int
```

- [ ] **Step 4: Set the default in the literal**

In the `cfg := Config{...}` literal, alongside `ExtractMaxTokens: 8192`:

```go
		WorkerMaxTokens:        4096,
```

- [ ] **Step 5: Resolve the model after MidModel is known**

Place this AFTER the `ExtractModel` resolution block (MidModel is resolved in the `defaults` loop above it):

```go
	// WorkerModel: optional override -> MidModel. Deliberately NOT chained to
	// PlanModel: worker calls want the cheap tier, and MidModel already is one.
	if v := env("ANTI_TANGENT_WORKER_MODEL"); v != "" {
		mr, err := ParseModelRef(v)
		if err != nil {
			return Config{}, fmt.Errorf("ANTI_TANGENT_WORKER_MODEL: %w", err)
		}
		cfg.WorkerModel = mr
	} else {
		cfg.WorkerModel = cfg.MidModel
	}
```

- [ ] **Step 6: Parse and clamp the token cap**

Place immediately BEFORE the existing `if v := env("ANTI_TANGENT_LOG_LEVEL")` block, so `MaxTokensCeiling` has already been read:

```go
	if v := env("ANTI_TANGENT_WORKER_MAX_TOKENS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("ANTI_TANGENT_WORKER_MAX_TOKENS: %w", err)
		}
		if n <= 0 {
			return Config{}, fmt.Errorf("ANTI_TANGENT_WORKER_MAX_TOKENS: must be positive, got %d", n)
		}
		cfg.WorkerMaxTokens = n
	}
	if cfg.WorkerMaxTokens > cfg.MaxTokensCeiling {
		cfg.WorkerMaxTokens = cfg.MaxTokensCeiling
	}
```

- [ ] **Step 7: Validate at startup**

In `cmd/anti-tangent-mcp/main.go`, after the `ExtractModel` validation:

```go
	if err := providers.ValidateModel(cfg.WorkerModel); err != nil {
		fail(logger, "worker model invalid", err)
	}
```

- [ ] **Step 8: Run tests to verify they pass**

Run: `go test -race ./internal/config/... -run Worker -v && go build ./...`
Expected: PASS, build clean

- [ ] **Step 9: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go cmd/anti-tangent-mcp/main.go
git commit -m "feat(config): worker model and token cap for bulk_read/code_write"
```

```json:metadata
{"files": ["internal/config/config.go", "internal/config/config_test.go", "cmd/anti-tangent-mcp/main.go"], "verifyCommand": "go test -race ./internal/config/... -run Worker -v", "acceptanceCriteria": ["WorkerModel falls back to MidModel", "ANTI_TANGENT_WORKER_MODEL overrides it", "malformed value errors naming the var", "WorkerMaxTokens defaults 4096 and clamps to ceiling", "non-positive token cap is a startup error"], "modelTier": "mechanical"}
```

---

### Task 2: Worker prompt templates

**Goal:** Two embedded templates and their render functions, with golden tests, producing the worker system + user prompts.

**Files:**
- Create: `internal/prompts/templates/worker_bulk_read.tmpl`
- Create: `internal/prompts/templates/worker_code_write.tmpl`
- Modify: `internal/prompts/prompts.go`
- Test: `internal/prompts/prompts_test.go`
- Create: `internal/prompts/testdata/worker_bulk_read.golden`, `internal/prompts/testdata/worker_code_write.golden`

**Acceptance Criteria:**
- [ ] `RenderWorkerBulkRead` wraps each file as `<file path="…">…</file>` and ends with the question
- [ ] `RenderWorkerCodeWrite` includes the reference file and the spec
- [ ] Both renderers return a non-empty `Output.System` carrying the package's standard system prompt — the goal promises system *and* user prompts, so `System` is part of the contract and both tests assert it
- [ ] Both return `prompts.Output` with `UserPrefix` empty (single-call, no cache breakpoint)
- [ ] Golden files match; `-update` regenerates them
- [ ] Instruction wording is paraphrased, not copied from upstream

**Verify:** `go test -race ./internal/prompts/... -run Worker -v` → PASS

**Steps:**

- [ ] **Step 1: Write the templates**

`{{.Path}}` and `{{.ReferencePath}}` are pre-escaped by the render function (Step 4). `text/template` escapes nothing, and an unescaped `"` in a path would close the attribute and let a filename forge prompt structure. `rejectControlChars` does NOT cover quotes or ampersands — both are legal in a Unix filename.

`internal/prompts/templates/worker_bulk_read.tmpl`:

```gotemplate
You are answering a specific question about source files that have been provided in full.

Ground rules:
- Answer ONLY the question asked. Do not summarise the files, do not describe what they
  are for, and do not volunteer observations the caller did not request.
- Reply as a flat list of bullets. Lead each bullet with an exact identifier from the
  source: a file name, a symbol name, a type, or a line number.
- If the files do not contain the answer, say so in one bullet. Do not speculate.
- Quote code only when the exact text is the answer.

{{range .Files}}<file path="{{.Path}}">
{{.Content}}
</file>

{{end}}## Question

{{.Question}}
```

`internal/prompts/templates/worker_code_write.tmpl`:

```gotemplate
You are generating code that must blend into an existing codebase.

Ground rules:
- Match the reference file's patterns, conventions, naming, formatting and idioms exactly.
- Where the specification is ambiguous, resolve it toward whatever the reference file does.
- Output ONLY the code. No explanation, no commentary, no markdown fences.

<file path="{{.ReferencePath}}">
{{.ReferenceContent}}
</file>

## What to generate

{{.Spec}}
```

- [ ] **Step 2: Write the failing tests**

Append to `internal/prompts/prompts_test.go`:

```go
func TestRenderWorkerBulkRead(t *testing.T) {
	out, err := RenderWorkerBulkRead(WorkerBulkReadInput{
		Question: "Which methods write to the database?",
		Files: []WorkerFile{
			{Path: "/repo/a.go", Content: "package a\n"},
			{Path: "/repo/b.go", Content: "package b\n"},
		},
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if out.System == "" {
		t.Error("Output.System must carry the package's standard system prompt")
	}
	if !strings.Contains(out.User, `<file path="/repo/a.go">`) {
		t.Error("missing file wrapper for a.go")
	}
	if !strings.Contains(out.User, "Which methods write to the database?") {
		t.Error("missing question")
	}
	if out.UserPrefix != "" {
		t.Errorf("UserPrefix should be empty on a single-call render, got %q", out.UserPrefix)
	}
	golden(t, "worker_bulk_read.golden", out.User)
}

func TestRenderWorkerCodeWrite(t *testing.T) {
	out, err := RenderWorkerCodeWrite(WorkerCodeWriteInput{
		Spec:             "A table test for Add.",
		ReferencePath:    "/repo/ref_test.go",
		ReferenceContent: "package ref\n",
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if out.System == "" {
		t.Error("Output.System must carry the package's standard system prompt")
	}
	if !strings.Contains(out.User, `<file path="/repo/ref_test.go">`) {
		t.Error("missing reference wrapper")
	}
	if !strings.Contains(out.User, "A table test for Add.") {
		t.Error("missing spec")
	}
	if out.UserPrefix != "" {
		t.Errorf("UserPrefix should be empty on a single-call render, got %q", out.UserPrefix)
	}
	golden(t, "worker_code_write.golden", out.User)
}

func TestRenderWorkerBulkReadEscapesAttributes(t *testing.T) {
	out, err := RenderWorkerBulkRead(WorkerBulkReadInput{
		Question: "q",
		Files:    []WorkerFile{{Path: `/repo/we"ird & <odd>.go`, Content: "package a\n"}},
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(out.User, `path="/repo/we"ird`) {
		t.Error("an unescaped quote broke out of the path attribute")
	}
	if !strings.Contains(out.User, "&#34;") || !strings.Contains(out.User, "&amp;") {
		t.Errorf("quote and ampersand must be escaped, got:\n%s", out.User)
	}
}
```

`golden` is the existing helper at `internal/prompts/prompts_test.go:38` — verified present. Do not add a second one.

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/prompts/... -run Worker -v`
Expected: FAIL — `undefined: RenderWorkerBulkRead`

- [ ] **Step 4: Add the input types and render functions**

In `internal/prompts/prompts.go`:

```go
// WorkerFile is one file attached to a worker call.
type WorkerFile struct {
	Path    string
	Content string
}

type WorkerBulkReadInput struct {
	Question string
	Files    []WorkerFile
}

// escapeAttr makes a path safe inside a path="…" attribute. text/template
// escapes nothing, so a filename containing a double quote would otherwise
// close the attribute and have the remainder read as prompt structure. Quotes
// and ampersands are legal in Unix filenames and are NOT covered by
// rejectControlChars, which refuses only control and Unicode format characters.
func escapeAttr(p string) string { return html.EscapeString(p) }

type WorkerCodeWriteInput struct {
	Spec             string
	ReferencePath    string
	ReferenceContent string
}

// RenderWorkerBulkRead renders the bulk_read worker prompt. UserPrefix is
// deliberately left empty: this is a single call, and an Anthropic cache
// breakpoint on a single call is a 1.25x write against zero reads.
func RenderWorkerBulkRead(in WorkerBulkReadInput) (Output, error) {
	esc := make([]WorkerFile, len(in.Files))
	for i, f := range in.Files {
		esc[i] = WorkerFile{Path: escapeAttr(f.Path), Content: f.Content}
	}
	in.Files = esc
	body, err := renderTemplate("worker_bulk_read.tmpl", in)
	if err != nil {
		return Output{}, err
	}
	return Output{System: systemPrompt, User: body, UserSuffix: body}, nil
}

// RenderWorkerCodeWrite renders the code_write worker prompt.
func RenderWorkerCodeWrite(in WorkerCodeWriteInput) (Output, error) {
	in.ReferencePath = escapeAttr(in.ReferencePath)
	body, err := renderTemplate("worker_code_write.tmpl", in)
	if err != nil {
		return Output{}, err
	}
	return Output{System: systemPrompt, User: body, UserSuffix: body}, nil
}
```

Use whatever the package's existing template-execution helper is called; if there is no shared `renderTemplate`, follow the pattern the neighbouring `RenderPrime` uses verbatim rather than inventing a new one.

- [ ] **Step 5: Generate the goldens and verify**

Run: `go test ./internal/prompts/... -run Worker -update && go test -race ./internal/prompts/... -run Worker -v`
Expected: PASS. Inspect both `testdata/worker_*.golden` before committing — a golden is only useful if a human read it once.

- [ ] **Step 6: Commit**

```bash
git add internal/prompts/
git commit -m "feat(prompts): worker templates for bulk_read and code_write"
```

```json:metadata
{"files": ["internal/prompts/templates/worker_bulk_read.tmpl", "internal/prompts/templates/worker_code_write.tmpl", "internal/prompts/prompts.go", "internal/prompts/prompts_test.go", "internal/prompts/testdata/worker_bulk_read.golden", "internal/prompts/testdata/worker_code_write.golden"], "verifyCommand": "go test -race ./internal/prompts/... -run Worker -v", "acceptanceCriteria": ["bulk_read prompt wraps files in <file path=...> and ends with the question", "code_write prompt carries reference file and spec", "UserPrefix empty on both", "both tests assert a non-empty Output.System", "goldens match and -update regenerates", "instruction wording paraphrased not copied"], "modelTier": "mechanical"}
```

---

### Task 3: Worker call path

**Goal:** One shared helper that invokes the worker model with a single-field JSON schema and returns the extracted text plus token counts.

**Files:**
- Create: `internal/mcpsrv/worker_call.go`
- Test: `internal/mcpsrv/worker_call_test.go`

**Acceptance Criteria:**
- [ ] `workerSchema("answer")` produces a strict one-field object schema
- [ ] `runWorker` returns the field's string value, model, elapsed ms, and both token counts
- [ ] A truncated provider response surfaces `providers.ErrResponseTruncated` unwrapped by `errors.Is`
- [ ] Malformed JSON returns an error naming the field, and the raw body is NOT echoed into it
- [ ] No network: tests drive a `providers.Reviewer` fake

**Verify:** `go test -race ./internal/mcpsrv/... -run Worker -v` → PASS

**Steps:**

- [ ] **Step 1: Write the failing tests**

Create `internal/mcpsrv/worker_call_test.go`:

```go
package mcpsrv

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/patiently/anti-tangent-mcp/internal/config"
	"github.com/patiently/anti-tangent-mcp/internal/prompts"
	"github.com/patiently/anti-tangent-mcp/internal/providers"
)

type fakeWorkerReviewer struct {
	resp providers.Response
	err  error
	got  providers.Request
}

func (f *fakeWorkerReviewer) Name() string { return "fake" }
func (f *fakeWorkerReviewer) Review(_ context.Context, r providers.Request) (providers.Response, error) {
	f.got = r
	return f.resp, f.err
}

func TestWorkerSchemaIsStrictOneField(t *testing.T) {
	var m map[string]any
	if err := json.Unmarshal(workerSchema("answer"), &m); err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}
	if m["type"] != "object" {
		t.Errorf("type = %v, want object", m["type"])
	}
	props, _ := m["properties"].(map[string]any)
	if len(props) != 1 {
		t.Errorf("want exactly one property, got %d: %v", len(props), props)
	}
	prop, _ := props["answer"].(map[string]any)
	if prop == nil {
		t.Fatal("schema missing the answer property")
	}
	if prop["type"] != "string" {
		t.Errorf("answer type = %v, want string", prop["type"])
	}
	req, _ := m["required"].([]any)
	if len(req) != 1 || req[0] != "answer" {
		t.Errorf("required = %v, want exactly [answer]", req)
	}
	if m["additionalProperties"] != false {
		t.Error("schema must set additionalProperties:false")
	}
}

func TestRunWorkerExtractsField(t *testing.T) {
	f := &fakeWorkerReviewer{resp: providers.Response{
		RawJSON:      []byte(`{"answer":"- a.go: does X"}`),
		Model:        "anthropic:claude-haiku-4-5-20251001",
		InputTokens:  120,
		OutputTokens: 9,
	}}
	h := &handlers{deps: Deps{Reviews: providers.Registry{"anthropic": f}}}
	got, err := h.runWorker(context.Background(),
		config.ModelRef{Provider: "anthropic", Model: "claude-haiku-4-5-20251001"},
		prompts.Output{System: "sys", User: "usr"}, 4096, "answer")
	if err != nil {
		t.Fatalf("runWorker: %v", err)
	}
	if got.Text != "- a.go: does X" {
		t.Errorf("Text = %q", got.Text)
	}
	if got.InputTokens != 120 || got.OutputTokens != 9 {
		t.Errorf("tokens = %d/%d, want 120/9", got.InputTokens, got.OutputTokens)
	}
	if f.got.MaxTokens != 4096 {
		t.Errorf("MaxTokens = %d, want 4096", f.got.MaxTokens)
	}
	if got.Model != "anthropic:claude-haiku-4-5-20251001" {
		t.Errorf("Model = %q, want the provider's reported model", got.Model)
	}
	if got.ReviewMS < 0 {
		t.Errorf("ReviewMS = %d, want >= 0", got.ReviewMS)
	}
}

func TestRunWorkerFallsBackToConfiguredModel(t *testing.T) {
	// An empty Response.Model must not produce an empty model_used.
	f := &fakeWorkerReviewer{resp: providers.Response{RawJSON: []byte(`{"answer":"x"}`)}}
	h := &handlers{deps: Deps{Reviews: providers.Registry{"anthropic": f}}}
	got, err := h.runWorker(context.Background(),
		config.ModelRef{Provider: "anthropic", Model: "claude-haiku-4-5-20251001"},
		prompts.Output{}, 10, "answer")
	if err != nil {
		t.Fatal(err)
	}
	if got.Model != "anthropic:claude-haiku-4-5-20251001" {
		t.Errorf("Model = %q, want the configured ref as fallback", got.Model)
	}
}

func TestRunWorkerPropagatesTruncation(t *testing.T) {
	f := &fakeWorkerReviewer{err: providers.ErrResponseTruncated}
	h := &handlers{deps: Deps{Reviews: providers.Registry{"anthropic": f}}}
	_, err := h.runWorker(context.Background(),
		config.ModelRef{Provider: "anthropic", Model: "claude-haiku-4-5-20251001"},
		prompts.Output{}, 10, "answer")
	if !errors.Is(err, providers.ErrResponseTruncated) {
		t.Fatalf("want ErrResponseTruncated, got %v", err)
	}
}

func TestRunWorkerMalformedJSONDoesNotEchoBody(t *testing.T) {
	f := &fakeWorkerReviewer{resp: providers.Response{RawJSON: []byte(`{"answer": SECRET`)}}
	h := &handlers{deps: Deps{Reviews: providers.Registry{"anthropic": f}}}
	_, err := h.runWorker(context.Background(),
		config.ModelRef{Provider: "anthropic", Model: "claude-haiku-4-5-20251001"},
		prompts.Output{}, 10, "answer")
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "answer") {
		t.Errorf("error should name the field, got %v", err)
	}
	if strings.Contains(err.Error(), "SECRET") {
		t.Errorf("error must not echo the raw body, got %v", err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/mcpsrv/... -run Worker -v`
Expected: FAIL — `undefined: workerSchema`, `undefined: runWorker`

- [ ] **Step 3: Implement**

Create `internal/mcpsrv/worker_call.go`:

```go
// Package mcpsrv: the shared invocation path for the I/O-delegation tools
// (bulk_read, code_write).
//
// Why a one-field JSON schema rather than a plain-text provider branch: all
// three provider clients unconditionally unmarshal req.JSONSchema and force
// structured output, so there is no text path to use. Wrapping the answer in
// a single-field object reuses that path with a zero-line provider diff, and
// — the deciding property — makes truncation FAIL CLOSED. A response cut at
// max_tokens mid-string does not parse, so code_write refuses to write half a
// file instead of writing it. A plain-text branch would need each provider's
// own stop_reason / finish_reason / finishReason spelling to get the same
// safety. See design §3.2.
package mcpsrv

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/patiently/anti-tangent-mcp/internal/config"
	"github.com/patiently/anti-tangent-mcp/internal/prompts"
	"github.com/patiently/anti-tangent-mcp/internal/providers"
)

// workerResult is one completed worker call.
type workerResult struct {
	Text         string
	Model        string
	ReviewMS     int64
	InputTokens  int
	OutputTokens int
}

// workerSchema builds the strict single-field object schema for a worker call.
// additionalProperties:false keeps a chatty model from smuggling commentary
// alongside the payload.
func workerSchema(field string) []byte {
	return []byte(fmt.Sprintf(
		`{"type":"object","properties":{%q:{"type":"string"}},"required":[%q],"additionalProperties":false}`,
		field, field))
}

// runWorker invokes the worker model and returns the named field's value.
//
// The error from a malformed response deliberately does NOT include the raw
// body: worker input is arbitrary repository source, so echoing an unparsed
// response into an error surfaces file content through a channel that is not
// the tool's declared output.
func (h *handlers) runWorker(
	ctx context.Context,
	model config.ModelRef,
	rendered prompts.Output,
	maxTokens int,
	field string,
) (workerResult, error) {
	rv, err := h.deps.Reviews.Get(model.Provider)
	if err != nil {
		return workerResult{}, err
	}

	start := time.Now()
	resp, err := rv.Review(ctx, providers.Request{
		Model:      model.Model,
		System:     rendered.System,
		User:       rendered.User,
		MaxTokens:  maxTokens,
		JSONSchema: workerSchema(field),
	})
	ms := time.Since(start).Milliseconds()
	if err != nil {
		return workerResult{}, err
	}

	var payload map[string]string
	if err := json.Unmarshal(resp.RawJSON, &payload); err != nil {
		return workerResult{}, fmt.Errorf(
			"worker response did not parse as an object with a %q field (%d bytes)", field, len(resp.RawJSON))
	}
	text, ok := payload[field]
	if !ok {
		return workerResult{}, fmt.Errorf("worker response is missing the %q field", field)
	}

	modelUsed := resp.Model
	if modelUsed == "" {
		modelUsed = model.String()
	}
	return workerResult{
		Text:         text,
		Model:        modelUsed,
		ReviewMS:     ms,
		InputTokens:  resp.InputTokens,
		OutputTokens: resp.OutputTokens,
	}, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -race ./internal/mcpsrv/... -run Worker -v`
Expected: all five `Worker` tests pass

- [ ] **Step 5: Commit**

```bash
git add internal/mcpsrv/worker_call.go internal/mcpsrv/worker_call_test.go
git commit -m "feat(mcpsrv): worker call path with one-field JSON schema"
```

```json:metadata
{"files": ["internal/mcpsrv/worker_call.go", "internal/mcpsrv/worker_call_test.go"], "verifyCommand": "go test -race ./internal/mcpsrv/... -run Worker -v", "acceptanceCriteria": ["workerSchema emits strict one-field object schema", "runWorker returns text plus model, ms and token counts", "truncation propagates for errors.Is", "malformed JSON errors naming the field without echoing the body"], "modelTier": "standard"}
```

---

### Task 4: `bulk_read` tool

**Goal:** An MCP tool that reads 1–50 absolute paths server-side and answers a question about them, returning no file content.

**Files:**
- Create: `internal/mcpsrv/worker_handlers.go`
- Modify: `internal/mcpsrv/server.go`
- Test: `internal/mcpsrv/worker_handlers_test.go`

**Acceptance Criteria:**
- [ ] `bulk_read` appears in the tool catalog
- [ ] Empty `question` or empty `paths` is a validation error
- [ ] More than 50 paths is rejected naming the limit
- [ ] A relative path is rejected before any provider call — the goal and tool description both promise absolute paths
- [ ] Paths go through `resolveFileInput`, so roots containment, `O_NOFOLLOW` and control-character refusal all apply
- [ ] The cap counts **caller-controlled bytes only**: `len(question)` + the sum of file bytes. Template scaffolding and `<file>` wrappers are excluded — server-controlled, bounded, and counting them would make the effective cap drift with every template edit. Stated in a comment so it is not re-litigated
- [ ] A multi-file call that crosses the cap on the LAST file is refused, tested at the boundary
- [ ] Total bytes over `MaxPayloadBytes` returns the structured too-large envelope, never a truncated read
- [ ] Success returns `answer`, `model_used`, `review_ms`, `input_tokens`, `output_tokens`, `files_read`, `bytes_read`
- [ ] The response contains no file content other than what the worker quoted

**Verify:** `go test -race ./internal/mcpsrv/... -run BulkRead -v` → PASS

**Steps:**

- [ ] **Step 1: Write the failing tests**

Create `internal/mcpsrv/worker_handlers_test.go`:

```go
package mcpsrv

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/patiently/anti-tangent-mcp/internal/config"
	"github.com/patiently/anti-tangent-mcp/internal/providers"
)

func workerDeps(t *testing.T, rv providers.Reviewer, roots []string) Deps {
	t.Helper()
	return Deps{
		Cfg: config.Config{
			WorkerModel:      config.ModelRef{Provider: "anthropic", Model: "claude-haiku-4-5-20251001"},
			WorkerMaxTokens:  4096,
			MaxTokensCeiling: 16384,
			MaxPayloadBytes:  204800,
			PlanRoots:        roots,
		},
		Reviews: providers.Registry{"anthropic": rv},
	}
}

func TestBulkReadRejectsEmptyQuestion(t *testing.T) {
	h := &handlers{deps: workerDeps(t, &fakeWorkerReviewer{}, nil)}
	_, _, err := h.BulkRead(context.Background(), nil, BulkReadArgs{Paths: []string{"/tmp/x"}})
	if err == nil || !strings.Contains(err.Error(), "question") {
		t.Fatalf("want a question validation error, got %v", err)
	}
}

func TestBulkReadRejectsTooManyPaths(t *testing.T) {
	paths := make([]string, 51)
	for i := range paths {
		paths[i] = "/tmp/x"
	}
	h := &handlers{deps: workerDeps(t, &fakeWorkerReviewer{}, nil)}
	_, _, err := h.BulkRead(context.Background(), nil, BulkReadArgs{Question: "q", Paths: paths})
	if err == nil || !strings.Contains(err.Error(), "50") {
		t.Fatalf("want an error naming the 50-path limit, got %v", err)
	}
}

func TestBulkReadRefusesOutsideRoots(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.go")
	if err := os.WriteFile(outside, []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := &handlers{deps: workerDeps(t, &fakeWorkerReviewer{}, []string{dir})}
	_, _, err := h.BulkRead(context.Background(), nil, BulkReadArgs{Question: "q", Paths: []string{outside}})
	if err == nil || !strings.Contains(err.Error(), "PLAN_ROOTS") {
		t.Fatalf("want a roots refusal, got %v", err)
	}
}

func TestBulkReadHappyPath(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.go")
	if err := os.WriteFile(p, []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := &fakeWorkerReviewer{resp: providers.Response{
		RawJSON: []byte(`{"answer":"- a.go: package a"}`), Model: "anthropic:claude-haiku-4-5-20251001",
		InputTokens: 42, OutputTokens: 7,
	}}
	h := &handlers{deps: workerDeps(t, f, []string{dir})}
	_, res, err := h.BulkRead(context.Background(), nil, BulkReadArgs{Question: "what package?", Paths: []string{p}})
	if err != nil {
		t.Fatalf("BulkRead: %v", err)
	}
	if res.Answer != "- a.go: package a" {
		t.Errorf("Answer = %q", res.Answer)
	}
	if res.FilesRead != 1 || res.BytesRead != len("package a\n") {
		t.Errorf("FilesRead=%d BytesRead=%d", res.FilesRead, res.BytesRead)
	}
	if res.InputTokens != 42 || res.OutputTokens != 7 {
		t.Errorf("tokens = %d/%d", res.InputTokens, res.OutputTokens)
	}
}

func TestBulkReadRefusesControlCharPath(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a\nb.go")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Skipf("filesystem rejects the name: %v", err)
	}
	h := &handlers{deps: workerDeps(t, &fakeWorkerReviewer{}, []string{dir})}
	_, _, err := h.BulkRead(context.Background(), nil, BulkReadArgs{Question: "q", Paths: []string{p}})
	if err == nil || !strings.Contains(err.Error(), "U+") {
		t.Fatalf("want a control-character refusal naming the code point, got %v", err)
	}
}

func TestBulkReadAnswerCarriesNoUnquotedSource(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.go")
	const sentinel = "SENTINEL_NOT_IN_ANSWER_9f3a"
	if err := os.WriteFile(p, []byte("package a // "+sentinel+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := &fakeWorkerReviewer{resp: providers.Response{RawJSON: []byte(`{"answer":"- a.go: package a"}`)}}
	h := &handlers{deps: workerDeps(t, f, []string{dir})}
	_, res, err := h.BulkRead(context.Background(), nil, BulkReadArgs{Question: "q", Paths: []string{p}})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(res)
	if strings.Contains(string(b), sentinel) {
		t.Error("file content the worker did not quote leaked into the response")
	}
}

func TestBulkReadCapCrossedOnLastFile(t *testing.T) {
	dir := t.TempDir()
	var paths []string
	for i := 0; i < 3; i++ {
		fp := filepath.Join(dir, fmt.Sprintf("f%d.go", i))
		if err := os.WriteFile(fp, make([]byte, 40), 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, fp)
	}
	d := workerDeps(t, &fakeWorkerReviewer{}, []string{dir})
	d.Cfg.MaxPayloadBytes = 100 // question(1) + 40 + 40 fits; the third crosses
	h := &handlers{deps: d}
	_, res, err := h.BulkRead(context.Background(), nil, BulkReadArgs{Question: "q", Paths: paths})
	if err != nil {
		t.Fatalf("want an envelope, got transport error: %v", err)
	}
	if res.Verdict != "fail" {
		t.Errorf("crossing the cap on the last file must refuse, got %+v", res)
	}
}

// catalogHas connects an in-memory client to a real server and reports whether
// the named tool is advertised. Mirrors the transport setup already used by
// internal/mcpsrv/integration_test.go — do not invent a second mechanism.
func catalogHas(t *testing.T, name string) bool {
	t.Helper()
	cfg, err := config.Load(func(k string) string {
		if k == "ANTHROPIC_API_KEY" {
			return "k"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := New(Deps{Cfg: cfg, Reviews: providers.Registry{"anthropic": &fakeWorkerReviewer{}}})

	st, ct := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	go func() { _ = srv.Run(ctx, st) }()

	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })

	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	for _, tool := range res.Tools {
		if tool.Name == name {
			return true
		}
	}
	return false
}

// Named so `-run BulkRead` selects it, and asserting ONLY bulk_read: code_write
// is not registered until Task 6, so a combined assertion here would leave
// `go test ./...` red for every task in between. Task 6 adds its own.
func TestBulkReadRegisteredInCatalog(t *testing.T) {
	if !catalogHas(t, "bulk_read") {
		t.Error("tool \"bulk_read\" is not registered")
	}
}

func TestBulkReadRejectsEmptyPaths(t *testing.T) {
	h := &handlers{deps: workerDeps(t, &fakeWorkerReviewer{}, nil)}
	_, _, err := h.BulkRead(context.Background(), nil, BulkReadArgs{Question: "q"})
	if err == nil || !strings.Contains(err.Error(), "paths") {
		t.Fatalf("want a paths validation error, got %v", err)
	}
}

func TestBulkReadTooLargePayload(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "big.go")
	if err := os.WriteFile(p, make([]byte, 5000), 0o644); err != nil {
		t.Fatal(err)
	}
	d := workerDeps(t, &fakeWorkerReviewer{}, []string{dir})
	d.Cfg.MaxPayloadBytes = 100
	h := &handlers{deps: d}
	_, res, err := h.BulkRead(context.Background(), nil, BulkReadArgs{Question: "q", Paths: []string{p}})
	if err != nil {
		t.Fatalf("too-large must be an envelope, not a transport error: %v", err)
	}
	if res.Verdict != "fail" || len(res.Findings) == 0 {
		t.Fatalf("want a fail envelope with findings, got %+v", res)
	}
	if res.Answer != "" {
		t.Error("a too-large refusal must not carry an answer")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/mcpsrv/... -run BulkRead -v`
Expected: FAIL — `undefined: BulkReadArgs`

- [ ] **Step 3: Implement the handler**

Create `internal/mcpsrv/worker_handlers.go`:

```go
// Package mcpsrv: the I/O-delegation tools. These do NOT review anything —
// they route file reading and boilerplate generation to a cheap worker model
// so the corpus never enters the implementer's context. Kept out of
// handlers.go, which is already large and is about the review loop.
package mcpsrv

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/patiently/anti-tangent-mcp/internal/prompts"
	"github.com/patiently/anti-tangent-mcp/internal/providers"
	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

// maxBulkReadPaths caps one call's attachment count. 50 is upstream's limit
// and is well under what any allowlisted model's context can hold at the
// payload cap.
const maxBulkReadPaths = 50

type BulkReadArgs struct {
	Question          string   `json:"question" jsonschema:"required"`
	Paths             []string `json:"paths"    jsonschema:"required"`
	Model             string   `json:"model,omitempty"`
	MaxTokensOverride int      `json:"max_tokens_override,omitempty"`
}

// BulkReadResult is the tool response. Verdict/Findings are populated ONLY on
// a structured refusal (payload too large), matching the review tools' shape
// so a caller can branch on the same field it already knows.
type BulkReadResult struct {
	Answer       string            `json:"answer,omitempty"`
	ModelUsed    string            `json:"model_used"`
	ReviewMS     int64             `json:"review_ms"`
	InputTokens  int               `json:"input_tokens,omitempty"`
	OutputTokens int               `json:"output_tokens,omitempty"`
	FilesRead    int               `json:"files_read,omitempty"`
	BytesRead    int               `json:"bytes_read,omitempty"`
	Verdict      string            `json:"verdict,omitempty"`
	Findings     []verdict.Finding `json:"findings,omitempty"`
}

func bulkReadTool() *mcp.Tool {
	return &mcp.Tool{
		Name: "bulk_read",
		Description: "Answer a question about one or more source files WITHOUT pulling them into your context. " +
			"The server reads the files itself and asks a cheap worker model your question, returning only the answer. " +
			"Pass ABSOLUTE paths. Ask a specific question — not 'summarise this file'. " +
			"If you intend to EDIT the code, follow the answer with a targeted Read (offset/limit) of the region it names: " +
			"the answer carries no reliable line anchors.",
	}
}

func (h *handlers) BulkRead(ctx context.Context, _ *mcp.CallToolRequest, args BulkReadArgs) (*mcp.CallToolResult, BulkReadResult, error) {
	if strings.TrimSpace(args.Question) == "" {
		return nil, BulkReadResult{}, errors.New("question is required and must not be empty")
	}
	if len(args.Paths) == 0 {
		return nil, BulkReadResult{}, errors.New("paths is required and must contain at least one absolute path")
	}
	if len(args.Paths) > maxBulkReadPaths {
		return nil, BulkReadResult{}, fmt.Errorf(
			"paths has %d entries, which exceeds the limit of %d; split the call", len(args.Paths), maxBulkReadPaths)
	}

	maxTokens, _, err := effectiveMaxTokens(args.MaxTokensOverride, h.deps.Cfg.WorkerMaxTokens, h.deps.Cfg.MaxTokensCeiling)
	if err != nil {
		return nil, BulkReadResult{}, err
	}
	model, err := h.resolveModel(args.Model, h.deps.Cfg.WorkerModel)
	if err != nil {
		return nil, BulkReadResult{}, err
	}

	// Read every file first, accumulating against the shared payload cap. The
	// cap is checked as we go so an oversized SET is refused before any
	// provider call is made, not after paying for one.
	//
	// WHAT COUNTS toward the cap: caller-controlled bytes only — the question
	// plus file contents. Template scaffolding and <file> wrappers are excluded
	// deliberately; they are server-controlled and bounded, and counting them
	// would make the effective cap drift whenever the template is edited.
	files := make([]prompts.WorkerFile, 0, len(args.Paths))
	total := len(args.Question)
	for _, p := range args.Paths {
		content, src, err := resolveFileInput(p, h.deps.Cfg.PlanRoots, h.deps.Cfg.MaxPayloadBytes)
		if errors.Is(err, errTooLarge) {
			return nil, bulkReadTooLarge(src.Bytes, h.deps.Cfg.MaxPayloadBytes, model.String()), nil
		}
		if err != nil {
			return nil, BulkReadResult{}, err
		}
		total += src.Bytes
		if total > h.deps.Cfg.MaxPayloadBytes {
			return nil, bulkReadTooLarge(total, h.deps.Cfg.MaxPayloadBytes, model.String()), nil
		}
		files = append(files, prompts.WorkerFile{Path: src.Path, Content: content})
	}

	rendered, err := prompts.RenderWorkerBulkRead(prompts.WorkerBulkReadInput{Question: args.Question, Files: files})
	if err != nil {
		return nil, BulkReadResult{}, fmt.Errorf("render bulk_read prompt: %w", err)
	}

	wr, err := h.runWorker(ctx, model, rendered, maxTokens, "answer")
	if errors.Is(err, providers.ErrResponseTruncated) {
		return nil, BulkReadResult{}, fmt.Errorf(
			"worker response was cut off at the token limit; raise ANTI_TANGENT_WORKER_MAX_TOKENS or ask a narrower question: %w", err)
	}
	if err != nil {
		return nil, BulkReadResult{}, err
	}

	bytesRead := 0
	for _, f := range files {
		bytesRead += len(f.Content)
	}
	return nil, BulkReadResult{
		Answer:       wr.Text,
		ModelUsed:    wr.Model,
		ReviewMS:     wr.ReviewMS,
		InputTokens:  wr.InputTokens,
		OutputTokens: wr.OutputTokens,
		FilesRead:    len(files),
		BytesRead:    bytesRead,
	}, nil
}

// bulkReadTooLarge mirrors tooLargeEnvelope's shape for a tool that has no
// session id. Critical severity so a caller branching on verdict sees fail.
func bulkReadTooLarge(size, limit int, model string) BulkReadResult {
	return BulkReadResult{
		ModelUsed: model,
		Verdict:   string(verdict.VerdictFail),
		Findings: []verdict.Finding{{
			Severity:   verdict.SeverityCritical,
			Category:   verdict.CategoryTooLarge,
			Criterion:  "payload",
			Evidence:   fmt.Sprintf("payload %d bytes exceeds cap %d", size, limit),
			Suggestion: "Split the call across fewer paths, or raise ANTI_TANGENT_MAX_PAYLOAD_BYTES.",
		}},
	}
}
```

- [ ] **Step 4: Register the tool**

In `internal/mcpsrv/server.go`, after `planRunReportTool`:

```go
	mcp.AddTool(srv, bulkReadTool(), h.BulkRead)
```

Update `New`'s doc comment: the count is now eight (nine after Task 6).

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test -race ./internal/mcpsrv/... -run BulkRead -v && go build ./...`
Expected: every `BulkRead` test passes, build clean. Do not assert a fixed test count here — it drifts every time a case is added.

- [ ] **Step 6: Commit**

```bash
git add internal/mcpsrv/worker_handlers.go internal/mcpsrv/worker_handlers_test.go internal/mcpsrv/server.go
git commit -m "feat(mcpsrv): add bulk_read tool"
```

```json:metadata
{"files": ["internal/mcpsrv/worker_handlers.go", "internal/mcpsrv/worker_handlers_test.go", "internal/mcpsrv/server.go"], "verifyCommand": "go test -race ./internal/mcpsrv/... -run BulkRead -v", "acceptanceCriteria": ["bulk_read registered in the catalog", "empty question or paths is a validation error", "more than 50 paths rejected naming the limit", "paths honour PLAN_ROOTS via resolveFileInput", "oversized payload returns a fail envelope not a truncated read", "success returns answer plus model, ms, tokens, files_read, bytes_read"], "modelTier": "standard"}
```

---

### Task 5: `resolveWriteTarget`

**Goal:** A write-path resolver that containment-checks the parent, refuses control characters, and opens the leaf without following symlinks.

**Files:**
- Create: `internal/mcpsrv/file_target.go`
- Create: `internal/mcpsrv/file_target_unix.go`
- Create: `internal/mcpsrv/file_target_windows.go`
- Test: `internal/mcpsrv/file_target_test.go`
- Test: `internal/mcpsrv/file_target_unix_test.go` (`//go:build !windows`; holds the leaf-symlink test only)

**Acceptance Criteria:**
- [ ] Relative, empty and whitespace-only paths are refused, each with its own table case
- [ ] A Unicode **format** character (e.g. U+200B) is refused, not only a C0 control like a newline — the two take different branches of `rejectControlChars`
- [ ] A parent that does not exist is refused naming the directory — no `MkdirAll`
- [ ] A parent outside `PlanRoots` is refused
- [ ] A resolved path containing a control or Unicode format character is refused
- [ ] With `overwrite=false`, an existing file is refused
- [ ] With `overwrite=true`, an existing regular file is truncated and rewritten
- [ ] **On Unix:** a symlink at the leaf is refused under BOTH `overwrite` values
- [ ] **On Windows:** the leaf-symlink guarantee does NOT hold. This is an accepted, documented non-goal — Go's `syscall` package exposes no `O_NOFOLLOW` equivalent there, exactly as for the existing read path's `openNoFollow`. Do not claim the unconditional contract while shipping the gap.
- [ ] A symlinked parent inside the roots resolves and is allowed

**Verify:** `go test -race ./internal/mcpsrv/... -run WriteTarget -v` → PASS

**Steps:**

- [ ] **Step 1: Write the failing tests**

Create `internal/mcpsrv/file_target_test.go`:

```go
package mcpsrv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteTargetRejectsRelative(t *testing.T) {
	if _, err := resolveWriteTarget("rel/path.go", nil, false); err == nil {
		t.Fatal("want an error for a relative path")
	}
}

func TestWriteTargetRejectsMissingParent(t *testing.T) {
	p := filepath.Join(t.TempDir(), "nope", "a.go")
	_, err := resolveWriteTarget(p, nil, false)
	if err == nil || !strings.Contains(err.Error(), "parent") {
		t.Fatalf("want a parent-directory error, got %v", err)
	}
}

func TestWriteTargetRejectsOutsideRoots(t *testing.T) {
	root := t.TempDir()
	other := t.TempDir()
	_, err := resolveWriteTarget(filepath.Join(other, "a.go"), []string{root}, false)
	if err == nil || !strings.Contains(err.Error(), "PLAN_ROOTS") {
		t.Fatalf("want a roots refusal, got %v", err)
	}
}

func TestWriteTargetRejectsControlChars(t *testing.T) {
	dir := t.TempDir()
	_, err := resolveWriteTarget(filepath.Join(dir, "a\nb.go"), nil, false)
	if err == nil || !strings.Contains(err.Error(), "U+") {
		t.Fatalf("want a control-character refusal naming the code point, got %v", err)
	}
}

func TestWriteTargetRefusesExistingWithoutOverwrite(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.go")
	if err := os.WriteFile(p, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := resolveWriteTarget(p, nil, false)
	if err == nil || !strings.Contains(err.Error(), "overwrite") {
		t.Fatalf("want a refusal mentioning overwrite, got %v", err)
	}
	if b, _ := os.ReadFile(p); string(b) != "old" {
		t.Error("the existing file must be untouched")
	}
}

func TestWriteTargetOverwriteTruncates(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.go")
	if err := os.WriteFile(p, []byte("a very long previous body"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := resolveWriteTarget(p, nil, true)
	if err != nil {
		t.Fatalf("resolveWriteTarget: %v", err)
	}
	if _, err := f.WriteString("new"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "new" {
		t.Errorf("content = %q, want %q (O_TRUNC missing?)", string(b), "new")
	}
}

```

**STOP — the next test goes in a DIFFERENT file.** Everything above belongs in
`internal/mcpsrv/file_target_test.go`. The leaf-symlink test below belongs in
`internal/mcpsrv/file_target_unix_test.go`, whose first lines are:

```go
//go:build !windows

package mcpsrv

import (
	"os"
	"path/filepath"
	"testing"
)
```

`!windows` rather than an explicit Unix list because that is exactly the
constraint the existing read path uses (`internal/mcpsrv/file_source_unix.go`);
matching it keeps one convention rather than two. The acceptance criteria accept
the Windows leaf-symlink gap as a documented non-goal, so a test asserting the
guarantee unconditionally would contradict them.

```go
func TestWriteTargetRefusesSymlinkLeaf(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "victim.go")
	if err := os.WriteFile(victim, []byte("precious"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.go")
	if err := os.Symlink(victim, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	for _, ow := range []bool{false, true} {
		if _, err := resolveWriteTarget(link, nil, ow); err == nil {
			t.Fatalf("overwrite=%v: writing through a symlink must be refused", ow)
		}
	}
	if b, _ := os.ReadFile(victim); string(b) != "precious" {
		t.Error("symlink target was modified")
	}
}

func TestWriteTargetAllowsSymlinkedParentInsideRoots(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	f, err := resolveWriteTarget(filepath.Join(link, "a.go"), []string{real}, false)
	if err != nil {
		t.Fatalf("a symlinked parent resolving inside the roots must be allowed: %v", err)
	}
	_ = f.Close()
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/mcpsrv/... -run WriteTarget -v`
Expected: FAIL — `undefined: resolveWriteTarget`

- [ ] **Step 3: Implement the resolver**

Create `internal/mcpsrv/file_target.go`:

```go
// Package mcpsrv: write-path resolution for code_write.
//
// resolveFileInput cannot be reused. It EvalSymlinks the FULL path, which
// fails outright when the file does not exist yet — the normal case for a
// generated file. The parent is resolved instead, and the leaf is opened with
// O_NOFOLLOW so a symlink planted at the leaf cannot redirect the write
// outside the roots that were just checked. See design §5.3.
package mcpsrv

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// resolveWriteTarget validates target and returns an open file handle ready
// to be written. The caller closes it.
//
// Deliberately does NOT create parent directories: creating a path that does
// not exist reopens the containment problem this function exists to close,
// and a worker model acting on a typo would scatter directories.
func resolveWriteTarget(target string, roots []string, overwrite bool) (*os.File, error) {
	if strings.TrimSpace(target) == "" {
		return nil, errors.New("target_path is empty")
	}
	if !filepath.IsAbs(target) {
		return nil, fmt.Errorf("target_path must be absolute, got %q", target)
	}

	parent, leaf := filepath.Dir(target), filepath.Base(target)
	if leaf == "." || leaf == string(filepath.Separator) {
		return nil, fmt.Errorf("target_path %q does not name a file", target)
	}

	parentResolved, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return nil, fmt.Errorf(
			"target_path parent directory %q must already exist (code_write never creates directories): %w", parent, err)
	}

	resolved := filepath.Join(parentResolved, leaf)
	if err := rejectControlChars(resolved); err != nil {
		return nil, err
	}
	if !withinRoots(parentResolved, roots) {
		return nil, fmt.Errorf(
			"%q is outside ANTI_TANGENT_PLAN_ROOTS (%s)", resolved, strings.Join(roots, string(os.PathListSeparator)))
	}

	f, err := openWriteNoFollow(resolved, overwrite)
	if err != nil {
		if !overwrite && errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf(
				"%q already exists; pass overwrite: true to replace it", resolved)
		}
		return nil, fmt.Errorf("open %q for writing: %w", resolved, err)
	}
	return f, nil
}
```

Create `internal/mcpsrv/file_target_unix.go`:

```go
//go:build !windows

package mcpsrv

import (
	"os"
	"syscall"
)

// openWriteNoFollow opens resolved for writing, refusing to follow a symlink
// at the final component.
//
// O_NOFOLLOW is the load-bearing flag: without it a symlink planted at the
// leaf between the containment check and the open redirects the write to an
// arbitrary path outside the roots. It applies regardless of overwrite —
// writing THROUGH a symlink is never what code_write means.
//
// O_EXCL additionally makes "does not already exist" atomic when overwrite is
// false, rather than a stat-then-open race.
func openWriteNoFollow(resolved string, overwrite bool) (*os.File, error) {
	flags := syscall.O_WRONLY | syscall.O_CREAT | syscall.O_NOFOLLOW
	if overwrite {
		flags |= syscall.O_TRUNC
	} else {
		flags |= syscall.O_EXCL
	}
	fd, err := syscall.Open(resolved, flags, 0o644)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: resolved, Err: err}
	}
	return os.NewFile(uintptr(fd), resolved), nil
}
```

Create `internal/mcpsrv/file_target_windows.go`:

```go
//go:build windows

package mcpsrv

import "os"

// openWriteNoFollow on Windows has no O_NOFOLLOW equivalent in Go's syscall
// package, exactly as the read path's openNoFollow does not. The
// final-component symlink-swap window is therefore NOT closed on Windows;
// ANTI_TANGENT_PLAN_ROOTS is advisory against caller mistakes there rather
// than enforced against a race. Documented in the README alongside the
// identical read-path caveat.
func openWriteNoFollow(resolved string, overwrite bool) (*os.File, error) {
	flags := os.O_WRONLY | os.O_CREATE
	if overwrite {
		flags |= os.O_TRUNC
	} else {
		flags |= os.O_EXCL
	}
	return os.OpenFile(resolved, flags, 0o644)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -race ./internal/mcpsrv/... -run WriteTarget -v`
Expected: PASS (8 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/mcpsrv/file_target.go internal/mcpsrv/file_target_unix.go internal/mcpsrv/file_target_windows.go internal/mcpsrv/file_target_test.go internal/mcpsrv/file_target_unix_test.go
git commit -m "feat(mcpsrv): write-target resolver with roots and O_NOFOLLOW"
```

```json:metadata
{"files": ["internal/mcpsrv/file_target.go", "internal/mcpsrv/file_target_unix.go", "internal/mcpsrv/file_target_windows.go", "internal/mcpsrv/file_target_test.go", "internal/mcpsrv/file_target_unix_test.go"], "verifyCommand": "go test -race ./internal/mcpsrv/... -run WriteTarget -v", "acceptanceCriteria": ["relative and empty paths refused", "missing parent refused without MkdirAll", "parent outside PlanRoots refused", "control/format characters refused", "existing file refused unless overwrite", "overwrite truncates", "on Unix a symlink at the leaf is refused under both overwrite values", "the Windows leaf-symlink gap is an accepted, documented non-goal", "symlinked parent inside roots allowed"], "modelTier": "standard"}
```

---

### Task 6: `code_write` tool

**Goal:** An MCP tool that generates code matching a required reference file and, given a target, writes it and returns only a line count.

**Files:**
- Modify: `internal/mcpsrv/worker_handlers.go`
- Modify: `internal/mcpsrv/server.go`
- Test: `internal/mcpsrv/worker_handlers_test.go`

**Acceptance Criteria:**
- [ ] A call with no `reference_path` is rejected with a message explaining why context-free code is useless
- [ ] With `target_path`, the response carries `written` + `lines_written` and **no** `code`
- [ ] Without `target_path`, the response carries `code` and no `written`
- [ ] Markdown fences are stripped when the whole body is fence-wrapped, and NOT stripped mid-body
- [ ] A truncated worker response writes nothing
- [ ] `lines_written` counts a final line with no trailing newline
- [ ] **Empty generated code (after fence-stripping) is an error and nothing is written.** This is what makes the `omitempty` tags safe: on every success `code` is non-empty and `lines_written` >= 1, so neither field the acceptance criteria promise can be dropped by the encoder
- [ ] A `Close` error on the target is reported, not discarded — a write is not done until the file closes cleanly, proven by a test that injects a failing `Close` through the `openWriteTarget` seam
- [ ] Empty, whitespace-only and fence-only worker output are each tested; for a targeted call the test asserts the target file was never created
- [ ] `code_write` appears in the tool catalog, asserted here (Task 4 asserts only `bulk_read`, since this task is what registers `code_write`)
- [ ] The tool description states the generated-code contract in the same terms as Tasks 11 and 14: code written to a `target_path` is **verified, not inspected** — the implementer chooses the reference and proves the result with the task's tests and build, and is not required to read it. See Global Constraints

**Verify:** `go test -race ./internal/mcpsrv/... -run 'CodeWrite|StripFences|CountLines' -v` → PASS

**Steps:**

- [ ] **Step 1: Write the failing tests**

Append to `internal/mcpsrv/worker_handlers_test.go`:

```go
func TestCodeWriteRequiresReferencePath(t *testing.T) {
	h := &handlers{deps: workerDeps(t, &fakeWorkerReviewer{}, nil)}
	_, _, err := h.CodeWrite(context.Background(), nil, CodeWriteArgs{Spec: "make a test"})
	if err == nil || !strings.Contains(err.Error(), "reference_path") {
		t.Fatalf("want a reference_path error, got %v", err)
	}
}

func TestCodeWriteWritesAndHidesCode(t *testing.T) {
	dir := t.TempDir()
	ref := filepath.Join(dir, "ref.go")
	if err := os.WriteFile(ref, []byte("package ref\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "out.go")
	f := &fakeWorkerReviewer{resp: providers.Response{
		RawJSON: []byte(`{"code":"package out\n\nfunc A() {}\n"}`), InputTokens: 5, OutputTokens: 3,
	}}
	h := &handlers{deps: workerDeps(t, f, []string{dir})}
	_, res, err := h.CodeWrite(context.Background(), nil, CodeWriteArgs{
		Spec: "a func", ReferencePath: ref, TargetPath: target,
	})
	if err != nil {
		t.Fatalf("CodeWrite: %v", err)
	}
	if res.Code != "" {
		t.Error("a targeted write must NOT return the code")
	}
	if res.Written != target || res.LinesWritten != 3 {
		t.Errorf("Written=%q LinesWritten=%d, want %q and 3", res.Written, res.LinesWritten, target)
	}
	b, _ := os.ReadFile(target)
	if string(b) != "package out\n\nfunc A() {}\n" {
		t.Errorf("on-disk content = %q", string(b))
	}
}

func TestCodeWriteWithoutTargetReturnsCode(t *testing.T) {
	dir := t.TempDir()
	ref := filepath.Join(dir, "ref.go")
	if err := os.WriteFile(ref, []byte("package ref\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := &fakeWorkerReviewer{resp: providers.Response{RawJSON: []byte(`{"code":"func A() {}"}`)}}
	h := &handlers{deps: workerDeps(t, f, []string{dir})}
	_, res, err := h.CodeWrite(context.Background(), nil, CodeWriteArgs{Spec: "s", ReferencePath: ref})
	if err != nil {
		t.Fatalf("CodeWrite: %v", err)
	}
	if res.Code != "func A() {}" || res.Written != "" {
		t.Errorf("Code=%q Written=%q", res.Code, res.Written)
	}
}

func TestCodeWriteTruncationWritesNothing(t *testing.T) {
	dir := t.TempDir()
	ref := filepath.Join(dir, "ref.go")
	if err := os.WriteFile(ref, []byte("package ref\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "out.go")
	f := &fakeWorkerReviewer{err: providers.ErrResponseTruncated}
	h := &handlers{deps: workerDeps(t, f, []string{dir})}
	if _, _, err := h.CodeWrite(context.Background(), nil, CodeWriteArgs{
		Spec: "s", ReferencePath: ref, TargetPath: target,
	}); err == nil {
		t.Fatal("want an error on truncation")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("a truncated response must not create the target file")
	}
}

func TestCodeWriteRegisteredInCatalog(t *testing.T) {
	// The code_write half of the catalog assertion lives here, not in Task 4:
	// this is the task that registers it.
	if !catalogHas(t, "code_write") {
		t.Error("tool \"code_write\" is not registered")
	}
}

func TestCodeWriteEmptyOutputIsAnErrorAndWritesNothing(t *testing.T) {
	dir := t.TempDir()
	ref := filepath.Join(dir, "ref.go")
	if err := os.WriteFile(ref, []byte("package ref\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Built with json.Marshal, NOT hand-written escapes: a hand-rolled
	// "```go\\n```" decodes to a literal backslash-n, so stripFences sees no
	// newline, leaves the text non-empty, and the case silently stops testing
	// what it claims to.
	mustBody := func(code string) []byte {
		b, err := json.Marshal(map[string]string{"code": code})
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	cases := map[string][]byte{
		"empty":      mustBody(""),
		"whitespace": mustBody("   \n\t"),
		"fence only": mustBody("```go\n```"),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			target := filepath.Join(dir, "out_"+strings.ReplaceAll(name, " ", "_")+".go")
			f := &fakeWorkerReviewer{resp: providers.Response{RawJSON: []byte(body)}}
			h := &handlers{deps: workerDeps(t, f, []string{dir})}
			if _, _, err := h.CodeWrite(context.Background(), nil, CodeWriteArgs{
				Spec: "s", ReferencePath: ref, TargetPath: target,
			}); err == nil {
				t.Fatal("empty generated code must be an error")
			}
			if _, err := os.Stat(target); !os.IsNotExist(err) {
				t.Error("nothing must be written when the worker returns no code")
			}
		})
	}
}

// closeFailTarget wraps a real file and reports a Close failure, so the
// "Close errors are reported" criterion has something to assert against.
type closeFailTarget struct {
	*os.File
	err error
}

func (c closeFailTarget) Close() error { _ = c.File.Close(); return c.err }

func TestCodeWriteReportsCloseError(t *testing.T) {
	dir := t.TempDir()
	ref := filepath.Join(dir, "ref.go")
	if err := os.WriteFile(ref, []byte("package ref\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	orig := openWriteTarget
	t.Cleanup(func() { openWriteTarget = orig })
	openWriteTarget = func(target string, roots []string, overwrite bool) (writeTarget, error) {
		f, err := resolveWriteTarget(target, roots, overwrite)
		if err != nil {
			return nil, err
		}
		return closeFailTarget{File: f, err: errors.New("disk went away")}, nil
	}

	f := &fakeWorkerReviewer{resp: providers.Response{RawJSON: []byte(`{"code":"package out\n"}`)}}
	h := &handlers{deps: workerDeps(t, f, []string{dir})}
	_, _, err := h.CodeWrite(context.Background(), nil, CodeWriteArgs{
		Spec: "s", ReferencePath: ref, TargetPath: filepath.Join(dir, "out.go"),
	})
	if err == nil || !strings.Contains(err.Error(), "disk went away") {
		t.Fatalf("a Close error must surface, got %v", err)
	}
}

func TestStripFences(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"wrapped with lang", "```go\nfunc A() {}\n```", "func A() {}\n"},
		{"wrapped bare", "```\nfunc A() {}\n```", "func A() {}\n"},
		{"trailing newline after fence", "```go\nfunc A() {}\n```\n", "func A() {}\n"},
		{"not wrapped", "func A() {}\n", "func A() {}\n"},
		{"fence mid-body is preserved", "func A() {\n// ```\n}\n", "func A() {\n// ```\n}\n"},
		{"opening fence only", "```go\nfunc A() {}\n", "```go\nfunc A() {}\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := stripFences(c.in); got != c.want {
				t.Errorf("stripFences(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestCountLines(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{{"", 0}, {"a\n", 1}, {"a\nb\n", 2}, {"a\nb", 2}, {"\n", 1}}
	for _, c := range cases {
		if got := countLines(c.in); got != c.want {
			t.Errorf("countLines(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/mcpsrv/... -run 'CodeWrite|StripFences|CountLines' -v`
Expected: FAIL — `undefined: CodeWriteArgs`

- [ ] **Step 3: Implement**

Append to `internal/mcpsrv/worker_handlers.go`:

```go
type CodeWriteArgs struct {
	Spec              string `json:"spec"           jsonschema:"required"`
	ReferencePath     string `json:"reference_path" jsonschema:"required"`
	TargetPath        string `json:"target_path,omitempty"`
	Overwrite         bool   `json:"overwrite,omitempty"`
	Model             string `json:"model,omitempty"`
	MaxTokensOverride int    `json:"max_tokens_override,omitempty"`
}

type CodeWriteResult struct {
	Written      string `json:"written,omitempty"`
	LinesWritten int    `json:"lines_written,omitempty"`
	Code         string `json:"code,omitempty"`
	ModelUsed    string `json:"model_used"`
	ReviewMS     int64  `json:"review_ms"`
	InputTokens  int    `json:"input_tokens,omitempty"`
	OutputTokens int    `json:"output_tokens,omitempty"`
}

func codeWriteTool() *mcp.Tool {
	return &mcp.Tool{
		Name: "code_write",
		Description: "Generate pattern-following boilerplate with a cheap worker model. " +
			"reference_path is REQUIRED and absolute — the generated code matches that file's conventions. " +
			"Set target_path (absolute) to have the server write the file and return only a line count, " +
			"so the generated code never enters your context. " +
			"Generated code is VERIFIED, NOT INSPECTED: you choose the reference file and prove the result " +
			"by running the task's tests and build. You are not required to read what was generated — " +
			"reading it would put back exactly the context this tool removes. " +
			"An existing target is refused unless overwrite is true, and the parent directory must already exist. " +
			"Do NOT use this for logic requiring judgement — only for boilerplate that follows an existing pattern.",
	}
}

func (h *handlers) CodeWrite(ctx context.Context, _ *mcp.CallToolRequest, args CodeWriteArgs) (*mcp.CallToolResult, CodeWriteResult, error) {
	if strings.TrimSpace(args.Spec) == "" {
		return nil, CodeWriteResult{}, errors.New("spec is required and must not be empty")
	}
	if strings.TrimSpace(args.ReferencePath) == "" {
		return nil, CodeWriteResult{}, errors.New(
			"reference_path is required: without a file whose patterns to match, the worker generates " +
				"context-free code that fits nothing in the project")
	}

	maxTokens, _, err := effectiveMaxTokens(args.MaxTokensOverride, h.deps.Cfg.WorkerMaxTokens, h.deps.Cfg.MaxTokensCeiling)
	if err != nil {
		return nil, CodeWriteResult{}, err
	}
	model, err := h.resolveModel(args.Model, h.deps.Cfg.WorkerModel)
	if err != nil {
		return nil, CodeWriteResult{}, err
	}

	refContent, refSrc, err := resolveFileInput(args.ReferencePath, h.deps.Cfg.PlanRoots, h.deps.Cfg.MaxPayloadBytes)
	if err != nil {
		return nil, CodeWriteResult{}, fmt.Errorf("reference_path: %w", err)
	}

	rendered, err := prompts.RenderWorkerCodeWrite(prompts.WorkerCodeWriteInput{
		Spec: args.Spec, ReferencePath: refSrc.Path, ReferenceContent: refContent,
	})
	if err != nil {
		return nil, CodeWriteResult{}, fmt.Errorf("render code_write prompt: %w", err)
	}

	// The worker call happens BEFORE the target is opened, so a truncated or
	// failed generation cannot leave a zero-length file behind.
	wr, err := h.runWorker(ctx, model, rendered, maxTokens, "code")
	if errors.Is(err, providers.ErrResponseTruncated) {
		return nil, CodeWriteResult{}, fmt.Errorf(
			"worker response was cut off at the token limit and nothing was written; "+
				"raise ANTI_TANGENT_WORKER_MAX_TOKENS or narrow the spec: %w", err)
	}
	if err != nil {
		return nil, CodeWriteResult{}, err
	}

	code := stripFences(wr.Text)
	// Empty output is an error, never a zero-line write. This also keeps the
	// omitempty tags on Code / LinesWritten honest: on success both are always
	// non-zero, so neither field the tool promises can vanish from the JSON.
	if strings.TrimSpace(code) == "" {
		return nil, CodeWriteResult{}, errors.New("worker returned no code; nothing was written")
	}
	res := CodeWriteResult{
		ModelUsed:    wr.Model,
		ReviewMS:     wr.ReviewMS,
		InputTokens:  wr.InputTokens,
		OutputTokens: wr.OutputTokens,
	}
	if strings.TrimSpace(args.TargetPath) == "" {
		res.Code = code
		return nil, res, nil
	}

	f, err := openWriteTarget(args.TargetPath, h.deps.Cfg.PlanRoots, args.Overwrite)
	if err != nil {
		return nil, CodeWriteResult{}, err
	}
	written := f.Name()
	if _, err := f.WriteString(code); err != nil {
		_ = f.Close()
		return nil, CodeWriteResult{}, fmt.Errorf("write %q: %w", written, err)
	}
	// Closed explicitly, not deferred: a deferred Close discards its error, and
	// on a write path that error is where a failed flush surfaces. Reporting
	// success for a file that did not close cleanly would be a lie.
	if err := f.Close(); err != nil {
		return nil, CodeWriteResult{}, fmt.Errorf("close %q: %w", written, err)
	}
	res.Written = written
	res.LinesWritten = countLines(code)
	return nil, res, nil
}

// writeTarget is the narrow surface CodeWrite needs from an open file.
// *os.File satisfies it. It exists solely so a test can force Close to fail —
// there is no way to make a real *os.File's Close return an error on demand,
// and "a Close error is reported, not discarded" is an acceptance criterion,
// so it needs a seam.
type writeTarget interface {
	io.StringWriter
	io.Closer
	Name() string
}

// openWriteTarget is a package var for the same reason. Production always
// resolves through Task 5's resolveWriteTarget; only tests replace it.
var openWriteTarget = func(target string, roots []string, overwrite bool) (writeTarget, error) {
	return resolveWriteTarget(target, roots, overwrite)
}

// stripFences removes a markdown code fence that wraps the ENTIRE body. A
// fence appearing mid-body is left alone — it is far more likely to be a
// comment or a doc string in the generated code than a formatting artifact.
func stripFences(s string) string {
	t := strings.TrimSpace(s)
	if !strings.HasPrefix(t, "```") {
		return s
	}
	nl := strings.IndexByte(t, '\n')
	if nl < 0 || !strings.HasSuffix(t, "```") {
		return s
	}
	body := t[nl+1 : len(t)-len("```")]
	return strings.TrimSuffix(body, "\n") + "\n"
}

// countLines counts lines including a final line with no trailing newline.
func countLines(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n")
	if !strings.HasSuffix(s, "\n") {
		n++
	}
	return n
}
```

- [ ] **Step 4: Register the tool**

In `internal/mcpsrv/server.go`, after the `bulkReadTool` registration:

```go
	mcp.AddTool(srv, codeWriteTool(), h.CodeWrite)
```

Update `New`'s doc comment to say nine registered tools.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test -race ./internal/mcpsrv/... && go build ./...`
Expected: PASS, build clean

- [ ] **Step 6: Commit**

```bash
git add internal/mcpsrv/worker_handlers.go internal/mcpsrv/worker_handlers_test.go internal/mcpsrv/server.go
git commit -m "feat(mcpsrv): add code_write tool with overwrite guard"
```

```json:metadata
{"files": ["internal/mcpsrv/worker_handlers.go", "internal/mcpsrv/worker_handlers_test.go", "internal/mcpsrv/server.go"], "verifyCommand": "go test -race ./internal/mcpsrv/... -run 'CodeWrite|StripFences|CountLines' -v", "acceptanceCriteria": ["missing reference_path rejected with an explanation", "target_path write returns written+lines_written and no code", "no target returns code and no written", "fences stripped only when wrapping the whole body", "truncation writes nothing", "countLines handles a missing trailing newline", "empty/whitespace/fence-only output errors and writes nothing", "a Close error is reported via the openWriteTarget seam", "code_write catalog registration asserted here", "tool description states the verified-not-inspected generated-code contract"], "modelTier": "standard"}
```

---

### Task 7: Worker telemetry in stats

**Goal:** Record worker calls with token counts, and aggregate them additively into `rollup.json`.

**Files:**
- Modify: `internal/stats/event.go`
- Modify: `internal/stats/rollup.go`
- Modify: `internal/mcpsrv/worker_handlers.go`
- Test: `internal/stats/rollup_test.go`, `internal/stats/event_test.go`

**Acceptance Criteria:**
- [ ] `Event` gains `input_tokens` / `output_tokens`, both omitted when zero
- [ ] `Rollup` gains a `worker` key, absent when the window holds no worker events
- [ ] `worker` reports per-tool call counts and summed input/output tokens
- [ ] **Every pre-existing `rollup.json` key is unchanged** — asserted explicitly
- [ ] Both tools record an event when `Stats` is non-nil, and are no-ops when nil — **each proven by a handler test**, not inferred from `computeRollup`
- [ ] The event is recorded when the **worker call** succeeds, not when the tool call does: a `code_write` whose worker answered but whose write then failed still records its token spend. Proven by a test that forces a write failure and asserts the event exists
- [ ] `internal/stats/event_test.go` asserts the JSON omission contract directly: both token fields absent at zero, present under their exact names when non-zero

**Verify:** `go test -race ./internal/stats/... ./internal/mcpsrv/... -v` → PASS (the handler-level telemetry and nil-recorder tests live in `internal/mcpsrv`, so a stats-only command would not run them)

**Steps:**

- [ ] **Step 1: Write the failing tests**

Put `TestEventTokenFieldsOmittedWhenZero` in **`internal/stats/event_test.go`** (the acceptance criteria and Files block both name that file — do not append it to `rollup_test.go`). The rollup tests below go in `internal/stats/rollup_test.go`:

```go
func TestRollupWorkerAggregates(t *testing.T) {
	now := time.Now()
	events := []Event{
		{Ts: now, Tool: "bulk_read", InputTokens: 100, OutputTokens: 10},
		{Ts: now, Tool: "bulk_read", InputTokens: 200, OutputTokens: 20},
		{Ts: now, Tool: "code_write", InputTokens: 50, OutputTokens: 300},
		{Ts: now, Tool: "validate_completion", Verdict: "pass"},
	}
	r := computeRollup(events, now)
	if r.Worker == nil {
		t.Fatal("Worker rollup must be present when worker events exist")
	}
	if r.Worker.InputTokens != 350 || r.Worker.OutputTokens != 330 {
		t.Errorf("tokens = %d/%d, want 350/330", r.Worker.InputTokens, r.Worker.OutputTokens)
	}
	if r.Worker.PerTool["bulk_read"] != 2 || r.Worker.PerTool["code_write"] != 1 {
		t.Errorf("PerTool = %v", r.Worker.PerTool)
	}
}

func TestRollupWorkerAbsentWithoutWorkerEvents(t *testing.T) {
	now := time.Now()
	r := computeRollup([]Event{{Ts: now, Tool: "check_progress"}}, now)
	if r.Worker != nil {
		t.Error("Worker must be nil when the window holds no worker events — absence means no data")
	}
}

func TestEventTokenFieldsOmittedWhenZero(t *testing.T) {
	b, err := json.Marshal(Event{Ts: time.Now(), Tool: "check_progress"})
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"input_tokens", "output_tokens"} {
		if strings.Contains(string(b), k) {
			t.Errorf("%s must be omitted when zero, got %s", k, b)
		}
	}
	b, err = json.Marshal(Event{Ts: time.Now(), Tool: "bulk_read", InputTokens: 5, OutputTokens: 7})
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{`"input_tokens":5`, `"output_tokens":7`} {
		if !strings.Contains(string(b), k) {
			t.Errorf("expected %s in %s", k, b)
		}
	}
}

func TestRollupExistingKeysUnchanged(t *testing.T) {
	now := time.Now()
	b, err := json.Marshal(computeRollup([]Event{{Ts: now, Tool: "bulk_read", InputTokens: 1}}, now))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	// The gnome-topbar consumer reads these by exact name. Adding a key is
	// safe; renaming or dropping one is a breaking change.
	for _, k := range []string{
		"window_start", "window_end", "total_calls", "per_tool", "verdict_counts",
		"findings_per_call", "severity_histogram", "category_histogram",
		"review_ms_p50", "review_ms_p95", "cache_hit_rate", "partial_rate",
		"model_usage", "generated_at",
	} {
		if _, ok := m[k]; !ok {
			t.Errorf("rollup.json lost the load-bearing key %q", k)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/stats/... -run Worker -v`
Expected: FAIL — `r.Worker undefined`

- [ ] **Step 3: Add the Event fields**

In `internal/stats/event.go`, inside `Event`:

```go
	// InputTokens / OutputTokens are set only by the I/O-delegation tools
	// (bulk_read, code_write). They are what makes "tokens kept out of the
	// implementer's context" computable. omitempty so no existing event shape
	// changes.
	InputTokens  int `json:"input_tokens,omitempty"`
	OutputTokens int `json:"output_tokens,omitempty"`
```

- [ ] **Step 4: Add the Worker rollup**

In `internal/stats/rollup.go`, add the field to `Rollup` (append — never reorder existing tags):

```go
	Worker            *WorkerRollup      `json:"worker,omitempty"`
```

and the type plus aggregation:

```go
// WorkerRollup reports I/O-delegation volume across the window. Nil (and so
// absent from rollup.json) when the window contains no worker events —
// absence means "no data", not "zero delegation", matching PlanHeadersRollup.
type WorkerRollup struct {
	Calls        int            `json:"calls"`
	PerTool      map[string]int `json:"per_tool"`
	InputTokens  int            `json:"input_tokens"`
	OutputTokens int            `json:"output_tokens"`
}

// workerTools is the set whose events feed WorkerRollup.
var workerTools = map[string]bool{"bulk_read": true, "code_write": true}
```

Inside `computeRollup`, after the existing per-event loop:

```go
	var w WorkerRollup
	w.PerTool = map[string]int{}
	for _, ev := range events {
		if !workerTools[ev.Tool] {
			continue
		}
		w.Calls++
		w.PerTool[ev.Tool]++
		w.InputTokens += ev.InputTokens
		w.OutputTokens += ev.OutputTokens
	}
	if w.Calls > 0 {
		r.Worker = &w
	}
```

- [ ] **Step 5: Record events from both handlers**

In `internal/mcpsrv/worker_handlers.go`, immediately before each successful `return` in `BulkRead` and `CodeWrite`:

```go
	h.deps.Stats.Record(stats.Event{
		Ts: time.Now(), Tool: "bulk_read", ReviewMS: wr.ReviewMS, Model: wr.Model,
		InputTokens: wr.InputTokens, OutputTokens: wr.OutputTokens, PayloadBytes: bytesRead,
	})
```

(and the `code_write` equivalent, with `PayloadBytes: refSrc.Bytes`).

**Record immediately after `runWorker` returns successfully — BEFORE fence
stripping, the empty-output check, and any filesystem work.** The goal is
"record worker calls", and those tokens are spent the moment the provider
answers. Recording at the tool's successful return instead would lose every
event where the worker succeeded but the write failed — precisely the calls
someone auditing spend most wants to see. Exactly one event per worker
invocation; a call that never reached the provider records nothing. `Record` is nil-safe by construction (`internal/stats/recorder.go` opens with `if r == nil { return }`), so no `h.deps.Stats != nil` guard is needed — verified, do not add one.

- [ ] **Step 5b: Prove the recording paths in the handlers**

Add to `internal/mcpsrv/worker_handlers_test.go` a test per tool that points `Deps.Stats` at a recorder backed by `t.TempDir()`, runs the successful path, and asserts exactly one row was appended to `events.jsonl` carrying the right `tool` and token counts. Then repeat each with `Stats: nil` and assert the call still succeeds and does not panic. The rollup test alone does not exercise either handler, so without this the recording code is untested.

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test -race ./internal/stats/... ./internal/mcpsrv/... && go build ./...`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/stats/ internal/mcpsrv/worker_handlers.go internal/mcpsrv/worker_handlers_test.go
git commit -m "feat(stats): worker token telemetry, additive to rollup.json"
```

```json:metadata
{"files": ["internal/stats/event.go", "internal/stats/event_test.go", "internal/stats/rollup.go", "internal/stats/rollup_test.go", "internal/mcpsrv/worker_handlers.go", "internal/mcpsrv/worker_handlers_test.go"], "verifyCommand": "go test -race ./internal/stats/... ./internal/mcpsrv/... -v", "acceptanceCriteria": ["Event gains omitempty token fields", "Rollup gains a worker key absent without worker events", "worker reports per-tool counts and summed tokens", "all pre-existing rollup.json keys unchanged", "handlers record events and are nil-safe"], "modelTier": "mechanical"}
```

---

### Task 8: Pin the summary_block contract

**Goal:** A test that fails the build if the substrings the guard hook greps for ever change.

**Files:**
- Create: `internal/mcpsrv/summary_contract_test.go`

**Acceptance Criteria:**
- [ ] Asserts `formatEnvelopeSummary` emits the literal `anti-tangent envelope`
- [ ] Asserts it emits `session_id:` and `verdict:`
- [ ] The test comment names the consuming hook file, so the coupling is discoverable from either end

**Verify:** `go test -race ./internal/mcpsrv/... -run SummaryBlockContract -v` → PASS

**Steps:**

- [ ] **Step 1: Write the test**

Create `internal/mcpsrv/summary_contract_test.go`:

```go
package mcpsrv

import (
	"regexp"
	"strings"
	"testing"

	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

// TestSummaryBlockContract pins the exact substrings that
// plugin/anti-tangent-guard/hooks/check-task-complete greps for in a
// transcript.
//
// The guard's second pass signal is the summary_block pasted into a
// subagent's DONE report — that is how a controller-side hook can tell
// validate_completion ran inside a subagent transcript it cannot see. It also
// parses the verdict from the same block to block a close that read `fail`.
//
// Both couplings are textual. Without this test a future change to
// formatEnvelopeSummary's wording would silently disarm the guard: the hook
// would find no marker, conclude the gate never ran, and start blocking every
// close — or, worse, stop recognising a fail verdict and let it through. A
// failing test here means: update the hook's grep patterns in the same commit.
func TestSummaryBlockContract(t *testing.T) {
	got := formatEnvelopeSummary(Envelope{
		SessionID:  "sess-123",
		Verdict:    string(verdict.VerdictFail),
		NextAction: "fix",
		ModelUsed:  "anthropic:claude-opus-4-7",
	})
	for _, want := range []string{"anti-tangent envelope", "session_id: sess-123"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary_block no longer contains %q — the guard hook greps for it.\n"+
				"Update plugin/anti-tangent-guard/hooks/check-task-complete in this commit.\ngot:\n%s", want, got)
		}
	}

	// Mirror the guard hook's ACTUAL parser, not an approximation of it.
	// check-task-complete extracts the verdict with ^\s*verdict:\s*(\w+) in
	// MULTILINE mode. Asserting "verdict:" and "fail" independently would pass
	// even if the value moved to another line — precisely the drift that would
	// stop the guard recognising a failed close while this test stayed green.
	re := regexp.MustCompile(`(?m)^\s*verdict:\s*(\w+)`)
	m := re.FindAllStringSubmatch(got, -1)
	if len(m) == 0 {
		t.Fatalf("no line matches the guard's verdict pattern.\ngot:\n%s", got)
	}
	if last := m[len(m)-1][1]; last != "fail" {
		t.Errorf("guard would parse verdict %q, want \"fail\"", last)
	}
}
```

- [ ] **Step 2: Run the test**

Run: `go test -race ./internal/mcpsrv/... -run SummaryBlockContract -v`
Expected: PASS immediately — this pins existing behaviour rather than driving new code. If it fails, the format already drifted and the hook patterns in Task 12 must match reality, not this test.

- [ ] **Step 3: Commit**

```bash
git add internal/mcpsrv/summary_contract_test.go
git commit -m "test(mcpsrv): pin summary_block substrings the guard hook parses"
```

```json:metadata
{"files": ["internal/mcpsrv/summary_contract_test.go"], "verifyCommand": "go test -race ./internal/mcpsrv/... -run SummaryBlockContract -v", "acceptanceCriteria": ["asserts the anti-tangent envelope marker", "asserts session_id: and verdict:", "comment names the consuming hook file"], "modelTier": "mechanical"}
```

---

### Task 9: Third-party attribution

**Goal:** `THIRD_PARTY_NOTICES.md` exists with the Apache-2.0 text, and a Go test fails the build if it is removed or gutted.

**Files:**
- Create: `THIRD_PARTY_NOTICES.md`
- Create: `internal/notices/doc.go`
- Create: `internal/notices/notices_test.go`

**Acceptance Criteria:**
- [ ] `THIRD_PARTY_NOTICES.md` states the repo is MIT except as noted
- [ ] It names **shunt**, the upstream URL, Copyright Spotify AB, and Apache-2.0
- [ ] It lists the derived files by path
- [ ] It contains the full Apache-2.0 licence text, and the test proves it by comparing against a checked-in canonical copy at `internal/notices/testdata/apache-2.0.txt` — substring spot-checks would pass a licence gutted down to its headings
- [ ] A Go test asserts the file exists and contains both `Spotify AB` and `Apache`
- [ ] `go vet ./...` stays clean (hence `doc.go`, so the package is not test-only)

**Verify:** `go test -race ./internal/notices/... -v` → PASS

**Steps:**

- [ ] **Step 1: Check whether upstream ships a NOTICE file**

```bash
code=$(curl -sS -o /tmp/upstream-NOTICE -w '%{http_code}' \
  https://raw.githubusercontent.com/spotify/portal-ai-plugins/3c24ca30ff63e1f5bbad1c43fe5324daff579123/NOTICE)
echo "NOTICE probe: $code"
```

- `200` → reproduce `/tmp/upstream-NOTICE` verbatim in a `## NOTICE` section. Apache-2.0 §4(d) requires it.
- `404` → there is no NOTICE file; skip that section.
- **Anything else** (redirect, 403, 5xx, timeout, empty `$code`) → **stop and resolve it**. Do not treat a transient failure as absence: silently skipping a NOTICE that does exist is a licence violation, and this is the one probe where "no answer" and "no file" must not be conflated. **Note:** `engineering.atspotify.com` is NOT reachable from this environment (403 CONNECT tunnel); do not block on it, and do not claim to have read the blog post.

- [ ] **Step 2: Write the notices file**

```bash
cat > THIRD_PARTY_NOTICES.md <<'EOF'
# Third-party notices

This repository is licensed under the MIT License (see `LICENSE`) except where
noted below.

## shunt

- Source: https://github.com/spotify/portal-ai-plugins
- Copyright Spotify AB
- Licensed under the Apache License, Version 2.0

Files in this repository derived from that work:

- `plugin/anti-tangent-shunt/hooks/check-file-size`
- `plugin/anti-tangent-shunt/hooks/check-bash-read`
- `plugin/anti-tangent-shunt/evals/run.sh`
- `plugin/anti-tangent-shunt/evals/hook-evals.json`
- `plugin/anti-tangent-shunt/evals/bash-hook-evals.json`

Task 10 adapts `evals/run.sh` from upstream as well as the two hooks, and may
fetch fixture files. **Task 10 reconciles this list against what it actually
copied** — if it fetches fixtures, they are added here in the same commit. An
attribution list that omits a copied file is the failure this exists to prevent.

The upstream commit ported from is `3c24ca30ff63e1f5bbad1c43fe5324daff579123`.

Attribution differs by file type, and the notice must say so rather than make a
blanket claim the port cannot keep:

- **`check-file-size`, `check-bash-read`, `evals/run.sh`** — shell scripts, so
  each carries upstream's original licence header plus a modification notice
  inline.
- **`evals/hook-evals.json`, `evals/bash-hook-evals.json`, and any fetched
  fixtures** — JSON and fixture data cannot carry comments, so they are covered
  by this repository-level notice alone.

"Each derived file carries its original header" would be false for the JSON
suites, and a licence notice inaccurate about its own scope is worse than a
terse one. The modification in every case is the same: the
Portal / AiKA delegation target was replaced by this project's own MCP tools,
and `SHUNT_MIN_LINES` was renamed to `ANTI_TANGENT_SHUNT_MIN_LINES`.

The full text of the Apache License, Version 2.0 follows.

EOF
curl -sS https://www.apache.org/licenses/LICENSE-2.0.txt >> THIRD_PARTY_NOTICES.md
```

If that URL is unreachable, paste the Apache-2.0 text from any local copy — the point is the full text, not the fetch.

- [ ] **Step 3: Write the package and its test**

`internal/notices/doc.go`:

```go
// Package notices exists so the repository's third-party attribution can be
// asserted by an ordinary `go test ./...` run rather than only in CI.
// Attribution is a licence obligation, so its accidental removal should break
// a developer's build, not merely a pipeline. The package has no runtime API;
// doc.go keeps it from being a test-only directory that `go vet` complains
// about.
package notices
```

`internal/notices/notices_test.go`:

```go
package notices

import (
	"os"
	"strings"
	"testing"
)

// noticesPath is relative because tests run with the package directory as cwd.
const noticesPath = "../../THIRD_PARTY_NOTICES.md"

func TestThirdPartyNoticesPresent(t *testing.T) {
	b, err := os.ReadFile(noticesPath)
	if err != nil {
		t.Fatalf("THIRD_PARTY_NOTICES.md is required: the shunt-derived hooks under "+
			"plugin/anti-tangent-shunt/ are Apache-2.0 and must carry attribution: %v", err)
	}
	body := string(b)
	// Substantive check, not a keyword check: a notice gutted down to the word
	// "Apache" would satisfy a naive test while failing the licence obligation.
	for _, want := range []string{
		"Spotify AB",
		"https://github.com/spotify/portal-ai-plugins",
		"MIT License",
		// Stable markers from three separate parts of the Apache-2.0 text.
		"Apache License",
		"TERMS AND CONDITIONS FOR USE, REPRODUCTION, AND DISTRIBUTION",
		"Version 2.0, January 2004",
		"WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("THIRD_PARTY_NOTICES.md must contain %q", want)
		}
	}
	for _, path := range []string{
		"plugin/anti-tangent-shunt/hooks/check-file-size",
		"plugin/anti-tangent-shunt/hooks/check-bash-read",
		"plugin/anti-tangent-shunt/evals/run.sh",
		"plugin/anti-tangent-shunt/evals/hook-evals.json",
		"plugin/anti-tangent-shunt/evals/bash-hook-evals.json",
	} {
		if !strings.Contains(body, path) {
			t.Errorf("THIRD_PARTY_NOTICES.md must list the derived file %s", path)
		}
	}
}
```

- [ ] **Step 4: Verify**

Run: `go test -race ./internal/notices/... -v && go vet ./...`
Expected: PASS, vet clean

- [ ] **Step 5: Commit**

```bash
git add THIRD_PARTY_NOTICES.md internal/notices/
git commit -m "docs: Apache-2.0 attribution for the ported shunt hooks"
```

```json:metadata
{"files": ["THIRD_PARTY_NOTICES.md", "internal/notices/doc.go", "internal/notices/notices_test.go"], "verifyCommand": "go test -race ./internal/notices/... -v", "acceptanceCriteria": ["notices file states MIT-except-as-noted", "names shunt, Spotify AB, Apache-2.0 and the upstream URL", "lists derived files by path", "contains full Apache-2.0 text", "Go test asserts presence and content", "go vet clean"], "modelTier": "mechanical"}
```

---

### Task 10: Port the two shunt hooks

**Goal:** `check-file-size` and `check-bash-read`, adapted from upstream, blocking with `exit 2` and pointing at `bulk_read`.

**Files:**
- Create: `plugin/anti-tangent-shunt/hooks/hooks.json`
- Create: `plugin/anti-tangent-shunt/hooks/check-file-size`
- Create: `plugin/anti-tangent-shunt/hooks/check-bash-read`
- Create: `plugin/anti-tangent-shunt/evals/run.sh`
- Create: `plugin/anti-tangent-shunt/evals/hook-evals.json`
- Create: `plugin/anti-tangent-shunt/evals/bash-hook-evals.json`
- Modify: `THIRD_PARTY_NOTICES.md` (reconcile the derived-file list with what was actually copied)
- Create (CONDITIONAL): `plugin/anti-tangent-shunt/evals/fixtures/` — created ONLY if the pinned suites reference fixture paths (Step 5 enumerates them). If they synthesise their own inputs, this directory is not created and should be dropped from this list.

**Acceptance Criteria:**
- [ ] Both scripts keep their upstream Apache-2.0 header plus the modification notice
- [ ] `SHUNT_MIN_LINES` is renamed `ANTI_TANGENT_SHUNT_MIN_LINES`, default 350
- [ ] Block messages name `mcp__anti-tangent__bulk_read` and show one example call
- [ ] Read hook passes: `offset`/`limit` set, sub-threshold files, nonexistent files
- [ ] Bash hook passes: pipes, redirections, `head`/`tail` with a count flag, non-read commands
- [ ] **Any parse ambiguity results in allow, never block**
- [ ] All 34 ported cases pass; `evals/run.sh` exits non-zero on any failure, **including** when the operational-reference check finds a leftover — proven by injecting one and asserting the runner goes red
- [ ] Every fetch uses `curl --fail` and asserts a non-empty result. Without `--fail`, a 404 writes its body into the destination file and exits 0 (measured 2026-09-07), so a missing upstream file would be silently ported as an error page
- [ ] Each ported JSON suite is asserted to hold exactly 17 cases (`jq length`), so a partial fetch cannot masquerade as a successful port
- [ ] `THIRD_PARTY_NOTICES.md` is reconciled against what this task actually copied — `run.sh` and any fetched fixtures included — in the same commit
- [ ] Every upstream file is fetched from the **pinned commit `3c24ca30ff63e1f5bbad1c43fe5324daff579123`**, never `main`, and that SHA is recorded in `THIRD_PARTY_NOTICES.md`
- [ ] `evals/fixtures/` contains exactly the files the two JSON suites reference — enumerated from the suites themselves, not guessed. If the suites synthesise their own inputs, the directory is not created at all
- [ ] No **operational** Portal/AiKA references remain. Attribution must SURVIVE: the modification notice, licence headers, README acknowledgement and benchmark comparison all legitimately name Portal/AiKA. The check asserts matches occur only on allowlisted lines — never that the grep is empty.

**Verify:** `bash plugin/anti-tangent-shunt/evals/run.sh` → all 34 cases pass, exit 0. `run.sh` must ALSO run the operational-reference check from Step 6, so the allowlist rule is enforced by the same command CI runs rather than living only in a manual step.

**Steps:**

- [ ] **Step 1: Fetch the upstream originals**

```bash
mkdir -p /tmp/shunt-upstream
# PINNED COMMIT, not main. `main` moves, and this task asserts a fixed case
# count (17 + 17). Verified 2026-09-07: this SHA resolves and serves the files.
SHUNT_SHA=3c24ca30ff63e1f5bbad1c43fe5324daff579123
BASE=https://raw.githubusercontent.com/spotify/portal-ai-plugins/$SHUNT_SHA/plugins/shunt
for f in hooks/hooks.json hooks/check-file-size hooks/check-bash-read \
         evals/run.sh evals/hook-evals.json evals/bash-hook-evals.json; do
  mkdir -p "/tmp/shunt-upstream/$(dirname "$f")"
  # --fail is load-bearing, not hygiene. Measured 2026-09-07: without it, a 404
  # writes the body "404: Not Found" INTO the destination file and exits 0 — the
  # port would "succeed" with a hook script containing an error page.
  curl --fail --location --silent --show-error -o "/tmp/shunt-upstream/$f" "$BASE/$f" \
    || { echo "FATAL: could not fetch $f from the pinned SHA"; exit 1; }
  [ -s "/tmp/shunt-upstream/$f" ] || { echo "FATAL: $f fetched empty"; exit 1; }
  echo "got $f"
done
head -20 /tmp/shunt-upstream/hooks/check-file-size
```

Read both hook scripts in full before adapting. **Preserve the licence header verbatim** — that header is the thing that makes `THIRD_PARTY_NOTICES.md` truthful.

- [ ] **Step 2: Port `check-file-size`**

Copy upstream to `plugin/anti-tangent-shunt/hooks/check-file-size`, then make exactly these changes:

1. Under the existing Apache header, add:
   ```
   # Modified 2026-09 for anti-tangent-mcp: replaced the Portal/AiKA delegation
   # target with the anti-tangent MCP tools.
   ```
2. Rename `SHUNT_MIN_LINES` → `ANTI_TANGENT_SHUNT_MIN_LINES` (default stays 350).
3. Replace the block message body with:
   ```
   FILE TOO LARGE TO READ DIRECTLY (%d lines, threshold %d)

   Reading this whole file would put ~%d lines into your context. Ask the
   question instead:

     mcp__anti-tangent__bulk_read
       question: "<the specific thing you need to know>"
       paths:    ["%s"]

   Ask a QUESTION, not "summarise this file" — you get back bullets, not prose.

   If you need to EDIT this file, follow the answer with a targeted read of the
   region it names: Read(file_path="%s", offset=<n>, limit=<n>). Targeted reads
   are never blocked.

   (Disable: ANTI_TANGENT_SHUNT_MIN_LINES=999999)
   ```
4. Keep the pass-through branches (offset/limit set, file missing, under threshold) byte-for-byte in logic.

- [ ] **Step 3: Port `check-bash-read`**

Same treatment. **Do not "improve" the command parsing.** Upstream's pass-through set — pipes, redirections, count flags, non-read commands — is the accumulated result of their eval cases, and this hook is the one whose false positives block real work. Where upstream is ambiguous, allow.

- [ ] **Step 4: Write `hooks.json`**

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Read",
        "hooks": [{ "type": "command", "command": "\"${CLAUDE_PLUGIN_ROOT}/hooks/check-file-size\"" }]
      },
      {
        "matcher": "Bash",
        "hooks": [{ "type": "command", "command": "\"${CLAUDE_PLUGIN_ROOT}/hooks/check-bash-read\"" }]
      }
    ]
  }
}
```

- [ ] **Step 5: Port the fixtures and runner**

First determine what the suites actually reference, then fetch exactly that:

```bash
jq -r '.. | strings' /tmp/shunt-upstream/evals/hook-evals.json \
       /tmp/shunt-upstream/evals/bash-hook-evals.json \
  | grep -o 'fixtures/[A-Za-z0-9._/-]*' | sort -u
```

Fetch each listed fixture from the same pinned SHA. If that command lists nothing, the suites generate their own inputs — then do **not** create `evals/fixtures/`, and drop it from the Files list.

Copy `hook-evals.json` (17) and `bash-hook-evals.json` (17) verbatim, editing ONLY the expected block-message substrings to match the new text. Do **not** port `transport-evals.sh` (17 cases) or `evals.json` (3) — both exercise the Portal CLI transport, which does not exist here. Adapt `run.sh` to drop its `--benchmark` mode and the transport suite, and to exit non-zero on any failure.

- [ ] **Step 6: Verify**

```bash
chmod +x plugin/anti-tangent-shunt/hooks/check-file-size \
         plugin/anti-tangent-shunt/hooks/check-bash-read \
         plugin/anti-tangent-shunt/evals/run.sh
bash plugin/anti-tangent-shunt/evals/run.sh

# Attribution is permitted in EXACTLY these places, identified by PATH (and, in
# the hooks, only on comment lines). Suppressing by keyword instead would let an
# operational line survive merely by containing a word like "upstream".
# Explicit if/else rather than a chained && ... || ...: the chained form does
# work (measured), but which branch runs on a match is not obvious to a reader,
# and this check is the one thing standing between an operational leftover and
# a green suite. run.sh must return non-zero when a match survives.
leftovers=$(grep -rinI 'portal\|aika' plugin/anti-tangent-shunt/ --exclude-dir=.git \
  | grep -vE '^plugin/anti-tangent-shunt/hooks/check-(file-size|bash-read):[0-9]+:[[:space:]]*#' \
  | grep -vE '^plugin/anti-tangent-shunt/(README\.md|evals/benchmarks\.md):' || true)
if [ -n "$leftovers" ]; then
  printf 'FAIL: operational Portal/AiKA reference:\n%s\n' "$leftovers"
  exit 1
fi
echo "clean: no operational Portal/AiKA references"
```

Expected: 34 cases pass, exit 0. The FILTERED grep prints nothing; any surviving line is a real leftover (a variable, a code path, an env var), not attribution. Do NOT "fix" a surviving attribution line by deleting it — that makes `THIRD_PARTY_NOTICES.md` untruthful.

- [ ] **Step 7: Smoke-test the real block path**

```bash
printf '{"tool_name":"Read","tool_input":{"file_path":"%s"}}' "$PWD/internal/mcpsrv/handlers.go" \
  | plugin/anti-tangent-shunt/hooks/check-file-size; echo "exit=$?"
printf '{"tool_name":"Read","tool_input":{"file_path":"%s","offset":10,"limit":20}}' "$PWD/internal/mcpsrv/handlers.go" \
  | plugin/anti-tangent-shunt/hooks/check-file-size; echo "exit=$?"
```

Expected: first blocks (`exit=2`, message names `mcp__anti-tangent__bulk_read`); second passes (`exit=0`, silent).

- [ ] **Step 8: Commit**

```bash
git add plugin/anti-tangent-shunt/ THIRD_PARTY_NOTICES.md
git commit -m "feat(shunt): port check-file-size and check-bash-read hooks

Adapted from spotify/portal-ai-plugins (Apache-2.0); Portal/AiKA delegation
replaced with mcp__anti-tangent__bulk_read. 34 upstream hook cases ported."
```

```json:metadata
{"files": ["plugin/anti-tangent-shunt/hooks/hooks.json", "plugin/anti-tangent-shunt/hooks/check-file-size", "plugin/anti-tangent-shunt/hooks/check-bash-read", "plugin/anti-tangent-shunt/evals/run.sh", "plugin/anti-tangent-shunt/evals/hook-evals.json", "plugin/anti-tangent-shunt/evals/bash-hook-evals.json", "THIRD_PARTY_NOTICES.md"], "verifyCommand": "bash plugin/anti-tangent-shunt/evals/run.sh", "acceptanceCriteria": ["Apache-2.0 headers preserved plus modification notice", "ANTI_TANGENT_SHUNT_MIN_LINES rename with default 350", "block messages name mcp__anti-tangent__bulk_read with an example", "read hook passes targeted/small/missing files", "bash hook passes pipes, redirections, count flags, non-read commands", "ambiguity allows rather than blocks", "34 ported cases pass", "all fetches use the pinned SHA recorded in THIRD_PARTY_NOTICES.md", "fixtures enumerated from the suites, not guessed", "no operational Portal/AiKA references remain; attribution, licence headers and benchmark comparison survive in the documented allowlisted locations"], "modelTier": "standard"}
```

---

### Task 11: Shunt plugin manifest, skills and README

**Goal:** The shunt plugin is installable and its skills tell the implementer how to use the tools well.

**Files:**
- Create: `plugin/anti-tangent-shunt/.claude-plugin/plugin.json`
- Create: `plugin/anti-tangent-shunt/README.md`
- Create: `plugin/anti-tangent-shunt/skills/bulk-reader/SKILL.md`
- Create: `plugin/anti-tangent-shunt/skills/code-writer/SKILL.md`

**Acceptance Criteria:**
- [ ] `plugin.json` mirrors `anti-tangent-protocol`'s field shape, `license: MIT`, version `0.1.0`
- [ ] README states the `jq` dependency, the threshold variable, and install instructions
- [ ] README carries the Acknowledgements pointing at shunt and `THIRD_PARTY_NOTICES.md`
- [ ] `bulk-reader/SKILL.md` says: ask a **question**, not "summarise"; and follow up with a targeted `Read` before editing
- [ ] `code-writer/SKILL.md` says: `reference_path` always; prefer `target_path`; `overwrite` is deliberate
- [ ] Both SKILL.md files have valid frontmatter with `name` and `description`

**Verify:** `jq -e . plugin/anti-tangent-shunt/.claude-plugin/plugin.json` plus the frontmatter parse loop in Step 5 → both clean

**Steps:**

- [ ] **Step 1: Write `plugin.json`**

```json
{
  "name": "anti-tangent-shunt",
  "description": "Routes I/O-heavy implementer work to a cheap worker model through anti-tangent-mcp. PreToolUse hooks block oversized full-file reads and redirect them to the bulk_read MCP tool, so a large file corpus never enters the implementer's context.",
  "version": "0.1.0",
  "author": { "name": "Patrick Gilmore", "email": "p@patiently.io" },
  "homepage": "https://github.com/patiently/anti-tangent-mcp",
  "repository": "https://github.com/patiently/anti-tangent-mcp",
  "license": "MIT",
  "keywords": ["anti-tangent", "mcp", "token-efficiency", "hooks", "delegation"]
}
```

- [ ] **Step 2: Write `skills/bulk-reader/SKILL.md`**

```markdown
---
name: bulk-reader
description: Use when a Read is blocked for being too large, or before reading several files just to answer one question. Delegates the reading to a cheap worker model so the file contents never enter your context.
---

# Reading files without reading them

`mcp__anti-tangent__bulk_read` reads files server-side and returns only an answer.

## Ask a question, not for a summary

The worker answers exactly what you ask and nothing else. A vague prompt wastes
the call.

- Bad: `"summarise this file"` — you get prose you must then re-read.
- Good: `"Which functions write to the database, and what are their receivers?"`
- Good: `"Where is retry configured, and what is the backoff?"`

Each bullet comes back led by an exact name, type or line number, so you can act
on it directly.

## If you are going to EDIT, read again afterwards

The answer carries **no reliable line anchors**. Never edit from it. Use it to
locate the region, then take a targeted read — which the hooks never block:

    Read(file_path="/abs/path.go", offset=210, limit=60)

## When NOT to delegate

- **Debugging.** That needs your reasoning over the real text, not a digest.
- **Editing.** See above — targeted reads.
- **Files under the threshold** (default 350 lines). The round trip costs more
  than it saves.
- **Architectural judgement.** Delegate the reading, never the deciding.

## Limits

Up to 50 absolute paths per call, subject to the server's payload cap. Split
larger sets rather than raising the cap.
```

- [ ] **Step 3: Write `skills/code-writer/SKILL.md`**

```markdown
---
name: code-writer
description: Use when generating boilerplate that follows an existing pattern - a test file mirroring another, a DTO, a repetitive interface implementation. Sends the generation to a cheap worker model so the generated code never enters your context.
---

# Generating boilerplate without reading it back

`mcp__anti-tangent__code_write` generates code that matches a reference file.

## `reference_path` is required, and it is the whole point

The worker matches the reference's conventions, naming and structure. Without
one it would emit context-free code that fits nothing. Pick the closest
existing example — the sibling test, the neighbouring handler.

## Prefer `target_path`

With `target_path` set, the server writes the file and returns only a line
count. The generated code never enters your context — which is the entire
saving. Omit it only when you genuinely need to inspect the code first.

    mcp__anti-tangent__code_write
      spec:           "Table test for Add covering zero, negative and overflow"
      reference_path: "/abs/repo/internal/math/sub_test.go"
      target_path:    "/abs/repo/internal/math/add_test.go"

## `overwrite` is deliberate

An existing target is refused unless you pass `overwrite: true`. That default
exists because a worker model acting on a misread spec would otherwise destroy
a hand-written file, and the response tells you only a line count — you would
not see what was lost. The parent directory must already exist; this tool never
creates directories.

## When NOT to delegate

Anything requiring judgement: business logic, security-sensitive code,
concurrency, an algorithm with a correctness argument. Boilerplate means the
pattern is already decided and only the filling changes.

## Verify what you generated

You have not read this code, and you do not need to — that is the saving. What
you DO owe is proof it works: run the task's tests and its build before treating
it as done, and `validate_completion` still applies to the task as a whole.

Verification, not inspection. If a change genuinely needs your eyes on the text,
it was not boilerplate and should not have been delegated.
```

- [ ] **Step 4: Write the plugin README**

Cover: what it does; the `jq` dependency; `ANTI_TANGENT_SHUNT_MIN_LINES`; that the MCP server must be configured with a worker model; install via
`claude plugin marketplace add patiently/anti-tangent-mcp` then `claude plugin install anti-tangent-shunt@anti-tangent-mcp`; how to run the evals; a **Benchmarks** placeholder heading that Task 16 fills; and an Acknowledgements section:

```markdown
## Acknowledgements

The hooks in this plugin are adapted from Spotify's
[shunt](https://github.com/spotify/portal-ai-plugins/tree/main/plugins/shunt)
plugin (Apache-2.0), described by Dimitri Mazmanov in
["Portal by Spotify cut my Claude Code token usage by 90%"](https://engineering.atspotify.com/2026/9/portal-by-spotify-cut-my-claude-code-token-usage-by-90)
(Spotify Engineering, September 2026). Their hook-gated routing, the 350-line
threshold and the explicit non-delegation list are carried over; the
Portal/AiKA transport is replaced by anti-tangent's own provider layer. See
[`THIRD_PARTY_NOTICES.md`](../../THIRD_PARTY_NOTICES.md).
```

- [ ] **Step 5: Verify and commit**

```bash
jq -e . plugin/anti-tangent-shunt/.claude-plugin/plugin.json >/dev/null && echo "manifest ok"

# Parse the frontmatter rather than eyeballing it: printing five lines proves
# nothing about whether name/description are present and non-empty.
for f in plugin/anti-tangent-shunt/skills/*/SKILL.md; do
  awk 'NR==1 && $0!="---" {print "FAIL no frontmatter: FILE"; exit 1}
       NR>1 && $0=="---" {exit}
       /^name:[[:space:]]*[^[:space:]]/ {n=1}
       /^description:[[:space:]]*[^[:space:]]/ {d=1}
       END {if (!n || !d) {print "FAIL missing name/description: FILE"; exit 1}}' \
    FILE="$f" "$f" || exit 1
  echo "frontmatter ok: $f"
done
head -5 plugin/anti-tangent-shunt/skills/bulk-reader/SKILL.md
git add plugin/anti-tangent-shunt/
git commit -m "feat(shunt): plugin manifest, bulk-reader and code-writer skills, README"
```

```json:metadata
{"files": ["plugin/anti-tangent-shunt/.claude-plugin/plugin.json", "plugin/anti-tangent-shunt/README.md", "plugin/anti-tangent-shunt/skills/bulk-reader/SKILL.md", "plugin/anti-tangent-shunt/skills/code-writer/SKILL.md"], "verifyCommand": "jq -e . plugin/anti-tangent-shunt/.claude-plugin/plugin.json", "acceptanceCriteria": ["plugin.json mirrors the protocol plugin shape with MIT and 0.1.0", "README states jq dependency, threshold var and install steps", "README carries Acknowledgements linking THIRD_PARTY_NOTICES.md", "bulk-reader skill says ask a question and re-read before editing", "code-writer skill covers reference_path, target_path and overwrite", "both SKILL.md have valid frontmatter"], "modelTier": "mechanical"}
```

---

### Task 12: The completion guard plugin

**Goal:** A `PostToolUse` hook that detects a `completed` task close which skipped `validate_completion` (or read `fail`), and forces the agent to reopen the task and run the gate before doing anything else.

**Enforcement contract — read this before the AC.** `PostToolUse` fires *after* the state change. It **cannot prevent** the close, and nothing here should claim it does. What `exit 2` does is return stderr to the model as something it must address before its next action. The contract is therefore **post-close detection plus a mandated recovery flow**, not prevention. That is deliberate: closing a task is legitimate; closing it without the gate is what must not stand. A `PreToolUse` hook could prevent the close but could not see the subagent's pasted `summary_block`, which only exists in the result of the very call being closed.

**Files:**
- Create: `plugin/anti-tangent-guard/.claude-plugin/plugin.json`
- Create: `plugin/anti-tangent-guard/README.md`
- Create: `plugin/anti-tangent-guard/hooks/hooks.json`
- Create: `plugin/anti-tangent-guard/hooks/check-task-complete`
- Create: `plugin/anti-tangent-guard/evals/run.sh`
- Create: `plugin/anti-tangent-guard/evals/guard-evals.json`

**Acceptance Criteria:**
- [ ] Exits 0 unless `tool_name == TaskUpdate` and `tool_input.status == completed`
- [ ] Passes when a `mcp__anti-tangent__validate_completion` `tool_use` is in the window
- [ ] Passes when an `anti-tangent envelope` + `session_id:` marker appears in a `tool_result` in the window
- [ ] Blocks (exit 2) when neither signal is present, with a message naming the tool to call
- [ ] Blocks (exit 2, **different message**) when the LAST summary block in the window reads `verdict: fail`
- [ ] `ANTI_TANGENT_COMPLETION_GUARD=0` short-circuits to exit 0
- [ ] Fails open: missing/unreadable transcript, absent `jq` or `python3`, malformed JSON → exit 0
- [ ] Window is scoped to the most recent `in_progress` for that `taskId`, whole transcript as fallback
- [ ] Both block messages state the recovery flow explicitly: reopen with `status=in_progress`, run the gate, re-close
- [ ] Malformed **stdin** JSON exits 0; a malformed **transcript line** is skipped and the remaining lines still decide — different inputs, different behaviours, tested separately
- [ ] `README.md` documents: active-on-install, both block conditions, the `jq`+`python3` dependency, the kill switch, the fail-open policy, the trace log, and how to run the evals

**Verify:** `bash plugin/anti-tangent-guard/evals/run.sh` → all 15 cases pass, exit 0

The count is 15: the table in Step 4 is the complete enumeration. `run.sh` must assert it ran exactly 15 cases, so adding a case without updating the count fails loudly instead of silently.

**Steps:**

- [ ] **Step 1: Write the hook**

Create `plugin/anti-tangent-guard/hooks/check-task-complete`:

```bash
#!/usr/bin/env bash
# PostToolUse hook: DETECT a task close that skipped anti-tangent's
# validate_completion gate, or ran it and read `fail`, and mandate recovery
# before the agent's next action. It does NOT prevent the close — see below.
#
# WHY PostToolUse ON TaskUpdate, not SubagentStop: anti-tangent's model is one
# task = one session = one subagent, which points at SubagentStop. But plans are
# executed two ways — executing-plans runs tasks in the controller's own
# session, subagent-driven-development dispatches one subagent per task. Only
# the TaskUpdate close is common to both, and both leave a detectable trace
# there (see the two pass signals below).
#
# WHY exit 2 AFTER the close rather than PreToolUse: closing the task is
# legitimate; closing it without the gate is not. exit 2 sends stderr back as
# something the model must address before its next action, without refusing a
# state change that may well be correct.
#
# FAILS OPEN, ALWAYS. A guard that blocks work because it broke is worse than
# the drift it prevents.
set -uo pipefail

TRACE_LOG="${ANTI_TANGENT_GUARD_TRACE_LOG:-/tmp/claude-hooks/anti-tangent-guard.log}"
mkdir -p "$(dirname "$TRACE_LOG")" 2>/dev/null || true
trace() {
    printf '%s | guard | task=%s | %s%s\n' \
        "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "${1:-?}" "${2:-?}" "${3:+ | $3}" \
        >> "$TRACE_LOG" 2>/dev/null || true
}

[[ "${ANTI_TANGENT_COMPLETION_GUARD:-1}" == "0" ]] && { trace "?" "skip" "guard=0"; exit 0; }
trap 'trace "?" "error" "trap-ERR"; exit 0' ERR
command -v jq >/dev/null 2>&1      || { trace "?" "skip" "no-jq"; exit 0; }
command -v python3 >/dev/null 2>&1 || { trace "?" "skip" "no-python3"; exit 0; }

INPUT=$(cat)
TOOL_NAME=$(echo "$INPUT" | jq -r '.tool_name // empty' 2>/dev/null | tr -d '\r')
[[ "$TOOL_NAME" != "TaskUpdate" ]] && { trace "?" "skip" "tool=$TOOL_NAME"; exit 0; }
STATUS=$(echo "$INPUT" | jq -r '.tool_input.status // empty' 2>/dev/null | tr -d '\r')
[[ "$STATUS" != "completed" ]] && { trace "?" "skip" "status=$STATUS"; exit 0; }
TASK_ID=$(echo "$INPUT" | jq -r '.tool_input.taskId // empty' 2>/dev/null | tr -d '\r')
[[ -z "$TASK_ID" ]] && { trace "?" "skip" "no-task-id"; exit 0; }
TRANSCRIPT=$(echo "$INPUT" | jq -r '.transcript_path // empty' 2>/dev/null | tr -d '\r')
[[ -z "$TRANSCRIPT" || ! -f "$TRANSCRIPT" ]] && { trace "$TASK_ID" "skip" "no-transcript"; exit 0; }

# Walk the transcript for the two pass signals. Python, not jq: the entries nest
# content blocks inside tool_result inside message.content, and task ids must be
# reconstructed by creation order because TaskCreate does not carry one.
PY='
import json, re, sys
path, task_id = sys.argv[1], str(sys.argv[2])
try:
    lines = open(path).readlines()
except Exception:
    print(json.dumps({"error": True})); sys.exit(0)

next_id, last_inprogress = 1, -1
called_idx, block_texts = [], []   # (idx) of direct calls; (idx, text) of summary blocks

for idx, line in enumerate(lines):
    try: entry = json.loads(line)
    except Exception: continue
    etype = entry.get("type")
    msg = entry.get("message") or {}
    if etype == "assistant":
        for c in msg.get("content") or []:
            if not isinstance(c, dict): continue
            if c.get("type") == "tool_use":
                name, inp = c.get("name", ""), (c.get("input") or {})
                if name == "mcp__anti-tangent__validate_completion":
                    called_idx.append(idx)
                elif name == "TaskCreate":
                    next_id += 1
                elif name == "TaskUpdate":
                    tid = str(inp.get("taskId", ""))
                    if tid == task_id and inp.get("status") == "in_progress":
                        last_inprogress = idx
                    try:
                        if int(tid) >= next_id: next_id = int(tid) + 1
                    except (ValueError, TypeError): pass
    elif etype == "user":
        content = msg.get("content")
        if isinstance(content, list):
            for c in content:
                if not isinstance(c, dict) or c.get("type") != "tool_result": continue
                inner = c.get("content")
                chunks = [inner] if isinstance(inner, str) else [
                    ic.get("text", "") for ic in (inner or []) if isinstance(ic, dict) and ic.get("type") == "text"]
                for t in chunks:
                    if t and "anti-tangent envelope" in t and "session_id:" in t:
                        block_texts.append((idx, t))

scan_from = last_inprogress if last_inprogress >= 0 else 0
called = any(i >= scan_from for i in called_idx)
blocks = [t for (i, t) in block_texts if i >= scan_from]

# LAST block wins: a re-validation after fixing findings is the whole point.
verdict = ""
if blocks:
    m = re.findall(r"^\s*verdict:\s*(\w+)", blocks[-1], re.MULTILINE)
    if m: verdict = m[-1]

print(json.dumps({"error": False, "called": called,
                  "block_found": bool(blocks), "verdict": verdict}))
'
RESULT=$({ python3 -c "$PY" "$TRANSCRIPT" "$TASK_ID" 2>/dev/null || echo '{"error":true}'; } | tr -d '\r')
[[ "$(echo "$RESULT" | jq -r '.error // true')" == "true" ]] && { trace "$TASK_ID" "skip" "parse-error"; exit 0; }

CALLED=$(echo "$RESULT" | jq -r '.called // false')
BLOCK=$(echo "$RESULT" | jq -r '.block_found // false')
VERDICT=$(echo "$RESULT" | jq -r '.verdict // ""')
trace "$TASK_ID" "parsed" "called=$CALLED block=$BLOCK verdict=$VERDICT"

if [[ "$VERDICT" == "fail" ]]; then
    trace "$TASK_ID" "block" "verdict-fail"
    {
        echo "TASK CLOSED ON A FAILED anti-tangent VERDICT"
        echo
        echo "Task #$TASK_ID went to completed, but the most recent"
        echo "validate_completion in this task's window returned verdict: fail."
        echo
        echo "The protocol is explicit: if the verdict is fail, or carries critical"
        echo "or major findings, do not report DONE. Fix the findings and re-validate."
        echo
        echo "  1. TaskUpdate taskId=$TASK_ID status=in_progress"
        echo "  2. Address the findings from that response"
        echo "  3. Call mcp__anti-tangent__validate_completion again"
        echo "  4. Re-close once the verdict is pass"
        echo
        echo "EXCEPTION: if the response carried submission_defect_only: true, the"
        echo "findings are about what you SUBMITTED, not your code. Attach the"
        echo "missing evidence (a complete final_diff, test output) and re-submit."
        echo
        echo "(Disable: ANTI_TANGENT_COMPLETION_GUARD=0. Trace: $TRACE_LOG)"
    } >&2
    exit 2
fi

if [[ "$CALLED" == "true" || "$BLOCK" == "true" ]]; then
    trace "$TASK_ID" "pass" "called=$CALLED block=$BLOCK"
    exit 0
fi

trace "$TASK_ID" "block" "no-validation"
{
    echo "TASK CLOSED WITHOUT anti-tangent validate_completion"
    echo
    echo "Task #$TASK_ID went to completed, but nothing in this task's window shows"
    echo "the completion gate ran — no mcp__anti-tangent__validate_completion call,"
    echo "and no 'anti-tangent envelope' summary_block in any subagent report."
    echo
    echo "Before moving on:"
    echo "  1. TaskUpdate taskId=$TASK_ID status=in_progress"
    echo "  2. Call mcp__anti-tangent__validate_completion with the session_id, a"
    echo "     summary, a COMPLETE final_diff (or final_diff_path), and test evidence"
    echo "  3. Paste the returned summary_block into your DONE report verbatim"
    echo "  4. Re-close once the verdict is pass"
    echo
    echo "If a subagent did the work, it must paste its summary_block into the"
    echo "report it returns — that is what makes the gate visible from here."
    echo
    echo "Do NOT simply re-close and hope this stops firing. It will not."
    echo
    echo "(Disable: ANTI_TANGENT_COMPLETION_GUARD=0. Trace: $TRACE_LOG)"
} >&2
exit 2
```

- [ ] **Step 2: Write `hooks.json`**

```json
{
  "hooks": {
    "PostToolUse": [
      {
        "matcher": "TaskUpdate",
        "hooks": [{ "type": "command", "command": "\"${CLAUDE_PLUGIN_ROOT}/hooks/check-task-complete\"" }]
      }
    ]
  }
}
```

- [ ] **Step 2b: Write the plugin README**

`plugin/anti-tangent-guard/README.md` must state, each explicitly: that installing the plugin activates it immediately; both block conditions (gate never ran; last verdict was `fail`); that it is **post-close detection with a mandated recovery flow**, not prevention — `PostToolUse` cannot stop the close; the `jq` + `python3` dependency; `ANTI_TANGENT_COMPLETION_GUARD=0` as the kill switch; the fail-open policy; the trace-log location and how to tail it; and `bash evals/run.sh`. Also state that the server itself never blocks — enforcement lives here, in a plugin, on purpose.

- [ ] **Step 3: Write `plugin.json`**

Same shape as Task 11's, `name: anti-tangent-guard`, version `0.1.0`, description explaining it enforces the completion gate at task close and is active on install.

- [ ] **Step 4: Write the evals**

`evals/guard-evals.json` — each case is a stdin payload plus a synthetic transcript and an expected exit code. Cover all fifteen:

| Case | Expect |
|---|---|
| `tool_name` is not TaskUpdate | 0 |
| status is `in_progress` | 0 |
| direct `validate_completion` call in window | 0 |
| summary_block in a `tool_result` in window | 0 |
| neither signal present | 2 |
| last block `verdict: fail` | 2 |
| block `verdict: fail` then a later block `verdict: pass` | 0 (last wins) |
| `ANTI_TANGENT_COMPLETION_GUARD=0` with no signal | 0 |
| `transcript_path` missing from disk | 0 (fail open) |
| malformed **stdin** JSON | 0 (fail open) |
| transcript with one malformed JSONL line, signal on another | 0 (bad line skipped, not fatal) |
| `jq` absent from PATH | 0 (fail open) |
| `python3` absent from PATH | 0 (fail open) |
| signal present BEFORE the latest `in_progress`, none after | 2 (window scoping excludes it) |
| no `in_progress` at all, signal present anywhere | 0 (whole-transcript fallback) |

Two JSON inputs, two behaviours: malformed **stdin** means the hook cannot know what it is looking at, so it fails open. A malformed **transcript line** is skipped and the rest still decides — that is not fail-open and must not be conflated with it. Test `jq`/`python3` absence with a stub directory, not by emptying `PATH` — the
hook also needs `cat`, `tr`, `mkdir` and `date`, so a bare `PATH=` breaks
unrelated machinery and proves nothing. Build a temp dir of symlinks to every
utility the hook uses EXCEPT the one under test, then invoke
`/bin/bash <hook>` with `PATH` set to just that dir.

`evals/run.sh` writes each transcript to a temp file, pipes the payload, compares the exit code, and exits non-zero on any mismatch.

- [ ] **Step 5: Verify**

```bash
chmod +x plugin/anti-tangent-guard/hooks/check-task-complete plugin/anti-tangent-guard/evals/run.sh
bash plugin/anti-tangent-guard/evals/run.sh
```

Expected: 15 cases pass, exit 0 — matching the 15 rows enumerated in Step 4.

- [ ] **Step 6: Commit**

```bash
git add plugin/anti-tangent-guard/
git commit -m "feat(guard): PostToolUse hook enforcing validate_completion at task close"
```

```json:metadata
{"files": ["plugin/anti-tangent-guard/hooks/check-task-complete", "plugin/anti-tangent-guard/hooks/hooks.json", "plugin/anti-tangent-guard/.claude-plugin/plugin.json", "plugin/anti-tangent-guard/README.md", "plugin/anti-tangent-guard/evals/run.sh", "plugin/anti-tangent-guard/evals/guard-evals.json"], "verifyCommand": "bash plugin/anti-tangent-guard/evals/run.sh", "acceptanceCriteria": ["exits 0 unless TaskUpdate with status completed", "passes on a direct validate_completion call in window", "passes on a summary_block marker in a tool_result", "blocks with exit 2 when neither signal present", "blocks with a distinct message on verdict fail", "last summary block wins", "GUARD=0 short-circuits", "fails open on missing transcript or absent jq/python3"], "modelTier": "standard"}
```

---

### Task 13: Hook evals in CI

**Goal:** Both eval suites gate every PR.

**Files:**
- Modify: `.github/workflows/ci.yml`

**Acceptance Criteria:**
- [ ] A `hook-evals` job runs both suites
- [ ] The eval **scripts** make no provider calls and no application network calls, and need no API keys. (The CI job itself still uses the network for `actions/checkout` and installing `jq` — ordinary CI setup, not something the suites depend on.)
- [ ] `build-test` gains `hook-evals` in its `needs` so a hook regression blocks the Go job too
- [ ] The job installs `jq` explicitly rather than assuming the runner image

**Verify:** `bash plugin/anti-tangent-shunt/evals/run.sh && bash plugin/anti-tangent-guard/evals/run.sh` locally; CI green on push

**Steps:**

- [ ] **Step 1: Add the job**

In `.github/workflows/ci.yml`, after the `protocol-docs` job:

```yaml
  hook-evals:
    name: Plugin hook evals
    runs-on: ubuntu-latest
    steps:
      - name: Checkout code
        uses: actions/checkout@v6

      - name: Install jq
        run: sudo apt-get update && sudo apt-get install -y jq

      - name: Shunt hook evals
        run: bash plugin/anti-tangent-shunt/evals/run.sh

      - name: Completion guard evals
        run: bash plugin/anti-tangent-guard/evals/run.sh

      - name: Eval suites make no provider or network calls
        run: |
          # A limited regression tripwire, NOT proof of network isolation: it
          # catches the obvious clients and credential names only. Something
          # determined (python sockets, /dev/tcp, a differently-named key) slips
          # past. The real guarantee is that these scripts read only stdin and files.
          ! grep -rnE 'curl|wget|nc |ANTHROPIC_API_KEY|OPENAI_API_KEY|GOOGLE_API_KEY' \
              plugin/anti-tangent-shunt/evals plugin/anti-tangent-guard/evals \
              plugin/anti-tangent-shunt/hooks plugin/anti-tangent-guard/hooks
```

- [ ] **Step 2: Gate the Go job on it**

```yaml
  build-test:
    name: Build & Test (Go)
    needs: [changelog, protocol-docs, hook-evals]
```

- [ ] **Step 3: Verify locally, then commit**

```bash
bash plugin/anti-tangent-shunt/evals/run.sh && bash plugin/anti-tangent-guard/evals/run.sh && echo "both suites pass"
git add .github/workflows/ci.yml
git commit -m "ci: gate on plugin hook eval suites"
```

```json:metadata
{"files": [".github/workflows/ci.yml"], "verifyCommand": "bash plugin/anti-tangent-shunt/evals/run.sh && bash plugin/anti-tangent-guard/evals/run.sh", "acceptanceCriteria": ["hook-evals job runs both suites", "no provider keys or network needed", "build-test needs hook-evals", "jq installed explicitly"], "modelTier": "mechanical"}
```

---

### Task 14: Protocol documentation

**Goal:** The protocol tells implementers what never gets delegated and what to do when a hook blocks, and tells controllers about the guard.

**Files:**
- Modify: `docs/protocol/core.md`
- Modify: `docs/protocol/implementer.md`
- Modify: `docs/protocol/controller.md`
- Modify: `plugin/anti-tangent-protocol/protocol/core.md` (resync, same commit)
- Modify: `plugin/anti-tangent-protocol/protocol/implementer.md` (resync, same commit)
- Modify: `plugin/anti-tangent-protocol/protocol/controller.md` (resync, same commit)

**Acceptance Criteria:**
- [ ] `core.md` gains an unnumbered "What is never delegated" section covering both loops
- [ ] That section does **not** contradict `code_write`: it must distinguish judgement-bearing changes to existing code (never delegated) from pattern-following generation of new files (delegable, with the implementer owning reference choice and **verification**). It must say "verification", never "review", of generated code — see the generated-code contract in Global Constraints
- [ ] Its wording is **ours**, not upstream's Apache-2.0 prose
- [ ] `implementer.md` gains an unnumbered "Large reads" clause: call `bulk_read` with a question; targeted `Read` before editing
- [ ] `controller.md` documents the guard, both block messages and the kill switch
- [ ] **`core.md`'s own tool count is corrected**: line 9 currently reads "It exposes seven tools" and enumerates them. It must say nine and name `bulk_read` / `code_write` as the I/O-delegation pair — they send nothing for *review*, so the following sentence about the reviewer LLM needs care. No other task edits this file, so if Task 14 skips it the stale count ships in the protocol doc and its CI-compared bundle copy
- [ ] **Every part stays strictly under 16,000 bytes**
- [ ] The bundled copy is byte-identical to `docs/protocol/`
- [ ] `scripts/check-protocol-docs.sh` passes (no new/duplicate `§` identifiers)

**Verify:** `bash scripts/check-protocol-docs.sh && for f in docs/protocol/*.md; do wc -c "$f"; done` → all < 16000

**Steps:**

- [ ] **Step 1: Check the budget before writing**

```bash
for f in docs/protocol/*.md; do printf '%s %s (headroom %s)\n' "$(wc -c < "$f")" "$f" "$((16000 - $(wc -c < "$f")))"; done
```

`core.md` has ~1,540 bytes and `implementer.md` ~1,983. Write to fit. If a section does not fit, trim existing prose in the SAME file — do not create a sixth part, which would need a `check-protocol-docs.sh` registry entry.

- [ ] **Step 2: Add to `core.md`** (unnumbered heading)

Measure before pasting: `wc -c` the insertion and add it to core.md's current size. If the total reaches 16,000, trim core.md's existing FAQ answers — the longest prose there and the least structurally load-bearing — never a numbered section, whose identifiers `scripts/check-protocol-docs.sh` tracks.

```markdown
### What is never delegated

Two loops in this protocol hand work to another model: the review loop (a
reviewer LLM judges a task) and the I/O loop (a worker model reads files or
generates boilerplate). Both stop at the same line.

- **Reasoning stays with the implementer.** Debugging, architecture, and any
  correctness argument. A digest of a file is not a substitute for reading it
  when the question is *why* something misbehaves.
- **Changes to existing code stay with the implementer.** A worker's answer
  carries no reliable line anchors: use it to locate a region, then read that
  region directly and edit it yourself. Generating a NEW file that follows an
  existing pattern is different, and is delegable — but choosing the reference
  and proving the result works remain yours. Generated code is **verified, not
  inspected**: run the tests and the build. You are not required to read it,
  which is the whole point — reading it would put back exactly the context the
  delegation removed.
- **Judgement stays with the implementer.** Delegating the reading is not
  delegating the deciding.
- **Small inputs are not worth delegating.** Below the threshold the round
  trip costs more than it saves.

Delegation moves *volume* off the implementer's context. It never moves
responsibility: the implementer still answers for the result at
`validate_completion`.
```

- [ ] **Step 3: Add to `implementer.md`** (unnumbered, ~500 bytes)

```markdown
### Large reads

If a `Read` is blocked for exceeding the line threshold, do not work around it
with `cat`, and do not lower the threshold. Call `bulk_read` with a **question**
— "which methods write to the database?", not "summarise this file". You get
bullets led by exact identifiers.

If you then need to EDIT what the answer found, take a targeted read of that
region (`offset`/`limit`); targeted reads are never blocked. Never edit from the
answer alone — it carries no reliable line anchors.
```

- [ ] **Step 4: Add to `controller.md`** (ample headroom)

Document: the guard is a `PostToolUse` hook on `TaskUpdate`; it passes on either a direct `validate_completion` call or a pasted `summary_block`; **this is why implementers must paste the block verbatim** — it is the only trace a controller-side hook can see of a subagent's gate; it blocks on absence and on `verdict: fail`; `ANTI_TANGENT_COMPLETION_GUARD=0` disables it; it fails open; and it lives in a plugin, so the server stays advisory.

- [ ] **Step 5: Resync the bundle and verify**

```bash
rm -f plugin/anti-tangent-protocol/protocol/*.md
cp docs/protocol/*.md plugin/anti-tangent-protocol/protocol/
for f in docs/protocol/*.md; do
  b=$(wc -c < "$f"); echo "$f = $b"; [ "$b" -ge 16000 ] && { echo "OVER BUDGET"; exit 1; }
done
diff -r docs/protocol plugin/anti-tangent-protocol/protocol && echo "bundle in sync"
bash scripts/check-protocol-docs.sh
```

- [ ] **Step 6: Commit** (docs and bundle together — CI byte-compares them)

```bash
git add docs/protocol/ plugin/anti-tangent-protocol/protocol/
git commit -m "docs(protocol): non-delegation list, large-reads clause, completion guard"
```

```json:metadata
{"files": ["docs/protocol/core.md", "docs/protocol/implementer.md", "docs/protocol/controller.md", "plugin/anti-tangent-protocol/protocol/core.md", "plugin/anti-tangent-protocol/protocol/implementer.md", "plugin/anti-tangent-protocol/protocol/controller.md"], "verifyCommand": "bash scripts/check-protocol-docs.sh && diff -r docs/protocol plugin/anti-tangent-protocol/protocol", "acceptanceCriteria": ["core.md gains an unnumbered non-delegation section in our own words", "implementer.md gains a Large reads clause", "controller.md documents the guard and kill switch", "core.md line 9 says nine tools and names bulk_read/code_write", "every part under 16000 bytes", "bundle byte-identical", "check-protocol-docs.sh passes"], "modelTier": "standard"}
```

---

### Task 15: README, CLAUDE.md and marketplace

**Goal:** The repo's own documentation describes nine tools, two new plugins, the new variables, and a trust model that covers writes.

**Files:**
- Modify: `README.md`
- Modify: `CLAUDE.md`
- Modify: `.claude-plugin/marketplace.json`

**Acceptance Criteria:**
- [ ] README documents `bulk_read` and `code_write` with their full field lists
- [ ] README documents `ANTI_TANGENT_WORKER_MODEL`, `ANTI_TANGENT_WORKER_MAX_TOKENS`, and states `ANTI_TANGENT_SHUNT_MIN_LINES` is read by hooks, not the server
- [ ] README's filesystem section covers **writes**, including the Windows `O_NOFOLLOW` gap
- [ ] README gains Acknowledgements crediting shunt and the blog post
- [ ] README documents both plugins and their install commands
- [ ] `CLAUDE.md` says nine tools, lists the new files, and states that blocking lives in plugins so the server stays advisory
- [ ] `marketplace.json` gains both plugins; its `version` bumps `0.8.0` → `0.9.0`
- [ ] No stale catalog-count claim remains on the **live surface** — `README.md`, `CLAUDE.md`, `docs/protocol/`, `internal/`, `examples/`, `plugin/`. Verified 2026-09-07, the live hits are exactly: `CLAUDE.md:7`, `README.md:275`, `docs/protocol/core.md:9`, `internal/mcpsrv/server.go:38`, and the bundled `plugin/anti-tangent-protocol/protocol/core.md:9` (carried by Task 14's resync)
- [ ] **`docs/superpowers/` is deliberately excluded.** Historical plans and specs record what was true when written — editing them to satisfy a grep would corrupt the record, and this plan and its own spec both quote the phrase while instructing its removal. An unscoped `git grep` is unsatisfiable, not merely noisy

**Verify:** `jq -e '.version == "0.9.0" and (.plugins | length == 4)' .claude-plugin/marketplace.json && ! git grep -nE 'seven tools|seven registered|three handlers' -- README.md CLAUDE.md docs/protocol internal examples plugin`

`git grep` searches every tracked file, which is what "anywhere" in the acceptance criteria means — `README.md` and `CLAUDE.md` alone would miss `internal/mcpsrv/server.go`, which Step 1 already flags.

**Steps:**

- [ ] **Step 1: Find every stale count**

```bash
git grep -nE 'seven tools|seven registered|three handlers' -- README.md CLAUDE.md docs/protocol internal examples plugin
```

- [ ] **Step 2: Update the README**

Add the two tools to the tool list and reference section. Add the two variables to the config table. Extend the filesystem-access paragraph — it currently argues only from reads:

> The server also **writes** one kind of file: `code_write` with a `target_path`. The same argument holds — the calling agent already has `Write` and `Edit`, so the server acquires no capability the caller lacks — but `ANTI_TANGENT_PLAN_ROOTS` is now load-bearing for writes as well as reads. A write resolves the target's **parent** directory (the leaf does not exist yet), containment-checks that, and opens the leaf with `O_NOFOLLOW`, so a symlink planted at the target cannot redirect the write outside the roots. An existing file is refused unless `overwrite: true`, and the parent must already exist — `code_write` never creates directories. On Windows the final-component symlink-swap window is not closed, exactly as for reads.

Add Acknowledgements (same text as the plugin README) and a plugins section with both install commands.

- [ ] **Step 3: Update `CLAUDE.md`**

- "seven tools" → "nine tools", naming `bulk_read` and `code_write` as the I/O-delegation pair (they send no *review*, so the sentence about a reviewer LLM needs care).
- Add `worker_call.go`, `worker_handlers.go`, `file_target.go`, `internal/notices/` to the architecture tree.
- Under "What This Repo Is Not", after the advisory bullet:

```markdown
  The v0.18.0 hooks (`anti-tangent-shunt`, `anti-tangent-guard`) **do** block —
  a PreToolUse hook refuses an oversized read before it happens, and the
  completion guard *detects* a task close that skipped `validate_completion`
  after the fact and returns a blocking instruction to reopen the task,
  validate, and re-close. (The guard cannot prevent the close: `PostToolUse`
  fires after the state change. See the guard plugin's README.) That is not a reversal of
  this non-goal: they are Claude Code plugin hooks the operator installs
  separately, each with a kill switch. The MCP server itself still never blocks
  and never corrects. Keep it that way — enforcement belongs in a plugin.
```

- [ ] **Step 4: Update `marketplace.json`**

Bump `version` to `0.9.0`, extend the description, and add both entries following the existing shape (`name`, `description`, `version: "0.1.0"`, `source: "./plugin/anti-tangent-<x>"`, `category: "productivity"`, `homepage`).

- [ ] **Step 5: Verify and commit**

```bash
jq -e '.version == "0.9.0"
  and (.plugins | length == 4)
  and ([.plugins[].name] | index("anti-tangent-shunt") != null and index("anti-tangent-guard") != null)' \
  .claude-plugin/marketplace.json && echo "marketplace ok"
git grep -nE 'seven tools|seven registered|three handlers' -- README.md CLAUDE.md docs/protocol internal examples plugin && echo "STALE COUNT REMAINS" || echo "counts updated"
bash scripts/check-protocol-docs.sh
git add README.md CLAUDE.md .claude-plugin/marketplace.json
git commit -m "docs: document the I/O tools, both plugins, and the write trust model"
```

```json:metadata
{"files": ["README.md", "CLAUDE.md", ".claude-plugin/marketplace.json"], "verifyCommand": "jq -e '.plugins | length == 4' .claude-plugin/marketplace.json", "acceptanceCriteria": ["README documents both tools with full field lists", "README documents the worker vars and notes SHUNT_MIN_LINES is hook-only", "README filesystem section covers writes and the Windows gap", "README gains Acknowledgements", "CLAUDE.md says nine tools and records the plugin-vs-server blocking distinction", "marketplace.json lists four plugins at version 0.9.0", "no stale catalog-count claim on the live surface (docs/superpowers historical records excluded)"], "modelTier": "mechanical"}
```

---

### Task 16: Reproduce the benchmarks

**Goal:** An honest savings table for this stack, published in the shunt plugin README.

**Files:**
- Modify: `plugin/anti-tangent-shunt/README.md`
- Create: `plugin/anti-tangent-shunt/evals/benchmarks.md`
- Create: `plugin/anti-tangent-shunt/evals/benchmarks.tsv`
- Create: `plugin/anti-tangent-shunt/evals/check-benchmark-table.sh`
- Create: `plugin/anti-tangent-shunt/evals/bench.sh` (the checked-in measurement harness)
- Modify: `.github/workflows/ci.yml` (run the checker in `hook-evals`)

**Acceptance Criteria:**
- [ ] All four upstream scenarios reproduced against a **Go** corpus of ~160K lines
- [ ] The corpus repo, commit SHA, and the exact files per scenario are pinned in `benchmarks.md`
- [ ] Reports implementer-context tokens without vs. with delegation, and the savings ratio
- [ ] Worker consumption is reported **separately and never netted off**
- [ ] Scenario 4 reports lines written, with no savings ratio (different unit — as upstream does)
- [ ] The README table is labelled as ours, next to upstream's for comparison
- [ ] The method is written down well enough for someone else to re-run it
- [ ] The **exact prompt** for every scenario is fixed BEFORE measuring and reproduced verbatim in `benchmarks.md` — answer size, and therefore the savings figure, depends materially on the question asked
- [ ] Corpus eligibility is objective (see Step 1), not "boring enough" — and package count is measured with `go list`, not by counting unique directories
- [ ] The corpus licence file and its SPDX identifier are recorded in `benchmarks.md`, with OSI-approved status confirmed rather than assumed
- [ ] Upstream's comparison figures are cited to the pinned source `spotify/portal-ai-plugins@3c24ca30ff63e1f5bbad1c43fe5324daff579123` `plugins/shunt/README.md`, which is where they were read — the accompanying blog post is unreachable from this environment and must not be cited as if read
- [ ] The verify command compares the README table **row by row** against `benchmarks.tsv`, joining on `scenario_id` (never on prose) and comparing every cell — not merely that each number appears somewhere
- [ ] The checker runs in CI (`hook-evals`) and in the whole-plan gate, so later documentation drift is caught
- [ ] The corpus file set comes from a command that actually applies the exclusion rules (vendored and generated files pruned), and every scenario file is drawn from it

**Verify:** `bash plugin/anti-tangent-shunt/evals/check-benchmark-table.sh` → exits 0, confirming every README table row matches its `benchmarks.tsv` row cell for cell

**Steps:**

- [ ] **Step 1: Pick and pin the corpus**

Objective eligibility — every one of these, no judgement calls:

- **120,000–200,000 lines** of non-generated, non-vendored Go (`*.go` excluding `*_test.go`, `vendor/`, and files whose first line matches `^// Code generated`)
- **Public** repository under an OSI-approved licence
- **At least 20 packages**, so the multi-file cross-package scenario is real rather than synthetic
- **Builds with `go build ./...`** at the chosen SHA, so the files are known-coherent
- **Not** anti-tangent-mcp, and not a repo whose code appears in this project's own prompts

Clone at a fixed SHA and measure:

```bash
git clone <candidate> /tmp/bench-corpus
git -C /tmp/bench-corpus rev-parse HEAD          # pin THIS sha in benchmarks.md

# The eligible file set, applying every exclusion rule above. A bare
# `find -name '*.go' -not -name '*_test.go'` does NOT implement these rules:
# it counts vendored and generated code, inflating the line total and allowing
# a generated file to be picked as a scenario input.
git -C /tmp/bench-corpus ls-files '*.go' \
  | grep -v '_test\.go$' \
  | grep -Ev '(^|/)vendor/' \
  | while IFS= read -r f; do
      head -1 "/tmp/bench-corpus/$f" | grep -q '^// Code generated' || printf '%s\n' "$f"
    done > /tmp/bench-prod-files.txt

# Scenario 2 needs a source + TEST pair, so build the matching test-file set with
# the SAME vendor/generated exclusions. One set excluding every _test.go while a
# scenario requires a test file is a contradiction, not an oversight.
git -C /tmp/bench-corpus ls-files '*_test.go' \
  | grep -Ev '(^|/)vendor/' \
  | while IFS= read -r f; do
      head -1 "/tmp/bench-corpus/$f" | grep -q '^// Code generated' || printf '%s\n' "$f"
    done > /tmp/bench-test-files.txt

wc -l < /tmp/bench-prod-files.txt                                          # eligible production files
(cd /tmp/bench-corpus && xargs -a /tmp/bench-prod-files.txt cat | wc -l)   # must be 120000-200000
(cd /tmp/bench-corpus && go list ./... | grep -v '/vendor/' | wc -l)   # Go PACKAGES, must be >= 20
ls /tmp/bench-corpus/LICEN[CS]E* /tmp/bench-corpus/COPYING* 2>/dev/null  # record the licence file
# Record its SPDX identifier in benchmarks.md and confirm that identifier is
# listed at https://opensource.org/licenses. "It looked open source" is not a record.
(cd /tmp/bench-corpus && go build ./...) && echo "corpus builds"
```

Scenario inputs come from `/tmp/bench-prod-files.txt`, except scenario 2's test file, which comes from `/tmp/bench-test-files.txt`. A file outside both sets silently violates the eligibility rules. The 120,000–200,000 line total is the **production** set only.

**This repo is deliberately not the corpus** — it is far too small for the multi-file scenario to mean anything. Record the SHA before measuring anything.

- [ ] **Step 2: Select the four scenarios**

Mirror upstream's shape so the tables are comparable: (1) one large file ~4,000 lines; (2) a source + test pair ~7,400 lines combined; (3) 3–4 files across package boundaries ~1,300 lines; (4) a `code_write` generating a test file from a large reference.

**Fix the exact prompt for each before measuring** and record it verbatim in `benchmarks.md`. A vaguer question returns a longer answer and flatters the savings figure, so the prompt is part of the measurement, not an incidental detail. Use these:

1. *"Which exported functions in this file mutate package-level state, and what are their receivers?"*
2. *"Which behaviours does the test file cover that the source file's exported API exposes, and which exported functions have no test?"*
3. *"Which functions cross package boundaries between these files, and in which direction does each call flow?"*
4. `code_write` spec: *"A table-driven test for the exported functions in the reference file, covering the zero value, one ordinary case, and one error case per function."*

Re-run each scenario **three times** and report the median. A single sample of a non-deterministic model is an anecdote, not a benchmark; state the sample count in the table caption.

- [ ] **Step 3: Measure "without"**

Tokens the file(s) would occupy in the implementer's context. Use the same tokenizer for both sides; if none is available, state that a `bytes/4` approximation was used and label the table accordingly. **An approximation that is disclosed is fine; an undisclosed one is not.**

**Scenario 4's `without` is different and must be stated explicitly:** an implementer doing it themselves would have to read the reference file AND emit the code, so `without` = tokens(reference file) + tokens(generated code). That is how upstream reports it ("40,614 tokens + generation"), and it is the only definition under which the comparison means anything. Its `with` side is `lines_written`, in `lines`, with no savings ratio.

**The TSV's `without_tokens` cell for scenario 4 is the computed SUM**, not the
reference-file component alone. `benchmarks.md` records both components
separately so the sum can be checked; the README shows the sum. Upstream
displays it as "40,614 + generation", which is the same quantity written as an
unresolved expression — we resolve it.

- [ ] **Step 4: Measure "with"**

Scenarios 1–3 run through `bulk_read`; **scenario 4 runs through `code_write`**, which is what it measures. For 1–3 record `input_tokens`, `output_tokens`, `review_ms` and the token size of the returned `answer`. For 4 record `input_tokens`, `output_tokens`, `review_ms` and `lines_written` — there is no `answer` and no savings ratio, because generated lines and context tokens are different units. **`answer` is the "with" figure** — that is what actually lands in the implementer's context. `input_tokens` is the worker's consumption and belongs in its own column.

**Harness — do not improvise it.** Drive the tools through the built server over
MCP stdio, exactly as a host would:

```bash
export ANTHROPIC_API_KEY=...                                            # or the provider you pin
export ANTI_TANGENT_WORKER_MODEL=anthropic:claude-haiku-4-5-20251001    # record this exact id
export ANTI_TANGENT_PLAN_ROOTS=/tmp/bench-corpus
export ANTI_TANGENT_STATS_DIR=/tmp/bench-stats                          # per-call token rows, free
go build -o /tmp/atm ./cmd/anti-tangent-mcp
```

Write the harness as `plugin/anti-tangent-shunt/evals/bench.sh` and check it in
— "do not improvise" is meaningless if the procedure lives only in one person's
shell history. It must: start `/tmp/atm` over stdio, complete the MCP
initialize handshake, issue one `tools/call` per scenario with the exact
payload recorded in `benchmarks.md`, capture the full response, and for
scenario 4 delete the target between runs so all three runs write rather than
hitting the overwrite refusal. Emit one TSV row per run to stdout.

Per run record: the returned `answer` (1–3) or `lines_written` (4), plus
`input_tokens`, `output_tokens` and `review_ms` from the response. Token counts
for the *without* side and for the returned answer use ONE tokenizer, named in
`benchmarks.md`. Median = the middle of the three recorded values, computed per
column, with all three raw runs listed in `benchmarks.md`.

- [ ] **Step 5: Write `benchmarks.md`**

Write TWO files.

`plugin/anti-tangent-shunt/evals/benchmarks.tsv` — machine-readable, one row per scenario, tab-separated, header first:

```
scenario_id	label	lines	without_tokens	with_value	with_unit	savings_pct	worker_in	worker_out	runs	statistic
single_large_file	Single large file	4014	33684	5737	tokens	82	31200	640	3	median
code_write	Code-write	3667	40614	833	lines		5100	2900	3	median
```

`plugin/anti-tangent-shunt/evals/benchmarks.md` — the human record: corpus repo + SHA, per-scenario file lists with line counts, the **verbatim prompt** for each scenario, the worker model id, the tokenizer (or the disclosed approximation), all three runs per scenario, and the commands to re-run.

`with_unit` is what lets scenario 4 report lines without pretending they are tokens; its `savings_pct` is deliberately EMPTY, and the checker must enforce that rather than compute a ratio across units.

`scenario_id` is the canonical key. The README table carries the same id in its first column (with the human `label` beside it), so the checker matches on the id and never on prose. Ids must be unique; the checker fails if any id appears twice, or appears in one file but not the other. **The README table's first column is `scenario_id`**, with the human label beside it — the checker joins on the id alone and treats the label as an ordinary cell that must also match.

The TSV exists so the README table can be checked mechanically. Comparing rendered prose against rendered prose is how the two drift apart.

- [ ] **Step 6: Publish the table**

Replace the README's Benchmarks placeholder:

```markdown
## Benchmarks

Upstream reports these figures against a 162K-line **Java** monorepo, delegating
through Portal/AiKA (read from `spotify/portal-ai-plugins@3c24ca30ff63e1f5bbad1c43fe5324daff579123`,
`plugins/shunt/README.md`):

| Scenario | Lines | Without | With | Savings |
|---|---|---|---|---|
| Single large file | 4,014 | 33,684 | 5,737 | 82% |
| Source + test pair | 7,408 | 75,990 | 4,148 | 94% |
| Multi-file cross-service | 1,281 | 16,221 | 821 | 94% |
| Code-write | 3,667 | 40,614 + generation | 833 lines to disk | — |

Ours, against `<repo>@<sha>` (~<N>K lines of **Go**), delegating through
`<worker model>`:

| scenario_id | Scenario | Lines | Without | With | Unit | Savings | Worker in | Worker out | Runs | Stat |
|---|---|---|---|---|---|---|---|---|---|---|
| single_large_file | Single large file | 4,014 | 33,684 | 5,737 | tokens | 82% | 31,200 | 640 | 3 | median |
| code_write | Code-write | 3,667 | 44,281 | 833 | lines | — | 5,100 | 2,900 | 3 | median |

The README header is **one column per TSV column, in TSV order**, so the checker
is a straight cell-by-cell comparison with no display transformation to encode.
Numbers may carry thousands separators and `savings_pct` renders with a `%`;
the checker normalises both before comparing. An empty `savings_pct` renders as
`—`.

"Without" is what a direct `Read` would put in the implementer's context;
"With" is the returned answer. **Worker tokens are shown separately and are not
netted off** — the claim is about implementer context, and combining the two
would overstate it. Method and per-file selections: [`evals/benchmarks.md`](evals/benchmarks.md).
```

- [ ] **Step 6b: Make the table checkable**

Write `plugin/anti-tangent-shunt/evals/check-benchmark-table.sh`. It must compare **row by row against `benchmarks.tsv`**: parse the README's "ours" table, join each row to its TSV row **on `scenario_id`** (never on prose), and compare every cell — label included. Exit non-zero naming the first mismatched scenario and column.

A "does this number appear anywhere in the file" check passes when two scenarios share a number, or when a stale figure survives in unrelated prose — exactly the drift this exists to catch. It must also fail when the README has a scenario the TSV lacks, or vice versa, so a dropped row cannot pass silently.

- [ ] **Step 6c: Wire the check into CI and the whole-plan gate**

A checker nothing runs is not a check. Add to the `hook-evals` job in `.github/workflows/ci.yml`, after the two eval suites:

```yaml
      - name: Benchmark table consistency
        run: bash plugin/anti-tangent-shunt/evals/check-benchmark-table.sh
```

It needs no keys and no network — it reads two files in the repo. Add the same line to the whole-plan Verification block at the end of this plan.

- [ ] **Step 7: Commit**

```bash
git add plugin/anti-tangent-shunt/README.md plugin/anti-tangent-shunt/evals/benchmarks.md plugin/anti-tangent-shunt/evals/benchmarks.tsv plugin/anti-tangent-shunt/evals/check-benchmark-table.sh .github/workflows/ci.yml
git commit -m "docs(shunt): reproduce the four savings scenarios on a Go corpus"
```

```json:metadata
{"files": ["plugin/anti-tangent-shunt/README.md", "plugin/anti-tangent-shunt/evals/benchmarks.md", "plugin/anti-tangent-shunt/evals/benchmarks.tsv", "plugin/anti-tangent-shunt/evals/check-benchmark-table.sh", ".github/workflows/ci.yml"], "verifyCommand": "bash plugin/anti-tangent-shunt/evals/check-benchmark-table.sh", "acceptanceCriteria": ["all four scenarios reproduced on a ~160K-line Go corpus", "repo, SHA and per-scenario files pinned", "reports without/with implementer tokens and the ratio", "worker consumption separate and not netted off", "scenario 4 reports lines with no ratio", "our table labelled and shown beside upstream's", "method reproducible by someone else"], "modelTier": "standard"}
```

---

## Verification

Whole-plan gate, run before opening the PR:

```bash
go vet ./... && go build ./... && go test -race -count=1 ./...
bash scripts/check-protocol-docs.sh
diff -r docs/protocol plugin/anti-tangent-protocol/protocol
for f in docs/protocol/*.md; do b=$(wc -c < "$f"); echo "$f=$b"; [ "$b" -ge 16000 ] && exit 1; done
[ "$(wc -c < INTEGRATION.md)" -lt 2000 ] || exit 1
bash plugin/anti-tangent-shunt/evals/run.sh
bash plugin/anti-tangent-guard/evals/run.sh
bash plugin/anti-tangent-shunt/evals/check-benchmark-table.sh
jq -e '.version == "0.9.0" and (.plugins | length == 4)' .claude-plugin/marketplace.json
! git grep -nE 'seven tools|seven registered|three handlers' -- README.md CLAUDE.md docs/protocol internal examples plugin
git diff --name-only origin/main -- VERSION | grep -q . && echo "VERSION MUST NOT CHANGE" && exit 1
grep -q '^## \[0.18.0\]' CHANGELOG.md
```
