# anti-tangent dogfood examples

Frozen-snapshot real example notes from anti-tangent's own KB at the v0.7.0 release. Each file follows the v0.7.0 templates in [`../`](..) and uses the conventions from [`docs/team-setup/project-knowledge-conventions.md`](../../../docs/team-setup/project-knowledge-conventions.md).

**These are educational references**, not live state. Anti-tangent's live KB lives in its own Basic Memory project. This directory is re-snapshotted manually on major releases when the picture has materially shifted; it is NOT auto-updated.

Read each file as a worked example for its type:

- [`epics/gh-23/main.md`](epics/gh-23/main.md) — v0.6.x project-knowledge epic, with the dashboard sections populated.
- [`stories/gh-25/main.md`](stories/gh-25/main.md) — single-PR single-subtask story for the shape-guard fix (issue #25 → v0.6.1).
- [`decisions/0001-text-only-reviewer/main.md`](decisions/0001-text-only-reviewer/main.md) — anti-tangent's seminal architectural decision: the reviewer is text-only and never reads the codebase.
- [`modules/review-pipeline/main.md`](modules/review-pipeline/main.md) — one coherent capability (the validate_X surface) spanning four Go packages.

No feature or glossary dogfood — INTEGRATION.md and the design specs already document anti-tangent's features and glossary terms; KB notes would duplicate.
