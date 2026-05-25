---
name: bm-scribe:create-gotcha
description: Use when creating a new project-knowledge gotcha note (a module-scoped lesson learned). Walks through the three-step BM v0.21.1 pattern and lands the note at the canonical v0.7.0 permalink `<PROJECT>/gotchas/<NNNN>-<slug>/main`. Two intake modes — default reads structured proposals from the most recent `extract_project_knowledge` envelope in the current conversation; `--from-review <source>` mines gotcha candidates from review text (PR comments, ultrareview / code-review / security-review output).
---

# create-gotcha

Creates a project-knowledge `gotcha` note at `<PROJECT>/gotchas/<NNNN>-<slug>/main` per the [v0.8.0 design](../../../../docs/superpowers/specs/2026-05-23-gotcha-note-type-design.md) and the [three-step pattern](../../docs/three-step-pattern.md).

Gotchas are module-scoped lessons learned during implementation or review — the kind of finding that's easy to re-introduce on the next plan unless someone wrote it down. Prime surfaces them on future plans touching the same module(s) via canonical-encoded `tags` entries in the controller's `kb_index` (see [`docs/team-setup/project-knowledge-conventions.md`](../../../../docs/team-setup/project-knowledge-conventions.md)).

## Intake modes

Choose the mode based on the invocation argument:

| Invocation | Mode | Candidate source |
|---|---|---|
| `/bm-scribe:create-gotcha` (no arg, default) | **Path A — post-plan** | Structured `Proposal{type: "gotcha", ...}` entries from the most recent `extract_project_knowledge` envelope in the current conversation context. |
| `/bm-scribe:create-gotcha --from-review pr:<N>` | **Path B — post-review (PR)** | All review-shaped comments on PR `<N>` of the current repo, fetched via `gh api`. |
| `/bm-scribe:create-gotcha --from-review <filesystem-path>` | **Path B — post-review (file)** | The file's contents read as plain text. |
| `/bm-scribe:create-gotcha --from-review paste:` | **Path B — post-review (paste)** | Multi-line stdin (heredoc) the user pastes interactively. |

If no recent extract envelope is visible AND no `--from-review` flag is given, ask the user which mode they want and re-invoke.

## Step 1 — Gather inputs

If `BM_SCRIBE_PROJECT` is unset, ask the user which BM project to write to and remember the answer for the rest of this session.

### Step 1 — Path A (post-plan) retrieval contract

Locate the most recent `extract_project_knowledge` MCP tool-call response in the current conversation context — concretely, the most recent assistant turn whose tool-use block has a tool name equal to either `extract_project_knowledge` or `mcp__anti-tangent__extract_project_knowledge`. The envelope is the JSON returned in that tool's response (the same JSON the controller would have parsed into an `ExtractResult`).

Recency hint: if the most recent extract call was more than ~50 conversation turns ago, OR if you cannot locate it within a few seconds of scanning, treat this as a "no envelope found" condition and present the fallback prompt below. Do not spend more time scanning — the envelope has probably been evicted from the context window, and the fallback paste/file path is faster than re-running extract.

If no such envelope is visible in the current conversation context, do NOT silently exit. Print:

> No `extract_project_knowledge` envelope visible in this conversation. Choose:
>   (a) paste the envelope JSON directly (end with `EOF` on its own line)
>   (b) supply a filesystem path to a saved envelope file
>   (c) re-invoke with `--from-review <source>` instead

Wait for the user's response. For (a), read a heredoc terminated by `EOF`. For (b), read the named file as UTF-8 and parse as JSON. For (c), exit cleanly so the user can re-invoke.

Once an envelope is in hand, filter its `proposals` array to entries with `type == "gotcha"`. If the filter returns zero entries, print:

> No `gotcha`-typed proposals in the extract envelope. Did you mean `/bm-scribe:create-gotcha --from-review <source>`?

…and exit cleanly (not an error).

For each gotcha-typed proposal, present this summary to the user:

```
Proposal N/M
  Title:    <proposal.title>
  Permalink: <proposal.permalink>     (the controller may renumber; see Step 2)
  Modules:   <parsed from frontmatter_json>
  Severity:  <parsed from frontmatter_json, or "(unset)">
  Origin:    <parsed from frontmatter_json, or "(unset)">
  Rationale: <proposal.rationale>
  Evidence:  <proposal.evidence_refs joined>
  Body preview:
    <first ~10 lines of proposal.body>
```

Ask:

> Accept this proposal, edit it, or skip it? [a/e/s]

- `a` → carry the proposal forward to Step 2 unchanged.
- `e` → prompt for edits to title / modules / severity / origin / body, then carry forward.
- `s` → skip this proposal, advance to the next.

If the user marks any proposal as superseding an existing gotcha (ask: "Does this gotcha supersede an existing note? Paste the predecessor's permalink or press enter to skip"), capture the predecessor permalink for Step 3 supersede handling.

### Step 1 — Path B (post-review)

Resolve `<source>` to raw `review_text`:

- `pr:<N>` — fetch via:
  ```bash
  gh api "repos/$(gh repo view --json nameWithOwner -q .nameWithOwner)/issues/<N>/comments" \
    --jq '.[] | "[\(.user.login)] \(.body)"'
  gh api "repos/$(gh repo view --json nameWithOwner -q .nameWithOwner)/pulls/<N>/reviews" \
    --jq '.[] | select(.body != "" and .body != null) | "[\(.user.login) review] \(.body)"'
  gh api "repos/$(gh repo view --json nameWithOwner -q .nameWithOwner)/pulls/<N>/comments" \
    --jq '.[] | "[\(.user.login) inline @ \(.path):\(.line // 0)] \(.body)"'
  ```
  Concatenate the three outputs in that order. Dedup comments where two endpoints return the same `id`. If any `gh` call fails (auth missing, network), fall back to asking the user:
  > Could not reach PR <N> via gh. Paste the review text directly (end with EOF on its own line):

- Filesystem path — read the file as UTF-8.
- `paste:` — collect a heredoc from stdin (everything until a line containing only `EOF`).

Build the inline mining prompt using:

```
You are extracting "gotchas" from code-review feedback. A gotcha is a
module-scoped lesson learned — a surprise, a regression caught in review,
a codebase footgun, an environmental quirk — that a future plan touching
the same module should know before writing code.

Given the review text below and the list of known module slugs in this
project, return a JSON array of gotcha candidates. Each candidate has
the shape:

  {
    "title": "<one-line human title>",
    "modules": ["<slug>", ...],         // pick from the module list; can be empty if no clean match
    "severity": "low" | "medium" | "high",
    "symptom": "<one paragraph>",
    "root_cause": "<one paragraph>",
    "how_to_avoid": "<one paragraph — the load-bearing actionable rule>"
  }

Return ONLY the JSON array. If no review comment carries gotcha-shaped
signal, return [].

Known module slugs in this project:
<comma-separated list from kb_index entries with type=module>

Review text:
<concatenated review_text>
```

Run that prompt against the host Claude model. Parse the JSON. If parse fails, retry ONCE with the appended instruction `Return ONLY the JSON array. No prose. No code fences.`. If the second attempt also fails, print the raw response and exit (the user can salvage manually).

If the parsed array is empty, print:

> No gotcha candidates found in `<source>`.

…and exit cleanly.

For each candidate, present the same accept/edit/skip prompt as Path A.

Ask the supersede question for each accepted candidate (as in Path A).

## Step 2 — Resolve project + permalink

This Step has three sub-steps. Run them in order — sub-step 2 must complete before sub-step 3 because the canonical permalink contains `<NNNN>`.

### Sub-step 2.1 — Resolve `<PROJECT>`

`<PROJECT>` = `$BM_SCRIBE_PROJECT` (or the answer captured in Step 1).

### Sub-step 2.2 — Auto-pick ADR number

Query `basic-memory:search_notes` with the prefix `<PROJECT>/gotchas/`. For each returned permalink, parse the leading four-digit `NNNN-` prefix off the path segment immediately under `<PROJECT>/gotchas/`. Find the maximum across all matches and set `NNNN = max + 1`, zero-padded to four digits. If no gotchas exist, start at `0001`. Ignore returned permalinks that do not match the `NNNN-<slug>` shape. (Same logic as `create-decision`.)

### Sub-step 2.3 — Construct the canonical permalink

Canonical permalink = `<PROJECT>/gotchas/<NNNN>-<slug>/main` with `<PROJECT>` from sub-step 2.1 and `<NNNN>` from sub-step 2.2. The directory portion (passed to `write_note`) is the canonical permalink with the trailing `/main` stripped.

If the user supplied no `<slug>` in Path B, derive it from the title: lowercase, replace non-alphanumerics with `-`, collapse runs of `-`, trim trailing `-`, truncate to 60 chars.

## Step 3 — Issue the three-step BM call sequence

Follow [`../../docs/three-step-pattern.md`](../../docs/three-step-pattern.md). Concretely (per accepted proposal/candidate):

```text
# Step 3a — create. BM ignores metadata.permalink.
basic-memory:write_note(
  title=<title>,
  directory="<PROJECT>/gotchas/<NNNN>-<slug>",
  note_type="gotcha",
  content=<rendered body — see template below>,
  metadata={
    permalink: "<PROJECT>/gotchas/<NNNN>-<slug>/main",
    type: "gotcha",
    status: "accepted",
    modules: ["<slug>", ...],
    severity: "<low|medium|high>",
    discovered_at: "<YYYY-MM-DD>",
    origin: "<origin permalink or null>",
    supersedes: <[] OR ["<predecessor permalink>"]>
  }
)
# Capture the returned permalink — call it AUTO_PERMALINK.

# Step 3b — relocate.
basic-memory:move_note(
  identifier=AUTO_PERMALINK,
  destination_path="<PROJECT>/gotchas/<NNNN>-<slug>/main.md"
)

# Step 3c — read the moved note to capture the YAML permalink line verbatim.
basic-memory:read_note(identifier="<PROJECT>/gotchas/<NNNN>-<slug>/main")
# Extract the current `permalink: …` line from the frontmatter; call it CURRENT_PERMALINK_LINE.

# Step 3d — rewrite the YAML permalink line.
basic-memory:edit_note(
  identifier="<PROJECT>/gotchas/<NNNN>-<slug>/main",
  operation="find_replace",
  find_text=CURRENT_PERMALINK_LINE,
  replace_text="permalink: <PROJECT>/gotchas/<NNNN>-<slug>/main"
)
```

### Body template

The body MUST contain these four sections in this order. Section 5 (`## Related`) is optional but recommended:

```markdown
## Symptom

<what was observed — concrete, reproducible if possible>

## Root cause

<why it happens — code paths, invariants violated, env quirks>

## How to avoid

<the actionable rule for future plans touching these modules>

## Evidence

- <link to PR / commit / review comment / log line>
- <link to test that pins the fix, if any>

## Related

- [[<PROJECT>/modules/<slug>/main]]
- [[<origin permalink>]]
```

For Path A, populate the four sections by copying the corresponding sections from the proposal's `body` field (the extract reviewer has already structured them this way per the template in `extract.tmpl`). For Path B, populate from the mining-prompt output's `symptom` / `root_cause` / `how_to_avoid` fields and synthesise `## Evidence` from the review-text source references.

### Step 3 — Supersede handling

If the user named a predecessor permalink in Step 1:

1. Run Steps 3a-3d above to create the new gotcha. Make sure `supersedes` in metadata contains the predecessor permalink.
2. Then issue a second call to flip the predecessor's status:

```text
basic-memory:read_note(identifier="<predecessor permalink>")
# Extract the current `status:` line from frontmatter; call it CURRENT_STATUS_LINE.

basic-memory:edit_note(
  identifier="<predecessor permalink>",
  operation="find_replace",
  find_text=CURRENT_STATUS_LINE,
  replace_text="status: superseded"
)
```

If the second call returns "no match" or "not found" — DO NOT roll back the new note. Print to the user:

> WARNING: created the new gotcha at `<new permalink>` but could not find the predecessor `<predecessor permalink>` to flip its status to `superseded`. The new note carries `supersedes: ["<predecessor>"]` in its frontmatter but the predecessor is unchanged. Please review manually.

…and exit cleanly.

## Step 4 — Verify

For each created gotcha:

- `basic-memory:read_note(identifier="<PROJECT>/gotchas/<NNNN>-<slug>/main")` returns the note.
- The YAML `permalink:` field in the returned frontmatter equals `<PROJECT>/gotchas/<NNNN>-<slug>/main` exactly.
- The YAML `type:` field equals `gotcha`.
- Body contains `## Symptom`, `## Root cause`, `## How to avoid`, `## Evidence` as level-2 headers.
- Report success with a paste-ready permalink: `[[<PROJECT>/gotchas/<NNNN>-<slug>/main]]`.

If any check fails, print the failed expectation and the actual value; do not silently swallow.
