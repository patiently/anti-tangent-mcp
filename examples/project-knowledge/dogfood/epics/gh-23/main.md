---
permalink: anti-tangent-mcp/epics/gh-23/main
type: epic
title: v0.6.x project-knowledge feature
status: in_progress
opened_at: 2026-05-18
closed_at: null
owners: ["@pgilmore"]
tracker_url: https://github.com/patiently/anti-tangent-mcp/issues/23
plan_refs: [docs/superpowers/plans/2026-05-19-project-knowledge-v0.6.0.md]
touches_modules: [anti-tangent-mcp/modules/review-pipeline/main]
produces_decisions: []
relates: []
tags: [epic]
---

## Charter

Ship the optional project-knowledge MCP layer: two new stateless tools (prime + extract), per-task `project_knowledge` input, a five-type note schema (v0.6.0) extended to six types with dashboards in v0.7.0, and operator-facing setup docs for Basic Memory deployment.

## In scope / Out of scope

- **In:** prime_project_knowledge and extract_project_knowledge tools; note-type templates; conventions doc; dogfood examples; team-setup playbook (VM and Docker variants).
- **Out:** runtime BM integration; concurrent-write resolution; story-done auto-detection.

## Acceptance (epic-level)

- [x] v0.6.0 ships the 5-type taxonomy + the two new tools.
- [x] v0.6.1 ships the Docker-container alternative + the shape-guard fix.
- [x] v0.6.2 trims INTEGRATION.md back under 40k + documents bm_commands→BM API mapping.
- [ ] v0.7.0 ships the 6th `story` type + epic-dashboard rewrite + project-prefix permalinks.

## Stories

<!-- This frozen snapshot includes only the story note that exists in dogfood/.
     Anti-tangent's live KB has rows for gh-29 (INTEGRATION.md trim) and gh-31
     (v0.7.0 conventions) as well; those are omitted here to keep the dogfood
     navigable without dangling cross-refs. -->

| Story | Status | Deployment | Tracker |
|---|---|---|---|
| [gh-25](anti-tangent-mcp/stories/gh-25/main) — shape-guard false-positive fix | done | prod | [#25](https://github.com/patiently/anti-tangent-mcp/issues/25) |
| _additional stories omitted in this frozen snapshot_ | — | — | — |

## Open PRs

<!-- Same trimming rationale as `## Stories` above — the live KB had a row
     for PR #32 (the spec+plan PR) referencing gh-31's story note, which isn't
     in this snapshot. -->

| PR | Story | State | Title |
|---|---|---|---|
| _no open PRs in this frozen snapshot_ | — | — | — |

## Progress ledger

- 2026-05-20 — v0.6.0 shipped: 16 tasks, 5-type taxonomy, prime+extract tools, INTEGRATION.md + team-setup docs.
- 2026-05-21 — v0.6.1 shipped: shape-guard fix (#25), Docker-container alternative (#26 tracker, PR #27).
- 2026-05-21 — v0.6.2 shipped: INTEGRATION.md trim back under 40k + bm_commands→BM API mapping subsection (#28 closed, #29 tracker, PR #30).
- 2026-05-21 — v0.7.0 spec landed on `version/0.7.0` branch (PR #32 in CR review).

## Open questions

- Whether `epic_origin`/`story_origin` should extend to `module` and `feature` proposals. Deferred to field signal.
- Whether `validate_completion` latency outliers warrant a soft `final_files` size threshold. Deferred per the v0.5.2 design.

---

**Note on terminal status.** Stories terminate with `status: done`; epics terminate with `status: closed`. This epic remains `in_progress` until v0.7.0 ships.
