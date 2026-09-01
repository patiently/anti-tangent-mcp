# anti-tangent protocol — for plan authors

Read [`core.md`](core.md) first. This part covers the task-block format that maps onto
`validate_task_spec` inputs. If you will also dispatch the plan, read
[`controller.md`](controller.md).

## 3. For plan authors — the anti-tangent-friendly task format

Give each task a small structured header block. The implementing subagent passes these fields verbatim into `validate_task_spec`; the reviewer uses them to decide whether the spec is implementable as written.

### 3.1 The required shape

```markdown
### Task N: <one-line title>

**Goal:** <one sentence: what success looks like>

**Acceptance criteria:**
- <testable criterion 1>
- <testable criterion 2>

**Non-goals:** *(optional but recommended)*
- <thing this task explicitly does NOT cover>

**Context:** *(optional)*
<relevant background, constraints, or links a fresh implementer needs>

<… your existing plan structure: Files / Steps / Code / etc. …>
```

This is the shape superpowers' `writing-plans` skill emits. It writes
`**Acceptance Criteria:**` with a capital C; the parser matches case-insensitively, so either
casing works.

`writing-plans` does **not** emit `**Non-goals:**`. Plans from it arrive with no scope bound,
and the reviewer will say so. Add non-goals by hand, or set a repo-level instruction that does.

The existing "Files:" / "Steps:" structure that superpowers, hone-ai, and most CLAUDE.md plans already use lives below the header block. The header is additive.

### 3.2 Worked example

```markdown
### Task 4: Add /healthz endpoint

**Goal:** Expose a liveness probe for the HTTP server.

**Acceptance criteria:**
- `GET /healthz` returns HTTP 200 with body `ok`.
- p95 latency under 50 ms at 100 RPS on a warm process.
- Endpoint is registered in `cmd/api/router.go` and covered by a handler test.

**Non-goals:**
- Database health (covered separately by `/healthz/deep`).
- Authentication on the endpoint.

**Context:**
The service is a Gin app on port 8080. The probe is consumed by the
Kubernetes liveness check defined in `deploy/k8s/api.yaml`.
```

### 3.3 What `validate_task_spec` actually checks

- **Structural completeness.** Is the goal stated? Are there acceptance criteria? Are non-goals declared where they help bound scope?
- **Acceptance-criterion quality.** Is each AC testable, specific, and unambiguous? For any vague AC, the reviewer suggests a concrete rewrite.
- **Implicit assumptions.** Each assumption a fresh implementer would have to make becomes a finding, so the spec author can either pin it down or explicitly mark it as implementer's discretion.

### 3.5 Anti-pattern: keep implementation steps OUT of the AC list

Acceptance criteria describe *what done looks like*, not *how to get there*. Implementation steps belong in the "Steps:" / "Files:" portion of the task, where they always lived. Mixing them produces brittle ACs that the reviewer flags as either redundant or hyper-specific.

### 3.6 Normative test bodies (binding test code in plans)

When a task pastes verbatim test code the implementer must land as written, wrap each test body in a fenced block immediately under a literal `**NORMATIVE TEST BODIES (verbatim):**` header. `validate_plan` extracts each fence server-side and threads the list into the per-task `validate_task_spec` `normative_test_bodies` input; the reviewer treats each entry as binding scope. Adjacent fences extract as separate entries. Bodies > 4000 Unicode code points are server-truncated with a `// truncated` marker; for legitimately longer bodies, paraphrase or excerpt and prefix with `// excerpt:` so the reviewer treats it as partial coverage.

### 3.7 `.trimIndent()` raw-string caveat

When a plan snippet is wrapped in `.trimIndent()` (or any equivalent raw-string trim), multi-line source phrases render newlines exactly where they sit in the markdown — anti-tangent reads the source, not the rendered output. Keep example strings on a single source line, and phrase ACs against the rendered string (e.g. "output contains `please decline politely`"), not against source layout.

### 3.8 Harness shape attestations (v0.5.2+)

`harness_shape_attestation` is a structured optional input on `validate_task_spec`. Each entry is `{harness: string, path: string, assertions: []string}`. Use it when ACs depend on a test harness's stated capabilities (or non-capabilities). The reviewer treats each attestation as authoritative caller-attested context (no independent verification) and flags ACs that EXPLICITLY contradict an entry — e.g. an AC asking for behavior a `does not …` assertion forbids, or asserting a state directly contradicting a positive assertion — as `attestation_contradiction` findings. Absence of a capability is NOT a contradiction; do not list things to forbid them.

### 3.9 `**Files:**` bullet syntax (what the Create/Modify check parses)

`validate_plan` runs a deterministic, reviewer-free check that a task's `Modify:` targets can
exist when that task runs. It reads ONE structure and nothing else: a literal `**Files:**` line,
followed by bullets naming a verb and a path.

```markdown
**Files:**
- Create: `internal/planparser/filerefs.go`
- Modify: `internal/mcpsrv/handlers.go`
- Modify: `internal/verdict/parser.go:57-70`
- Create/Modify: `internal/prompts/templates/plan.tmpl`
- Delete: `internal/legacy/shim.go`
- Modify: internal/config/config.go (the roots parsing)
```

Rules the parser actually applies:

- The heading must be a line of its own reading `**Files:**` (case-insensitive).
- Bullets may use `-` or `*` and MUST have a space after the marker.
- The verb is `Create`, `Modify`, or `Delete`, case-insensitive. Two verbs may be joined with
  `/` (`Create/Modify:`) for a file one task creates and another edits; both are recorded.
- The path may be backtick-quoted or bare. Bare takes the first whitespace-delimited token.
- A trailing parenthetical (`(the roots parsing)`) is dropped, and so is a trailing line anchor
  — `:57`, `:57-70`, `:57,70` — so anchoring a `Modify:` to the lines you are editing is safe.
- Paths are repo-relative. Collection stops at the first line that is neither a bullet nor
  blank, so a following `**Steps:**` section is never harvested.

The section is OPTIONAL. A task without it yields no file operations and no findings — the check
guards plans that opt into the structure, it does not demand that they do. The `json:metadata`
fence's `files` array is a flat list with no verb, so it cannot drive this check; the bullets are
the only source.
