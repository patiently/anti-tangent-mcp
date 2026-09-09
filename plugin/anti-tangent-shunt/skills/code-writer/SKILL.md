---
name: code-writer
description: Use when generating boilerplate that follows an existing pattern - a test file mirroring another, a DTO, a repetitive interface implementation. Sends the generation to a cheap worker model so the generated code never enters your context.
---

# Generating boilerplate without reading it back

`mcp__anti-tangent__code_write` generates code that matches a reference file.

## `reference_path` is required, and it is the whole point

The worker matches the reference's conventions, naming and structure. Without
one it would emit context-free code that fits nothing. Pick the closest
existing example — the sibling test, the neighbouring handler.

## Prefer `target_path`

With `target_path` set, the server writes the file and returns only a line
count. The generated code never enters your context — which is the entire
saving. Omit it only when you genuinely need to inspect the code first.

The server does the writing, so the file never passes through `Edit`/`Write`
and `anti-tangent-guard`'s write-time comment hook never sees it. Only the
close-time scan over your submitted diff, and the reviewer, will catch a
generated comment that carries change history.

    mcp__anti-tangent__code_write
      spec:           "Table test for Add covering zero, negative and overflow"
      reference_path: "/abs/repo/internal/math/sub_test.go"
      target_path:    "/abs/repo/internal/math/add_test.go"

**Not supported on Windows.** The server refuses `target_path` there outright
— it cannot guarantee a symlink/reparse point planted at the target's final
path component is refused rather than followed on that platform. `code_write`
still generates the code and returns it as `code` on Windows; omit
`target_path` and write the file yourself.

## `overwrite` is deliberate

An existing target is refused unless you pass `overwrite: true`. That default
exists because a worker model acting on a misread spec would otherwise destroy
a hand-written file, and the response tells you only a line count — you would
not see what was lost. The parent directory must already exist; this tool never
creates directories.

An `overwrite: true` write is atomic: the server writes a temp file next to
the target and renames it over, so a failed write leaves the original
untouched instead of truncated. The rename does replace the target's inode,
though — a hard link to it now points at the pre-write content, and ownership
is not carried across (only the file mode is). Don't rely on a hard-linked
`target_path` surviving an overwrite.

## When NOT to delegate

Anything requiring judgement: business logic, security-sensitive code,
concurrency, an algorithm with a correctness argument. Boilerplate means the
pattern is already decided and only the filling changes.

## Verify what you generated

You have not read this code, and you do not need to — that is the saving. What
you DO owe is proof it works: run the task's tests and its build before treating
it as done, and `validate_completion` still applies to the task as a whole.

Verification, not inspection. If a change genuinely needs your eyes on the text,
it was not boilerplate and should not have been delegated.
