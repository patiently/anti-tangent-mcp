# v0.20.0 reviewer e2e result

Ran: 2026-09-10T16:01:08Z
SKIPPED: ANTHROPIC_API_KEY not set.

`TestCommentPolicyFindingE2E` and `TestCommentHygieneFenceFindingE2E`
(`internal/mcpsrv/plan_comment_policy_e2e_test.go`, build tag `e2e`) are pinned to
`realAnthropicReviewer`, which itself calls `t.Skip("ANTHROPIC_API_KEY required for live e2e")`
when that key is absent — so these two tests do not run without it, regardless of any other
provider key present in the environment. `OPENAI_API_KEY` was set when this record was written;
it is not sufficient on its own, since neither test uses it.

G11 and the comment-hygiene fence rule ship in v0.20.0 without behavioural (live-reviewer)
verification in this run. Re-run the loop below with `ANTHROPIC_API_KEY` set to close this gap:

```bash
PASSES=0
for i in 1 2 3 4 5; do
  if go test -tags=e2e ./internal/mcpsrv/... -count=1 \
       -run 'TestCommentPolicyFindingE2E|TestCommentHygieneFenceFindingE2E'; then
    PASSES=$((PASSES + 1))
  fi
done
echo "$PASSES/5"
```

Task 9's static verification (`go vet -tags=e2e ./...`, `go build -tags=e2e ./...`, `go test
-tags=e2e ./internal/mcpsrv/... -list '.*'`) already confirmed the file builds and both tests are
discoverable under the `e2e` tag; only the live run against Anthropic remains outstanding.
