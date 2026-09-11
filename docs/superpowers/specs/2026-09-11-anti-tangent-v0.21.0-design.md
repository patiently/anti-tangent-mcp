# anti-tangent-mcp v0.21.0 — removing repository-defined command execution from the close-time hook

**Status:** design
**Date:** 2026-09-11 (**revised same day** after an adversarial review — see [Revision](#revision-what-the-review-changed))
**Issue:** [#75](https://github.com/patiently/anti-tangent-mcp/issues/75) — `git_added_lines` runs repository-defined commands; plus two smaller findings from the same audit

## Summary

`anti-tangent-guard` 0.3.0 derives "which lines are new" for a `final_files` completion by running
`git diff HEAD -- <path>` in the directory of each submitted path. To compare a worktree file
against a blob, git must first convert the worktree side, and that conversion runs the content
filter the repository's own `.git/config` names. The hook therefore executes a command chosen by
the repository it is pointed at.

The module already knows this class of hazard. `core.fsmonitor` is pinned with the comment "a
repository config can point it at an arbitrary command, which git would otherwise run from inside
this hook." The content filters are the same class and are not pinned.

The fix is structural rather than enumerative. git offers no flag that disables content filters,
and `-c` accepts no wildcard, so pinning `filter.<name>.*` would require first reading the hostile
repository's config to learn which names to pin. Instead the module stops asking git to convert
the worktree at all: the `HEAD` side comes from `git cat-file blob`, which emits the raw object,
the worktree side is an ordinary file read, and the comparison happens in Python using the
`added()` function the write-time hook already uses.

That alone is not enough, and an adversarial review of the first draft is what established it. A
blob missing from the object store sends `cat-file` down a partial-clone lazy fetch that execs a
repository-controlled command — pre-existing, since the current `git diff` does the same, but not
closed by the structural move. `protocol.allow=never` and `GIT_NO_LAZY_FETCH=1` close it, and the
claim this design makes is correspondingly widened: no call converts worktree content, and no call
fetches a missing object.

Two smaller findings from the same audit ride along: a `*`-led line inside a multi-line string
literal is scanned as a block-comment continuation and refuses its own write, and the
false-positive gate drops `ANTI_TANGENT_TICKET_PATTERN` before importing the scanner, so an
operator gets no false-positive measurement for the pattern the project is being asked to set.

## Revision: what the review changed

The first draft was reviewed adversarially against scratch repositories on git 2.43.0. It came
back **SHIP WITH FIXES** with five blocking findings. Every one was reproduced independently
before being accepted here; three of them invalidated claims the draft made.

1. **The fix as drafted left a repository-controlled exec path open.** `cat-file blob` on a blob
   that is absent from the object store makes a partial-clone repository lazily fetch it, and the
   fetch execs `core.sshCommand` / `remote.origin.uploadpack` / `git-remote-<vcs>`. Reproduced:
   `cat-file blob HEAD:f.go` emitted `SSH FIRED: evil.example git-upload-pack '/x.git'`. The
   current `git diff` fires identically, so this is pre-existing rather than introduced — but the
   draft's security claim was scoped to `filter.*` and would have shipped believing the hole
   closed. Now pinned shut (§1g).
2. **The draft's "`HEAD` verified + `cat-file` failed ⇒ the whole file is new" rule was unsound.**
   It mapped *every* failure onto a whole-file scan, which is the false-block direction this module
   is forbidden to err in. Confirmed: a single Latin-1 byte anywhere in the `HEAD` blob makes
   `subprocess.run(text=True)` raise, `_git` returns `(127, "")` through its `except Exception`,
   and a pre-existing `// café fixes #1` on an untouched line would then block the close. Today
   that file yields exactly one added line. Fixed by demanding a positive not-in-`HEAD` signal
   (§1c) and by reading the blob as bytes (§1e).
3. **Filter-tracked files become whole-file scans.** The clean filter this design stops running is
   the very thing that made a git-lfs pointer comparable to its worktree content. Without it,
   every line of an LFS or git-crypt file reads as added. The converse is worse:
   `working-tree-encoding=UTF-16` currently catches a genuine `// fixes #1` and would become a
   silent miss. `filter` is now skipped explicitly; `working-tree-encoding` is *honoured* by
   decoding the worktree with the declared codec, which the review proposed skipping and which
   turns out to recover the detection for free (§1f).
4. **The `*`-continuation tokenizer was under-specified in a way that loses detections.** A single
   stray apostrophe — a Rust lifetime, an apostrophe in JSX text, a C `#error` — opened a string
   span that ran to end of file and suppressed every genuine block comment after it. Today's
   per-line `_quotes_balanced` confines that damage to one line. Fixed by an end-of-line resync
   rule and by running the walk lazily (§2b).
5. **A non-UTF-8 `HEAD` blob is already skipped today**, not partially answered. The draft said
   the new code would be "less informative than the old" here. It is the reverse: the decode
   failure that skips it today happens on the diff output too, and reading bytes makes the file
   scannable for the first time. Caught by the plan-handoff gate, after the review. (§1e,
   Compatibility)
6. **`git diff HEAD` does not fail on a staged-new file.** The draft's state matrix and its
   compatibility note both said it did. It returns rc 0 and the whole file. Reproduced. So the
   three-way split still has to exist, but it *preserves* current behaviour rather than widening
   it, and the draft's one "widens" claim was false (§1d, Compatibility).

Smaller corrections from the same review are folded in where they belong: the
`core.alternateRefsCommand` pin is dropped as never-consulted, `read_text_capped` does not fail on
undecodable bytes (it replaces them), the context hand-off needs a return-shape decision (§2c),
Claude Code's `Edit` has a `replace_all` mode the draft ignored (§2c), and a fixture arming both
`filter.x.process` and `filter.x.clean` only ever exercises `process`, so the two need separate
fixtures (Testing).

## Verification of the report

Every claim below was reproduced against git 2.43.0 in a scratch repository before this design was
written, using a filter command that appends to a sentinel file. **The report is wrong on one
point, in the direction that strengthens the case for the structural fix.**

The report states: "Checked and **not** firing: plain `clean`/`smudge`, `textconv`, external diff,
aliases, `core.hooksPath`. `filter.*.process` is the live one."

`filter.<name>.clean` fires as well. Against a tracked, modified file:

| config key | `git diff --no-ext-diff --no-textconv HEAD -- <path>` | `git cat-file blob HEAD:<rel>` |
|---|---|---|
| `filter.x.process` | **fires** | clean |
| `filter.x.clean` | **fires** | clean |
| `filter.x.smudge` | clean | clean |

`smudge` staying clean is expected — it is the checkout direction, and nothing here checks out.
`clean` firing is the point: it is the conversion `git diff` performs on the worktree side, and it
is the most ordinary of the three keys to find in a real `.git/config`. An enumeration fix would
have had to cover it, and the report would have led that enumeration to omit it. This is the
second time the pin list has been found incomplete; the design does not attempt a third list.

Every call the fixed module still makes was checked against all three keys armed at once, and
independently re-checked by the review against `include.path`, `.git/info/attributes`,
`diff.*.textconv`, `diff.*.command`, `diff.external`, `core.pager`, `core.hooksPath`,
`credential.helper`, `gpg.program`, `core.editor` and aliases shadowing builtins:

```
ls-files -z --full-name --error-unmatch   clean
check-attr -z filter working-tree-encoding clean
check-ignore -q                            clean
rev-parse --show-toplevel                  clean
rev-parse --verify HEAD                    clean
ls-tree -z HEAD -- ":(top)<rel>"           clean
cat-file blob HEAD:<rel>                   clean  (with the §1g caveat)
```

`cat-file blob` being clean is a controlled result, not an absence of evidence: the same sentinel
harness *does* observe a filter firing through `git cat-file --filters --path=<p> HEAD:<p>`, which
is the documented opt-in. The harness can see cat-file; plain `cat-file blob` genuinely does not
convert.

The caveat matters and is not a filter: a *missing* blob in a partial clone sends `cat-file` down a
lazy-fetch path that execs a repository-controlled command. §1g closes it. The current `git diff`
has the same behaviour, so it is not introduced here — but "clean against `filter.*`" is a narrower
claim than "runs no repository-defined command", and the draft conflated them.

## Non-goals

- **No attempt to sandbox git.** The hook keeps running as the user, in a directory it did not
  choose. The claim made here is narrower and checkable: **no call this module makes converts
  worktree content, and none of them fetches a missing object** — the second half added after a
  review found the draft's `filter.*`-scoped claim left a live exec path open (§1g). It is a claim
  about the calls made, not a guarantee about every future git version.
- **No enumeration of command-valued config keys as the primary defence.** The pin list is kept
  and documented as best-effort hardening. What closes the hole is not calling the converting
  command.
- **No change to what counts as a violating comment.** `TELLS` is untouched.
- **No change to the advisory MCP server.** All three findings are in the guard plugin.

## Part 1 — F1: the close-time hook executes repository-defined commands

### 1a. The replacement sequence

The tracked-path branch of `final_files_added_lines()` currently runs one `git diff` and parses
`+` lines out of it. It becomes, in order:

```python
rc, rel  = _git(parent, "ls-files", "-z", "--full-name", "--error-unmatch", "--", path)
                                               # tracked? and the repo-relative name, in one call
skip if check_attr(basename) sets filter or working-tree-encoding     # §1f
new_text = read_text_capped(path, encoding)   # encoding comes from the probe just above
rc, rec  = _git(parent, "ls-tree", "-z", "HEAD", "--", ":(top)" + rel)       # §1c
rc, blob = _git_bytes(parent, "cat-file", "blob", "HEAD:" + rel)             # §1e
lines    = added(blob_text, new_text)
```

The read cannot run first: its `encoding` argument is the attribute probe's output, so `ls-files`
(which supplies `rel`) and `check_attr` (which supplies `encoding`) both have to run before
`read_text_capped` can. `added()` lives in `comment_scan.py` and is already the write-time hook's
definition of an added line. Both hooks converge on one definition, which they do not share today.

### 1b. Resolving `rel` without `os.path.relpath`

`cat-file` addresses a blob as `HEAD:<repository-relative path>`. The draft computed that with
`os.path.relpath(path, _repo_root(...))`. That is wrong whenever the worktree root is reached
through a symlink — `/tmp` and `/var` on macOS, any symlinked home — because `--show-toplevel`
prints the realpath while the submitted path does not. The relative path then comes out as
`../link/f.go`, and `HEAD:../link/f.go` is interpreted as cwd-relative and rejected:

```
os.path.relpath(path, root)  ->  ../link/f.go
cat-file blob HEAD:../link/f.go  ->  fatal: '../link/f.go' is outside repository
```

`git ls-files --full-name` answers the same question correctly, because git resolves symlinks and
`core.worktree` itself. It is also a call the module already makes, so taking `rel` from its
output costs nothing: `-z` for a NUL-terminated record, `--error-unmatch` for the tracked test that
call already performs.

This makes `core.quotePath=false` genuinely load-bearing for the first time — `ls-files` quotes
non-ASCII names when it is on (`"\303\274.go"`), and that output is now parsed. §1g no longer has
to hedge about why the pin is kept.

### 1c. A positive not-in-`HEAD` signal

The draft inferred "not in `HEAD`" from `cat-file` failing. That is unsound: it maps *every*
failure — a decode error, a timeout, an unfetchable blob, a malformed object — onto "the whole file
is new", which blocks the close on comments the task never wrote. It also contradicts this module's
own standing rule, stated in its docstring for `ls-files` and `check-ignore`: a question that could
not be answered means skip, never guess.

So the absence of a blob must be established *positively*, by a call that does not read the object
and that keeps genuine absence separate from a failure to look:

```
git ls-tree -z HEAD -- ":(top)<rel>"
  rc 0, one NUL-terminated record  -> the path is in HEAD
  rc 0, no records                 -> it is not
  rc != 0                          -> unanswerable; skip the path
```

Only `rc 0` with no record licenses treating the file as new. A `cat-file` that then fails means
skip.

`rev-parse --verify --quiet HEAD:<rel>` cannot supply this signal, even though it reads no object
either: `--quiet` collapses *every* failure to rc 1, so a missing or corrupt tree is indistinguishable
from an absent path and reads as "the whole file is new" — the false-block direction this section
exists to close. Measured on git 2.43.0 from a subdirectory, with the path genuinely present in
`HEAD`:

| repository state | `rev-parse --verify --quiet HEAD:<rel>` | `ls-tree -z HEAD -- ":(top)<rel>"` |
|---|---|---|
| intact | rc 0 | rc 0, 1 record |
| path absent | rc 1 | rc 0, 0 records |
| subtree object deleted | rc 1 | rc 1 |
| root tree object deleted | rc 1 | rc 128 |
| treeless clone (`--filter=tree:0`), lazy fetch refused | rc 128 | rc 128 |

Blobless clones (`--filter=blob:none`) are unaffected either way: the trees are present, so the
membership probe succeeds and it is `cat-file` that fails, which already means skip.

The `:(top)` prefix is load-bearing. `ls-tree` takes a *pathspec* and matches it relative to the
current directory, which here is the file's own directory rather than the repository root, so a
bare `<rel>` matches nothing from a subdirectory and returns rc 0 with no records — a present file
reported as new. `--full-tree` does not fix this. `":(top)" + rel` and `os.path.basename(path)`
both answer correctly; the anchored form is used because it names the same path the `cat-file`
read addresses.

`HEAD` itself is still verified once per repository root with `rev-parse --verify --quiet HEAD`,
cached alongside `roots`. An unborn `HEAD` does not need this check to be classified correctly:
`ls-tree HEAD` on a repository with no commits fails outright (rc 128, row five of the table
above), which the membership probe already reads as unanswerable and skips. What this check buys
is cost, not correctness -- one cheap, cached call per root instead of spending the `ls-tree` and
`cat-file` calls on every tracked path in a fresh checkout, when a single `rev-parse` already knows
both can only fail.

### 1d. The state matrix

The draft's version of this table was wrong in one row, and the error propagated into the
compatibility note. `git diff HEAD -- <staged-new-file>` does **not** fail; it returns rc 0 with
the whole file as added lines. Verified.

| state | today | after |
|---|---|---|
| repository has no `HEAD` (no commits) | skip | skip |
| `HEAD` exists, path not in it (staged-new) | whole file added | whole file added |
| `HEAD` exists, blob exists | diff | diff |
| `HEAD` exists, blob unreadable / undecodable / unfetchable | one added line, correctly | **skip** |

So the three-way split exists to *preserve* current behaviour, not to widen it. Nothing in Part 1
causes a file to be scanned that is not scanned today.

The staged-new branch is routed through `_is_vendored()` while it is being written. The tracked
branch never consults it today, so a staged-new `vendor/lib/u.go` already reports its whole
upstream header as added — the same shape the untracked branch was given the exemption for. That
is a pre-existing gap, fixed here because this branch is being rewritten anyway.

### 1e. Reading the blob as bytes

`_git` runs `subprocess.run(..., text=True)`. A single non-UTF-8 byte anywhere in the blob makes
that raise `UnicodeDecodeError` inside `subprocess.run`, which `_git`'s `except Exception` converts
into `(127, "")`. Under the draft's rule that became "the whole file is new". Reproduced on a file
whose committed second line is `// café fixes #1 pre-existing`:

```
text=True  ->  RAISED UnicodeDecodeError  =>  _git() returns (127, '')
bytes      ->  rc=0  b'package x\n// caf\xe9 fixes #1 pre-existing\n'
```

Two consequences, and the first draft of this section got the baseline wrong on both.

**Today the path is silently skipped, not partially answered.** The same decode failure already
happens on `git diff` output, which carries the offending byte as context; `_git` returns
`(127, "")`, `rc_diff != 0`, and the path is dropped. Measured: today's
`final_files_added_lines` returns `{}` for that file. So a file with one Latin-1 byte anywhere in
it is currently unscannable, and nothing says so.

**Reading bytes fixes that.** Both sides decode with `errors="replace"`, so the same byte becomes
the same replacement character on both, `added()` sees no difference there, and the genuinely
added line is reported. This case gets *better*, not worse.

What the draft would have done is still the bug: mapping `(127, "")` onto "the whole file is new"
would have reported thirteen lines including the pre-existing tell and blocked the close.

So the blob is read through a bytes-mode sibling of `_git` and decoded with `errors="replace"`,
which is exactly what `read_text_capped` already does for the worktree side. Both sides then
degrade the same way on the same bytes, which is what keeps `added()` from seeing a spurious
difference.

The blob is capped on **bytes** before decoding, mirroring `read_text_capped`'s `READ_CAP_BYTES`;
over the cap, the path is skipped. The worktree file is read before the *remaining* git calls —
`ls-files` still comes first, since nothing else can say whether the path is tracked and it is the
call that yields `rel` — so an unreadable, oversized or non-regular path costs one cheap call
rather than four. Reading it ahead of `ls-files` would waste a 2 MB read on every untracked path
whose entry already carries `content`, which is the lightweight-completion case.

The draft claimed `read_text_capped` returns a non-string for an "undecodable" file. It does not —
it decodes with `errors="replace"`. It returns a non-string for a symlink, FIFO, directory,
oversized or unreadable path. The draft also claimed the two commands are "unbounded in exactly the
same way"; they are not, since diff output is bounded by the size of the change while `cat-file` is
bounded by the size of the blob. Hence the explicit cap rather than an appeal to parity.

### 1f. Files git would have converted

The clean filter this design stops running is the thing that made a converted file comparable to
its worktree form. Removing it has a consequence the draft missed, and the two halves of it want
different answers:

- **git-lfs, git-crypt (`filter`)** — `HEAD` holds a pointer or ciphertext, the worktree holds
  content. Every line reads as added. Measured: today `{}`, under the draft a whole-file scan that
  blocks on a pre-existing `// bundle header: fixes #1234 upstream`.
- **`working-tree-encoding`** — the worse direction. Today a genuine `// fixes #1` is caught;
  under the draft the UTF-16 worktree bytes are decoded as UTF-8 and the tells cannot match
  through the interleaved NULs, so it becomes a **silent miss** rather than a visible failure.

Both are detected with one call, measured to run no filter itself:

```
git check-attr -z filter working-tree-encoding -- <basename>
```

**`filter` is skipped.** Recovering it would mean running the very command this design exists not
to run. Skipping is fail-open, consistent with the module's rule, and a hostile `.gitattributes`
gains nothing by setting it — it is what an attacker could already achieve by not submitting the
path at all.

**`working-tree-encoding` is honoured, not skipped.** The review proposed skipping it too, and the
draft of this section accepted that; it gives up a detection for no reason. The attribute's value
*is* the encoding name, it is already in the `check-attr` output above, and decoding with it is a
pure `codecs` lookup — no subprocess, no filter, nothing repository-controlled beyond a codec name
Python either knows or does not:

```
utf-8 (as the draft had it)   violations -> []
UTF-16 (from check-attr)      violations -> [('// fixes #1', 'an issue or pull-request reference')]
```

So the worktree side is decoded with the declared encoding when there is one. A codec name Python
does not know, or a decode that fails outright, falls back to skipping the path — the same
fail-open answer, reached only when it is actually needed.

This does mean `read_text_capped()` needs an optional encoding argument. It currently hardcodes
`raw.decode("utf-8", errors="replace")`; the byte cap, `O_NOFOLLOW` and `S_ISREG` guards are
unaffected and stay exactly where they are.

### 1g. Pins and environment

`cat-file blob` is not sufficient on its own. When the blob is absent from the object store and the
repository is a partial clone, git lazily fetches it, and the fetch execs a repository-controlled
command. Reproduced with `extensions.partialClone=origin`, `remote.origin.promisor=true`,
`remote.origin.url=ssh://…` and `core.sshCommand` pointed at a sentinel:

```
cat-file blob HEAD:f.go     rc=128   SSH FIRED: evil.example git-upload-pack '/x.git'
diff HEAD -- f.go           rc=128   SSH FIRED: evil.example git-upload-pack '/x.git'
```

The same shape is reachable through `remote.origin.uploadpack` with a local-path URL and through
`remote.origin.vcs` naming a `git-remote-<name>` helper on `PATH`. The current code fires
identically, so this is pre-existing rather than introduced by the change — but it is exactly the
hole this design claims to close, and a fix that left it open would be shipping a false claim.

Two mitigations, both measured clean against the same sentinel:

- `-c protocol.allow=never` in the pin list. It is filter-name-free and dies in
  `transport_check_allowed` before ssh, upload-pack or a remote helper is spawned.
- `GIT_NO_LAZY_FETCH=1` in the subprocess environment, honoured on 2.43.0
  (`warning: lazy fetching disabled`).

Both are applied. They are independent mechanisms and neither is known to subsume the other across
git versions.

The rest of the pin list:

- `diff.noprefix`, `diff.mnemonicPrefix` — **removed**. With no diff call they pin the shape of
  output nothing parses.
- `core.quotePath=false` — **kept**, and now load-bearing for real (§1b).
- `core.fsmonitor=false` — **kept**. Measured load-bearing: unpinned, `ls-files` ran the configured
  hook; pinned, it did not.
- `core.alternateRefsCommand=false` — **not added**. The draft proposed it; it was measured never
  to be consulted by any remaining call, even with an `objects/info/alternates` present. It is also
  a command-valued key, so `false` would be the command `false`, not a boolean. Adding it would
  have been the same allowlist reflex this design exists to stop.

`_git`'s docstring is rewritten. It currently explains itself in terms of diff-prefix parsing, and
must instead say what is actually true: the list is best-effort hardening, the property relied on
is that no call here converts worktree content or fetches a missing object, and the list is not a
completeness claim — it has now been found incomplete twice.

### 1h. `added()` is not `git diff`

`added()` is an occurrence-aware multiset difference; `git diff` is LCS-based. Measured against the
shipped function: a CRLF blob against an LF worktree yields only the genuinely new line, a moved
line yields nothing, and a newly added duplicate of an existing line is still reported.

The multiset answer is a subset of the LCS answer, so the disagreement is in the permissive
direction — which is the direction this module is required to err in, since it is defence in depth
and a partial answer must never become a blocked close.

There is one real miss, and it should be written down rather than left for someone to rediscover:
**deleting a function whose comment carries change history and pasting that identical comment at a
new site.** git flags the comment as added at the new location; `added()` sees the line count
unchanged and reports nothing. It is narrow — the policy already requires rewriting a bad comment
in code you touch, and the write-time `Write` path has the same property today — but it is a gap,
not a nuance.

Line endings come out even: `str.splitlines()` treats `\r\n` and `\n` as the same break and both
sides go through it, so `core.autocrlf` does not produce a file-wide false diff.

### 1i. What this costs

A tracked path goes from two git calls to four (`ls-files`, `check-attr`, `ls-tree`,
`cat-file`), plus one cached `HEAD` verification per repository root. `GIT_BUDGET_SECONDS` is
unchanged at 20s: the walk already truncates on the budget and reports it through
`stats["truncated"]`, and truncation is fail-open. Reading the worktree file before spending any
git call keeps the common junk-path case at zero subprocesses.

## Part 2 — F2: a `*`-led line inside a string literal refuses its own write

### 2a. Why it cannot be fixed from the line

`comment_spans()` treats a stripped line starting with `*` (not `*/`) followed by whitespace as a
block-comment continuation. Inside a Go raw string or a Kotlin triple-quoted string, such a line is
string content:

```go
const help = `
 * added in v1.2.3 the --foo flag
`
```

The line is byte-identical in both cases. Nothing on it distinguishes them, so no tightening of the
per-line test can separate them — the fix needs the surrounding file.

### 2b. Test the heuristic's own premise

`violations(path, added_lines, context=None)` gains an optional third argument carrying the full
post-change text. When it is supplied, the starred branch fires only when the line is actually
inside an open block comment — the premise the heuristic asserts — rather than on its shape alone.

`context` is walked by a single-pass state machine that tracks line comments, block comments and
string literals, and returns the set of line *texts* sitting inside a block comment. A starred
added line fires only if its text is in that set.

The draft left this at "tracks strings", which a review showed is not good enough. A faithful
prototype of that description lost detections the current code catches: one stray apostrophe — a
Rust lifetime `'a`, an apostrophe in JSX text, a C `#error don't` — opened a string span that ran
to end of file and suppressed every genuine block comment after it. Today's per-line
`_quotes_balanced` confines that damage to a single line, so the naive walk would have been a
regression. Four rules close it:

1. **Single- and double-quoted spans end at end of line.** No real language carries them across a
   newline unescaped, and an unterminated one is a typo, not a span. This is the rule that turns
   a stray apostrophe from a file-wide outage into a one-line one. Measured on the review's
   fixtures: with the rule, the Rust / JSX / C / regex-literal cases all keep firing on a genuine
   `/** * fixes #1 */` placed after them; without it, all four go dark.

   Stated the other way round, because it is the form an implementer is likeliest to get wrong: a
   `'` that does not close on the same line opens nothing at all. A Rust lifetime (`&'a str`,
   `impl<'a>`) is the common case and is not a string by any reading.
2. **Only the genuinely multi-line forms cross a newline**: backtick, and the triple-quoted forms.
   Raw-string forms that the languages in `SCAN_EXTS` actually use are named explicitly rather than
   left to the implementer — Rust `r#"…"#`, C++ `R"(…)"`, and the `${…}` interpolation holes inside
   a backtick span.
3. **The walk is lazy.** It runs only when an added line is a starred candidate. A file with no
   starred added line pays nothing, and — more importantly — a tokenizer exception or its share of
   the 2-second scan deadline cannot take down a `//` finding elsewhere in the same file.
4. **Failure means the old behaviour, not no answer.** If the walk raises or the deadline fires,
   the starred branch falls back to `context=None` semantics.

Matching on text rather than line index is forced by the data: the close-time tracked path derives
its lines from a multiset difference and has no indices to offer. Its failure mode is a line whose
text appears both inside and outside a block comment, which fires — the same answer as today, so it
regresses nothing.

Cost is not a concern: the prototype walked 1.3 MB in 0.21 s.

### 2c. What each caller passes, and how it gets there

| caller | context |
|---|---|
| write-time, `Write` | `content` from the payload |
| write-time, `Edit` | the reconstructed post-edit text (below) |
| close-time, tracked | the `new_text` Part 1 already reads |
| close-time, untracked | submitted `content`, else `read_text_capped(path)` |
| close-time, `final_diff` | **none** — see below |

**The `Edit` reconstruction.** Neither `new_string` alone nor the on-disk text alone is the
post-edit file: `new_string` is unbalanced when the edit inserts a line into a literal that already
exists on disk, and the on-disk text does not contain the added line. So the branch reads the file
and reconstructs. Two details the draft got wrong: Claude Code's `Edit` has a `replace_all` mode,
so the reconstruction must honour it rather than always replacing once; and the `Edit` branch does
not read the file today, so `read_text_capped` returning `None`/`MISSING` must fall back to
`context=None` rather than failing the scan. Both fail open.

**The return-shape decision.** `final_files_added_lines()` returns `{path: [lines]}`, and
`check-task-complete` derives its stats from that shape. Changing it to carry the context would
break that wrapper. Instead the function takes an optional `contexts` dict parameter and fills it,
exactly as it already does for `stats`. The return contract is unchanged.

**`final_diff` keeps the false positive.** When a completion submits a diff rather than files,
there is no file text to walk, so that path passes `context=None` and a `*`-led line inside a raw
string still refuses the close there. Fixing it would mean reconstructing file text from a diff
hunk, which is not worth it. It is stated here so the gap is known rather than assumed closed.

### 2d. Detections this gives up

A ` * text` line with no enclosing block comment anywhere in the file no longer fires. That line is
not a comment in any language whose openers include `*`, so the detection was not sound to begin
with.

The KDoc case that motivated the v0.20.0 work — ` * ABC-1234: …` inside `/** … */` — is inside an
open block and keeps firing; it is an explicit test. So do the four review fixtures from §2b.

One trap in writing that test: a bare tracker key matches nothing on its own. `_ticket_tell()`
returns `None` unless `ANTI_TANGENT_TICKET_PATTERN` is configured, and an unconfigured project
therefore has no tracker tell at all — measured, ` * ABC-1234: widen the parser` yields `[]`
while ` * fixes #1` yields a hit. The preservation test must use a built-in tell, or configure a
pattern before importing the module.

The fail-open rule is unchanged: any exception, or the scan deadline, yields no violations.

## Part 3 — F3: the false-positive gate cannot measure a configured pattern

`evals/fp-scan.py` pops `ANTI_TANGENT_TICKET_PATTERN` before importing the scanner. That is correct
for its job — the gate measures the shipped tells, and a developer's local pattern must not move a
committed verdict. But `comment_scan` binds the tell at import (`_ticket = _ticket_tell()`), so
popping it there means a project that sets the pattern, which v0.20.0 asks operators to do, gets no
false-positive coverage for it at all.

A `--ticket-pattern <regex>` flag is added. Because the binding happens at import, argv is parsed
and the environment set *before* the `from comment_scan import …` line — the import stays where it
is and the environment manipulation moves above it.

- Without the flag: the variable is popped exactly as today. The gate's meaning, its TSV output and
  `fp-class.tsv` are untouched.
- With the flag: the variable is set to the supplied pattern, and the stderr summary reports total
  hits and how many carry the ticket tell's `why` string (`"a tracker reference"`), which is what
  distinguishes them.

`_ticket_tell()` silently drops a pattern that fails to compile or exceeds 200 characters. Under
the flag that silence is a trap: the operator reads "0 tracker hits" as "this pattern is safe" when
the pattern was never installed. So `fp-scan` compiles and length-checks the supplied pattern
itself and exits non-zero with a message if it would be dropped, rather than reporting a clean run.

The reported figure is the number of lines where the ticket tell was the *first* matching tell —
`violations()` breaks on first match, in `TELLS` order. That is the right number, because it is
what the pattern would newly block, and the summary says so rather than leaving it to be inferred.

This is a reporting mode an operator runs by hand before committing to a pattern. It is not wired
into CI, and the default path is byte-identical to today's.

## Testing

Unit (`hooks/comment_scan_test.py`):

- **Two** filter fixtures, not one. With `filter.x.process` and `filter.x.clean` both set, only
  `process` fires — a single fixture arming both would never exercise `clean`, which is the key the
  issue got wrong. One fixture per key, each asserting the sentinel is never created and the added
  lines are still correct.
- A partial-clone fixture (`extensions.partialClone`, `remote.origin.promisor`, a removed blob,
  `core.sshCommand` pointed at a sentinel) asserting no exec — the §1g regression test.
- `HEAD` exists, path not in it → whole file added; and the same path under `vendor/` → skipped.
- Repository with no commits → path skipped.
- A `HEAD` blob containing a non-UTF-8 byte → only the genuinely added line, sentinel-free; the
  §1e regression test.
- A worktree root reached through a symlink → correct `rel`, path still scanned (§1b).
- A path with `filter` set (lfs-style pointer in `HEAD`) → skipped, not whole-file.
- A path with `working-tree-encoding=UTF-16` → the genuine `// fixes #1` is still caught, decoded
  through the declared codec; and an unresolvable codec name on the same fixture → skipped.
- Existing `_git` stubs updated for the new call sequence.
- `*`-led line inside a Go raw string and a Kotlin triple-quoted string → `[]`.
- ` * ABC-1234: …` inside `/** */` → still a violation.
- The four §2b fixtures — Rust lifetime, JSX apostrophe, C `#error`, JS regex literal — each
  followed by a genuine `/** * fixes #1 */`, all still firing.
- `violations()` with `context=None` behaves exactly as before.

Hook-level evals (`evals/guard-evals.json`, `evals/run.sh`): the filter case end-to-end through
`check-task-complete`, asserting the sentinel is absent — unit coverage alone would not prove the
wiring. The existing `setup_script` hook builds the fixture repositories. The review confirmed that
none of the 142 existing evals carries a starred `Write`/`Edit` payload, so Part 2 regresses none
of them.

Commands: `bash plugin/anti-tangent-guard/evals/run.sh`,
`python3 plugin/anti-tangent-guard/hooks/comment_scan_test.py`, `go test -race ./...`,
`bash scripts/check-protocol-docs.sh`, `python3 scripts/check_md_links.py`.

## Compatibility

No MCP tool signature, prompt template or protocol document changes. `validate_completion`'s wire
format is untouched. `violations()` gains an optional third parameter and
`final_files_added_lines()` an optional `contexts` dict; both stay call-compatible, and
`final_files_added_lines()`'s return shape is unchanged.

Behaviour changes visible to a user. The draft claimed a widening that does not exist (`git diff`
already reports a staged-new file in full) and a narrowing that does not either (a non-UTF-8 blob
is already skipped today, not partially answered):

1. **Widens.** A file containing a byte that is not UTF-8 is scanned correctly, where today the
   decode failure silently skips it (§1e).
2. A file carrying a `filter` attribute — git-lfs, git-crypt — is skipped (§1f). A
   `working-tree-encoding` file is **not**: it keeps working, via the declared codec. Only an
   encoding name Python cannot resolve falls through to a skip.
3. A moved line is no longer reported as added (§1h).
4. A ` * text` line outside any block comment no longer fires (§2d).
5. A staged-new path under a vendored directory is now skipped (§1d) — it is scanned today.
6. A path whose `HEAD` blob is genuinely unreadable — an unfetchable promisor object, a
   `cat-file` that fails for any other reason — is skipped rather than guessed at (§1c). Today
   the same path is also skipped, by a different route, so this is not a change in outcome.

## Release

`CHANGELOG.md` gains a `## [0.21.0] - 2026-09-11` block with `### Security` (F1), `### Fixed` (F2)
and `### Added` (F3). `VERSION` stays at `0.20.1` — the release workflow bumps it, and pre-bumping
on the branch breaks the workflow's changelog validation. The guard plugin's `plugin.json` goes to
`0.4.0`. The branch is `version/0.21.0`; the merge commit carries `[minor]`.

## References

- Issue [#75](https://github.com/patiently/anti-tangent-mcp/issues/75)
- [v0.20.0 design](2026-09-10-anti-tangent-v0.20.0-design.md) — introduced the `final_files` path this hardens
- `git help attributes`, "Filtering" — `clean`/`smudge`/`process` and the conversion directions
