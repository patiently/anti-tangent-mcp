# Lean by Default Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship anti-tangent-mcp 0.23.0: the guard plugin gains a session guard (a write-time start gate and a close-time no-session rule), and the server hands implementers a lean-coding ruleset, checks plans and diffs for over-building, and marks lightweight completions.

**Architecture:** Two merges to `main` from one branch. Merge 1 is the guard plugin alone (Part 1 of the spec), so the `claude-sandbox-pinned` pin can move to it and the rest of the plan is implemented under the start gate. Merge 2 is the server and protocol: a `lean.tmpl` returned by `validate_task_spec` and included by the mid/post reviewer prompts, a `plan_lean_rules.tmpl` included by the plan and pre-task prompts, `authoring.md` §3.10, a `lightweight` flag with a `mode: lightweight` summary line, ponytail attribution, and a replay gate.

**Tech Stack:** Go 1.25 (`go.mod`; `text/template`, `testify` v1.11.1), bash + Python 3 hooks (no third-party modules), Claude Code plugin hooks (`PreToolUse`/`PostToolUse`), goreleaser-driven release on merge.

**Spec:** `docs/superpowers/specs/2026-09-18-lean-by-default-design.md` — the plan argues from it; read both.

## Global Constraints

- **Comments** follow root `CLAUDE.md`: explain non-trivial behaviour or a hazard; never change history (no "previously", no version/issue/task references). The installed `anti-tangent-guard` refuses such writes; a clean run is not proof — apply the policy yourself.
- **`go test -race ./...`** is green after every task. Golden files are regenerated only with `go test ./internal/prompts/... -update`, and the diff is read before committing.
- **Guard evals** (`bash plugin/anti-tangent-guard/evals/run.sh`) are green after every guard task; `EXPECTED_CASE_COUNT` in `evals/run.sh` is bumped in the same commit as the fixtures it counts.
- **Protocol parts** stay under 16,000 bytes each; after editing `docs/protocol/authoring.md`, resync the bundle in the same commit: `rm -f plugin/anti-tangent-protocol/protocol/*.md && cp docs/protocol/*.md plugin/anti-tangent-protocol/protocol/`. `controller.md` has 60 bytes of headroom; the one edit to it (Task 4) is byte-counted.
- **Finding shape everywhere:** `category: quality`, `criterion: over_building`, `severity: minor`. Cardinality follows spec §2.2 and §4.3: `check_progress` and `validate_completion` emit at most one per call; `validate_task_spec` at most one for its spec; `validate_plan` at most one per task plus at most one plan-level finding for a cross-task pattern. No new category, no schema change.
- **Template names:** `lean.tmpl` is a plain file template included as `{{template "lean.tmpl" .}}`; `plan_lean_rules.tmpl` holds two `define`s, `plan_lean_rules` and `plan_lean_cross_task`. Exported Go: `prompts.LeanGuidance() (string, error)`, `Envelope.ImplementationGuidance`, `Envelope.Lightweight`.
- **Kill switch:** `ANTI_TANGENT_SESSION_GUARD=0` governs Rule A and Rule B and nothing else. The close-time hook short-circuits before reading stdin only when all three switches are `0`.
- **`VERSION` stays `0.22.0` — never edit it.** `release.yml` reads it as the current version, computes the new one from the merge commit's `[minor]` marker (`0.23.0`), and its bot commit (`chore: release v0.23.0 [skip ci]`) writes it back; every earlier release did the same. A hand-bump to `0.23.0` would make the workflow compute `0.24.0`. The Checkpoint A merge carries `[skip ci]`, so it bumps nothing.
- **Branch:** work continues on this worktree's branch; it is pushed as `version/0.23.0` (CI needs the name to match the `## [0.23.0]` CHANGELOG entry) at Checkpoint A and reused at Checkpoint B.
- **Anti-tangent on this plan:** the per-task hooks and the plan gate DO run. The memory rule that skips them for plans fixing anti-tangent does not apply: the reviews here are made by the installed 0.22.0 binary, not by the code being edited, so their findings are trustworthy — and exercising the guard on Parts 2–4 is the point of merging it first.

**User decisions (already made):**
- Implementers get the ruleset as a `validate_task_spec` envelope field, not via a plugin hook or a protocol pointer ("field only, no protocol pointer").
- Plan authors are reached too: `validate_plan` flags plan-level over-building; `authoring.md` gains §3.10.
- The guard is built and merged first; the pin moves to it; the rest is implemented under Rule A ("otherwise we will be burning a development cycle building something that needs to be trimmed down").
- Rule A (write-time start gate) and Rule B (close-time no-session rule) are both in; the server does NOT refuse an empty `session_id`.
- No env kill switch for the guidance or the reviewer check.
- One rolled-up `over_building` finding per call at task level, and per task (plus one cross-task) at plan level; always `minor`.
- The spec's "scope word" for `plan_lean_rules` is realised as a fixed parenthetical, not a per-input field: three struct fields for one word is the ruleset's own `yagni`.

---

## File Structure

**Part 1 — guard (merge 1)**

| File | Responsibility |
|---|---|
| `plugin/anti-tangent-guard/hooks/check-task-start` (new, bash) | Rule A wrapper: kill switch, trace, exit-code mapping — mirrors `check-comment-write` |
| `plugin/anti-tangent-guard/hooks/check_task_start.py` (new) | Rule A body: fingerprint the first user message, scan for `validate_task_spec`, sentinel, block message |
| `plugin/anti-tangent-guard/hooks/check_task_start_test.py` (new) | unittest for `classify()`; `run.sh` discovers `*_test.py` |
| `plugin/anti-tangent-guard/hooks/check-task-complete` (modify) | Rule B: `no_session` / `lite_marker` signals in the Python scan; fourth block in bash; three-switch short-circuit |
| `plugin/anti-tangent-guard/hooks/hooks.json` (modify) | second `PreToolUse` entry |
| `plugin/anti-tangent-guard/evals/guard-evals.json`, `evals/run.sh` (modify) | fixtures for both rules; `EXPECTED_CASE_COUNT` |
| `plugin/anti-tangent-guard/README.md`, `.claude-plugin/plugin.json`, `.claude-plugin/marketplace.json`, `README.md`, `CLAUDE.md`, `docs/protocol/controller.md`, `examples/lightweight-dispatch.md`, `CHANGELOG.md` (modify) | packaging and the three places that state the kill-switch set |

**Part 2–4 — server and protocol (merge 2)**

| File | Responsibility |
|---|---|
| `internal/mcpsrv/handlers.go`, `internal/mcpsrv/summary.go` (modify) | `Envelope.Lightweight`, `mode: lightweight` line, `Envelope.ImplementationGuidance` |
| `internal/prompts/templates/lean.tmpl` (new) | the implementer ruleset; one text, two readers |
| `internal/prompts/templates/plan_lean_rules.tmpl` (new) | `plan_lean_rules` (tags, per-task emission) and `plan_lean_cross_task` (plan-level emission) |
| `internal/prompts/templates/{pre,plan,plan_tasks_chunk,plan_findings_only,mid,post}.tmpl` (modify) | includes |
| `internal/prompts/prompts.go`, `prompts_test.go`, `testdata/*.golden` (modify) | `LeanGuidance()`, goldens |
| `internal/stats/event.go` (modify) | `over_building` sentinel |
| `docs/protocol/authoring.md`, `plugin/anti-tangent-protocol/protocol/authoring.md`, `scripts/check-protocol-docs.sh` (modify) | §3.10 and the section guard |
| `THIRD_PARTY_NOTICES.md`, `internal/notices/notices_test.go` (modify) | ponytail attribution |
| `internal/mcpsrv/testdata/replay/lean/*.json` (new) | replay fixtures |

---

### Task 1: Live probe — a hook inside a subagent gets the subagent's transcript

**Goal:** Confirm, with captured output, that a `PreToolUse` hook firing inside a dispatched subagent receives the subagent's own `transcript_path` (under `…/subagents/agent-*.jsonl`) and not the parent's — Rule A depends on it.

**USER-ORDERED GATE — NON-SKIPPABLE.** This task was requested by the user in the current conversation. It MUST NOT be closed by walking around it, by declaring it "verified inline", or by substituting a cheaper check. Close only after every item in `acceptanceCriteria` has been re-validated independently, with output captured.

**Files:**
- Create (temporary, deleted at the end of the task): `.claude/settings.local.json`
- Create (temporary): `.claude/hook-probe.jsonl`

**Acceptance Criteria:**
- [ ] `.claude/hook-probe.jsonl` contains at least one line whose `transcript_path` matches `/subagents/agent-` and whose `tool_name` is `Read`.
- [ ] The file also holds the parent's own line (a `transcript_path` not under `/subagents/`), and the DONE report states whether the subagent line's `session_id` and `scratchpad_dir` equal the parent's. Rule A keys its sentinel on the transcript, so either answer is safe, but the guard README states it.
- [ ] The captured lines are pasted into the task's DONE report verbatim.
- [ ] The probe never overwrites an existing `.claude/settings.local.json`: Step 1 refuses to start if one exists (none does in this worktree at plan time), so the cleanup's `rm -f` only ever removes the probe's own file.
- [ ] `.claude/settings.local.json` and `.claude/hook-probe.jsonl` are removed before the task closes; `git status --short` shows neither.

**Verify:** `git status --short .claude/` → empty. The probe file is gone by then (Step 4), so the positive evidence is the `/subagents/agent-` lines pasted into the DONE report from Step 3, not a re-run grep.

**Steps:**

- [ ] **Step 1: Write the probe hook**

Hooks are read at session start, so the probe runs in a fresh headless session rather than this one.

```bash
if [ -e .claude/settings.local.json ] || [ -e .claude/hook-probe.jsonl ]; then
  echo "refusing: .claude/settings.local.json or hook-probe.jsonl already exists — stop and ask the user"; exit 1
fi
mkdir -p .claude
cat > .claude/settings.local.json <<'EOF'
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Read",
        "hooks": [
          {
            "type": "command",
            "command": "jq -c '{tool_name, session_id, transcript_path, scratchpad_dir}' >> \"$CLAUDE_PROJECT_DIR/.claude/hook-probe.jsonl\""
          }
        ]
      }
    ]
  }
}
EOF
```

- [ ] **Step 2: Run a headless session that dispatches a subagent which Reads a file**

```bash
claude -p --model haiku --dangerously-skip-permissions \
  "First use the Read tool on the file VERSION yourself. Then use the Agent tool (subagent_type general-purpose) to dispatch ONE subagent whose only job is to Read the file README.md in the current directory and reply with its first line. Report the subagent's reply."
```

Expected: a reply quoting README's first line, and `.claude/hook-probe.jsonl` now exists.

- [ ] **Step 3: Read the probe file**

```bash
cat .claude/hook-probe.jsonl
wc -l < .claude/hook-probe.jsonl
grep -c '/subagents/agent-' .claude/hook-probe.jsonl
jq -c '{subagent: (.transcript_path | test("/subagents/agent-")), session_id, scratchpad_dir}' .claude/hook-probe.jsonl
```

Expected: at least one line like
`{"tool_name":"Read","session_id":"…","transcript_path":"/home/…/.claude/projects/…/<session>/subagents/agent-<id>.jsonl","scratchpad_dir":"…"}`.
The two counts are the total lines (at least 2: the parent's `VERSION` read and the subagent's `README.md` read) and the subagent lines (at least 1). From the `jq` output, note whether `scratchpad_dir` is present, and whether the subagent's `session_id` and `scratchpad_dir` equal the parent's. Rule A's sentinel lives under `scratchpad_dir` when it is present and is named after the transcript, so a scratchpad shared with the parent is still safe.

If NO line matches `/subagents/agent-`: paste the file's contents and both counts into the report, run Step 4's cleanup and confirm `git status --short .claude/` is empty, then mark the task as failed. The controller then **stops the run and returns to the user for replanning** — it does not continue. Spec §1.3 says Rule A is then dropped and Rule B ships alone, and that changes Task 2 (dropped), Task 4 (no start-gate section, three hooks become two, two kill-switch scopes), Task 5 (no `task-start` probe) and the CHANGELOG; those edits are a revised plan, not an in-flight improvisation.

- [ ] **Step 4: Clean up**

```bash
rm -f .claude/settings.local.json .claude/hook-probe.jsonl
rmdir .claude 2>/dev/null || true
git status --short
```

Expected: no `.claude/` entries in the status.

- [ ] **Step 5: Report**

Paste into the DONE report: the probe lines, the two counts (total, subagent), and the two comparisons, each reported as exactly one of `equal`, `different` or `absent` (absent from either line), with the parent and subagent lines that support it. Nothing to commit.

```json:metadata
{"files": [".claude/settings.local.json", ".claude/hook-probe.jsonl"], "verifyCommand": "git status --short .claude/", "acceptanceCriteria": ["hook-probe.jsonl has a Read line whose transcript_path matches /subagents/agent-", "the parent's line is present and the DONE report states whether the subagent's session_id and scratchpad_dir equal the parent's", "probe lines, both counts and both equalities pasted into the DONE report", "settings.local.json and hook-probe.jsonl removed; git status clean"], "modelTier": "standard", "userGate": true, "tags": ["user-gate"], "requireEvidenceTokens": [["/subagents/agent-"]]}
```

---

### Task 2: Rule A — the write-time start gate (`check-task-start`)

**Goal:** A `PreToolUse` hook on `Edit|Write|NotebookEdit` that, in a session whose first user message is a full-protocol dispatch prompt, refuses every write until `mcp__anti-tangent__validate_task_spec` has been called; with module tests and thirteen eval fixtures.

**Files:**
- Create: `plugin/anti-tangent-guard/hooks/check-task-start`
- Create: `plugin/anti-tangent-guard/hooks/check_task_start.py`
- Create: `plugin/anti-tangent-guard/hooks/check_task_start_test.py`
- Modify: `plugin/anti-tangent-guard/hooks/hooks.json`
- Modify: `plugin/anti-tangent-guard/evals/guard-evals.json` (append 13 cases), `plugin/anti-tangent-guard/evals/run.sh` (`EXPECTED_CASE_COUNT=145` → `158`)

**Acceptance Criteria:**
- [ ] A transcript whose first `type: "user"` entry contains `## Drift-protection protocol (anti-tangent-mcp)` and no `validate_task_spec` `tool_use` → exit 2, stderr contains `EDIT BEFORE validate_task_spec`.
- [ ] Same transcript with a `mcp__anti-tangent__validate_task_spec` `tool_use` after the first user entry → exit 0, and `<scratchpad_dir>/anti-tangent-guard/session-ok/<transcript stem>` is written, holding the transcript's full path, when `scratchpad_dir` is in the payload. The sentinel is per transcript, not per scratchpad: a subagent may share its parent's `scratchpad_dir`, and one file per scratchpad would let the first implementer's pass wave every later one through.
- [ ] A first user entry carrying `Drift-protection protocol (lightweight)` → exit 0. A first user entry with no clause → exit 0, even when the clause appears later inside an `Agent` `tool_use` input.
- [ ] A sentinel whose content is this transcript's full path → exit 0 without opening the transcript, proven by the `task-start | pass | sentinel` trace event (a missing transcript also exits 0, so the exit code alone proves nothing). A sentinel of the same name holding another transcript's path — two transcripts sharing a stem — does not pass.
- [ ] `ANTI_TANGENT_SESSION_GUARD=0`, a missing transcript, a transcript with no parseable line, malformed stdin, `tool_name: Read` → exit 0.
- [ ] `NotebookEdit` is gated like `Edit`.
- [ ] `python3 -B -m unittest discover -s plugin/anti-tangent-guard/hooks -p '*_test.py'` passes; `bash plugin/anti-tangent-guard/evals/run.sh` reports 158 passed.

**Verify:** `bash plugin/anti-tangent-guard/evals/run.sh` → `Total: 158 passed, 0 failed, 158 total`

**Steps:**

- [ ] **Step 1: Write the module test first**

`plugin/anti-tangent-guard/hooks/check_task_start_test.py`:

```python
import json
import unittest

import os
import tempfile

from check_task_start import classify, sentinel_path, sentinel_matches, FULL_HEADING, LITE_HEADING, SPEC_TOOL


def user(text):
    return json.dumps({"type": "user", "message": {"role": "user", "content": text}})


def user_parts(text):
    return json.dumps({"type": "user", "message": {"role": "user", "content": [{"type": "text", "text": text}]}})


def tool_use(name, inp=None):
    return json.dumps({"type": "assistant", "message": {"role": "assistant", "content": [
        {"type": "tool_use", "id": "t1", "name": name, "input": inp or {}}]}})


CLAUSE = "Implement Task 3.\n\n" + FULL_HEADING + "\n\nAt task start ..."


class ClassifyTest(unittest.TestCase):
    def test_implementer_without_call_blocks(self):
        self.assertEqual(classify([user(CLAUSE), tool_use("Read")]), "block")

    def test_implementer_with_call_passes(self):
        self.assertEqual(classify([user(CLAUSE), tool_use("Read"), tool_use(SPEC_TOOL, {"goal": "g"})]), "pass")

    def test_lightweight_dispatch_skips(self):
        self.assertEqual(classify([user(CLAUSE + "\n" + LITE_HEADING)]), "skip")

    def test_non_implementer_skips_even_when_clause_appears_later(self):
        lines = [user("Plan the release."),
                 tool_use("Agent", {"prompt": CLAUSE})]
        self.assertEqual(classify(lines), "skip")

    def test_first_user_entry_may_be_a_text_part_list(self):
        self.assertEqual(classify([user_parts(CLAUSE)]), "block")

    def test_entries_before_first_user_are_ignored(self):
        lines = [json.dumps({"type": "attachment"}), json.dumps({"type": "system"}), user(CLAUSE)]
        self.assertEqual(classify(lines), "block")

    def test_malformed_lines_are_skipped(self):
        self.assertEqual(classify(["not json", user(CLAUSE), "{", tool_use(SPEC_TOOL)]), "pass")

    def test_empty_transcript_skips(self):
        self.assertEqual(classify([]), "skip")

    def test_call_inside_dispatch_prompt_text_does_not_count(self):
        # The clause itself names the tool; only a tool_use entry is a call.
        self.assertEqual(classify([user(CLAUSE + "\nmcp__anti-tangent__validate_task_spec")]), "block")


class SentinelPathTest(unittest.TestCase):
    def test_sentinel_is_named_after_the_transcript(self):
        got = sentinel_path({"scratchpad_dir": "/s", "transcript_path": "/p/subagents/agent-a1.jsonl"})
        self.assertEqual(got, "/s/anti-tangent-guard/session-ok/agent-a1")

    def test_no_scratchpad_or_no_transcript_means_no_sentinel(self):
        self.assertEqual(sentinel_path({"transcript_path": "/p/agent-a1.jsonl"}), "")
        self.assertEqual(sentinel_path({"scratchpad_dir": "/s"}), "")

    def test_sentinel_matches_only_the_path_it_holds(self):
        with tempfile.TemporaryDirectory() as d:
            sentinel = os.path.join(d, "agent-a1")
            with open(sentinel, "w") as fh:
                fh.write("/p/a/agent-a1.jsonl\n")
            self.assertTrue(sentinel_matches(sentinel, "/p/a/agent-a1.jsonl"))
            self.assertFalse(sentinel_matches(sentinel, "/p/b/agent-a1.jsonl"))
            self.assertFalse(sentinel_matches(os.path.join(d, "absent"), "/p/a/agent-a1.jsonl"))


if __name__ == "__main__":
    unittest.main()
```

- [ ] **Step 2: Run it to see it fail**

Run: `cd plugin/anti-tangent-guard/hooks && python3 -B -m unittest check_task_start_test -v`
Expected: `ModuleNotFoundError: No module named 'check_task_start'`

- [ ] **Step 3: Write the Python body**

`plugin/anti-tangent-guard/hooks/check_task_start.py`:

```python
"""PreToolUse body: refuse an Edit/Write in a dispatched implementer's session
until validate_task_spec has been called.

Reads the hook payload as JSON on stdin; the wrapper consumes the hook's own
stdin and re-feeds it here, so nothing else in this process may read stdin.
Exit 0 = allow (the call exists), 4 = allow (sentinel from an earlier
positive), 3 = allow without deciding (not an implementer session, no readable
transcript, an ungated tool), 2 = block. The wrapper maps every other exit to
allow.

A session is an implementer's when its FIRST user entry — the dispatch prompt —
carries the full clause heading and not the lightweight one. Controllers meet
the clause only in file reads and Agent inputs, never as their own first user
message, so they are never gated.
"""
import json
import os
import sys

FULL_HEADING = "## Drift-protection protocol (anti-tangent-mcp)"
LITE_HEADING = "Drift-protection protocol (lightweight)"
SPEC_TOOL = "mcp__anti-tangent__validate_task_spec"
GATED_TOOLS = ("Edit", "Write", "NotebookEdit")
SENTINEL_DIR = os.path.join("anti-tangent-guard", "session-ok")

BLOCK_MESSAGE = """EDIT BEFORE validate_task_spec

This session was dispatched under the full anti-tangent protocol (§4.2), which
requires validate_task_spec before any edit. It has not been called.

Call mcp__anti-tangent__validate_task_spec with the task's fields first. Its
response carries the pre-task review of the spec and the build guidance for this
task; the session_id it returns is what validate_completion needs at the end.

Reads are not gated — read what the change touches, then call it, then edit.

(Disable: ANTI_TANGENT_SESSION_GUARD=0. Trace: {trace})
"""


def text_of(content):
    if isinstance(content, str):
        return content
    return "\n".join(c.get("text") or "" for c in (content or [])
                     if isinstance(c, dict) and c.get("type") == "text")


def classify(lines):
    """Return "skip", "pass" or "block" for an iterable of transcript lines.

    Entries before the first user entry (attachments, system records) are
    ignored. The first user entry decides whether the session is gated at all;
    after it, one validate_task_spec tool_use is a pass. Text merely naming the
    tool is not a call.
    """
    seen_first_user = False
    for line in lines:
        try:
            entry = json.loads(line)
        except Exception:
            continue
        if not isinstance(entry, dict):
            continue
        etype = entry.get("type")
        msg = entry.get("message") or {}
        if not seen_first_user:
            if etype != "user":
                continue
            seen_first_user = True
            first = text_of(msg.get("content"))
            if FULL_HEADING not in first or LITE_HEADING in first:
                return "skip"
            continue
        if etype == "assistant":
            for c in msg.get("content") or []:
                if isinstance(c, dict) and c.get("type") == "tool_use" and c.get("name") == SPEC_TOOL:
                    return "pass"
    return "block" if seen_first_user else "skip"


def sentinel_path(data):
    """Return this transcript's sentinel under the scratchpad, or "".

    A dispatched subagent may share its parent's scratchpad_dir, so one file
    per scratchpad would let the first implementer's pass wave every later
    implementer through. The file is named after the transcript's stem and
    holds its full path; sentinel_matches compares that path, so two
    transcripts sharing a stem never share a pass.
    """
    base = data.get("scratchpad_dir") or ""
    stem = os.path.splitext(os.path.basename(data.get("transcript_path") or ""))[0]
    stem = "".join(ch for ch in stem if ch.isalnum() or ch in "-_")
    return os.path.join(base, SENTINEL_DIR, stem) if base and stem else ""


def sentinel_matches(sentinel, transcript_path):
    try:
        with open(sentinel, encoding="utf-8") as fh:
            return fh.read().strip() == transcript_path
    except OSError:
        return False


def main():
    try:
        data = json.loads(sys.stdin.read())
    except Exception:
        return 3
    if not isinstance(data, dict) or (data.get("tool_name") or "") not in GATED_TOOLS:
        return 3
    sentinel = sentinel_path(data)
    path = data.get("transcript_path") or ""
    if sentinel and sentinel_matches(sentinel, path):
        return 4
    try:
        with open(path, encoding="utf-8", errors="replace") as fh:
            verdict = classify(fh)
    except OSError:
        return 3
    if verdict == "skip":
        return 3
    if verdict == "pass":
        if sentinel:
            try:
                os.makedirs(os.path.dirname(sentinel), mode=0o700, exist_ok=True)
                with open(sentinel, "w", encoding="utf-8") as fh:
                    fh.write(path + "\n")
            except OSError:
                pass
        return 0
    sys.stderr.write(BLOCK_MESSAGE.format(trace=os.environ.get("ATG_TRACE_LOG", "")))
    return 2


if __name__ == "__main__":
    sys.exit(main())
```

- [ ] **Step 4: Run the module tests to see them pass**

Run: `cd plugin/anti-tangent-guard/hooks && python3 -B -m unittest check_task_start_test -v`
Expected: `Ran 12 tests … OK`

- [ ] **Step 5: Write the bash wrapper**

`plugin/anti-tangent-guard/hooks/check-task-start` (then `chmod +x`):

```bash
#!/usr/bin/env bash
# PreToolUse hook on Edit/Write/NotebookEdit: in a session whose first user
# message is a full-protocol dispatch prompt, refuse every write until
# validate_task_spec has been called. Reads are never gated. The Python body
# owns the transcript work; this wrapper owns the kill switch, the trace log
# and the exit-code mapping, the same split as check-comment-write.
set -uo pipefail

PLUGIN_ROOT="${CLAUDE_PLUGIN_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
TRACE_LOG="${ANTI_TANGENT_GUARD_TRACE_LOG:-/tmp/claude-hooks/anti-tangent-guard.log}"
# Mode 700 on creation: the default lives in a world-writable directory.
mkdir -p -m 700 "$(dirname "$TRACE_LOG")" 2>/dev/null || true
ATG_SESSION="-"

atg_rotate_trace() {
    local max=${ANTI_TANGENT_GUARD_TRACE_MAX_BYTES:-1048576} size
    [[ "$max" =~ ^[0-9]+$ ]] || max=1048576
    [[ -L "$TRACE_LOG" ]] && return 0
    [[ -f "$TRACE_LOG" ]] || return 0
    size=$(wc -c < "$TRACE_LOG" 2>/dev/null) || return 0
    [[ "$size" -gt "$max" ]] || return 0
    mv -f "$TRACE_LOG" "$TRACE_LOG.1" 2>/dev/null || true
    return 0
}
atg_rotate_trace

trace() {
    # Never append through a symlink, and scrub the field separators out of
    # every argument at the sink: a newline or "|" reaching printf would split
    # one record into two.
    [[ -L "$TRACE_LOG" ]] && return 0
    local ev=${1:-?} detail=${2:-}
    ev=${ev//[$'\r\n|']/}
    detail=${detail//[$'\r\n|']/}
    printf '%s | s=%s | task-start | %s%s\n' \
        "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$ATG_SESSION" "${ev:-?}" "${detail:+ | $detail}" \
        >> "$TRACE_LOG" 2>/dev/null || true
}

[[ "${ANTI_TANGENT_SESSION_GUARD:-1}" == "0" ]] && { trace "skip" "guard=0"; exit 0; }
command -v python3 >/dev/null 2>&1 || { trace "skip" "no-python3"; exit 0; }
[[ -r "$PLUGIN_ROOT/hooks/check_task_start.py" ]] || { trace "skip" "no-body"; exit 0; }

ATG_INPUT="$(cat)"
ATG_SESSION_RAW=$(printf '%s' "$ATG_INPUT" | jq -r '(.session_id // "") | gsub("[^A-Za-z0-9_-]"; "") | .[0:8]' 2>/dev/null) || ATG_SESSION_RAW=""
[[ -n "$ATG_SESSION_RAW" ]] && ATG_SESSION="$ATG_SESSION_RAW"

printf '%s' "$ATG_INPUT" | ATG_TRACE_LOG="$TRACE_LOG" python3 -I -B "$PLUGIN_ROOT/hooks/check_task_start.py"
status=${PIPESTATUS[1]}
case "$status" in
    0) trace "pass" "spec-called"; exit 0 ;;
    4) trace "pass" "sentinel"; exit 0 ;;
    2) trace "block" "no-spec-call"; exit 2 ;;
    3) trace "skip" "not-gated"; exit 0 ;;
    *) trace "error" "python-exit=$status"; exit 0 ;;
esac
```

- [ ] **Step 6: Register the hook**

`plugin/anti-tangent-guard/hooks/hooks.json` — add a second `PreToolUse` entry (keep the existing one):

```json
{
  "hooks": {
    "PostToolUse": [
      {
        "matcher": "TaskUpdate",
        "hooks": [{ "type": "command", "command": "\"${CLAUDE_PLUGIN_ROOT}/hooks/check-task-complete\"" }]
      }
    ],
    "PreToolUse": [
      {
        "matcher": "Edit|Write",
        "hooks": [{ "type": "command", "command": "\"${CLAUDE_PLUGIN_ROOT}/hooks/check-comment-write\"" }]
      },
      {
        "matcher": "Edit|Write|NotebookEdit",
        "hooks": [{ "type": "command", "command": "\"${CLAUDE_PLUGIN_ROOT}/hooks/check-task-start\"" }]
      }
    ]
  }
}
```

- [ ] **Step 7: Append the eval fixtures**

Append these thirteen objects to the `evals` array in `plugin/anti-tangent-guard/evals/guard-evals.json`, with `id` continuing from the last existing id (146…158). `run.sh` names every case's transcript `transcript.jsonl` — including the `no_transcript` path, `…/does-not-exist/transcript.jsonl` — so the sentinel for a fixture is `{{TMPDIR}}/scratch/anti-tangent-guard/session-ok/transcript`. The `input` objects need no `transcript_path` substitution beyond what `run.sh` already does (`.transcript_path = $tp`); `scratchpad_dir` uses `{{TMPDIR}}`, which `run.sh` substitutes throughout `input`. `IMPL` below stands for this exact transcript line (write it out in each fixture; `run.sh` reads fixtures independently):

```
{"type": "user", "message": {"role": "user", "content": "Implement Task 3: add the flag.\n\n## Drift-protection protocol (anti-tangent-mcp)\n\nAt task start and before DONE, you must use validate_task_spec and validate_completion."}}
```

| name | hook | transcript_raw_lines | input extras | expected |
|---|---|---|---|---|
| `task-start-implementer-no-call-blocks` | `check-task-start` | IMPL; `tool_use Read` | `tool_name: Edit`, `tool_input.file_path: {{TMPDIR}}/x.go`, `scratchpad_dir: {{TMPDIR}}/scratch` | exit 2, stderr contains `EDIT BEFORE validate_task_spec` |
| `task-start-implementer-call-passes-and-writes-sentinel` | same | IMPL; `tool_use Read`; `tool_use mcp__anti-tangent__validate_task_spec` | same | exit 0; `expected_file_contains: {"{{TMPDIR}}/scratch/anti-tangent-guard/session-ok/transcript": ["/transcript.jsonl"]}` — the sentinel holds the full transcript path |
| `task-start-lightweight-dispatch-passes` | same | first user content = IMPL's text with `"\n\n## Drift-protection protocol (lightweight)"` appended | same | exit 0 |
| `task-start-controller-clause-in-agent-input-passes` | same | `user "Execute the plan."`; `tool_use Agent {prompt: <IMPL text>}` | same | exit 0 |
| `task-start-no-clause-passes` | same | `user "Fix the typo in README."`; `tool_use Read` | same | exit 0 |
| `task-start-sentinel-short-circuits` | same | — (`stdin_raw` below, whose `transcript_path` does not exist) | see below | exit 0, and `expected_file_contains: {"{{TMPDIR}}/trace.log": ["task-start | pass | sentinel"]}` |
| `task-start-same-stem-other-path-does-not-pass` | same | — (`stdin_raw` below; the transcript is a `tmpdir_fixture`) | see below | exit 2, stderr contains `EDIT BEFORE validate_task_spec` |
| `task-start-missing-transcript-fails-open` | same | `"no_transcript": true` | same, no sentinel | exit 0 |
| `task-start-malformed-stdin-fails-open` | same | — (`"stdin_raw": "not json"` instead of `input`) | — | exit 0 |
| `task-start-unparsable-transcript-fails-open` | same | `["not json", "{", "]]"]` — no line parses, so there is no first user entry to fingerprint | same, no sentinel | exit 0 |
| `task-start-kill-switch` | same | IMPL; `tool_use Read` | same + `env: {"ANTI_TANGENT_SESSION_GUARD": "0"}` | exit 0 |
| `task-start-read-is-not-gated` | same | IMPL | `tool_name: Read` | exit 0 |
| `task-start-notebookedit-blocks` | same | IMPL | `tool_name: NotebookEdit`, `tool_input.notebook_path: {{TMPDIR}}/n.ipynb` | exit 2, stderr contains `EDIT BEFORE validate_task_spec` |

The two sentinel cases need a `transcript_path` the fixture itself can name, which `input` cannot give (`run.sh` overwrites it with a per-case path outside `{{TMPDIR}}`). They use `stdin_raw`, where `run.sh` substitutes `{{TMPDIR}}`, and a `setup_script`, which runs in `{{TMPDIR}}` with the same substitution:

```json
{
  "name": "task-start-sentinel-short-circuits",
  "hook": "check-task-start",
  "stdin_raw": "{\"tool_name\": \"Edit\", \"tool_input\": {\"file_path\": \"{{TMPDIR}}/x.go\"}, \"transcript_path\": \"{{TMPDIR}}/gone/agent-a1.jsonl\", \"scratchpad_dir\": \"{{TMPDIR}}/scratch\", \"session_id\": \"sess-start-6\"}",
  "setup_script": "mkdir -p scratch/anti-tangent-guard/session-ok && printf '%s\\n' '{{TMPDIR}}/gone/agent-a1.jsonl' > scratch/anti-tangent-guard/session-ok/agent-a1",
  "env": {"ANTI_TANGENT_GUARD_TRACE_LOG": "{{TMPDIR}}/trace.log"},
  "expected_exit": 0,
  "expected_file_contains": {"{{TMPDIR}}/trace.log": ["task-start | pass | sentinel"]}
}
```

```json
{
  "name": "task-start-same-stem-other-path-does-not-pass",
  "hook": "check-task-start",
  "stdin_raw": "{\"tool_name\": \"Edit\", \"tool_input\": {\"file_path\": \"{{TMPDIR}}/x.go\"}, \"transcript_path\": \"{{TMPDIR}}/b/agent-a1.jsonl\", \"scratchpad_dir\": \"{{TMPDIR}}/scratch\", \"session_id\": \"sess-start-7\"}",
  "tmpdir_fixture": {"b/agent-a1.jsonl": "IMPL_LINE\n"},
  "setup_script": "mkdir -p scratch/anti-tangent-guard/session-ok && printf '%s\\n' '{{TMPDIR}}/a/agent-a1.jsonl' > scratch/anti-tangent-guard/session-ok/agent-a1",
  "expected_exit": 2,
  "expected_stderr_contains": ["EDIT BEFORE validate_task_spec"]
}
```

`IMPL_LINE` is the IMPL transcript line above, JSON-escaped into the string. Both also get an `id` and a `reason`.

Each fixture carries a one-sentence `reason` naming a concrete regression that would flip its expected result (the file's convention — read fixture 1's `reason` for the shape). The first fixture, written out in full:

```json
{
  "id": 146,
  "name": "task-start-implementer-no-call-blocks",
  "hook": "check-task-start",
  "input": {
    "tool_name": "Edit",
    "tool_input": {"file_path": "{{TMPDIR}}/x.go", "old_string": "a", "new_string": "b"},
    "transcript_path": "{{TRANSCRIPT}}",
    "scratchpad_dir": "{{TMPDIR}}/scratch",
    "session_id": "sess-start-1"
  },
  "transcript_raw_lines": [
    "{\"type\": \"user\", \"message\": {\"role\": \"user\", \"content\": \"Implement Task 3: add the flag.\\n\\n## Drift-protection protocol (anti-tangent-mcp)\\n\\nAt task start and before DONE, you must use validate_task_spec and validate_completion.\"}}",
    "{\"type\": \"assistant\", \"message\": {\"content\": [{\"type\": \"tool_use\", \"name\": \"Read\", \"input\": {\"file_path\": \"/tmp/y.go\"}}]}}"
  ],
  "expected_exit": 2,
  "expected_stderr_contains": ["EDIT BEFORE validate_task_spec"],
  "reason": "the first user entry is a full-clause dispatch prompt and no validate_task_spec tool_use follows, so the edit must be refused — a fingerprint that missed the heading, or a scan that counted the clause's own mention of the tool as a call, would let it through"
}
```

Then in `plugin/anti-tangent-guard/evals/run.sh` change `EXPECTED_CASE_COUNT=145` to `EXPECTED_CASE_COUNT=158`.

- [ ] **Step 8: Run the evals**

Run: `bash plugin/anti-tangent-guard/evals/run.sh`
Expected: the module tests pass (12 new + existing), then `Total: 158 passed, 0 failed, 158 total`.

- [ ] **Step 9: Commit**

```bash
git add plugin/anti-tangent-guard/hooks/check-task-start plugin/anti-tangent-guard/hooks/check_task_start.py plugin/anti-tangent-guard/hooks/check_task_start_test.py plugin/anti-tangent-guard/hooks/hooks.json plugin/anti-tangent-guard/evals/guard-evals.json plugin/anti-tangent-guard/evals/run.sh
git commit -m "feat(guard): refuse an implementer's first edit until validate_task_spec is called"
```

```json:metadata
{"files": ["plugin/anti-tangent-guard/hooks/check-task-start", "plugin/anti-tangent-guard/hooks/check_task_start.py", "plugin/anti-tangent-guard/hooks/check_task_start_test.py", "plugin/anti-tangent-guard/hooks/hooks.json", "plugin/anti-tangent-guard/evals/guard-evals.json", "plugin/anti-tangent-guard/evals/run.sh"], "verifyCommand": "bash plugin/anti-tangent-guard/evals/run.sh", "acceptanceCriteria": ["full-clause first user entry without validate_task_spec tool_use exits 2 with EDIT BEFORE validate_task_spec", "with the tool_use exits 0 and writes the per-transcript sentinel, holding the full transcript path, under scratchpad_dir", "a matching sentinel short-circuits (trace pass | sentinel); a same-stem sentinel holding another path does not pass", "lightweight heading, no clause, clause only inside an Agent input, kill switch, missing transcript, unparsable transcript, malformed stdin, tool_name Read all exit 0", "NotebookEdit is gated", "run.sh reports 158 passed"], "modelTier": "standard"}
```

---

### Task 3: Rule B — the close-time no-session rule

**Goal:** `check-task-complete` blocks a close whose last `validate_completion` ran with an empty `session_id` (or whose pasted block carries `mode: lightweight`) when nothing in the window shows a lightweight dispatch, under `ANTI_TANGENT_SESSION_GUARD`, with the hook's short-circuit now requiring all three switches off.

**Files:**
- Modify: `plugin/anti-tangent-guard/hooks/check-task-complete` (the `PY` scan ~lines 200–290; the switch preamble ~line 170; the decision block ~lines 660–732)
- Modify: `plugin/anti-tangent-guard/evals/guard-evals.json` (give 41 existing cases a `session_id`, re-point fixture 119's trace needle, append 14 cases), `plugin/anti-tangent-guard/evals/run.sh` (`EXPECTED_CASE_COUNT=158` → `172`)

**Acceptance Criteria:**
- [ ] Every pre-existing `validate_completion` `tool_use` input in `guard-evals.json` carries a `session_id`. A missing key is read as empty — it is what the server does with one — so without this 25 passing fixtures (ids 35–36, 38–40, 42–44, 53, 86, 119, 121–126, 129, 131, 137–139, 143–145) would flip to block.
- [ ] Direct topology: a `validate_completion` `tool_use` with `session_id: ""` in the window and no lightweight marker → exit 2, stderr contains `TASK CLOSED WITHOUT A TASK SESSION`.
- [ ] Same, plus a user entry whose **text part** contains `Drift-protection protocol (lightweight)` → exit 0.
- [ ] Pasted topology: a block with a blank `session_id:` line, and an `Agent` `tool_use` whose `prompt` carries the full clause → exit 2; the same with the lightweight heading in the `prompt` → exit 0.
- [ ] A block whose `session_id:` is non-blank but which carries a `mode: lightweight` line → exit 2.
- [ ] In a window with both a direct call and a completion block, the later one by transcript position decides: a session-backed direct call followed by a pasted block with a blank `session_id:` → exit 2; a pasted blank block followed by a session-backed direct call with no `tool_result` → exit 0.
- [ ] A qualifying pasted block always contains `session_id:` (the hook's qualification already requires it). One whose `session_id:` is not on a line of its own is malformed — no server writes it — and reads as no session, so it blocks: `SESSION_RE` finding nothing is `no_session`, the conservative side.
- [ ] The lightweight heading inside a `tool_result` (a file read) does NOT exempt → exit 2.
- [ ] `ANTI_TANGENT_SESSION_GUARD=0` → exit 0; `ANTI_TANGENT_COMPLETION_GUARD=0` alone → still exit 2; all three `0` → exit 0 before stdin is read, proven by the trace event `| skip | guard=0`, which only the pre-stdin short-circuit writes.
- [ ] Fixture 119 (`switch-both-off-allows`, two switches off) now asserts `| skip | completion-guard=0`: with the session guard still on, two switches no longer short-circuit, so its old `| skip | guard=0` needle would fail.
- [ ] `verdict: fail` outranks the no-session rule (stderr says `FAILED anti-tangent VERDICT`).
- [ ] A block in `summary.go`'s layout carrying `mode: lightweight` is still read by the unchanged `tool:`/`verdict:` regexes: under a lightweight dispatch it qualifies (exit 0), and with `verdict: fail` it still blocks on the failed verdict. This is spec §1.5's compatibility proof — those regexes are the ones guard 0.4.0 ships.
- [ ] All 158 pre-existing cases still pass, both after the fixture migration alone and after the hook change (119 with its re-pointed needle); `run.sh` reports 172.

**Verify:** `bash plugin/anti-tangent-guard/evals/run.sh` → `Total: 172 passed, 0 failed, 172 total`

**Steps:**

- [ ] **Step 1: Give the existing fixtures a `session_id`**

`session_id` is required by the `validate_completion` schema, so every real transcript carries the key; 42 fixture calls in 41 cases omit it only because no rule read it until now. Add it before touching the hook, so the eval run in between proves the migration is neutral on its own:

```bash
python3 - <<'EOF'
import json
p = "plugin/anti-tangent-guard/evals/guard-evals.json"
d = json.load(open(p))
for c in d["evals"]:
    lines = c.get("transcript_raw_lines") or []
    for n, line in enumerate(lines):
        try:
            entry = json.loads(line)
        except Exception:
            continue
        hit = False
        for part in (entry.get("message") or {}).get("content") or []:
            if (isinstance(part, dict) and part.get("type") == "tool_use"
                    and part.get("name") == "mcp__anti-tangent__validate_completion"
                    and "session_id" not in (part.get("input") or {})):
                part["input"] = {"session_id": "sess-eval", **(part.get("input") or {})}
                hit = True
        if hit:
            lines[n] = json.dumps(entry)
with open(p, "w") as fh:
    fh.write(json.dumps(d, indent=2, ensure_ascii=False) + "\n")
EOF
git diff --stat plugin/anti-tangent-guard/evals/guard-evals.json
bash plugin/anti-tangent-guard/evals/run.sh | tail -1
```

Expected: `1 file changed, 42 insertions(+), 42 deletions(-)` — the file round-trips byte-identically through `json.dumps(indent=2, ensure_ascii=False)`, so only the touched lines move — and `Total: 158 passed, 0 failed, 158 total`. Ids 3 and 22 already carry the key and are untouched.

- [ ] **Step 2: Add the signals to the Python scan**

In the `PY='…'` block of `check-task-complete`, make these edits. **The block is a single-quoted bash string: an apostrophe anywhere in the inserted Python — a comment included — ends the string and breaks the hook.** The existing block contains none; keep it that way (`grep -c "'"` over the block's lines stays at its two delimiters).

Add constants after the existing regex definitions (`VERDICT_RE = …`):

`[ \t]*` rather than `\s*`: `\s` matches a newline, so a blank `session_id:` followed by a one-token line would read that token as the id.

```python
SESSION_RE = re.compile(r"(?m)^[ \t]*session_id:[ \t]*(\S*)[ \t]*$")
MODE_LITE_RE = re.compile(r"(?m)^[ \t]*mode:[ \t]*lightweight[ \t]*$")
LITE_HEADING = "Drift-protection protocol (lightweight)"
```

In the per-entry loop, alongside the existing `called_idx` / `texts` collection, collect the marker signal. Inside `if etype == "assistant":` → inside `if c.get("type") == "tool_use":`, after the `elif name == "TaskUpdate":` branch add:

```python
                elif name in ("Agent", "Task"):
                    if LITE_HEADING in str(inp.get("prompt", "")):
                        lite_idx.append(idx)
```

and inside `elif etype == "user":` — before the `if isinstance(content, list):` tool_result handling — add text-part handling that deliberately ignores `tool_result` parts:

```python
        if isinstance(content, str):
            if LITE_HEADING in content:
                lite_idx.append(idx)
        elif isinstance(content, list):
            for c in content:
                if isinstance(c, dict) and c.get("type") == "text" and LITE_HEADING in (c.get("text") or ""):
                    lite_idx.append(idx)
```

Declare `lite_idx = []` next to `called_idx, texts = [], []`.

After `verdict = ""` / the `if qualifying:` block, derive the two new outputs. The block scan repeats the qualification rule per `tool_result` chunk rather than reusing `qualifying`, because `qualifying` is split from the joined text and has lost each block's transcript position. Simulated against every existing fixture after Step 1, this selection flips none:

```python
# Did the last completion in the window run without a task session? The
# latest signal by transcript position wins: the tool_use input of a direct call (a
# missing key is empty, as the server reads it), or a validate_completion
# block -- pasted, or synthesized above from the tool_result of a direct call,
# which sits after its own tool_use. A block reads as no session on a blank
# session_id: line or a mode: lightweight line. A tool_result carrying the
# lightweight heading is file content, not a dispatch decision, so it never
# exempts.
last_direct = None
for (i, inp) in completion_inputs:
    if i >= scan_from:
        last_direct = (i, inp)
last_block = None
for (i, t) in texts:
    if i < scan_from:
        continue
    heads = [m.start() for m in HEADER_RE.finditer(t)]
    for k, h in enumerate(heads):
        b = t[h:(heads[k + 1] if k + 1 < len(heads) else len(t))]
        tm = TOOL_RE.search(b)
        if "session_id:" in b and tm and tm.group(1) == "validate_completion":
            last_block = (i, b)
no_session = False
if last_direct and (last_block is None or last_direct[0] > last_block[0]):
    no_session = not str(last_direct[1].get("session_id") or "").strip()
elif last_block:
    sm = SESSION_RE.search(last_block[1])
    no_session = sm is None or sm.group(1) == "" or bool(MODE_LITE_RE.search(last_block[1]))
lite_marker = any(i >= scan_from for i in lite_idx)
```

and add both to the final `print(json.dumps({...}))`: `"no_session": no_session, "lite_marker": lite_marker,`.

- [ ] **Step 3: Wire the switch and the block in bash**

Replace the switch preamble:

```bash
COMPLETION_GUARD="${ANTI_TANGENT_COMPLETION_GUARD:-1}"
COMMENT_GUARD="${ANTI_TANGENT_COMMENT_GUARD:-1}"
SESSION_GUARD="${ANTI_TANGENT_SESSION_GUARD:-1}"
if [[ "$COMPLETION_GUARD" == "0" && "$COMMENT_GUARD" == "0" && "$SESSION_GUARD" == "0" ]]; then
    trace "?" "skip" "guard=0"
    exit 0
fi
```

After `VERDICT=$(…)` add:

```bash
NO_SESSION=$(echo "$RESULT" | jq -r '.no_session // false')
LITE_MARKER=$(echo "$RESULT" | jq -r '.lite_marker // false')
```

and extend the existing `trace "$TASK_ID" "parsed" "…"` detail with ` no_session=$NO_SESSION lite=$LITE_MARKER`.

Reorder the decision tail so it reads, after the comment-violation block:

```bash
if [[ "$COMPLETION_GUARD" != "0" && "$VERDICT" == "fail" ]]; then
    trace "$TASK_ID" "block" "verdict-fail"
    {
        … existing FAILED-VERDICT message, unchanged …
    } >&2
    exit 2
fi

if [[ "$SESSION_GUARD" != "0" && "$NO_SESSION" == "true" && "$LITE_MARKER" != "true" ]]; then
    trace "$TASK_ID" "block" "no-session"
    {
        echo "TASK CLOSED WITHOUT A TASK SESSION"
        echo
        echo "Task #$TASK_ID was closed on a validate_completion that ran with an empty"
        echo "session_id. That is lightweight mode: the review was made against a"
        echo "synthesized spec with no acceptance criteria. Nothing in this window shows a"
        echo "lightweight dispatch, so this task was dispatched under the full protocol and"
        echo "validate_task_spec was never called."
        echo
        echo "  1. TaskUpdate taskId=$TASK_ID status=in_progress"
        echo "  2. Call mcp__anti-tangent__validate_task_spec with the task's fields"
        echo "  3. Call mcp__anti-tangent__validate_completion with the returned session_id"
        echo "  4. Re-close once the verdict is pass"
        echo
        echo "If the controller did dispatch this task lightweight, its dispatch prompt"
        echo "must carry the heading \"Drift-protection protocol (lightweight)\"."
        echo
        echo "(Disable: ANTI_TANGENT_SESSION_GUARD=0. Trace: $TRACE_LOG)"
    } >&2
    exit 2
fi

if [[ "$COMPLETION_GUARD" == "0" ]]; then
    trace "$TASK_ID" "skip" "completion-guard=0"
    exit 0
fi

if [[ "$CALLED" == "true" || "$BLOCK" == "true" ]]; then
    … existing pass, unchanged …
fi

… existing no-validation block, unchanged …
```

The verdict-fail block moves above the `COMPLETION_GUARD == "0"` exit and gains that guard in its condition; its message text does not change.

- [ ] **Step 4: Append the eval fixtures**

Fourteen cases, `hook` omitted (defaults to `check-task-complete`), ids 159–172. `IN_PROGRESS` = the `TaskUpdate in_progress` line every existing fixture opens with; `CLAUSE_PROMPT` = the IMPL text from Task 2; `BLANK_BLOCK` = a `tool_result` whose text is `"anti-tangent envelope\ntool: validate_completion\nsession_id: \nverdict: pass\n"`.

| name | transcript_raw_lines (after IN_PROGRESS) | env | expected |
|---|---|---|---|
| `close-no-session-direct-blocks` | `tool_use mcp__anti-tangent__validate_completion {session_id: ""}` | — | 2, `TASK CLOSED WITHOUT A TASK SESSION` |
| `close-no-session-direct-lite-in-user-text-passes` | `user` with content `[{"type":"text","text":"Lightweight task.\n\n## Drift-protection protocol (lightweight)"}]`; then the same `tool_use` | — | 0 |
| `close-no-session-pasted-blank-blocks` | `tool_use Agent {prompt: CLAUSE_PROMPT}`; BLANK_BLOCK | — | 2, same needle |
| `close-no-session-pasted-lite-in-agent-prompt-passes` | `tool_use Agent {prompt: "…## Drift-protection protocol (lightweight)…"}`; BLANK_BLOCK | — | 0 |
| `close-no-session-mode-line-blocks` | `tool_use Agent {prompt: CLAUSE_PROMPT}`; block text `"anti-tangent envelope\ntool: validate_completion\nsession_id: -\nverdict: pass\nmode: lightweight\n"` | — | 2 |
| `close-no-session-lite-in-tool-result-does-not-exempt` | `tool_use Read`; `tool_result` text `"# Lightweight dispatch clause\n## Drift-protection protocol (lightweight)"`; `tool_use Agent {prompt: CLAUSE_PROMPT}`; BLANK_BLOCK | — | 2 |
| `close-no-session-session-guard-off-passes` | as `close-no-session-direct-blocks` | `ANTI_TANGENT_SESSION_GUARD=0` | 0 |
| `close-no-session-completion-guard-off-still-blocks` | as `close-no-session-direct-blocks` | `ANTI_TANGENT_COMPLETION_GUARD=0` | 2 |
| `switch-all-three-off-allows` | as `close-no-session-direct-blocks` | all three `0`, plus `ANTI_TANGENT_GUARD_TRACE_LOG: {{TMPDIR}}/trace.log` | 0; `expected_file_contains: {"{{TMPDIR}}/trace.log": ["| skip | guard=0"]}` — with all three off neither path blocks, so this event, which only the pre-stdin exit writes, is what separates the short-circuit from a full parse |
| `close-mode-line-lite-dispatch-passes` | `tool_use Agent {prompt: "…## Drift-protection protocol (lightweight)…"}`; block text in `summary.go`'s layout: `"anti-tangent envelope\n  tool:          validate_completion\n  session_id:    \n  verdict:       pass\n  mode:          lightweight\n  model_used:    m\n"` | — | 0 |
| `close-mode-line-fail-verdict-still-read` | same Agent prompt; same block with `verdict:       fail` | — | 2, `TASK CLOSED ON A FAILED anti-tangent VERDICT` |
| `close-mixed-window-later-blank-block-blocks` | `tool_use Agent {prompt: CLAUSE_PROMPT}`; `tool_use mcp__anti-tangent__validate_completion {session_id: "sess-eval"}` (no `tool_result`); then BLANK_BLOCK | — | 2, `TASK CLOSED WITHOUT A TASK SESSION` |
| `close-mixed-window-later-direct-call-passes` | `tool_use Agent {prompt: CLAUSE_PROMPT}`; BLANK_BLOCK; then `tool_use mcp__anti-tangent__validate_completion {session_id: "sess-eval"}` (no `tool_result`) | — | 0 |
| `close-no-session-fail-verdict-wins` | `tool_use {id: toolu_ns1, validate_completion, session_id: ""}`; `tool_result tool_use_id toolu_ns1` text = the JSON body from fixture 22 with `"session_id": ""` | — | 2, `TASK CLOSED ON A FAILED anti-tangent VERDICT` |

The two `close-mode-line-*` cases pin spec §1.5: a `mode:` line inside the header region does not disturb `TOOL_RE` or `VERDICT_RE`. The spec's "session present → pass" cells are the existing fixtures: id 35 (direct, `sess-eval` after Step 1) and id 4 (pasted, `session_id: sess-1`).

In fixture 119 (`switch-both-off-allows`), change the `expected_file_contains` needle from `"| skip | guard=0"` to `"| skip | completion-guard=0"` and its `reason` to: "completion and comment scanning off, session guard on: the hook reads stdin, Rule B finds a session-backed completion, and the completion-guard exit is taken — the all-three-off fixture owns the pre-stdin short-circuit".

Set `EXPECTED_CASE_COUNT=172`.

- [ ] **Step 5: Run the evals**

Run: `bash plugin/anti-tangent-guard/evals/run.sh`
Expected: `Total: 172 passed, 0 failed, 172 total`. In particular the pre-existing `switch-both-off-allows` (id 119) still passes, on its re-pointed needle: with only two switches off the hook now reads stdin and finds a completion call carrying `session_id: sess-eval` (Step 1), so Rule B does not fire, and it exits 0 at the `COMPLETION_GUARD == "0"` step. Without Step 1 this case — and 24 others — would exit 2 here.

- [ ] **Step 6: Commit**

```bash
git add plugin/anti-tangent-guard/hooks/check-task-complete plugin/anti-tangent-guard/evals/guard-evals.json plugin/anti-tangent-guard/evals/run.sh
git commit -m "feat(guard): block a full-protocol close whose completion ran without a task session"
```

```json:metadata
{"files": ["plugin/anti-tangent-guard/hooks/check-task-complete", "plugin/anti-tangent-guard/evals/guard-evals.json", "plugin/anti-tangent-guard/evals/run.sh"], "verifyCommand": "bash plugin/anti-tangent-guard/evals/run.sh", "acceptanceCriteria": ["empty-session completion with no lightweight marker exits 2 with TASK CLOSED WITHOUT A TASK SESSION, in both topologies", "lightweight heading in a user text part or an Agent prompt exempts; inside a tool_result it does not", "mode: lightweight line blocks even with a non-blank session_id", "SESSION_GUARD=0 passes, COMPLETION_GUARD=0 alone still blocks, all three off short-circuits", "verdict fail outranks the rule", "every pre-existing validate_completion fixture input carries session_id; a missing key reads as empty", "a block carrying mode: lightweight still qualifies on tool: and still blocks on verdict: fail", "fixture 119 re-pointed to the completion-guard=0 trace event; the all-three-off fixture asserts guard=0", "in a mixed window the later of direct call and completion block decides", "all 158 pre-existing cases pass; run.sh reports 172 passed"], "modelTier": "standard"}
```

---

### Task 4: Guard packaging and the documents that state the switch set

**Goal:** Guard 0.5.0 is described everywhere the guard is described: plugin metadata, its README, the root README, `CLAUDE.md`, `controller.md` (within 60 bytes), the lightweight example, and a new `## [0.23.0]` CHANGELOG block.

**Files:**
- Modify: `plugin/anti-tangent-guard/.claude-plugin/plugin.json`, `.claude-plugin/marketplace.json`
- Modify: `plugin/anti-tangent-guard/README.md`
- Modify: `README.md` (§anti-tangent-guard, ~line 612–620), `CLAUDE.md` (~line 160), `docs/protocol/controller.md` (~line 86–89) + bundle resync
- Modify: `examples/lightweight-dispatch.md`
- Modify: `CHANGELOG.md`

**Acceptance Criteria:**
- [ ] `plugin.json` and `marketplace.json` say `"version": "0.5.0"` for `anti-tangent-guard`, and their descriptions name three hooks and three kill switches.
- [ ] The guard README has a "Start gate (PreToolUse)" section, "The four block conditions" (renamed and extended), `ANTI_TANGENT_SESSION_GUARD` in the kill-switch list, and the limits from spec §1.3/§1.4 (Bash writes bypass Rule A; the heading is the fingerprint; order is not checked under executing-plans; a non-canonical lightweight clause needs the heading).
- [ ] `docs/protocol/controller.md` is ≤ 16,000 bytes after the edit and `diff -r docs/protocol plugin/anti-tangent-protocol/protocol` is empty.
- [ ] `examples/lightweight-dispatch.md` says its heading is the guard's marker.
- [ ] `CHANGELOG.md` has `## [0.23.0] - 2026-09-18` above `## [0.22.0]` with the guard entries under `### Added`.
- [ ] `go test -race ./internal/notices/...` still passes (nothing here touches it, but the CHANGELOG CI job reads the file: `grep -n '^## \[0.23.0\]' CHANGELOG.md` prints one line).

**Verify:** `wc -c docs/protocol/controller.md && diff -r docs/protocol plugin/anti-tangent-protocol/protocol && grep -c '"version": "0.5.0"' plugin/anti-tangent-guard/.claude-plugin/plugin.json .claude-plugin/marketplace.json && grep -n '^## \[0.23.0\]' CHANGELOG.md` → `<=16000 docs/protocol/controller.md`, no diff output, `1` for each file, one CHANGELOG line.

**Steps:**

- [ ] **Step 1: plugin.json and marketplace.json**

In both files set the guard's `"version"` to `"0.5.0"` and replace its `description` with:

```
Three hooks enforcing anti-tangent-mcp's conventions. A PreToolUse hook on Edit/Write/NotebookEdit refuses a dispatched implementer's first edit until validate_task_spec has been called. A PostToolUse hook on TaskUpdate mandates the validate_completion gate at task close, blocks a full-protocol close whose completion ran with no task session, and, when the evidence that call submitted adds comments carrying change history, returns a blocking instruction to reopen and fix — reading the submitted diff, or a final_files completion through git, which reports nothing for work already committed. It detects rather than prevents: PostToolUse fires after the state change. A PreToolUse hook on Edit/Write refuses such a comment before it is written. Set ANTI_TANGENT_TICKET_PATTERN to your tracker's key shape to have tracker keys caught. Kill switches: ANTI_TANGENT_SESSION_GUARD=0 (start gate and no-session close rule), ANTI_TANGENT_COMPLETION_GUARD=0 (completion gate only), ANTI_TANGENT_COMMENT_GUARD=0 (comment scan, both hooks).
```

In `marketplace.json`'s top-level catalog `description`, replace "anti-tangent-guard for completion enforcement and comment hygiene, which also refuses Edit/Write calls that add comments carrying change history" with "anti-tangent-guard for task-session and completion enforcement and comment hygiene, which also refuses a dispatched implementer's edits before validate_task_spec and Edit/Write calls that add comments carrying change history".

Leave `marketplace.json`'s top-level `"version"` alone; Checkpoint B bumps it with the release.

- [ ] **Step 2: Guard README**

Rename `## The three block conditions` to `## The four block conditions` and append, after item 3:

```markdown
4. **The last `validate_completion` in the window ran with no task session, and
   nothing in the window shows a lightweight dispatch.** An empty `session_id`
   is the server's lightweight path: the review is made against a synthesized
   spec with no acceptance criteria. Under the full protocol that means step 1
   (`validate_task_spec`) was skipped. The empty session is read from the
   direct call's input, or from a pasted block whose `session_id:` line is
   blank or which carries `mode: lightweight`. The lightweight marker is the
   heading `Drift-protection protocol (lightweight)` — the one
   `examples/lightweight-dispatch.md` carries — found in a user message's own
   text or in the `prompt` of an `Agent` tool call. Inside a `tool_result` it
   is file content, not a dispatch decision, and does not count. Absence means
   full protocol: the default dispatch is the full clause, so the default is
   to block. Kill switch: `ANTI_TANGENT_SESSION_GUARD=0`.

   Two limits. Under executing-plans there is no dispatch prompt, so a
   lightweight task there needs the heading in the user's instruction — or
   `validate_task_spec` gets called, which is the right outcome. And the hook
   sees that `validate_task_spec` ran, not when: a call made after the edits
   satisfies this rule; the start gate below is what enforces the order, and
   only in dispatched-subagent sessions.
```

Update the paragraph after the list ("The first two messages state the same recovery flow…") to say the first two and the fourth.

Add a new section before `## Write-time comment guard (PreToolUse hook)`, filling the one `<Task 1's finding: …>` slot from Task 1's DONE report:

```markdown
## Start gate (PreToolUse hook)

`check-task-start` fires on `Edit`, `Write` and `NotebookEdit`. It reads the
transcript up to the first user entry — in a dispatched subagent's transcript
that entry is the dispatch prompt — and gates the session only when that entry
carries `## Drift-protection protocol (anti-tangent-mcp)` and not
`Drift-protection protocol (lightweight)`. In a gated session every write is
refused (`exit 2`) until an `mcp__anti-tangent__validate_task_spec` tool call
appears in the transcript. Reads are never gated: read what the change
touches, call `validate_task_spec`, then edit.

Only a dispatched implementer's session has that first user entry. A
controller meets the clause in file reads and in `Agent` inputs, never as its
own first user message, so a controller editing a CHANGELOG is never touched.

The first positive is cached as
`<scratchpad_dir>/anti-tangent-guard/session-ok/<transcript stem>` when the
hook payload carries `scratchpad_dir`; it holds the transcript's full path,
and later writes from that exact path exit 0 without opening the transcript.
It is per transcript because a dispatched subagent's payload <Task 1's
finding: carries / does not carry> its parent's `scratchpad_dir`, and one file
per scratchpad would let the first implementer's pass wave every later one
through. Without `scratchpad_dir`
there is no cache, and each write before step 1 re-reads a transcript that is
still short.

Limits: writes through `Bash` (`cat >`, `sed -i`) bypass this hook, as they
bypass the comment guard; the close-time no-session rule is the backstop. A
controller that rewrites the clause heading defeats the fingerprint and the
hook fails open — the heading is the contract. A transcript that cannot be
read exits 0. Kill switch: `ANTI_TANGENT_SESSION_GUARD=0`.
```

In `## Kill switches` add a bullet and fix the last one:

```markdown
- `ANTI_TANGENT_SESSION_GUARD=0` disables the start gate (`PreToolUse`) and
  the no-session close rule (block condition 4). It leaves the completion gate
  and the comment scan alone.
- Setting all three to `0` is what short-circuits the `PostToolUse` hook to
  `exit 0` before it reads stdin. With any one still on, the hook reads stdin
  and runs the rules that are still enabled.
```

Add a `task-start` line to the `## Trace log` section's event list matching the wrapper's events (`pass | spec-called`, `pass | sentinel`, `block | no-spec-call`, `skip | not-gated`, `skip | guard=0`) and `block | no-session` for the close-time hook.

- [ ] **Step 3: Root README, CLAUDE.md, controller.md**

`README.md` `### anti-tangent-guard`: change "Two hooks" to "Three hooks", add one paragraph after the `PreToolUse` comment paragraph:

```markdown
A second `PreToolUse` hook, on `Edit`/`Write`/`NotebookEdit`, refuses a dispatched implementer's first edit until `validate_task_spec` has been called, and the close-time hook blocks a full-protocol close whose `validate_completion` ran with no task session (an empty `session_id` is reviewed against a spec with no acceptance criteria). The dispatch prompt's heading is the fingerprint: `## Drift-protection protocol (anti-tangent-mcp)` gates the session, `Drift-protection protocol (lightweight)` exempts it.
```

and replace the kill-switch line with:

```markdown
Kill switches: `ANTI_TANGENT_SESSION_GUARD=0` turns off the start gate and the no-session close rule; `ANTI_TANGENT_COMPLETION_GUARD=0` turns off the completion gate only; `ANTI_TANGENT_COMMENT_GUARD=0` turns off comment scanning at both write time and close time; setting all three is what silences the close-time hook entirely.
```

`CLAUDE.md` ~line 160: replace "`anti-tangent-guard`'s two kill switches are scoped by concern, not by hook: … and setting both is what silences the close-time hook entirely" with "`anti-tangent-guard`'s three kill switches are scoped by concern, not by hook: `ANTI_TANGENT_SESSION_GUARD=0` turns off the start gate and the no-session close rule, `ANTI_TANGENT_COMPLETION_GUARD=0` turns off the completion gate only, and `ANTI_TANGENT_COMMENT_GUARD=0` turns off comment scanning at both write time and close time; each governs its own concern, and setting all three is what silences the close-time hook entirely". Also update the sentence before it that says the guard "blocks two ways of its own" to "three ways", adding: "a `PreToolUse` hook refuses a dispatched implementer's first edit until `validate_task_spec` has been called".

`docs/protocol/controller.md` lines 86–89 currently read as below; the fourth line continues with "It also fails open on its own errors…", which stays as it is:

```
`ANTI_TANGENT_COMPLETION_GUARD=0` turns off the completion gate only and
`ANTI_TANGENT_COMMENT_GUARD=0` turns off comment scanning; setting both is
what disables the hook outright, short-circuiting it to a silent no-op
before it reads anything.
```

Replace with (net +43 bytes, to 15,983; verify with `wc -c` — the part is at 15,940 and must stay ≤ 16,000). This sentence is the one protocol-part change Part 1 makes: Rule B makes "setting both … disables the hook outright" false, and spec §1.6 names it:

```
`ANTI_TANGENT_COMPLETION_GUARD=0` turns off the completion gate only,
`ANTI_TANGENT_COMMENT_GUARD=0` turns off comment scanning, and (guard 0.5.0+)
`ANTI_TANGENT_SESSION_GUARD=0` turns off the session rules; setting all three
is what disables the hook outright, before it reads anything.
```

If `wc -c` reports more than 16,000, shorten "before it reads anything." to "before it reads stdin." and re-count. Then resync:

```bash
rm -f plugin/anti-tangent-protocol/protocol/*.md && cp docs/protocol/*.md plugin/anti-tangent-protocol/protocol/
```

- [ ] **Step 4: Lightweight example**

In `examples/lightweight-dispatch.md`, directly under the `## Drift-protection protocol (lightweight)` heading, add:

```markdown
> Keep this heading verbatim when you adapt the clause. `anti-tangent-guard` reads it as the
> lightweight marker: without it, a close whose `validate_completion` ran with an empty
> `session_id` is blocked as a skipped `validate_task_spec`.
```

- [ ] **Step 5: CHANGELOG**

Insert above `## [0.22.0] - 2026-09-15`:

```markdown
## [0.23.0] - 2026-09-18

### Added

- `anti-tangent-guard` 0.5.0 gains a session guard under one new kill switch,
  `ANTI_TANGENT_SESSION_GUARD=0`. A `PreToolUse` hook on `Edit`/`Write`/`NotebookEdit`
  (`check-task-start`) refuses a dispatched implementer's first edit until
  `validate_task_spec` has been called; the session is recognised by its first user message
  carrying `## Drift-protection protocol (anti-tangent-mcp)` and not the lightweight heading.
  The close-time hook gains a fourth block condition: a `validate_completion` that ran with an
  empty `session_id` — a review against a spec with no acceptance criteria — closes a task only
  when the window shows a lightweight dispatch, marked by the heading
  `Drift-protection protocol (lightweight)` in a user message or an `Agent` prompt. The
  close-time hook now short-circuits only when all three switches are `0`.

### Changed

- The guard README counts four block conditions and three kill switches;
  `examples/lightweight-dispatch.md` names its heading as the guard's marker.
```

- [ ] **Step 6: Verify and commit**

```bash
wc -c docs/protocol/*.md
diff -r docs/protocol plugin/anti-tangent-protocol/protocol && echo "bundle in sync"
bash scripts/check-protocol-docs.sh
grep -n '^## \[0.23.0\]' CHANGELOG.md
git add -A plugin/anti-tangent-guard .claude-plugin/marketplace.json README.md CLAUDE.md docs/protocol/controller.md plugin/anti-tangent-protocol/protocol/controller.md examples/lightweight-dispatch.md CHANGELOG.md
git commit -m "docs(guard): 0.5.0 — three hooks, four block conditions, three kill switches"
```

```json:metadata
{"files": ["plugin/anti-tangent-guard/.claude-plugin/plugin.json", ".claude-plugin/marketplace.json", "plugin/anti-tangent-guard/README.md", "README.md", "CLAUDE.md", "docs/protocol/controller.md", "plugin/anti-tangent-protocol/protocol/controller.md", "examples/lightweight-dispatch.md", "CHANGELOG.md"], "verifyCommand": "wc -c docs/protocol/controller.md && diff -r docs/protocol plugin/anti-tangent-protocol/protocol && bash scripts/check-protocol-docs.sh && grep -n '^## \\[0.23.0\\]' CHANGELOG.md", "acceptanceCriteria": ["plugin.json and marketplace.json at 0.5.0 with three hooks and three switches in the description", "guard README: start gate section, four block conditions, SESSION_GUARD in kill switches, limits stated", "controller.md <= 16000 bytes and bundle in sync", "lightweight example names its heading as the marker", "CHANGELOG has ## [0.23.0] - 2026-09-18 with the guard entries"], "modelTier": "standard"}
```

---

### Task 5: Checkpoint A — merge the guard, move the pin

**Goal:** The guard lands on `main` with `[skip ci]`, the `claude-sandbox-pinned` marketplace pin moves to guard 0.5.0, and the sessions that implement Tasks 6–12 run under Rule A.

**USER-ORDERED GATE — NON-SKIPPABLE.** This task was requested by the user in the current conversation. It MUST NOT be closed by walking around it, by declaring it "verified inline", or by substituting a cheaper check. Close only after every item in `acceptanceCriteria` has been re-validated independently, with output captured.

**Files:**
- None in this repo beyond what Tasks 1–4 committed. The pin lives in the user's `claude-sandbox` repo.

**Acceptance Criteria:**
- [ ] The branch is pushed as `origin/version/0.23.0` (`git push origin HEAD:refs/heads/version/0.23.0`) and CI's `changelog` and `hook-evals` jobs are green on it.
- [ ] A PR from `version/0.23.0` to `main` titled `guard 0.5.0: session guard (start gate + no-session close rule) [skip ci]` is merged by the user **as a merge commit — not squash, not rebase** (`gh pr merge <PR#> --merge`). The branch is reused for Checkpoint B, so its guard commits must be in `main`'s ancestry; a squash would put them in the second PR again. The merge commit message carries `[skip ci]`, and `git merge-base --is-ancestor <branch tip at merge> origin/main` succeeds.
- [ ] The user has moved the `claude-sandbox-pinned` pin for `anti-tangent-guard` to 0.5.0 and the local install shows it: `jq -r '.plugins["anti-tangent-guard@claude-sandbox-pinned"][0].version' "${CLAUDE_CONFIG_DIR:-$HOME/.claude}/plugins/installed_plugins.json"` → `0.5.0`.
- [ ] The executing session has been restarted after the pin moved (hooks load at session start), and a disposable probe subagent dispatched from it (Step 3) has its first `Write` refused with `EDIT BEFORE validate_task_spec`, with a matching `task-start | block | no-spec-call` line in `/tmp/claude-hooks/anti-tangent-guard.log` (or the trace path the environment sets). The probe is part of this task, so Task 5 closes before Task 6 is dispatched.

**Verify:** `gh pr checks <PR#> --json name,state -q '.[] | select(.name == "Changelog entry" or .name == "Plugin hook evals") | .name + " " + .state'` → both `SUCCESS` (the display names of `ci.yml`'s `changelog` and `hook-evals` jobs); `gh pr view --json state,mergeCommit -q '.state + " " + .mergeCommit.oid' <PR#>` → `MERGED <sha>`; `git fetch origin main && git log -1 --format=%B <sha> | grep -F '[skip ci]'` → the line carrying it; `jq -r '.plugins["anti-tangent-guard@claude-sandbox-pinned"][0].version' "${CLAUDE_CONFIG_DIR:-$HOME/.claude}/plugins/installed_plugins.json"` → `0.5.0`.

**Steps:**

- [ ] **Step 1: Push and open the PR**

```bash
go build ./... && go test -race ./... && bash plugin/anti-tangent-guard/evals/run.sh
git push origin HEAD:refs/heads/version/0.23.0
gh pr create --base main --head version/0.23.0 \
  --title "guard 0.5.0: session guard (start gate + no-session close rule) [skip ci]" \
  --body-file - <<'EOF'
Part 1 of docs/superpowers/specs/2026-09-18-lean-by-default-design.md: the anti-tangent-guard session guard.

- `check-task-start` (PreToolUse on Edit/Write/NotebookEdit): a dispatched implementer's first edit is refused until validate_task_spec has been called.
- `check-task-complete`: fourth block condition — a full-protocol close whose validate_completion ran with an empty session_id.
- One new kill switch, ANTI_TANGENT_SESSION_GUARD=0.

Merges with [skip ci]; the release ships with Part 2 (server + protocol) from the same branch.

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
```

- [ ] **Step 2: Hand to the user**

Tell the user: CI status, the PR link, and the two actions that are theirs — merge with **Create a merge commit** (not squash or rebase: this branch is reused for Checkpoint B) and `[skip ci]` in the merge commit message, then move the `claude-sandbox-pinned` pin to guard 0.5.0 and restart this session. Resume with `/superpowers-extended-cc:executing-plans docs/superpowers/plans/2026-09-18-lean-by-default.md` (the `.tasks.json` carries the state). Do not proceed to Task 6 in a session whose hooks predate the pin.

- [ ] **Step 3: Confirm after restart, with a disposable probe**

```bash
jq -r '.plugins["anti-tangent-guard@claude-sandbox-pinned"][0].version' "${CLAUDE_CONFIG_DIR:-$HOME/.claude}/plugins/installed_plugins.json"
```

Expected: `0.5.0`. Confirm the merge kept the branch's history:

```bash
git fetch origin main && git merge-base --is-ancestor HEAD origin/main && echo "branch tip is in main"
```

Expected: `branch tip is in main` (run before any Task 6 commit). Then create the probe's target directory and note its path:

```bash
PROBE_DIR=$(mktemp -d) && echo "$PROBE_DIR"
```

Dispatch ONE throwaway subagent with the `Agent` tool (`subagent_type: general-purpose`), with that printed path in place of `<probe_dir>`:

```
Start-gate probe — not a task. Nothing here is to be implemented.

## Drift-protection protocol (anti-tangent-mcp)

The heading above is here only so the guard fingerprints this session. Do NOT
call any anti-tangent tool. Your only action: use the Write tool to create
<probe_dir>/start-gate-probe.txt containing the word probe. Report the Write
tool's result verbatim, including any error text, and stop.
```

Expected: the reply quotes `EDIT BEFORE validate_task_spec`;

```bash
test ! -e "$PROBE_DIR/start-gate-probe.txt" && echo "probe file absent"
```

prints `probe file absent`; and

```bash
grep 'task-start' "${ANTI_TANGENT_GUARD_TRACE_LOG:-/tmp/claude-hooks/anti-tangent-guard.log}" | tail -3
```

shows a `task-start | block | no-spec-call` line. The probe costs no reviewer call — it never reaches `validate_task_spec`. Only after this passes does Task 6 get dispatched.

```json:metadata
{"files": [], "verifyCommand": "jq -r '.plugins[\"anti-tangent-guard@claude-sandbox-pinned\"][0].version' \"${CLAUDE_CONFIG_DIR:-$HOME/.claude}/plugins/installed_plugins.json\"", "acceptanceCriteria": ["branch pushed as version/0.23.0 with green changelog and hook-evals jobs, shown by gh pr checks", "PR merged to main; the merge commit message carries [skip ci], shown by git log", "installed guard version is 0.5.0", "session restarted; a disposable probe subagent's first Write is refused with EDIT BEFORE validate_task_spec and traces task-start block no-spec-call"], "modelTier": "standard", "userGate": true, "tags": ["user-gate"], "requireEvidenceTokens": [["MERGED"], ["[skip ci]"], ["SUCCESS"], ["0.5.0"], ["branch tip is in main"], ["EDIT BEFORE validate_task_spec"], ["probe file absent"]]}
```

---

### Task 6: `lightweight` on the envelope and `mode: lightweight` in the summary block

**Goal:** An empty-session `validate_completion` sets `Envelope.Lightweight` and prints one extra header line in `summary_block`; a session-backed one is byte-identical to today.

**Files:**
- Modify: `internal/mcpsrv/handlers.go` (`Envelope` ~line 31–60; the payload-cap rejection in `ValidateCompletion` ~line 1688; the `env := Envelope{…}` in `ValidateCompletion` ~line 1845)
- Modify: `internal/mcpsrv/summary.go` (`formatEnvelopeSummary`, after the `verdict:` line)
- Test: `internal/mcpsrv/handlers_test.go`, `internal/mcpsrv/summary_contract_test.go`

**Acceptance Criteria:**
- [ ] `ValidateCompletion` with `SessionID: ""` and non-empty `FinalFiles` returns `env.Lightweight == true` and a `summary_block` containing a line matching `(?m)^\s*mode:\s*lightweight\s*$` immediately after the `verdict:` line.
- [ ] The `payload_too_large` rejection of an empty-session `ValidateCompletion` also sets `env.Lightweight` and prints the `mode:` line: the flag describes the call, not the review, and spec §1.5 does not qualify it.
- [ ] A session-backed `ValidateCompletion` returns `env.Lightweight == false` and no `mode:` line. `formatEnvelopeSummary` of a minimal session-backed envelope equals a pinned literal — the block as rendered before this task, captured at plan time — and the same envelope with `Lightweight: true` differs from it by exactly the `mode:` line.
- [ ] The guard's regexes still match on a block carrying the line: the contract test asserts `(?m)^\s*tool:\s*validate_completion\s*$`, `session_id:`, and `(?m)^\s*verdict:\s*(\w+)` all match, and that `mode:` sits between `verdict:` and `model_used:`.
- [ ] `go test -race ./internal/mcpsrv/...` passes.

**Verify:** `go test -race ./internal/mcpsrv/ -run 'Lightweight|SummaryBlockContract' -v` → all PASS

**Steps:**

- [ ] **Step 1: Write the failing tests**

Append to `internal/mcpsrv/handlers_test.go`, next to the other `TestValidateCompletion_LightweightMode_*` tests:

```go
func TestValidateCompletion_LightweightMode_FlagsEnvelopeAndSummary(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}
	_, env, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		SessionID:  "",
		Summary:    "trivial doc change",
		FinalFiles: []CompletionFileArg{{Path: "doc.md", Content: strPtr("updated\n")}},
	})
	require.NoError(t, err)
	assert.True(t, env.Lightweight, "an empty-session completion is lightweight")
	modeRe := regexp.MustCompile(`(?m)^\s*verdict:\s*\w+\s*\n\s*mode:\s*lightweight\s*$`)
	assert.True(t, modeRe.MatchString(env.SummaryBlock),
		"summary_block must carry mode: lightweight right after verdict:\n%s", env.SummaryBlock)
}

func TestValidateCompletion_SessionBacked_IsNotLightweight(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}
	_, pre, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "T", Goal: "g", AcceptanceCriteria: []string{"a"},
	})
	require.NoError(t, err)
	_, env, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		SessionID:  pre.SessionID,
		Summary:    "done",
		FinalFiles: []CompletionFileArg{{Path: "doc.md", Content: strPtr("updated\n")}},
	})
	require.NoError(t, err)
	assert.False(t, env.Lightweight)
	assert.NotContains(t, env.SummaryBlock, "mode:")
}

func TestValidateCompletion_LightweightMode_TooLargeStillFlagged(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	d.Cfg.MaxPayloadBytes = 200
	h := &handlers{deps: d}
	_, env, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		SessionID:  "",
		Summary:    "trivial doc change",
		FinalFiles: []CompletionFileArg{{Path: "doc.md", Content: strPtr(strings.Repeat("x", 300))}},
	})
	require.NoError(t, err)
	require.Equal(t, "fail", env.Verdict, "over the cap is a payload_too_large rejection")
	assert.True(t, env.Lightweight, "the rejection of an empty-session completion is still lightweight")
	assert.Contains(t, env.SummaryBlock, "mode:          lightweight")
}
```

(If `handlers_test.go` does not already import `regexp` or `strings`, add them.)

Append to `internal/mcpsrv/summary_contract_test.go`:

```go
// TestSummaryBlockContractModeLineDoesNotDisturbMarkers pins that the
// lightweight mode line is an extra header key the guard's per-key regexes
// step over: tool:, session_id: and verdict: still match, and the line sits
// between verdict: and model_used: so a block reads the same to a guard that
// has never heard of it.
func TestSummaryBlockContractModeLineDoesNotDisturbMarkers(t *testing.T) {
	got := formatEnvelopeSummary(Envelope{
		Tool:        "validate_completion",
		SessionID:   "",
		Verdict:     string(verdict.VerdictPass),
		Lightweight: true,
		NextAction:  "proceed",
		ModelUsed:   "anthropic:claude-opus-4-7",
	})
	require.True(t, regexp.MustCompile(`(?m)^\s*tool:\s*validate_completion\s*$`).MatchString(got), got)
	require.Contains(t, got, "session_id:")
	require.True(t, regexp.MustCompile(`(?m)^\s*verdict:\s*(\w+)`).MatchString(got), got)
	seq := regexp.MustCompile(`(?s)verdict:.*\n\s*mode:\s*lightweight\s*\n\s*model_used:`)
	require.True(t, seq.MatchString(got), "mode: must sit between verdict: and model_used:\n%s", got)

	plain := formatEnvelopeSummary(Envelope{Tool: "validate_completion", SessionID: "s", Verdict: "pass", NextAction: "x", ModelUsed: "m"})
	require.Equal(t, "anti-tangent envelope\n"+
		"  tool:          validate_completion\n"+
		"  session_id:    s\n"+
		"  verdict:       pass\n"+
		"  model_used:    m\n"+
		"  review_ms:     0\n"+
		"  findings:      0 total (0 critical, 0 major, 0 minor)\n"+
		"  next_action:   x\n", plain, "a session-backed block carries no mode line and no other change")
	lite := formatEnvelopeSummary(Envelope{Tool: "validate_completion", SessionID: "s", Verdict: "pass", Lightweight: true, NextAction: "x", ModelUsed: "m"})
	require.Equal(t, plain, strings.Replace(lite, "  mode:          lightweight\n", "", 1),
		"the mode line is the only difference the flag makes")
}
```

- [ ] **Step 2: Run to see them fail**

Run: `go test -race ./internal/mcpsrv/ -run 'LightweightMode_Flags|LightweightMode_TooLarge|SessionBacked_IsNotLightweight|ModeLine' 2>&1 | head -20`
Expected: compile error `env.Lightweight undefined`.

- [ ] **Step 3: Add the field and the line**

In `Envelope` (handlers.go), after `ControllerRulings`:

```go
	// Lightweight marks a validate_completion that ran with no task session:
	// the review used a synthesized spec — Goal only, no acceptance criteria —
	// so a controller reading the DONE report can tell this review from a
	// session-backed one on any host. formatEnvelopeSummary prints it as a
	// `mode: lightweight` header line.
	Lightweight bool `json:"lightweight,omitempty"`
```

In `ValidateCompletion`'s `env := Envelope{…}` literal add `Lightweight: lightweight,`. In the payload-cap rejection a few lines after `lightweight := args.SessionID == ""`, set it on the rejection envelope before it is returned. `prependClamp` only prepends a finding; `summary_block` is formatted later, inside `rejectionEnvelopeResult` → `envelopeResult`, so a flag set here reaches the `mode:` line:

```go
		env := prependClamp(tooLargeEnvelope("validate_completion", args.SessionID, h.deps.Cfg.PostModel, size, h.deps.Cfg.MaxPayloadBytes,
			"Send a unified diff via final_diff rather than whole files. "+completionShrinkAdvice), clamp)
		env.Lightweight = lightweight
```

In `summary.go`, directly after the `verdict:` `Fprintf`:

```go
	if env.Lightweight {
		b.WriteString("  mode:          lightweight\n")
	}
```

- [ ] **Step 4: Run the package tests**

Run: `go test -race ./internal/mcpsrv/...`
Expected: PASS. If `guard_eval_fixture_test.go` fails, its pinned fixtures are session-backed and must not have changed — investigate before touching the fixtures.

- [ ] **Step 5: Commit**

```bash
git add internal/mcpsrv/handlers.go internal/mcpsrv/summary.go internal/mcpsrv/handlers_test.go internal/mcpsrv/summary_contract_test.go
git commit -m "feat: mark a lightweight validate_completion in the envelope and summary block"
```

```json:metadata
{"files": ["internal/mcpsrv/handlers.go", "internal/mcpsrv/summary.go", "internal/mcpsrv/handlers_test.go", "internal/mcpsrv/summary_contract_test.go"], "verifyCommand": "go test -race ./internal/mcpsrv/ -run 'Lightweight|SummaryBlockContract' -v", "acceptanceCriteria": ["empty-session completion sets Lightweight and prints mode: lightweight after verdict:", "the payload_too_large rejection of an empty-session completion sets it too", "session-backed completion has neither", "guard marker regexes still match a block with the mode line and mode: sits between verdict: and model_used:", "go test -race ./internal/mcpsrv/... passes"], "modelTier": "standard"}
```

---

### Task 7: `lean.tmpl`, `prompts.LeanGuidance()`, and the ponytail attribution

**Goal:** The implementer ruleset exists as one embedded template with a golden, an exported render function, and its MIT attribution asserted by a test.

**Files:**
- Create: `internal/prompts/templates/lean.tmpl`
- Modify: `internal/prompts/prompts.go` (add `LeanGuidance`), `internal/prompts/prompts_test.go`
- Create (via `-update`): `internal/prompts/testdata/lean_guidance.golden`
- Modify: `THIRD_PARTY_NOTICES.md`, `internal/notices/notices_test.go`

**Acceptance Criteria:**
- [ ] `prompts.LeanGuidance()` returns the text below verbatim (golden `lean_guidance.golden`), beginning with `## Build guidance (apply while implementing this task)`.
- [ ] `THIRD_PARTY_NOTICES.md` has a `## ponytail` section naming `https://github.com/DietrichGebert/ponytail`, `Copyright (c) 2026 DietrichGebert`, `MIT`, commit `e3ba2aa6f1e6f0bc4d69eb09c9f0d0a93af56156`, and both derived files.
- [ ] A new notices test, `TestThirdPartyNoticesListPonytail` (beside the existing `TestThirdPartyNoticesPresent`, which is left as it is), asserts those strings and the two paths (the plan_lean_rules path is asserted now; Task 9 creates the file).
- [ ] The attributed facts are true upstream, re-checked in Step 0: at commit `e3ba2aa6f1e6f0bc4d69eb09c9f0d0a93af56156`, `LICENSE` is the MIT licence with `Copyright (c) 2026 DietrichGebert`, and `skills/ponytail/SKILL.md` and `skills/ponytail-review/SKILL.md` exist. (Checked at plan time on 2026-09-18.)
- [ ] `go test -race ./internal/prompts/... ./internal/notices/...` passes.

**Verify:** `go test -race ./internal/prompts/... ./internal/notices/...` → ok

**Steps:**

- [ ] **Step 0: Re-check the upstream facts the notice states**

```bash
gh api 'repos/DietrichGebert/ponytail/contents/LICENSE?ref=e3ba2aa6f1e6f0bc4d69eb09c9f0d0a93af56156' --jq .content | base64 -d | head -3
gh api 'repos/DietrichGebert/ponytail/git/trees/e3ba2aa6f1e6f0bc4d69eb09c9f0d0a93af56156?recursive=1' --jq '.tree[].path' | grep -E '^skills/ponytail(-review)?/SKILL.md$'
```

Expected: `MIT License`, a blank line, `Copyright (c) 2026 DietrichGebert`; then both `SKILL.md` paths. If either differs, stop and report — the notice text below states these facts.

- [ ] **Step 1: Write the failing tests**

Append to `internal/prompts/prompts_test.go`:

```go
func TestLeanGuidance(t *testing.T) {
	got, err := LeanGuidance()
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(got, "## Build guidance (apply while implementing this task)"), got)
	golden(t, "lean_guidance", got)
}
```

In `internal/notices/notices_test.go`, add a second test after the first:

```go
// The ponytail ruleset adapted into lean.tmpl and plan_lean_rules.tmpl is
// MIT-licensed; the notice must survive alongside the shunt entry.
func TestThirdPartyNoticesListPonytail(t *testing.T) {
	b, err := os.ReadFile(noticesPath)
	if err != nil {
		t.Fatalf("THIRD_PARTY_NOTICES.md is required: %v", err)
	}
	body := string(b)
	for _, want := range []string{
		"## ponytail",
		"https://github.com/DietrichGebert/ponytail",
		"Copyright (c) 2026 DietrichGebert",
		"MIT",
		"e3ba2aa6f1e6f0bc4d69eb09c9f0d0a93af56156",
		"internal/prompts/templates/lean.tmpl",
		"internal/prompts/templates/plan_lean_rules.tmpl",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("THIRD_PARTY_NOTICES.md must contain %q", want)
		}
	}
}
```

- [ ] **Step 2: Run to see them fail**

Run: `go test ./internal/prompts/ -run TestLeanGuidance; go test ./internal/notices/`
Expected: `undefined: LeanGuidance`; the notices test reports each missing string.

- [ ] **Step 3: Write the template**

`internal/prompts/templates/lean.tmpl` — exactly this content, no `define` wrapper (it is executed by file name and included by file name):

```markdown
## Build guidance (apply while implementing this task)

Build every acceptance criterion in its leanest working form. Lean means
efficient, not careless: read the task and the code it touches first, trace
the real flow end to end, then climb the ladder and stop at the first rung
that holds.

1. Already in this codebase? A helper, util, type or pattern that already
   lives here → reuse it. Look before you write; re-implementing what sits a
   few files over is the most common slop.
2. Stdlib does it? Use it.
3. Native platform feature covers it? `<input type="date">` over a picker
   lib, CSS over JS, a DB constraint over app code.
4. An already-installed dependency solves it? Use it. Never add a new one
   for what a few lines can do.
5. Can it be one line? One line.
6. Only then: the minimum code that works.

Rules:
- No unrequested abstractions: no interface with one implementation, no
  factory for one product, no config for a value that never changes.
- No boilerplate, no scaffolding "for later"; later can scaffold for itself.
- Deletion over addition. Boring over clever — clever is what someone
  decodes at 3am.
- Fewest files, shortest working diff — once you understand the problem.
  The smallest change in the wrong place is not lean, it is a second bug.
- Bug fix = root cause, not symptom. Grep every caller before you edit; one
  guard in the shared function is a smaller diff than a guard in every caller.
- Two stdlib options, same size? Take the one that is correct on edge cases.
- A deliberate simplification with a known ceiling (global lock, O(n²) scan,
  naive heuristic) gets a comment naming the ceiling and the upgrade path.

Scope is set by the acceptance criteria, not by this guidance: never skip,
narrow or defer an AC to be lean. Speculative work beyond the ACs is what
to skip. If an AC itself mandates more structure than its goal needs, build
it as written — scope is the controller's call. The completion review will
still name it, and your summary_block carries that to the controller.
Anything the task spec's Context: justifies is deliberate; build it.

Never simplify away: input validation at trust boundaries, error handling
that prevents data loss, security measures, accessibility basics, the tests
the task calls for, or anything explicitly requested.

The reviewer checks your changes against this ruleset at check_progress and
validate_completion and reports departures as quality / over_building, minor.
```

- [ ] **Step 4: Export the render function**

In `internal/prompts/prompts.go`, after `RenderPost`:

```go
// LeanGuidance renders the build ruleset validate_task_spec hands an
// implementer. mid.tmpl and post.tmpl include the same file, so the reviewer
// judges the diff against the text the implementer was given rather than a
// second copy that could drift.
func LeanGuidance() (string, error) {
	return render("lean.tmpl", nil)
}
```

- [ ] **Step 5: Write the attribution**

In `THIRD_PARTY_NOTICES.md`, after the shunt section's file list and before the `---` separator that precedes the Apache text, add:

```markdown
## ponytail

- Source: https://github.com/DietrichGebert/ponytail
- Copyright (c) 2026 DietrichGebert
- Licensed under the MIT License

Files in this repository derived from that work:

- `internal/prompts/templates/lean.tmpl` — adapted from `skills/ponytail/SKILL.md`
- `internal/prompts/templates/plan_lean_rules.tmpl` — the tag vocabulary and examples from
  `skills/ponytail-review/SKILL.md`

The upstream commit adapted from is `e3ba2aa6f1e6f0bc4d69eb09c9f0d0a93af56156`.

MIT License

Copyright (c) 2026 DietrichGebert

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

`TestThirdPartyNoticesPresent` compares the text after the LAST `---` to the canonical Apache text, so this section must sit above that separator and contain no `---` line of its own.

- [ ] **Step 6: Generate the golden, run both packages**

```bash
go test ./internal/prompts/ -run TestLeanGuidance -update
git diff --stat internal/prompts/testdata/
go test -race ./internal/prompts/... ./internal/notices/...
```

Expected: one new golden; both packages `ok`.

- [ ] **Step 7: Commit**

```bash
git add internal/prompts/templates/lean.tmpl internal/prompts/prompts.go internal/prompts/prompts_test.go internal/prompts/testdata/lean_guidance.golden THIRD_PARTY_NOTICES.md internal/notices/notices_test.go
git commit -m "feat(prompts): lean build guidance template, with ponytail attribution"
```

```json:metadata
{"files": ["internal/prompts/templates/lean.tmpl", "internal/prompts/prompts.go", "internal/prompts/prompts_test.go", "internal/prompts/testdata/lean_guidance.golden", "THIRD_PARTY_NOTICES.md", "internal/notices/notices_test.go"], "verifyCommand": "go test -race ./internal/prompts/... ./internal/notices/...", "acceptanceCriteria": ["LeanGuidance() renders lean.tmpl verbatim, pinned by lean_guidance.golden", "THIRD_PARTY_NOTICES.md has a ponytail section with URL, copyright, MIT, commit and both derived paths", "notices test asserts the ponytail entry", "both packages pass with -race"], "modelTier": "standard"}
```

---

### Task 8: `implementation_guidance` on `validate_task_spec`

**Goal:** Every `validate_task_spec` response that reached the reviewer carries the lean ruleset in `implementation_guidance`; the other two envelope tools never set it; the README documents both new envelope fields.

**Files:**
- Modify: `internal/mcpsrv/handlers.go` (`Envelope`; `ValidateTaskSpec` after `env := Envelope{…}`)
- Test: `internal/mcpsrv/handlers_test.go`
- Modify: `README.md` (envelope JSON block ~line 502–520 and the paragraph after it)

**Acceptance Criteria:**
- [ ] `ValidateTaskSpec` (reviewed) returns `env.ImplementationGuidance` starting with `## Build guidance` and equal to `prompts.LeanGuidance()`.
- [ ] The guidance is not in `env.SummaryBlock`.
- [ ] `CheckProgress` and `ValidateCompletion` return an empty `ImplementationGuidance`.
- [ ] A `validate_task_spec` rejected before review (`payload_too_large`) has an empty `ImplementationGuidance`.
- [ ] README's envelope example lists `implementation_guidance` and `lightweight` with one sentence each.

**Verify:** `go test -race ./internal/mcpsrv/ -run 'ImplementationGuidance' -v` → PASS; `grep -c -E '"(implementation_guidance|lightweight)":' README.md` → `2` (both keys in the envelope example) and `grep -c -E '^`implementation_guidance`' README.md` → `1` (the explanatory paragraph)

**Steps:**

- [ ] **Step 1: Write the failing tests**

Append to `internal/mcpsrv/handlers_test.go`:

```go
func TestValidateTaskSpec_ReturnsImplementationGuidance(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}
	_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "T", Goal: "g", AcceptanceCriteria: []string{"a"},
	})
	require.NoError(t, err)
	want, err := prompts.LeanGuidance()
	require.NoError(t, err)
	assert.Equal(t, want, env.ImplementationGuidance)
	assert.True(t, strings.HasPrefix(env.ImplementationGuidance, "## Build guidance"))
	assert.NotContains(t, env.SummaryBlock, "Build guidance",
		"the ruleset is for the implementer, not for the pasted DONE report")
}

func TestCheckProgressAndCompletion_NoImplementationGuidance(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	h := &handlers{deps: d}
	_, pre, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "T", Goal: "g", AcceptanceCriteria: []string{"a"},
	})
	require.NoError(t, err)
	_, mid, err := h.CheckProgress(context.Background(), nil, CheckProgressArgs{
		SessionID: pre.SessionID, WorkingOn: "x",
		ChangedFiles: []FileArg{{Path: "a.go", Content: "package a\n"}},
	})
	require.NoError(t, err)
	assert.Empty(t, mid.ImplementationGuidance)
	_, post, err := h.ValidateCompletion(context.Background(), nil, ValidateCompletionArgs{
		SessionID: pre.SessionID, Summary: "done",
		FinalFiles: []CompletionFileArg{{Path: "a.go", Content: strPtr("package a\n")}},
	})
	require.NoError(t, err)
	assert.Empty(t, post.ImplementationGuidance)
}

func TestValidateTaskSpec_PayloadTooLarge_NoImplementationGuidance(t *testing.T) {
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	d := newDeps(t, rv)
	d.Cfg.MaxPayloadBytes = 200
	h := &handlers{deps: d}
	_, env, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "T", Goal: "g", AcceptanceCriteria: []string{"a"},
		ProjectKnowledge: strings.Repeat("p", 300),
	})
	require.Error(t, err, "over the cap is rejected before review")
	assert.Empty(t, env.ImplementationGuidance)
}
```

The over-cap call returns `Envelope{}` and an error from `normalizeTaskSpecInputs` (handlers.go ~line 120) before the `env :=` literal, so the third test passes as soon as it compiles; it pins the spec's "not on `payload_too_large`" against a later refactor that builds the envelope first. Add `"github.com/patiently/anti-tangent-mcp/internal/prompts"` to the test imports if absent.

- [ ] **Step 2: Run to see them fail**

Run: `go test ./internal/mcpsrv/ -run ImplementationGuidance 2>&1 | head`
Expected: `env.ImplementationGuidance undefined`.

- [ ] **Step 3: Add the field and set it**

In `Envelope`, after `Lightweight`:

```go
	// ImplementationGuidance is the build ruleset the implementer applies
	// while working: lean.tmpl, which mid.tmpl and post.tmpl also include as
	// the reviewer's definition of over-building. Only validate_task_spec sets
	// it, because only implementers call that tool; it is not part of the
	// summary block, which is what gets pasted into DONE reports.
	ImplementationGuidance string `json:"implementation_guidance,omitempty"`
```

In `ValidateTaskSpec`, immediately after the `env := Envelope{…}` literal and before the `if args.PlanRunID == ""` block:

```go
	guidance, err := prompts.LeanGuidance()
	if err != nil {
		return nil, Envelope{}, fmt.Errorf("render lean guidance: %w", err)
	}
	env.ImplementationGuidance = guidance
```

- [ ] **Step 4: Document in README**

In the envelope JSON block, add a comma to the last line (`"session_ttl_remaining_seconds": 14399,`) and append, before the closing `}`:

```json
  "implementation_guidance": "## Build guidance (apply while implementing this task)\n…",
  "lightweight": true
```

and after the paragraph that starts "`session_expires_at` and `session_ttl_remaining_seconds` are included…", add:

```markdown
`implementation_guidance` (v0.23.0+) is set only by `validate_task_spec`: the lean build ruleset the implementer applies, the same text `check_progress` and `validate_completion` hold the diff to as `quality` / `over_building` (always `minor`). `lightweight: true` (v0.23.0+) is set by a `validate_completion` that ran with an empty `session_id`; its `summary_block` carries a `mode: lightweight` line, so a controller can tell a review that had no acceptance criteria from a session-backed one.
```

- [ ] **Step 5: Run and commit**

```bash
go test -race ./internal/mcpsrv/...
git add internal/mcpsrv/handlers.go internal/mcpsrv/handlers_test.go README.md
git commit -m "feat: validate_task_spec returns the lean build guidance as implementation_guidance"
```

```json:metadata
{"files": ["internal/mcpsrv/handlers.go", "internal/mcpsrv/handlers_test.go", "README.md"], "verifyCommand": "go test -race ./internal/mcpsrv/ -run 'ImplementationGuidance' -v", "acceptanceCriteria": ["validate_task_spec returns implementation_guidance equal to prompts.LeanGuidance()", "summary_block does not contain it", "check_progress and validate_completion leave it empty", "a payload_too_large validate_task_spec leaves it empty", "README documents implementation_guidance and lightweight"], "modelTier": "standard"}
```

---

### Task 9: `plan_lean_rules.tmpl` and its inclusions

**Goal:** `validate_plan` (all three templates) and `validate_task_spec` check for plan-level over-building with the six tags; goldens regenerated and reviewed.

**Files:**
- Create: `internal/prompts/templates/plan_lean_rules.tmpl`
- Modify: `internal/prompts/templates/plan.tmpl`, `plan_tasks_chunk.tmpl`, `plan_findings_only.tmpl`, `pre.tmpl`
- Modify (via `-update`): `internal/prompts/testdata/{pre_*,plan_*}.golden`
- Test: `internal/prompts/prompts_test.go`

**Acceptance Criteria:**
- [ ] Rendered `pre`, `plan`, `plan_tasks_chunk` prompts contain `### Over-building` and the six tag words, and the shared section reads for a single task spec as well as a plan (spec §2.1's scope word: the opening line, `delete`, the never-flag Goal and the emission sentence each name the one-spec case); `plan_findings_only` contains `### Over-building across tasks` and not the per-task emission sentence.
- [ ] The per-task and cross-task scopes are mutually exclusive: the cross-task section is for patterns no single task owns, and says a structure one task introduces is that task's `yagni:` instance and "is never repeated here" (asserted in `plan_findings_only`).
- [ ] `plan_tasks_chunk` does not contain "plan-level finding" (chunk calls must not emit `plan_findings`); `plan_findings_only` does not contain the per-task emission sentence (the one beginning "Emit at most ONE", which the test pins by its `finding per task` wording); the cross-task section's own "judged per task, not here" is expected.
- [ ] `plan.tmpl` and `plan_findings_only.tmpl` place the section before `{{template "plan_comment_rules" . -}}`, so the cached `UserPrefix` (everything before `## What to evaluate`) is unchanged — assert `splitPlanPrompt`'s prefix for `plan_findings_only` is byte-identical to the chunk render's prefix (the existing `TestRenderPlan*Prefix*` test, if present, covers it; otherwise add the assertion below).
- [ ] Goldens regenerated; `git diff internal/prompts/testdata/` shows only the new section added.

**Verify:** `go test -race ./internal/prompts/...` → ok

**Steps:**

- [ ] **Step 1: Write the failing test**

Append to `internal/prompts/prompts_test.go`:

```go
func TestLeanRulesPlacement(t *testing.T) {
	pre, err := RenderPre(PreInput{Spec: sampleSpec()})
	require.NoError(t, err)
	assert.Contains(t, pre.User, "### Over-building")

	plan, err := RenderPlan(PlanInput{PlanText: "### Task 1: x\n\n**Goal:** g\n"})
	require.NoError(t, err)
	// "### Over-building" is a prefix of the cross-task heading, so the
	// per-task section is pinned by its own emission sentence instead.
	assert.Contains(t, plan.User, "at most ONE `over_building` finding per task")
	assert.Contains(t, plan.User, "### Over-building across tasks")

	chunk, err := RenderPlanTasksChunk(PlanChunkInput{PlanText: "### Task 1: x\n", ChunkTasks: []planparser.RawTask{{Title: "Task 1: x"}}})
	require.NoError(t, err)
	assert.Contains(t, chunk.User, "### Over-building")
	assert.NotContains(t, chunk.User, "plan-level finding")

	only, err := RenderPlanFindingsOnly(PlanInput{PlanText: "### Task 1: x\n"})
	require.NoError(t, err)
	assert.Contains(t, only.User, "### Over-building across tasks")
	// One structure is reported once: a single task's interface is that task's
	// yagni instance, and the cross-task finding is for patterns no task owns.
	assert.Contains(t, only.User, "is never repeated here")
	assert.NotContains(t, only.User, "at most ONE `over_building` finding per task")

	assert.Contains(t, pre.User, "for a single task spec, one finding in all",
		"validate_task_spec reviews one spec, so the emission rule must read for it too")

	for _, tag := range []string{"`reuse:`", "`stdlib:`", "`native:`", "`yagni:`", "`delete:`", "`shrink:`"} {
		assert.Contains(t, pre.User, tag)
		assert.Contains(t, plan.User, tag)
		assert.Contains(t, chunk.User, tag)
	}
	// The shared cacheable head is identical across the chunked templates
	// because the over-building sections sit in the per-call suffix.
	assert.Equal(t, only.UserPrefix, chunk.UserPrefix)
}
```

Check `planparser.RawTask`'s field names with `grep -n 'type RawTask' -A6 internal/planparser/*.go` and adjust the literal if `Title` is named differently.

- [ ] **Step 2: Run to see it fail**

Run: `go test ./internal/prompts/ -run TestLeanRulesPlacement`
Expected: FAIL on the first `Contains`.

- [ ] **Step 3: Write the template**

`internal/prompts/templates/plan_lean_rules.tmpl`:

```
{{define "plan_lean_rules"}}### Over-building

Plans — and the single task specs drawn from them — over-build in six ways. Report each as `category: quality`, `criterion: over_building`, always `severity: minor`, tagging every instance with one of these words:

- `reuse:` a task writes a helper that an attached file or the Project knowledge section already provides. Cite the attached path and the symbol. Never infer this from the black box: a helper you cannot see is silence, not approval.
- `stdlib:` a task adds a dependency, or hand-writes a utility, for what the language's standard library covers. Read the language from the paths and fences; this is general knowledge, not a codebase claim. Say an installed dependency already covers it only when a manifest (`go.mod`, `package.json`, `pyproject.toml`, …) is attached.
- `native:` a task builds in application code what the platform provides — uniqueness enforced in a service instead of a database constraint, a date-picker library over `<input type="date">`, JavaScript over CSS.
- `yagni:` a task mandates an interface or abstract type with one implementation, a factory or registry for one product, a configuration value or flag no task varies, or a layer with one caller — judged across the whole plan (or, when a single task spec is all you have, across that spec). Cite the introducing task and the absence of a second consumer.
- `delete:` a task scaffolds for a phase this plan (or this task) does not deliver: placeholder modules, "extension points", empty hooks, stubs for later. A whole task outside the plan's goal is scope drift, not this.
- `shrink:` fenced code — transcribed into the repository verbatim — does in many lines what a shorter form does in few. Test fences are exempt from `shrink` only; arrange/act/assert is verbose by nature.

Never flag: validation at trust boundaries, error handling that prevents data loss, security measures, accessibility basics, tests an acceptance criterion calls for, normative test bodies, or anything the plan's or the task's Goal explicitly asks for. A structure the task's `Context:` justifies — a second implementation named in a later task, a dependency chosen for edge cases the standard library mishandles, a decision quoted from Project knowledge — is deliberate and draws no finding.

Emit at most ONE `over_building` finding per task, in that task's findings (for a single task spec, one finding in all). `evidence` lists every instance on its own line as `<tag>: <what>. <replacement>.`; `suggestion` is the rewritten acceptance criterion or step, or the `Context:` line to add when the structure is deliberate.

This finding is minor because that is the severity it is worth, not because it is discretionary: an instruction elsewhere in this prompt to surface only the most-severe findings or to omit minor ones does not reach it. Emit it in addition to any such cap.

{{end}}{{define "plan_lean_cross_task"}}### Over-building across tasks

Over-building that only shows when tasks are read together and belongs to no single task — three tasks each writing their own config loader, a helper re-implemented in two tasks — is ONE plan-level finding: `category: quality`, `criterion: over_building`, `severity: minor`, with `evidence` naming the tasks and each instance as `<tag>: <what>. <replacement>.` using the tags `reuse`, `stdlib`, `native`, `yagni`, `delete`, `shrink`. A structure one task introduces — an interface no other task implements, say — is that task's `yagni:` instance and is never repeated here; over-building inside a single task is judged per task, not here. A structure a task's `Context:` justifies draws no finding. This finding is exempt from any cap on minor findings elsewhere in this prompt.

{{end}}
```

- [ ] **Step 4: Include it**

`pre.tmpl`: insert `{{template "plan_lean_rules" .}}` on its own line directly before the line `Set \`same_as\` to null on every finding.`

`plan.tmpl`: insert `{{template "plan_lean_rules" .}}{{template "plan_lean_cross_task" .}}` directly before `{{template "plan_comment_rules" . -}}`.

`plan_tasks_chunk.tmpl`: insert `{{template "plan_lean_rules" .}}` directly before `## Output`.

`plan_findings_only.tmpl`: insert `{{template "plan_lean_cross_task" .}}` directly before `{{template "plan_comment_rules" . -}}`.

- [ ] **Step 5: Regenerate goldens, read the diff, run the package**

```bash
go test ./internal/prompts/... -update
git diff --stat internal/prompts/testdata/
git diff internal/prompts/testdata/pre_basic.golden | head -60
go test -race ./internal/prompts/...
```

Expected: only `pre_*`, `plan_*`, `plan_tasks_chunk_*`, `plan_findings_only_*` goldens change, each by the added section; the package passes.

- [ ] **Step 6: Commit**

```bash
git add internal/prompts/templates/plan_lean_rules.tmpl internal/prompts/templates/pre.tmpl internal/prompts/templates/plan.tmpl internal/prompts/templates/plan_tasks_chunk.tmpl internal/prompts/templates/plan_findings_only.tmpl internal/prompts/prompts_test.go internal/prompts/testdata/
git commit -m "feat(prompts): flag plan-level over-building in validate_plan and validate_task_spec"
```

```json:metadata
{"files": ["internal/prompts/templates/plan_lean_rules.tmpl", "internal/prompts/templates/pre.tmpl", "internal/prompts/templates/plan.tmpl", "internal/prompts/templates/plan_tasks_chunk.tmpl", "internal/prompts/templates/plan_findings_only.tmpl", "internal/prompts/prompts_test.go", "internal/prompts/testdata/"], "verifyCommand": "go test -race ./internal/prompts/...", "acceptanceCriteria": ["pre, plan and chunk prompts carry ### Over-building with the six tags", "plan_findings_only carries only the cross-task section; chunk carries no plan-level emission", "chunked templates' UserPrefix unchanged", "goldens regenerated and reviewed"], "modelTier": "standard"}
```

---

### Task 10: `authoring.md` §3.10 and the section guard

**Goal:** Plan authors read what `over_building` flags and how `Context:` pre-empts it; the CI section guard distinguishes `### 3.1` from `### 3.10`; the bundle is resynced.

**Files:**
- Modify: `docs/protocol/authoring.md` (insert after §3.9, before `### Write-time comment guard`)
- Modify: `scripts/check-protocol-docs.sh` (the `sections` array)
- Modify: `plugin/anti-tangent-protocol/protocol/authoring.md` (resync)

**Acceptance Criteria:**
- [ ] `docs/protocol/authoring.md` contains `### 3.10 Lean by default` and is < 16,000 bytes.
- [ ] `bash scripts/check-protocol-docs.sh` prints `✓ protocol docs OK` — with `'### 3\.1 '` (trailing space) and `'### 3\.10'` both in the array.
- [ ] `diff -r docs/protocol plugin/anti-tangent-protocol/protocol` is empty.

**Verify:** `bash scripts/check-protocol-docs.sh && wc -c docs/protocol/authoring.md && diff -r docs/protocol plugin/anti-tangent-protocol/protocol && echo OK` → `✓ protocol docs OK`, a byte count under 16000, `OK`

**Steps:**

- [ ] **Step 1: Tighten the section guard first, and watch it fail on today's docs**

In `scripts/check-protocol-docs.sh` change `'### 3\.1'` to `'### 3\.1 '` and add `'### 3\.10'` after `'### 3\.9'`. Run `bash scripts/check-protocol-docs.sh` — expected: `section '### 3\.10' appears 0 times` (red, because §3.10 does not exist yet).

- [ ] **Step 2: Add §3.10**

Insert into `docs/protocol/authoring.md`, immediately before `### Write-time comment guard (if \`anti-tangent-guard\` is installed)`:

```markdown
### 3.10 Lean by default

`validate_plan` flags a plan that mandates over-building, as one `quality` / `over_building`
finding per task, always `minor`, each instance tagged:

- `reuse:` a task re-writes a helper an attached file or Project knowledge already provides.
- `stdlib:` a dependency, or a hand-written utility, for what the standard library covers.
- `native:` application code for what the platform provides (a DB constraint, `<input type="date">`, CSS).
- `yagni:` an interface with one implementation, a factory for one product, a config value no
  task varies, a layer with one caller — judged across the whole plan.
- `delete:` scaffolding for a phase this plan does not deliver.
- `shrink:` fenced code a shorter form replaces; test fences are exempt.

**Justify deliberate structure in `Context:`, not in the dispatch conversation.** The same check
runs at task start (`validate_task_spec`) and on the built code (`validate_completion`), and only
`Context:` reaches them: "Task 9 adds the S3 backend", "chosen for the TZ edge cases the stdlib
mishandles". A controller ruling waives the finding at plan level only.

**Attach what a task might duplicate.** A helper the reviewer can see in `context_paths` is a
`reuse` finding; one it cannot see is silence, not approval.

An acceptance criterion that names an interface is an implementation step in disguise (§3.5):
state the outcome and let the implementer pick the leanest structure that delivers it.

```

- [ ] **Step 3: Resync and verify**

```bash
rm -f plugin/anti-tangent-protocol/protocol/*.md && cp docs/protocol/*.md plugin/anti-tangent-protocol/protocol/
bash scripts/check-protocol-docs.sh
wc -c docs/protocol/authoring.md
diff -r docs/protocol plugin/anti-tangent-protocol/protocol && echo OK
```

Expected: `✓ protocol docs OK`; roughly 10,900 bytes; `OK`.

- [ ] **Step 4: Commit**

```bash
git add docs/protocol/authoring.md plugin/anti-tangent-protocol/protocol/authoring.md scripts/check-protocol-docs.sh
git commit -m "docs(protocol): authoring §3.10 — lean by default, and what Context: pre-empts"
```

```json:metadata
{"files": ["docs/protocol/authoring.md", "plugin/anti-tangent-protocol/protocol/authoring.md", "scripts/check-protocol-docs.sh"], "verifyCommand": "bash scripts/check-protocol-docs.sh && wc -c docs/protocol/authoring.md && diff -r docs/protocol plugin/anti-tangent-protocol/protocol && echo OK", "acceptanceCriteria": ["authoring.md has ### 3.10 Lean by default and stays under 16000 bytes", "check-protocol-docs.sh distinguishes 3.1 from 3.10 and passes", "bundle in sync"], "modelTier": "mechanical"}
```

---

### Task 11: The reviewer check in `post.tmpl` and `mid.tmpl`, and the stats sentinel

**Goal:** `validate_completion` judges the diff against `lean.tmpl` with all six tags; `check_progress` with the five structural ones; `over_building` is counted by the stats ledger.

**Files:**
- Modify: `internal/prompts/templates/post.tmpl` (after `### Stale comments`, before `Every finding carries \`same_as\``), `internal/prompts/templates/mid.tmpl` (after `DO NOT critique code style…`, before `## Working on`)
- Modify (via `-update`): `internal/prompts/testdata/{mid_*,post_*}.golden`
- Modify: `internal/stats/event.go` (`countedCriteria`), `internal/stats/event_test.go`
- Test: `internal/prompts/prompts_test.go`

**Acceptance Criteria:**
- [ ] The rendered `post` prompt contains `### Over-building`, the full `lean.tmpl` text, all six tags, `net: -N lines`, and the "only when a diff is present" rule.
- [ ] The rendered `mid` prompt contains `### Over-building`, the full `lean.tmpl` text, the five structural tags, and states that `shrink:` is not judged mid-task.
- [ ] `stats.CountFindings` counts a finding with `Criterion: "over_building"` (and `" Over_Building "`) under `over_building`.
- [ ] Goldens regenerated and reviewed.

**Verify:** `go test -race ./internal/prompts/... ./internal/stats/...` → ok

**Steps:**

- [ ] **Step 1: Write the failing tests**

Append to `internal/prompts/prompts_test.go`:

```go
func TestOverBuildingSectionsIncludeTheOneRuleset(t *testing.T) {
	lean, err := LeanGuidance()
	require.NoError(t, err)

	post, err := RenderPost(PostInput{Spec: sampleSpec(), Summary: "s", FinalDiff: "--- a\n+++ b\n"})
	require.NoError(t, err)
	assert.Contains(t, post.User, "### Over-building")
	assert.Contains(t, post.User, lean, "post.tmpl must include lean.tmpl verbatim")
	for _, tag := range []string{"`reuse:`", "`stdlib:`", "`native:`", "`yagni:`", "`delete:`", "`shrink:`"} {
		assert.Contains(t, post.User, tag)
	}
	assert.Contains(t, post.User, "net: -N lines")
	assert.Contains(t, post.User, "ONLY when a diff is present")

	mid, err := RenderMid(MidInput{Spec: sampleSpec(), WorkingOn: "w", Files: []File{{Path: "a.go", Content: "package a\n"}}})
	require.NoError(t, err)
	assert.Contains(t, mid.User, "### Over-building")
	assert.Contains(t, mid.User, lean, "mid.tmpl must include lean.tmpl verbatim")
	assert.Contains(t, mid.User, "Not `shrink:`")
	// The backticked tag words appear only in the over-building sections, so
	// these assertions are scoped to them without slicing the prompt.
	for _, tag := range []string{"`reuse:`", "`stdlib:`", "`native:`", "`yagni:`", "`delete:`"} {
		assert.Contains(t, mid.User, tag)
	}
}
```

Append to `internal/stats/event_test.go`:

```go
func TestCountFindings_OverBuildingIsCounted(t *testing.T) {
	findings := []verdict.Finding{
		{Severity: verdict.SeverityMinor, Category: verdict.CategoryQuality,
			Criterion: "over_building", Evidence: "e", Suggestion: "s"},
		{Severity: verdict.SeverityMinor, Category: verdict.CategoryQuality,
			Criterion: " Over_Building ", Evidence: "e", Suggestion: "s"},
	}
	_, _, crit, total := CountFindings(findings)
	require.Equal(t, 2, total)
	assert.Equal(t, 2, crit["over_building"])
}
```

- [ ] **Step 2: Run to see them fail**

Run: `go test ./internal/prompts/ -run OverBuilding; go test ./internal/stats/ -run OverBuilding`
Expected: both FAIL.

- [ ] **Step 3: post.tmpl**

Insert after the `### Stale comments` block's last paragraph (the one beginning "Report every stale comment in ONE finding") and before `Every finding carries \`same_as\``:

```markdown
### Over-building

The implementer was handed this ruleset when the task started:

{{template "lean.tmpl" .}}

Check the change against it. Judge ONLY what this change adds, and ONLY when a diff is present: with `final_files` and no diff you cannot tell a new interface from one that was already there, and the summary is not evidence — emit no over-building finding at all. Tag each instance:

- `reuse:` the diff adds a helper that a submitted file already has, or that the task spec's `Context:` or pinned-by entries name. Only from the evidence in front of you: you never see the rest of the codebase, and absence there is silence, not approval.
- `stdlib:` a hand-rolled utility, or a new dependency, for what the language's standard library covers. Language from the paths; general knowledge, not a codebase claim.
- `native:` application code for what the platform provides — a uniqueness check in a service instead of a database constraint, a date-picker library over `<input type="date">`, JavaScript over CSS.
- `yagni:` an interface with one implementation, a factory for one product, a config value nothing varies, a layer with one caller — within the evidence.
- `delete:` scaffolding for later: placeholder modules, unused flexibility, empty hooks, retry around an idempotent local call.
- `shrink:` the same logic in fewer lines; show the shorter form. Test code is exempt from `shrink`; the other tags apply to it — a mocking dependency for what a stub does is still `native`.

Never flag: validation at trust boundaries, error handling that prevents data loss, security measures, accessibility basics, tests the task calls for, or anything explicitly requested. Anything the task spec's `Context:` justifies is deliberate and draws no finding.

A structure an acceptance criterion itself mandated is still reported: say in `evidence` which criterion mandated it, address `suggestion` to the plan author ("drop the interface from the AC, or justify it in `Context:`"), and set `same_as` to the pre-task `over_building` finding when there is one. The implementer is not expected to act on it; the summary block carries it to the controller.

Report every instance in ONE finding: `category: quality`, `criterion: over_building`, `severity: minor`. `evidence` lists each instance as `path:line: <tag>: <what>. <replacement>.` — line numbers from the diff's hunk headers, never invented — and ends with `net: -N lines`, your estimate of what the replacements remove. `suggestion` gives the replacement code or the removal. If there is nothing to cut, emit no over-building finding.

```

- [ ] **Step 4: mid.tmpl**

Insert after the line `DO NOT critique code style or polish at this stage. Style is noise mid-task.` and before `## Working on`:

```markdown

### Over-building

The implementer was handed this ruleset when the task started:

{{template "lean.tmpl" .}}

The files below are the ones the implementer reports changing. Flag structure in them that the acceptance criteria do not call for and `Context:` does not justify — `reuse:` (a helper a submitted file already has, or the task spec names), `stdlib:` (a hand-rolled utility or new dependency for what the standard library covers), `native:` (application code for what the platform provides), `yagni:` (an interface with one implementation, a factory for one product, a config value nothing varies, a layer with one caller), `delete:` (scaffolding for later). Not `shrink:` — line-count polish is the style noise above; it is judged at completion. Never flag trust-boundary validation, data-loss handling, security, accessibility, tests the task calls for, or anything explicitly requested. Report every instance in ONE finding, `category: quality`, `criterion: over_building`, `severity: minor`, `evidence` listing each as `path: <tag>: <what>. <replacement>.`, `suggestion` giving the replacement or the removal. An unnecessary abstraction is cheapest to remove now, before tests and callers accrete on it.
```

- [ ] **Step 5: Stats sentinel**

In `internal/stats/event.go` add `"over_building": true,` to `countedCriteria`, after `"comment_hygiene": true,`.

- [ ] **Step 6: Regenerate goldens, read the diff, run**

```bash
go test ./internal/prompts/... -update
git diff --stat internal/prompts/testdata/
go test -race ./internal/prompts/... ./internal/stats/...
```

Expected: only `mid_*` and `post_*` goldens change; both packages pass.

- [ ] **Step 7: Commit**

```bash
git add internal/prompts/templates/post.tmpl internal/prompts/templates/mid.tmpl internal/prompts/prompts_test.go internal/prompts/testdata/ internal/stats/event.go internal/stats/event_test.go
git commit -m "feat(prompts): review the diff against the lean ruleset as quality/over_building"
```

```json:metadata
{"files": ["internal/prompts/templates/post.tmpl", "internal/prompts/templates/mid.tmpl", "internal/prompts/prompts_test.go", "internal/prompts/testdata/", "internal/stats/event.go", "internal/stats/event_test.go"], "verifyCommand": "go test -race ./internal/prompts/... ./internal/stats/...", "acceptanceCriteria": ["post prompt includes lean.tmpl verbatim, six tags, net: -N lines, diff-only rule", "mid prompt includes lean.tmpl verbatim, five tags, shrink excluded", "stats counts over_building", "goldens regenerated and reviewed"], "modelTier": "standard"}
```

---

### Task 12: Replay gate — two synthetic fixtures, one paid run

**Goal:** Evidence that the completion-time check names planted over-building and stays quiet on a lean diff of the same job, before the release.

**USER-ORDERED GATE — NON-SKIPPABLE.** This task was requested by the user in the current conversation. It MUST NOT be closed by walking around it, by declaring it "verified inline", or by substituting a cheaper check. Close only after every item in `acceptanceCriteria` has been re-validated independently, with output captured.

**Files:**
- Create: `internal/mcpsrv/testdata/replay/lean/over-built.json`, `internal/mcpsrv/testdata/replay/lean/lean.json`

**Acceptance Criteria:**
- [ ] `over-built.json`: in ≥ 3 of 5 runs, the run's `over_building` finding matches at least two of the three expectation groups — co-occurrence within one run, not two independent tallies each reaching 3. Computed from the report's per-run `matches` lines by the Step 6 `jq`, and read by eye as well.
- [ ] `lean.json` draws no `over_building` finding in ≥ 4 of 5 runs.
- [ ] The report records no `critical` or `major` finding in any run of either fixture: every call's `blocking` map is empty (Step 6's third `jq` prints `0`). The fixtures are synthetic and have no baseline, so any blocking finding counts.
- [ ] The user approved the spend before the run (5 runs × 2 fixtures × one `validate_task_spec` + one `validate_completion` at the configured models), and the run's report is pasted into the DONE report.

**Verify:** read the report Step 5 saved — no provider call, so verification never repeats the paid run: the three `jq` gates in Step 6 over `"${TMPDIR:-/tmp}/replay-lean-report.json"` print `≥ 3`, `≤ 1` and `0`. Step 5's command carries `-timeout 4h`, which is not optional: `go test` defaults to 10 minutes, which kills a 20-call paid run part-way; the harness itself allows 15 minutes per run per fixture.

**Steps:**

- [ ] **Step 1: Write the over-built fixture**

`internal/mcpsrv/testdata/replay/lean/over-built.json` — three plants: an interface with one implementation, a hand-rolled `contains`, a config struct for one value:

```json
{
  "name": "over-built",
  "validate_task_spec": {
    "task_title": "Task 2: Persist the last-seen cursor",
    "goal": "The poller resumes from the cursor it last saw after a restart.",
    "acceptance_criteria": [
      "After SaveCursor(\"abc\") and a restart, LoadCursor() returns \"abc\".",
      "LoadCursor() on a fresh install returns \"\" and no error.",
      "IsKnownTopic(\"orders\") is true for the configured topics and false otherwise."
    ],
    "non_goals": ["No remote storage."],
    "context": "The cursor lives in a single file under the data directory. Topics are the fixed list in topics.go."
  },
  "validate_completion": {
    "summary": "Added a CursorStore interface with a FileCursorStore implementation, a CursorConfig struct, and an IsKnownTopic helper.",
    "final_diff": "diff --git a/internal/poll/cursor.go b/internal/poll/cursor.go\nnew file mode 100644\n--- /dev/null\n+++ b/internal/poll/cursor.go\n@@ -0,0 +1,58 @@\n+package poll\n+\n+import (\n+\t\"os\"\n+\t\"path/filepath\"\n+)\n+\n+// CursorStore persists the poller's last-seen cursor.\n+type CursorStore interface {\n+\tSaveCursor(cursor string) error\n+\tLoadCursor() (string, error)\n+}\n+\n+// CursorConfig holds the cursor storage settings.\n+type CursorConfig struct {\n+\tFileName string\n+}\n+\n+// DefaultCursorConfig returns the default configuration.\n+func DefaultCursorConfig() CursorConfig {\n+\treturn CursorConfig{FileName: \"cursor\"}\n+}\n+\n+// FileCursorStore stores the cursor in a file under dir.\n+type FileCursorStore struct {\n+\tdir string\n+\tcfg CursorConfig\n+}\n+\n+// NewFileCursorStore builds a FileCursorStore.\n+func NewFileCursorStore(dir string, cfg CursorConfig) *FileCursorStore {\n+\treturn &FileCursorStore{dir: dir, cfg: cfg}\n+}\n+\n+func (s *FileCursorStore) path() string { return filepath.Join(s.dir, s.cfg.FileName) }\n+\n+func (s *FileCursorStore) SaveCursor(cursor string) error {\n+\treturn os.WriteFile(s.path(), []byte(cursor), 0o600)\n+}\n+\n+func (s *FileCursorStore) LoadCursor() (string, error) {\n+\tb, err := os.ReadFile(s.path())\n+\tif os.IsNotExist(err) {\n+\t\treturn \"\", nil\n+\t}\n+\treturn string(b), err\n+}\n+\n+// IsKnownTopic reports whether topic is configured.\n+func IsKnownTopic(topic string) bool {\n+\tfor i := 0; i < len(Topics); i++ {\n+\t\tif Topics[i] == topic {\n+\t\t\treturn true\n+\t\t}\n+\t}\n+\treturn false\n+}\n",
    "test_evidence": "ok  \tinternal/poll\t0.012s"
  },
  "expectations": [
    {"call": "validate_completion", "any_of_keywords": ["CursorStore", "interface", "yagni"]},
    {"call": "validate_completion", "any_of_keywords": ["slices.Contains", "IsKnownTopic", "stdlib"]},
    {"call": "validate_completion", "any_of_keywords": ["CursorConfig", "config", "FileName"]}
  ]
}
```

- [ ] **Step 2: Write the lean fixture**

`internal/mcpsrv/testdata/replay/lean/lean.json` — same task, same ACs, lean diff:

```json
{
  "name": "lean",
  "validate_task_spec": {
    "task_title": "Task 2: Persist the last-seen cursor",
    "goal": "The poller resumes from the cursor it last saw after a restart.",
    "acceptance_criteria": [
      "After SaveCursor(\"abc\") and a restart, LoadCursor() returns \"abc\".",
      "LoadCursor() on a fresh install returns \"\" and no error.",
      "IsKnownTopic(\"orders\") is true for the configured topics and false otherwise."
    ],
    "non_goals": ["No remote storage."],
    "context": "The cursor lives in a single file under the data directory. Topics are the fixed list in topics.go."
  },
  "validate_completion": {
    "summary": "Two functions over a fixed file name, and slices.Contains for the topic check.",
    "final_diff": "diff --git a/internal/poll/cursor.go b/internal/poll/cursor.go\nnew file mode 100644\n--- /dev/null\n+++ b/internal/poll/cursor.go\n@@ -0,0 +1,26 @@\n+package poll\n+\n+import (\n+\t\"os\"\n+\t\"path/filepath\"\n+\t\"slices\"\n+)\n+\n+const cursorFile = \"cursor\"\n+\n+func SaveCursor(dir, cursor string) error {\n+\treturn os.WriteFile(filepath.Join(dir, cursorFile), []byte(cursor), 0o600)\n+}\n+\n+// LoadCursor returns \"\" on a fresh install: a missing file is not an error.\n+func LoadCursor(dir string) (string, error) {\n+\tb, err := os.ReadFile(filepath.Join(dir, cursorFile))\n+\tif os.IsNotExist(err) {\n+\t\treturn \"\", nil\n+\t}\n+\treturn string(b), err\n+}\n+\n+func IsKnownTopic(topic string) bool {\n+\treturn slices.Contains(Topics, topic)\n+}\n",
    "test_evidence": "ok  \tinternal/poll\t0.011s"
  },
  "expectations": [
    {"call": "validate_completion", "any_of_keywords": ["over_building"]}
  ]
}
```

The harness has no negative expectation: an expectation only counts the runs whose findings match it (`replayTally.Matched`, printed as `validate_completion ["over_building"]: N/5`). For `lean.json` that count is the false-positive count, and the gate reads it as N ≤ 1 — the fixture uses the same shape as a positive one, and only Step 6's threshold differs.

- [ ] **Step 3: Dry-run the harness on both fixtures**

```bash
ANTI_TANGENT_REPLAY_DIR=$PWD/internal/mcpsrv/testdata/replay/lean ANTI_TANGENT_REPLAY_DRY_RUN=1 \
  go test -tags=e2e -count=1 ./internal/mcpsrv/ -run TestReplay_E2E -v 2>&1 | tail -30
```

Expected: both fixtures load and validate (no "expectations[…] has no any_of_keywords"); no provider call is made.

- [ ] **Step 4: Estimate spend and ask the user**

Two calls per run, five runs, two fixtures = 20 reviewer calls at the configured `ANTI_TANGENT_PRE_MODEL` / `ANTI_TANGENT_POST_MODEL`. Report the models and a clearly labelled rough upper bound in USD: per call, (the dry run's largest `prompt_bytes` for that call ÷ 4) input tokens plus the configured maximum output tokens, priced at the provider's published per-million-token list price for that model; × 5 runs × 2 fixtures. Run only after the user says go.

- [ ] **Step 5: Run and read the report**

```bash
LOG="${TMPDIR:-/tmp}/replay-lean.log"
OUT="${TMPDIR:-/tmp}/replay-lean-report.json"
ANTI_TANGENT_REPLAY_DIR=$PWD/internal/mcpsrv/testdata/replay/lean ANTI_TANGENT_REPLAY_RUNS=5 ANTI_TANGENT_REPLAY_OUT="$OUT" \
  go test -tags=e2e -count=1 -timeout 4h ./internal/mcpsrv/ -run TestReplay_E2E -v 2>&1 | tee "$LOG" | tail -80
echo "log: $LOG  report: $OUT"
```

Read `matches` per run for `over-built`: the quoted finding text must be an `over_building` finding naming the plant, not another finding that happens to contain the keyword. Record, per fixture: runs, expectation counts, and every `blocking` entry (`<severity> <category> "<criterion>"` in N/5).

- [ ] **Step 6: Decide**

The harness tallies each expectation independently, but every `matches` line starts `run N: <category> <criterion>: `, so per-run co-occurrence is derivable from the report:

```bash
# Runs in which at least two plant groups were named by an over_building finding (need >= 3).
jq -r '.[] | select(.fixture == "over-built") | [.expectations[] | [.matches[]? | select(test(": quality over_building: "; "i")) | capture("^run (?<r>[0-9]+):").r] | unique] | flatten | group_by(.) | map(select(length >= 2)) | length' "$OUT"
# Runs in which the lean fixture drew an over_building finding (need <= 1).
jq -r '.[] | select(.fixture == "lean") | [.expectations[].matches[]? | select(test(": quality over_building: "; "i")) | capture("^run (?<r>[0-9]+):").r] | unique | length' "$OUT"
```

and the severity gate:

```bash
# Critical or major findings recorded in any run of either fixture (need 0).
jq '[.[] | .calls[] | (.blocking // {}) | length] | add // 0' "$OUT"
```

If the first prints ≥ 3, the second ≤ 1 and the third 0: commit the fixtures. If not: report the `matches` text and stop — the prompt wording in Task 11 is what changes, and that is a new task, not a silent retry.

```bash
git add internal/mcpsrv/testdata/replay/lean/
git commit -m "test(replay): over-built and lean fixtures for the over_building check"
```

```json:metadata
{"files": ["internal/mcpsrv/testdata/replay/lean/over-built.json", "internal/mcpsrv/testdata/replay/lean/lean.json"], "verifyCommand": "OUT=\"${TMPDIR:-/tmp}/replay-lean-report.json\"; jq -r '.[] | select(.fixture == \"over-built\") | [.expectations[] | [.matches[]? | select(test(\": quality over_building: \"; \"i\")) | capture(\"^run (?<r>[0-9]+):\").r] | unique] | flatten | group_by(.) | map(select(length >= 2)) | length' \"$OUT\"; jq -r '.[] | select(.fixture == \"lean\") | [.expectations[].matches[]? | select(test(\": quality over_building: \"; \"i\")) | capture(\"^run (?<r>[0-9]+):\").r] | unique | length' \"$OUT\"; jq '[.[] | .calls[] | (.blocking // {}) | length] | add // 0' \"$OUT\"", "acceptanceCriteria": ["over-built: over_building finding matching >=2 of 3 groups in >=3 of 5 runs, verified from matches text", "lean: no over_building in >=4 of 5 runs", "no new critical or major in any run", "spend approved by the user before the run; report pasted into DONE"], "modelTier": "standard", "userGate": true, "tags": ["user-gate"], "requireEvidenceTokens": [["over-built"], ["lean"]]}
```

---

### Task 13: Checkpoint B — finish the CHANGELOG and merge the release

**Goal:** `## [0.23.0]` describes the whole release; the second PR from `version/0.23.0` merges with `[minor]`; the release workflow ships server 0.23.0 and guard 0.5.0.

**USER-ORDERED GATE — NON-SKIPPABLE.** This task was requested by the user in the current conversation. It MUST NOT be closed by walking around it, by declaring it "verified inline", or by substituting a cheaper check. Close only after every item in `acceptanceCriteria` has been re-validated independently, with output captured.

**Files:**
- Modify: `CHANGELOG.md` (extend the `## [0.23.0]` block), `.claude-plugin/marketplace.json` (top-level `version` 0.10.0 → 0.11.0)

**Acceptance Criteria:**
- [ ] The `## [0.23.0]` block lists, under `### Added`: `implementation_guidance`; the `over_building` criterion at plan, task-start, mid-task and completion; `authoring.md` §3.10; `lightweight` / `mode: lightweight`; the ponytail attribution; the replay fixtures.
- [ ] `go build ./... && go test -race ./... && bash plugin/anti-tangent-guard/evals/run.sh && bash scripts/check-protocol-docs.sh` all green locally; CI green on the branch.
- [ ] `git log --oneline origin/main..HEAD` lists only Tasks 6–13's commits — none of the guard commits Checkpoint A already merged — before the PR is opened.
- [ ] `VERSION` still reads `0.22.0` on the branch before the merge (`cat VERSION`); the release workflow's own commit bumps it (Global Constraints).
- [ ] The PR from `version/0.23.0` to `main` is merged by the user with `[minor]` in the merge commit; the release workflow publishes `v0.23.0`, and `main`'s `VERSION` then reads `0.23.0`.

**Verify:** `gh pr checks <PR#> --json name,state -q '.[] | .name + " " + .state'` → every line ends `SUCCESS`; `gh pr view <PR#> --json state,mergeCommit -q '.state + " " + .mergeCommit.oid'` → `MERGED <sha>`; `git fetch origin main && git log -1 --format=%B <sha> | grep -F '[minor]'` → the line carrying it; `gh release view v0.23.0 --json tagName -q .tagName` → `v0.23.0`

**Steps:**

- [ ] **Step 1: Extend the CHANGELOG block**

Under `## [0.23.0]` → `### Added`, above the guard bullet, add:

```markdown
- `validate_task_spec` returns `implementation_guidance`: a lean build ruleset adapted from
  DietrichGebert's MIT-licensed ponytail (attributed in `THIRD_PARTY_NOTICES.md`). It reaches
  implementers on every MCP host, once per task, and is the same text the reviewer holds the
  diff to.
- A new `quality` / `over_building` criterion, always `minor` and rolled up — one finding per call, or per task plus one cross-task finding in `validate_plan`:
  `validate_plan` and `validate_task_spec` flag plan text that mandates over-building (an
  interface with one implementation, a dependency for what the stdlib does, scaffolding for a
  later phase), `check_progress` flags structure the acceptance criteria do not call for, and
  `validate_completion` judges the diff's added lines against the ruleset, tagging each instance
  `reuse` / `stdlib` / `native` / `yagni` / `delete` / `shrink`. A structure the task's
  `Context:` justifies draws no finding at any of the four. `authoring.md` §3.10 tells plan
  authors what is flagged and how one `Context:` line pre-empts it.
- An empty-session `validate_completion` sets `lightweight: true` on the envelope and prints
  `mode: lightweight` in its `summary_block`, so a DONE report shows when a review had no
  acceptance criteria to check.
- Synthetic replay fixtures under `internal/mcpsrv/testdata/replay/lean/` for the
  `over_building` check.
```

Bump `.claude-plugin/marketplace.json`'s top-level `"version"` to `"0.11.0"`.

- [ ] **Step 2: Full local verification**

```bash
go build ./... && go test -race ./... && bash plugin/anti-tangent-guard/evals/run.sh && bash scripts/check-protocol-docs.sh && wc -c docs/protocol/*.md
git add CHANGELOG.md .claude-plugin/marketplace.json
git commit -m "docs: changelog for 0.23.0"
git push origin HEAD:refs/heads/version/0.23.0
```

- [ ] **Step 3: Open the PR and hand to the user**

```bash
test "$(cat VERSION)" = 0.22.0 && echo "VERSION 0.22.0 before merge"
git fetch origin main && git log --oneline origin/main..HEAD
gh pr create --base main --head version/0.23.0 \
  --title "v0.23.0: lean by default — over_building at plan, task and review level [minor]" \
  --body-file - <<'EOF'
Part 2 of docs/superpowers/specs/2026-09-18-lean-by-default-design.md.

- validate_task_spec returns implementation_guidance (lean.tmpl, adapted from ponytail, MIT).
- quality/over_building at validate_plan, validate_task_spec, check_progress, validate_completion; always minor, rolled up (one per call; per task plus one cross-task in validate_plan).
- authoring.md §3.10.
- lightweight: true + `mode: lightweight` on empty-session completions.
- Replay fixtures and the gate result are in the plan's Task 12 report.

Merge with [minor].

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
```

Then report the PR link, CI state, and the replay result to the user; the merge is theirs.

- [ ] **Step 4: Confirm the release**

```bash
gh pr checks <PR#> --json name,state -q '.[] | .name + " " + .state'
SHA=$(gh pr view <PR#> --json state,mergeCommit -q '.mergeCommit.oid')
gh pr view <PR#> --json state -q .state
git fetch origin main && git log -1 --format=%B "$SHA" | grep -F '[minor]'
gh release view v0.23.0 --json tagName,publishedAt -q '.tagName + " " + .publishedAt'
git fetch origin main && echo "main VERSION $(git show origin/main:VERSION)"
```

Expected: every check `SUCCESS`, `MERGED`, the merge message line carrying `[minor]`, `v0.23.0 <timestamp>`, then `main VERSION 0.23.0` (the release bot's commit). With Step 3's `VERSION 0.22.0 before merge`, that is both `VERSION` states. Paste all of it into the DONE report.

```json:metadata
{"files": ["CHANGELOG.md", ".claude-plugin/marketplace.json"], "verifyCommand": "gh release view v0.23.0 --json tagName -q .tagName", "acceptanceCriteria": ["CHANGELOG 0.23.0 block covers every shipped change", "full local verification green and every PR check SUCCESS, shown by gh pr checks", "PR merged; the merge commit message carries [minor], shown by git log", "v0.23.0 released, shown by gh release view", "VERSION reads 0.22.0 on the branch before the merge and 0.23.0 on main after the release, both captured"], "modelTier": "standard", "userGate": true, "tags": ["user-gate"], "requireEvidenceTokens": [["SUCCESS"], ["MERGED"], ["[minor]"], ["v0.23.0"], ["VERSION 0.22.0 before merge"], ["main VERSION 0.23.0"]]}
```
