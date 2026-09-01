# `validate_plan` `context_paths` + order-aware Create/Modify check — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a caller attach source files to `validate_plan` so the reviewer verifies codebase claims instead of emitting `unverifiable_codebase_claim` for each one, and add a deterministic order-aware check that a task's `Modify:` targets can exist when the task runs.

**Architecture:** Attached files are resolved through the existing `resolveFileInput` (same `ANTI_TANGENT_PLAN_ROOTS` sandbox as `plan_path`), rendered into the plan prompt ahead of the plan itself so they land inside the existing single cache prefix, and governed by their own byte caps. The reviewer's ground rules — today duplicated across three plan templates — are extracted to one shared partial and rewritten to enumerate what is attached and restate that everything else stays black-box. A plan claim an attached file refutes becomes a new hard-severity `contradicted_codebase_claim`. Separately, a reviewer-free check parses `**Files:**` bullets and flags `Modify:` targets that cannot exist yet.

**Tech Stack:** Go 1.x, `text/template` + `embed`, `testify`, `go test -race`. No new dependencies.

**Spec:** `docs/superpowers/specs/2026-09-01-context-paths-design.md`

## Global Constraints

- **`-race` always on.** `go test -race ./...` is the mainline command. No test may touch the network; HTTP-shaped tests use `httptest.Server`.
- **Golden files are not regenerated casually.** A no-attachment render MUST stay byte-identical to today. If a golden diff appears on an existing `plan_basic` / `plan_findings_only` / `plan_tasks_chunk` case, that is a **bug in the change**, not a golden to update. Fix the template with `{{-` / `-}}` trim markers. Run `-update` only for the genuinely new `*_with_context_files` goldens.
- **Protocol docs are CI-enforced.** Each `docs/protocol/*.md` part must stay under **16,000 bytes** (`core.md` is at 13,023 — ~3 KB headroom), `INTEGRATION.md` under 2,000, and `plugin/anti-tangent-protocol/protocol/` must be byte-identical to `docs/protocol/`. Resync in the same commit as any protocol edit.
- **Section numbers §1, §3.x, §4.x, §5.x, §6 in the protocol parts are cited externally — never renumber.**
- **Do not modify `VERSION`.** The release workflow bumps it; pre-bumping breaks the release workflow's changelog validation.
- **`CHANGELOG.md` gets its `## [0.17.0] - 2026-09-01` entry as the code is written, not at the end.** CI enforces that the `version/0.17.0` branch name matches a changelog entry.
- **Backward compatibility is absolute.** A `validate_plan` call supplying neither `context_paths` nor `repo_root` must behave byte-identically to 0.16.0 — same prompts, same goldens, same envelopes.
- **Names fixed across tasks** (use these exactly): `contextFile`, `resolveContextPaths`, `resolveDirInput`, `contextTooLargeError`, `maxContextFiles`, `Config.ContextMaxFileBytes`, `Config.ContextMaxPayloadBytes`, `prompts.ContextFile`, `PlanInput.ContextFiles`, `PlanChunkInput.ContextFiles`, `planparser.TaskFileRefs`, `planparser.FileRefs`, `checkFileConsistency`, `verdict.CategoryContradictedCodebaseClaim`, `planSummaryMeta.ContextFiles`.

**User decisions (already made):**
- Both the `context_paths` feature and the Create/Modify check ship in 0.17.0 together.
- A false claim about an attached file gets a **new** `contradicted_codebase_claim` category with no severity floor — not a reuse of `attestation_contradiction`, not a continued `unverifiable_codebase_claim`.
- **Whole files only.** Oversized attachments are refused, never truncated and never line-ranged.
- **One cache breakpoint.** `providers.Request.CachePrefix` stays a single string; the two-breakpoint files/plan split is deferred to its own issue.

---

### Task 1: Config — attachment byte caps

**Goal:** Add the two attachment byte caps to `Config` with env-var parsing that matches the existing pattern.

**Files:**
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Acceptance Criteria:**
- [ ] `Config.ContextMaxFileBytes` defaults to 131072 and `Config.ContextMaxPayloadBytes` defaults to 524288.
- [ ] `ANTI_TANGENT_CONTEXT_MAX_FILE_BYTES` and `ANTI_TANGENT_CONTEXT_MAX_PAYLOAD_BYTES` override them.
- [ ] A non-integer or non-positive value for either returns an error naming that env var.
- [ ] No other config default changes.

**Verify:** `go test -race ./internal/config/...` → PASS

**Steps:**

- [ ] **Step 1: Write the failing tests**

Append to `internal/config/config_test.go`:

```go
func TestLoad_ContextCaps_Defaults(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "k")
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, 131072, cfg.ContextMaxFileBytes)
	assert.Equal(t, 524288, cfg.ContextMaxPayloadBytes)
}

func TestLoad_ContextCaps_Overrides(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "k")
	t.Setenv("ANTI_TANGENT_CONTEXT_MAX_FILE_BYTES", "4096")
	t.Setenv("ANTI_TANGENT_CONTEXT_MAX_PAYLOAD_BYTES", "8192")
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, 4096, cfg.ContextMaxFileBytes)
	assert.Equal(t, 8192, cfg.ContextMaxPayloadBytes)
}

func TestLoad_ContextCaps_Invalid(t *testing.T) {
	for _, tc := range []struct{ env, val string }{
		{"ANTI_TANGENT_CONTEXT_MAX_FILE_BYTES", "nope"},
		{"ANTI_TANGENT_CONTEXT_MAX_FILE_BYTES", "0"},
		{"ANTI_TANGENT_CONTEXT_MAX_PAYLOAD_BYTES", "-1"},
	} {
		t.Run(tc.env+"="+tc.val, func(t *testing.T) {
			t.Setenv("ANTHROPIC_API_KEY", "k")
			t.Setenv(tc.env, tc.val)
			_, err := Load()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.env)
		})
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test -race ./internal/config/ -run TestLoad_ContextCaps -v`
Expected: FAIL — `cfg.ContextMaxFileBytes` undefined (compile error).

- [ ] **Step 3: Add the struct fields**

In `internal/config/config.go`, immediately after the `PlanRoots []string` field:

```go
	// ContextMaxFileBytes caps each individual file attached to
	// validate_plan via context_paths. ContextMaxPayloadBytes caps the
	// attached set as a whole. Both are separate from PlanMaxPayloadBytes so
	// an oversized attachment set is refused with its own actionable
	// message rather than being blamed on the plan. Oversized attachments
	// are REFUSED, never truncated: the reviewer is told attached files are
	// complete, and a silently-shortened file turns that promise into a
	// source of false contradicted_codebase_claim findings. See design §3.2.
	ContextMaxFileBytes    int
	ContextMaxPayloadBytes int
```

- [ ] **Step 4: Add the defaults**

In the `cfg := Config{...}` literal, after `PlanMaxPayloadBytes: 1048576,`:

```go
		ContextMaxFileBytes:    131072,
		ContextMaxPayloadBytes: 524288,
```

- [ ] **Step 5: Add the env parsing**

Immediately after the existing `ANTI_TANGENT_PLAN_MAX_PAYLOAD_BYTES` block:

```go
	if v := env("ANTI_TANGENT_CONTEXT_MAX_FILE_BYTES"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("ANTI_TANGENT_CONTEXT_MAX_FILE_BYTES: %w", err)
		}
		if n <= 0 {
			return Config{}, fmt.Errorf("ANTI_TANGENT_CONTEXT_MAX_FILE_BYTES: must be positive, got %d", n)
		}
		cfg.ContextMaxFileBytes = n
	}
	if v := env("ANTI_TANGENT_CONTEXT_MAX_PAYLOAD_BYTES"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("ANTI_TANGENT_CONTEXT_MAX_PAYLOAD_BYTES: %w", err)
		}
		if n <= 0 {
			return Config{}, fmt.Errorf("ANTI_TANGENT_CONTEXT_MAX_PAYLOAD_BYTES: must be positive, got %d", n)
		}
		cfg.ContextMaxPayloadBytes = n
	}
```

- [ ] **Step 6: Run to verify pass**

Run: `go test -race ./internal/config/... -v`
Expected: PASS

- [ ] **Step 7: Add the CHANGELOG entry**

Insert directly below the `## [0.16.0] - 2026-08-31` heading's preceding blank line — i.e. as the new top entry in `CHANGELOG.md`, after the intro paragraph:

```markdown
## [0.17.0] - 2026-09-01

### Added
- **`ANTI_TANGENT_CONTEXT_MAX_FILE_BYTES`** (default 131072) and
  **`ANTI_TANGENT_CONTEXT_MAX_PAYLOAD_BYTES`** (default 524288) — per-file and whole-set byte
  caps for `validate_plan`'s new `context_paths` attachments. Separate from the plan cap so an
  oversized attachment set is refused with its own actionable message.
```

- [ ] **Step 8: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go CHANGELOG.md
git commit -m "feat(config): attachment byte caps for validate_plan context_paths"
```

```json:metadata
{"files": ["internal/config/config.go", "internal/config/config_test.go", "CHANGELOG.md"], "verifyCommand": "go test -race ./internal/config/...", "acceptanceCriteria": ["ContextMaxFileBytes defaults to 131072 and ContextMaxPayloadBytes to 524288", "Both env vars override the defaults", "Non-integer or non-positive values error naming the env var", "No other config default changes"], "modelTier": "mechanical"}
```

---

### Task 2: `contradicted_codebase_claim` category

**Goal:** Add the new finding category to the verdict type, the validator, and the three plan schemas, with no severity floor.

**Files:**
- Modify: `internal/verdict/verdict.go`
- Modify: `internal/verdict/parser.go:57-70` (`validCategory`)
- Modify: `internal/verdict/plan_schema.json`
- Modify: `internal/verdict/plan_findings_only_schema.json`
- Modify: `internal/verdict/tasks_only_schema.json`
- Test: `internal/verdict/parser_test.go`

**Acceptance Criteria:**
- [ ] `verdict.CategoryContradictedCodebaseClaim` exists with value `"contradicted_codebase_claim"`.
- [ ] `validCategory` accepts it.
- [ ] `applySeverityFloor` leaves a `major` or `critical` one **unchanged** (contrast: `unverifiable_codebase_claim` is floored to minor).
- [ ] The category appears in the `category` enum of `plan_schema.json`, `plan_findings_only_schema.json`, and `tasks_only_schema.json`, and in **no other** schema.
- [ ] All three edited JSON files remain valid JSON.

**Verify:** `go test -race ./internal/verdict/...` → PASS

**Steps:**

- [ ] **Step 1: Write the failing tests**

Append to `internal/verdict/parser_test.go`:

```go
func TestContradictedCodebaseClaim_IsValidCategory(t *testing.T) {
	assert.True(t, validCategory(CategoryContradictedCodebaseClaim))
}

// The whole point of the category: an attached file is ground truth read from
// disk, so a contradiction keeps the reviewer's severity. Contrast with
// unverifiable_codebase_claim, which is floored to minor because the reviewer
// cannot check the claim at all.
func TestContradictedCodebaseClaim_NotSeverityFloored(t *testing.T) {
	f := applySeverityFloor(Finding{
		Severity: SeverityMajor,
		Category: CategoryContradictedCodebaseClaim,
	})
	assert.Equal(t, SeverityMajor, f.Severity)

	floored := applySeverityFloor(Finding{
		Severity: SeverityMajor,
		Category: CategoryUnverifiableCodebaseClaim,
	})
	assert.Equal(t, SeverityMinor, floored.Severity, "control: unverifiable IS floored")
}

func TestContradictedCodebaseClaim_InPlanSchemasOnly(t *testing.T) {
	const cat = `"contradicted_codebase_claim"`
	for _, s := range [][]byte{PlanSchema(), PlanFindingsOnlySchema(), TasksOnlySchema()} {
		assert.Contains(t, string(s), cat)
	}
	for _, s := range [][]byte{Schema(), PrimeSchema(), ExtractSchema()} {
		assert.NotContains(t, string(s), cat,
			"the category is validate_plan-only; context_paths exists on no other tool")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test -race ./internal/verdict/ -run TestContradictedCodebaseClaim -v`
Expected: FAIL — `CategoryContradictedCodebaseClaim` undefined (compile error).

- [ ] **Step 3: Add the constant**

In `internal/verdict/verdict.go`, immediately after the `CategoryAttestationContradiction` block:

```go
	// CategoryContradictedCodebaseClaim is emitted by the reviewer when a
	// plan statement is refuted by the contents of a file supplied via
	// validate_plan's context_paths. Distinct from
	// unverifiable_codebase_claim: an attached file is ground truth read
	// from disk by the server, so a contradiction is a hard finding, not
	// "can't verify." Distinct from attestation_contradiction, which is a
	// conflict with a caller-ASSERTED harness shape rather than with bytes
	// the server read. Intentionally NOT in applySeverityFloor's list — the
	// reviewer's chosen severity (typically major) is preserved. Only valid
	// for claims about files that were actually attached; the ground rules
	// forbid emitting it about anything outside the attached set.
	CategoryContradictedCodebaseClaim Category = "contradicted_codebase_claim"
```

- [ ] **Step 4: Extend `validCategory`**

In `internal/verdict/parser.go`, change the `case` list so the line reading

```go
		CategoryConventionDeviation, CategoryAttestationContradiction,
```

becomes

```go
		CategoryConventionDeviation, CategoryAttestationContradiction,
		CategoryContradictedCodebaseClaim,
```

- [ ] **Step 5: Extend the three plan schemas**

In each of `internal/verdict/plan_schema.json`, `internal/verdict/plan_findings_only_schema.json`, and `internal/verdict/tasks_only_schema.json`, in the `category` enum, add a line immediately after `"attestation_contradiction",`:

```json
            "contradicted_codebase_claim",
```

Match the surrounding indentation of each file exactly. Do **not** touch `schema.json`, `prime_schema.json`, or `extract_schema.json`.

- [ ] **Step 6: Verify the JSON is still valid**

Run:
```bash
for f in internal/verdict/plan_schema.json internal/verdict/plan_findings_only_schema.json internal/verdict/tasks_only_schema.json; do
  python3 -c "import json,sys; json.load(open(sys.argv[1])); print('ok', sys.argv[1])" "$f"
done
```
Expected: three `ok` lines.

- [ ] **Step 7: Run to verify pass**

Run: `go test -race ./internal/verdict/... -v`
Expected: PASS

- [ ] **Step 8: Commit**

```bash
git add internal/verdict/
git commit -m "feat(verdict): add contradicted_codebase_claim category, no severity floor"
```

```json:metadata
{"files": ["internal/verdict/verdict.go", "internal/verdict/parser.go", "internal/verdict/plan_schema.json", "internal/verdict/plan_findings_only_schema.json", "internal/verdict/tasks_only_schema.json", "internal/verdict/parser_test.go"], "verifyCommand": "go test -race ./internal/verdict/...", "acceptanceCriteria": ["CategoryContradictedCodebaseClaim exists with the right string value", "validCategory accepts it", "applySeverityFloor leaves major/critical unchanged", "Present in the three plan schema enums and no others", "All edited JSON files remain valid"], "modelTier": "mechanical"}
```

---

### Task 3: `context_paths` resolution with caps

**Goal:** Turn a `[]string` of absolute paths into resolved, de-duplicated, cap-checked file contents — or a typed refusal — reusing the existing `resolveFileInput`.

**Files:**
- Create: `internal/mcpsrv/context_files.go`
- Create: `internal/mcpsrv/context_files_test.go`

**Acceptance Criteria:**
- [ ] `resolveContextPaths` returns `[]contextFile` in caller-supplied order with total bytes.
- [ ] A list longer than `maxContextFiles` (50) is refused **before any file is opened**.
- [ ] A single file over `cfg.ContextMaxFileBytes` returns a `*contextTooLargeError` naming that path.
- [ ] A set exceeding `cfg.ContextMaxPayloadBytes` returns a `*contextTooLargeError` with `Path == ""` and the running total.
- [ ] Paths resolving to the same file (e.g. via symlink) are de-duplicated, first occurrence kept, counted once.
- [ ] A missing / relative / non-regular path, or one outside `ANTI_TANGENT_PLAN_ROOTS`, returns a plain error (not `*contextTooLargeError`).
- [ ] `resolveDirInput` resolves an absolute directory subject to roots and rejects a regular file.

**Verify:** `go test -race ./internal/mcpsrv/ -run 'TestResolveContextPaths|TestResolveDirInput' -v` → PASS

**Steps:**

- [ ] **Step 1: Write the failing tests**

Create `internal/mcpsrv/context_files_test.go`:

```go
package mcpsrv

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/config"
)

func ctxCfg(fileCap, setCap int, roots []string) config.Config {
	return config.Config{
		ContextMaxFileBytes:    fileCap,
		ContextMaxPayloadBytes: setCap,
		PlanRoots:              roots,
	}
}

func writeTemp(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(p, []byte(content), 0o600))
	return p
}

func TestResolveContextPaths_HappyPath(t *testing.T) {
	dir := t.TempDir()
	a := writeTemp(t, dir, "a.go", "package a\n")
	b := writeTemp(t, dir, "b.go", "package b\n")

	files, total, err := resolveContextPaths([]string{a, b}, ctxCfg(1000, 10000, nil))
	require.NoError(t, err)
	require.Len(t, files, 2)
	assert.Equal(t, "package a\n", files[0].Content)
	assert.Equal(t, "package b\n", files[1].Content)
	assert.Equal(t, len("package a\n")+len("package b\n"), total)
	assert.NotEmpty(t, files[0].Source.SHA256)
}

func TestResolveContextPaths_CountCapRefusesBeforeAnyRead(t *testing.T) {
	// A path that does not exist: if the count cap is checked first (as it
	// must be), we never try to open it and the error is about the count.
	paths := make([]string, maxContextFiles+1)
	for i := range paths {
		paths[i] = "/nonexistent/does-not-exist.go"
	}
	_, _, err := resolveContextPaths(paths, ctxCfg(1000, 10000, nil))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "51")
	assert.Contains(t, err.Error(), "50")
	assert.NotContains(t, err.Error(), "does-not-exist",
		"count cap must be enforced before any path is opened")
}

func TestResolveContextPaths_PerFileCap(t *testing.T) {
	dir := t.TempDir()
	big := writeTemp(t, dir, "big.go", strings.Repeat("x", 300))

	_, _, err := resolveContextPaths([]string{big}, ctxCfg(100, 10000, nil))
	var tle *contextTooLargeError
	require.True(t, errors.As(err, &tle))
	assert.Equal(t, 100, tle.Limit)
	assert.Contains(t, tle.Path, "big.go")
	assert.Contains(t, tle.Error(), "ANTI_TANGENT_CONTEXT_MAX_FILE_BYTES")
}

func TestResolveContextPaths_SetCap(t *testing.T) {
	dir := t.TempDir()
	a := writeTemp(t, dir, "a.go", strings.Repeat("a", 60))
	b := writeTemp(t, dir, "b.go", strings.Repeat("b", 60))

	_, _, err := resolveContextPaths([]string{a, b}, ctxCfg(100, 100, nil))
	var tle *contextTooLargeError
	require.True(t, errors.As(err, &tle))
	assert.Equal(t, "", tle.Path, "set-level breach names no single file")
	assert.Equal(t, 120, tle.Bytes)
	assert.Equal(t, 100, tle.Limit)
	assert.Contains(t, tle.Error(), "ANTI_TANGENT_CONTEXT_MAX_PAYLOAD_BYTES")
}

func TestResolveContextPaths_DeduplicatesAfterSymlinkResolution(t *testing.T) {
	dir := t.TempDir()
	a := writeTemp(t, dir, "a.go", strings.Repeat("a", 60))
	link := filepath.Join(dir, "link.go")
	require.NoError(t, os.Symlink(a, link))

	// Set cap of 100 would be breached if the 60-byte file were counted twice.
	files, total, err := resolveContextPaths([]string{a, link}, ctxCfg(100, 100, nil))
	require.NoError(t, err)
	require.Len(t, files, 1)
	assert.Equal(t, 60, total)
}

func TestResolveContextPaths_BadPathsAreOrdinaryErrors(t *testing.T) {
	dir := t.TempDir()
	for name, p := range map[string]string{
		"missing":  filepath.Join(dir, "nope.go"),
		"relative": "internal/config/config.go",
		"dir":      dir,
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := resolveContextPaths([]string{p}, ctxCfg(1000, 10000, nil))
			require.Error(t, err)
			var tle *contextTooLargeError
			assert.False(t, errors.As(err, &tle),
				"bad input is a transport error, not a too-large envelope")
		})
	}
}

func TestResolveContextPaths_OutsideRoots(t *testing.T) {
	dir := t.TempDir()
	other := t.TempDir()
	a := writeTemp(t, other, "a.go", "package a\n")

	_, _, err := resolveContextPaths([]string{a}, ctxCfg(1000, 10000, []string{dir}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ANTI_TANGENT_PLAN_ROOTS")
}

func TestResolveContextPaths_EmptyInput(t *testing.T) {
	files, total, err := resolveContextPaths(nil, ctxCfg(1000, 10000, nil))
	require.NoError(t, err)
	assert.Empty(t, files)
	assert.Equal(t, 0, total)
}

func TestResolveDirInput(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "a.go", "package a\n")

	got, err := resolveDirInput(dir, nil)
	require.NoError(t, err)
	assert.Equal(t, mustEval(t, dir), got)

	_, err = resolveDirInput(f, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a directory")

	_, err = resolveDirInput("relative/path", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "absolute")
}

func mustEval(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	require.NoError(t, err)
	return r
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test -race ./internal/mcpsrv/ -run 'TestResolveContextPaths|TestResolveDirInput' -v`
Expected: FAIL — `resolveContextPaths` undefined (compile error).

- [ ] **Step 3: Write the implementation**

Create `internal/mcpsrv/context_files.go`:

```go
// Package mcpsrv: resolution of validate_plan's context_paths attachments.
// Scope: filesystem reads and cap enforcement only; no provider calls, no
// rendering. See design §3.
package mcpsrv

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/patiently/anti-tangent-mcp/internal/config"
)

// maxContextFiles bounds how many files one validate_plan call may attach.
// A fixed constant rather than an env var, mirroring the existing 50-entry
// cap on pinned_by: it exists to stop a degenerate list from producing an
// unreadable prompt, not to be tuned. The byte caps in config are the dials.
const maxContextFiles = 50

// contextFile is one resolved attachment. Source carries the symlink-resolved
// path, byte count, and sha256 actually read — so the prompt, the summary
// provenance, and the plan cache key all describe the same bytes.
type contextFile struct {
	Source  fileSource
	Content string
}

// contextTooLargeError signals a cap breach. Path names the offending file
// for a per-file breach and is empty for a set-level breach, so the caller
// can build a message that says which dial to turn. Distinct from the plain
// errors returned for bad paths, because a cap breach maps to the
// too-large ENVELOPE while a bad path maps to a transport error (§3.3).
type contextTooLargeError struct {
	Path  string
	Bytes int
	Limit int
}

func (e *contextTooLargeError) Error() string {
	if e.Path != "" {
		return fmt.Sprintf(
			"context_paths: %q is %d bytes > per-file cap %d (raise ANTI_TANGENT_CONTEXT_MAX_FILE_BYTES, or drop the file)",
			e.Path, e.Bytes, e.Limit)
	}
	return fmt.Sprintf(
		"context_paths: attached set is %d bytes > cap %d (raise ANTI_TANGENT_CONTEXT_MAX_PAYLOAD_BYTES, or attach fewer files)",
		e.Bytes, e.Limit)
}

// resolveContextPaths reads every path subject to cfg's roots and caps.
//
// Order is load-bearing. The count cap is checked FIRST, before any path is
// opened, so a degenerate list costs one comparison rather than 500 syscalls.
// Then each file is resolved through resolveFileInput — which owns the
// symlink, roots, O_NOFOLLOW, regular-file, and capped-read guarantees — and
// only afterwards is the running total compared against the set cap, so the
// error can report the true accumulated size.
//
// De-duplication happens on the SYMLINK-RESOLVED path, so two arguments
// naming the same file through different routes are billed and rendered once.
// The first occurrence wins, preserving caller-supplied ordering.
func resolveContextPaths(paths []string, cfg config.Config) ([]contextFile, int, error) {
	if len(paths) == 0 {
		return nil, 0, nil
	}
	if len(paths) > maxContextFiles {
		return nil, 0, fmt.Errorf(
			"context_paths: %d entries > cap %d (attach only the files the plan makes claims about)",
			len(paths), maxContextFiles)
	}

	out := make([]contextFile, 0, len(paths))
	seen := make(map[string]bool, len(paths))
	total := 0

	for _, p := range paths {
		content, src, err := resolveFileInput(p, cfg.PlanRoots, cfg.ContextMaxFileBytes)
		if errors.Is(err, errTooLarge) {
			return nil, 0, &contextTooLargeError{
				Path:  src.Path,
				Bytes: src.Bytes,
				Limit: cfg.ContextMaxFileBytes,
			}
		}
		if err != nil {
			return nil, 0, fmt.Errorf("context_paths: %w", err)
		}
		if seen[src.Path] {
			continue
		}
		seen[src.Path] = true
		total += src.Bytes
		if total > cfg.ContextMaxPayloadBytes {
			return nil, 0, &contextTooLargeError{
				Bytes: total,
				Limit: cfg.ContextMaxPayloadBytes,
			}
		}
		out = append(out, contextFile{Source: src, Content: content})
	}
	return out, total, nil
}

// resolveDirInput is the directory-shaped sibling of resolveFileInput, used
// for repo_root. Same absolute-path requirement, same symlink resolution
// before the roots check, but it stats for a directory and reads nothing.
func resolveDirInput(path string, roots []string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("repo_root must be absolute, got %q", path)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolve repo_root %q: %w", path, err)
	}
	if !withinRoots(resolved, roots) {
		return "", fmt.Errorf("repo_root %q is outside ANTI_TANGENT_PLAN_ROOTS", resolved)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("stat repo_root %q: %w", resolved, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("repo_root %q is not a directory", resolved)
	}
	return resolved, nil
}
```

- [ ] **Step 4: Run to verify pass**

Run: `go test -race ./internal/mcpsrv/ -run 'TestResolveContextPaths|TestResolveDirInput' -v`
Expected: PASS (9 tests)

- [ ] **Step 5: Run the whole package for regressions**

Run: `go test -race ./internal/mcpsrv/...`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/mcpsrv/context_files.go internal/mcpsrv/context_files_test.go
git commit -m "feat(mcpsrv): resolve context_paths attachments with per-file and set caps"
```

```json:metadata
{"files": ["internal/mcpsrv/context_files.go", "internal/mcpsrv/context_files_test.go"], "verifyCommand": "go test -race ./internal/mcpsrv/ -run 'TestResolveContextPaths|TestResolveDirInput' -v", "acceptanceCriteria": ["resolveContextPaths returns files in order with total bytes", "Count cap refuses before any file is opened", "Per-file breach returns contextTooLargeError naming the path", "Set breach returns contextTooLargeError with empty Path and the running total", "Symlink-equal paths de-duplicated and counted once", "Bad paths return plain errors, not contextTooLargeError", "resolveDirInput accepts a directory and rejects a regular file"], "modelTier": "standard"}
```

---

### Task 4: Extract the shared plan ground-rules partial (no behavior change)

**Goal:** Move the ground-rules text — currently duplicated verbatim in three plan templates — into one shared partial, with the existing golden files proving nothing changed.

**Files:**
- Create: `internal/prompts/templates/plan_rules.tmpl`
- Modify: `internal/prompts/templates/plan.tmpl:1-16`
- Modify: `internal/prompts/templates/plan_findings_only.tmpl:1-16`
- Modify: `internal/prompts/templates/plan_tasks_chunk.tmpl:1-16`
- Modify: `internal/prompts/prompts.go` (`render`)
- Test: `internal/prompts/prompts_test.go` (existing goldens are the test)

**Acceptance Criteria:**
- [ ] The ground-rules text exists in exactly one file, `plan_rules.tmpl`.
- [ ] All three plan templates include it via `{{template "plan_rules" .}}`.
- [ ] **Every existing golden file passes unchanged** — no `-update` run, no golden diff.
- [ ] `render` parses the whole `templates/*.tmpl` set and executes the named template.

**Verify:** `go test -race ./internal/prompts/...` → PASS with zero golden diffs

**Steps:**

- [ ] **Step 1: Confirm the three copies are byte-identical before moving them**

Run:
```bash
for f in plan plan_findings_only plan_tasks_chunk; do
  sed -n '1,16p' internal/prompts/templates/$f.tmpl | sha256sum
done
```
Expected: three identical hashes. If they differ, stop and diff them — the partial must reproduce whichever text the goldens were generated from, and a pre-existing divergence changes the task.

- [ ] **Step 2: Create the partial by extracting the bytes, not retyping them**

The rules block is lines 1–16 of the current `plan.tmpl` — from `## Reviewer ground rules` through the `unverifiable_codebase_claim` paragraph, stopping before the `{{if .ProjectKnowledge -}}` block that emits the Project-knowledge *section*. Build the partial mechanically:

```bash
{ printf '{{define "plan_rules"}}'
  sed -n '1,16p' internal/prompts/templates/plan.tmpl
  printf '{{end}}\n'
} > internal/prompts/templates/plan_rules.tmpl
```

Then confirm the boundary is right — the last captured line must be the `unverifiable_codebase_claim` paragraph, and the `{{if .ProjectKnowledge -}}` line must NOT be in the file:

```bash
tail -3 internal/prompts/templates/plan_rules.tmpl
grep -c 'if .ProjectKnowledge -}}' internal/prompts/templates/plan_rules.tmpl   # must print 0
```

Extract, never retype. A single changed character shows up as a golden diff in Step 5, which is exactly why this refactor happens before Task 5 adds new text.

- [ ] **Step 3: Replace the three copies**

In each of `plan.tmpl`, `plan_findings_only.tmpl`, and `plan_tasks_chunk.tmpl`, delete lines 1–16 and put in their place the single line:

```gotemplate
{{template "plan_rules" .}}
```

- [ ] **Step 4: Teach `render` to parse the full set**

In `internal/prompts/prompts.go`, replace the body of `render`:

```go
// render parses the whole embedded template set and executes the named one.
//
// The full set (rather than just the named file) is parsed because the plan
// templates share the "plan_rules" partial — the reviewer ground rules were
// previously duplicated verbatim across all three, which is how a posture
// edit drifts. Parsing eight small templates costs microseconds and always
// precedes an HTTP call to a reviewer LLM, so the waste is unmeasurable next
// to what it prevents.
func render(name string, data any) (string, error) {
	tmpl, err := template.ParseFS(templatesFS, "templates/*.tmpl")
	if err != nil {
		return "", fmt.Errorf("parse templates: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		return "", fmt.Errorf("execute %s: %w", name, err)
	}
	return buf.String(), nil
}
```

- [ ] **Step 5: Run the golden tests — expect ZERO diffs**

Run: `go test -race ./internal/prompts/... -v`
Expected: PASS.

**If any golden fails, the diff is whitespace.** Fix it with template trim markers (`{{define "plan_rules" -}}`, `{{- end}}`, or `{{template "plan_rules" .}}` placement), **not** by running `-update`. The whole value of this task is that the goldens are untouched.

- [ ] **Step 6: Verify the text really is de-duplicated**

Run: `grep -rc "Treat such identifiers as black-box references" internal/prompts/templates/`
Expected: `plan_rules.tmpl:1` and `0` for every other file.

- [ ] **Step 7: Commit**

```bash
git add internal/prompts/
git commit -m "refactor(prompts): extract the triplicated plan ground rules into one partial"
```

```json:metadata
{"files": ["internal/prompts/templates/plan_rules.tmpl", "internal/prompts/templates/plan.tmpl", "internal/prompts/templates/plan_findings_only.tmpl", "internal/prompts/templates/plan_tasks_chunk.tmpl", "internal/prompts/prompts.go"], "verifyCommand": "go test -race ./internal/prompts/...", "acceptanceCriteria": ["Ground-rules text lives in exactly one file", "All three plan templates include it via the partial", "Every existing golden passes unchanged with no -update run", "render parses the whole templates/*.tmpl set"], "modelTier": "standard"}
```

---

### Task 5: Render attached files and rewrite the reviewer posture

**Goal:** Render an attachment section into the plan prompts and rewrite the ground rules so the reviewer knows exactly what it can and cannot conclude from the attached set.

**Files:**
- Modify: `internal/prompts/prompts.go` (`PlanInput`, `PlanChunkInput`, new `ContextFile`)
- Modify: `internal/prompts/templates/plan_rules.tmpl`
- Modify: `internal/prompts/templates/plan.tmpl`
- Modify: `internal/prompts/templates/plan_findings_only.tmpl`
- Modify: `internal/prompts/templates/plan_tasks_chunk.tmpl`
- Test: `internal/prompts/prompts_test.go`
- Test: `internal/prompts/testdata/plan_basic_with_context_files.golden` (new)

**Acceptance Criteria:**
- [ ] `prompts.ContextFile{Path, Bytes, SHA256Short, Content}` exists; `PlanInput` and `PlanChunkInput` each carry `ContextFiles []ContextFile`.
- [ ] With no context files, all existing goldens still pass **unchanged**.
- [ ] With context files, the prompt contains a `## Attached source files` section using `--- BEGIN FILE: … ---` / `--- END FILE: … ---` delimiters and **no code fence**.
- [ ] The ground rules enumerate each attached path, state that attached files are complete, state that absence from the attached set is not evidence of absence from the codebase, scope `unverifiable_codebase_claim` to unattached code, and scope `contradicted_codebase_claim` to attached files only.
- [ ] The attachment section renders **before** `## Plan under review`.
- [ ] `splitPlanPrompt` still finds the real `## What to evaluate` when an attached file contains that exact line at the start of a line.
- [ ] A file whose content contains backticks and a ``` fence renders intact.

**Verify:** `go test -race ./internal/prompts/...` → PASS

**Steps:**

- [ ] **Step 1: Write the failing tests**

Append to `internal/prompts/prompts_test.go`:

```go
func ctxFiles() []ContextFile {
	return []ContextFile{
		{Path: "/repo/internal/config/config.go", Bytes: 42, SHA256Short: "9f2ab41c", Content: "package config\n\ntype Config struct{ PlanRoots []string }\n"},
		{Path: "/repo/internal/verdict/verdict.go", Bytes: 21, SHA256Short: "3c1af09b", Content: "package verdict\n"},
	}
}

func TestRenderPlan_WithContextFiles_Golden(t *testing.T) {
	out, err := RenderPlan(PlanInput{
		PlanText:     "# Plan\n\n### Task 1: t1\n\n**Goal:** g1\n",
		ContextFiles: ctxFiles(),
	})
	require.NoError(t, err)
	golden(t, "plan_basic_with_context_files", out.System+"\n---USER---\n"+out.User)
}

func TestRenderPlan_WithContextFiles_Structure(t *testing.T) {
	out, err := RenderPlan(PlanInput{PlanText: "# Plan\n", ContextFiles: ctxFiles()})
	require.NoError(t, err)

	assert.Contains(t, out.User, "## Attached source files")
	assert.Contains(t, out.User, "--- BEGIN FILE: /repo/internal/config/config.go (42 bytes, sha256 9f2ab41c…) ---")
	assert.Contains(t, out.User, "--- END FILE: /repo/internal/config/config.go ---")

	// Posture: both directions of the guard must be present.
	assert.Contains(t, out.User, "absence from the attached set is NOT evidence")
	assert.Contains(t, out.User, "contradicted_codebase_claim")

	// Each attached path is enumerated in the rules, not just summarized.
	assert.Contains(t, out.User, "/repo/internal/verdict/verdict.go")

	// Ordering: attachments precede the plan.
	assert.Less(t, strings.Index(out.User, "## Attached source files"),
		strings.Index(out.User, "## Plan under review"))
}

func TestRenderPlan_WithoutContextFiles_OmitsSection(t *testing.T) {
	out, err := RenderPlan(PlanInput{PlanText: "# Plan\n"})
	require.NoError(t, err)
	assert.NotContains(t, out.User, "## Attached source files")
	assert.NotContains(t, out.User, "contradicted_codebase_claim")
	assert.Contains(t, out.User, "You have access ONLY to the plan markdown")
}

// An attached file that itself contains a line-anchored "## What to evaluate"
// must not shrink the cacheable prefix: attachments render before the real
// heading, so LastIndex still lands on the template's own.
func TestRenderPlanTasksChunk_AttachedFileWithEvaluateHeading(t *testing.T) {
	out, err := RenderPlanTasksChunk(PlanChunkInput{
		PlanText:   "# Plan\n\n### Task 1: t1\n",
		ChunkTasks: []planparser.RawTask{{Title: "Task 1: t1", Body: "### Task 1: t1\n"}},
		ContextFiles: []ContextFile{{
			Path: "/repo/doc.md", Bytes: 30, SHA256Short: "deadbeef",
			Content: "## What to evaluate\n\nnot the real one\n",
		}},
	})
	require.NoError(t, err)
	require.NotEmpty(t, out.UserPrefix)
	assert.True(t, strings.HasPrefix(out.UserSuffix, "## What to evaluate"))
	assert.Contains(t, out.UserPrefix, "not the real one",
		"the decoy heading stays inside the cacheable prefix")
	assert.Equal(t, 1, strings.Count(out.UserSuffix, "## What to evaluate"),
		"the suffix starts at the template's own heading, not the decoy")
}

// Attached content is delimited, never fenced — Go raw strings contain
// backticks and would break any fence length we picked.
func TestRenderPlan_AttachedFileWithBackticksAndFence(t *testing.T) {
	content := "const q = `SELECT 1`\n\n```go\nfmt.Println(\"x\")\n```\n"
	out, err := RenderPlan(PlanInput{
		PlanText:     "# Plan\n",
		ContextFiles: []ContextFile{{Path: "/repo/q.go", Bytes: 50, SHA256Short: "aaaabbbb", Content: content}},
	})
	require.NoError(t, err)
	assert.Contains(t, out.User, content, "content must survive verbatim")
}
```

Add `"strings"` and the `planparser` import to the test file if not already present.

- [ ] **Step 2: Run to verify failure**

Run: `go test -race ./internal/prompts/ -run 'ContextFiles|AttachedFile' -v`
Expected: FAIL — `ContextFile` undefined (compile error).

- [ ] **Step 3: Add the prompts-side type and fields**

In `internal/prompts/prompts.go`, after the existing `File` type:

```go
// ContextFile is one source file attached to validate_plan via context_paths.
// Path is the symlink-resolved path actually read, and SHA256Short is the
// first 8 hex digits — the same provenance the summary block shows — so the
// reviewer can cite a file by the identity the human will see. Content is the
// COMPLETE file: the ground rules promise the reviewer that absence within an
// attached file is evidence, which is only true if nothing was truncated.
type ContextFile struct {
	Path        string
	Bytes       int
	SHA256Short string
	Content     string
}
```

Add `ContextFiles []ContextFile` to both `PlanInput` and `PlanChunkInput`.

- [ ] **Step 4: Rewrite the ground-rules partial**

In `internal/prompts/templates/plan_rules.tmpl`, wrap the existing black-box opening in a `{{if not .ContextFiles}}` guard and add the attached-set posture. The rules block becomes:

```gotemplate
{{define "plan_rules"}}## Reviewer ground rules
{{if .ContextFiles -}}
You have been given the plan markdown below AND the COMPLETE contents of these {{len .ContextFiles}} source files, read from disk by the server:
{{range .ContextFiles}}
- `{{.Path}}`
{{- end}}

For those files, and only those files, absence IS evidence: if a symbol is not present in an attached file, it is not in that file, and you may say so.

Every other file, path, or symbol referenced by the plan remains a black-box identifier reference — you do not know its definition, signature, return type, or behavior. **The caller attached only what they judged relevant, so absence from the attached set is NOT evidence of absence from the codebase.** The overwhelming majority of this repository is not in front of you.

Emit `unverifiable_codebase_claim` only for claims about code that is NOT attached. Do not emit it for a claim the attached files settle.

When a plan claim is refuted by an attached file, emit `category: contradicted_codebase_claim` instead. Quote the attached file's path and the specific symbol or line in `evidence`. Severity is yours to choose (`major` is typical). **Never emit `contradicted_codebase_claim` about a file, symbol, or path that is not in the attached list above** — you cannot refute what you cannot see.
{{- else -}}
You have access ONLY to the plan markdown rendered below. You have NOT been given the codebase. Any function name, file path, variable, struct field, environment variable, or other symbol that appears in the plan is an identifier reference — you do not know its definition, signature, return type, or behavior. Treat such identifiers as black-box references.
{{- end}}
{{end}}
```

The `{{end}}` on the snippet's last line is the partial's existing closing `{{end}}` — the snippet shows the TOP of the file, not the whole file.

You are inserting a conditional around one paragraph, not rewriting the file. The **only** existing line you touch is the black-box paragraph beginning `You have access ONLY to the plan markdown rendered below.`, which moves inside the `{{- else -}}` arm verbatim. Everything after the new conditional — the `{{if .ProjectKnowledge}}` paragraphs, the `unstated_assumption` paragraph, the evidence paragraph, the `e.g. illustrative` paragraph, and the closing `unverifiable_codebase_claim` paragraph — stays byte-for-byte where Task 4 put it.

Verify you moved exactly one paragraph and added the new arm:

```bash
grep -c 'You have access ONLY to the plan markdown' internal/prompts/templates/plan_rules.tmpl   # 1
grep -c 'absence from the attached set is NOT evidence' internal/prompts/templates/plan_rules.tmpl # 1
grep -c 'e.g. illustrative' internal/prompts/templates/plan_rules.tmpl                            # 1
```

- [ ] **Step 5: Add the attachment section to all three plan templates**

In each of `plan.tmpl`, `plan_findings_only.tmpl`, and `plan_tasks_chunk.tmpl`, insert between the Project-knowledge block and `## Plan under review`:

```gotemplate
{{if .ContextFiles -}}
## Attached source files (complete contents, read from disk by the server)

{{range .ContextFiles}}--- BEGIN FILE: {{.Path}} ({{.Bytes}} bytes, sha256 {{.SHA256Short}}…) ---
{{.Content}}
--- END FILE: {{.Path}} ---

{{end}}
{{end -}}
## Plan under review
```

Delimiters, not fences: attached Go source contains backticks, so any fence length can be broken by a legitimate attachment.

- [ ] **Step 6: Generate ONLY the new golden**

Run: `go test ./internal/prompts/ -run TestRenderPlan_WithContextFiles_Golden -update`
Then: `git status --short internal/prompts/testdata/`
Expected: exactly one new file, `plan_basic_with_context_files.golden`. **If any pre-existing golden shows as modified, revert it and fix the template whitespace** — a no-attachment render must not change.

```bash
git diff --stat internal/prompts/testdata/   # must be empty for pre-existing goldens
```

- [ ] **Step 7: Read the new golden**

Run: `cat internal/prompts/testdata/plan_basic_with_context_files.golden`
Confirm by eye: the enumerated path list, both delimiter lines per file, the attachment section before `## Plan under review`, and both halves of the absence guard.

- [ ] **Step 8: Run the full package**

Run: `go test -race ./internal/prompts/... -v`
Expected: PASS

- [ ] **Step 9: Commit**

```bash
git add internal/prompts/
git commit -m "feat(prompts): render attached context files and rewrite the reviewer posture"
```

```json:metadata
{"files": ["internal/prompts/prompts.go", "internal/prompts/templates/plan_rules.tmpl", "internal/prompts/templates/plan.tmpl", "internal/prompts/templates/plan_findings_only.tmpl", "internal/prompts/templates/plan_tasks_chunk.tmpl", "internal/prompts/prompts_test.go", "internal/prompts/testdata/plan_basic_with_context_files.golden"], "verifyCommand": "go test -race ./internal/prompts/...", "acceptanceCriteria": ["ContextFile type and ContextFiles fields on both plan inputs", "Existing goldens unchanged with no context files", "Attachment section uses BEGIN/END FILE delimiters and no code fence", "Ground rules enumerate paths and carry both halves of the absence guard", "Attachments render before the plan", "A decoy '## What to evaluate' in an attached file does not shrink the cacheable prefix", "Backticks and fences in attached content survive verbatim"], "modelTier": "standard"}
```

---

### Task 6: Wire `context_paths` into `ValidatePlan`

**Goal:** Accept `context_paths` on the tool, resolve it, thread it into rendering, refuse cap breaches with the too-large envelope, and count its bytes in stats.

**Files:**
- Modify: `internal/mcpsrv/handlers.go` (`ValidatePlanArgs`, `validatePlanTool`, `ValidatePlan`, `tooLargePlanResult`, `renderPlanReviewInputs`, `renderPlanReview`)
- Test: `internal/mcpsrv/handlers_plan_test.go`

**Acceptance Criteria:**
- [ ] `ValidatePlanArgs` carries `ContextPaths []string` and `RepoRoot string`.
- [ ] Resolved attachments reach both the single-call and chunked prompts.
- [ ] A cap breach returns the too-large **envelope** (not a transport error) naming the context bytes; a bad path returns a **transport error**.
- [ ] `recordStat`'s `payloadBytes` includes the attached bytes.
- [ ] On the chunked path the attachment lands in `CachePrefix` (byte-identical across chunks) and not in the per-call suffix.
- [ ] A call with no `context_paths` produces the identical reviewer prompt it did before this task.

**Verify:** `go test -race ./internal/mcpsrv/...` → PASS

**Steps:**

- [ ] **Step 1: Write the failing tests**

Append to `internal/mcpsrv/handlers_plan_test.go`:

```go
func TestValidatePlan_ContextPaths_ReachThePrompt(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "config.go")
	require.NoError(t, os.WriteFile(f, []byte("package config\n// SENTINEL_ATTACHED\n"), 0o600))

	sr := &scriptedReviewer{responses: []providers.Response{singlePlanResp(t, 1)}}
	d := newDepsWithScripted(t, sr, 8)
	h := &handlers{deps: d}

	_, _, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{
		PlanText:     buildPlanWithNTasks(1),
		ContextPaths: []string{f},
	})
	require.NoError(t, err)
	require.NotEmpty(t, sr.requests)
	assert.Contains(t, sr.requests[0].User, "SENTINEL_ATTACHED")
	assert.Contains(t, sr.requests[0].User, "## Attached source files")
}

func TestValidatePlan_ContextPaths_TooLargeIsAnEnvelope(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "big.go")
	require.NoError(t, os.WriteFile(f, []byte(strings.Repeat("x", 500)), 0o600))

	sr := &scriptedReviewer{}
	d := newDepsWithScripted(t, sr, 8)
	d.Cfg.ContextMaxFileBytes = 100
	h := &handlers{deps: d}

	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{
		PlanText:     buildPlanWithNTasks(1),
		ContextPaths: []string{f},
	})
	require.NoError(t, err, "a cap breach is an envelope, not a transport error")
	assert.Equal(t, verdict.VerdictFail, pr.PlanVerdict)
	require.NotEmpty(t, pr.PlanFindings)
	assert.Equal(t, verdict.CategoryTooLarge, pr.PlanFindings[0].Category)
	assert.Contains(t, pr.PlanFindings[0].Evidence, "ANTI_TANGENT_CONTEXT_MAX_FILE_BYTES")
	assert.Zero(t, sr.calls, "no reviewer call is made on a refused payload")
}

func TestValidatePlan_ContextPaths_BadPathIsTransportError(t *testing.T) {
	sr := &scriptedReviewer{}
	d := newDepsWithScripted(t, sr, 8)
	h := &handlers{deps: d}

	_, _, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{
		PlanText:     buildPlanWithNTasks(1),
		ContextPaths: []string{"/nonexistent/nope.go"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "context_paths")
	assert.Zero(t, sr.calls)
}

// Spec §11: the attachment block must land inside the CACHEABLE prefix on
// the chunked path, not in the per-call suffix — that placement is the whole
// reason attachments render before the plan.
func TestValidatePlan_ContextPaths_LandInTheCachePrefix(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "config.go")
	require.NoError(t, os.WriteFile(f, []byte("package config\n// SENTINEL_ATTACHED\n"), 0o600))

	sr := &scriptedReviewer{
		responses: []providers.Response{
			passOneResp(),
			chunkResp(t, titlesRange(1, 8)),
			chunkResp(t, titlesRange(9, 9)),
		},
	}
	d := newDepsWithScripted(t, sr, 8)
	h := &handlers{deps: d}

	_, _, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{
		PlanText:     buildPlanWithNTasks(9),
		ContextPaths: []string{f},
	})
	require.NoError(t, err)
	require.Len(t, sr.requests, 3)

	// Pass 1 carries no breakpoint (its tools block differs), so the
	// attachment is in its plain User body.
	assert.Empty(t, sr.requests[0].CachePrefix)
	assert.Contains(t, sr.requests[0].User, "SENTINEL_ATTACHED")

	// Both chunk calls carry the attachment in the SHARED prefix, and their
	// per-call suffixes must not repeat it.
	for i := 1; i < 3; i++ {
		assert.Contains(t, sr.requests[i].CachePrefix, "SENTINEL_ATTACHED", "chunk %d prefix", i)
		assert.NotContains(t, sr.requests[i].User, "SENTINEL_ATTACHED", "chunk %d suffix", i)
	}
	assert.Equal(t, sr.requests[1].CachePrefix, sr.requests[2].CachePrefix,
		"the prefix must be byte-identical across chunks or nothing is ever cache-read")
}

func TestValidatePlan_NoContextPaths_PromptUnchanged(t *testing.T) {
	sr := &scriptedReviewer{responses: []providers.Response{singlePlanResp(t, 1)}}
	d := newDepsWithScripted(t, sr, 8)
	h := &handlers{deps: d}

	_, _, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{
		PlanText: buildPlanWithNTasks(1),
	})
	require.NoError(t, err)
	assert.NotContains(t, sr.requests[0].User, "## Attached source files")
	assert.Contains(t, sr.requests[0].User, "You have access ONLY to the plan markdown")
}
```

If `scriptedReviewer` does not already record `requests`, add a `requests []providers.Request` field to it in `internal/mcpsrv/handlers_helpers_test.go` and append `req` at the top of its `Review` method. If a `singlePlanResp` helper does not exist, add one alongside `passOneResp` returning a valid single-call `PlanResult` JSON with N tasks.

- [ ] **Step 2: Run to verify failure**

Run: `go test -race ./internal/mcpsrv/ -run TestValidatePlan_ContextPaths -v`
Expected: FAIL — unknown field `ContextPaths` (compile error).

- [ ] **Step 3: Add the args and update the tool description**

In `internal/mcpsrv/handlers.go`, add to `ValidatePlanArgs`:

```go
	ContextPaths []string `json:"context_paths,omitempty"`
	RepoRoot     string   `json:"repo_root,omitempty"`
```

Append to `validatePlanTool`'s `Description`:

```go
			"Optionally pass context_paths: ABSOLUTE paths to source files the plan makes claims about. " +
			"The server reads them whole and the reviewer verifies claims against them instead of emitting unverifiable_codebase_claim. " +
			"Attach only files the plan actually touches — this is by far the most expensive input the tool takes. " +
			"Optionally pass repo_root (absolute) to enable the disk tier of the Create/Modify consistency check. "
```

- [ ] **Step 4: Widen `tooLargePlanResult`**

Change the signature and the evidence string:

```go
func tooLargePlanResult(total, planBytes, pkBytes, contextBytes, limit int) verdict.PlanResult {
	return verdict.PlanResult{
		PlanVerdict: verdict.VerdictFail,
		PlanFindings: []verdict.Finding{{
			Severity:   verdict.SeverityCritical,
			Category:   verdict.CategoryTooLarge,
			Criterion:  "payload",
			Evidence:   fmt.Sprintf("payload %d bytes > cap %d (plan: %d, project_knowledge: %d, context_paths: %d)", total, limit, planBytes, pkBytes, contextBytes),
			Suggestion: "Split the plan into smaller chunks or pass a unified diff.",
		}},
		Tasks:      []verdict.PlanTaskResult{},
		NextAction: "Reduce plan size and retry.",
	}
}
```

Update its two existing call sites in `ValidatePlan` to pass `0` for `contextBytes`.

- [ ] **Step 5: Add a dedicated envelope for a context cap breach**

Add next to `tooLargePlanResult`:

```go
// contextTooLargePlanResult renders a context_paths cap breach as the same
// too-large envelope shape as an oversized plan, but with the attachment's
// own message — which names the offending file (or the running total) and
// the env var that governs it. A cap breach is content-too-large, so it is
// an envelope; a bad path is bad input, so it stays a transport error.
// See design §3.3.
func contextTooLargePlanResult(err *contextTooLargeError) verdict.PlanResult {
	return verdict.PlanResult{
		PlanVerdict: verdict.VerdictFail,
		PlanFindings: []verdict.Finding{{
			Severity:   verdict.SeverityCritical,
			Category:   verdict.CategoryTooLarge,
			Criterion:  "context_paths",
			Evidence:   err.Error(),
			Suggestion: "Attach fewer or smaller files, or raise the named cap.",
		}},
		Tasks:      []verdict.PlanTaskResult{},
		NextAction: "Reduce the attached file set and retry.",
	}
}
```

- [ ] **Step 6: Resolve attachments in `ValidatePlan`**

Immediately after the existing `plan_path` resolution block (after the `if args.PlanPath != "" { ... }` block, before `planBytes := len(planText)`):

```go
	contextFiles, contextBytes, cerr := resolveContextPaths(args.ContextPaths, h.deps.Cfg)
	if cerr != nil {
		var tle *contextTooLargeError
		if errors.As(cerr, &tle) {
			pr := prependPlanClamp(contextTooLargePlanResult(tle), clamp)
			h.recordStat(statParams{
				tool:         "validate_plan",
				verdict:      string(pr.PlanVerdict),
				findings:     planFindings(pr),
				modelUsed:    h.deps.Cfg.PlanModel.String(),
				payloadBytes: len(planText) + pkBytes,
			})
			return planEnvelopeResult(pr, planSummaryMeta{ModelUsed: h.deps.Cfg.PlanModel.String(), Source: planSrc.String()})
		}
		return nil, verdict.PlanResult{}, cerr
	}
	var repoRoot string
	if args.RepoRoot != "" {
		var rerr error
		repoRoot, rerr = resolveDirInput(args.RepoRoot, h.deps.Cfg.PlanRoots)
		if rerr != nil {
			return nil, verdict.PlanResult{}, rerr
		}
	}
	_ = repoRoot // consumed by the Create/Modify check in Task 9
```

- [ ] **Step 7: Include context bytes in the payload total and stats**

Change the plan-cap check to include the attachment bytes, and pass them to `tooLargePlanResult`:

```go
	planBytes := len(planText)
	if total := planBytes + pkBytes; total > h.deps.Cfg.PlanMaxPayloadBytes {
		pr := prependPlanDeprecation(
			prependPlanClamp(tooLargePlanResult(total, planBytes, pkBytes, contextBytes, h.deps.Cfg.PlanMaxPayloadBytes), clamp),
			args.PlanText != "")
```

The plan cap deliberately still measures `planBytes + pkBytes` only — attachments have their own budget (§8.2), so an oversized attachment must not be reported as an oversized plan.

Then, in **every** `h.recordStat(statParams{...})` call inside `ValidatePlan` that currently passes `payloadBytes: planBytes + pkBytes`, change it to `payloadBytes: planBytes + pkBytes + contextBytes`.

- [ ] **Step 8: Thread attachments into rendering**

Add to `renderPlanReviewInputs`:

```go
	ContextFiles []prompts.ContextFile
```

In `renderPlanReview`, pass `ContextFiles: in.ContextFiles` into all three of `prompts.RenderPlan`, `prompts.RenderPlanFindingsOnly`, and `prompts.RenderPlanTasksChunk`.

Add a converter next to `toPromptFiles`:

```go
// toPromptContextFiles converts resolved attachments into the prompts
// package's render-facing shape. The short hash is the same 8-digit prefix
// the summary block shows, so a reviewer citing a file and a human reading
// the summary are looking at the same identity.
func toPromptContextFiles(files []contextFile) []prompts.ContextFile {
	out := make([]prompts.ContextFile, 0, len(files))
	for _, f := range files {
		short := f.Source.SHA256
		if len(short) > 8 {
			short = short[:8]
		}
		out = append(out, prompts.ContextFile{
			Path:        f.Source.Path,
			Bytes:       f.Source.Bytes,
			SHA256Short: short,
			Content:     f.Content,
		})
	}
	return out
}
```

And at the `renderPlanReview` call site in `ValidatePlan`, add `ContextFiles: toPromptContextFiles(contextFiles),`.

- [ ] **Step 9: Run to verify pass**

Run: `go test -race ./internal/mcpsrv/ -run TestValidatePlan -v`
Expected: PASS

- [ ] **Step 10: Full suite**

Run: `go build ./... && go test -race ./...`
Expected: PASS

- [ ] **Step 11: Commit**

```bash
git add internal/mcpsrv/handlers.go internal/mcpsrv/handlers_plan_test.go internal/mcpsrv/handlers_helpers_test.go
git commit -m "feat(mcpsrv): accept context_paths on validate_plan and render attachments"
```

```json:metadata
{"files": ["internal/mcpsrv/handlers.go", "internal/mcpsrv/handlers_plan_test.go", "internal/mcpsrv/handlers_helpers_test.go"], "verifyCommand": "go test -race ./internal/mcpsrv/...", "acceptanceCriteria": ["ValidatePlanArgs carries ContextPaths and RepoRoot", "Attachments reach both single-call and chunked prompts", "Cap breach returns the too-large envelope naming the env var", "Bad path returns a transport error with no reviewer call", "recordStat payloadBytes includes attached bytes", "Chunked path carries the attachment in CachePrefix, identical across chunks, absent from the suffix", "A call with no context_paths produces the prior prompt"], "modelTier": "standard"}
```

---

### Task 7: Cache key and summary provenance

**Goal:** Make the plan cache key depend on attached file content, and show the attached set in the summary block.

**Files:**
- Modify: `internal/mcpsrv/plan_cache.go` (`planPassCacheKey`)
- Modify: `internal/mcpsrv/summary.go` (`planSummaryMeta`, `formatPlanSummary`)
- Modify: `internal/mcpsrv/handlers.go` (call sites)
- Test: `internal/mcpsrv/handlers_plan_test.go`, `internal/mcpsrv/summary_test.go`

**Acceptance Criteria:**
- [ ] Two calls with identical plans but different attached-file **content** produce different cache keys.
- [ ] Two calls with identical plans and identical attachments produce the same key.
- [ ] The summary block lists the attached files with byte counts when any are attached, and shows no `context:` line when none are.
- [ ] The attached list is carried per-call in `planSummaryMeta`, never stored on the cache entry.

**Verify:** `go test -race ./internal/mcpsrv/ -run 'CacheKey|Summary' -v` → PASS

**Steps:**

- [ ] **Step 1: Write the failing tests**

Append to `internal/mcpsrv/handlers_plan_test.go`:

```go
// A stale review after an attached file is edited is the failure this guards:
// planPassCache keys on plan content, so without the attachment hashes a
// caller who fixes the file a finding complained about and re-validates
// immediately gets the pre-fix review back with no indication of reuse.
func TestPlanPassCacheKey_VariesWithAttachedContent(t *testing.T) {
	rendered := renderedPlanReview{}
	mk := func(sha string) [32]byte {
		return planPassCacheKey("plan", "", "", "m", 100, 0, rendered,
			[]contextFile{{Source: fileSource{Path: "/a.go", Bytes: 10, SHA256: sha}}})
	}
	assert.NotEqual(t, mk("aaaa"), mk("bbbb"))
	assert.Equal(t, mk("aaaa"), mk("aaaa"))
}

func TestPlanPassCacheKey_NoAttachmentsIsStable(t *testing.T) {
	rendered := renderedPlanReview{}
	a := planPassCacheKey("plan", "", "", "m", 100, 0, rendered, nil)
	b := planPassCacheKey("plan", "", "", "m", 100, 0, rendered, nil)
	assert.Equal(t, a, b)
}
```

Append to `internal/mcpsrv/summary_test.go`:

```go
func TestFormatPlanSummary_ContextFiles(t *testing.T) {
	pr := verdict.PlanResult{PlanVerdict: verdict.VerdictPass, PlanQuality: verdict.PlanQualityActionable}
	out := formatPlanSummary(pr, planSummaryMeta{
		ModelUsed: "anthropic:m",
		ContextFiles: []fileSource{
			{Path: "/repo/a.go", Bytes: 1204, SHA256: "9f2ab41c00"},
			{Path: "/repo/b.go", Bytes: 22, SHA256: "3c1af09b00"},
		},
	})
	assert.Contains(t, out, "context:       2 files, 1226 B")
	assert.Contains(t, out, "/repo/a.go")
	assert.Contains(t, out, "/repo/b.go")
}

func TestFormatPlanSummary_NoContextFilesOmitsLine(t *testing.T) {
	pr := verdict.PlanResult{PlanVerdict: verdict.VerdictPass, PlanQuality: verdict.PlanQualityActionable}
	out := formatPlanSummary(pr, planSummaryMeta{ModelUsed: "anthropic:m"})
	assert.NotContains(t, out, "context:")
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test -race ./internal/mcpsrv/ -run 'CacheKey|Summary_Context|Summary_NoContext' -v`
Expected: FAIL — wrong argument count to `planPassCacheKey` (compile error).

- [ ] **Step 3: Extend the cache key**

In `internal/mcpsrv/plan_cache.go`, bump the version constant and add the parameter:

```go
	planPassCacheVersion = "plan-pass-cache-v3"
```

```go
// contextFileKey is the cache-key projection of one attachment: identity and
// content hash, never the bytes. The key is itself hashed, so carrying
// hundreds of KB into it would buy nothing.
type contextFileKey struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

func planPassCacheKey(planText, projectKnowledge, mode, model string, maxTokens, maxTokensOverride int, rendered renderedPlanReview, contextFiles []contextFile) [32]byte {
	ctxKeys := make([]contextFileKey, 0, len(contextFiles))
	for _, f := range contextFiles {
		ctxKeys = append(ctxKeys, contextFileKey{Path: f.Source.Path, SHA256: f.Source.SHA256})
	}
	keyInput := struct {
		Version           string            `json:"version"`
		PlanText          string            `json:"plan_text"`
		ProjectKnowledge  string            `json:"project_knowledge"`
		Mode              string            `json:"mode"`
		Model             string            `json:"model"`
		MaxTokens         int               `json:"max_tokens"`
		MaxTokensOverride int               `json:"max_tokens_override"`
		Prompts           []planCachePrompt `json:"prompts"`
		ContextFiles      []contextFileKey  `json:"context_files"`
	}{
		Version:           planPassCacheVersion,
		PlanText:          planText,
		ProjectKnowledge:  projectKnowledge,
		Mode:              mode,
		Model:             model,
		MaxTokens:         maxTokens,
		MaxTokensOverride: maxTokensOverride,
		Prompts:           rendered.cachePrompts(),
		ContextFiles:      ctxKeys,
	}
	keyJSON, _ := json.Marshal(keyInput)
	return sha256.Sum256(keyJSON)
}
```

Update the single call site in `ValidatePlan` to pass `contextFiles`.

- [ ] **Step 4: Extend the summary**

In `internal/mcpsrv/summary.go`, add to `planSummaryMeta`:

```go
	// ContextFiles is the attached set for THIS call. Carried per-call, never
	// stored on the cache entry, for the same reason as Source: planPassCacheKey
	// hashes content, so echoing a stored entry's list would name another
	// caller's files.
	ContextFiles []fileSource
```

In `formatPlanSummary`, immediately after the `meta.Source` block:

```go
	if len(meta.ContextFiles) > 0 {
		totalCtx := 0
		for _, f := range meta.ContextFiles {
			totalCtx += f.Bytes
		}
		fmt.Fprintf(&b, "  context:       %d files, %d B\n", len(meta.ContextFiles), totalCtx)
		for _, f := range meta.ContextFiles {
			fmt.Fprintf(&b, "                 - %s (%d B)\n", f.Path, f.Bytes)
		}
	}
```

- [ ] **Step 5: Populate it at every `planSummaryMeta` construction in `ValidatePlan`**

Add a helper next to `toPromptContextFiles`:

```go
func contextSources(files []contextFile) []fileSource {
	out := make([]fileSource, 0, len(files))
	for _, f := range files {
		out = append(out, f.Source)
	}
	return out
}
```

Add `ContextFiles: contextSources(contextFiles),` to each `planSummaryMeta{...}` literal in `ValidatePlan` that is constructed **after** attachments are resolved. The two early-exit envelopes that run before resolution keep an empty list.

- [ ] **Step 6: Run to verify pass**

Run: `go test -race ./internal/mcpsrv/... -v`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/mcpsrv/
git commit -m "fix(mcpsrv): key the plan cache on attached file content; show attachments in the summary"
```

```json:metadata
{"files": ["internal/mcpsrv/plan_cache.go", "internal/mcpsrv/summary.go", "internal/mcpsrv/handlers.go", "internal/mcpsrv/handlers_plan_test.go", "internal/mcpsrv/summary_test.go"], "verifyCommand": "go test -race ./internal/mcpsrv/ -run 'CacheKey|Summary' -v", "acceptanceCriteria": ["Different attached content yields different cache keys", "Identical inputs yield identical keys", "Summary lists attached files with byte counts", "No context line when nothing is attached", "The list is per-call, never stored on the cache entry"], "modelTier": "mechanical"}
```

---

### Task 8: Parse `**Files:**` bullets per task

**Goal:** Extract each task's `Create:` / `Modify:` / `Delete:` path lists from its markdown body.

**Files:**
- Create: `internal/planparser/filerefs.go`
- Create: `internal/planparser/filerefs_test.go`

**Acceptance Criteria:**
- [ ] `FileRefs(body string) TaskFileRefs` returns the three path lists.
- [ ] Backtick-quoted paths are unquoted; bare paths are taken as the first whitespace-delimited token.
- [ ] A trailing parenthetical such as `(lines 1651-1875)` is stripped.
- [ ] `Create/Modify:` on one bullet contributes to **both** lists.
- [ ] Matching is case-insensitive on the verb and tolerates `-` or `*` bullets.
- [ ] A body with no `**Files:**` section returns three empty lists.
- [ ] Bullets after the `**Files:**` section ends (a blank line followed by a non-bullet line) are not collected.

**Verify:** `go test -race ./internal/planparser/...` → PASS

**Steps:**

- [ ] **Step 1: Write the failing tests**

Create `internal/planparser/filerefs_test.go`:

```go
package planparser

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFileRefs_Basic(t *testing.T) {
	body := "### Task 1: t\n\n**Files:**\n" +
		"- Create: `internal/a/a.go`\n" +
		"- Modify: `internal/b/b.go` (lines 10-20)\n" +
		"- Delete: internal/c/c.go\n\n" +
		"**Acceptance Criteria:**\n- [ ] x\n"

	refs := FileRefs(body)
	assert.Equal(t, []string{"internal/a/a.go"}, refs.Create)
	assert.Equal(t, []string{"internal/b/b.go"}, refs.Modify)
	assert.Equal(t, []string{"internal/c/c.go"}, refs.Delete)
}

func TestFileRefs_CreateOrModifyCountsAsBoth(t *testing.T) {
	refs := FileRefs("**Files:**\n- Create/Modify: `x.go`\n")
	assert.Equal(t, []string{"x.go"}, refs.Create)
	assert.Equal(t, []string{"x.go"}, refs.Modify)
}

func TestFileRefs_CaseAndBulletTolerance(t *testing.T) {
	refs := FileRefs("**Files:**\n* modify: `x.go`\n-   MODIFY:   `y.go`\n")
	assert.Equal(t, []string{"x.go", "y.go"}, refs.Modify)
}

func TestFileRefs_NoFilesSection(t *testing.T) {
	refs := FileRefs("### Task 1: t\n\n**Goal:** g\n\n- Modify: `x.go`\n")
	assert.Empty(t, refs.Create)
	assert.Empty(t, refs.Modify)
	assert.Empty(t, refs.Delete)
}

func TestFileRefs_StopsAtSectionEnd(t *testing.T) {
	body := "**Files:**\n- Modify: `a.go`\n\n**Steps:**\n- Modify: `b.go`\n"
	refs := FileRefs(body)
	assert.Equal(t, []string{"a.go"}, refs.Modify, "bullets under a later section are not file refs")
}

func TestFileRefs_TestPrefixIgnored(t *testing.T) {
	refs := FileRefs("**Files:**\n- Test: `a_test.go`\n- Modify: `a.go`\n")
	assert.Equal(t, []string{"a.go"}, refs.Modify)
	assert.Empty(t, refs.Create)
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test -race ./internal/planparser/ -run TestFileRefs -v`
Expected: FAIL — `FileRefs` undefined (compile error).

- [ ] **Step 3: Write the implementation**

Create `internal/planparser/filerefs.go`:

```go
package planparser

import (
	"regexp"
	"strings"
)

// TaskFileRefs is one task's declared file operations, taken from the
// **Files:** bullet list. The json:metadata fence's "files" array is a FLAT
// list with no Create/Modify distinction, so it cannot drive an order-aware
// consistency check — the bullets are the only source of the verb.
type TaskFileRefs struct {
	Create []string
	Modify []string
	Delete []string
}

var (
	// filesHeadingRe matches the **Files:** section heading, case-insensitively.
	filesHeadingRe = regexp.MustCompile(`(?i)^\s*\*\*files:\*\*\s*$`)
	// fileBulletRe matches "- Create: path", "* modify: `path`",
	// "- Create/Modify: path". The verb group may name two verbs joined by
	// "/", which the plan template uses for a file that is created by one
	// task and edited by another.
	fileBulletRe = regexp.MustCompile(`(?i)^\s*[-*]\s*((?:create|modify|delete)(?:/(?:create|modify|delete))*)\s*:\s*(.+)$`)
	// trailingParenRe strips a trailing "(lines 10-20)"-style annotation.
	trailingParenRe = regexp.MustCompile(`\s*\([^)]*\)\s*$`)
)

// FileRefs extracts a task body's declared file operations.
//
// Collection starts at the **Files:** heading and stops at the first line
// that is neither a bullet nor blank — so a later "**Steps:**" section whose
// bullets happen to read like file operations is not harvested. A body with
// no **Files:** section yields three empty lists and is not a finding
// anywhere: the consistency check guards plans that opt into the structure,
// it does not demand that they do.
func FileRefs(body string) TaskFileRefs {
	var refs TaskFileRefs
	inSection := false
	for _, line := range strings.Split(body, "\n") {
		if !inSection {
			if filesHeadingRe.MatchString(line) {
				inSection = true
			}
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		m := fileBulletRe.FindStringSubmatch(line)
		if m == nil {
			if strings.HasPrefix(strings.TrimSpace(line), "-") ||
				strings.HasPrefix(strings.TrimSpace(line), "*") {
				// A bullet with an unrecognized verb (e.g. "Test:") — skip it
				// but stay in the section.
				continue
			}
			break
		}
		path := cleanRefPath(m[2])
		if path == "" {
			continue
		}
		for _, verb := range strings.Split(strings.ToLower(m[1]), "/") {
			switch verb {
			case "create":
				refs.Create = append(refs.Create, path)
			case "modify":
				refs.Modify = append(refs.Modify, path)
			case "delete":
				refs.Delete = append(refs.Delete, path)
			}
		}
	}
	return refs
}

// cleanRefPath takes the path out of a bullet's tail: backtick-quoted when
// present, else the first whitespace-delimited token, with any trailing
// parenthetical annotation removed.
func cleanRefPath(tail string) string {
	tail = strings.TrimSpace(trailingParenRe.ReplaceAllString(strings.TrimSpace(tail), ""))
	if i := strings.Index(tail, "`"); i >= 0 {
		rest := tail[i+1:]
		if j := strings.Index(rest, "`"); j >= 0 {
			return strings.TrimSpace(rest[:j])
		}
	}
	return strings.TrimSpace(strings.Fields(tail+" ")[0])
}
```

- [ ] **Step 4: Run to verify pass**

Run: `go test -race ./internal/planparser/... -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/planparser/filerefs.go internal/planparser/filerefs_test.go
git commit -m "feat(planparser): extract Create/Modify/Delete refs from task Files bullets"
```

```json:metadata
{"files": ["internal/planparser/filerefs.go", "internal/planparser/filerefs_test.go"], "verifyCommand": "go test -race ./internal/planparser/...", "acceptanceCriteria": ["FileRefs returns Create/Modify/Delete lists", "Backticked and bare paths both parse", "Trailing parentheticals stripped", "Create/Modify contributes to both lists", "Case-insensitive verbs, - and * bullets", "No Files section yields empty lists", "Bullets after the section ends are not collected"], "modelTier": "mechanical"}
```

---

### Task 9: Order-aware Create/Modify consistency check

**Goal:** Flag `Modify:` targets that cannot exist when their task runs — order tier always, disk tier when `repo_root` is supplied — and wire it into `ValidatePlan`.

**Files:**
- Create: `internal/mcpsrv/file_consistency.go`
- Create: `internal/mcpsrv/file_consistency_test.go`
- Modify: `internal/mcpsrv/handlers.go` (`ValidatePlan`)

**Acceptance Criteria:**
- [ ] Order tier: Task N modifies a path only a later task creates → one finding.
- [ ] Order tier: Task N modifies a path an earlier task creates → no finding.
- [ ] Disk tier (with `repo_root`): a `Modify:` target absent from disk and created by no earlier task → finding.
- [ ] **A `Create:` target that already exists on disk produces NO finding** — the already-implemented-worktree regression guard.
- [ ] Without `repo_root`, the disk tier is skipped and the order tier still runs.
- [ ] A `Modify:` path escaping `repo_root` via `..` is ignored, not stat'd.
- [ ] The finding is plan-level, `major`, criterion `task_order_contradiction`, one line per contradiction.
- [ ] The finding appears in `PlanResult.PlanFindings` on a normal review.

**Verify:** `go test -race ./internal/mcpsrv/ -run 'TestCheckFileConsistency|TestValidatePlan_FileConsistency' -v` → PASS

**Steps:**

- [ ] **Step 1: Write the failing tests**

Create `internal/mcpsrv/file_consistency_test.go`:

```go
package mcpsrv

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/planparser"
	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

func tasksFrom(bodies ...string) []planparser.RawTask {
	out := make([]planparser.RawTask, 0, len(bodies))
	for _, b := range bodies {
		out = append(out, planparser.RawTask{Body: b})
	}
	return out
}

func TestCheckFileConsistency_OrderTier_LaterCreateIsAContradiction(t *testing.T) {
	f := checkFileConsistency(tasksFrom(
		"**Files:**\n- Modify: `a.go`\n",
		"**Files:**\n- Create: `a.go`\n",
	), "")
	require.NotNil(t, f)
	assert.Equal(t, verdict.SeverityMajor, f.Severity)
	assert.Equal(t, "task_order_contradiction", f.Criterion)
	assert.Contains(t, f.Evidence, "Task 1")
	assert.Contains(t, f.Evidence, "Task 2")
	assert.Contains(t, f.Evidence, "a.go")
}

func TestCheckFileConsistency_OrderTier_EarlierCreateIsFine(t *testing.T) {
	f := checkFileConsistency(tasksFrom(
		"**Files:**\n- Create: `a.go`\n",
		"**Files:**\n- Modify: `a.go`\n",
	), "")
	assert.Nil(t, f)
}

func TestCheckFileConsistency_NoRepoRoot_SkipsDiskTier(t *testing.T) {
	// a.go is created by no task and (without repo_root) cannot be checked
	// against disk — so no finding.
	f := checkFileConsistency(tasksFrom("**Files:**\n- Modify: `a.go`\n"), "")
	assert.Nil(t, f)
}

func TestCheckFileConsistency_DiskTier_MissingTargetIsAContradiction(t *testing.T) {
	dir := t.TempDir()
	f := checkFileConsistency(tasksFrom("**Files:**\n- Modify: `a.go`\n"), dir)
	require.NotNil(t, f)
	assert.Contains(t, f.Evidence, "does not exist")
}

func TestCheckFileConsistency_DiskTier_ExistingTargetIsFine(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.go"), []byte("x"), 0o600))
	f := checkFileConsistency(tasksFrom("**Files:**\n- Modify: `a.go`\n"), dir)
	assert.Nil(t, f)
}

// THE regression guard. The naive version of this check reported 10
// contradictions on a real plan and all 10 were false positives — 9 of them
// because an earlier task had already been implemented in the worktree, so
// its Create: target existed. A resumed plan run is a legitimate state and
// must never be flagged. See design §6.2.
func TestCheckFileConsistency_AlreadyImplementedWorktreeIsNotAFinding(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.go"), []byte("x"), 0o600))
	f := checkFileConsistency(tasksFrom(
		"**Files:**\n- Create: `a.go`\n",
		"**Files:**\n- Modify: `a.go`\n",
	), dir)
	assert.Nil(t, f, "a Create: target that already exists is not a contradiction")
}

func TestCheckFileConsistency_PathEscapingRepoRootIsIgnored(t *testing.T) {
	dir := t.TempDir()
	f := checkFileConsistency(tasksFrom("**Files:**\n- Modify: `../../etc/passwd`\n"), dir)
	assert.Nil(t, f, "a path outside repo_root is ignored, not stat'd")
}

func TestCheckFileConsistency_NoFilesSections(t *testing.T) {
	assert.Nil(t, checkFileConsistency(tasksFrom("**Goal:** g\n"), t.TempDir()))
}
```

Append to `internal/mcpsrv/handlers_plan_test.go`:

```go
func TestValidatePlan_FileConsistencyFindingReachesTheEnvelope(t *testing.T) {
	plan := "# Plan\n\n" +
		"### Task 1: t1\n\n**Goal:** g\n\n**Acceptance criteria:**\n- ac\n\n**Files:**\n- Modify: `a.go`\n\n" +
		"### Task 2: t2\n\n**Goal:** g\n\n**Acceptance criteria:**\n- ac\n\n**Files:**\n- Create: `a.go`\n\n"

	sr := &scriptedReviewer{responses: []providers.Response{singlePlanResp(t, 2)}}
	d := newDepsWithScripted(t, sr, 8)
	h := &handlers{deps: d}

	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: plan})
	require.NoError(t, err)

	var found bool
	for _, f := range pr.PlanFindings {
		if f.Criterion == "task_order_contradiction" {
			found = true
			assert.Equal(t, verdict.SeverityMajor, f.Severity)
		}
	}
	assert.True(t, found, "the deterministic check's finding must reach the envelope")
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test -race ./internal/mcpsrv/ -run TestCheckFileConsistency -v`
Expected: FAIL — `checkFileConsistency` undefined (compile error).

- [ ] **Step 3: Write the implementation**

Create `internal/mcpsrv/file_consistency.go`:

```go
// Package mcpsrv: the deterministic, reviewer-free Create/Modify consistency
// check for validate_plan. No provider call; stats only, never reads file
// contents. See design §6.
package mcpsrv

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/patiently/anti-tangent-mcp/internal/planparser"
	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

// checkFileConsistency reports Modify: targets that cannot exist when their
// task runs. Returns nil when the plan is consistent — which is the expected
// outcome on a well-formed plan; this is a guard, not a source of findings.
//
// ONLY the Modify direction is checked. The mirror check — flagging a
// Create: target that already exists on disk — was measured at 9 false
// positives out of 10 findings on a real plan, every one of them because an
// earlier task had already been implemented in the worktree. A resumed or
// partially-executed plan run is a legitimate state, so that direction is
// deliberately absent. See design §6.2.
//
// Two tiers:
//
//   - Order tier (always): a Modify: target whose only Create: comes from a
//     LATER task. Provable from the plan text alone.
//   - Disk tier (repoRoot != ""): a Modify: target that exists on neither
//     disk nor any earlier task's Create: list.
//
// repoRoot == "" skips the disk tier rather than erroring: the check is free
// and optional, and refusing to review a plan for want of an optional
// argument would be a regression.
func checkFileConsistency(tasks []planparser.RawTask, repoRoot string) *verdict.Finding {
	refs := make([]planparser.TaskFileRefs, len(tasks))
	for i, t := range tasks {
		refs[i] = planparser.FileRefs(t.Body)
	}

	// createdBy[path] = 1-based index of the FIRST task that creates it.
	createdBy := map[string]int{}
	for i, r := range refs {
		for _, p := range r.Create {
			if _, seen := createdBy[p]; !seen {
				createdBy[p] = i + 1
			}
		}
	}

	var lines []string
	for i, r := range refs {
		taskNum := i + 1
		for _, p := range r.Modify {
			if created, ok := createdBy[p]; ok {
				if created > taskNum {
					lines = append(lines, fmt.Sprintf(
						"Task %d modifies `%s`, which is not created until Task %d", taskNum, p, created))
				}
				continue
			}
			if repoRoot == "" {
				continue
			}
			abs, ok := resolveUnderRoot(repoRoot, p)
			if !ok {
				continue
			}
			if _, err := os.Stat(abs); err != nil {
				lines = append(lines, fmt.Sprintf(
					"Task %d modifies `%s`, which does not exist and is created by no earlier task", taskNum, p))
			}
		}
	}
	if len(lines) == 0 {
		return nil
	}
	return &verdict.Finding{
		Severity:   verdict.SeverityMajor,
		Category:   verdict.CategoryOther,
		Criterion:  "task_order_contradiction",
		Evidence:   strings.Join(lines, "\n"),
		Suggestion: "Reorder the tasks, or add the missing Create: bullet to an earlier task.",
	}
}

// resolveUnderRoot joins a repo-relative plan path to root and reports
// whether the result stays inside root. A bullet reading "../../etc/passwd"
// is ignored rather than stat'd — the check has no business touching
// anything outside the repository it was pointed at.
func resolveUnderRoot(root, rel string) (string, bool) {
	if filepath.IsAbs(rel) {
		return "", false
	}
	abs := filepath.Clean(filepath.Join(root, rel))
	r, err := filepath.Rel(filepath.Clean(root), abs)
	if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return "", false
	}
	return abs, true
}
```

- [ ] **Step 4: Wire it into `ValidatePlan`**

In `internal/mcpsrv/handlers.go`, replace the `_ = repoRoot` placeholder line added in Task 6 with nothing, and add the check immediately after `populateNormativeTestBodies(&pr, tasks)`:

```go
	if fc := checkFileConsistency(tasks, repoRoot); fc != nil {
		pr.PlanFindings = append(pr.PlanFindings, *fc)
	}
```

It runs before `finalizePlanVerdict`, so a `major` contradiction participates in the severity ladder — and, not being an `unverifiable_codebase_claim`, it correctly blocks `calibratePlanVerdictForUnverifiableOnly` from force-passing the plan.

- [ ] **Step 5: Run to verify pass**

Run: `go test -race ./internal/mcpsrv/ -run 'TestCheckFileConsistency|TestValidatePlan_FileConsistency' -v`
Expected: PASS (9 tests)

- [ ] **Step 6: Full suite**

Run: `go build ./... && go test -race ./...`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/mcpsrv/file_consistency.go internal/mcpsrv/file_consistency_test.go internal/mcpsrv/handlers.go internal/mcpsrv/handlers_plan_test.go
git commit -m "feat(mcpsrv): order-aware Create/Modify consistency check"
```

```json:metadata
{"files": ["internal/mcpsrv/file_consistency.go", "internal/mcpsrv/file_consistency_test.go", "internal/mcpsrv/handlers.go", "internal/mcpsrv/handlers_plan_test.go"], "verifyCommand": "go test -race ./internal/mcpsrv/ -run 'TestCheckFileConsistency|TestValidatePlan_FileConsistency' -v", "acceptanceCriteria": ["Order tier flags a Modify before its Create", "Order tier passes a Modify after its Create", "Disk tier flags a missing Modify target", "A Create target that already exists is NOT flagged", "No repo_root skips the disk tier only", "Paths escaping repo_root are ignored", "Finding is plan-level major with criterion task_order_contradiction", "The finding reaches PlanFindings on a normal review"], "modelTier": "standard"}
```

---

### Task 10: Pin the rollup and calibration guards

**Goal:** Prove that a `contradicted_codebase_claim` is never absorbed into the checklist rollup and never force-passed — the one failure mode that would make this feature worse than not shipping it.

**Files:**
- Test: `internal/mcpsrv/plan_normalize_test.go`

**Acceptance Criteria:**
- [ ] A task-level `contradicted_codebase_claim` survives `normalizePlanUnverifiableFindings` attached to its task.
- [ ] It does not appear in the `codebase_reference_checklist` rollup evidence.
- [ ] `calibratePlanVerdictForUnverifiableOnly` does **not** force `pass` when one is present alongside minor unverifiable findings.
- [ ] A control case with only minor unverifiable findings still force-passes (proving the test discriminates).

**Verify:** `go test -race ./internal/mcpsrv/ -run 'TestNormalizePlan|TestCalibratePlan' -v` → PASS

**Steps:**

- [ ] **Step 1: Write the tests**

Append to `internal/mcpsrv/plan_normalize_test.go` (create the file with `package mcpsrv` and the testify imports if it does not exist):

```go
func TestNormalizePlanUnverifiableFindings_LeavesContradictionsAttached(t *testing.T) {
	pr := verdict.PlanResult{Tasks: []verdict.PlanTaskResult{{
		TaskIndex: 1,
		Findings: []verdict.Finding{
			{Severity: verdict.SeverityMinor, Category: verdict.CategoryUnverifiableCodebaseClaim,
				Criterion: "c1", Evidence: "unverifiable-evidence", Suggestion: "s"},
			{Severity: verdict.SeverityMajor, Category: verdict.CategoryContradictedCodebaseClaim,
				Criterion: "c2", Evidence: "contradiction-evidence", Suggestion: "s"},
		},
	}}}
	normalizePlanUnverifiableFindings(&pr)

	require.Len(t, pr.Tasks[0].Findings, 1)
	assert.Equal(t, verdict.CategoryContradictedCodebaseClaim, pr.Tasks[0].Findings[0].Category,
		"the contradiction stays on its task")

	require.Len(t, pr.PlanFindings, 1)
	assert.Equal(t, "codebase_reference_checklist", pr.PlanFindings[0].Criterion)
	assert.Contains(t, pr.PlanFindings[0].Evidence, "unverifiable-evidence")
	assert.NotContains(t, pr.PlanFindings[0].Evidence, "contradiction-evidence",
		"a hard contradiction must never be rolled into the go-grep-it-yourself checklist")
}

func TestCalibratePlanVerdict_DoesNotForcePassWithAContradiction(t *testing.T) {
	pr := verdict.PlanResult{
		PlanVerdict: verdict.VerdictWarn,
		PlanFindings: []verdict.Finding{
			{Severity: verdict.SeverityMinor, Category: verdict.CategoryUnverifiableCodebaseClaim,
				Criterion: "c1", Evidence: "e", Suggestion: "s"},
			{Severity: verdict.SeverityMajor, Category: verdict.CategoryContradictedCodebaseClaim,
				Criterion: "c2", Evidence: "e", Suggestion: "s"},
		},
	}
	calibratePlanVerdictForUnverifiableOnly(&pr)
	assert.Equal(t, verdict.VerdictWarn, pr.PlanVerdict, "must not be force-passed")
}

// Control: without the contradiction, the same shape DOES force-pass. Without
// this, the test above would pass even if calibration were broken outright.
func TestCalibratePlanVerdict_StillForcePassesUnverifiableOnly(t *testing.T) {
	pr := verdict.PlanResult{
		PlanVerdict: verdict.VerdictWarn,
		PlanFindings: []verdict.Finding{
			{Severity: verdict.SeverityMinor, Category: verdict.CategoryUnverifiableCodebaseClaim,
				Criterion: "c1", Evidence: "e", Suggestion: "s"},
		},
	}
	calibratePlanVerdictForUnverifiableOnly(&pr)
	assert.Equal(t, verdict.VerdictPass, pr.PlanVerdict)
}
```

- [ ] **Step 2: Run them**

Run: `go test -race ./internal/mcpsrv/ -run 'TestNormalizePlan|TestCalibratePlan' -v`
Expected: PASS.

These are characterization tests — the existing category checks in `plan_normalize.go` should already satisfy them. **If any fails, fix `plan_normalize.go`**, do not weaken the test: the assertion is the spec's §5.2 requirement.

- [ ] **Step 3: Commit**

```bash
git add internal/mcpsrv/plan_normalize_test.go
git commit -m "test(mcpsrv): pin that contradicted_codebase_claim is never rolled up or force-passed"
```

```json:metadata
{"files": ["internal/mcpsrv/plan_normalize_test.go"], "verifyCommand": "go test -race ./internal/mcpsrv/ -run 'TestNormalizePlan|TestCalibratePlan' -v", "acceptanceCriteria": ["Contradiction survives rollup attached to its task", "Contradiction evidence is absent from the checklist rollup", "Calibration does not force-pass when a contradiction is present", "Control case with only minor unverifiable findings still force-passes"], "modelTier": "mechanical"}
```

---

### Task 11: Docs, protocol resync, and changelog

**Goal:** Document the feature where its users will look, fix the stale cost claim, and satisfy the CI-enforced protocol invariants.

**Files:**
- Modify: `docs/protocol/controller.md`
- Modify: `docs/protocol/core.md`
- Modify: `plugin/anti-tangent-protocol/protocol/*.md` (mechanical resync)
- Modify: `README.md`
- Modify: `CHANGELOG.md`

**Acceptance Criteria:**
- [ ] `controller.md` documents `context_paths` / `repo_root` and replaces the "~$0.01–$0.02" figure with real numbers plus an explicit opt-in-and-expensive warning.
- [ ] `core.md` documents `contradicted_codebase_claim` next to `unverifiable_codebase_claim`.
- [ ] Every `docs/protocol/*.md` part is **under 16,000 bytes**; `INTEGRATION.md` under 2,000.
- [ ] `plugin/anti-tangent-protocol/protocol/` is byte-identical to `docs/protocol/`.
- [ ] README documents both new env vars, the reused `ANTI_TANGENT_PLAN_ROOTS`, and that OpenAI/Google reviewers get no caching so attachments are full price every call.
- [ ] `CHANGELOG.md`'s `## [0.17.0]` entry covers the whole release.
- [ ] No protocol section number (§1, §3.x, §4.x, §5.x, §6) is renumbered.

**Verify:** `bash -c 'for f in docs/protocol/*.md; do s=$(wc -c < "$f"); [ "$s" -lt 16000 ] || { echo "OVER: $f $s"; exit 1; }; done; diff -r docs/protocol plugin/anti-tangent-protocol/protocol && echo SYNC-OK'` → `SYNC-OK`

**Steps:**

- [ ] **Step 1: Update `controller.md`**

Replace the stale cost sentence:

> **Why this matters:** catching a vague AC at handoff costs one `validate_plan` call (~$0.01–$0.02); …

with an accurate one, and add a `context_paths` subsection covering:
- what it is (absolute paths, whole files, read by the server);
- that the reviewer verifies claims against attached files instead of emitting `unverifiable_codebase_claim`, and emits `contradicted_codebase_claim` when a claim is refuted;
- the cost: a 170KB plan with ~100K tokens of attachments runs roughly **$1.31 per round** on the default plan model versus cents without attachments — so attach only the files the plan makes claims about;
- that oversized attachments are refused, never truncated;
- `repo_root` as the optional enabler of the Create/Modify disk check.

- [ ] **Step 2: Update `core.md` — mind the byte budget**

Add a short `contradicted_codebase_claim` entry beside `unverifiable_codebase_claim`: emitted only when `context_paths` attached the file, carries the reviewer's own severity (unlike `unverifiable_codebase_claim`, which is always minor), and is never rolled into the reference checklist.

`core.md` starts at 13,023 bytes of a 16,000 budget. Check as you go:

```bash
wc -c docs/protocol/core.md
```

- [ ] **Step 3: Verify every part is under budget**

Run:
```bash
for f in docs/protocol/*.md; do printf '%8d  %s\n' "$(wc -c < "$f")" "$f"; done
wc -c docs/protocol/INTEGRATION.md 2>/dev/null || true
```
Expected: every part < 16000. If `core.md` is over, tighten the new text — do not move protocol text into `INTEGRATION.md`, which stays an index.

- [ ] **Step 4: Resync the plugin bundle**

Run:
```bash
rm -f plugin/anti-tangent-protocol/protocol/*.md
cp docs/protocol/*.md plugin/anti-tangent-protocol/protocol/
diff -r docs/protocol plugin/anti-tangent-protocol/protocol && echo SYNC-OK
```
Expected: `SYNC-OK`

- [ ] **Step 5: Update the README**

In the env-var table add:

| Var | Default | Meaning |
|---|---|---|
| `ANTI_TANGENT_CONTEXT_MAX_FILE_BYTES` | `131072` | Per-file cap for `validate_plan` `context_paths` attachments. Oversized files are refused, never truncated. |
| `ANTI_TANGENT_CONTEXT_MAX_PAYLOAD_BYTES` | `524288` | Cap for the attached set as a whole. |

And add a note in the filesystem-access section:

> `context_paths` and `repo_root` are governed by the same `ANTI_TANGENT_PLAN_ROOTS` allowlist as `plan_path` — restricting one restricts all three.
>
> **Attachments are only cached on Anthropic reviewers.** The OpenAI and Google clients have no prompt-cache support, so a large attached set is billed in full on every reviewer call of every round when `ANTI_TANGENT_PLAN_MODEL` names one of them.

- [ ] **Step 6: Complete the CHANGELOG entry**

Extend the `## [0.17.0] - 2026-09-01` block started in Task 1 so the whole release is covered. The `### Added` section gains, above the two env-var bullets already there:

```markdown
- **`context_paths` on `validate_plan`.** Absolute paths to source files the plan makes claims
  about. The server reads them whole and renders them ahead of the plan, and the reviewer's
  ground rules are rewritten to enumerate exactly what is attached: for those files absence is
  evidence, and everything else stays black-box. The reviewer verifies codebase claims instead
  of emitting `unverifiable_codebase_claim` for each one. **Opt-in and materially expensive** —
  a 170KB plan with ~100K tokens of attachments runs roughly $1.31 per round on the default
  plan model, against cents without them. Attach only the files the plan actually touches.
  Oversized attachments are refused, never truncated: the reviewer is told attached files are
  complete, and a silently-shortened file would turn that promise into false findings.
- **`contradicted_codebase_claim`.** A new finding category for a plan claim an attached file
  refutes. Unlike `unverifiable_codebase_claim` it carries no severity floor — an attached file
  is ground truth read from disk, so the reviewer's chosen severity stands — and it is never
  rolled into the `codebase_reference_checklist` nor force-passed by the unverifiable-only
  verdict calibration.
- **Order-aware Create/Modify consistency check.** Deterministic and reviewer-free: a task's
  `Modify:` target must exist on disk or be created by a lower-numbered task. Emits one
  plan-level `task_order_contradiction` finding. The mirror check — flagging a `Create:` target
  that already exists — is deliberately absent: measured on a real plan it produced 9 false
  positives, every one of them because an earlier task had already been implemented in the
  worktree, which is a legitimate state for a resumed plan run.
- **`repo_root` on `validate_plan`** — optional absolute path that enables the disk tier of that
  check. Without it the order tier still runs.
```

And add:

```markdown
### Changed
- `docs/protocol/controller.md`'s per-call cost figure for the plan gate was stale — it
  advertised ~$0.01–$0.02, which was already wrong for a large plan and off by roughly 50× with
  attachments. It now carries real numbers for both cases.
- The reviewer ground rules, previously duplicated verbatim across all three plan templates, are
  now one shared partial. No behavioural change for a call without `context_paths`: the rendered
  prompt is byte-identical to 0.16.0.

### Fixed
- **`validate_plan`'s in-process result cache now keys on attached file content.** Without this,
  editing a source file a finding complained about and immediately re-validating would return
  the stale pre-fix review from the 3-minute cache, with no indication anything was reused.
```

- [ ] **Step 7: Run the whole verification**

Run:
```bash
go build ./... && go test -race ./...
for f in docs/protocol/*.md; do s=$(wc -c < "$f"); [ "$s" -lt 16000 ] || { echo "OVER: $f $s"; exit 1; }; done
diff -r docs/protocol plugin/anti-tangent-protocol/protocol && echo SYNC-OK
git status --short VERSION   # must print nothing
```
Expected: tests PASS, `SYNC-OK`, `VERSION` unmodified.

- [ ] **Step 8: Commit**

```bash
git add docs/protocol/ plugin/anti-tangent-protocol/protocol/ README.md CHANGELOG.md
git commit -m "docs: document context_paths, contradicted_codebase_claim, and real gate costs"
```

```json:metadata
{"files": ["docs/protocol/controller.md", "docs/protocol/core.md", "plugin/anti-tangent-protocol/protocol/controller.md", "plugin/anti-tangent-protocol/protocol/core.md", "README.md", "CHANGELOG.md"], "verifyCommand": "for f in docs/protocol/*.md; do s=$(wc -c < \"$f\"); [ \"$s\" -lt 16000 ] || { echo \"OVER: $f $s\"; exit 1; }; done; diff -r docs/protocol plugin/anti-tangent-protocol/protocol && echo SYNC-OK", "acceptanceCriteria": ["controller.md documents context_paths/repo_root and replaces the stale cost figure", "core.md documents contradicted_codebase_claim", "Every protocol part under 16000 bytes and INTEGRATION.md under 2000", "plugin protocol dir byte-identical to docs/protocol", "README documents both env vars, the shared roots allowlist, and the non-Anthropic caching caveat", "CHANGELOG 0.17.0 entry covers the release", "No protocol section renumbered"], "modelTier": "standard"}
```
