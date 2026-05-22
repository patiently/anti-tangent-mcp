---
name: bm-scribe:tick-todo
description: Use when marking a personal todo as done. Flips the n'th unchecked bullet in `<USERNAME>/todo/main` from `- [ ]` to `- [x]` and appends a date stamp.
---

# tick-todo

Marks the n'th unchecked bullet in `<USERNAME>/todo/main` as done.

## Step 1 — Gather inputs

- `n` — 1-based index of the bullet to tick. Comes from the invocation argument. Indices come from `bm-scribe:list-todos` output.
- `<USERNAME>` = `$BM_SCRIBE_USERNAME`, then `$USER` (POSIX). If both unset, ask the user for their handle and cache the answer for the session.

## Step 2 — Find the target bullet

Read the note:

```text
basic-memory:read_note(identifier="<USERNAME>/todo/main")
```

Walk the body looking for lines matching `^- \[[ x]\] (.+)$`. Use the 1-based counter from `list-todos` (count BOTH checked and unchecked bullets). Locate the n'th bullet; capture its exact line text as `TARGET_LINE`.

If `TARGET_LINE` is already `- [x] …` (already done), report "Already done." and stop.

## Step 3 — Compute the replacement

`TARGET_LINE` looks like `- [ ] <text>`. The replacement is `- [x] <text> — done <YYYY-MM-DD>` where the date is today (UTC).

## Step 4 — Issue the edit

```text
basic-memory:edit_note(
  identifier="<USERNAME>/todo/main",
  operation="find_replace",
  find_text=<TARGET_LINE>,
  replace_text="- [x] <text> — done <YYYY-MM-DD>"
)
```

## Step 5 — Verify

- Re-read the note and confirm the bullet at index `n` is now `- [x]`.
- Report: "Ticked: <text> — done <YYYY-MM-DD>".
