# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.7.0] - 2026-05-21

### Added
- New 6th note type `story` under the project-knowledge taxonomy. Frontmatter scoped to ticket-driven workflow (issue ID, parent epic, owners, tracker URL); body provides a live operational dashboard with multi-PR list + relationships, subtasks, deployment state, and decisions produced. Template lands as `examples/project-knowledge/story.md`. Schema enum `proposals[].type` in `internal/verdict/extract_schema.json` gains `"story"`; `ProposalTypeStory` constant added to `internal/verdict/extract.go`. Parser is backwards-compatible — v0.6.x five-type proposals continue to parse.
- New adopter conventions doc at `docs/team-setup/project-knowledge-conventions.md`: when this pattern earns its keep, the one-BM-project-per-repo recommendation (with the monorepo namespacing exception), issue-ID format guidance, folder convention, milestone-event list, project-prefix bootstrap, tracker integration, and maintenance ownership.
- New committed dogfood directory `examples/project-knowledge/dogfood/` with frozen-snapshot real anti-tangent example notes (epics/gh-23, stories/gh-25, decisions/0001-text-only-reviewer, modules/review-pipeline). Re-snapshotted manually on major releases.
- Optional `story_origin` frontmatter field on `decision` notes alongside the existing `epic_origin`. Enables extract to populate a story's `## Decisions produced` section by walking `story_origin` matches across decision notes.

### Changed
- `examples/project-knowledge/epic.md` rewritten with live operational dashboard sections (`## Stories` table with status + deployment, `## Open PRs` table aggregated across stories in the epic, `## Acceptance (epic-level)` checklist). Charter + progress-ledger sections from v0.6.0 kept as supporting context.
- All six note templates adopt the project-prefixed folder-per-ticket permalink shape: `<PROJECT>/<type>/<key>/main`. Cross-references in frontmatter become permalink strings. Backwards-compatible — pre-v0.7.0 extract outputs without the project prefix continue to parse.
- `internal/prompts/templates/extract.tmpl` recognises the `story` type, infers the project prefix from `kb_index` permalinks (falls back to `<PROJECT>` placeholder + emits `missing_index_entry` finding when no prefix can be inferred), and proposes dashboard updates only on milestone events (PR opened, PR state transition, deployment landed, decision finalized) via `replace_section` operation bm_commands.
- `INTEGRATION.md` "Project knowledge (optional)" section gains a one-line mention of the 6-type taxonomy and a link to the new conventions doc. Total file size kept under the 40,000-byte user-instructions threshold.

### Fixed
- `docs/team-setup/basic-memory-shared-vm.md` §8 `commit-and-push.sh` script: `GIT_SSH_COMMAND` now includes `-o IdentitiesOnly=yes -o IdentityAgent=none` alongside the existing `StrictHostKeyChecking=yes`. Without `IdentitiesOnly=yes` SSH tries every key in `~/.ssh/` before the explicit `-i` deploy key, so a key that belongs to a different account can auth first and the BM repo push fails with "Permission denied" or "Repository not found". `IdentityAgent=none` defends against `SSH_AUTH_SOCK` leaking into the systemd unit's environment and the agent's keys overriding the deploy key. Both options are now documented inline next to the script with rationale for each.

### Removed

### Deprecated

### Security

## [0.6.2] - 2026-05-21

### Added
- New subsection in `INTEGRATION.md`'s "Project knowledge (optional)" block titled "Applying bm_commands to BM v0.21.1": short tables mapping extract's emitted `bm_commands` arg shape (`{permalink, frontmatter, body}` / `{permalink, section, content}`) to BM v0.21.1's literal `write_note` / `edit_note` MCP signatures, plus a note on the permalink-slug divergence between anti-tangent's proposed slugs and BM's auto-derived ones. Closes #28.

### Changed
- `INTEGRATION.md` trimmed back under the 40k user-instructions context budget. v0.6.0's "Project knowledge (optional)" section is the primary target: the architecture diagram is dropped in favor of a one-line link to the spec, the anchored BM tool-names list is compressed to a link to the verified-contract block in the v0.6.0 plan, and the auto-apply ladder + controller-workflow prose is tightened. Protocol contracts, env var names, error categories, and field names are preserved verbatim — only prose density and content duplicated with the spec are reduced. Mirrors the v0.5.1 trim's posture.

### Fixed

### Removed

### Deprecated

### Security

## [0.6.1] - 2026-05-21

### Added
- New "Alternative: Docker container on an existing host" section in [`docs/team-setup/basic-memory-shared-vm.md`](docs/team-setup/basic-memory-shared-vm.md): run upstream's `ghcr.io/basicmachines-co/basic-memory:0.21.1` (pinned; bump deliberately) against a host bind-mount, expose its SSE transport via a reverse proxy with per-dev bearer-token auth, reuse the existing git-backed sync (host-side systemd timer against the bind-mount). For teams that already run a Docker host and don't want to provision a dedicated VM.

### Changed

### Fixed
- `validate_completion`'s `malformed_evidence` shape-guard no longer false-positives on Go's `./pkg/...` package-recursion syntax in `test_evidence` strings or test-file contents. The `/...` substring pattern added to `evidenceTruncationPatterns` in v0.5.2 was too aggressive — every other v0.5.2 placeholder in the list is comment-form (`/* ... */`, `// snip`, `// elided`, `// ... rest unchanged`) and unambiguous; the bare `/...` is removed. If a real `/...` truncation pattern surfaces in the field, we'll re-add it with a tighter regex (preceded by a comment marker). Fixes #25.

### Removed

### Deprecated

### Security

## [0.6.0] - 2026-05-20

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
- INTEGRATION.md and README.md "four tools" references updated to "six tools" — the v0.6.0 pair lands on top of the existing four (`validate_plan`, `validate_task_spec`, `check_progress`, `validate_completion`). README's tool-catalog smoke-test assertion and `max_tokens_override` posture extended to all six.
- `prime_handler` now emits one structured `slog.Info` line on every exit path (success / validation_error / payload_too_large / model_resolution_error / render_error / truncated / reviewer_error) via a deferred logger, matching the pattern shipped for `extract_handler`. Previously only the success path logged.
- `prime_schema.json` and `extract_schema.json` add `minLength: 1` on `bm_commands.args_json`, and `extract_schema.json` additionally constrains `proposals.frontmatter_json` — closes the gap at the OpenAI strict-mode layer before the parser-side rejection fires. `body` and `body_patch` remain unconstrained because empty-string placeholders are valid for those fields per the action-conditional parser path.
- The output-schema hint inside `prime.tmpl` and `extract.tmpl` now enumerates the full 17-category vocabulary (was a truncated subset) so the reviewer is not biased away from valid categories like `scope_drift`, `ambiguous_spec`, or `convention_deviation`.

### Fixed

### Removed

### Deprecated

### Security

## [0.5.2] - 2026-05-19

### Added

- New finding category `attestation_contradiction` (NOT severity-floored — distinct from `convention_deviation` / `unverifiable_codebase_claim`). Emitted by the reviewer when an acceptance criterion explicitly contradicts a caller-attested harness shape; see `harness_shape_attestation` input below. Added to all four reviewer-output JSON schemas and to the parser's `validCategory` allowlist.
- `validate_task_spec` accepts a new optional `harness_shape_attestation` input: a list of `{harness, path, assertions[]}` objects declaring caller-attested shape facts about test harnesses or fixtures. Caps: ≤ 25 entries; harness/path ≤ 240 code points; ≤ 10 assertions each ≤ 480 code points; whitespace-trim + canonical-JSON dedup. Threads through the session and into the pre-hook prompt for reviewer rendering (see Task 15 / pre.tmpl).
- `verdict.FinalizeVerdict(Result) Result` derives the canonical verdict from finding-severity counts via a published ladder: `critical >= 1 OR major >= 2 → fail`; `major >= 1 OR minor >= 3 → warn`; otherwise `pass`. When the `minor >= 3 → warn` branch fires (no critical/major), an advisory `noise_cluster` finding (`severity: minor`, `category: other`, `criterion: noise_cluster`) is appended so callers can see why. Idempotent.
- `verdict.FinalizePlanVerdict(*PlanResult)` derives per-task verdicts via the same severity ladder, derives the plan-level verdict from `PlanFindings`, appends noise_cluster advisories at task and plan level where applicable, and re-runs `ApplyPlanQualitySanity` so `plan_quality` stays consistent with the server-derived `plan_verdict`. Idempotent. Nil-safe.

### Changed

- `README.md` lists `harness_shape_attestation` alongside the existing optional `validate_task_spec` inputs.
- Reviewer is now instructed to demote `major ambiguous_spec` findings to `minor` when a normative test body explicitly pins the ambiguous value/assertion. Demoted findings carry a `(resolved-by-normative-body: <citation>)` suffix on `suggestion` so callers can see why. Instruction lands in both `pre.tmpl` and `post.tmpl`.
- `pre.tmpl` now instructs the reviewer to emit a `minor ambiguous_spec` finding citing INTEGRATION.md §3.7 when plan text contains `.trimIndent()` / `.trimMargin()` / `textwrap.dedent` / tagged-template `dedent` alongside a multi-line string literal comparison.
- Per-task handlers (`validate_task_spec`, `check_progress`, `validate_completion`) now derive `verdict` server-side via `FinalizeVerdict` AFTER suppression/rollup AND after the clamp finding is folded into the result, so `max_tokens_override` clamps participate in the severity ladder. The per-task no-recovery truncation finding is bumped from `minor` to `major` so the ladder derives `warn` consistently with the previously-explicit assignment.
- Hard-rejection synthetic findings (`payload_too_large` in both per-task and plan-level paths, `malformed_evidence`) bumped from `major` to `critical` so the verdict ladder derives `fail` consistently with the envelopes' explicit `Verdict: fail`. `session_not_found` was already `critical` and is unchanged.
- `validate_plan` derives per-task and plan-level verdicts server-side via `FinalizePlanVerdict`, which slots into the existing `finalizePlanResult` pipeline after unverifiable-rollup and calibration. The plan-level `max_tokens_override` clamp now participates in the severity ladder. The plan-level no-analysis truncation finding remains `major` (already was — confirmed by regression test).
- `controller_verified_references` suppression for `unverifiable_codebase_claim` findings now runs server-side (deterministic Go-side) in addition to the existing reviewer-prompt instruction. Suppression scope is per-claim: any CVR-entry substring match against the finding's `evidence` or `criterion` (either direction) suppresses the entire finding. 4-code-point floor on CVR entries prevents single-letter false matches.
- `pre.tmpl` CVR-suppression instruction now includes a worked multi-symbol example, mirroring the Go-side `suppressUnverifiableCodebaseClaim` semantics.
- `pre.tmpl` gains a `## Harness shape attestations` section (rendered only when `harness_shape_attestation` is non-empty) and instructs the reviewer to emit `attestation_contradiction` findings ONLY for explicit AC-vs-attestation contradictions (not for absent capabilities).
- `validate_completion` now sees `normative_test_bodies` from the session at post-hook time. `post.tmpl` renders a `## Normative test bodies (binding)` section that instructs the reviewer to treat the bodies as authoritative for fixture state, exact strings, and assertions; AC-vs-fixture mismatches are suppressed when a body pins the value. Lightweight mode (empty `session_id`) is unaffected — no session, no bodies, no section.
- `INTEGRATION.md` documents `harness_shape_attestation` (§3.8 + §4.2 args list), the `attestation_contradiction` finding category (§6 FAQ), the deterministic server-side CVR suppression (§5.7), and adds the `check_progress` trigger nudge ("test that 'should' fail doesn't" / ">5 min debugging") to both §4 lifecycle table and §4.2 paste-clause "During work" step.

### Fixed

- `validate_completion` `malformed_evidence` shape-guard extended with six new placeholder/truncation patterns observed in the field: `/* ... */`, `/* ...rest unchanged */`, `// snip`, `// elided`, `// ... rest unchanged`, `/...`. Each is matched (case-insensitive substring) against BOTH `final_diff` AND every `final_files[].content`.

### Removed

### Deprecated

### Security

## [0.5.1] - 2026-05-19

### Added

### Changed
- `INTEGRATION.md` trimmed for the 40k user-instructions context budget: §2 Setup (install / register / provider keys / model split / smoke test) removed in favor of `README.md`, which gains a new `### Picking a reviewer model` subsection (the implementer→reviewer mapping table) and a `### Smoke test` one-liner. `INTEGRATION.md` opens with a one-line cross-reference to `README.md` for install/configure and is now scoped strictly to using-the-MCP protocol.
- `INTEGRATION.md` §3 trimmed: §3.4 "Mapping to existing plan-writers" removed (the header-block + Files/Steps pattern is documented in §3.1 and applies across plan-writers without per-tool guidance); §3.2 worked-example trailing prose dropped — §3.3 covers what `validate_task_spec` checks.
- `INTEGRATION.md` §4 consolidated: the line-314 lightweight callout AND §4.1 protocol summary collapsed into one short preamble under the §4 H2; §4.2a (short dispatch shape) and §4.2b (language-scoping caveat) folded inline as notes within §4.2; CodeScene companion subsection trimmed to its complementary-scope rationale + tool-to-phase mapping + advisory-posture / lightweight-mode notes (consumer setup links delegated to upstream); §4.4 Concrete examples deleted in full — Example A's lesson is covered by §3.2/§3.3, Example B by §5.4, and Example C by §6 FAQ.
- `INTEGRATION.md` §5 tightened: §5.2 dispatch-addendum collapsed from 4 paragraphs + per-skill bullets to a single paragraph; §5.6 and §5.7 merged into a single `### 5.6 Per-call tool args and partial-response handling` subsection (covering `max_tokens_override`, `mode`, and `partial: true`); former §5.8 renumbered to §5.7 and the two paragraphs duplicating §5.6 / §6 FAQ content removed.
- `INTEGRATION.md` §3.6 (normative test bodies) and §3.7 (`.trimIndent()` caveat) compressed by ~60% — protocol surface is preserved (marker shape, server-side extraction, 4000-code-point cap, `// excerpt:` escape hatch, one-source-line + render-aware-AC rules); explanatory prose dropped. §6 FAQ trimmed by removing three entries that fully duplicate other sections (plan-handoff gate failure → §5.1; reviewer-is-wrong → §4.3; ad-hoc code changes → §1). Final `INTEGRATION.md` size: 33,186 chars (was 50,757; under the 40,000 user-instructions warning threshold by 6,814 chars).

### Fixed
- `validate_plan` failed with OpenAI provider HTTP 400 (`Invalid schema for response_format 'review': … Missing 'exit_contracts'`) whenever the reviewer was actually invoked. Root cause: OpenAI structured-outputs `strict: true` requires every property in a JSON-schema object to appear in `required`. The v0.5.0 task-items schema declared `exit_contracts` / `exit_contracts_inferred` (and v0.4.0 had earlier added `lightweight_eligible` / `lightweight_reason`) as optional `properties` without listing them in `required`. Both `plan_schema.json` and `tasks_only_schema.json` patched; a new `internal/verdict/schema_invariants_test.go` regression test asserts every property must be in `required` across all four reviewer-output schemas so the class of bug cannot recur silently. Anthropic and Google providers were not impacted (they don't enforce strict-mode at the request layer).

### Removed

### Deprecated

### Security

## [0.5.0] - 2026-05-18

### Added
- New finding category `convention_deviation` (minor-floored) emitted when a `codebase_conventions` entry conflicts with the spec. Added to the reviewer-output JSON schema category enums.
- `validate_task_spec` accepts optional `test_strategy_notes`, `codebase_conventions`, `testability_extractions`, and `normative_test_bodies` so controllers can surface joint-coverage intent, module conventions, intentional testability extractions, and binding test bodies that the structured-fields-only spec otherwise hides from the reviewer.
- `validate_plan` task results include optional `normative_test_bodies`, populated server-side by deterministic markdown extraction of `**NORMATIVE TEST BODIES (verbatim):**` sections from each task's plan markdown.
- `validate_plan` task results include optional `exit_contracts` (hybrid: explicit `**Exit contracts:**` section if present, reviewer-inferred otherwise) with a sibling `exit_contracts_inferred` provenance flag.
- `validate_completion` accepts optional `exit_contracts` plus `exit_contracts_inferred`; reviewer flags misses as `missing_acceptance_criterion` with `criterion: exit_contract`, calibrating miss severity by provenance.

### Changed
- `pre.tmpl` treats `normative_test_bodies` as binding AC, treats adjacent complementary tests as joint coverage when `test_strategy_notes` explains the split, emits `convention_deviation` findings on observed deviations from `codebase_conventions`, and respects `testability_extractions` when judging scope drift.
- `validate_task_spec` deterministically suppresses reviewer-emitted `scope_drift` findings whose evidence names a caller-supplied `testability_extractions` entry (substring match in either direction).
- `plan.tmpl` and `plan_tasks_chunk.tmpl` ask the reviewer to populate `exit_contracts` and `exit_contracts_inferred` per task. `plan.tmpl` also notes that `normative_test_bodies` is populated server-side and must not be reviewer-emitted.
- `post.tmpl` renders a provenance-aware `Exit contracts (...)` section when `exit_contracts` is non-empty and instructs the reviewer to walk each contract against final-file evidence.
- Integration docs add the normative-test-bodies convention, CVR-scope clarification (single-category suppression; `convention_deviation` not suppressed), `.trimIndent()` raw-string caveat, language-scoping prose caveat, and a lightweight-mode callout at the top of the implementer section. (Doc-only items folded under `### Changed` per project CLAUDE.md convention on Keep-a-Changelog subsections; v0.4.0 used `### Documentation`, which is a divergence — this release re-aligns.)
- README ships a one-shot paste-in install prompt for Claude Code and opencode under `## Install`. The prompts fetch the latest release, place the binary in `~/.local/bin`, register the MCP at user scope, download `INTEGRATION.md` to the host's user-instructions dir, and wire it into `~/.claude/CLAUDE.md` (Claude Code) or opencode.json's top-level `instructions` array (opencode, per INTEGRATION.md). Linux/macOS scope; secrets-redaction directive included. The opencode prompt defaults to `{env:NAME}` substitution for the reviewer API key (with `{file:path}` and literal-value paths offered as alternatives) so the secret never has to be written into `opencode.json` by default.

### Fixed

### Removed

### Deprecated

### Security

## [0.4.0] - 2026-05-17

### Added
- `validate_task_spec` accepts optional `controller_verified_references` entries so controllers can identify codebase references they already grep-verified before dispatch.
- `validate_plan` task results include optional `lightweight_eligible` and `lightweight_reason` fields to guide controller-side lightweight dispatch decisions.
- `validate_plan` caches identical passing plan reviews in memory for 3 minutes, returning cached hits with `review_ms: 0` and a `[cached <=3m]` `next_action` prefix.

### Changed
- `validate_task_spec` rolls multiple per-task `unverifiable_codebase_claim` findings into one `codebase_reference_checklist` finding.
- `validate_completion` prompts now include prior major pre-task findings so reviewers can check whether the implementation mitigated them.
- `validate_task_spec` prompt guidance is tuned for test-only tasks to reduce repeated low-value `null`/`unchanged` ambiguity findings while preserving invocation-count and negative-assertion critiques.

### Documentation
- Integration docs clarify `pinned_by` vs `context` vs `controller_verified_references`, shorten the target dispatch clause, and make CodeScene's deterministic mid-task safeguard recommended when configured.

## [0.3.3] - 2026-05-14

### Added
- `validate_task_spec` accepts optional `pinned_by` entries naming existing tests, docs, commands, or static checks that pin behavior, plus optional `phase` (`pre` default, `post` for post-hoc/session-recovery reviews).
- `validate_completion` prompts now highlight summary-referenced doc/artifact paths that are missing from `final_files` and `final_diff` evidence.

### Changed
- `validate_plan` now scales its default output-token budget by task count when no `max_tokens_override` is supplied, bounded by `ANTI_TANGENT_MAX_TOKENS_CEILING`.
- No-analysis `validate_plan` truncation responses now emit a `warn` envelope with a `major` finding and self-contained retry guidance.
- Task-level `unverifiable_codebase_claim` findings from `validate_plan` are rolled up into a single plan-level `codebase_reference_checklist` finding.
- Plans whose only findings are minor `unverifiable_codebase_claim` checklist items now return `plan_verdict: pass` with `plan_quality: actionable` (preserving `rigorous` when the reviewer already emitted it).

### Fixed
_None._

### Removed
_None._

### Deprecated
_None._

### Security
_None._

### Documentation
- New `INTEGRATION.md` sections: `### Reducing text-only review noise` (caller discipline learned from YN-10178), `### Using v0.3.3 review-context features` (`pinned_by` / `phase` / adaptive-plan retry / completion-evidence selection examples), and a setup checklist under the existing CodeScene companion section.
- New `### validate_task_spec arguments (v0.3.3+)` subsection in `README.md` plus two paragraphs in the `validate_plan` section covering the adaptive budget and unverifiable-rollup behavior.

## [0.3.2] - 2026-05-13

### Added
- Documentation for [CodeScene MCP](https://github.com/codescene-oss/codescene-mcp-server) as the recommended optional companion. Anti-tangent is text-only by design; CodeScene's deterministic Code Health analysis closes the codebase-grounded blind spot. New `### CodeScene MCP companion (optional)` section in `INTEGRATION.md` covers tool-to-phase mapping (`pre_commit_code_health_safeguard` mid-task, `analyze_change_set` before DONE), advisory posture, and lightweight-mode interaction. `README.md` gains an attribution + overview section.

### Changed
- Dispatch-clause template in `INTEGRATION.md` gains optional Step 2b (`pre_commit_code_health_safeguard` mid-task) and Step 3b (`analyze_change_set` before DONE). Both gated on "if codescene-mcp is configured in your host" — silent skip when absent. Anti-tangent itself is unchanged; the integration lives at the convention layer.
- `examples/lightweight-dispatch.md` notes that lightweight tasks skip the CodeScene companion calls too.

### Fixed
_None._

### Removed
_None._

### Deprecated
_None._

### Security
_None._

### Documentation
- New `### Companion tool: CodeScene MCP (optional)` section in `README.md` attributes [CodeScene](https://codescene.com) and describes the pairing.

Closes [#14](https://github.com/patiently/anti-tangent-mcp/issues/14).

## [0.3.1] - 2026-05-13

### Added
- `summary_block` field on every tool response: paste-ready textual envelope (verdict, findings, model_used, review_ms, session_ttl_remaining_seconds) that implementers can copy verbatim into DONE reports. Reduces the protocol's reliance on the implementer correctly formatting JSON.
- `plan_quality` field on `PlanResult` (`rough` | `actionable` | `rigorous`). Separate axis from `plan_verdict` — tracks "how close to ship-ready" rather than "is this dispatchable." Reviewer-emitted with a server sanity check (critical findings or `fail` verdict force `rough`; missing/invalid values fall back to verdict-based default).
- `unverifiable_codebase_claim` finding category: lets the reviewer explicitly flag plan or task-spec statements it cannot verify from text alone (field names, signatures, file paths, repo conventions) rather than silently passing or fabricating critiques. Server enforces `severity: minor` for this category. Applies to `validate_plan` and `validate_task_spec` (both text-only inputs); not applied to `check_progress` / `validate_completion` which receive code.
- `malformed_evidence` finding category: the new `validate_completion` evidence-shape guard rejects submissions that contain truncation markers (`(truncated)`, `[truncated]`, `// ... unchanged`, etc.) or empty `final_files.Path` entries — saves strong-model time on cycles that were driven by tooling friction rather than correctness. Replaces the (misleading) previous reuse of `payload_too_large` for shape failures. Note: if the file you're submitting legitimately contains one of these literal strings, send a complete `final_diff` instead of pasting the file via `final_files`.
- `examples/lightweight-dispatch.md` reference template for trivial tasks (doc edits, mechanical relocations).

### Changed
- `check_progress` demoted from RECOMMENDED to OPTIONAL in the dispatch-clause template. Field data showed 0 substantive catches across 5 representative tasks; the call is now advisory.
- `validate_completion` rejected-submissions are cached for 5 minutes by canonical content hash to short-circuit identical re-submissions (see the new `malformed_evidence` category above).
- `validate_completion` now accepts an empty `session_id` when `final_files`, `final_diff`, or `test_evidence` is non-empty — supports the new lightweight protocol mode. The reviewer is called with a synthesized task spec (Goal = `args.Summary`, no ACs).
- `summary_block` population moved to the marshalling helpers (`envelopeResult` / `planEnvelopeResult`) so every exit path — happy paths, partial-recovery, legacy-truncation, `notFoundEnvelope`, `tooLargeEnvelope`, `noHeadingsPlanResult`, evidence-shape rejection — populates the field automatically.

### Fixed
_None._

### Removed
_None._

### Deprecated
_None._

### Security
_None._

### Documentation
- New `## Scope and limits` section in `INTEGRATION.md` explicitly documents the text-only architectural boundary: what the tool catches, what it structurally cannot (codebase symbol existence, function signatures, repo-wide invariants encoded elsewhere, CI/test policy), and the recommendation to pair with a codebase-aware review for any plan that lands in real code.
- New `### Lightweight protocol mode` section in `INTEGRATION.md` documents the controller-side convention for trivial tasks.

Closes [#12](https://github.com/patiently/anti-tangent-mcp/issues/12).

## [0.3.0] - 2026-05-12

### Added
- `max_tokens_override` optional arg on all four tools (`validate_task_spec`, `check_progress`, `validate_completion`, `validate_plan`) for per-call control over the reviewer's output-token budget. Clamped to `ANTI_TANGENT_MAX_TOKENS_CEILING` (default 16384); over-ceiling values emit a `minor` clamp finding so the behaviour is visible. Negative values are rejected at the handler boundary.
- `mode: "quick" | "thorough"` optional arg on `validate_plan`. `quick` instructs the reviewer to surface at most 3 most-severe findings per scope (plan-level and each task) and omit stylistic nits; `thorough` (default) preserves prior behavior. Invalid values are rejected at the handler boundary.
- `partial: true` field on `Result` and `PlanResult` envelopes when the reviewer's response was truncated at the `max_tokens` cap but partial findings could be recovered. Marshaled with `omitempty` so the field is absent in the common (non-truncated) case.
- Hypothetical-marker guardrail (`e.g. illustrative —` prefix) added as a 4th paragraph in the `## Reviewer ground rules` block in `validate_plan` templates, complementing the 0.2.1 epistemic-boundary work.
- `next_action` specificity nudge in `validate_plan` templates: the field must name the single highest-leverage finding, not generic advice.
- `ANTI_TANGENT_MAX_TOKENS_CEILING` env var (default 16384) caps the per-call `max_tokens_override` value.

### Changed
- The synthetic truncation finding emitted on `max_tokens` cap hits is now `severity: minor` (was `major`), with wording that references both the env-var and `max_tokens_override` mitigations.

### Fixed
- Reviewer-output truncation no longer discards complete findings produced before the cap hit. All four tools now run truncated responses through a tolerant JSON parser and emit any recoverable findings alongside a downgraded (`minor`) truncation marker. Previously, ~9 KB of plan input could yield zero usable feedback when the reviewer's output cap was reached mid-response. Closes [#10](https://github.com/patiently/anti-tangent-mcp/issues/10).

### Removed
_None._

### Deprecated
_None._

### Security
_None._

## [0.2.1] - 2026-05-12

### Changed
- `validate_plan` prompt templates (`plan.tmpl`, `plan_findings_only.tmpl`, `plan_tasks_chunk.tmpl`) now include a `## Reviewer ground rules` block that pins the reviewer's epistemic horizon to the plan text — no claims about behavior of code symbols the reviewer cannot see. `unstated_assumption` findings are constrained to assumption gaps visible in the plan itself, and every finding's `evidence` field must point at plan text (present or expected-but-absent). Closes [#8](https://github.com/patiently/anti-tangent-mcp/issues/8).

## [0.2.0] - 2026-05-12

### Added
- `validate_completion` accepts optional `final_diff` evidence for unified diffs.
- Stateful hook envelopes include optional `session_expires_at` and `session_ttl_remaining_seconds`.
- Reviewer-response truncation is detected and surfaced as structured findings with max-token retry guidance.

### Changed
- **(breaking)** `validate_completion` now requires at least one of `final_files`, `final_diff`, or `test_evidence` to be non-empty. Summary-only completion requests are rejected with `validate_completion: at least one of final_files, final_diff, or test_evidence must be non-empty`. Migration: include test command output in `test_evidence` (smallest path), a unified diff in `final_diff`, or full files in `final_files`. Rationale: the reviewer prompt rewrite grades against concrete evidence; summary text alone caused the over-firing pattern in #6 §3.
- Default `ANTI_TANGENT_REQUEST_TIMEOUT` is 180s.
- Timeout errors include the configured timeout and `ANTI_TANGENT_REQUEST_TIMEOUT`.
- Invalid model override errors list supported models for the selected provider.
- `validate_completion` review guidance grades `final_files` / `final_diff` / `test_evidence` (not the `summary`), treats the task spec's `Context:` block as authoritative when it disambiguates an AC, and biases ambiguous-but-fully-covered evidence toward `verdict: pass` with a `category: quality` finding while reserving `severity: major`/`critical` for affirmative contradictions or for an AC left unaddressed.
- `validate_plan` chunk prompts ask reviewers to echo the `Task N:` prefix verbatim.
- Payload-too-large findings include tool-specific retry suggestions (`final_diff`-or-split for `validate_completion`; smaller `changed_files`-or-split for `check_progress`).

### Fixed
- Chunked `validate_plan` identity reconciliation accepts task titles when reviewers strip the `Task N:` prefix while still rejecting wrong or duplicate tasks.

### Removed

_None._

### Deprecated

_None._

### Security

_None._

## [0.1.4] - 2026-05-11

### Added
- `validate_plan` now automatically chunks large plans so reviewer responses don't truncate mid-JSON. Plans with more than `ANTI_TANGENT_PLAN_TASKS_PER_CHUNK` tasks (default 8) are reviewed via one Pass-1 plan-findings call plus `ceil(n/N)` per-chunk calls; the merged `PlanResult` is identical in shape to the single-call path. Plans of 8 tasks or fewer take the existing single-call path unchanged.
- Three new optional env vars: `ANTI_TANGENT_PER_TASK_MAX_TOKENS` (default 4096) governs output budget for `validate_task_spec` / `check_progress` / `validate_completion`; `ANTI_TANGENT_PLAN_MAX_TOKENS` (default 4096) governs output budget for `validate_plan` (single-call and per-chunk); `ANTI_TANGENT_PLAN_TASKS_PER_CHUNK` (default 8) sets both the chunking threshold and per-chunk task count. All three reject zero / negative / non-integer values at startup.
- Per-chunk identity validation: the chunked path verifies every returned `task_title` matches one of the requested chunk's headings (no duplicates, exact count). Mismatch triggers the existing retry-once path; second failure surfaces as an error rather than partial results.
- Gated e2e test `TestValidatePlan_E2E_LargePlanChunked` (build tag `e2e` + `ANTI_TANGENT_E2E_LARGE=1`) exercising the chunked path against a live OpenAI reviewer with a 25-task plan.

### Fixed
- `validate_plan` returning `decode plan result: EOF` on plans of ~12+ tasks. Root cause was a hardcoded `MaxTokens: 4096` cap that the reviewer's JSON response was overflowing on dense plans; both the cap is now configurable and the chunking path keeps each individual response well within budget.

## [0.1.3] - 2026-05-10

### Added
- `google:gemini-3.1-pro-preview` and `google:gemini-3.1-flash-lite` to the reviewer-model allowlist (verified via the Gemini `models.list` endpoint as supporting `generateContent`).
- `openai:gpt-5.5` and `openai:gpt-5.4-mini` (bare-name aliases that route to the latest dated snapshot). Verified live against `/v1/chat/completions` with `response_format: json_object`. The dated `gpt-5.5-2026-04-23` and `gpt-5.4-mini-2026-03-17` entries remain for callers who want pinned snapshots.
- README and `INTEGRATION.md`: opencode (`~/.config/opencode/opencode.json`) registration example, and a "Supported reviewer models" table grouped by provider so callers can see what `ANTI_TANGENT_*_MODEL` accepts at a glance.

## [0.1.2] - 2026-05-10

### Fixed
- Release workflow: write the release-notes file to `$RUNNER_TEMP` instead of the checkout directory. The previous path (`.release-notes.md` in the work tree) made GoReleaser see a dirty git state and refuse to publish. Moving the file outside the work tree keeps the tree clean and lets GoReleaser run end-to-end without `--skip=validate`.

## [0.1.1] - 2026-05-10

### Added 
- Extending .gitignore with claude droppings
- Fixing release task 

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
- `validate_plan` MCP tool — plan-level handoff gate that reviews an entire implementation plan in one call and proposes ready-to-paste structured-header blocks (Goal / Acceptance criteria / Non-goals / Context) for tasks that lack them. Replaces the per-task plan-handoff loop.
- `ANTI_TANGENT_PLAN_MODEL` env var — overrides the model used by `validate_plan`. Defaults to `ANTI_TANGENT_PRE_MODEL`.
