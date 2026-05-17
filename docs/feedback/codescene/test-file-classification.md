# CodeScene MCP feedback: document test-file classification expectations

## Summary

Code Health checks can be easier to interpret when users know how test files are classified and whether test-only changes are expected to be quiet, lower priority, or reported like production-code changes.

## Suggested Documentation

Document the expected treatment of test files in CodeScene MCP output, including:

- How test files are detected or classified.
- Whether test-file findings are reported by default.
- Whether test-file findings are weighted differently from production-code findings.
- How mixed changes across production and test files are summarized.
- Any configuration or conventions users can apply when their repository uses non-standard test paths.

## Why This Helps

Clear expectations would reduce ambiguity during task completion reviews. If a change only touches tests, callers can understand whether quiet output is expected. If findings are reported for test files, callers can interpret whether they indicate a current-change regression, an inherited issue, or a classification mismatch that needs configuration.
