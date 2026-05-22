---
name: bm-scribe:create-decision
description: Use when creating a new project-knowledge decision note. Walks through the three-step BM v0.21.1 pattern and lands the note at the canonical v0.7.0 permalink `<PROJECT>/decisions/<NNNN>-<slug>/main`.
---

# create-decision

Creates a project-knowledge `decision` note at `<PROJECT>/decisions/<NNNN>-<slug>/main` per the [v0.7.0 canonical layout](../../../../docs/superpowers/specs/2026-05-21-project-knowledge-conventions-design.md) and the [three-step pattern](../../docs/three-step-pattern.md).

## Step 1 — Gather inputs

Ask the user for:

- `<slug>` — kebab-case slug for the decision (e.g. `text-only-reviewer`). Required; comes from the invocation argument if provided.
- `title` — one-line human title for the decision.
- `status` — `proposed` (default) or `accepted`.
- `epic_origin` — optional permalink of the epic that triggered this decision.
- `context` — what's true that makes this decision necessary.
- `decision` — the decision itself.
- `consequences` — what this decision implies (good and bad).

If `BM_SCRIBE_PROJECT` is unset, ask the user which BM project to write to and remember the answer for the rest of this session.

## Step 2 — Resolve project + permalink

This Step has three sub-steps. Run them in order — sub-step 2 must complete before sub-step 3 because the canonical permalink contains `<NNNN>`.

### Sub-step 2.1 — Resolve `<PROJECT>`

`<PROJECT>` = `$BM_SCRIBE_PROJECT` (or the answer captured in Step 1).

### Sub-step 2.2 — Auto-pick ADR number

Query `basic-memory:search_notes` with the prefix `<PROJECT>/decisions/`. For each returned permalink, parse the leading four-digit `NNNN-` prefix off the path segment immediately under `<PROJECT>/decisions/`. Find the maximum across all matches and set `NNNN = max + 1`, zero-padded to four digits. If no decisions exist, start at `0001`. Ignore returned permalinks that do not match the `NNNN-<slug>` shape.

### Sub-step 2.3 — Construct the canonical permalink

Canonical permalink = `<PROJECT>/decisions/<NNNN>-<slug>/main` with `<PROJECT>` from sub-step 2.1 and `<NNNN>` from sub-step 2.2. The directory portion (passed to `write_note`) is the canonical permalink with the trailing `/main` stripped.

## Step 3 — Issue the three-step BM call sequence

Follow [`../../docs/three-step-pattern.md`](../../docs/three-step-pattern.md). Concretely:

```text
# Step 3a — create. BM ignores metadata.permalink.
basic-memory:write_note(
  title=<title>,
  directory="<PROJECT>/decisions/<NNNN>-<slug>",
  note_type="decision",
  content=<rendered body>,
  metadata={
    permalink: "<PROJECT>/decisions/<NNNN>-<slug>/main",
    status: <status>,
    epic_origin: <epic_origin or null>,
  }
)
# Capture the returned permalink — call it AUTO_PERMALINK.

# Step 3b — relocate.
basic-memory:move_note(
  identifier=AUTO_PERMALINK,
  destination_path="<PROJECT>/decisions/<NNNN>-<slug>/main.md"
)

# Step 3c — read the moved note to capture the YAML permalink line verbatim.
basic-memory:read_note(identifier="<PROJECT>/decisions/<NNNN>-<slug>/main")
# Extract the current `permalink: …` line from the frontmatter; call it CURRENT_PERMALINK_LINE.

# Step 3d — rewrite the YAML permalink line.
basic-memory:edit_note(
  identifier="<PROJECT>/decisions/<NNNN>-<slug>/main",
  operation="find_replace",
  find_text=CURRENT_PERMALINK_LINE,
  replace_text="permalink: <PROJECT>/decisions/<NNNN>-<slug>/main"
)
```

## Step 4 — Verify

- `basic-memory:read_note(identifier="<PROJECT>/decisions/<NNNN>-<slug>/main")` returns the note.
- The YAML `permalink:` field in the returned frontmatter equals `<PROJECT>/decisions/<NNNN>-<slug>/main` exactly.
- Report success to the user with the canonical permalink in a paste-ready form: `[[<PROJECT>/decisions/<NNNN>-<slug>/main]]`.
