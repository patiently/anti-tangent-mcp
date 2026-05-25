---
permalink: <PROJECT>/gotchas/<NNNN>-<slug>/main
type: gotcha
title: <one-line title — what bit us>
status: accepted                 # accepted | superseded
modules: [<slug>, <slug>]        # one or more module slugs this gotcha applies to
origin: <PROJECT>/stories/<TICKET-ID>/main   # optional; story / epic / PR permalink that surfaced this
severity: medium                 # low | medium | high
discovered_at: <YYYY-MM-DD>
supersedes: []                   # list of <PROJECT>/gotchas/<NNNN>-<slug>/main permalinks; non-empty if this replaces an earlier gotcha
tags: []
---

## Symptom

<what was observed — concrete, reproducible if possible>

## Root cause

<why it happens — code paths, invariants violated, environmental quirks>

## How to avoid

<the actionable rule for future plans touching these modules — the load-bearing section; prime excerpts this for the next plan>

## Evidence

- <link to PR / commit / review comment / log line>
- <link to test that pins the fix, if any>

## Related

- [[<PROJECT>/modules/<slug>/main]]
- [[<origin permalink>]]
