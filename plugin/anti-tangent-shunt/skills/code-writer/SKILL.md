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

    mcp__anti-tangent__code_write
      spec:           "Table test for Add covering zero, negative and overflow"
      reference_path: "/abs/repo/internal/math/sub_test.go"
      target_path:    "/abs/repo/internal/math/add_test.go"

## `overwrite` is deliberate

An existing target is refused unless you pass `overwrite: true`. That default
exists because a worker model acting on a misread spec would otherwise destroy
a hand-written file, and the response tells you only a line count — you would
not see what was lost. The parent directory must already exist; this tool never
creates directories.

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
