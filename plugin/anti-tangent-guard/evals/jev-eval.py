"""Score jev-comments.jsonl against the shipped question. Never runs in CI.

Every row is a paid request against the real TypeSafe endpoint, so this
script refuses to run at all unless an operator has explicitly turned it on
-- see refuse_unless_authorized(). The scoring itself reuses jev_scan.config()
and jev_scan.judge(), the same functions check-comment-write calls at write
time, so the model, threshold and endpoint this measures are exactly the ones
the hook would use for the same environment.
"""
import hashlib
import json
import os
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.join(os.path.dirname(HERE), "hooks"))

import jev_scan  # noqa: E402

# A CI job that inherits a key must still not spend it, so the refusal is
# positive -- checked explicitly -- rather than a convention nobody enforces.
CI_MARKERS = ("CI", "GITHUB_ACTIONS", "BUILD_NUMBER")

FIXTURE_PATH = os.path.join(HERE, "jev-comments.jsonl")
QUESTION_PATH = os.path.join(os.path.dirname(HERE), "hooks", "jev-question.json")


def refuse_unless_authorized(env):
    """Exit, naming what is missing, unless every manual-run gate is met.

    Pulled out as its own function so the real CLI and the offline test
    exercise the identical three conditions in the identical order, rather
    than the test re-deriving them from the script's top level.
    """
    if env.get("ANTI_TANGENT_JEV") != "1" or not env.get("TYPESAFE_API_KEY"):
        sys.exit("set ANTI_TANGENT_JEV=1 and TYPESAFE_API_KEY to run the calibration suite")
    for marker in CI_MARKERS:
        if env.get(marker):
            sys.exit("the calibration suite is manual: %s is set" % marker)


def question_hash(cfg, question_path=QUESTION_PATH):
    """The cache namespace: the shipped question's bytes plus the model id.

    Both inputs can change what a cached probability means. Hashing only one
    of them would let an edited question, or a model swap with the question
    left alone, be scored against a cache holding another run's answers.
    """
    with open(question_path, "rb") as fh:
        payload = fh.read() + cfg.model.encode("utf-8")
    return hashlib.sha256(payload).hexdigest()[:12]


def load_rows(fixture_path, limit=None):
    """The calibration rows, minus the ones nobody has decided.

    Ambiguous rows are excluded here, before the caller ever builds a
    request: a row nobody could classify is not evidence for or against the
    question, and scoring it would only spend money to record a number
    against no expected answer.
    """
    with open(fixture_path) as fh:
        rows = [json.loads(line) for line in fh if line.strip()]
    rows = [r for r in rows if r["label"] != "ambiguous"]
    if limit is not None:
        rows = rows[:limit]
    return rows


def load_cache(cache_path):
    if os.path.exists(cache_path):
        with open(cache_path) as fh:
            return json.load(fh)
    return {}


def save_cache(cache_path, cache):
    with open(cache_path, "w") as fh:
        json.dump(cache, fh)


def score_rows(rows, cfg, cache, cache_path, judge=None):
    """Fill `cache` with every row's probability, persisting after each one.

    One block per request, the shape the hook sends. judge() reports the
    highest probability it saw whether or not it flagged, so a clean row is
    scored on its real number rather than on a default -- which is what lets
    a re-score reuse this cache instead of re-asking every row again. The
    cache is written after each call, not once at the end, so a run that
    dies partway through keeps the answers it already paid for.
    """
    judge = judge or jev_scan.judge
    for row in rows:
        if row["id"] in cache:
            continue
        verdict = judge([jev_scan.Block(row["block"], 0)], cfg)
        if verdict.event == "jev-error":
            sys.exit("row %s failed: %s" % (row["id"], verdict.detail))
        cache[row["id"]] = verdict.probability
        save_cache(cache_path, cache)


def build_groups(rows, cache, threshold):
    """Per-source counts: how many of each label were flagged, by which tier."""
    groups = {}
    for row in rows:
        probability = cache[row["id"]]
        flagged = probability >= threshold or row["regex_hit"]
        jev_only = probability >= threshold
        g = groups.setdefault(row["source"], {"pos": 0, "neg": 0, "tp": 0, "fp": 0,
                                                "regex_tp": 0, "jev_tp": 0})
        if row["label"] == "history":
            g["pos"] += 1
            g["tp"] += flagged
            g["jev_tp"] += jev_only
            g["regex_tp"] += bool(row["regex_hit"])
        else:
            g["neg"] += 1
            g["fp"] += flagged
    return groups


def rate(num, den):
    return "n/a" if not den else "%.0f%%" % (100.0 * num / den)


def format_table(groups):
    lines = ["%-28s %4s %4s %4s %4s %5s %5s %10s %11s"
              % ("group", "pos", "neg", "tp", "fp", "regex", "jev", "recall", "precision")]
    for name, g in sorted(groups.items()):
        lines.append("%-28s %4d %4d %4d %4d %5d %5d %10s %11s"
                      % (name, g["pos"], g["neg"], g["tp"], g["fp"], g["regex_tp"], g["jev_tp"],
                         "%s (%d/%d)" % (rate(g["tp"], g["pos"]), g["tp"], g["pos"]),
                         "%s (%d/%d)" % (rate(g["tp"], g["tp"] + g["fp"]), g["tp"], g["tp"] + g["fp"])))
    tp = sum(g["tp"] for g in groups.values())
    fp = sum(g["fp"] for g in groups.values())
    pos = sum(g["pos"] for g in groups.values())
    neg = sum(g["neg"] for g in groups.values())
    lines.append("")
    lines.append("together: recall %d/%d, %d false flags in %d clean comments" % (tp, pos, fp, neg))
    return "\n".join(lines)


def main(argv, env, fixture_path=None, cache_dir=None, question_path=None, judge=None):
    refuse_unless_authorized(env)
    cfg = jev_scan.config(dict(env), "calibration.go")
    qhash = question_hash(cfg, question_path or QUESTION_PATH)
    limit = None
    if "--limit" in argv:
        limit = int(argv[argv.index("--limit") + 1])
    rows = load_rows(fixture_path or FIXTURE_PATH, limit)
    cache_path = os.path.join(cache_dir or HERE, ".jev-eval-cache-%s.json" % qhash)
    cache = load_cache(cache_path)
    score_rows(rows, cfg, cache, cache_path, judge=judge)
    table = format_table(build_groups(rows, cache, cfg.threshold))
    print(table)
    return table


if __name__ == "__main__":
    main(sys.argv, os.environ)
