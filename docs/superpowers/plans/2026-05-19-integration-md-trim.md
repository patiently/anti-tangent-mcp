# INTEGRATION.md trim Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Trim `INTEGRATION.md` from 50,757 chars to ≤ 34,000 chars (~6 KB buffer under Claude Code's 40,000 user-instructions warning threshold) without losing load-bearing protocol content for any of the three audiences (plan authors, controllers, implementers).

**Architecture:** Pure editorial trim plus one small cross-doc migration. Section §2 Setup (~6.7k chars) moves to README — the README already has install/configure/provider keys/supported-models; only the implementer→reviewer model-split mapping and the smoke-test one-liner are missing from README, so those get migrated before §2 is deleted. §3, §4, §5 trims fold redundant sub-sections and remove an examples block that overlaps §3.2/§3.3. Task 5 finishes the job with two compression passes (§3.6 + §3.7 niche caveats; §6 FAQ entries that duplicate other sections). Each task is a separate commit on `version/0.5.1`.

**Tech Stack:** Markdown only. Verification is `wc -c INTEGRATION.md`.

---

## Execution notes (for the future executor)

- **Branch:** `version/0.5.1`. CHANGELOG must carry a `## [0.5.1] - 2026-05-19` heading by the end of Task 1 — CI enforces branch-vs-CHANGELOG match.
- **Anti-tangent protocol applies, but lightweight mode is the right choice for every task here.** All five tasks are doc-only, mechanical, with the exact diff/insertion shape pinned in this plan. Each task calls `validate_completion` as a sanity gate before the per-task commit; `validate_task_spec` and `check_progress` are skipped per the lightweight-mode criteria.
- **Use `final_files` (full content), not `final_diff`, for the completion gate.** The trimmed `INTEGRATION.md` itself says (line 41 in the pre-trim file): "For doc deliverables, submit full content via `final_files`; diffs or prose summaries are often insufficient evidence." This is the project's own guidance. Each task's `validate_completion` call passes the FULL post-edit content of every touched file in `final_files` and uses `test_evidence` for the `wc -c` byte-count check. The 200 KB payload cap (default `ANTI_TANGENT_MAX_PAYLOAD_BYTES`) easily accommodates `INTEGRATION.md` (≤ 50 KB) + `README.md` (~13 KB) + `CHANGELOG.md` (~20 KB).
- **No `git push` and no `git merge` in this plan.** Work lands as commits on `version/0.5.1`; merge is a separate human decision after the branch is complete.
- **Char-count discipline.** After each task, run `wc -c INTEGRATION.md` and confirm progress toward the ≤ 34,000 target (interim ≤ 35,500 after Task 4; final ≤ 34,000 after Task 5). Report the running number in the commit body.
- **Cross-reference cleanup.** Section numbers get referenced inside the doc (e.g. "see §4.2", "see §5.1"). After each task, grep for orphan references to any section header you removed or renamed and either update or delete them.
- **README cross-link.** After Task 1, the trimmed INTEGRATION.md should open with a one-line pointer like "Install and configure: see [`README.md`](README.md). This document covers the using-the-MCP protocol." Do not duplicate install content.

---

## File structure

Modified files:

- `INTEGRATION.md` — primary subject; trimmed in Tasks 1–5
- `README.md` — receives migrated content in Task 1 (model-split mapping table + smoke-test one-liner)
- `CHANGELOG.md` — `## [0.5.1] - 2026-05-19` block added in Task 1; subsequent tasks append bullets under `### Changed`

No new files. No code or test changes.

---

## Char budget

Budget arithmetic uses **measured** section sizes (see `Measurements` block below), not estimates.

| Phase | Starting size | Target cut | Resulting size |
|---|---:|---:|---:|
| Pre-trim | 50,757 | — | 50,757 |
| After Task 1 (§2 removed, README migration) | 50,757 | ~6,500 | ~44,300 |
| After Task 2 (§3.4 + §3.2 prose trim) | ~44,300 | ~600 | ~43,700 |
| After Task 3 (§4 consolidations, §4.4 fully removed) | ~43,700 | ~6,300 | ~37,400 |
| After Task 4 (§5 tightening) | ~37,400 | ~1,600 | ~35,800 |
| After Task 5 (§3.6/§3.7 compression + §6 FAQ trim) | ~35,800 | ~2,330 | ~33,470 |

Target: ≤ 34,000 chars (~6 KB buffer under the 40,000 threshold). If after Task 5 you have additional appetite for cuts, the Task 5 Step 5 fallback ("Choosing `pinned_by`, `context`, `controller_verified_references`" preamble compression) gets another ~870 chars; otherwise stop at the post-Task-5 size.

### Measurements (chars, pre-trim)

- §4.1 protocol summary: 1,327
- §4.2a short dispatch shape: 690
- §4.2b language-scoping caveat: 787
- §4 preamble lightweight callout (line 314): 857
- CodeScene companion "Enabling + feedback drafts" tail (lines 453–475): 1,149
- §4.4 Concrete examples (all three, Examples A+B+C): 3,092
- §3.6 normative test bodies: 1,639
- §3.7 `.trimIndent()` caveat: 1,006
- §6 FAQ: 4,831
- §5.2 dispatch addendum: 965
- §5.8 review-context features: 2,098

---

## Task 1: Migrate model-split content to README, remove §2 Setup from INTEGRATION.md

**Files:**
- Modify: `README.md` — add model-split mapping table + smoke-test note under `## Configure`
- Modify: `INTEGRATION.md` — delete lines 71–214 (entire `## 2. Setup` block including the `---` separator at line 214); add one-line cross-reference to README at the top of the doc
- Modify: `CHANGELOG.md` — add `## [0.5.1] - 2026-05-19` block with first bullet

- [ ] **Step 1: Add the model-split mapping table to README**

Locate the `## Configure` section in `README.md`. Add a new subsection at the END of `## Configure` (immediately before `### Large plans (chunking)`):

```markdown
### Picking a reviewer model

The reviewer LLM should not be the same model as the implementer. Same model + same training data ≈ same blind spots, which defeats the point.

| If your implementer is… | Set `ANTI_TANGENT_*_MODEL` to… |
|---|---|
| Anthropic Claude (Sonnet/Opus) | `openai:gpt-5` and/or `google:gemini-3.1-pro-preview` |
| OpenAI GPT-5 family | `anthropic:claude-sonnet-4-6` and/or `google:gemini-3.1-pro-preview` |
| Google Gemini | `anthropic:claude-sonnet-4-6` and/or `openai:gpt-5` |

The mid-hook (`check_progress`) is called more often — a fast/cheap tier there is fine. The plan-level hook (`validate_plan`) reasons over the whole plan in one shot — give it a strong tier. `ANTI_TANGENT_PLAN_MODEL` falls back to `ANTI_TANGENT_PRE_MODEL` if unset.
```

- [ ] **Step 2: Add the smoke-test one-liner to README**

In `README.md`, immediately after the `### Picking a reviewer model` block from Step 1, add:

```markdown
### Smoke test

Launch your MCP host with debug logging on and confirm all four tools — `validate_plan`, `validate_task_spec`, `check_progress`, `validate_completion` — appear in the discovered tool catalog. Server-side configuration errors print to stderr at startup.
```

- [ ] **Step 3: Add the cross-reference line to the top of INTEGRATION.md**

In `INTEGRATION.md`, insert a new line immediately after line 3 (the preamble paragraph that ends with "...for the authoritative design.") as a new paragraph:

```markdown

**Install and configure:** see [`README.md`](README.md). This document covers the using-the-MCP protocol.
```

- [ ] **Step 4: Delete the §2 Setup block from INTEGRATION.md**

Delete the entire block from line 71 (`## 2. Setup`) through line 214 inclusive (the `---` separator that precedes `## 3. For plan authors…`). After the delete, the line that previously read `## 3. For plan authors — the anti-tangent-friendly task format` should be immediately preceded by the `---` separator that closed §1.

Verify the delete with: `grep -n "^## 2\." INTEGRATION.md` — expected: no match.

- [ ] **Step 5: Sweep for orphan references to §2.x**

Run: `grep -n -E "§ ?2\.|\(see §2|see Setup|see ## 2" INTEGRATION.md`

For each match, decide:
- If the reference points at install/config: rewrite to point at `README.md` (e.g. "see [`README.md`](README.md) §Configure").
- If the reference is incidental and the link no longer adds anything: delete the parenthetical.

Re-run the grep to confirm zero matches before moving on.

- [ ] **Step 6: Char-count checkpoint**

Run: `wc -c INTEGRATION.md`
Expected: ~44,000 chars (was 50,757; cut ~6,700).

If the result is materially above 45,000, something failed to delete — re-check Step 4.

- [ ] **Step 7: Open the v0.5.1 CHANGELOG entry**

In `CHANGELOG.md`, insert the new release block immediately above `## [0.5.0] - 2026-05-18`:

```markdown
## [0.5.1] - 2026-05-19

### Added

### Changed
- `INTEGRATION.md` trimmed for the 40k user-instructions context budget: §2 Setup (install / register / provider keys / model split / smoke test) removed in favor of `README.md`, which gains a new `### Picking a reviewer model` subsection (the implementer→reviewer mapping table) and a `### Smoke test` one-liner. `INTEGRATION.md` opens with a one-line cross-reference to `README.md` for install/configure and is now scoped strictly to using-the-MCP protocol.

### Fixed

### Removed

### Deprecated

### Security

```

- [ ] **Step 8: Lightweight `validate_completion` sanity gate**

Call `validate_completion` with:
- `session_id`: `""` (empty — lightweight mode)
- `summary`: `"INTEGRATION.md §2 Setup removed (~6.5k chars net); model-split mapping + smoke test migrated to README; cross-reference line added at top of INTEGRATION.md; v0.5.1 CHANGELOG block opened."`
- `final_files`: `[{Path: "INTEGRATION.md", Content: <full post-edit content>}, {Path: "README.md", Content: <full post-edit content>}, {Path: "CHANGELOG.md", Content: <full post-edit content>}]`
- `test_evidence`: `"wc -c INTEGRATION.md output: <paste actual byte count>"`

Per INTEGRATION.md's own guidance for doc deliverables, pass full file content; do NOT use `final_diff`. If verdict is `fail` or contains `critical` / `major` findings, address before committing.

- [ ] **Step 9: Commit**

```bash
git add INTEGRATION.md README.md CHANGELOG.md
git commit -m "docs: migrate §2 Setup to README; open v0.5.1 CHANGELOG

INTEGRATION.md drops the entire §2 Setup block (install, register, provider
keys, model split, smoke test) in favor of README, which gains a 'Picking a
reviewer model' mapping table and a one-liner Smoke test note. Adds a
cross-reference at the top of INTEGRATION.md so install/configure has a
single home.

Char count: 50,757 → ~44,300 (cut ~6,500). Target ≤ 34,000 after Tasks 2–5."
```

---

## Task 2: Trim §3 (plan-author section)

**Files:**
- Modify: `INTEGRATION.md` — delete §3.4 entirely; trim the trailing prose in §3.2
- Modify: `CHANGELOG.md` — append a bullet under `### Changed` in the v0.5.1 block

- [ ] **Step 1: Delete §3.4 "Mapping to existing plan-writers"**

In `INTEGRATION.md`, delete the entire `### 3.4 Mapping to existing plan-writers` subsection — header line plus the four bullets that follow (corresponds to lines 271–275 in the pre-Task-2 state, but use the current line numbers after Task 1's deletes). The doc keeps §3.3 directly followed by §3.5.

Note: §3.5 retains its `3.5` number. Do not renumber.

- [ ] **Step 2: Trim §3.2 worked-example trailing prose**

Locate §3.2. Keep the fenced markdown code block (the `### Task 4: Add /healthz endpoint` worked example). Delete the trailing paragraph that begins with `A common style mistake is a vague AC like…` through the end of the paragraph. The example stands on its own; §3.3 right after it already covers what `validate_task_spec` checks, including ambiguous-AC behavior.

- [ ] **Step 3: Sweep for orphan references to §3.4**

Run: `grep -n -E "§ ?3\.4|see 3\.4" INTEGRATION.md`
Expected: no match. If any remain, delete or rewrite.

- [ ] **Step 4: Char-count checkpoint**

Run: `wc -c INTEGRATION.md`
Expected: ~43,600–43,800 chars (cut ~600 from the ~44,300 Task 2 starting point).

- [ ] **Step 5: Append a CHANGELOG bullet**

In `CHANGELOG.md` under `## [0.5.1] - 2026-05-19` → `### Changed`, append:

```markdown
- `INTEGRATION.md` §3 trimmed: §3.4 "Mapping to existing plan-writers" removed (the header-block + Files/Steps pattern is documented in §3.1 and applies across plan-writers without per-tool guidance); §3.2 worked-example trailing prose dropped — §3.3 covers what `validate_task_spec` checks.
```

- [ ] **Step 6: Lightweight `validate_completion` sanity gate**

Call `validate_completion` with:
- `session_id`: `""`
- `summary`: `"INTEGRATION.md §3.4 deleted; §3.2 trailing prose trimmed."`
- `final_files`: `[{Path: "INTEGRATION.md", Content: <full post-edit content>}, {Path: "CHANGELOG.md", Content: <full post-edit content>}]`
- `test_evidence`: `"wc -c INTEGRATION.md output: <paste>"`

Per INTEGRATION.md's own guidance for doc deliverables, pass full file content; do NOT use `final_diff`. Address any `critical` / `major` findings before commit.

- [ ] **Step 7: Commit**

```bash
git add INTEGRATION.md CHANGELOG.md
git commit -m "docs(integration): trim §3 plan-author section

Remove §3.4 'Mapping to existing plan-writers' (the header-block +
Files/Steps pattern in §3.1 is plan-writer-agnostic). Drop §3.2's
trailing ambiguous-AC paragraph — §3.3 already covers it.

Char count: ~44,300 → ~43,700."
```

---

## Task 3: Consolidate §4 (implementer section)

**Files:**
- Modify: `INTEGRATION.md` — consolidate the line-314 lightweight callout AND §4.1 into one short preamble under the §4 H2; fold §4.2a + §4.2b as inline notes within §4.2; trim CodeScene companion subsection; delete §4.4 Concrete examples in its entirety
- Modify: `CHANGELOG.md` — append a bullet under `### Changed`

- [ ] **Step 1: Consolidate the §4 lightweight callout and §4.1 protocol summary into one short preamble**

Pre-trim state to be aware of: `INTEGRATION.md` line 314 already carries a long lightweight-eligibility blockquote ABOVE §4.1 (857 chars). My earlier draft would have left that intact and added a SECOND lightweight blurb in place of §4.1's header — i.e. a duplicate. Instead, **replace BOTH** the line-314 blockquote AND the entire §4.1 subsection (header line through the `#### check_progress per-tool note` block at line 335) with the consolidated preamble below.

§4.1 currently contains a 3-row protocol summary table, a "One task = one session" note, a parenthetical about the controller's separate session, and a `#### check_progress per-tool note` H4. Strategy: keep the 3-row table and the "One task = one session" line. Drop the parenthetical (already covered in §5.1 / §5.4). Move the `check_progress` advisory into the optional Step 2 of the paste-clause in §4.2 as a one-sentence inline note rather than its own H4.

Concretely: delete every line from the start of the line-314 blockquote through the last line of the §4.1 `check_progress per-tool note` block. Insert this block in its place, immediately under the `## 4. For implementers — the lifecycle protocol` H2:

```markdown
> **Lightweight eligibility first.** Many tasks qualify for lightweight mode (skip `validate_task_spec`, skip `check_progress`, keep `validate_completion` as the sanity gate). Lightweight applies when ALL of: (a) the task touches ≤ 2 files OR is docs/config/data-only; (b) it is mechanical (no production-design or test-design choices); (c) the spec includes the literal text, exact diff, exact command, or exact insertion shape. `validate_plan` may pre-annotate tasks with `lightweight_eligible: true` and `lightweight_reason` — advisory only. See [Lightweight protocol mode](#lightweight-protocol-mode-v031) below for the reference clause.

| Phase | Tool | Required? | When to call |
|---|---|---|---|
| Start | `validate_task_spec` | **Yes** | Once, before writing any code |
| During | `check_progress` | Optional (advisory; low-signal in field data — call only when you suspect drift) | Mid-task, when you suspect drift |
| End | `validate_completion` | **Yes** | Before reporting DONE |

One task = one session = one subagent. The session_id returned by `validate_task_spec` lives in the implementer's context for the lifetime of the task; it is not handed off to anyone else.
```

Then `### 4.2 The implementer-prompt clause (paste this into every dispatch)` follows directly. §4.2 keeps its number.

Verify the consolidation with: `grep -c "lightweight" INTEGRATION.md | head -1` — expected: a small single-digit count, not the larger pre-trim count, and crucially no longer two top-of-§4 callouts saying the same thing.

- [ ] **Step 2: Fold §4.2a "Short dispatch target shape" inline into §4.2**

Replace the standalone `### 4.2a Short dispatch target shape` subsection with a short paragraph at the END of §4.2 (after the closing code fence of the paste clause). Outer fence is 4 backticks so the inner markdown fence inside the snippet renders correctly:

````markdown
**Short variant for agents with the protocol already in their system prompt.** If the implementer already has the full clause above in its system prompt or local instructions, controllers may dispatch the shorter clause:

```markdown
## Drift protection

Use anti-tangent per the standard dispatch protocol. For this task:
- Call `validate_task_spec` before edits unless `lightweight_eligible: true` is set by the controller.
- Call `validate_completion` before DONE and paste its `summary_block`.
- If CodeScene MCP is configured, run `pre_commit_code_health_safeguard` after meaningful code changes.
- If any major pre-task finding is accepted rather than fixed, include a one-sentence mitigation in DONE.
```
````

Delete the original §4.2a header + body.

- [ ] **Step 3: Fold §4.2b "Language-scoping prose caveat" inline as a one-paragraph note**

Replace the standalone `### 4.2b Language-scoping prose caveat` with a one-paragraph note immediately after the Step-2 short-variant paragraph:

```markdown
**Language-scoping prose caveat.** Reviewers can surface `ambiguous_spec` findings around closure/scoping semantics — Kotlin `var` captured by a lambda, Python `nonlocal`, JS `let`/`const` in arrow bodies — when the prose AC reads ambiguously even though the verbatim code block in the plan is unambiguous. Trust the verbatim plan code block; only deviate if the *tests* disagree with the prose. If you genuinely cannot reconcile code and prose, stop and ask the controller.
```

Delete the original §4.2b header + body.

- [ ] **Step 4: Trim the CodeScene MCP companion subsection**

Locate `### CodeScene MCP companion (optional)`. Keep:
- The first paragraph that explains the complementary scope.
- The "Surface" table (anti-tangent-mcp vs codescene-mcp).
- The "Tool-to-phase mapping" bullet list.
- The "Advisory posture" paragraph.
- The "Lightweight mode" one-paragraph.

Delete:
- The "On already-degraded files…" paragraph.
- The "The repository also keeps anonymized upstream-feedback drafts…" paragraph (with the four `docs/feedback/codescene/*.md` links).
- The entire `**Enabling CodeScene companion tools.**` subsection including the JSON config block (consumer setup belongs in the README or in the CodeScene MCP repo's own docs, not in INTEGRATION.md).

After the cuts, the section ends after the "Lightweight mode" paragraph.

- [ ] **Step 5: Delete §4.4 Concrete examples in its entirety**

Locate `### 4.4 Concrete examples`. Delete the H3 header and the full block through the end of §4 (the `---` separator that precedes `## 5. For controllers`). All three examples go:
- Example A (pre-hook surfaces vague AC) — the JSON request/response demo is unique, but the lesson it teaches (vague AC → `ambiguous_spec` finding → rewrite) is already covered by §3.2's worked example and §3.3's bullet list.
- Example B (mid-hook scope drift) — overlapped by §5.4.
- Example C (post-hook untouched AC) — overlapped by §6 FAQ.

Cuts 3,092 chars in one stroke instead of 2,324 chars from a B+C-only trim, and removes the only meaningful overlap §4 has with §3 / §5 / §6.

- [ ] **Step 6: Sweep for orphan references**

Run: `grep -n -E "§ ?4\.1|§ ?4\.2a|§ ?4\.2b|§ ?4\.4|Example A|Example B|Example C|see 4\.1|see 4\.4" INTEGRATION.md`

For each match:
- "§4.1" / "see 4.1" → rewrite to "§4" or delete.
- "§4.2a" / "§4.2b" → rewrite to "§4.2" or delete.
- "§4.4" / "see 4.4" / "Example A/B/C" → delete the surrounding clause; the §4.4 examples no longer exist.

Re-run the grep to confirm zero matches.

- [ ] **Step 7: Char-count checkpoint**

Run: `wc -c INTEGRATION.md`
Expected: ~37,000–37,500 chars (cut ~6,300 from Task 3 start).

If the result is above 38,500, recheck Steps 4 and 5 — those are the bulk of the cut.

- [ ] **Step 8: Append a CHANGELOG bullet**

In `CHANGELOG.md` under `## [0.5.1] - 2026-05-19` → `### Changed`, append:

```markdown
- `INTEGRATION.md` §4 consolidated: the line-314 lightweight callout AND §4.1 protocol summary collapsed into one short preamble under the §4 H2; §4.2a (short dispatch shape) and §4.2b (language-scoping caveat) folded inline as notes within §4.2; CodeScene companion subsection trimmed to its complementary-scope rationale + tool-to-phase mapping + advisory-posture / lightweight-mode notes (consumer setup links delegated to upstream); §4.4 Concrete examples deleted in full — Example A's lesson is covered by §3.2/§3.3, Example B by §5.4, and Example C by §6 FAQ.
```

- [ ] **Step 9: Lightweight `validate_completion` sanity gate**

Call `validate_completion` with:
- `session_id`: `""`
- `summary`: `"INTEGRATION.md §4 consolidated: §4 lightweight callout + §4.1 merged into one short preamble (deduped the doubled callout); §4.2a + §4.2b folded inline; CodeScene companion trimmed; §4.4 Concrete examples deleted in full."`
- `final_files`: `[{Path: "INTEGRATION.md", Content: <full post-edit content>}, {Path: "CHANGELOG.md", Content: <full post-edit content>}]`
- `test_evidence`: `"wc -c INTEGRATION.md output: <paste actual byte count>"`

Per INTEGRATION.md's own guidance ("doc deliverables → full content via `final_files`"), pass full file content; do NOT use `final_diff`. Address any `critical` / `major` findings before commit.

- [ ] **Step 10: Commit**

```bash
git add INTEGRATION.md CHANGELOG.md
git commit -m "docs(integration): consolidate §4 implementer section

Collapse the duplicate lightweight callouts (line-314 blockquote +
§4.1 protocol summary) into a single short preamble under the §4 H2.
Fold §4.2a (short dispatch shape) and §4.2b (language-scoping caveat)
inline as notes within §4.2. Trim CodeScene companion subsection to
its complementary-scope rationale + tool-to-phase mapping + advisory-
posture/lightweight-mode notes; drop consumer setup JSON + upstream-
feedback drafts pointer. Delete §4.4 Concrete examples entirely —
overlap with §3.2/§3.3 / §5.4 / §6 FAQ.

Char count: ~43,700 → ~37,400."
```

---

## Task 4: Tighten §5 (controller section), interim size checkpoint

**Files:**
- Modify: `INTEGRATION.md` — shorten §5.2 dispatch addendum; merge §5.6 + §5.7 into one subsection; light trim on §5.8 redundancy
- Modify: `CHANGELOG.md` — append a bullet under `### Changed` and add a Char-budget result line

- [ ] **Step 1: Shorten §5.2 Dispatch addendum**

Locate `### 5.2 Dispatch addendum (paste the §4.2 clause into every implementer prompt)`. The current section has 4 paragraphs + a per-skill-system bullet list. Replace the body with:

```markdown
For each task you dispatch to an implementing subagent, paste the §4.2 clause verbatim into that subagent's prompt — subagents do not inherit your CLAUDE.md or any harness-level system prompt. Append it right before the "Report Format" section of your existing dispatch template. Apply only to subagents that will implement a Goal/AC/Non-goals task; skip for read-only research subagents per §1.
```

This collapses the 4 paragraphs + bullet list into one paragraph; the per-skill pointers (superpowers / hone-ai / vanilla) added no information past "your existing dispatch template."

- [ ] **Step 2: Merge §5.6 and §5.7 into one subsection**

Locate ``### 5.6 Per-call tool args (v0.3.0+)`` and ``### 5.7 `partial: true` envelope field (v0.3.0+)``. Replace BOTH headers + bodies with a single ``### 5.6 Per-call tool args and partial-response handling (v0.3.0+)`` subsection. Keep all three knobs (`max_tokens_override`, `mode`, `partial`) — only collapse the H3 split; preserve the technical content. §5.8 then becomes §5.7 — renumber it.

Body shape (this is the entire merged subsection):

```markdown
### 5.6 Per-call tool args and partial-response handling (v0.3.0+)

**`max_tokens_override`** (all four tools): optional non-negative int. Replaces the configured `PerTaskMaxTokens` / `PlanMaxTokens` for this call. Clamped to `ANTI_TANGENT_MAX_TOKENS_CEILING` (default 16384); over-ceiling values are clamped and a `minor` clamp finding is appended. Negative values are rejected with `max_tokens_override must be ≥ 0`. Use when one specific call needs a larger reviewer budget without changing global config.

**`mode`** (`validate_plan` only): optional `"quick"` or `"thorough"` (default `"thorough"`). `"quick"` instructs the reviewer to surface only the most-severe findings — at most 3 per scope — and omit stylistic nits. Useful for small ASAP plans where late rounds surface only polish. Invalid values rejected with `mode must be "quick" or "thorough"`.

**`partial: true`** envelope field: when the reviewer's output was truncated at its `max_tokens` cap but at least one complete finding could be recovered, the response carries `"partial": true` and the synthetic truncation finding is `severity: minor` rather than `major`. The field is `omitempty` — absent in the common case. If partial recovery fails (no complete finding before the cap), the envelope falls back to the legacy single `severity: major` truncation finding with no `partial` field set.

Passing `validate_plan` calls are cached in memory for 3 minutes when the rendered prompt, model, mode, and token budget are identical. Cache hits return `review_ms: 0` and prefix the original `next_action` with `[cached <=3m]`.
```

- [ ] **Step 3: Renumber §5.8 → §5.7**

After Step 2, the former §5.8 "Using review-context features" becomes §5.7. Update only the header line: `### 5.8 Using review-context features` → `### 5.7 Using review-context features`. The body is unchanged in this step.

- [ ] **Step 4: Light trim on §5.7 (formerly §5.8) Using review-context features**

The subsection currently has TWO JSON examples (`pinned_by` and `controller_verified_references`). Keep both — they're load-bearing examples. But delete the standalone paragraph that begins `For \`validate_plan\`, normally omit \`max_tokens_override\`…` because §5.6 (the merged subsection from Step 2) already covers `max_tokens_override` and `codebase_reference_checklist` is mentioned in §6 FAQ.

Also delete the final paragraph beginning `For \`validate_completion\`, submit doc/generated deliverables through \`final_files\`…` — the `final_files` / `final_diff` / `test_evidence` envelope is documented in §6 FAQ.

The trimmed §5.7 ends after the `controller_verified_references` JSON example's wrap-up sentence ("These entries are attestations from the caller. They suppress matching `unverifiable_codebase_claim` findings by substring match only; they do not suppress real contradictions or ambiguity.").

- [ ] **Step 5: Sweep for orphan references**

Run: `grep -n -E "§ ?5\.7|§ ?5\.8|see 5\.6|see 5\.7|see 5\.8" INTEGRATION.md`

For each match:
- References to `§5.7` (old `partial:` section) → rewrite to `§5.6` (merged subsection).
- References to `§5.8` → rewrite to `§5.7` (renumbered review-context features).

Re-run the grep to confirm zero matches.

Also run the full sweep one more time to catch anything missed in Tasks 1–3:

```bash
grep -n -E "§ ?2\.|§ ?3\.4|§ ?4\.1|§ ?4\.2a|§ ?4\.2b|Example B|Example C" INTEGRATION.md
```

Expected: zero matches.

- [ ] **Step 6: Char-count checkpoint**

Run: `wc -c INTEGRATION.md`
Expected: ~35,500–35,800 chars (cut ~1,500 from Task 4 start).

This is INTERIM. The ≤ 34,000 final target is gated by Task 5. If Task 4 lands materially above 36,500, recheck Steps 1–4 — most likely §5.2 didn't actually collapse or one of the §5.6/§5.7 paragraphs got duplicated during the merge.

- [ ] **Step 7: Append the §5 CHANGELOG bullet**

In `CHANGELOG.md` under `## [0.5.1] - 2026-05-19` → `### Changed`, append:

```markdown
- `INTEGRATION.md` §5 tightened: §5.2 dispatch-addendum collapsed from 4 paragraphs + per-skill bullets to a single paragraph; §5.6 and §5.7 merged into a single `### 5.6 Per-call tool args and partial-response handling` subsection (covering `max_tokens_override`, `mode`, and `partial: true`); former §5.8 renumbered to §5.7 and the two paragraphs duplicating §5.6 / §6 FAQ content removed.
```

- [ ] **Step 8: Lightweight `validate_completion` sanity gate**

Call `validate_completion` with:
- `session_id`: `""`
- `summary`: `"INTEGRATION.md §5 tightened: §5.2 collapsed; §5.6+§5.7 merged; §5.8 renumbered to §5.7 and trimmed."`
- `final_files`: `[{Path: "INTEGRATION.md", Content: <full post-edit content>}, {Path: "CHANGELOG.md", Content: <full post-edit content>}]`
- `test_evidence`: `"wc -c INTEGRATION.md output: <paste>"`

Per INTEGRATION.md's own guidance for doc deliverables, pass full file content; do NOT use `final_diff`. Address any `critical` / `major` findings before commit.

- [ ] **Step 9: Commit**

```bash
git add INTEGRATION.md CHANGELOG.md
git commit -m "docs(integration): tighten §5 controller section

Collapse §5.2 dispatch-addendum from 4 paragraphs + per-skill bullets
to one paragraph (the per-skill pointers added nothing past 'your
existing dispatch template'). Merge §5.6 (per-call tool args) and §5.7
(partial-response handling) into a single subsection covering
max_tokens_override + mode + partial. Renumber former §5.8 → §5.7 and
drop two paragraphs duplicating §5.6 / §6 FAQ content.

Char count: ~37,400 → ~35,800 (Task 5 still to come for final size)."
```

---

## Task 5: Compress §3.6, §3.7, and the §6 FAQ; final size assertion

**Files:**
- Modify: `INTEGRATION.md` — compress §3.6 (normative test bodies) and §3.7 (`.trimIndent()` caveat) by ~60%; trim 3 §6 FAQ entries that fully duplicate other sections
- Modify: `CHANGELOG.md` — append the final `### Changed` bullet for v0.5.1 and record the actual final char count

- [ ] **Step 1: Compress §3.6 Normative test bodies**

§3.6 is currently 1,639 chars across the heading, a long prose explanation, a fenced markdown example, and a paragraph on adjacency / truncation / `// excerpt:`. The protocol-relevant content is: (1) the literal header marker `**NORMATIVE TEST BODIES (verbatim):**` and the "wrap each test body in a fenced block immediately under it" rule, (2) the one-line note that `validate_plan` extracts these server-side and threads them into `validate_task_spec`, (3) adjacency-separates-entries behavior, and (4) the 4000-code-point cap plus the `// excerpt:` escape hatch. The fenced kotlin example is intentionally not preserved — the marker shape is unambiguous from the prose alone, and any reader writing a real plan will look at `examples/` rather than this caveat section.

Replace the entire §3.6 subsection (header through last line) with:

```markdown
### 3.6 Normative test bodies (binding test code in plans)

When a task's plan pastes verbatim test code that the implementer must land as written, wrap each test body in a fenced block immediately under a literal `**NORMATIVE TEST BODIES (verbatim):**` header. `validate_plan` extracts each fence server-side (deterministic markdown parsing) and threads the list into the per-task `validate_task_spec` `normative_test_bodies` input; the reviewer then treats each entry as binding scope. Adjacent fences extract as separate entries. Bodies exceeding 4000 Unicode code points are server-truncated with a `// truncated` marker; for legitimately longer bodies, paraphrase or excerpt and start the body with `// excerpt:` so the reviewer treats it as partial coverage.
```

Target: ≤ 700 chars (saves ~940).

- [ ] **Step 2: Compress §3.7 `.trimIndent()` raw-string caveat**

§3.7 is 1,006 chars across a prose lead, two rules, and a worked phrasing example. The protocol-relevant content is: (1) the failure mode (line-wraps in plan source render as newlines), (2) the rule to keep example strings on one source line, and (3) the rule to phrase ACs against the rendered string.

Replace the entire §3.7 subsection with:

```markdown
### 3.7 `.trimIndent()` raw-string caveat

When a plan snippet is wrapped in `.trimIndent()` (or any equivalent raw-string trim), multi-line source phrases render newlines exactly where they sit in the markdown — anti-tangent reads the source, not the rendered output. Keep example strings the implementation will compare against on a single source line, and phrase ACs against the rendered string (e.g. "output contains `please decline politely`"), not against source layout.
```

Target: ≤ 400 chars (saves ~600).

- [ ] **Step 3: Trim §6 FAQ entries that fully duplicate other sections**

§6 currently has ~11 Q/A pairs (4,831 chars total). Three entries fully duplicate other sections — delete them:

- **"What happens if a task fails the plan-handoff gate?"** — duplicates §5.1 procedure.
- **"What if the reviewer is wrong?"** — duplicates §4.3 ("Address vs. push back").
- **"Should I use this for ad-hoc code changes outside a plan?"** — duplicates §1 ("When the protocol applies").

Delete each entry (the bold Q line and the answer paragraph that follows). Target cut: ~1,000 chars combined. The remaining FAQ entries keep their order; no renumbering needed (entries are unnumbered).

- [ ] **Step 4: Sweep for orphan references**

Run: `grep -n -E "see FAQ|§ ?6\." INTEGRATION.md`

Most matches will be incidental references to "FAQ" that stay valid. Manually inspect each match; rewrite or delete only if the reference pointed specifically at one of the three deleted entries.

Run the comprehensive pre-trim sweep one more time to catch anything missed across Tasks 1–4:

```bash
grep -n -E "§ ?2\.|§ ?3\.4|§ ?4\.1|§ ?4\.2a|§ ?4\.2b|§ ?4\.4|§ ?5\.7|§ ?5\.8|Example [ABC]" INTEGRATION.md
```

Expected: zero matches across the doc body. (Matches in the §5.6 / §5.7 merged subsection body that explicitly use the new numbers are fine, but the old §5.7 / §5.8 references should be gone.)

- [ ] **Step 5: Final char-count assertion**

Run: `wc -c INTEGRATION.md`

Expected: ≤ 34,000 chars (measured arithmetic lands at ~33,430). Capture the exact number — it goes into the CHANGELOG bullet and the commit message.

If above 34,000: apply the fallback trims below, re-run `wc -c`, and only proceed to Step 6 once the result is ≤ 34,000. Apply Fallback A first (lower-risk and higher-yield); only add Fallback B if Fallback A is insufficient on its own.

- **Fallback A — Compress the "Choosing `pinned_by`, `context`, `controller_verified_references`" preamble subsection (lines 43–49, ~1,267 chars; partial overlap with §5.7).** Replace the entire subsection with the fenced block below; target replacement ≤ 400 chars; saves ~870.

  ```markdown
  ### Choosing review-context inputs

  Use `context` for background a fresh implementer needs (constraints, prior decisions, why a non-obvious approach is required). Use `pinned_by` for existing tests, docs, commands, or static checks that pin a "behavior unchanged" AC. Use `controller_verified_references` for codebase references the controller already grep-verified (paths, symbols, line anchors, commands, adjacent patterns); CVR suppresses matching `unverifiable_codebase_claim` findings by substring only — contradictions, missing ACs, ambiguity, and `convention_deviation` are NOT suppressed. CVR is single-category; use `testability_extractions` for intentional `scope_drift` suppression and `codebase_conventions` to actively trigger `convention_deviation`.
  ```
- **Fallback B — Compress §5.7 "Using review-context features" by dropping the `controller_verified_references` JSON example** (keep the `pinned_by` JSON example, since CVR is already explained in Fallback A's replacement when applied). Saves ~400–500 chars.

If above 35,000: a step failed. Re-run each task's char-count checkpoint to find which task underperformed and inspect the corresponding diff.

- [ ] **Step 6: Append the final CHANGELOG bullet (with the actual byte count)**

In `CHANGELOG.md` under `## [0.5.1] - 2026-05-19` → `### Changed`, append:

```markdown
- `INTEGRATION.md` §3.6 (normative test bodies) and §3.7 (`.trimIndent()` caveat) compressed by ~60% — protocol surface is preserved (marker shape, server-side extraction, 4000-code-point cap, `// excerpt:` escape hatch, one-source-line + render-aware-AC rules); explanatory prose dropped. §6 FAQ trimmed by removing three entries that fully duplicate other sections (plan-handoff gate failure → §5.1; reviewer-is-wrong → §4.3; ad-hoc code changes → §1). Final `INTEGRATION.md` size: <ACTUAL CHARS> chars (was 50,757; under the 40,000 user-instructions warning threshold by <40000 − ACTUAL> chars).
```

Replace `<ACTUAL CHARS>` and `<40000 − ACTUAL>` with the values from Step 5.

- [ ] **Step 7: Lightweight `validate_completion` sanity gate**

Call `validate_completion` with:
- `session_id`: `""`
- `summary`: `"INTEGRATION.md §3.6 + §3.7 compressed ~60%; §6 FAQ trimmed (3 duplicate entries removed). Final size: <ACTUAL> chars."`
- `final_files`: `[{Path: "INTEGRATION.md", Content: <full post-edit content>}, {Path: "CHANGELOG.md", Content: <full post-edit content>}]`
- `test_evidence`: `"wc -c INTEGRATION.md output: <paste actual byte count>"`

Per INTEGRATION.md's own guidance for doc deliverables, pass full file content; do NOT use `final_diff`. Address any `critical` / `major` findings before commit.

- [ ] **Step 8: Commit**

```bash
git add INTEGRATION.md CHANGELOG.md
git commit -m "docs(integration): compress §3.6/§3.7 + trim §6 FAQ duplicates

Compress §3.6 normative-test-bodies and §3.7 .trimIndent() caveat by
~60% each; protocol surface preserved (marker shape, server-side
extraction, 4000-code-point cap, // excerpt: escape hatch, source-line
+ rendered-AC rules). Drop three §6 FAQ entries that fully duplicate
other sections (plan-handoff gate failure → §5.1; reviewer-is-wrong
→ §4.3; ad-hoc code changes → §1).

Final INTEGRATION.md size: <ACTUAL> chars (was 50,757; <40000 − ACTUAL>
chars of buffer under the 40k user-instructions warning threshold)."
```

Replace `<ACTUAL>` and `<40000 − ACTUAL>` with the values from Step 5.

---

## Self-Review

**Spec coverage** (the user's directive: "small and on-point, well below the 40k budget"):
- §2 Setup removal → Task 1 (~6,500 chars cut net, measured)
- §3.4 + §3.2 trims → Task 2 (~600 chars cut, measured)
- §4 consolidations + CodeScene trim + §4.4 deletion → Task 3 (~6,300 chars cut, measured)
- §5 tightening → Task 4 (~1,600 chars cut, measured)
- §3.6 / §3.7 compression + §6 FAQ duplicate trim → Task 5 (~2,330 chars cut, measured: 874 + 521 + 934)
- Final size: ~33,500 chars (~6,500-char buffer under the 40,000 threshold; fallback in Task 5 Step 5 gets another ~870 chars if needed).

**CHANGELOG / branch alignment.** `## [0.5.1] - 2026-05-19` opens in Task 1 Step 7; one `### Changed` bullet appended per subsequent task. Branch-vs-CHANGELOG CI check (per project CLAUDE.md) satisfied at the end of Task 1 and re-satisfied after each later task.

**Placeholder scan.** No "TBD" / "TODO" / "fill in details" — every step has either a literal diff/insertion shape or a concrete inspection command. The two `<ACTUAL>` placeholders in Task 5 are explicitly defined as the `wc -c` result from the immediately preceding step; they are not unbounded TBDs.

**Type / reference consistency.** Section renumbering happens only in Task 4 Step 3 (§5.8 → §5.7); the orphan sweep in Task 5 Step 4 verifies no stale `§5.8` references survive anywhere. Task 4's grep also catches stale `§5.7` references that should have moved to the merged §5.6. The Task 3 / Task 5 sweeps catch stale `§4.4` / `Example [ABC]` / `§3.4` references.

**Doc-deliverable evidence.** Every `validate_completion` call uses `final_files` (full post-edit content) per the project's own guidance at the pre-trim INTEGRATION.md line 41; none use `final_diff`. The 200 KB payload cap is uncontested at our file sizes.

**Char-budget arithmetic** (measured, not estimated):
50,757 − 6,500 − 600 − 6,300 − 1,600 − 2,330 = 33,427 chars. Target ≤ 34,000 achieved with ~570-char margin; ~6.5 KB buffer under the 40,000 user-instructions threshold. Task 5 Step 5 fallback (compress "Choosing `pinned_by`, `context`, `controller_verified_references`" preamble) gets another ~870 chars if a tighter buffer is wanted, dropping to ~32,560 (~7.4 KB buffer).
