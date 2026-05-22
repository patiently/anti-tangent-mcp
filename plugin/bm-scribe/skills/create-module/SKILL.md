---
name: bm-scribe:create-module
description: Use when creating a new project-knowledge module note. Walks through the three-step BM v0.21.1 pattern and lands the note at the canonical v0.7.0 permalink `<PROJECT>/modules/<slug>/main`.
---

# create-module

Creates a project-knowledge `module` note at `<PROJECT>/modules/<slug>/main` per the [v0.7.0 canonical layout](../../../../docs/superpowers/specs/2026-05-21-project-knowledge-conventions-design.md) and the [three-step pattern](../../docs/three-step-pattern.md).

## Step 1 — Gather inputs

Ask the user for:

- `<slug>` (a kebab-case module name) — required, comes from the invocation argument if provided.
- `title` — one-line human title.
- `owners` — list of owner handles.
- `invariants` — list of invariants this module enforces.
- `conventions` — list of in-module conventions.

If `BM_SCRIBE_PROJECT` is unset, ask the user which BM project to write to and remember the answer for the rest of this session.

## Step 2 — Resolve project + permalink

- `<PROJECT>` = `$BM_SCRIBE_PROJECT` (or the answer from Step 1).
- Canonical permalink = `<PROJECT>/modules/<slug>/main` (substitute `<PROJECT>` and `<slug>`).
- Directory portion (passed to `write_note`) = canonical permalink with the trailing `/main` stripped.

## Step 3 — Issue the three-step BM call sequence

Follow [`../../docs/three-step-pattern.md`](../../docs/three-step-pattern.md). Concretely:

```text
# Step 3a — create. BM ignores metadata.permalink.
basic-memory:write_note(
  title=<title>,
  directory="<PROJECT>/modules/<slug>",
  note_type="module",
  content=<rendered body>,
  metadata={
    permalink: "<PROJECT>/modules/<slug>/main",
    owners: <list>,
  }
)
# Capture the returned permalink — call it AUTO_PERMALINK.

# Step 3b — relocate.
basic-memory:move_note(
  identifier=AUTO_PERMALINK,
  destination_path="<PROJECT>/modules/<slug>/main.md"
)

# Step 3c — read the moved note to capture the YAML permalink line verbatim.
basic-memory:read_note(identifier="<PROJECT>/modules/<slug>/main")
# Extract the current `permalink: …` line from the frontmatter; call it CURRENT_PERMALINK_LINE.

# Step 3d — rewrite the YAML permalink line.
basic-memory:edit_note(
  identifier="<PROJECT>/modules/<slug>/main",
  operation="find_replace",
  find_text=CURRENT_PERMALINK_LINE,
  replace_text="permalink: <PROJECT>/modules/<slug>/main"
)
```

## Step 4 — Verify

- `basic-memory:read_note(identifier="<PROJECT>/modules/<slug>/main")` returns the note.
- The YAML `permalink:` field in the returned frontmatter equals `<PROJECT>/modules/<slug>/main` exactly.
- Report success to the user with the canonical permalink in a paste-ready form: `[[<PROJECT>/modules/<slug>/main]]`.
