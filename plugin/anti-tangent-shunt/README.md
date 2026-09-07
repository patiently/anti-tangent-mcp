# anti-tangent-shunt

Routes I/O-heavy implementer work to a cheap worker model, keeping the
implementer's context small and token-efficient.

This plugin provides PreToolUse hooks that intercept oversized Read and Bash
calls, redirecting them to anti-tangent-mcp's `bulk_read` tool. The server
reads the files server-side and delegates the question to a cheap worker model,
returning only the answer. This keeps large file corpora out of the implementer's
context entirely.

The plugin requires the anti-tangent-mcp server to be installed and configured
with a `WORKER_MODEL` env var pointing to a cheap model (e.g. `claude-3-5-haiku`
or similar). The server must be launched before Claude Code can route calls.

## Installation

Add the plugin via the Claude Code marketplace:

```bash
claude plugin marketplace add patiently/anti-tangent-mcp
```

Then install the anti-tangent-shunt plugin from the repository:

```bash
claude plugin install anti-tangent-shunt@anti-tangent-mcp
```

## Dependencies

This plugin requires `jq` to be available in your PATH for hook execution.

## Configuration

The plugin reads one env var to decide when to delegate a Read:

- `ANTI_TANGENT_SHUNT_MIN_LINES` — threshold file line count (default: 350).
  Reads of files exceeding this limit are routed to `bulk_read`; smaller reads
  proceed normally.

The server configuration is separate: the anti-tangent-mcp server must be
started with a `WORKER_MODEL` env var set to the cheap model to use for
delegated work. The server routes the actual questions to that model.

## Running evaluations

The plugin includes a test suite for the hook routing logic. To run it:

```bash
bash plugin/anti-tangent-shunt/evals/run.sh
```

The suite includes:
- Hook decision tests: verifies Read and Bash hooks correctly identify files to
  block and delegate.
- Operational-reference check: ensures no leftover references to the upstream
  delegation target (Portal/AiKA) survive in the port, outside of allowed
  attribution and documentation files.

## Benchmarks

Upstream reports these figures against a 162K-line **Java** monorepo, delegating
through Portal/AiKA (read from `spotify/portal-ai-plugins@3c24ca30ff63e1f5bbad1c43fe5324daff579123`,
`plugins/shunt/README.md` — the accompanying blog post returns HTTP 403 from
this environment and was not read):

| Scenario | Lines | Without | With | Savings |
|---|---|---|---|---|
| Single large file | 4,014 | 33,684 | 5,737 | 82% |
| Source + test pair | 7,408 | 75,990 | 4,148 | 94% |
| Multi-file cross-service | 1,281 | 16,221 | 821 | 94% |
| Code-write | 3,667 | 40,614 + generation | 833 lines to disk | — |

Ours, against `prometheus/prometheus@7f48230f675e7c459398bf1d0f055f6f55caf90a`
(~152K lines of **Go**), delegating through `openai:gpt-5.6-luna`:

| scenario_id | Scenario | Lines | Without | With | Unit | Savings | Worker in | Worker out | Runs | Stat |
|---|---|---|---|---|---|---|---|---|---|---|
| single_large_file | Single large file | 4,984 | 42,993 | 36 | tokens | 100% | 46,001 | 558 | 3 | median |
| source_test_pair | Source + test pair | 7,624 | 54,127 | 373 | tokens | 99% | 61,127 | 1,236 | 3 | median |
| multi_file_cross_package | Multi-file cross-package | 1,302 | 10,372 | 74 | tokens | 99% | 10,864 | 357 | 3 | median |
| code_write | Code-write | 3,142 | 28,634 | 199 | lines | — | 29,325 | 2,315 | 3 | median |

"Without" is what a direct `Read` would put in the implementer's context;
"With" is the returned answer. **Worker tokens are shown separately and are not
netted off** — the claim is about implementer context, and combining the two
would overstate it. Method, per-scenario file lists, verbatim prompts, and all
raw runs: [`evals/benchmarks.md`](evals/benchmarks.md).

## Acknowledgements

The hooks in this plugin are adapted from Spotify's
[shunt](https://github.com/spotify/portal-ai-plugins/tree/main/plugins/shunt)
plugin (Apache-2.0), described by Dimitri Mazmanov in
["Portal by Spotify cut my Claude Code token usage by 90%"](https://engineering.atspotify.com/2026/9/portal-by-spotify-cut-my-claude-code-token-usage-by-90)
(Spotify Engineering, September 2026). Their hook-gated routing, the 350-line
threshold and the explicit non-delegation list are carried over; the
Portal/AiKA transport is replaced by anti-tangent's own provider layer. See
[`THIRD_PARTY_NOTICES.md`](../../THIRD_PARTY_NOTICES.md).
