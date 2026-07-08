# Slim install: on-demand protocol via `anti-tangent-protocol` plugin — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the always-loaded ~40 KB `INTEGRATION.md` `@`-import with a marketplace plugin (Claude Code, on-demand skill) and a slim opencode pointer, dropping the always-loaded footprint to a single skill-description line.

**Architecture:** New companion plugin `plugin/anti-tangent-protocol/` mirrors `plugin/bm-scribe/`: a single description-triggered skill whose body `Read`s a bundled copy of `INTEGRATION.md` on demand. A CI guard pins the bundled copy to root. opencode (no skills) gets a slim pointer template. README install flows and CHANGELOG are updated. No Go/server code changes.

**Tech Stack:** Markdown, JSON (plugin/marketplace manifests), GitHub Actions YAML. No Go.

**User decisions (already made):**
- Packaging: **marketplace plugin** mirroring `bm-scribe/` (not a standalone user-skill, not docs-only). Bundled `INTEGRATION.md` + CI sync guard accepted.
- Version: **v0.11.1 (patch)** — precedent: bm-scribe shipped in the v0.7.1 patch.
- Plugin/skill name: **`anti-tangent-protocol`**.
- **Drop** the deterministic "`@`-import the whole file" fallback entirely — clean replacement.
- Plugin ships **no plugin-level `CLAUDE.md`** (it would be always-loaded and reintroduce the cost).

Spec: `docs/superpowers/specs/2026-07-08-slim-install-on-demand-protocol-design.md`

---

## File Structure

**New**
- `plugin/anti-tangent-protocol/.claude-plugin/plugin.json` — plugin manifest (v0.1.0).
- `plugin/anti-tangent-protocol/skills/anti-tangent-protocol/SKILL.md` — the on-demand skill (thin; `Read`s the bundled doc).
- `plugin/anti-tangent-protocol/INTEGRATION.md` — bundled byte-for-byte copy of root.
- `plugin/anti-tangent-protocol/README.md` — what it is, install, trade-off.
- `examples/anti-tangent-pointer.md` — slim opencode pointer template (`__ANTI_TANGENT_DOC_PATH__` token).

**Modified**
- `.claude-plugin/marketplace.json` — add plugin entry + bump marketplace version.
- `.github/workflows/ci.yml` — bundled-copy drift guard (step in `integration-size` job).
- `README.md` — CC + opencode install prompts, intro sentence, Integration section.
- `CLAUDE.md` — new `## Editing INTEGRATION.md` section (size + sync invariants).
- `CHANGELOG.md` — `## [0.11.1]`.

All tasks are docs/config only (no new logic, exact content given) → each is **lightweight-protocol eligible** at execution time.

---

### Task 1: Scaffold the `anti-tangent-protocol` plugin and register it

**Goal:** Create the plugin (manifest + skill + bundled doc + README) and register it in the marketplace so `claude plugin install anti-tangent-protocol@anti-tangent-mcp` resolves.

**Files:**
- Create: `plugin/anti-tangent-protocol/.claude-plugin/plugin.json`
- Create: `plugin/anti-tangent-protocol/skills/anti-tangent-protocol/SKILL.md`
- Create: `plugin/anti-tangent-protocol/INTEGRATION.md` (via `cp`, not hand-authored)
- Create: `plugin/anti-tangent-protocol/README.md`
- Modify: `.claude-plugin/marketplace.json`

**Acceptance Criteria:**
- [ ] `plugin.json` and `marketplace.json` both parse as JSON; marketplace lists a plugin named `anti-tangent-protocol` with `source: "./plugin/anti-tangent-protocol"`.
- [ ] `SKILL.md` frontmatter has `name: anti-tangent-protocol:anti-tangent-protocol` and a `description` that names the Goal/Acceptance-criteria trigger.
- [ ] `plugin/anti-tangent-protocol/INTEGRATION.md` is byte-identical to root `INTEGRATION.md`.
- [ ] No `plugin/anti-tangent-protocol/CLAUDE.md` file exists.
- [ ] The skill body references the bundled doc by the relative path `../../INTEGRATION.md`.

**Verify:**
```bash
jq -e '.plugins[] | select(.name=="anti-tangent-protocol" and .source=="./plugin/anti-tangent-protocol")' .claude-plugin/marketplace.json && \
jq -e '.name=="anti-tangent-protocol"' plugin/anti-tangent-protocol/.claude-plugin/plugin.json && \
diff -q INTEGRATION.md plugin/anti-tangent-protocol/INTEGRATION.md && \
grep -q 'name: anti-tangent-protocol:anti-tangent-protocol' plugin/anti-tangent-protocol/skills/anti-tangent-protocol/SKILL.md && \
grep -qF '../../INTEGRATION.md' plugin/anti-tangent-protocol/skills/anti-tangent-protocol/SKILL.md && \
test ! -e plugin/anti-tangent-protocol/CLAUDE.md && echo OK
```
Expected: `OK` (asserts the marketplace `source`, the skill `name`, the bundled-doc relative path in the body, byte-identical bundle, and no plugin CLAUDE.md).

**Steps:**

- [ ] **Step 1: Create the plugin manifest**

Create `plugin/anti-tangent-protocol/.claude-plugin/plugin.json`:

```json
{
  "name": "anti-tangent-protocol",
  "description": "On-demand loader for the anti-tangent-mcp drift-protection protocol. A description-triggered skill that loads the full INTEGRATION.md only when a Goal/Acceptance-criteria task appears — keeping the always-loaded footprint to one line instead of ~10k tokens.",
  "version": "0.1.0",
  "author": {
    "name": "Patrick Gilmore",
    "email": "p@patiently.io"
  },
  "homepage": "https://github.com/patiently/anti-tangent-mcp",
  "repository": "https://github.com/patiently/anti-tangent-mcp",
  "license": "MIT",
  "keywords": [
    "anti-tangent",
    "drift-protection",
    "mcp",
    "skills",
    "implementation-plan"
  ]
}
```

- [ ] **Step 2: Create the skill**

Create `plugin/anti-tangent-protocol/skills/anti-tangent-protocol/SKILL.md`:

````markdown
---
name: anti-tangent-protocol:anti-tangent-protocol
description: Use when you are about to implement, or dispatch a subagent to implement, a task that has a Goal / Acceptance-criteria header from an implementation plan. Loads the anti-tangent-mcp drift-protection protocol (validate_task_spec → check_progress → validate_completion; validate_plan for controllers).
---

# anti-tangent-protocol

Loads the full anti-tangent-mcp integration protocol on demand, so the
~10k-token `INTEGRATION.md` is not always resident in context.

## When this applies

Only when the current task carries a structured **Goal / Acceptance criteria /
(Non-goals) / (Context)** header from an implementation plan. For read-only
research, Q&A, ad-hoc edits, plan authoring, or code review, the protocol does
not apply — stop here.

## Step 1 — Read the protocol

`Read` the bundled protocol document (relative to this skill file):

    ../../INTEGRATION.md

It is the single source of truth: the plan-handoff gate (`validate_plan`), the
per-task lifecycle (`validate_task_spec` → `check_progress` →
`validate_completion`), the dispatch clauses, and the scope/limits.

## Step 2 — Follow the clause that fits your role

- **Implementer** (you will write the code for this task): follow the §4
  lifecycle — call `validate_task_spec` before editing, `validate_completion`
  before reporting DONE, and paste its `summary_block`.
- **Controller** (you dispatch subagents): run the §5.1 `validate_plan`
  handoff gate before dispatch, and paste the §4.2 clause into each
  implementer's prompt.

Anti-tangent is advisory — it never blocks. Treat `critical` / `major` findings
as blocking-or-explain per the protocol.
````

- [ ] **Step 3: Bundle the protocol doc (copy, do not hand-author)**

```bash
cp INTEGRATION.md plugin/anti-tangent-protocol/INTEGRATION.md
```

- [ ] **Step 4: Create the plugin README**

Create `plugin/anti-tangent-protocol/README.md`:

```markdown
# anti-tangent-protocol

A Claude Code companion plugin that makes the [anti-tangent-mcp](https://github.com/patiently/anti-tangent-mcp)
drift-protection protocol available **on demand** instead of always-inlined.

The plugin ships a single skill whose one-line `description` is the only thing
always in context. When you are about to implement (or dispatch a subagent to
implement) a task that has a **Goal / Acceptance-criteria** header from an
implementation plan, the skill loads and `Read`s the bundled `INTEGRATION.md`
(the full protocol). For everything else — Q&A, exploration, ad-hoc edits — the
full ~10k-token document never loads.

This replaces the older install that `@`-imported the whole `INTEGRATION.md`
into global `~/.claude/CLAUDE.md` (a flat ~10k-token cost on every call).

## Install

```bash
claude plugin marketplace add patiently/anti-tangent-mcp
claude plugin install anti-tangent-protocol@anti-tangent-mcp
```

Verify with `claude plugin list`. The plugin complements the MCP server (install
that separately — see the main README); the server provides the tools, this
plugin provides the on-demand "when + how" guidance.

## Trade-off

A skill body loads when the model judges its `description` relevant — slightly
less deterministic than an always-inlined block. That is the correct trade
against a flat ~10k-token tax on every call; the description's Goal/AC-header
wording is written to make the trigger fire reliably.

## Source of truth

`INTEGRATION.md` here is a byte-for-byte copy of the repository root
`INTEGRATION.md`, kept identical by a CI guard. Edit the root file, not this copy.
```

- [ ] **Step 5: Register the plugin in the marketplace**

Modify `.claude-plugin/marketplace.json` — bump the top-level `version` from `0.7.1` to `0.8.0`, and add the `anti-tangent-protocol` entry to the `plugins` array (before or after `bm-scribe`). Final file:

```json
{
  "$schema": "https://json.schemastore.org/claude-code-marketplace.json",
  "name": "anti-tangent-mcp",
  "version": "0.8.0",
  "description": "Anti-tangent MCP server companions: the anti-tangent-protocol on-demand protocol plugin and the bm-scribe Basic Memory note-writing plugin.",
  "owner": {
    "name": "Patrick Gilmore",
    "email": "p@patiently.io"
  },
  "plugins": [
    {
      "name": "anti-tangent-protocol",
      "description": "On-demand loader for the anti-tangent-mcp drift-protection protocol — a description-triggered Claude Code skill that loads the full INTEGRATION.md only when a Goal/Acceptance-criteria task appears.",
      "version": "0.1.0",
      "source": "./plugin/anti-tangent-protocol",
      "category": "productivity",
      "homepage": "https://github.com/patiently/anti-tangent-mcp"
    },
    {
      "name": "bm-scribe",
      "description": "Claude Code plugin that writes Basic Memory notes per v0.7.0 project-knowledge conventions and the BM v0.21.1 three-step permalink-canonicalization pattern.",
      "version": "0.2.0",
      "source": "./plugin/bm-scribe",
      "category": "productivity",
      "homepage": "https://github.com/patiently/anti-tangent-mcp"
    }
  ]
}
```

- [ ] **Step 6: Verify and commit**

```bash
jq -e '.plugins[] | select(.name=="anti-tangent-protocol" and .source=="./plugin/anti-tangent-protocol")' .claude-plugin/marketplace.json >/dev/null && \
jq -e '.name=="anti-tangent-protocol"' plugin/anti-tangent-protocol/.claude-plugin/plugin.json >/dev/null && \
diff -q INTEGRATION.md plugin/anti-tangent-protocol/INTEGRATION.md && \
grep -q 'name: anti-tangent-protocol:anti-tangent-protocol' plugin/anti-tangent-protocol/skills/anti-tangent-protocol/SKILL.md && \
grep -qF '../../INTEGRATION.md' plugin/anti-tangent-protocol/skills/anti-tangent-protocol/SKILL.md && \
test ! -e plugin/anti-tangent-protocol/CLAUDE.md && echo OK
git add plugin/anti-tangent-protocol .claude-plugin/marketplace.json
git commit -m "feat: add anti-tangent-protocol on-demand plugin + register in marketplace"
```
Expected: `OK`, then a commit.

---

### Task 2: CI guard — bundled `INTEGRATION.md` must match root

**Goal:** Fail CI if `plugin/anti-tangent-protocol/INTEGRATION.md` drifts from root `INTEGRATION.md`.

**Files:**
- Modify: `.github/workflows/ci.yml` (add a step to the existing `integration-size` job, after the size-budget step at lines ~57-65)

**Acceptance Criteria:**
- [ ] With the copies identical, the drift-guard step exits 0.
- [ ] With a deliberate one-byte difference, the step exits non-zero and prints the resync hint.
- [ ] The step lives in the `integration-size` job (which already checks out the repo).

**Verify:**
```bash
# identical → passes:
diff -q INTEGRATION.md plugin/anti-tangent-protocol/INTEGRATION.md && echo PASS
# drift → fails (temp edit, then restore):
printf 'x' >> plugin/anti-tangent-protocol/INTEGRATION.md; \
  diff -q INTEGRATION.md plugin/anti-tangent-protocol/INTEGRATION.md || echo "FAIL-AS-EXPECTED"; \
  cp INTEGRATION.md plugin/anti-tangent-protocol/INTEGRATION.md
```
Expected: `PASS`, then `FAIL-AS-EXPECTED`.

**Steps:**

- [ ] **Step 1: Add the drift-guard step**

In `.github/workflows/ci.yml`, inside the `integration-size` job, immediately after the existing "Verify INTEGRATION.md is under the 40,000-byte budget" step, add:

```yaml
      - name: Verify anti-tangent-protocol bundled INTEGRATION.md matches root
        run: |
          if ! diff -q INTEGRATION.md plugin/anti-tangent-protocol/INTEGRATION.md >/dev/null; then
            echo "::error file=plugin/anti-tangent-protocol/INTEGRATION.md::Bundled copy has drifted from root INTEGRATION.md. Resync with: cp INTEGRATION.md plugin/anti-tangent-protocol/INTEGRATION.md"
            exit 1
          fi
          echo "✓ bundled plugin copy matches root INTEGRATION.md"
```

- [ ] **Step 2: Confirm the step landed with the exact hint, and drift behavior locally**

```bash
# step present with the exact resync hint text (the AC's "print the resync hint"):
grep -q 'Verify anti-tangent-protocol bundled INTEGRATION.md matches root' .github/workflows/ci.yml && \
grep -q 'Resync with: cp INTEGRATION.md plugin/anti-tangent-protocol/INTEGRATION.md' .github/workflows/ci.yml && echo "step+hint ok"
# YAML sanity only if a parser is available (do not require PyYAML):
command -v yq >/dev/null && yq . .github/workflows/ci.yml >/dev/null && echo "yaml ok" || echo "yaml check skipped (no yq)"
diff -q INTEGRATION.md plugin/anti-tangent-protocol/INTEGRATION.md && echo "identical ok"
```
Expected: `step+hint ok`, a yaml line (ok or skipped), `identical ok`.

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/ci.yml
git commit -m "ci: guard that anti-tangent-protocol bundled INTEGRATION.md matches root"
```

---

### Task 3: opencode slim-pointer template

**Goal:** Add the canonical slim pointer opencode installs into `instructions` (in place of the full file).

**Files:**
- Create: `examples/anti-tangent-pointer.md`

**Acceptance Criteria:**
- [ ] File is short (≈ one screen, < 1500 bytes) and states the Goal/AC trigger.
- [ ] It instructs the agent to `Read` the full doc on demand at the `__ANTI_TANGENT_DOC_PATH__` token (which the install step substitutes).
- [ ] It explicitly says NOT to act on the protocol from the pointer alone.

**Verify:**
```bash
test -f examples/anti-tangent-pointer.md && \
grep -q '__ANTI_TANGENT_DOC_PATH__' examples/anti-tangent-pointer.md && \
grep -qi 'Goal / Acceptance' examples/anti-tangent-pointer.md && \
grep -qi 'not act on the protocol from this pointer alone' examples/anti-tangent-pointer.md && \
[ "$(wc -c < examples/anti-tangent-pointer.md)" -lt 1500 ] && echo OK
```
Expected: `OK`

**Steps:**

- [ ] **Step 1: Create the pointer template**

Create `examples/anti-tangent-pointer.md`:

```markdown
# anti-tangent-mcp — protocol pointer (opencode / non-skill hosts)

The anti-tangent-mcp drift-protection protocol is available in this host as an
MCP server (tools: `validate_plan`, `validate_task_spec`, `check_progress`,
`validate_completion`, plus the optional project-knowledge pair).

**When the protocol applies:** only when the task you are about to implement (or
dispatch a subagent to implement) carries a structured **Goal / Acceptance
criteria / (Non-goals) / (Context)** header from an implementation plan. For
read-only research, Q&A, ad-hoc edits, plan authoring, or code review, it does
not apply.

**When it applies, load the full protocol on demand:** `Read` the full document
at `__ANTI_TANGENT_DOC_PATH__` and follow it — the §4 per-task lifecycle if you
are the implementer, the §5 controller gate + dispatch clause if you dispatch
subagents. Do not act on the protocol from this pointer alone; the full document
is the single source of truth.

This pointer is the only always-loaded piece; the full ~10k-token document loads
only when a Goal/Acceptance-criteria task actually appears.
```

- [ ] **Step 2: Verify + commit**

```bash
test -f examples/anti-tangent-pointer.md && grep -q '__ANTI_TANGENT_DOC_PATH__' examples/anti-tangent-pointer.md && grep -qi 'not act on the protocol from this pointer alone' examples/anti-tangent-pointer.md && [ "$(wc -c < examples/anti-tangent-pointer.md)" -lt 1500 ] && echo OK
git add examples/anti-tangent-pointer.md
git commit -m "docs: add slim opencode protocol-pointer template"
```
Expected: `OK`, then a commit.

---

### Task 4: Rewrite the README install flows

**Goal:** Replace the always-inlined `INTEGRATION.md` install with the plugin (Claude Code) and slim pointer (opencode) in both one-shot prompts, the intro sentence, and the Integration section.

**Files:**
- Modify: `README.md` (intro line ~21; Claude Code prompt steps 7-9 and Report line ~59-83; opencode prompt steps 6-8 ~137-153; Integration section ~404-406)

**Acceptance Criteria:**
- [ ] The Claude Code prompt no longer downloads `INTEGRATION.md` to `~/.claude/anti-tangent.md` nor appends an `@`-import to `~/.claude/CLAUDE.md`; it installs the `anti-tangent-protocol` plugin instead.
- [ ] The bm-scribe step reuses the already-added marketplace (no duplicate `marketplace add`).
- [ ] The Claude Code Report line no longer asks for "the final contents of `~/.claude/CLAUDE.md`".
- [ ] The opencode prompt downloads the full `INTEGRATION.md` to `~/.config/opencode/anti-tangent.md` (on-demand) and adds ONLY the slim pointer to `instructions`.
- [ ] No remaining README text tells a user to always-load the full doc: (a) no Claude `@`-import of an `anti-tangent.md` path, AND (b) no opencode `"instructions"` array entry pointing at `anti-tangent.md` (only the `anti-tangent-pointer.md` path is allowed), AND (c) the opencode step carries an explicit "must stay on-demand" guard.

**Verify:**
```bash
# (a) no Claude @-import of an anti-tangent.md path; (b) no opencode instructions
# entry pointing at the full anti-tangent.md (pointer path is fine); plus the
# plugin install, the pointer, the substitution token, and the on-demand guard:
! grep -nE '@[^ ]*/\.claude/anti-tangent\.md' README.md && \
! grep -nE '"instructions":[^]]*/anti-tangent\.md"' README.md && \
grep -q 'anti-tangent-protocol@anti-tangent-mcp' README.md && \
grep -q 'anti-tangent-pointer' README.md && \
grep -q '__ANTI_TANGENT_DOC_PATH__' README.md && \
grep -q 'must stay on-demand' README.md && echo OK
```
Expected: `OK`

**Steps:**

- [ ] **Step 1: Update the "One-shot install" intro sentence (~line 21)**

Replace:
```
…register the MCP, download `INTEGRATION.md`, and wire it into your user-level instructions so every future session sees the protocol. The prompts resolve "latest" from the GitHub API, so they don't need to be edited each release.
```
with:
```
…register the MCP, and set up the protocol so it loads **on demand** (Claude Code installs the `anti-tangent-protocol` plugin; opencode wires a slim pointer). The full protocol document loads only when a task carries a Goal/Acceptance-criteria header — not on every call. The prompts resolve "latest" from the GitHub API, so they don't need to be edited each release.
```

- [ ] **Step 2: Replace Claude Code prompt steps 7-8 with the plugin install, and renumber the bm-scribe step**

Replace the current steps 7, 8, and 9 (lines ~59-80) with:

```
7. Install the anti-tangent-protocol plugin — it carries the drift-protection
   protocol as an on-demand skill. Its one-line description is the only
   always-loaded footprint; the full protocol loads only when a task has a
   Goal/Acceptance-criteria header. This replaces the old always-inlined
   INTEGRATION.md import (do NOT write INTEGRATION.md into ~/.claude/):
       claude plugin marketplace add patiently/anti-tangent-mcp
       claude plugin install anti-tangent-protocol@anti-tangent-mcp
   Verify with `claude plugin list`.
8. Optional: install the bm-scribe companion plugin (available since
   anti-tangent-mcp v0.7.1). It wraps the
   standard `basic-memory` MCP tools with skills that enforce the v0.7.0
   project-knowledge layout and the BM v0.21.1 three-step permalink-
   canonicalization pattern. Ask me whether to enable it. If yes (the
   marketplace was already added in step 7):
       claude plugin install bm-scribe@anti-tangent-mcp
   Verify with `claude plugin list`. Tell me to set `BM_SCRIBE_PROJECT` (and
   optionally `BM_SCRIBE_USERNAME`) in my shell env before using any of the
   `bm-scribe:*` skills. Skip this step if I say no — the MCP install from
   steps 1-7 is complete and useful without the plugin.
```

- [ ] **Step 3: Update the Claude Code Report line (~line 82-83)**

Replace:
```
Report: installed version, binary path, `claude mcp list` output (with any
key values redacted), and the final contents of `~/.claude/CLAUDE.md`.
```
with:
```
Report: installed version, binary path, `claude mcp list` output (with any
key values redacted), and `claude plugin list` output.
```

- [ ] **Step 4: Replace opencode prompt steps 6-7 (lines ~137-148)**

Replace the current steps 6 and 7 with:

```
6. Download `INTEGRATION.md` for the installed version to
   `~/.config/opencode/anti-tangent.md` (overwrite if present). This is the
   FULL protocol, loaded ON DEMAND — it is NOT added to `instructions`:
       https://raw.githubusercontent.com/patiently/anti-tangent-mcp/v${VERSION}/INTEGRATION.md
7. opencode has no skill mechanism, so wire only a SLIM POINTER into
   `instructions` (not the full file). Download the pointer template:
       https://raw.githubusercontent.com/patiently/anti-tangent-mcp/v${VERSION}/examples/anti-tangent-pointer.md
   to `~/.config/opencode/anti-tangent-pointer.md`, then replace the token
   `__ANTI_TANGENT_DOC_PATH__` in it with the absolute path to the
   `anti-tangent.md` you downloaded in step 6. Add ONLY the pointer's absolute
   path to opencode's top-level `instructions` array in the same
   `opencode.json` you edited in step 5:
       {
         "instructions": ["/abs/path/to/.config/opencode/anti-tangent-pointer.md"]
       }
   If an `instructions` array is already present, append the pointer path only
   if not already listed. Do NOT add `anti-tangent.md` itself to `instructions`
   — it must stay on-demand.
```

- [ ] **Step 5: Add a plugin note to the Integration section (~line 404-406)**

After the existing paragraph that points to `INTEGRATION.md`, append:

```
For Claude Code, the recommended install packages this playbook as the
`anti-tangent-protocol` plugin, which loads `INTEGRATION.md` on demand only when
a task carries a Goal/Acceptance-criteria header (see the one-shot install
above). opencode loads it on demand via the slim pointer
(`examples/anti-tangent-pointer.md`).
```

- [ ] **Step 6: Verify + commit**

```bash
! grep -nE '@[^ ]*/\.claude/anti-tangent\.md' README.md && \
! grep -nE '"instructions":[^]]*/anti-tangent\.md"' README.md && \
grep -q 'anti-tangent-protocol@anti-tangent-mcp' README.md && \
grep -q 'anti-tangent-pointer' README.md && \
grep -q '__ANTI_TANGENT_DOC_PATH__' README.md && \
grep -q 'must stay on-demand' README.md && echo OK
git add README.md
git commit -m "docs(readme): slim install — plugin (CC) + slim pointer (opencode), drop always-inlined INTEGRATION.md"
```
Expected: `OK`, then a commit.

---

### Task 5: CLAUDE.md editing-invariants section + CHANGELOG [0.11.1]

**Goal:** Document the two INTEGRATION.md invariants (size + bundled-copy sync) for future editors, and add the release changelog entry.

**Files:**
- Modify: `CLAUDE.md` (new `## Editing INTEGRATION.md` section, after `## Architecture`)
- Modify: `CHANGELOG.md` (new `## [0.11.1] - 2026-07-08` block below the header, above `## [0.11.0]`)

**Acceptance Criteria:**
- [ ] `CLAUDE.md` documents the < 40,000-byte budget AND the `cp` resync of the bundled plugin copy.
- [ ] `CHANGELOG.md` has a `## [0.11.1] - 2026-07-08` entry with `### Changed` + `### Added` describing the slim install + plugin.
- [ ] `go build ./...` still succeeds (no code touched).

**Verify:**
```bash
grep -q '## Editing INTEGRATION.md' CLAUDE.md && \
grep -q 'cp INTEGRATION.md plugin/anti-tangent-protocol/INTEGRATION.md' CLAUDE.md && \
grep -q '## \[0.11.1\] - 2026-07-08' CHANGELOG.md && \
go build ./... && echo OK
```
Expected: `OK`

**Steps:**

- [ ] **Step 1: Add the CLAUDE.md section**

In `CLAUDE.md`, immediately after the `## Architecture` section (before `## Branch & Version Conventions`), insert:

```markdown
## Editing INTEGRATION.md

`INTEGRATION.md` is the single source of truth for the integration protocol, and
is bundled byte-for-byte into the `anti-tangent-protocol` plugin so Claude Code
can load it on demand. Two invariants are CI-enforced:

- It must stay **under 40,000 bytes** (user-instructions context budget).
- The bundled copy `plugin/anti-tangent-protocol/INTEGRATION.md` must be
  **identical** to root.

After editing `INTEGRATION.md`, resync the bundled copy in the same commit:

```bash
cp INTEGRATION.md plugin/anti-tangent-protocol/INTEGRATION.md
```
```

- [ ] **Step 2: Add the CHANGELOG entry**

In `CHANGELOG.md`, insert immediately above `## [0.11.0] - 2026-07-07`:

```markdown
## [0.11.1] - 2026-07-08

### Changed
- Recommended install is now slim + on-demand. Claude Code installs the new
  `anti-tangent-protocol` plugin — a description-triggered skill that `Read`s the
  bundled `INTEGRATION.md` only when a task carries a Goal/Acceptance-criteria
  header — instead of `@`-importing the full ~40 KB `INTEGRATION.md` into global
  `~/.claude/CLAUDE.md`. opencode wires a slim pointer into `instructions` and
  loads the full document on demand. The always-loaded footprint drops from
  ~10k tokens to a single skill-description line.

### Added
- `plugin/anti-tangent-protocol/` — companion plugin carrying the protocol as an
  on-demand skill; registered in the marketplace.
- `examples/anti-tangent-pointer.md` — slim opencode / non-skill-host pointer
  template.
- CI guard that the plugin's bundled `INTEGRATION.md` stays byte-identical to
  root.

```

- [ ] **Step 3: Verify + commit**

```bash
grep -q '## Editing INTEGRATION.md' CLAUDE.md && \
grep -q 'cp INTEGRATION.md plugin/anti-tangent-protocol/INTEGRATION.md' CLAUDE.md && \
grep -q '## \[0.11.1\] - 2026-07-08' CHANGELOG.md && \
go build ./... && echo OK
git add CLAUDE.md CHANGELOG.md
git commit -m "docs: INTEGRATION.md editing invariants + CHANGELOG 0.11.1"
```
Expected: `OK`, then a commit.

---

## Self-Review

**Spec coverage:** Component 1 (plugin) → Task 1; Component 2 (CI guard) → Task 2; Component 3 (opencode pointer) → Task 3; Component 4 (README) → Task 4; Component 5 (CLAUDE.md + CHANGELOG) → Task 5. The "no plugin CLAUDE.md" constraint is a Task 1 AC. All spec sections mapped.

**Placeholder scan:** All file contents are given verbatim; the only intentional literal token is `__ANTI_TANGENT_DOC_PATH__` (a runtime substitution point, documented as such). No TBD/TODO.

**Type/name consistency:** Plugin name `anti-tangent-protocol`, skill name `anti-tangent-protocol:anti-tangent-protocol`, bundled path `plugin/anti-tangent-protocol/INTEGRATION.md`, relative skill reference `../../INTEGRATION.md`, and the `__ANTI_TANGENT_DOC_PATH__` token are used identically across Tasks 1-5 and the verify commands.

**Dependencies:** Task 2 blockedBy Task 1 (needs the bundled copy). Task 4 blockedBy Task 1 + Task 3 (references the plugin + pointer artifacts). Tasks 3 and 5 are otherwise independent.
