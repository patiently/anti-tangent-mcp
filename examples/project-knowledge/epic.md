---
permalink: <PROJECT>/epics/<EPIC-TICKET-ID>/main
type: epic
title: <one-line epic title>
status: planned                  # planned | in_progress | closed | abandoned
opened_at: <YYYY-MM-DD>
closed_at: null
owners: ["@<handle>"]
tracker_url: <issue URL or null>
plan_refs: [docs/superpowers/plans/<plan-file>]
touches_modules: [<PROJECT>/modules/<name>/main]   # KB permalinks, not Go-package paths
produces_decisions: []                              # filled in by extract over time
relates: [<PROJECT>/features/<slug>/main]
tags: [epic]
---

## Charter

<two to three sentences naming the user-visible goal and the success criterion>

## In scope / Out of scope

- **In:** <bullet>
- **In:** <bullet>
- **Out:** <bullet>
- **Out:** <bullet>

## Acceptance (epic-level)

<!-- Per-story ACs live in story notes. Epic-AC ticks are a human-curation gesture
     at story-close or epic-close — extract does not auto-tick (matching closed
     stories to ACs requires semantic judgement that's not deterministic across
     reviewer models). -->

- [ ] <epic-level AC>
- [ ] <epic-level AC>
- [x] <ticked once the corresponding story closes and someone confirms the match>

## Stories

<!-- Deployment column shows the HIGHEST env reached by the story.
     Rank: prod > staging > none. Customise the rank for your env ladder
     in docs/team-setup/project-knowledge-conventions.md. -->

| Story | Status | Deployment | Tracker |
|---|---|---|---|
| [<TICKET-ID>](<PROJECT>/stories/<TICKET-ID>/main) — <story title> | planned | none | [<TICKET-ID>](<tracker URL>) |

## Open PRs

<!-- Aggregated across all stories in this epic. A PR enters this table when opened
     and leaves it when merged or closed. The story's own `## PRs` keeps the durable
     per-story history. -->

| PR | Story | State | Title |
|---|---|---|---|
| #<N> | [<TICKET-ID>](<PROJECT>/stories/<TICKET-ID>/main) | draft or review | <PR title> |

## Progress ledger

<!-- Append-only. One entry per milestone. The extract tool writes here via
     edit_note(insert_before_section, "## Open questions") so existing entries
     are never clobbered. -->

- <YYYY-MM-DD> — <one-line summary of the milestone>

## Open questions

- <bullet>

---

**Note on terminal status.** Stories terminate with `status: done`; epics terminate with `status: closed`. The asymmetry is deliberate — engineering finishes a story; product management closes an epic once all stories are done AND any post-merge follow-through (deployment, retro, AC confirmation) has landed.
