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

// contextTooLargeError signals a cap breach. Path names the offending file
// for a per-file breach and is empty for a set-level breach, so the caller
// can build a message that says which dial to turn. Distinct from the plain
// errors returned for bad paths, because a cap breach maps to the
// too-large ENVELOPE while a bad path maps to a transport error (§3.3).
type contextTooLargeError struct {
	Path  string
	Bytes int
	Limit int
}

func (e *contextTooLargeError) Error() string {
	if e.Path != "" {
		return fmt.Sprintf(
			"context_paths: %q is %d bytes > per-file cap %d (raise ANTI_TANGENT_CONTEXT_MAX_FILE_BYTES, or drop the file)",
			e.Path, e.Bytes, e.Limit)
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
		return nil, 0, fmt.Errorf(
			"context_paths: %d entries > cap %d (attach only the files the plan makes claims about)",
			len(paths), maxContextFiles)
	}

	out := make([]contextFile, 0, len(paths))
	seen := make(map[string]bool, len(paths))
	total := 0

	for _, p := range paths {
		content, src, err := resolveFileInput(p, cfg.PlanRoots, cfg.ContextMaxFileBytes)
		if errors.Is(err, errTooLarge) {
			return nil, 0, &contextTooLargeError{
				Path:  src.Path,
				Bytes: src.Bytes,
				Limit: cfg.ContextMaxFileBytes,
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
// before the roots check, but it stats for a directory and reads nothing.
func resolveDirInput(path string, roots []string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("repo_root must be absolute, got %q", path)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolve repo_root %q: %w", path, err)
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
