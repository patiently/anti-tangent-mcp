---
permalink: <PROJECT>/stories/<TICKET-ID>/main
type: story
title: <one-line story title>
status: planned                  # planned | in_progress | review | done | abandoned
opened_at: <YYYY-MM-DD>
closed_at: null
parent_epic: <PROJECT>/epics/<EPIC-TICKET-ID>/main   # null if standalone (rare)
owners: ["@<handle>"]
tracker_url: <issue URL or null>
tags: [story]
---

## Story brief

<one to two paragraphs: scope, why it's part of the parent epic, what success looks like>

## Acceptance criteria

- <testable criterion 1>
- <testable criterion 2>

## PRs

<!-- Multi-PR tracking. State enum: draft | review | merged | closed.
     Relationship enum: initial | follow-up to #N | supersedes #N | parallel with #N. -->

| PR | State | Branch | Relationship | Merged into | Deployed |
|---|---|---|---|---|---|
| #<N> | draft | <branch-name> | initial | — | none |

## Subtasks

- [ ] <subtask> (linked to #<PR> if applicable)
- [ ] <subtask>
- [x] <completed subtask>

## Deployment state

<!-- One row per environment the team tracks. Highest-env-reached shows up on the parent epic's `## Stories` row. -->

| Env | Latest landed | Date | Notes |
|---|---|---|---|
| staging | — | — | — |
| prod | — | — | — |

## Decisions produced

<!-- KB permalinks to decision notes produced under this story.
     Extract auto-populates this section by walking `story_origin` matches across decision notes. -->

- [<PROJECT>/decisions/<NNNN>-<slug>/main](<PROJECT>/decisions/<NNNN>-<slug>/main) — <one-sentence summary>

## Open questions

- <bullet>
