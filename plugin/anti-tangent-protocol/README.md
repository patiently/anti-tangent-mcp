# anti-tangent-protocol

A Claude Code companion plugin that makes the [anti-tangent-mcp](https://github.com/patiently/anti-tangent-mcp)
drift-protection protocol available **on demand** instead of always-inlined.

The plugin ships a single skill whose one-line `description` is the only thing
always in context. When you are about to implement (or dispatch a subagent to
implement) a task that has a **Goal / Acceptance-criteria** header from an
implementation plan, the skill loads and `Read`s the bundled `INTEGRATION.md`
(the full protocol). For everything else — Q&A, exploration, ad-hoc edits — the
full ~10k-token document never loads.

This replaces the older install that `@`-imported the whole `INTEGRATION.md`
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
against a flat ~10k-token tax on every call; the description's Goal/AC-header
wording is written to make the trigger fire reliably.

## Source of truth

`INTEGRATION.md` here is a byte-for-byte copy of the repository root
`INTEGRATION.md`, kept identical by a CI guard. Edit the root file, not this copy.
