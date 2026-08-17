# Integrating anti-tangent-mcp

`anti-tangent-mcp` is an advisory MCP server that helps prevent implementing-subagent drift
while working on **tasks from a written implementation plan**. The protocol is split by reader
role so an agent loads only what applies to it.

**Install and configure:** see [`README.md`](https://github.com/patiently/anti-tangent-mcp/blob/main/README.md).

| Part | Who reads it | Covers |
|---|---|---|
| [`core.md`](docs/protocol/core.md) | everyone | tool surface, scope and limits, §1 when the protocol applies, §6 FAQ and finding categories |
| [`authoring.md`](docs/protocol/authoring.md) | plan authors | §3 task-block format, normative test bodies, attestations |
| [`implementer.md`](docs/protocol/implementer.md) | implementing subagents | §4 lifecycle, the paste-in dispatch clause, lightweight mode, CodeScene companion |
| [`controller.md`](docs/protocol/controller.md) | controllers | §5 plan-handoff gate, dispatch addendum, end-of-run reporting |
| [`project-knowledge.md`](docs/protocol/project-knowledge.md) | controllers, only with a KB | prime/extract loop, note types, Basic Memory translation |

Section numbers are stable across the split: §1 and §6 are in `core.md`, §3 in `authoring.md`,
§4 in `implementer.md`, §5 in `controller.md`.

The authoritative design is
[`docs/superpowers/specs/2026-05-07-anti-tangent-mcp-design.md`](https://github.com/patiently/anti-tangent-mcp/blob/main/docs/superpowers/specs/2026-05-07-anti-tangent-mcp-design.md).
