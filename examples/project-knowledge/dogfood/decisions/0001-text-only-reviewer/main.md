---
permalink: anti-tangent-mcp/decisions/0001-text-only-reviewer/main
type: decision
title: Reviewer LLM reasons over plan-text only; never reads the codebase
status: accepted
supersedes: []
proposed_by: "@pgilmore"
decided_at: 2026-05-07
epic_origin: null
story_origin: null
relates: []
tags: [architecture, seminal]
---

## Context

When designing anti-tangent's reviewer pipeline, a choice was needed: should the reviewer LLM have access to the codebase (via tools like Read, Grep, etc.) or operate purely on the plan text + submitted evidence the controller hands it?

A codebase-aware reviewer can catch more bugs (missing symbols, wrong signatures, repo-wide invariants). A text-only reviewer is faster, cheaper, simpler to operate, and never has to fight tool authentication / sandboxing / parallel-execution issues.

## Decision

The reviewer is **text-only**. It reasons over the plan text, the submitted task spec, and any evidence the caller pastes into the tool call (`final_files`, `final_diff`, `test_evidence`, `pinned_by`, `controller_verified_references`, `harness_shape_attestation`, `project_knowledge`). It NEVER reads the codebase directly.

## Consequences

- Anti-tangent is **advisory, not authoritative**. It catches plan-internal contradictions but cannot detect codebase facts (missing symbols, signature mismatches).
- Pair with a codebase-aware review for any plan that lands real code. Default recommendation: CodeRabbit.
- The reviewer prompt explicitly tells the model it cannot verify codebase claims; when it encounters one, it emits `unverifiable_codebase_claim` (severity-floored to minor) so the caller can grep before dispatch.
- Several follow-up mechanisms (`controller_verified_references`, `harness_shape_attestation`, `codebase_conventions`) exist to let the caller anchor text-only review against codebase reality without giving the reviewer codebase access.

## Alternatives considered

- **Codebase-aware reviewer with read-only repo access.** Rejected because of operational complexity — tool authentication, sandboxing, parallel execution conflicts, file-system permissions. Also slower (the reviewer would re-grep the repo on every call).
- **Hybrid: text-only by default + opt-in codebase tool.** Rejected because the opt-in cliff would create two different behaviors for callers to reason about. Better to be uniformly text-only and let upstream tools (CodeRabbit, CodeScene) own the codebase-aware layer.
