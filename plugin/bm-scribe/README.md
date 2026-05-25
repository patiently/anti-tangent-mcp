# bm-scribe

A Claude Code plugin that writes Basic Memory notes per the v0.7.0 project-knowledge conventions (see [`anti-tangent-mcp`](https://github.com/patiently/anti-tangent-mcp)) and the BM v0.21.1 three-step permalink-canonicalization pattern (see [`docs/three-step-pattern.md`](docs/three-step-pattern.md)).

## Install

Persistent (recommended), via the anti-tangent-mcp marketplace:

```bash
claude plugin marketplace add patiently/anti-tangent-mcp
claude plugin install bm-scribe@anti-tangent-mcp
```

Verify with `claude plugin list`. The plugin's twelve skills become available under the `bm-scribe:` namespace immediately.

Ephemeral (single-session test), via `--plugin-dir`:

```bash
claude --plugin-dir /path/to/anti-tangent-mcp/plugin/bm-scribe
```

Both forms require the `basic-memory` MCP server to be separately configured in Claude Code — bm-scribe is advisory wrapper, not a storage replacement.

## What it does

Wraps the standard `basic-memory` MCP tools (`write_note`, `move_note`, `edit_note`, `read_note`, `search_notes`) with twelve narrowly-scoped skills that enforce:

- v0.7.0 canonical permalink layout: `<PROJECT>/<type>/<key>/main`, plural type folders, ADR-numbered decisions.
- The three-step pattern: `write_note` (BM auto-derives the wrong slug) → `move_note` (path moves, permalink stays auto-derived) → `edit_note` find_replace on the YAML `permalink:` line (canonical permalink finally sticks).
- A per-user personal namespace at `<USERNAME>/todo/main` (rolling checkbox list) and `<USERNAME>/notes/<slug>/main` (per-topic notes).

The plugin is markdown-only — no executable runtime. All BM operations happen through the `basic-memory` MCP server.

## Subcommands

| Skill | What it does |
|---|---|
| `create-epic <TICKET-ID>` | Create a project-knowledge epic at `<PROJECT>/epics/<TICKET-ID>/main`. |
| `create-story <TICKET-ID>` | Create a project-knowledge story at `<PROJECT>/stories/<TICKET-ID>/main`. |
| `create-decision <slug>` | Create a project-knowledge decision at `<PROJECT>/decisions/<NNNN>-<slug>/main` with auto-picked ADR number. |
| `create-module <slug>` | Create a project-knowledge module at `<PROJECT>/modules/<slug>/main`. |
| `create-feature <slug>` | Create a project-knowledge feature at `<PROJECT>/features/<slug>/main`. |
| `create-glossary <term>` | Create a project-knowledge glossary term at `<PROJECT>/glossary/<term>/main`. |
| `create-gotcha [--from-review <source>]` | Create a project-knowledge gotcha at `<PROJECT>/gotchas/<NNNN>-<slug>/main` from extract proposals (default) or mined review text (`pr:<N>` / filesystem path / `paste:`). |
| `add-todo "<text>"` | Append a checkbox bullet to `<USERNAME>/todo/main` (creates note on first use). |
| `list-todos` | Print all checkbox bullets from `<USERNAME>/todo/main` with index numbers. |
| `tick-todo <n>` | Flip the n'th unchecked bullet to checked + date stamp. |
| `add-note <slug>` | Create a personal note at `<USERNAME>/notes/<slug>/main`. |
| `fetch-note <slug>` | Print a personal note at `<USERNAME>/notes/<slug>/main`. |
| `list-notes` | Print all personal notes under `<USERNAME>/notes/*`. |

## Environment

- `BM_SCRIBE_PROJECT` — BM project slug to write to (e.g. `monorepo`). Asked at first use if unset.
- `BM_SCRIBE_USERNAME` — handle for the personal namespace (e.g. `pgilmore`). Defaults to `$USER`.

## See also

- [Design spec](../../docs/superpowers/specs/2026-05-21-bm-scribe-design.md) — full rationale and contract.
- [Three-step pattern](docs/three-step-pattern.md) — the BM v0.21.1 quirk every creator skill encodes.
