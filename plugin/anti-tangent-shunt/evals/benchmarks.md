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

Each scenario was run **3 times**; the table reports the **median** of each
column across the three runs.

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

## Raw results (all three runs per scenario)

All `input_tokens` / `output_tokens` / `review_ms` values below are read
directly from the server's response (`structuredContent`), not computed.
"Answer/gen bytes" is `wc -c` of the returned `answer` text (scenarios 1–3)
or of the file `code_write` wrote (scenario 4), which is what the `bytes/4`
approximation in the table above is applied to.

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

**Re-run (used for the published figures):**

| Run | input_tokens | output_tokens | review_ms | answer bytes | answer tokens (≈) |
|---|---|---|---|---|---|
| 1 | 46002 | 555 | 8643 | 103 | 26 |
| 2 | 46002 | 988 | 11969 | 117 | 29 |
| 3 | 46002 | 560 | 8629 | 114 | 29 |
| **median** | **46002** | **560** | 8643 | — | **29** |

All three re-run answers correctly identify `(*query).Close` (receiver
`*query`) as the exported function mutating the package-level `fPointPool` /
`hPointPool` point pools — unlike the superseded run 1 above. The median
savings percentage happens to round to the same 99.9% either way (see
Computed table); what changed is that the number is now backed by three
runs that answered the question correctly, not one that didn't.

Without = round(171971/4) = **42993** tokens.

### `source_test_pair`

| Run | input_tokens | output_tokens | review_ms | answer bytes | answer tokens (≈) |
|---|---|---|---|---|---|
| 1 | 61127 | 1297 | 13916 | 1129 | 282 |
| 2 | 61127 | 1236 | 14199 | 2941 | 735 |
| 3 | 61127 | 869 | 9745 | 1490 | 373 |
| **median** | **61127** | **1236** | 13916 | — | **373** |

Without = round(216506/4) = **54127** tokens.

### `multi_file_cross_package`

| Run | input_tokens | output_tokens | review_ms | answer bytes | answer tokens (≈) |
|---|---|---|---|---|---|
| 1 | 10864 | 357 | 4993 | 214 | 54 |
| 2 | 10864 | 406 | 5141 | 296 | 74 |
| 3 | 10864 | 341 | 4641 | 306 | 77 |
| **median** | **10864** | **357** | 4993 | — | **74** |

Without = round(41489/4) = **10372** tokens.

### `code_write`

| Run | input_tokens | output_tokens | review_ms | generated bytes | generated tokens (≈) | without = ref+gen (≈) | lines_written |
|---|---|---|---|---|---|---|---|
| 1 | 29325 | 1474 | 18657 | 1332 | 333 | 27908 | 67 |
| 2 | 29325 | 2384 | 23884 | 4235 | 1059 | 28634 | 199 |
| 3 | 29325 | 2315 | 21659 | 4611 | 1153 | 28728 | 201 |
| **median** | **29325** | **2315** | 21659 | — | — | **28634** | **199** |

Reference-file component (constant): round(110300/4) = **27575** tokens.

## Computed table (matches `benchmarks.tsv` / the README)

| scenario_id | lines | without | with | unit | savings | worker in | worker out |
|---|---|---|---|---|---|---|---|
| single_large_file | 4,984 | 42,993 | 29 | tokens | 99.9% | 46,002 | 560 |
| source_test_pair | 7,624 | 54,127 | 373 | tokens | 99% | 61,127 | 1,236 |
| multi_file_cross_package | 1,302 | 10,372 | 74 | tokens | 99% | 10,864 | 357 |
| code_write | 3,142 | 28,634 | 199 | lines | — | 29,325 | 2,315 |

## Concerns / observations

- **Worker `output_tokens` is much larger than the visible answer would
  suggest** (e.g. `single_large_file` run 2: 988 output tokens for a
  117-byte answer, ~29 approximated tokens). `gpt-5.6-luna` appears to spend
  a substantial share of its billed output on hidden reasoning that never
  reaches the `answer` field. This is reported exactly as observed —
  `worker_out` in the table is the provider's real reported count, not
  reconciled against the visible answer length — but it means "worker cost"
  and "visible answer size" are not the same thing for this model, and a
  reader comparing worker spend across models should not assume otherwise.
- **Scenario sizes deviate from upstream's targets** by amounts ranging from
  +2.9% (`source_test_pair`) to +24% (`single_large_file`, and `code_write`
  at −14%): real corpus files come in whatever sizes they come in, and no
  file was padded or trimmed to hit a round number. `multi_file_cross_package`
  came out closest (1,302 vs. upstream's 1,281, +1.6%) purely because a
  3-file real cross-package call chain happened to total almost exactly that.
- **Answer quality varies run to run** for the same fixed prompt and input.
  The clearest example on record: the original `single_large_file` run 1
  claimed *no* exported function mutates package-level state, while its
  runs 2–3 correctly identified `(*query).Close` mutating the point pools —
  and because run 1 happened to be the median, the first published savings
  figure was computed from the wrong answer (see the re-run note under
  `single_large_file` above). The 2026-09-08 re-run's three answers are all
  substantively correct, but still vary in phrasing and length (26/29/29
  approximated tokens) — this is normal LLM sampling variance at
  temperature > 0 and is why the method runs each scenario three times and
  reports the median rather than a single sample. It is not a defect in the
  harness, but it is a reason not to over-read any single scenario's answer
  as ground truth, and a reason to check *what* the median run said, not
  only its size.
- The `bytes/4` approximation is coarse for very short answers (a handful of
  words carries proportionally more punctuation/backtick overhead per token
  than the corpus prose it approximates for the "without" side); the
  relative error is largest exactly where the savings percentage is highest.
  Disclosed here rather than presented as tokenizer-accurate.
