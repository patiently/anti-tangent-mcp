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
