# anti-tangent-mcp — protocol pointer (opencode / non-skill hosts)

The anti-tangent-mcp drift-protection protocol is available in this host as an
MCP server (tools: `validate_plan`, `validate_task_spec`, `check_progress`,
`validate_completion`, plus the optional project-knowledge pair).

**When the protocol applies:** only when the task you are about to implement (or
dispatch a subagent to implement) carries a structured **Goal / Acceptance
criteria / (Non-goals) / (Context)** header from an implementation plan. For
read-only research, Q&A, ad-hoc edits, plan authoring, or code review, it does
not apply.

**When it applies, load the full protocol on demand:** `Read` the full document
at `__ANTI_TANGENT_DOC_PATH__` and follow it — the §4 per-task lifecycle if you
are the implementer, the §5 controller gate + dispatch clause if you dispatch
subagents. Do not act on the protocol from this pointer alone; the full document
is the single source of truth.

This pointer is the only always-loaded piece; the full ~10k-token document loads
only when a Goal/Acceptance-criteria task actually appears.
