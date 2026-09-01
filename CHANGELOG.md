# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.17.0] - 2026-09-01

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
- **`context_paths` sends file contents verbatim to the configured reviewer vendor.** Everything
  the caller attaches is transmitted to whichever third-party API `ANTI_TANGENT_PLAN_MODEL`
  names, in full, on every reviewer call of the round. Attach only what the plan makes claims
  about, and set `ANTI_TANGENT_PLAN_ROOTS` to bound what the server will read on the caller's
  behalf. The tool description now says so at the call site.

### Fixed
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
  rendered marker verbatim (`^--- (?:BEGIN|END) FILE <token>: `), so a line carrying the CORRECT
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
