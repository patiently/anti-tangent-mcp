---
name: bm-scribe:list-notes
description: Use when listing all your personal notes. Prints titles + permalinks for every note under `<USERNAME>/notes/`.
---

# list-notes

Lists all personal notes under `<USERNAME>/notes/`.

## Step 1 — Resolve username

- `<USERNAME>` = `$BM_SCRIBE_USERNAME`, then `$USER` (POSIX). If both unset, ask the user for their handle and cache the answer for the session.

## Step 2 — Search

```text
basic-memory:search_notes(
  query="",
  filters={ permalink_prefix: "<USERNAME>/notes/" }
)
```

(If `basic-memory:search_notes` uses a different filter shape in your installed version, fall back to `query="<USERNAME>/notes/"` — BM's full-text search will match the prefix in the permalink.)

## Step 3 — Print

For each result, print one line: `<title> — [[<permalink>]]`. Sort by title.

If the result set is empty, report "No personal notes yet. Use `bm-scribe:add-note <slug>` to create one." and stop.

## Step 4 — Summary

After listing, print: `<N> personal note(s).`
