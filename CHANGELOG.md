# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.21.0] - 2026-09-11

### Security

- `anti-tangent-guard`'s close-time hook no longer executes commands named by the
  repository it is pointed at. Deriving added lines for a `final_files` completion used
  `git diff HEAD -- <path>`, which must convert the worktree side and so runs a
  `filter.<name>.clean` or `filter.<name>.process` command from that repository's
  `.git/config`. The comparison now reads the raw `HEAD` blob with `git cat-file blob`
  and diffs in-process, converting nothing.
- The same hook no longer reaches a second, unrelated exec path: a blob missing from a
  partial clone sent git down a lazy fetch that spawned `core.sshCommand`,
  `remote.*.uploadpack`, or a `git-remote-*` helper. Git is now invoked with
  `protocol.allow=never`, `GIT_ALLOW_PROTOCOL=none` and `GIT_NO_LAZY_FETCH=1`.
  `protocol.allow` alone was not enough: git documents it as the default for schemes
  that carry no `protocol.<name>.allow` of their own, so a scanned repository could
  grant its own scheme and spawn the transport anyway — measured firing on git 2.43.0
  against a partial-clone fixture setting `protocol.ssh.allow=always`. That left
  `GIT_NO_LAZY_FETCH` holding the line alone, and it is read only from git 2.39.4, so
  older toolchains (Apple Git-146 ships 2.39.3) had no working defence.
  `GIT_ALLOW_PROTOCOL` overrides configuration outright, per-scheme keys included.

### Fixed

- A `*`-led line inside a raw string literal — a Go backtick string, a Kotlin
  triple-quoted string — is no longer scanned as a block-comment continuation, so a
  changelog or help text embedded in source stops refusing its own write. The scanner
  now decides from the surrounding file whether a block comment is actually open.
- `${...}` reopens code only in the languages whose backtick literals interpolate —
  `.js`, `.jsx`, `.ts`, `.tsx`. A Go raw string is uninterpreted, so a comment written
  between its backticks is string content and is left alone.
- A backslash escapes the next character only in those same interpolating languages. In
  a Go raw string a backslash is literal text, and treating it as an escape meant a raw
  string ending in one — a Windows path, the idiomatic reason to use Go backticks — ate
  its own closing backtick and hid every `/* */` block after it in the file.
- A backtick opens a string span only in `.go` and the interpolating extensions, the
  ones that actually have the literal. Rust, C, C++, Java and Kotlin do not, so an odd
  backtick in their prose — markdown in a doc comment, a byte of an `r#"…"#` or `R"(…)"`
  raw string — no longer swallows the rest of the file.
- A submitted path is matched literally. `git ls-files` takes a pathspec, so a file
  actually named with `?`, `*` or `[...]` matched its own siblings, and the first record
  of the answer was read — comparing the submitted file against a different file's blob
  and reporting its pre-existing comments as newly added. Both pathspecs now carry
  `literal`.
- A path carrying a `filter` attribute (git-lfs, git-crypt) is skipped rather than
  reported as wholly new, and a `working-tree-encoding` path is decoded with its
  declared codec instead of being misread as UTF-8.
- A `HEAD` blob carrying bytes that are not valid UTF-8 is now decoded with replacement and
  scanned. Previously the decode failure made the whole comparison unanswerable and the file was
  silently skipped, so a file with one stray byte was never checked. A blob that cannot be
  retrieved, exceeds the read cap, or declares a codec Python cannot resolve is still skipped —
  deliberately, rather than guessed at.

### Added

- `fp-scan.py --ticket-pattern <regex>` measures what a candidate
  `ANTI_TANGENT_TICKET_PATTERN` would newly flag in this repository, and refuses to run
  silently when the pattern would be dropped for being invalid or over the length cap.

## [0.20.1] - 2026-09-11

Two CI checks reported green without having tested what they claim to test
([#73](https://github.com/patiently/anti-tangent-mcp/issues/73)).

### Fixed
- **The `anti-tangent-shunt` eval suite no longer inherits `ANTI_TANGENT_SHUNT_MIN_LINES` from the shell that runs it.** Both hooks read that variable and every fixture is sized against its default of 350, so an exported value decided each case that sets no value of its own: `=50` failed four allow cases, `=100000` failed sixteen block cases, and a value that happened to agree with a case passed it for the wrong reason. `evals/run.sh` now unsets it before the suites run, and CI runs the suite twice more under hostile values (`1` and `100000`) so the isolation cannot quietly regress. The plugin moves to 0.1.3; its hooks are unchanged.
- **The docs link check validates `#anchor` fragments.** It confirmed a link's file existed and discarded the fragment, so a link left pointing at a renamed heading passed. Each fragment is now matched against the ids GitHub generates for the target file's headings (lowercased, punctuation and emphasis markers dropped, spaces to hyphens, `-1`/`-2` on repeats, code spans kept verbatim), plus any explicit `<a id>` or `<a name>`. Nothing inside a code fence, an inline code span or an HTML comment counts as a heading, an anchor or a link. A fence or comment still open at the end of a file is reported, since everything after it renders as code or not at all.
- **The same check covers every tracked markdown file**, not only `INTEGRATION.md`, `README.md` and `docs/protocol/`: `docs/team-setup/`, `docs/feedback/`, `plugin/*/`, `CLAUDE.md` and the rest were outside its reach for file existence as well as anchors. Two areas stay out. `docs/superpowers/` holds dated specs and plans, each a record of the tree as it stood when written. The example notes under `examples/` link with Basic Memory permalinks and template placeholders, not repository paths; `examples/project-knowledge/README.md` is ordinary prose and is checked. The link check moves to `scripts/check_md_links.py`, which carries its own tests; `scripts/check-protocol-docs.sh` runs the tests and then the check, and a passing run prints how many links and anchors it checked.

## [0.20.0] - 2026-09-10

Closes the ten comment-hygiene and completion-gate gaps reported in
[#71](https://github.com/patiently/anti-tangent-mcp/issues/71). Design:
`docs/superpowers/specs/2026-09-10-anti-tangent-v0.20.0-design.md`.

### Added
- **`ANTI_TANGENT_TICKET_PATTERN`** (`anti-tangent-guard`) — an optional per-project regex for your tracker's key shape, so `// ABC-1234: …` is caught. The tell is added when `comment_scan` is imported, so **both** hooks honour it: the write-time `Edit`/`Write` guard and the close-time scan over a completion's submitted evidence. There is deliberately no default: a generic pattern matches hardware identifiers and prose labels far more often than tracker keys, and the write-time hook blocks writes. **Without it set, the tracker-key shape reported in #71 is still not detected.**
- **`skip_evidence` on the `codescene` argument** — the failing tool's own error text, which is what now separates a checkable CodeScene skip from an unverifiable one.
- The write-time scanner reads **three comment shapes it previously could not**: a starred block continuation (a KDoc or Javadoc body line), a `/* … */` opened and closed on one line, and a comment after code on the same line. Implemented as an opener extension plus a quote-parity test that declines any delimiter whose preceding quotes are unbalanced. An *unstarred* block interior is a known miss — a line-at-a-time scan cannot tell that such a line sits inside a comment — and is covered by the reviewer's own comment rule whenever a diff is present. The parity test is conservative for the single-line quoting shapes it models, not a general lexer. The starred-continuation shape has no parity test of its own — the prefix before a line-leading `*` is always whitespace, so a markdown bullet inside a Go raw string or a Kotlin `"""` block can be misread as a comment continuation and block the write; set `ANTI_TANGENT_COMMENT_GUARD=0` to disable the scan if this happens.
- The close-time hook scans completions that submitted **`final_files` instead of a diff**, asking git which lines are new. Rooted at each file's own directory, so a nested worktree resolves correctly. **git reports a line as new only while it is uncommitted or its file is untracked**, so a task whose work is already committed by the time it closes — the usual shape under per-task commits — is still not scanned this way; submit a diff from the task's base commit (`git diff <task-base-commit> -- <task paths>`) to have those lines read.
- `validate_plan` flags change-history comments inside normative code fences, which is where the transcribed-into-the-repository ones come from.
- `validate_plan` emits a plan-level `major` (`criterion: comment_policy_absent`) when a plan gives its implementers neither the comment policy nor a pointer to `implementer.md` §4.4. Without it, an implementer working without the protocol plugin loaded is told nothing and spends a review round discovering the policy. One line satisfies it, and the finding's suggestion gives that line.

### Changed
- **`ANTI_TANGENT_CODESCENE=required` now grades a skip with no `skip_evidence` as a `major`**, exactly as it grades saying nothing; a `skip_reason` paired with `skip_evidence` still draws a `minor` — the severity a bare `skip_reason` used to buy on its own. Previously any non-empty `skip_reason` alone bought that same `minor`, which made composing a sentence the cheapest route to a pass. `required` is an operator assertion that CodeScene is present on the host, and a task being lightweight is not a skip reason. Operators who have not set the variable see no change.
- **`validate_completion` emits a `major` when `test_evidence` states that no test executed** — `NO-SOURCE`, pytest's "no tests ran", jest's "No tests found", Go's `[no test files]`. Cached and up-to-date output does **not** fire: Gradle marks a task `UP-TO-DATE`/`FROM-CACHE` only after a successful prior run and Go caches only passing results, so those attest a pass.
- Those two findings **stack**. One major alone yields `warn`, and the close-time hook blocks only on `fail` — but a lightweight task under `required` that skips CodeScene without evidence *and* pastes a no-execution test run now produces two majors, a `fail`, and a blocked close where it previously closed clean.
- **The protocol parts under `docs/protocol/` no longer carry parenthetical `(vX.Y.Z+)` availability markers.** `README.md` is the version-facing reference and keeps them, including the note-type availability table. A protocol part is loaded per task by an implementer working with whatever version the operator installed, so a bare marker there is something it cannot act on, and the parts are held under a hard 16,000-byte ceiling. **Load-bearing version conditions stay** — a sentence whose meaning *is* the condition: `controller.md`'s note that an older server emits no `tool:` tag (which explains a real guard misfire), a deprecation naming the release that removes the thing, or a layout named after the version that defined it. The rule is about the marker shape, not about naming a version.
- **The close-time hook's two kill switches now each govern only the concern they name.** `ANTI_TANGENT_COMPLETION_GUARD=0` disables the completion-gate check and `ANTI_TANGENT_COMMENT_GUARD=0` disables the comment-hygiene scan over a close's submitted evidence — and setting `ANTI_TANGENT_COMMENT_GUARD=0` on its own already worked correctly before this release. What changes: previously, `ANTI_TANGENT_COMPLETION_GUARD=0` on its own silently skipped comment scanning too, even though the comment-hygiene block's own message pointed to `ANTI_TANGENT_COMMENT_GUARD` as the switch for that. Setting *both* to `0` is now what's required to silence the close-time hook entirely.

### Fixed
- `validate_plan` no longer reports an existing file as missing when a `Files:` bullet anchors more than two line numbers (`docs/x.md:60,166,174,419`). The anchor pattern permitted only one separator per group, so it matched the first pair and then failed the whole anchor, leaving the digits attached to the path. `a.kt:27-30,40-50` was broken the same way.
- The no-execution `test_evidence` check recognises gotestsum's `DONE 42 tests in 1.2s` summary as evidence a suite ran. `gotestsum --format testdox` — what this project's own CI runs — prints no `ok pkg` line and no per-test counts, so pasting its output for a build containing one package with no test files had nothing to suppress the finding.
- `plan_run_report` fits a CodeScene skip evidence into 200 runes of its table cell: 199 of the evidence and an ellipsis marking the cut. The field is capped at 2,000 runes on the way in, which is several screens of one cell in a table whose other columns are capped at 40; the ledger still records the whole of it.
- The close-time scan divides a submitted diff into files by the line counts each hunk declares for itself. An added line whose content starts with `++ ` reaches the parser as the bytes `+++ `, byte-identical to a file header, and nothing about the line alone separates the two: reading every one as a header attributed a file's added lines to a path nobody wrote, and reading every one inside a hunk as content swallowed the next file's genuine header, dropping that whole file from the scan. Two rules divide it. `@@ -a,b +c,d @@` states how many lines the hunk holds: a hunk still owing **more than one** added line when a `+++ ` runs straight into the next `@@` was cut short, and is closed there rather than swallowing the rest of the diff, while a hunk owing exactly one is what `git diff -U0` produces at every hunk boundary, so that line stays body text and its file is not split. No count can settle a hunk that over-declares by a single line, so a second rule carries that case: a `--- ` line immediately above a `+++ ` line is a file header pair whatever the open hunk still owes, which is how every genuine file boundary in a unified diff is written. **Three shapes are read wrong**, and the parser docstring states each of them while eval cases 137-139 pin each at its actual behaviour: a hunk that declares FEWER added lines than it delivers, where a genuine body line is read as a header; a hunk that declares MORE of them under a bare `+++ ` header with no `--- ` above it and no `@@` after it, where a genuine header is read as body; and — the price of the header-pair rule, and the only one of the three that misreads a well-formed diff — ordinary source text carrying a removed line starting `-- ` immediately above an added line starting `++ `. A real diff of a diff does not trip it: git writes those with four markers. Each lands the added lines that follow on a path the diff body named rather than the one it meant, so where that path has no scannable extension the scan goes silent, and where it has one the close is blocked against a file the diff never touched.
- The close-time comment scan reads a submitted diff **whatever path prefix it carries**. It required `+++ b/`, so a diff produced under `diff.noprefix` or `diff.mnemonicPrefix` — the developer's own git configuration, not anything the caller chose — had every added line discarded, and the close passed exactly as though the scan had run and found nothing.
- The close-time trace line now records what the scan looked at: `scan | src=final_files submitted=3 scanned=0 lines=0`. A submission with nothing scannable in it was previously indistinguishable from a clean scan, on both the exit code and the log. Either budget running out appends `budget-exhausted`, a vendored skip appends `vendored-skipped=N`, and a `129` from git — its generic usage error, returned both by a git that does not know `--no-optional-locks` and by a command line malformed any other way — makes the walk drop the flag and refresh the index it meant to leave alone, appending `optional-locks-dropped`.
- The close-time scan is bounded across a whole completion, not only per file: twenty seconds over the git questions and twenty over the scans, with anything a budget cuts short recorded on the trace instead of passing as clean. A completion naming many files against a stalled git could hold the session for minutes.
- An untracked path under `vendor/`, `third_party/` or `node_modules/` is not scanned. Every line of an untracked file counts as added, so a vendored file carrying its upstream history in a header would block the close over code the repository does not own. The directory names are matched against the path *relative to the repository root*, so a checkout that itself lives under one of them keeps its own files scanned, and any path the exemption drops is counted on the trace.
- A close that both carries a change-history comment and failed its verdict now reports both conditions in one message, rather than surfacing the verdict only after the comment is rewritten.
- The close-time hook refuses to load its scanner from a relative or empty `ATG_ROOT` — that would put a working-directory-relative entry first on `sys.path`, the shadowing `-I` exists to prevent — and traces `comment-scan-unavailable` instead. Its git calls also pin `core.fsmonitor` off and pass `--no-textconv`, so a repository config cannot get a command of its own choosing run inside the hook. They also pass `--no-optional-locks` where git accepts it — a git that does not know the flag rejects the whole command line and answers `129`, its generic usage error, rather than naming the flag, so the first such rejection drops it and retries rather than letting every git question fail as an empty scan.
- `evals/run.sh` passes `-B` to its unit-test step, so running the evals no longer writes `__pycache__` into the plugin tree.
- **`anti-tangent-shunt` 0.1.2** — its `code-writer` skill described the close-time comment scan as diff-only. It now states the real coverage: a `final_files` completion is read through git, and a generated file's comments are seen only while they are uncommitted or the file is untracked.
- The `anti-tangent-guard` eval harness (`plugin/anti-tangent-guard/evals/run.sh`) no longer inherits the invoking shell's `ANTI_TANGENT_TICKET_PATTERN`, `ANTI_TANGENT_COMPLETION_GUARD` or `ANTI_TANGENT_COMMENT_GUARD`. A case with no `env` block of its own used to see whatever the developer's or CI's shell happened to have exported instead of the documented default, so a switch-combination case could produce a false pass as easily as a false failure. Anyone who ran that suite with any of the three set in their own environment may have been reading a result about their shell, not the code — `ANTI_TANGENT_TICKET_PATTERN` included, which is the variable this same release asks operators to export per project.

### Security
- `test_evidence` reaches the reviewer inside the same untrusted-data fence as `final_diff` and `final_files` do. It was the one evidence field interpolated at instruction level, so text an implementer composed could read to the reviewer as an instruction rather than as evidence to weigh. All three fences are chosen from the value they quote — a run of backticks one longer than the longest run inside it, and never fewer than four — so evidence that carries a fence of its own cannot close the block quoting it and leave the remainder arriving as prompt.

## [0.19.0] - 2026-09-09

### Added
- **A comment-hygiene policy, enforced at write time and backstopped at task close.** Comments
  may explain non-trivial behaviour, or a non-obvious invariant or hazard that would bite the next
  editor — the test is that they read correctly to someone who never saw the change. They may not
  carry change history: issue, PR or task references, version references, review references, or
  narration of what the code used to do. Git already holds that, and a comment repeating it goes
  stale on the next change. A comment failing these criteria is removed or rewritten by whatever
  task next touches it; there is no big-bang cleanup.

  Enforced in three layers, split by what each can actually decide and what each can see.
  **Prevention** — a `PreToolUse` hook on `Edit`/`Write` in `anti-tangent-guard`
  (**0.1.0 → 0.2.0**) refuses the write outright before a violating comment reaches disk. It is
  the only layer that prevents rather than detects, and the only one whose reach does not depend
  on transcript visibility or evidence shape. **Review** — `validate_completion`'s reviewer
  already receives the full diff, whichever agent submitted it, and gains a rule emitting
  `category: quality` / `criterion: comment_hygiene` findings **pinned to `severity: minor`** —
  `quality` is not severity-floored server-side, so an unpinned rule would let two comment nits
  become a `fail` and hard-block the close. This reaches every path that submits a diff to
  `validate_completion`, regardless of which agent later closes the task, but only when a diff is
  actually present in that call — `final_files`-only or `test_evidence`-only evidence gets no
  comment-hygiene review from this layer. No new finding category and no schema change.
  **Detection** — the same `check-task-complete` hook that already mandates the completion gate
  scans the added comment lines of the last `validate_completion` call's diff for the unambiguous
  mechanical tells (issue/PR references, version references, task/review references) and blocks
  the close with the existing reopen-fix-revalidate flow, catching a comment that reached disk
  through `Bash` or another path prevention cannot intercept. It is defence in depth, not the
  primary gate: it fires on the *closing* agent's transcript, so on the subagent-driven path —
  where a subagent validates and the controller closes — it sees only the pasted `summary_block`
  and no diff. Prose-history tells like "previously" and "used to" are deliberately left out of
  both scans: they cannot be matched without false positives, so review is what catches them.

  Every scan (prevention and detection) is restricted to **added** lines in source files. `.md`
  and config files are excluded: a changelog entry legitimately carries issue and version
  references, and this repo requires one in every change. `Write` over an existing file is diffed
  against what is on disk, so rewriting a file does not demand cleanup of every comment already in
  it that stays byte-for-byte unchanged — moving an untouched line is not a touch. A comment line
  whose bytes change is treated as added and scanned, and that includes a pure re-indent: changing
  a line's bytes is touching it, even when the change is only whitespace. Kill switch
  `ANTI_TANGENT_COMMENT_GUARD=0` disables both the prevention and detection hook layers while
  leaving the completion-gate check active. As with the completion guard, the blocking half here
  is a Claude Code plugin the operator installs and can disable — the MCP server itself remains
  advisory and never blocks.

- **`criterion_counts` on stats events.** `stats.Event` recorded `category_counts` but nothing
  finer, so a `quality` finding about a comment was indistinguishable from any other `quality`
  finding and the comment policy's effect could not be measured. Counts come from an **allowlist
  of server-recognised criterion sentinels**, never from raw criterion text: `pre.tmpl` tells the
  reviewer to quote verbatim acceptance-criterion text as the criterion, and the ledger has until
  now held no free text at all — every string in it is a bounded enum or a hash. Recording raw
  criterion would have written task-specification text to disk and given the map one key per
  acceptance criterion ever reviewed.

- **A re-runnable zero-false-positive gate for the comment-hygiene scanner**, under
  `plugin/anti-tangent-guard/evals/`. `fp-scan.py` runs the shipped scanner over every tracked
  source file's HEAD blob — skipping vendored third-party bundles, whose comments nobody here can
  rewrite — and treats every comment line as added; `fp-class.tsv` records what each hit is;
  `fp-report.sh` joins the two on path plus comment text, strictly in both directions, rejecting
  unclassified, unknown, duplicate and miscounted entries before counting, and fails unless the
  false-positive count is zero. Keying on the text rather than on a line number means an unrelated
  commit that shifts a classified comment does not turn CI red, while a rewritten one still
  surfaces as both an unknown classification and an unclassified hit. Wired into CI, so widening a
  tell can no longer quietly reopen a false positive.

### Fixed
- **`validate_task_spec` now reserves `major` for ambiguity that would actually cause
  misimplementation.** Across 191 real calls, the tool returned `fail` 58% of the time and `warn`
  a further 32%, and **81% of those non-passes were driven by `major` findings** — overwhelmingly
  `ambiguous_spec` (522 of roughly 800 findings). The cause was calibration, not the severity
  ladder: `pre.tmpl` asked the reviewer to enumerate every implicit assumption in a brief, said
  nothing about how severe an assumption is, and defined `major` as "a competent implementer
  would still misimplement it" — which is exactly how an enumerated assumption reads. `post.tmpl`
  has had a calibration clause guarding against this since it was written; the pre-hook prompt
  had no analogue. It does now, along with a bound on the assumptions clause so related
  assumptions consolidate into one finding instead of one finding each.
- **`Context:` is now authoritative for the pre-task reviewer.** It was already rendered into the
  pre-hook prompt, and `post.tmpl` already told the post reviewer to treat it as the
  disambiguator — but nothing told the *pre* reviewer the same. A task brief that answered a
  question in `Context:` (including in verbatim code) was still failed for leaving it open in the
  acceptance criteria above. This was the shape of one of the two field reports in
  [#58](https://github.com/patiently/anti-tangent-mcp/issues/58): every finding on a fully
  specified task was about prose the brief's own code answered directly below it. The
  contradiction rule that comes with it is scoped to match: only an irreconcilable AC/`Context:`
  pair, where nothing in the spec indicates which side governs, is a `major` `ambiguous_spec`.
  Where `Context:` explicitly anticipates or approves a deviation from an AC's literal wording,
  the spec is coherent and the pre reviewer emits nothing — the same call `post.tmpl` already
  makes about that shape, so a task can no longer draw a `major` before implementation for
  precisely what the final review is told to accept.
- **The pre-task gate's terminal state is documented.** `implementer.md` §4.2 told an implementer
  to treat `critical` as blocking and `major` as address-or-explain, and stopped — leaving no
  stopping rule for a `warn` that will not move. It now states one.
- **The guard's trace log now says which session wrote each line, and stops growing forever.**
  Every hook wrote to one shared `/tmp` path with no identity in the line, so on a machine running
  more than one Claude session the log could not answer the only question it exists to answer —
  which hook fired, for whom. Lines now carry a short session id (`-` where the payload has not
  been read yet), and the file rotates to a single `.1` sibling past
  `ANTI_TANGENT_GUARD_TRACE_MAX_BYTES`. Both are best-effort: a trace failure never changes a
  hook's exit status. `ANTI_TANGENT_GUARD_TRACE_LOG` still repoints the path for anyone wanting
  per-session isolation.
- **A large `Write` no longer slips past the write-time guard.** The hook handed its payload to the
  Python body as an environment variable, and Linux caps a single environment string at
  `MAX_ARG_STRLEN` (32 pages, 131072 bytes on x86-64) — so a `Write` of a large file made `exec`
  fail, which the wrapper read as an internal error and allowed. The payload travels on stdin
  now, so payload size is no longer a ceiling. A `Write` over an existing file that is not valid
  UTF-8 no longer crashes the scan into a silent allow either: the content is read in binary and
  decoded with `errors="replace"`, matching the close-time scan exactly. That read stays capped
  at 2,000,000 bytes, and a target above it is still skipped — deliberately, and now traced as
  `skip | unreadable-target` rather than logged as a clean pass.
- **Both guard hooks harden the paths they open and the log they write.** The three guards the
  server applies to a caller-supplied path — `O_NOFOLLOW` refusing a symlink at the final
  component, `O_NONBLOCK` keeping a FIFO from parking the hook inside `open()`, and an `S_ISREG`
  check rejecting everything else — now live in one helper in `comment_scan.py` that both hooks
  call, alongside the byte cap and the replacing decode, so the write-time `Write` target and the
  close-time `final_diff_path` cannot drift apart again. Every one of those refusals fails open.
  Both hooks run their Python under `python3 -I`, so neither a `json.py` in the working directory
  nor a `PYTHONPATH` entry can shadow the standard library inside a hook that runs unsandboxed,
  and neither writes `__pycache__` into an installed plugin tree. The trace directory is created
  mode `700`, neither hook appends through a symlink at the log path, and both trace sinks strip
  CR, LF and `|` out of every field they are handed — a task id, status or tool name carrying a
  newline could otherwise split one log call into two physical lines, the second reading as a
  genuine record. Those checks stay best-effort and never change an exit status. Matched comment
  lines echoed into either hook's block message are truncated, so one very long line cannot reach
  the model as megabytes of hook stderr.
- **`criterion_counts` now reaches `rollup.json`, the LLM summary and the tray.** The per-event
  counts were written but never aggregated, so `criterion_histogram` — and with it the
  comment-hygiene numbers in the LLM summary — did not exist. The summary prompt's topic list now
  names the per-criterion counts, so the model is asked about the data it is handed rather than
  left to volunteer it. The gnome-topbar stats page decodes the key and renders it as a "Criteria"
  table beside Severity and Categories; a `rollup.json` without the key still decodes cleanly and
  simply omits the section. **That tray half does not ship in this release.**
  `gnome-topbar/daemon` is a separate Go module with its own release workflow, triggered by
  `gnome-topbar-v*` tags, and the server's release workflow ignores `gnome-topbar/**` — so the
  "Criteria" table reaches users with the next `gnome-topbar-v*` tag, not by merging this one.
  Criterion lookups also normalise case and
  surrounding whitespace before the allowlist test: criterion is free reviewer text, and a
  reviewer writing `Comment_Hygiene` silently vanished from the metric.
- **The write-time guard skips an unscanned file without starting an interpreter**, deciding from
  the extension in the wrapper. It also traces the allow path, so the trace log records every
  decision as its documentation says. `check-comment-write`'s copy of the extension list and
  `comment_scan.py`'s `SCAN_EXTS` are asserted identical by the eval suite; an extension in only
  one of them would be silently unguarded.
- **The version tell no longer backtracks quadratically** over a long whitespace run: its optional
  preposition bridge gave the whitespace two greedy owners with an optional group between them.
- **`README.md` and the marketplace catalog description described the guard as a single
  close-time hook.** Both now name the `PreToolUse` write-refusal hook and both kill switches,
  so installing from the README no longer produces a hook that rejects edits with no warning it
  exists. The guard's own limitations list gains `code_write`, which writes server-side and so
  bypasses the `Edit`/`Write` matcher exactly as `Bash` writes do — and whose generated code never
  enters the agent's context to be self-reviewed either. The `code-writer` skill says so where it
  recommends `target_path`, so the readers most likely to take that bypass see the caveat without
  reading the guard's README. `INTEGRATION.md`'s router table names §4.4, the write-time guard and
  §5.3's guard hooks in the rows that carry them, so an agent picking a protocol part by what it
  covers can find them. The CI protocol-byte budget warns at 15,500 as well as failing at 16,000:
  the binding file sits under 1% below the cap, where the first signal of a problem should not be
  a red build.
- **A write the guard could not scan is no longer logged as one it scanned and cleared.** A
  `Write` whose target is a symlink, a FIFO, a directory or a file past the read cap has no old
  text to diff against, so the scan is declined and the write allowed — correctly, and unchanged.
  But the wrapper traced that as `pass`, indistinguishable in the log from a clean scan. The body
  now reports it as its own status and the wrapper traces `skip | unreadable-target`. Only the
  trace line changes; the hook still exits 0 and still never blocks on it. The guard README gains
  a table of the write-time fail-open causes, which it previously documented only for the
  close-time hook.
- `plugin/anti-tangent-protocol` is version 0.2.1 and `plugin/anti-tangent-shunt` is 0.1.1: both
  shipped changed content in this release — three protocol parts, and the `code-writer` skill's
  guard-bypass caveat — and nothing in the release workflow bumps a plugin, so both are bumped by
  hand here together with their `.claude-plugin/marketplace.json` entries.

No schema, tool-argument or envelope change: the verdict distribution shifts, but every public
type and field is byte-identical. The one addition is internal to the opt-in stats ledger —
`stats.Event` gains an optional `criterion_counts` map, omitted entirely when empty. Callers that calibrated against the observed distribution will see it
move, which is why this is a minor rather than a patch.

## [0.18.2] - 2026-09-08

### Fixed
- **The GitHub Release body is populated for real this time.** `.goreleaser.yaml` set
  `changelog.disable: true`, and GoReleaser loads `--release-notes` *inside* the changelog pipe —
  `Skip()` returns `Changelog.Disable`, and `Run()` is what calls
  `loadContent(ctx.ReleaseNotesFile)`. With the pipe skipped, the notes file the release workflow
  writes was never read, so every release from **v0.11.0 through v0.18.1** published a bare
  newline no matter what the workflow passed. Disabling it was also unnecessary: `Run()` returns
  early once a notes file is supplied, so no git-log changelog is generated on top of ours.
  0.18.1's release-notes plumbing was a real improvement — it removed a fragile multiline
  job-output hop and logged the byte count, which is how this was finally diagnosed — but it was
  not the fix.
- **The release now asserts the outcome instead of the intermediate.** The previous guard checked
  that the notes *file* was non-empty, which was always true and stayed true through nine empty
  releases, because the file was never read. The workflow now reads back the *published* release
  body and fails if it is empty. Both guards test for the presence of non-whitespace content
  rather than a byte threshold — `wc -c` counts bytes while `jq '.body | length'` counts
  characters, so a threshold compared across the two could reject a body one guard had already
  accepted, and any count will happily pass twenty spaces.

## [0.18.1] - 2026-09-08

### Fixed
- **The GitHub Release body is no longer empty.** The release workflow extracted the matching
  `## [X.Y.Z]` CHANGELOG section in one job and passed it to the GoReleaser job as a multiline
  job output, where it arrived empty — so every release from **v0.11.0 through v0.18.0** shipped
  with a 1-byte body while the workflow reported success. The GoReleaser job now extracts the
  section from its own checkout, so only the short single-line `new_version` crosses the job
  boundary, and the step fails loudly if the result is empty instead of publishing a blank
  release. v0.18.0's body has been backfilled by hand.

### Added
- **`actionlint` runs in CI**, gating `build-test`. `release.yml` triggers only on `push` to
  `main`, so a pull request's CI never executes it and workflow changes went unvalidated until
  after they merged — which is how a shell comment containing an empty workflow expression (a
  parse error that can stop a workflow starting) passed every check on the PR that fixed the
  release notes. It was caught in review rather than by CI. `actionlint` also invokes
  `shellcheck` on every `run:` block, which surfaced and fixed an unquoted `$GITHUB_OUTPUT`
  redirect in `release.yml`.
- **The version-bump marker is read from the commit SUBJECT**, not the whole message. It was a
  bare substring match over the full commit body, so a merge commit whose body explained that it
  carried *no* marker — writing the two words in brackets to say so — was read as a major bump.
  The release failed validating a changelog entry for a version that did not exist. Nothing
  shipped (the tag, GoReleaser and Docker jobs were all skipped), but the release was blocked by
  prose describing the convention rather than invoking it. The documented convention is that the
  merge commit carries the marker, and a merge subject is the PR title, so the subject is where
  it is now read from; body prose can discuss the markers freely.

## [0.18.0] - 2026-09-08

### Added
- **`bulk_read` and `code_write`** — two MCP tools that route I/O-heavy implementer work to a
  cheap worker model, so a large file corpus and generated boilerplate never enter the
  implementer's context. `bulk_read` reads files server-side under the same
  `ANTI_TANGENT_PLAN_ROOTS` rules as `validate_completion` and answers a question about them;
  `code_write` generates code matching a required `reference_path` and, given a `target_path`,
  writes it and returns only a line count. The delegation pattern is inspired by Spotify's
  [shunt](https://github.com/spotify/portal-ai-plugins/tree/main/plugins/shunt) plugin, described
  in ["Portal by Spotify cut my Claude Code token usage by 90%"](https://engineering.atspotify.com/2026/9/portal-by-spotify-cut-my-claude-code-token-usage-by-90)
  — these two Go tools are original code, not derived from it.
- **`plugin/anti-tangent-shunt`** — two PreToolUse hooks that block oversized full-file reads
  (`Read`, and `cat`/`head`/`tail`/`less`/`more` via `Bash`) and point at `bulk_read`, plus
  bulk-reader / code-writer skills and a key-free eval suite. Adapted from Spotify's
  [shunt](https://github.com/spotify/portal-ai-plugins/tree/main/plugins/shunt) plugin
  (Apache-2.0) — see `THIRD_PARTY_NOTICES.md`.
- **`plugin/anti-tangent-guard`** — a PostToolUse hook on `TaskUpdate` that detects a
  `completed` close when `validate_completion` did not run, or ran and returned `fail`, and
  mandates a reopen-fix-revalidate recovery flow. `PostToolUse` fires after the state change
  has already landed, so the hook cannot refuse the close itself — this is post-close detection
  plus mandated recovery, not a block on the close. Active on install;
  `ANTI_TANGENT_COMPLETION_GUARD=0` disables it. Fails open on any error. Its summary-block pass
  signal and verdict read are scoped to blocks tagged `tool: validate_completion` — see the new
  `Envelope.tool` field below — so a `check_progress` or `validate_task_spec` block (rendered
  byte-identical otherwise) cannot satisfy the gate or have its verdict misread as
  validate_completion's. The hook reads that tag, and the verdict, positionally (the first
  `tool:`/`verdict:` line within a block, never a scan for the target value anywhere in it), and
  `formatEnvelopeSummary` escapes every continuation line of a finding's `Evidence`/`Criterion`
  or an envelope's `next_action` with a non-whitespace sentinel — so neither a forged tag nor a
  forged verdict smuggled through that reviewer-authored free text can be mistaken for the
  genuine header line. A direct `validate_completion` call's own MCP result is JSON-marshalled,
  so its summary block's newlines survive only as escapes inside one JSON string; the hook
  matches that result back to its call and parses the JSON directly for `verdict`, so a `fail`
  is caught on the direct-call path too, not only on a pasted marker block. Requires an
  anti-tangent-mcp server >= 0.18.0; an untagged block from an older server does not satisfy
  the guard.
- **`ANTI_TANGENT_WORKER_MODEL`** (defaults to `ANTI_TANGENT_MID_MODEL`) and
  **`ANTI_TANGENT_WORKER_MAX_TOKENS`** (4096, clamped by `ANTI_TANGENT_MAX_TOKENS_CEILING`).
  `ANTI_TANGENT_SHUNT_MIN_LINES` (350) is read by the shunt hooks, not the server.
- **`THIRD_PARTY_NOTICES.md`** — Apache-2.0 notice for the ported shunt hooks and eval suites.

### Changed
- `stats.Event` gains optional `input_tokens` / `output_tokens`; `rollup.json` gains an
  additive `worker` key. Existing keys are unchanged — the gnome-topbar consumer reads them
  by exact name.
- `Envelope` (the JSON returned by `validate_task_spec`, `check_progress`, and
  `validate_completion`) gains a `tool` field naming the MCP tool that produced it. Additive;
  existing fields are unchanged. `formatEnvelopeSummary` now emits a `tool:` line, which
  `plugin/anti-tangent-guard`'s hook requires to identify a `validate_completion` block.
- The paste-ready `summary_block`'s multi-line rendering now prefixes every continuation line
  with a non-whitespace `| ` sentinel instead of pure whitespace. The rule lives in the new
  leaf package `internal/blocktext`; `internal/mcpsrv/summary.go` applies it to the per-task
  envelope and to the `validate_plan` / `prime_project_knowledge` / `extract_project_knowledge`
  blocks, and `internal/planrun/report.go` applies it to `plan_run_report` — whose header is
  deliberately different (`anti-tangent plan run report`) and which was therefore missed on the
  first pass. Covered fields: `criterion`, `evidence`, `next_action`, `task_title`, a pick's
  `permalink`/`reason`, a proposal's `permalink`/`rationale`, the provenance and context-file
  paths, and, in the plan-run report, the `plan_run_id`, task title, verdict cell and the whole
  CodeScene cell (skip reason, quality gate, and the category-count map's keys). User-visible
  formatting change, intended: without it, a caller- or reviewer-authored line that happened to
  read `tool: validate_completion` or `verdict: pass` — or a bare `anti-tangent envelope`
  header, which starts a whole new block — could be mistaken for the block's own grammar by a
  downstream parser. Enum-typed fields are not escaped; the parsers in `internal/verdict` reject
  an out-of-enum value before a formatter sees it. This is hygiene for a machine-read format,
  not a security boundary: an agent that wants to skip `plugin/anti-tangent-guard`'s completion
  gate can compose a block in its own report text without going near these fields — see that
  plugin's README, "How much to trust each pass signal".
  `internal/mcpsrv/summary_forgery_test.go` enumerates the header-emitting formatters from the
  package's own source and drives a forged payload through every free-text field of each, so a
  new formatter or a new field cannot be added without escaping and stay green. See
  `plugin/anti-tangent-guard` above.
- The README's filesystem trust-model section now covers writes, not only reads, and states
  plainly that `bulk_read` sends the full contents of every path it reads (and `code_write` its
  `reference_path`) to the configured worker provider — the documented mechanism of both tools,
  not a new behavior, called out because it wasn't stated loudly enough for someone deciding
  whether to enable them.

### Fixed
- **`code_write`'s `overwrite: true` write is now atomic.** It previously opened the target with
  `O_TRUNC`, which emptied the file at `open(2)` before a single byte of the new content had been
  written — a failure partway through left the caller with an empty or half-written file and no
  way back. It now writes a temp file in the target's own directory and renames it over the
  target, so every failure before that rename leaves the original byte-identical to what it held
  before the call. The rename carries the target's file mode across but replaces its inode, so a
  hard link to `target_path` now points at the pre-write content after an overwrite, and
  ownership is not preserved across it (only the mode is). `overwrite: false` is unchanged
  (`O_CREAT|O_EXCL|O_NOFOLLOW`).
- **`target_path` is now refused on Windows**, rather than silently accepted with a gap. Unix
  refuses a symlink planted at the target's final path component between the containment check
  and the write, however it got there; Windows exposes no equivalent through Go's `syscall`
  package, so on that platform the same symlink/reparse point would be a static bypass, not a
  narrow race — and nothing in this repo builds or tests on Windows to validate a guard against
  it. `code_write` without `target_path` still generates the code and returns it as `code`, so
  the tool degrades gracefully rather than failing outright. See README's "Writes: `code_write`
  and `target_path`" for the full reasoning, including why the read path's Windows story is
  narrower and stays unchanged.

## [0.17.0] - 2026-09-02

### Added
- **`context_paths` on `validate_plan`.** Absolute paths to source files the plan makes claims
  about. The server reads them whole and renders them ahead of the plan, and the reviewer's
  ground rules are rewritten to enumerate exactly what is attached: for those files absence is
  evidence, and everything else stays black-box. The reviewer verifies codebase claims instead
  of emitting `unverifiable_codebase_claim` for each one. **Opt-in and materially expensive** —
  a 170KB plan with ~100K tokens of attachments runs roughly $1.31 per round on the default
  plan model, against cents without them. Attach only the files the plan actually touches.
  Oversized attachments are refused, never truncated: the reviewer is told attached files are
  complete, and a silently-shortened file would turn that promise into false findings.
- **`contradicted_codebase_claim`.** A new finding category for a plan claim an attached file
  refutes. When an attached file backs it, it carries no severity floor — the file is ground truth
  read from disk, so the reviewer's chosen severity stands — and it is neither rolled into the
  `codebase_reference_checklist` nor force-passed by the unverifiable-only verdict calibration. A
  contradiction the attached set does NOT back is demoted server-side to
  `unverifiable_codebase_claim`, and from there it is floored to minor, rolled up and force-passable
  like any other unverifiable claim. `docs/protocol/core.md` states the rule with that
  qualification.
- **Order-aware Create/Modify consistency check.** Deterministic and reviewer-free, with two
  independently-gated tiers rather than one combined AND check. Order tier (always runs): a
  `Modify:` target whose earliest `Create:` bullet in the plan belongs to a later task is
  flagged from plan text alone, regardless of what's on disk — an already-implemented worktree
  does not excuse a genuine ordering bug. Disk tier (needs `repo_root`): reached only for a
  `Modify:` target that no task creates at all, and flags it if it also doesn't exist on disk.
  Either tier emits one plan-level `task_order_contradiction` finding. The mirror check —
  flagging a `Create:` target that already exists — is deliberately absent: measured on a real
  plan it produced 9 false positives, every one of them because an earlier task had already been
  implemented in the worktree, which is a legitimate state for a resumed plan run.
- **`repo_root` on `validate_plan`** — optional absolute path that enables the disk tier of that
  check. Without it the order tier still runs.
- **`ANTI_TANGENT_CONTEXT_MAX_FILE_BYTES`** (default 131072) and
  **`ANTI_TANGENT_CONTEXT_MAX_PAYLOAD_BYTES`** (default 524288) — per-file and whole-set byte
  caps for `validate_plan`'s new `context_paths` attachments. Separate from the plan cap so an
  oversized attachment set is refused with its own actionable message.

### Changed
- `docs/protocol/controller.md`'s per-call cost figure for the plan gate was stale — it
  advertised ~$0.01–$0.02, which was already wrong for a large plan and off by roughly 50× with
  attachments. It now carries real numbers for both cases, and `core.md`'s FAQ (which still
  quoted the old figure) points at it.
- `docs/protocol/authoring.md` gains §3.9, documenting the literal `**Files:**` bullet syntax the
  Create/Modify check parses — verbs, backticked or bare paths, `Create/Modify:`, trailing
  parentheticals and line anchors. The check has always keyed on that structure and it was
  documented nowhere authors look.
- `core.md`'s `payload_too_large` FAQ names the `validate_plan` and `context_paths` caps, not just
  the shared `ANTI_TANGENT_MAX_PAYLOAD_BYTES`.
- `docs/protocol/controller.md` §5.8 says what an unusable `repo_root` produces — a minor
  `criterion: repo_root` finding, verdict unchanged, disk tier skipped. Controllers act on the
  findings list, and an unexplained entry there is an operational question at gate time.
- Three stale doc claims corrected: `authoring.md` §3.9 said `**Files:**` bullets "MUST have a
  space after the marker" (the parser accepts `-Create:`; the space governs only the
  unrecognized-verb fallback); `.claude-plugin/marketplace.json` still advertised the plugin as
  loading "the full INTEGRATION.md ... instead of ~10k tokens", stale since the role-scoped
  split; and README hardcoded "the chunked (>8-task) path" where
  `ANTI_TANGENT_PLAN_TASKS_PER_CHUNK` is documented as tunable. README's env table also now
  documents the relation between the two `context_paths` caps.
- README's prompt-caching claim was wrong in both directions: only the Anthropic client sends a
  cache breakpoint, and only on the chunked path — single-call plans and the OpenAI/Google clients
  re-send the whole attached set every call. README's context-window sizing guidance now accounts
  for the attachment bytes re-sent alongside the plan.
- `plugin/anti-tangent-protocol` is version 0.2.0 (bundled protocol content changed). Nothing in
  the release workflow bumps it, so it is bumped by hand here, together with its
  `.claude-plugin/marketplace.json` entry.
- **`stats` `payload_bytes` changed meaning on `validate_plan` events in 0.17.0**, with no events
  schema version marker (a schema bump is deliberately out of scope for this release). It now
  includes the `context_paths` bytes, and on a chunked round those bytes are multiplied by the
  number of reviewer calls, because the whole attached set is re-sent on every call. A cache HIT
  makes no reviewer call, so it bills the attached set exactly once. Plan text is
  deliberately NOT multiplied, so rows from calls without `context_paths` stay comparable with
  pre-0.17.0 history. Treat `payload_bytes` on rows carrying attachments as a new series.
- A bad `repo_root` no longer kills the review. It resolves BEFORE any attachment is read (a typo
  used to throw away every file the server had just read off disk) and, on failure, degrades to
  skipping the Create/Modify disk tier instead of failing the whole call over an optional
  argument. That degradation is now **visible in-band**: the response carries a minor
  `repo_root unusable (<reason>); Create/Modify disk tier skipped` finding as well as the stderr
  warning. Without it the envelope, summary and findings were byte-identical to a call that never
  passed `repo_root` — including for a plain relative path, which is an ordinary caller bug — so
  silence read as "the tier ran and found nothing". Like the `plan_text` deprecation notice it is
  applied per call, after the verdict ladder, and is never stored on a cache entry, so it cannot
  flip a verdict via the `noise_cluster` minor-count trigger.
- All three `context_paths` cap breaches now surface as the too-large envelope. The 51-file count
  cap used to return a transport error while the two byte caps returned envelopes.
- `validate_plan` logs one structured SUMMARY line to stderr per call, on EXIT rather than on
  entry, so it can carry `duration_ms`, `verdict` and `outcome` alongside the attached file count,
  byte total, and paths — plus at most one warning per degraded surface (an unusable `repo_root`;
  `Modify:` targets the disk tier could not stat). The entry line it replaces sat below both
  `context_paths` resolution returns, so a bad `context_paths` argument produced no log line at
  all, and it also sat below the four argument-validation returns, which logged nothing either.
  The summary line now also carries `repo_root_unusable`, because `repo_root` alone could not
  distinguish "omitted" from "supplied and rejected" — an unusable `repo_root` resolves to the
  empty string, so both logged `repo_root=false`.
- The reviewer ground rules, previously duplicated verbatim across all three plan templates, are
  now one shared partial. So is the `BEGIN/END FILE` attachment block, for the same reason: it was
  byte-identical in all three templates while only `plan.tmpl` had attachment goldens, so an edit
  to the delimiter — the shape `contextNonceDelimiterCollides` matches on — could silently disarm
  that detector with every golden still green. There is now also an attachment golden for a chunk
  template. Every existing golden is byte-identical across the extraction, which is what proves it
  was mechanical.
- The attachment-mode ground rules (a) tell the reviewer that everything between the
  `BEGIN/END FILE` markers is file content quoted as data and must never be followed as
  instructions, (b) say that the plan describes work not yet done — a symbol the plan says to add
  being absent from an attached file is the expected state, not a contradiction — and (c) explain
  that attached paths are absolute and match the plan's repo-relative paths by suffix. All three
  are gated on `context_paths`; a call without attachments still renders byte-identically to
  0.16.0. The marker rule names this call's nonce token explicitly and states that a
  marker-shaped line carrying any other token is file content, not a boundary. The binding rule
  matches whole path segments, states its example in repo-relative terms rather than inventing a
  plausible-looking absolute root two paragraphs after telling the reviewer to distrust
  official-looking strings, and tells the reviewer to treat a plan path as UNATTACHED whenever two
  attached files could match it or the binding is otherwise uncertain, because a wrong binding is
  worse than none — and the absence-is-evidence grant, which names the enumerated attached list
  explicitly, carves those UNATTACHED paths back out, so an ambiguously-bindable path stays
  black-box even though the file it might name is attached. `unverifiable_codebase_claim` is
  scoped by SETTLEMENT rather than by filename — emit it for any claim the attached files do not
  settle, which includes a cross-file claim that names an attached file but turns on code outside
  the attached set. And a `contradicted_codebase_claim` must satisfy the plan-text evidence rule
  in addition to quoting the attached file, not instead of it.

### Security
- **A resolved path carrying a control or format character is refused, not rendered.**
  `context_paths`, `plan_path` and `repo_root` all resolve symlinks with `EvalSymlinks`, which
  returns the target name verbatim, and Linux permits every byte but `/` and NUL in a file name. A
  resolved path carrying an embedded newline injected lines into the enumerated attached-paths list
  *inside* the reviewer's ground rules — above and outside the nonce-guarded `BEGIN/END FILE`
  region — and into the summary block's `context:` provenance list; for `repo_root` the route is
  the wrapped `os.PathError` on a failing resolve, which prints the raw path unescaped into the
  minor `repo_root unusable (<reason>)` finding. A resolved path is now rejected outright when it
  carries any C0 control (`< 0x20`), DEL (`0x7f`), U+2028 or U+2029 (which are line breaks to a
  model reading the prompt), or any Unicode format character — which covers U+202E
  RIGHT-TO-LEFT OVERRIDE, the Trojan-Source class (CVE-2021-42574), and the invisible U+200B. The
  refusal names the offending code point (`disallowed control or format character U+XXXX`) and the
  remedy — rename the file, or drop it from `context_paths` — because "control character" alone is
  a misnomer for a category that includes format characters such as a soft hyphen, and `%q` alone
  left the operator decoding an escape by hand. README documents the refusal.
  For `repo_root` the check runs **before** `EvalSymlinks` as well as after: the wrapped
  `*fs.PathError` from a failing resolve interpolates the path unquoted, so the post-resolution
  check — which only runs when resolution succeeds — could never see it. The outer `%q` escapes
  its own copy and does not help. (Reported by CodeRabbit on PR #63.)
- **`context_paths` sends file contents verbatim to the configured reviewer vendor.** Everything
  the caller attaches is transmitted to whichever third-party API `ANTI_TANGENT_PLAN_MODEL`
  names, in full, on every reviewer call of the round. Attach only what the plan makes claims
  about, and set `ANTI_TANGENT_PLAN_ROOTS` to bound what the server will read on the caller's
  behalf. The tool description now says so at the call site.

### Fixed
- **The `Create:`/`Modify:` disk tier no longer follows a symlink out of the repository.**
  `resolveUnderRoot` rejects lexical traversal (`../../etc/passwd`) but is purely textual, and
  `os.Stat` follows symlinks — so a link living inside `repo_root` but pointing outside it made the
  check answer "does this path exist anywhere on this machine" rather than "does this file exist in
  the repository". The parent directory is now symlink-resolved and required to stay under the root
  before the leaf is stat'd. The leaf uses `Lstat`: a symlink the repository contains *is* a file
  the repository contains, and `Stat` reported every dangling in-repo link as missing — a false
  positive that is now fixed as a side effect. (Reported by CodeRabbit on PR #63.)
- **`./a.go` and `a.go` are one file in the order-aware check.** `cleanRefPath` preserved each
  bullet's raw spelling, so `Modify: ./a.go` in an early task and `Create: a.go` in a later one
  were unrelated map keys and the contradiction went unreported. Repo-relative paths are now
  canonicalised with `path.Clean`. URLs and absolute paths are left untouched — `path.Clean`
  collapses the `//` in `https://example.com/a.go`, and absolute paths are refused upstream anyway.
  This was only ever visible without `repo_root`, since the disk tier would otherwise have caught
  it. (Reported by CodeRabbit on PR #63.)
- **Two roots-allowlist tests could pass without exercising the branch they pin.** The
  outside-roots and symlink-escape tests in `context_files_test.go` passed a raw `t.TempDir()` as
  the root while `resolveFileInput` compares the symlink-*resolved* candidate against it. Where an
  ancestor of the temp directory is itself a symlink (macOS `/tmp` → `/private/tmp`), every path
  failed the roots check because the root never matched — and both tests, which only assert that
  *an* error occurred, would have passed while covering nothing. Both now resolve the root first,
  as `TestResolveDirInput_RootsAllowlist` already did. (Reported by CodeRabbit on PR #63.)
- **Line-anchored `Modify:` bullets no longer produce phantom "does not exist" findings.** Plans
  routinely anchor a file reference to the lines being edited (`- Modify: internal/x.go:57-70`)
  — the convention superpowers' task-format reference asks for. The Create/Modify consistency
  check kept the anchor as part of the path, so the disk tier stat'd `internal/x.go:57-70`, a
  path that can never exist, and the order tier never matched the anchored form against its
  unanchored `Create:` twin. A trailing `:N`, `:N-M`, `:N,M`, or a repeated form such as the
  `line:column` an editor emits (`internal/x.go:57:12`) is now stripped before either tier looks
  at the path.
- **The Create/Modify order tier now orders tasks by position, not by their declared number.**
  Tasks execute in the order they appear in the plan, but nothing forces `### Task N:` headings
  to be ascending 1..N — two corpus plans already restart numbering mid-file for phases. The
  comparison used the declared number, so a correctly-ordered plan whose numbers restart drew a
  bogus blocking finding, and a genuinely out-of-order plan whose numbers descend was silently
  missed. Findings still name tasks by the plan's own numbers.
- **The disk tier only reports "does not exist" for a genuine not-exists.** A permission or I/O
  error from `os.Stat` now leaves the target alone instead of being reported as missing, and
  surfaces instead as a single aggregated stderr warning per call carrying the count plus the first
  path and error. So an unreadable `repo_root` no longer makes the whole disk tier silently inert —
  and, because the warning is emitted once rather than from inside the nested loop over every
  `Modify:` bullet of every task, it cannot bury stderr under hundreds of lines for one call.
- **`contradicted_codebase_claim` is suppressed server-side unless an attached file backs it.**
  The category deliberately carries no severity floor, because an attached file is ground truth —
  so a reviewer emitting one about code it never saw could fail a plan gate at `major`. Any such
  finding naming none of the attached files is demoted to `unverifiable_codebase_claim` (floored to
  minor), independent of whether the reviewer obeyed the prompt's prohibition. The test is per
  finding, not per call: gating it on "nothing was attached at all" meant a single attached file
  re-armed the unfloored severity for a contradiction about some other, unattached file.
  Both `evidence` and `criterion` are read, matching the sibling `validate_task_spec` guard —
  reviewers routinely name the file in `criterion`, and reading `evidence` alone would demote a
  correct, ground-truth refutation to a minor unverifiable claim, which the unverifiable-only
  calibration then force-passes with "No blocking plan-quality findings remain". The attached file's
  basename must be bounded at BOTH ends by something that cannot be part of a filename, so
  `config.golden` and `myconfig.go` are not mentions of `config.go`. A boundary is anything that is
  not a letter, digit, `_` or `-` — stated as what breaks a filename rather than as a list of the
  punctuation reviewers were observed using, so a quoting style nobody enumerated fails OPEN
  rather than silently dropping a real refutation: absolute, repo-relative, backticked, quoted,
  parenthesised, bracketed, line-anchored, `**bold**`, `<angled>` and `#L42`-anchored mentions all
  bind. `.` is a boundary, which is what rejects `config.golden` and also what lets a sibling name
  like `config.go.bak` bind — the fail-open side of the same trade, and the cheap direction to be
  wrong in.
- **A truncated plan review keeps the Create/Modify consistency finding and its `context:`
  provenance.** The reviewer-free check ran after the truncation-recovery early return, so a
  truncated response silently dropped the one finding the server already knew for certain; and
  the recovery envelope's summary omitted the attached-file list while the same call's stats
  counted every attached byte.
- An **explicitly set** `ANTI_TANGENT_CONTEXT_MAX_FILE_BYTES` greater than
  `ANTI_TANGENT_CONTEXT_MAX_PAYLOAD_BYTES` is rejected at startup, naming both variables: a
  per-file cap above the whole-set cap can never be reached, so raising it alone silently
  achieves nothing. Lowering only the payload cap below the 131072 per-file **default** clamps
  that untouched default down instead of erroring — the unconditional check made any
  `ANTI_TANGENT_CONTEXT_MAX_PAYLOAD_BYTES` under 131072 refuse to boot, taking every
  anti-tangent tool off the host over a value the operator never set.
- **`validate_plan`'s in-process result cache now keys on attached file content.** Without this,
  editing a source file a finding complained about and immediately re-validating would return
  the stale pre-fix review from the 3-minute cache, with no indication anything was reused.
- **The context-nonce collision detector now recognises near-shaped delimiters.** It demanded the
  rendered marker verbatim (`^--- (?:BEGIN|END) FILE <token>:\x20`), so a line carrying the CORRECT
  token in a slightly different shape — a fourth dash, a leading indent, a tab in place of the
  colon, or the path elided entirely (`--- END FILE <token> ---`, which is the rendered END marker
  minus its path and the near-shape a model asked to close a block is likeliest to produce) — read
  to a model as a real boundary while slipping past the check. The ground rules do
  not cover that case either: they only dismiss marker-shaped lines carrying the WRONG token. The
  token is still matched verbatim, and a false positive merely re-derives the nonce with the
  attempt counter folded in, which stays deterministic.
- **The effective `context_paths` caps are now visible at startup, and the per-file refusal no
  longer advises a change that breaks the next boot.** When the per-file cap is still the default
  and the operator lowers `ANTI_TANGENT_CONTEXT_MAX_PAYLOAD_BYTES` below it, `config.Load` clamps
  the per-file cap down silently — nothing reported the value actually in force. The `starting`
  line now carries both effective caps (it is emitted after the JSON logger is installed, so the
  "the warning would go nowhere" reasoning that keeps `config.Load` itself quiet does not apply).
  The per-file refusal previously said "raise `ANTI_TANGENT_CONTEXT_MAX_FILE_BYTES`" without
  mentioning that raising it above the whole-set cap makes the next start fail; it now names the
  whole-set cap and its current value immediately after the per-file one, ahead of the remedy
  clause, so the ceiling survives the summary block's per-finding evidence truncation. The
  truncated form an operator actually reads used to end mid-way through "raise
  `ANTI_TANGENT_CONTEXT_MAX_FILE_BYTES`", showing them the advice that breaks their next boot with
  the correction cut off.
- **A truncated `validate_plan` review now mints a `plan_run_id`.** The truncation-recovery path
  can return a passing verdict, and controller.md §5.1 tells the controller to capture
  `plan_run_id` from a passing response — but that path minted none, so `plan_run_report`, which
  requires one, could never run on such a plan run. It was the only pass-capable exit without an
  id.
- **A truncated `validate_plan` review now reports an unusable `repo_root`.** The minor
  `repo_root unusable (<reason>); Create/Modify disk tier skipped` advisory was applied on the
  fresh-review and cache-hit paths only, so a truncated call with a bad `repo_root` produced a
  findings list byte-identical to one that never passed the argument.
- **A truncated `validate_plan` review now carries normative test bodies.** They are extracted
  from the plan text server-side, not from the reviewer response, so a truncated reviewer reply
  is no reason for the recovered per-task results to lose them.
- The three post-review tails inside `validate_plan` (fresh review, truncation recovery, cache
  hit) are now assembled from one shared `planCallContext` instead of being hand-written at each
  site. The three fixes above were all the same defect — the recovery site drifting from the
  other two — found one field at a time across three review rounds. The verdict ladder stays
  deliberately outside the shared helper: the cache-hit path must never re-run it on an
  already-finalized entry.

## [0.16.0] - 2026-08-31

### Added
- **`plan_path` on `validate_plan`.** Pass an absolute path and the server reads the plan itself.
  A plan large enough to exceed the caller's max-output-tokens setting was previously
  unsubmittable under the caller's output-token limit, because `plan_text` had to be emitted as
  part of the calling model's own tool-call output. Reading from disk also guarantees the
  reviewer sees the same document the implementing subagents will.
- **Path inputs on `validate_completion`.** Omit a `final_files` entry's `content` to have the
  server read its `path`, or pass `final_diff_path` instead of `final_diff`. Truncation checks
  run on the resolved content, so a path is not a way around the evidence-shape guard.
- **`ANTI_TANGENT_PLAN_ROOTS`** — a list of absolute directories, joined with the OS path-list
  separator (`:` on Unix, `;` on Windows), that file-path inputs may be read from. Empty (the
  default) is unrestricted.
- **`ANTI_TANGENT_PLAN_MAX_PAYLOAD_BYTES`** (default 1MB) — payload cap for `validate_plan` only.
  The other tools keep the shared 200KB `ANTI_TANGENT_MAX_PAYLOAD_BYTES`.
- **Reviewer-side prompt caching on the chunked plan path.** The first chunk call marks its
  byte-identical plan-text prefix as an Anthropic cache breakpoint; every later chunk call reads
  it back. Cuts input tokens on a chunked round, increasingly so as the chunk count grows. The
  findings-only Pass 1 call sends a differing `tools` block, so it can neither write nor read
  that cache and carries no breakpoint of its own. Single-call plans are deliberately not
  cached, for the same reason — a breakpoint there is a write premium against zero reads.

### Changed
- The `validate_plan` too-large finding reports `plan:` rather than `plan_text:`, since the
  content may have arrived via `plan_path`.
- When `plan_path` is used, the summary block gains a `source:` line naming the resolved path,
  byte count, and a short hash, so a controller can show which document cleared the gate.

### Fixed
- **`content` stayed required on `check_progress` and `extract_project_knowledge`.** Adding path
  support to `validate_completion` gave the shared `FileArg.content` field `omitempty` so a
  `final_files` entry could omit it and be read from disk. `check_progress`'s `changed_files` and
  `extract_project_knowledge`'s completion-envelope `final_files` reuse the same `FileArg` type
  but neither resolves a path — so `content` had silently become optional there too, and a caller
  following the new "prefer paths" convention on those two tools would ship empty file bodies to
  the reviewer with no error. `validate_completion` now has its own `CompletionFileArg` (a
  nilable `content`); `FileArg.content` is required again, matching 0.15.0's schema exactly, and
  `check_progress` / `extract_project_knowledge` are unchanged in schema and behaviour.
- **`validate_completion`'s `final_files[].content: ""` is no longer treated as omitted.** An
  explicit empty string used to be indistinguishable from "not supplied" and always triggered a
  filesystem read of `path` — breaking a deleted-file entry submitted as `{"path": "...",
  "content": ""}` (`EvalSymlinks` on a path that no longer exists, losing the whole call to a
  transport error) and any relative `path` paired with empty content (a `path must be absolute`
  error, even though this codebase's own convention is relative repo paths). `content` is now a
  pointer on the wire: omitted/`null` reads `path` from disk, an explicit `""` is taken literally
  as "this file is genuinely empty."
- `ANTI_TANGENT_PLAN_ROOTS` entries are now symlink-resolved at server start (falling back to the
  cleaned path when a root doesn't exist yet), matching the symlink-resolved candidate paths
  `resolveFileInput` checks them against. Previously a root under a symlinked ancestor (e.g.
  macOS's `/tmp` → `/private/tmp`) refused every legitimate path beneath it.
- A `validate_plan` `plan_text` call whose reviewer response truncates now still carries the
  `plan_text` deprecation notice; it previously skipped that recovery/partial path only.
- `validate_completion`'s "referenced paths missing evidence" advisory now matches an absolute
  `final_files` path against a relative path named in `summary` (e.g. `/repo/docs/foo.md`
  satisfies a `summary` mention of `docs/foo.md`), instead of only exact string equality — the
  advisory used to false-fire on every doc deliverable submitted the documented absolute-path way.
- **`ANTI_TANGENT_PLAN_ROOTS` was unusable on Windows.** It was split on a hardcoded `:`, so a
  Windows value like `C:\plans` parsed as `["C", "\plans"]` and the second segment's
  not-absolute check failed `Load` at startup — no value of the variable worked on that platform.
  Parsing now uses `filepath.SplitList` (`os.PathListSeparator`: `:` on Unix, `;` on Windows).
- **`validate_plan`'s new payload cap silently regressed hosts that had already raised the shared
  cap.** `ANTI_TANGENT_PLAN_MAX_PAYLOAD_BYTES` defaulted to a flat 1MB regardless of
  `ANTI_TANGENT_MAX_PAYLOAD_BYTES`, so an operator running the shared cap above 1MB saw
  `validate_plan` start rejecting plans it accepted before this version, with no config change on
  their side and an error that never named the new variable. It now defaults to
  `max(1048576, ANTI_TANGENT_MAX_PAYLOAD_BYTES)`; an explicit
  `ANTI_TANGENT_PLAN_MAX_PAYLOAD_BYTES` still overrides in either direction.
- **`implementer.md`'s `final_diff_path` recipe used `git rev-parse --git-dir`, which is relative
  in a normal checkout.** A subagent following the recipe verbatim there passed a relative
  `.git/anti-tangent-change.diff`, which `resolveFileInput` rejects as `path must be absolute` —
  losing the whole `validate_completion` call. Switched to `git rev-parse --absolute-git-dir`,
  which is absolute in both a normal checkout and a worktree.
- **`ANTI_TANGENT_PLAN_ROOTS=/` (or a Windows drive root) matched nothing.** `withinRoots`'s
  prefix test degenerated to requiring the literal string `//` when a root was the filesystem
  root, so a root meant to allow every path refused all of them. Containment is now decided with
  `filepath.Rel`, which handles a root-of-`/` correctly while still enforcing the
  separator-boundary property (`/home/foo` still does not authorize `/home/foobar`).
- The "outside `ANTI_TANGENT_PLAN_ROOTS`" error message joined the configured roots with a
  hardcoded `:`, printing a value a Windows operator could not have typed (and that
  `filepath.SplitList` would reject if pasted back). Now joined with
  `os.PathListSeparator`, matching the parser.
- **`validate_completion` could pass with literally no evidence.** The at-least-one-evidence
  guard only checked that `final_diff_path` or a `final_files[].path` STRING was non-empty,
  before those paths were resolved from disk. A `final_diff_path` (or path-only `final_files`
  entry) pointing at a genuinely empty file therefore passed the guard, resolved to an empty
  string, and reached the reviewer with nothing to review — returning `pass` with zero findings
  instead of the rejection the README documents for evidence-poor submissions. The guard now
  re-runs against the resolved content: when the empty path is the ONLY evidence on the call, it
  is a structured `malformed_evidence` rejection naming the offending field (e.g.
  `final_diff_path resolved to 0 bytes: <path>`), same as before, with no reviewer call. When
  other real evidence is also present — non-empty `test_evidence`, or another non-empty
  `final_files` entry — the documented "diff plus test evidence" flow, which is exactly the
  shape `docs/protocol/implementer.md` recommends — the call now proceeds and carries an
  `insufficient_evidence` finding naming the empty path instead of silently reviewing only the
  other evidence. `test_evidence` alone with no path inputs supplied at all still satisfies the
  guard with no finding, and an explicit `final_files[].content: ""` deletion marker is still
  never treated as a resolution accident.
- **A leftover named pipe (FIFO) at `plan_path` or `final_diff_path` hung the tool call
  forever.** Reading a file input first opens it with `O_NOFOLLOW` to refuse a symlink swapped in
  at the final path component between the roots check and the read — but opening a FIFO
  `O_RDONLY` blocks until a writer connects, and that open ran before the regular-file check that
  would have rejected it. `resolveFileInput` takes no `context.Context`, so neither
  `ANTI_TANGENT_REQUEST_TIMEOUT` nor MCP request cancellation could unblock it — the goroutine and
  its file descriptor leaked for the process lifetime. The open now also sets `O_NONBLOCK` (a
  no-op for regular files), so a FIFO open returns immediately and is rejected as not a regular
  file instead of hanging.
- **`ANTI_TANGENT_PLAN_ROOTS` could fail open on a malformed value.** A value that was set but
  parsed down to zero usable entries (e.g. a bare path-list separator, or all-whitespace entries)
  left `PlanRoots` `nil` with no startup error — and `nil` roots means "unrestricted" — so an
  operator who believed they had narrowed the server's file access had not. Setting the variable
  to such a value is now a startup error naming `ANTI_TANGENT_PLAN_ROOTS`.

### Security
- **`docs/protocol/implementer.md`'s `final_diff_path` recipe over-captured.** It told
  implementers to run `git add -A && git diff HEAD`, which stages and diffs every non-ignored
  change in the worktree — unrelated tracked edits, scratch files, another task's half-finished
  work — and everything in that diff is sent to a third-party reviewer LLM via `final_diff_path`.
  The recipe now scopes both the `git add` and the `git diff` to the task's own paths (the task's
  `**Files:**` list, used as a pathspec), with new files either covered by that same pathspec or
  passed individually as `final_files[].path` entries, so completeness no longer requires
  disclosing unrelated work.

### Deprecated
- **`plan_text` on `validate_plan`.** Still fully functional, now reporting one `minor` finding
  pointing at `plan_path`. It will be removed in 1.0.0.

## [0.15.0] - 2026-08-17

### Added
- **`plan_run_report`** (seventh tool) — a deterministic, reviewer-free per-task report over a
  finished plan run, showing the anti-tangent verdict and the CodeScene result side by side.
  `validate_plan` now mints a `plan_run_id` the controller threads into each
  `validate_task_spec` call.
- **In-band CodeScene results.** `validate_completion` accepts a structured `codescene`
  argument (the `analyze_change_set` digest). It reaches the reviewer as authoritative
  caller-attested context (no independent verification) — the first input that partially
  covers anti-tangent's text-only blind spot — and is attributed to the task in the plan-run
  report. `ANTI_TANGENT_CODESCENE=required` makes a missing run observable as a
  `codescene_not_run` finding; unset (the default) changes nothing. Still advisory: a failed
  quality gate never fails a verdict server-side.
- **`submission_defect_only`** on `validate_completion` envelopes. True when every blocking
  finding is about the submission (`insufficient_evidence`, `malformed_evidence`,
  `codescene_not_run`) rather than the code, so an implementer re-submits instead of reworking.
  Field data showed two-thirds of first-round failures were evidence complaints treated as
  blocking code defects.
- **Plan-header adoption telemetry** in `rollup.json` (`plan_headers`), and an optional durable
  plan-run ledger behind `ANTI_TANGENT_PLAN_LEDGER=1`.

### Fixed
- `HasStructuredHeader` matched `**Acceptance criteria:**` case-sensitively and was read
  nowhere outside tests. superpowers' `writing-plans` — the dominant plan generator — emits a
  capital C and grep-enforces it, so every plan it produced scored as headerless and the
  resulting adoption signal was false. The matcher is now case-insensitive and the result is
  reported as telemetry.

### Changed
- **The protocol document is now role-scoped.** `INTEGRATION.md` is a router over
  `docs/protocol/{core,authoring,implementer,controller,project-knowledge}.md`, and the
  `anti-tangent-protocol` skill reads only `core.md` plus the part matching the agent's role.
  An implementing subagent previously read the whole ~40 KB document — including ~10 KB of
  project-knowledge protocol it is structurally forbidden from acting on — once per dispatch;
  it now reads `core.md` + `implementer.md`, roughly 22 KB. **Deep links to `INTEGRATION.md`
  anchors no longer resolve**; section numbers are unchanged, so `§4.2` is still `§4.2`, now
  inside `implementer.md`.
- The skill's trigger covers the end of a plan run, so a controller calling `plan_run_report`
  after the last task reports DONE has the protocol loaded.
- `post.tmpl` instructs the reviewer to use `insufficient_evidence` for acceptance criteria it
  cannot assess, instead of `missing_acceptance_criterion`.

## [0.14.0] - 2026-07-09

### Added
- OpenAI **GPT-5.6** family to the reviewer allowlist: `gpt-5.6-sol` (heavy),
  `gpt-5.6-terra` (balanced), `gpt-5.6-luna` (fast). No client change was
  needed — GPT-5.6 speaks the same Chat Completions surface the existing OpenAI
  reviewer already uses (`/v1/chat/completions` with `max_completion_tokens` and
  `response_format: json_schema`). At launch the 5.6 family is a **gated limited
  preview**, so calls fail with an access error unless the `OPENAI_API_KEY` is
  enrolled; OpenAI has not yet published dated snapshot ids, so pin a snapshot
  (as with `gpt-5.5-2026-04-23`) once one exists before making it a standing
  default. README's "Supported reviewer models" table and the model-picking
  notes document the caveats.

## [0.13.0] - 2026-07-09

### Changed
- The CodeScene pre-DONE check now asks for a **positive one-line CodeScene
  status in every DONE report** (the `analyze_change_set` delta, or that the
  call was skipped and why), not only a mention on regression or deliberate
  skip. Because anti-tangent structurally cannot observe whether the companion
  calls ran, a clean run and a silent non-adoption previously looked identical
  to the controller; requiring the status line makes a *missing* line itself
  the non-adoption signal. This unifies the previously scattered pre-DONE
  "surface a regression" / "state a deliberate skip" wording in `INTEGRATION.md`
  §4.2 step 3b, the §4.2 short variant, and the "CodeScene MCP companion"
  section (and `README.md`) into one attestation rule scoped to the pre-DONE
  `analyze_change_set` call (the mid-task step 2b keeps its report-on-skip
  wording). Still prompt-level only — anti-tangent
  stays advisory and never enforces CodeScene findings server-side. Follows up
  the v0.12.0 required-when-configured promotion (#49).

## [0.12.0] - 2026-07-08

### Changed
- CodeScene companion calls are now **required when `codescene-mcp` is
  configured** in the host, raised from RECOMMENDED (mid-task
  `pre_commit_code_health_safeguard`) / OPTIONAL (pre-DONE
  `analyze_change_set`) in `INTEGRATION.md` §4.2 steps 2b/3b, the §4.2 short
  variant, and the "CodeScene MCP companion" section (and mirrored in
  `README.md`). The companion exists to cover anti-tangent's text-only blind
  spot, but under-adoption of the optional calls meant that coverage was
  largely not happening. The requirement is **prompt-level only** — a
  deliberate skip must be stated in the DONE report; anti-tangent remains
  advisory and never enforces CodeScene findings server-side, and all
  companion calls are still skipped silently when CodeScene MCP isn't
  configured or on lightweight-protocol tasks. (#49)

## [0.11.1] - 2026-07-08

### Changed
- Recommended install is now slim + on-demand. Claude Code installs the new
  `anti-tangent-protocol` plugin — a description-triggered skill that `Read`s the
  bundled `INTEGRATION.md` only when a task carries a Goal/Acceptance-criteria
  header — instead of `@`-importing the full ~40 KB `INTEGRATION.md` into global
  `~/.claude/CLAUDE.md`. opencode wires a slim pointer into `instructions` and
  loads the full document on demand. The always-loaded footprint drops from
  ~10k tokens to a single skill-description line.

### Added
- `plugin/anti-tangent-protocol/` — companion plugin carrying the protocol as an
  on-demand skill; registered in the marketplace.
- `examples/anti-tangent-pointer.md` — slim opencode / non-skill-host pointer
  template.
- CI guard that the plugin's bundled `INTEGRATION.md` stays byte-identical to
  root.

## [0.11.0] - 2026-07-07

### Changed
- CodeScene stats record + rollup redesigned around `analyze_change_set`'s actual
  categorical output (per-file verdicts, quality-gate, problem-points) instead of a
  numeric Code Health score, which the tool does not return for a change set.
  `CodesceneEvent`/`CodesceneRollup` drop `score_before`/`score_after`/`delta`/
  `latest_score`/`score_p50`; add `quality_gate`/`verdicts`/`net_pp` and rollup
  `gates_passed`/`gates_failed`/`latest_gate`/`latest_net_pp`/`net_pp_p50`.

### Added
- `examples/hooks/codescene-log.sh`: a PostToolUse hook that appends one counts-only
  record per `analyze_change_set` run to `codescene-events.jsonl`. See
  `docs/team-setup/codescene-stats.md`.

## [0.10.0] - 2026-06-02

### Added
- Opt-in statistics subsystem (`ANTI_TANGENT_STATS_DIR`): records one counts-only record per hook call to `events.jsonl`, periodically aggregates a deterministic `rollup.json` and an LLM-written `summary.md`, and prunes by `ANTI_TANGENT_STATS_RETENTION_DAYS`. Entirely inert when the var is unset (no files, no overhead, no behavior change). Records hold counts + metadata only — no finding text, no plan/spec content, no raw session id (salted hash only). New vars: `ANTI_TANGENT_STATS_MODEL`, `ANTI_TANGENT_STATS_SUMMARY_INTERVAL`, `ANTI_TANGENT_STATS_SUMMARY_THRESHOLD`, `ANTI_TANGENT_STATS_RETENTION_DAYS`, `ANTI_TANGENT_STATS_MAX_TOKENS`.
- CodeScene companion (spec §12): the agent appends one counts-only record per `analyze_change_set` run to `codescene-events.jsonl`; the Compactor aggregates them into a nested `codescene` block in `rollup.json` and retention-prunes the file. See `docs/team-setup/codescene-stats.md`.

### Changed

### Fixed

### Removed

### Deprecated

### Security

## [0.9.1] - 2026-05-29

### Added
- CI `INTEGRATION.md size budget` job (`ci.yml`) that fails any change pushing `INTEGRATION.md` to ≥ 40,000 bytes, preventing silent regressions of the user-instructions context budget. `build-test` now depends on it, so a violation blocks the merge.

### Changed

### Fixed
- `INTEGRATION.md` trimmed back under the 40,000-byte user-instructions budget (40,137 → 39,786) by condensing content already covered in the conventions doc / design specs; the v0.9.0 howto additions had pushed it 137 bytes over.

### Removed

### Deprecated

### Security

## [0.9.0] - 2026-05-29

### Added
- `howto` project-knowledge note type (eighth type) — a slug-keyed, update-in-place operational runbook; the durable-reference counterpart to `gotcha`. Proposed by `extract_project_knowledge` with `action: create` / `action: update` (never `supersede`).
- `bm-scribe:create-howto` skill — captures a `howto` at `<PROJECT>/howtos/<slug>/main` via the three-step BM v0.21.1 pattern, with in-place update of an existing runbook's `## Steps`.

### Changed
- `plugin/bm-scribe` bumped to 0.3.0 (new `create-howto` skill; 14 skills total).

### Fixed
- `validate_plan` now parses task headings at any of h2–h4 (`##`/`###`/`####`), not just `###`. A plan whose task headings drifted one level (e.g. `## Task N:`) previously parsed to zero tasks and failed the first `validate_plan` call, wasting a full review round-trip; `###` remains canonical.

### Removed

### Deprecated

### Security

## [0.8.3] - 2026-05-27

### Added

### Changed
- `docs/team-setup/basic-memory-shared-vm.md` Docker container path now documents **streamable-http** as the recommended transport, with SSE relegated to a legacy fallback. Field-verified against a live BM v0.21.1 deployment with the new `command:` directive in §13.3. Specific section updates:
  - §1 topology table: Docker path transport label changed from "SSE on HTTP(S)" to "streamable-http on HTTP(S) (recommended) or SSE (legacy)".
  - §13.3 compose file: added a `command: ["basic-memory", "mcp", "--transport", "streamable-http", "--host", "0.0.0.0", "--port", "8000", "--path", "/mcp"]` directive overriding the image's default SSE CMD. Comments cite §13.8.8 for the rationale.
  - §13.4 reverse-proxy intro: clarified that the same Caddy / nginx snippets work for both transports (no path-specific routing); BM serves the chosen transport on the `--path` value (`/mcp` for streamable-http by default).
  - §13.5 per-dev Claude Code MCP config: switched the JSON example from `"transport": "sse"` / `.../sse` to `"transport": "streamable-http"` / `.../mcp`. Added a paste-ready smoke-test `curl` command (verifies HTTP 200 + `Mcp-Session-Id` header + valid JSON-RPC result) and a migration paragraph for teams moving from SSE.

### Fixed
- `docs/team-setup/basic-memory-shared-vm.md` §13.8.8 refactored: previously framed the `-32602` symptom as a live bug with reload / keepalive workarounds. Now leads with the actual fix (switch the BM container to streamable-http per §13.3 / §13.5), reserves the workarounds for teams pinned to SSE for external reasons, and traces the upstream MCP-SDK code path that produces the bug (`modelcontextprotocol/typescript-sdk` `SSEClientTransport` does not re-initialize on reconnect; `modelcontextprotocol/python-sdk` `_receive_loop` mis-categorizes the initialization-state RuntimeError as `INVALID_PARAMS` instead of the spec-defined `SERVER_NOT_INITIALIZED` / `-32002`).

### Removed

### Deprecated

### Security

## [0.8.2] - 2026-05-27

### Added

### Changed
- `docs/team-setup/basic-memory-shared-vm.md` §13.4 Caddyfile snippet now ships `read_timeout 0` (unbounded) instead of `read_timeout 1h` on the upstream transport. The v0.7.x recommendation of `1h` consistently force-closed BM upstream connections at the 60-minute mark for users with long-idle sessions; `0` is safe because BM is on loopback and HTTP/2 connection-level keepalive will detect a real upstream death. Added a global-block `servers { timeouts { idle 0 ... } }` recommendation for symmetric client-facing connection durability.

### Fixed
- `docs/team-setup/basic-memory-shared-vm.md` §13.8.4 (SSE endpoint hangs or cuts off mid-stream): added a sub-paragraph explicitly calling out the `read_timeout` hit as a distinct failure mode from buffering, and pointing at the §13.4 update for the fix.
- Added new §13.8.8 troubleshooting entry for the `-32602 Invalid request parameters` symptom that surfaces after long-idle MCP sessions. Documents the symptom (only-fixable-by-MCP-reload), correctly identifies it as upstream MCP protocol session-state desync (NOT a Caddy issue), and lists three working hypotheses plus diagnostic data the user should capture before filing the upstream bug at `github.com/basicmachines-co/basic-memory/issues`. Includes manual / client-side-keepalive / external-keepalive workarounds.

### Removed

### Deprecated

### Security

## [0.8.1] - 2026-05-25

### Added

### Changed

### Fixed
- `INTEGRATION.md` re-trimmed back under the 40,000-byte user-instructions context budget. The v0.8.0 release inadvertently pushed it to 40,419 bytes (419 over) because the new `gotcha` table row body was unusually long compared to the existing rows. Two trims: (1) shortened the `gotcha` row body in the "Seven note types in three groups" table from 449 chars to ~120 chars by dropping content already covered by the conventions doc + design spec; (2) tightened the "v0.7.0 canonical layout" paragraph by dropping the `charter.md` / `retro.md` side-docs aside (covered in the conventions doc) and inlining the auto-pick clause. Net result: INTEGRATION.md back to 39,886 bytes (114 under).

### Removed

### Deprecated

### Security

## [0.8.0] - 2026-05-23

### Added
- New design spec `docs/superpowers/specs/2026-05-23-gotcha-note-type-design.md` introducing a seventh project-knowledge note type, `gotcha` (implementation landed in the same v0.8.0 release — see the per-surface bullets below). The spec covers:
  - **Storage and frontmatter.** ADR-numbered permalink at `<PROJECT>/gotchas/<NNNN>-<slug>/main`. Frontmatter carries `modules: [...]`, `origin:`, `severity`, `status: accepted | superseded`, `discovered_at`, `supersedes: []`.
  - **Lifecycle.** Supersede-chain mechanics mirroring `decision`: new note carries `supersedes: [<predecessor>]`, and a follow-up `edit_note(find_replace)` flips the predecessor's `status` to `superseded`.
  - **Two intake paths.** Post-plan via `extract_project_knowledge` proposing `ProposalTypeGotcha` records (anti-tangent server change); post-review via a new `bm-scribe:create-gotcha` skill that mines CodeRabbit / `/ultrareview` / `/code-review` / `/security-review` output inline (plugin-only — no anti-tangent change for this path).
  - **Prime integration.** Read side requires no anti-tangent code change. Existing `prime_project_knowledge` loop finds gotchas via canonical-encoded `tags` entries (`status:<value>`, `module:<slug>`) in the existing `KBIndexEntryArg` wire schema. Reviewer prompt and BM schema are unchanged.
- New `ProposalTypeGotcha` constant in `internal/verdict/extract.go`, added to the parser type-switch allowlist in `internal/verdict/extract_parser.go`, and added to the `proposals[].type` enum in `internal/verdict/extract_schema.json`. The reviewer can now propose `gotcha`-typed entries from `extract_project_knowledge` envelopes; the parser round-trips them via the new `TestParseExtract_AcceptsGotchaType` test and the renamed `TestParseExtract_AcceptsAllSevenTypes` regression. No change to `ProposalAction` — supersede support reuses the existing `action: "supersede"` + `supersedes: [...]` wire shape.
- Extended `internal/prompts/templates/extract.tmpl` to teach the reviewer the gotcha category: ADR-style permalink shape, required frontmatter (`status`, `modules`, `severity`, `discovered_at`; optional `origin`, `supersedes`), four-section body template (`## Symptom` / `## Root cause` / `## How to avoid` / `## Evidence`), and supersede mechanics (new instructions `3a-gotcha` and `3a-gotcha-supersede`). Goldens regenerated.
- New `plugin/bm-scribe/skills/create-gotcha/SKILL.md` creator skill with dual-mode intake: default reads structured `gotcha`-typed proposals from the most recent `extract_project_knowledge` envelope in the conversation; `--from-review <source>` mines candidates from review text (PR comments via `gh api`, filesystem path, or `paste:` heredoc). Applies the three-step BM v0.21.1 creator pattern with auto-picked ADR number; supersede leg flips the predecessor's `status` to `superseded` without rolling back the new note on failure.
- New `examples/project-knowledge/gotcha.md` template with full frontmatter and the four-section body shape. `examples/project-knowledge/README.md` updated from "Six types in two layers" → "Seven types in three groups" with `gotcha` added under a new "Lessons-learned layer".
- New `` ## Gotcha encoding in `kb_index` `tags` `` subsection in `docs/team-setup/project-knowledge-conventions.md` documenting the canonical `status:<value>` / `module:<slug>` tag format controllers must use to surface gotcha frontmatter through `KBIndexEntryArg.Tags`. No anti-tangent code change required — the encoding rides on the existing `tags` array.
- `plugin/bm-scribe` bumped to `v0.2.0` across all four manifests (`package.json`, `gemini-extension.json`, `plugin/bm-scribe/.claude-plugin/plugin.json`, and the bm-scribe entry in `.claude-plugin/marketplace.json`) for the new creator skill.

### Changed
- `INTEGRATION.md`: renamed "Six note types in two layers" → "Seven note types in three groups" and added the `gotcha` row.
- `internal/verdict/extract_parser_test.go`: renamed `TestParseExtract_AcceptsAllSixTypes` → `TestParseExtract_AcceptsAllSevenTypes`. The renamed test now covers all seven types (`decision`, `module`, `feature`, `glossary`, `epic`, `story`, `gotcha`) via a single table-driven sub-test loop.

### Fixed

### Removed

### Deprecated

### Security

## [0.7.1] - 2026-05-22

### Added
- New design spec `docs/superpowers/specs/2026-05-21-bm-scribe-design.md` for the BM-scribe plugin: twelve subcommands across project-knowledge creators and personal-namespace verbs, the three-step `write_note → move_note → edit_note` permalink-canonicalization contract field-tested against BM v0.21.1, and the personal-namespace shape.
- New `plugin/bm-scribe/` Claude Code plugin scaffolding: `package.json` + `gemini-extension.json` manifests, `README.md` with the twelve-subcommand catalogue, `CLAUDE.md` instructing the plugin's posture (always emit the three-step pattern, never short-cut step 3), and `docs/three-step-pattern.md` with a literal worked example for the load-bearing `write_note → move_note → edit_note` contract.
- Six project-knowledge creator skills under `plugin/bm-scribe/skills/`: `create-epic`, `create-story`, `create-decision` (with `search_notes`-based ADR auto-numbering), `create-module`, `create-feature`, `create-glossary`. All six encode the three-step `write_note → move_note → edit_note` BM v0.21.1 pattern and land at canonical v0.7.0 permalinks (`<PROJECT>/<type-plural>/<key>/main`).
- Three personal-namespace todo skills under `plugin/bm-scribe/skills/`: `add-todo` (handles both create-on-first-use via the three-step pattern and subsequent appends via `insert_before_section`), `list-todos` (prints bullets with numeric indices), `tick-todo` (flips an unchecked bullet to checked with date stamp via `find_replace`). All three target `<USERNAME>/todo/main`.
- Three personal-namespace note skills under `plugin/bm-scribe/skills/`: `add-note` (three-step create at `<USERNAME>/notes/<slug>/main`), `fetch-note` (read + print), `list-notes` (search by `<USERNAME>/notes/` prefix and print titles + permalinks).
- New personal-namespace templates under `examples/project-knowledge/personal/`: `README.md` (overview), `todo.md` (rolling checkbox list at `<USERNAME>/todo/main` with `## Active` / `## Done` sections), and `note.md` (one note per topic at `<USERNAME>/notes/<slug>/main`). The `bm-scribe:add-todo` skill instantiates `todo.md` on first-use create.
- New §9 "Personal namespace (`<USERNAME>/`)" in `docs/team-setup/project-knowledge-conventions.md` documenting the `<USERNAME>/todo/main` and `<USERNAME>/notes/<slug>/main` layouts, the same-BM-project posture, the explicit boundary that anti-tangent's `prime` / `extract` never scan the personal namespace, and a pointer to `plugin/bm-scribe/` for the write side.
- New `.claude-plugin/marketplace.json` at the repo root listing `bm-scribe` as a v0.1.0 plugin, plus `plugin/bm-scribe/.claude-plugin/plugin.json` per Claude Code's plugin-manifest convention. Users can now install the companion plugin via `claude plugin marketplace add patiently/anti-tangent-mcp` followed by `claude plugin install bm-scribe@anti-tangent-mcp`. Both manifests pass `claude plugin validate` (one informational warning that the plugin-root `CLAUDE.md` is not auto-loaded as project context — the Hard Rules it carries are duplicated inside each SKILL.md body, so functionality is unaffected; consider folding them into an auto-loaded skill in a follow-up release).
- README.md gains a "Companion: bm-scribe plugin (v0.7.1+)" section with the two-line `marketplace add` + `plugin install` commands and an ephemeral `--plugin-dir` fallback. The Claude Code one-shot install prompt gains an optional step 9 that installs the companion plugin if the user wants it. The opencode prompt is left untouched — opencode does not load Claude Code plugins.
- `plugin/bm-scribe/README.md` gains an Install section covering both the persistent (marketplace) and ephemeral (`--plugin-dir`) paths.

### Changed
- `INTEGRATION.md` "Project knowledge (optional)" section: moved the "Applying bm_commands to BM v0.21.1" subsection up so it sits directly under "Controller workflow (per epic)" — readers now see the translation contract **before** any bm_commands paste step. The full literal worked example (`write_note → move_note → read_note → edit_note(find_replace)` with annotated BM responses) lives at [`plugin/bm-scribe/docs/three-step-pattern.md`](plugin/bm-scribe/docs/three-step-pattern.md); INTEGRATION.md links to it rather than duplicating to stay under the 40,000-byte user-instructions context budget. Subsection points at `plugin/bm-scribe/` as the encoded form of the contract.
- `INTEGRATION.md` gains a new "v0.7.0 canonical layout" subsection inline (between "Six note types in two layers" and "The `project_knowledge` field"). Tabulates the canonical permalink shape per note type with concrete examples, calls out plural type folders, ADR-numbered decisions (not date-prefix), and the legacy posture of v0.6.x flat shapes. References `plugin/bm-scribe/` as the canonical writer.

### Fixed

### Removed

### Deprecated

### Security

## [0.7.0] - 2026-05-21

### Added
- New 6th note type `story` under the project-knowledge taxonomy. Frontmatter scoped to ticket-driven workflow (issue ID, parent epic, owners, tracker URL); body provides a live operational dashboard with multi-PR list + relationships, subtasks, deployment state, and decisions produced. Template lands as `examples/project-knowledge/story.md`. Schema enum `proposals[].type` in `internal/verdict/extract_schema.json` gains `"story"`; `ProposalTypeStory` constant added to `internal/verdict/extract.go`. Parser is backwards-compatible — v0.6.x five-type proposals continue to parse.
- New adopter conventions doc at `docs/team-setup/project-knowledge-conventions.md`: when this pattern earns its keep, the one-BM-project-per-repo recommendation (with the monorepo namespacing exception), issue-ID format guidance, folder convention, milestone-event list, project-prefix bootstrap, tracker integration, and maintenance ownership.
- New committed dogfood directory `examples/project-knowledge/dogfood/` with frozen-snapshot real anti-tangent example notes (epics/gh-23, stories/gh-25, decisions/0001-text-only-reviewer, modules/review-pipeline). Re-snapshotted manually on major releases.
- Optional `story_origin` frontmatter field on `decision` notes alongside the existing `epic_origin`. Enables extract to populate a story's `## Decisions produced` section by walking `story_origin` matches across decision notes.

### Changed
- `examples/project-knowledge/epic.md` rewritten with live operational dashboard sections (`## Stories` table with status + deployment, `## Open PRs` table aggregated across stories in the epic, `## Acceptance (epic-level)` checklist). Charter + progress-ledger sections from v0.6.0 kept as supporting context.
- All six note templates adopt the project-prefixed folder-per-ticket permalink shape: `<PROJECT>/<type>/<key>/main`. Cross-references in frontmatter become permalink strings. Backwards-compatible — pre-v0.7.0 extract outputs without the project prefix continue to parse.
- `internal/prompts/templates/extract.tmpl` recognises the `story` type, infers the project prefix from `kb_index` permalinks (falls back to `<PROJECT>` placeholder + emits `missing_index_entry` finding when no prefix can be inferred), and proposes dashboard updates only on milestone events (PR opened, PR state transition, deployment landed, decision finalized) via `replace_section` operation bm_commands.
- `INTEGRATION.md` "Project knowledge (optional)" section gains a one-line mention of the 6-type taxonomy and a link to the new conventions doc. Total file size kept under the 40,000-byte user-instructions threshold.

### Fixed
- `docs/team-setup/basic-memory-shared-vm.md` §8 `commit-and-push.sh` script: `GIT_SSH_COMMAND` now includes `-o IdentitiesOnly=yes -o IdentityAgent=none` alongside the existing `StrictHostKeyChecking=yes`. Without `IdentitiesOnly=yes` SSH tries every key in `~/.ssh/` before the explicit `-i` deploy key, so a key that belongs to a different account can auth first and the BM repo push fails with "Permission denied" or "Repository not found". `IdentityAgent=none` defends against `SSH_AUTH_SOCK` leaking into the systemd unit's environment and the agent's keys overriding the deploy key. Both options are now documented inline next to the script with rationale for each.

### Removed

### Deprecated

### Security

## [0.6.2] - 2026-05-21

### Added
- New subsection in `INTEGRATION.md`'s "Project knowledge (optional)" block titled "Applying bm_commands to BM v0.21.1": short tables mapping extract's emitted `bm_commands` arg shape (`{permalink, frontmatter, body}` / `{permalink, section, content}`) to BM v0.21.1's literal `write_note` / `edit_note` MCP signatures, plus a note on the permalink-slug divergence between anti-tangent's proposed slugs and BM's auto-derived ones. Closes #28.

### Changed
- `INTEGRATION.md` trimmed back under the 40k user-instructions context budget. v0.6.0's "Project knowledge (optional)" section is the primary target: the architecture diagram is dropped in favor of a one-line link to the spec, the anchored BM tool-names list is compressed to a link to the verified-contract block in the v0.6.0 plan, and the auto-apply ladder + controller-workflow prose is tightened. Protocol contracts, env var names, error categories, and field names are preserved verbatim — only prose density and content duplicated with the spec are reduced. Mirrors the v0.5.1 trim's posture.

### Fixed

### Removed

### Deprecated

### Security

## [0.6.1] - 2026-05-21

### Added
- New "Alternative: Docker container on an existing host" section in [`docs/team-setup/basic-memory-shared-vm.md`](docs/team-setup/basic-memory-shared-vm.md): run upstream's `ghcr.io/basicmachines-co/basic-memory:0.21.1` (pinned; bump deliberately) against a host bind-mount, expose its SSE transport via a reverse proxy with per-dev bearer-token auth, reuse the existing git-backed sync (host-side systemd timer against the bind-mount). For teams that already run a Docker host and don't want to provision a dedicated VM.

### Changed

### Fixed
- `validate_completion`'s `malformed_evidence` shape-guard no longer false-positives on Go's `./pkg/...` package-recursion syntax in `test_evidence` strings or test-file contents. The `/...` substring pattern added to `evidenceTruncationPatterns` in v0.5.2 was too aggressive — every other v0.5.2 placeholder in the list is comment-form (`/* ... */`, `// snip`, `// elided`, `// ... rest unchanged`) and unambiguous; the bare `/...` is removed. If a real `/...` truncation pattern surfaces in the field, we'll re-add it with a tighter regex (preceded by a comment marker). Fixes #25.

### Removed

### Deprecated

### Security

## [0.6.0] - 2026-05-20

### Added
- New stateless `prime_project_knowledge` MCP tool: given a task spec and a Basic-Memory-style `kb_index`, returns prioritized note picks the controller should attach to the implementer's brief. Optional `bm_commands` paste-ready calls when `ANTI_TANGENT_KB_STORE=basic-memory`.
- New stateless `extract_project_knowledge` MCP tool: given one or more `validate_completion` envelopes, returns structured create/update/supersede proposals for the project KB. Optional `bm_commands` paste-ready calls under the same env gate.
- `validate_task_spec` and `validate_plan` accept an optional `project_knowledge` string. The reviewer treats its contents as authoritative caller-supplied context (same posture as `pinned_by`).
- Six new finding categories: `kb_gap`, `ambiguous_pick`, `missing_index_entry` (prime); `insufficient_evidence`, `redundant_proposal`, `contradicts_existing` (extract).
- Five new env vars: `ANTI_TANGENT_KB_STORE`, `ANTI_TANGENT_PRIME_MODEL`, `ANTI_TANGENT_EXTRACT_MODEL`, `ANTI_TANGENT_PRIME_MAX_TOKENS` (default 4096), `ANTI_TANGENT_EXTRACT_MAX_TOKENS` (default 8192).
- Five note-type templates under `examples/project-knowledge/`: `decision`, `module`, `feature`, `glossary`, `epic`, plus a `README.md`.
- New operator-facing doc `docs/team-setup/basic-memory-shared-vm.md` for teams running a shared Basic Memory on a VM.
- New `INTEGRATION.md` section "Project knowledge (optional)" plus a ~5-line addition to the dispatch clause covering the auto-attached project-knowledge block.
- `README.md` gains one paragraph + link describing the optional KB integration.

### Changed
- INTEGRATION.md and README.md "four tools" references updated to "six tools" — the v0.6.0 pair lands on top of the existing four (`validate_plan`, `validate_task_spec`, `check_progress`, `validate_completion`). README's tool-catalog smoke-test assertion and `max_tokens_override` posture extended to all six.
- `prime_handler` now emits one structured `slog.Info` line on every exit path (success / validation_error / payload_too_large / model_resolution_error / render_error / truncated / reviewer_error) via a deferred logger, matching the pattern shipped for `extract_handler`. Previously only the success path logged.
- `prime_schema.json` and `extract_schema.json` add `minLength: 1` on `bm_commands.args_json`, and `extract_schema.json` additionally constrains `proposals.frontmatter_json` — closes the gap at the OpenAI strict-mode layer before the parser-side rejection fires. `body` and `body_patch` remain unconstrained because empty-string placeholders are valid for those fields per the action-conditional parser path.
- The output-schema hint inside `prime.tmpl` and `extract.tmpl` now enumerates the full 17-category vocabulary (was a truncated subset) so the reviewer is not biased away from valid categories like `scope_drift`, `ambiguous_spec`, or `convention_deviation`.

### Fixed

### Removed

### Deprecated

### Security

## [0.5.2] - 2026-05-19

### Added

- New finding category `attestation_contradiction` (NOT severity-floored — distinct from `convention_deviation` / `unverifiable_codebase_claim`). Emitted by the reviewer when an acceptance criterion explicitly contradicts a caller-attested harness shape; see `harness_shape_attestation` input below. Added to all four reviewer-output JSON schemas and to the parser's `validCategory` allowlist.
- `validate_task_spec` accepts a new optional `harness_shape_attestation` input: a list of `{harness, path, assertions[]}` objects declaring caller-attested shape facts about test harnesses or fixtures. Caps: ≤ 25 entries; harness/path ≤ 240 code points; ≤ 10 assertions each ≤ 480 code points; whitespace-trim + canonical-JSON dedup. Threads through the session and into the pre-hook prompt for reviewer rendering (see Task 15 / pre.tmpl).
- `verdict.FinalizeVerdict(Result) Result` derives the canonical verdict from finding-severity counts via a published ladder: `critical >= 1 OR major >= 2 → fail`; `major >= 1 OR minor >= 3 → warn`; otherwise `pass`. When the `minor >= 3 → warn` branch fires (no critical/major), an advisory `noise_cluster` finding (`severity: minor`, `category: other`, `criterion: noise_cluster`) is appended so callers can see why. Idempotent.
- `verdict.FinalizePlanVerdict(*PlanResult)` derives per-task verdicts via the same severity ladder, derives the plan-level verdict from `PlanFindings`, appends noise_cluster advisories at task and plan level where applicable, and re-runs `ApplyPlanQualitySanity` so `plan_quality` stays consistent with the server-derived `plan_verdict`. Idempotent. Nil-safe.

### Changed

- `README.md` lists `harness_shape_attestation` alongside the existing optional `validate_task_spec` inputs.
- Reviewer is now instructed to demote `major ambiguous_spec` findings to `minor` when a normative test body explicitly pins the ambiguous value/assertion. Demoted findings carry a `(resolved-by-normative-body: <citation>)` suffix on `suggestion` so callers can see why. Instruction lands in both `pre.tmpl` and `post.tmpl`.
- `pre.tmpl` now instructs the reviewer to emit a `minor ambiguous_spec` finding citing INTEGRATION.md §3.7 when plan text contains `.trimIndent()` / `.trimMargin()` / `textwrap.dedent` / tagged-template `dedent` alongside a multi-line string literal comparison.
- Per-task handlers (`validate_task_spec`, `check_progress`, `validate_completion`) now derive `verdict` server-side via `FinalizeVerdict` AFTER suppression/rollup AND after the clamp finding is folded into the result, so `max_tokens_override` clamps participate in the severity ladder. The per-task no-recovery truncation finding is bumped from `minor` to `major` so the ladder derives `warn` consistently with the previously-explicit assignment.
- Hard-rejection synthetic findings (`payload_too_large` in both per-task and plan-level paths, `malformed_evidence`) bumped from `major` to `critical` so the verdict ladder derives `fail` consistently with the envelopes' explicit `Verdict: fail`. `session_not_found` was already `critical` and is unchanged.
- `validate_plan` derives per-task and plan-level verdicts server-side via `FinalizePlanVerdict`, which slots into the existing `finalizePlanResult` pipeline after unverifiable-rollup and calibration. The plan-level `max_tokens_override` clamp now participates in the severity ladder. The plan-level no-analysis truncation finding remains `major` (already was — confirmed by regression test).
- `controller_verified_references` suppression for `unverifiable_codebase_claim` findings now runs server-side (deterministic Go-side) in addition to the existing reviewer-prompt instruction. Suppression scope is per-claim: any CVR-entry substring match against the finding's `evidence` or `criterion` (either direction) suppresses the entire finding. 4-code-point floor on CVR entries prevents single-letter false matches.
- `pre.tmpl` CVR-suppression instruction now includes a worked multi-symbol example, mirroring the Go-side `suppressUnverifiableCodebaseClaim` semantics.
- `pre.tmpl` gains a `## Harness shape attestations` section (rendered only when `harness_shape_attestation` is non-empty) and instructs the reviewer to emit `attestation_contradiction` findings ONLY for explicit AC-vs-attestation contradictions (not for absent capabilities).
- `validate_completion` now sees `normative_test_bodies` from the session at post-hook time. `post.tmpl` renders a `## Normative test bodies (binding)` section that instructs the reviewer to treat the bodies as authoritative for fixture state, exact strings, and assertions; AC-vs-fixture mismatches are suppressed when a body pins the value. Lightweight mode (empty `session_id`) is unaffected — no session, no bodies, no section.
- `INTEGRATION.md` documents `harness_shape_attestation` (§3.8 + §4.2 args list), the `attestation_contradiction` finding category (§6 FAQ), the deterministic server-side CVR suppression (§5.7), and adds the `check_progress` trigger nudge ("test that 'should' fail doesn't" / ">5 min debugging") to both §4 lifecycle table and §4.2 paste-clause "During work" step.

### Fixed

- `validate_completion` `malformed_evidence` shape-guard extended with six new placeholder/truncation patterns observed in the field: `/* ... */`, `/* ...rest unchanged */`, `// snip`, `// elided`, `// ... rest unchanged`, `/...`. Each is matched (case-insensitive substring) against BOTH `final_diff` AND every `final_files[].content`.

### Removed

### Deprecated

### Security

## [0.5.1] - 2026-05-19

### Added

### Changed
- `INTEGRATION.md` trimmed for the 40k user-instructions context budget: §2 Setup (install / register / provider keys / model split / smoke test) removed in favor of `README.md`, which gains a new `### Picking a reviewer model` subsection (the implementer→reviewer mapping table) and a `### Smoke test` one-liner. `INTEGRATION.md` opens with a one-line cross-reference to `README.md` for install/configure and is now scoped strictly to using-the-MCP protocol.
- `INTEGRATION.md` §3 trimmed: §3.4 "Mapping to existing plan-writers" removed (the header-block + Files/Steps pattern is documented in §3.1 and applies across plan-writers without per-tool guidance); §3.2 worked-example trailing prose dropped — §3.3 covers what `validate_task_spec` checks.
- `INTEGRATION.md` §4 consolidated: the line-314 lightweight callout AND §4.1 protocol summary collapsed into one short preamble under the §4 H2; §4.2a (short dispatch shape) and §4.2b (language-scoping caveat) folded inline as notes within §4.2; CodeScene companion subsection trimmed to its complementary-scope rationale + tool-to-phase mapping + advisory-posture / lightweight-mode notes (consumer setup links delegated to upstream); §4.4 Concrete examples deleted in full — Example A's lesson is covered by §3.2/§3.3, Example B by §5.4, and Example C by §6 FAQ.
- `INTEGRATION.md` §5 tightened: §5.2 dispatch-addendum collapsed from 4 paragraphs + per-skill bullets to a single paragraph; §5.6 and §5.7 merged into a single `### 5.6 Per-call tool args and partial-response handling` subsection (covering `max_tokens_override`, `mode`, and `partial: true`); former §5.8 renumbered to §5.7 and the two paragraphs duplicating §5.6 / §6 FAQ content removed.
- `INTEGRATION.md` §3.6 (normative test bodies) and §3.7 (`.trimIndent()` caveat) compressed by ~60% — protocol surface is preserved (marker shape, server-side extraction, 4000-code-point cap, `// excerpt:` escape hatch, one-source-line + render-aware-AC rules); explanatory prose dropped. §6 FAQ trimmed by removing three entries that fully duplicate other sections (plan-handoff gate failure → §5.1; reviewer-is-wrong → §4.3; ad-hoc code changes → §1). Final `INTEGRATION.md` size: 33,186 chars (was 50,757; under the 40,000 user-instructions warning threshold by 6,814 chars).

### Fixed
- `validate_plan` failed with OpenAI provider HTTP 400 (`Invalid schema for response_format 'review': … Missing 'exit_contracts'`) whenever the reviewer was actually invoked. Root cause: OpenAI structured-outputs `strict: true` requires every property in a JSON-schema object to appear in `required`. The v0.5.0 task-items schema declared `exit_contracts` / `exit_contracts_inferred` (and v0.4.0 had earlier added `lightweight_eligible` / `lightweight_reason`) as optional `properties` without listing them in `required`. Both `plan_schema.json` and `tasks_only_schema.json` patched; a new `internal/verdict/schema_invariants_test.go` regression test asserts every property must be in `required` across all four reviewer-output schemas so the class of bug cannot recur silently. Anthropic and Google providers were not impacted (they don't enforce strict-mode at the request layer).

### Removed

### Deprecated

### Security

## [0.5.0] - 2026-05-18

### Added
- New finding category `convention_deviation` (minor-floored) emitted when a `codebase_conventions` entry conflicts with the spec. Added to the reviewer-output JSON schema category enums.
- `validate_task_spec` accepts optional `test_strategy_notes`, `codebase_conventions`, `testability_extractions`, and `normative_test_bodies` so controllers can surface joint-coverage intent, module conventions, intentional testability extractions, and binding test bodies that the structured-fields-only spec otherwise hides from the reviewer.
- `validate_plan` task results include optional `normative_test_bodies`, populated server-side by deterministic markdown extraction of `**NORMATIVE TEST BODIES (verbatim):**` sections from each task's plan markdown.
- `validate_plan` task results include optional `exit_contracts` (hybrid: explicit `**Exit contracts:**` section if present, reviewer-inferred otherwise) with a sibling `exit_contracts_inferred` provenance flag.
- `validate_completion` accepts optional `exit_contracts` plus `exit_contracts_inferred`; reviewer flags misses as `missing_acceptance_criterion` with `criterion: exit_contract`, calibrating miss severity by provenance.

### Changed
- `pre.tmpl` treats `normative_test_bodies` as binding AC, treats adjacent complementary tests as joint coverage when `test_strategy_notes` explains the split, emits `convention_deviation` findings on observed deviations from `codebase_conventions`, and respects `testability_extractions` when judging scope drift.
- `validate_task_spec` deterministically suppresses reviewer-emitted `scope_drift` findings whose evidence names a caller-supplied `testability_extractions` entry (substring match in either direction).
- `plan.tmpl` and `plan_tasks_chunk.tmpl` ask the reviewer to populate `exit_contracts` and `exit_contracts_inferred` per task. `plan.tmpl` also notes that `normative_test_bodies` is populated server-side and must not be reviewer-emitted.
- `post.tmpl` renders a provenance-aware `Exit contracts (...)` section when `exit_contracts` is non-empty and instructs the reviewer to walk each contract against final-file evidence.
- Integration docs add the normative-test-bodies convention, CVR-scope clarification (single-category suppression; `convention_deviation` not suppressed), `.trimIndent()` raw-string caveat, language-scoping prose caveat, and a lightweight-mode callout at the top of the implementer section. (Doc-only items folded under `### Changed` per project CLAUDE.md convention on Keep-a-Changelog subsections; v0.4.0 used `### Documentation`, which is a divergence — this release re-aligns.)
- README ships a one-shot paste-in install prompt for Claude Code and opencode under `## Install`. The prompts fetch the latest release, place the binary in `~/.local/bin`, register the MCP at user scope, download `INTEGRATION.md` to the host's user-instructions dir, and wire it into `~/.claude/CLAUDE.md` (Claude Code) or opencode.json's top-level `instructions` array (opencode, per INTEGRATION.md). Linux/macOS scope; secrets-redaction directive included. The opencode prompt defaults to `{env:NAME}` substitution for the reviewer API key (with `{file:path}` and literal-value paths offered as alternatives) so the secret never has to be written into `opencode.json` by default.

### Fixed

### Removed

### Deprecated

### Security

## [0.4.0] - 2026-05-17

### Added
- `validate_task_spec` accepts optional `controller_verified_references` entries so controllers can identify codebase references they already grep-verified before dispatch.
- `validate_plan` task results include optional `lightweight_eligible` and `lightweight_reason` fields to guide controller-side lightweight dispatch decisions.
- `validate_plan` caches identical passing plan reviews in memory for 3 minutes, returning cached hits with `review_ms: 0` and a `[cached <=3m]` `next_action` prefix.

### Changed
- `validate_task_spec` rolls multiple per-task `unverifiable_codebase_claim` findings into one `codebase_reference_checklist` finding.
- `validate_completion` prompts now include prior major pre-task findings so reviewers can check whether the implementation mitigated them.
- `validate_task_spec` prompt guidance is tuned for test-only tasks to reduce repeated low-value `null`/`unchanged` ambiguity findings while preserving invocation-count and negative-assertion critiques.

### Documentation
- Integration docs clarify `pinned_by` vs `context` vs `controller_verified_references`, shorten the target dispatch clause, and make CodeScene's deterministic mid-task safeguard recommended when configured.

## [0.3.3] - 2026-05-14

### Added
- `validate_task_spec` accepts optional `pinned_by` entries naming existing tests, docs, commands, or static checks that pin behavior, plus optional `phase` (`pre` default, `post` for post-hoc/session-recovery reviews).
- `validate_completion` prompts now highlight summary-referenced doc/artifact paths that are missing from `final_files` and `final_diff` evidence.

### Changed
- `validate_plan` now scales its default output-token budget by task count when no `max_tokens_override` is supplied, bounded by `ANTI_TANGENT_MAX_TOKENS_CEILING`.
- No-analysis `validate_plan` truncation responses now emit a `warn` envelope with a `major` finding and self-contained retry guidance.
- Task-level `unverifiable_codebase_claim` findings from `validate_plan` are rolled up into a single plan-level `codebase_reference_checklist` finding.
- Plans whose only findings are minor `unverifiable_codebase_claim` checklist items now return `plan_verdict: pass` with `plan_quality: actionable` (preserving `rigorous` when the reviewer already emitted it).

### Fixed
_None._

### Removed
_None._

### Deprecated
_None._

### Security
_None._

### Documentation
- New `INTEGRATION.md` sections: `### Reducing text-only review noise` (caller discipline learned from YN-10178), `### Using v0.3.3 review-context features` (`pinned_by` / `phase` / adaptive-plan retry / completion-evidence selection examples), and a setup checklist under the existing CodeScene companion section.
- New `### validate_task_spec arguments (v0.3.3+)` subsection in `README.md` plus two paragraphs in the `validate_plan` section covering the adaptive budget and unverifiable-rollup behavior.

## [0.3.2] - 2026-05-13

### Added
- Documentation for [CodeScene MCP](https://github.com/codescene-oss/codescene-mcp-server) as the recommended optional companion. Anti-tangent is text-only by design; CodeScene's deterministic Code Health analysis closes the codebase-grounded blind spot. New `### CodeScene MCP companion (optional)` section in `INTEGRATION.md` covers tool-to-phase mapping (`pre_commit_code_health_safeguard` mid-task, `analyze_change_set` before DONE), advisory posture, and lightweight-mode interaction. `README.md` gains an attribution + overview section.

### Changed
- Dispatch-clause template in `INTEGRATION.md` gains optional Step 2b (`pre_commit_code_health_safeguard` mid-task) and Step 3b (`analyze_change_set` before DONE). Both gated on "if codescene-mcp is configured in your host" — silent skip when absent. Anti-tangent itself is unchanged; the integration lives at the convention layer.
- `examples/lightweight-dispatch.md` notes that lightweight tasks skip the CodeScene companion calls too.

### Fixed
_None._

### Removed
_None._

### Deprecated
_None._

### Security
_None._

### Documentation
- New `### Companion tool: CodeScene MCP (optional)` section in `README.md` attributes [CodeScene](https://codescene.com) and describes the pairing.

Closes [#14](https://github.com/patiently/anti-tangent-mcp/issues/14).

## [0.3.1] - 2026-05-13

### Added
- `summary_block` field on every tool response: paste-ready textual envelope (verdict, findings, model_used, review_ms, session_ttl_remaining_seconds) that implementers can copy verbatim into DONE reports. Reduces the protocol's reliance on the implementer correctly formatting JSON.
- `plan_quality` field on `PlanResult` (`rough` | `actionable` | `rigorous`). Separate axis from `plan_verdict` — tracks "how close to ship-ready" rather than "is this dispatchable." Reviewer-emitted with a server sanity check (critical findings or `fail` verdict force `rough`; missing/invalid values fall back to verdict-based default).
- `unverifiable_codebase_claim` finding category: lets the reviewer explicitly flag plan or task-spec statements it cannot verify from text alone (field names, signatures, file paths, repo conventions) rather than silently passing or fabricating critiques. Server enforces `severity: minor` for this category. Applies to `validate_plan` and `validate_task_spec` (both text-only inputs); not applied to `check_progress` / `validate_completion` which receive code.
- `malformed_evidence` finding category: the new `validate_completion` evidence-shape guard rejects submissions that contain truncation markers (`(truncated)`, `[truncated]`, `// ... unchanged`, etc.) or empty `final_files.Path` entries — saves strong-model time on cycles that were driven by tooling friction rather than correctness. Replaces the (misleading) previous reuse of `payload_too_large` for shape failures. Note: if the file you're submitting legitimately contains one of these literal strings, send a complete `final_diff` instead of pasting the file via `final_files`.
- `examples/lightweight-dispatch.md` reference template for trivial tasks (doc edits, mechanical relocations).

### Changed
- `check_progress` demoted from RECOMMENDED to OPTIONAL in the dispatch-clause template. Field data showed 0 substantive catches across 5 representative tasks; the call is now advisory.
- `validate_completion` rejected-submissions are cached for 5 minutes by canonical content hash to short-circuit identical re-submissions (see the new `malformed_evidence` category above).
- `validate_completion` now accepts an empty `session_id` when `final_files`, `final_diff`, or `test_evidence` is non-empty — supports the new lightweight protocol mode. The reviewer is called with a synthesized task spec (Goal = `args.Summary`, no ACs).
- `summary_block` population moved to the marshalling helpers (`envelopeResult` / `planEnvelopeResult`) so every exit path — happy paths, partial-recovery, legacy-truncation, `notFoundEnvelope`, `tooLargeEnvelope`, `noHeadingsPlanResult`, evidence-shape rejection — populates the field automatically.

### Fixed
_None._

### Removed
_None._

### Deprecated
_None._

### Security
_None._

### Documentation
- New `## Scope and limits` section in `INTEGRATION.md` explicitly documents the text-only architectural boundary: what the tool catches, what it structurally cannot (codebase symbol existence, function signatures, repo-wide invariants encoded elsewhere, CI/test policy), and the recommendation to pair with a codebase-aware review for any plan that lands in real code.
- New `### Lightweight protocol mode` section in `INTEGRATION.md` documents the controller-side convention for trivial tasks.

Closes [#12](https://github.com/patiently/anti-tangent-mcp/issues/12).

## [0.3.0] - 2026-05-12

### Added
- `max_tokens_override` optional arg on all four tools (`validate_task_spec`, `check_progress`, `validate_completion`, `validate_plan`) for per-call control over the reviewer's output-token budget. Clamped to `ANTI_TANGENT_MAX_TOKENS_CEILING` (default 16384); over-ceiling values emit a `minor` clamp finding so the behaviour is visible. Negative values are rejected at the handler boundary.
- `mode: "quick" | "thorough"` optional arg on `validate_plan`. `quick` instructs the reviewer to surface at most 3 most-severe findings per scope (plan-level and each task) and omit stylistic nits; `thorough` (default) preserves prior behavior. Invalid values are rejected at the handler boundary.
- `partial: true` field on `Result` and `PlanResult` envelopes when the reviewer's response was truncated at the `max_tokens` cap but partial findings could be recovered. Marshaled with `omitempty` so the field is absent in the common (non-truncated) case.
- Hypothetical-marker guardrail (`e.g. illustrative —` prefix) added as a 4th paragraph in the `## Reviewer ground rules` block in `validate_plan` templates, complementing the 0.2.1 epistemic-boundary work.
- `next_action` specificity nudge in `validate_plan` templates: the field must name the single highest-leverage finding, not generic advice.
- `ANTI_TANGENT_MAX_TOKENS_CEILING` env var (default 16384) caps the per-call `max_tokens_override` value.

### Changed
- The synthetic truncation finding emitted on `max_tokens` cap hits is now `severity: minor` (was `major`), with wording that references both the env-var and `max_tokens_override` mitigations.

### Fixed
- Reviewer-output truncation no longer discards complete findings produced before the cap hit. All four tools now run truncated responses through a tolerant JSON parser and emit any recoverable findings alongside a downgraded (`minor`) truncation marker. Previously, ~9 KB of plan input could yield zero usable feedback when the reviewer's output cap was reached mid-response. Closes [#10](https://github.com/patiently/anti-tangent-mcp/issues/10).

### Removed
_None._

### Deprecated
_None._

### Security
_None._

## [0.2.1] - 2026-05-12

### Changed
- `validate_plan` prompt templates (`plan.tmpl`, `plan_findings_only.tmpl`, `plan_tasks_chunk.tmpl`) now include a `## Reviewer ground rules` block that pins the reviewer's epistemic horizon to the plan text — no claims about behavior of code symbols the reviewer cannot see. `unstated_assumption` findings are constrained to assumption gaps visible in the plan itself, and every finding's `evidence` field must point at plan text (present or expected-but-absent). Closes [#8](https://github.com/patiently/anti-tangent-mcp/issues/8).

## [0.2.0] - 2026-05-12

### Added
- `validate_completion` accepts optional `final_diff` evidence for unified diffs.
- Stateful hook envelopes include optional `session_expires_at` and `session_ttl_remaining_seconds`.
- Reviewer-response truncation is detected and surfaced as structured findings with max-token retry guidance.

### Changed
- **(breaking)** `validate_completion` now requires at least one of `final_files`, `final_diff`, or `test_evidence` to be non-empty. Summary-only completion requests are rejected with `validate_completion: at least one of final_files, final_diff, or test_evidence must be non-empty`. Migration: include test command output in `test_evidence` (smallest path), a unified diff in `final_diff`, or full files in `final_files`. Rationale: the reviewer prompt rewrite grades against concrete evidence; summary text alone caused the over-firing pattern in #6 §3.
- Default `ANTI_TANGENT_REQUEST_TIMEOUT` is 180s.
- Timeout errors include the configured timeout and `ANTI_TANGENT_REQUEST_TIMEOUT`.
- Invalid model override errors list supported models for the selected provider.
- `validate_completion` review guidance grades `final_files` / `final_diff` / `test_evidence` (not the `summary`), treats the task spec's `Context:` block as authoritative when it disambiguates an AC, and biases ambiguous-but-fully-covered evidence toward `verdict: pass` with a `category: quality` finding while reserving `severity: major`/`critical` for affirmative contradictions or for an AC left unaddressed.
- `validate_plan` chunk prompts ask reviewers to echo the `Task N:` prefix verbatim.
- Payload-too-large findings include tool-specific retry suggestions (`final_diff`-or-split for `validate_completion`; smaller `changed_files`-or-split for `check_progress`).

### Fixed
- Chunked `validate_plan` identity reconciliation accepts task titles when reviewers strip the `Task N:` prefix while still rejecting wrong or duplicate tasks.

### Removed

_None._

### Deprecated

_None._

### Security

_None._

## [0.1.4] - 2026-05-11

### Added
- `validate_plan` now automatically chunks large plans so reviewer responses don't truncate mid-JSON. Plans with more than `ANTI_TANGENT_PLAN_TASKS_PER_CHUNK` tasks (default 8) are reviewed via one Pass-1 plan-findings call plus `ceil(n/N)` per-chunk calls; the merged `PlanResult` is identical in shape to the single-call path. Plans of 8 tasks or fewer take the existing single-call path unchanged.
- Three new optional env vars: `ANTI_TANGENT_PER_TASK_MAX_TOKENS` (default 4096) governs output budget for `validate_task_spec` / `check_progress` / `validate_completion`; `ANTI_TANGENT_PLAN_MAX_TOKENS` (default 4096) governs output budget for `validate_plan` (single-call and per-chunk); `ANTI_TANGENT_PLAN_TASKS_PER_CHUNK` (default 8) sets both the chunking threshold and per-chunk task count. All three reject zero / negative / non-integer values at startup.
- Per-chunk identity validation: the chunked path verifies every returned `task_title` matches one of the requested chunk's headings (no duplicates, exact count). Mismatch triggers the existing retry-once path; second failure surfaces as an error rather than partial results.
- Gated e2e test `TestValidatePlan_E2E_LargePlanChunked` (build tag `e2e` + `ANTI_TANGENT_E2E_LARGE=1`) exercising the chunked path against a live OpenAI reviewer with a 25-task plan.

### Fixed
- `validate_plan` returning `decode plan result: EOF` on plans of ~12+ tasks. Root cause was a hardcoded `MaxTokens: 4096` cap that the reviewer's JSON response was overflowing on dense plans; both the cap is now configurable and the chunking path keeps each individual response well within budget.

## [0.1.3] - 2026-05-10

### Added
- `google:gemini-3.1-pro-preview` and `google:gemini-3.1-flash-lite` to the reviewer-model allowlist (verified via the Gemini `models.list` endpoint as supporting `generateContent`).
- `openai:gpt-5.5` and `openai:gpt-5.4-mini` (bare-name aliases that route to the latest dated snapshot). Verified live against `/v1/chat/completions` with `response_format: json_object`. The dated `gpt-5.5-2026-04-23` and `gpt-5.4-mini-2026-03-17` entries remain for callers who want pinned snapshots.
- README and `INTEGRATION.md`: opencode (`~/.config/opencode/opencode.json`) registration example, and a "Supported reviewer models" table grouped by provider so callers can see what `ANTI_TANGENT_*_MODEL` accepts at a glance.

## [0.1.2] - 2026-05-10

### Fixed
- Release workflow: write the release-notes file to `$RUNNER_TEMP` instead of the checkout directory. The previous path (`.release-notes.md` in the work tree) made GoReleaser see a dirty git state and refuse to publish. Moving the file outside the work tree keeps the tree clean and lets GoReleaser run end-to-end without `--skip=validate`.

## [0.1.1] - 2026-05-10

### Added 
- Extending .gitignore with claude droppings
- Fixing release task 

## [0.1.0] - 2026-05-07

### Added
- Initial release. MCP server (`anti-tangent-mcp`) exposing three tools that
  review implementing-subagent work at the start, middle, and end of a task:
  - `validate_task_spec` — checks structural completeness, AC quality, and
    unstated assumptions before coding begins.
  - `check_progress` — flags scope drift, untouched ACs, and unaddressed
    prior findings during implementation.
  - `validate_completion` — walks every AC and non-goal in a final review.
- Multi-provider reviewer support: Anthropic Messages API (tool_use),
  OpenAI Chat Completions (json_schema), Google Gemini generateContent
  (responseSchema). Per-hook model defaults overridable per call.
- In-memory session store with configurable TTL (default 4h).
- Cross-platform binaries via GoReleaser (linux/darwin/windows × amd64/arm64).
- Distroless static container image published to ghcr.io.
- GitHub Actions CI (changelog enforcement, `go test -race`) and release
  workflow (commit-tag-driven semver bump, tag, GoReleaser, GHCR push).
- `validate_plan` MCP tool — plan-level handoff gate that reviews an entire implementation plan in one call and proposes ready-to-paste structured-header blocks (Goal / Acceptance criteria / Non-goals / Context) for tasks that lack them. Replaces the per-task plan-handoff loop.
- `ANTI_TANGENT_PLAN_MODEL` env var — overrides the model used by `validate_plan`. Defaults to `ANTI_TANGENT_PRE_MODEL`.
