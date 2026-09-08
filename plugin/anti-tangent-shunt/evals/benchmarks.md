# Benchmarks: method and raw data

This is the human-readable record behind the "Ours" table in
[`../README.md`](../README.md). Every number in that table, and in the
machine-readable [`benchmarks.tsv`](benchmarks.tsv) it is checked against,
traces back to a run recorded here. The harness that produced these numbers
is [`bench.sh`](bench.sh) — check it out and re-run it to reproduce.

Upstream's own figures (Spotify's `shunt` plugin) are cited to the pinned
repo source, **not** to the accompanying blog post, which returns HTTP 403
from this environment and was never read:
`spotify/portal-ai-plugins@3c24ca30ff63e1f5bbad1c43fe5324daff579123`,
`plugins/shunt/README.md`.

## Step 1: corpus

**Repository:** `github.com/prometheus/prometheus`
**Pinned commit:** `7f48230f675e7c459398bf1d0f055f6f55caf90a`
**Not** anti-tangent-mcp itself, and no Prometheus source appears anywhere
in this repo's own prompts or fixtures (checked with
`grep -rli prometheus internal/prompts`, no hits).

Eligibility, measured against that commit, every rule applied — not judgement:

| Criterion | Requirement | Measured |
|---|---|---|
| Production Go lines | 120,000–200,000 | **152,290** |
| Eligible production files | — | 445 |
| Licence | OSI-approved, public repo | `LICENSE` = Apache License 2.0 (SPDX `Apache-2.0`), OSI-approved: <https://opensource.org/licenses/Apache-2.0> |
| Packages | ≥ 20 | **113** (`go list ./... \| grep -v /vendor/ \| wc -l`) |
| Builds | `go build ./...` | exit 0, no errors |

Commands actually run:

```bash
git clone --depth 1 https://github.com/prometheus/prometheus.git /tmp/bench-corpus
git -C /tmp/bench-corpus rev-parse HEAD
# 7f48230f675e7c459398bf1d0f055f6f55caf90a

cd /tmp/bench-corpus
git ls-files '*.go' | grep -v '_test\.go$' | grep -Ev '(^|/)vendor/' \
  | while IFS= read -r f; do head -1 "$f" | grep -q '^// Code generated' || printf '%s\n' "$f"; done \
  > /tmp/bench-prod-files.txt
wc -l < /tmp/bench-prod-files.txt                                        # 445
xargs -a /tmp/bench-prod-files.txt cat | wc -l                           # 152290

git ls-files '*_test.go' | grep -Ev '(^|/)vendor/' \
  | while IFS= read -r f; do head -1 "$f" | grep -q '^// Code generated' || printf '%s\n' "$f"; done \
  > /tmp/bench-test-files.txt                                            # 278 files

go list ./... | grep -v '/vendor/' | wc -l                               # 113
ls LICEN[CS]E* COPYING*                                                  # LICENSE
go build ./...                                                           # exit 0
```

Every scenario's input files below are drawn from `/tmp/bench-prod-files.txt`
(and, for scenario 2's test file, `/tmp/bench-test-files.txt`) — confirmed by
membership check before use, not assumed.

## Step 2: scenarios

Four scenarios mirroring upstream's shape. Exact prompts were fixed **before**
any measurement and are reproduced verbatim below and in `bench.sh`.

### Scenario `single_large_file` — one large file

**File:** `promql/engine.go` — 4,984 lines, 171,971 bytes.
(Upstream's shape target was ~4,000 lines; the corpus's largest single file
close to that size is 4,984 — a real file, not adjusted to hit a round
number.)

**Prompt (verbatim):**
> Which exported functions in this file mutate package-level state, and what are their receivers?

Chosen because `engine.go` declares real package-level state (`fPointPool`,
`hPointPool`, `matrixSelectorHPool`, `ratiosampler`) that methods in the file
actually `Get`/`Put` against.

### Scenario `source_test_pair` — source + test pair

**Files:** `web/api/v1/api.go` (2,485 lines, 76,473 bytes) +
`web/api/v1/api_test.go` (5,139 lines, 140,033 bytes) = 7,624 lines,
216,506 bytes combined. (Upstream: 7,408 lines combined.)

**Prompt (verbatim):**
> Which behaviours does the test file cover that the source file's exported API exposes, and which exported functions have no test?

### Scenario `multi_file_cross_package` — cross-package calls

**Files:**
- `rules/group.go` (package `rules`) — 1,234 lines, 39,224 bytes
- `model/timestamp/timestamp.go` (package `timestamp`) — 34 lines, 1,054 bytes
- `model/value/value.go` (package `value`) — 34 lines, 1,211 bytes

Total: 1,302 lines, 41,489 bytes. (Upstream: 1,281 lines.)

**Prompt (verbatim):**
> Which functions cross package boundaries between these files, and in which direction does each call flow?

Chosen because `rules/group.go` makes real, explicit cross-package calls —
`timestamp.FromTime(...)` and `value.IsStaleNaN(...)` / `value.StaleNaN` — into
the two small files above, so the question has a checkable answer rather than
a coincidental grouping of unrelated files.

### Scenario `code_write` — generate a table-driven test

**Reference file:** `tsdb/head.go` — 3,142 lines, 110,300 bytes.
(Upstream: 3,667 lines.)

**Spec (verbatim):**
> A table-driven test for the exported functions in the reference file, covering the zero value, one ordinary case, and one error case per function.

**Target:** `$OUT_DIR/head_bench_test.go`, deleted before each of the three
runs so every run is a fresh write, never the overwrite-refusal path.

The generated file was not required to compile or pass — `code_write`'s own
contract is "verified, not inspected": the caller proves the result by
running the task's build/tests, which is out of scope for a token/line
measurement. Run 3's output was spot-checked and is real, syntactically
plausible Go (`package tsdb`, `import ("math"; "testing")`, a
`TestDefaultHeadOptions` table-driven test against `DefaultHeadOptions()`) —
not garbage.

## Step 3 & 4: measurement method

**Tokenizer:** no offline tokenizer was available in this environment
(`tiktoken` is not installed and installing it was judged unnecessary
complexity for a disclosed approximation). **A `bytes / 4` approximation is
used throughout, rounded half-up**, for both the "without" side (source
bytes) and the "with" side (the returned `answer` text) — the same method on
both sides of every ratio, named here as the spec requires.

**Worker model:** `openai:gpt-5.6-luna` — this is the host's real configured
`ANTI_TANGENT_MID_MODEL` (and hence, via fallback, `ANTI_TANGENT_WORKER_MODEL`)
in the environment these runs were executed in. It is recorded here exactly
because the numbers below are meaningless without it: a different worker
model will produce different worker token counts and likely different answer
lengths.

**"Without" for scenarios 1–3** = `round(sum(file bytes) / 4)` — what a
direct `Read` of the file(s) would cost the implementer.

**"Without" for `code_write`** = `round(reference bytes / 4) + round(generated
code bytes / 4)`, i.e. tokens(reference) + tokens(generation), computed **per
run** (the generated size varies) and then the three per-run sums are
medianed — this is the resolved form of upstream's unresolved
"40,614 + generation". The reference-file component (27,575 tokens, constant)
and the per-run generated-code component are both recorded in the raw table
below so the sum is checkable, per the spec.

**"With" for scenarios 1–3** = `round(bytes(answer) / 4)` — the returned
answer is what actually lands in the implementer's context.

**"With" for `code_write`** = `lines_written`, in lines. No savings ratio is
computed or displayed for this scenario — lines and tokens are different
units, and forcing a ratio across them would misstate the comparison. This is
enforced by the checker, not just by convention.

**Worker consumption** (`input_tokens`, `output_tokens`) is the provider's own
reported token counts for the worker call — **not** an approximation, and
reported **separately, never netted off** against the "without"/"with"
figures above: the claim being measured is implementer-context cost, and
worker spend is a real, separate cost paid to make that saving happen.

Each scenario is run **3 times**; a run whose answer fails its scenario's validity criterion (see
"Validity criteria and verdicts" below) is re-rolled and an additional run added, until **3
valid** runs are collected. The table reports the **median** of each column across those 3 valid
runs — never across whatever landed first. An invalid run is kept visible in the raw tables below
with its verdict, not deleted: it is excluded from the statistic, not from the record.

## Harness configuration

`bench.sh` talks to the real, built server (`go build -o /tmp/atm
./cmd/anti-tangent-mcp`) over MCP stdio — the same newline-delimited
JSON-RPC 2.0 transport a host uses (`mcp.StdioTransport`) — doing the
`initialize` → `notifications/initialized` → `tools/call` handshake itself,
issuing exactly one `tools/call` per run with the payload shown above, and
capturing the full raw response for every run under
`$BENCH_OUT_DIR/raw/<scenario_id>-run<N>.json`.

Three env vars are set **unconditionally** by the script (overriding, not
merely defaulting over, whatever the caller's shell already has set — this
environment's own ambient `ANTI_TANGENT_PLAN_ROOTS` pointed at an unrelated
tree and silently broke the first attempt at this run until the script
stopped deferring to it):

- `ANTI_TANGENT_PLAN_ROOTS="$CORPUS_DIR:$OUT_DIR"` — the corpus (read) and
  the scratch dir `code_write`'s target lives under (write).
- `ANTI_TANGENT_MAX_PAYLOAD_BYTES=262144` — raised from the 204800-byte
  default because `source_test_pair`'s combined 216,506 bytes exceeds it.
  This changes nothing about what is measured on either side of the ratio;
  it only lets the call complete rather than being refused as too large.
- `ANTI_TANGENT_WORKER_MAX_TOKENS=16384` — raised from the 4096 default to
  the ceiling, so `code_write`'s longer generations are not cut off
  mid-file (a truncated `code_write` response fails closed and writes
  nothing, per the tool's own design).
- `ANTI_TANGENT_STATS_DIR` is scoped under `$OUT_DIR/stats` so this run's
  stats don't mix into any other stats directory the caller has configured.

Reproduce with:

```bash
export OPENAI_API_KEY=...
export ANTI_TANGENT_WORKER_MODEL=openai:gpt-5.6-luna
export CORPUS_DIR=/tmp/bench-corpus     # clone + pin the commit above first
bash plugin/anti-tangent-shunt/evals/bench.sh
```

## Validity criteria and verdicts

**The published statistic is computed over valid runs only, and this is not optional
disclosure — it is the method.** For the *second* time (see `single_large_file` below), a
freshly-sampled round's byte-count median traced to a run whose answer was factually wrong; a
wrong answer tends to be short, so including it biased the reported savings *upward* rather than
measuring anything about delegation working. The fix is a concrete, checkable fact per scenario,
applied to every run before any median is taken — not a rubric needing judgement, and cheap
enough that a re-runner can apply it by eye or by `grep`:

- **`single_large_file`.** Valid iff the answer names `(*query).Close` (receiver `*query`, in
  `promql/engine.go`) as an exported function mutating package-level state — it returns
  point/histogram slices to the package-level `fPointPool`/`hPointPool` pools (Step 2 above). An
  answer claiming no exported function mutates package-level state is invalid.
- **`source_test_pair`.** Valid iff the answer identifies `NewAPI` — `web/api/v1/api.go`'s
  exported constructor — as lacking a direct test in `api_test.go`. Checkable with
  `grep -n 'NewAPI(' web/api/v1/api_test.go`, which returns no hits: no test in the file
  constructs an `API` via `NewAPI`. An answer claiming full test coverage of the exported surface
  is invalid.
- **`multi_file_cross_package`.** Valid iff the answer identifies both real cross-package calls
  named in Step 2 — `timestamp.FromTime(...)` (`rules` → `model/timestamp`) and
  `value.IsStaleNaN(...)` / `value.StaleNaN` (`rules` → `model/value`) — with the call direction
  the right way round (`rules/group.go` calling out, not the reverse).
- **`code_write`.** Valid iff the call succeeds, `target_path` exists afterward, and
  `lines_written > 0`. Generated-code *correctness* is explicitly out of scope for this
  measurement — `code_write`'s own contract is "verified, not inspected" (Step 2), and the
  harness never compiles or runs the output — so validity here is about the measurement
  completing, not about grading the generated test.

A run that fails its criterion is marked **INVALID** in the raw tables below, right where it
happened, and stays there rather than being deleted or moved to a "superseded" section on its
own: a model getting a real question wrong on a non-trivial fraction of runs (see
`single_large_file`'s tally under Concerns) is itself a finding about how much to trust
unsupervised delegation, and sanding it off would hide exactly that. It is excluded only from the
median the published tables report.

## Raw results (all runs per scenario, including re-rolls)

All `input_tokens` / `output_tokens` / `review_ms` values below are read
directly from the server's response (`structuredContent`), not computed.
"Answer/gen bytes" is `wc -c` of the returned `answer` text (scenarios 1–3)
or of the file `code_write` wrote (scenario 4), which is what the `bytes/4`
approximation in the table above is applied to.

**Re-run 2026-09-08b (finding A / PR #65): `bench.sh` byte-count fix.**
CodeRabbit found that `bench.sh`'s answer-byte measurement for scenarios 1–3
went `answer=$(jq -r ... <<<"$resp")` then `printf '%s' "$answer" | wc -c` —
routing the answer through a `$(...)` command substitution, which strips
*all* trailing newlines from what it captures. An answer ending in one or
more newlines was silently undercounted, and that undercount fed directly
into the published "with" column. `bench.sh` now pipes `jq -j` (join, no
added newline) straight into `wc -c`, with no `$(...)` round trip, so a
trailing newline in the answer is counted, not discarded — see `bench.sh`
for the fix itself.

Because the raw provider responses are **not checked in**
(`$BENCH_OUT_DIR/raw/` is a scratch directory), verifying the fix could not
be done by recomputing over old data — it required a fresh run of all four
scenarios, 3 runs each, same corpus/model/prompts as before. The tables
below are that fresh run and are what `benchmarks.tsv`, the README and the
Computed table above now report.

**The fix itself made no measurable difference on this run's data.**
Checked directly: for all 12 raw responses captured by this run, computing
`answer` bytes both the old (buggy) way and the new (`jq -j`) way gives the
*same* byte count in every case — none of this run's 12 answers happened to
end in a trailing newline, so the bug had zero effect on this particular
sample set. Every number below that differs from the table this superseded
differs because it is a **new, independently-sampled run** (`gpt-5.6-luna`
is called at temperature > 0; see "Answer quality varies run to run" under
Concerns), not because the byte-count bug distorted the old figures — that
would only be true of a run whose answer(s) actually ended in a newline. The
old table cannot be re-checked directly against this claim because its own
raw JSON was never retained either — this is disclosed as the honest limit
of what "same corpus, model and prompts" can prove after the fact, not
papered over.

`code_write`'s own byte count (`wc -c` of the file it wrote to disk) never
went through `jq`/`$(...)` at all and was never affected by this bug; its
figures below changed for sampling-variance reasons only, included here
purely because the task asked to re-run all four scenarios together as one
consistent set.

### `single_large_file`

**Re-run 2026-09-08.** The original three runs below produced a median (run
1, 36 answer tokens) whose answer was itself wrong — see the first bullet
under Concerns — so the published savings figure was measuring an incorrect,
abnormally short answer rather than delegation working. Per the 2026-09-07
final review (finding 7 / I5), the scenario was re-run from scratch against
the same pinned corpus, model and prompt; the fresh runs are what the
Computed table, `benchmarks.tsv` and the README now report.

**Original run (superseded — median answer was incorrect):**

| Run | input_tokens | output_tokens | review_ms | answer bytes | answer tokens (≈) |
|---|---|---|---|---|---|
| 1 | 46001 | 292 | 5294 | 143 | 36 |
| 2 | 46001 | 1061 | 16295 | 260 | 65 |
| 3 | 46001 | 558 | 8547 | 119 | 30 |
| **median** | **46001** | **558** | 8547 | — | **36** |

**Re-run (superseded — correct answers, but measured with the pre-finding-A
buggy byte count; see "Re-run 2026-09-08b" below for the currently-published
figures):**

| Run | input_tokens | output_tokens | review_ms | answer bytes | answer tokens (≈) |
|---|---|---|---|---|---|
| 1 | 46002 | 555 | 8643 | 103 | 26 |
| 2 | 46002 | 988 | 11969 | 117 | 29 |
| 3 | 46002 | 560 | 8629 | 114 | 29 |
| **median** | **46002** | **560** | 8643 | — | **29** |

All three re-run answers correctly identify `(*query).Close` (receiver
`*query`) as the exported function mutating the package-level `fPointPool` /
`hPointPool` point pools — unlike the superseded run 1 above.

**Re-run 2026-09-08b (finding A / PR #65 — used for the published
figures):**

| Run | input_tokens | output_tokens | review_ms | answer bytes | answer tokens (≈) |
|---|---|---|---|---|---|
| 1 | 46001 | 759 | 10535 | 117 | 29 |
| 2 | 46001 | 400 | 5803 | 120 | 30 |
| 3 | 46001 | 865 | 11175 | 131 | 33 |
| **median** | **46001** | **759** | 10535 | — | **30** |

Runs 1 and 3 correctly identify `(*query).Close` (receiver `*query`)
mutating the package-level `fPointPool`/`hPointPool` pools. **Run 2's answer
is wrong** — it claims no exported function in the file mutates
package-level state — and run 2's byte count (120) happens to be the median
of the three, so the published "with" figure for this scenario traces to an
incorrect answer, same defect class as the superseded original run above,
just smaller in effect (120 bytes is the middle value here, not an outlier
low one). **Superseded** — see "Re-run 2026-09-08c" below, which applies the
validity criterion and computes the median over valid runs only, per task 25.

**Re-run 2026-09-08c (task 25 — validity-gated statistic; used for the published figures).**
This is the *second* fresh round (the 2026-09-08b round above was the first) in which the
freshly-sampled median happened to be a wrong answer. Rather than accept that a third time, every
run below carries an explicit verdict against the criterion in "Validity criteria and verdicts",
and the median is taken over valid runs only, re-rolling until there are three of them.

| Run | input_tokens | output_tokens | review_ms | answer bytes | answer tokens (≈) | Verdict |
|---|---|---|---|---|---|---|
| 1 | 46001 | 560 | 10159 | 115 | 29 | valid — names `(*query).Close` |
| 2 | 46001 | 1090 | 14832 | 195 | 49 | **INVALID** — claims no exported function mutates state |
| 3 | 46001 | 556 | 9026 | 113 | 28 | valid — names `query.Close` |
| 4 (re-roll) | 46001 | 555 | 10981 | 111 | 28 | valid — names `query.Close` |

3 valid of 4 attempted. **Median of the 3 valid runs (1, 3, 4):** input_tokens 46001,
output_tokens 556, review_ms 10159, answer bytes 113 → answer tokens ≈ **28**.

Run 2 is kept above rather than deleted, exactly as in the superseded round: this is the same
failure mode recurring. Counting every round recorded in this document (the original superseded
run, the 2026-09-08 re-run, 2026-09-08b, and this one), `gpt-5.6-luna` has now given this exact
question a wrong answer in **3 of the 13** total `single_large_file` runs sampled — roughly
1-in-4. That rate is itself a finding; see "Answer quality varies run to run" under Concerns.

Without = round(171971/4) = **42993** tokens.

### `source_test_pair`

**Superseded (pre-finding-A byte count; see "Re-run 2026-09-08b" below for
the currently-published figures):**

| Run | input_tokens | output_tokens | review_ms | answer bytes | answer tokens (≈) |
|---|---|---|---|---|---|
| 1 | 61127 | 1297 | 13916 | 1129 | 282 |
| 2 | 61127 | 1236 | 14199 | 2941 | 735 |
| 3 | 61127 | 869 | 9745 | 1490 | 373 |
| **median** | **61127** | **1236** | 13916 | — | **373** |

**Superseded (finding A / PR #65 byte-count fix; see "Re-run 2026-09-08c" below for the
currently-published figures):**

| Run | input_tokens | output_tokens | review_ms | answer bytes | answer tokens (≈) |
|---|---|---|---|---|---|
| 1 | 61127 | 855 | 8938 | 1473 | 368 |
| 2 | 61127 | 1175 | 13551 | 1402 | 351 |
| 3 | 61127 | 680 | 8677 | 764 | 191 |
| **median** | **61127** | **855** | 8938 | — | **351** |

**Re-run 2026-09-08c (task 25 — validity-gated statistic; used for the published figures).**
All three runs are checked against the `NewAPI` criterion in "Validity criteria and verdicts";
no re-roll was needed.

| Run | input_tokens | output_tokens | review_ms | answer bytes | answer tokens (≈) | Verdict |
|---|---|---|---|---|---|---|
| 1 | 61127 | 1304 | 16412 | 1304 | 326 | valid — names `NewAPI` as untested |
| 2 | 61127 | 866 | 11094 | 1555 | 389 | valid — names `NewAPI` as untested |
| 3 | 61127 | 1010 | 11104 | 2155 | 539 | valid — names `NewAPI` as untested |

3 valid of 3 attempted. **Median:** input_tokens 61127, output_tokens 1010, review_ms 11104,
answer bytes 1555 → answer tokens ≈ **389**.

All three runs also correctly note `TSDBStatsFromIndexStats` is exercised only indirectly (via
`API.serveTSDBStatus` in `TestTSDBStatus`), consistent with `grep -n
'TSDBStatsFromIndexStats' web/api/v1/api_test.go` showing no direct call — not part of the
validity criterion (`NewAPI` alone is), but corroborating that the answers engage with the real
file rather than confabulating.

Without = round(216506/4) = **54127** tokens.

### `multi_file_cross_package`

**Superseded (pre-finding-A byte count; see "Re-run 2026-09-08b" below for
the currently-published figures):**

| Run | input_tokens | output_tokens | review_ms | answer bytes | answer tokens (≈) |
|---|---|---|---|---|---|
| 1 | 10864 | 357 | 4993 | 214 | 54 |
| 2 | 10864 | 406 | 5141 | 296 | 74 |
| 3 | 10864 | 341 | 4641 | 306 | 77 |
| **median** | **10864** | **357** | 4993 | — | **74** |

**Superseded (finding A / PR #65 byte-count fix; see "Re-run 2026-09-08c" below for the
currently-published figures):**

| Run | input_tokens | output_tokens | review_ms | answer bytes | answer tokens (≈) |
|---|---|---|---|---|---|
| 1 | 10864 | 328 | 4317 | 237 | 59 |
| 2 | 10864 | 321 | 3805 | 208 | 52 |
| 3 | 10864 | 504 | 5719 | 210 | 53 |
| **median** | **10864** | **328** | 4317 | — | **53** |

**Re-run 2026-09-08c (task 25 — validity-gated statistic; used for the published figures).**
All three runs are checked against the two-call criterion in "Validity criteria and verdicts";
no re-roll was needed.

| Run | input_tokens | output_tokens | review_ms | answer bytes | answer tokens (≈) | Verdict |
|---|---|---|---|---|---|---|
| 1 | 10864 | 425 | 5896 | 324 | 81 | valid — both calls, correct direction |
| 2 | 10864 | 251 | 3504 | 212 | 53 | valid — both calls, correct direction |
| 3 | 10864 | 317 | 4518 | 192 | 48 | valid — both calls, correct direction |

3 valid of 3 attempted. **Median:** input_tokens 10864, output_tokens 317, review_ms 4518,
answer bytes 212 → answer tokens ≈ **53**.

Without = round(41489/4) = **10372** tokens.

### `code_write`

Never affected by the finding A byte-count bug — this scenario's byte count
is `wc -c` of the file `code_write` wrote to disk, which never passed
through `jq`/`$(...)`. Re-run anyway (finding A / PR #65) purely so the
published set is four scenarios from one consistent run rather than three
fresh plus one old.

**Superseded:**

| Run | input_tokens | output_tokens | review_ms | generated bytes | generated tokens (≈) | without = ref+gen (≈) | lines_written |
|---|---|---|---|---|---|---|---|
| 1 | 29325 | 1474 | 18657 | 1332 | 333 | 27908 | 67 |
| 2 | 29325 | 2384 | 23884 | 4235 | 1059 | 28634 | 199 |
| 3 | 29325 | 2315 | 21659 | 4611 | 1153 | 28728 | 201 |
| **median** | **29325** | **2315** | 21659 | — | — | **28634** | **199** |

**Superseded (finding A / PR #65 re-run):**

| Run | input_tokens | output_tokens | review_ms | generated bytes | generated tokens (≈) | without = ref+gen (≈) | lines_written |
|---|---|---|---|---|---|---|---|
| 1 | 29325 | 3121 | 21707 | 5228 | 1307 | 28882 | 257 |
| 2 | 29325 | 2898 | 21688 | 4624 | 1156 | 28731 | 191 |
| 3 | 29325 | 2521 | 18032 | 4886 | 1222 | 28797 | 219 |
| **median** | **29325** | **2898** | 21688 | — | — | **28797** | **219** |

Run 3's output was spot-checked and is real, syntactically plausible Go
(`package tsdb`, `import ("context"; "math"; "testing"; ...)`, a
`TestDefaultHeadOptions` table-driven test against `DefaultHeadOptions()`) —
not garbage.

**Re-run 2026-09-08c (task 25 — validity-gated statistic; used for the published figures).**
All three calls succeeded, wrote `target_path`, and reported `lines_written > 0`, so all three
are valid under this scenario's criterion; no re-roll was needed.

| Run | input_tokens | output_tokens | review_ms | generated bytes | generated tokens (≈) | without = ref+gen (≈) | lines_written | Verdict |
|---|---|---|---|---|---|---|---|---|
| 1 | 29325 | 2908 | 24100 | 5291 | 1323 | 28898 | 215 | valid — succeeded, wrote 215 lines |
| 2 | 29325 | 2748 | 19809 | 5577 | 1394 | 28969 | 233 | valid — succeeded, wrote 233 lines |
| 3 | 29325 | 2358 | 19666 | 3241 | 810 | 28385 | 115 | valid — succeeded, wrote 115 lines |

3 valid of 3 attempted. **Median:** input_tokens 29325, output_tokens 2748, review_ms 19809,
without ≈ **28898**, lines_written **215**.

Only run 3's file survives on disk at the end of the loop (`bench.sh` deletes and re-creates
`$OUT_DIR/head_bench_test.go` before each run) and it was spot-checked: real, syntactically
plausible Go (`package tsdb`, a table-driven `TestExportedFunctions` covering
`DefaultHeadOptions()`'s zero, ordinary and error cases) — not garbage. Runs 1 and 2's generated
files were not retained for inspection, which is why validity for this scenario is checked by
`lines_written > 0` and the file existing, not by reading the content — see "Validity criteria
and verdicts".

Reference-file component (constant): round(110300/4) = **27575** tokens.

## Computed table (matches `benchmarks.tsv` / the README)

| scenario_id | lines | without | with | unit | savings | worker in | worker out |
|---|---|---|---|---|---|---|---|
| single_large_file | 4,984 | 42,993 | 28 | tokens | 99.9% | 46,001 | 556 |
| source_test_pair | 7,624 | 54,127 | 389 | tokens | 99% | 61,127 | 1,010 |
| multi_file_cross_package | 1,302 | 10,372 | 53 | tokens | 99% | 10,864 | 317 |
| code_write | 3,142 | 28,898 | 215 | lines | — | 29,325 | 2,748 |

Every figure above is the **median of the 3 valid runs** for that scenario — see "Validity
criteria and verdicts". `single_large_file`'s `runs`/`statistic` columns in `benchmarks.tsv` read
3 because 3 valid runs went into the median, not because only 3 were attempted: one run (of 4)
was invalid and is excluded from this table but not from the raw record above.

## Concerns / observations

- **Worker `output_tokens` is much larger than the visible answer would
  suggest** (e.g. `single_large_file` run 3, 2026-09-08b re-run: 865 output
  tokens for a 131-byte answer, ~33 approximated tokens). `gpt-5.6-luna`
  appears to spend a substantial share of its billed output on hidden
  reasoning that never reaches the `answer` field. This is reported exactly
  as observed — `worker_out` in the table is the provider's real reported
  count, not reconciled against the visible answer length — but it means
  "worker cost" and "visible answer size" are not the same thing for this
  model, and a reader comparing worker spend across models should not assume
  otherwise.
- **Scenario sizes deviate from upstream's targets** by amounts ranging from
  +2.9% (`source_test_pair`) to +24% (`single_large_file`, and `code_write`
  at −14%): real corpus files come in whatever sizes they come in, and no
  file was padded or trimmed to hit a round number. `multi_file_cross_package`
  came out closest (1,302 vs. upstream's 1,281, +1.6%) purely because a
  3-file real cross-package call chain happened to total almost exactly that.
- **Answer quality varies run to run** for the same fixed prompt and input,
  and this keeps recurring across re-runs of `single_large_file`
  specifically — three separate times now, across three independently-sampled
  rounds. The original run 1 claimed *no* exported function mutates
  package-level state, while its runs 2–3 correctly identified
  `(*query).Close` mutating the point pools, and because run 1 happened to
  be the median, the first published savings figure was computed from the
  wrong answer (see the superseded original-run note under
  `single_large_file` above). The 2026-09-08 re-run's three answers were all
  substantively correct. The 2026-09-08b re-run (finding A / PR #65) hit the
  *same* failure mode a second time: its run 2 claimed no exported function
  mutates package-level state, and run 2's byte count (120) happened to be
  the median of that round's three, so the figure published at the time
  traced to an incorrect answer. **This is why the method changed for task
  25**, rather than being disclosed a third time and left in the statistic:
  every run is now checked against the criterion in "Validity criteria and
  verdicts" *before* any median is taken, and an invalid run is excluded from
  the statistic rather than trusted because it happened to land in the
  middle. The 2026-09-08c round (the currently-published one) hit the same
  failure mode a *third* time — its run 2 again claimed no exported function
  mutates package-level state — but this time the criterion catches it before
  publication: run 2 is marked INVALID and excluded, and a fourth run was
  added to reach three valid ones. Across every round recorded in this
  document (including the 2026-09-08 re-run, whose three answers were all
  correct), `gpt-5.6-luna` has now given this specific question a wrong
  answer in **3 of 13** total `single_large_file` runs — roughly 1-in-4, a
  real, recurring failure rate on this one question, not a fluke, and worth
  knowing on its own terms independent of the benchmark numbers: a caller
  delegating this exact kind of "which function mutates this state" question
  to this model should not treat a single answer as reliable without
  checking it. It is not a defect in the harness — three (now
  validity-gated) samples is the method working as designed, not failing.
- The `bytes/4` approximation is coarse for very short answers (a handful of
  words carries proportionally more punctuation/backtick overhead per token
  than the corpus prose it approximates for the "without" side); the
  relative error is largest exactly where the savings percentage is highest.
  Disclosed here rather than presented as tokenizer-accurate.
