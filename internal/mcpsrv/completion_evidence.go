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
// paths named in summary that are NOT present in either files (path-suffix
// match — see pathTailMatches) or finalDiff (substring match). The result is
// used by ValidateCompletion to render an advisory note in the post-review
// prompt — it never mutates findings, never rejects the request, and never
// affects the verdict. It only nudges the reviewer to require full evidence
// if a listed path is a deliverable.
func referencedPathsMissingEvidence(summary string, files []FileArg, finalDiff string) []string {
	candidates := referencedEvidencePathRE.FindAllString(summary, -1)
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
		if pathPresentInEvidence(path, files, finalDiff) {
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
// (path-suffix match — see pathTailMatches) or anywhere in the final_diff
// text (substring). The final_diff substring match is intentionally
// permissive — diff headers, rename old/new paths, and contextual filename
// mentions all count as "evidence was provided."
func pathPresentInEvidence(path string, files []FileArg, finalDiff string) bool {
	for _, f := range files {
		if pathTailMatches(f.Path, path) {
			return true
		}
	}
	return strings.Contains(finalDiff, path)
}

// pathTailMatches reports whether tail names the same file as candidate:
// either the two are identical, or candidate ends with tail immediately
// after a path separator.
//
// docs/protocol/implementer.md tells implementers to pass ABSOLUTE
// final_files paths, while args.Summary is scanned for the RELATIVE paths
// implementers actually write in prose (e.g. "docs/foo.md") — so exact
// equality alone would never match and this advisory would fire spuriously
// on every doc deliverable submitted the documented way. Comparing by
// suffix fixes that (an absolute f.Path ending in the relative summary
// mention counts as present), but a bare suffix check on its own is too
// loose: "myfoo.md" would satisfy a reference to "foo.md" purely because
// the characters happen to line up at the tail, with no real path
// relationship between the two. Requiring a separator immediately before
// the matched tail closes that gap while still accepting the legitimate
// absolute-vs-relative case.
//
// Both sides are normalized to forward slashes before comparing, and the
// separator check accepts either byte after that — NOT filepath.Separator,
// which is a single OS-specific byte ('/' when this server itself runs on
// Unix). final_files paths are the implementer's own filesystem paths, which
// on a Windows implementer are backslash-separated (e.g. `C:\repo\docs\foo.md`)
// regardless of what OS this server happens to run on; comparing against
// filepath.Separator alone made this advisory fire spuriously on every doc
// deliverable from a Windows caller — the exact false positive this function
// exists to avoid, just triggered from the other side.
func pathTailMatches(candidate, tail string) bool {
	candidate = strings.ReplaceAll(candidate, `\`, "/")
	tail = strings.ReplaceAll(tail, `\`, "/")
	if candidate == tail {
		return true
	}
	if tail == "" || !strings.HasSuffix(candidate, tail) {
		return false
	}
	i := len(candidate) - len(tail)
	return candidate[i-1] == '/'
}
