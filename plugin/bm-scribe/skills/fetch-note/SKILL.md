---
name: bm-scribe:fetch-note
description: Use when reading a personal note. Reads `<USERNAME>/notes/<slug>/main` and prints it.
---

# fetch-note

Reads and prints a personal note at `<USERNAME>/notes/<slug>/main`.

## Step 1 — Gather inputs

- `slug` — kebab-case identifier. Comes from the invocation argument.
- `<USERNAME>` = `$BM_SCRIBE_USERNAME`, then `$USER` (POSIX). If both unset, ask the user for their handle and cache the answer for the session.

## Step 2 — Read

```text
basic-memory:read_note(identifier="<USERNAME>/notes/<slug>/main")
```

If it errors with "not found", report "No note at `<USERNAME>/notes/<slug>/main`. Did you mean to `bm-scribe:add-note <slug>`?" and stop.

## Step 3 — Print

Print the note in this exact shape: line 1 is `title: <value>` from the frontmatter; line 2 is `tags: <value>` from the frontmatter (if `tags` is missing or the list is empty, print `tags: []`); line 3 is blank; from line 4 onward, the full body verbatim. All other frontmatter fields (including `permalink`, `note_type`) are suppressed. Preserve markdown formatting in the body.
