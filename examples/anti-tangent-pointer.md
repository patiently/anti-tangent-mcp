# anti-tangent-mcp — protocol pointer (opencode / non-skill hosts)

The anti-tangent-mcp drift-protection protocol is available in this host as an
MCP server (tools: `validate_plan`, `validate_task_spec`, `check_progress`,
`validate_completion`, `plan_run_report`, plus the optional project-knowledge
pair).

**When the protocol applies:** either of:

- The task you are about to implement (or dispatch a subagent to implement)
  carries a structured **Goal / Acceptance criteria / (Non-goals) / (Context)**
  header from an implementation plan.
- A multi-task plan run has just finished and you are reporting on it.

For read-only research, Q&A, ad-hoc edits, plan authoring, or code review, it
does not apply.

**When it applies, load the protocol on demand:** `Read` the router at
`__ANTI_TANGENT_DOC_PATH__`, then read the part for your role — the
implementer part if you are writing the code, the controller part if you
dispatch subagents or are reporting on a finished plan run. When the last
task in a plan run has reported DONE, load the controller part and call
`plan_run_report` with the `plan_run_id`. Do not act on the protocol from
this pointer alone; the parts are the source of truth.

This pointer is the only always-loaded piece; the router and its role-scoped
parts load only when a Goal/Acceptance-criteria task, or a just-finished
plan run, actually appears.
