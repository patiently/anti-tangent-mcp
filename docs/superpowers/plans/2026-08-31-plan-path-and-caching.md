# 0.16.0 — File-path inputs + reviewer-side prompt caching Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let callers pass file paths to `validate_plan` and `validate_completion` instead of emitting content as tool-call output, and stop re-billing the full plan on every chunked reviewer call.

**Architecture:** One shared filesystem resolver (`internal/mcpsrv/file_source.go`) runs at the top of each handler and produces a string; every existing downstream stage keeps operating on text and is untouched. Caching splits the already-identical template prefix (through the plan) from the per-call suffix and marks the prefix `cache_control: ephemeral` on the Anthropic client only.

**Tech Stack:** Go 1.25, `net/http` (no vendor SDKs), `testify`, golden-file prompt tests, `go test -race`.

**Spec:** `docs/superpowers/specs/2026-08-31-plan-path-design.md`

## Global Constraints

- **Absolute paths only.** Relative paths are rejected with an error, never resolved against the server's cwd.
- **`ANTI_TANGENT_PLAN_ROOTS` empty means unrestricted.** That is the default and must stay the default.
- **`PlanMaxPayloadBytes` (1MB) applies to `validate_plan` only.** `validate_completion` and the other five tools keep the shared `MaxPayloadBytes` (204800). Do not let the plan cap leak into any other handler.
- **`checkEvidenceShape` must run on resolved content**, never on the pre-resolution args. A path input must not become a bypass for the truncation guard.
- **Cache breakpoints only on the chunked path.** `rendered.Single` must leave `CachePrefix` empty — a breakpoint there is a 1.25× write premium against zero reads.
- **Unit tests never hit the network.** Use `httptest.Server`.
- **Existing behavior is byte-identical when the new inputs are absent.** `plan_text` callers, `final_files` with `content` set, and the OpenAI/Google clients must produce exactly today's requests.
- Prompt golden files change only via `go test ./internal/prompts/... -update`, and the diff is reviewed before commit.

**User decisions (already made):**
- "On by default, restrictable" — file reads work with no configuration; `ANTI_TANGENT_PLAN_ROOTS` narrows them.
- "Path + bytes + sha256, in summary only" — no `verdict.PlanResult` schema change.
- "Separate plan cap, default 1MB" — new `ANTI_TANGENT_PLAN_MAX_PAYLOAD_BYTES`, shared cap untouched for other tools.
- "Deprecate in 0.16.0, remove in 1.0.0" — `plan_text` still works and reports its own deprecation.
- "Split: 0.16.0 paths, 0.17.0 attachment" — source-file attachment is issue #61, explicitly out of scope here.
- Provider file attachment (Files API) is rejected: it does not reduce input tokens.

---

### Task 1: Config — plan payload cap and read roots

**Goal:** `Config` carries `PlanMaxPayloadBytes` (default 1MB) and `PlanRoots` (default empty = unrestricted), parsed from two new env vars.

**Files:**
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Acceptance Criteria:**
- [ ] `PlanMaxPayloadBytes` defaults to `1048576` when the env var is unset
- [ ] `ANTI_TANGENT_PLAN_MAX_PAYLOAD_BYTES` rejects non-numeric and non-positive values, naming the var in the error
- [ ] `PlanRoots` is empty by default; `ANTI_TANGENT_PLAN_ROOTS` splits on `:`, trims blanks, and `filepath.Clean`s each entry
- [ ] A relative entry in `ANTI_TANGENT_PLAN_ROOTS` is a startup error naming the offending path
- [ ] `MaxPayloadBytes` still defaults to `204800` and is unchanged

**Verify:** `go test -race ./internal/config/...` → PASS

**Steps:**

- [ ] **Step 1: Write the failing tests**

```go
func TestPlanPayloadCapAndRoots(t *testing.T) {
	base := map[string]string{"ANTHROPIC_API_KEY": "k"}
	envFrom := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}

	t.Run("defaults", func(t *testing.T) {
		cfg, err := Load(envFrom(base))
		require.NoError(t, err)
		assert.Equal(t, 1048576, cfg.PlanMaxPayloadBytes)
		assert.Equal(t, 204800, cfg.MaxPayloadBytes)
		assert.Empty(t, cfg.PlanRoots)
	})

	t.Run("plan cap override", func(t *testing.T) {
		m := map[string]string{"ANTHROPIC_API_KEY": "k", "ANTI_TANGENT_PLAN_MAX_PAYLOAD_BYTES": "4096"}
		cfg, err := Load(envFrom(m))
		require.NoError(t, err)
		assert.Equal(t, 4096, cfg.PlanMaxPayloadBytes)
		assert.Equal(t, 204800, cfg.MaxPayloadBytes, "shared cap untouched")
	})

	t.Run("plan cap rejects non-positive", func(t *testing.T) {
		m := map[string]string{"ANTHROPIC_API_KEY": "k", "ANTI_TANGENT_PLAN_MAX_PAYLOAD_BYTES": "0"}
		_, err := Load(envFrom(m))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "ANTI_TANGENT_PLAN_MAX_PAYLOAD_BYTES")
	})

	t.Run("roots parsed and cleaned", func(t *testing.T) {
		m := map[string]string{"ANTHROPIC_API_KEY": "k", "ANTI_TANGENT_PLAN_ROOTS": "/home/a/:/srv/b: "}
		cfg, err := Load(envFrom(m))
		require.NoError(t, err)
		assert.Equal(t, []string{"/home/a", "/srv/b"}, cfg.PlanRoots)
	})

	t.Run("relative root rejected", func(t *testing.T) {
		m := map[string]string{"ANTHROPIC_API_KEY": "k", "ANTI_TANGENT_PLAN_ROOTS": "relative/path"}
		_, err := Load(envFrom(m))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "ANTI_TANGENT_PLAN_ROOTS")
		assert.Contains(t, err.Error(), "absolute")
	})
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/config/ -run TestPlanPayloadCapAndRoots -v`
Expected: FAIL — `cfg.PlanMaxPayloadBytes` undefined

- [ ] **Step 3: Add the fields**

In the `Config` struct, after `MaxTokensCeiling`:

```go
	// PlanMaxPayloadBytes caps plan content + project_knowledge for
	// validate_plan only. Separate from MaxPayloadBytes because validate_plan
	// gained ~10x headroom with plan_path while the other tools did not; a
	// 1MB plan is ~270K tokens, which the chunked path sends on every call,
	// so the cap stays a real guard. See design §4.
	PlanMaxPayloadBytes int
	// PlanRoots restricts which directories file-path inputs may be read
	// from. Empty (the default) means unrestricted — the server is stdio-only
	// and the calling agent already has unrestricted file read, so the server
	// acquires no capability the caller lacks. See design §3.1 / §3.2.
	PlanRoots []string
```

- [ ] **Step 4: Add the default**

In the `cfg := Config{...}` literal, after `PlanTasksPerChunk: 8,`:

```go
		PlanMaxPayloadBytes:   1048576,
```

- [ ] **Step 5: Add env parsing**

Add `"path/filepath"` to the import block, then insert after the `ANTI_TANGENT_PLAN_TASKS_PER_CHUNK` block:

```go
	if v := env("ANTI_TANGENT_PLAN_MAX_PAYLOAD_BYTES"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("ANTI_TANGENT_PLAN_MAX_PAYLOAD_BYTES: %w", err)
		}
		if n <= 0 {
			return Config{}, fmt.Errorf("ANTI_TANGENT_PLAN_MAX_PAYLOAD_BYTES: must be positive, got %d", n)
		}
		cfg.PlanMaxPayloadBytes = n
	}
	if v := env("ANTI_TANGENT_PLAN_ROOTS"); v != "" {
		for _, p := range filepath.SplitList(v) {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			if !filepath.IsAbs(p) {
				return Config{}, fmt.Errorf("ANTI_TANGENT_PLAN_ROOTS: %q is not an absolute path", p)
			}
			cfg.PlanRoots = append(cfg.PlanRoots, filepath.Clean(p))
		}
	}
```

- [ ] **Step 6: Run tests**

Run: `go test -race ./internal/config/...`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): add PlanMaxPayloadBytes and PlanRoots"
```

---

### Task 2: Shared file-path resolver

**Goal:** One resolver both handlers use, enforcing absolute paths, symlink resolution before the roots check, regular-file-only, and a size cap that never loads an oversized file.

**Files:**
- Create: `internal/mcpsrv/file_source.go`
- Test: `internal/mcpsrv/file_source_test.go`

**Acceptance Criteria:**
- [ ] Relative paths are rejected without touching the filesystem
- [ ] Symlinks are resolved via `filepath.EvalSymlinks` **before** the roots check, so a symlink cannot escape an allowlisted root
- [ ] A symlink pointing *into* a root is accepted
- [ ] Root `/home/foo` does not authorize `/home/foobar`
- [ ] Directories, and other non-regular files, are rejected
- [ ] A file over the cap returns `errTooLarge` with the file's **true** size, and is never read into memory
- [ ] Empty roots accepts any absolute path
- [ ] Returned `fileSource` carries the symlink-resolved path, byte count, and full hex SHA-256

**Verify:** `go test -race ./internal/mcpsrv/ -run TestResolveFileInput -v` → PASS

**Steps:**

- [ ] **Step 1: Write the failing test**

```go
func TestResolveFileInput(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "plan.md")
	require.NoError(t, os.WriteFile(real, []byte("hello"), 0o644))

	// Resolve the temp dir itself — macOS /var -> /private/var makes the raw
	// t.TempDir() path a symlink, which would false-fail the roots checks.
	dirResolved, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)

	t.Run("reads file and reports provenance", func(t *testing.T) {
		content, src, err := resolveFileInput(real, nil, 1024)
		require.NoError(t, err)
		assert.Equal(t, "hello", content)
		assert.Equal(t, 5, src.Bytes)
		// sha256("hello")
		assert.Equal(t, "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824", src.SHA256)
		assert.Equal(t, filepath.Join(dirResolved, "plan.md"), src.Path)
	})

	t.Run("relative path rejected", func(t *testing.T) {
		_, _, err := resolveFileInput("docs/plan.md", nil, 1024)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "absolute")
	})

	t.Run("missing file", func(t *testing.T) {
		_, _, err := resolveFileInput(filepath.Join(dir, "nope.md"), nil, 1024)
		require.Error(t, err)
	})

	t.Run("directory rejected", func(t *testing.T) {
		_, _, err := resolveFileInput(dir, nil, 1024)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "regular file")
	})

	t.Run("over cap reports true size and does not read", func(t *testing.T) {
		big := filepath.Join(dir, "big.md")
		require.NoError(t, os.WriteFile(big, bytes.Repeat([]byte("x"), 5000), 0o644))
		_, src, err := resolveFileInput(big, nil, 1024)
		require.ErrorIs(t, err, errTooLarge)
		assert.Equal(t, 5000, src.Bytes, "true size, not cap+1")
	})

	t.Run("symlink escaping root rejected", func(t *testing.T) {
		outside := t.TempDir()
		target := filepath.Join(outside, "secret.md")
		require.NoError(t, os.WriteFile(target, []byte("secret"), 0o644))
		link := filepath.Join(dir, "escape.md")
		require.NoError(t, os.Symlink(target, link))

		_, _, err := resolveFileInput(link, []string{dirResolved}, 1024)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "ANTI_TANGENT_PLAN_ROOTS")
	})

	t.Run("symlink into root accepted", func(t *testing.T) {
		linkDir := t.TempDir()
		link := filepath.Join(linkDir, "into.md")
		require.NoError(t, os.Symlink(real, link))

		content, _, err := resolveFileInput(link, []string{dirResolved}, 1024)
		require.NoError(t, err)
		assert.Equal(t, "hello", content)
	})

	t.Run("root prefix does not match sibling directory", func(t *testing.T) {
		parent := t.TempDir()
		parentResolved, err := filepath.EvalSymlinks(parent)
		require.NoError(t, err)
		require.NoError(t, os.Mkdir(filepath.Join(parent, "foo"), 0o755))
		require.NoError(t, os.Mkdir(filepath.Join(parent, "foobar"), 0o755))
		victim := filepath.Join(parent, "foobar", "p.md")
		require.NoError(t, os.WriteFile(victim, []byte("x"), 0o644))

		_, _, err = resolveFileInput(victim, []string{filepath.Join(parentResolved, "foo")}, 1024)
		require.Error(t, err, "/foo must not authorize /foobar")
	})

	t.Run("empty roots is unrestricted", func(t *testing.T) {
		_, _, err := resolveFileInput(real, nil, 1024)
		require.NoError(t, err)
	})
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/mcpsrv/ -run TestResolveFileInput -v`
Expected: FAIL — `resolveFileInput` undefined

- [ ] **Step 3: Write the implementation**

```go
// Package mcpsrv: shared file-path input resolution for validate_plan and
// validate_completion. Scope: filesystem reads only; no provider calls.
//
// Named file_source.go rather than plan_source.go because validate_completion
// shares it — a plan-specific name would invite a divergent second copy the
// first time someone adds paths to check_progress. See design §3.
package mcpsrv

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// fileSource is the provenance of one resolved file input. Path is the
// symlink-resolved path actually read, so a caller reading the summary sees
// what the reviewer saw rather than what was requested.
type fileSource struct {
	Path   string
	Bytes  int
	SHA256 string
}

// errTooLarge signals the file exceeded the caller's cap. Callers map it to
// their own too-large envelope rather than surfacing a transport error, so the
// response shape matches an oversized inline payload.
var errTooLarge = errors.New("file exceeds cap")

// resolveFileInput reads path subject to roots and a byte cap.
//
// Order is load-bearing: symlinks resolve BEFORE the roots check so a symlink
// cannot hop outside an allowlisted root, and the size check uses stat so an
// oversized file is never read into memory.
func resolveFileInput(path string, roots []string, maxBytes int) (string, fileSource, error) {
	if strings.TrimSpace(path) == "" {
		return "", fileSource{}, errors.New("path is empty")
	}
	if !filepath.IsAbs(path) {
		return "", fileSource{}, fmt.Errorf("path must be absolute, got %q", path)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fileSource{}, fmt.Errorf("resolve %q: %w", path, err)
	}
	if !withinRoots(resolved, roots) {
		return "", fileSource{}, fmt.Errorf(
			"%q is outside ANTI_TANGENT_PLAN_ROOTS (%s)", resolved, strings.Join(roots, ":"))
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fileSource{}, fmt.Errorf("stat %q: %w", resolved, err)
	}
	if !info.Mode().IsRegular() {
		return "", fileSource{}, fmt.Errorf("%q is not a regular file", resolved)
	}
	if info.Size() > int64(maxBytes) {
		return "", fileSource{Path: resolved, Bytes: int(info.Size())}, errTooLarge
	}
	b, err := os.ReadFile(resolved)
	if err != nil {
		return "", fileSource{}, fmt.Errorf("read %q: %w", resolved, err)
	}
	sum := sha256.Sum256(b)
	return string(b), fileSource{
		Path:   resolved,
		Bytes:  len(b),
		SHA256: hex.EncodeToString(sum[:]),
	}, nil
}

// withinRoots reports whether p sits inside any root. Empty roots means
// unrestricted. Matching requires a separator boundary so /home/foo does not
// authorize /home/foobar.
func withinRoots(p string, roots []string) bool {
	if len(roots) == 0 {
		return true
	}
	p = filepath.Clean(p)
	for _, r := range roots {
		r = filepath.Clean(r)
		if p == r || strings.HasPrefix(p, r+string(filepath.Separator)) {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run tests**

Run: `go test -race ./internal/mcpsrv/ -run TestResolveFileInput -v`
Expected: PASS (all 9 subtests)

- [ ] **Step 5: Commit**

```bash
git add internal/mcpsrv/file_source.go internal/mcpsrv/file_source_test.go
git commit -m "feat(mcpsrv): add shared file-path input resolver"
```

---

### Task 3: `plan_path` on validate_plan, deprecation finding, plan cap

**Goal:** `validate_plan` accepts `plan_path`, still accepts `plan_text` with a deprecation finding, and uses `PlanMaxPayloadBytes`.

**Files:**
- Modify: `internal/mcpsrv/handlers.go` (`ValidatePlanArgs`, `validatePlanTool`, `ValidatePlan`, `tooLargePlanResult`)
- Test: `internal/mcpsrv/handlers_plan_test.go`

**Acceptance Criteria:**
- [ ] Neither input set → error `plan_text or plan_path is required`
- [ ] Both set → error `plan_text and plan_path are mutually exclusive`
- [ ] `plan_path` produces a `PlanResult` identical to the same content via `plan_text`, apart from the deprecation finding
- [ ] `plan_text` emits exactly one `minor` / `other` finding with criterion `input`, and the plan verdict is unchanged by it
- [ ] The cap check uses `PlanMaxPayloadBytes`; a plan between `MaxPayloadBytes` and `PlanMaxPayloadBytes` is accepted
- [ ] Oversized `plan_path` content returns the `tooLargePlanResult` envelope (not a transport error) reporting the file's true size
- [ ] `tooLargePlanResult` evidence says `plan:` not `plan_text:`, and `NextAction` says "Reduce plan size and retry."

**Verify:** `go test -race ./internal/mcpsrv/ -run 'TestValidatePlan' -v` → PASS

**Steps:**

- [ ] **Step 1: Write the failing tests**

```go
func TestValidatePlanPathInput(t *testing.T) {
	planMD := "# P\n\n### Task 1: Do a thing\n\n**Goal:** g\n\n**Acceptance criteria:**\n- [ ] a\n"

	t.Run("neither input", func(t *testing.T) {
		h := newTestPlanHandlers(t)
		_, _, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "plan_text or plan_path is required")
	})

	t.Run("both inputs", func(t *testing.T) {
		h := newTestPlanHandlers(t)
		_, _, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{
			PlanText: planMD, PlanPath: "/tmp/x.md",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "mutually exclusive")
	})

	t.Run("plan_path matches plan_text", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "plan.md")
		require.NoError(t, os.WriteFile(p, []byte(planMD), 0o644))

		h := newTestPlanHandlers(t)
		_, viaPath, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanPath: p})
		require.NoError(t, err)

		h2 := newTestPlanHandlers(t)
		_, viaText, err := h2.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: planMD})
		require.NoError(t, err)

		assert.Equal(t, len(viaPath.Tasks), len(viaText.Tasks))
		assert.Equal(t, viaPath.PlanVerdict, viaText.PlanVerdict)
	})

	t.Run("plan_text emits one deprecation finding", func(t *testing.T) {
		h := newTestPlanHandlers(t)
		_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: planMD})
		require.NoError(t, err)

		var dep []verdict.Finding
		for _, f := range pr.PlanFindings {
			if f.Criterion == "input" {
				dep = append(dep, f)
			}
		}
		require.Len(t, dep, 1)
		assert.Equal(t, verdict.SeverityMinor, dep[0].Severity)
		assert.Equal(t, verdict.CategoryOther, dep[0].Category)
		assert.Contains(t, dep[0].Suggestion, "plan_path")
	})

	t.Run("plan_path over plan cap returns envelope", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "big.md")
		require.NoError(t, os.WriteFile(p, bytes.Repeat([]byte("x"), 5000), 0o644))

		h := newTestPlanHandlers(t)
		h.deps.Cfg.PlanMaxPayloadBytes = 1024
		_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanPath: p})
		require.NoError(t, err, "too-large is an envelope, not a transport error")
		require.NotEmpty(t, pr.PlanFindings)
		assert.Equal(t, verdict.CategoryTooLarge, pr.PlanFindings[0].Category)
		assert.Contains(t, pr.PlanFindings[0].Evidence, "5000", "true file size")
		assert.Contains(t, pr.PlanFindings[0].Evidence, "plan:")
		assert.NotContains(t, pr.PlanFindings[0].Evidence, "plan_text:")
	})

	t.Run("plan cap is independent of shared cap", func(t *testing.T) {
		h := newTestPlanHandlers(t)
		h.deps.Cfg.MaxPayloadBytes = 10 // would reject if the plan path used it
		h.deps.Cfg.PlanMaxPayloadBytes = 1 << 20
		_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: planMD})
		require.NoError(t, err)
		for _, f := range pr.PlanFindings {
			assert.NotEqual(t, verdict.CategoryTooLarge, f.Category)
		}
	})
}
```

> `newTestPlanHandlers(t)` is the existing helper in `handlers_helpers_test.go` — it wires a
> `fakeReviewer` returning `passPlanResp(...)`. Do not add a new constructor. Tests needing a
> specific call sequence use `newDepsWithScripted(t, sr, chunkSize)` + `&handlers{deps: d}`, as
> `TestReviewPlanChunked_9Tasks_2Chunks` does.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/mcpsrv/ -run TestValidatePlanPathInput -v`
Expected: FAIL — unknown field `PlanPath`

- [ ] **Step 3: Widen the args struct and tool description**

```go
type ValidatePlanArgs struct {
	PlanText          string `json:"plan_text,omitempty"`
	PlanPath          string `json:"plan_path,omitempty"`
	ProjectKnowledge  string `json:"project_knowledge,omitempty"`
	ModelOverride     string `json:"model_override,omitempty"`
	MaxTokensOverride int    `json:"max_tokens_override,omitempty"`
	Mode              string `json:"mode,omitempty"`
}
```

In `validatePlanTool()`, append to the description:

```go
			"Pass plan_path with the ABSOLUTE path to the plan file — the server reads it, so a large plan costs the caller no output tokens. " +
			"plan_text is deprecated and will be removed in 1.0.0. Exactly one of the two must be set. " +
```

- [ ] **Step 4: Add the deprecation helper**

Next to `prependPlanClamp`:

```go
// prependPlanDeprecation prepends the plan_text deprecation notice. Minor
// severity so it never changes a verdict — the server is advisory, and a
// deprecated input is not a plan defect. Mirrors prependPlanClamp so it
// survives every early-exit path.
func prependPlanDeprecation(pr verdict.PlanResult, usedPlanText bool) verdict.PlanResult {
	if !usedPlanText {
		return pr
	}
	f := verdict.Finding{
		Severity:   verdict.SeverityMinor,
		Category:   verdict.CategoryOther,
		Criterion:  "input",
		Evidence:   "plan_text was supplied; it is deprecated and will be removed in 1.0.0",
		Suggestion: "pass plan_path with the absolute path to the plan file instead",
	}
	pr.PlanFindings = append([]verdict.Finding{f}, pr.PlanFindings...)
	return pr
}
```

- [ ] **Step 5: Fix the too-large labels**

```go
			Evidence:   fmt.Sprintf("payload %d bytes > cap %d (plan: %d, project_knowledge: %d)", total, limit, planBytes, pkBytes),
			Suggestion: "Split the plan into smaller chunks or pass a unified diff.",
		}},
		Tasks:      []verdict.PlanTaskResult{},
		NextAction: "Reduce plan size and retry.",
```

Update the existing assertion in `handlers_plan_test.go` from `"plan_text:"` to `"plan:"`.

- [ ] **Step 6: Wire resolution into ValidatePlan**

Replace the opening `if args.PlanText == ""` guard with:

```go
	if args.PlanText == "" && args.PlanPath == "" {
		return nil, verdict.PlanResult{}, errors.New("plan_text or plan_path is required")
	}
	if args.PlanText != "" && args.PlanPath != "" {
		return nil, verdict.PlanResult{}, errors.New("plan_text and plan_path are mutually exclusive")
	}
```

Immediately after the `effectiveMaxTokens` call and the `projectKnowledge` / `pkBytes` assignments, insert:

```go
	planText := args.PlanText
	var planSrc fileSource
	if args.PlanPath != "" {
		var rerr error
		planText, planSrc, rerr = resolveFileInput(
			args.PlanPath, h.deps.Cfg.PlanRoots, h.deps.Cfg.PlanMaxPayloadBytes)
		if errors.Is(rerr, errTooLarge) {
			total := planSrc.Bytes + pkBytes
			pr := prependPlanClamp(
				tooLargePlanResult(total, planSrc.Bytes, pkBytes, h.deps.Cfg.PlanMaxPayloadBytes), clamp)
			h.recordStat(statParams{
				tool:         "validate_plan",
				verdict:      string(pr.PlanVerdict),
				findings:     planFindings(pr),
				modelUsed:    h.deps.Cfg.PlanModel.String(),
				payloadBytes: total,
			})
			return planEnvelopeResult(pr, h.deps.Cfg.PlanModel.String(), 0)
		}
		if rerr != nil {
			return nil, verdict.PlanResult{}, rerr
		}
	}
```

Then, throughout the rest of `ValidatePlan`:
- replace every `args.PlanText` with `planText`
- replace `planBytes := len(args.PlanText)` with `planBytes := len(planText)`
- replace `h.deps.Cfg.MaxPayloadBytes` with `h.deps.Cfg.PlanMaxPayloadBytes` (this handler only)

Finally, add the deprecation to both result paths. After the existing `pr = prependPlanClamp(pr, clamp)`:

```go
	pr = prependPlanDeprecation(pr, args.PlanText != "")
```

and wrap the two early-exit envelopes (`noHeadingsPlanResult`, the cap rejection) the same way:

```go
		pr := prependPlanDeprecation(
			prependPlanClamp(noHeadingsPlanResult(), clamp), args.PlanText != "")
```

- [ ] **Step 7: Run tests**

Run: `go test -race ./internal/mcpsrv/ -run 'TestValidatePlan' -v`
Expected: PASS. If the cached-result test fails, confirm the cache key still sees `planText`.

- [ ] **Step 8: Commit**

```bash
git add internal/mcpsrv/handlers.go internal/mcpsrv/handlers_plan_test.go
git commit -m "feat(validate_plan): accept plan_path, deprecate plan_text, split payload cap"
```

---

### Task 4: Provenance line in the plan summary

**Goal:** When `plan_path` was used, `SummaryBlock` names the resolved path, byte count, and short hash — including on a cache hit, where it must name the *current* call's path.

**Files:**
- Modify: `internal/mcpsrv/summary.go` (`formatPlanSummary`)
- Modify: `internal/mcpsrv/handlers.go` (`finalizePlanResult`, `planEnvelopeResult`, `planEnvelopeResultFinalized`, call sites)
- Modify: `internal/mcpsrv/plan_cache.go` (`lookup`)
- Test: `internal/mcpsrv/summary_test.go`, `internal/mcpsrv/handlers_plan_test.go`

**Acceptance Criteria:**
- [ ] `formatPlanSummary` takes a `planSummaryMeta` struct rather than three scalars
- [ ] With `plan_path`, the summary contains `source: <resolved path> (<n> B, sha256 <first 8 hex>…)` after `plan_run_id`
- [ ] With `plan_text`, no `source:` line appears at all
- [ ] A cache hit renders the **current** call's provenance, not the stored entry's
- [ ] No field is added to `verdict.PlanResult`; `plan_schema.json` is unchanged

**Verify:** `go test -race ./internal/mcpsrv/ -run 'TestFormatPlanSummary|TestValidatePlanProvenance' -v` → PASS

**Steps:**

- [ ] **Step 1: Write the failing tests**

```go
func TestFormatPlanSummarySource(t *testing.T) {
	pr := verdict.PlanResult{PlanVerdict: verdict.VerdictPass, PlanQuality: "good"}

	t.Run("with source", func(t *testing.T) {
		out := formatPlanSummary(pr, planSummaryMeta{
			ModelUsed: "anthropic:claude-sonnet-4-6",
			ReviewMS:  1200,
			Source:    "/abs/plan.md (170158 B, sha256 4f2a9c1e…)",
		})
		assert.Contains(t, out, "source:")
		assert.Contains(t, out, "/abs/plan.md (170158 B, sha256 4f2a9c1e…)")
	})

	t.Run("without source", func(t *testing.T) {
		out := formatPlanSummary(pr, planSummaryMeta{ModelUsed: "m", ReviewMS: 1})
		assert.NotContains(t, out, "source:")
	})
}

func TestValidatePlanProvenanceOnCacheHit(t *testing.T) {
	planMD := "# P\n\n### Task 1: T\n\n**Goal:** g\n\n**Acceptance criteria:**\n- [ ] a\n"
	dirA, dirB := t.TempDir(), t.TempDir()
	pa := filepath.Join(dirA, "plan.md")
	pb := filepath.Join(dirB, "plan.md")
	require.NoError(t, os.WriteFile(pa, []byte(planMD), 0o644))
	require.NoError(t, os.WriteFile(pb, []byte(planMD), 0o644)) // identical content

	h := newTestPlanHandlers(t)
	_, first, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanPath: pa})
	require.NoError(t, err)
	require.Equal(t, verdict.VerdictPass, first.PlanVerdict, "must pass so it is cached")

	_, second, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanPath: pb})
	require.NoError(t, err)
	assert.Contains(t, second.SummaryBlock, "[cached", "identical content hits the cache")
	assert.Contains(t, second.SummaryBlock, dirB, "echoes the CURRENT call's path")
	assert.NotContains(t, second.SummaryBlock, dirA, "must not echo the cached entry's path")
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/mcpsrv/ -run 'TestFormatPlanSummarySource|TestValidatePlanProvenance' -v`
Expected: FAIL — `planSummaryMeta` undefined

- [ ] **Step 3: Introduce the meta struct**

In `summary.go`:

```go
// planSummaryMeta bundles the non-PlanResult inputs to formatPlanSummary.
// Carried on a struct rather than as scalars so the signature stays narrow
// (1 arg vs. 4) and matches CodeScene's "max arguments = 4" threshold; mirrors
// the renderPlanReviewInputs / planReviewErrInputs pattern.
//
// Source is the pre-rendered provenance string, empty when plan_text was used.
// It is passed per-call rather than stored on the cache entry: planPassCacheKey
// hashes content, so two different paths holding identical plans share an
// entry, and echoing the stored path would name an earlier caller's file.
type planSummaryMeta struct {
	ModelUsed string
	ReviewMS  int64
	Source    string
}

func formatPlanSummary(pr verdict.PlanResult, meta planSummaryMeta) string {
	var b strings.Builder
	b.WriteString("anti-tangent envelope (validate_plan)\n")
	fmt.Fprintf(&b, "  plan_verdict:  %s\n", pr.PlanVerdict)
	fmt.Fprintf(&b, "  plan_quality:  %s\n", pr.PlanQuality)
	if pr.PlanRunID != "" {
		fmt.Fprintf(&b, "  plan_run_id:   %s\n", pr.PlanRunID)
	}
	if meta.Source != "" {
		fmt.Fprintf(&b, "  source:        %s\n", meta.Source)
	}
	if pr.Partial {
		b.WriteString("  partial:       true\n")
	}
	fmt.Fprintf(&b, "  model_used:    %s\n", meta.ModelUsed)
	fmt.Fprintf(&b, "  review_ms:     %d\n", meta.ReviewMS)
	// ... rest of the function unchanged, with modelUsed -> meta.ModelUsed
	// and reviewMS -> meta.ReviewMS
```

- [ ] **Step 4: Add the provenance renderer**

In `file_source.go`:

```go
// String renders the summary provenance line's value. Empty for a zero
// fileSource, so plan_text callers get no source line at all.
func (s fileSource) String() string {
	if s.Path == "" {
		return ""
	}
	short := s.SHA256
	if len(short) > 8 {
		short = short[:8]
	}
	return fmt.Sprintf("%s (%d B, sha256 %s…)", s.Path, s.Bytes, short)
}
```

- [ ] **Step 5: Thread it through the call chain**

```go
func planEnvelopeResult(pr verdict.PlanResult, meta planSummaryMeta) (*mcp.CallToolResult, verdict.PlanResult, error) {
	return planEnvelopeResultFinalized(finalizePlanResult(pr, meta), meta)
}

func finalizePlanResult(pr verdict.PlanResult, meta planSummaryMeta) verdict.PlanResult {
	normalizePlanUnverifiableFindings(&pr)
	calibratePlanVerdictForUnverifiableOnly(&pr)
	verdict.FinalizePlanVerdict(&pr)
	pr.SummaryBlock = formatPlanSummary(pr, meta)
	return pr
}

func planEnvelopeResultFinalized(pr verdict.PlanResult, meta planSummaryMeta) (*mcp.CallToolResult, verdict.PlanResult, error) {
	body, err := json.MarshalIndent(struct {
		verdict.PlanResult
		ModelUsed string `json:"model_used"`
		ReviewMS  int64  `json:"review_ms"`
	}{pr, meta.ModelUsed, meta.ReviewMS}, "", "  ")
	if err != nil {
		return nil, verdict.PlanResult{}, err
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(body)}},
	}, pr, nil
}
```

Update every call site in `ValidatePlan` to build `planSummaryMeta{ModelUsed: …, ReviewMS: …, Source: planSrc.String()}`. Also update the `pr.SummaryBlock = formatPlanSummary(...)` line in the `PlanRunID` branch.

- [ ] **Step 6: Pass provenance into the cache lookup**

In `plan_cache.go`:

```go
func (c *planPassCache) lookup(key [32]byte, source string) (verdict.PlanResult, string, bool) {
	// ... unchanged through the TTL check ...
	pr := clonePlanResult(entry.result)
	pr.NextAction = "[cached <=3m] " + pr.NextAction
	pr.SummaryBlock = formatPlanSummary(pr, planSummaryMeta{
		ModelUsed: entry.modelUsed,
		ReviewMS:  0,
		Source:    source, // current call's provenance, never the entry's
	})
	return pr, entry.modelUsed, true
}
```

Update the call in `ValidatePlan` to `h.planCache().lookup(cacheKey, planSrc.String())`.

- [ ] **Step 7: Run the package**

Run: `go test -race ./internal/mcpsrv/...`
Expected: PASS. Fix any remaining `formatPlanSummary` / `finalizePlanResult` call sites the compiler flags.

- [ ] **Step 8: Commit**

```bash
git add internal/mcpsrv/
git commit -m "feat(validate_plan): report plan_path provenance in the summary block"
```

---

### Task 5: `validate_completion` path inputs

**Goal:** `final_files` entries may omit `content` (server reads `path`), and `final_diff_path` is accepted — with `checkEvidenceShape` running on the resolved content.

**Files:**
- Modify: `internal/mcpsrv/handlers.go` (`FileArg`, `ValidateCompletionArgs`, `validateCompletionTool`, `ValidateCompletion`)
- Test: `internal/mcpsrv/handlers_test.go`

**Acceptance Criteria:**
- [ ] `final_files` entry with `content` set behaves byte-identically to today
- [ ] `final_files` entry with `content` omitted is read from `path`
- [ ] `final_diff` and `final_diff_path` together → error `final_diff and final_diff_path are mutually exclusive`
- [ ] `final_diff_path` content reaches the reviewer exactly as inline `final_diff` would
- [ ] **`checkEvidenceShape` rejects a file whose contents contain `// snip` or a bare `...` line**, reached via either path
- [ ] Resolution uses the shared `MaxPayloadBytes`, never `PlanMaxPayloadBytes`
- [ ] The at-least-one-evidence guard counts `final_diff_path` as evidence

**Verify:** `go test -race ./internal/mcpsrv/ -run 'TestValidateCompletion' -v` → PASS

**Steps:**

- [ ] **Step 1: Write the failing tests**

```go
func TestValidateCompletionPathInputs(t *testing.T) {
	dir := t.TempDir()

	t.Run("final_files without content is read from disk", func(t *testing.T) {
		p := filepath.Join(dir, "impl.go")
		require.NoError(t, os.WriteFile(p, []byte("package x\n\nfunc F() int { return 1 }\n"), 0o644))

		h := newTestHandlers(t)
		_, env, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
			Summary:    "did the thing",
			FinalFiles: []FileArg{{Path: p}},
		})
		require.NoError(t, err)
		assert.NotEqual(t, verdict.VerdictFail, verdict.Verdict(env.Verdict))
	})

	t.Run("mutually exclusive diff inputs", func(t *testing.T) {
		h := newTestHandlers(t)
		_, _, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
			Summary: "s", FinalDiff: "diff --git a b", FinalDiffPath: "/tmp/x.diff",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "mutually exclusive")
	})

	t.Run("final_diff_path counts as evidence", func(t *testing.T) {
		p := filepath.Join(dir, "change.diff")
		require.NoError(t, os.WriteFile(p, []byte("diff --git a/x b/x\n@@ -1 +1 @@\n-a\n+b\n"), 0o644))

		h := newTestHandlers(t)
		_, _, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
			Summary: "s", FinalDiffPath: p,
		})
		require.NoError(t, err, "must not trip the at-least-one-evidence guard")
	})

	// The regression that matters most: a path must not become a way around
	// the truncation guard.
	t.Run("evidence shape guard runs on resolved content", func(t *testing.T) {
		for name, body := range map[string]string{
			"snip":     "package x\n// snip\nfunc F() {}\n",
			"ellipsis": "package x\n...\nfunc F() {}\n",
		} {
			t.Run(name, func(t *testing.T) {
				p := filepath.Join(t.TempDir(), "trunc.go")
				require.NoError(t, os.WriteFile(p, []byte(body), 0o644))

				h := newTestHandlers(t)
				_, env, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
					Summary:    "s",
					FinalFiles: []FileArg{{Path: p}},
				})
				require.NoError(t, err)
				assert.Equal(t, string(verdict.VerdictFail), env.Verdict,
					"truncated evidence via a path must be rejected exactly as inline would be")
			})
		}
	})
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/mcpsrv/ -run TestValidateCompletionPathInputs -v`
Expected: FAIL — unknown field `FinalDiffPath`

- [ ] **Step 3: Widen the arg types**

```go
type FileArg struct {
	Path    string `json:"path"`
	Content string `json:"content,omitempty"` // omitted -> server reads Path
}
```

Add to `ValidateCompletionArgs`, after `FinalDiff`:

```go
	FinalDiffPath         string            `json:"final_diff_path,omitempty"`
```

In `validateCompletionTool()`, append to the description:

```go
			"Omit a final_files entry's content to have the server read its absolute path, and pass final_diff_path instead of final_diff, to avoid emitting large evidence as output tokens. " +
```

- [ ] **Step 4: Resolve before every guard**

In `ValidateCompletion`, replace the at-least-one-evidence guard and insert resolution immediately after the `args.Summary` check:

```go
	if args.FinalDiff != "" && args.FinalDiffPath != "" {
		return nil, Envelope{}, errors.New("final_diff and final_diff_path are mutually exclusive")
	}
	if len(args.FinalFiles) == 0 && args.FinalDiff == "" && args.FinalDiffPath == "" && args.TestEvidence == "" {
		return nil, Envelope{}, errors.New("validate_completion: at least one of final_files, final_diff, or test_evidence must be non-empty")
	}

	// Resolve path inputs BEFORE the payload cap, the evidence-shape guard,
	// and the evidence cache key. checkEvidenceShape must see resolved
	// content: otherwise a caller could bypass the truncation guard entirely
	// by passing a path to a file full of elided content. See design §2.1.
	if err := h.resolveCompletionInputs(&args); err != nil {
		return nil, Envelope{}, err
	}
```

Add the helper next to `checkEvidenceShape`:

```go
// resolveCompletionInputs fills in FinalDiff and each FinalFiles[].Content
// from disk for entries that supplied only a path. Mutates args in place so
// every later stage — payload cap, evidence-shape guard, evidence cache key,
// prompt render — sees fully materialized evidence.
//
// Uses the shared MaxPayloadBytes, never PlanMaxPayloadBytes: completion
// evidence did not gain the headroom validate_plan did.
func (h *handlers) resolveCompletionInputs(args *ValidateCompletionArgs) error {
	cap := h.deps.Cfg.MaxPayloadBytes
	roots := h.deps.Cfg.PlanRoots

	if args.FinalDiffPath != "" {
		content, _, err := resolveFileInput(args.FinalDiffPath, roots, cap)
		if err != nil {
			return fmt.Errorf("final_diff_path: %w", err)
		}
		args.FinalDiff = content
	}
	for i := range args.FinalFiles {
		f := &args.FinalFiles[i]
		if f.Path == "" || f.Content != "" {
			continue
		}
		content, _, err := resolveFileInput(f.Path, roots, cap)
		if err != nil {
			return fmt.Errorf("final_files[%d].path: %w", i, err)
		}
		f.Content = content
	}
	return nil
}
```

- [ ] **Step 5: Run tests**

Run: `go test -race ./internal/mcpsrv/ -run 'TestValidateCompletion' -v`
Expected: PASS, including both evidence-shape subtests

- [ ] **Step 6: Commit**

```bash
git add internal/mcpsrv/handlers.go internal/mcpsrv/handlers_test.go
git commit -m "feat(validate_completion): accept file paths for final_files and final_diff"
```

---

### Task 6: Split the plan prompt into cacheable prefix + per-call suffix

**Goal:** `prompts.Output` exposes the already-identical shared head (through the plan text) separately from the per-call instructions, and the plan-pass cache key covers both halves.

**Files:**
- Modify: `internal/prompts/prompts.go` (`Output`, `RenderPlanFindingsOnly`, `RenderPlanTasksChunk`)
- Modify: `internal/mcpsrv/plan_cache.go` (`planCachePrompt`, `planPassCacheVersion`)
- Modify: `internal/mcpsrv/handlers.go` (`renderedPlanReview.cachePrompts`)
- Test: `internal/prompts/prompts_test.go`, `internal/mcpsrv/handlers_plan_test.go`

**Acceptance Criteria:**
- [ ] `Output` gains `UserPrefix` / `UserSuffix`; `User` remains the full concatenation so no existing consumer changes
- [ ] For the two chunked templates, `UserPrefix` ends with the plan text and `UserSuffix` starts at `## What to evaluate`
- [ ] `UserPrefix` is byte-identical between `RenderPlanFindingsOnly` and `RenderPlanTasksChunk` for the same plan and project knowledge
- [ ] Every other renderer (`RenderPre`/`RenderMid`/`RenderPost`/`RenderPlan`/`RenderPrime`/`RenderExtract`) leaves `UserPrefix` empty and puts everything in `UserSuffix`
- [ ] `planCachePrompt` carries both halves and `planPassCacheVersion` is `plan-pass-cache-v2`
- [ ] A template edit confined to the per-call suffix changes the cache key
- [ ] Existing prompt goldens are unchanged (the split is structural, not textual)

**Verify:** `go test -race ./internal/prompts/... ./internal/mcpsrv/...` → PASS

**Steps:**

- [ ] **Step 1: Write the failing test**

```go
func TestPlanPromptSplit(t *testing.T) {
	in := PlanInput{PlanText: "# Plan\n\n### Task 1: T\n\nbody\n", Mode: "thorough"}
	tasks := []planparser.RawTask{{Title: "Task 1: T", Body: "### Task 1: T\n\nbody\n"}}

	fo, err := RenderPlanFindingsOnly(in)
	require.NoError(t, err)
	ch, err := RenderPlanTasksChunk(PlanChunkInput{
		PlanText: in.PlanText, Mode: in.Mode, ChunkTasks: tasks,
	})
	require.NoError(t, err)

	t.Run("prefix is shared byte-for-byte", func(t *testing.T) {
		require.NotEmpty(t, fo.UserPrefix)
		assert.Equal(t, fo.UserPrefix, ch.UserPrefix,
			"the cache prefix must be identical or no cache read ever happens")
	})

	t.Run("prefix carries the plan, suffix carries the instructions", func(t *testing.T) {
		assert.Contains(t, fo.UserPrefix, "### Task 1: T")
		assert.NotContains(t, fo.UserPrefix, "## What to evaluate")
		assert.True(t, strings.HasPrefix(strings.TrimLeft(fo.UserSuffix, "\n"), "## What to evaluate"))
	})

	t.Run("User is the concatenation", func(t *testing.T) {
		assert.Equal(t, fo.UserPrefix+fo.UserSuffix, fo.User)
		assert.Equal(t, ch.UserPrefix+ch.UserSuffix, ch.User)
	})

	t.Run("single-call renderer does not split", func(t *testing.T) {
		single, err := RenderPlan(in)
		require.NoError(t, err)
		assert.Empty(t, single.UserPrefix, "single call must not get a breakpoint")
		assert.Equal(t, single.User, single.UserSuffix)
	})
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/prompts/ -run TestPlanPromptSplit -v`
Expected: FAIL — `UserPrefix` undefined

- [ ] **Step 3: Widen `Output` and add the splitter**

```go
type Output struct {
	System string
	// User is the full user prompt — always UserPrefix + UserSuffix.
	User string
	// UserPrefix is the cacheable shared head (reviewer ground rules, project
	// knowledge, and the plan itself), populated only for the chunked
	// validate_plan templates where several calls share it byte-for-byte.
	// Empty everywhere else, which is what keeps single-call renders from
	// paying a cache-write premium against zero reads.
	UserPrefix string
	// UserSuffix is the per-call remainder. Equal to User when UserPrefix is
	// empty.
	UserSuffix string
}

// planSuffixMarker is the first line of the per-call section in both chunked
// plan templates. Everything before it — ground rules, project knowledge, and
// the plan under review — is byte-identical across the findings-only call and
// every chunk call, which is exactly the prefix worth caching.
const planSuffixMarker = "## What to evaluate"

// splitPlanPrompt divides a rendered chunked-plan prompt at planSuffixMarker.
// If the marker is absent the whole body becomes the suffix, so a template
// edit that removes it degrades to today's uncached behavior rather than
// silently caching the wrong span.
func splitPlanPrompt(body string) Output {
	idx := strings.Index(body, planSuffixMarker)
	if idx < 0 {
		return Output{System: systemPrompt, User: body, UserSuffix: body}
	}
	return Output{
		System:     systemPrompt,
		User:       body,
		UserPrefix: body[:idx],
		UserSuffix: body[idx:],
	}
}
```

Add `"strings"` to the imports.

- [ ] **Step 4: Use it in the two chunked renderers**

```go
func RenderPlanTasksChunk(in PlanChunkInput) (Output, error) {
	body, err := render("plan_tasks_chunk.tmpl", in)
	if err != nil {
		return Output{}, err
	}
	return splitPlanPrompt(body), nil
}

func RenderPlanFindingsOnly(in PlanInput) (Output, error) {
	body, err := render("plan_findings_only.tmpl", in)
	if err != nil {
		return Output{}, err
	}
	return splitPlanPrompt(body), nil
}
```

- [ ] **Step 5: Make every other renderer fill `UserSuffix`**

For `RenderPre`, `RenderMid`, `RenderPost`, `RenderPlan`, `RenderPrime`, `RenderExtract`, change the return to:

```go
	return Output{System: systemPrompt, User: body, UserSuffix: body}, nil
```

- [ ] **Step 6: Extend the cache key**

In `plan_cache.go`:

```go
	planPassCacheVersion = "plan-pass-cache-v2"
```

```go
type planCachePrompt struct {
	System     string `json:"system"`
	UserPrefix string `json:"user_prefix"`
	UserSuffix string `json:"user_suffix"`
}
```

In `handlers.go`, update `cachePrompts()`:

```go
func (r renderedPlanReview) cachePrompts() []planCachePrompt {
	toPrompt := func(o prompts.Output) planCachePrompt {
		return planCachePrompt{System: o.System, UserPrefix: o.UserPrefix, UserSuffix: o.UserSuffix}
	}
	if r.Single != nil {
		return []planCachePrompt{toPrompt(*r.Single)}
	}
	out := make([]planCachePrompt, 0, 1+len(r.Chunks))
	if r.FindingsOnly != nil {
		out = append(out, toPrompt(*r.FindingsOnly))
	}
	for _, chunk := range r.Chunks {
		out = append(out, toPrompt(chunk.Prompt))
	}
	return out
}
```

- [ ] **Step 7: Run tests and check goldens**

Run: `go test -race ./internal/prompts/... ./internal/mcpsrv/...`
Expected: PASS with **no** golden churn — the split is structural. If a golden diff appears, the template text was changed by mistake; revert it rather than running `-update`.

- [ ] **Step 8: Commit**

```bash
git add internal/prompts/ internal/mcpsrv/plan_cache.go internal/mcpsrv/handlers.go
git commit -m "feat(prompts): split chunked plan prompts into cacheable prefix and suffix"
```

---

### Task 7: Prompt caching on the Anthropic client

**Goal:** The chunked path marks the shared prefix `cache_control: ephemeral`, so chunk calls read it at ~0.1× instead of re-billing the full plan.

**Files:**
- Modify: `internal/providers/reviewer.go` (`Request`, `Response`)
- Modify: `internal/providers/anthropic.go`
- Modify: `internal/providers/openai.go`, `internal/providers/google.go`
- Modify: `internal/mcpsrv/handlers.go` (`reviewPlanSingle`, `reviewPlanChunked`)
- Test: `internal/providers/anthropic_test.go`, `internal/providers/openai_test.go`, `internal/providers/google_test.go`

**Acceptance Criteria:**
- [ ] `Request` gains `CachePrefix`; `Response` gains `CacheReadInputTokens` / `CacheCreationInputTokens`
- [ ] With `CachePrefix` empty, the Anthropic request body is byte-identical to today (`content` is a plain string)
- [ ] With `CachePrefix` set, `content` is two text blocks and only the first carries `cache_control: {"type":"ephemeral"}`
- [ ] The concatenation of the two blocks equals `CachePrefix + User`
- [ ] OpenAI and Google send `CachePrefix + User` as one string, byte-identical to today
- [ ] `reviewPlanChunked` sets `CachePrefix`; `reviewPlanSingle` leaves it empty
- [ ] The malformed-JSON retry appends `RetryHint()` to the suffix, leaving `CachePrefix` untouched

**Verify:** `go test -race ./internal/providers/... ./internal/mcpsrv/...` → PASS

**Steps:**

- [ ] **Step 1: Write the failing tests**

```go
func TestAnthropicCachePrefix(t *testing.T) {
	capture := func(t *testing.T, req Request) map[string]any {
		t.Helper()
		var got map[string]any
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.NoError(t, json.NewDecoder(r.Body).Decode(&got))
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"model":"m","stop_reason":"tool_use",
				"content":[{"type":"tool_use","input":{"ok":true}}],
				"usage":{"input_tokens":10,"output_tokens":2,
				         "cache_read_input_tokens":7,"cache_creation_input_tokens":3}}`))
		}))
		defer srv.Close()
		_, err := NewAnthropic("k", srv.URL, 5*time.Second).Review(context.Background(), req)
		require.NoError(t, err)
		return got
	}

	base := Request{Model: "m", System: "sys", User: "tail", MaxTokens: 100, JSONSchema: []byte(`{"type":"object"}`)}

	t.Run("no prefix keeps a plain string content", func(t *testing.T) {
		got := capture(t, base)
		msgs := got["messages"].([]any)
		content := msgs[0].(map[string]any)["content"]
		assert.Equal(t, "tail", content, "unchanged wire shape when caching is off")
	})

	t.Run("prefix produces two blocks with cache_control on the first", func(t *testing.T) {
		req := base
		req.CachePrefix = "head"
		got := capture(t, req)

		blocks := got["messages"].([]any)[0].(map[string]any)["content"].([]any)
		require.Len(t, blocks, 2)

		first := blocks[0].(map[string]any)
		assert.Equal(t, "head", first["text"])
		assert.Equal(t, map[string]any{"type": "ephemeral"}, first["cache_control"])

		second := blocks[1].(map[string]any)
		assert.Equal(t, "tail", second["text"])
		assert.NotContains(t, second, "cache_control", "only the prefix is a breakpoint")
	})

	t.Run("cache usage is surfaced", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"model":"m","stop_reason":"tool_use",
				"content":[{"type":"tool_use","input":{"ok":true}}],
				"usage":{"input_tokens":10,"output_tokens":2,
				         "cache_read_input_tokens":7,"cache_creation_input_tokens":3}}`))
		}))
		defer srv.Close()
		resp, err := NewAnthropic("k", srv.URL, 5*time.Second).Review(context.Background(), base)
		require.NoError(t, err)
		assert.Equal(t, 7, resp.CacheReadInputTokens)
		assert.Equal(t, 3, resp.CacheCreationInputTokens)
	})
}
```

Add the equivalent subtest to the OpenAI and Google tests, using each file's existing
body-capture helper:

```go
// internal/providers/openai_test.go
t.Run("cache prefix is concatenated, not blocked", func(t *testing.T) {
	req := base
	req.CachePrefix = "head"
	got := capture(t, req) // same captured-body map the file's other tests use

	msgs := got["messages"].([]any)
	user := msgs[len(msgs)-1].(map[string]any)
	assert.Equal(t, "headtail", user["content"])
})
```

```go
// internal/providers/google_test.go
t.Run("cache prefix is concatenated, not blocked", func(t *testing.T) {
	req := base
	req.CachePrefix = "head"
	got := capture(t, req)

	contents := got["contents"].([]any)
	parts := contents[len(contents)-1].(map[string]any)["parts"].([]any)
	assert.Equal(t, "headtail", parts[0].(map[string]any)["text"])
})
```

If those files have no `capture` helper yet, copy the `httptest`-based one from the Anthropic
test above and adapt the canned response body to each provider's shape — do not introduce a
shared cross-provider helper, since the three clients' wire formats are deliberately separate.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/providers/ -run 'CachePrefix' -v`
Expected: FAIL — unknown field `CachePrefix`

- [ ] **Step 3: Widen the provider types**

```go
type Request struct {
	Model     string
	System    string
	// CachePrefix is a stable head shared byte-for-byte across several calls.
	// When non-empty the Anthropic client sends it as its own content block
	// marked cache_control: ephemeral, so later calls read it at ~0.1x input
	// price instead of re-billing it. Providers without a prefix-cache
	// concept concatenate it onto User. Set ONLY on the chunked validate_plan
	// path: on a single call a breakpoint is a 1.25x write against zero reads.
	CachePrefix string
	User        string
	MaxTokens   int
	JSONSchema  []byte
}

type Response struct {
	RawJSON                  []byte
	Model                    string
	InputTokens              int
	OutputTokens             int
	CacheReadInputTokens     int
	CacheCreationInputTokens int
}
```

- [ ] **Step 4: Emit content blocks in the Anthropic client**

Replace the `"messages"` entry in `body`:

```go
	var userContent any = req.User
	if req.CachePrefix != "" {
		userContent = []map[string]any{
			{
				"type":          "text",
				"text":          req.CachePrefix,
				"cache_control": map[string]any{"type": "ephemeral"},
			},
			{"type": "text", "text": req.User},
		}
	}

	body := map[string]any{
		"model":      req.Model,
		"max_tokens": req.MaxTokens,
		"system":     req.System,
		"messages": []map[string]any{
			{"role": "user", "content": userContent},
		},
		// ... tools / tool_choice unchanged
	}
```

Extend the usage struct and the returned `Response`:

```go
		Usage struct {
			InputTokens              int `json:"input_tokens"`
			OutputTokens             int `json:"output_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
		} `json:"usage"`
```

```go
	out := Response{
		RawJSON:                  []byte(raw),
		Model:                    parsed.Model,
		InputTokens:              parsed.Usage.InputTokens,
		OutputTokens:             parsed.Usage.OutputTokens,
		CacheReadInputTokens:     parsed.Usage.CacheReadInputTokens,
		CacheCreationInputTokens: parsed.Usage.CacheCreationInputTokens,
	}
```

- [ ] **Step 5: Concatenate in the other two clients**

`openai.go`:

```go
			{"role": "user", "content": req.CachePrefix + req.User},
```

`google.go`:

```go
			"parts": []map[string]string{{"text": req.CachePrefix + req.User}},
```

- [ ] **Step 6: Set the prefix only on the chunked path**

In `reviewPlanChunked`, where each `providers.Request` is built:

```go
	req := providers.Request{
		Model:       model.Model,
		System:      rendered.System,
		CachePrefix: rendered.UserPrefix,
		User:        rendered.UserSuffix,
		MaxTokens:   maxTokens,
		JSONSchema:  verdict.PlanSchema(),
	}
```

and on the retry inside that function, append to the suffix only:

```go
		req.User = rendered.UserSuffix + "\n\n" + verdict.RetryHint()
```

Leave `reviewPlanSingle` exactly as it is — it keeps `User: rendered.User` and no `CachePrefix`.

- [ ] **Step 7: Add the e2e cache-hit proof**

Unit tests prove the *request shape*; only a real call proves the cache is actually read. Add to
the existing `-tags=e2e` suite:

```go
//go:build e2e

// TestPlanCachingReadsPrefixE2E sends a plan large enough to clear the
// minimum cacheable prefix and asserts a later chunk call reads it back.
// Without this the caching change is unfalsifiable: the request looks right
// and the bill is unchanged.
func TestPlanCachingReadsPrefixE2E(t *testing.T) {
	plan := buildPlanWithNTasks(9) // 9 > default PlanTasksPerChunk 8 -> chunked
	rec := &usageRecordingReviewer{inner: realAnthropicReviewer(t)}

	d := newDeps(t, rec)
	d.Cfg.PlanTasksPerChunk = 8
	d.Reviews = providers.Registry{"anthropic": rec}
	h := &handlers{deps: d}

	_, _, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: plan})
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(rec.usages), 2, "chunked path makes >= 2 calls")

	assert.Greater(t, rec.usages[0].CacheCreationInputTokens, 0, "first call writes the prefix")
	assert.Greater(t, rec.usages[1].CacheReadInputTokens, 0, "second call reads it back")
}
```

`usageRecordingReviewer` is a thin `providers.Reviewer` wrapper appending each `Response` to a
slice. If the plan is too small to meet the provider's minimum cacheable prefix, the write is
silently skipped and this test fails with zeroes — grow `buildPlanWithNTasks` output rather than
weakening the assertion.

- [ ] **Step 8: Run the suite**

Run: `go test -race ./...`
Expected: PASS

Run (optional, costs real API calls): `go test -tags=e2e ./internal/mcpsrv/ -run TestPlanCachingReadsPrefixE2E -v`
Expected: PASS with non-zero cache creation on call 1 and non-zero cache read on call 2

- [ ] **Step 9: Commit**

```bash
git add internal/providers/ internal/mcpsrv/
git commit -m "feat(providers): cache the shared plan prefix on chunked reviewer calls"
```

---

### Task 8: Docs, protocol, and CHANGELOG

**Goal:** The protocol tells controllers and implementers to use paths, the README documents the new vars and the trust model, and the prior rejection is not left silently contradicted.

**Files:**
- Modify: `docs/protocol/controller.md`
- Modify: `docs/protocol/implementer.md`
- Modify: `plugin/anti-tangent-protocol/protocol/*.md` (resync, not hand-edit)
- Modify: `README.md`
- Modify: `docs/superpowers/specs/2026-05-14-review-noise-and-evidence-context-design.md`
- Modify: `docs/superpowers/specs/2026-05-07-anti-tangent-mcp-design.md`
- Modify: `CHANGELOG.md`

**Acceptance Criteria:**
- [ ] `controller.md` §5.1 says to prefer `plan_path` with an absolute path when the plan is on disk
- [ ] `implementer.md` says to omit `final_files[].content` and prefer `final_diff_path`
- [ ] `plugin/anti-tangent-protocol/protocol/` is byte-identical to `docs/protocol/`
- [ ] Every protocol part is under 16,000 bytes and `INTEGRATION.md` is under 2,000
- [ ] README documents `ANTI_TANGENT_PLAN_ROOTS` and `ANTI_TANGENT_PLAN_MAX_PAYLOAD_BYTES` plus the §3.2 trust model
- [ ] The `plan_text_from_file` out-of-scope line in the 2026-05-14 spec points at the new design's §8
- [ ] `CHANGELOG.md` has a `## [0.16.0]` block with `### Added`, `### Changed`, and `### Deprecated`

**Verify:** `go test -race ./... && bash -c 'diff -r docs/protocol plugin/anti-tangent-protocol/protocol && for f in docs/protocol/*.md; do s=$(wc -c <"$f"); [ "$s" -lt 16000 ] || { echo "TOO BIG: $f $s"; exit 1; }; done && echo OK'` → `OK`

**Steps:**

- [ ] **Step 1: Update the controller protocol**

In `docs/protocol/controller.md`, replace the §5.1 wording *"call `validate_plan` once with the full plan markdown"* with:

```markdown
Before executing a multi-task plan — whether you implement it yourself or dispatch to
subagents — **call `validate_plan` once, passing `plan_path` with the absolute path to the
plan file**. The server reads it, so plan size costs you no output tokens and the reviewer is
guaranteed to see the same document your subagents will. Use `plan_text` only when the plan is
not on disk; it is deprecated and will be removed in 1.0.0.
```

Update the numbered steps in the same section that say "with the full plan markdown" to say "with `plan_path`".

- [ ] **Step 2: Update the implementer protocol**

In `docs/protocol/implementer.md`, in the `validate_completion` evidence guidance, add:

```markdown
- Prefer paths over inline content: omit a `final_files` entry's `content` and the server reads
  its absolute `path`, and pass `final_diff_path` instead of `final_diff` (write it first with
  `git diff > /tmp/change.diff`). Truncation checks still apply to the resolved content — a file
  containing `// snip` or a bare `...` line is rejected exactly as inline evidence would be.
```

- [ ] **Step 3: Resync the plugin bundle**

```bash
rm -f plugin/anti-tangent-protocol/protocol/*.md
cp docs/protocol/*.md plugin/anti-tangent-protocol/protocol/
diff -r docs/protocol plugin/anti-tangent-protocol/protocol && echo "in sync"
for f in docs/protocol/*.md; do echo "$(wc -c <"$f") $f"; done
```

- [ ] **Step 4: Update the README**

Add to the dotenv block:

```bash
# validate_plan file-path input (design 2026-08-31)
ANTI_TANGENT_PLAN_MAX_PAYLOAD_BYTES=1048576   # plan content cap; other tools keep MAX_PAYLOAD_BYTES
ANTI_TANGENT_PLAN_ROOTS=                      # colon-separated absolute roots; empty = unrestricted
```

And a prose paragraph:

```markdown
### File-path inputs and the trust model

`validate_plan` accepts `plan_path`, and `validate_completion` accepts `final_diff_path` plus
`final_files` entries with no `content`. The server reads those files itself, so a large plan or
diff costs the calling agent no output tokens.

This means the server reads files on your filesystem and sends their contents to the reviewer
provider. It is stdio-only — the host spawns it as a child process, so it shares your container,
mounts, and uid, and the calling agent already has unrestricted file read. The server therefore
acquires no capability the caller lacks. If you want it narrower anyway, set
`ANTI_TANGENT_PLAN_ROOTS` to a colon-separated list of absolute directories; paths outside them
are refused. Symlinks are resolved before the check, so a link cannot escape a root.
```

- [ ] **Step 5: Annotate the superseded decision**

In `docs/superpowers/specs/2026-05-14-review-noise-and-evidence-context-design.md`, amend the out-of-scope bullet:

```markdown
- `plan_text_from_file`. MCP hosts vary in filesystem access; direct server-side file reads would
  change the deployment/security model.
  **Superseded in 0.16.0** — see `2026-08-31-plan-path-design.md` §8, which addresses both
  premises and accepts the deployment-model change explicitly.
```

In `docs/superpowers/specs/2026-05-07-anti-tangent-mcp-design.md`, note in the Architecture section that `validate_plan` and `validate_completion` read from disk when given path inputs.

- [ ] **Step 6: Write the CHANGELOG entry**

```markdown
## [0.16.0] - 2026-08-31

### Added
- **`plan_path` on `validate_plan`.** Pass an absolute path and the server reads the plan itself.
  A plan large enough to exceed the caller's max-output-tokens setting was previously
  unsubmittable at any token budget, because `plan_text` had to be emitted as part of the
  calling model's own tool-call output. Reading from disk also guarantees the reviewer sees the
  same document the implementing subagents will.
- **Path inputs on `validate_completion`.** Omit a `final_files` entry's `content` to have the
  server read its `path`, or pass `final_diff_path` instead of `final_diff`. Truncation checks
  run on the resolved content, so a path is not a way around the evidence-shape guard.
- **`ANTI_TANGENT_PLAN_ROOTS`** — colon-separated absolute directories that file-path inputs may
  be read from. Empty (the default) is unrestricted.
- **`ANTI_TANGENT_PLAN_MAX_PAYLOAD_BYTES`** (default 1MB) — payload cap for `validate_plan` only.
  The other tools keep the shared 200KB `ANTI_TANGENT_MAX_PAYLOAD_BYTES`.
- **Reviewer-side prompt caching on the chunked plan path.** The findings-only call and every
  chunk call share a byte-identical prefix through the plan text, now marked as an Anthropic
  cache breakpoint. Cuts input tokens on a chunked round by roughly half. Single-call plans are
  deliberately not cached — a breakpoint there is a write premium against zero reads.

### Changed
- The `validate_plan` too-large finding reports `plan:` rather than `plan_text:`, since the
  content may have arrived via `plan_path`.
- When `plan_path` is used, the summary block gains a `source:` line naming the resolved path,
  byte count, and content hash, so a controller can show which document cleared the gate.

### Deprecated
- **`plan_text` on `validate_plan`.** Still fully functional, now reporting one `minor` finding
  pointing at `plan_path`. It will be removed in 1.0.0.
```

- [ ] **Step 7: Verify everything**

```bash
go test -race ./...
diff -r docs/protocol plugin/anti-tangent-protocol/protocol && echo "protocol in sync"
for f in docs/protocol/*.md; do s=$(wc -c <"$f"); [ "$s" -lt 16000 ] || echo "TOO BIG: $f $s"; done
s=$(wc -c <INTEGRATION.md); [ "$s" -lt 2000 ] || echo "INTEGRATION.md TOO BIG: $s"
```

Expected: tests PASS, "protocol in sync", no size warnings.

- [ ] **Step 8: Commit**

```bash
git add docs/ plugin/ README.md CHANGELOG.md
git commit -m "docs: document path inputs, caching, and the 0.16.0 trust model"
```
