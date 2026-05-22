---
name: bm-scribe:list-todos
description: Use when reviewing your personal rolling todo list. Prints all bullets from `<USERNAME>/todo/main` with numeric indices for use with `tick-todo`.
---

# list-todos

Reads `<USERNAME>/todo/main` and prints every checkbox bullet with a 1-based index.

## Step 1 — Resolve username

- `<USERNAME>` = `$BM_SCRIBE_USERNAME`, then `$USER` (POSIX). If both unset, ask the user for their handle and cache the answer for the session.

## Step 2 — Read the note

```text
basic-memory:read_note(identifier="<USERNAME>/todo/main")
```

If it errors with "not found", report "No todos yet. Use `bm-scribe:add-todo` to start." and stop.

## Step 3 — Parse and print

Walk the body line by line. For each line matching `^- \[[ x]\] (.+)$`:

- Maintain a 1-based counter `n`, incremented for every matching line (both checked and unchecked).
- Print: `n. [<state>] <text>` where `<state>` is `x` for checked, ` ` for unchecked.

Print the `## Active` and `## Done` section headers as section breaks above their bullets so the user can see structure.

## Step 4 — Report counts

After listing, print a summary line: `<active-count> active, <done-count> done`.
