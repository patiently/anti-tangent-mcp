# Audit MEDIUM fixes (v0.29.0) — design

**Status:** design, pre-implementation. The F1 and F3 directions in §1.3 are the maintainer's
choices (2026-10-11) and are settled.
**Date:** 2026-10-11
**Issues:** [#105](https://github.com/patiently/anti-tangent-mcp/issues/105) (F1),
[#106](https://github.com/patiently/anti-tangent-mcp/issues/106) (F3),
[#107](https://github.com/patiently/anti-tangent-mcp/issues/107) (F8)
**Input:** an external security audit of v0.25.0. It recorded three MEDIUM deployment findings, F1, F3
and F8, and the v0.28.0 re-audit confirmed that all three still stand at `cc5d3c5`. §2 verifies each
against this branch before designing from it.
**Release vehicle:** `0.29.0`, a backward-compatible minor. 0.28.0 is released and no `version/*`
branch above 0.25.0 is in flight. The work is built on `bugfix/v0-29-0-audit-medium-fixes` and merges
to `main` in a pull request titled `[minor]`. CI's CHANGELOG check runs only on `version/X.Y.Z`
branches, so the `## [0.29.0]` entry must be right without it. Do not bump `VERSION`; the release
workflow does that.

---

## 1. Summary

Three defects, each reachable by an ordinary agent following the protocol:

- **F1:** every path input sends whole files to a third-party model, and nothing stops a secrets file
  being one of them.
- **F3:** a lightweight `validate_completion` honours rulings that no controller issued and that name
  findings nobody has seen yet.
- **F8:** a session-less call can overwrite the session-backed result `plan_run_report` shows the user,
  without the `(lite)` marker.

### 1.1 Goals

- An accidental secrets attachment is refused before any bytes leave the host, on every path input.
- A lightweight ruling carries the same guarantee as a session-backed one: it can only name a finding
  the server issued for that task.
- A `plan_run_report` row only ever shows a verdict produced by the kind of call it is marked as.

### 1.2 Non-goals

- **Stopping a determined caller.** The calling agent can read any file and paste it into `summary`,
  `final_diff` or `test_evidence`. No server-side filter changes that. F1 targets the accident.
- **Scanning inline text fields.** `final_diff`, `test_evidence`, `plan_text` and `summary` are
  text the caller wrote out deliberately. Only bytes the *server* reads from disk are gated.
- **Authenticating rulings.** On both paths an implementer can still resubmit a ruling it wrote itself
  on an ID it was shown. The controller's DONE check (§5.9) stays the mitigation. F3 closes the
  extra hole the lightweight path adds; it does not close v0.22.0's F2.
- **A per-tool `context_paths` switch.** It was offered and not chosen.

### 1.3 Decisions

| # | Decision | Chosen by |
|---|----------|-----------|
| D1 | F1 is fixed with a **path denylist plus a content scan**, both on by default, in the one shared reader. | Maintainer |
| D2 | F3 is fixed by **binding lightweight rulings to IDs the server issued on that plan-run row**. A call with no row cannot carry rulings. | Maintainer |
| D3 | F8: a lightweight call **never mutates a session-attached row**, and `Attach` **clears lite results** when it takes a row over. | This design (only one shape fits the report's contract) |
| D4 | A gated file is a **transport error**, the same as a path outside `ANTI_TANGENT_PLAN_ROOTS`. No reviewer call is made and no session is opened. | This design (matches §3.3's existing split: a bad path is an error, a cap breach is an envelope) |

---

## 2. Verification against `cc5d3c5`

| Claim | Holds? | Where |
|-------|--------|-------|
| F1: `context_paths` on both per-task tools reads whole files, 50 × 128 KiB, 512 KiB per call | Yes | `resolveContextPaths`, `context_files.go:96`; callers `handlers.go:153`, `:1902` |
| F1: the only gate is the roots, with no content filter | Yes. Worse than stated: empty roots, the default, means **unrestricted** | `withinRoots`, `file_source.go:246` |
| F1 is the same class as the other path inputs | Yes. All share one reader | `resolveFileInput`, `file_source.go:101`, called for `context_paths`, `final_files`, `final_diff_path`, `test_evidence_path`, `plan_path`, `bulk_read` paths, `code_write`'s `reference_path` and the stale-comment hint's repo reads |
| F3: lightweight rulings are only shape-checked | Yes | `lightweightRulings`, `finding_rulings.go:612`; called `handlers.go:2050` |
| F3: `same_as` extends a waiver through the ruled IDs | Yes. `shown` is the set of ruled IDs | `waiveRuled`, `finding_rulings.go:102` |
| F3: server findings cannot be waived | Yes | `waiveRuled` is applied to the reviewer's findings only |
| F3, an addition: a ruling can be issued **before** the finding is ever shown | Yes. Lightweight IDs are `Fingerprint(category, "", criterion)`, and the completion prompt pins criteria such as `comment_hygiene` and `correctness` | `fingerprintOf`, `finding_rulings.go:81` |
| F8: `UpsertLite` mutates a session-attached row in place, leaving `Lite=false` | Yes | `planrun.go:648`, `rowFor` `:664` |
| F8: the report renders it with no marker | Yes | `verdictCell`, `report.go:184` |
| F8: the call can set `rate_digest` | Yes | `withRateDigest` wraps the lite update, `plan_run_rows.go:286` |
| F8, an addition: the **reverse** route. A lite result survives an `Attach`, which only flips `Lite=false` | Yes | `Attach`, `planrun.go:600`, sets `SessionID`, `PreVerdict`, `Attempts` and `Lite` only |

The `noSessionResponsesAdvisory` text (`finding_rulings.go:512`) currently *recommends* lightweight
rulings ("which apply without a session"). It changes with F3.

---

## 3. F1 — a secrets gate on every file the server reads

### 3.1 Placement

A new leaf package, **`internal/secretgate`**, stdlib only, with one responsibility: deciding whether
a path or a byte slice looks like a secret. It holds no config parsing and no I/O.

```go
type Gate struct { /* compiled name rules + content rules; zero value = off */ }

func New(mode Mode, extraNames []string) (*Gate, error) // error: a malformed glob
func (g *Gate) CheckPath(requested, resolved string) error   // name tier
func (g *Gate) CheckContent(path string, b []byte) error     // content tier

type Refusal struct { Path, Tier, Rule string } // implements error
```

`resolveFileInput` (`internal/mcpsrv/file_source.go`) is the single place it is called. That gives
every path input the gate in one place, and adding a new path input gets it for free. The reader
takes the gate as a parameter, the way it takes `roots` today. Callers pass `cfg`'s gate. A nil
`*Gate` (tests, `off`) checks nothing.

Order inside `resolveFileInput`:

1. Existing absolute-path, symlink-resolution, control-char and roots checks.
2. **`CheckPath(requested, resolved)`**, before the file is opened. Both names are checked: a symlink
   called `notes.txt` that resolves to `.env` is refused, and so is a symlink called `.env`.
3. Existing open, stat and capped read.
4. **`CheckContent(resolved, bytes)`** on the bytes read, before they are returned.

### 3.2 Name tier

The match is against the **basename**, plus a few path-shaped rules. Matching is case-insensitive
on Windows, the way `withinRootsFold` handles it, and case-sensitive elsewhere.

| Rule | Matches |
|------|---------|
| `dotenv` | `.env`, `.env.*`, except `.env.example`, `.env.sample`, `.env.template`, `.env.dist` |
| `private-key-file` | `*.pem`, `*.key`, `*.p12`, `*.pfx`, `*.jks`, `*.keystore`, `*.kdbx` |
| `ssh-key` | `id_rsa`, `id_dsa`, `id_ecdsa`, `id_ed25519` (not `*.pub`); any file under a `.ssh/` directory except `*.pub`, `known_hosts`, `authorized_keys`, `config` |
| `credential-file` | `.netrc`, `_netrc`, `.git-credentials`, `.npmrc`, `.pypirc`, `.htpasswd`, `.pgpass` |
| `cloud-credentials` | `.aws/credentials`, `.aws/config`, `.docker/config.json`, `.kube/config`, `gcloud/credentials.db`, `gcloud/application_default_credentials.json` |
| `gnupg` | any file under a `.gnupg/` directory |
| `terraform-state` | `*.tfstate`, `*.tfstate.backup`, `*.tfvars` |
| `extra` | each glob in `ANTI_TANGENT_FILE_DENY_EXTRA` (see §3.4), matched against the basename and the full resolved path |

### 3.3 Content tier

The content tier runs on the bytes `resolveFileInput` read, which are already capped. Each rule is a
compiled regexp with a fixed prefix, so the false-positive rate stays low:

| Rule | Pattern (sketch) |
|------|------------------|
| `pem-private-key` | a `BEGIN … PRIVATE KEY` armour line followed by a base64 body (a line of 40+ base64 characters, optionally after `Proc-Type:` / `DEK-Info:` headers); a bare header in prose does not match |
| `aws-access-key-id` | `\b(AKIA|ASIA)[0-9A-Z]{16}\b` |
| `github-token` | `\bgh[pousr]_[A-Za-z0-9]{36,}\b`, `\bgithub_pat_[A-Za-z0-9_]{60,}\b` |
| `slack-token` | `\bxox[abposr]-[A-Za-z0-9-]{10,}` |
| `anthropic-key` | `\bsk-ant-[A-Za-z0-9_-]{20,}` |
| `openai-key` | `\bsk-(proj-)?[A-Za-z0-9_-]{32,}` (checked after `anthropic-key`) |
| `google-api-key` | `\bAIza[0-9A-Za-z_-]{35}\b` |
| `stripe-live-key` | `\b[rs]k_live_[0-9A-Za-z]{24,}\b` |

A `Refusal` names the path, the tier and the rule. **It never quotes the matched bytes.** Its
`Error()` string is built from `%q`-quoted parts, in line with `rejectControlChars`' invariant that
nothing caller-controlled reaches the summary block unescaped.

### 3.4 Configuration

| Var | Values | Default |
|-----|--------|---------|
| `ANTI_TANGENT_SECRET_GATE` | `on` (both tiers), `names` (name tier only), `off` | `on` |
| `ANTI_TANGENT_FILE_DENY_EXTRA` | OS path-list separated globs (`filepath.Match` syntax), appended to the name tier | empty |

An unknown `ANTI_TANGENT_SECRET_GATE` value, or a glob `filepath.Match` rejects, fails `config.Load`.
That matches how every other malformed variable behaves today. `names` exists for a repo whose diffs
legitimately carry key-shaped test fixtures, this one included, so an operator can keep the name tier
without the content tier.

### 3.5 Error shape per caller

`Refusal` passes through each caller's existing error wrapping, so the transport error reads like
`context_paths: "/abs/.env" refused by the secret gate (names: dotenv); remove it, or set
ANTI_TANGENT_SECRET_GATE to change the gate`. The remedy text lives once, in `Refusal.Error()`.

| Caller | Today on a bad path | With a gate refusal |
|--------|---------------------|---------------------|
| `context_paths` (all three tools) | transport error | transport error. On `validate_task_spec`, no session is opened (F6 behaviour) |
| `final_files[].path`, `final_diff_path`, `test_evidence_path`, `plan_path` | transport error | transport error |
| `bulk_read` paths, `code_write` `reference_path` | transport error | transport error |
| stale-comment hint repo reads | silently skipped (`readUnderRepoRoot`) | silently skipped. The hint loses one file; nothing is sent |

The one deliberate oddity: a gated `final_files` entry fails the whole completion call. A task that
legitimately edits `.env.production` has to submit a diff instead. The gate exists for exactly that
situation, and the error text says how to get past it.

### 3.6 Docs

- README: add both variables to the env table; one sentence on the gate next to the existing
  `ANTI_TANGENT_PLAN_ROOTS` line and in the `context_paths` descriptions.
- Tool schema text for `context_paths`, `final_files`, `final_diff_path`, `test_evidence_path`: add
  "a file the secret gate refuses fails the call". `tool_schema_contract_test.go` pins these strings.
- The design spec's file-input section (`docs/superpowers/specs/2026-05-07-anti-tangent-mcp-design.md`
  §3): one paragraph.
- Protocol: no change. The controller already writes briefs under the worktree, and a brief is not a
  secrets file.

---

## 4. F3 — lightweight rulings bound to issued IDs

### 4.1 Row state

`planrun.TaskRow` gains an in-memory field, written to no ledger:

```go
// IssuedIDs is the base IDs of the reviewer findings lightweight
// validate_completion calls on this task returned, oldest first, capped.
IssuedIDs []string `json:"-"`
```

It is capped at **200** with oldest-first eviction, the same bound as `measurements`. A new store
method reads the state a lightweight call needs, in one locked lookup:

```go
// LiteState is what a lightweight validate_completion needs to know about
// the row ref names before its review.
type LiteState struct {
    Found         bool     // the run is live and ref names a task
    SessionOwned  bool     // a validate_task_spec session is attached (F8)
    IssuedIDs     []string // copy
}
func (s *Store) LiteState(runID string, ref TaskRef) LiteState
```

`LiteState` does **not** create a row. Rows are still created only by `UpsertLite`, after the review.

### 4.2 Handler flow (lightweight branch, `handlers.go` around line 2050)

1. When `args.ControllerRulings` is empty: no change.
2. Shape-check as today (`planRulings`). Malformed IDs keep their existing advisory.
3. If the call has no `plan_run_id`, names no task, or `LiteState` reports `!Found`: **every
   ruling is ignored**, with one minor advisory, `criterion: controller_rulings`, using
   `ignoredArgumentAdvisory`:
   *"controller_rulings on a lightweight validate_completion apply only to finding ids an earlier
   lightweight validate_completion returned for the same plan_run_id and task; this call names no
   such task, so they were ignored."*
4. If `SessionOwned`, ignore every ruling with the same advisory shape and the F8 wording (§5.2).
   The finish-on-the-session remedy covers both.
5. Otherwise keep only the rulings whose `BaseID` is in `IssuedIDs`. The rest are ignored with one
   advisory listing them (they passed `ValidDisplayID`, so they are `f_` plus hex and safe to echo): *"These controller_rulings ids were not returned by an
   earlier validate_completion on this task, so they were ignored: …"*.
6. `review.shown` is the set of **`IssuedIDs`**, not the ruled IDs. A `same_as` can then reach a
   finding only through an ID the server actually issued, which is the session path's rule
   (`knownSessionFindings`).
7. After the review, every finding ID the envelope returns (`envelopeIDs`, waived ones included) is
   appended to the row's `IssuedIDs` inside the `UpsertLite` mutate, the same set the session path
   stores. Server findings' IDs are included. That is harmless, because `waiveRuled` never sees
   server findings, and it saves telling the two kinds apart.

`lightweightRulings` is replaced by a function taking `(rulingArgs, issued []string)` and returning
`(rulings, shown, malformed, unissued)`. It stays in `finding_rulings.go`.

### 4.3 Text that changes with it

- `noSessionResponsesAdvisory`: the suggestion stops saying rulings "apply without a session". New
  suggestion: *"Fix the finding, or, on a task in a plan run, have the controller rule on it and
  resubmit with plan_run_id, task_index and controller_rulings."*
- Tool schema text for `validate_completion.controller_rulings`: add the lightweight binding rule.
- `docs/protocol/implementer.md`, lightweight section: one sentence saying rulings on a lightweight
  call need `plan_run_id` + `task_index` and only cover IDs an earlier call on that task returned.
  **`implementer.md` is at 15,986 of 16,000 bytes**, so the sentence must be paid for by trimming the
  same section. Resync the plugin bundle in the same commit (CLAUDE.md "Editing the protocol").

### 4.4 What this does not change

- A session-backed call's rulings: untouched.
- Waiving server findings: still impossible.
- An implementer re-using an ID it was shown with a ruling it wrote itself: still possible, as on the
  session path (§1.2). The controller's §5.9 check covers both.

---

## 5. F8 — lite calls never touch session rows; sessions never inherit lite results

### 5.1 Store

- **`UpsertLite`** re-resolves the row under its lock. If the row has a non-empty `SessionID`, it
  **does not call `mutate`**, and returns the row with a new result value. Its signature becomes:

  ```go
  type UpsertResult int
  const (
      UpsertApplied UpsertResult = iota
      UpsertUnknown        // run unknown or expired, or ref names nothing
      UpsertSessionOwned   // row belongs to a session; nothing changed
  )
  func (s *Store) UpsertLite(runID string, ref TaskRef, mutate func(*TaskRow)) (TaskRow, UpsertResult)
  ```

  The re-check under the lock is what makes the outcome correct even when a session attaches
  between the handler's `LiteState` read and the upsert.

- **`Attach`**: when the row it takes over has `Lite == true`, reset every completion-derived field
  before recording the session: `PostVerdict`, `Severity`, `SubmissionOnly`, `Codescene`,
  `CodesceneState` (to `StateMissing`), `CompletedAt`, `Waived`, `Escalated`, `Categories`,
  `LinesAdded`, `LinesRemoved`, `OverBuildingRuled`, `RateDigest` and `IssuedIDs`. **Keep `Calls`**:
  it is a log of every call made for the task, and a lite call was made. A row that was never lite
  (a re-validation) is unchanged from today.

### 5.2 Handler

`recordLightweightCompletionRow` switches on the result. `UpsertUnknown` keeps today's warning log.
`UpsertSessionOwned` logs one warning. **The advisory, though, is decided before the review** from
`LiteState.SessionOwned`, so it can be in the envelope the caller sees. The advisory is minor,
`criterion: plan_run_id`, via `ignoredArgumentAdvisory`, appended after the verdict is final (like
`repoRootUnusableAdvisory`), so it never moves the verdict:

*"This task has a validate_task_spec session in the plan run, so a lightweight validate_completion is
not recorded against it."* Suggestion: *"Finish the task with validate_completion on its session_id."*

If the race in §5.1 happens (the session attaches after the read), the envelope has no advisory, but
the row is still left alone. That is the outcome that matters.

### 5.3 Report

No change to `report.go`: once a lite result can no longer reach a session row and a session never
inherits one, `verdictCell`'s two branches are correct as written.

---

## 6. Testing

All unit tests run under `-race`, with no network access.

**`internal/secretgate`**: table tests for every name rule, including the exceptions
(`.env.example`, `id_rsa.pub`, `.ssh/known_hosts`) and Windows folding through an injected GOOS.
Content tests for each pattern, with a near-miss for each (a 15-char AKIA tail, `sk-` too short).
One test that a `Refusal`'s `Error()` never contains the matched bytes. Glob errors from `New`.

**Fixtures never contain a literal key-shaped string.** Every content-tier test builds its sample at
runtime, by concatenation (the `AKIA` prefix plus a repeated filler, the armour line assembled from
parts). Otherwise this repository's own `final_diff_path` submissions, and any plan or spec that quotes
a sample, would trip the content tier once the gate ships. The plan document follows the same rule.

**`resolveFileInput`**: a refused name (direct, and through a symlink in each direction), a refused
content match, and `nil` gate passes both.

**Handlers**: one test per caller in §3.5's table that a refused file returns a transport error with
the rule name, and that no reviewer call happens (the stub reviewer's call count stays 0). For
`validate_task_spec`, that no session is created.

**Config**: both variables parse; bad values fail `Load`; `names` builds a gate with no content
rules.

**F3** (`handlers_rulings_test.go` style, stub reviewer):
- a lightweight call with a ruling and no `plan_run_id` → ruling ignored, advisory present, finding
  not waived;
- first call on a row with a ruling on a predictable ID (`Fingerprint(cat, "", "correctness")`)
  → ignored (not issued yet), with the unissued advisory;
- the audit's own sequence: call 1 returns `f_X` → call 2 rules on `f_X` → waived (the legitimate
  path still works);
- `same_as` naming an issued ID that carries a ruling waives the finding; naming a ruled-but-unissued
  ID does not;
- `IssuedIDs` caps at 200, oldest first.

**F8** (`internal/planrun`, plus a handler test):
- the audit's sequence: session completion `warn` → lite call on the same index → row still `warn`,
  `Lite=false`, `UpsertSessionOwned`, advisory in the envelope, `rate_digest` unchanged;
- reverse: lite `pass` → `Attach` → row has no `PostVerdict`, `Waived=0`, nil `RateDigest`,
  `CodesceneState=missing`, `Calls` kept; the report shows `open (pre: …)`;
- race: a session attaches between `LiteState` and `UpsertLite` (call the store directly) → no
  mutation.

---

## 7. Release

- `CHANGELOG.md` `## [0.29.0] - <date>`:
  - **Security:** the secret gate (F1), lightweight ruling binding (F3), and session rows protected
    from lite calls (F8). Each entry references its issue number.
  - **Changed:** the `noSessionResponsesAdvisory` wording, and the new env vars.
- The plugin bundle is resynced if the protocol changed. Bump `anti-tangent-protocol`'s plugin version
  (patch) only if its text changed.
- Merge to `main` with `[minor]` in the merge commit. Do not touch `VERSION`.

## 8. Open questions

None blocking. Two to check while implementing, without changing the design:
- Does any existing fixture under `internal/mcpsrv/testdata` get read through `resolveFileInput` in a
  test with a key-shaped string? If so, that test builds its config with a nil gate. No fixture
  changes.
- `bulk_read` with several paths, one of them gated: today any bad path fails the whole call. Keep
  that; do not add partial success.
