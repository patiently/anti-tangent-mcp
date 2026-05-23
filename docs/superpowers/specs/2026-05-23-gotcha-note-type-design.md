# Gotcha note type — design spec

**Status:** proposed
**Version target:** v0.8.0
**Backing issue:** [#35](https://github.com/patiently/anti-tangent-mcp/issues/35)
**Authors:** Patrick Gilmore

## 1. Overview

A "gotcha" is a project-and-module-scoped lesson learned during implementation or review — the kind of finding that's easy to re-introduce on the next plan unless someone wrote it down. This spec adds gotchas as a **seventh project-knowledge note type** alongside the existing six (`decision`, `module`, `feature`, `glossary`, `epic`, `story`), introduces one new bm-scribe creator skill to capture them, and relies entirely on the existing `prime_project_knowledge` loop to surface them on future plans.

Two intake paths:

- **Post-plan** — anti-tangent's `extract_project_knowledge` proposes gotcha-typed proposals from completion envelopes, the user curates them via the new skill.
- **Post-review** — the skill mines gotcha candidates from code-review output (CodeRabbit PR comments, `/ultrareview`, `/code-review`, `/security-review`) using inline Claude prompts inside the skill.

Both paths converge on the same creator step (the three-step BM v0.21.1 pattern, ADR-numbered slug).

## 2. Goals

- **Capture institutional memory at the moment it's freshest** — at end-of-plan, when the implementer's surprise is still in scope; and at end-of-review, when fresh eyes have just looked at the diff.
- **Re-use existing prime/validate_plan plumbing.** Gotchas stored as BM notes with `modules: [...]` frontmatter are findable by the existing kb_index/picks loop. No reviewer-prompt change in prime; no new MCP tool.
- **Preserve the cross-model-review property where it already exists.** For the post-plan path, anti-tangent's extract reviewer (different model from implementer) does the mining. For the post-review path — where the source is already external output — mining happens inside the skill on the host model.
- **One discoverable command.** A single `/bm-scribe:create-gotcha` skill handles both paths, dispatching on its argument.

## 3. Non-goals

- **No new MCP tool.** No `mine_gotchas` server endpoint. Mining for review-text sources stays client-side in the skill.
- **No automatic invocation.** The skill is user-triggered like every other bm-scribe creator skill. Auto-running it from a controller is out of scope.
- **No automatic supersede detection.** The user names a predecessor explicitly when superseding; the skill does not try to fuzzy-match new gotchas against existing ones.
- **No status filtering in `prime_project_knowledge`.** Whether superseded gotchas should rank lower is handled by the controller's kb_index search + prime's ranking, not by a code-level filter in anti-tangent.
- **No batch / cron / scheduled invocation.**
- **No persistent state in the skill.** Session-scoped only, same as every bm-scribe skill.

## 4. Architecture & component boundaries

Two repos, three components, one new wire.

```text
┌───────────────────── anti-tangent-mcp (Go) ──────────────────────┐
│  internal/verdict/extract.go                                     │
│    + ProposalTypeGotcha                       (new enum value)   │
│    + reviewer-schema entry                                       │
│  internal/prompts/extract.tmpl                                   │
│    + new "gotcha" category section in the template               │
│  internal/prompts/testdata/extract_basic.golden                  │
│    + regenerated with `-update`                                  │
│                                                                  │
│  internal/verdict/prime.go        (NO change)                    │
│  internal/prompts/prime.tmpl      (NO change)                    │
└──────────────────────────────────────────────────────────────────┘
                   │   ExtractResult.proposals[type=gotcha]
                   ▼
┌──────────────────── plugin/bm-scribe ───────────────────────────┐
│  skills/create-gotcha/SKILL.md       (NEW)                       │
│    Path A intake: structured extract proposals (no mining)       │
│    Path B intake: review text → inline mine → walk candidates    │
│    Creator: three-step BM v0.21.1 pattern, ADR-numbered slug     │
│                                                                  │
│  Supersede mechanics: write_note (status: accepted, supersedes)  │
│                       + edit_note (flip predecessor status)      │
└──────────────────────────────────────────────────────────────────┘
```

**Anti-tangent's responsibility** ends at producing `Proposal{type: gotcha, ...}` records in the existing extract envelope. It does not know about review text, PR comments, or the skill.

**bm-scribe's responsibility** is the entire user-facing flow: detect intake mode from skill args, mine when needed, walk candidates with the user, apply the three-step BM pattern, and handle supersede chains.

**Prime (the read side)** needs no code change. Once gotchas exist as notes with `modules: [...]` frontmatter, the existing `prime_project_knowledge` loop finds them via `kb_index` like any other note type.

## 5. Data flow

### 5.1 Path A — post-plan (default invocation)

```text
[implementer DONE]
       │
       ▼
controller calls extract_project_knowledge(envelopes, kb_index, epic_permalink)
       │
       ▼
ExtractResult.proposals[] includes Proposal{type: "gotcha", ...}
       │
       ▼
controller presents proposals to user (existing flow); user runs:
       /bm-scribe:create-gotcha
       │
       ▼
skill reads the most recent extract envelope from conversation context
       │
       ▼
for each Proposal{type: gotcha}:
   • show user: title, modules, evidence_refs, proposed body
   • user: accept / edit / skip
   • on accept → three-step BM pattern
   • on supersede → write new + edit_note to flip predecessor.status
```

### 5.2 Path B — post-review

Invocation: `/bm-scribe:create-gotcha --from-review <source>` where `<source>` matches one of three literal forms:

- `pr:<N>` — PR number on the repo of the current working directory
- a filesystem path (absolute or relative) ending in any extension — read as text
- `paste:` — followed by a heredoc-style multi-line block

```text
skill normalizes source → review_text:
   • pr:<N>       → gh api repos/<owner>/<repo>/issues/<N>/comments
                  + gh api repos/<owner>/<repo>/pulls/<N>/reviews
                  (deduped by comment id; all reviewers in scope by default)
   • file path    → read file
   • paste:       → multi-line stdin
       │
       ▼
skill runs an inline Claude prompt:
   input  = review_text + module-list from kb_index
   output = [{title, modules, severity, symptom, root_cause, how_to_avoid}, ...]
       │
       ▼
user walks candidates (same accept / edit / skip flow as Path A)
       │
       ▼
three-step BM pattern per accepted candidate
```

Both paths converge on the same creator step. Only the candidate source differs.

## 6. Note shape

### 6.1 Permalink (ADR-style, mirrors `decision`)

```text
<PROJECT>/gotchas/<NNNN>-<slug>/main
```

`<NNNN>` is the next zero-padded number across all gotchas in the project, computed at write time by searching `<PROJECT>/gotchas/` — same logic `create-decision` already uses for ADR numbers. `<slug>` follows the same kebab-case derivation rule as `create-decision`'s slug (lowercased title with non-alphanumerics collapsed to hyphens).

### 6.2 Frontmatter

```yaml
---
title: GraphQL N+1 on driver-search
permalink: yc/gotchas/0042-graphql-n+1-on-driver-search/main
type: gotcha
status: accepted          # enum: accepted | superseded
modules: [driver-search, driver-network]
origin: yc/stories/YC-4521/main      # optional but recommended
severity: medium          # enum: low | medium | high
discovered_at: 2026-05-23
supersedes: []            # array of permalinks; empty unless this replaces an earlier gotcha
---
```

`status` mirrors `decision`: `accepted` for live, `superseded` for the predecessor of a chain. No `proposed` state — gotchas come in already curated by the user via the skill.

### 6.3 Body sections

```markdown
## Symptom
<what was observed — concrete, reproducible if possible>

## Root cause
<why it happens — code paths, invariants violated, env quirks>

## How to avoid
<the actionable rule for future plans touching these modules>

## Evidence
- <link to PR / commit / review comment / log line>
- <link to test that pins the fix, if any>

## Related
- [[<wikilink to module note>]]
- [[<wikilink to origin story/epic>]]
```

The "How to avoid" section is the load-bearing one — that's what prime excerpts into `project_knowledge` for the next plan, and what `validate_plan`'s reviewer reads when flagging ACs that would reproduce a known gotcha.

### 6.4 Supersede mechanics

Same two-call shape as `decision`:

1. **Write the new gotcha** — `write_note` with `supersedes: [<predecessor permalink>]` in metadata, then `move_note` + `edit_note(find_replace)` to fix the YAML `permalink:` line.
2. **Flip the predecessor** — `edit_note(find_replace)` on the predecessor's frontmatter: `status: accepted` → `status: superseded`.

If step 2 fails (e.g. predecessor doesn't exist), the new note is left as a standalone gotcha and the skill reports the failure — safer than attempting an automatic rollback.

## 7. Prime integration (the read side)

No code change in anti-tangent. The existing prime loop works once notes exist.

### 7.1 How a gotcha reaches the next plan

```text
new plan touches module `driver-search`
       │
       ▼
controller's pre-dispatch step (per INTEGRATION.md §"Project knowledge"):
   • build kb_index from BM search:
       search_notes(query="driver-search")  →  includes gotchas/0042-*
   • call prime_project_knowledge(task_fields, kb_index, epic_permalink)
       │
       ▼
prime's reviewer LLM ranks picks; gotchas with modules:[driver-search]
score high because the task names that module
       │
       ▼
controller reads picked gotcha notes → kb_excerpts string
       │
       ▼
kb_excerpts passed into validate_plan(project_knowledge=...)
and dispatched to the implementer as auto-attached "Project knowledge"
       │
       ▼
validate_plan's reviewer treats gotchas as authoritative
```

### 7.2 Doc updates in `docs/team-setup/project-knowledge-conventions.md`

1. **Search hint for controllers.** Add gotchas to the list of note types the pre-dispatch search should query. One-line addition: "search for gotchas matching the plan's `touches_modules` and include hits in kb_index."
2. **Superseded ranking note.** Document that controllers should include each gotcha's `status` field in its `kb_index` entry (alongside `permalink`, `title`, `modules`). Prime's reviewer reads `status: superseded` and naturally ranks those lower than `status: accepted` peers when both match a task's module set — no code change, no prompt change, the mechanism is just "give the reviewer the signal it needs." Superseded entries still carry "we used to have this, here's the resolution" value, so they remain in scope rather than being filtered out. If reviewer over-weighting becomes a problem in practice, revisit with a status-aware filter (deferred per §10).

### 7.3 What stays unchanged

- `prime_project_knowledge` reviewer prompt — relevance ranking already handles gotchas, no need to teach the prompt about the type.
- `validate_plan` reviewer prompt — already treats `project_knowledge` as authoritative; gotcha excerpts inherit that.
- BM schema — gotchas are plain markdown with frontmatter.

## 8. Testing & verification

### 8.1 Anti-tangent (Go)

| What | Where | How |
|---|---|---|
| `ProposalTypeGotcha` enum + JSON schema | `internal/verdict/extract_test.go` | Table test: parser accepts `{type: "gotcha"}`; schema invariants test (`schema_invariants_test.go`) covers the new value |
| Extract reviewer prompt | `internal/prompts/testdata/extract_basic.golden` | Regenerate with `-update` after editing the template; review the diff to confirm only the new "gotcha" section was added |
| Round-trip via extract handler | `internal/mcpsrv/integration_test.go` | Stub reviewer returning a gotcha proposal; assert handler decodes and forwards it unchanged |

`-race ./...` remains the mainline command (per project `CLAUDE.md`).

No `prime.go` change → no prime test change.

### 8.2 bm-scribe skill (smoke against real BM)

Skills are markdown contracts. Validation is a smoke run end-to-end:

1. Run extract on a real plan completion that introduced a known gotcha; assert proposal includes `type: gotcha`.
2. Run `/bm-scribe:create-gotcha` against that proposal; assert the three-step BM pattern lands a note at the canonical permalink with frontmatter intact.
3. Run a supersede: invoke the skill again with a new gotcha that names the old one as predecessor; assert (a) new note has `supersedes: [old]`, (b) old note's frontmatter flipped to `status: superseded`.
4. Run prime against a new plan touching the same module; assert the live gotcha appears in `picks` and the superseded one ranks lower or is filtered.

### 8.3 Failure modes the skill must handle explicitly

| Failure | Handling |
|---|---|
| Mining produces zero candidates (Path B) | Print `no gotcha candidates found in <source>` and exit cleanly. Not an error. |
| User edits a candidate to empty | Treat as skip; no note written. |
| Supersede predecessor doesn't exist | `edit_note(find_replace)` returns no-match; skill aborts with a clear message. Does NOT roll back the new note — leaves it standalone for user cleanup. |
| `BM_SCRIBE_PROJECT` unset | Ask user, cache for the session (matches every other bm-scribe creator skill). |
| PR fetch fails (`gh` not authed, network) | Fall back to asking user to paste review text into a heredoc. |
| Inline mining prompt returns malformed JSON | One retry with a JSON-only reminder (mirrors anti-tangent's reviewer parse-retry pattern); after that, exit with the raw response so user can salvage manually. |

## 9. Versioning & rollout

- **Version target:** `v0.8.0` (backward-compatible feature: new ProposalType + new plugin skill).
- **Branch:** `version/0.8.0`.
- **CHANGELOG entries:** under `## [0.8.0]` — `### Added`: "Gotcha note type proposed by `extract_project_knowledge`" and "`bm-scribe:create-gotcha` skill (post-plan + post-review intake paths)."
- **Migration:** none. Existing KBs work unchanged; gotchas appear only when extract starts proposing them.
- **Backward compatibility:** consumers parsing `ExtractResult.proposals` see a new `type` value. The `ProposalType` enum is already an open string in the wire schema (existing consumers should treat unknown types defensively per the strict-mode invariant). If anyone is doing exhaustive switches on `ProposalType`, they'll get a compile error in Go and a runtime miss in TS — flagged as a release-note item.

## 10. Open questions

None blocking. Two things to monitor after rollout:

- **Mining-prompt quality on Path B.** If review-text mining produces too many false positives, we may need to tighten the inline prompt or move mining server-side (the option this spec explicitly rejected). First evidence: dogfood for 2–3 plans and count accept/reject ratio.
- **Superseded ranking.** If superseded gotchas crowd active ones in prime's picks, add a status-aware ranking hint to the conventions doc or, as a last resort, a status filter in prime.
