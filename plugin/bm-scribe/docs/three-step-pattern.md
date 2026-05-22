# The three-step permalink-canonicalization pattern

Field-tested against Basic Memory v0.21.1 (2026-05-21). Every creator skill in bm-scribe emits exactly these three operations.

## Why three steps

BM v0.21.1 ignores the `permalink:` you pass in `write_note`'s `metadata` and auto-derives a slug from `title` (lowercased, hyphenated, normalised). `move_note` relocates the file but leaves the YAML `permalink:` value pointing at the auto-derived slug. `edit_note` against that YAML line is what finally lands the canonical permalink.

Skip step 3 and any wikilink written as `[[<canonical-permalink>]]` will fail to resolve, because BM's index keys on the YAML `permalink:` field, not on the file path.

## Worked example — creating one epic note

```text
# Step 1 — write the note. BM ignores metadata.permalink and auto-derives.
write_note(
  title="YN-10206 — DriverInvite testability",
  directory="monorepo/epics/YN-10206",
  note_type="epic",
  content="<charter body>",
  metadata={ permalink: "monorepo/epics/YN-10206/main", ... }
)
# → BM creates: main/monorepo/epics/yn-10206-driverinvite-testability.md
# → YAML permalink:  main/monorepo/epics/yn-10206-driverinvite-testability
# → NOT the canonical permalink we asked for.

# Step 2 — move the file to the canonical path.
move_note(
  identifier="main/monorepo/epics/yn-10206-driverinvite-testability",
  destination_path="monorepo/epics/YN-10206/main.md"
)
# → File now lives at: monorepo/epics/YN-10206/main.md
# → BUT the YAML permalink: line still reads:
#   permalink: main/monorepo/epics/yn-10206-driverinvite-testability
# → Wikilinks like [[monorepo/epics/YN-10206/main]] do NOT resolve yet.

# Step 3a — read the moved note to capture the YAML permalink line verbatim.
read_note(identifier="monorepo/epics/YN-10206/main")
# → Returns the note. Extract the YAML `permalink:` line — call it CURRENT_PERMALINK_LINE.
# → For this example, CURRENT_PERMALINK_LINE = "permalink: main/monorepo/epics/yn-10206-driverinvite-testability".
# → Do NOT guess this string: BM normalises slugs in ways that can surprise (case, hyphenation, embedded dates).

# Step 3b — rewrite the YAML permalink line. Load-bearing.
edit_note(
  identifier="monorepo/epics/YN-10206/main",
  operation="find_replace",
  find_text=CURRENT_PERMALINK_LINE,
  replace_text="permalink: monorepo/epics/YN-10206/main"
)
# → Frontmatter updated. Wikilinks [[monorepo/epics/YN-10206/main]] resolve.
```

Terminology note: "three-step pattern" refers to three logical phases — (1) create at the wrong slug, (2) relocate the file, (3) fix the permalink. Phase 3 always requires **two** BM MCP calls (a `read_note` to capture the current line, then an `edit_note(find_replace)` to rewrite it), so the full sequence is four MCP calls but three conceptual steps. Every creator skill emits all four calls.

## What to verify after step 3

- `read_note` against the canonical permalink succeeds.
- The YAML `permalink:` field matches the canonical permalink you wanted.
- Any wikilinks in the body that point to the canonical permalink resolve (BM's `read_note` output includes resolved-relation counts).
