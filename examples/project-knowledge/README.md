# Project-knowledge note templates

Seven templates seed the project-knowledge schema used by the optional v0.6.0+
`prime_project_knowledge` and `extract_project_knowledge` tools. They are
markdown with [Basic Memory](https://github.com/basicmachines-co/basic-memory)
frontmatter; copy a template into your shared KB and fill it in.

**Authoritative spec:** [`docs/superpowers/specs/2026-05-18-project-knowledge-design.md`](../../docs/superpowers/specs/2026-05-18-project-knowledge-design.md) (v0.6.0 base) + [`docs/superpowers/specs/2026-05-21-project-knowledge-conventions-design.md`](../../docs/superpowers/specs/2026-05-21-project-knowledge-conventions-design.md) (v0.7.0 conventions layer).

**Adopter tuning:** [`docs/team-setup/project-knowledge-conventions.md`](../../docs/team-setup/project-knowledge-conventions.md) covers issue-ID format, folder convention, milestone events, and the per-project tuning loop. Read this before adopting.

**Dogfood examples:** [`dogfood/`](dogfood/) contains frozen-snapshot real notes from anti-tangent's own KB at v0.7.0. Study them for shape and rationale; do not copy verbatim.

## Seven types in three groups

- **Durable reference layer** (timeless / slow-evolving): `decision`, `module`, `feature`, `glossary`. Survives epics.
- **Operational layer** (time-bounded; live state during work): `epic`, `story`. Both terminate at completion — epics with `status: closed` (PM gesture), stories with `status: done` (engineering gesture).
- **Lessons-learned layer** (module-scoped; surfaced on future plans): `gotcha`. Accumulates across epics; superseded entries remain in scope but rank lower.

## Permalink convention

All seven types use the shape `<PROJECT>/<type>/<key>/main`, where:
- `<PROJECT>` is the BM project name (one BM project per git repo; see the conventions doc).
- `<type>` is one of `decisions`, `modules`, `features`, `glossary`, `epics`, `stories`, `gotchas`.
- `<key>` is either a slug (decisions, modules, features, glossary, gotchas — gotchas use ADR-numbered slugs like decisions) or a ticket ID (epics, stories).
- The trailing `/main` allows arbitrary side-docs (charter, retro, sub-decisions) to live in the same folder.

## Modules describe coherent capabilities, not Go packages

A `module` note describes one user-facing capability — what the module DOES — and may span multiple Go packages. Example from anti-tangent: `modules/review-pipeline/main` covers `internal/mcpsrv` + `internal/verdict` + `internal/prompts` + `internal/providers` jointly implementing the `validate_X` surface. Avoid the temptation to write one module note per Go package; the user-facing surface is the right granularity.

## Maintenance ownership

| Type | Author at birth | Updated by |
|---|---|---|
| `epic` | Human at kickoff | Mostly automated (extract appends ledger via milestone events; humans edit open questions and AC checklist) |
| `story` | Human at story-open | Mostly automated (extract updates dashboard sections on milestone events; humans set `status: done` to signal terminal merge) |
| `decision` | Drafted by extract → reviewed by human → merged | Append-only; new decisions supersede old ones |
| `module` | Human (or seeded from a spec) | Mostly human; extract proposes invariant/convention edits when it sees drift |
| `feature` | Human (or seeded from a spec) | Mostly human; extract proposes "Recent material changes" entries |
| `glossary` | Opportunistic (human or extract) | Opportunistic |
| `gotcha` | Drafted by `extract_project_knowledge` (post-plan) or by `bm-scribe:create-gotcha --from-review <source>` (post-review) → reviewed by human → applied via the three-step BM creator pattern | Append-only; new gotchas supersede old ones via `action: "supersede"`; module-scoped via the `modules:` frontmatter array; prime surfaces accepted and superseded entries alike on future plans touching the same modules |
