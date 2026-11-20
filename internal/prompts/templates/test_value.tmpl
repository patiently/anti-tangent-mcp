### Tests (apply before writing each one)

A test earns its place by failing when behaviour someone depends on breaks.
Before writing a test, name the acceptance criterion or caller-visible
behaviour it protects. If you cannot, do not write it.

Start from one test per acceptance criterion. Add another only for a failure
or boundary case the criterion names, and write several inputs to one
behaviour as one table-driven or parametrised test, not as separate tests.
Test each criterion once, at the cheapest layer that observes it, not through
each of its helpers. Each test beyond the first for a criterion
gets a `test_ledger` entry saying what it protects that the others cannot;
each test you considered and did not write goes in `tests_declined`.

Write:
- tests that run the behaviour an acceptance criterion describes, including
  the failure and boundary cases it names — "rejects a request without a
  token" or "never logs the secret" is behaviour, not an absence guard;
- a regression test for the bug this task fixes, written first and seen to
  fail (say how in `failed_when`);
- a guard for a property that matters, after seeing it fail with the property
  broken (say how in `failed_when`).

Do not write:
- absence guards: assertions that a removed framework, symbol, file or
  dependency stays removed (git and review catch a return);
- one test per function, or a test that restates the implementation;
- a second test for behaviour another test already asserts;
- tests that read the source code under test as text instead of running it
  (checking a generated artefact, golden file, schema or config the product
  ships is fine);
- tests pinning incidental output no caller depends on (log wording, map
  order), or asserting only a snapshot (a snapshot beside a real assertion is
  fine).

Existing tests are out of scope: do not delete or rewrite tests the task did
not ask you to touch.
