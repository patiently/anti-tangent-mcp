# Part 1 — Self-Describing Tool Surface Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Every constraint an agent needs to make a valid anti-tangent call is stated in the tool schema or in the response it gets back.

**Architecture:** Five independent server-side changes in `internal/`: actionable error text, a corrected placeholder guard, a `codescene` argument that accepts unknown keys and CodeScene's raw output, an advisory plus a ledger header that make plan-run attachment visible, and real JSON-schema descriptions on every tool input property enforced by a `tools/list` contract test.

**Tech Stack:** Go 1.25, `github.com/modelcontextprotocol/go-sdk` v1.6.0, `github.com/google/jsonschema-go` v0.4.3, testify.

**Spec:** `docs/superpowers/specs/2026-09-15-field-assessment-improvements-design.md` — Part 1, sections 1.1–1.5.

## Global Constraints

- `go test -race ./...` must pass after every task. Unit tests never touch the network.
- The server stays advisory. An advisory finding added by the server is appended after the verdict is finalized and must never change `verdict`.
- Comments follow `CLAUDE.md` § Comments: explain non-obvious behaviour or invariants in the present tense; no issue, PR, task or version references, no "previously"/"no longer"/"now". The `anti-tangent-guard` hook refuses such comments at write time.
- Struct-tag descriptions (`jsonschema:"…"`) contain no double quotes and no backticks, and never begin with a `WORD=` token (jsonschema-go rejects that prefix).
- `CHANGELOG.md` gets exactly one release entry for this work, `## [0.22.0] - 2026-09-15`, created in Task 1. Later tasks add lines under it, in Keep a Changelog subsection order: `### Added`, `### Changed`, `### Fixed`. Do not create a second heading.
- Do not edit the `VERSION` file; the release workflow bumps it.
- Any edit to `docs/protocol/*.md` is copied to `plugin/anti-tangent-protocol/protocol/` in the same commit, and every part stays under 16,000 bytes.
- This repository is public: no consumer-project identifiers in code, tests, comments, CHANGELOG or commit messages.

**User decisions (already made):**
- This work is version 0.22.0, developed on branch `version/0.22.0`.
- Part 1 merges to `main` when finished, with `[skip ci]` in the PR title; no release is cut until Part 3 lands.
- The 200 KB `validate_completion` payload cap stays as it is; only the recovery advice changes.
- The `codescene` argument accepts unknown keys and CodeScene's raw `analyze_change_set` output.
- The missing-`plan_run_id` signal is a post-finalization minor `other` finding naming the most recently created live plan run.

---

### Task 1: Actionable roots and payload_too_large errors

**Goal:** A path outside `ANTI_TANGENT_PLAN_ROOTS` and an oversized `validate_completion` payload both come back with advice that actually recovers the call.

**Files:**
- Modify: `internal/mcpsrv/file_source.go` (`resolveFileInput`, the `withinRoots` refusal)
- Modify: `internal/mcpsrv/handlers.go` (`validateCompletionTool` description; the two `validate_completion` `tooLargeEnvelope` suggestions; new `completionShrinkAdvice` const)
- Test: `internal/mcpsrv/file_source_test.go`, `internal/mcpsrv/handlers_test.go`
- Modify: `CHANGELOG.md`

**Acceptance Criteria:**
- [ ] Work happens on branch `version/0.22.0`, and `CHANGELOG.md` has a `## [0.22.0] - 2026-09-15` entry
- [ ] A read path outside `ANTI_TANGENT_PLAN_ROOTS` returns an error naming the roots and saying to pass a path inside the repository being worked in, noting a `/tmp` scratch directory is usually outside them
- [ ] Both `validate_completion` `payload_too_large` suggestions mention `-U1` and `ANTI_TANGENT_MAX_PAYLOAD_BYTES` and never contain the word `split`
- [ ] The `validate_completion` tool description states that path inputs must be under `ANTI_TANGENT_PLAN_ROOTS` when it is set

**Non-goals:**
- Do not change the 200 KB payload cap or split completion review across calls.
- Do not edit `VERSION`.

**Context:**
- This task modifies path-refusal and oversized-payload recovery text in `internal/mcpsrv` and creates the shared `CHANGELOG.md` release entry used by later tasks.
- Path inputs remain constrained by `ANTI_TANGENT_PLAN_ROOTS`; the implementation only makes that constraint and its recovery path explicit.

**Verify:** `go test -race ./internal/mcpsrv/ -run 'TestResolveFileInput|TestValidateCompletion_PayloadTooLargeSuggestionIsActionable|TestValidateCompletion_OversizedPathSuggestionIsActionable|TestValidateCompletionPathInputs_TooLarge|TestValidateCompletionTool_DescriptionStatesRootsRule' -v` → all PASS

**Steps:**

- [ ] **Step 1: Create the release branch and changelog entry**

```bash
git checkout -b version/0.22.0
```

In `CHANGELOG.md`, insert directly above the line `## [0.21.0] - 2026-09-11`:

```markdown
## [0.22.0] - 2026-09-15

### Changed

- A file path outside `ANTI_TANGENT_PLAN_ROOTS` is refused with a way to recover: pass a path
  inside the repository you are working in. A per-session scratch directory under `/tmp` is
  usually outside the roots, which is where a generated diff most often lands.
- `validate_completion`'s `payload_too_large` suggestion no longer advises splitting the
  evidence into smaller chunks. Each call is reviewed on its own, so evidence spread over several
  calls is never seen together. It now names a `-U1` diff, leaving out generated, lockfile and
  snapshot files, not sending a file in both `final_diff` and `final_files`, and
  `ANTI_TANGENT_MAX_PAYLOAD_BYTES`.

```

- [ ] **Step 2: Write the failing tests**

In `internal/mcpsrv/file_source_test.go`, inside `TestResolveFileInput`, add this subtest directly after the `"symlink escaping root rejected"` subtest:

```go
	t.Run("outside roots says how to recover", func(t *testing.T) {
		outside := t.TempDir()
		p := filepath.Join(outside, "evidence.diff")
		require.NoError(t, os.WriteFile(p, []byte("x"), 0o644))

		_, _, err := resolveFileInput(p, []string{dirResolved}, 1024)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "ANTI_TANGENT_PLAN_ROOTS")
		assert.Contains(t, err.Error(), dirResolved, "the error must name the configured roots")
		assert.Contains(t, err.Error(), "inside the repository you are working in")
		assert.Contains(t, err.Error(), "/tmp")
	})
```

In `internal/mcpsrv/handlers_test.go`, replace the whole function `TestValidateCompletion_PayloadTooLargeSuggestsFinalDiff` with:

```go
func TestValidateCompletion_PayloadTooLargeSuggestionIsActionable(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-opus-4-7")}
	d := newDeps(t, rv)
	d.Cfg.MaxPayloadBytes = 10
	h := &handlers{deps: d}

	_, pre, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{TaskTitle: "T", Goal: "G", AcceptanceCriteria: []string{"AC"}})
	require.NoError(t, err)

	_, env, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		SessionID:  pre.SessionID,
		Summary:    "implemented",
		FinalFiles: []CompletionFileArg{{Path: "f.go", Content: strPtr("this is way too much")}},
	})
	require.NoError(t, err)
	require.Len(t, env.Findings, 1)
	assert.Equal(t, "payload_too_large", string(env.Findings[0].Category))
	s := env.Findings[0].Suggestion
	assert.Contains(t, s, "final_diff")
	assert.Contains(t, s, "-U1")
	assert.Contains(t, s, "ANTI_TANGENT_MAX_PAYLOAD_BYTES")
	assert.NotContains(t, s, "split", "each call is reviewed alone, so splitting evidence across calls must not be advised")
	assert.Contains(t, env.Findings[0].Evidence, "bytes")
	assert.Contains(t, env.Findings[0].Evidence, "10")
}

func TestValidateCompletion_OversizedPathSuggestionIsActionable(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "big.diff")
	require.NoError(t, os.WriteFile(p, []byte(strings.Repeat("x", 100)), 0o644))

	h := newTestHandlers(t)
	h.deps.Cfg.MaxPayloadBytes = 10

	_, env, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		Summary:       "s",
		FinalDiffPath: p,
	})
	require.NoError(t, err)
	var s string
	for _, f := range env.Findings {
		if f.Category == verdict.CategoryTooLarge {
			s = f.Suggestion
		}
	}
	require.NotEmpty(t, s, "expected a payload_too_large finding, got %+v", env.Findings)
	assert.Contains(t, s, "final_diff_path is 100 bytes")
	assert.Contains(t, s, "-U1")
	assert.Contains(t, s, "ANTI_TANGENT_MAX_PAYLOAD_BYTES")
	assert.NotContains(t, s, "split")
}
```

In the same file, add:

```go
func TestValidateCompletionTool_DescriptionStatesRootsRule(t *testing.T) {
	d := validateCompletionTool().Description
	assert.Contains(t, d, "ANTI_TANGENT_PLAN_ROOTS")
	assert.Contains(t, d, "/tmp")
}
```

In the same file, inside `TestValidateCompletionPathInputs_TooLarge`, in the subtest `"path outside ANTI_TANGENT_PLAN_ROOTS stays a plain transport error"`, add after the existing `assert.Contains(t, err.Error(), "ANTI_TANGENT_PLAN_ROOTS")`:

```go
		assert.Contains(t, err.Error(), "inside the repository you are working in")
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test -race ./internal/mcpsrv/ -run 'TestResolveFileInput|TestValidateCompletion_PayloadTooLargeSuggestionIsActionable|TestValidateCompletion_OversizedPathSuggestionIsActionable|TestValidateCompletionPathInputs_TooLarge|TestValidateCompletionTool_DescriptionStatesRootsRule' -v`
Expected: FAIL — `"inside the repository you are working in"` not found, `"-U1"` not found, the suggestion contains `split`, and the tool description does not mention `ANTI_TANGENT_PLAN_ROOTS`.

- [ ] **Step 4: Implement**

In `internal/mcpsrv/file_source.go`, replace the `withinRoots` refusal in `resolveFileInput`:

```go
	if !withinRoots(resolved, roots) {
		return "", fileSource{}, fmt.Errorf(
			"%q is outside ANTI_TANGENT_PLAN_ROOTS (%s); pass a path under one of those roots, "+
				"for example inside the repository you are working in, since a per-session scratch directory under /tmp is usually outside them",
			resolved, strings.Join(roots, string(os.PathListSeparator)))
	}
```

In `internal/mcpsrv/handlers.go`, add this const directly above `func tooLargeEnvelope`:

```go
// completionShrinkAdvice is the recovery advice on validate_completion's
// payload_too_large finding. It must not suggest spreading evidence over
// several calls: each call is reviewed on its own, so the reviewer never sees
// split evidence together and answers every part with insufficient_evidence.
const completionShrinkAdvice = "Each call is reviewed on its own, so evidence spread over several calls is never seen together. " +
	"Regenerate the diff with -U1, leave out generated, lockfile and snapshot files, " +
	"and do not send a file in both final_diff and final_files. " +
	"The operator can raise the cap with ANTI_TANGENT_MAX_PAYLOAD_BYTES."
```

Replace the oversized-path suggestion (the `fmt.Sprintf` passed to `tooLargeEnvelope` in the `errors.As(err, &tooLarge)` branch of `ValidateCompletion`):

```go
				fmt.Sprintf("%s is %d bytes, over the %d-byte cap. %s",
					tooLarge.field, tooLarge.bytes, h.deps.Cfg.MaxPayloadBytes, completionShrinkAdvice)), clamp)
```

Replace the combined-payload suggestion in step 5 of `ValidateCompletion` (the literal `"Send a unified diff via final_diff, or split the call into smaller chunks."`):

```go
			"Send a unified diff via final_diff rather than whole files. "+completionShrinkAdvice), clamp)
```

In `validateCompletionTool`, replace the last description segment:

```go
			"Omit a final_files entry's content to have the server read its absolute path, and pass final_diff_path instead of final_diff, to avoid emitting large evidence as output tokens. " +
			"When ANTI_TANGENT_PLAN_ROOTS is set, both kinds of path must be under one of its roots, which a per-session scratch directory under /tmp usually is not.",
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test -race ./internal/mcpsrv/ -run 'TestResolveFileInput|TestValidateCompletion_PayloadTooLargeSuggestionIsActionable|TestValidateCompletion_OversizedPathSuggestionIsActionable|TestValidateCompletionPathInputs_TooLarge|TestValidateCompletionTool_DescriptionStatesRootsRule' -v`
Expected: PASS

Run: `go test -race ./...`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
gofmt -l internal/ && go vet ./internal/mcpsrv/
git add CHANGELOG.md internal/mcpsrv/file_source.go internal/mcpsrv/handlers.go internal/mcpsrv/file_source_test.go internal/mcpsrv/handlers_test.go
git commit -m "fix(mcpsrv): say how to recover from a roots refusal and an oversized completion"
```

```json:metadata
{"files": ["internal/mcpsrv/file_source.go", "internal/mcpsrv/handlers.go", "internal/mcpsrv/file_source_test.go", "internal/mcpsrv/handlers_test.go", "CHANGELOG.md"], "verifyCommand": "go test -race ./internal/mcpsrv/ -run 'TestResolveFileInput|TestValidateCompletion_PayloadTooLargeSuggestionIsActionable|TestValidateCompletion_OversizedPathSuggestionIsActionable|TestValidateCompletionPathInputs_TooLarge|TestValidateCompletionTool_DescriptionStatesRootsRule' -v", "acceptanceCriteria": ["on branch version/0.22.0 with a 0.22.0 CHANGELOG entry", "roots refusal names roots and a recovery path", "payload_too_large suggestions name -U1 and ANTI_TANGENT_MAX_PAYLOAD_BYTES without split", "validate_completion description states the roots rule"], "modelTier": "mechanical"}
```

---

### Task 2: Placeholder guard catches added `...` lines, not unchanged ones

**Goal:** `malformed_evidence` fires on an added bare `...` line and stops firing on unchanged or removed diff lines and on Python stub bodies.

**Files:**
- Modify: `internal/mcpsrv/handlers.go` (`checkEvidenceShape`; new `diffEllipsisPlaceholderOffset`, `ellipsisExemptPath`)
- Test: `internal/mcpsrv/handlers_test.go`
- Modify: `docs/protocol/core.md`, `plugin/anti-tangent-protocol/protocol/core.md`
- Modify: `CHANGELOG.md`

**Acceptance Criteria:**
- [ ] In a `final_diff` with hunk headers, a bare `...` on an unchanged (leading space) or removed (leading `-`) line is not flagged, and on an added line (leading `+`) it is
- [ ] A `final_diff` without hunk headers is still checked line by line
- [ ] A bare `...` line is exempt in a `.py`/`.pyi` file, both in `final_files` and as an added line under that file's `+++` header; other truncation markers are still rejected there
- [ ] `core.md` no longer advises passing a complete `final_diff` as a workaround; the plugin copy is identical and under 16,000 bytes

**Non-goals:**
- Do not exempt truncation markers other than a bare `...` line in Python files.
- Do not treat unchanged or removed unified-diff lines as newly supplied evidence.

**Context:**
- The guard must distinguish added, removed, and context lines only when hunk headers identify the input as a unified diff.
- Protocol documentation in `docs/protocol/core.md` must be copied to the plugin bundle and remain under 16,000 bytes.

**Verify:** `go test -race ./internal/mcpsrv/ -run 'TestCheckEvidenceShape' -v` → PASS; `diff -r docs/protocol plugin/anti-tangent-protocol/protocol && wc -c docs/protocol/core.md` → no diff, under 16000

**Steps:**

- [ ] **Step 1: Write the failing test**

In `internal/mcpsrv/handlers_test.go`, add directly after `TestCheckEvidenceShape_GoPackageRecursionAccepted`:

```go
func TestCheckEvidenceShape_EllipsisPlaceholderLine(t *testing.T) {
	goHunk := "diff --git a/x.go b/x.go\n--- a/x.go\n+++ b/x.go\n@@ -1,3 +1,3 @@\n"
	pyHunk := "diff --git a/s.py b/s.py\n--- a/s.py\n+++ b/s.py\n@@ -1,1 +1,2 @@\n def f():\n"
	cases := []struct {
		name   string
		diff   string
		files  []FileArg
		reject bool
	}{
		{name: "unchanged line in a diff", diff: goHunk + " ...\n+ok := true\n"},
		{name: "removed line in a diff", diff: goHunk + "-...\n+ok := true\n"},
		{name: "added line in a diff", diff: goHunk + "+...\n", reject: true},
		{name: "added indented line in a diff", diff: goHunk + "+    ...\n", reject: true},
		{name: "added stub line in a python diff", diff: pyHunk + "+    ...\n"},
		{name: "added line in a go file after a python file", diff: pyHunk + "+    ...\n" + goHunk + "+...\n", reject: true},
		{name: "other marker added in a python diff", diff: pyHunk + "+# (truncated)\n", reject: true},
		{name: "plain text evidence", diff: "header\n...\nmore", reject: true},
		{name: "python file stub", files: []FileArg{{Path: "pkg/stub.py", Content: "def f():\n    ...\n"}}},
		{name: "python interface stub", files: []FileArg{{Path: "pkg/stub.pyi", Content: "class C:\n    ...\n"}}},
		{name: "go file placeholder", files: []FileArg{{Path: "x.go", Content: "package x\n...\n"}}, reject: true},
		{name: "python file with another marker", files: []FileArg{{Path: "s.py", Content: "x = 1\n# (truncated)\n"}}, reject: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reason := checkEvidenceShape(tc.diff, tc.files)
			if tc.reject {
				assert.NotEmpty(t, reason, "must reject")
			} else {
				assert.Empty(t, reason, "must accept")
			}
		})
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -race ./internal/mcpsrv/ -run TestCheckEvidenceShape_EllipsisPlaceholderLine -v`
Expected: FAIL on `unchanged line in a diff`, `added line in a diff`, `added indented line in a diff`, `added line in a go file after a python file`, `python file stub`, `python interface stub`.

- [ ] **Step 3: Implement**

In `internal/mcpsrv/handlers.go`, add directly below the `evidenceEllipsisLine` var:

```go
// diffEllipsisPlaceholderOffset returns the byte offset of the first bare `...`
// line in finalDiff, or -1. In a unified diff (one with hunk headers) an
// unchanged or removed line carries no new evidence and is skipped, and an
// added line is checked with its `+` stripped; a bare `...` on an unchanged
// line is ordinary code, while on an added line it is elided evidence. Text
// with no hunk header is not a unified diff and is checked line by line as it
// stands. Lines under a `+++` header for a file ellipsisExemptPath accepts are
// not checked.
func diffEllipsisPlaceholderOffset(finalDiff string) int {
	if !strings.HasPrefix(finalDiff, "@@ ") && !strings.Contains(finalDiff, "\n@@ ") {
		if loc := evidenceEllipsisLine.FindStringIndex(finalDiff); loc != nil {
			return loc[0]
		}
		return -1
	}
	offset := 0
	exempt := false
	for _, line := range strings.SplitAfter(finalDiff, "\n") {
		body := strings.TrimRight(line, "\r\n")
		switch {
		case strings.HasPrefix(body, "+++ "):
			exempt = ellipsisExemptPath(strings.TrimPrefix(strings.TrimPrefix(body, "+++ "), "b/"))
		case strings.HasPrefix(body, " "), strings.HasPrefix(body, "-"):
		case strings.HasPrefix(body, "+"):
			if !exempt && strings.TrimSpace(body[1:]) == "..." {
				return offset
			}
		default:
			if !exempt && strings.TrimSpace(body) == "..." {
				return offset
			}
		}
		offset += len(line)
	}
	return -1
}

// ellipsisExemptPath reports whether a bare `...` line is legitimate content
// in the file at path: in Python, `...` is the idiomatic body of a stub.
func ellipsisExemptPath(path string) bool {
	switch strings.ToLower(filepath.Ext(strings.TrimSpace(path))) {
	case ".py", ".pyi":
		return true
	}
	return false
}
```

In `checkEvidenceShape`, replace the final_diff ellipsis check:

```go
		if off := diffEllipsisPlaceholderOffset(finalDiff); off >= 0 {
			return fmt.Sprintf("final_diff contains a placeholder line `...` at offset %d", off)
		}
```

and replace the final_files ellipsis check:

```go
		if !ellipsisExemptPath(f.Path) {
			if loc := evidenceEllipsisLine.FindStringIndex(f.Content); loc != nil {
				return fmt.Sprintf("final_files[%d].content (path %q) contains a placeholder line `...` at offset %d", i, f.Path, loc[0])
			}
		}
```

Add `"path/filepath"` to `handlers.go`'s import block (it is not imported there yet).

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/mcpsrv/ -run 'TestCheckEvidenceShape|TestValidateCompletionPathInputs' -v`
Expected: PASS (including the existing `TestCheckEvidenceShape_NewPatternsRejected` and `TestCheckEvidenceShape_GoPackageRecursionAccepted`)

- [ ] **Step 5: Fix the protocol advice and resync the bundle**

In `docs/protocol/core.md`, in the paragraph starting `**A `validate_completion` call returned `category: malformed_evidence`.**`, replace the sentence

`If a file legitimately contains one of these strings (a fixture or doc), pass a complete `final_diff` instead.`

with

`A bare `...` line is not flagged on a diff's unchanged or removed lines, or in a `.py`/`.pyi` file. The other markers are checked everywhere, `final_diff` included.`

Then:

```bash
rm -f plugin/anti-tangent-protocol/protocol/*.md
cp docs/protocol/*.md plugin/anti-tangent-protocol/protocol/
diff -r docs/protocol plugin/anti-tangent-protocol/protocol
wc -c docs/protocol/core.md
```

Expected: no diff output; `core.md` under 16000 bytes.

- [ ] **Step 6: CHANGELOG**

Under `## [0.22.0] - 2026-09-15`, add a `### Fixed` subsection after `### Changed` (create it if absent) with:

```markdown
- The evidence-shape guard's bare `...` placeholder check read a unified diff backwards: it
  flagged an unchanged ` ...` line and never an added `+...` line, which is the one that signals
  elided evidence. In a diff with hunk headers it now skips unchanged and removed lines and checks
  added lines with the `+` stripped. A bare `...` line in a `.py` or `.pyi` file, where it is the
  idiomatic stub body, is no longer flagged in either `final_files` or a diff.
```

- [ ] **Step 7: Commit**

```bash
gofmt -l internal/ && go test -race ./...
git add CHANGELOG.md internal/mcpsrv/handlers.go internal/mcpsrv/handlers_test.go docs/protocol/core.md plugin/anti-tangent-protocol/protocol/core.md
git commit -m "fix(mcpsrv): flag an added ... placeholder line, not an unchanged one"
```

```json:metadata
{"files": ["internal/mcpsrv/handlers.go", "internal/mcpsrv/handlers_test.go", "docs/protocol/core.md", "plugin/anti-tangent-protocol/protocol/core.md", "CHANGELOG.md"], "verifyCommand": "go test -race ./internal/mcpsrv/ -run 'TestCheckEvidenceShape' -v && diff -r docs/protocol plugin/anti-tangent-protocol/protocol", "acceptanceCriteria": ["unchanged and removed diff lines skipped, added lines checked", "non-diff text checked line by line", "python stubs exempt in files and diffs, other markers still rejected", "core.md advice fixed, bundle identical, under 16000 bytes"], "modelTier": "mechanical"}
```

---

### Task 3: `codescene` accepts unknown keys and CodeScene's raw output

**Goal:** An unexpected key in the optional `codescene` argument no longer rejects the whole `validate_completion` call, and CodeScene's raw `analyze_change_set` output is reduced to the digest server-side.

**Files:**
- Modify: `internal/codescene/codescene.go` (new `rawChangeSetResult` type and `(*Digest).UnmarshalJSON`)
- Test: `internal/codescene/codescene_test.go`
- Create: `internal/mcpsrv/completion_schema.go` (`validateCompletionInputSchema`)
- Modify: `internal/mcpsrv/handlers.go` (`validateCompletionTool` sets `InputSchema`)
- Test: `internal/mcpsrv/integration_test.go`
- Modify: `go.mod`, `go.sum` (jsonschema-go becomes a direct dependency)
- Modify: `README.md` (CodeScene "In-band attribution" paragraph), `CHANGELOG.md`

**Acceptance Criteria:**
- [ ] A `validate_completion` call whose `codescene` object, or its `verdicts`, carries keys outside the digest shape is accepted over MCP instead of failing schema validation
- [ ] Raw `analyze_change_set` output (`quality_gates`, `results[]`) passed as `codescene` yields `ran=true`, `tool=analyze_change_set`, `quality_gate`, `files_analyzed = len(results)`, per-verdict counts, `net_pp = Σ(new-pp − old-pp)` and per-category counts
- [ ] A digest field that is present wins over the value derived from raw keys; a raw key of the wrong type is ignored on its own, without failing the call or stopping the reduction of the other raw key
- [ ] Under `ANTI_TANGENT_CODESCENE=required`, both shapes above draw no `codescene_not_run` or `codescene_skipped` finding

**Non-goals:**
- Do not invoke CodeScene or change which CodeScene command analyzes a task.
- Do not reject the optional `codescene` object solely because it contains unknown or mistyped raw side fields.

**Context:**
- The MCP input schema must admit both the existing digest and raw `analyze_change_set` JSON.
- `codescene.Digest` performs server-side reduction, while explicitly present digest keys take precedence over derived values.

**Verify:** `go test -race ./internal/codescene/ ./internal/mcpsrv/ -run 'TestDigest_UnmarshalJSON|TestIntegration_CodesceneArgumentAcceptsUnknownAndRawKeys' -v` → PASS

**Steps:**

- [ ] **Step 1: Write the failing unit tests**

In `internal/codescene/codescene_test.go`, add:

```go
func TestDigest_UnmarshalJSON_ReducesRawChangeSet(t *testing.T) {
	raw := `{"quality_gates":"passed","results":[
		{"name":"a.go","verdict":"improved","findings":[{"category":"Complex Method","new-pp":1.0,"old-pp":2.5}]},
		{"name":"b.go","verdict":"stable","findings":[]},
		{"name":"c.go","verdict":"degraded","findings":[{"category":"Complex Method","new-pp":3.0,"old-pp":2.0},{"category":"Bumpy Road Ahead","new-pp":1.0,"old-pp":0.0}]}
	]}`
	var d Digest
	require.NoError(t, json.Unmarshal([]byte(raw), &d))
	assert.True(t, d.Ran)
	assert.Equal(t, "analyze_change_set", d.Tool)
	assert.Equal(t, "passed", d.QualityGate)
	assert.Equal(t, 3, d.FilesAnalyzed)
	require.NotNil(t, d.Verdicts)
	assert.Equal(t, Verdicts{Improved: 1, Degraded: 1, Stable: 1}, *d.Verdicts)
	assert.InDelta(t, 0.5, d.NetPP, 1e-9)
	assert.Equal(t, map[string]int{"Complex Method": 2, "Bumpy Road Ahead": 1}, d.CategoryCounts)
}

func TestDigest_UnmarshalJSON_EmptyChangeSetStillRan(t *testing.T) {
	var d Digest
	require.NoError(t, json.Unmarshal([]byte(`{"quality_gates":"passed","results":[]}`), &d))
	assert.True(t, d.Ran)
	assert.Equal(t, 0, d.FilesAnalyzed)
	require.NotNil(t, d.Verdicts)
	assert.Equal(t, Verdicts{}, *d.Verdicts)
}

func TestDigest_UnmarshalJSON_PresentDigestFieldsWin(t *testing.T) {
	raw := `{"ran":false,"skip_reason":"tool errored","quality_gate":"failed","quality_gates":"passed",
		"files_analyzed":7,"verdicts":{"improved":0,"degraded":2,"stable":5},"net_pp":4,
		"results":[{"verdict":"improved","findings":[{"category":"X","new-pp":0,"old-pp":9}]}]}`
	var d Digest
	require.NoError(t, json.Unmarshal([]byte(raw), &d))
	assert.False(t, d.Ran)
	assert.Equal(t, "failed", d.QualityGate)
	assert.Equal(t, 7, d.FilesAnalyzed)
	assert.Equal(t, Verdicts{Degraded: 2, Stable: 5}, *d.Verdicts)
	assert.InDelta(t, 4.0, d.NetPP, 1e-9)
	assert.Equal(t, map[string]int{"X": 1}, d.CategoryCounts, "category_counts was absent, so it is derived")
}

func TestDigest_UnmarshalJSON_SentToolWins(t *testing.T) {
	var empty Digest
	require.NoError(t, json.Unmarshal([]byte(`{"tool":"","quality_gates":"passed","results":[]}`), &empty))
	assert.Equal(t, "", empty.Tool, "a tool key the caller sent wins over the derived value, even when empty")

	var named Digest
	require.NoError(t, json.Unmarshal([]byte(`{"tool":"codescene-cli","results":[]}`), &named))
	assert.Equal(t, "codescene-cli", named.Tool)
}

func TestDigest_UnmarshalJSON_IgnoresUnknownAndMistypedRawKeys(t *testing.T) {
	raw := `{"ran":true,"base_ref":"abc123","note":"free text","results":{"not":"a list"},
		"verdicts":{"improved":9,"degraded":0,"stable":3,"unknown_or_no_findings":17}}`
	var d Digest
	require.NoError(t, json.Unmarshal([]byte(raw), &d))
	assert.True(t, d.Ran)
	assert.Equal(t, 0, d.FilesAnalyzed)
	assert.Equal(t, Verdicts{Improved: 9, Stable: 3}, *d.Verdicts)
}

func TestDigest_UnmarshalJSON_MistypedRawKeyLeavesTheOtherReduced(t *testing.T) {
	var badGate Digest
	require.NoError(t, json.Unmarshal([]byte(`{"quality_gates":true,"results":[
		{"verdict":"degraded","findings":[{"category":"Complex Method","new-pp":2,"old-pp":1}]}]}`), &badGate))
	assert.True(t, badGate.Ran)
	assert.Equal(t, "analyze_change_set", badGate.Tool)
	assert.Equal(t, "", badGate.QualityGate)
	assert.Equal(t, 1, badGate.FilesAnalyzed)
	require.NotNil(t, badGate.Verdicts)
	assert.Equal(t, Verdicts{Degraded: 1}, *badGate.Verdicts)
	assert.InDelta(t, 1.0, badGate.NetPP, 1e-9)
	assert.Equal(t, map[string]int{"Complex Method": 1}, badGate.CategoryCounts)

	var badResults Digest
	require.NoError(t, json.Unmarshal([]byte(`{"quality_gates":"failed","results":"n/a"}`), &badResults))
	assert.True(t, badResults.Ran)
	assert.Equal(t, "failed", badResults.QualityGate)
	assert.Equal(t, 0, badResults.FilesAnalyzed)
	assert.Nil(t, badResults.Verdicts)
}

func TestDigest_UnmarshalJSON_DigestShapeRoundTrips(t *testing.T) {
	want := Digest{Ran: true, Tool: "analyze_change_set", QualityGate: "passed", FilesAnalyzed: 2,
		Verdicts: &Verdicts{Improved: 1, Stable: 1}, Trend: TrendImprovement, NetPP: -1,
		CategoryCounts: map[string]int{"Complex Method": 1}}
	b, err := json.Marshal(want)
	require.NoError(t, err)
	var got Digest
	require.NoError(t, json.Unmarshal(b, &got))
	assert.Equal(t, want, got)
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test -race ./internal/codescene/ -run TestDigest_UnmarshalJSON -v`
Expected: FAIL — `ReducesRawChangeSet` and `EmptyChangeSetStillRan` see `Ran=false`, `Tool=""`, nil `Verdicts`.

- [ ] **Step 3: Implement the decoder**

In `internal/codescene/codescene.go`, add `"encoding/json"` to the import block and add:

```go
// rawChangeSetResult is one file entry of CodeScene's raw analyze_change_set
// output: its verdict, and the findings whose problem points and categories
// reduce into a Digest.
type rawChangeSetResult struct {
	Verdict  string `json:"verdict"`
	Findings []struct {
		Category string  `json:"category"`
		NewPP    float64 `json:"new-pp"`
		OldPP    float64 `json:"old-pp"`
	} `json:"findings"`
}

// UnmarshalJSON accepts both the digest shape and CodeScene's raw
// analyze_change_set output, reducing quality_gates and results[] the way
// examples/hooks/codescene-log.sh does. A digest field present in the input
// always wins over the value derived from raw keys. Unknown keys are ignored,
// and each raw key is decoded on its own, so one of the wrong type is ignored
// without disturbing the other: the argument is optional, and a malformed side
// field must not cost the caller the whole call or the rest of the reduction.
func (d *Digest) UnmarshalJSON(b []byte) error {
	type plainDigest Digest
	var p plainDigest
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	*d = Digest(p)

	var present map[string]json.RawMessage
	if err := json.Unmarshal(b, &present); err != nil {
		return nil
	}
	has := func(key string) bool { _, ok := present[key]; return ok }

	var gate string
	gateOK := has("quality_gates") && json.Unmarshal(present["quality_gates"], &gate) == nil
	var results []rawChangeSetResult
	resultsOK := has("results") && json.Unmarshal(present["results"], &results) == nil && results != nil
	if !gateOK && !resultsOK {
		return nil
	}

	if !has("ran") {
		d.Ran = true
	}
	if !has("tool") {
		d.Tool = "analyze_change_set"
	}
	if gateOK && !has("quality_gate") {
		d.QualityGate = gate
	}
	if !resultsOK {
		return nil
	}
	var verdicts Verdicts
	var netPP float64
	counts := map[string]int{}
	for _, r := range results {
		switch r.Verdict {
		case "improved":
			verdicts.Improved++
		case "degraded":
			verdicts.Degraded++
		case "stable":
			verdicts.Stable++
		}
		for _, f := range r.Findings {
			netPP += f.NewPP - f.OldPP
			if f.Category != "" {
				counts[f.Category]++
			}
		}
	}
	if !has("files_analyzed") {
		d.FilesAnalyzed = len(results)
	}
	if !has("verdicts") {
		d.Verdicts = &verdicts
	}
	if !has("net_pp") {
		d.NetPP = netPP
	}
	if !has("category_counts") && len(counts) > 0 {
		d.CategoryCounts = counts
	}
	return nil
}
```

Note: `{"results":[]}` decodes to a non-nil empty slice, which is why `EmptyChangeSetStillRan` gets zero-valued `Verdicts`; `"results": null` or a non-array `results` leaves `resultsOK` false, so only a valid `quality_gates` can still mark the run.

- [ ] **Step 4: Run the unit tests to verify they pass**

Run: `go test -race ./internal/codescene/ -v`
Expected: PASS (new and existing tests)

- [ ] **Step 5: Write the failing MCP-level test**

In `internal/mcpsrv/integration_test.go`, add:

```go
func TestIntegration_CodesceneArgumentAcceptsUnknownAndRawKeys(t *testing.T) {
	d := newDeps(t, &fakeReviewer{name: "anthropic", resp: passResp("claude-opus-4-7")})
	d.Cfg.Codescene = "required"
	srv := New(d)

	st, ct := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() { _ = srv.Run(ctx, st) }()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, ct, nil)
	require.NoError(t, err)
	defer func() { _ = cs.Close() }()

	cases := map[string]map[string]any{
		"digest carrying unknown keys": {
			"ran": true, "tool": "analyze_change_set", "quality_gate": "passed", "files_analyzed": 12,
			"base_ref": "abc123",
			"verdicts": map[string]any{"improved": 9, "degraded": 0, "stable": 3, "unknown_or_no_findings": 17},
		},
		"raw analyze_change_set output": {
			"quality_gates": "passed",
			"note":          "free text",
			"results": []any{
				map[string]any{"name": "a.go", "verdict": "improved", "findings": []any{
					map[string]any{"category": "Complex Method", "new-pp": 1.0, "old-pp": 2.0},
				}},
				map[string]any{"name": "b.go", "verdict": "stable", "findings": []any{}},
			},
		},
	}
	for name, digest := range cases {
		t.Run(name, func(t *testing.T) {
			pre := callTool(t, ctx, cs, "validate_task_spec", map[string]any{
				"task_title": "T", "goal": "G", "acceptance_criteria": []string{"AC"},
			})
			post := callTool(t, ctx, cs, "validate_completion", map[string]any{
				"session_id": pre.SessionID,
				"summary":    "done",
				"final_diff": "diff --git a/x b/x\n+ok\n",
				"codescene":  digest,
			})
			assert.False(t, hasCategory(post.Findings, verdict.CategoryCodesceneNotRun), "findings: %+v", post.Findings)
			assert.False(t, hasCategory(post.Findings, verdict.CategoryCodesceneSkipped), "findings: %+v", post.Findings)
		})
	}
}
```

- [ ] **Step 6: Run it to verify it fails**

Run: `go test -race ./internal/mcpsrv/ -run TestIntegration_CodesceneArgumentAcceptsUnknownAndRawKeys -v`
Expected: FAIL in `callTool`'s `require.NoError` with a schema error such as `unexpected additional properties ["base_ref"]`.

- [ ] **Step 7: Open the schema**

Create `internal/mcpsrv/completion_schema.go`:

```go
package mcpsrv

import (
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"
)

// validateCompletionInputSchema is the schema inferred from
// ValidateCompletionArgs with the codescene object, and its verdicts, opened
// to unknown keys. Inference closes every struct with additionalProperties:
// false, and the SDK validates arguments against the schema before the
// handler runs, so a single unexpected key in this optional field would
// reject the whole call. codescene.Digest's UnmarshalJSON decides what the
// extra keys mean.
func validateCompletionInputSchema() *jsonschema.Schema {
	s, err := jsonschema.For[ValidateCompletionArgs](nil)
	if err != nil {
		panic(fmt.Sprintf("infer validate_completion input schema: %v", err))
	}
	cs, ok := s.Properties["codescene"]
	if !ok {
		panic("validate_completion input schema has no codescene property")
	}
	cs.AdditionalProperties = nil
	cs.Required = nil
	if v, ok := cs.Properties["verdicts"]; ok {
		v.AdditionalProperties = nil
		v.Required = nil
	}
	return s
}
```

In `internal/mcpsrv/handlers.go`, in `validateCompletionTool`, add the field after `Name`:

```go
		InputSchema: validateCompletionInputSchema(),
```

Then:

```bash
go mod tidy
```

Expected: `github.com/google/jsonschema-go` moves from the indirect block to a direct `require` in `go.mod`.

- [ ] **Step 8: Run the tests to verify they pass**

Run: `go test -race ./internal/codescene/ ./internal/mcpsrv/ -run 'TestDigest_UnmarshalJSON|TestIntegration_' -v`
Expected: PASS

Run: `go test -race ./...`
Expected: PASS

- [ ] **Step 9: Docs**

In `README.md`, at the end of the paragraph that starts `**In-band attribution (v0.15.0+).**`, append:

```markdown
 Since v0.22.0 the argument also accepts `analyze_change_set`'s raw JSON (`quality_gates`, `results[]`) and reduces it server-side, and unknown keys are ignored instead of rejecting the call. `pre_commit_code_health_safeguard` sees only uncommitted changes, so after a commit it reports zero files and is not a CodeScene run of the task.
```

Under `## [0.22.0] - 2026-09-15`, add to `### Added` (create it above `### Changed` if absent):

```markdown
- `validate_completion`'s `codescene` argument accepts CodeScene's raw `analyze_change_set`
  output and reduces it to the digest server-side: `quality_gates`, the length of `results`, the
  per-file verdict tally, Σ(`new-pp` − `old-pp`) and per-category counts. A digest field that is
  present wins over the derived value.
```

and to `### Fixed`:

```markdown
- An unexpected key in `validate_completion`'s optional `codescene` argument, or in its
  `verdicts`, no longer fails schema validation and loses the whole call. Unknown keys are
  ignored.
```

- [ ] **Step 10: Commit**

```bash
gofmt -l internal/ && go vet ./internal/...
git add go.mod go.sum internal/codescene/codescene.go internal/codescene/codescene_test.go internal/mcpsrv/completion_schema.go internal/mcpsrv/handlers.go internal/mcpsrv/integration_test.go README.md CHANGELOG.md
git commit -m "feat(codescene): accept raw analyze_change_set output and unknown keys"
```

```json:metadata
{"files": ["internal/codescene/codescene.go", "internal/codescene/codescene_test.go", "internal/mcpsrv/completion_schema.go", "internal/mcpsrv/handlers.go", "internal/mcpsrv/integration_test.go", "go.mod", "go.sum", "README.md", "CHANGELOG.md"], "verifyCommand": "go test -race ./internal/codescene/ ./internal/mcpsrv/ -run 'TestDigest_UnmarshalJSON|TestIntegration_CodesceneArgumentAcceptsUnknownAndRawKeys' -v", "acceptanceCriteria": ["unknown keys in codescene and verdicts accepted over MCP", "raw analyze_change_set output reduced to the digest", "present digest fields win; a mistyped raw key is ignored without stopping the other's reduction", "required mode draws no codescene_not_run or codescene_skipped for either shape"], "modelTier": "standard"}
```

---

### Task 4: Make plan-run attachment visible

**Goal:** A task that forgets `plan_run_id` is told which run to attach to, a run no task attached to is still reported as known, and the unknown-run message leads with the likely cause.

**Files:**
- Modify: `internal/planrun/planrun.go` (new `(*Store).Latest`)
- Modify: `internal/planrun/ledger.go` (new `ledgerHeaderLine`, `(*Ledger).AppendHeader`, shared `appendLine`; header handling in `Load` and `Prune`)
- Test: `internal/planrun/planrun_test.go`, `internal/planrun/ledger_test.go`
- Modify: `internal/mcpsrv/review_error.go` (`planCallContext.PlanLedger`; header write in `mintPlanRunID`)
- Modify: `internal/mcpsrv/handlers.go` (`planRunIDAdvisory`, `unattachedPlanRunFinding`; advisory in `ValidateTaskSpec`; `PlanLedger` on both `planCallContext` literals; not-found evidence in `PlanRunReport`)
- Create: `internal/mcpsrv/handlers_plan_run_attach_test.go`
- Modify: `CHANGELOG.md`

**Acceptance Criteria:**
- [ ] `Store.Latest` returns the most recently created run not idle past the TTL, does not refresh `LastAccessed`, and is safe on a nil `*Store`
- [ ] `validate_task_spec` without `plan_run_id`, while a live run exists, returns one extra minor `other` finding with `criterion: plan_run_id` naming that run; the verdict is identical to the same call with no live run; with `plan_run_id` passed, or with no live run, there is no such finding
- [ ] With the ledger enabled, `validate_plan` appends one header line per freshly minted run; `Ledger.Load` returns a header-only run with zero rows, never turns a header into a row, and `Prune` drops a header older than the cutoff by its `created_at`
- [ ] `plan_run_report` for a known run with zero rows includes a minor `other` finding saying no task passed its `plan_run_id`; after a restart with the ledger enabled, a header-only run is reported as known, not `session_not_found`
- [ ] The unknown-run `session_not_found` evidence names a missing `plan_run_id` on `validate_task_spec` as the usual cause, then idle expiry, then a different or restarted server without a ledger

**Non-goals:**
- Do not let the missing-`plan_run_id` advisory affect the finalized task verdict.
- Do not make the optional ledger required for plan validation or reporting.

**Context:**
- Live-run lookup is advisory and must not refresh idle lifetime.
- A ledger header records run identity before any task row exists, allowing a header-only run to remain known after restart.

**Verify:** `go test -race ./internal/planrun/ ./internal/mcpsrv/ -run 'TestLatest|TestLedger_Header|TestPlanRunReport|TestValidateTaskSpec_PlanRunIDAdvisory|TestValidatePlan_LedgerHeader|TestValidatePlan_OneLedgerHeader' -v` → PASS

**Steps:**

- [ ] **Step 1: Write the failing store and ledger tests**

In `internal/planrun/planrun_test.go`, add:

```go
func TestLatest_MostRecentLiveRun(t *testing.T) {
	s := NewStore(time.Minute)
	_, ok := s.Latest()
	assert.False(t, ok, "empty store")

	older := s.Create("pass", "actionable", 1)
	newer := s.Create("warn", "rigorous", 2)
	older.CreatedAt = time.Now().Add(-10 * time.Second)

	got, ok := s.Latest()
	require.True(t, ok)
	assert.Equal(t, newer.ID, got.ID)

	newer.LastAccessed = time.Now().Add(-2 * time.Minute)
	got, ok = s.Latest()
	require.True(t, ok)
	assert.Equal(t, older.ID, got.ID, "a run idle past the TTL is skipped")

	before := older.LastAccessed
	_, _ = s.Latest()
	assert.Equal(t, before, older.LastAccessed, "Latest must not refresh LastAccessed")
}

func TestLatest_NilStore(t *testing.T) {
	var s *Store
	_, ok := s.Latest()
	assert.False(t, ok)
}
```

In `internal/planrun/ledger_test.go`, add:

```go
func TestLedger_HeaderOnlyRunLoads(t *testing.T) {
	dir := t.TempDir()
	l := &Ledger{Dir: dir}
	created := time.Date(2026, 9, 15, 10, 0, 0, 0, time.UTC)
	require.NoError(t, l.AppendHeader(&Run{ID: "pr_aaaaaaaaaaaa", CreatedAt: created, PlanVerdict: "warn", PlanQuality: "actionable", TaskCount: 18}))

	got, ok := l.Load("pr_aaaaaaaaaaaa")
	require.True(t, ok)
	assert.Equal(t, "warn", got.PlanVerdict)
	assert.Equal(t, 18, got.TaskCount)
	assert.True(t, got.CreatedAt.Equal(created))
	assert.Empty(t, got.Rows)

	b, err := os.ReadFile(filepath.Join(dir, ledgerFile))
	require.NoError(t, err)
	assert.NotContains(t, string(b), "task_title", "a header carries no task title")
}

func TestLedger_HeaderIsNeverARow(t *testing.T) {
	l := &Ledger{Dir: t.TempDir()}
	run := &Run{ID: "pr_bbbbbbbbbbbb", CreatedAt: time.Now().UTC(), PlanVerdict: "pass", PlanQuality: "rigorous", TaskCount: 2}
	require.NoError(t, l.AppendHeader(run))
	require.NoError(t, l.Append(run, TaskRow{Index: 1, TaskTitle: "Add endpoint", PostVerdict: "pass", CompletedAt: time.Now().UTC()}))

	got, ok := l.Load(run.ID)
	require.True(t, ok)
	require.Len(t, got.Rows, 1)
	assert.Equal(t, "Add endpoint", got.Rows[0].TaskTitle)
}

func TestLedger_HeaderPrunedByCreatedAt(t *testing.T) {
	l := &Ledger{Dir: t.TempDir()}
	cutoff := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	require.NoError(t, l.AppendHeader(&Run{ID: "pr_old000000000", CreatedAt: cutoff.Add(-time.Hour)}))
	require.NoError(t, l.AppendHeader(&Run{ID: "pr_new000000000", CreatedAt: cutoff.Add(time.Hour)}))

	require.NoError(t, l.Prune(cutoff))
	_, ok := l.Load("pr_old000000000")
	assert.False(t, ok, "a header older than the cutoff is pruned")
	_, ok = l.Load("pr_new000000000")
	assert.True(t, ok)
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test -race ./internal/planrun/ -run 'TestLatest|TestLedger_Header' -v`
Expected: FAIL to compile — `s.Latest undefined`, `l.AppendHeader undefined`.

- [ ] **Step 3: Implement the store and ledger changes**

In `internal/planrun/planrun.go`, add after `Snapshot`:

```go
// Latest returns the most recently created run that has not been idle past
// the TTL. It does not refresh LastAccessed: naming a run in an advisory must
// not keep a stale run alive. Safe on a nil Store.
func (s *Store) Latest() (*Run, bool) {
	if s == nil {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	var latest *Run
	for _, r := range s.runs {
		if now.Sub(r.LastAccessed) > s.ttl {
			continue
		}
		if latest == nil || r.CreatedAt.After(latest.CreatedAt) {
			latest = r
		}
	}
	return latest, latest != nil
}
```

In `internal/planrun/ledger.go`, add two fields to `ledgerLine` after `Row`:

```go
	// Header marks the line validate_plan writes when it mints a run, before any
	// task is attached. It has no row, so Load must not turn it into one.
	Header bool `json:"header,omitempty"`
	// CreatedAt is set on header lines only. Prune keys task rows on
	// Row.CompletedAt, and a header has no row to key on.
	CreatedAt time.Time `json:"created_at,omitzero"`
```

Add the header record type after `ledgerLine`:

```go
// ledgerHeaderLine is the on-disk shape of a header. It is marshalled from its
// own type rather than from ledgerLine so the line carries no zero-valued row.
type ledgerHeaderLine struct {
	PlanRunID   string    `json:"plan_run_id"`
	PlanVerdict string    `json:"plan_verdict,omitempty"`
	PlanQuality string    `json:"plan_quality,omitempty"`
	TaskCount   int       `json:"task_count,omitempty"`
	Header      bool      `json:"header"`
	CreatedAt   time.Time `json:"created_at"`
}
```

Replace `Append` with `Append`, `AppendHeader` and the shared `appendLine`:

```go
func (l *Ledger) Append(run *Run, row TaskRow) error {
	if l == nil || l.Dir == "" {
		return nil
	}
	b, err := json.Marshal(ledgerLine{
		PlanRunID: run.ID, PlanVerdict: run.PlanVerdict,
		PlanQuality: run.PlanQuality, TaskCount: run.TaskCount, Row: row,
	})
	if err != nil {
		return err
	}
	return l.appendLine(b)
}

// AppendHeader records a run when validate_plan mints it, so a run that no
// task was ever attached to is still known to Load. It carries no task title.
func (l *Ledger) AppendHeader(run *Run) error {
	if l == nil || l.Dir == "" {
		return nil
	}
	b, err := json.Marshal(ledgerHeaderLine{
		PlanRunID: run.ID, PlanVerdict: run.PlanVerdict, PlanQuality: run.PlanQuality,
		TaskCount: run.TaskCount, Header: true, CreatedAt: run.CreatedAt.UTC(),
	})
	if err != nil {
		return err
	}
	return l.appendLine(b)
}

func (l *Ledger) appendLine(b []byte) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.afterAppendLock != nil {
		l.afterAppendLock()
	}
	f, err := os.OpenFile(filepath.Join(l.Dir, ledgerFile), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	// OpenFile's mode argument only applies at creation, so a file created with
	// a wider mode would otherwise never get tightened by a plain append. Chmod
	// is best-effort and its error is intentionally swallowed: ledger writes
	// are advisory, and a chmod failure must never turn into a failed append.
	_ = f.Chmod(0o600)
	_, err = f.Write(append(b, '\n'))
	return err
}
```

In `Load`, replace the body of the scan loop after the `run == nil` initialisation:

```go
		if run == nil {
			run = &Run{
				ID: ln.PlanRunID, PlanVerdict: ln.PlanVerdict,
				PlanQuality: ln.PlanQuality, TaskCount: ln.TaskCount,
			}
		}
		if ln.Header {
			if run.CreatedAt.IsZero() {
				run.CreatedAt = ln.CreatedAt
			}
			continue
		}
		byIndex[ln.Row.Index] = ln.Row // last-seen wins: a resubmission overwrites its own index
```

In `Prune`, replace the cutoff check inside the scan loop:

```go
		stamp := ln.Row.CompletedAt
		if ln.Header {
			stamp = ln.CreatedAt
		}
		if !stamp.IsZero() && stamp.Before(cutoff) {
			continue // has a real timestamp and it is stale: drop
		}
```

- [ ] **Step 4: Run the planrun tests to verify they pass**

Run: `go test -race ./internal/planrun/ -v`
Expected: PASS (new and existing)

- [ ] **Step 5: Write the failing handler tests**

Create `internal/mcpsrv/handlers_plan_run_attach_test.go`:

```go
package mcpsrv

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/planrun"
	"github.com/patiently/anti-tangent-mcp/internal/providers"
	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

func planRunIDFindings(fs []verdict.Finding) []verdict.Finding {
	var out []verdict.Finding
	for _, f := range fs {
		if f.Criterion == "plan_run_id" {
			out = append(out, f)
		}
	}
	return out
}

// twoMinorsResp returns two minor findings, one short of the noise_cluster
// rule, so an advisory that leaked into verdict finalization would lift the
// verdict and the comparison below would catch it.
func twoMinorsResp() providers.Response {
	return providers.Response{
		RawJSON: []byte(`{"verdict":"pass","findings":[` +
			`{"severity":"minor","category":"quality","criterion":"a","evidence":"e","suggestion":"s"},` +
			`{"severity":"minor","category":"quality","criterion":"b","evidence":"e","suggestion":"s"}` +
			`],"next_action":"go"}`),
		Model: "claude-sonnet-4-6",
	}
}

func TestValidateTaskSpec_PlanRunIDAdvisory(t *testing.T) {
	args := ValidateTaskSpecArgs{TaskTitle: "T", Goal: "G", AcceptanceCriteria: []string{"AC"}}

	t.Run("no live run, no advisory", func(t *testing.T) {
		h := &handlers{deps: newDeps(t, &fakeReviewer{name: "anthropic", resp: twoMinorsResp()})}
		_, env, err := h.ValidateTaskSpec(context.Background(), nil, args)
		require.NoError(t, err)
		assert.Empty(t, planRunIDFindings(env.Findings))
	})

	t.Run("live run and no plan_run_id names the run without moving the verdict", func(t *testing.T) {
		baseline := &handlers{deps: newDeps(t, &fakeReviewer{name: "anthropic", resp: twoMinorsResp()})}
		_, want, err := baseline.ValidateTaskSpec(context.Background(), nil, args)
		require.NoError(t, err)

		h := &handlers{deps: newDeps(t, &fakeReviewer{name: "anthropic", resp: twoMinorsResp()})}
		run := h.deps.PlanRuns.Create("pass", "actionable", 3)
		_, env, err := h.ValidateTaskSpec(context.Background(), nil, args)
		require.NoError(t, err)

		got := planRunIDFindings(env.Findings)
		require.Len(t, got, 1)
		assert.Equal(t, verdict.SeverityMinor, got[0].Severity)
		assert.Equal(t, verdict.CategoryOther, got[0].Category)
		assert.Contains(t, got[0].Suggestion, "plan_run_id="+run.ID)
		assert.Equal(t, want.Verdict, env.Verdict, "the advisory must not change the verdict")
		assert.Contains(t, env.SummaryBlock, run.ID)
	})

	t.Run("plan_run_id passed, no advisory", func(t *testing.T) {
		h := &handlers{deps: newDeps(t, &fakeReviewer{name: "anthropic", resp: twoMinorsResp()})}
		run := h.deps.PlanRuns.Create("pass", "actionable", 3)
		withRun := args
		withRun.PlanRunID = run.ID
		_, env, err := h.ValidateTaskSpec(context.Background(), nil, withRun)
		require.NoError(t, err)
		assert.Empty(t, planRunIDFindings(env.Findings))
	})
}

func TestPlanRunReport_UnattachedRunIsExplained(t *testing.T) {
	h := &handlers{deps: newDeps(t, &fakeReviewer{name: "anthropic", resp: passResp("m")})}
	run := h.deps.PlanRuns.Create("warn", "actionable", 18)

	_, res, err := h.PlanRunReport(context.Background(), nil, PlanRunReportArgs{PlanRunID: run.ID})
	require.NoError(t, err)
	got := planRunIDFindings(res.Findings)
	require.Len(t, got, 1)
	assert.Equal(t, verdict.CategoryOther, got[0].Category)
	assert.Contains(t, got[0].Evidence, "no validate_task_spec call passed")
	assert.Contains(t, got[0].Evidence, "18 tasks")
}

func TestPlanRunReport_UnknownRunNamesTheUsualCauseFirst(t *testing.T) {
	h := &handlers{deps: newDeps(t, &fakeReviewer{name: "anthropic", resp: passResp("m")})}
	_, res, err := h.PlanRunReport(context.Background(), nil, PlanRunReportArgs{PlanRunID: "pr_does_not_exist"})
	require.NoError(t, err)
	require.Len(t, res.Findings, 1)
	e := res.Findings[0].Evidence
	cause := strings.Index(e, "no validate_task_spec call passed it as plan_run_id")
	expiry := strings.Index(e, h.deps.PlanRuns.TTL().String())
	restart := strings.Index(e, "restarted server")
	require.True(t, cause >= 0 && expiry >= 0 && restart >= 0, "evidence: %s", e)
	assert.True(t, cause < expiry && expiry < restart, "causes must appear in likelihood order: %s", e)
}

func TestValidatePlan_LedgerHeaderKeepsAnUnattachedRunKnown(t *testing.T) {
	ledger := &planrun.Ledger{Dir: t.TempDir()}
	h := newTestPlanHandlers(t)
	h.deps.PlanLedger = ledger

	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{
		PlanText: "### Task 1: t1\n\n**Goal:** g\n\n**Acceptance criteria:**\n- ac\n",
	})
	require.NoError(t, err)
	require.NotEmpty(t, pr.PlanRunID)

	restarted := newTestPlanHandlers(t)
	restarted.deps.PlanRuns = planrun.NewStore(time.Hour)
	restarted.deps.PlanLedger = ledger
	_, res, err := restarted.PlanRunReport(context.Background(), nil, PlanRunReportArgs{PlanRunID: pr.PlanRunID})
	require.NoError(t, err)
	assert.False(t, hasCategory(res.Findings, verdict.CategorySessionMissing), "a header-only run is known: %+v", res.Findings)
	assert.Len(t, planRunIDFindings(res.Findings), 1)
	assert.Equal(t, string(pr.PlanVerdict), res.PlanVerdict)
}

func TestValidatePlan_OneLedgerHeaderPerMintedRun(t *testing.T) {
	dir := t.TempDir()
	h := newTestPlanHandlers(t)
	h.deps.PlanLedger = &planrun.Ledger{Dir: dir}
	args := ValidatePlanArgs{PlanText: buildPlanWithNTasks(1)}

	_, first, err := h.ValidatePlan(context.Background(), nil, args)
	require.NoError(t, err)
	_, second, err := h.ValidatePlan(context.Background(), nil, args)
	require.NoError(t, err)
	require.Equal(t, first.PlanRunID, second.PlanRunID, "an identical passing call inside the cache window reuses its run")

	b, err := os.ReadFile(filepath.Join(dir, "plan-runs.jsonl"))
	require.NoError(t, err)
	headers := 0
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var rec struct {
			PlanRunID string `json:"plan_run_id"`
			Header    bool   `json:"header"`
		}
		require.NoError(t, json.Unmarshal([]byte(line), &rec))
		if rec.Header && rec.PlanRunID == first.PlanRunID {
			headers++
		}
	}
	assert.Equal(t, 1, headers, "a cache hit must not append a second header")
}
```

- [ ] **Step 6: Run them to verify they fail**

Run: `go test -race ./internal/mcpsrv/ -run 'TestValidateTaskSpec_PlanRunIDAdvisory|TestPlanRunReport_UnattachedRunIsExplained|TestPlanRunReport_UnknownRunNamesTheUsualCauseFirst|TestValidatePlan_LedgerHeaderKeepsAnUnattachedRunKnown|TestValidatePlan_OneLedgerHeaderPerMintedRun' -v`
Expected: FAIL — no `plan_run_id` advisory, no unattached finding, old evidence wording, `session_not_found` after restart, and no ledger file written by `validate_plan`.

- [ ] **Step 7: Implement the handler changes**

In `internal/mcpsrv/review_error.go`, add `"log/slog"` to the imports, add this field to `planCallContext` after `PlanRuns`:

```go
	// PlanLedger receives a header line for every freshly minted run. Nil-safe:
	// nil unless ANTI_TANGENT_STATS_DIR and ANTI_TANGENT_PLAN_LEDGER are set.
	PlanLedger *planrun.Ledger
```

and replace `mintPlanRunID`:

```go
func (c planCallContext) mintPlanRunID(pr *verdict.PlanResult) {
	if pr.PlanRunID != "" {
		return
	}
	run := c.PlanRuns.Create(string(pr.PlanVerdict), string(pr.PlanQuality), len(pr.Tasks))
	pr.PlanRunID = run.ID
	if err := c.PlanLedger.AppendHeader(run); err != nil {
		slog.Warn("plan ledger header append failed", "plan_run_id", run.ID, "err", err)
	}
}
```

In `internal/mcpsrv/handlers.go`, set `PlanLedger: h.deps.PlanLedger,` in both `planCallContext{...}` literals in `ValidatePlan` (the `cachedCall` literal and the `call` literal), directly after `PlanRuns: h.deps.PlanRuns,`.

Add these two functions directly below `prependPlanDeprecation`:

```go
// planRunIDAdvisory tells a validate_task_spec caller that this server holds a
// live plan run the call did not name. It describes the call's arguments, not
// the task, so it is appended after the verdict is finalized and must never
// move it. It names the most recently created run because every validate_plan
// round mints a new id and supersedes the previous one.
func planRunIDAdvisory(runID string) verdict.Finding {
	return verdict.Finding{
		Severity:  verdict.SeverityMinor,
		Category:  verdict.CategoryOther,
		Criterion: "plan_run_id",
		Evidence: fmt.Sprintf("This call passed no plan_run_id, but this server holds a live plan run, %s, "+
			"minted by the most recent validate_plan.", runID),
		Suggestion: fmt.Sprintf("If this task belongs to that plan, call validate_task_spec with plan_run_id=%s "+
			"so plan_run_report can include it. Ignore this if the task is not part of a plan run.", runID),
	}
}

// unattachedPlanRunFinding explains a known plan run with no task rows: the
// run was minted, but no validate_task_spec call passed its id.
func unattachedPlanRunFinding(run *planrun.Run) verdict.Finding {
	return verdict.Finding{
		Severity:  verdict.SeverityMinor,
		Category:  verdict.CategoryOther,
		Criterion: "plan_run_id",
		Evidence: fmt.Sprintf("Plan run %s is known (%d tasks in the plan), but no validate_task_spec call passed "+
			"its plan_run_id, so no task is attached to it.", run.ID, run.TaskCount),
		Suggestion: "Pass plan_run_id on every implementing subagent's validate_task_spec call. " +
			"For this run, report from the per-task DONE envelopes instead.",
	}
}
```

In `ValidateTaskSpec`, directly after `env = h.withSessionTTL(env, sess)`, add:

```go
	if args.PlanRunID == "" {
		if run, ok := h.deps.PlanRuns.Latest(); ok {
			env.Findings = append(env.Findings, planRunIDAdvisory(run.ID))
		}
	}
```

In `PlanRunReport`, replace the `Evidence` and `Suggestion` of the `session_not_found` finding:

```go
				Evidence: fmt.Sprintf("No plan run %q is known to this server. The usual cause is that no validate_task_spec "+
					"call passed it as plan_run_id: nothing then keeps a run alive, so it expired %s after validate_plan minted it. "+
					"A run is also unknown to a different or restarted server unless the plan ledger recorded it.",
					args.PlanRunID, h.deps.PlanRuns.TTL()),
				Suggestion: "Nothing to recover — report from the per-task DONE envelopes instead. " +
					"Pass plan_run_id on every validate_task_spec call, and set ANTI_TANGENT_STATS_DIR and " +
					"ANTI_TANGENT_PLAN_LEDGER=1 so a run survives a restart.",
```

and in the found branch, directly before `return planRunReportResult(res)`, add:

```go
	if len(run.Rows) == 0 {
		res.Findings = append(res.Findings, unattachedPlanRunFinding(run))
	}
```

- [ ] **Step 8: Run the tests to verify they pass**

Run: `go test -race ./internal/planrun/ ./internal/mcpsrv/ -run 'TestLatest|TestLedger_|TestPlanRunReport|TestValidateTaskSpec_PlanRunIDAdvisory|TestValidatePlan_LedgerHeader|TestValidatePlan_OneLedgerHeader' -v`
Expected: PASS, including the existing `TestPlanRunReport_UnknownID`, `TestPlanRunReport_FoundZeroRows_TasksWireEmptyArray` and `TestPlanRunReport_RestartFallsBackToLedger`.

Run: `go test -race ./...`
Expected: PASS

- [ ] **Step 9: CHANGELOG**

Under `## [0.22.0] - 2026-09-15`, add to `### Added`:

```markdown
- `validate_task_spec` called without `plan_run_id` while the server holds a live plan run
  returns a minor `other` finding naming the most recently created run, so the task can be
  attached. It is added after the verdict is decided and never changes it.
- With the plan ledger enabled, `validate_plan` records a header for each run it mints, so
  `plan_run_report` recognises a run no task was attached to — including after a restart — and
  says no `validate_task_spec` call passed its `plan_run_id`.
```

and to `### Changed`:

```markdown
- `plan_run_report`'s unknown-run evidence leads with the usual cause, a `validate_task_spec`
  call that never passed `plan_run_id`, before idle expiry and a restarted server.
```

- [ ] **Step 10: Commit**

```bash
gofmt -l internal/ && go vet ./internal/...
git add CHANGELOG.md internal/planrun/planrun.go internal/planrun/ledger.go internal/planrun/planrun_test.go internal/planrun/ledger_test.go internal/mcpsrv/review_error.go internal/mcpsrv/handlers.go internal/mcpsrv/handlers_plan_run_attach_test.go
git commit -m "feat(planrun): name the live run a task forgot, and remember runs no task joined"
```

```json:metadata
{"files": ["internal/planrun/planrun.go", "internal/planrun/ledger.go", "internal/planrun/planrun_test.go", "internal/planrun/ledger_test.go", "internal/mcpsrv/review_error.go", "internal/mcpsrv/handlers.go", "internal/mcpsrv/handlers_plan_run_attach_test.go", "CHANGELOG.md"], "verifyCommand": "go test -race ./internal/planrun/ ./internal/mcpsrv/ -run 'TestLatest|TestLedger_Header|TestPlanRunReport|TestValidateTaskSpec_PlanRunIDAdvisory|TestValidatePlan_LedgerHeader|TestValidatePlan_OneLedgerHeader' -v", "acceptanceCriteria": ["Store.Latest returns newest live run without touching it, nil-safe", "advisory names the run, never moves the verdict, absent when id passed or no run", "exactly one ledger header per freshly minted run, cache hit adds none; loads header-only, never a row, pruned by created_at", "report explains a known run with zero rows; restart with ledger reports it known", "unknown-run evidence orders causes by likelihood"], "modelTier": "standard"}
```

---

### Task 5: Real descriptions on every tool input property

**Goal:** Every property of every tool input schema has a real description that states its limits, and a contract test fails the build if one does not.

**Files:**
- Create: `internal/mcpsrv/tool_schema_contract_test.go`
- Modify: `internal/config/config.go` (new `DefaultMaxPayloadBytes` const, used by `Load`)
- Modify: `internal/mcpsrv/handlers.go` (`ValidateTaskSpecArgs`, `FileArg`, `CompletionFileArg`, `CheckProgressArgs`, `ValidateCompletionArgs`, `ValidatePlanArgs`, `PlanRunReportArgs`)
- Modify: `internal/mcpsrv/prime_handler.go` (`KBIndexEntryArg`, `PrimeProjectKnowledgeArgs`)
- Modify: `internal/mcpsrv/extract_handler.go` (`CompletionEnvelopeArg`, `ExtractProjectKnowledgeArgs`)
- Modify: `internal/mcpsrv/worker_handlers.go` (`BulkReadArgs`, `CodeWriteArgs`)
- Modify: `internal/session/session.go` (`HarnessShapeAttestation`)
- Modify: `internal/codescene/codescene.go` (`Verdicts`, `Digest`)
- Modify: `internal/verdict/verdict.go` (`Finding`)
- Modify: `CHANGELOG.md`

**Acceptance Criteria:**
- [ ] The `tools/list` tool set is exactly the nine named tools, and their input schemas contain no `$ref`, `$defs`, `definitions`, `allOf`, `anyOf` or `oneOf` node the contract walkers would skip
- [ ] Across all nine tools' `tools/list` input schemas, recursively (nested objects and array items), no property has an empty description or the literal `required`
- [ ] The stated limits match their Go constants: 50/500 on `validate_task_spec`'s five bounded string lists and on `exit_contracts`; 20/4000 on `normative_test_bodies`; 25, 240, 240, 10, 480 on `harness_shape_attestation`; `config.DefaultMaxPayloadBytes` on every payload-cap field; `maxContextFiles` on `context_paths`; `maxBulkReadPaths` on `bulk_read.paths`; `defaultMaxPicks`/`maxMaxPicks` on `max_picks`
- [ ] Required-ness of every property is unchanged, pinned by a test over every object's `required` set

**Non-goals:**
- Do not change JSON field names, field types, required-ness, or runtime validation limits.
- Do not add or remove tools.

**Context:**
- Descriptions are emitted through `tools/list` from `jsonschema` struct tags, including nested object and array-item types.
- `config.DefaultMaxPayloadBytes` becomes the shared source for the default payload-cap value stated in schemas and used by configuration loading.

**Verify:** `go test -race ./internal/mcpsrv/ -run 'TestToolInputSchemas' -v` → PASS

**Steps:**

- [ ] **Step 1: Write the failing contract test**

Create `internal/mcpsrv/tool_schema_contract_test.go`:

```go
package mcpsrv

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/config"
	"github.com/patiently/anti-tangent-mcp/internal/providers"
)

// toolInputSchemas lists every tool through a real in-memory MCP client and
// returns each input schema decoded as plain JSON, keyed by tool name.
func toolInputSchemas(t *testing.T) map[string]map[string]any {
	t.Helper()
	cfg, err := config.Load(func(k string) string {
		if k == "ANTHROPIC_API_KEY" {
			return "k"
		}
		return ""
	})
	require.NoError(t, err)
	srv := New(Deps{Cfg: cfg, Reviews: providers.Registry{"anthropic": &fakeReviewer{name: "anthropic"}}})

	st, ct := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	go func() { _ = srv.Run(ctx, st) }()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, ct, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })

	res, err := cs.ListTools(ctx, nil)
	require.NoError(t, err)
	out := map[string]map[string]any{}
	for _, tool := range res.Tools {
		b, err := json.Marshal(tool.InputSchema)
		require.NoError(t, err)
		var schema map[string]any
		require.NoError(t, json.Unmarshal(b, &schema), tool.Name)
		out[tool.Name] = schema
	}
	return out
}

// propertyDescriptions walks object properties and array items, recording each
// property's description under a dotted path; array items add "[]".
func propertyDescriptions(schema map[string]any, path string, into map[string]string) {
	if props, ok := schema["properties"].(map[string]any); ok {
		for name, raw := range props {
			prop, _ := raw.(map[string]any)
			p := path + "." + name
			desc, _ := prop["description"].(string)
			into[p] = desc
			propertyDescriptions(prop, p, into)
		}
	}
	if items, ok := schema["items"].(map[string]any); ok {
		propertyDescriptions(items, path+"[]", into)
	}
}

func allPropertyDescriptions(t *testing.T) map[string]string {
	t.Helper()
	descs := map[string]string{}
	for name, schema := range toolInputSchemas(t) {
		propertyDescriptions(schema, name, descs)
	}
	return descs
}

// schemaIndirections lists every $ref, $defs, definitions, allOf, anyOf or oneOf
// node under node. propertyDescriptions and requiredSets follow only properties
// and items, which covers a schema completely only while it has none of these.
func schemaIndirections(node any, path string, into *[]string) {
	switch v := node.(type) {
	case map[string]any:
		for key, child := range v {
			switch key {
			case "$ref", "$defs", "definitions", "allOf", "anyOf", "oneOf":
				*into = append(*into, path+"."+key)
			}
			schemaIndirections(child, path+"."+key, into)
		}
	case []any:
		for i, child := range v {
			schemaIndirections(child, fmt.Sprintf("%s[%d]", path, i), into)
		}
	}
}

func TestToolInputSchemas_ToolSetAndShape(t *testing.T) {
	schemas := toolInputSchemas(t)
	names := make([]string, 0, len(schemas))
	var indirections []string
	for name, schema := range schemas {
		names = append(names, name)
		schemaIndirections(schema, name, &indirections)
	}
	sort.Strings(names)
	assert.Equal(t, []string{
		"bulk_read", "check_progress", "code_write", "extract_project_knowledge", "plan_run_report",
		"prime_project_knowledge", "validate_completion", "validate_plan", "validate_task_spec",
	}, names)
	sort.Strings(indirections)
	assert.Empty(t, indirections, "the contract walkers do not follow these nodes; extend them before trusting the other schema tests")
}

func TestToolInputSchemas_EveryPropertyDescribed(t *testing.T) {
	descs := allPropertyDescriptions(t)
	var missing []string
	for path, desc := range descs {
		if strings.TrimSpace(desc) == "" || desc == "required" {
			missing = append(missing, fmt.Sprintf("%s (%q)", path, desc))
		}
	}
	sort.Strings(missing)
	assert.Empty(t, missing, "input properties without a real description")
}

// requiredSets records each object's sorted required property names under the
// same dotted path propertyDescriptions uses.
func requiredSets(schema map[string]any, path string, into map[string][]string) {
	if req, ok := schema["required"].([]any); ok && len(req) > 0 {
		names := make([]string, 0, len(req))
		for _, r := range req {
			names = append(names, fmt.Sprint(r))
		}
		sort.Strings(names)
		into[path] = names
	}
	if props, ok := schema["properties"].(map[string]any); ok {
		for name, raw := range props {
			prop, _ := raw.(map[string]any)
			requiredSets(prop, path+"."+name, into)
		}
	}
	if items, ok := schema["items"].(map[string]any); ok {
		requiredSets(items, path+"[]", into)
	}
}

// TestToolInputSchemas_RequiredSetsUnchanged pins every object's required set.
// Descriptions live in jsonschema tags and cannot change required-ness, but a
// json tag edited alongside one can; update this table only for an intended
// change to a tool's contract. validate_completion.codescene.verdicts is
// absent on purpose: its keys are optional so a partial digest is accepted.
func TestToolInputSchemas_RequiredSetsUnchanged(t *testing.T) {
	want := map[string][]string{
		"bulk_read":                        {"paths", "question"},
		"check_progress":                   {"session_id", "working_on"},
		"check_progress.changed_files[]":   {"content", "path"},
		"code_write":                       {"reference_path", "spec"},
		"extract_project_knowledge":        {"completion_envelopes"},
		"extract_project_knowledge.completion_envelopes[]":               {"summary", "verdict"},
		"extract_project_knowledge.completion_envelopes[].final_files[]": {"content", "path"},
		"extract_project_knowledge.completion_envelopes[].findings[]":    {"category", "criterion", "evidence", "severity", "suggestion"},
		"extract_project_knowledge.kb_index[]":                           {"permalink", "summary", "title", "type"},
		"plan_run_report":                                                {"plan_run_id"},
		"prime_project_knowledge":                                        {"acceptance_criteria", "goal", "task_title"},
		"prime_project_knowledge.kb_index[]":                             {"permalink", "summary", "title", "type"},
		"validate_completion":                                            {"session_id", "summary"},
		"validate_completion.final_files[]":                              {"path"},
		"validate_task_spec":                                             {"goal", "task_title"},
		"validate_task_spec.harness_shape_attestation[]":                 {"assertions", "harness", "path"},
	}
	got := map[string][]string{}
	for name, schema := range toolInputSchemas(t) {
		requiredSets(schema, name, got)
	}
	assert.Equal(t, want, got)
}

func TestToolInputSchemas_StatedLimitsMatchConstants(t *testing.T) {
	descs := allPropertyDescriptions(t)
	n := strconv.Itoa
	bounded := []string{n(maxPinnedByEntries), n(maxPinnedByChars)}
	payload := []string{n(config.DefaultMaxPayloadBytes), "ANTI_TANGENT_MAX_PAYLOAD_BYTES"}
	cases := map[string][]string{
		"validate_task_spec.pinned_by":                                 bounded,
		"validate_task_spec.controller_verified_references":            bounded,
		"validate_task_spec.test_strategy_notes":                       bounded,
		"validate_task_spec.codebase_conventions":                      bounded,
		"validate_task_spec.testability_extractions":                   bounded,
		"validate_task_spec.normative_test_bodies":                     {n(maxNormativeTestBodyEntries), n(maxNormativeTestBodyChars)},
		"validate_task_spec.harness_shape_attestation":                 {n(maxHarnessShapeAttestationEntries)},
		"validate_task_spec.harness_shape_attestation[].harness":       {n(maxHarnessShapeAttestationHarnessChars)},
		"validate_task_spec.harness_shape_attestation[].path":          {n(maxHarnessShapeAttestationPathChars)},
		"validate_task_spec.harness_shape_attestation[].assertions":    {n(maxHarnessShapeAttestationAssertions), n(maxHarnessShapeAttestationAssertionChars)},
		"validate_task_spec.project_knowledge":                         payload,
		"validate_completion.exit_contracts":                           bounded,
		"validate_completion.final_diff":                               payload,
		"validate_completion.final_files":                              payload,
		"check_progress.changed_files":                                 payload,
		"bulk_read.paths":                                              append([]string{n(maxBulkReadPaths)}, payload...),
		"validate_plan.context_paths":                                  {n(maxContextFiles)},
		"prime_project_knowledge.max_picks":                            {n(defaultMaxPicks), n(maxMaxPicks)},
		"validate_completion.final_diff_path":                          {"ANTI_TANGENT_PLAN_ROOTS"},
		"validate_plan.plan_path":                                      {"ANTI_TANGENT_PLAN_ROOTS"},
		"validate_completion.codescene":                                {"analyze_change_set", "pre_commit_code_health_safeguard"},
	}
	for path, wants := range cases {
		desc, ok := descs[path]
		if !assert.True(t, ok, "no property %s", path) {
			continue
		}
		for _, want := range wants {
			re := regexp.MustCompile(`(^|[^0-9A-Za-z_])` + regexp.QuoteMeta(want) + `($|[^0-9A-Za-z_])`)
			assert.Regexp(t, re, desc, "%s must state %s", path, want)
		}
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test -race ./internal/mcpsrv/ -run TestToolInputSchemas -v`
Expected: FAIL to compile — `undefined: config.DefaultMaxPayloadBytes`.

- [ ] **Step 3: Add the payload-cap constant**

In `internal/config/config.go`, add above `func Load`:

```go
// DefaultMaxPayloadBytes is the ANTI_TANGENT_MAX_PAYLOAD_BYTES default. Tool
// input descriptions state it, and a contract test holds them to this value.
const DefaultMaxPayloadBytes = 204800
```

and in `Load`, replace `MaxPayloadBytes:        204800,` with `MaxPayloadBytes:        DefaultMaxPayloadBytes,`.

Run: `go test -race ./internal/mcpsrv/ -run TestToolInputSchemas -v`
Expected: FAIL — `EveryPropertyDescribed` lists undescribed properties across all nine tools; `StatedLimitsMatchConstants` fails on every case. `RequiredSetsUnchanged` already PASSES: it is the guard that Step 4's tag edits must keep green.

- [ ] **Step 4: Describe every property**

Replace each struct below with the tagged version. Only tags change; field names, types and `json` tags stay exactly as they are, so required-ness is unchanged. Keep each struct's existing doc comment and the existing `PlanRunID` field comment.

`internal/mcpsrv/handlers.go`:

```go
type ValidateTaskSpecArgs struct {
	TaskTitle                    string                            `json:"task_title"           jsonschema:"The task's title, verbatim from its heading in the plan."`
	Goal                         string                            `json:"goal"                 jsonschema:"The task's Goal line, verbatim."`
	AcceptanceCriteria           []string                          `json:"acceptance_criteria,omitempty" jsonschema:"The task's acceptance criteria, one entry per bullet, verbatim."`
	NonGoals                     []string                          `json:"non_goals,omitempty" jsonschema:"The task's Non-goals bullets, verbatim, when the task has them."`
	Context                      string                            `json:"context,omitempty" jsonschema:"The task's Context section, verbatim: constraints, repo carve-outs and prior decisions a fresh implementer needs. The reviewer treats it as authoritative."`
	PinnedBy                     []string                          `json:"pinned_by,omitempty" jsonschema:"Existing tests, docs, commands or static checks that pin behavior an acceptance criterion says stays unchanged. Caller-supplied anchors, not verified facts. At most 50 entries of at most 500 characters each."`
	ControllerVerifiedReferences []string                          `json:"controller_verified_references,omitempty" jsonschema:"Paths, symbols, line anchors, commands or adjacent patterns the controller already verified before dispatch; a matching unverifiable_codebase_claim finding is suppressed by substring match. At most 50 entries of at most 500 characters each, so split a long reference list into several short entries."`
	TestStrategyNotes            []string                          `json:"test_strategy_notes,omitempty" jsonschema:"How tests divide coverage between this task and adjacent ones, so complementary tests read as joint coverage. At most 50 entries of at most 500 characters each."`
	CodebaseConventions          []string                          `json:"codebase_conventions,omitempty" jsonschema:"Module conventions the task must follow; a spec that conflicts with one draws convention_deviation. At most 50 entries of at most 500 characters each."`
	TestabilityExtractions       []string                          `json:"testability_extractions,omitempty" jsonschema:"Code the task deliberately extracts to make it testable, so the reviewer does not flag the extraction as scope_drift. At most 50 entries of at most 500 characters each."`
	NormativeTestBodies          []string                          `json:"normative_test_bodies,omitempty" jsonschema:"Test bodies the implementer must land verbatim; the reviewer treats each as binding scope. At most 20 entries of at most 4000 characters each."`
	HarnessShapeAttestation      []session.HarnessShapeAttestation `json:"harness_shape_attestation,omitempty" jsonschema:"Caller-attested facts about test harnesses or fixtures; an acceptance criterion that contradicts one draws attestation_contradiction. At most 25 entries."`
	ProjectKnowledge             string                            `json:"project_knowledge,omitempty" jsonschema:"Markdown excerpts from the project knowledge base that the controller selected for this task. The reviewer treats them as authoritative. Counts toward the payload cap, ANTI_TANGENT_MAX_PAYLOAD_BYTES, default 204800 bytes."`
	Phase                        string                            `json:"phase,omitempty" jsonschema:"pre, the default, for the review at task start; post only for a post-hoc or session-recovery review."`
	ModelOverride                string                            `json:"model_override,omitempty" jsonschema:"Reviewer model for this call only, as provider:model, such as anthropic:claude-opus-4-7. Must be on the server's model allowlist."`
	MaxTokensOverride            int                               `json:"max_tokens_override,omitempty" jsonschema:"Reviewer output-token budget for this call only. 0 uses the configured default; a value above ANTI_TANGENT_MAX_TOKENS_CEILING is clamped with a minor finding; a negative value is rejected."`
	// PlanRunID ties this task to a plan run minted by validate_plan. Best
	// effort: an unknown or expired id must not fail the review.
	PlanRunID string `json:"plan_run_id,omitempty" jsonschema:"The plan_run_id from the controller's final passing validate_plan call. It attaches this task to that plan run so plan_run_report can include it; an unknown or expired id does not fail the call."`
}
```

```go
type FileArg struct {
	Path    string `json:"path" jsonschema:"Path of the file, as the task names it."`
	Content string `json:"content" jsonschema:"The file's full current content."`
}
```

```go
type CompletionFileArg struct {
	Path    string  `json:"path" jsonschema:"Path of the file. When content is omitted it must be absolute and the server reads the file itself; with ANTI_TANGENT_PLAN_ROOTS set, it must be under one of those roots."`
	Content *string `json:"content,omitempty" jsonschema:"The file's full content. Omit it to have the server read path from disk; send an empty string for a deleted or genuinely empty file."`
}
```

```go
type CheckProgressArgs struct {
	SessionID         string    `json:"session_id"     jsonschema:"The session_id returned by this task's validate_task_spec call."`
	WorkingOn         string    `json:"working_on"     jsonschema:"What you are doing right now, in a sentence or two. The reviewer checks the changed files against it for drift."`
	ChangedFiles      []FileArg `json:"changed_files,omitempty" jsonschema:"Files changed so far, each with its full current content. Counts toward the payload cap, ANTI_TANGENT_MAX_PAYLOAD_BYTES, default 204800 bytes."`
	Questions         []string  `json:"questions,omitempty" jsonschema:"Open questions you want the reviewer to weigh in on."`
	ModelOverride     string    `json:"model_override,omitempty" jsonschema:"Reviewer model for this call only, as provider:model, such as anthropic:claude-opus-4-7. Must be on the server's model allowlist."`
	MaxTokensOverride int       `json:"max_tokens_override,omitempty" jsonschema:"Reviewer output-token budget for this call only. 0 uses the configured default; a value above ANTI_TANGENT_MAX_TOKENS_CEILING is clamped with a minor finding; a negative value is rejected."`
}
```

```go
type ValidateCompletionArgs struct {
	SessionID             string              `json:"session_id"  jsonschema:"The session_id returned by this task's validate_task_spec call. An empty string runs a lightweight review with no session."`
	Summary               string              `json:"summary"     jsonschema:"What you implemented and how each acceptance criterion is met. A claim in the summary is not evidence on its own."`
	FinalFiles            []CompletionFileArg `json:"final_files,omitempty" jsonschema:"Changed files, with full content or with content omitted so the server reads them. Counts toward the payload cap, ANTI_TANGENT_MAX_PAYLOAD_BYTES, default 204800 bytes; do not also send a file that final_diff already covers."`
	FinalDiff             string              `json:"final_diff,omitempty" jsonschema:"A unified diff of the task's changes. Counts toward the payload cap, ANTI_TANGENT_MAX_PAYLOAD_BYTES, default 204800 bytes; when it is large, generate it with -U1 and leave out generated, lockfile and snapshot files."`
	FinalDiffPath         string              `json:"final_diff_path,omitempty" jsonschema:"Absolute path to a unified diff file that the server reads instead of final_diff. With ANTI_TANGENT_PLAN_ROOTS set it must be under one of those roots, which a per-session scratch directory under /tmp usually is not."`
	TestEvidence          string              `json:"test_evidence,omitempty" jsonschema:"The test run output that proves the change, verbatim. Output showing no test executed draws a finding."`
	ExitContracts         []string            `json:"exit_contracts,omitempty" jsonschema:"Symbols or behavior later tasks rely on this task leaving in place, copied from validate_plan's exit_contracts for this task; a hard miss draws missing_acceptance_criterion. At most 50 entries of at most 500 characters each."`
	ExitContractsInferred bool                `json:"exit_contracts_inferred,omitempty" jsonschema:"validate_plan's exit_contracts_inferred for this task: true when the contracts were inferred from cross-task references rather than written in the plan, which caps a miss at minor."`
	ModelOverride         string              `json:"model_override,omitempty" jsonschema:"Reviewer model for this call only, as provider:model, such as anthropic:claude-opus-4-7. Must be on the server's model allowlist."`
	MaxTokensOverride     int                 `json:"max_tokens_override,omitempty" jsonschema:"Reviewer output-token budget for this call only. 0 uses the configured default; a value above ANTI_TANGENT_MAX_TOKENS_CEILING is clamped with a minor finding; a negative value is rejected."`
	Codescene             *codescene.Digest   `json:"codescene,omitempty" jsonschema:"The CodeScene result for this task: analyze_change_set's raw JSON, or the reduced digest. Unknown keys are ignored. pre_commit_code_health_safeguard sees only uncommitted changes, so after a commit it reports zero files and is not a run of the task. When a run was attempted and failed, send ran false with skip_reason and skip_evidence."`
}
```

```go
type ValidatePlanArgs struct {
	PlanText          string   `json:"plan_text,omitempty" jsonschema:"Deprecated and removed in 1.0.0: the plan markdown inline. Pass plan_path instead; exactly one of plan_text and plan_path must be set."`
	PlanPath          string   `json:"plan_path,omitempty" jsonschema:"Absolute path to the plan markdown file, which the server reads. With ANTI_TANGENT_PLAN_ROOTS set it must be under one of those roots. Exactly one of plan_text and plan_path must be set."`
	ProjectKnowledge  string   `json:"project_knowledge,omitempty" jsonschema:"Markdown excerpts from the project knowledge base that the controller selected for this plan. The reviewer treats them as authoritative."`
	ModelOverride     string   `json:"model_override,omitempty" jsonschema:"Reviewer model for this call only, as provider:model, such as anthropic:claude-opus-4-7. Must be on the server's model allowlist."`
	MaxTokensOverride int      `json:"max_tokens_override,omitempty" jsonschema:"Reviewer output-token budget for this call only. 0 uses the configured default scaled by task count; a value above ANTI_TANGENT_MAX_TOKENS_CEILING is clamped with a minor finding; a negative value is rejected."`
	Mode              string   `json:"mode,omitempty" jsonschema:"thorough, the default, or quick, which surfaces only the most severe findings, at most 3 per scope."`
	ContextPaths      []string `json:"context_paths,omitempty" jsonschema:"Absolute paths to source files the plan makes claims about, at most 50. Each file is sent in full to the reviewer vendor on every reviewer call of the round, so attach only files the plan touches and never secrets. With ANTI_TANGENT_PLAN_ROOTS set they must be under those roots."`
	RepoRoot          string   `json:"repo_root,omitempty" jsonschema:"Absolute path to the repository root; enables the disk tier of the Create/Modify consistency check. With ANTI_TANGENT_PLAN_ROOTS set it must be under those roots."`
}
```

```go
type PlanRunReportArgs struct {
	PlanRunID string `json:"plan_run_id" jsonschema:"The plan_run_id returned by the final passing validate_plan call of the run."`
}
```

`internal/mcpsrv/prime_handler.go`:

```go
type KBIndexEntryArg struct {
	Permalink string   `json:"permalink" jsonschema:"The note's permalink."`
	Type      string   `json:"type" jsonschema:"The note's type, such as decision, module or gotcha."`
	Title     string   `json:"title" jsonschema:"The note's title."`
	Summary   string   `json:"summary" jsonschema:"One or two sentences on what the note covers."`
	Tags      []string `json:"tags,omitempty" jsonschema:"The note's tags."`
}
```

```go
type PrimeProjectKnowledgeArgs struct {
	TaskTitle          string            `json:"task_title"          jsonschema:"The task's title, verbatim."`
	Goal               string            `json:"goal"                jsonschema:"The task's Goal line, verbatim."`
	AcceptanceCriteria []string          `json:"acceptance_criteria" jsonschema:"The task's acceptance criteria, one entry per bullet."`
	NonGoals           []string          `json:"non_goals,omitempty" jsonschema:"The task's Non-goals bullets, when the task has them."`
	Context            string            `json:"context,omitempty" jsonschema:"The task's Context section, when it has one."`
	KBIndex            []KBIndexEntryArg `json:"kb_index,omitempty" jsonschema:"The knowledge-base notes available to pick from, one entry per note."`
	EpicPermalink      string            `json:"epic_permalink,omitempty" jsonschema:"Permalink of the epic note in flight, when there is one; picks lean toward that epic."`
	MaxPicks           int               `json:"max_picks,omitempty" jsonschema:"The most picks to return. 0 or a negative value uses 10; a value above 25 is capped at 25."`
	ModelOverride      string            `json:"model_override,omitempty" jsonschema:"Model for this call only, as provider:model, such as anthropic:claude-opus-4-7. Must be on the server's model allowlist."`
	MaxTokensOverride  int               `json:"max_tokens_override,omitempty" jsonschema:"Output-token budget for this call only. 0 uses the configured default; a value above ANTI_TANGENT_MAX_TOKENS_CEILING is clamped with a minor finding; a negative value is rejected."`
}
```

`internal/mcpsrv/extract_handler.go`:

```go
type CompletionEnvelopeArg struct {
	TaskTitle    string            `json:"task_title,omitempty" jsonschema:"The task's title."`
	Summary      string            `json:"summary" jsonschema:"The summary the implementer sent to validate_completion."`
	Verdict      string            `json:"verdict" jsonschema:"The envelope's verdict: pass, warn or fail."`
	Findings     []verdict.Finding `json:"findings,omitempty" jsonschema:"The envelope's findings, as returned."`
	FinalDiff    string            `json:"final_diff,omitempty" jsonschema:"The unified diff the implementer submitted."`
	FinalFiles   []FileArg         `json:"final_files,omitempty" jsonschema:"The files the implementer submitted, with full content."`
	TestEvidence string            `json:"test_evidence,omitempty" jsonschema:"The test output the implementer submitted."`
}
```

```go
type ExtractProjectKnowledgeArgs struct {
	CompletionEnvelopes []CompletionEnvelopeArg `json:"completion_envelopes" jsonschema:"One or more validate_completion envelopes from the finished tasks, as returned. Must not be empty."`
	PlanText            string                  `json:"plan_text,omitempty" jsonschema:"The plan markdown, for context on what the tasks set out to do."`
	KBIndex             []KBIndexEntryArg       `json:"kb_index,omitempty" jsonschema:"The knowledge-base notes that already exist, so proposals update them instead of duplicating them."`
	CurrentKBExcerpts   map[string]string       `json:"current_kb_excerpts,omitempty" jsonschema:"Current bodies of notes a proposal may update or supersede, keyed by permalink."`
	EpicPermalink       string                  `json:"epic_permalink,omitempty" jsonschema:"Permalink of the epic note in flight; new decision proposals record it as their origin and add a progress-ledger entry to it."`
	ModelOverride       string                  `json:"model_override,omitempty" jsonschema:"Model for this call only, as provider:model, such as anthropic:claude-opus-4-7. Must be on the server's model allowlist."`
	MaxTokensOverride   int                     `json:"max_tokens_override,omitempty" jsonschema:"Output-token budget for this call only. 0 uses the configured default; a value above ANTI_TANGENT_MAX_TOKENS_CEILING is clamped with a minor finding; a negative value is rejected."`
}
```

`internal/mcpsrv/worker_handlers.go`:

```go
type BulkReadArgs struct {
	Question          string   `json:"question" jsonschema:"A specific question about the files. Only the answer comes back; the file contents never enter your context."`
	Paths             []string `json:"paths"    jsonschema:"Absolute paths of the files to read, at most 50. Each file is sent in full to the worker provider. With ANTI_TANGENT_PLAN_ROOTS set they must be under those roots, and together they count toward the payload cap, ANTI_TANGENT_MAX_PAYLOAD_BYTES, default 204800 bytes."`
	Model             string   `json:"model,omitempty" jsonschema:"Worker model for this call only, as provider:model. Defaults to ANTI_TANGENT_WORKER_MODEL."`
	MaxTokensOverride int      `json:"max_tokens_override,omitempty" jsonschema:"Worker output-token budget for this call only. 0 uses ANTI_TANGENT_WORKER_MAX_TOKENS; a value above ANTI_TANGENT_MAX_TOKENS_CEILING is clamped."`
}
```

```go
type CodeWriteArgs struct {
	Spec              string `json:"spec"           jsonschema:"What to generate. Only boilerplate that follows the reference file's pattern; logic that needs judgement is out of scope."`
	ReferencePath     string `json:"reference_path" jsonschema:"Absolute path to an existing file whose conventions the generated code follows. Its full content is sent to the worker provider. With ANTI_TANGENT_PLAN_ROOTS set it must be under those roots."`
	TargetPath        string `json:"target_path,omitempty" jsonschema:"Absolute path to write the generated code to, so it never enters your context. Its parent directory must already exist and, with ANTI_TANGENT_PLAN_ROOTS set, be under those roots. Refused on Windows."`
	Overwrite         bool   `json:"overwrite,omitempty" jsonschema:"Replace an existing target_path. False, the default, refuses to write over an existing file."`
	Model             string `json:"model,omitempty" jsonschema:"Worker model for this call only, as provider:model. Defaults to ANTI_TANGENT_WORKER_MODEL."`
	MaxTokensOverride int    `json:"max_tokens_override,omitempty" jsonschema:"Worker output-token budget for this call only. 0 uses ANTI_TANGENT_WORKER_MAX_TOKENS; a value above ANTI_TANGENT_MAX_TOKENS_CEILING is clamped."`
}
```

`internal/session/session.go`:

```go
type HarnessShapeAttestation struct {
	Harness    string   `json:"harness" jsonschema:"Name of the test harness or fixture. At most 240 characters."`
	Path       string   `json:"path" jsonschema:"Path of the harness or fixture file. At most 240 characters."`
	Assertions []string `json:"assertions" jsonschema:"Facts about the harness's shape that the caller attests to. At most 10 entries of at most 480 characters each."`
}
```

`internal/codescene/codescene.go` (keep the existing trailing `// passed|failed` and `// improvement|regression|neutral` comments):

```go
type Verdicts struct {
	Improved int `json:"improved" jsonschema:"Files whose Code Health improved."`
	Degraded int `json:"degraded" jsonschema:"Files whose Code Health degraded."`
	Stable   int `json:"stable" jsonschema:"Files whose Code Health did not change."`
}
```

```go
type Digest struct {
	Ran            bool           `json:"ran,omitempty" jsonschema:"True when a CodeScene analysis of the task's changes actually ran."`
	SkipReason     string         `json:"skip_reason,omitempty" jsonschema:"Why the analysis did not run, when ran is false. The first 300 characters are kept."`
	SkipEvidence   string         `json:"skip_evidence,omitempty" jsonschema:"The failing tool's own error text, when ran is false. Without it a skip is graded like no analysis at all. The first 2000 characters are kept."`
	Tool           string         `json:"tool,omitempty" jsonschema:"The CodeScene tool that produced the result, normally analyze_change_set."`
	QualityGate    string         `json:"quality_gate,omitempty" jsonschema:"passed or failed; analyze_change_set reports it as quality_gates."` // passed|failed
	FilesAnalyzed  int            `json:"files_analyzed,omitempty" jsonschema:"Number of files analysed: the length of analyze_change_set's results."`
	Verdicts       *Verdicts      `json:"verdicts,omitempty" jsonschema:"Per-file verdict counts."`
	Trend          string         `json:"trend,omitempty" jsonschema:"Ignored on input; the server derives it from net_pp."` // improvement|regression|neutral
	NetPP          float64        `json:"net_pp,omitempty" jsonschema:"Net change in problem points: the sum of new-pp minus old-pp over every finding. Positive means worse."`
	CategoryCounts map[string]int `json:"category_counts,omitempty" jsonschema:"Number of findings per CodeScene category, such as Complex Method. The 20 largest are kept."`
}
```

`internal/verdict/verdict.go`:

```go
type Finding struct {
	Severity   Severity `json:"severity" jsonschema:"critical, major or minor."`
	Category   Category `json:"category" jsonschema:"The finding's category, such as missing_acceptance_criterion or scope_drift."`
	Criterion  string   `json:"criterion" jsonschema:"The acceptance criterion or spec field the finding is about."`
	Evidence   string   `json:"evidence" jsonschema:"What the reviewer saw that supports the finding."`
	Suggestion string   `json:"suggestion" jsonschema:"The concrete next action that would resolve the finding."`
}
```

Run `gofmt -w internal/` afterwards so struct alignment is normalized.

- [ ] **Step 5: Run the contract tests to verify they pass**

Run: `go test -race ./internal/mcpsrv/ -run TestToolInputSchemas -v`
Expected: PASS. If `EveryPropertyDescribed` still lists a property, it belongs to a nested type not shown above: find the Go struct behind that path and give the field a description in the same style.

Run: `go test -race ./...`
Expected: PASS

- [ ] **Step 6: CHANGELOG**

Under `## [0.22.0] - 2026-09-15`, add to `### Added`:

```markdown
- Every tool input property carries a real description in the MCP schema, including its limits:
  entry and character caps on the bounded lists, the payload cap and its env var, the
  `ANTI_TANGENT_PLAN_ROOTS` rule on path inputs, and the accepted `codescene` shapes. Before this,
  a required field's description was the literal word `required` and every other field had none.
  A contract test over `tools/list` fails when a property is undescribed or a stated limit
  disagrees with the constant that enforces it.
```

- [ ] **Step 7: Commit**

```bash
gofmt -l internal/ && go vet ./internal/...
git add CHANGELOG.md internal/config/config.go internal/mcpsrv/handlers.go internal/mcpsrv/prime_handler.go internal/mcpsrv/extract_handler.go internal/mcpsrv/worker_handlers.go internal/mcpsrv/tool_schema_contract_test.go internal/session/session.go internal/codescene/codescene.go internal/verdict/verdict.go
git commit -m "feat(mcpsrv): describe every tool input property and hold limits to their constants"
```

```json:metadata
{"files": ["internal/mcpsrv/tool_schema_contract_test.go", "internal/config/config.go", "internal/mcpsrv/handlers.go", "internal/mcpsrv/prime_handler.go", "internal/mcpsrv/extract_handler.go", "internal/mcpsrv/worker_handlers.go", "internal/session/session.go", "internal/codescene/codescene.go", "internal/verdict/verdict.go", "CHANGELOG.md"], "verifyCommand": "go test -race ./internal/mcpsrv/ -run 'TestToolInputSchemas' -v", "acceptanceCriteria": ["tool set is exactly the nine named tools and schemas carry no indirection nodes", "no input property across the nine tools has an empty or 'required' description", "stated limits match their Go constants", "required-ness unchanged, pinned by a test over every object's required set"], "modelTier": "standard"}
```

---

## After the last task

- `go test -race ./...` passes and `goreleaser release --snapshot --clean --skip=publish` still builds.
- Open the PR from `version/0.22.0` to `main` with a title that contains `[skip ci]`, for example
  `v0.22.0 part 1: a self-describing tool surface [skip ci]`. Do not add `[minor]`: the release is
  cut by the Part 3 merge.
