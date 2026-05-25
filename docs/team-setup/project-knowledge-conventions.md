# Project-knowledge conventions

How to tune the v0.6.0+ project-knowledge feature for your team's project structure. Pairs with [`examples/project-knowledge/`](../../examples/project-knowledge/) templates and the v0.6.0 + v0.7.0 design specs.

## 1. When this pattern earns its keep

The conventions in this doc target **epic-scale, multi-agent, ticket-driven workflows** — teams running multi-week initiatives broken into tracked stories, with multiple PRs per story, agents and humans both contributing. If you're a single-author developer on a short-lived project, the v0.6.0 five-type taxonomy (decision / module / feature / glossary / epic) is enough — the operational dashboards in `epic.md` and `story.md` are overhead you don't need.

Pick the operational layer (`epic` + `story` dashboards) when:
- You track work as tickets in Jira / Linear / GitHub Issues / similar.
- Multiple PRs can land under one ticket (initial + follow-up).
- You want a single KB query to answer "where are we on epic X?"
- Multiple agents may pick up work without shared session context.

Skip it when:
- You're a single human doing small commits without ticket scaffolding.
- Your work doesn't accumulate; each PR is independent.

## 2. One BM project per git repo

Recommended pattern: **one BM project per git repo**. Name the BM project after the repo so the project-prefix in permalinks is self-explanatory.

**Exception: monorepos.** If your repo is a monorepo holding multiple products, run one BM project for the monorepo and namespace per-product notes under `products/<product-name>/` interior directories:

- `<MONOREPO>/products/<product>/decisions/<NNNN>-<slug>/main` — product-scoped decisions.
- `<MONOREPO>/products/<product>/stories/<TICKET-ID>/main` — product-scoped stories.
- `<MONOREPO>/decisions/<NNNN>-<slug>/main` — monorepo-wide decisions (rare; reserve for genuinely cross-product policy).

Substitute `<PROJECT>` in the shipped templates with the BM project name you pick.

## 3. Issue-ID format

Pick **one** consistent format for `<TICKET-ID>` across your KB. Common shapes:

- **Jira / Linear:** `ABC-123` (team prefix + number).
- **GitHub Issues:** `gh-NNN` or `issue-NNN` (avoid `#NNN` in paths — the `#` is fragile across some tools).
- **YouTrack:** `XX-NNN`.

The templates use `<TICKET-ID>` placeholder; adopters substitute. Don't mix formats within the same BM project — the project-prefix inference in `extract_project_knowledge` treats the entire permalink shape uniformly.

## 4. Folder convention

All seven note types use `<PROJECT>/<type>/<key>/main` where `<key>` is either a slug (`decisions`, `modules`, `features`, `glossary`, `gotchas`) or a ticket ID (`epics`, `stories`). Gotchas use ADR-numbered slugs (`0042-graphql-n+1-on-driver-search`) like decisions, not ticket IDs. The trailing `/main.md` allows arbitrary side-docs per ticket:

- `<PROJECT>/epics/<TICKET-ID>/main.md` — the live operational dashboard.
- `<PROJECT>/epics/<TICKET-ID>/charter.md` — extended charter (optional).
- `<PROJECT>/epics/<TICKET-ID>/retro.md` — retrospective written at epic-close.
- `<PROJECT>/stories/<TICKET-ID>/main.md` — story dashboard.
- `<PROJECT>/stories/<TICKET-ID>/postmortem.md` — post-incident note if applicable.

Extract's dashboard updates target the `main.md` files; side-docs are human-curated.

## 5. Milestone events

The reviewer prompt for `extract_project_knowledge` recognises these milestone events:

- **PR opened** (any state — draft or ready).
- **PR transitions state**: `draft → review`, `review → merged`, `review → closed-without-merge`.
- **Deployment lands** in any environment (staging, prod, etc.).
- **Decision finalizes** (`status: accepted` in the decision's frontmatter).

When a completion envelope surfaces one of these milestones, extract proposes a `replace_section` (or related) bm_command to update the relevant epic / story dashboard section.

**To extend with your own milestones:** the reviewer prompt is generic about what counts; if your team needs (say) "security review passed" as a milestone, mention it explicitly in your completion envelope's `summary` and the reviewer will recognise it as a milestone-like signal. Anti-tangent doesn't enforce a fixed milestone enumeration — the above list is the recommended default.

## 6. Project-prefix bootstrap (fresh setup AND v0.6.x migration)

The reviewer infers `<PROJECT>` from the most common prefix in `kb_index` permalinks. To bootstrap or migrate, follow the appropriate path below.

### Fresh setup (new KB)

1. Pick your `<PROJECT>` name (per §2).
2. Write your first `epic` note at `<PROJECT>/epics/<FIRST-TICKET>/main` using the shipped template.
3. From this point on, all `extract_project_knowledge` calls will infer the prefix correctly.

### Migration from v0.6.x (existing KB without project prefixes)

If your existing notes use the v0.6.x shape (`decisions/0042-x` — no project prefix), you have two migration paths:

**Path A — Bulk rename via BM `move_note` (recommended for small KBs, < 50 notes):**

```
move_note(permalink="decisions/0042-x", new_permalink="<PROJECT>/decisions/0042-x/main")
move_note(permalink="modules/foo",     new_permalink="<PROJECT>/modules/foo/main")
… repeat for each note …
```

Run via your BM MCP client. One-time setup; future `extract` calls see a consistent prefix immediately.

**Path B — Leave legacy notes; tag new notes with the prefix (recommended for larger KBs):**

Write all NEW notes under the v0.7.0 shape; let the legacy notes keep their old permalinks. The `missing_index_entry` finding will fire on the first `extract_project_knowledge` call (because legacy notes dilute the prefix-count vote). Once enough new-shape notes accumulate, the prefix becomes the majority and inference stabilises. Operationally noisier than Path A but avoids the bulk-rename step.

Either way, the v0.7.0 reviewer never modifies existing notes' permalinks — it proposes NEW notes under the new shape and emits `replace_section` updates against existing notes at their current permalinks.

## 7. Tracker integration

The `epic.md` and `story.md` templates have a `tracker_url` frontmatter field. Substitute your team's tracker URL pattern. Examples:

- Jira: `https://<org>.atlassian.net/browse/ABC-123`
- Linear: `https://linear.app/<team>/issue/TEAM-123`
- GitHub Issues: `https://github.com/<org>/<repo>/issues/NNN`
- YouTrack: `https://<org>.youtrack.cloud/issue/XX-NNN`

The reviewer prompt doesn't validate or enforce the URL shape — it's pure human-readable context for KB readers.

## Gotcha encoding in `kb_index` `tags`

The `KBIndexEntryArg` wire schema (`internal/mcpsrv/prime_handler.go`) carries `permalink`, `type`, `title`, `summary`, and `tags` — no dedicated `status` or `modules` fields. Anti-tangent's design explicitly does **not** extend the schema (see [gotcha design spec §3 + §7](../superpowers/specs/2026-05-23-gotcha-note-type-design.md)). When the controller builds a `kb_index` entry for a gotcha note, it MUST encode the gotcha's `status` and per-`modules` frontmatter into the existing `tags` array using two canonical key:value formats:

- `status:accepted` or `status:superseded` — one entry per gotcha.
- `module:<slug>` — one entry per module in the gotcha's frontmatter `modules:` array (a two-module gotcha contributes two `module:` tags).

Example: a gotcha at `<PROJECT>/gotchas/0042-graphql-n+1-on-driver-search/main` with frontmatter `status: accepted, modules: [driver-search, driver-network]` should produce this `KBIndexEntryArg`:

```json
{
  "permalink": "<PROJECT>/gotchas/0042-graphql-n+1-on-driver-search/main",
  "type": "gotcha",
  "title": "GraphQL N+1 on driver-search",
  "summary": "<the gotcha's How to avoid paragraph, ≤ 200 chars>",
  "tags": ["status:accepted", "module:driver-search", "module:driver-network"]
}
```

Prime's reviewer already considers `tags` when ranking relevance, so no prompt change is needed: a task touching `driver-search` matches the `module:driver-search` tag organically, and `status:superseded` reads as a de-prioritization signal to the reviewer's natural-language ranking. **Superseded entries remain in scope** rather than being filtered out — they still carry "we used to have this, here's the resolution" value, which prevents regressions on the next plan touching the same modules. If reviewer over-weighting becomes a problem in practice, revisit with a status-aware filter (deferred to a follow-up).

This encoding is not gotcha-specific in principle — any note type the controller wants to surface `status` or `module` signals for can use the same `status:<value>` / `module:<slug>` tag format. Gotchas are the first type to require it because prime's relevance match relies on module-scoping for them.

### Pre-dispatch search hints

Cover all seven note types when building `kb_index` for a new plan — search across `decisions`, `modules`, `features`, `glossary`, `epics`, `stories`, and `gotchas` for entries matching the plan's `touches_modules`. In particular, include `<PROJECT>/gotchas/` matches so accepted-and-superseded gotchas surface alongside relevant decisions, modules, and features.

## 8. Maintenance ownership

Who updates what:

| Action | Owner |
|---|---|
| Create an epic note at kickoff | Human (PM or tech lead) |
| Create a story note at story-open | Human (engineer picking up the ticket) |
| Update `## Stories` table on epic when a story changes status | Extract (via milestone events) |
| Update `## Open PRs` tables (story + epic) on PR state changes | Extract (via milestone events) |
| Append to `## Progress ledger` on milestone | Extract (via milestone events) |
| Tick `## Acceptance` checkboxes on epic | Human (at story-close or epic-close) |
| Set story `status: done` before final-PR merge | Human or agent (explicit closure gesture; signals terminal-merge to extract) |
| Write a decision note | Drafted by extract → reviewed by human → merged |
| Edit `## Open questions` on epic / story | Human (discussion notes; not auto) |

The principle: **extract proposes dashboard updates from milestone events; humans curate the durable layer**.

## 9. Personal namespace (`<USERNAME>/`)

v0.7.1 introduces an optional personal namespace that lives **inside the same BM project** as project knowledge, namespaced by each user's handle:

- `<USERNAME>/todo/main.md` — one rolling personal todo list per user (markdown checkbox bullets).
- `<USERNAME>/notes/<slug>/main.md` — one personal note per topic.

The personal namespace is **invisible to anti-tangent**. `prime_project_knowledge` and `extract_project_knowledge` operate over the `<PROJECT>/` prefix only; controllers should pass a `kb_index` that excludes `<USERNAME>/*` permalinks before calling either tool. This is the boundary that lets personal notes coexist with project knowledge in one BM project without leaking into the reviewer's context.

The `plugin/bm-scribe/` plugin owns the write side (`bm-scribe:add-todo`, `bm-scribe:add-note`, etc.); see [BM scribe design spec](../superpowers/specs/2026-05-21-bm-scribe-design.md) for the contract and [`examples/project-knowledge/personal/`](../../examples/project-knowledge/personal/) for ready-to-paste templates.

`<USERNAME>` is the user's chosen handle, not necessarily their OS username. One value per Claude Code installation; the plugin caches it in local state.

---

## See also

- [`examples/project-knowledge/`](../../examples/project-knowledge/) — the shipped templates.
- [`examples/project-knowledge/dogfood/`](../../examples/project-knowledge/dogfood/) — frozen-snapshot real anti-tangent example notes.
- [`examples/project-knowledge/personal/`](../../examples/project-knowledge/personal/) — personal-namespace templates (v0.7.1).
- [`plugin/bm-scribe/`](../../plugin/bm-scribe/) — the Claude Code plugin that writes notes per these conventions (v0.7.1).
- [v0.6.0 spec](../superpowers/specs/2026-05-18-project-knowledge-design.md) and [v0.7.0 spec](../superpowers/specs/2026-05-21-project-knowledge-conventions-design.md) — authoritative design docs.
- [`INTEGRATION.md` § "Project knowledge (optional)"](../../INTEGRATION.md#project-knowledge-optional) — generic-adopter integration guide.
