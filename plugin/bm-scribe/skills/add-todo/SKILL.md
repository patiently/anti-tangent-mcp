---
name: bm-scribe:add-todo
description: Use when adding a personal todo to your rolling list. Appends a `- [ ] <text>` bullet to `<USERNAME>/todo/main`. Creates the note on first use.
---

# add-todo

Appends a checkbox bullet to your personal rolling todo list at `<USERNAME>/todo/main`. On first use, creates the note via the three-step pattern.

## Step 1 — Gather inputs

- `text` — the todo content. Comes from the invocation argument.
- `<USERNAME>` = `$BM_SCRIBE_USERNAME`, then `$USER` (POSIX). If both unset, ask the user for their handle and cache the answer for the session.

## Step 2 — Check whether the todo note exists

Call:

```text
basic-memory:read_note(identifier="<USERNAME>/todo/main")
```

If it returns the note, proceed to Step 3a (append). If it errors with "not found", proceed to Step 3b (create).

## Step 3a — Note exists: append a bullet

Call:

```text
basic-memory:edit_note(
  identifier="<USERNAME>/todo/main",
  operation="insert_before_section",
  section="## Done",
  content="- [ ] <text>\n"
)
```

The `insert_before_section` operation puts the new bullet at the bottom of the section above `## Done`, i.e. at the end of `## Active`.

## Step 3b — Note doesn't exist: create then append

Issue the full three-step pattern (see [`../../docs/three-step-pattern.md`](../../docs/three-step-pattern.md)). The note body is the following template, with `<text>` substituted from Step 1 and `<USERNAME>` substituted from your env. The note shape is also documented in `examples/project-knowledge/personal/todo.md` (in the anti-tangent-mcp repo), but the skill does NOT depend on that file existing at install time — the template is inlined here verbatim:

```text
---
title: <USERNAME>'s todo
permalink: <USERNAME>/todo/main
note_type: personal_todo
---

# Todo

One rolling checkbox list for personal todos. Edited via `bm-scribe:add-todo` / `bm-scribe:tick-todo` / `bm-scribe:list-todos`, or directly in any markdown editor.

## Active

- [ ] <text>

## Done
```

```text
# 3b.i — write the note.
basic-memory:write_note(
  title="<USERNAME>'s todo",
  directory="<USERNAME>/todo",
  note_type="personal_todo",
  content=<rendered template with - [ ] <text> as the first bullet>,
  metadata={ permalink: "<USERNAME>/todo/main" }
)
# Capture AUTO_PERMALINK from the return value.

# 3b.ii — move.
basic-memory:move_note(
  identifier=AUTO_PERMALINK,
  destination_path="<USERNAME>/todo/main.md"
)

# 3b.iii — read to get the current YAML permalink line.
basic-memory:read_note(identifier="<USERNAME>/todo/main")
# Extract the `permalink: …` line.

# 3b.iv — rewrite the permalink.
basic-memory:edit_note(
  identifier="<USERNAME>/todo/main",
  operation="find_replace",
  find_text=<current permalink line>,
  replace_text="permalink: <USERNAME>/todo/main"
)
```

## Step 4 — Verify

- `basic-memory:read_note(identifier="<USERNAME>/todo/main")` returns the note with the new bullet present under `## Active`.
- Report success: "Added: - [ ] <text>".
