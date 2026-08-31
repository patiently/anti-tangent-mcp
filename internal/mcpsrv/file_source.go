// Package mcpsrv: shared file-path input resolution for validate_plan and
// validate_completion. Scope: filesystem reads only; no provider calls.
//
// Named file_source.go rather than plan_source.go because validate_completion
// shares it — a plan-specific name would invite a divergent second copy the
// first time someone adds paths to check_progress. See design §3.
package mcpsrv

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// fileSource is the provenance of one resolved file input. Path is the
// symlink-resolved path actually read, so a caller reading the summary sees
// what the reviewer saw rather than what was requested.
type fileSource struct {
	Path   string
	Bytes  int
	SHA256 string
}

// String renders the summary provenance line's value. Empty for a zero
// fileSource, so plan_text callers get no source line at all.
func (s fileSource) String() string {
	if s.Path == "" {
		return ""
	}
	short := s.SHA256
	if len(short) > 8 {
		short = short[:8]
	}
	return fmt.Sprintf("%s (%d B, sha256 %s…)", s.Path, s.Bytes, short)
}

// errTooLarge signals the file exceeded the caller's cap. Callers map it to
// their own too-large envelope rather than surfacing a transport error, so the
// response shape matches an oversized inline payload.
var errTooLarge = errors.New("file exceeds cap")

// resolveFileInput reads path subject to roots and a byte cap.
//
// Order is load-bearing: symlinks resolve BEFORE the roots check so a symlink
// cannot hop outside an allowlisted root, and the size check uses stat so an
// oversized file is never read into memory.
func resolveFileInput(path string, roots []string, maxBytes int) (string, fileSource, error) {
	if strings.TrimSpace(path) == "" {
		return "", fileSource{}, errors.New("path is empty")
	}
	if !filepath.IsAbs(path) {
		return "", fileSource{}, fmt.Errorf("path must be absolute, got %q", path)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fileSource{}, fmt.Errorf("resolve %q: %w", path, err)
	}
	if !withinRoots(resolved, roots) {
		return "", fileSource{}, fmt.Errorf(
			"%q is outside ANTI_TANGENT_PLAN_ROOTS (%s)", resolved, strings.Join(roots, ":"))
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fileSource{}, fmt.Errorf("stat %q: %w", resolved, err)
	}
	if !info.Mode().IsRegular() {
		return "", fileSource{}, fmt.Errorf("%q is not a regular file", resolved)
	}
	if info.Size() > int64(maxBytes) {
		return "", fileSource{Path: resolved, Bytes: int(info.Size())}, errTooLarge
	}
	b, err := os.ReadFile(resolved)
	if err != nil {
		return "", fileSource{}, fmt.Errorf("read %q: %w", resolved, err)
	}
	sum := sha256.Sum256(b)
	return string(b), fileSource{
		Path:   resolved,
		Bytes:  len(b),
		SHA256: hex.EncodeToString(sum[:]),
	}, nil
}

// withinRoots reports whether p sits inside any root. Empty roots means
// unrestricted. Matching requires a separator boundary so /home/foo does not
// authorize /home/foobar.
func withinRoots(p string, roots []string) bool {
	if len(roots) == 0 {
		return true
	}
	p = filepath.Clean(p)
	for _, r := range roots {
		r = filepath.Clean(r)
		if p == r || strings.HasPrefix(p, r+string(filepath.Separator)) {
			return true
		}
	}
	return false
}
