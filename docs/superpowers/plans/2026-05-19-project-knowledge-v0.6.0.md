# Project Knowledge (v0.6.0) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

> **Post-merge reconciliation note v2 (2026-05-20):** v0.5.2 landed on `main` after the previous reconciliation. Key new surface every implementer subagent must account for: (a) **`CategoryAttestationContradiction`** ("attestation_contradiction") added to the canonical category set — Task 1's `validCategory` switch and all four reviewer-output schema enums MUST include it alongside the existing categories AND the six new project-knowledge categories. The lock-step invariant test will fail if any schema omits it. (b) **`harness_shape_attestation`** field on `ValidateTaskSpecArgs` (handlers.go:52) and `session.TaskSpec.HarnessShapeAttestations` — Task 3's cumulative size guard must add `args.HarnessShapeAttestation` to the summed inputs (one more case in `totalNormalizedTaskSpecBytes`), and the non-persistence regression test from Task 3 step 6a should confirm the sentinel doesn't leak into this new field. (c) **`FinalizePlanVerdict`** in `internal/verdict/finalize.go` is wired into `validate_plan` — Task 4's `renderPlanReview` refactor must not break that wiring; check `internal/mcpsrv/handlers.go` around the existing `FinalizePlanVerdict` call site before refactoring. (d) **`suppressUnverifiableCodebaseClaim`** helper exists in `internal/mcpsrv/` — Task 3's prompt-template additions should not duplicate its work; pre.tmpl is now 101 lines (was 66) so insertion-point line numbers in Task 3 step 3 have shifted again — locate by section heading, not line. (e) The post-merge file `internal/verdict/finalize.go` ships `FinalizePlanVerdict` — Task 1's category-enum updates must also be wired into any finalize-side checks that walk findings. (f) **Severity ladder shifts in v0.5.2.** `truncatedEnvelope` per-task finding is now `SeverityMajor` (was `Minor`). `tooLargeEnvelope` / `tooLargePlanResult` / `malformed_evidence` synthetic findings are now `SeverityCritical` (were `Major`). The plan's earlier "use `SeverityMajor` for `primeTooLargeResult` to match existing helpers" and "use `SeverityMinor` for prime/extract truncation envelope" guidance MUST be revised: prime/extract too-large = `SeverityCritical`; prime/extract truncation = `SeverityMajor`. Both pair with explicit `Verdict: fail` (too-large) or `Verdict: warn` (truncation) via `FinalizeVerdict` semantics. (g) **FinalizeVerdict/FinalizePlanVerdict** server-side verdict derivation — Task 7/10 handler skeletons should not set `result.Verdict` explicitly; let `FinalizeVerdict` derive it from the severity ladder (mirroring the v0.5.2 per-task path). **Action for every implementer subagent:** before validate_task_spec, run `git log --oneline -20 origin/main` to confirm no further drift, and `grep -n CategoryAttestationContradiction internal/verdict/verdict.go` to confirm the v0.5.2 surface is present in your worktree.

> **Post-merge reconciliation note (2026-05-19):** The v0.5.0/v0.5.1 line landed on `main` after the first draft of this plan. The following adjustments were applied so the plan reflects current code: (1) the existing finding-category vocabulary now includes `convention_deviation` — every snippet that touches `validCategory`, `internal/verdict/schema.json`, `internal/verdict/plan*_schema.json`, `internal/verdict/tasks_only_schema.json`, `prime_schema.json`, or `extract_schema.json` preserves it; (2) `ValidateTaskSpecArgs` gained four new optional list inputs (`test_strategy_notes`, `codebase_conventions`, `testability_extractions`, `normative_test_bodies`) — Task 3's cumulative-size guard and `taskSpecInputs` struct extension now include them; (3) `pre.tmpl` insertion points moved past the new `NormativeTestBodies` block; (4) `INTEGRATION.md` was trimmed in v0.5.1, so Task 12 anchors on section headings, not line numbers; (5) `prompts_test.go` uses `golden(t, name, got)` — Task 3/4/6/9 use that helper, not the placeholder `assertGolden`; (6) handlers.go / config.go line offsets refreshed.

**Goal:** Ship the v0.6.0 project-knowledge feature from `docs/superpowers/specs/2026-05-18-project-knowledge-design.md`: two new stateless MCP tools (`prime_project_knowledge` / `extract_project_knowledge`), a new `project_knowledge` field on `validate_task_spec` and `validate_plan`, six new finding categories, five new env vars, five note-type templates under `examples/project-knowledge/`, and the matching INTEGRATION / README / team-setup docs.

**Architecture:** Keep anti-tangent stateless, text-only, and advisory. Both new tools are pure request → reviewer → response — no session, no disk I/O, no Basic Memory dependency in Go code. Output adaptation (`bm_commands`) is gated entirely on `ANTI_TANGENT_KB_STORE`. The new `project_knowledge` input field threads through existing prompt templates and counts against the existing 200 KB payload cap; reviewer treats its contents as authoritative (same posture as `pinned_by`).

**Tech Stack:** Go, `testing`, `testify`, embedded `text/template` prompt templates, JSON Schema files in `internal/verdict/`, existing fake-reviewer MCP handler tests.

---

## Cross-cutting technical constraints

These three constraints shape the schema and parser snippets in Tasks 5, 7, 8, 9, and 10. Each task references this section by name rather than re-explaining the rule.

**(A) OpenAI strict-mode schema rules.** `internal/providers/openai.go` sets `response_format.json_schema.strict: true`. OpenAI rejects with HTTP 400 any schema that (i) omits a property from the enclosing `required` array, (ii) declares a freeform `{"type": "object"}` without enumerated `properties`, or (iii) lacks `additionalProperties: false` on any object node. The v0.5.1 invariant test (`internal/verdict/schema_invariants_test.go`) guards (i); Task 1 extends it with two more invariants for (ii) and (iii). Practical consequences elsewhere in this plan:

- Reviewer-emitted variable-shape object payloads must be sent as JSON-encoded strings, not nested objects. `BMCommand.ArgsJSON` (Tasks 5 + 8) and `Proposal.FrontmatterJSON` (Task 8) are flat strings; consumers parse them via `json.Unmarshal` after receipt.
- Optional placeholders are emitted as `"{}"` / `""` / `[]` rather than omitted. The reviewer must always emit `bm_commands` (possibly `[]`), and every per-proposal field whose JSON name appears in `required` must be present even when empty.
- `*string` wire pointers (Task 8's `proposalWire`) are used wherever the parser needs to distinguish "field missing on the wire" from "field present-but-empty"; the schema-required-but-can-be-empty fields cannot be enforced any other way.

**(B) Server-owned vs reviewer-emitted fields.** `PrimeResult.SummaryBlock`, `PrimeResult.Partial`, `ExtractResult.SummaryBlock`, `ExtractResult.Partial` are populated by the handler, never by the reviewer. Tasks 5 and 8 use private `primeWire` / `extractWire` decode structs that omit these fields so a non-OpenAI provider cannot spoof them via `DisallowUnknownFields` (which would otherwise accept them because the names exist on the public struct).

**(C) Shared `validateFindingStrings` parser helper (added unconditionally in Task 1).** The existing per-task / plan parsers don't reject empty `criterion` / `evidence` / `suggestion` strings; today the schemas' `minLength: 1` is the only enforcement. Task 1 adds a `validateFindingStrings(f Finding, where string) error` helper to `internal/verdict/parser.go` and wires it into `Parse`, `validateFinding` (used by ParsePlan + ParseTasksOnly), `ParsePrime` (Task 5), and `ParseExtract` (Task 8). The helper exists regardless of whether Task 1's keyword-decision keeps or strips `minLength` from the schemas — it is the durable parser-side guard.

---

## File Structure

- **Precondition (per spec §5.8):** 0.6.0 cuts from `main` *after* 0.5.0 has merged. As of 2026-05-19 this is satisfied: `main` carries both the v0.5.0 and v0.5.1 release commits, and the v0.5.1 tag is published. 0.6.0 cuts from `main` at HEAD; treat v0.5.1 as the predecessor when wording the changelog stub.
- Add a matching `## [0.6.0] - 2026-05-19` entry to `CHANGELOG.md` before code commits land (Task 0). CI enforces branch ↔ changelog alignment; doing this first avoids late-task push failures. Task 0 also files the tracking issue per spec front-matter.
- Task 0a verifies the Basic Memory contract (transport + tool names) once, up front, and records the canonical names in this plan so Tasks 6, 7, 9, 10, 12, and 14 reference verified values instead of inferred ones.
- **All tasks run sequentially.** The prime cluster (Tasks 5–7) and the extract cluster (Tasks 8–10) both modify `internal/mcpsrv/server.go`, `internal/mcpsrv/summary.go`, `internal/mcpsrv/plan_budget.go`, and `internal/mcpsrv/integration_test.go`. Parallel dispatch across the two clusters would race on those files — do not try it. Tasks 11–15 (docs) run after Task 10 so they describe implemented behavior; they each touch a different top-level markdown file but the implementation references (BM tool names, env vars, dispatch clause shape) span all of them, so run them sequentially too.
- Modify `internal/verdict/verdict.go`, `internal/verdict/parser.go`, and `internal/verdict/schema.json` to extend the finding-category vocabulary.
- Create `internal/verdict/prime.go`, `internal/verdict/prime_schema.json`, `internal/verdict/prime_parser.go`, and `internal/verdict/prime_parser_test.go` for prime output types.
- Create `internal/verdict/extract.go`, `internal/verdict/extract_schema.json`, `internal/verdict/extract_parser.go`, and `internal/verdict/extract_parser_test.go` for extract output types.
- Modify `internal/config/config.go` and `internal/config/config_test.go` for the five new env vars.
- Do **not** modify `internal/session/session.go`. Per spec §3.3 the new `project_knowledge` field is "not stored in the session beyond the current call." The handler threads it through the pre-render path only via a new `PreInput.ProjectKnowledge` field; it is never written to `session.Spec`, so subsequent `check_progress` / `validate_completion` calls do not see it.
- Modify `internal/mcpsrv/handlers.go` for the new `project_knowledge` field on `ValidateTaskSpecArgs` and `ValidatePlanArgs` plus two new tool handlers.
- Modify `internal/mcpsrv/task_spec_input.go` (or create a sibling) for `project_knowledge` size guard.
- Create `internal/mcpsrv/prime_handler.go` and `internal/mcpsrv/extract_handler.go` so the giant `handlers.go` does not grow past comfort. (The existing file is ~1500 lines.)
- Modify `internal/mcpsrv/server.go` to register the two new tools.
- Modify `internal/prompts/prompts.go`, `pre.tmpl`, `plan.tmpl`, `plan_tasks_chunk.tmpl` for the new field; create `internal/prompts/templates/prime.tmpl` and `extract.tmpl`.
- Update goldens in `internal/prompts/testdata/` via `go test ./internal/prompts/... -update` after intentional changes; review the diff before commit.
- Extend tests in `internal/mcpsrv/integration_test.go` for both new tools end-to-end.
- Create `examples/project-knowledge/{decision,module,feature,glossary,epic,README}.md` per the spec §2 schemas.
- Create `docs/team-setup/basic-memory-shared-vm.md` per the spec outline. Section 5 (BM remote-MCP transport) cites the verified contract from Task 0a — it does NOT resolve the transport open question during Task 14 itself (that was the original intent but Task 0a now owns up-front BM verification).
- Update `INTEGRATION.md` with the "Project knowledge (optional)" section and the ~5-line dispatch-clause addition.
- Update `README.md` with a one-paragraph mention + link.
- Finalize `CHANGELOG.md` 0.6.0 entry as the last task.

---

### Task 0: Prepare 0.6.0 branch and changelog stub

**Goal:** The implementation branch and changelog satisfy repository release policy before any code commit lands.

**Acceptance criteria:**
- Current branch is `version/0.6.0` before Task 1 starts.
- `CHANGELOG.md` contains `## [0.6.0] - 2026-05-19` before any code commit is pushed.
- The changelog stub uses only Keep-a-Changelog canonical sections (`### Added`, `### Changed`, `### Fixed`, `### Removed`, `### Deprecated`, `### Security`) per repo convention — existing entries in `CHANGELOG.md` never use a `### Documentation` section, so neither should this one. Doc deliverables (new INTEGRATION.md section, README addition, team-setup doc) live under `### Added` alongside the tools and env vars. Task 15 may add `### Changed` entries if existing surfaces are reworded (e.g. the "four tools" → "six tools" updates from Task 12/13).
- Precondition holds: `main` already contains v0.5.0 and v0.5.1. Verify with `git log --oneline origin/main | head -10` showing both `chore: release v0.5.1 [skip ci]` and the prior `chore: release v0.5.0 [skip ci]` commits.
- A tracking issue is filed on `patiently/anti-tangent-mcp` titled "Implement v0.6.0 project-knowledge feature" and linked from the spec frontmatter (replacing the `Tracking issue: _to be filed before implementation_` placeholder).

**Non-goals:**
- Do not implement runtime behavior in this task.
- Do not update the `VERSION` file; release automation owns version bumps.
- Do not cherry-pick from `version/0.5.0` or `version/0.5.1`. Per spec §5.8 the 0.6.0 branch cuts from `main` after the 0.5.x line ships, not from the 0.5.x branches.

**Context:**
CI enforces that a `version/X.Y.Z` branch has a matching `## [X.Y.Z] - YYYY-MM-DD` entry in `CHANGELOG.md` (see project `CLAUDE.md`). Creating the stub up front prevents downstream task commits from failing branch-policy checks. The spec mandates 0.6.0 cuts after 0.5.0; that precondition is satisfied — v0.5.0 and v0.5.1 are both on `main`.

**Files:**
- Modify: `CHANGELOG.md`
- Modify: `docs/superpowers/specs/2026-05-18-project-knowledge-design.md` (replace the `Tracking issue:` placeholder with the URL from Step 3)

- [ ] **Step 1: Create the version branch from `main`**

Run: `git switch main && git pull --ff-only && git switch -c version/0.6.0`

Expected: branch changes to `version/0.6.0`. If the branch already exists locally, run `git switch version/0.6.0` instead.

- [ ] **Step 2: Add the changelog stub**

Insert the following block immediately below the `# Changelog` intro lines and above the current top release entry in `CHANGELOG.md` (which is `## [0.5.1]` at the time this task runs — see the precondition; if the top entry is something else, the insertion still goes above whatever it is):

```markdown
## [0.6.0] - 2026-05-19

### Added
- New stateless `prime_project_knowledge` MCP tool: given a task spec and a Basic-Memory-style `kb_index`, returns prioritized note picks the controller should attach to the implementer's brief. Optional `bm_commands` paste-ready calls when `ANTI_TANGENT_KB_STORE=basic-memory`.
- New stateless `extract_project_knowledge` MCP tool: given one or more `validate_completion` envelopes, returns structured create/update/supersede proposals for the project KB. Optional `bm_commands` paste-ready calls under the same env gate.
- `validate_task_spec` and `validate_plan` accept an optional `project_knowledge` string. The reviewer treats its contents as authoritative caller-supplied context (same posture as `pinned_by`).
- Six new finding categories: `kb_gap`, `ambiguous_pick`, `missing_index_entry` (prime); `insufficient_evidence`, `redundant_proposal`, `contradicts_existing` (extract).
- Five new env vars: `ANTI_TANGENT_KB_STORE`, `ANTI_TANGENT_PRIME_MODEL`, `ANTI_TANGENT_EXTRACT_MODEL`, `ANTI_TANGENT_PRIME_MAX_TOKENS` (default 4096), `ANTI_TANGENT_EXTRACT_MAX_TOKENS` (default 8192).
- Five note-type templates under `examples/project-knowledge/`: `decision`, `module`, `feature`, `glossary`, `epic`, plus a `README.md`.
- New operator-facing doc `docs/team-setup/basic-memory-shared-vm.md` for teams running a shared Basic Memory on a VM.
- New `INTEGRATION.md` section "Project knowledge (optional)" plus a ~5-line addition to the dispatch clause covering the auto-attached project-knowledge block.
- `README.md` gains one paragraph + link describing the optional KB integration.

### Changed
- INTEGRATION.md and README.md "four tools" claims (introductory paragraph; smoke-test instructions; per-call args section; the `## The 4 tools` heading) updated to "six tools" to reflect the new pair.
```

- [ ] **Step 3: File the tracking issue**

```bash
gh issue create --repo patiently/anti-tangent-mcp \
  --title "Implement v0.6.0 project-knowledge feature" \
  --body "$(cat <<'EOF'
Tracking issue for v0.6.0 — see authoritative design at docs/superpowers/specs/2026-05-18-project-knowledge-design.md and the implementation plan at docs/superpowers/plans/2026-05-19-project-knowledge-v0.6.0.md.

Surface, in brief: two new MCP tools (prime_project_knowledge, extract_project_knowledge), a new project_knowledge field on validate_task_spec and validate_plan, six new finding categories, five env vars, five note templates, and the matching INTEGRATION / README / team-setup docs.
EOF
)"
```

Capture the returned issue URL. Then update the spec frontmatter line `**Tracking issue:** _to be filed before implementation_` in `docs/superpowers/specs/2026-05-18-project-knowledge-design.md` to point at the new issue.

- [ ] **Step 4: Verify branch-policy precondition**

Run: `git branch --show-current`

Expected: `version/0.6.0`

Run: `rg '^## \[0\.6\.0\] - 2026-05-19' CHANGELOG.md`

Expected: one matching changelog heading.

- [ ] **Step 5: Commit task 0**

```bash
git add CHANGELOG.md docs/superpowers/specs/2026-05-18-project-knowledge-design.md
git commit -m "$(cat <<'EOF'
docs: add 0.6.0 changelog stub and link tracking issue

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 0a: Verify Basic Memory contract and pin tool names

**Goal:** Confirm Basic Memory's current remote-MCP transport story and the exact tool names this plan should hard-code, BEFORE any prompt template or handler that mentions BM lands. The verified names get recorded in this plan so downstream tasks reference truth, not inference.

**Heads-up (load-bearing):** prior reviews flagged that current BM releases expose `write_note`, `read_note`, `edit_note`, `move_note`, `delete_note`, `search_notes` — and **no `supersede_note`**. The spec's `supersede_note` is an inference, not a verified upstream verb. Resolve before any prompt or handler ships.

**Acceptance criteria:**
- The spec's two outstanding open questions on BM ("current remote-MCP transport story" and "exact tool names for read/write/supersede") are resolved with citations from the current upstream BM release.
- A new "Basic Memory contract (verified <YYYY-MM-DD>)" block is appended to the bottom of this plan file with: (a) the upstream BM version checked, (b) the canonical tool names for search / read / write / edit / move / delete (whatever subset BM exposes), (c) the **supersede mapping** — how this plan's logical "supersede" action maps to actual BM tool calls (likely a two-step: `write_note` for the superseding note plus `edit_note` to flip the predecessor's `status: superseded` frontmatter), and (d) the canonical transport recommendation for shared-VM deployments. Downstream tasks reference this block by name.
- The **authoritative design spec** at `docs/superpowers/specs/2026-05-18-project-knowledge-design.md` is updated to reflect the verified contract: the spec currently references `supersede_note` at lines 244, 304, and 352, and lists BM tool names + transport as open questions at lines 466-469. After this task, the spec's `supersede_note` mentions become the two-step `write_note + edit_note` mapping (or whatever the verified contract prescribes), and the open-questions list collapses to remove the two now-resolved items. The spec stays the source of truth; if any future task discovers the contract is wrong, the spec is updated FIRST, then this plan's verified block, then downstream tasks.
- The verified block explicitly names the supersede mapping in a `### Supersede mapping` sub-block so Tasks 9/10/12/14 cannot accidentally re-introduce a `supersede_note` invocation. Example shape: `For action: supersede, emit two bm_commands entries: (1) write_note for the new note with status: accepted and supersedes: [<predecessor permalink>]; (2) edit_note for the predecessor to flip status from accepted to superseded.`
- If any of the spec's assumed names (`search_notes`, `read_note`, `write_note`, `edit_note`, `supersede_note`) differ from verified reality, the verified names replace the inferred names in: Task 6 (prime.tmpl — reviewer instructions), Task 7 (handler / `kb_store_mismatch` heuristic if relevant), Task 9 (extract.tmpl — reviewer is instructed to emit 1-or-2 `bm_commands` entries per `Proposal` according to the verified mapping), Task 10 (extract handler — validates and post-processes the reviewer-emitted commands; it does NOT synthesize them), Task 12 (INTEGRATION.md anchor), Task 14 (team-setup doc section 5). Authoritative source of truth: the reviewer emits `bm_commands` via the prompt templates in Tasks 6 and 9; handlers in Tasks 7 and 10 only validate and post-process. In particular, Tasks 9 and 10 stop referencing `supersede_note` and instead apply the verified mapping.
- A short note on the verification method (commands run, URLs fetched) is committed alongside the plan update so future readers can re-verify.

**Non-goals:**
- Do not write code yet. This is research + plan-update only.
- Do not author the full team-setup doc here — only the verified inputs section 5 will consume.

**Context:**
The spec deliberately defers these questions to implementation time, but it does not say "defer until the latest possible moment." Doing the verification up front turns Tasks 9/10/12/14 into mechanical reference-the-verified-block tasks instead of risk-laden mention-then-fix-on-discovery tasks. If BM has renamed a tool between when the spec was drafted (2026-05-18) and now, the earlier we catch it the less rework propagates.

**Files:**
- Modify: `docs/superpowers/plans/2026-05-19-project-knowledge-v0.6.0.md` (append the verified-contract block)
- Modify: `docs/superpowers/specs/2026-05-18-project-knowledge-design.md` (replace `supersede_note` mentions with the verified two-step mapping; remove the two now-resolved open questions)

- [ ] **Step 1: Verify upstream Basic Memory state**

Run: `gh repo view basicmachines-co/basic-memory --json description,homepageUrl,defaultBranchRef,updatedAt`

Run: `gh api /repos/basicmachines-co/basic-memory/releases/latest --jq '{tag_name, name, published_at}'`

Run: `gh api /repos/basicmachines-co/basic-memory/contents/README.md --jq '.content' | base64 -d > /tmp/bm-readme.md && head -200 /tmp/bm-readme.md`

Use `WebFetch` against the upstream README anchors if anything is unclear. Look specifically for:

- The MCP tool list — confirm the exact names for search / read note / write note / edit note / supersede note. The spec assumes `search_notes`, `read_note`, `write_note`, `edit_note`, `supersede_note`.
- The transport story for remote / shared deployments — is there a documented SSE or streamable-HTTP endpoint, or is the canonical pattern stdio-via-SSH-proxy?

- [ ] **Step 2: Append the verified-contract block to this plan**

At the bottom of this plan file, append:

```markdown

---

## Basic Memory contract (verified <YYYY-MM-DD>)

- Upstream version checked: `<tag from gh api>`.
- Verification commands run: `<one-liners from Step 1>`.
- Canonical tool names actually exposed by BM: `<the subset BM provides — likely write_note, read_note, edit_note, search_notes, move_note, delete_note>`.
- Source for the names: `<upstream README anchor / docs URL>`.

### Supersede mapping

BM does **not** ship a single `supersede_note` verb (verified <date>). This plan's logical `Proposal{action: "supersede"}` therefore maps to a **pair** of `bm_commands` entries:

1. `{ "tool": "write_note", "args": { "permalink": "<new>", "frontmatter": {"status": "accepted", "supersedes": ["<predecessor>"], ...}, "body": "<new body>" } }`
2. `{ "tool": "edit_note", "args": { "permalink": "<predecessor>", "frontmatter_patch": {"status": "superseded"} } }`

If the predecessor permalink is missing or the new note carries no body, the reviewer emits an `insufficient_evidence` finding instead of fabricating either command.

### Transport

- Canonical remote transport recommendation for shared-VM deployments: `<SSE | streamable-HTTP | stdio-via-SSH-proxy | other>`. Source: `<upstream README anchor / docs URL>`.

Downstream tasks (6, 7, 9, 10, 12, 14) must reference these names and the supersede mapping verbatim. If a future BM release changes anything, update this block first, then propagate.
```

Replace each `<placeholder>` with the verified value from Step 1. If the verified value differs from this template's example, use the verified one.

- [ ] **Step 2b: Update the authoritative spec**

The design spec at `docs/superpowers/specs/2026-05-18-project-knowledge-design.md` is the source of truth. It has five known drift points that need correcting in this task:

1. **`supersede_note` references** (lines 244, 304, 352). Replace every `supersede_note` mention with the verified mapping. For most callers this means rewriting "`supersede_note`" to "the two-step `write_note + edit_note` mapping" plus a short footnote pointing at the team-setup doc once it exists.
2. **Open questions** (lines 466-469). Remove the two now-resolved bullets (BM transport story, BM tool names). The third bullet about `epic_origin` on module/feature stays — it's not in scope for this task.
3. **Env-var count drift**. Spec §"Scope" line 24 says "One new env var, `ANTI_TANGENT_KB_STORE`…" while spec §5.1 (around line 360) actually lists five new env vars (`ANTI_TANGENT_KB_STORE`, `_PRIME_MODEL`, `_EXTRACT_MODEL`, `_PRIME_MAX_TOKENS`, `_EXTRACT_MAX_TOKENS`). The plan's Task 2 matches §5.1's five-var surface. Update line 24 to "Five new env vars, headed by `ANTI_TANGENT_KB_STORE` which gates output-format adaptation, plus four optional reviewer-model and max-tokens overrides; see §5.1 for the full list." Also update spec line 178 ("Two new stateless tools, one new input field, one new env var.") to "…five new env vars."
4. **Section 8 outline drift** (spec line 458). The spec's team-setup outline says section 8 is "Storage & backup (git-on-cron for the KB directory)" but Task 14 of this plan prescribes a **systemd timer (60s cadence)** as primary, with inotify-recursive and crontab as alternatives. Update spec line 458 to "Storage & backup (git-backed KB directory; systemd timer primary, inotify-recursive and cron fallbacks — see team-setup doc §8 for the full shape)." Without this update, a future reader of the spec would think cron is the recommended approach.
5. **Tracking-issue placeholder** (spec line 5). The spec frontmatter contains `**Tracking issue:** _to be filed before implementation_`. Task 0 Step 3 files the tracking issue and updates this placeholder; if Task 0a runs before Task 0 (it shouldn't — Task 0 → 0a is the documented order), this placeholder is stale, but the plan order keeps it correct.
6. **Dispatch-clause insertion point** (spec lines 310-324). The spec's §4 "Implementer dispatch-clause addition" describes the Project-knowledge block in a position implying it lands *after* the Task spec field list, while Task 12 of this plan inserts it *before* the `## Task spec` heading so the implementer reads the project-knowledge brief BEFORE deciding what to pass into the task_spec call. **Resolution: A** — edit spec §4 so the example placement matches the plan (before-Task-spec position). Rationale: the brief informs the call, not the other way around; keeping spec, plan, and INTEGRATION.md in lock-step minimizes future drift. Concrete edit: in spec §4 "Implementer dispatch-clause addition", move the "Project knowledge (auto-attached by the controller)" example block so it appears immediately before the "Task spec (pass these fields verbatim…)" subsection, mirroring the order Task 12 will ship in INTEGRATION.md.
7. Add a one-line "Updated <YYYY-MM-DD>: BM contract verified, env-var count + section-8 storage approach + dispatch-clause placement reconciled — see plan task 0a verified block and `INTEGRATION.md` for canonical tool names" to the spec's status line at the top.

This keeps the spec authoritative: implementers reading it later see the verified mapping inline, not a `supersede_note` reference that downstream tasks then have to override.

Run: `rg -n 'supersede_note' docs/superpowers/specs/2026-05-18-project-knowledge-design.md`

Expected: zero matches after the edit (or matches only inside a "previously inferred as" historical aside if you keep one for traceability).

- [ ] **Step 3: Propagate corrections (only if needed)**

If any of the spec's assumed names differ from verified reality, before moving on to Task 1 search-and-replace the wrong names throughout this plan. Run:

```bash
rg -n 'search_notes|read_note|write_note|edit_note|supersede_note|move_note|delete_note' docs/superpowers/plans/2026-05-19-project-knowledge-v0.6.0.md
```

For any mismatch, edit the relevant task in this plan so its prompt-template / handler / doc references match the verified names. **In particular**, every remaining mention of `supersede_note` in Tasks 9, 10, 12 must be rewritten to use the two-step `write_note + edit_note` mapping from the verified block above. Do NOT yet touch source code — this is plan-only at Task 0a.

- [ ] **Step 4: Commit task 0a**

```bash
git add docs/superpowers/plans/2026-05-19-project-knowledge-v0.6.0.md docs/superpowers/specs/2026-05-18-project-knowledge-design.md
git commit -m "$(cat <<'EOF'
docs(spec,plan): pin Basic Memory contract for v0.6.0

Resolves the spec's two open questions on BM transport story and
exact tool names. The authoritative design spec is updated to use
the verified two-step write_note + edit_note supersede mapping
(BM does not expose supersede_note), and the plan appends a
verified-contract block downstream tasks reference. The spec
remains source of truth; the plan block exists so downstream tasks
have a single non-spec anchor for canonical tool names.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 1: Add the six new finding categories

**Goal:** `internal/verdict/` accepts the six new reviewer-emitted categories (`kb_gap`, `ambiguous_pick`, `missing_index_entry`, `insufficient_evidence`, `redundant_proposal`, `contradicts_existing`) without breaking any existing tests.

**Acceptance criteria:**
- `internal/verdict/verdict.go` exports six new `Category` constants matching the names above.
- All four reviewer-facing JSON schemas list the six new values (alongside the existing entries, including `convention_deviation`): `internal/verdict/schema.json` (per-task), `internal/verdict/plan_schema.json`, `internal/verdict/plan_findings_only_schema.json`, and `internal/verdict/tasks_only_schema.json`. The category vocabularies must stay in lock-step so a reviewer emitting one of the new categories on the plan surface (which can legitimately surface scope/quality findings) is not rejected.
- Two new strict-mode invariants in `internal/verdict/schema_invariants_test.go`, both walking every object-typed node (anywhere reachable via `properties`, `items`, or `definitions`) in each reviewer-facing schema:
  - `TestReviewerSchemas_NoFreeformObject_ForOpenAIStrictMode`: fails any node that has `"type": "object"` AND no `properties` map (or an empty one). OpenAI strict structured-outputs reject bare `{"type": "object"}` because there's no enumerated property set to apply `additionalProperties: false` against.
  - `TestReviewerSchemas_AdditionalPropertiesFalse_ForOpenAIStrictMode`: fails any object-typed node that lacks `"additionalProperties": false`. Without it OpenAI returns HTTP 400 at request time.

  Both invariants join the existing required-vs-properties invariant (from v0.5.1) and the new `TestReviewerSchemas_CategoryEnumsAreInLockstep` (added below) as the four strict-mode guards. All four are data-driven over a `schemas` slice that Tasks 5 and 8 extend (PrimeSchema, ExtractSchema).
- **Parser-side non-empty enforcement is added UNCONDITIONALLY in this task.** The existing per-task and plan parsers (`Parse` at `internal/verdict/parser.go:32-45`, `ParsePlan` at `internal/verdict/plan_parser.go:65-78`, `ParseTasksOnly` at `internal/verdict/tasks_only_parser.go:38-40`) currently only validate severity / category / (task_title for tasks_only); they do NOT reject empty `criterion` / `evidence` / `suggestion`. Today the schemas' `minLength: 1` is the only enforcement. This task adds a shared `validateFindingStrings(f Finding) error` helper to `internal/verdict/parser.go` that returns an error when any of `criterion` / `evidence` / `suggestion` is empty, and calls it from Parse, ParsePlan, ParseTasksOnly (immediately after the existing `applySeverityFloor` invocation in each). The Tasks 5/8 ParsePrime/ParseExtract have their own inline checks today; they should also be refactored to call the shared helper to keep the rule in one place. This guard exists regardless of any decision about schema keywords — it is defense-in-depth, not a fallback.
- **Schema keyword decision (separate from the parser tightening above).** Current schemas use `minLength` on required strings and `minimum` on integers. OpenAI's structured-outputs strict mode supports a limited subset of JSON-Schema keywords. Task 1's implementer must confirm support by either (a) running an end-to-end smoke against a real OpenAI key with the existing schemas to confirm the request lands without HTTP 400, or (b) reading the current OpenAI structured-outputs docs and listing the supported subset in a comment at the top of `schema_invariants_test.go`. If `minLength` / `minimum` are NOT supported, strip them — the unconditional parser tightening above keeps the constraint enforced. If they ARE supported, leave them as belt-and-braces (schema + parser both enforce). Do NOT add a `TestReviewerSchemas_NoUnsupportedKeywords` invariant — keyword support is provider-version-sensitive and a hard-coded allowlist would rot; the comment + the parser checks are the durable guard.
- A new test `TestReviewerSchemas_CategoryEnumsAreInLockstep` in `internal/verdict/schema_invariants_test.go` walks all four reviewer-facing schemas existing at Task 1 time and asserts every `category.enum` array reachable in each schema matches a single canonical set. Implementation note: current schemas (`internal/verdict/plan_schema.json`, `tasks_only_schema.json`, `plan_findings_only_schema.json`) use `"$ref": "#/definitions/finding"` rather than inline finding-property schemas; the canonical category enum therefore lives at `definitions.finding.properties.category.enum` in those files, NOT at `properties.findings.items.properties.category.enum`. The walker should NOT hard-code one path — instead, recursively collect every node whose key chain ends in `properties.category` and whose value has an `enum` array, then assert all collected enums equal the canonical set. This way the test stays correct whether the schema uses `$ref` indirection or inlines the finding shape. `schema.json` (per-task) inlines the finding shape today; the plan-side schemas use `$ref`. Missing an entry in any single schema causes the test to fail naming the file path and the diff. The walker is data-driven (one shared canonical set; the set of schemas grows as Tasks 5 and 8 add `prime_schema.json` and `extract_schema.json`) so adding a category in future is a one-line change. **Tasks 5 and 8 extend the schemas slice** to include `PrimeSchema()` and `ExtractSchema()` once they exist; from Task 8 onward the test covers all SIX reviewer-output schemas in lock-step.
- `internal/verdict/parser.go::validCategory` returns true for the six new categories.
- `go test -race ./internal/verdict/...` passes.
- A new unit test asserts each of the six categories is accepted by `Parse` when emitted in a finding.

**Non-goals:**
- Do not yet wire these categories into prompts or handlers — that happens in later tasks.
- Do not add server-side severity floors for any of the six.
- Do not change `CategoryMalformedEvidence` posture (still server-only, still excluded from the schema).

**Context:**
The existing `Category` set lives in `internal/verdict/verdict.go` (constants run from line ~26 down through `CategoryOther`) and the JSON-schema enum mirrors it in `internal/verdict/schema.json`. `validCategory` in `parser.go:54-61` is the gate that rejects unknown values. After the v0.5.0/v0.5.1 merge, the existing vocabulary already includes `convention_deviation` (alongside the long-standing categories and the still-server-only `CategoryMalformedEvidence`). Preserve `convention_deviation` in every snippet below — do not delete it when adding the six new categories.

**Files:**
- Modify: `internal/verdict/verdict.go`
- Modify: `internal/verdict/schema.json`
- Modify: `internal/verdict/plan_schema.json`
- Modify: `internal/verdict/plan_findings_only_schema.json`
- Modify: `internal/verdict/tasks_only_schema.json`
- Modify: `internal/verdict/parser.go` (also: add `validateFindingStrings` helper; call from `Parse` after `applySeverityFloor`)
- Modify: `internal/verdict/plan_parser.go` (call `validateFindingStrings` from `validateFinding` after `applySeverityFloor`)
- Modify: `internal/verdict/parser_test.go` (also: add empty-criterion/evidence/suggestion rejection tests for `Parse`, `ParsePlan`, `ParseTasksOnly`)
- Modify: `internal/verdict/schema_invariants_test.go` (add TWO new tests: `TestReviewerSchemas_AdditionalPropertiesFalse_ForOpenAIStrictMode` and `TestReviewerSchemas_CategoryEnumsAreInLockstep`, walking all four reviewer-facing schemas)

- [ ] **Step 1: Write the failing test first**

Append to `internal/verdict/parser_test.go` — note the file is `package verdict` (internal test, NOT `package verdict_test`), so all symbols are unqualified:

```go
func TestParse_AcceptsProjectKnowledgeCategories(t *testing.T) {
	categories := []Category{
		CategoryKBGap,
		CategoryAmbiguousPick,
		CategoryMissingIndexEntry,
		CategoryInsufficientEvidence,
		CategoryRedundantProposal,
		CategoryContradictsExisting,
	}
	for _, c := range categories {
		c := c
		t.Run(string(c), func(t *testing.T) {
			raw := []byte(fmt.Sprintf(`{"verdict":"warn","findings":[{"severity":"minor","category":%q,"criterion":"x","evidence":"y","suggestion":"z"}],"next_action":"go"}`, c))
			r, err := Parse(raw)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if got := r.Findings[0].Category; got != c {
				t.Fatalf("category: got %q want %q", got, c)
			}
		})
	}
}
```

Add the `fmt` import to the test file if it isn't present.

- [ ] **Step 2: Run the test, confirm it fails**

Run: `go test ./internal/verdict/... -run TestParse_AcceptsProjectKnowledgeCategories`

Expected: FAIL with `invalid category "kb_gap"` (and similar) from `parser.go::validCategory`.

- [ ] **Step 3: Add the six constants in `verdict.go`**

In `internal/verdict/verdict.go`, inside the existing `const (...)` block declaring `Category` values, append above `CategoryOther`:

```go
// prime
CategoryKBGap             Category = "kb_gap"
CategoryAmbiguousPick     Category = "ambiguous_pick"
CategoryMissingIndexEntry Category = "missing_index_entry"

// extract
CategoryInsufficientEvidence Category = "insufficient_evidence"
CategoryRedundantProposal    Category = "redundant_proposal"
CategoryContradictsExisting  Category = "contradicts_existing"
```

- [ ] **Step 4: Extend `validCategory` in `parser.go`**

Modify the switch in `internal/verdict/parser.go:54-61` to add the six new categories alongside the existing set (which includes `CategoryConventionDeviation` from v0.5.x — do NOT drop it):

```go
func validCategory(c Category) bool {
	switch c {
	case CategoryMissingAC, CategoryScopeDrift, CategoryAmbiguousSpec,
		CategoryUnaddressed, CategoryQuality, CategorySessionMissing,
		CategoryTooLarge, CategoryUnverifiableCodebaseClaim,
		CategoryConventionDeviation,
		CategoryKBGap, CategoryAmbiguousPick, CategoryMissingIndexEntry,
		CategoryInsufficientEvidence, CategoryRedundantProposal, CategoryContradictsExisting,
		CategoryOther:
		return true
	}
	return false
}
```

- [ ] **Step 5: Add the six values to every reviewer-facing schema enum**

Apply the same insertion to all four schema files: `internal/verdict/schema.json`, `internal/verdict/plan_schema.json`, `internal/verdict/plan_findings_only_schema.json`, and `internal/verdict/tasks_only_schema.json`. Each carries an identical `category.enum` array today (verified post-merge); insert the six new strings before `"other"` and preserve the existing `"convention_deviation"` entry from v0.5.x. The final ordering should be:

```json
"enum": [
  "missing_acceptance_criterion",
  "scope_drift",
  "ambiguous_spec",
  "unaddressed_finding",
  "quality",
  "session_not_found",
  "payload_too_large",
  "unverifiable_codebase_claim",
  "convention_deviation",
  "kb_gap",
  "ambiguous_pick",
  "missing_index_entry",
  "insufficient_evidence",
  "redundant_proposal",
  "contradicts_existing",
  "other"
]
```

- [ ] **Step 5a: Add unconditional `validateFindingStrings` parser helper**

Add to `internal/verdict/parser.go` (next to `validCategory`):

```go
// validateFindingStrings rejects findings whose criterion/evidence/suggestion
// are empty strings. The reviewer-output JSON schemas enforce this via
// `minLength: 1` today, but that keyword may not survive every provider's
// strict-mode subset; this helper is the parser-side belt-and-braces guard
// and the durable enforcement point regardless of schema-level keyword
// support. Called from Parse, ParsePlan, ParseTasksOnly, ParsePrime,
// ParseExtract (and validateFinding in plan_parser.go).
func validateFindingStrings(f Finding, where string) error {
	if f.Criterion == "" {
		return fmt.Errorf("%s: criterion is required", where)
	}
	if f.Evidence == "" {
		return fmt.Errorf("%s: evidence is required", where)
	}
	if f.Suggestion == "" {
		return fmt.Errorf("%s: suggestion is required", where)
	}
	return nil
}
```

In `Parse` (`internal/verdict/parser.go`, after `applySeverityFloor`), add a call:

```go
if err := validateFindingStrings(r.Findings[i], fmt.Sprintf("finding[%d]", i)); err != nil {
    return Result{}, err
}
```

In `validateFinding` (`internal/verdict/plan_parser.go`), after the `applySeverityFloor` line, add:

```go
if err := validateFindingStrings(*f, where); err != nil {
    return err
}
```

`ParseTasksOnly` already routes through `validateFinding`, so the call site there is covered transitively.

**Forward dependency:** Tasks 5 and 8 (defining `ParsePrime` and `ParseExtract`) consume this helper. Their Step 3 parser snippets call `validateFindingStrings(f, fmt.Sprintf("finding[%d]", i))` directly instead of duplicating the three inline `f.Criterion == ""` / etc. checks. That call MUST resolve to the helper added in this step — keep the helper exported within the package (lowercase `validateFindingStrings` is fine since both new parsers live in `package verdict`).

Add parser-side test cases that pass a finding with an empty `criterion` / `evidence` / `suggestion` through each of `Parse`, `ParsePlan`, `ParseTasksOnly` and assert the parser rejects with the appropriate "{field}: ... is required" error. These tests are required even if Step 5 leaves `minLength: 1` in the schemas — they exercise the parser path independently.

- [ ] **Step 6: Run all verdict tests**

Run: `go test -race ./internal/verdict/...`

Expected: PASS.

- [ ] **Step 7: Run full test suite**

Run: `go test -race ./...`

Expected: PASS (no other package depends on the closed set of categories).

- [ ] **Step 8: Commit task 1**

```bash
git add internal/verdict/
git commit -m "$(cat <<'EOF'
feat(verdict): add project-knowledge finding categories

Six new reviewer-emitted categories: kb_gap, ambiguous_pick,
missing_index_entry (for prime); insufficient_evidence,
redundant_proposal, contradicts_existing (for extract).
Wired into Category constants, JSON schema enum, and validCategory.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: Add the five new env vars to `internal/config`

**Goal:** `Config.Load` recognizes `ANTI_TANGENT_KB_STORE`, `ANTI_TANGENT_PRIME_MODEL`, `ANTI_TANGENT_EXTRACT_MODEL`, `ANTI_TANGENT_PRIME_MAX_TOKENS`, `ANTI_TANGENT_EXTRACT_MAX_TOKENS`, with the documented defaults and fallback chain.

**Acceptance criteria:**
- New `Config` fields: `KBStore string`, `PrimeModel ModelRef`, `ExtractModel ModelRef`, `PrimeMaxTokens int`, `ExtractMaxTokens int`.
- `KBStore` default is `""` (empty); only `""` and `"basic-memory"` are accepted; any other value is a startup error naming the env var.
- `PrimeModel` resolves: explicit `ANTI_TANGENT_PRIME_MODEL` → fallback to resolved `PlanModel` → fallback to resolved `PreModel`. Same chain for `ExtractModel`.
- `PrimeMaxTokens` default `4096`, `ExtractMaxTokens` default `8192`; both rejected if `<= 0`.
- `cmd/anti-tangent-mcp/main.go` validates `PrimeModel` and `ExtractModel` via `providers.ValidateModel` at startup, matching the existing pre/mid/post/plan validation. A misspelled `ANTI_TANGENT_PRIME_MODEL` or `ANTI_TANGENT_EXTRACT_MODEL` fails fast at process boot, not at first request.
- `internal/config/config_test.go` covers: defaults, explicit override, fallback chain (with and without `ANTI_TANGENT_PLAN_MODEL` set), invalid `KBStore`, non-positive max-tokens.
- `go test -race ./internal/config/... ./cmd/...` passes.

**Non-goals:**
- Do not change any existing env-var defaults.
- Do not introduce a separate "knowledge-store" abstraction; `KBStore` is a plain string flag at this stage.

**Context:**
`PlanModel` already implements the "explicit override → fallback to PreModel" pattern in `internal/config/config.go:84-93` (post-merge offsets unchanged). Reuse that pattern for `PrimeModel` and `ExtractModel` but chain through `PlanModel` first (per spec §5.1). The `ANTI_TANGENT_PLAN_MAX_TOKENS` block sits at line 135. `cmd/anti-tangent-mcp/main.go` validates pre/mid/post/plan models against the provider allowlist; extending that list is a four-line change and keeps the failure surface consistent. (Confirm the validation block's exact lines with `grep -n 'ValidateModel' cmd/anti-tangent-mcp/main.go` — it shifted slightly across recent merges.)

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Modify: `cmd/anti-tangent-mcp/main.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/config/config_test.go` (top-level function declarations):

```go
func TestLoad_KBStore(t *testing.T) {
	env := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}
	t.Run("default empty", func(t *testing.T) {
		c, err := config.Load(env(map[string]string{"ANTHROPIC_API_KEY": "k"}))
		if err != nil {
			t.Fatal(err)
		}
		if c.KBStore != "" {
			t.Fatalf("KBStore: got %q want empty", c.KBStore)
		}
	})
	t.Run("basic-memory accepted", func(t *testing.T) {
		c, err := config.Load(env(map[string]string{"ANTHROPIC_API_KEY": "k", "ANTI_TANGENT_KB_STORE": "basic-memory"}))
		if err != nil {
			t.Fatal(err)
		}
		if c.KBStore != "basic-memory" {
			t.Fatalf("KBStore: got %q", c.KBStore)
		}
	})
	t.Run("invalid value rejected", func(t *testing.T) {
		_, err := config.Load(env(map[string]string{"ANTHROPIC_API_KEY": "k", "ANTI_TANGENT_KB_STORE": "bogus"}))
		if err == nil || !strings.Contains(err.Error(), "ANTI_TANGENT_KB_STORE") {
			t.Fatalf("expected ANTI_TANGENT_KB_STORE error, got %v", err)
		}
	})
}

func TestLoad_PrimeAndExtractModelFallback(t *testing.T) {
	mk := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}
	t.Run("falls back to PreModel when nothing else set", func(t *testing.T) {
		c, err := config.Load(mk(map[string]string{"ANTHROPIC_API_KEY": "k"}))
		if err != nil {
			t.Fatal(err)
		}
		if c.PrimeModel != c.PreModel || c.ExtractModel != c.PreModel {
			t.Fatalf("prime=%v extract=%v want pre=%v", c.PrimeModel, c.ExtractModel, c.PreModel)
		}
	})
	t.Run("falls back to PlanModel when set", func(t *testing.T) {
		c, err := config.Load(mk(map[string]string{
			"ANTHROPIC_API_KEY":        "k",
			"ANTI_TANGENT_PLAN_MODEL":  "anthropic:claude-opus-4-7",
		}))
		if err != nil {
			t.Fatal(err)
		}
		if c.PrimeModel != c.PlanModel || c.ExtractModel != c.PlanModel {
			t.Fatalf("prime=%v extract=%v want plan=%v", c.PrimeModel, c.ExtractModel, c.PlanModel)
		}
	})
	t.Run("explicit overrides win", func(t *testing.T) {
		c, err := config.Load(mk(map[string]string{
			"ANTHROPIC_API_KEY":             "k",
			"ANTI_TANGENT_PLAN_MODEL":       "anthropic:claude-opus-4-7",
			"ANTI_TANGENT_PRIME_MODEL":      "anthropic:claude-sonnet-4-6",
			"ANTI_TANGENT_EXTRACT_MODEL":    "anthropic:claude-haiku-4-5-20251001",
		}))
		if err != nil {
			t.Fatal(err)
		}
		if c.PrimeModel.Model != "claude-sonnet-4-6" {
			t.Fatalf("PrimeModel: %v", c.PrimeModel)
		}
		if c.ExtractModel.Model != "claude-haiku-4-5-20251001" {
			t.Fatalf("ExtractModel: %v", c.ExtractModel)
		}
	})
}

func TestLoad_PrimeExtractMaxTokens(t *testing.T) {
	mk := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}
	c, err := config.Load(mk(map[string]string{"ANTHROPIC_API_KEY": "k"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.PrimeMaxTokens != 4096 {
		t.Fatalf("PrimeMaxTokens default: got %d", c.PrimeMaxTokens)
	}
	if c.ExtractMaxTokens != 8192 {
		t.Fatalf("ExtractMaxTokens default: got %d", c.ExtractMaxTokens)
	}
	if _, err := config.Load(mk(map[string]string{
		"ANTHROPIC_API_KEY":              "k",
		"ANTI_TANGENT_PRIME_MAX_TOKENS":  "0",
	})); err == nil {
		t.Fatalf("expected error for non-positive PrimeMaxTokens")
	}
	if _, err := config.Load(mk(map[string]string{
		"ANTHROPIC_API_KEY":               "k",
		"ANTI_TANGENT_EXTRACT_MAX_TOKENS": "-1",
	})); err == nil {
		t.Fatalf("expected error for non-positive ExtractMaxTokens")
	}
}
```

Ensure the test file imports `strings` and `github.com/patiently/anti-tangent-mcp/internal/config` (it likely already does).

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/config/... -run 'TestLoad_(KBStore|PrimeAndExtractModelFallback|PrimeExtractMaxTokens)'`

Expected: FAIL with build error on missing `KBStore`, `PrimeModel`, etc.

- [ ] **Step 3: Extend the `Config` struct**

In `internal/config/config.go`, add to the `Config` struct (after `MaxTokensCeiling`):

```go
KBStore          string
PrimeModel       ModelRef
ExtractModel     ModelRef
PrimeMaxTokens   int
ExtractMaxTokens int
```

In `Load`, after the existing defaults block (`SessionTTL`, etc.) set:

```go
cfg.PrimeMaxTokens = 4096
cfg.ExtractMaxTokens = 8192
```

- [ ] **Step 4: Resolve `KBStore`**

After the `LogLevel` block at the bottom of `Load`, insert:

```go
if v := env("ANTI_TANGENT_KB_STORE"); v != "" {
	switch v {
	case "basic-memory":
		cfg.KBStore = v
	default:
		return Config{}, fmt.Errorf("ANTI_TANGENT_KB_STORE: unknown value %q (allowed: \"\", \"basic-memory\")", v)
	}
}
```

- [ ] **Step 5: Resolve `PrimeModel` and `ExtractModel`**

After the `PlanModel` resolution block (`internal/config/config.go:84-93`, post-merge offsets unchanged), insert:

```go
// PrimeModel: optional override; falls back to PlanModel (which itself
// falls back to PreModel).
if v := env("ANTI_TANGENT_PRIME_MODEL"); v != "" {
	mr, err := ParseModelRef(v)
	if err != nil {
		return Config{}, fmt.Errorf("ANTI_TANGENT_PRIME_MODEL: %w", err)
	}
	cfg.PrimeModel = mr
} else {
	cfg.PrimeModel = cfg.PlanModel
}

// ExtractModel: same fallback chain.
if v := env("ANTI_TANGENT_EXTRACT_MODEL"); v != "" {
	mr, err := ParseModelRef(v)
	if err != nil {
		return Config{}, fmt.Errorf("ANTI_TANGENT_EXTRACT_MODEL: %w", err)
	}
	cfg.ExtractModel = mr
} else {
	cfg.ExtractModel = cfg.PlanModel
}
```

- [ ] **Step 6: Resolve max-token env vars**

After the existing `ANTI_TANGENT_PLAN_MAX_TOKENS` block (`internal/config/config.go:135-144`, post-merge offsets unchanged), insert:

```go
if v := env("ANTI_TANGENT_PRIME_MAX_TOKENS"); v != "" {
	n, err := strconv.Atoi(v)
	if err != nil {
		return Config{}, fmt.Errorf("ANTI_TANGENT_PRIME_MAX_TOKENS: %w", err)
	}
	if n <= 0 {
		return Config{}, fmt.Errorf("ANTI_TANGENT_PRIME_MAX_TOKENS: must be positive, got %d", n)
	}
	cfg.PrimeMaxTokens = n
}
if v := env("ANTI_TANGENT_EXTRACT_MAX_TOKENS"); v != "" {
	n, err := strconv.Atoi(v)
	if err != nil {
		return Config{}, fmt.Errorf("ANTI_TANGENT_EXTRACT_MAX_TOKENS: %w", err)
	}
	if n <= 0 {
		return Config{}, fmt.Errorf("ANTI_TANGENT_EXTRACT_MAX_TOKENS: must be positive, got %d", n)
	}
	cfg.ExtractMaxTokens = n
}
```

- [ ] **Step 7: Run the new tests**

Run: `go test -race ./internal/config/...`

Expected: PASS.

- [ ] **Step 7a: Extend main.go startup validation**

In `cmd/anti-tangent-mcp/main.go`, locate the existing validation block that iterates over `cfg.PreModel`, `cfg.MidModel`, `cfg.PostModel`, `cfg.PlanModel` and calls `providers.ValidateModel`. Add `cfg.PrimeModel` and `cfg.ExtractModel` to that loop (typically a slice literal). The error path should already surface a clean message naming the offending model ref; verify by hand.

Run: `go build ./...`

Expected: PASS.

Add a quick smoke test (or extend an existing one) that constructs a `Config` with an invalid `PrimeModel` and asserts startup-validation returns an error mentioning `ANTI_TANGENT_PRIME_MODEL` or the model ref itself. Same for `ExtractModel`.

- [ ] **Step 8: Run full test suite**

Run: `go test -race ./...`

Expected: PASS.

- [ ] **Step 9: Commit task 2**

```bash
git add internal/config/ cmd/anti-tangent-mcp/main.go
git commit -m "$(cat <<'EOF'
feat(config): add project-knowledge env vars

ANTI_TANGENT_KB_STORE (default empty; only "basic-memory" accepted)
ANTI_TANGENT_PRIME_MODEL / ANTI_TANGENT_EXTRACT_MODEL with fallback to
PlanModel → PreModel; both validated via providers.ValidateModel at
startup in cmd/anti-tangent-mcp/main.go.
ANTI_TANGENT_PRIME_MAX_TOKENS (default 4096) and
ANTI_TANGENT_EXTRACT_MAX_TOKENS (default 8192).

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: Add `project_knowledge` to `validate_task_spec`

**Goal:** `validate_task_spec` accepts, normalizes, and renders an optional `project_knowledge` string without changing behavior for callers who omit it. The value lives only on the pre-call render path; it is **not** persisted in the session, so subsequent `check_progress` / `validate_completion` calls do not see it (spec §3.3).

**Acceptance criteria:**
- `ValidateTaskSpecArgs` accepts `project_knowledge string` (optional).
- `prompts.PreInput` gains a `ProjectKnowledge string` sibling field next to `Spec`. `session.TaskSpec` is **not** modified — the value never reaches the session store.
- Whitespace-only input is treated as unset.
- Size guard rejects with a clear error when the **cumulative** task-spec payload — `task_title + goal + acceptance_criteria + non_goals + context + pinned_by + controller_verified_references + test_strategy_notes + codebase_conventions + testability_extractions + normative_test_bodies + project_knowledge` (sum of bytes) — exceeds `MaxPayloadBytes`. The error reports the cumulative total, the cap, and the byte count for each major contributor (`project_knowledge`, `context`, `normative_test_bodies`, `acceptance_criteria`) so the caller can see at a glance which field is largest — without the server having to second-guess which to single out. Per-field shape limits (e.g. `pinned_by` 50 × 500-char entries via `normalizeBoundedStringList`) still apply on top of the cumulative cap.
- `pre.tmpl` renders both the "Project knowledge" section AND the matching reviewer-guidance paragraph **only** when `.ProjectKnowledge` is non-empty. With the field unset, the rendered prompt is byte-identical to today, so the existing `pre_basic.golden` is genuinely unchanged. Reviewer guidance states: treat project_knowledge as authoritative (same posture as `pinned_by`); do not emit `unverifiable_codebase_claim` for claims that appear in it. The reviewer remains free to emit `convention_deviation` if a stated convention in project_knowledge contradicts the task spec.
- New golden file `pre_with_project_knowledge.golden` covers the rendered output when the field is set.
- Existing `pre_basic.golden` is unchanged.
- Two complementary regression tests: (1) a struct-shape check in `internal/session/session_test.go` confirming `session.TaskSpec` has no field named `ProjectKnowledge`; (2) a **handler-level check** in `internal/mcpsrv/handlers_test.go` that actually invokes `ValidateTaskSpec` with a sentinel `project_knowledge` value, retrieves the created session, and walks every string and `[]string` field on `sess.Spec` asserting none contains the sentinel. The handler test catches the "value routed into an existing field by mistake" failure mode that the struct-only test would miss.
- **Three handler-level payload-cap tests** in `internal/mcpsrv/handlers_test.go` exercising the new cumulative size guard:
  - (a) **Over-cap rejection**: construct `ValidateTaskSpecArgs` whose cumulative byte count (across all twelve summed fields) exceeds `MaxPayloadBytes`. Call `ValidateTaskSpec`. Assert the returned error contains the cumulative byte count, the cap, and the per-contributor breakdown (`project_knowledge:`, `context:`, `normative_test_bodies:`, `acceptance_criteria:`). The handler must NOT create a session — assert `env.SessionID == ""`. Use a small `MaxPayloadBytes` (e.g. set `deps.Cfg.MaxPayloadBytes = 200` for the test) so the test fixture stays compact.
  - (b) **Under-cap acceptance**: same inputs but total just under cap; assert success (session created, no error).
  - (c) **Per-contributor evidence**: assert that an args where `project_knowledge` is the dominant contributor reports a larger `project_knowledge:` byte count than the other named contributors in the error string.
- `go test -race ./internal/prompts/... ./internal/mcpsrv/... ./internal/session/...` passes.

**Non-goals:**
- Do not yet expose `project_knowledge` on `validate_plan` (separate task).
- Do not expose it on `check_progress` / `validate_completion` (spec §3.3 deliberately scopes it out for 0.6.0; KB content can change during the 4 h session TTL, and a snapshot would silently drift).
- Do not deduplicate against `pinned_by` or `context` server-side; the reviewer prompt distinguishes them.

**Context:**
After the v0.5.x merge, `ValidateTaskSpecArgs` lives at `internal/mcpsrv/handlers.go:40-56` and carries fifteen fields including the new (post-merge) `TestStrategyNotes`, `CodebaseConventions`, `TestabilityExtractions`, and `NormativeTestBodies`. The pre-prompt template `internal/prompts/templates/pre.tmpl` already has analogous sections for `pinned_by`, `controller_verified_references`, and the four post-merge lists; the new section follows the same shape, but its data comes from the *PreInput* (not `Spec`) so it never round-trips through the session. Normalization for the existing list inputs goes through `normalizeBoundedStringList` in `internal/mcpsrv/task_spec_input.go`, and `normalizeTaskSpecInputs` already returns a `taskSpecInputs` struct with seven fields; this task appends `ProjectKnowledge` to that struct (do not redefine the struct from scratch). The existing 200 KB cap is currently enforced only in `validate_completion`; this task extends it to `validate_task_spec` by summing every user-supplied string field on the args (including `project_knowledge` and the four post-merge lists) and rejecting against the cumulative cap, fail-fast with a self-explaining error that names `project_knowledge` as a likely culprit when it is the largest contributor.

**Files:**
- Modify: `internal/mcpsrv/handlers.go`
- Modify: `internal/mcpsrv/task_spec_input.go`
- Modify: `internal/mcpsrv/handlers_test.go` (add the handler-level non-persistence test + the three payload-cap tests from the AC)
- Modify: `internal/prompts/prompts.go`
- Modify: `internal/prompts/templates/pre.tmpl`
- Modify: `internal/prompts/prompts_test.go`
- Modify: `internal/session/session_test.go` (struct-shape non-persistence check)
- Create: `internal/prompts/testdata/pre_with_project_knowledge.golden`

- [ ] **Step 1: Extend `PreInput` with `ProjectKnowledge`**

In `internal/prompts/prompts.go`, change `PreInput`:

```go
type PreInput struct {
	Spec             session.TaskSpec
	ProjectKnowledge string
}
```

`session.TaskSpec` is **not** modified.

- [ ] **Step 2: Add the input arg and a size guard**

In `internal/mcpsrv/handlers.go`, add to `ValidateTaskSpecArgs` (alongside the existing fifteen fields; append after `NormativeTestBodies` and before `Phase`):

```go
ProjectKnowledge string `json:"project_knowledge,omitempty"`
```

In `internal/mcpsrv/task_spec_input.go`, add a helper:

```go
// normalizeProjectKnowledge trims surrounding whitespace. The cumulative
// payload-cap check happens in normalizeTaskSpecInputs so it can sum
// project_knowledge against every other string field on the args. We
// deliberately do NOT reject here on a per-field cap — a 200 KB
// project_knowledge alone is still under the cap; what matters is total
// args size.
func normalizeProjectKnowledge(raw string) string {
	return strings.TrimSpace(raw)
}
```

And add a cumulative-size helper used by `normalizeTaskSpecInputs`. The four post-merge list fields (`TestStrategyNotes`, `CodebaseConventions`, `TestabilityExtractions`, `NormativeTestBodies`) must all be summed:

```go
// sumLen is a tiny helper used by the error formatter and totalNormalizedTaskSpecBytes.
func sumLen(ss []string) int {
	n := 0
	for _, s := range ss {
		n += len(s)
	}
	return n
}

// totalNormalizedTaskSpecBytes returns the byte sum of every user-supplied
// string field on a task-spec call AFTER per-list normalization (trim +
// drop-empty via normalizeBoundedStringList). Counting raw args.* would
// include whitespace-only entries that the renderer drops, producing
// spurious cap rejections. TaskTitle / Goal / Context / projectKnowledge
// are not list-normalized — their raw lengths flow through verbatim.
// Used to enforce MaxPayloadBytes cumulatively (spec §5.2 / §3.3).
func totalNormalizedTaskSpecBytes(args ValidateTaskSpecArgs, projectKnowledge string, in taskSpecInputs) int {
	total := len(args.TaskTitle) + len(args.Goal) + len(args.Context) + len(projectKnowledge)
	for _, s := range args.AcceptanceCriteria { total += len(s) }
	for _, s := range args.NonGoals             { total += len(s) }
	total += sumLen(in.PinnedBy)
	total += sumLen(in.ControllerVerifiedReferences)
	total += sumLen(in.TestStrategyNotes)
	total += sumLen(in.CodebaseConventions)
	total += sumLen(in.TestabilityExtractions)
	total += sumLen(in.NormativeTestBodies)
	return total
}
```

Extend the existing `taskSpecInputs` struct in `task_spec_input.go:60-68` by appending a `ProjectKnowledge string` field (do NOT redefine the struct — preserve the seven post-merge fields):

```go
type taskSpecInputs struct {
	Phase                        string
	PinnedBy                     []string
	ControllerVerifiedReferences []string
	TestStrategyNotes            []string
	CodebaseConventions          []string
	TestabilityExtractions       []string
	NormativeTestBodies          []string
	ProjectKnowledge             string
}
```

**Breaking signature change to `normalizeTaskSpecInputs`.** Current at `internal/mcpsrv/task_spec_input.go:70`:

```go
func normalizeTaskSpecInputs(args ValidateTaskSpecArgs) (taskSpecInputs, error)
```

Change to:

```go
func normalizeTaskSpecInputs(args ValidateTaskSpecArgs, maxPayload int) (taskSpecInputs, error)
```

Run the post-merge normalization unchanged, then add project-knowledge handling and the cumulative cap check **after** all list normalization completes (so `in` is fully populated and the size accounting sees the post-trim shape). The sole call site is in `ValidateTaskSpec` (handlers.go:86); update it to pass `h.deps.Cfg.MaxPayloadBytes`.

```go
func normalizeTaskSpecInputs(args ValidateTaskSpecArgs, maxPayload int) (taskSpecInputs, error) {
	// ... existing phase / pinnedBy / controllerVerifiedReferences / testStrategyNotes /
	// codebaseConventions / testabilityExtractions / normativeTestBodies normalization
	// stays exactly as today and populates a local `in taskSpecInputs` ...

	projectKnowledge := normalizeProjectKnowledge(args.ProjectKnowledge)
	in.ProjectKnowledge = projectKnowledge
	if total := totalNormalizedTaskSpecBytes(args, projectKnowledge, in); total > maxPayload {
		// The error names the cumulative cap and reports each major
		// contributor's byte count so the caller can see at a glance which
		// field is most likely the cause. We do not single out
		// project_knowledge unless it is in fact the largest contributor.
		// Report normalized contributor lengths where available (lists that
		// went through normalizeBoundedStringList). project_knowledge and
		// context are not list-normalized — their raw len is what counts.
		// acceptance_criteria stays raw because it isn't list-normalized
		// either (no per-entry trim helper applied today).
		return taskSpecInputs{}, fmt.Errorf(
			"task spec payload %d bytes > cap %d (project_knowledge: %d, context: %d, normative_test_bodies: %d, acceptance_criteria: %d)",
			total, maxPayload,
			len(projectKnowledge), len(args.Context),
			sumLen(in.NormativeTestBodies), sumLen(args.AcceptanceCriteria),
		)
	}

	return taskSpecInputs{
		Phase:                        phase,
		PinnedBy:                     pinnedBy,
		ControllerVerifiedReferences: controllerVerifiedReferences,
		TestStrategyNotes:            testStrategyNotes,
		CodebaseConventions:          codebaseConventions,
		TestabilityExtractions:       testabilityExtractions,
		NormativeTestBodies:          normativeTestBodies,
		ProjectKnowledge:             projectKnowledge,
	}, nil
}
```

In `ValidateTaskSpec` (`internal/mcpsrv/handlers.go:81-150`), update the call site at line ~86 to pass the cap:

```go
inputs, err := normalizeTaskSpecInputs(args, h.deps.Cfg.MaxPayloadBytes)
```

The `spec` literal **does not** gain a `ProjectKnowledge` field — `session.TaskSpec` is unchanged. Instead, pass the normalized value into the `resolvePreCallContext` call site at line ~106:

```go
cc, err := h.resolvePreCallContext(
	args.MaxTokensOverride,
	h.deps.Cfg.PerTaskMaxTokens,
	args.ModelOverride,
	h.deps.Cfg.PreModel,
	func() (prompts.Output, error) {
		return prompts.RenderPre(prompts.PreInput{Spec: spec, ProjectKnowledge: inputs.ProjectKnowledge})
	},
	"render pre prompt",
)
```

This is the only place `inputs.ProjectKnowledge` is read. The field is never written to `session.Spec`; `h.deps.Sessions.Create(spec)` later in the handler stores only the existing `TaskSpec` fields, so `check_progress` and `validate_completion` cannot see project_knowledge — exactly the posture spec §3.3 requires.

- [ ] **Step 3: Render the new section in `pre.tmpl`**

Edit `internal/prompts/templates/pre.tmpl`. The pre.tmpl rendering body runs through several `{{if .Spec.<field>}}` blocks in this order today (post-merge): `PinnedBy` (lines ~14-17), `ControllerVerifiedReferences` (~17-19), `TestStrategyNotes` (~20-22), `CodebaseConventions` (~23-25), `TestabilityExtractions` (~26-28), `NormativeTestBodies` (~29-31), followed by the `## What to evaluate` heading at line ~33. Insert the new Project-knowledge block **after the `NormativeTestBodies` block (line ~32) and before `## What to evaluate`** so it follows the existing pattern of "caller-supplied authoritative context blocks":

```gotemplate
{{if .ProjectKnowledge}}
Project knowledge (caller-supplied context from the team's KB):

{{.ProjectKnowledge}}
{{end}}
```

Note: the template reads `{{.ProjectKnowledge}}` (sibling of `.Spec`), not `{{.Spec.ProjectKnowledge}}` — the value lives on `PreInput`, not on `TaskSpec`.

In the same file, append a new **conditional** guidance block. It must be gated on `{{if .ProjectKnowledge}} … {{end}}` so the existing `pre_basic.golden` stays byte-identical when the field is unset. Insert immediately after the existing "If a Normative test bodies section is present…" paragraph (line ~56) and before the `Severity:` line (~58):

```gotemplate
{{if .ProjectKnowledge}}
If a Project knowledge section is present, treat its contents as authoritative caller-supplied context (same posture as pinned_by). Decisions, module invariants, glossary terms, and prior-task summaries it carries are trusted; do not emit `unverifiable_codebase_claim` merely because a claim is grounded in that section. You may still emit `ambiguous_spec`, `quality`, or `convention_deviation` findings if the project_knowledge content contradicts the task spec or leaves an AC vague.
{{end}}
```

After updating both insertions (the section and the guidance), `git diff internal/prompts/testdata/pre_basic.golden` MUST return no output (Step 6 verifies). If it doesn't, you have an unconditional template change to fix — re-check the gating.

- [ ] **Step 4: Add the golden test**

Create `internal/prompts/testdata/pre_with_project_knowledge.golden` by adding a new test case in `internal/prompts/prompts_test.go` that calls `RenderPre` with a `PreInput` whose `ProjectKnowledge` is non-empty (the `Spec` itself reuses `sampleSpec()`), then runs `-update` once to materialize the file.

The existing helper is `golden(t, name, got)` (not `assertGolden`). It takes the **bare basename** (no `testdata/` prefix, no `.golden` suffix) and the golden content is `out.System + "\n---USER---\n" + out.User` — match the existing `TestRenderPre` pattern at `prompts_test.go:44-48`:

```go
func TestRenderPre_WithProjectKnowledge(t *testing.T) {
	in := PreInput{
		Spec:             sampleSpec(),
		ProjectKnowledge: "Decision 0042: cache pass reviews for 3 minutes.\nModule mcpsrv invariant: stdout is reserved for MCP stdio traffic.",
	}
	out, err := RenderPre(in)
	require.NoError(t, err)
	golden(t, "pre_with_project_knowledge", out.System+"\n---USER---\n"+out.User)
}
```

This file is `package prompts` (internal tests) and `golden` lives in the same package, so the call is unqualified.

- [ ] **Step 5: Generate the golden file**

Run: `go test ./internal/prompts/... -update -run TestRenderPre_WithProjectKnowledge`

Expected: a new file `internal/prompts/testdata/pre_with_project_knowledge.golden` is created.

Inspect the diff:

Run: `git diff internal/prompts/testdata/pre_with_project_knowledge.golden`

Expected: the rendered prompt contains a `Project knowledge (caller-supplied context from the team's KB):` section with the two lines from the test input, and the trailing guidance paragraph mentions "Project knowledge section is present."

- [ ] **Step 6a: Assert session non-persistence (struct + handler)**

Two complementary regression tests:

**(1) Struct-shape check** in `internal/session/session_test.go` — guards against accidentally adding the field to TaskSpec:

```go
func TestTaskSpec_DoesNotCarryProjectKnowledge(t *testing.T) {
	// Confirms spec §3.3: project_knowledge is not stored in session.TaskSpec.
	// Runtime reflection check that no TaskSpec field name matches
	// "ProjectKnowledge" (case-insensitive).
	specType := reflect.TypeOf(TaskSpec{})
	for i := 0; i < specType.NumField(); i++ {
		if name := specType.Field(i).Name; strings.EqualFold(name, "ProjectKnowledge") {
			t.Fatalf("session.TaskSpec must not carry %q — project_knowledge is per-call only per spec §3.3", name)
		}
	}
}
```

Add `reflect` and `strings` to the imports if not already present. Place in `package session` (internal).

**(2) Handler-level check** in `internal/mcpsrv/handlers_test.go` — actually invokes `ValidateTaskSpec` with a sentinel `project_knowledge` value and asserts that NO field of the stored session's `Spec` contains the sentinel. The struct-only test from (1) catches "someone added a field literally named ProjectKnowledge" but would miss "someone routed the value into `Context`" or any other existing field; the handler test closes that gap by checking actual data flow:

```go
func TestValidateTaskSpec_ProjectKnowledgeNeverPersistedToSession(t *testing.T) {
	// Sentinel string that no other field in TaskSpec would naturally
	// contain. If it ends up in any stored session.Spec field, the
	// non-persistence invariant from spec §3.3 is broken.
	const sentinel = "PK-SENTINEL-39d8b1f0-pls-do-not-store-this"
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	deps := newDeps(t, rv)
	h := &handlers{deps: deps}

	args := ValidateTaskSpecArgs{
		TaskTitle:          "T",
		Goal:               "G",
		AcceptanceCriteria: []string{"AC"},
		ProjectKnowledge:   sentinel,
	}
	_, env, err := h.ValidateTaskSpec(context.Background(), nil, args)
	require.NoError(t, err)
	require.NotEmpty(t, env.SessionID, "session should be created on a passing review")

	sess, ok := deps.Sessions.Get(env.SessionID)
	require.True(t, ok, "session must be retrievable")

	// Walk every string and []string field on the stored Spec and assert
	// none contains the sentinel.
	specVal := reflect.ValueOf(sess.Spec)
	for i := 0; i < specVal.NumField(); i++ {
		f := specVal.Field(i)
		switch f.Kind() {
		case reflect.String:
			if strings.Contains(f.String(), sentinel) {
				t.Fatalf("session.Spec.%s leaked project_knowledge sentinel", specVal.Type().Field(i).Name)
			}
		case reflect.Slice:
			if f.Type().Elem().Kind() == reflect.String {
				for j := 0; j < f.Len(); j++ {
					if strings.Contains(f.Index(j).String(), sentinel) {
						t.Fatalf("session.Spec.%s[%d] leaked project_knowledge sentinel", specVal.Type().Field(i).Name, j)
					}
				}
			}
		}
	}
}
```

Reuses the existing `fakeReviewer`, `passResp`, and `newDeps` helpers from `handlers_test.go`. Add `reflect` to its imports if not already present.

- [ ] **Step 6: Confirm `pre_basic.golden` did not change**

Run: `git diff internal/prompts/testdata/pre_basic.golden`

Expected: no output (file unchanged, since the basic test's `PreInput.ProjectKnowledge` is empty).

- [ ] **Step 7: Run the package tests**

Run: `go test -race ./internal/prompts/... ./internal/mcpsrv/... ./internal/session/...`

Expected: PASS.

- [ ] **Step 8: Commit task 3**

```bash
git add internal/mcpsrv/ internal/prompts/ internal/session/
git commit -m "$(cat <<'EOF'
feat(validate_task_spec): accept project_knowledge field

Threads ProjectKnowledge through ValidateTaskSpecArgs and the new
PreInput.ProjectKnowledge field; pre.tmpl renders a "Project
knowledge" section when present. Reviewer treats it as authoritative
caller-supplied context (same posture as pinned_by). Value is NOT
written to session.TaskSpec so check_progress / validate_completion
do not see it (spec §3.3). Cumulative task-spec payload cap now
sums all string fields including the four post-merge lists and the
new project_knowledge string, rejecting fast with an actionable
error. Golden coverage and a session-non-persistence regression
test in internal/session/session_test.go added.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 4: Add `project_knowledge` to `validate_plan`

**Goal:** `validate_plan` accepts an optional `project_knowledge` string, threads it into both the single-pass and chunked plan prompts, and continues to behave identically for callers who omit it.

**Acceptance criteria:**
- `ValidatePlanArgs` accepts `project_knowledge string` (optional).
- The string is trimmed; whitespace-only is treated as unset.
- The existing `payload_too_large` synthetic envelope is emitted on the **cumulative** size of `plan_text + project_knowledge`, not on `plan_text` alone. The synthetic finding's evidence names both contributors so the caller can tell which to shrink. The existing path that calls `tooLargePlanResult(size, cap)` is updated to take the cumulative size.
- All three plan templates — `plan.tmpl` (single-pass), `plan_tasks_chunk.tmpl` (chunked pass-2..K+1), and `plan_findings_only.tmpl` (chunked pass-1) — render a `## Project knowledge` section above the plan body only when the field is non-empty. Additionally, each template's existing `## Reviewer ground rules` block gains a `{{if .ProjectKnowledge}}`-guarded amendment that softens the "You have access ONLY to the plan markdown rendered below" sentence and the evidence-must-tie-to-plan-text paragraph so they correctly describe the broader grounding when Project knowledge is present. Reviewer guidance mirrors the addition from Task 3 (authoritative; do not emit `unverifiable_codebase_claim` for claims grounded there; evidence may cite either source). All guards are `{{if .ProjectKnowledge}} … {{end}}` so the rendered prompt is byte-identical to today when the field is unset and existing goldens stay unchanged.
- New golden files cover all three templates when the field is set; existing goldens are unchanged.
- Plan-pass cache key incorporates `ProjectKnowledge` so cache hits stay correct.
- **Three handler-level tests** in `internal/mcpsrv/handlers_plan_test.go` (the existing plan-handler test file): (a) **over-cap rejection** with cumulative `plan_text + project_knowledge` total exceeding `MaxPayloadBytes` — assert the synthetic `payload_too_large` finding's evidence contains both `plan_text:` and `project_knowledge:` byte counts; (b) **under-cap acceptance** dispatches to the reviewer normally; (c) **cache-key separation**: two calls with identical `plan_text` but different non-empty `project_knowledge` values must NOT hit the same cache entry (one should produce `review_ms > 0`, the other should NOT be served as `[cached <=3m]`). The third test guards against the regression where the cache key omits `project_knowledge` and serves stale grounding.
- `go test -race ./internal/prompts/... ./internal/mcpsrv/... ./internal/verdict/...` passes.

**Non-goals:**
- Do not store `project_knowledge` anywhere — `validate_plan` is stateless.
- Do not change the chunking threshold or any other plan-level behavior.

**Context:**
After the v0.5.x merge: `ValidatePlanArgs` lives at `internal/mcpsrv/handlers.go:660-665`, `ValidatePlan` at line 994, the payload guard at line 1008, and `renderPlanReview` at line 1095. The plan-pass cache key is built by `planPassCacheKey` in `internal/mcpsrv/plan_cache.go:38` with signature `planPassCacheKey(planText, mode, model string, maxTokens, maxTokensOverride int, rendered renderedPlanReview)`; the new field has to flow into the rendered prompt (and therefore the cache key derived from it). `PlanInput` is in `internal/prompts/prompts.go:52-55`. The chunked path runs three prompt templates total — pass-1 is `plan_findings_only.tmpl` (cross-cutting findings, no per-task data); pass-2..K+1 are `plan_tasks_chunk.tmpl` (per-task analysis); the single-pass path uses `plan.tmpl`. All three now begin with a `## Reviewer ground rules` preamble and only have their `## Plan under review` heading around line 13 of each file. All three need the new section inserted **above** that heading.

**Files:**
- Modify: `internal/mcpsrv/handlers.go`
- Modify: `internal/prompts/prompts.go`
- Modify: `internal/prompts/templates/plan.tmpl`
- Modify: `internal/prompts/templates/plan_tasks_chunk.tmpl`
- Modify: `internal/prompts/templates/plan_findings_only.tmpl`
- Modify: `internal/prompts/prompts_test.go`
- Create: `internal/prompts/testdata/plan_basic_with_project_knowledge.golden`
- Create: `internal/prompts/testdata/plan_tasks_chunk_with_project_knowledge.golden`
- Create: `internal/prompts/testdata/plan_findings_only_with_project_knowledge.golden`

- [ ] **Step 1: Add the input arg**

In `internal/mcpsrv/handlers.go`, extend `ValidatePlanArgs`:

```go
type ValidatePlanArgs struct {
	PlanText          string `json:"plan_text"      jsonschema:"required"`
	ProjectKnowledge  string `json:"project_knowledge,omitempty"`
	ModelOverride     string `json:"model_override,omitempty"`
	MaxTokensOverride int    `json:"max_tokens_override,omitempty"`
	Mode              string `json:"mode,omitempty"`
}
```

- [ ] **Step 2: Extend the `PlanInput` and `PlanChunkInput` shapes**

In `internal/prompts/prompts.go`:

```go
type PlanInput struct {
	PlanText         string
	ProjectKnowledge string
	Mode             string
}

type PlanChunkInput struct {
	PlanText         string
	ProjectKnowledge string
	ChunkTasks       []planparser.RawTask
	Mode             string
}
```

- [ ] **Step 3: Render the section in all three plan templates**

**Important** — the existing plan templates open with `## Reviewer ground rules` whose first sentence is "You have access ONLY to the plan markdown rendered below." Inserting a Project-knowledge section above `## Plan under review` would put authoritative context above that "ONLY" statement, contradicting it. Two coordinated edits are required:

1. Insert a `{{if .ProjectKnowledge}}` guarded amendment INSIDE the existing `## Reviewer ground rules` block (NOT above it) that softens the "ONLY" line when project_knowledge is supplied. In each of `plan.tmpl`, `plan_tasks_chunk.tmpl`, and `plan_findings_only.tmpl`, locate the existing first paragraph that reads "You have access ONLY to the plan markdown rendered below. …" Immediately after that paragraph, add:

   ```gotemplate
   {{if .ProjectKnowledge}}
   You ALSO have access to a "Project knowledge" section below — caller-supplied context from the team's knowledge base. Treat its contents as authoritative caller context (same posture as `pinned_by`): decisions, module invariants, glossary terms, and prior-task summaries it carries are trusted. Evidence-citing findings may quote text from either the plan markdown OR the Project knowledge section. Do not emit `unverifiable_codebase_claim` for claims grounded in Project knowledge; still emit `ambiguous_spec`, `quality`, or `convention_deviation` if it contradicts the plan or leaves an AC vague.
   {{end}}
   ```

   This conditional softening keeps the rendered prompt byte-identical to today when `ProjectKnowledge` is empty (so existing goldens stay unchanged).

2. Then, immediately above the existing `## Plan under review` heading (around line 13 post-merge), add the section itself:

```gotemplate
{{if .ProjectKnowledge}}
## Project knowledge (caller-supplied context from the team's KB)

{{.ProjectKnowledge}}

{{end}}
```

Apply BOTH the ground-rules softening AND the section insertion to `plan.tmpl`, `plan_tasks_chunk.tmpl`, and `plan_findings_only.tmpl`. The three templates must agree so single-pass and chunked paths produce identical grounding. The "evidence must be tied to plan text" paragraph further down in the ground rules is also affected — when ProjectKnowledge is present, evidence may be tied to either source. A second `{{if .ProjectKnowledge}}` guard immediately after that paragraph noting the broader allowance keeps the constraint coherent:

```gotemplate
{{if .ProjectKnowledge}}
When Project knowledge is supplied, evidence may also be tied to text quoted from that section, in addition to the plan markdown.
{{end}}
```

- [ ] **Step 4: Thread the field through `renderPlanReview` and the handler**

In `internal/mcpsrv/handlers.go::renderPlanReview` (line 1095 post-merge), refactor the signature to take an input struct rather than adding a 5th positional argument. The repo enforces a 4-argument max via CodeScene; see `planReviewErrInputs` at `internal/mcpsrv/review_error.go:79-81` for the established pattern. Define `renderPlanReviewInputs` next to it:

```go
type renderPlanReviewInputs struct {
	PlanText         string
	ProjectKnowledge string
	Tasks            []planparser.RawTask
	ChunkSize        int
	Mode             string
}
```

Then change `renderPlanReview(planText string, tasks []planparser.RawTask, chunkSize int, mode string)` to `renderPlanReview(in renderPlanReviewInputs)`. Update the lone caller (`ValidatePlan` at line 1027) to pass `renderPlanReviewInputs{...}`. Internally the function passes the fields into both `PlanInput` and `PlanChunkInput`:

```go
func renderPlanReview(planText, projectKnowledge string, tasks []planparser.RawTask, chunkSize int, mode string) (renderedPlanReview, error) {
	if len(tasks) <= chunkSize {
		rendered, err := prompts.RenderPlan(prompts.PlanInput{PlanText: planText, ProjectKnowledge: projectKnowledge, Mode: mode})
		// …
	}
	// …
	findingsOnly, err := prompts.RenderPlanFindingsOnly(prompts.PlanInput{PlanText: planText, ProjectKnowledge: projectKnowledge, Mode: mode})
	// …
	for i := 0; i < len(tasks); i += chunkSize {
		// …
		chunkPrompt, err := prompts.RenderPlanTasksChunk(prompts.PlanChunkInput{
			PlanText:         planText,
			ProjectKnowledge: projectKnowledge,
			ChunkTasks:       chunkTasks,
			Mode:             mode,
		})
		// …
	}
}
```

In `ValidatePlan` (the call site at line 994 post-merge), pre-trim the field, then replace the existing single-field payload guard (line 1008) with a cumulative one:

```go
projectKnowledge := strings.TrimSpace(args.ProjectKnowledge)
planBytes := len(args.PlanText)
pkBytes := len(projectKnowledge)
// Replace the existing `if size := len(args.PlanText); size > cap` guard with:
if total := planBytes + pkBytes; total > h.deps.Cfg.MaxPayloadBytes {
	return planEnvelopeResult(prependPlanClamp(tooLargePlanResult(total, planBytes, pkBytes, h.deps.Cfg.MaxPayloadBytes), clamp), h.deps.Cfg.PlanModel.String(), 0)
}
// …
rendered, err := renderPlanReview(renderPlanReviewInputs{
	PlanText:         args.PlanText,
	ProjectKnowledge: projectKnowledge,
	Tasks:            tasks,
	ChunkSize:        h.deps.Cfg.PlanTasksPerChunk,
	Mode:             args.Mode,
})
```

**Breaking signature change to `tooLargePlanResult`.** Current at `internal/mcpsrv/handlers.go:1219`:

```go
func tooLargePlanResult(size, limit int) verdict.PlanResult
```

Change to:

```go
func tooLargePlanResult(total, planBytes, pkBytes, limit int) verdict.PlanResult
```

The `evidence` field inside the body becomes `fmt.Sprintf("payload %d bytes > cap %d (plan_text: %d, project_knowledge: %d)", total, limit, planBytes, pkBytes)`. Every existing call site must be updated; the production call site at `handlers.go:1008` is shown above (passes `total, planBytes, pkBytes`), and any test-helper call sites pass `tooLargePlanResult(planBytes, planBytes, 0, limit)` to preserve their pre-change semantics. Confirm with `rg -n 'tooLargePlanResult\(' internal/` before committing — every match must use the new four-arg shape.

Include `projectKnowledge` in the plan-pass cache key. In `internal/mcpsrv/plan_cache.go::planPassCacheKey` (signature: `planPassCacheKey(planText, mode, model string, maxTokens, maxTokensOverride int, rendered renderedPlanReview) [32]byte` at line 38), add a `projectKnowledge string` parameter and include it in the anonymous JSON struct that gets hashed (alongside `PlanText`, `Mode`, `Model`, etc.). At the call site in `ValidatePlan`, pass `projectKnowledge` as that new argument.

- [ ] **Step 5: Add golden tests**

In `internal/prompts/prompts_test.go` (which is `package prompts`, internal — match the existing test style and use the in-package `golden(t, name, got)` helper at line 32; bare basenames, no `testdata/` prefix; content is `out.System+"\n---USER---\n"+out.User`):

```go
func TestRenderPlan_WithProjectKnowledge(t *testing.T) {
	in := PlanInput{
		PlanText:         "# Plan\n\n### Task 1: First\n\nbody.\n",
		ProjectKnowledge: "Decision 0042: cache pass reviews for 3 minutes.",
	}
	out, err := RenderPlan(in)
	require.NoError(t, err)
	golden(t, "plan_basic_with_project_knowledge", out.System+"\n---USER---\n"+out.User)
}

func TestRenderPlanTasksChunk_WithProjectKnowledge(t *testing.T) {
	in := PlanChunkInput{
		PlanText:         "# Plan\n\n### Task 1: First\n\nbody.\n### Task 2: Second\n\nbody.\n",
		ProjectKnowledge: "Module mcpsrv invariant: stdout reserved for MCP stdio.",
		// planparser.RawTask has fields {Title, Body, HasStructuredHeader} — NO Index.
		// Mirror the existing TestRenderPlanTasksChunk_Golden at prompts_test.go:155 if signatures change.
		ChunkTasks: []planparser.RawTask{
			{Title: "Task 1: First", Body: "body.\n"},
			{Title: "Task 2: Second", Body: "body.\n"},
		},
	}
	out, err := RenderPlanTasksChunk(in)
	require.NoError(t, err)
	golden(t, "plan_tasks_chunk_with_project_knowledge", out.System+"\n---USER---\n"+out.User)
}

func TestRenderPlanFindingsOnly_WithProjectKnowledge(t *testing.T) {
	in := PlanInput{
		PlanText:         "# Plan\n\n### Task 1: First\n\nbody.\n### Task 2: Second\n\nbody.\n",
		ProjectKnowledge: "Decision 0017: text-only reviewer is canonical.",
	}
	out, err := RenderPlanFindingsOnly(in)
	require.NoError(t, err)
	golden(t, "plan_findings_only_with_project_knowledge", out.System+"\n---USER---\n"+out.User)
}
```

(If `planparser.RawTask` field names differ from `Index/Title`, copy from the existing `TestRenderPlanTasksChunk_Golden` test at `prompts_test.go:155`.)

- [ ] **Step 6: Generate goldens and inspect**

Run: `go test ./internal/prompts/... -update -run 'TestRenderPlan_WithProjectKnowledge|TestRenderPlanTasksChunk_WithProjectKnowledge|TestRenderPlanFindingsOnly_WithProjectKnowledge'`

Run: `git diff internal/prompts/testdata/plan_basic.golden internal/prompts/testdata/plan_basic_quick.golden internal/prompts/testdata/plan_tasks_chunk.golden internal/prompts/testdata/plan_tasks_chunk_quick.golden internal/prompts/testdata/plan_findings_only.golden internal/prompts/testdata/plan_findings_only_quick.golden`

Expected: no diff for existing goldens (because the new template branch only renders when `ProjectKnowledge` is set).

Run: `cat internal/prompts/testdata/plan_basic_with_project_knowledge.golden internal/prompts/testdata/plan_findings_only_with_project_knowledge.golden`

Expected: both contain `## Project knowledge (caller-supplied context from the team's KB)` above `## Plan under review`.

- [ ] **Step 7: Run package tests**

Run: `go test -race ./internal/prompts/... ./internal/mcpsrv/... ./internal/verdict/...`

Expected: PASS.

- [ ] **Step 8: Commit task 4**

```bash
git add internal/mcpsrv/ internal/prompts/
git commit -m "$(cat <<'EOF'
feat(validate_plan): accept project_knowledge field

Threads ProjectKnowledge through ValidatePlanArgs, PlanInput,
PlanChunkInput, and all three plan templates (plan.tmpl,
plan_tasks_chunk.tmpl, plan_findings_only.tmpl). Cumulative size
guard on (plan_text + project_knowledge) replaces the previous
single-field guard. tooLargePlanResult's evidence reports both
contributors so the caller can see which to shrink. Cache key
incorporates the field so cache hits stay correct. Golden coverage
added for all three templates; existing goldens unchanged.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 5: Add prime verdict types, schema, and parser

**Goal:** `internal/verdict/` exports `PrimeResult`, `Pick`, and a `ParsePrime` function so the prime handler (Task 7) has a parser ready to consume.

**Acceptance criteria:**
- `internal/verdict/prime.go` declares:
  - `Pick struct { Permalink, Reason string; Priority Severity }` (reusing `Severity` from the existing package since spec says `critical|major|minor`).
  - `BMCommand struct { Tool string; ArgsJSON string }` — `ArgsJSON` is a JSON-encoded object string (NOT a `map[string]any`), because OpenAI strict structured-outputs rejects freeform `object` schemas. Callers parse `ArgsJSON` via `json.Unmarshal` after receipt; see Task 7's handler.
  - `PrimeResult struct { Verdict Verdict; Findings []Finding; Picks []Pick; BMCommands []BMCommand; NextAction string; Partial bool }` with JSON tags matching spec §3.1.
- `internal/verdict/prime_schema.json` enforces shape. The `findings.category` enum is the **full shared vocabulary** from the main schema (every category in `internal/verdict/schema.json` plus the six new ones from Task 1). This keeps the prime/extract/per-task surfaces uniform so a reviewer emitting `quality` or `scope_drift` on the prime surface is not rejected, and the parser's `validCategory` (already broadened in Task 1) does not have a different opinion than the JSON schema.
- **`minLength` / `minimum` usage MUST follow Task 1's keyword decision.** Task 1 added the unconditional `validateFindingStrings` parser helper, so parser enforcement is guaranteed; the schema's `minLength: 1` is now belt-and-braces only. The snippets below show `minLength: 1` on required strings. Keep them if Task 1 confirmed OpenAI strict mode supports `minLength`; otherwise remove them from these prime/extract schemas to match the existing schemas. ParsePrime/ParseExtract should call `validateFindingStrings` (refactored in Task 1 step 5a) rather than duplicating the three inline `f.Criterion == ""` / etc. checks.
- `internal/verdict/prime_schema.json` lists `bm_commands` under `properties` AND in the top-level `required` array. The v0.5.1 invariant (`schema_invariants_test.go`) enforces that every property in every reviewer-output schema is also in `required`, because OpenAI structured-outputs (`response_format` strict:true) rejects schemas with any optional top-level property with HTTP 400. The reviewer is instructed (via the prime prompt in Task 6) to emit `bm_commands: []` when `KBStoreIsBasicMemory: false` and one entry per pick when true. Inside each `BMCommand` item, `args` is shaped as `args_json: string` (a JSON-encoded string), NOT as a freeform `object` — OpenAI strict mode rejects `{"type": "object"}` without an enumerated `properties` set + `additionalProperties: false`, and the BM command surface has variable per-tool keys we cannot enumerate in the schema. Callers parse `args_json` via `json.Unmarshal` after receiving the envelope; see Task 7 step 2 for the handler's parse-and-validate path.
- `ParsePrime(raw []byte) (PrimeResult, error)` enforces enums (`verdict`, `severity`, `category`) and the `Pick.Priority` enum, tolerates ```json fences, and rejects extra/missing required fields. Per the prime schema, `findings`, `picks`, AND `bm_commands` are required arrays (each may be empty `[]` but must be present) — the parser explicitly rejects a decoded `PrimeResult` whose `Findings`, `Picks`, or `BMCommands` is nil (Go's JSON decoder leaves missing fields as nil, so the schema-required posture has to be re-enforced post-decode). For each pick, `Permalink` and `Reason` must be non-empty and `Priority` must be one of the three severities. For each BMCommand, `Tool` must be non-empty and `ArgsJSON` must parse as a JSON **object** literal (rejected if empty, malformed, a non-object JSON value such as an array or scalar, OR the literal string `"null"` — `json.Unmarshal` of `null` into a `map[string]any` pointer succeeds with the map set to nil, so the parser must explicitly check `probe == nil` after the unmarshal and reject with a "use \"{}\" not \"null\"" hint). Severity-floor handling delegates to the canonical `applySeverityFloor` helper in `parser_partial.go` so prime stays in lock-step with the per-task parser — `applySeverityFloor` floors BOTH `unverifiable_codebase_claim` and `convention_deviation` to minor.
- `internal/verdict/prime_parser_test.go` covers: happy path (with `bm_commands: []` present), missing `next_action`, missing `findings` / `picks` / `bm_commands` (each rejected with a clear error naming the field), invalid `verdict`, invalid `Pick.Priority`, invalid `Category`, **empty `criterion` / `evidence` / `suggestion` on a finding (each rejected with the field name)**, fenced JSON, extra fields rejected, the three prime-specific categories accepted, a `bm_commands` round-trip (reviewer emits a `read_note` command and the parser preserves it on `PrimeResult.BMCommands`), an empty-`bm_commands` round-trip (`[]` accepted, `nil` rejected), **and a server-owned-field-spoof rejection** — when raw JSON contains `summary_block` or `partial` (which `PrimeResult` exposes but the wire-only decode struct does NOT), `DisallowUnknownFields` rejects the input with "unknown field" so a non-strict-mode provider cannot inject these.
- `ParsePrime` decodes into a private wire-only struct (`primeWire` in `prime.go`) that omits `SummaryBlock` and `Partial`, then lifts the reviewer-emitted fields into the public `PrimeResult`. This is the load-bearing fix: without the wire struct, `DisallowUnknownFields` would let `summary_block`/`partial` slip through because those names exist on `PrimeResult`.
- `internal/verdict/schema_invariants_test.go::TestReviewerSchemas_RequireAllProperties_ForOpenAIStrictMode` includes `PrimeSchema()` in its schemas slice and the test still passes — confirming `prime_schema.json` cannot regress an optional property in future.
- `go test -race ./internal/verdict/...` passes.

**Non-goals:**
- Do not add `SummaryBlock` generation here — that lives in `internal/mcpsrv/summary.go` and is wired in Task 7.
- Do not add a partial-recovery parser (the existing per-task partial recovery is a `Result` shape; PrimeResult is small enough that we don't need partial recovery in 0.6.0).

**Context:**
The existing `Parse` function in `internal/verdict/parser.go:13-48` is the model to copy. JSON schema lives in `internal/verdict/schema.json`. Embedding follows the existing `//go:embed plan_schema.json` pattern used for the plan schemas in `internal/verdict/plan.go:1-22`.

**Files:**
- Create: `internal/verdict/prime.go`
- Create: `internal/verdict/prime_schema.json`
- Create: `internal/verdict/prime_parser.go`
- Create: `internal/verdict/prime_parser_test.go`
- Modify: `internal/verdict/schema_invariants_test.go` (add `PrimeSchema()` to the schemas slice so the strict-schema invariant guards prime against future optional-property regressions; without this, an inadvertent `bm_commands,omitempty` change would not fail locally and would surface as an OpenAI HTTP 400 in production)

- [ ] **Step 1: Define types and schema embed**

Create `internal/verdict/prime.go`:

```go
package verdict

import _ "embed"

// Pick is one note recommendation produced by prime_project_knowledge.
type Pick struct {
	Permalink string   `json:"permalink"`
	Reason    string   `json:"reason"`
	Priority  Severity `json:"priority"`
}

// BMCommand is a paste-ready Basic Memory client call. Emitted only when
// the server is configured with ANTI_TANGENT_KB_STORE=basic-memory.
// ArgsJSON is the BM tool's args object encoded as a JSON string — OpenAI
// strict structured-outputs rejects freeform `object` schemas, so the wire
// format flattens args to a string and callers parse it on receipt.
type BMCommand struct {
	Tool     string `json:"tool"`
	ArgsJSON string `json:"args_json"`
}

// PrimeResult is the canonical shape returned by prime_project_knowledge.
// BMCommands is required-but-can-be-empty (OpenAI strict-mode schema
// invariant; see internal/verdict/schema_invariants_test.go).
//
// SummaryBlock and Partial are SERVER-OWNED — they are populated by the
// handler, never by the reviewer. To stop a non-OpenAI reviewer (Anthropic,
// Google, etc., which do not enforce strict-mode and so cannot block
// extra fields at the provider boundary) from spoofing these, the parser
// decodes into a separate wire-only struct that excludes them — see
// primeWire below and ParsePrime's implementation in prime_parser.go.
type PrimeResult struct {
	Verdict      Verdict     `json:"verdict"`
	Findings     []Finding   `json:"findings"`
	Picks        []Pick      `json:"picks"`
	BMCommands   []BMCommand `json:"bm_commands"`
	NextAction   string      `json:"next_action"`
	SummaryBlock string      `json:"summary_block,omitempty"`
	Partial      bool        `json:"partial,omitempty"`
}

// primeWire is the reviewer-emitted shape (no server-owned fields). The
// parser decodes raw bytes into this with DisallowUnknownFields, then
// the parser lifts the reviewer fields into a PrimeResult with the
// server-owned fields zero-valued. This closes the spoof-vector where a
// non-strict-mode provider could emit a fake `summary_block` or `partial`
// and have it round-trip through the server.
type primeWire struct {
	Verdict    Verdict     `json:"verdict"`
	Findings   []Finding   `json:"findings"`
	Picks      []Pick      `json:"picks"`
	BMCommands []BMCommand `json:"bm_commands"`
	NextAction string      `json:"next_action"`
}

//go:embed prime_schema.json
var primeSchema []byte

// PrimeSchema returns a defensive byte copy of the prime JSON schema.
func PrimeSchema() []byte {
	out := make([]byte, len(primeSchema))
	copy(out, primeSchema)
	return out
}
```

- [ ] **Step 2: Define the schema**

Create `internal/verdict/prime_schema.json`:

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "title": "PrimeResult",
  "type": "object",
  "required": ["verdict", "findings", "picks", "bm_commands", "next_action"],
  "additionalProperties": false,
  "properties": {
    "verdict": { "type": "string", "enum": ["pass", "warn", "fail"] },
    "findings": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["severity", "category", "criterion", "evidence", "suggestion"],
        "additionalProperties": false,
        "properties": {
          "severity":   { "type": "string", "enum": ["critical", "major", "minor"] },
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
              "unverifiable_codebase_claim",
              "convention_deviation",
              "attestation_contradiction",
              "kb_gap",
              "ambiguous_pick",
              "missing_index_entry",
              "insufficient_evidence",
              "redundant_proposal",
              "contradicts_existing",
              "other"
            ]
          },
          "criterion":  { "type": "string", "minLength": 1 },
          "evidence":   { "type": "string", "minLength": 1 },
          "suggestion": { "type": "string", "minLength": 1 }
        }
      }
    },
    "picks": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["permalink", "reason", "priority"],
        "additionalProperties": false,
        "properties": {
          "permalink": { "type": "string", "minLength": 1 },
          "reason":    { "type": "string", "minLength": 1 },
          "priority":  { "type": "string", "enum": ["critical", "major", "minor"] }
        }
      }
    },
    "bm_commands": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["tool", "args_json"],
        "additionalProperties": false,
        "properties": {
          "tool":      { "type": "string", "minLength": 1 },
          "args_json": { "type": "string" }
        }
      }
    },
    "next_action": { "type": "string", "minLength": 1 }
  }
}
```

The category enum mirrors the main `internal/verdict/schema.json` enum (extended in Task 1 with the six new categories) so reviewers can emit any valid category on this surface — keeping the parser-side `validCategory` and the JSON schema in lock-step.

`bm_commands` is enumerated under `properties` AND included in the top-level `required` array. This is non-negotiable: the v0.5.1 invariant test (`internal/verdict/schema_invariants_test.go`) walks every reviewer-output schema and fails any node whose `required` set does not match `properties`, because OpenAI's strict structured-outputs mode (`internal/providers/openai.go` sets `strict: true`) rejects optional top-level properties with HTTP 400. The reviewer is instructed in `prime.tmpl` (Task 6) to emit `bm_commands: []` when the caller is not using Basic Memory and a non-empty list when it is.

- [ ] **Step 3: Implement the parser**

Create `internal/verdict/prime_parser.go`:

```go
package verdict

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// ParsePrime decodes provider output into a PrimeResult, validating enum
// fields and rejecting extra/missing fields. Tolerates ```json fences.
// Decodes into the wire-only `primeWire` struct so reviewer-emitted
// `summary_block` / `partial` fields are rejected as unknown — server-owned
// fields land on PrimeResult only via handler-side population.
func ParsePrime(raw []byte) (PrimeResult, error) {
	body := stripFences(bytes.TrimSpace(raw))
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	var w primeWire
	if err := dec.Decode(&w); err != nil {
		return PrimeResult{}, fmt.Errorf("decode prime result: %w", err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return PrimeResult{}, fmt.Errorf("decode prime result: extra JSON after document")
	}
	r := PrimeResult{
		Verdict:    w.Verdict,
		Findings:   w.Findings,
		Picks:      w.Picks,
		BMCommands: w.BMCommands,
		NextAction: w.NextAction,
		// SummaryBlock and Partial deliberately left zero — handler populates.
	}
	switch r.Verdict {
	case VerdictPass, VerdictWarn, VerdictFail:
	default:
		return PrimeResult{}, fmt.Errorf("invalid verdict %q", r.Verdict)
	}
	if r.NextAction == "" {
		return PrimeResult{}, fmt.Errorf("decode prime result: next_action is required")
	}
	if r.Findings == nil {
		return PrimeResult{}, fmt.Errorf("decode prime result: findings is required (use [] for none)")
	}
	if r.Picks == nil {
		return PrimeResult{}, fmt.Errorf("decode prime result: picks is required (use [] for none)")
	}
	if r.BMCommands == nil {
		return PrimeResult{}, fmt.Errorf("decode prime result: bm_commands is required (use [] for none)")
	}
	for i := range r.Findings {
		f := r.Findings[i]
		switch f.Severity {
		case SeverityCritical, SeverityMajor, SeverityMinor:
		default:
			return PrimeResult{}, fmt.Errorf("finding[%d]: invalid severity %q", i, f.Severity)
		}
		if !validCategory(f.Category) {
			return PrimeResult{}, fmt.Errorf("finding[%d]: invalid category %q", i, f.Category)
		}
		// Delegate the criterion/evidence/suggestion non-empty checks to the
		// shared validateFindingStrings helper added in Task 1 step 5a. This
		// keeps the rule in one place and matches Parse / ParsePlan /
		// ParseTasksOnly.
		if err := validateFindingStrings(f, fmt.Sprintf("finding[%d]", i)); err != nil {
			return PrimeResult{}, err
		}
		// Delegate to the canonical severity-floor helper in parser_partial.go
		// so the prime parser stays in lock-step with the per-task parser.
		// applySeverityFloor floors BOTH unverifiable_codebase_claim AND
		// convention_deviation to minor; duplicating the rule here would drift.
		r.Findings[i] = applySeverityFloor(r.Findings[i])
	}
	for i, p := range r.Picks {
		switch p.Priority {
		case SeverityCritical, SeverityMajor, SeverityMinor:
		default:
			return PrimeResult{}, fmt.Errorf("pick[%d]: invalid priority %q", i, p.Priority)
		}
		if p.Permalink == "" || p.Reason == "" {
			return PrimeResult{}, fmt.Errorf("pick[%d]: permalink and reason are required", i)
		}
	}
	for i, c := range r.BMCommands {
		if c.Tool == "" {
			return PrimeResult{}, fmt.Errorf("bm_commands[%d]: tool is required", i)
		}
		// args_json must be a JSON object literal (BM tool args are always
		// an object). Empty-object `{}` is acceptable; anything else (array,
		// scalar, `null`, malformed JSON) is rejected with an actionable error.
		if c.ArgsJSON == "" {
			return PrimeResult{}, fmt.Errorf("bm_commands[%d]: args_json is required (use \"{}\" for none)", i)
		}
		var probe map[string]any
		if err := json.Unmarshal([]byte(c.ArgsJSON), &probe); err != nil {
			return PrimeResult{}, fmt.Errorf("bm_commands[%d]: args_json is not a JSON object: %w", i, err)
		}
		// json.Unmarshal of `null` into a map[string]any pointer succeeds
		// with probe == nil, which would silently slip past as "valid JSON
		// object." Reject explicitly — the contract is a JSON object literal.
		if probe == nil {
			return PrimeResult{}, fmt.Errorf("bm_commands[%d]: args_json must be a JSON object literal (use \"{}\" for none, not \"null\")", i)
		}
	}
	return r, nil
}
```

- [ ] **Step 4: Add unit tests**

Create `internal/verdict/prime_parser_test.go`:

```go
package verdict_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

func TestParsePrime_Happy(t *testing.T) {
	raw := []byte(`{
		"verdict": "pass",
		"findings": [],
		"picks": [
			{"permalink": "decisions/0042-x", "reason": "shaped recent caching", "priority": "major"},
			{"permalink": "modules/mcpsrv", "reason": "invariants apply", "priority": "minor"}
		],
		"bm_commands": [],
		"next_action": "attach picks and dispatch"
	}`)
	r, err := verdict.ParsePrime(raw)
	if err != nil {
		t.Fatal(err)
	}
	if r.Verdict != verdict.VerdictPass || len(r.Picks) != 2 {
		t.Fatalf("unexpected result: %+v", r)
	}
	if r.BMCommands == nil {
		t.Fatalf("BMCommands must be non-nil even when empty")
	}
}

func TestParsePrime_Errors(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"invalid verdict", `{"verdict":"oops","findings":[],"picks":[],"bm_commands":[],"next_action":"x"}`, "invalid verdict"},
		{"missing next_action", `{"verdict":"pass","findings":[],"picks":[],"bm_commands":[]}`, "next_action is required"},
		{"missing bm_commands", `{"verdict":"pass","findings":[],"picks":[],"next_action":"x"}`, "bm_commands is required"},
		{"invalid priority", `{"verdict":"pass","findings":[],"picks":[{"permalink":"p","reason":"r","priority":"huge"}],"bm_commands":[],"next_action":"x"}`, "invalid priority"},
		{"invalid category", `{"verdict":"warn","findings":[{"severity":"minor","category":"bogus","criterion":"c","evidence":"e","suggestion":"s"}],"picks":[],"bm_commands":[],"next_action":"x"}`, "invalid category"},
		{"extra fields rejected", `{"verdict":"pass","findings":[],"picks":[],"bm_commands":[],"next_action":"x","mystery":1}`, "unknown field"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := verdict.ParsePrime([]byte(c.raw))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v; want substring %q", err, c.want)
			}
		})
	}
}

func TestParsePrime_AcceptsNewCategories(t *testing.T) {
	for _, c := range []verdict.Category{
		verdict.CategoryKBGap,
		verdict.CategoryAmbiguousPick,
		verdict.CategoryMissingIndexEntry,
	} {
		raw := []byte(`{"verdict":"warn","findings":[{"severity":"minor","category":"` + string(c) + `","criterion":"c","evidence":"e","suggestion":"s"}],"picks":[],"bm_commands":[],"next_action":"x"}`)
		if _, err := verdict.ParsePrime(raw); err != nil {
			t.Fatalf("category %q: %v", c, err)
		}
	}
}

func TestParsePrime_StripsFences(t *testing.T) {
	raw := []byte("```json\n{\"verdict\":\"pass\",\"findings\":[],\"picks\":[],\"bm_commands\":[],\"next_action\":\"x\"}\n```")
	if _, err := verdict.ParsePrime(raw); err != nil {
		t.Fatalf("fenced parse: %v", err)
	}
}

func TestParsePrime_BMCommandsRoundTrip(t *testing.T) {
	raw := []byte(`{
		"verdict": "pass",
		"findings": [],
		"picks": [{"permalink": "decisions/0042-x", "reason": "r", "priority": "minor"}],
		"bm_commands": [{"tool": "read_note", "args_json": "{\"permalink\":\"decisions/0042-x\"}"}],
		"next_action": "go"
	}`)
	r, err := verdict.ParsePrime(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.BMCommands) != 1 || r.BMCommands[0].Tool != "read_note" {
		t.Fatalf("BMCommands not preserved: %+v", r.BMCommands)
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(r.BMCommands[0].ArgsJSON), &args); err != nil {
		t.Fatalf("args_json should parse as object: %v", err)
	}
	if args["permalink"] != "decisions/0042-x" {
		t.Fatalf("args_json content not preserved: %+v", args)
	}
}

func TestParsePrime_RejectsArgsJSONNonObject(t *testing.T) {
	// args_json must be a JSON object literal — array, scalar, JSON null,
	// or malformed JSON is rejected with an actionable error. The `null`
	// case is load-bearing: json.Unmarshal of "null" into &m succeeds with
	// m == nil, which would silently slip past as "valid JSON object" if
	// the parser only checked for unmarshal errors.
	cases := []struct {
		name, argsJSON string
	}{
		{"array", "[1,2,3]"},
		{"scalar", "42"},
		{"null", "null"},
		{"malformed", "{not json"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			payload := fmt.Sprintf(
				`{"verdict":"pass","findings":[],"picks":[{"permalink":"decisions/0042-x","reason":"r","priority":"minor"}],"bm_commands":[{"tool":"read_note","args_json":%q}],"next_action":"go"}`,
				c.argsJSON,
			)
			if _, err := verdict.ParsePrime([]byte(payload)); err == nil || !strings.Contains(err.Error(), "args_json") {
				t.Fatalf("%s: want args_json error, got %v", c.name, err)
			}
		})
	}
}

func TestParsePrime_RejectsReviewerSpoofedServerFields(t *testing.T) {
	// summary_block and partial are server-owned. Non-OpenAI providers do
	// not enforce strict-mode at the wire level, so a malicious or
	// confused reviewer could try to emit them. The parser MUST reject
	// these as unknown fields (decoded into primeWire which has neither).
	cases := []struct{ name, field string }{
		{"summary_block", `"summary_block":"spoof"`},
		{"partial", `"partial":true`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			raw := []byte(`{"verdict":"pass","findings":[],"picks":[],"bm_commands":[],` + c.field + `,"next_action":"go"}`)
			if _, err := verdict.ParsePrime(raw); err == nil || !strings.Contains(err.Error(), "unknown field") {
				t.Fatalf("want unknown-field error for spoofed %s, got %v", c.field, err)
			}
		})
	}
}

func TestParsePrime_RejectsMissingBMCommands(t *testing.T) {
	// bm_commands is required (OpenAI strict-mode invariant). Missing the
	// field must fail even though every other required field is present.
	raw := []byte(`{
		"verdict": "pass",
		"findings": [],
		"picks": [],
		"next_action": "go"
	}`)
	if _, err := verdict.ParsePrime(raw); err == nil || !strings.Contains(err.Error(), "bm_commands is required") {
		t.Fatalf("want bm_commands-required error, got %v", err)
	}
}
```

- [ ] **Step 5: Extend the schema-invariant `schemas` slice**

Task 1 already defined all four strict-mode invariants (`TestReviewerSchemas_RequireAllProperties_ForOpenAIStrictMode`, `TestReviewerSchemas_NoFreeformObject_ForOpenAIStrictMode`, `TestReviewerSchemas_AdditionalPropertiesFalse_ForOpenAIStrictMode`, `TestReviewerSchemas_CategoryEnumsAreInLockstep`). This step only extends their shared `schemas` slice:

```go
{"prime_schema.json", PrimeSchema()},
```

Run `go test ./internal/verdict/...` to confirm `prime_schema.json` passes all four invariants. If `TestReviewerSchemas_NoFreeformObject_ForOpenAIStrictMode` fails, the schema has a freeform object — flatten it to a JSON-encoded string field as we did with `args_json`.

- [ ] **Step 6: Run tests**

Run: `go test -race ./internal/verdict/...`

Expected: PASS.

- [ ] **Step 7: Commit task 5**

```bash
git add internal/verdict/prime*.go internal/verdict/prime_schema.json internal/verdict/schema_invariants_test.go
git commit -m "$(cat <<'EOF'
feat(verdict): add PrimeResult types, schema, and parser

PrimeResult / Pick / BMCommand declared with JSON tags per spec §3.1.
ParsePrime enforces enum constraints, rejects extra fields, tolerates
``\`json fences, and applies the existing unverifiable_codebase_claim
severity floor.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 6: Add the prime prompt template + golden test

**Goal:** `internal/prompts/RenderPrime` produces a reviewer prompt that instructs the model to recommend at most `max_picks` notes from `kb_index`, biased toward the epic context, and to emit `kb_gap` / `ambiguous_pick` / `missing_index_entry` findings as appropriate.

**Acceptance criteria:**
- `internal/prompts/prompts.go` declares `PrimeInput` and `RenderPrime(PrimeInput) (Output, error)`.
- `PrimeInput` carries: `TaskTitle`, `Goal`, `AcceptanceCriteria`, `NonGoals`, `Context`, `KBIndex []KBIndexEntry`, `EpicPermalink`, `MaxPicks int`, `KBStoreIsBasicMemory bool`.
- `internal/prompts/templates/prime.tmpl` renders all four sections (task spec / KB index / picking instructions / output instructions) and references the prime schema by description.
- New golden file `prime_basic.golden` covers a representative input.
- Empty `KBIndex` causes the template to emit explicit "Index is empty" guidance so the reviewer returns gaps only.
- `KBStoreIsBasicMemory: true` causes the template to instruct the reviewer to emit one `{ "tool": "read_note", "args_json": "{\"permalink\":\"…\"}" }` entry per pick (note: `args_json` is a JSON-encoded string, NOT a nested object — per the "Cross-cutting technical constraints" section and `prime_schema.json`); `false` causes the template to instruct the reviewer to emit `bm_commands: []`. The field is ALWAYS present in the rendered output schema — it is required by `prime_schema.json` (strict-mode invariant) and must not be made optional.
- `go test -race ./internal/prompts/...` passes.

**Non-goals:**
- Do not register the tool yet (Task 7).
- Do not make `bm_commands` an optional template branch — it is required by the schema and the reviewer must emit `[]` rather than omit it when the caller is not using Basic Memory. The handler in Task 7 strips populated entries server-side when the env is unset as a belt-and-braces defense, replacing them with an empty slice (NOT nil) to preserve the strict-schema posture on the wire.

**Context:**
The existing render functions live in `internal/prompts/prompts.go:59-122` (post-merge) and follow the system + user prompt pattern. The `KBIndexEntry` type belongs in `prompts.go` (alongside `File` / `PreInput` / etc.); the spec describes it as `{permalink, type, title, summary, tags?}`. Golden tests use the in-package `golden(t, name, got)` helper at `prompts_test.go:32`; the bare basename is passed (no `testdata/` prefix, no `.golden` suffix) and the content is `out.System+"\n---USER---\n"+out.User`.

**Files:**
- Modify: `internal/prompts/prompts.go`
- Create: `internal/prompts/templates/prime.tmpl`
- Modify: `internal/prompts/prompts_test.go`
- Create: `internal/prompts/testdata/prime_basic.golden`

- [ ] **Step 1: Declare `KBIndexEntry` and `PrimeInput` in `prompts.go`**

```go
type KBIndexEntry struct {
	Permalink string
	Type      string
	Title     string
	Summary   string
	Tags      []string
}

type PrimeInput struct {
	TaskTitle             string
	Goal                  string
	AcceptanceCriteria    []string
	NonGoals              []string
	Context               string
	KBIndex               []KBIndexEntry
	EpicPermalink         string
	MaxPicks              int
	KBStoreIsBasicMemory  bool
}

func RenderPrime(in PrimeInput) (Output, error) {
	body, err := render("prime.tmpl", in)
	if err != nil {
		return Output{}, err
	}
	return Output{System: systemPrompt, User: body}, nil
}
```

- [ ] **Step 2: Write the template**

Create `internal/prompts/templates/prime.tmpl`:

```gotemplate
## Task spec

Title: {{.TaskTitle}}
Goal:  {{.Goal}}

Acceptance criteria:
{{range .AcceptanceCriteria}}- {{.}}
{{end}}{{if .NonGoals}}
Non-goals:
{{range .NonGoals}}- {{.}}
{{end}}{{end}}{{if .Context}}
Context:
{{.Context}}
{{end}}{{if .EpicPermalink}}
Epic in flight: {{.EpicPermalink}}
Bias picks toward this epic's `touches_modules` and linked decisions.
{{end}}

## Knowledge-base index

Limit: at most {{.MaxPicks}} picks.

{{if .KBIndex}}
{{range .KBIndex}}- [{{.Type}}] {{.Permalink}} — {{.Title}}: {{.Summary}}
{{end}}{{else}}
The index is empty. Do NOT fabricate picks. Return zero picks and emit one `kb_gap` finding describing the absence.
{{end}}

## What to do

1. Recommend the smallest set of notes (≤ {{.MaxPicks}}) the implementer must read before starting this task. For each, set `priority`:
   - `critical` — without it the implementer will almost certainly produce wrong code.
   - `major` — the implementer can make the change without it but is very likely to violate an invariant or duplicate prior work.
   - `minor` — useful background that won't independently cause regressions.
2. If the index contains no note that plausibly grounds the task, emit a `kb_gap` finding naming what is missing.
3. If the task spec mentions a concept that has no glossary or feature note in the index, emit a `missing_index_entry` finding.
4. If two notes look like plausible authorities for the same decision (e.g. two ADRs with overlapping `supersedes` chains), emit an `ambiguous_pick` finding.
5. Each pick must reference a `permalink` present in the supplied index. Do not invent permalinks.
{{if .EpicPermalink}}
6. The `epic_permalink` field above identifies an in-flight epic note. If it is not present in the index, emit a `missing_index_entry` finding with `severity: major`.
{{end}}
{{if .KBStoreIsBasicMemory}}
7. The caller will paste the prime output into a Basic Memory MCP client. For every pick, also emit one entry under `bm_commands` shaped exactly:

   `{ "tool": "read_note", "args_json": "{\"permalink\":\"<pick permalink>\"}" }`

   `args_json` is a JSON-encoded string (NOT a nested object) — OpenAI strict structured-outputs rejects freeform object schemas, so the wire format flattens args to a string the caller parses. Always emit `args_json` as a JSON object literal in string form (use `"{}"` if there are no args).

   Order `bm_commands` to mirror `picks`.
{{else}}
7. The caller is not using Basic Memory. Emit `bm_commands` as an empty array `[]`. Do NOT omit the field — the response schema requires it.
{{end}}

## Output schema (paste-ready)

Return ONLY a JSON object matching this shape:

```
{
  "verdict": "pass | warn | fail",
  "findings": [
    {"severity": "critical|major|minor", "category": "kb_gap|ambiguous_pick|missing_index_entry|unverifiable_codebase_claim|payload_too_large|other", "criterion": "<string>", "evidence": "<string>", "suggestion": "<string>"}
  ],
  "picks": [
    {"permalink": "<existing kb_index permalink>", "reason": "<one sentence>", "priority": "critical|major|minor"}
  ],
  "bm_commands": [{{if .KBStoreIsBasicMemory}}
    {"tool": "read_note", "args_json": "{\"permalink\":\"<pick permalink>\"}"}
  {{end}}],
  "next_action": "<one sentence telling the controller what to do next>"
}
```
```

- [ ] **Step 3: Add a golden test**

Append to `internal/prompts/prompts_test.go` (the file is `package prompts`, so no qualifier prefix):

```go
func TestRenderPrime_Basic(t *testing.T) {
	in := PrimeInput{
		TaskTitle:          "Task 7: extract handler",
		Goal:               "Implement extract_project_knowledge.",
		AcceptanceCriteria: []string{"Returns Proposals when given a completion envelope."},
		Context:            "Anti-tangent stays stateless.",
		KBIndex: []KBIndexEntry{
			{Permalink: "decisions/0042-cache-pass", Type: "decision", Title: "Cache pass reviews", Summary: "TTL 3m."},
			{Permalink: "modules/mcpsrv", Type: "module", Title: "mcpsrv", Summary: "stdout reserved."},
		},
		EpicPermalink:        "epics/2026-q2-large-project-support",
		MaxPicks:             10,
		KBStoreIsBasicMemory: true,
	}
	out, err := RenderPrime(in)
	require.NoError(t, err)
	golden(t, "prime_basic", out.System+"\n---USER---\n"+out.User)
}
```

- [ ] **Step 4: Materialize and inspect**

Run: `go test ./internal/prompts/... -update -run TestRenderPrime_Basic`

Run: `cat internal/prompts/testdata/prime_basic.golden`

Expected: Output contains all six sections (task spec, KB index, what to do, bm_commands instruction, output schema), references both index entries, and instructs the reviewer to emit `bm_commands`.

- [ ] **Step 5: Run package tests**

Run: `go test -race ./internal/prompts/...`

Expected: PASS.

- [ ] **Step 6: Commit task 6**

```bash
git add internal/prompts/
git commit -m "$(cat <<'EOF'
feat(prompts): add prime_project_knowledge prompt template

Declares KBIndexEntry / PrimeInput / RenderPrime and a prime.tmpl
that instructs the reviewer to recommend picks bounded by max_picks,
emit kb_gap / ambiguous_pick / missing_index_entry findings, and
conditionally produce bm_commands.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 7: Implement the `prime_project_knowledge` handler

**Goal:** A new MCP tool `prime_project_knowledge` is registered on the server. End-to-end, callers can pass `task_title` / `goal` / `acceptance_criteria` / `kb_index` and receive a `PrimeResult` envelope. The tool is stateless (no session). Output adaptation strips `bm_commands` when `ANTI_TANGENT_KB_STORE` is empty.

**Acceptance criteria:**
- `internal/mcpsrv/prime_handler.go` declares `PrimeProjectKnowledgeArgs` matching spec §3.1 inputs:
  - `task_title` (required), `goal` (required), `acceptance_criteria` (required, ≥1), `non_goals`, `context`, `kb_index` (default empty), `epic_permalink`, `max_picks` (default 10, ceiling 25), `max_tokens_override`, `model_override`.
- `kb_index` entries are typed as `KBIndexEntryArg { Permalink, Type, Title, Summary string; Tags []string }`.
- The handler order mirrors `ValidateCompletion` in `handlers.go:862-887` exactly:
  1. Validates required fields (`task_title`, `goal`, ≥1 AC). Returns a Go error → MCP error on failure.
  2. Picks `MaxPicks` (default 10, ceiling 25).
  3. Computes payload size = sum of all string fields + serialized `kb_index`.
  4. Resolves `effectiveMaxTokens` (yielding a `verdict.Finding` clamp value that's zero when no clamp applies). This happens BEFORE the payload check so a too-large envelope can carry the clamp finding for free.
  5. **Payload-cap check**. If size > `MaxPayloadBytes`, returns `primeEnvelopeResult(prependPrimeClamp(primeTooLargeResult(size, cap), clamp), h.deps.Cfg.PrimeModel.String(), 0)` — using the configured default model, NOT a resolved override (mirrors `tooLargeEnvelope`'s use of `cfg.PostModel`). Severity is `SeverityMajor` to match `tooLargeEnvelope` and `tooLargePlanResult`.
  6. Resolves model via `args.ModelOverride` → `cfg.PrimeModel`. Validates via `providers.ValidateModel` (same pattern as existing handlers). `cfg.PrimeModel` is guaranteed non-zero by `config.Load`'s fallback chain (Task 2: `ANTI_TANGENT_PRIME_MODEL` → `ANTI_TANGENT_PLAN_MODEL` → `ANTI_TANGENT_PRE_MODEL`); no extra nil-check is needed in this handler. A misspelled `model_override` produces an error here — but only after a clean payload-too-large rejection has already happened, so the caller doesn't see two failure modes interleaved.
  7. If `MaxTokensOverride == 0`, replace `maxTokens` with `adaptivePrimeMaxTokens(h.deps.Cfg, len(args.KBIndex))` — explicit override (clamped by `MaxTokensCeiling`) wins, otherwise scale `max(cfg.PrimeMaxTokens, min(cfg.MaxTokensCeiling, 1500 + 50*len(kb_index)))`. Add a helper `adaptivePrimeMaxTokens` that mirrors `adaptivePlanMaxTokens` in `internal/mcpsrv/plan_budget.go`.
  8. Renders the prompt via `prompts.RenderPrime`, passing `KBStoreIsBasicMemory: cfg.KBStore == "basic-memory"`.
  9. Calls the reviewer with `JSONSchema: verdict.PrimeSchema()`.
  10. Parses via `verdict.ParsePrime`. One parse-retry with `RetryHint` (mirroring `review()` in `handlers.go:153-200`).
  11. **Post-processing:** if `cfg.KBStore == ""`, set `result.BMCommands = []verdict.BMCommand{}` (empty slice, NOT nil — `BMCommand` arrays are required by the schema and a nil slice marshals as `null`, breaking the wire-format contract). If `cfg.KBStore == "basic-memory"` and any `Pick.Permalink` does not look like a BM permalink (i.e. contains a leading `/` or a `://`), emit a server-side `other / kb_store_mismatch` minor finding per spec §5.5.
  12. Populates `SummaryBlock` via `formatPrimeSummary` (new helper in `internal/mcpsrv/summary.go`).
  13. Logs one structured JSON line on stderr matching spec §5.6.
- New unit tests in `internal/mcpsrv/prime_handler_test.go`:
  - happy path returns a parsed PrimeResult with picks (uses `fakeReviewer` returning a canned `PrimeResult` JSON).
  - missing required field → MCP error.
  - oversized payload → `payload_too_large` synthetic envelope.
  - `KBStore=""` strips `bm_commands` from the reviewer's response.
  - `KBStore="basic-memory"` with a non-BM permalink emits `kb_store_mismatch`.
  - empty `kb_index` is accepted (reviewer is expected to emit `kb_gap`).
  - `max_picks` defaults to 10 and is ceilinged at 25.
- Tool registered in `internal/mcpsrv/server.go`.
- `go test -race ./internal/mcpsrv/...` passes.

**Non-goals:**
- Do not create a session.
- Do not add a partial-recovery path.
- Do not validate that `Pick.Permalink` values exist in `kb_index`. The reviewer is instructed not to invent permalinks; if it does, surface as `other` rather than rejecting server-side. (Open question: tighten this in a follow-up if field data shows reviewers inventing.)

**Context:**
The existing `validate_completion` handler at `internal/mcpsrv/handlers.go:862` is the closest existing handler shape since both are stateless-ish (validate_completion supports session_id but allows empty). The new tool is fully stateless. `effectiveMaxTokens`, `prependClamp`, and `handlePerTaskReviewErr` already exist in mcpsrv and should be reused — the `review()` helper at line 162 is the canonical pattern for retry-on-parse-failure plus `providers.ErrResponseTruncated` handling.

**Files:**
- Create: `internal/mcpsrv/prime_handler.go`
- Create: `internal/mcpsrv/prime_handler_test.go`
- Modify: `internal/mcpsrv/server.go`
- Modify: `internal/mcpsrv/summary.go`
- Modify: `internal/mcpsrv/plan_budget.go` (add `adaptivePrimeMaxTokens`)
- Modify: `internal/mcpsrv/integration_test.go`

- [ ] **Step 1: Add `adaptivePrimeMaxTokens` helper**

Append to `internal/mcpsrv/plan_budget.go`:

```go
// adaptivePrimeMaxTokens implements spec §5.3 sizing for prime_project_knowledge.
func adaptivePrimeMaxTokens(cfg config.Config, kbIndexLen int) int {
	scaled := 1500 + 50*kbIndexLen
	if scaled > cfg.MaxTokensCeiling {
		scaled = cfg.MaxTokensCeiling
	}
	if scaled < cfg.PrimeMaxTokens {
		return cfg.PrimeMaxTokens
	}
	return scaled
}
```

`adaptiveExtractMaxTokens` is **not** added here — it lives in Task 10 to keep prime-cluster commits scoped to prime-only code. Prime and extract clusters still execute sequentially (see File Structure); the localized helper is for diff-scope hygiene, not parallelization.

- [ ] **Step 2: Implement the handler skeleton**

Create `internal/mcpsrv/prime_handler.go`. The handler is wired analogously to `ValidateTaskSpec`. Key elements:

```go
package mcpsrv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/patiently/anti-tangent-mcp/internal/config"
	"github.com/patiently/anti-tangent-mcp/internal/prompts"
	"github.com/patiently/anti-tangent-mcp/internal/providers"
	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

const (
	defaultMaxPicks = 10
	maxMaxPicks     = 25
)

type KBIndexEntryArg struct {
	Permalink string   `json:"permalink"`
	Type      string   `json:"type"`
	Title     string   `json:"title"`
	Summary   string   `json:"summary"`
	Tags      []string `json:"tags,omitempty"`
}

type PrimeProjectKnowledgeArgs struct {
	TaskTitle          string            `json:"task_title"          jsonschema:"required"`
	Goal               string            `json:"goal"                jsonschema:"required"`
	AcceptanceCriteria []string          `json:"acceptance_criteria" jsonschema:"required"`
	NonGoals           []string          `json:"non_goals,omitempty"`
	Context            string            `json:"context,omitempty"`
	KBIndex            []KBIndexEntryArg `json:"kb_index,omitempty"`
	EpicPermalink      string            `json:"epic_permalink,omitempty"`
	MaxPicks           int               `json:"max_picks,omitempty"`
	ModelOverride      string            `json:"model_override,omitempty"`
	MaxTokensOverride  int               `json:"max_tokens_override,omitempty"`
}

func primeProjectKnowledgeTool() *mcp.Tool {
	return &mcp.Tool{
		Name: "prime_project_knowledge",
		Description: "Given a task spec and a kb_index of available knowledge-base notes, return prioritized note picks for the implementer to read before starting. Stateless; no session created. " +
			"Emits paste-ready bm_commands when ANTI_TANGENT_KB_STORE=basic-memory.",
	}
}

func (h *handlers) PrimeProjectKnowledge(ctx context.Context, _ *mcp.CallToolRequest, args PrimeProjectKnowledgeArgs) (*mcp.CallToolResult, verdict.PrimeResult, error) {
	if strings.TrimSpace(args.TaskTitle) == "" || strings.TrimSpace(args.Goal) == "" {
		return nil, verdict.PrimeResult{}, errors.New("task_title and goal are required")
	}
	if len(args.AcceptanceCriteria) == 0 {
		return nil, verdict.PrimeResult{}, errors.New("acceptance_criteria must contain at least one entry")
	}
	maxPicks := args.MaxPicks
	if maxPicks <= 0 {
		maxPicks = defaultMaxPicks
	}
	if maxPicks > maxMaxPicks {
		maxPicks = maxMaxPicks
	}

	// Payload size guard. Sum string fields and serialized KB index length.
	size := len(args.TaskTitle) + len(args.Goal) + len(args.Context) + len(args.EpicPermalink)
	for _, ac := range args.AcceptanceCriteria {
		size += len(ac)
	}
	for _, ng := range args.NonGoals {
		size += len(ng)
	}
	if kbBytes, _ := json.Marshal(args.KBIndex); kbBytes != nil {
		size += len(kbBytes)
	}
	// Match the per-task handler ordering at handlers.go:862-887: resolve
	// the clamp finding FIRST, then check payload, then resolve the model
	// override. This way a too-large payload rejects fast even when the
	// caller also passed a misspelled model_override (which would otherwise
	// produce a less actionable error). The synthetic too-large envelope
	// reports the configured-default model (h.deps.Cfg.PrimeModel) — not a
	// resolved override — exactly like tooLargeEnvelope does for per-task.
	maxTokens, clamp, err := effectiveMaxTokens(args.MaxTokensOverride, h.deps.Cfg.PrimeMaxTokens, h.deps.Cfg.MaxTokensCeiling)
	if err != nil {
		return nil, verdict.PrimeResult{}, err
	}

	if size > h.deps.Cfg.MaxPayloadBytes {
		return primeEnvelopeResult(prependPrimeClamp(primeTooLargeResult(size, h.deps.Cfg.MaxPayloadBytes), clamp), h.deps.Cfg.PrimeModel.String(), 0)
	}

	model, err := h.resolveModel(args.ModelOverride, h.deps.Cfg.PrimeModel)
	if err != nil {
		return nil, verdict.PrimeResult{}, err
	}

	if args.MaxTokensOverride == 0 {
		maxTokens = adaptivePrimeMaxTokens(h.deps.Cfg, len(args.KBIndex))
	}

	rendered, err := prompts.RenderPrime(prompts.PrimeInput{
		TaskTitle:            args.TaskTitle,
		Goal:                 args.Goal,
		AcceptanceCriteria:   args.AcceptanceCriteria,
		NonGoals:             args.NonGoals,
		Context:              args.Context,
		KBIndex:              toPromptKBIndex(args.KBIndex),
		EpicPermalink:        args.EpicPermalink,
		MaxPicks:             maxPicks,
		KBStoreIsBasicMemory: h.deps.Cfg.KBStore == "basic-memory",
	})
	if err != nil {
		return nil, verdict.PrimeResult{}, fmt.Errorf("render prime prompt: %w", err)
	}

	result, modelUsed, ms, partialRaw, err := h.reviewPrime(ctx, model, rendered, maxTokens)
	if errors.Is(err, providers.ErrResponseTruncated) {
		// Synthesize a warn envelope with category:other / criterion:reviewer_response
		// — mirror the per-task path's handlePerTaskReviewErr-style synthesis.
		return primeEnvelopeResult(prependPrimeClamp(primeTruncationResult(partialRaw), clamp), model.String(), 0)
	}
	if err != nil {
		return nil, verdict.PrimeResult{}, err
	}

	if h.deps.Cfg.KBStore == "" {
		// Reviewer is contracted to emit [] when KBStoreIsBasicMemory:false; still
		// strip server-side as a belt-and-braces guard. Use empty slice, NOT nil,
		// so MarshalJSON preserves the strict-schema `bm_commands: []` posture.
		result.BMCommands = []verdict.BMCommand{}
	} else if h.deps.Cfg.KBStore == "basic-memory" {
		result.Findings = append(result.Findings, kbStoreMismatchFindings(result.Picks)...)
	}

	result = prependPrimeClamp(result, clamp)
	slog.Info("prime_project_knowledge",
		slog.String("tool", "prime_project_knowledge"),
		slog.Int64("duration_ms", ms),
		slog.String("model", modelUsed),
		slog.String("verdict", string(result.Verdict)),
		slog.Int("picks", len(result.Picks)),
		slog.Int("findings", len(result.Findings)),
		slog.Int("kb_index_size", len(args.KBIndex)),
		slog.String("epic", args.EpicPermalink),
	)
	return primeEnvelopeResult(result, modelUsed, ms)
}

func toPromptKBIndex(in []KBIndexEntryArg) []prompts.KBIndexEntry {
	out := make([]prompts.KBIndexEntry, len(in))
	for i, e := range in {
		out[i] = prompts.KBIndexEntry{
			Permalink: e.Permalink, Type: e.Type, Title: e.Title, Summary: e.Summary, Tags: e.Tags,
		}
	}
	return out
}

// kbStoreMismatchFindings emits one minor `other / kb_store_mismatch` finding
// per pick whose permalink doesn't look like a BM permalink (contains "/" but
// also leading "/", or contains "://").
func kbStoreMismatchFindings(picks []verdict.Pick) []verdict.Finding {
	var fs []verdict.Finding
	for _, p := range picks {
		if strings.HasPrefix(p.Permalink, "/") || strings.Contains(p.Permalink, "://") {
			fs = append(fs, verdict.Finding{
				Severity:   verdict.SeverityMinor,
				Category:   verdict.CategoryOther,
				Criterion:  "kb_store_mismatch",
				Evidence:   fmt.Sprintf("pick permalink %q does not look like a Basic Memory permalink", p.Permalink),
				Suggestion: "Use a Basic Memory permalink (e.g. decisions/0042-…); strip leading slashes and URI schemes.",
			})
		}
	}
	return fs
}

// reviewPrime is the prime-shaped sibling of review(). It mirrors review()'s
// truncation handling: on providers.ErrResponseTruncated the caller can build
// a synthetic envelope with a category:other, criterion:reviewer_response
// finding so the truncation surfaces like every other per-task tool does.
func (h *handlers) reviewPrime(ctx context.Context, model config.ModelRef, p prompts.Output, maxTokens int) (verdict.PrimeResult, string, int64, []byte, error) {
	rv, err := h.deps.Reviews.Get(model.Provider)
	if err != nil {
		return verdict.PrimeResult{}, "", 0, nil, err
	}
	start := time.Now()
	req := providers.Request{
		Model: model.Model, System: p.System, User: p.User,
		MaxTokens: maxTokens, JSONSchema: verdict.PrimeSchema(),
	}
	resp, err := rv.Review(ctx, req)
	if err != nil {
		if errors.Is(err, providers.ErrResponseTruncated) {
			return verdict.PrimeResult{}, "", 0, resp.RawJSON, err
		}
		return verdict.PrimeResult{}, "", 0, nil, err
	}
	r, err := verdict.ParsePrime(resp.RawJSON)
	if err != nil {
		req.User = p.User + "\n\n" + verdict.RetryHint()
		resp, err = rv.Review(ctx, req)
		if err != nil {
			if errors.Is(err, providers.ErrResponseTruncated) {
				return verdict.PrimeResult{}, "", 0, resp.RawJSON, err
			}
			return verdict.PrimeResult{}, "", 0, nil, err
		}
		r, err = verdict.ParsePrime(resp.RawJSON)
		if err != nil {
			return verdict.PrimeResult{}, "", 0, nil, fmt.Errorf("prime provider response failed schema after retry: %w", err)
		}
	}
	modelUsed := model.Provider + ":" + resp.Model
	if resp.Model == "" {
		modelUsed = model.String()
	}
	return r, modelUsed, time.Since(start).Milliseconds(), nil, nil
}
```

The skeleton above references `primeTruncationResult`; define it next to `primeTooLargeResult`:

```go
// primeTruncationResult builds the synthetic envelope for an
// ErrResponseTruncated. Matches the per-task truncation pattern used by the
// existing review() helper in handlers.go. BMCommands is initialized to an
// empty slice (NOT nil) so the strict-schema invariant survives if the result
// is round-tripped to JSON.
// primeTruncationResult mirrors truncatedEnvelope (handlers.go:390-403) for
// the no-analysis case where the reviewer's response was cut off and NO
// complete findings were recovered. The existing per-task convention is:
// - Severity = Minor (not Major).
// - Partial is NOT set true. Partial: true is reserved for the
//   recoverPartialFindings path (handlers.go:472-506) which actually
//   recovered complete findings; this helper has nothing to recover from.
// `partialRaw` is accepted for API symmetry but not embedded in evidence —
// matching truncatedEnvelope's posture of citing the error string rather
// than the byte count.
func primeTruncationResult(_ []byte) verdict.PrimeResult {
	return verdict.PrimeResult{
		Verdict: verdict.VerdictWarn,
		Findings: []verdict.Finding{{
			Severity:   verdict.SeverityMinor,
			Category:   verdict.CategoryOther,
			Criterion:  "reviewer_response",
			Evidence:   providers.ErrResponseTruncated.Error(),
			Suggestion: "Raise ANTI_TANGENT_PRIME_MAX_TOKENS (bounded by ANTI_TANGENT_MAX_TOKENS_CEILING), shrink kb_index, or pass an explicit max_tokens_override.",
		}},
		Picks:      []verdict.Pick{},
		BMCommands: []verdict.BMCommand{},
		NextAction: "Retry with a higher max_tokens_override (or raise the configured max-tokens cap).",
	}
}
```

Add `primeTooLargeResult` and `primeEnvelopeResult` helpers next to it (mirroring `tooLargeEnvelope` / `envelopeResult` in handlers.go; primary differences: PrimeResult shape; `primeEnvelopeResult` populates `SummaryBlock` via the new `formatPrimeSummary` you'll add in step 4):

```go
// primeTooLargeResult mirrors tooLargeEnvelope (handlers.go:622) and
// tooLargePlanResult (handlers.go:1219). Severity is Major — same as the
// existing helpers — so all three payload-too-large surfaces converge on
// one severity across the tool family.
func primeTooLargeResult(size, cap int) verdict.PrimeResult {
	return verdict.PrimeResult{
		Verdict:    verdict.VerdictFail,
		Findings:   []verdict.Finding{{
			Severity: verdict.SeverityMajor, Category: verdict.CategoryTooLarge,
			Criterion: "payload", Evidence: fmt.Sprintf("payload %d bytes > cap %d", size, cap),
			Suggestion: "Shrink kb_index (search_notes pre-filtering) or split into multiple calls.",
		}},
		Picks:      []verdict.Pick{},
		BMCommands: []verdict.BMCommand{},
		NextAction: "Shrink kb_index and retry.",
	}
}

// prependPrimeClamp inserts the clamp finding at the head of r.Findings when
// clamp is non-zero. Mirrors prependClamp / prependPlanClamp in handlers.go so
// every flow (success, partial recovery, truncation, payload-too-large) treats
// the clamp finding identically — it lives in Findings, NOT in next_action.
func prependPrimeClamp(r verdict.PrimeResult, clamp verdict.Finding) verdict.PrimeResult {
	if clamp.Severity == "" {
		return r
	}
	r.Findings = append([]verdict.Finding{clamp}, r.Findings...)
	return r
}

func primeEnvelopeResult(r verdict.PrimeResult, modelUsed string, reviewMS int64) (*mcp.CallToolResult, verdict.PrimeResult, error) {
	r.SummaryBlock = formatPrimeSummary(r, modelUsed, reviewMS)
	wrapper := struct {
		verdict.PrimeResult
		ModelUsed string `json:"model_used"`
		ReviewMS  int64  `json:"review_ms"`
	}{r, modelUsed, reviewMS}
	body, err := json.Marshal(wrapper)
	if err != nil {
		return nil, verdict.PrimeResult{}, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(body)}}}, r, nil
}
```

(Adjust the wrapper to match exactly how existing envelope helpers handle `model_used` / `review_ms`. The existing `planEnvelopeResult` is the closest analog.)

- [ ] **Step 3: Register the tool**

In `internal/mcpsrv/server.go::New`:

```go
mcp.AddTool(srv, primeProjectKnowledgeTool(), h.PrimeProjectKnowledge)
```

- [ ] **Step 4: Add `formatPrimeSummary` to `summary.go`**

Append:

```go
// formatPrimeSummary renders a paste-ready summary for a PrimeResult.
func formatPrimeSummary(r verdict.PrimeResult, modelUsed string, reviewMS int64) string {
	var b strings.Builder
	b.WriteString("anti-tangent envelope (prime_project_knowledge)\n")
	fmt.Fprintf(&b, "  verdict:       %s\n", r.Verdict)
	if r.Partial {
		b.WriteString("  partial:       true\n")
	}
	fmt.Fprintf(&b, "  model_used:    %s\n", modelUsed)
	fmt.Fprintf(&b, "  review_ms:     %d\n", reviewMS)
	writeFindingsSummary(&b, r.Findings, "  ")
	fmt.Fprintf(&b, "  picks: %d\n", len(r.Picks))
	for _, p := range r.Picks {
		fmt.Fprintf(&b, "    - [%s] %s — %s\n", p.Priority, p.Permalink, truncate(p.Reason, summaryEvidenceMax))
	}
	if len(r.BMCommands) > 0 {
		fmt.Fprintf(&b, "  bm_commands: %d\n", len(r.BMCommands))
	}
	fmt.Fprintf(&b, "  next_action:   %s\n", r.NextAction)
	return b.String()
}
```

- [ ] **Step 5: Unit tests**

Create `internal/mcpsrv/prime_handler_test.go`. Cover:

- happy path (canned reviewer JSON),
- missing required fields,
- oversized payload,
- `bm_commands` strip with `KBStore=""`,
- `kb_store_mismatch` finding emission with `KBStore="basic-memory"` and a non-BM permalink,
- empty `kb_index` is accepted,
- `max_picks` defaults to 10 and ceilings at 25,
- truncation parity: when the fake reviewer returns `providers.ErrResponseTruncated`, the handler emits a `PrimeResult` envelope with `verdict: warn`, exactly one finding `{ severity: minor, category: other, criterion: reviewer_response }`, and a populated `SummaryBlock`. `Partial` must be the zero value (false / omitted from JSON) — partial-findings recovery is not implemented for prime in 0.6.0; the no-analysis truncation envelope mirrors `truncatedEnvelope` at handlers.go:390-403. Mirror the existing per-task truncation tests in `handlers_test.go`.
- **parse-retry path**: a two-response fake reviewer returns malformed JSON on the first call (e.g. `{ not json`) and valid JSON on the second. Asserts `reviewPrime` retries once with the `RetryHint` appended to the user prompt (mirroring `review()` at handlers.go:162-200) and ultimately returns the parsed `PrimeResult`. A separate sub-test exercises malformed-twice → assert the handler returns the underlying `verdict.ErrInvalidJSON`-style error wrapped with `"prime provider response failed schema after retry"`. Both tests use the same `fakeReviewer` pattern as the per-task `handlers_test.go` retry coverage.
- envelope shape parity: every happy-path response carries a non-empty `summary_block`, a `model_used` string of the form `provider:model`, and a `review_ms >= 0` integer. Add an explicit assertion that the parsed `model_used` matches the configured / overridden model.
- clamp finding propagation: when `max_tokens_override` exceeds `MaxTokensCeiling`, the resulting `result.Findings[0]` is the clamp finding (severity `minor`, category `other`, criterion `max_tokens_override`) — `prependPrimeClamp` inserts it at the head of Findings, matching how `prependClamp` works for per-task envelopes (handlers.go:438-447). Clamp does NOT modify `next_action`.

Pattern is identical to existing `handlers_test.go` fixtures — reuse `fakeReviewer` and `passResp`-style helpers.

- [ ] **Step 6: Integration test (end-to-end)**

In `internal/mcpsrv/integration_test.go`, add a new test:

```go
func TestIntegration_PrimeProjectKnowledge(t *testing.T) {
	// Boilerplate mirrors TestIntegration_ValidatePlan.
	// Fake reviewer returns: {"verdict":"pass","findings":[],"picks":[{"permalink":"decisions/0042","reason":"r","priority":"major"}],"bm_commands":[],"next_action":"go"}.
	// bm_commands is REQUIRED by prime_schema.json — ParsePrime rejects nil — so the canned JSON must include "bm_commands":[] even when the test runs with KBStore="" (which would strip the field server-side anyway).
	// Call prime_project_knowledge with a small kb_index; assert verdict==pass and len(picks)==1.
}
```

- [ ] **Step 7: Run tests**

Run: `go test -race ./internal/mcpsrv/...`

Expected: PASS.

- [ ] **Step 8: Commit task 7**

```bash
git add internal/mcpsrv/ internal/prompts/
git commit -m "$(cat <<'EOF'
feat(mcpsrv): implement prime_project_knowledge tool

Stateless MCP tool that returns prioritized note picks for a task
given a kb_index. Output-token budget scales with index size per
spec §5.3. KBStore env gates bm_commands emission and triggers
kb_store_mismatch findings for non-BM permalinks. Includes
adaptivePrimeMaxTokens helper and formatPrimeSummary block.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 8: Add extract verdict types, schema, and parser

**Goal:** `internal/verdict/` exports `ExtractResult`, `Proposal`, and `ParseExtract` so the extract handler (Task 10) has a parser ready.

**Acceptance criteria:**
- `internal/verdict/extract.go` declares:
  - `ProposalAction string` const set: `create`, `update`, `supersede`.
  - `ProposalType string` const set: `decision`, `module`, `feature`, `glossary`, `epic`.
  - `Proposal struct` with `Action`, `Type`, `Permalink`, `Title`, `FrontmatterJSON string` (JSON-encoded object string — same rationale as `args_json` on BMCommand: OpenAI strict mode rejects freeform `object` schemas and note frontmatter has variable per-type keys), `Body`, `BodyPatch`, `Rationale`, `EvidenceRefs []string`, `Supersedes []string`. Callers parse `FrontmatterJSON` after receipt.
  - `ExtractResult struct { Verdict; Findings; Proposals []Proposal; BMCommands []BMCommand; NextAction; SummaryBlock; Partial }`.
- `internal/verdict/extract_schema.json` enforces **field-shape** rules only: required field presence (`action`, `type`, `permalink`, `title`, `rationale`, `evidence_refs`), enum values for `action` and `type`, the full-vocabulary `findings.category` enum (matching `internal/verdict/schema.json` after Task 1), and the `bm_commands` array (enumerated under `properties` AND listed in top-level `required` — the v0.5.1 strict-schema invariant in `internal/verdict/schema_invariants_test.go` rejects any optional property). **`minLength` / `minimum` usage MUST follow Task 1's keyword decision** (same posture as Task 5): keep `minLength: 1` on required strings if Task 1 confirmed OpenAI strict mode supports it; otherwise strip them. Either way, Task 1's unconditional `validateFindingStrings` helper enforces non-empty `criterion`/`evidence`/`suggestion` at the parser layer, so the schema's `minLength` is belt-and-braces only. `ParseExtract` calls `validateFindingStrings` (refactor away the inline duplication). Per-proposal optional fields (`frontmatter_json`, `body`, `body_patch`, `supersedes`) likewise must be added to that level's `required` list. `frontmatter_json` is a JSON-encoded object string (NOT a nested `object`) — strict mode rejects freeform `{"type": "object"}`, and the no-freeform-object invariant from Task 5 would fail otherwise. Reviewer emits `"{}"` placeholder when no frontmatter is being changed.
- The **action-conditional business rules** are enforced by `ParseExtract`, not by the JSON schema (draft-07 expression of these conditional constraints is awkward, and a Go-side check produces a more actionable error). Per spec §3.2 the rules are:
  - `action: create` requires a non-empty `body`. `body_patch` is an update-only alternative; for a brand-new note there is nothing to patch.
  - `action: update` requires `body` OR `body_patch` (exactly one non-empty). The reviewer chooses based on note size — full `body` for small notes, unified-diff `body_patch` for large ones.
  - `action: supersede` requires a non-empty `supersedes` array. `body` and `body_patch` may both be empty in the supersede proposal itself; the new superseding note (if any) is emitted as a separate `create` proposal.
  - `body` and `body_patch` are mutually exclusive in any single proposal.
- `ParseExtract` enforces enums (`verdict`, `severity`, `category`, `Proposal.Action`, `Proposal.Type`), required-field presence post-decode (`Findings`, `Proposals`, `BMCommands` must be non-nil arrays; per-proposal `Permalink`, `Title`, `Rationale`, `EvidenceRefs` must be non-empty; per-proposal `FrontmatterJSON`, `Body`, `BodyPatch`, `Supersedes` must be PRESENT on the wire — empty-string / empty-array placeholders are accepted but literal field-omission is not), and the action-conditional rules above. **Wire indirection**: the parser decodes into a `proposalWire` struct that uses `*string` pointers for `FrontmatterJSON`, `Body`, and `BodyPatch` so missing (nil pointer) is distinguishable from present-but-empty (non-nil pointer, dereferences to `""`). Without this, schema-required-but-empty placeholders are indistinguishable from omission, and an `action=supersede` proposal that legitimately omits `body`/`body_patch` cannot be rejected. **Check order matters**: presence checks (nil → "use … for none" hint) MUST run before the action-conditional checks, because empty-string body matches the action=create error and the missing-field case would otherwise be swallowed. It tolerates ```json fences and delegates severity-floor handling to the canonical `applySeverityFloor` helper (which floors both `unverifiable_codebase_claim` and `convention_deviation` to minor).
- `internal/verdict/extract_parser_test.go` covers: happy path (with `bm_commands: []`, `frontmatter_json: "{}"`, `body: ""`, `body_patch: ""` placeholders explicitly present), missing `next_action`, missing `findings`/`proposals`/`bm_commands` (each rejected with a clear error naming the field), invalid `action`, invalid `type`, `create` with empty `body` (rejected — per spec §3.2 `body_patch` is an update-only alternative), `update` with neither `body` nor `body_patch`, `supersede` with empty `supersedes`, per-proposal empty `permalink`/`title`/`rationale`/`evidence_refs`, **per-proposal missing `body` (field absent on the wire — rejected with `"" for none` hint, NOT silently treated as a placeholder)**, **per-proposal missing `body_patch` (same)**, **per-proposal missing `frontmatter_json` (rejected with `"{}"` hint)**, per-proposal `frontmatter_json: ""` (rejected — empty string not allowed; use `"{}"`), per-proposal `frontmatter_json: "null"` (rejected — JSON null unmarshals to nil map and would silently pass the type check), per-proposal `frontmatter_json: "[1,2,3]"` (rejected — JSON array, not object), per-proposal missing `supersedes` (rejected with `[]` hint), **empty `criterion` / `evidence` / `suggestion` on a finding (each rejected with the field name)**, fenced JSON, extra fields rejected, a `bm_commands` round-trip (reviewer emits a `write_note` command and the parser preserves it), an empty-`bm_commands` round-trip (`[]` accepted, `nil` rejected), AND a server-owned-field-spoof rejection: reviewer-emitted `summary_block` or `partial` is rejected with "unknown field" because the parser decodes into a private wire-only `extractWire` struct that omits those.
- `ParseExtract` decodes into the private `extractWire` struct in `extract.go` (Verdict / Findings / Proposals / BMCommands / NextAction only) and lifts those fields into the public `ExtractResult`, leaving `SummaryBlock` and `Partial` zero for handler-side population.
- `internal/verdict/schema_invariants_test.go::TestReviewerSchemas_RequireAllProperties_ForOpenAIStrictMode` includes `ExtractSchema()` in its schemas slice and the test still passes — confirming `extract_schema.json` cannot regress an optional property in future.
- `go test -race ./internal/verdict/...` passes.

**Non-goals:**
- Do not validate that `Supersedes` permalinks exist anywhere (no codebase / KB lookup; the reviewer is responsible).
- Do not enforce frontmatter shape beyond "map[string]any"; spec keeps schema layer caller-side.

**Context:**
Mirror Task 5's structure. The `body` XOR `body_patch` constraint lives in `ParseExtract`, NOT in the JSON schema — draft-07 expression of "exactly one of two optional fields is non-empty" is awkward, and OpenAI strict structured-outputs does not interpret `oneOf` against arbitrary boolean constraints reliably anyway. The schema treats both fields as required-but-can-be-empty placeholders; the parser does the action-conditional check (body required on create; body XOR body_patch on update; non-empty supersedes on supersede) and produces actionable error messages naming the field at fault. The schema-split paragraph in Section 2 of the Acceptance criteria above is the authoritative description; this Context line is the rationale.

**Files:**
- Create: `internal/verdict/extract.go`
- Create: `internal/verdict/extract_schema.json`
- Create: `internal/verdict/extract_parser.go`
- Create: `internal/verdict/extract_parser_test.go`
- Modify: `internal/verdict/schema_invariants_test.go` (add `ExtractSchema()` to the schemas slice; same rationale as Task 5)

- [ ] **Step 1: Types**

Create `internal/verdict/extract.go`:

```go
package verdict

import _ "embed"

type ProposalAction string

const (
	ProposalActionCreate    ProposalAction = "create"
	ProposalActionUpdate    ProposalAction = "update"
	ProposalActionSupersede ProposalAction = "supersede"
)

type ProposalType string

const (
	ProposalTypeDecision ProposalType = "decision"
	ProposalTypeModule   ProposalType = "module"
	ProposalTypeFeature  ProposalType = "feature"
	ProposalTypeGlossary ProposalType = "glossary"
	ProposalTypeEpic     ProposalType = "epic"
)

// Proposal is the canonical shape of one extract proposal. All fields use
// non-omitempty JSON tags because the reviewer-output schema requires every
// property to appear in the response (strict-mode invariant). The reviewer
// emits placeholders (`""` / `[]`) when a field is unused; the parser runs
// the action-conditional checks against those placeholders.
//
// FrontmatterJSON is a JSON-encoded string (NOT a nested object) — OpenAI
// strict structured-outputs rejects freeform object schemas, and note
// frontmatter has variable per-type keys (decision uses `decided_at`,
// `supersedes`, etc.; module uses `last_changed_in`, `relates_features`;
// epic uses `owners`, `plan_refs`) we cannot enumerate in the schema.
// Callers parse FrontmatterJSON via `json.Unmarshal` after receiving the
// envelope; see Task 10's handler for the parse-and-route path.
type Proposal struct {
	Action          ProposalAction `json:"action"`
	Type            ProposalType   `json:"type"`
	Permalink       string         `json:"permalink"`
	Title           string         `json:"title"`
	FrontmatterJSON string         `json:"frontmatter_json"`
	Body            string         `json:"body"`
	BodyPatch       string         `json:"body_patch"`
	Rationale       string         `json:"rationale"`
	EvidenceRefs    []string       `json:"evidence_refs"`
	Supersedes      []string       `json:"supersedes"`
}

// ExtractResult is the canonical shape returned by extract_project_knowledge.
// BMCommands is required-but-can-be-empty (OpenAI strict-mode schema
// invariant; see internal/verdict/schema_invariants_test.go).
//
// SummaryBlock and Partial are SERVER-OWNED — same posture as PrimeResult.
// ParseExtract decodes into the wire-only `extractWire` struct below to
// reject any reviewer-emitted server-owned field.
type ExtractResult struct {
	Verdict      Verdict     `json:"verdict"`
	Findings     []Finding   `json:"findings"`
	Proposals    []Proposal  `json:"proposals"`
	BMCommands   []BMCommand `json:"bm_commands"`
	NextAction   string      `json:"next_action"`
	SummaryBlock string      `json:"summary_block,omitempty"`
	Partial      bool        `json:"partial,omitempty"`
}

// extractWire is the reviewer-emitted shape — no server-owned fields, and
// per-proposal optional strings use *string so the parser can distinguish
// "field missing" from "field present-but-empty". The strict-mode schema
// requires `body`, `body_patch`, `frontmatter_json`, and `supersedes` to be
// present in every proposal (with empty placeholders when unused). Decoding
// `body`/`body_patch` directly into a plain string would lose that
// distinction — both missing and present-as-"" decode to "", and the parser
// could not reject an action=supersede proposal that omitted body/body_patch
// entirely (which the schema requires) versus one that legitimately emitted
// "" placeholders.
//
// ParseExtract decodes into extractWire with DisallowUnknownFields, then
// converts proposalWire entries into Proposal values after presence checks.
type extractWire struct {
	Verdict    Verdict         `json:"verdict"`
	Findings   []Finding       `json:"findings"`
	Proposals  []proposalWire  `json:"proposals"`
	BMCommands []BMCommand     `json:"bm_commands"`
	NextAction string          `json:"next_action"`
}

// proposalWire mirrors Proposal but with *string for the four
// required-but-can-be-empty placeholders, so the parser can detect a
// missing field (pointer == nil) versus a present-but-empty one
// (pointer != nil, *pointer == "").
type proposalWire struct {
	Action          ProposalAction `json:"action"`
	Type            ProposalType   `json:"type"`
	Permalink       string         `json:"permalink"`
	Title           string         `json:"title"`
	FrontmatterJSON *string        `json:"frontmatter_json"`
	Body            *string        `json:"body"`
	BodyPatch       *string        `json:"body_patch"`
	Rationale       string         `json:"rationale"`
	EvidenceRefs    []string       `json:"evidence_refs"`
	Supersedes      []string       `json:"supersedes"`
}

//go:embed extract_schema.json
var extractSchema []byte

func ExtractSchema() []byte {
	out := make([]byte, len(extractSchema))
	copy(out, extractSchema)
	return out
}
```

- [ ] **Step 2: Schema**

Create `internal/verdict/extract_schema.json`:

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "title": "ExtractResult",
  "type": "object",
  "required": ["verdict", "findings", "proposals", "bm_commands", "next_action"],
  "additionalProperties": false,
  "properties": {
    "verdict": { "type": "string", "enum": ["pass", "warn", "fail"] },
    "findings": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["severity", "category", "criterion", "evidence", "suggestion"],
        "additionalProperties": false,
        "properties": {
          "severity":   { "type": "string", "enum": ["critical", "major", "minor"] },
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
              "unverifiable_codebase_claim",
              "convention_deviation",
              "attestation_contradiction",
              "kb_gap",
              "ambiguous_pick",
              "missing_index_entry",
              "insufficient_evidence",
              "redundant_proposal",
              "contradicts_existing",
              "other"
            ]
          },
          "criterion":  { "type": "string", "minLength": 1 },
          "evidence":   { "type": "string", "minLength": 1 },
          "suggestion": { "type": "string", "minLength": 1 }
        }
      }
    },
    "proposals": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["action", "type", "permalink", "title", "frontmatter_json", "body", "body_patch", "rationale", "evidence_refs", "supersedes"],
        "additionalProperties": false,
        "properties": {
          "action":           { "type": "string", "enum": ["create", "update", "supersede"] },
          "type":             { "type": "string", "enum": ["decision", "module", "feature", "glossary", "epic"] },
          "permalink":        { "type": "string", "minLength": 1 },
          "title":            { "type": "string", "minLength": 1 },
          "frontmatter_json": { "type": "string" },
          "body":             { "type": "string" },
          "body_patch":       { "type": "string" },
          "rationale":        { "type": "string", "minLength": 1 },
          "evidence_refs":    { "type": "array", "items": { "type": "string", "minLength": 1 } },
          "supersedes":       { "type": "array", "items": { "type": "string", "minLength": 1 } }
        }
      }
    },
    "bm_commands": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["tool", "args_json"],
        "additionalProperties": false,
        "properties": {
          "tool":      { "type": "string", "minLength": 1 },
          "args_json": { "type": "string" }
        }
      }
    },
    "next_action": { "type": "string", "minLength": 1 }
  }
}
```

The category enum mirrors `internal/verdict/schema.json` (the full shared vocabulary) so reviewers can emit any valid category on the extract surface.

`bm_commands` is enumerated explicitly under top-level `properties` AND listed in the top-level `required` array because the v0.5.1 strict-schema invariant (`internal/verdict/schema_invariants_test.go`) and OpenAI structured-outputs both reject any optional top-level property. The reviewer emits `bm_commands: []` when `KBStoreIsBasicMemory: false`. Server-side stripping when `ANTI_TANGENT_KB_STORE` is empty happens in the handler (Task 10) — but the reviewer must still produce an empty array, not omit the field.

Per-proposal optional fields (`frontmatter_json`, `body`, `body_patch`, `supersedes`) are likewise listed in the proposal-item `required` array. The reviewer emits placeholders (`""` for absent strings — `body`, `body_patch`; `"{}"` (a string containing two characters) for absent `frontmatter_json`, NOT a JSON object literal `{}`; `[]` for absent `supersedes`) and the parser performs the existing action-conditional checks (body required on create, body XOR body_patch on update, non-empty supersedes on supersede) against those placeholders.

Body XOR body_patch is enforced in the parser rather than the JSON schema, since draft-07 expression of "exactly one of two optional fields is non-empty" is awkward.

- [ ] **Step 3: Parser**

Create `internal/verdict/extract_parser.go`:

```go
package verdict

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// ParseExtract decodes provider output into an ExtractResult, validating
// enums and rejecting extra/missing fields. Decodes into the wire-only
// `extractWire` struct so reviewer-emitted `summary_block` / `partial` are
// rejected as unknown — server-owned fields land on ExtractResult only via
// handler-side population.
func ParseExtract(raw []byte) (ExtractResult, error) {
	body := stripFences(bytes.TrimSpace(raw))
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	var w extractWire
	if err := dec.Decode(&w); err != nil {
		return ExtractResult{}, fmt.Errorf("decode extract result: %w", err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return ExtractResult{}, fmt.Errorf("decode extract result: extra JSON after document")
	}
	// Proposals is filled in after the per-proposal validation loop lifts
	// each proposalWire into a Proposal; leave it nil here so the existing
	// nil-rejection check below still distinguishes "field missing" from
	// "empty list" on the wire.
	r := ExtractResult{
		Verdict:    w.Verdict,
		Findings:   w.Findings,
		BMCommands: w.BMCommands,
		NextAction: w.NextAction,
		// SummaryBlock and Partial deliberately left zero — handler populates.
	}
	switch r.Verdict {
	case VerdictPass, VerdictWarn, VerdictFail:
	default:
		return ExtractResult{}, fmt.Errorf("invalid verdict %q", r.Verdict)
	}
	if r.NextAction == "" {
		return ExtractResult{}, fmt.Errorf("decode extract result: next_action is required")
	}
	if r.Findings == nil {
		return ExtractResult{}, fmt.Errorf("decode extract result: findings is required (use [] for none)")
	}
	if w.Proposals == nil {
		return ExtractResult{}, fmt.Errorf("decode extract result: proposals is required (use [] for none)")
	}
	if r.BMCommands == nil {
		return ExtractResult{}, fmt.Errorf("decode extract result: bm_commands is required (use [] for none)")
	}
	for i := range r.Findings {
		f := r.Findings[i]
		switch f.Severity {
		case SeverityCritical, SeverityMajor, SeverityMinor:
		default:
			return ExtractResult{}, fmt.Errorf("finding[%d]: invalid severity %q", i, f.Severity)
		}
		if !validCategory(f.Category) {
			return ExtractResult{}, fmt.Errorf("finding[%d]: invalid category %q", i, f.Category)
		}
		// Delegate the criterion/evidence/suggestion non-empty checks to the
		// shared validateFindingStrings helper added in Task 1 step 5a.
		if err := validateFindingStrings(f, fmt.Sprintf("finding[%d]", i)); err != nil {
			return ExtractResult{}, err
		}
		// Delegate to the canonical severity-floor helper (see Task 5 §3 for
		// the same call in ParsePrime). applySeverityFloor floors BOTH
		// unverifiable_codebase_claim AND convention_deviation to minor.
		r.Findings[i] = applySeverityFloor(r.Findings[i])
	}
	proposals := make([]Proposal, 0, len(w.Proposals))
	for i, p := range w.Proposals {
		switch p.Action {
		case ProposalActionCreate, ProposalActionUpdate, ProposalActionSupersede:
		default:
			return ExtractResult{}, fmt.Errorf("proposal[%d]: invalid action %q", i, p.Action)
		}
		switch p.Type {
		case ProposalTypeDecision, ProposalTypeModule, ProposalTypeFeature, ProposalTypeGlossary, ProposalTypeEpic:
		default:
			return ExtractResult{}, fmt.Errorf("proposal[%d]: invalid type %q", i, p.Type)
		}
		// PRESENCE CHECKS first — schema-required fields must be present
		// (the reviewer emits placeholders when empty). Run these before
		// action-conditional checks so a missing field gets the actionable
		// "use [] / use \"{}\" for none" hint instead of being swallowed by
		// a generic action=supersede check (len(nil) == 0, so the action
		// check below would otherwise match a literally-missing supersedes).
		// The *string-typed wire fields let us distinguish missing (== nil)
		// from present-but-empty (!= nil, deref == ""); the strict-mode
		// schema requires all four to be present, so missing is a hard fail.
		if p.Permalink == "" {
			return ExtractResult{}, fmt.Errorf("proposal[%d]: permalink is required", i)
		}
		if p.Title == "" {
			return ExtractResult{}, fmt.Errorf("proposal[%d]: title is required", i)
		}
		if p.Rationale == "" {
			return ExtractResult{}, fmt.Errorf("proposal[%d]: rationale is required", i)
		}
		if len(p.EvidenceRefs) == 0 {
			return ExtractResult{}, fmt.Errorf("proposal[%d]: evidence_refs is required (must be non-empty)", i)
		}
		// frontmatter_json / body / body_patch must be PRESENT on the wire
		// (non-nil pointer). Empty-string value is acceptable as a placeholder.
		if p.FrontmatterJSON == nil {
			return ExtractResult{}, fmt.Errorf("proposal[%d]: frontmatter_json is required (use \"{}\" for none)", i)
		}
		if p.Body == nil {
			return ExtractResult{}, fmt.Errorf("proposal[%d]: body is required (use \"\" for none)", i)
		}
		if p.BodyPatch == nil {
			return ExtractResult{}, fmt.Errorf("proposal[%d]: body_patch is required (use \"\" for none)", i)
		}
		// Then validate frontmatter_json content shape ("{}" string is the
		// minimal valid placeholder; reviewer must not emit "null" or non-object).
		if *p.FrontmatterJSON == "" {
			return ExtractResult{}, fmt.Errorf("proposal[%d]: frontmatter_json must not be empty string (use \"{}\" for none)", i)
		}
		var fmProbe map[string]any
		if err := json.Unmarshal([]byte(*p.FrontmatterJSON), &fmProbe); err != nil {
			return ExtractResult{}, fmt.Errorf("proposal[%d]: frontmatter_json is not a JSON object: %w", i, err)
		}
		if fmProbe == nil {
			return ExtractResult{}, fmt.Errorf("proposal[%d]: frontmatter_json must be a JSON object literal (use \"{}\" for none, not \"null\")", i)
		}
		if p.Supersedes == nil {
			return ExtractResult{}, fmt.Errorf("proposal[%d]: supersedes is required (use [] for none)", i)
		}
		// ACTION-CONDITIONAL checks run last. By this point all four wire
		// pointers are non-nil and Supersedes is non-nil; the *string deref
		// is safe. body/body_patch can both be empty strings (acceptable
		// for action=supersede).
		body := *p.Body
		bodyPatch := *p.BodyPatch
		if p.Action == ProposalActionCreate && body == "" {
			return ExtractResult{}, fmt.Errorf("proposal[%d]: action=create requires non-empty body", i)
		}
		// NOTE: the following two checks compare the local `body` and
		// `bodyPatch` strings (set above to `*p.Body` and `*p.BodyPatch`),
		// NOT the *string fields on p directly. `p.Body != ""` would be a
		// type error (cannot compare *string to ""); we deref once above
		// and then operate on the values.
		if p.Action == ProposalActionUpdate && body == "" && bodyPatch == "" {
			return ExtractResult{}, fmt.Errorf("proposal[%d]: action=update requires body or body_patch", i)
		}
		if body != "" && bodyPatch != "" {
			return ExtractResult{}, fmt.Errorf("proposal[%d]: body and body_patch are mutually exclusive", i)
		}
		if p.Action == ProposalActionSupersede && len(p.Supersedes) == 0 {
			return ExtractResult{}, fmt.Errorf("proposal[%d]: action=supersede requires non-empty supersedes (use a permalink list, not [])", i)
		}
		// Lift the validated wire entry into a Proposal.
		proposals = append(proposals, Proposal{
			Action:          p.Action,
			Type:            p.Type,
			Permalink:       p.Permalink,
			Title:           p.Title,
			FrontmatterJSON: *p.FrontmatterJSON,
			Body:            body,
			BodyPatch:       bodyPatch,
			Rationale:       p.Rationale,
			EvidenceRefs:    p.EvidenceRefs,
			Supersedes:      p.Supersedes,
		})
	}
	r.Proposals = proposals
	for i, c := range r.BMCommands {
		if c.Tool == "" {
			return ExtractResult{}, fmt.Errorf("bm_commands[%d]: tool is required", i)
		}
		// args_json must be a JSON object literal. Empty-object `{}` is
		// acceptable; anything else (array, scalar, `null`, malformed) is
		// rejected. The nil-map check below catches `null`, which would
		// otherwise unmarshal successfully into a nil map[string]any.
		if c.ArgsJSON == "" {
			return ExtractResult{}, fmt.Errorf("bm_commands[%d]: args_json is required (use \"{}\" for none)", i)
		}
		var probe map[string]any
		if err := json.Unmarshal([]byte(c.ArgsJSON), &probe); err != nil {
			return ExtractResult{}, fmt.Errorf("bm_commands[%d]: args_json is not a JSON object: %w", i, err)
		}
		if probe == nil {
			return ExtractResult{}, fmt.Errorf("bm_commands[%d]: args_json must be a JSON object literal (use \"{}\" for none, not \"null\")", i)
		}
	}
	return r, nil
}
```

- [ ] **Step 4: Tests**

Create `internal/verdict/extract_parser_test.go` covering the cases enumerated in the AC. Pattern mirrors `prime_parser_test.go` from Task 5. Include a `bm_commands` round-trip test analogous to `TestParsePrime_BMCommandsRoundTrip`:

```go
func TestParseExtract_BMCommandsRoundTrip(t *testing.T) {
	raw := []byte(`{
		"verdict": "pass",
		"findings": [],
		"proposals": [{"action":"create","type":"decision","permalink":"decisions/0099-x","title":"t","frontmatter_json":"{}","body":"b","body_patch":"","rationale":"r","evidence_refs":["completion[0].finding[0]"],"supersedes":[]}],
		"bm_commands": [{"tool": "write_note", "args_json": "{\"permalink\":\"decisions/0099-x\"}"}],
		"next_action": "go"
	}`)
	r, err := verdict.ParseExtract(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.BMCommands) != 1 || r.BMCommands[0].Tool != "write_note" {
		t.Fatalf("BMCommands not preserved: %+v", r.BMCommands)
	}
}

func TestParseExtract_RejectsMissingBMCommands(t *testing.T) {
	// bm_commands is required (OpenAI strict-mode invariant).
	raw := []byte(`{
		"verdict": "pass",
		"findings": [],
		"proposals": [],
		"next_action": "go"
	}`)
	if _, err := verdict.ParseExtract(raw); err == nil || !strings.Contains(err.Error(), "bm_commands is required") {
		t.Fatalf("want bm_commands-required error, got %v", err)
	}
}
```

- [ ] **Step 5: Extend the schema-invariant `schemas` slice**

```go
{"extract_schema.json", ExtractSchema()},
```

Run `go test ./internal/verdict/...` to confirm `extract_schema.json` passes all four strict-mode invariants (defined in Task 1). From this point onward the invariants cover all SIX reviewer-output schemas in lock-step.

- [ ] **Step 6: Run tests**

Run: `go test -race ./internal/verdict/...`

Expected: PASS.

- [ ] **Step 7: Commit task 8**

```bash
git add internal/verdict/extract*.go internal/verdict/extract_schema.json internal/verdict/schema_invariants_test.go
git commit -m "$(cat <<'EOF'
feat(verdict): add ExtractResult types, schema, and parser

ExtractResult / Proposal types with ProposalAction and ProposalType
constants. JSON schema enforces required fields and enums.
ParseExtract additionally enforces body XOR body_patch and
non-empty supersedes when action=supersede.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 9: Add the extract prompt template + golden test

**Goal:** `internal/prompts/RenderExtract` produces a reviewer prompt that takes one or more completion envelopes plus optional KB context and instructs the reviewer to emit `Proposal`s for create/update/supersede plus `bm_commands` when configured.

**Acceptance criteria:**
- `internal/prompts/prompts.go` declares `ExtractInput`, `CompletionEnvelopeForExtract`, and `RenderExtract(ExtractInput) (Output, error)`.
- `ExtractInput` carries: `CompletionEnvelopes []CompletionEnvelopeForExtract`, `PlanText string`, `KBIndex []KBIndexEntry`, `CurrentKBExcerpts map[string]string`, `EpicPermalink string`, `KBStoreIsBasicMemory bool`.
- `CompletionEnvelopeForExtract` carries: `TaskTitle`, `Summary`, `Verdict`, `Findings []verdict.Finding`, `FinalDiff string`, `FinalFiles []File`, `TestEvidence string` so the reviewer can read whatever the caller submitted.
- `extract.tmpl` includes sections: completion envelopes (with verdict, summary, findings, diff/files), plan text (when present), KB index, current KB excerpts (when present), epic permalink (when present), instructions, paste-ready output schema. Reviewer is instructed:
  - emit `Proposal`s strictly grounded in evidence_refs that point at fields visible in the envelopes or `plan_text`,
  - emit `insufficient_evidence` (critical) when envelopes are empty,
  - emit `redundant_proposal` when a proposal duplicates an existing note in the index,
  - emit `contradicts_existing` (major or critical) when a proposal contradicts an existing note's `decided` content,
  - when `EpicPermalink` is set, append a progress-ledger entry to that epic and set `epic_origin: <EpicPermalink>` on any newly proposed decision,
  - always emit `bm_commands` (the response schema requires it). Each entry is shaped `{"tool": "<verb>", "args_json": "<JSON-encoded object string>"}` — `args_json` is a string, not a nested object, because OpenAI strict structured-outputs rejects freeform object schemas. Use `"{}"` for an empty arg set. When `KBStoreIsBasicMemory: true`, emit one or more entries per the **verified Task 0a contract** at the bottom of this plan: `write_note` for create, `edit_note` for update, and for supersede emit the **two-step mapping** (one `write_note` for the new note + one `edit_note` to flip the predecessor's `status` frontmatter). Do not emit a `supersede_note` verb — BM does not expose one. When `KBStoreIsBasicMemory: false`, emit `bm_commands: []`. Do not omit the field.
  - always emit `frontmatter_json`, `body`, `body_patch`, and `supersedes` on every Proposal (use `"{}"`, `""`, `""`, `[]` placeholders when unused). `frontmatter_json` is a JSON-encoded string of the note's frontmatter object (NOT a nested object) — OpenAI strict structured-outputs rejects freeform object schemas. Use a literal `"{}"` string when no frontmatter change is intended; use a JSON-object string like `"{\"status\":\"superseded\"}"` for partial updates. The response schema requires all four placeholders to be present — the parser runs the action-conditional checks (body required on create, body XOR body_patch on update, non-empty supersedes on supersede) against those placeholders.
- New golden file `extract_basic.golden` covers a representative input.
- `go test -race ./internal/prompts/...` passes.

**Non-goals:**
- Do not register the tool (Task 10).
- Do not validate evidence refs server-side.

**Context:**
The prompt is the largest template in the codebase. Keep it tight; lean on the output schema block to anchor the JSON shape. INTEGRATION.md documents the same BM tool names already pinned in the Task 0a verified block at the bottom of this plan — reference them verbatim (`write_note` for create, `edit_note` for update, the two-step `write_note + edit_note` mapping for supersede). The template MUST NOT emit a `supersede_note` verb; if BM ever ships one, Task 0a's verified block updates first, then this template.

**Files:**
- Modify: `internal/prompts/prompts.go`
- Create: `internal/prompts/templates/extract.tmpl`
- Modify: `internal/prompts/prompts_test.go`
- Create: `internal/prompts/testdata/extract_basic.golden`

- [ ] **Step 1: Declare types**

In `internal/prompts/prompts.go`:

```go
type CompletionEnvelopeForExtract struct {
	TaskTitle    string
	Summary      string
	Verdict      string
	Findings     []verdict.Finding
	FinalDiff    string
	FinalFiles   []File
	TestEvidence string
}

type ExtractInput struct {
	CompletionEnvelopes  []CompletionEnvelopeForExtract
	PlanText             string
	KBIndex              []KBIndexEntry
	CurrentKBExcerpts    map[string]string
	EpicPermalink        string
	KBStoreIsBasicMemory bool
}

func RenderExtract(in ExtractInput) (Output, error) {
	body, err := render("extract.tmpl", in)
	if err != nil {
		return Output{}, err
	}
	return Output{System: systemPrompt, User: body}, nil
}
```

- [ ] **Step 2: Write `extract.tmpl`**

Create `internal/prompts/templates/extract.tmpl`. Follow the structure outlined in the AC. Include the output schema block at the end with `bm_commands` conditionally rendered. Mirror the writing style of `prime.tmpl` from Task 6.

- [ ] **Step 3: Golden test**

Add a `TestRenderExtract_Basic` test in `internal/prompts/prompts_test.go` that exercises one completion envelope (with a small `FinalDiff`), a two-entry `KBIndex`, an `EpicPermalink`, and `KBStoreIsBasicMemory: true`. Use the in-package `golden(t, "extract_basic", out.System+"\n---USER---\n"+out.User)` helper — same style as Task 6's `TestRenderPrime_Basic`. Materialize the golden with `-update` and verify the file by eye.

- [ ] **Step 4: Run tests**

Run: `go test -race ./internal/prompts/...`

Expected: PASS.

- [ ] **Step 5: Commit task 9**

```bash
git add internal/prompts/
git commit -m "$(cat <<'EOF'
feat(prompts): add extract_project_knowledge prompt template

Declares CompletionEnvelopeForExtract / ExtractInput / RenderExtract
and an extract.tmpl that instructs the reviewer to emit Proposals
(create/update/supersede). bm_commands is always emitted (required
by extract_schema.json strict-mode invariant): one or more entries
per the verified BM contract when KBStoreIsBasicMemory:true, [] when
false.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 10: Implement the `extract_project_knowledge` handler

**Goal:** A new MCP tool `extract_project_knowledge` is registered. End-to-end, callers pass one or more completion envelopes plus optional KB context and receive an `ExtractResult` envelope.

**Acceptance criteria:**
- `internal/mcpsrv/extract_handler.go` declares `ExtractProjectKnowledgeArgs`:
  - `completion_envelopes` (required, ≥1), each carrying the fields from `CompletionEnvelopeForExtract` plus `Verdict string` and `Findings []verdict.Finding`,
  - `plan_text` (optional),
  - `kb_index` (optional, reuse `KBIndexEntryArg`),
  - `current_kb_excerpts` (optional, `map[string]string`),
  - `epic_permalink` (optional),
  - `model_override`, `max_tokens_override`.
- The handler:
  1. Rejects empty `completion_envelopes` with a synthetic `insufficient_evidence` (critical) result envelope.
  2. Resolves `effectiveMaxTokens` for clamp (mirroring Task 7 step 4) BEFORE the payload check. Computes payload size = serialized JSON of all args. If > `MaxPayloadBytes`, returns `extractEnvelopeResult(prependExtractClamp(extractTooLargeResult(size, cap), clamp), h.deps.Cfg.ExtractModel.String(), 0)`. The `extractTooLargeResult` helper uses `verdict.SeverityMajor` for the synthetic finding, matching `tooLargeEnvelope` / `tooLargePlanResult` / `primeTooLargeResult`.
  3. **Per-envelope evidence accounting (presence AND shape).** An envelope is "evidence-bare" when ALL THREE of `FinalDiff`, `FinalFiles`, and `TestEvidence` are empty — matching the existing per-task at-least-one-evidence rule in `handlers.go:868-872`. `TestEvidence` is real evidence for extract: a passing-test summary is enough for the reviewer to ground a "tests for feature X added" decision proposal even when no code diff is supplied (test-first development scenarios). In addition to the presence check, run a shape check by reusing the existing `checkEvidenceShape` helper (`internal/mcpsrv/handlers.go:711`) per envelope: build a synthetic `ValidateCompletionArgs{FinalDiff: e.FinalDiff, FinalFiles: e.FinalFiles}` and call `checkEvidenceShape` on it; a non-empty return string means the envelope's evidence is malformed (truncation markers, empty paths, placeholder `...` lines) and the envelope is treated as evidence-bare for accounting purposes (with the malformed-shape reason cited in the `insufficient_evidence` finding's `evidence` field). This catches the case where a caller passes `final_files` whose contents are `// ... truncated for brevity` and the reviewer would otherwise propose decisions grounded in nothing. Walk `args.CompletionEnvelopes` and, for every evidence-bare envelope, accumulate one `insufficient_evidence` (major) finding into a `preFindings []verdict.Finding` slice keyed by envelope index. Then branch on the accumulated count: if EVERY envelope was evidence-bare, synthesize an `ExtractResult{Verdict: fail, Findings: preFindings, Proposals: []verdict.Proposal{}, BMCommands: []verdict.BMCommand{}, NextAction: "…"}` and skip the reviewer call entirely. The two empty-slice initializers are LOAD-BEARING: per the strict-schema invariant the wire-format schema requires `proposals` and `bm_commands` arrays to be present, and a nil slice marshals as JSON `null`, breaking the wire contract on round-trip. The same shape applies to the empty-`completion_envelopes` synthetic and to every other synthetic ExtractResult path (`extractTooLargeResult`, the truncation envelope, etc.) — wherever the plan says "synthesize an ExtractResult", the three array fields MUST be initialized to non-nil empty slices when empty. Add a unit-test assertion that round-trips a synthetic refusal through `json.Marshal` and checks the resulting JSON contains `"proposals":[]` and `"bm_commands":[]` (NOT `"proposals":null`). If AT LEAST ONE envelope has evidence, dispatch the reviewer with the full envelope list (evidence-bare envelopes still get sent so the reviewer can correlate across them), and after parsing prepend `preFindings` to `result.Findings` so the major findings survive into the response.
  4. Resolves model via `args.ModelOverride` → `cfg.ExtractModel`.
  5. Computes output max tokens per spec §5.3 (`adaptiveExtractMaxTokens`).
  6. Renders the prompt via `prompts.RenderExtract` with `KBStoreIsBasicMemory: cfg.KBStore == "basic-memory"`.
  7. Calls the reviewer with `JSONSchema: verdict.ExtractSchema()`.
  8. Parses via `verdict.ParseExtract` with one parse-retry.
  9. When `cfg.KBStore == ""`, sets `result.BMCommands = []verdict.BMCommand{}` (empty slice, NOT nil) defensively, mirroring the prime handler. Never use `nil` — `BMCommand` arrays are required by the schema and a nil slice marshals as `null`.
  10. When `cfg.KBStore == "basic-memory"`, runs the same `kb_store_mismatch` heuristic as the prime handler (Task 7) against **every `Proposal.Permalink` AND every parsed `BMCommand.ArgsJSON.permalink` string value**. Parse `ArgsJSON` with `json.Unmarshal([]byte(c.ArgsJSON), &args)` then probe `args["permalink"].(string)`; anything that starts with `/` or contains `://` produces one minor `other / kb_store_mismatch` finding per offender. The reviewer's text-only check cannot guarantee proposal permalinks look BM-shaped, so the server enforces it. If `ArgsJSON` does not parse or has no `permalink` field, skip silently — the parser already rejected malformed `args_json` upstream.
  11. Populates `SummaryBlock` via `formatExtractSummary` (new helper in `summary.go`).
  12. Logs one structured JSON line on stderr matching spec §5.6.
- Tool registered in `internal/mcpsrv/server.go`.
- Unit tests in `internal/mcpsrv/extract_handler_test.go` cover: happy path, empty `completion_envelopes` (synthetic insufficient_evidence), oversized payload, all envelopes evidence-bare (i.e. all three of `FinalDiff` / `FinalFiles` / `TestEvidence` empty — synthetic refusal), an envelope with ONLY `TestEvidence` populated (NOT treated as evidence-bare — passes through to the reviewer), `bm_commands` stripping with `KBStore=""`, model override.
- Integration test in `internal/mcpsrv/integration_test.go` exercises the tool end-to-end with a canned reviewer JSON.
- `go test -race ./internal/mcpsrv/...` passes.

**Non-goals:**
- Do not validate proposals against the index (no permalink lookup); the reviewer is responsible.
- Do not auto-apply proposals or write any files.

**Context:**
Pattern is the prime handler's twin. This task owns `adaptiveExtractMaxTokens` (intentionally not in Task 7) and the parallel `prependExtractClamp(r verdict.ExtractResult, clamp verdict.Finding) verdict.ExtractResult` helper so each cluster's commits stay scoped to its own helper; sequential execution of Task 7 → Task 10 still applies (see File Structure). The clamp helper signature MUST match `prependPrimeClamp` and the existing `prependClamp` / `prependPlanClamp` — accept a `verdict.Finding` and insert it at the head of `r.Findings` when `clamp.Severity != ""`. Do NOT touch `next_action`.

**Files:**
- Create: `internal/mcpsrv/extract_handler.go`
- Create: `internal/mcpsrv/extract_handler_test.go`
- Modify: `internal/mcpsrv/server.go`
- Modify: `internal/mcpsrv/summary.go`
- Modify: `internal/mcpsrv/plan_budget.go`
- Modify: `internal/mcpsrv/integration_test.go`

- [ ] **Step 1: Add `adaptiveExtractMaxTokens` helper**

Append to `internal/mcpsrv/plan_budget.go`:

```go
// adaptiveExtractMaxTokens implements spec §5.3 sizing for extract_project_knowledge.
func adaptiveExtractMaxTokens(cfg config.Config, envelopeCount int) int {
	scaled := 2000 + 1200*envelopeCount
	if scaled > cfg.MaxTokensCeiling {
		scaled = cfg.MaxTokensCeiling
	}
	if scaled < cfg.ExtractMaxTokens {
		return cfg.ExtractMaxTokens
	}
	return scaled
}
```

If Task 7 has already landed, this is an append-only change (no conflict with `adaptivePrimeMaxTokens`).

- [ ] **Step 2: Handler skeleton**

Create `internal/mcpsrv/extract_handler.go` mirroring `prime_handler.go`. Key:

```go
type CompletionEnvelopeArg struct {
	TaskTitle    string             `json:"task_title,omitempty"`
	Summary      string             `json:"summary"`
	Verdict      string             `json:"verdict"`
	Findings     []verdict.Finding  `json:"findings,omitempty"`
	FinalDiff    string             `json:"final_diff,omitempty"`
	FinalFiles   []FileArg          `json:"final_files,omitempty"`
	TestEvidence string             `json:"test_evidence,omitempty"`
}

// toPromptCompletionEnvelopes converts handler-side CompletionEnvelopeArg
// values (with []FileArg) into the prompt-side CompletionEnvelopeForExtract
// shape (with []prompts.File), reusing the existing toPromptFiles helper at
// internal/mcpsrv/handlers.go:358 so file conversion stays in one place.
func toPromptCompletionEnvelopes(in []CompletionEnvelopeArg) []prompts.CompletionEnvelopeForExtract {
	out := make([]prompts.CompletionEnvelopeForExtract, len(in))
	for i, e := range in {
		out[i] = prompts.CompletionEnvelopeForExtract{
			TaskTitle:    e.TaskTitle,
			Summary:      e.Summary,
			Verdict:      e.Verdict,
			Findings:     e.Findings,
			FinalDiff:    e.FinalDiff,
			FinalFiles:   toPromptFiles(e.FinalFiles),
			TestEvidence: e.TestEvidence,
		}
	}
	return out
}

type ExtractProjectKnowledgeArgs struct {
	CompletionEnvelopes []CompletionEnvelopeArg `json:"completion_envelopes" jsonschema:"required"`
	PlanText            string                  `json:"plan_text,omitempty"`
	KBIndex             []KBIndexEntryArg       `json:"kb_index,omitempty"`
	CurrentKBExcerpts   map[string]string       `json:"current_kb_excerpts,omitempty"`
	EpicPermalink       string                  `json:"epic_permalink,omitempty"`
	ModelOverride       string                  `json:"model_override,omitempty"`
	MaxTokensOverride   int                     `json:"max_tokens_override,omitempty"`
}

func extractProjectKnowledgeTool() *mcp.Tool {
	return &mcp.Tool{
		Name: "extract_project_knowledge",
		Description: "Given one or more validate_completion envelopes (plus optional plan text and current KB context), return structured create/update/supersede proposals for the team's project knowledge base. Stateless; no session created. Emits paste-ready bm_commands when ANTI_TANGENT_KB_STORE=basic-memory.",
	}
}
```

The handler body, mirroring `PrimeProjectKnowledge` and the per-task `ValidateCompletion` ordering at `handlers.go:862-887` exactly. **Validation order matters**: evidence accounting and the all-evidence-bare refusal happen BEFORE model resolution, so a caller that combines a misspelled `model_override` with an all-evidence-bare envelope list still gets the actionable insufficient_evidence response (not the less-actionable model-validation error).

1. Validate `len(args.CompletionEnvelopes) > 0`; else synthesize an `insufficient_evidence` ExtractResult (uses `cfg.ExtractModel.String()` for `model_used`).
2. Resolve `effectiveMaxTokens` (yielding the `verdict.Finding` clamp). This MUST run BEFORE the payload-cap check so the synthetic too-large envelope can carry the clamp finding via `prependExtractClamp`.
3. Compute payload size; if > cap, return `extractEnvelopeResult(prependExtractClamp(extractTooLargeResult(size, cap), clamp), h.deps.Cfg.ExtractModel.String(), 0)`. Use the configured-default `ExtractModel`, NOT a resolved override (the model isn't resolved yet — same shape as the per-task path).
4. **Per-envelope evidence accounting (BEFORE model resolution).** An envelope is "evidence-bare" when NO valid evidence remains. Algorithm: (a) check the three evidence fields; if all empty → evidence-bare; (b) otherwise run `checkEvidenceShape` (handlers.go:711) against `ValidateCompletionArgs{FinalDiff: e.FinalDiff, FinalFiles: e.FinalFiles}`; if it returns a malformed-shape reason, the diff/files are unusable but `TestEvidence` may still be valid evidence in its own right (a passing-test summary is enough for the reviewer to ground "tests for feature X added" decisions even without code). Treat the envelope as evidence-bare only when `e.TestEvidence == ""` AND `checkEvidenceShape` fired; otherwise count it as having evidence and cite the malformed-shape reason as a `quality` (not `insufficient_evidence`) sub-finding so the reviewer sees the diff issue without losing the test-evidence grounding. Accumulate `preFindings` accordingly. If EVERY envelope is evidence-bare, synthesize the refusal envelope NOW with `h.deps.Cfg.ExtractModel.String()` as `model_used` and return — do NOT resolve `args.ModelOverride` first, so a misspelled override does not mask the insufficient_evidence response. Required unit tests: (i) bad model_override + all envelopes evidence-bare → insufficient_evidence, not model-validation error; (ii) truncation marker in final_diff with empty test_evidence → counted evidence-bare and flagged with the marker text; (iii) truncation marker in final_diff with NON-EMPTY test_evidence → envelope counts as having evidence, reviewer is dispatched, and a `quality` sub-finding cites the malformed diff.
5. Resolve model via `args.ModelOverride` → `cfg.ExtractModel`. `cfg.ExtractModel` is guaranteed non-zero by `config.Load`'s fallback chain (Task 2: `ANTI_TANGENT_EXTRACT_MODEL` → `ANTI_TANGENT_PLAN_MODEL` → `ANTI_TANGENT_PRE_MODEL`); no extra nil-check is needed. A misspelled override fails here, but only after a clean evidence-accounting / payload-too-large rejection has already happened.
6. If `MaxTokensOverride == 0`, replace `maxTokens` with `adaptiveExtractMaxTokens(h.deps.Cfg, len(args.CompletionEnvelopes))`.
7. Render prompt via `prompts.RenderExtract` with `KBStoreIsBasicMemory: cfg.KBStore == "basic-memory"`.
8. Call reviewer with `JSONSchema: verdict.ExtractSchema()`; parse via `verdict.ParseExtract` with one parse-retry.
8a. **Merge `preFindings` from step 4 into the parsed result.** Explicit line of code:
    ```go
    result.Findings = append(append([]verdict.Finding{}, preFindings...), result.Findings...)
    ```
    This is LOAD-BEARING: the mixed-envelope case (some envelopes have evidence, others are evidence-bare) accumulated `insufficient_evidence` findings in step 4 but bypassed the all-evidence-bare refusal short-circuit. Without an explicit merge here, those pre-findings would be dropped and the mixed-envelope test would silently regress. Order: pre-findings come first so the caller sees them above the reviewer-emitted findings. The new slice header avoids aliasing `preFindings` into the returned envelope.
9. **Post-process bm_commands.** This handler does NOT synthesize `bm_commands` — they are emitted by the reviewer per `extract.tmpl`'s instructions (Task 9). The handler's role is post-processing only: if `cfg.KBStore == ""`, replace `result.BMCommands` with an empty slice `[]verdict.BMCommand{}` (defensive strip); if `cfg.KBStore == "basic-memory"`, run the `kb_store_mismatch` heuristic against every `Proposal.Permalink` and every permalink-string parsed out of each `BMCommand.ArgsJSON`, emitting one minor finding per offender.
10. **Truncation parity:** `reviewExtract` returns `partialRaw []byte` and routes `providers.ErrResponseTruncated` exactly like `reviewPrime` (Task 7). On a truncation with no recovered findings (the only case 0.6.0 supports), the handler synthesizes an `ExtractResult{Verdict: warn, Findings: [{Severity: minor, Category: other, Criterion: "reviewer_response", Evidence: providers.ErrResponseTruncated.Error(), Suggestion: "Raise ANTI_TANGENT_EXTRACT_MAX_TOKENS …"}], Proposals: [], BMCommands: []verdict.BMCommand{}}` and returns it via `extractEnvelopeResult(prependExtractClamp(synth, clamp), …)`. This matches the per-task no-analysis truncation pattern in `internal/mcpsrv/handlers.go:390-403` — severity Minor, `Partial` field NOT set (Partial: true is reserved for the partial-findings-recovered path, which this task does not implement).
11. Populate `SummaryBlock`, log, return.

- [ ] **Step 3: Register the tool**

In `internal/mcpsrv/server.go::New`:

```go
mcp.AddTool(srv, extractProjectKnowledgeTool(), h.ExtractProjectKnowledge)
```

- [ ] **Step 4: `formatExtractSummary` helper**

In `internal/mcpsrv/summary.go`:

```go
func formatExtractSummary(r verdict.ExtractResult, modelUsed string, reviewMS int64) string {
	var b strings.Builder
	b.WriteString("anti-tangent envelope (extract_project_knowledge)\n")
	fmt.Fprintf(&b, "  verdict:       %s\n", r.Verdict)
	if r.Partial {
		b.WriteString("  partial:       true\n")
	}
	fmt.Fprintf(&b, "  model_used:    %s\n", modelUsed)
	fmt.Fprintf(&b, "  review_ms:     %d\n", reviewMS)
	writeFindingsSummary(&b, r.Findings, "  ")
	fmt.Fprintf(&b, "  proposals: %d\n", len(r.Proposals))
	for _, p := range r.Proposals {
		fmt.Fprintf(&b, "    - [%s] %s %s — %s\n", p.Action, p.Type, p.Permalink, truncate(p.Rationale, summaryEvidenceMax))
	}
	if len(r.BMCommands) > 0 {
		fmt.Fprintf(&b, "  bm_commands: %d\n", len(r.BMCommands))
	}
	fmt.Fprintf(&b, "  next_action:   %s\n", r.NextAction)
	return b.String()
}
```

- [ ] **Step 5: Unit + integration tests**

Mirror Task 7 step 5 and step 6 patterns for the extract handler. Cover the same axes:

- happy path,
- empty `completion_envelopes` (synthetic `insufficient_evidence` envelope),
- oversized payload,
- every envelope evidence-bare (all three of `FinalDiff` / `FinalFiles` / `TestEvidence` empty): synthetic refusal without a reviewer call,
- **mixed envelopes**: two envelopes, one with `FinalDiff` and one evidence-bare. The reviewer IS dispatched (because at least one envelope has evidence), and the returned `ExtractResult.Findings` contains one `insufficient_evidence` finding identifying the evidence-bare envelope by `task_title` plus whatever findings the reviewer produced. Order: pre-findings first, reviewer findings after.
- **bad model_override + all envelopes evidence-bare**: caller passes a malformed `model_override` AND all envelopes are evidence-bare. The handler returns the `insufficient_evidence` refusal envelope, NOT a model-validation error — because evidence accounting runs before model resolution. Asserts the order documented in step 4 above.
- `bm_commands` strip with `KBStore=""` and round-trip with `KBStore="basic-memory"`,
- `kb_store_mismatch` finding emission with `KBStore="basic-memory"` when a proposal's `Permalink` or a permalink string parsed out of `bm_commands[*].args_json` starts with `/` or contains `://`,
- truncation parity: fake reviewer returns `providers.ErrResponseTruncated`; the handler emits an `ExtractResult` envelope with `verdict: warn`, one finding `{severity: minor, category: other, criterion: reviewer_response}`, and a populated `SummaryBlock`. `Partial` must be the zero value (false / omitted) — partial-findings recovery is not implemented for extract in 0.6.0; this matches the no-analysis truncation pattern at handlers.go:390-403,
- **parse-retry path** (mirror of Task 7's): two-response fake reviewer with malformed-first / valid-on-retry, asserting `reviewExtract` retries once with `RetryHint` and returns the parsed `ExtractResult`; separate sub-test for malformed-twice → wrapped retry-exhausted error,
- envelope shape parity: every happy-path response carries a non-empty `summary_block`, a `provider:model`-shaped `model_used`, and `review_ms >= 0`,
- clamp finding propagation: `max_tokens_override > MaxTokensCeiling` produces a head-of-Findings clamp entry (severity `minor`, category `other`, criterion `max_tokens_override`) via `prependExtractClamp(r, clamp)` where `clamp` is the `verdict.Finding` returned by `effectiveMaxTokens` — matching `prependClamp` / `prependPlanClamp` in handlers.go. Clamp does NOT modify `next_action`.

- [ ] **Step 6: Run tests**

Run: `go test -race ./...`

Expected: PASS.

- [ ] **Step 7: Commit task 10**

```bash
git add internal/mcpsrv/ internal/prompts/
git commit -m "$(cat <<'EOF'
feat(mcpsrv): implement extract_project_knowledge tool

Stateless MCP tool that returns Proposals (create/update/supersede)
plus optional bm_commands given completion envelopes. Output-token
budget scales with envelope count per spec §5.3. Synthetic
insufficient_evidence handling for empty or evidence-bare inputs.
Includes formatExtractSummary block.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 11: Create the five note templates under `examples/project-knowledge/`

**Goal:** Ship the five note-type templates (`decision`, `module`, `feature`, `glossary`, `epic`) plus a `README.md` that describes the convention and the maintenance-ownership table from spec §2.

**Acceptance criteria:**
- `examples/project-knowledge/README.md` summarises the two layers (durable / epic-scoped), enumerates the five types, links the spec, and includes the maintenance-ownership table from spec §2.
- `examples/project-knowledge/decision.md` template carries frontmatter matching spec §2 (including the new optional `epic_origin` field) and body sections `## Context`, `## Decision`, `## Consequences`, `## Alternatives considered`.
- `examples/project-knowledge/module.md` carries body sections `## Purpose`, `## Invariants`, `## Conventions`, `## Touch-points`.
- `examples/project-knowledge/feature.md` carries frontmatter from spec §2 and body sections `## What it does`, `## How it works`, `## Recent material changes`, `## Related`.
- `examples/project-knowledge/glossary.md` carries a short-definition body skeleton.
- `examples/project-knowledge/epic.md` carries frontmatter from spec §2 and body sections `## Charter`, `## In scope`, `## Out of scope`, `## Acceptance (epic-level)`, `## Progress ledger`, `## Open questions`.
- Each frontmatter block uses placeholder values (e.g. `<one-line title>`) so the templates are obviously starter-text.

**Non-goals:**
- Do not add `epic_origin` to `module` or `feature` templates. Spec §"Open questions" defers that decision; keep `epic_origin` on `decision` only for 0.6.0.
- Do not generate auto-fill scripts; templates are markdown-only.

**Context:**
This is the spec §2 schema, materialized one-to-one. Frontmatter fields use Basic Memory's standard frontmatter as the base. Be precise about field names — INTEGRATION.md and the extract prompt reference these.

**Files:**
- Create: `examples/project-knowledge/README.md`
- Create: `examples/project-knowledge/decision.md`
- Create: `examples/project-knowledge/module.md`
- Create: `examples/project-knowledge/feature.md`
- Create: `examples/project-knowledge/glossary.md`
- Create: `examples/project-knowledge/epic.md`

- [ ] **Step 1: Create `examples/project-knowledge/README.md`**

Contents:

```markdown
# Project-knowledge note templates

These templates seed the project-knowledge schema used by the optional v0.6.0
`prime_project_knowledge` and `extract_project_knowledge` tools. They are
markdown with [Basic Memory](https://github.com/basicmachines-co/basic-memory)
frontmatter; copy a template into your shared KB and fill it in.

Authoritative design: [`docs/superpowers/specs/2026-05-18-project-knowledge-design.md`](../../docs/superpowers/specs/2026-05-18-project-knowledge-design.md).

## Two layers

- **Durable reference layer** (timeless / slow-evolving): `decision`, `module`, `feature`, `glossary`.
- **Epic-scoped layer** (time-bounded): `epic`.

## Maintenance ownership

| Type | Author at birth | Updated by |
|---|---|---|
| `epic` | Human at kickoff | Mostly automated (extract appends ledger; humans edit open questions) |
| `decision` | Drafted by extract → reviewed by human → merged | Append-only; new decisions supersede old ones |
| `module` | Human (or seeded from a spec) | Mostly human; extract proposes invariant/convention edits when it sees drift |
| `feature` | Human (or seeded from a spec) | Mostly human; extract proposes "Recent material changes" entries |
| `glossary` | Opportunistic (human or extract) | Opportunistic |
```

- [ ] **Step 2: Create `examples/project-knowledge/epic.md`**

Use the spec §2 epic schema. Frontmatter:

```markdown
---
permalink: epics/<slug>
type: epic
title: <one-line title>
status: planned
opened_at: <YYYY-MM-DD>
closed_at: null
owners: ["@<handle>"]
plan_refs: []
touches_modules: []
produces_decisions: []
relates: []
tags: [epic]
---

## Charter

<two or three sentences naming the user-visible goal and the success criterion>

## In scope

- <bullet>

## Out of scope

- <bullet>

## Acceptance (epic-level)

- <epic-level AC; per-task ACs live in the implementation plan>

## Progress ledger

<!-- Append-only. One entry per finished task. The extract tool writes here. -->

## Open questions

- <bullet>
```

- [ ] **Step 3: Create `examples/project-knowledge/decision.md`**

Use spec §2 decision schema including the new optional `epic_origin` field.

```markdown
---
permalink: decisions/<NNNN>-<slug>
type: decision
title: <one-line title>
status: proposed
supersedes: []
proposed_by: "@<handle>"
decided_at: <YYYY-MM-DD>
epic_origin: <epics/permalink or omit>
relates: []
tags: []
---

## Context

<the constraint or pressure that forced a choice>

## Decision

<what we chose, in one paragraph>

## Consequences

- <bullet>

## Alternatives considered

- <bullet>
```

- [ ] **Step 4: Create `examples/project-knowledge/module.md`**

```markdown
---
permalink: modules/<name>
type: module
title: <one-line title>
status: stable
last_changed_in: <X.Y.Z>
relates_features: []
shaped_by_decisions: []
tags: [module]
---

## Purpose

<one sentence on what this module exists to do>

## Invariants

- <bullet>

## Conventions

- <bullet>

## Touch-points

- <module/file path>
```

- [ ] **Step 5: Create `examples/project-knowledge/feature.md`**

Use spec §2 feature schema.

```markdown
---
permalink: features/<slug>
type: feature
title: <one-line title>
surface: mcp_tool
status: stable
since_version: <X.Y.Z>
last_changed_in: <X.Y.Z>
relates_modules: []
shaped_by_decisions: []
tags: []
---

## What it does

<one paragraph user-visible description>

## How it works

<one paragraph architectural summary; link decisions / modules>

## Recent material changes

- <X.Y.Z> — <one line; details live in the linked decision>

## Related

- <links>
```

- [ ] **Step 6: Create `examples/project-knowledge/glossary.md`**

```markdown
---
permalink: glossary/<term>
type: glossary
title: <Term>
status: stable
tags: [glossary]
---

**<Term>** — <one-sentence canonical definition>.

<Optional notes paragraph for nuance, common confusions, or related terms.>
```

- [ ] **Step 7: Verify file listing**

Run: `ls examples/project-knowledge/`

Expected: `README.md decision.md epic.md feature.md glossary.md module.md`

- [ ] **Step 8: Commit task 11**

```bash
git add examples/project-knowledge/
git commit -m "$(cat <<'EOF'
docs(examples): add project-knowledge note templates

Five note-type templates per spec §2: decision (with optional
epic_origin), module, feature, glossary, epic. Plus a README that
links the spec and reproduces the maintenance-ownership table.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 12: Update INTEGRATION.md with the project-knowledge section

**Goal:** `INTEGRATION.md` gains a top-level "Project knowledge (optional)" section that describes the new tools, the controller workflow, the dispatch-clause addition, the auto-apply ladder, and the BM tool names anchor. The existing dispatch clause is extended with a short "Project knowledge (auto-attached by the controller)" block per spec §4.

**Heads-up:** INTEGRATION.md was trimmed in the v0.5.1 release (the `2026-05-19-integration-md-trim` plan landed). Section anchors are now: `## Drift-protection protocol (anti-tangent-mcp)` at line 155 (the full implementer-prompt clause), `## Task spec (pass these fields verbatim…)` at line 195 (the field list inside that clause), `## Drift protection` at line 212 (the short variant for system-prompt-installed agents), and `## 5. For controllers — plan-handoff gate + dispatch addendum` at line 270 (with the plan-handoff gate now living at `### 5.1`, not as a top-level section). Treat the bullets below as the source of truth; line numbers will drift again as the file evolves.

**Acceptance criteria:**
- New `## Project knowledge (optional)` top-level section sits **immediately above `## 5. For controllers — plan-handoff gate + dispatch addendum`** (~line 270). The plan-handoff gate is now a `### 5.1` subsection, not a top-level section — do not search for `## Plan-handoff gate`. The new section logically precedes the controller-flow section because the prime/extract loop is a controller responsibility.
- The section covers, in order:
  1. What it is and when it earns its keep (project size, multi-agent, multi-author).
  2. Architecture diagram (lifted / adapted from spec §1).
  3. Controller workflow per epic (spec §4 flow ladder).
  4. The five note types and where templates live (link to `examples/project-knowledge/`).
  5. The `project_knowledge` field on `validate_task_spec` / `validate_plan` (and the deliberate omission from `check_progress` / `validate_completion` per spec §3.3).
  6. Auto-apply ladder table from spec §4.3.
  7. Anchored BM tool names taken **verbatim from the Task 0a verified block** at the bottom of this plan (typically: `search_notes`, `read_note`, `write_note`, `edit_note`, `move_note`, `delete_note`), plus the supersede mapping (two-step `write_note + edit_note`, no `supersede_note` verb). The section anchors these so a future BM rename is a doc + verified-block change only.
  8. Env-var summary (`ANTI_TANGENT_KB_STORE`, `ANTI_TANGENT_PRIME_MODEL`, `ANTI_TANGENT_EXTRACT_MODEL`, `ANTI_TANGENT_PRIME_MAX_TOKENS`, `ANTI_TANGENT_EXTRACT_MAX_TOKENS`).
- The existing dispatch clause (section 4.2 in the current file: `## Drift-protection protocol (anti-tangent-mcp)` at line 155 through `## Task spec (pass these fields verbatim…)` at line 195) gains the spec §4 "Project knowledge (auto-attached by the controller)" block inserted **between** the closing line of the drift-protection clause's numbered steps (immediately after step 3b's CodeScene pre-DONE check paragraph, ~line 193) and the `## Task spec` heading at line 195. Use the verbatim text from spec §4 "Implementer dispatch-clause addition." **Divergence note** — the spec's draft text places this block *after* the `## Task spec` field list; this plan places it *before* because the implementer must read the project-knowledge section before deciding what to pass into the task_spec call (the brief informs the call). Task 0a Step 2b point 6 resolves this by editing spec §4 to match the before-Task-spec placement (Resolution A); by the time Task 12 runs, spec + plan + INTEGRATION.md all agree.
- The opening paragraph at INTEGRATION.md:3 currently says "It exposes four tools: a plan-level handoff gate (`validate_plan`) and three per-task lifecycle hooks…". Update it to: "It exposes six tools: a plan-level handoff gate (`validate_plan`), three per-task lifecycle hooks (`validate_task_spec` / `check_progress` / `validate_completion`), and an optional project-knowledge pair (`prime_project_knowledge` / `extract_project_knowledge`)."
- INTEGRATION.md:319 currently says "`max_tokens_override` (all four tools)" — update to "(all six tools)". The new pair accepts `max_tokens_override` (Task 7 and Task 10 both wire it in), so the existing per-call-args section applies unchanged to them.
- After both edits, run `rg -n 'four tools' INTEGRATION.md` — expected: zero matches.
- The short variant clause at `## Drift protection` (line 212) does NOT need a separate project-knowledge block — by design it's a short reference that delegates to the full clause. Add one new bullet at the end of its list pointing to the new auto-attached block in the long variant: `- If a Project knowledge section is auto-attached, read it before validate_task_spec and pass it verbatim as project_knowledge.`
- A short note explains that this block is omitted when the project has no KB.
- `rg "Project knowledge \(optional\)" INTEGRATION.md` returns a match.

**Non-goals:**
- Do not modify behavior of any other section.
- Do not include the long team-setup VM playbook here — link to `docs/team-setup/basic-memory-shared-vm.md` (created in Task 14).

**Context:**
The dispatch clause in INTEGRATION.md is **the** canonical paste-in for controllers. Anchoring the BM tool names here lets future BM renames be a doc-only change (because `extract.tmpl` references the same names; if they ever diverge, the prompt becomes the source of truth and the doc updates to match).

**Files:**
- Modify: `INTEGRATION.md`

- [ ] **Step 1: Insert the new top-level section**

Add the section per the AC. Lift the architecture diagram from spec §1 (lines 43-69) verbatim. Lift the auto-apply ladder table from spec §4.3 verbatim. Keep prose tight.

- [ ] **Step 2: Reference the verified BM tool names**

Task 0a already pinned the canonical BM tool names in the "Basic Memory contract (verified <date>)" block at the bottom of this plan file. Use those names verbatim in this INTEGRATION.md section. If the verified block was updated after Tasks 6/9 were authored, those prompts and their goldens should already reflect the verified names (Task 0a Step 3 propagation); confirm with:

```bash
rg -n 'search_notes|read_note|write_note|edit_note|move_note|delete_note|supersede_note' internal/prompts/templates/ internal/prompts/testdata/ INTEGRATION.md
```

Every match should be a canonical name from Task 0a's verified block. Any hit on `supersede_note` is a regression — it should have been rewritten to the two-step `write_note + edit_note` mapping during Task 0a Step 3. Fix the verified block first if needed, then re-run any affected golden regenerations.

- [ ] **Step 3: Insert the dispatch-clause addition**

Locate the existing block at INTEGRATION.md `## Drift-protection protocol (anti-tangent-mcp)` (line 155 post-merge) through `## Task spec (pass these fields verbatim to validate_task_spec)` (line 195). Immediately after the closing of the "**3b. CodeScene pre-DONE check**" paragraph (around line 193 post-merge) and before the `## Task spec` subsection (line 195), insert:

```markdown
## Project knowledge (auto-attached by the controller)

The task brief above includes a "Project knowledge" section with excerpts
the controller pre-selected from the project KB. Read it before
`validate_task_spec` — it carries decisions, module invariants, and prior
context relevant to this task. Treat it as authoritative.

When calling `validate_task_spec`, also pass that same section verbatim as
`project_knowledge` so the reviewer has the same grounding you do. (Omit
this block if there is no KB attached.)
```

Also add `project_knowledge:` to the `## Task spec` field list (after `controller_verified_references:`), describing it as `<optional, v0.6.0+; markdown excerpts the controller pre-selected from the KB>`.

- [ ] **Step 4: Verify**

Run: `rg '^## Project knowledge \(optional\)' INTEGRATION.md`

Expected: one match.

Run: `rg '^## Project knowledge \(auto-attached by the controller\)' INTEGRATION.md`

Expected: one match (inside the dispatch clause block).

- [ ] **Step 5: Commit task 12**

```bash
git add INTEGRATION.md
git commit -m "$(cat <<'EOF'
docs(integration): document project knowledge integration

New top-level "Project knowledge (optional)" section covering the
controller flow, note types, env vars, auto-apply ladder, and
anchored Basic Memory tool names. Dispatch clause gains the
auto-attached project-knowledge block per spec §4.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 13: Update README.md with the project-knowledge mention

**Goal:** `README.md` gains a one-paragraph mention plus a link to the new INTEGRATION.md section and the team-setup doc, so a reader who lands on the repo page learns the optional KB integration exists.

**Acceptance criteria:**
- A new `## Project knowledge (optional)` subsection (or equivalent) is added under the existing tool-surface listing.
- The paragraph is one to two sentences plus links to: the spec, `INTEGRATION.md#project-knowledge-optional`, `docs/team-setup/basic-memory-shared-vm.md`, and `examples/project-knowledge/`.
- The README's "Tools" or equivalent list of MCP tools is updated to mention `prime_project_knowledge` and `extract_project_knowledge` with one-line summaries.
- README.md:188 ("confirm all four tools — `validate_plan`, `validate_task_spec`, `check_progress`, `validate_completion` — appear in the discovered tool catalog") is updated to "all six tools — `validate_plan`, `validate_task_spec`, `check_progress`, `validate_completion`, `prime_project_knowledge`, `extract_project_knowledge`".
- README.md:196 ("All four tools accept an optional `max_tokens_override`…") — the optional `max_tokens_override` posture extends to the two new tools, so this sentence becomes "All six tools accept an optional `max_tokens_override`…". After the edit, `rg -n 'four tools' README.md` returns zero matches.
- The README.md "## The 4 tools" heading at line 294 is renamed to "## The 6 tools" with the new pair appended in the same paste-ready style as the existing four. After the edit, `rg -n '^## The 4 tools' README.md` returns zero matches.
- The README's existing environment-variable reference block (currently a fenced code block at `README.md:156-171`, listing one `KEY=value   # comment` per line) gains entries for the five new env vars (`ANTI_TANGENT_KB_STORE`, `ANTI_TANGENT_PRIME_MODEL`, `ANTI_TANGENT_EXTRACT_MODEL`, `ANTI_TANGENT_PRIME_MAX_TOKENS`, `ANTI_TANGENT_EXTRACT_MAX_TOKENS`) matching the style and defaults already shown for `ANTI_TANGENT_PRE_MODEL` / `ANTI_TANGENT_PLAN_MAX_TOKENS`. Each entry names its default and (where applicable) its fallback chain, using the trailing-comment shape already in use.

**Non-goals:**
- Do not rewrite the README. Add focused additions only.

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Locate the tools list**

Run: `rg -n 'validate_task_spec|validate_plan' README.md | head -20` and identify where the tool list lives.

- [ ] **Step 2: Add the two new tools to the list**

Append entries for `prime_project_knowledge` and `extract_project_knowledge` matching the existing tools' one-line summary style.

- [ ] **Step 3: Add the project-knowledge subsection**

Insert a short subsection that links spec, INTEGRATION.md anchor, team-setup doc, and the examples directory.

- [ ] **Step 3a: Extend the env-var reference block**

Locate the existing fenced env-var block at `README.md:156-171` (run `rg -n 'ANTI_TANGENT_PRE_MODEL|ANTI_TANGENT_PLAN_MAX_TOKENS' README.md` to confirm). Append entries matching the style — `KEY=default   # one-line description / fallback chain` per line — covering each of `ANTI_TANGENT_KB_STORE` (default empty; only `basic-memory` accepted), `ANTI_TANGENT_PRIME_MODEL` (falls back to `ANTI_TANGENT_PLAN_MODEL` → `ANTI_TANGENT_PRE_MODEL`), `ANTI_TANGENT_EXTRACT_MODEL` (same fallback chain), `ANTI_TANGENT_PRIME_MAX_TOKENS` (default 4096), and `ANTI_TANGENT_EXTRACT_MAX_TOKENS` (default 8192).

- [ ] **Step 4: Verify**

Run: `rg -n 'prime_project_knowledge|extract_project_knowledge' README.md`

Expected: at least one match per tool.

- [ ] **Step 5: Commit task 13**

```bash
git add README.md
git commit -m "$(cat <<'EOF'
docs(readme): announce project knowledge integration

Adds the two new MCP tools to the tools list and a short
project-knowledge subsection linking the spec, INTEGRATION.md,
team-setup doc, and the templates directory.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 14: Author `docs/team-setup/basic-memory-shared-vm.md`

**Goal:** Ship an operator-facing setup doc for teams running Basic Memory on a shared VM, following the spec §"docs/team-setup/basic-memory-shared-vm.md outline" twelve-section layout. Section 5 cites the BM remote-MCP transport story from the **verified Task 0a contract** at the bottom of this plan (the transport open question is resolved up front by Task 0a, not during this task — Task 14 just renders it into operator-facing prose). Section 8 prescribes a **git-backed BM data directory with periodic commit-and-push** (systemd timer running every 60 seconds) so every agent-driven KB edit lands in a private team repo, with an inotify-based low-latency variant kept as an alternative for operators who want per-edit attribution and can accept the operational overhead.

**Acceptance criteria:**
- `docs/team-setup/` directory exists and contains `basic-memory-shared-vm.md`.
- All twelve sections from spec §"docs/team-setup/basic-memory-shared-vm.md outline" are present and titled accurately.
- Section 5 ("Configuring remote MCP transport") names the verified transport from Task 0a's "Basic Memory contract (verified <date>)" block (SSE, streamable-HTTP, stdio-via-SSH-proxy, or whatever Task 0a verified) and links the upstream BM doc URL captured during Task 0a step 1. The doc MUST NOT contain "TBD", "see open questions", or any tracking-pointer-as-fallback wording — Task 0a's contract is that the transport is resolved up front BEFORE Tasks 6/7/9/10/12/14 run, so by the time this task executes the verified block is authoritative. If you reach this task and the verified block is missing or still says "open", stop and go back to Task 0a; do not invent fallback prose here.
- **Section 8 ("Storage & backup") prescribes a git-backed BM data directory** with the BM data dir initialised as a git working tree (or symlinked into one), authenticated to a private remote via a deploy key. The deploy key MUST be owned `bm:bm` mode `0600` (NOT `root:bm 0600` — the service runs as user `bm` and cannot read root-owned files at mode 0600). The data directory `/var/lib/basic-memory/kb` and its `.git/` subtree MUST be owned `bm:bm` so `git commit` can create `.git/index.lock` and write objects; section 8 includes the explicit `install -d -o bm -g bm` + `sudo -u bm git init` invocations and warns that running `git init` as root will silently break the service. The section presents the **systemd timer approach (60s cadence) as the primary recommendation** and the **inotify-recursive variant as an alternative** for teams that want per-edit attribution; the long-cadence snapshot cron (5-minute) is a documented fallback for hosts without systemd. All three variants land in section 8 as ready-to-paste configs.
- Section 8's **primary variant** ships a `basic-memory-git-sync.service` (Type=oneshot) + `basic-memory-git-sync.timer` (`OnUnitActiveSec=60s`, `Persistent=true`) pair. The service runs `commit-and-push.sh`. 60-second cadence chosen so concurrent multi-file writes across the recursive BM tree coalesce naturally into one commit per minute. Explanation in the doc: systemd `.path` units use `inotifyaddwatch(2)` non-recursively, so a single `PathChanged=/var/lib/basic-memory/kb` would miss edits to nested notes (BM stores under `kb/decisions/*.md`, `kb/modules/*.md`, etc.); a path-unit-per-subdir is brittle because BM creates new subdirs at runtime. The timer is the simplest correct shape.
- Section 8's **alternative variant (inotify-recursive)** ships a small foreground service that runs `inotifywait -r -m -e close_write,create,move,delete --exclude '(^|/)\.git(/|$)' /var/lib/basic-memory/kb` and pipes each event into a debouncer (15-second quiet window) that invokes `commit-and-push.sh`. The `--exclude` is load-bearing: the KB directory IS the git working tree, so without it every `git commit` would touch `.git/index`, `.git/objects/`, `.git/HEAD`, etc. and re-trigger the watcher in an infinite loop. Concrete shape uses `inotifywait` (Linux) plus a tiny bash loop, NOT systemd `PathChanged=`. The doc states why: per-edit attribution at the cost of an extra installed package (`inotify-tools`) and a slightly more complex unit to operate. Teams that don't need per-edit attribution should NOT pick this.
- Section 8's **long-cadence fallback** ships a `systemd.timer` (or crontab) that runs `commit-and-push.sh` every 5 minutes. Choose this on hosts without systemd or when commit volume is a hard constraint.
- Section 8 documents the **two-VM / push-conflict** path explicitly: most teams run one shared VM and this is moot, but if a second VM (or a hand-edit on a developer laptop) writes to the same remote, the `commit-and-push.sh` script attempts `git pull --rebase --autostash` once before re-pushing; on rebase conflict the script exits non-zero, the systemd service enters `failed` state, and the doc names the manual-resolution playbook.
- Section 11 (license compatibility note) states AGPL-3.0; trivially compliant via the network-service pattern; policy line: bugs go upstream, no fork-and-patch.
- Section 12 (troubleshooting) lists at least **seven** common failure modes and their resolutions: (1) MCP transport handshake failure, (2) credential rotation (token or SSH keypair, depending on transport), (3) BM index out-of-sync with markdown source, (4) **`git push` failure (network blip, remote-rejected, deploy-key expired) — commits queue locally, are flushed by the next successful trigger; describe how to inspect the queue via `git log origin/<branch>..HEAD`**, (5) **rebase conflict on the shared remote — name the exact `git status` shape and the resolve-and-resume command**, (6) **`.git/index.lock` permission denied — caused by running git as root once during setup; resolution is `chown -R bm:bm /var/lib/basic-memory/kb && systemctl restart basic-memory-git-sync.service`**, (7) **`Host key verification failed` — `known_hosts` not provisioned for the bm user, or remote host key rotated; resolution is the §8 `ssh-keyscan` invocation (after confirming legitimacy if the key changed)**.
- Internal links to `examples/project-knowledge/` and INTEGRATION.md resolve.

**Non-goals:**
- Do not duplicate the BM upstream README. Link, summarize, and add only the team-VM-specific specifics.
- Do not embed secrets handling for any specific vault (1Password / Vault / etc.); recommend a generic per-dev token rotation flow if the verified transport uses tokens, or a per-dev SSH keypair flow if the verified transport is stdio-via-SSH-proxy.
- Do not prescribe a git provider (GitHub / GitLab / Gitea / self-hosted) — use a generic `git@<remote>:<org>/<repo>.git` placeholder so the doc is provider-agnostic.
- Do not extend the git-backed model to BM's SQLite index — only the markdown source files are committed. The index regenerates from markdown on BM startup; committing it would bloat history and introduce non-deterministic diffs.

**Context:**
The BM transport story was resolved in Task 0a and recorded in this plan file's "Basic Memory contract (verified <date>)" block. This task consumes that resolution rather than re-doing the research. Section 5 of the team-setup doc cites the verified transport from the block (with the original upstream link captured during Task 0a Step 1) and adds shared-VM-specific operational details (firewall, systemd unit, per-dev credentials) that the upstream BM docs do not cover. The exact shape of "per-dev credentials" depends on the verified transport: a URL+token model for SSE / streamable-HTTP, an SSH keypair per dev (`~/.ssh/basic-memory-<dev>`) for stdio-via-SSH-proxy. Sections 6 (auth) and 7 (per-developer MCP config) MUST follow whichever shape Task 0a verified — do not paste a token snippet if the transport is SSH-based, and do not paste an SSH config if the transport is HTTP-based.

BM's storage is a plain directory of frontmatter-laden markdown with no proprietary lock or DB-only state on the source-of-truth path, which makes git tracking safe: `git add -A` captures the full KB state and a `git checkout` cleanly restores it. The primary variant's 60-second timer cadence is long enough to coalesce a single extract proposal's create + edit pair into one commit but short enough that operators see freshly-written notes pushed within the next minute. The inotify-recursive alternative gives per-edit attribution (each agent action ends up as its own commit) at the cost of installing `inotify-tools` and operating a long-running watcher service. Operators expecting hundreds of commits/day should size the repo accordingly (shallow clones for new VMs; periodic `git gc --aggressive` via cron).

**Why not systemd path units.** systemd's `.path` units use `inotify_add_watch(2)` non-recursively — a `PathChanged=/var/lib/basic-memory/kb` triggers only on changes to the kb/ directory itself (file creates/deletes at that level), not on edits to nested files like `kb/decisions/0042-x.md`. Since BM stores notes in nested subdirectories that it creates at runtime, a path-unit primary would silently miss most actual note edits. A workaround using one path unit per subdir is brittle (BM creates new subdirs as new types appear). The 60-second timer is the simplest correct shape; for lower latency, the inotify-recursive variant uses `inotifywait -r` directly.

**Open questions** (resolve while writing the doc):
- **Secrets policy.** Does the team allow BM notes to contain anything that should not live in a private repo's history? Likely no for most teams (notes are project decisions and module invariants), but Section 8 must include a literal callout: "Treat the KB git repo as private. Anything you would not paste into a private Slack channel does not belong in a BM note." If a team decides notes may contain restricted material, they need to either (a) accept the repo's existing access boundary as the policy boundary, or (b) bypass the git-backing entirely and run a daily encrypted snapshot to a different store.

**Files:**
- Create: `docs/team-setup/basic-memory-shared-vm.md`

- [ ] **Step 1: Create the directory**

Run: `mkdir -p docs/team-setup`

- [ ] **Step 2: Pull verified BM contract from Task 0a**

Read the "Basic Memory contract (verified <date>)" block at the bottom of this plan file. Section 5 of the team-setup doc must cite the transport recommendation from that block (with the upstream URL recorded in Task 0a Step 1). Do not re-do the upstream research here.

- [ ] **Step 3: Draft sections 1-12**

Author the file following the AC. Section 5 must resolve the open question. Section 7 (per-developer Claude Code MCP config) needs a concrete JSON snippet whose shape depends on the verified transport from Task 0a:

- If transport is SSE or streamable-HTTP: paste an `mcpServers` entry with `"transport": "sse"` (or `"http"`), `"url": "<vm-host>/<bm-endpoint>"`, and a per-dev `"headers": { "Authorization": "Bearer <PER_DEV_TOKEN>" }` placeholder. Cover token rotation in §6.
- If transport is stdio-via-SSH-proxy: paste an `mcpServers` entry with `"command": "ssh"`, `"args": ["-i", "~/.ssh/basic-memory-<dev>", "<vm-user>@<vm-host>", "basic-memory", "mcp"]` (or whichever invocation the verified contract specifies). Cover SSH keypair generation + authorized-keys provisioning in §6.
- If transport is something else (per Task 0a step 1's enumeration): document whatever the verified contract specifies — do NOT improvise.

- [ ] **Step 3a: Author section 8 — git-backed storage**

Write section 8 with four subsections: (i) setup (chown the BM data dir + .git working tree to `bm:bm` so the service user can write — `git commit` creates `.git/index.lock`, `.git/HEAD.lock`, etc., and the bm user must own the tree to touch them; initialise BM data dir as a git working tree; configure remote + deploy key with **`bm:bm 0600`** ownership so the service user can read it; set `git config user.email "basic-memory@<team>.local"` and `user.name "Basic Memory"` so commits have stable attribution), (ii) primary variant (systemd timer at 60s cadence), (iii) alternative variant (inotify-recursive watcher for per-edit attribution), (iv) long-cadence fallback (5-minute timer). All variants share the same `commit-and-push.sh` so the doc only writes the script once.

Concrete shape for the working-tree setup (paste-ready, run as root once during VM provisioning):

```bash
# 1. Ensure the bm user/group exists. Adjust UID/GID to whatever your team uses.
id -u bm >/dev/null 2>&1 || useradd --system --create-home --home-dir /var/lib/basic-memory --shell /usr/sbin/nologin bm
# 2. The KB working tree, owned by bm:bm. BM writes notes here; the systemd
#    service commits here. Both run as bm, so the tree itself plus .git must
#    be bm-owned; otherwise git commit fails with "fatal: Unable to create
#    '.../.git/index.lock'" the moment the service runs.
install -d -o bm -g bm -m 0750 /var/lib/basic-memory/kb
# 3. Initialise as a working tree pointing at the team's private remote.
sudo -u bm git -C /var/lib/basic-memory/kb init -b main
sudo -u bm git -C /var/lib/basic-memory/kb remote add origin git@<remote>:<org>/<repo>.git
sudo -u bm git -C /var/lib/basic-memory/kb config user.email "basic-memory@<team>.local"
sudo -u bm git -C /var/lib/basic-memory/kb config user.name  "Basic Memory"
```

The `sudo -u bm` invocations matter — running `git init` as root would leave `.git/` root-owned and the service would fail on first commit. If you forget and the service errors, run `chown -R bm:bm /var/lib/basic-memory/kb` to recover.

Concrete shape for the script (paste-ready in the doc):

```bash
#!/usr/bin/env bash
# /usr/local/bin/basic-memory-commit-and-push.sh
set -euo pipefail
cd /var/lib/basic-memory/kb
export GIT_SSH_COMMAND="ssh -i /etc/basic-memory/deploy_key -o StrictHostKeyChecking=yes"

git add -A

# Commit if there are staged changes. Skip the commit step quietly when
# there's nothing to stage — but DO NOT exit yet. A previous tick may
# have committed locally and then failed to push (network blip, deploy
# key issue, etc.). Those commits are still ahead of origin/main and
# this tick is responsible for flushing them.
if ! git diff --staged --quiet; then
  git commit -m "bm: $(date -Iseconds)"
fi

# Check whether we have anything to push. `git rev-list --count
# origin/main..HEAD` returns the number of local commits that origin/main
# is missing. If it's zero we're fully synced and can exit silently;
# otherwise (either a fresh commit above or a queued backlog from a
# previous failed push) we push. We fetch first so the ahead-count is
# correct even after the remote moved.
#
# Bootstrap case: on the first run, the team's remote may be an empty
# repository (no `main` branch yet). `git fetch origin main` will fail
# silently because there is no remote branch to fetch, leaving no
# `origin/main` ref. We detect that case and fall through to the push
# path so the first commit lands as `origin/main`. Without this branch
# the script would compute `ahead=0` from the missing ref and exit
# without pushing — leaving the team with a local-only commit history.
git fetch --quiet origin main || true
if git rev-parse --quiet --verify origin/main >/dev/null; then
  ahead=$(git rev-list --count "origin/main..HEAD")
  if [[ "$ahead" -eq 0 ]]; then
    exit 0
  fi
else
  # No origin/main yet — bootstrap path. Use `git push -u origin HEAD:main`
  # below so the first push both creates the remote branch and sets the
  # local upstream tracking ref. Skip the ahead-count gate entirely.
  exec_bootstrap_push=1
fi

# Bootstrap path: no origin/main exists yet — create it and set upstream.
if [[ -n "${exec_bootstrap_push:-}" ]]; then
  git push -u origin HEAD:main
  exit 0
fi

# Normal sync uses a fast-forward push. If the remote has diverged (rare:
# only when a second writer hit the same repo), pull-rebase once and try
# again. We do NOT use --force-with-lease for the normal push because this
# is a backup/sync job — rewriting remote history on every tick would be
# surprising and could trip branch-protection rules. Only after a clean
# local rebase do we re-push with --force-with-lease so the rebase's
# rewritten history can land.
if ! git push origin main; then
  git pull --rebase --autostash origin main
  git push --force-with-lease origin main
fi
```

Deploy-key install (paste-ready). The `commit-and-push.sh` script invokes git with `StrictHostKeyChecking=yes`, so the bm user's `known_hosts` MUST contain the remote's host key BEFORE the first push; otherwise the service fails with "Host key verification failed":

```bash
install -d -o bm -g bm -m 0700 /etc/basic-memory
install -o bm -g bm -m 0600 /path/to/deploy_key /etc/basic-memory/deploy_key
# Provision known_hosts for the bm user against the team's git remote.
# Replace <remote-host> with the hostname/port of your git provider (e.g.
# github.com, gitlab.example.com:22, or your self-hosted host). Use port
# 22 implicitly or specify it explicitly with `-p`. Run as bm so the
# resulting file ends up owned bm:bm with mode 0600.
install -d -o bm -g bm -m 0700 /var/lib/basic-memory/.ssh
sudo -u bm ssh-keyscan -H <remote-host> >> /var/lib/basic-memory/.ssh/known_hosts
sudo -u bm chmod 0600 /var/lib/basic-memory/.ssh/known_hosts
```

If the team's git provider rotates its host key, the next sync run will fail with "REMOTE HOST IDENTIFICATION HAS CHANGED" — operator must run `sudo -u bm ssh-keygen -R <remote-host>` then re-keyscan.

Script install (paste-ready — both variants reuse `basic-memory-commit-and-push.sh`; the inotify alternative additionally installs `basic-memory-watch-and-commit.sh`):

```bash
install -o root -g root -m 0755 ./basic-memory-commit-and-push.sh /usr/local/bin/basic-memory-commit-and-push.sh
# Only for the inotify-recursive alternative:
install -o root -g root -m 0755 ./basic-memory-watch-and-commit.sh /usr/local/bin/basic-memory-watch-and-commit.sh
```

Both scripts are owned by `root:root` mode `0755` (world-read+exec, root-write) — the systemd units invoke them as user `bm`, which only needs read+exec. World-write would allow any unprivileged user on the VM to substitute a malicious script before the next timer tick; `0755` blocks that.

`bm:bm 0600` — the service runs as user `bm` (see unit below) and must be able to read the key. `root:bm 0600` would silently break the service because group-read is off; if a future operator prefers root ownership they must use `root:bm 0640`, but `bm:bm 0600` is the simpler default and what the doc prescribes.

Concrete shape for the **primary variant (60-second timer)** — paste-ready. The service and timer are two separate files; the doc presents them as two separate fenced blocks so a reader can paste each into the right path without splitting the content:

`/etc/systemd/system/basic-memory-git-sync.service`:

```ini
[Unit]
Description=Commit and push BM KB changes
After=network-online.target

[Service]
Type=oneshot
ExecStart=/usr/local/bin/basic-memory-commit-and-push.sh
User=bm
Group=bm
```

`/etc/systemd/system/basic-memory-git-sync.timer`:

```ini
[Unit]
Description=Run basic-memory-git-sync every minute

[Timer]
OnBootSec=60s
OnUnitActiveSec=60s
Unit=basic-memory-git-sync.service
Persistent=true

[Install]
WantedBy=timers.target
```

The 60-second cadence is the natural debounce — concurrent writes within the window coalesce into one commit. Drop to 30s or raise to 120s based on observed write volume.

Concrete shape for the **alternative variant (inotify-recursive)** — paste-ready. Two files, presented as separate fenced blocks:

`/etc/systemd/system/basic-memory-git-watcher.service`:

```ini
[Unit]
Description=Watch BM KB recursively and commit on edits
After=network-online.target
Requires=network-online.target

[Service]
Type=simple
User=bm
Group=bm
ExecStart=/usr/local/bin/basic-memory-watch-and-commit.sh
Restart=on-failure
RestartSec=5s

[Install]
WantedBy=multi-user.target
```

`/usr/local/bin/basic-memory-watch-and-commit.sh`:

```bash
#!/usr/bin/env bash
# Requires inotify-tools (apt-get install inotify-tools).
set -euo pipefail
DIR=/var/lib/basic-memory/kb
DEBOUNCE_SEC=15
last_commit_pid=
# --exclude '(^|/)\.git(/|$)' is load-bearing: DIR is the git working tree,
# so every `git commit` (invoked by commit-and-push.sh below) writes to
# .git/index / .git/objects/ / .git/HEAD / refs/. Without the exclude the
# watcher would self-trigger on its own commits in a tight loop. The
# regex matches `.git` as the leading directory component OR any nested
# `.git` (worktrees / submodules — unlikely here but cheap to handle).
inotifywait -r -m -q \
  -e close_write -e create -e move -e delete \
  --exclude '(^|/)\.git(/|$)' \
  --format '%w%f' "$DIR" \
| while read -r _; do
    # Debounce: kill any pending committer; schedule a new one.
    if [[ -n "$last_commit_pid" ]] && kill -0 "$last_commit_pid" 2>/dev/null; then
      kill "$last_commit_pid" 2>/dev/null || true
    fi
    ( sleep "$DEBOUNCE_SEC" && /usr/local/bin/basic-memory-commit-and-push.sh ) &
    last_commit_pid=$!
  done
```

`-r` is the recursive flag — without it the watcher misses edits to nested note files, which is the failure mode the primary variant avoids by polling on a timer. `--exclude '(^|/)\.git(/|$)'` prevents the watcher from triggering on its own commits.

Concrete shape for the **long-cadence fallback (5-minute cadence)** — two forms depending on whether the host has systemd.

**With systemd** — only the timer changes; the service file from the primary variant is reused unchanged.

`/etc/systemd/system/basic-memory-git-sync.timer` (5-min variant):

```ini
[Timer]
OnCalendar=*:0/5
Persistent=true
Unit=basic-memory-git-sync.service

[Install]
WantedBy=timers.target
```

**Without systemd (crontab fallback)** — useful for older hosts, container images that ship without systemd, or operators who simply prefer cron. The log file MUST be provisioned with `bm:bm` ownership BEFORE installing the crontab; `/var/log/` is root-writable on most distros, so a naive `>> /var/log/basic-memory-git-sync.log` from a `bm`-owned crontab would fail with "Permission denied" on the first run and the user would never see the error (it's just lost). Provision under `/var/log/` if you want it alongside other system logs (chown required), or under `/var/lib/basic-memory/` if you'd rather keep everything bm-owned in one tree.

```bash
# Provision the log file (one-time, as root) so bm can append to it:
install -o bm -g bm -m 0644 /dev/null /var/log/basic-memory-git-sync.log

# Then install the crontab as the bm user:
sudo -u bm crontab -l 2>/dev/null > /tmp/bm.cron || true
echo '*/5 * * * * /usr/local/bin/basic-memory-commit-and-push.sh >> /var/log/basic-memory-git-sync.log 2>&1' >> /tmp/bm.cron
sudo -u bm crontab /tmp/bm.cron
rm /tmp/bm.cron
```

If you prefer to keep everything bm-owned without touching `/var/log/`, swap the log path for `/var/lib/basic-memory/git-sync.log` (no separate `install` step needed — the parent dir is already bm-owned from §8 setup).

`>>` to a log file replaces the structured `journalctl` view that systemd users get. Rotate the log via the host's standard `logrotate` config — section 9 (day-2 ops) covers this.

The service script is the same in both forms; only the trigger differs.

Include in this subsection a small "verification" block: after applying, write a test note in BM, wait one cadence-window (60s for primary, ~20s for inotify, 5m for fallback), then `cd /var/lib/basic-memory/kb && git log -1 --stat` and confirm the new file appears in the most-recent commit.

- [ ] **Step 3b: Author section 12 — push-failure troubleshooting**

Append the two new failure modes named in the AC. For (4) `git push` failure, the resolution is to inspect `journalctl -u basic-memory-git-sync` for the underlying error and to run the script manually once the cause is fixed; commits accumulated locally during the outage flush automatically. For (5) rebase conflict, the resolution is `cd /var/lib/basic-memory/kb && git status` (which will show the conflicted notes), hand-merge the markdown (notes are append-mostly so conflicts are rare and usually obvious), then `git rebase --continue && systemctl restart basic-memory-git-sync.service`.

Add a sixth common failure mode: **`.git/index.lock` permission denied**. Symptom: `journalctl -u basic-memory-git-sync` shows `fatal: Unable to create '/var/lib/basic-memory/kb/.git/index.lock': Permission denied`. Cause: somebody ran `git init` (or a manual `git` command) as root and left part of the `.git/` tree root-owned. Resolution: `chown -R bm:bm /var/lib/basic-memory/kb && systemctl restart basic-memory-git-sync.service`.

Add a seventh failure mode: **`Host key verification failed`**. Symptom: `journalctl` shows `Host key verification failed.` or `REMOTE HOST IDENTIFICATION HAS CHANGED!`. Cause: either the bm user's `known_hosts` was never provisioned (initial-setup error — see §8 setup), or the git remote rotated its host key. Resolution for first-time setup: run the `ssh-keyscan -H <remote-host> >> /var/lib/basic-memory/.ssh/known_hosts` block from §8 as user `bm`. Resolution for a rotated host key: `sudo -u bm ssh-keygen -R <remote-host>` then re-run `ssh-keyscan`, after confirming with the git provider that the rotation is legitimate (NOT a MITM attack).

- [ ] **Step 4: Cross-check INTEGRATION.md links**

Run: `rg 'basic-memory-shared-vm' INTEGRATION.md README.md`

Expected: links to the new doc from both files (added in Tasks 12 and 13).

- [ ] **Step 5: Commit task 14**

```bash
git add docs/team-setup/
git commit -m "$(cat <<'EOF'
docs(team-setup): add basic-memory shared-VM setup doc

Twelve-section operator-facing doc per spec outline. Section 5
cites BM's remote-MCP transport from the Task 0a verified contract
(Task 0a owns up-front BM verification; this task only renders it).
Section 8 prescribes a git-backed BM data directory via a 60s
systemd timer (primary), an inotify-recursive watcher variant (for
per-edit attribution), and a 5-minute long-cadence fallback. Deploy
key provisioned bm:bm 0600 so the service user can read it.
Section 12 covers push-failure, rebase-conflict, index.lock
permission, and host-key verification modes. Includes per-developer
MCP config snippet, AGPL-3.0 compliance note, and secrets-policy
callout.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 15: Finalize the 0.6.0 CHANGELOG entry

**Goal:** The `## [0.6.0] - 2026-05-19` block in `CHANGELOG.md` accurately reflects the shipped surface.

**Acceptance criteria:**
- `### Added` lists: `prime_project_knowledge`, `extract_project_knowledge`, `project_knowledge` field (on `validate_task_spec` and `validate_plan`), six new finding categories, five env vars (with each var name and its default), five note templates, `docs/team-setup/basic-memory-shared-vm.md`, the new INTEGRATION.md section + dispatch-clause addition, and the README announcement. Do NOT introduce a `### Documentation` section — repo convention (per existing entries in `CHANGELOG.md`) is to keep all doc additions under `### Added`.
- `### Changed` lists the "four tools" → "six tools" rewordings in INTEGRATION.md and README.md from Tasks 12/13, plus any other non-additive surface changes that shipped in passing. Section is omitted only if no `### Changed` entries apply.
- The trailing tracker links at the bottom of `CHANGELOG.md` are unchanged (or extended for 0.6.0 only if the existing pattern includes them).
- `rg '^## \[0\.6\.0\] - 2026-05-19' CHANGELOG.md` returns exactly one match.

**Non-goals:**
- Do not bump `VERSION`. Release automation owns that.
- Do not write release notes longer than the existing 0.4.0 block; the changelog is reader-skimmed.

**Files:**
- Modify: `CHANGELOG.md`

- [ ] **Step 1: Replace the Task 0 stub with the final entry**

Edit the existing `## [0.6.0]` block. The intro stub from Task 0 is a working draft; reconcile it with what actually shipped. In particular:

- Confirm each bullet under `### Added` describes a behavior present in the merged code (not just on the roadmap).
- If any tool / env-var / finding category changed name during implementation, fix it here.
- If a non-additive change snuck in (e.g. a default value tweak), add it under `### Changed`.

- [ ] **Step 2: Verify**

Run: `rg '^## \[0\.6\.0\] - 2026-05-19' CHANGELOG.md`

Expected: one match.

Run: `git diff CHANGELOG.md`

Inspect: the changes match the actual shipped surface.

- [ ] **Step 3: Run full test suite as a final sanity check**

Run: `go test -race ./...`

Expected: PASS.

- [ ] **Step 4: Commit task 15**

```bash
git add CHANGELOG.md
git commit -m "$(cat <<'EOF'
docs(changelog): finalize 0.6.0 entry

Reconciles the Task 0 stub with the shipped surface: two new MCP
tools, project_knowledge field on validate_task_spec / validate_plan,
six new finding categories, five env vars, five note templates,
INTEGRATION.md section, and the basic-memory-shared-vm team-setup doc.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Plan-level reminders

- Run `go test -race ./...` after every task whose AC mentions code, not just at the end. Catching a regression at task boundary is cheaper than at PR time.
- Run `go build ./...` after Tasks 1, 3, 4, 5, 6, 7, 8, 9, 10 — they each touch package surface.
- Do not skip `go test ./internal/prompts/... -update`'s diff review. Golden bloat is a real failure mode.
- Both spec open questions (BM tool names AND BM transport story) are resolved up front in **Task 0a** and recorded in the "Basic Memory contract (verified <date>)" block at the bottom of this plan. Tasks 6, 7, 9, 10, 12, and 14 read that block — they do not re-do the upstream research. If a later task discovers that the verified block is wrong, update the block first (Task 0a step 2), then propagate via Task 0a step 3 before continuing. Do NOT defer BM verification to Tasks 12 or 14: by the time those tasks run, Tasks 6/7/9/10 have already shipped prompts and handlers that quote the verified names, and a late discovery would force rework in already-merged code.
- The third open question — whether `epic_origin` belongs on `module` and `feature` proposals — is deliberately deferred. Ship 0.6.0 with `epic_origin` on `decision` only (YAGNI). Revisit on field evidence.

---

## Basic Memory contract (verified 2026-05-20)

- **Upstream version checked:** v0.21.1 (released 2026-05-16; https://github.com/basicmachines-co/basic-memory/releases/tag/v0.21.1).
- **Verification method:** `gh repo view basicmachines-co/basic-memory`, `gh api /repos/basicmachines-co/basic-memory/releases/latest`, and WebFetch of https://raw.githubusercontent.com/basicmachines-co/basic-memory/main/README.md (2026-05-20).
- **Canonical tool names actually exposed by BM (the subset relevant to this plan):**
  - `search_notes` — search across notes by query string.
  - `read_note` — read a note by permalink.
  - `write_note` — create or replace a note.
  - `edit_note` — partial update (frontmatter patch / append / replace section).
  - `move_note` — rename / relocate a note.
  - `delete_note` — remove a note.
  - Plus: `search`, `recent_activity`, `list_directory`, `build_context`, `canvas`, `read_content`, `view_note`, and project/schema/cloud tools (not used by this plan).
- **Source for the names:** upstream README MCP-tool list (2026-05-20). Verified by WebFetch.

### Supersede mapping

BM does **NOT** ship a `supersede_note` verb (verified 2026-05-20). This plan's logical `Proposal{action: "supersede"}` therefore maps to a **pair** of `bm_commands` entries:

1. `{ "tool": "write_note", "args_json": "{\"permalink\":\"<new>\", \"frontmatter\":{\"status\":\"accepted\", \"supersedes\":[\"<predecessor>\"], ...}, \"body\":\"<new body>\"}" }`
2. `{ "tool": "edit_note", "args_json": "{\"permalink\":\"<predecessor>\", \"frontmatter_patch\":{\"status\":\"superseded\"}}" }`

If the predecessor permalink is missing or the new note carries no body, the reviewer emits an `insufficient_evidence` finding instead of fabricating either command.

### Transport

- The upstream README does NOT prescribe a remote-MCP transport for shared-VM deployments — BM's default invocation is stdio (`basic-memory mcp`).
- **Canonical recommendation for shared-VM deployments: stdio-via-SSH-proxy.** Each developer's Claude Code config invokes `ssh -i <key> bm@<vm-host> basic-memory mcp` to launch a per-session stdio MCP process on the shared VM. This requires no extra transport infrastructure beyond OpenSSH and works against the upstream's default mode. Source: upstream README (no explicit remote-transport guidance; SSH-proxy is the conventional pattern for stdio MCP servers).
- Operators who prefer URL/token-based transport (SSE or streamable-HTTP) can run BM behind a reverse proxy of their choice; that path is out of scope for v0.6.0's team-setup doc and may be revisited if upstream ships a first-class remote transport.

Downstream tasks (6, 7, 9, 10, 12, 14) reference these names and the supersede mapping verbatim. If a future BM release changes anything, update this block first, then propagate.
