# 0.31.0 test-value replay: results

Task 8 of `2026-11-20-test-value-and-development-cycle.md`, measured against the replay table in spec §10.

- **Model:** `openai:gpt-5.6-terra` for pre and post (`ANTI_TANGENT_PRE_MODEL`, `ANTI_TANGENT_POST_MODEL`), `ANTI_TANGENT_REPLAY_RUNS=1`, stats off.
- **Server replayed:** `version/0.31.0` at `6f412e1`; comparison point `v0.30.1` (`1d8d6bc`).
- **Date:** 2026-11-20.
- **Round 2 server:** `version/0.31.0` at `4f9ebaa`, which applies proposed wording 1–6 below (threshold change approved 2026-11-20).
- **Round 3 server:** `version/0.31.0` at `f883aa0`, which reverts wording 2 and keeps the threshold and wordings 3–6.
- **Paid reviewer calls:** 580 in total: 260 in round 1, 180 in round 2 and 140 in round 3, each the full approved budget. Round 1 breaks down as 100 fixtures × (pre + post) at head, plus the 30 lean fixtures × 2 at `v0.30.1`. No reviewer response was truncated, so the harness made no retries.
- **Fixtures:** 100 fixtures, kept outside the repo in `anti-tangent-replay/0.31.0/` (see its `MANIFEST.md`). Their diffs are real `anti-tangent-mcp` commits. The gamed, under-tested and loosened variants are hand edits of real commits, and each one compiles. **Each fixture's task spec (Goal and ACs) was written for this replay from the commit message and test names. Its `test_evidence` is invented pass output, not captured output.**
- **Scoring:** every finding's criterion, severity and evidence text was read. Keyword tallies were not used.

## Results

Round 1 is at `6f412e1`. Round 2 is at `4f9ebaa` and reruns five sets: over-tested, lean, look-alikes, gamed and under-tested (90 fixtures × 2 = 180 calls). It reuses round 1's loosened result and its `v0.30.1` lean report. Round 3 is at `f883aa0` and reruns lean, look-alikes and under-tested (70 fixtures × 2 = 140 calls), against the same `v0.30.1` lean report. No response was truncated in any round.

| Set | n | Bar | Round 1 k | Round 2 k | Round 3 k | Latest result |
|---|---|---|---|---|---|---|
| over-tested (real superpowers-era diffs) | 10 | `test_value` major ≥ 7 | 0 (2 minor) | 1 (3 minor) | not rerun | **miss** (round 2) |
| lean, well-tested | 30 | no `test_value` major ≥ 28 | 30 | 30 | 30 | pass |
| legitimate look-alikes | 30 | no `test_value` / `test_weakened` major ≥ 28 | 30 | 30 | 29 | pass |
| gamed (near-identical subtests; padded ledgers) | 10 | `test_value` major ≥ 7 | 3 | 5 (subtests 5/5, ledgers 0/5 with 2 minor) | not rerun | **miss** (round 2, with wording 2) |
| under-tested (AC with no test; vacuous test) | 10 | `test_adequacy` major ≥ 8 | 7 | 10 | 10 | pass |
| loosened assertions | 10 | `test_weakened` ≥ 7 | 9 (all major) | not rerun | not rerun | pass (round 1) |
| lean `test_adequacy` vs 0.30.1, pre + post | 30 | ≥ 50% fewer | 4 vs 6 (−33%) | 8 vs 6 (+33%) | 5 vs 6 (−17%) | **miss** |

### `test_adequacy` majors on legitimate diffs, pre + post

| | Round 1 (`6f412e1`) | Round 2 (`4f9ebaa`, wording 1–6) | Round 3 (`f883aa0`, wording 2 reverted) |
|---|---|---|---|
| lean (30) | 1 (pre 0, post 1) | 4 (pre 2, post 2) | 1 (pre 1, post 0) |
| look-alikes (30) | 9 (pre 1, post 8) | 14 (pre 3, post 11) | 10 (pre 1, post 9) |

### `test_adequacy` on the lean set, head vs 0.30.1

| | pre | post | total |
|---|---|---|---|
| `v0.30.1` | 2 minor (deprecation-cache, plan-reuse-context) | 4: 2 major (lite-leaves-session-row, withdraw-attached), 2 minor (line-anchor-lists, plan-cap-roots) | 6 |
| head, round 1 | 0 | 4: 1 major (lite-leaves-session-row), 3 minor (diff-header-lines, line-anchor-lists, plan-cap-roots) | 4 |
| head, round 2 | 3: 2 major (blank-title-attach, oversize-reference), 1 minor (hunk-header-disguise) | 5: 2 major (oversize-reference, withdraw-attached), 3 minor (line-anchor-lists, lite-leaves-session-row, plan-cap-roots) | 8 |
| head, round 3 | 1 major (oversize-reference) | 4 minor (line-anchor-lists, lite-leaves-session-row, plan-cap-roots, report-no-call-log) | 5 |

The pre-side recalibration did its job: both spec-time `test_adequacy` findings are gone. The post side did not change. Three of the four head findings are accurate `minor` boundary gaps: an anchored Windows/URL case, a negative cap value, and the separator variant. The rule keeps all three on purpose. The one major (lite-leaves-session-row) is over-graded. The test asserts the verdict, lite flag, digest and advisory criterion, and misses only the advisory's severity. That is a missing assertion on a tested criterion, which should be minor, not "asserts nothing about the behaviour".

## Round 3 judgement (wording 2 reverted)

Per the instructions for this task, no further tuning was done after round 3.

- **The question round 3 answers: wording 2 caused the rise in `test_adequacy` majors on legitimate diffs.** With it reverted, lean majors fell from 4 to 1 and look-alike majors from 14 to 10, back to round 1's level (1 and 9). Wordings 3–6 alone do not inflate them.
- **Under-tested holds at 10/10** without wording 2, so wording 5 is what routes untested behaviour to `test_adequacy`.
- **Lean post-side is now four minors and no major.** All four are boundary gaps the rule keeps on purpose. Wording 6 graded lite-leaves-session-row `minor` this time, which round 1 judged it should be.
- **Lean `test_adequacy` total: 5 vs 6, still a miss.** The one major is a pre-side ambiguity complaint filed as `test_adequacy` (oversize-reference, the same misfiling as round 2). Single-run variance (4, 8, 5 across the three rounds) is larger than the reduction the bar asks for, so this bar cannot be decided with one run per fixture.
- **Look-alikes: 29/30, still a pass.** The one `test_value` major is a false positive. On write-target-nofollow, the reviewer flagged five refusal tests as `consolidate:`, but each is a negative case an AC names. This is a single instance, so it does not show that the threshold is too low.
- **Gamed subtests are unmeasured without wording 2.** Round 2's 5/5 on subtests was credited to wording 2, and gamed was not rerun in round 3. Expect it to fall back toward round 1's 3/5, but this is not measured.

## Round 2 judgement (after wording 1–6)

Per the instructions for this task, no further tuning was done after round 2.

- **Under-tested now passes (10/10).** Wording 5 routed all three round-1 misses to `test_adequacy` major. This is a template fix that worked.
- **Gamed subtests: 5/5 major,** up from 3/5. Wording 2 made the reviewer count cases before reporting adequacy; plan-kind and revise-rounds now draw a `consolidate:` major. This is a template fix that worked.
- **Gamed padded ledgers: 0/5 major. This is a fixture problem.** Wording 4 does work: code-write's `TestStripFences`, whose `failed_when` was generic, is now flagged `trivia:` despite the ledger. But the diffs behind these fixtures are real ones in which only one or two tests could go. Padding a ledger cannot make a light diff heavy. To measure the ledger, a fixture has to sit on a heavy diff, so that the ledger is the only thing that could suppress the major.
- **Over-tested: 1/10 major (lightweight-flag), plus 3 minor. Mostly a fixture problem.** With the threshold at three-or-a-third, the reviewer now finds `consolidate:` and `trivia:` instances where they exist (bulk-read, ledger-prune). Where it does not, it reads the separate tests as distinct failure cases the criterion names, and the rule correctly spares those. One example is bulk-read's three rejections: merging them saves 2 of 10 tests, which is light. My round-1 per-fixture count treated distinct named cases as removable, and the rule does not. So these real diffs from this repo are less over-tested than their labels claim, and a bar of 7 needs fixtures with per-function or restating tests. Recall is still imperfect (rate-digest and attach-by-title drew no test-value finding), but no wording change would turn these diffs major without breaking the look-alike bar.
- **Lean `test_adequacy` vs 0.30.1: 8 vs 6, a miss. The cause is variance plus pre-side category drift.**
  - Pre went 0 → 3 between rounds, even though pre.tmpl did not change. Two of the three are majors that complain a criterion is ambiguous ("does not define what makes a round latest"). They are filed under `test_adequacy` through plan_rules/pre's "cannot be tested as written" clause, and the same misfiling appears once on the post side (oversize-reference).
  - The one remaining post major (withdraw-attached: the test calls the helper directly, so deleting the new call site would not fail it) is accurate, and v0.30.1 raised it too.
  - A baseline of 6 findings, with single-run variance of ±3, cannot resolve a 50% reduction. Measuring this bar needs several runs per fixture. Any wording fix would route "cannot be tested as written because the criterion is ambiguous" to `ambiguous_spec`, not `test_adequacy`, in pre.tmpl and plan_rules.tmpl. That is not measured.
- **Side effect worth watching:** look-alike `test_adequacy` majors rose from 9 to 14 between rounds (pre 1 → 3, post 8 → 11), and lean post majors went from 1 to 2. Wordings 2 and 5 make the reviewer look harder at tests. The look-alike bar only counts `test_value`/`test_weakened`, so this does not miss a bar, but it adds blocking findings on legitimate diffs.

## Round 1 per-miss judgement

The round-1 run used all 260 approved calls, so tuning waited for a second approval. The wording below was proposed after round 1, then **applied in `4f9ebaa` and measured in round 2** (results above).

### Over-tested: 0/10 major. Fixture and template both contribute.

The template only calls a `consolidate:` instance heavy when at least 3 tests could go AND they are at least half of the tests the change adds. Read against that rule, these real diffs split:

| Fixture | Removable / added (by the rule) | Correct severity | Reviewer |
|---|---|---|---|
| attach-by-task | ≈6/11 | major | minor (one `trivia:`) |
| attach-by-title | ≈4/7 | major | none |
| rate-digest | ≈7/14 | major | none |
| bulk-read | ≈5/10 | major (borderline) | none |
| lightweight-flag | ≈3/6 | major (borderline) | minor (2-test merge) |
| test-evidence-path | ≈3/10 | minor | none |
| context-paths-resolve | ≈4/9 | minor | none |
| codescene-in-band | ≈3/10 | minor | none |
| plan-run-report | ≈3/9 | minor | none |
| ledger-prune | ≈4/9 | minor | `test_adequacy` major on a no-op test |

- **Fixture side:** 5 of the 10 real diffs are correctly *minor* under the half-of-added threshold. With a perfect reviewer this set would score about 5/10, so the ≥ 7 bar cannot be reached with these fixtures as the rule now stands. Either the bar is mismatched to the threshold, or the threshold is too strict for real superpowers-era diffs. That is a spec decision, not a wording fix.
- **Template side:** recall is the bigger problem. 6 of 10 fixtures drew no test-value finding at all, not even a minor one, and the 5 that should be heavy produced no major. In three fixtures the reviewer spent its test attention on `test_adequacy` instead. ledger-prune is the clearest case: a no-op test that duplicates others was reported as `test_adequacy` "asserts nothing" major, when the rule makes it a `test_value` `trivia:`/`consolidate:` instance.

### Gamed: 3/10 major. Mostly template.

- Near-identical subtests: 3/5 major (correctness-rates, criterion-counts, elapsed-time).
  - plan-kind (14 near-identical rows) drew only a `test_adequacy` minor for a dropped fence case.
  - revise-rounds (11 near-identical subtests) drew `test_adequacy` major for the rows it never asserts.
  - Both clearly meet the heavy bar, so these are template misses: `test_adequacy` crowds out `test_value`.
- Padded ledgers: 0/5. Fixture and template both contribute.
  - The underlying diffs are the same kind of real diff as the over-tested set, mostly light under the half-of-added threshold.
  - The padding gave generic `failed_when` text ("would fail if X regressed") to the catalog and helper tests. The rule exempts any test with a `failed_when` from `trivia:`. So by the letter of the rule, the padding worked. That is a template gap: a `failed_when` that names no observed failure should not exempt anything.

### Under-tested: 7/10 major. Template.

- deprecation-gap and heading-lookup: the reviewer saw a diff with no test for a behaviour AC and reported it as `insufficient_evidence` major, not `test_adequacy`. post.tmpl's "Do not use this category for evidence that is merely absent: that is `insufficient_evidence`" reads as covering this case.
- agent-network-final: the reviewer reported only the narrow `go test` command as `insufficient_evidence`.

All three are category routing, not missed defects. The failing behaviour was flagged major in two of them under the wrong criterion.

### Loosened: passed at 9/10.

implementation-guidance was missed (`assert.Equal` on `ModelUsed` relaxed to `NotEmpty`). This is single-run variance on a subtle relaxation, and no action is needed.

## Wording (applied in `4f9ebaa`, measured in round 2; wording 2 reverted in `f883aa0`, measured in round 3)

All of these changes are in `post.tmpl`, plus the pre/plan sentence noted below. None of them touches `test_value.tmpl`.

1. **Test value, heavy threshold.** Only if the spec owner wants real over-tested diffs to read as major: "Heavy when at least 3 tests or cases could go, or they are at least a third of those the change adds; otherwise light." This changes the spec's definition of heavy, so it needs that decision first.
2. **Test value, recall.** Add before the tag list: "Do this before writing any `test_adequacy` finding: group the tests this change adds by the criterion they run, and compare each group's count with the failure and boundary cases that criterion names. A group larger than that is a `consolidate:` instance even when another test finding applies to the same tests."

   **Reverted in `f883aa0`.** It raised `test_adequacy` majors on legitimate diffs (look-alikes 9 → 14, lean 1 → 4) and moved over-tested only from 0 to 1, well short of 7. Round 3 confirmed it as the cause: with it removed, the counts went back to 10 and 1.
3. **Test value, routing.** Add to `trivia:`: "A test whose only assertion is that a call does not panic or is a no-op, where another test already runs that path, is `trivia:` here, not `test_adequacy`."
4. **Ledger.** Change "not an instance when the ledger gives `failed_when` for it" to: "not an instance when the ledger's `failed_when` names a specific change to the code under test that made the test fail; a `failed_when` that only restates that the test would fail on a regression exempts nothing."
5. **Test adequacy, absence.** Replace "Do not use this category for evidence that is merely absent: that is `insufficient_evidence`" with: "When a diff is present and adds no test that would catch a behaviour criterion's breakage, that is this category, not `insufficient_evidence`; `insufficient_evidence` is for test output or files that were not submitted."
6. **Test adequacy, severity.** Add: "A test that runs the behaviour and asserts most of it, missing one property, is `minor`; `major` is for a criterion no test would catch, or a test that asserts nothing about the behaviour."
