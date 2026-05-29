---
name: bm-scribe:create-howto
description: Use when creating or updating a project-knowledge howto note (an operational runbook). Walks through the three-step BM v0.21.1 pattern and lands the note at the canonical v0.7.0 permalink `<PROJECT>/howtos/<slug>/main`.
---

# create-howto

Creates (or updates) a project-knowledge `howto` note at `<PROJECT>/howtos/<slug>/main` per the [v0.7.0 canonical layout](../../../../docs/superpowers/specs/2026-05-21-project-knowledge-conventions-design.md) and the [three-step pattern](../../docs/three-step-pattern.md). A `howto` is an operational runbook — the procedure the team should follow rather than rediscover. It is slug-keyed and updated in place (no ADR number, no supersede chain).

## Step 1 — Gather inputs

If the most recent `extract_project_knowledge` envelope in this conversation carries one or more `type: howto` proposals, offer to pre-fill from one (show its `title`, `modules`, and proposed `## Steps`). Otherwise gather interactively. Ask the user for:

- `<slug>` — kebab-case procedure name; required, comes from the invocation argument if provided.
- `title` — one-line human title.
- `modules` — list of module slugs this procedure touches.
- `when_to_use` — one or two sentences on the trigger (the `## When to use` section).
- `steps` — the ordered procedure (the load-bearing `## Steps` section).
- `verification` — how to confirm it worked (recommended).
- `origin` — optional; the story / epic / PR permalink that produced or validated this procedure.
- `prerequisites`, `rollback`, `related` — optional sections.

If `BM_SCRIBE_PROJECT` is unset, ask the user which BM project to write to and remember the answer for the rest of this session.

For an UPDATE invocation (the note already exists), gather only the section(s) that changed — typically the new `## Steps` and a refreshed `last_verified`; you do not need to re-collect unchanged sections.

## Step 2 — Resolve project + permalink

- `<PROJECT>` = `$BM_SCRIBE_PROJECT` (or the answer from Step 1).
- Canonical permalink = `<PROJECT>/howtos/<slug>/main` (substitute `<PROJECT>` and `<slug>`).
- Directory portion (passed to `write_note`) = canonical permalink with the trailing `/main` stripped.

## Step 3 — Decide create vs update

```text
basic-memory:read_note(identifier="<PROJECT>/howtos/<slug>/main")
```

- If it returns a note → this is an UPDATE; go to **Step 3-update**.
- If it errors with "not found" → this is a CREATE; go to **Step 3-create**.
- If `extract_project_knowledge` proposed `action: "update"` but the read returned "not found", tell the user you are creating instead of updating (e.g. "No existing howto at `<PROJECT>/howtos/<slug>/main` — creating a new note rather than updating."), then proceed with **Step 3-create**.

## Step 3-create — Issue the three-step BM call sequence

Follow [`../../docs/three-step-pattern.md`](../../docs/three-step-pattern.md). Render the body with `## When to use` and `## Steps` (required), plus any supplied optional sections (`## Prerequisites`, `## Verification`, `## Rollback / if it goes wrong`, `## Related`).

```text
# Step 3a — create. BM ignores metadata.permalink.
basic-memory:write_note(
  title=<title>,
  directory="<PROJECT>/howtos/<slug>",
  note_type="howto",
  content=<rendered body>,
  metadata={
    permalink: "<PROJECT>/howtos/<slug>/main",
    status: "active",
    modules: <list>,
    last_verified: "<YYYY-MM-DD>",
    origin: "<PROJECT>/stories/<TICKET-ID>/main",   # OMIT this key entirely when no origin is known — do NOT pass the literal string "null"
  }
)
# Capture the returned permalink — call it AUTO_PERMALINK.

# Step 3b — relocate.
basic-memory:move_note(
  identifier=AUTO_PERMALINK,
  destination_path="<PROJECT>/howtos/<slug>/main.md"
)

# Step 3c — read the moved note to capture the YAML permalink line verbatim.
basic-memory:read_note(identifier="<PROJECT>/howtos/<slug>/main")
# Extract the current `permalink: …` line; call it CURRENT_PERMALINK_LINE.

# Step 3d — rewrite the YAML permalink line.
basic-memory:edit_note(
  identifier="<PROJECT>/howtos/<slug>/main",
  operation="find_replace",
  find_text=CURRENT_PERMALINK_LINE,
  replace_text="permalink: <PROJECT>/howtos/<slug>/main"
)
```

## Step 3-update — Edit the existing howto in place

The note already lives at the canonical permalink — NO `move_note`, NO permalink rewrite.

```text
# Replace the procedure section with the new steps.
basic-memory:edit_note(
  identifier="<PROJECT>/howtos/<slug>/main",
  operation="replace_section",
  section="## Steps",
  content=<new steps>
)

# Bump the freshness stamp.
basic-memory:read_note(identifier="<PROJECT>/howtos/<slug>/main")
# Extract the current `last_verified: …` line; call it CURRENT_LAST_VERIFIED_LINE.
basic-memory:edit_note(
  identifier="<PROJECT>/howtos/<slug>/main",
  operation="find_replace",
  find_text=CURRENT_LAST_VERIFIED_LINE,
  replace_text="last_verified: <YYYY-MM-DD>"
)
```

To retire a dead procedure, `edit_note(find_replace)` flipping the frontmatter line `status: active` → `status: deprecated`.

## Step 4 — Verify

- `basic-memory:read_note(identifier="<PROJECT>/howtos/<slug>/main")` returns the note.
- The YAML `permalink:` equals `<PROJECT>/howtos/<slug>/main` exactly.
- For an update, `## Steps` reflects the new procedure and `last_verified` is today's date.
- Report success with the canonical permalink in paste-ready form: `[[<PROJECT>/howtos/<slug>/main]]`.
