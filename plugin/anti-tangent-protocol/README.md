# anti-tangent-protocol

A Claude Code companion plugin that makes the [anti-tangent-mcp](https://github.com/patiently/anti-tangent-mcp)
drift-protection protocol available **on demand** instead of always-inlined.

The plugin ships a single skill whose one-line `description` is the only thing
always in context. When you are about to implement (or dispatch a subagent to
implement) a task that has a **Goal / Acceptance-criteria** header from an
implementation plan — or when a multi-task plan run has just finished and you
need to report on it — the skill loads and `Read`s the bundled protocol,
sliced by role, from [`protocol/`](protocol/):

| Part | Who reads it | Covers |
|---|---|---|
| [`protocol/core.md`](protocol/core.md) | everyone | tool surface, scope and limits, §1 when the protocol applies, §6 FAQ and finding categories |
| [`protocol/authoring.md`](protocol/authoring.md) | plan authors | §3 task-block format, normative test bodies, attestations |
| [`protocol/implementer.md`](protocol/implementer.md) | implementing subagents | §4 lifecycle, the paste-in dispatch clause, lightweight mode, CodeScene companion |
| [`protocol/controller.md`](protocol/controller.md) | controllers | §5 plan-handoff gate, dispatch addendum, end-of-run reporting |
| [`protocol/project-knowledge.md`](protocol/project-knowledge.md) | controllers, only with a KB | prime/extract loop, note types, Basic Memory translation |

An agent reads `core.md` plus only the part matching its role — never all five by default. For
everything else — Q&A, exploration, ad-hoc edits — none of it loads.

This replaces the older install that `@`-imported the whole protocol document
into global `~/.claude/CLAUDE.md` (a flat ~10k-token cost on every call).

## Install

```bash
claude plugin marketplace add patiently/anti-tangent-mcp
claude plugin install anti-tangent-protocol@anti-tangent-mcp
```

Verify with `claude plugin list`. The plugin complements the MCP server (install
that separately — see the main README); the server provides the tools, this
plugin provides the on-demand "when + how" guidance.

## Trade-off

A skill body loads when the model judges its `description` relevant — slightly
less deterministic than an always-inlined block. That is the correct trade
against a flat token tax on every call; the description's Goal/AC-header and
end-of-run wording is written to make the trigger fire reliably.

## Source of truth

`protocol/` here is a byte-for-byte copy of the repository root `docs/protocol/`,
kept identical by a CI guard. Edit the root files, not this copy.
