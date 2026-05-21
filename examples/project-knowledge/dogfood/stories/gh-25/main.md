---
permalink: anti-tangent-mcp/stories/gh-25/main
type: story
title: validate_completion shape-guard false-positives on Go ./pkg/... syntax
status: done
opened_at: 2026-05-21
closed_at: 2026-05-21
parent_epic: anti-tangent-mcp/epics/gh-23/main
owners: ["@pgilmore"]
tracker_url: https://github.com/patiently/anti-tangent-mcp/issues/25
tags: [story]
---

## Story brief

The v0.5.2 release added six placeholder/truncation patterns to `evidenceTruncationPatterns`. One of them (`/...`) false-positives on Go's standard package-recursion syntax (`./internal/<pkg>/...`) when test_evidence prose or test files quote a `go test ./pkg/...` command. v0.6.0 implementation subagents on Tasks 9 and 10 both worked around the issue. This story fixes it in v0.6.1.

## Acceptance criteria

- The bare `/...` substring is removed from `evidenceTruncationPatterns`.
- A regression test confirms `final_diff` content containing `./internal/<pkg>/...` is no longer rejected.
- The other five v0.5.2 comment-form placeholders (`/* ... */`, `// snip`, etc.) remain — they're unambiguous.

## PRs

| PR | State | Branch | Relationship | Merged into | Deployed |
|---|---|---|---|---|---|
| #27 | merged | version/0.6.1 | initial | main | prod |

## Subtasks

- [x] Drop `/...` from `evidenceTruncationPatterns` in `internal/mcpsrv/handlers.go`.
- [x] Add `TestCheckEvidenceShape_GoPackageRecursionAccepted` regression test.
- [x] Document the change in CHANGELOG v0.6.1 entry.

## Deployment state

| Env | Latest landed | Date | Notes |
|---|---|---|---|
| prod | PR #27 | 2026-05-21 | v0.6.1 release shipped. |

## Decisions produced

None — pure bug fix.

## Open questions

None — story closed.
