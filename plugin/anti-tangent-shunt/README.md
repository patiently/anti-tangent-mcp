# anti-tangent-shunt

Routes I/O-heavy implementer work to a cheap worker model, keeping the
implementer's context small and token-efficient.

This plugin provides PreToolUse hooks that intercept oversized Read and Bash
calls, redirecting them to anti-tangent-mcp's `bulk_read` tool. The server
reads the files server-side and delegates the question to a cheap worker model,
returning only the answer. This keeps large file corpora out of the implementer's
context entirely.

The plugin requires the anti-tangent-mcp server to be installed and configured
with an `ANTI_TANGENT_WORKER_MODEL` env var pointing to a cheap model (e.g.
`anthropic:claude-haiku-4-5-20251001`; see the root README's "Model tiers"
table for other allowlisted options). The server must be launched before
Claude Code can route calls.

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

The plugin reads one env var to decide when to delegate a read — both the
Read hook (`check-file-size`) and the Bash hook (`check-bash-read`, which
catches `cat`/`head`/`tail`/`less`/`more` on the same files) use it:

- `ANTI_TANGENT_SHUNT_MIN_LINES` — threshold file line count (default: 350).
  Reads of files exceeding this limit are routed to `bulk_read`; smaller reads
  proceed normally.

The server configuration is separate: the anti-tangent-mcp server must be
started with an `ANTI_TANGENT_WORKER_MODEL` env var set to the cheap model to
use for delegated work (e.g. `anthropic:claude-haiku-4-5-20251001` — see the
root README's "Model tiers" table for the full allowlist). The server routes
the actual questions to that model.

### Bash hook: count-flag behaviour

`check-bash-read` does **not** treat a count flag as evidence of a targeted
read, despite what the design doc says — the shipped behaviour, pinned by
`bash-hook-evals.json`, is:

- `head -100 large.txt` — **blocked**. `-100` is stripped as a flag, so the
  parser still finds `large.txt` as the path and measures it.
- `head -n 5 large.txt` — **allowed**, but not on purpose: `-n` is stripped as
  a flag and `5` — not `large.txt` — is taken as the path, a file that
  (almost always) doesn't exist, so the hook falls through to allow. This is
  a parser bug inherited from upstream, documented in `bash-hook-evals.json`
  case `head-n-space-count` as "Parser bug", not a deliberate carve-out for
  count flags.

This hook is a pinned, byte-for-byte port of upstream's own eval-derived
parsing rules (see `THIRD_PARTY_NOTICES.md`); its header explicitly warns
against "improving" it, since doing so risks the accumulated upstream eval
cases it must keep passing. So this behaviour is documented here rather than
changed. (The project's own design doc currently asserts the opposite of the
`-n 5` case; that is a documentation defect in the design doc, not in this
hook or its evals.)

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
| single_large_file | Single large file | 4,984 | 42,993 | 29 | tokens | 99.9% | 46,002 | 560 | 3 | median |
| source_test_pair | Source + test pair | 7,624 | 54,127 | 373 | tokens | 99% | 61,127 | 1,236 | 3 | median |
| multi_file_cross_package | Multi-file cross-package | 1,302 | 10,372 | 74 | tokens | 99% | 10,864 | 357 | 3 | median |
| code_write | Code-write | 3,142 | 28,634 | 199 | lines | — | 29,325 | 2,315 | 3 | median |

**The two tables are not directly comparable, and ours being higher does not
mean this port is better.** Different language and corpus (Go/Prometheus vs
Java), different worker model and transport, and — the part that moves the
number most — a different question. "With" is the size of the answer to the
prompt *we* chose; a narrower question returns fewer tokens and scores higher
without anything being more efficient. Read each table as that stack's result
under its own method, never as a head-to-head.

"Without" is what a direct `Read` would put in the implementer's context;
"With" is the returned answer. **Worker tokens are shown separately and are not
netted off** — the claim is about implementer context, and combining the two
would overstate it. Method, per-scenario file lists, verbatim prompts, and all
raw runs: [`evals/benchmarks.md`](evals/benchmarks.md).

## License

This plugin's declared licence is `MIT AND Apache-2.0`, not a plain `MIT`: the
two hooks, `evals/run.sh`, `hooks/hooks.json` and both eval fixture suites are
adapted from Spotify's Apache-2.0 `shunt` plugin, while the README and the two
skills under `skills/` are original and MIT-licensed. See
[`THIRD_PARTY_NOTICES.md`](../../THIRD_PARTY_NOTICES.md) for exactly which
files carry which licence, and the attribution below.

## Acknowledgements

The hooks in this plugin are adapted from Spotify's
[shunt](https://github.com/spotify/portal-ai-plugins/tree/main/plugins/shunt)
plugin (Apache-2.0), described by Dimitri Mazmanov in
["Portal by Spotify cut my Claude Code token usage by 90%"](https://engineering.atspotify.com/2026/9/portal-by-spotify-cut-my-claude-code-token-usage-by-90)
(Spotify Engineering, September 2026). Their hook-gated routing, the 350-line
threshold and the explicit non-delegation list are carried over; the
Portal/AiKA transport is replaced by anti-tangent's own provider layer. See
[`THIRD_PARTY_NOTICES.md`](../../THIRD_PARTY_NOTICES.md).
