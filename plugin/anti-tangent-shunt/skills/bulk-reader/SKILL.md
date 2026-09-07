---
name: bulk-reader
description: Use when a Read is blocked for being too large, or before reading several files just to answer one question. Delegates the reading to a cheap worker model so the file contents never enter your context.
---

# Reading files without reading them

`mcp__anti-tangent__bulk_read` reads files server-side and returns only an answer.

## Ask a question, not for a summary

The worker answers exactly what you ask and nothing else. A vague prompt wastes
the call.

- Bad: `"summarise this file"` — you get prose you must then re-read.
- Good: `"Which functions write to the database, and what are their receivers?"`
- Good: `"Where is retry configured, and what is the backoff?"`

Each bullet comes back led by an exact name, type or line number, so you can act
on it directly.

## If you are going to EDIT, read again afterwards

The answer carries **no reliable line anchors**. Never edit from it. Use it to
locate the region, then take a targeted read — which the hooks never block:

    Read(file_path="/abs/path.go", offset=210, limit=60)

## When NOT to delegate

- **Debugging.** That needs your reasoning over the real text, not a digest.
- **Editing.** See above — targeted reads.
- **Files under the threshold** (default 350 lines). The round trip costs more
  than it saves.
- **Architectural judgement.** Delegate the reading, never the deciding.

## Limits

Up to 50 absolute paths per call, subject to the server's payload cap. Split
larger sets rather than raising the cap.
