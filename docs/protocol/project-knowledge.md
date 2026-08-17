# anti-tangent protocol — project knowledge (optional)

Read [`core.md`](core.md) and [`controller.md`](controller.md) first. This part is
**controller-only** — implementing subagents make no prime/extract calls. Skip it entirely
unless a knowledge base is configured.

## Project knowledge (optional)

An optional v0.6.0+ loop that grounds the reviewer in **what's already true about your project** — decisions, module invariants, feature surfaces, glossary terms, epic progress. Earns its keep on epic-scale projects with multiple agents and multiple authors where each task validates cleanly but the pieces stop composing into a working end product. Skip on single-author or short-lived projects.

Two new MCP tools — `prime_project_knowledge` (pre-task; recommends notes to read) and `extract_project_knowledge` (post-task; proposes notes to write) — plus a `project_knowledge` field on `validate_task_spec` and `validate_plan`. Knowledge lives in [Basic Memory](https://github.com/basicmachines-co/basic-memory) (recommended) or any markdown-backed store; anti-tangent has **zero code dependency** on Basic Memory.

Architecture diagram and component boundaries: see [project-knowledge design spec §1](https://github.com/patiently/anti-tangent-mcp/blob/main/docs/superpowers/specs/2026-05-18-project-knowledge-design.md#1-architecture--boundaries).

### Controller workflow (per epic)

The server is stateless; the controller's dispatch logic ties prime → implement → extract together.

1. **Before dispatch.** Search the KB by task terms + epic's `touches_modules` / `relates` → `kb_index`. Call `prime_project_knowledge` with task fields + `kb_index` + `epic_permalink`; it returns `picks` (and `bm_commands` when `ANTI_TANGENT_KB_STORE=basic-memory`). Read the picked notes into a `kb_excerpts` markdown string.
2. **Dispatch.** Include `kb_excerpts` in the implementer's brief AND pass it verbatim as `project_knowledge` into `validate_task_spec`. The subagent makes no prime/extract calls.
3. **After DONE.** Call `extract_project_knowledge` with the completion envelope(s), `kb_index`, optional `current_kb_excerpts`, and `epic_permalink`. Returns `proposals` (and `bm_commands` when configured).
4. **Apply.** A human (or the controller, gated by the ladder below) reviews proposals and pastes the `bm_commands` — see the "Applying bm_commands to BM v0.21.1" subsection immediately below for the translation steps **before** you paste.

### Applying bm_commands to BM v0.21.1

Anti-tangent's `bm_commands` arrays are paste-ready *conceptual* shape — the tool names match BM verbatim, but the arg shapes track the spec's logical model rather than each BM release's literal signature (the explicit non-goal: don't couple anti-tangent to BM's per-release API churn). Field-tested against BM v0.21.1 on 2026-05-21, three small translation steps land between paste and apply.

**`write_note` arg mapping** (extract's `Proposal{action: "create"}` and supersede-leg-1):

| Extract emits | BM v0.21.1 takes | Mapping |
|---|---|---|
| `permalink: "<dir>/<slug>"` | `directory` + `title` | Split on the last `/`; prefix is `directory`. Pass `proposal.title` directly as `title` rather than slug-back-to-title. |
| `frontmatter: {…}` | `metadata: {…}` | Verbatim — BM merges into the YAML frontmatter at the top of the file. |
| `body: "…"` | `content: "…"` | Verbatim. |
| `proposal.type` | `note_type` | E.g. `"decision"`, `"epic"`, `"feature"`, `"module"`, `"glossary"`. |

**`edit_note` operation hints.** BM v0.21.1's `edit_note` requires an explicit `operation` enum that extract does not emit; the agent picks based on the target note's structure:

- Ledger / "Recent material changes" appends — `insert_before_section` keyed on the section AFTER your target (puts the new entry at the bottom of the target section without clobbering).
- Supersede-leg-2 (flipping a predecessor's `status` to `superseded`) — `find_replace` against the frontmatter line, or BM's frontmatter-patch verb if available in your version.
- Replacing a whole section's body — `replace_section`.
- Appending to the very end of the note (no section anchor) — `append`.

**Permalink-slug expectations.** BM auto-derives the stored slug from `title` (lowercased, hyphenated), so the permalink extract proposes (e.g. `<PROJECT>/decisions/0042-docker-bm-deployment-is-alternative`) diverges from what BM stores. Cross-links (`epic_origin`, etc.) then won't resolve. Cleanest fix: a **three-step pattern** — `write_note` to create, `move_note` to the canonical path, `edit_note(find_replace)` to rewrite the YAML `permalink:` line. Step 3 is load-bearing; steps 1+2 alone leave wikilinks broken.

**Worked example.** See [`plugin/bm-scribe/docs/three-step-pattern.md`](https://github.com/patiently/anti-tangent-mcp/blob/main/plugin/bm-scribe/docs/three-step-pattern.md) for a literal end-to-end example showing `write_note → move_note → read_note → edit_note(find_replace)` with annotated BM responses at each step. The `plugin/bm-scribe/` plugin shipped from this repo encodes this pattern across every creator skill.

### Eight note types in three groups

| Type | Layer | Body |
|---|---|---|
| `decision` | durable | ADR-style; append-only; new decisions supersede old ones |
| `module` | durable | coherent capabilities (user-facing surface), not 1:1 Go packages |
| `feature` | durable | user-facing capability catalog with release-tagged change pointers |
| `glossary` | durable | canonical domain-term definitions |
| `howto` | durable | operational runbook; slug key; update-in-place; `status: active`/`deprecated` (v0.9.0+) |
| `epic` | operational | live dashboard: charter, stories table, open PRs, acceptance checklist, progress ledger |
| `story` | operational | live dashboard: brief, multi-PR table, subtasks, deployment state, decisions produced (v0.7.0+) |
| `gotcha` | lessons-learned | module-scoped lesson learned; ADR-numbered slug; supersede chain (v0.8.0+) |

Templates: [`examples/project-knowledge/`](https://github.com/patiently/anti-tangent-mcp/tree/main/examples/project-knowledge); frozen real examples: [`examples/project-knowledge/dogfood/`](https://github.com/patiently/anti-tangent-mcp/tree/main/examples/project-knowledge/dogfood). Per-project tuning: [`docs/team-setup/project-knowledge-conventions.md`](https://github.com/patiently/anti-tangent-mcp/blob/main/docs/team-setup/project-knowledge-conventions.md).

### v0.7.0 canonical layout

Permalinks follow `<PROJECT>/<type>/<key>/main`. Type folders are **plural** (`epics`, `stories`, `decisions`, `modules`, `features`, `glossary`, `gotchas`, `howtos`); `<key>` is a `<TICKET-ID>` for epics/stories, a `<NNNN>-<slug>` (ADR-numbered) for decisions and gotchas, a `<slug>` for modules/features/howtos, and a `<term>` for glossary. Example: `monorepo/decisions/0001-text-only-reviewer/main`. The `plugin/bm-scribe/` plugin (v0.7.1+) auto-picks ADR numbers and enforces this layout.

### The `project_knowledge` field

`validate_task_spec` and `validate_plan` accept an optional `project_knowledge` string (markdown ok). The reviewer treats it as **authoritative** — same posture as `pinned_by` — so stated facts are not flagged as `unverifiable_codebase_claim`. Counts against the 200 KB payload cap; keep under ~16 KB per call (prime's picks keep it bounded).

`check_progress` and `validate_completion` deliberately do **not** accept `project_knowledge` (spec §3.3): the field is session-context-only, never persisted, because KB content can change during a task's session and a snapshot taken at `validate_task_spec` time would silently drift.

### Auto-apply ladder for extract proposals

Recommended default disposition (the server doesn't enforce; teams can override):

| Proposal kind | Default disposition |
|---|---|
| `epic` progress-ledger append | Auto-apply |
| `feature` "Recent material changes" append | Auto-apply |
| `decision` create with `status: proposed` | Auto-apply (draft for humans to review) |
| `decision` create with `status: accepted` | **Human review** |
| `decision` supersede | **Human review** |
| `module` invariant/convention edit | **Human review** |
| `glossary` create | Auto-apply |
| `howto` create | **Human review** |
| `howto` update | **Human review** |
| Anything with `contradicts_existing` finding | **Human review, blocking** |

### Anchored Basic Memory tool names

When `ANTI_TANGENT_KB_STORE=basic-memory`, prime and extract emit `bm_commands` arrays referencing canonical BM tool names (`search_notes`, `read_note`, `write_note`, `edit_note`, `move_note`, `delete_note`). BM has no `supersede_note` verb — a `Proposal{action: "supersede"}` maps to `write_note` (new note with `status: accepted`, `supersedes: [<predecessor>]`) plus `edit_note` flipping the predecessor's `status` to `superseded`. Full contract: [Basic Memory contract block](https://github.com/patiently/anti-tangent-mcp/blob/main/docs/superpowers/plans/2026-05-19-project-knowledge-v0.6.0.md#basic-memory-contract-verified-yyyy-mm-dd) at the bottom of the v0.6.0 plan.

For the operator-side topology of running BM as a shared service across a team, see [`docs/team-setup/basic-memory-shared-vm.md`](https://github.com/patiently/anti-tangent-mcp/blob/main/docs/team-setup/basic-memory-shared-vm.md) — covers a dedicated VM via stdio-over-SSH and a Docker container via SSE behind a reverse proxy.

### Environment variables

Defaults shown; see [`README.md`](https://github.com/patiently/anti-tangent-mcp/blob/main/README.md) for the full dotenv block.

- `ANTI_TANGENT_KB_STORE` — `""` (off). Set to `basic-memory` to enable `bm_commands` arrays in prime/extract outputs. Any other non-empty value is rejected at startup.
- `ANTI_TANGENT_PRIME_MODEL` — reviewer for `prime_project_knowledge`. Falls back to `ANTI_TANGENT_PLAN_MODEL` then `ANTI_TANGENT_PRE_MODEL`.
- `ANTI_TANGENT_EXTRACT_MODEL` — reviewer for `extract_project_knowledge`. Same fallback chain.
- `ANTI_TANGENT_PRIME_MAX_TOKENS` — output cap for prime; default `4096`. Ceiling-clamped by `ANTI_TANGENT_MAX_TOKENS_CEILING`.
- `ANTI_TANGENT_EXTRACT_MAX_TOKENS` — output cap for extract; default `8192`. Ceiling-clamped by `ANTI_TANGENT_MAX_TOKENS_CEILING`.

Existing flows are unaffected when both `ANTI_TANGENT_KB_STORE` and `project_knowledge` are unset (backward-compat guarantee).
