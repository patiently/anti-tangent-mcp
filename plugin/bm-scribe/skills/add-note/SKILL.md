---
name: bm-scribe:add-note
description: Use when creating a personal note on a topic. Creates `<USERNAME>/notes/<slug>/main` via the three-step pattern.
---

# add-note

Creates a personal note at `<USERNAME>/notes/<slug>/main`.

## Step 1 — Gather inputs

- `slug` — kebab-case identifier for the note. Comes from the invocation argument.
- `title` — one-line human title.
- `body` — free-form markdown.
- `tags` — optional list of tags.
- `<USERNAME>` = `$BM_SCRIBE_USERNAME`, then `$USER` (POSIX). If both unset, ask the user for their handle and cache the answer for the session.

## Step 2 — Resolve permalink

Canonical permalink = `<USERNAME>/notes/<slug>/main`. Directory = `<USERNAME>/notes/<slug>`.

## Step 3 — Issue the three-step BM call sequence

Follow [`../../docs/three-step-pattern.md`](../../docs/three-step-pattern.md):

```text
# 3a — write. BM ignores metadata.permalink.
basic-memory:write_note(
  title=<title>,
  directory="<USERNAME>/notes/<slug>",
  note_type="personal_note",
  content=<rendered body>,
  metadata={
    permalink: "<USERNAME>/notes/<slug>/main",
    tags: <tags or []>
  }
)
# Capture AUTO_PERMALINK.

# 3b — move.
basic-memory:move_note(
  identifier=AUTO_PERMALINK,
  destination_path="<USERNAME>/notes/<slug>/main.md"
)

# 3c — read to find the current YAML permalink line.
basic-memory:read_note(identifier="<USERNAME>/notes/<slug>/main")
# Extract `permalink: …` line.

# 3d — rewrite the permalink.
basic-memory:edit_note(
  identifier="<USERNAME>/notes/<slug>/main",
  operation="find_replace",
  find_text=<current permalink line>,
  replace_text="permalink: <USERNAME>/notes/<slug>/main"
)
```

## Step 4 — Verify

- `basic-memory:read_note(identifier="<USERNAME>/notes/<slug>/main")` returns the note.
- YAML `permalink:` equals `<USERNAME>/notes/<slug>/main` exactly.
- Report: "Created note: [[<USERNAME>/notes/<slug>/main]]".
