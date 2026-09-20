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

Full result:
[Jev vs the comment-history regex](https://claude.ai/code/artifact/68994bbc-918a-4f52-b64d-706336ed1e16).

**What the numbers do and do not support.** The false-flag rate that matters in the field is 1 in
42 randomly sampled ordinary comments — a 95% interval of roughly 0.4% to 12%, not the 1-in-79
that counting every negative gives, because 37 of those negatives are one-liners built to trip the
regex. At this repository's recent rate of comment-carrying writes that is on the order of one
wrong block a day, and the interval reaches several. Three further limits: the 99% precision
figure depends on Decision 5 below, since all six regression-test comments score 0.98 or above and
the other reading of that policy puts precision near 90%; the winning variant and threshold were
picked from five variants at three thresholds on 162 rows, so the headline flatters itself by some
unmeasured margin; and the prose-history positives were found by grepping for cue words, so 84% is
recall on cue-word history, not on history in general. Nothing here was measured on another
repository, another language, or the one-question-per-request shape the hook will actually send.

Three findings shape the design. The question's wording decides the outcome — a plain yes/no
question made 12 false flags where the three-way choice made 1, and the policy's own phrasing
("reads correctly to someone who never saw the change") scores worst of five variants, because it
asks about a reader's knowledge rather than about the comment. Jev follows the criteria it is
given, so a policy decision belongs in the criteria, not in a threshold. And the one false flag
scored 1.00, so no threshold removes it, and no cheap ensemble does either: AND-ing the choice
with either Noul variant at any threshold leaves it flagged.

## Decisions

1. Jev runs at write time only — the `PreToolUse` `Edit`/`Write` hook. The close-time hook and the
   MCP server are untouched.
2. A flagged comment **blocks**, like a regex hit.
3. Off unless `ANTI_TANGENT_JEV=1` and `TYPESAFE_API_KEY` is non-empty.
4. No path restriction: the setting is the control, and any repository edited while it is on sends
   its added comment text to TypeSafe.
5. A comment describing an earlier version's bug is change history, including in a test.
6. A touched comment block is judged **whole**, pre-existing lines included. History already in a
   block the edit touches is to be cleaned up, not stepped around.

## Non-goals

- No close-time (`PostToolUse`) or MCP-server integration. The server stays advisory.
- No retry and no result cache. A blocking hook waits once or gives up. (The error breaker below
  is not a result cache: it remembers that the service is down, never a verdict.)
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

New module `hooks/jev_scan.py`. The whole tier is wrapped so that no exception can escape: an
uncaught one would exit 1 with a traceback on the user's stderr and trace `python-exit=1` rather
than a Jev event, which would make "every failure allows the write" false in the one case nobody
anticipated.

`comment_scan.py` gains one pure helper (see "What is judged") and stays network-free, so its unit
tests, `fp-scan.py` and the zero-false-positive gate keep working unchanged.

### What is judged: the touched block, in full

The unit is the comment block. From the post-edit text the hook already builds (`context`), take
each maximal run of consecutive lines carrying a comment span, and keep the run when this edit
touched any line in it.

**This deliberately widens the regex tier's scoping, and the README must say so.** That tier judges
added lines alone, and the v0.19.0 design states the invariant behind it: an unchanged line
carrying an old violation must not block, "or the on-touch rule becomes a big-bang sweep by the
back door". The root CLAUDE.md requires the opposite for comments — "When you touch code whose
comments break these rules, remove or rewrite them as part of your task. There is no separate
cleanup pass." A touched block that still narrates history is therefore a defect this tier reports.
The sweep the invariant guards against is bounded here by the block, not the file: an edit is asked
to clean the comment it touches, never every comment in the file, and a file's other blocks are
never sent. The cost is real and accepted — an agent adding a line beside legacy history must
rewrite that block, and a wrong flag makes it rewrite text it did not author, which is why the
block message quotes the offending lines rather than naming the block as a whole.

The builder must match the scanner's own idea of a comment, or it will egress and judge text that
is not one:

- **Starred lines.** `comment_spans` returns a span for ` * text` unless told otherwise, so a Go
  raw string or a Kotlin `"""` block holding such a line looks like a comment. Only `violations()`
  consults `block_comment_lines` to decide. That decision moves into a pure helper in
  `comment_scan.py` that both callers use.
- **Hash-family strings.** A column-zero `#` inside a Python triple-quoted string is read as a
  comment by the same code, which would make this tier block its own test fixtures. The helper
  covers this case too; fixtures carrying history text live in the calibration `.jsonl` and are
  loaded from there rather than written inline.
- **Trailing comments.** A comment after code on the same line has a span, so a naive run would
  glue code lines together and ship them. Only the spans are sent, never the raw lines, so a
  trailing comment contributes its comment text and nothing else.
- **Fragment edits.** An Edit's added "line" can be a fragment (`foo() // this used to panic`),
  which equals no line of the post-edit text. Matching is by containment, and when nothing matches,
  the tier falls back to the added fragments alone — the same fallback the regex tier takes.

Bounds: a block is windowed to 2,000 characters **around the touched lines**, never from the top,
so a long licence or package header cannot push the touched text out of what is judged. At most 20
blocks per write, subject to the deadline below; a write that exceeds either judges what fits and
records `jev-capped`.

Before egress, every block passes a redaction step: high-entropy tokens and `key`/`secret`/`token`/
`password` assignments are replaced with a placeholder. Commented-out credentials are common, and
Decision 4 points this tier at every repository the operator edits.

### Request

One request per block, on a small thread pool, one attempt each, under a single wall-clock deadline
of 4 seconds for the whole tier. One block per request is what the measurements used, and
TypeSafe's own guidance is that unrelated content in the state costs accuracy. Most edits add one
block, so this is usually one request.

```
POST $ANTI_TANGENT_JEV_URL
{"model": "jev-1.13.0", "state": {"comment": "<block>"}, "questions": {"kind": <the question file>}}
```

Flagged when `answers.kind.probabilities.change_history >= threshold`.

Timing is specified rather than left to the pool: a flag prints its message and exits immediately
via `os._exit`, because Python joins pool threads at interpreter exit and a plain `sys.exit` would
wait for every in-flight request and could lose a verdict already found to the hook timeout. The
block cap and pool size are derived so that `blocks ÷ workers × per-request timeout` stays inside
the deadline. Host resolution happens inside a worker under the same deadline, since a socket
timeout does not bound `getaddrinfo` and a dropped VPN would otherwise stall every edit.

### The question file

`hooks/jev-question.json` holds the Choice question and the default threshold: options
`change_history`, `compatibility_contract` and `present_behaviour` with their rules and examples,
and a `focus` instructing that any history statement decides the answer even when the rest of the
comment explains present behaviour. Keeping it as data makes the wording reviewable on its own and
tunable against the calibration suite without touching the hook.

The shipped file is byte-identical to the measured `v3_choice` question, so the calibration set
stays an honest measurement of what runs. Its examples are synthetic; none is drawn from the
calibration set.

### Verdict, message, exit codes

A flag exits with a new status the wrapper maps to 2, so the trace log distinguishes a Jev block
from a regex block. The message quotes the offending comment lines with the probability, says the
comment reads as change history, and asks for a rewrite of those lines. Quoting is what makes the
block actionable: told only that "this reads as change history", an agent facing the unremovable
1.00 false flag rewrites, is refused again, and loops until a human intervenes. The message does
not name the off switch; the README documents that for the operator.

**Open:** whether a second consecutive Jev block on the same path within a session should degrade
to a non-blocking warning. It bounds the loop, and it weakens enforcement exactly where a wrong
verdict is most likely. Decide before implementation.

### Failure

Every failure allows the write: connection error, DNS failure, TLS failure, timeout, non-200,
malformed body, missing field, unparsable threshold, and any unexpected exception. One attempt, no
retry — a retry in a blocking hook doubles the wait, and 429 or 529 means back off rather than
block an edit. `hooks.json` gets an explicit 10-second timeout for this hook; Claude Code lets a
timed-out command hook proceed, so that fails open too (an assumption about the host, stated here
because no test in this repository can pin it).

A failure writes a breaker stamp in the trace directory, and the tier skips Jev for 60 seconds
after one. A dead network then costs one slow edit rather than every edit, and a persistent failure
emits one warning per session on stderr, so a silent permanent fail-open — an expired CA bundle
under `python3 -I`, a proxy that refuses CONNECT — is visible without reading the trace log.

### Trace

New events: `jev-block` (with the probability), `jev-pass`, `jev-skip` (with the reason it was
off), `jev-error` (with the failure class and the host), and `jev-capped`. The wrapper today runs
the body as a bare pipeline and reads `PIPESTATUS`, so it captures no stdout: it gains a redirect
of the body's stdout to a temp file, reads one `event|detail` line back, and a new status arm that
maps a Jev flag to exit 2 with its own trace event. Command substitution is not usable here — it
would break `PIPESTATUS`, which is how the wrapper reads the body's status at all. An eval case
pins that the hook's own stdout stays empty, so nothing reaches Claude Code's transcript.

### Configuration

| Variable | Default | Purpose |
|---|---|---|
| `ANTI_TANGENT_JEV` | unset | Must be exactly `1`. |
| `TYPESAFE_API_KEY` | unset | Required. |
| `ANTI_TANGENT_JEV_THRESHOLD` | `0.7` | Flag at or above. Clamped to (0, 1]; anything else, including `nan`, falls back to the default. |
| `ANTI_TANGENT_JEV_MODEL` | `jev-1.13.0` | Pinned; an alias moves under a tuned threshold. |
| `ANTI_TANGENT_JEV_URL` | `https://api.typesafe.ai/v1/systemone` | Stub endpoint for evals; proxy for operators. |
| `ANTI_TANGENT_JEV_EXCLUDE` | unset | Path globs never sent. |

**The key is sent only to the default host or to loopback.** Environment reaches this hook from a
repository's own checked-in `.claude/settings.json`, so a cloned repository could otherwise point
the hook at a server of its choosing and be handed `Authorization: Bearer $TYPESAFE_API_KEY` along
with the comment text. Any other host requires `ANTI_TANGENT_JEV_URL_TRUSTED=1`, which an operator
sets in their own global settings, and every `jev-*` trace line records the host.

`ANTI_TANGENT_COMMENT_GUARD=0` continues to disable comment scanning entirely, both tiers.

## Testing

**Unit** (`jev_scan_test.py`, stubbed transport, no network): block building from touched lines and
post-edit text, including the fragment-containment path, the no-match fallback, windowed
truncation, the starred-line and triple-quote cases, and both bounds; the three-part gate; the URL
trust rule; threshold clamping; redaction; response parsing; the deadline and first-flag exit; and
every failure path allowing the write.

**Eval cases** (`guard-evals.json`, driving the real hook binary against a stub server through
`ANTI_TANGENT_JEV_URL`): setting off; key absent; a flag blocking with the probability in the trace
and empty hook stdout; a timeout allowing; and a regex hit short-circuiting before any call. The
suite needs three things it does not have today: a stub-server facility (port allocation,
background lifetime under the existing EXIT trap, safe under concurrent runs), `EXPECTED_CASE_COUNT`
and the header's group partition grown for a named Jev group, and `ANTI_TANGENT_JEV`,
`ANTI_TANGENT_JEV_URL` and `TYPESAFE_API_KEY` added to the variables the runner unsets — without
that last one, a developer with the tier enabled runs 76 comment-write fixtures against the live
service.

**Calibration** (`evals/jev-comments.jsonl` plus a runner, gated on the key and the setting, never
in CI): the labeled set, reporting recall and precision per group. This is what catches the
question wording drifting or a model change costing recall — the job `fp-class.tsv` does for the
regex. It must be rebuilt with the hook's own block builder, so that the fixture text is byte-for-
byte what the hook would send; the set as measured was built by a separate script whose idea of a
block differs. The runner keys its cache on a hash of the question file, so an edited question
cannot be scored against stale answers, and it prints the five-group table above. `.jsonl` is not a
scannable extension, so the fixture's own history comments cannot reach `fp-scan.py`.

**Before shipping**, run the tier's exact request shape over a foreign Go module's comments and
inspect every flag by hand. It needs no labels and it is the only evidence available that the
1-in-42 rate is not an artefact of this repository's house style.

## Risks

- **About one wrong block a day at the point estimate**, and the interval reaches several. No
  threshold removes the worst case, since the known false flag scores 1.00.
- **A wrong flag on pre-existing text costs more than on added text**, because Decision 6 asks the
  agent to rewrite a comment it did not author.
- **A literal-reading blind spot.** Three "X used to [verb]" history blocks scored near zero. The
  question was not tuned afterwards, since tuning on the measured set would inflate the numbers.
- **Egress.** Added comment text from every edited repository, redacted for credentials, while the
  setting is on. TypeSafe offers zero data retention only on enterprise plans.
- **Model drift.** `jev-1.13.0` is pinned, but the pin only defers the question; the calibration
  suite is what answers it at upgrade time.
- **Windows.** `read_text_capped` returns None without `O_NOFOLLOW`, so a Write over an existing
  file exits 3 and this tier never runs; an Edit takes the no-context path. Documented, not fixed.
- **Measurement limits.** 162 scored comments from one public Go repository, labeled by Claude and
  not reviewed by a second judge, one run, six questions per request through a proxy. The rules
  were written knowing which kinds of comment the set holds.

## Open at implementation time

- Confirmation of the calibration set's labels before it is committed.
- The loop-breaker question under "Verdict, message, exit codes".
- The exact block-message wording.

## Shipping

Rebase onto main (0.23.0), branch `version/0.24.0`, matching `CHANGELOG.md` entry, and a guard
README covering: both gates and the on-touch split Decision 6 introduces, what leaves the machine
and the redaction, the fail-open table and the breaker, the CA-bundle failure mode under
`python3 -I`, the Windows gap, and the off switch. `plugin.json`'s version and its description
("pattern set") change with it.
