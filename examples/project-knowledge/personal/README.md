# Personal namespace templates

Templates for the per-user personal namespace introduced in v0.7.1. The `plugin/bm-scribe/` plugin in this repo writes notes that conform to these shapes; humans can also paste them directly into BM.

## What the personal namespace is

The personal namespace lives **in the same BM project** as project-knowledge notes, namespaced by the user's handle (e.g. `pgilmore`, `alice`, `bob`). Two note types ship in v0.7.1:

- `<USERNAME>/todo/main.md` — one rolling checkbox list per user.
- `<USERNAME>/notes/<slug>/main.md` — one personal note per topic.

## Why anti-tangent ignores it

`prime_project_knowledge` and `extract_project_knowledge` operate over the `<PROJECT>/` prefix only — personal-namespace permalinks (`<USERNAME>/…`) are never indexed for the reviewer, never picked for primes, never proposed by extract. This is enforced at the `kb_index` filter the controller passes in; anti-tangent itself never sees the `<USERNAME>/` notes.

The boundary is intentional: personal notes are personal. Project knowledge is shared.

## See also

- [`docs/team-setup/project-knowledge-conventions.md`](../../../docs/team-setup/project-knowledge-conventions.md) § 9 — full conventions for the personal namespace.
- [BM scribe design spec](../../../docs/superpowers/specs/2026-05-21-bm-scribe-design.md) — the plugin that writes these.
- [`plugin/bm-scribe/`](../../../plugin/bm-scribe/) — the plugin implementation.
