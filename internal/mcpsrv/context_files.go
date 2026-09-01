// Package mcpsrv: resolution of validate_plan's context_paths attachments.
// Scope: filesystem reads and cap enforcement only; no provider calls, no
// rendering. See design §3.
package mcpsrv

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/patiently/anti-tangent-mcp/internal/config"
)

// maxContextFiles bounds how many files one validate_plan call may attach.
// A fixed constant rather than an env var, mirroring the existing 50-entry
// cap on pinned_by: it exists to stop a degenerate list from producing an
// unreadable prompt, not to be tuned. The byte caps in config are the dials.
const maxContextFiles = 50

// contextFile is one resolved attachment. Source carries the symlink-resolved
// path, byte count, and sha256 actually read — so the prompt, the summary
// provenance, and the plan cache key all describe the same bytes.
type contextFile struct {
	Source  fileSource
	Content string
}

// contextTooLargeError signals a cap breach. Exactly one of three shapes:
// Count > 0 for the file-COUNT cap, Path != "" for a per-file byte cap, and
// neither for the whole-set byte cap — so the caller can build a message that
// says which dial to turn. Distinct from the plain errors returned for bad
// paths, because a cap breach maps to the too-large ENVELOPE while a bad path
// maps to a transport error (§3.3).
//
// All THREE caps use this type. The count cap used to return a plain error,
// which meant one of the three "you asked for too much" outcomes arrived as a
// transport error and the other two as envelopes — the caller had to handle
// the same class of mistake two different ways.
type contextTooLargeError struct {
	Path  string
	Bytes int
	Limit int
	Count int
	// PayloadLimit is the whole-set cap in force, carried on the PER-FILE
	// shape so the refusal can name the ceiling the suggested remedy has to
	// stay under. Without it the message advised "raise
	// ANTI_TANGENT_CONTEXT_MAX_FILE_BYTES" with no hint that raising it above
	// ANTI_TANGENT_CONTEXT_MAX_PAYLOAD_BYTES makes the very next boot fail
	// with "must be <= ANTI_TANGENT_CONTEXT_MAX_PAYLOAD_BYTES" (config.go's
	// cross-check) — the server refusing a file and then, if you follow its
	// own advice literally, refusing to start. Unset on the other two shapes.
	// Rendered as "whole-set cap N" right after the per-file cap so it
	// survives the summary block's evidence truncation; see Error().
	PayloadLimit int
}

func (e *contextTooLargeError) Error() string {
	if e.Count > 0 {
		return fmt.Sprintf(
			"context_paths: %d entries > cap %d (attach only the files the plan makes claims about)",
			e.Count, e.Limit)
	}
	if e.Path != "" {
		// The whole-set cap is named IMMEDIATELY after the per-file one, not
		// at the end of the remedy clause. This message becomes a finding's
		// evidence, and formatFindingEvidence truncates evidence at
		// summaryEvidenceMax (120) runes — with an absolute path in front of
		// it the tail is always cut, so the previous wording showed the
		// operator "(raise ANTI_TANGENT_CONTEXT_MAX_FILE_BYTES…" and elided
		// the "but not above…" that stops that advice from breaking their
		// next boot. Front-loading the ceiling means the truncated form
		// carries both numbers and no half-advice; the full text, with both
		// env var names, still reaches the JSON plan_findings.
		return fmt.Sprintf(
			"context_paths: %q is %d bytes > per-file cap %d (whole-set cap %d — raise both ANTI_TANGENT_CONTEXT_MAX_FILE_BYTES and ANTI_TANGENT_CONTEXT_MAX_PAYLOAD_BYTES, or drop the file)",
			e.Path, e.Bytes, e.Limit, e.PayloadLimit)
	}
	return fmt.Sprintf(
		"context_paths: attached set is %d bytes > cap %d (raise ANTI_TANGENT_CONTEXT_MAX_PAYLOAD_BYTES, or attach fewer files)",
		e.Bytes, e.Limit)
}

// resolveContextPaths reads every path subject to cfg's roots and caps.
//
// Order is load-bearing. The count cap is checked FIRST, before any path is
// opened, so a degenerate list costs one comparison rather than 500 syscalls.
// Then each file is resolved through resolveFileInput — which owns the
// symlink, roots, O_NOFOLLOW, regular-file, and capped-read guarantees — and
// only afterwards is the running total compared against the set cap, so the
// error can report the true accumulated size.
//
// De-duplication happens on the SYMLINK-RESOLVED path, so two arguments
// naming the same file through different routes are billed and rendered once.
// The first occurrence wins, preserving caller-supplied ordering.
func resolveContextPaths(paths []string, cfg config.Config) ([]contextFile, int, error) {
	if len(paths) == 0 {
		return nil, 0, nil
	}
	if len(paths) > maxContextFiles {
		return nil, 0, &contextTooLargeError{Count: len(paths), Limit: maxContextFiles}
	}

	out := make([]contextFile, 0, len(paths))
	seen := make(map[string]bool, len(paths))
	total := 0

	for _, p := range paths {
		content, src, err := resolveFileInput(p, cfg.PlanRoots, cfg.ContextMaxFileBytes)
		if errors.Is(err, errTooLarge) {
			return nil, 0, &contextTooLargeError{
				Path:         src.Path,
				Bytes:        src.Bytes,
				Limit:        cfg.ContextMaxFileBytes,
				PayloadLimit: cfg.ContextMaxPayloadBytes,
			}
		}
		if err != nil {
			return nil, 0, fmt.Errorf("context_paths: %w", err)
		}
		if seen[src.Path] {
			continue
		}
		seen[src.Path] = true
		total += src.Bytes
		if total > cfg.ContextMaxPayloadBytes {
			return nil, 0, &contextTooLargeError{
				Bytes: total,
				Limit: cfg.ContextMaxPayloadBytes,
			}
		}
		out = append(out, contextFile{Source: src, Content: content})
	}
	return out, total, nil
}

// resolveDirInput is the directory-shaped sibling of resolveFileInput, used
// for repo_root. Same absolute-path requirement, same symlink resolution
// before the roots check, same rejectControlChars guard, but it stats for a
// directory and reads nothing.
//
// The rejectControlChars call is in LOCKSTEP with resolveFileInput's, and for
// the same reason: EvalSymlinks returns the target name verbatim, and a
// resolved repo_root carrying an embedded newline reaches the operator's eyes
// unescaped through the failure paths BELOW it. The %w-wrapped os.PathError
// from a failing stat prints the raw path, that error becomes the reason
// string on the minor `repo_root unusable (<reason>)` finding, and
// formatFindingEvidence preserves newlines and merely re-indents them — so
// the path forges lines in the paste-ready summary block a human reads as
// server output. rejectControlChars' own comment ("%q escapes the offending
// bytes, so naming the path in the error cannot re-inject them") holds only
// for the errors that use %q; these use %w. Rejecting up front is the guard
// that makes the claim true for both siblings.
func resolveDirInput(path string, roots []string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("repo_root must be absolute, got %q", path)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolve repo_root %q: %w", path, err)
	}
	if err := rejectControlChars(resolved); err != nil {
		return "", err
	}
	if !withinRoots(resolved, roots) {
		return "", fmt.Errorf("repo_root %q is outside ANTI_TANGENT_PLAN_ROOTS", resolved)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("stat repo_root %q: %w", resolved, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("repo_root %q is not a directory", resolved)
	}
	return resolved, nil
}
