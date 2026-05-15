// Package mcpsrv: helpers for the validate_completion evidence prompt's
// "referenced paths missing evidence" advisory. See ValidateCompletion.
package mcpsrv

import (
	"regexp"
	"strings"
)

// referencedEvidencePathRE matches doc/artifact path tokens that might be named
// in the implementer's summary. The extension list is intentionally narrow:
// source-code extensions (.go, .kt, .py, .ts) are excluded because they almost
// always appear in diffs even when not deliverables — including them would
// produce noisy hints. Doc/config formats are far more likely to be deliverables
// that need explicit evidence.
var referencedEvidencePathRE = regexp.MustCompile(`[A-Za-z0-9_./-]+\.(?:md|txt|json|ya?ml)\b`)

// referencedPathsMissingEvidence returns the deduplicated set of doc/artifact
// paths named in args.Summary that are NOT present in either args.FinalFiles
// (exact Path match) or args.FinalDiff (substring match). The result is used
// by ValidateCompletion to render an advisory note in the post-review prompt
// — it never mutates findings, never rejects the request, and never affects
// the verdict. It only nudges the reviewer to require full evidence if a
// listed path is a deliverable.
func referencedPathsMissingEvidence(args ValidateCompletionArgs) []string {
	candidates := referencedEvidencePathRE.FindAllString(args.Summary, -1)
	if len(candidates) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	missing := make([]string, 0, len(candidates))
	for _, path := range candidates {
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		if pathPresentInEvidence(path, args) {
			continue
		}
		missing = append(missing, path)
	}
	if len(missing) == 0 {
		return nil
	}
	return missing
}

// pathPresentInEvidence reports whether path appears as a final_files entry
// (exact Path equality) or anywhere in the final_diff text (substring). The
// final_diff substring match is intentionally permissive — diff headers,
// rename old/new paths, and contextual filename mentions all count as
// "evidence was provided."
func pathPresentInEvidence(path string, args ValidateCompletionArgs) bool {
	for _, f := range args.FinalFiles {
		if f.Path == path {
			return true
		}
	}
	return strings.Contains(args.FinalDiff, path)
}
