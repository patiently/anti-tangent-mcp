---
name: bm-scribe:create-epic
description: Use when creating a new project-knowledge epic note. Walks through the three-step BM v0.21.1 pattern and lands the note at the canonical v0.7.0 permalink `<PROJECT>/epics/<TICKET-ID>/main`.
---

# create-epic

Creates a project-knowledge `epic` note at `<PROJECT>/epics/<TICKET-ID>/main` per the [v0.7.0 canonical layout](../../../../docs/superpowers/specs/2026-05-21-project-knowledge-conventions-design.md) and the [three-step pattern](../../docs/three-step-pattern.md).

## Step 1 — Gather inputs

Ask the user for:

- `<TICKET-ID>` (tracker ticket ID (e.g. `YN-10206`, `gh-23`)) — required, comes from the invocation argument if provided.
- `title` — one-line human title.
- `charter` — multi-line description of the epic's goal and scope.
- `touches_modules` — list of module permalinks this epic touches (optional).
- `relates` — list of related permalinks (optional).

If `BM_SCRIBE_PROJECT` is unset, ask the user which BM project to write to and remember the answer for the rest of this session.

## Step 2 — Resolve project + permalink

- `<PROJECT>` = `$BM_SCRIBE_PROJECT` (or the answer from Step 1).
- Canonical permalink = `<PROJECT>/epics/<TICKET-ID>/main` (substitute `<PROJECT>` and `<TICKET-ID>`).
- Directory portion (passed to `write_note`) = canonical permalink with the trailing `/main` stripped.

## Step 3 — Issue the three-step BM call sequence

Follow [`../../docs/three-step-pattern.md`](../../docs/three-step-pattern.md). Concretely:

```text
# Step 3a — create. BM ignores metadata.permalink.
basic-memory:write_note(
  title=<title>,
  directory="<PROJECT>/epics/<TICKET-ID>",
  note_type="epic",
  content=<rendered body>,
  metadata={
    permalink: "<PROJECT>/epics/<TICKET-ID>/main",
    status: "open",
    touches_modules: <list>,
    relates: <list>,
  }
)
# Capture the returned permalink — call it AUTO_PERMALINK.

# Step 3b — relocate.
basic-memory:move_note(
  identifier=AUTO_PERMALINK,
  destination_path="<PROJECT>/epics/<TICKET-ID>/main.md"
)

# Step 3c — read the moved note to capture the YAML permalink line verbatim.
basic-memory:read_note(identifier="<PROJECT>/epics/<TICKET-ID>/main")
# Extract the current `permalink: …` line from the frontmatter; call it CURRENT_PERMALINK_LINE.

# Step 3d — rewrite the YAML permalink line.
basic-memory:edit_note(
  identifier="<PROJECT>/epics/<TICKET-ID>/main",
  operation="find_replace",
  find_text=CURRENT_PERMALINK_LINE,
  replace_text="permalink: <PROJECT>/epics/<TICKET-ID>/main"
)
```

## Step 4 — Verify

- `basic-memory:read_note(identifier="<PROJECT>/epics/<TICKET-ID>/main")` returns the note.
- The YAML `permalink:` field in the returned frontmatter equals `<PROJECT>/epics/<TICKET-ID>/main` exactly.
- Report success to the user with the canonical permalink in a paste-ready form: `[[<PROJECT>/epics/<TICKET-ID>/main]]`.
