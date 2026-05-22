# BM scribe — plugin design spec

**Status:** proposed
**Version target:** v0.7.1
**Backing issue:** [#33](https://github.com/patiently/anti-tangent-mcp/issues/33)
**Authors:** Patrick Gilmore

## 1. Overview

BM scribe is a Claude Code plugin shipped from this repo under `plugin/bm-scribe/`. It helps users (and the agents they dispatch) write Basic Memory notes that conform to the v0.7.0 project-knowledge conventions and to BM v0.21.1's slug-derivation behaviour. It is **advisory tooling on top of** Basic Memory's existing MCP server — it does not replace `basic-memory`, it wraps the create-with-correct-permalink workflow as a set of markdown skills.

Anti-tangent-mcp itself does not depend on BM scribe at runtime. The two are aligned by this spec.

## 2. Goals

- **Eliminate rediscovery of the three-step permalink pattern.** BM v0.21.1 ignores `permalink:` passed in metadata at `write_note` time and auto-derives a slug from `title`; `move_note` relocates the file but does not re-derive the permalink; an explicit `edit_note` against the YAML `permalink:` line is what actually lands the canonical permalink. BM scribe encodes this.
- **Make v0.7.0 layout the default.** Subcommand names map 1:1 to note types; folder shape is enforced (`<PROJECT>/<type>/<key>/main.md`, plural type folders, ADR-numbered decisions).
- **Add a per-user personal namespace.** Todos and personal notes scoped under `<USERNAME>/`, sharing the BM project with project-knowledge notes but never read by anti-tangent's `prime` / `extract`.

## 3. Non-goals

- Replacing Basic Memory's MCP server. BM scribe issues calls to `basic-memory` tools; it does not host its own storage.
- Hosting project-knowledge reviewer logic. That stays in anti-tangent-mcp.
- Multi-user concurrency. Personal namespace is single-user per Claude Code installation (the username comes from the host environment, not from a server).
- Plugin-side runtime code. All skills are markdown; the LLM is the runtime.

## 4. Plugin shape

The plugin lives at `plugin/bm-scribe/` inside this repo. Shape mirrors the superpowers plugin (verified against `superpowers/5.1.0` on 2026-05-21):

```text
plugin/bm-scribe/
├── package.json                 # manifest: {name, version, description}
├── gemini-extension.json        # Gemini-compatible metadata
├── README.md                    # what the plugin is + subcommand catalogue
├── CLAUDE.md                    # instructions when this plugin is active
├── docs/
│   └── three-step-pattern.md    # the load-bearing pattern, referenced by every creator skill
└── skills/
    ├── create-epic/SKILL.md
    ├── create-story/SKILL.md
    ├── create-decision/SKILL.md
    ├── create-module/SKILL.md
    ├── create-feature/SKILL.md
    ├── create-glossary/SKILL.md
    ├── add-todo/SKILL.md
    ├── list-todos/SKILL.md
    ├── tick-todo/SKILL.md
    ├── add-note/SKILL.md
    ├── fetch-note/SKILL.md
    └── list-notes/SKILL.md
```

The plugin reads two pieces of environment context (asked at first use if unset, with the answer cached locally by Claude Code):

- `BM_SCRIBE_PROJECT` — the BM project slug to write to (e.g. `monorepo`).
- `BM_SCRIBE_USERNAME` — the user's handle for the personal namespace (e.g. `pgilmore`). Defaults to `$USER` on POSIX.

Both can be overridden per invocation with explicit arguments.

## 5. Subcommand catalogue

### 5.1 Project-knowledge note creators

All six creators follow the three-step pattern from §6. They differ only in what they prompt for and how they shape the canonical permalink.

| Subcommand | Permalink shape | Prompts for |
|---|---|---|
| `create-epic <TICKET-ID>` | `<PROJECT>/epics/<TICKET-ID>/main` | title, charter (multi-line), `touches_modules`, `relates` |
| `create-story <TICKET-ID>` | `<PROJECT>/stories/<TICKET-ID>/main` | title, parent epic permalink, brief |
| `create-decision <slug>` | `<PROJECT>/decisions/<NNNN>-<slug>/main` | title, status (`proposed`/`accepted`), epic_origin (optional), context, decision body, consequences |
| `create-module <slug>` | `<PROJECT>/modules/<slug>/main` | title, owners, invariants list, conventions list |
| `create-feature <slug>` | `<PROJECT>/features/<slug>/main` | title, user-facing description, recent material changes (release-tagged) |
| `create-glossary <term>` | `<PROJECT>/glossary/<term>/main` | term, definition, examples, see-also |

**Auto-incrementing decision IDs.** `create-decision` first queries `search_notes` for existing `<PROJECT>/decisions/*` notes, parses the leading `NNNN-` prefix off each, and picks `max + 1` (zero-padded to four digits). If no decisions exist yet, it starts at `0001`.

### 5.2 Personal namespace

| Subcommand | Target permalink | Behaviour |
|---|---|---|
| `add-todo "<text>"` | `<USERNAME>/todo/main` | Append a `- [ ] <text>` bullet under the `## Active` section. Creates the note (via three-step) on first use. |
| `list-todos` | `<USERNAME>/todo/main` | Read the note and print all `- [ ]` and `- [x]` bullets with index numbers. |
| `tick-todo <n>` | `<USERNAME>/todo/main` | `edit_note` with `find_replace`: flip the n'th `- [ ] X` into `- [x] X` and append a date stamp. |
| `add-note <slug>` | `<USERNAME>/notes/<slug>/main` | Prompt for title + body. Three-step create. |
| `fetch-note <slug>` | `<USERNAME>/notes/<slug>/main` | `read_note` and print. |
| `list-notes` | `<USERNAME>/notes/*` | `search_notes` with prefix filter, print titles + permalinks. |

## 6. Three-step permalink-canonicalization contract

Field-tested against Basic Memory v0.21.1 (2026-05-21). BM auto-derives a note's stored slug from its `title` and ignores the `permalink:` value passed in frontmatter at `write_note` time. A second `move_note` call changes the file path but does **not** re-derive the permalink. A third `edit_note` against the YAML `permalink:` line is what actually makes the canonical permalink stick (and resolves wikilinks pointing at it).

Every `create-*` subcommand emits exactly these three operations in order, with the canonical permalink hard-coded:

```text
# Step 1 — create the note. BM ignores metadata.permalink and auto-derives.
write_note(
  title="<human title>",
  directory="<PROJECT>/<type>/<key>",
  note_type="<type>",
  content="<body>",
  metadata={..., permalink: "<PROJECT>/<type>/<key>/main"}  # BM v0.21.1 IGNORES
)

# Step 2 — move the file to the canonical path. The in-frontmatter permalink
# still reflects the auto-derived slug, NOT the canonical one.
move_note(
  identifier="<auto-derived-permalink-from-step-1>",
  destination_path="<PROJECT>/<type>/<key>/main.md"
)

# Step 3a — read the moved note to capture the YAML permalink line verbatim.
# (BM normalises slugs in ways that can surprise — do NOT guess the line.)
read_note(identifier="<PROJECT>/<type>/<key>/main")
# → Capture the YAML `permalink:` line as CURRENT_PERMALINK_LINE.

# Step 3b — rewrite the YAML permalink line so it matches the path.
# Without this, cross-links written as [[<PROJECT>/<type>/<key>/main]] fail to resolve.
edit_note(
  identifier="<PROJECT>/<type>/<key>/main",
  operation="find_replace",
  find_text=CURRENT_PERMALINK_LINE,
  replace_text="permalink: <PROJECT>/<type>/<key>/main"
)
```

Terminology note: "three-step pattern" refers to three logical phases — (1) write at the wrong slug, (2) relocate, (3) fix the permalink. Phase 3 always requires two MCP calls (`read_note` to capture the current line, then `edit_note(find_replace)` to rewrite). So the full sequence is **four** MCP calls but **three** conceptual phases. Every creator skill emits all four calls.

### BM v0.21.1 `edit_note` operation enums used by this plugin

The plugin exercises these `edit_note` operation enums; each is named here so SKILL.md authors can grep for the contract:

- **`find_replace`** — the load-bearing step-3 operation in the permalink-canonicalization contract above. Also used by `tick-todo` to flip a single `- [ ]` bullet to `- [x]`. Targets exact substrings, so the caller must capture the current line verbatim via `read_note` first.
- **`insert_before_section`** — used by `add-todo` for subsequent appends after the personal todo note already exists. Inserts a new bullet immediately before a named section header, which positions it at the bottom of the preceding section without clobbering surrounding content.
- **`replace_section`** — documented here as an alternative for future skills that need to rewrite a whole section body (e.g. a `## Decisions produced` block on a story note). Not used by any of the twelve subcommands in this spec.
- **`append`** — documented here as an alternative for future skills that need to add content to the very end of a note with no section anchor. Not used by any of the twelve subcommands in this spec.

## 7. Personal-namespace shape

Personal namespace lives in the **same BM project** as project-knowledge notes, namespaced by the user's handle:

```text
<PROJECT>/
├── epics/<TICKET-ID>/main.md            # project knowledge
├── stories/<TICKET-ID>/main.md
├── decisions/<NNNN>-<slug>/main.md
├── modules/<slug>/main.md
├── features/<slug>/main.md
└── glossary/<term>/main.md

<USERNAME>/                              # personal namespace (one per Claude install)
├── todo/main.md                         # one rolling checkbox list
└── notes/<slug>/main.md                 # one note per topic
```

**Anti-tangent ignores the personal namespace.** `prime_project_knowledge` and `extract_project_knowledge` operate on the `<PROJECT>/` prefix only. The boundary is enforced at the `kb_index` filter the controller passes in, not by BM scribe — BM scribe owns the WRITE side of the namespace.

## 8. Open questions

- **Spin-out to a sibling repo.** v0.7.1 ships the plugin co-located in this repo for simpler iteration. Once the contract is stable, spin out to `patiently/bm-scribe` and reference from anti-tangent-mcp's README. Tracked separately.
- **Should `list-todos` distinguish "active" vs "done"?** Current spec treats the note as one flat list with state on each bullet, sectioned by `## Active` / `## Done`. Could fold into a single linear list.
- **Concurrent todo edits.** No locking; if two `tick-todo` calls race, the second one's `find_replace` may target a stale state. Acceptable for single-user usage; revisit if multi-pane Claude Code installs grow.

## 9. See also

- [#33](https://github.com/patiently/anti-tangent-mcp/issues/33) — backing issue.
- [v0.7.0 project-knowledge conventions](2026-05-21-project-knowledge-conventions-design.md) — the layout this plugin aligns BM-scribe with.
- [`INTEGRATION.md` § "Applying bm_commands to BM v0.21.1"](../../../INTEGRATION.md) — the BM v0.21.1 contract this plugin encodes.
- [`docs/team-setup/project-knowledge-conventions.md`](../../team-setup/project-knowledge-conventions.md) — per-team tuning loop; §9 (added in v0.7.1) covers the personal namespace.
- [`plugin/bm-scribe/`](../../../plugin/bm-scribe/) — the plugin implementation.
