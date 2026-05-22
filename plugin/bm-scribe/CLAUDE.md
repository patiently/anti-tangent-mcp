# bm-scribe — instructions for Claude

When a bm-scribe skill is invoked, follow it verbatim. The skill body tells you what to ask the user, what to call on `basic-memory`, and how to verify success.

## Posture

This plugin is **advisory wrapper** over Basic Memory. It exists because BM v0.21.1 ignores the `permalink:` you pass in metadata at `write_note` time and auto-derives a slug from the title. A single `write_note` call lands the note at the wrong permalink; a `move_note` relocates the file but leaves the YAML `permalink:` field pointing at the auto-derived slug; only an explicit `edit_note` against that YAML line lands the canonical permalink. Skip step 3 and wikilinks break.

## Hard rules

- **Always emit the three-step pattern for any creator skill.** Never short-circuit step 3 even if step 1 appears to have landed the correct permalink — BM might have auto-derived a slug that *looks* right but isn't.
- **Capture step 1's returned permalink verbatim.** It is the input to step 2 (`identifier`). Do not assume it matches the metadata `permalink:` you passed.
- **Read the moved note after step 2** to extract the current YAML `permalink:` value. That's the `find_text` for step 3. Do not guess it — BM normalises slugs in ways that can surprise (case, hyphenation, embedded dates).
- **Never write outside the user's BM project.** `BM_SCRIBE_PROJECT` scopes every call; if unset, ask the user before any write.
- **Personal namespace stays personal.** `<USERNAME>/todo/main` and `<USERNAME>/notes/<slug>/main` are written by this plugin and are explicitly NOT scanned by anti-tangent's `prime` / `extract`. Do not propose adding them to a project-knowledge `kb_index`.
- **Resolve `<USERNAME>` before every personal-namespace call.** Use `$BM_SCRIBE_USERNAME` first, then `$USER` (POSIX), then ask the user interactively and cache the answer for the session. The literal string `<USERNAME>` is **never** written to Basic Memory — always substitute the resolved handle before any `basic-memory` call.

## Reference

- [`docs/three-step-pattern.md`](docs/three-step-pattern.md) — the load-bearing pattern with worked example.
- [Design spec](../../docs/superpowers/specs/2026-05-21-bm-scribe-design.md) — full rationale.
