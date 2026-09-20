# anti-tangent-guard — a semantic second tier for comment hygiene

Status: approved design, not yet implemented. Target release 0.24.0.

## Problem

`comment_scan.py`'s tells catch change history that carries an extractable signal: a task id, an
issue or pull-request reference, a version governed by a change verb, a configured tracker key.
Change history written as ordinary prose carries none of those, and four rounds of narrowing the
version tell (v0.19.0 design, Part 3) ended by declaring that class permanently reviewer-led: the
ambiguity is in what the surrounding sentence means, not in what sits next to a token.

Reviewer-led means caught at task close, by `validate_completion`, at a 27-second median — or not
at all, for a comment written on a path the reviewer never sees. The evidence that it is not
caught: this repository carries at least 37 prose-history comment blocks at HEAD, and the local
stats ledger recorded 41 `comment_hygiene` findings across 156 `validate_completion` calls in the
three days to 2026-09-19.

## Evidence

A labeled set of 165 comment blocks (83 history, 79 not, 3 ambiguous) drawn from the guard's own
eval cases, `fp-class.tsv`, comments at HEAD, random comments at HEAD, and before/after pairs from
commits that removed history framing. Measured 2026-09-19 against `jev-1.13.0`, with the questions
frozen before the first request:

| Comment group | Total | Regex | Jev ≥ 0.7 | Either |
|---|---|---|---|---|
| History written as prose | 43 | 2 | 36 | 36 |
| History citing an issue, task or version | 40 | 37 | 33 | 37 |
| Same wording, present behaviour | 13 | 0 | 0 | 0 |
| Ordinary comments, sampled | 42 | 0 | 1 | 1 |
| Written to trip the regex | 24 | 0 | 0 | 0 |

Together: 88% recall at 99% precision, against the regex's 47% recall. Full result:
[Jev vs the comment-history regex](https://claude.ai/code/artifact/68994bbc-918a-4f52-b64d-706336ed1e16).

Three findings shape this design. The question's wording decides the outcome — a plain yes/no
question made 12 false flags where the three-way choice made 1, and the policy's own phrasing
("reads correctly to someone who never saw the change") scores worst of five variants, because it
asks about a reader's knowledge rather than about the comment. Jev follows the criteria it is
given, so a policy decision belongs in the criteria, not in a threshold. And the one false flag
scored 1.00, so no threshold removes it: blocking accepts roughly one wrongly blocked comment in
79 clean ones.

## Decisions

1. Jev runs at write time only — the `PreToolUse` `Edit`/`Write` hook. The close-time hook and the
   MCP server are untouched.
2. A flagged comment **blocks**, like a regex hit.
3. Off unless `ANTI_TANGENT_JEV=1` and `TYPESAFE_API_KEY` is non-empty.
4. No path restriction: the setting is the control, and any repository edited while it is on sends
   its added comment text to TypeSafe.
5. A comment describing an earlier version's bug is change history, including in a test.

## Non-goals

- No close-time (`PostToolUse`) or MCP-server integration. The server stays advisory.
- No retry, no queue, no cache. A blocking hook waits once or gives up.
- No Jev call when the regex already blocks.
- No replacement of any tell. The regex tier keeps catching what it catches, including the three
  cases Jev missed (a bare parenthesised version, and two tracker keys).

## Design

### Gate and control flow

`check_comment_write.py` keeps its structure. After `violations()` returns:

- non-empty → exit 2 with today's message. No Jev call: the write is already refused, so a call
  would spend latency and egress on a decided outcome.
- empty, and all three of `ANTI_TANGENT_COMMENT_GUARD != 0`, `ANTI_TANGENT_JEV == "1"` and a
  non-empty `TYPESAFE_API_KEY` → the Jev tier.
- otherwise → exit 0, exactly as today.

New module `hooks/jev_scan.py`. `comment_scan.py` gains nothing and stays a pure function over
text, so its unit tests, `fp-scan.py` and the zero-false-positive gate keep working unchanged.

### What is judged

The unit is the comment block, not the line. From the post-edit text the hook already builds
(`context`), take each maximal run of consecutive comment lines, and keep the run when this edit
added any line in it. Neighbouring lines the edit did not touch are included: a history clause
often spans lines, "used to" can only be read in a whole sentence, and the block is the unit the
measurements above used. When `context` is None — an Edit whose `old_string` is not found on disk
— fall back to grouping the added lines alone.

Bounds: each block truncated to 2,000 characters, at most 20 blocks per write. A write that
exceeds either judges what fits and records `jev-capped`.

### Request

One request per block, sent concurrently on a small thread pool, one attempt each. One block per
request is what the measurements used, and TypeSafe's own guidance is that unrelated content in
the state costs accuracy. Most edits add one block, so this is usually one request.

```
POST $ANTI_TANGENT_JEV_URL
{"model": "jev-1.13.0", "state": {"comment": "<block>"}, "questions": {"kind": <the question file>}}
```

Flagged when `answers.kind.probabilities.change_history >= threshold`.

### The question file

`hooks/jev-question.json` holds the Choice question and the default threshold: options
`change_history`, `compatibility_contract` and `present_behaviour`, each with `what`, `not_for`
and `examples`, and a `focus` instructing that any history statement decides the answer even when
the rest of the comment explains present behaviour. Keeping it as data makes the wording
reviewable on its own and tunable against the calibration suite without touching the hook.

Its examples are synthetic. None is drawn from the calibration set, so the set stays an honest
measurement of the shipped wording.

### Verdict, message, exit codes

A flag exits with a new status the wrapper maps to 2, so the trace log distinguishes a Jev block
from a regex block. The message quotes the comment, states that it reads as change history, and
asks for a rewrite, in the shape of the regex tier's message. It does not name the off switch: the
README documents that for the operator, and the agent's recovery is to fix the comment.

### Failure

Every failure allows the write: connection error, timeout, non-200, malformed body, missing field,
unparsable threshold. Three-second timeout per request, one attempt, no retry — a retry in a
blocking hook doubles the wait, and 429 or 529 means back off rather than block an edit.
`hooks.json` gets an explicit 10-second timeout for this hook; Claude Code lets a timed-out
command hook proceed, so that fails open too.

### Trace

New events through the existing wrapper, which keeps one writer and one format for the log:
`jev-block` (with the probability), `jev-pass`, `jev-skip` (with the reason it was off),
`jev-error` (with the failure class) and `jev-capped`. The body reports them on stdout, which the
wrapper captures and scrubs; nothing reaches Claude Code's transcript.

### Configuration

| Variable | Default | Purpose |
|---|---|---|
| `ANTI_TANGENT_JEV` | unset | Must be exactly `1`. |
| `TYPESAFE_API_KEY` | unset | Required. |
| `ANTI_TANGENT_JEV_THRESHOLD` | `0.7` | Flag at or above. Malformed → default. |
| `ANTI_TANGENT_JEV_MODEL` | `jev-1.13.0` | Pinned; an alias moves under a tuned threshold. |
| `ANTI_TANGENT_JEV_URL` | `https://api.typesafe.ai/v1/systemone` | Stub endpoint for evals; proxy for operators. |

`ANTI_TANGENT_COMMENT_GUARD=0` continues to disable comment scanning entirely, both tiers.

## Testing

**Unit** (`jev_scan_test.py`, stubbed transport, no network): block building from added lines and
post-edit text, including the None-context fallback and both bounds; the three-part gate; threshold
parsing; response parsing; and every failure path allowing the write.

**Eval cases** (`guard-evals.json`, driving the real hook binary against a stub server through
`ANTI_TANGENT_JEV_URL`): setting off; key absent; a flag blocking with the probability in the
trace; a timeout allowing; and a regex hit short-circuiting before any call, asserted with the
sentinel-file trick the suite already uses for a command that must not run.

**Calibration** (`evals/jev-comments.jsonl` plus a runner, gated on the key and the setting, never
in CI): the labeled set, reporting recall and precision per group. This is what catches the
question wording drifting or a model change costing recall — the job `fp-class.tsv` does for the
regex. `.jsonl` is not a scannable extension, so the fixture's own history comments cannot reach
`fp-scan.py`.

## Risks

- **One wrongly blocked comment in about 79 clean ones**, unremovable by threshold. Accepted; the
  recovery is a rewrite or the off switch.
- **A literal-reading blind spot.** Three "X used to [verb]" history blocks scored near zero. The
  question was not tuned afterwards, since tuning on the measured set would inflate the numbers.
- **Egress.** Added comment text from every edited repository, while the setting is on.
- **Model drift.** `jev-1.13.0` is pinned, but the pin only defers the question; the calibration
  suite is what answers it at upgrade time.
- **Measurement limits.** 162 scored comments from one public Go repository, labeled by Claude and
  not yet reviewed by a second judge, one run. The rules were written knowing which kinds of
  comment the set holds, which flatters the result even though no wording changed after the run.

## Open at implementation time

- Confirmation of the calibration set's labels before it is committed.
- The exact block-message wording.

## Shipping

Rebase onto main (0.23.0), branch `version/0.24.0`, matching `CHANGELOG.md` entry, and a guard
README section covering both gates, what leaves the machine, fail-open, and the off switch.
