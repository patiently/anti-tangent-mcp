# anti-tangent-mcp Integration — Design

**Date:** 2026-05-08
**Status:** Draft — pending spec review

## Purpose

Define a system-agnostic guide that helps users wire `anti-tangent-mcp` into their LLM-driven implementation workflow. The deliverable is a single markdown document, `INTEGRATION.md`, shipped at the root of the `anti-tangent-mcp` repository. Users include or copy from it into their own project's CLAUDE.md / AGENTS.md / dispatch-prompt template.

The integration covers two audiences:
- **Plan authors** — get a recommended task format that maps directly to `validate_task_spec` inputs.
- **Implementing subagents** — get a paste-in lifecycle clause that mandates pre + post calls, recommends mid calls, and tells them how to handle findings.

It also includes a short controller addendum for orchestrators who dispatch subagents (superpowers' `subagent-driven-development`, hone-ai's equivalent, or hand-rolled loops).

## Goals

- Users can adopt `anti-tangent-mcp` in any LLM harness that supports MCP servers — superpowers, hone-ai, vanilla Claude Code, Cursor, custom — without touching the harness's source.
- The guide makes one round of pre-validation and one round of post-validation **mandatory** in the dispatched subagent's prompt; mid-validation is recommended.
- The plan-task format is prescribed but not enforced — plans that don't follow it still work, with reduced reviewer-output quality.
- The reviewer model split (reviewer ≠ implementer) is communicated as the load-bearing operational decision, with a starter table.

## Non-goals

- Not a fork of superpowers or hone-ai. The guide is portable text; users paste, the harness doesn't change.
- No runtime dependency on a specific skill format. The guide deliberately avoids skill frontmatter or any harness-specific YAML.
- No automation of the prompt assembly. Users splice the clause into their dispatch prompts manually (one-time).
- No new MCP server-side behavior. The guide describes how to use the existing tool surface.
- Not a quality gate on the controller — the post-hook the implementer calls IS the gate. The guide does not introduce a second post-hook for controllers.

## Deliverable shape

A single markdown file at `INTEGRATION.md` (repo root, alongside `README.md` and `CLAUDE.md`). Self-contained. No companion files. No skill packaging.

Rationale: maximum portability across harnesses; users can read once and paste the relevant chunks where they need them. A skill-based deliverable would couple us to a specific harness's loader and force a fork or PR upstream when those harnesses evolve.

## Document structure

`INTEGRATION.md` is organized by audience, not by tool. The implementer-prompt clause (Section 4b) is the load-bearing reusable artifact and is the single longest block in the document.

### 1. What this is + when to use it (~5 sentences)

- One paragraph summarizing the lifecycle protocol (pre/mid/post + advisory verdicts).
- One paragraph stating the two audiences (plan authors vs. implementers) and a one-line note on the optional controller addendum.
- One paragraph on system-agnosticism: superpowers, hone-ai, vanilla CLAUDE.md, custom harnesses — all welcome.
- A "when to use" callout: anti-tangent's value compounds when (a) tasks are specced before being implemented, (b) the implementer is an LLM that can drift, (c) the implementer LLM differs from the reviewer LLM. Without all three, anti-tangent is just extra latency.

### 2. Setup

1. **Install the binary** — three options: `go install`, GoReleaser binary, GHCR image. One short paragraph each.
2. **`.mcp.json` snippet** — Claude Code example; one note that other harnesses use the same `command`/`env` shape.
3. **Provider keys** — at least one of `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GOOGLE_API_KEY` is required; only providers with a key are activated.
4. **Model-split rationale + starter config table:**

   | If your implementer is… | Set `ANTI_TANGENT_*_MODEL` to… |
   |---|---|
   | Anthropic Claude | `openai:gpt-5` and/or `google:gemini-2.5-pro` |
   | OpenAI GPT-5 | `anthropic:claude-sonnet-4-6` and/or `google:gemini-2.5-pro` |
   | Google Gemini | `anthropic:claude-sonnet-4-6` and/or `openai:gpt-5` |

   Plus a note that mid-hook can use a cheap fast model (Haiku / gpt-5-nano / gemini-flash) since it's called more often.

5. **Smoke test** — one suggestion: launch the harness with debug logging and verify `validate_task_spec` appears in the tool catalog.

### 3. For plan authors — the anti-tangent-friendly task format

1. **The required shape** — Goal (mandatory), Acceptance criteria (mandatory), Non-goals (optional but recommended), Context (optional). Files / Steps / etc. live below this header block as before. Markdown sample provided.
2. **Worked example** — one full task block ("Add /healthz endpoint" or similar). Includes one common style mistake (a vague AC like "should be fast") with a callout on what `validate_task_spec` will flag and a concrete rewrite.
3. **What `validate_task_spec` checks** — a short paragraph each on:
   - Structural completeness (Goal? AC? Non-goals where useful?)
   - AC quality (testable / specific / unambiguous)
   - Implicit assumptions surfaced as findings
4. **Mapping to existing plan-writers** — one bullet each for superpowers `writing-plans`, hone-ai, vanilla CLAUDE.md plans. They all gain the same Goal/AC/Non-goals/Context header.
5. **Anti-pattern callout:** keep implementation steps OUT of the AC list. AC is "what does done look like," not "how to get there."

### 4. For implementers — the lifecycle protocol

#### 4a. Protocol summary (top-of-section table)

| Phase | Tool | Required? | When to call |
|---|---|---|---|
| Start | `validate_task_spec` | **Yes** | Once, before writing any code |
| During | `check_progress` | Recommended | After each meaningful chunk; at any moment of uncertainty |
| End | `validate_completion` | **Yes** | Before reporting DONE |

#### 4b. The implementer-prompt clause (load-bearing reusable artifact)

A ~25-line block the controller pastes into every dispatched subagent's prompt:

```markdown
## Drift-protection protocol (anti-tangent-mcp)

Before, during, and after this task, you must use the `validate_task_spec`,
`check_progress`, and `validate_completion` MCP tools.

**1. At the start (REQUIRED).** Before writing any code, call
`validate_task_spec` with the structured task fields below. Save the
returned `session_id` — you'll thread it through subsequent calls.
- Read the findings list. Treat `severity: critical` as blocking and
  `severity: major` as "address or explain." If the spec is too ambiguous to
  proceed, stop and ask the controller for clarification rather than guessing.

**2. During work (RECOMMENDED).** After each meaningful change (a new file,
a non-trivial logic block, finishing one acceptance criterion), call
`check_progress` with: the session_id, a one-sentence `working_on` summary,
and the changed files. Address findings before continuing.

**3. Before reporting DONE (REQUIRED).** Call `validate_completion` with the
session_id, your summary, the final files, and any test evidence. If the
verdict is `fail` or contains `critical`/`major` findings, do not report
DONE — fix the findings and re-validate.

## Task spec (pass these fields verbatim to validate_task_spec)

- task_title: <from the task block>
- goal: <from "Goal:">
- acceptance_criteria: <from "Acceptance criteria:" bullets>
- non_goals: <from "Non-goals:" bullets if present>
- context: <from "Context:" if present>
```

#### 4c. How to address findings

Three short paragraphs:

- **Address vs. push back.** Reviewer LLMs can be wrong. If a finding misreads the code, document the disagreement in the next call's `working_on` field (e.g. `working_on: "addressed all findings except F#3 which is incorrect because…"`) and re-validate. Don't silently ignore.
- **The retry loop.** Parse failures on the reviewer's response are handled inside the server (one retry with a JSON-only reminder). The implementer does not need to handle that.
- **Session not found.** If `check_progress` or `validate_completion` returns a `session_not_found` finding, the session expired (default TTL 4h) or was never created. Call `validate_task_spec` again to start a fresh session.

#### 4d. Concrete examples

- A `validate_task_spec` call that returns a finding on a vague AC, the AC is rewritten, re-validation passes.
- A `check_progress` call that flags scope drift, the implementer corrects, the next checkpoint passes.
- A `validate_completion` call that walks every AC and finds one untouched; the implementer fixes and re-validates.

These are short JSON-shaped illustrations (envelope + finding + revised input), not full transcripts.

### 5. For controllers — optional dispatch addendum

1. **Framing paragraph.** If you orchestrate implementer subagents, paste the §4b clause into every dispatch prompt. The controller itself does not call the MCP tools — it just makes sure each subagent knows to.
2. **Paste block.** Verbatim drop-in: the §4b clause framed by "*Append this section to your implementer-subagent prompt template, right before the 'Report Format' section.*"
3. **Per-skill-system pointers** (one line each):
   - **superpowers:** open `subagent-driven-development/implementer-prompt.md`, paste before the "Report Format" heading.
   - **hone-ai:** equivalent file in their dispatch template.
   - **Vanilla:** wherever your dispatch prompt lives.
4. **DONE-gate callout.** After the subagent reports DONE, you may want to require evidence that `validate_completion` was called and returned `pass` (or `warn` with all findings addressed). Simplest way: ask for the verdict + findings JSON in the subagent's DONE report. The MCP does not enforce this; the prompt does.
5. **Anti-pattern callout.** Do NOT have the controller call `validate_completion` itself after the subagent reports DONE — sessions are scoped to the subagent's lifetime. The subagent's post-hook IS the gate.

### 6. FAQ / failure modes

Q&A format. Six to eight entries, each ~3-5 lines:

1. **"What if the reviewer is wrong?"** Findings are advisory. Document the disagreement in the next call so the reviewer sees your reasoning. Don't silently ignore.
2. **"My implementer is also Claude Sonnet — does this still help?"** Less. Different family at minimum (Sonnet implementer + Opus reviewer; or Sonnet + Haiku mid + Opus post). Different provider is best.
3. **"How do I know my session expired?"** `category: session_not_found`. Default TTL 4h. Re-call `validate_task_spec`.
4. **"Payload too large."** `category: payload_too_large`. Default cap 200KB across files. Send a diff or split the call.
5. **"validate_task_spec is asking for ACs my plan doesn't have."** Add them, or document the gap in the next `working_on`.
6. **"What if the implementer skips post-hook?"** Two defenses: the prompt (§4b) marks it REQUIRED; the controller can require the post-hook envelope in the DONE report.
7. **"Does check_progress catch failing tests?"** No — reviewer LLMs reason over text, not execution. Use mid-checks for drift detection, not debugging. Run tests separately.
8. **"Cost / latency overhead."** ~1-2s + ~$0.001-$0.02 per call. Two mandatory calls per task minimum. Use cheap-fast model for mid; powerful model for post.

## Out of scope (deferred)

- A skill-formatted distribution (superpowers / hone-ai plugin). Could be added later; ship the markdown first.
- Automated splice/install tooling (a CLI that injects the §4b clause into a target prompt file). Manual paste is fine for v1.
- A reference dispatch-script implementation. The §4b clause is sufficient guidance.
- Post-hook envelope schema for the DONE report. Section 5's callout is advisory; the implementer is free to format their report however the controller wants.
- Localization / non-English docs.
- Plan-author authoring tools (e.g. linting plan tasks for the required Goal/AC/Non-goals/Context block).

## Open questions

None at design time. The integration's interface is the existing MCP tool surface; nothing in this design requires server-side change.
