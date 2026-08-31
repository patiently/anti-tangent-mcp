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
	"io"
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
// fileSource, so plan_text callers get no source line at all. SHA256 is
// empty on the too-large early exit (resolveFileInput returns before
// hashing), so the "sha256 …" fragment is omitted entirely rather than
// rendering as zero hex digits.
func (s fileSource) String() string {
	if s.Path == "" {
		return ""
	}
	if s.SHA256 == "" {
		return fmt.Sprintf("%s (%d B)", s.Path, s.Bytes)
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

// readCapped reads at most maxBytes+1 bytes from r and returns errTooLarge
// (wrapping nothing extra — callers use errors.Is) when more than maxBytes
// bytes were available. Factored out of resolveFileInput so the cap-at-read
// boundary can be unit tested against a plain io.Reader, independent of any
// filesystem stat: the whole point of enforcing the cap here (rather than
// trusting a stat taken moments earlier) is that it holds regardless of what
// stat reported.
//
// On overflow the returned []byte has length maxBytes+1, not the source's
// true size — callers that need the true size re-stat separately.
func readCapped(r io.Reader, maxBytes int) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, int64(maxBytes)+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxBytes {
		return b, errTooLarge
	}
	return b, nil
}

// resolveFileInput reads path subject to roots and a byte cap.
//
// Order is load-bearing: symlinks resolve BEFORE the roots check so a symlink
// cannot hop outside an allowlisted root, and the size check uses stat so an
// oversized file is never read into memory.
//
// Between the roots check and the read there is otherwise a TOCTOU window:
// the resolved path could be replaced with a symlink pointing outside the
// roots, or the file could grow past maxBytes, after the check but before
// the read. openNoFollow (platform-specific: real O_NOFOLLOW on Unix, a
// plain open on Windows — see file_source_unix.go / file_source_windows.go)
// closes the first half of that window by refusing to follow a symlink at
// the final path component; the capped io.LimitReader read below closes the
// second half by making the cap byte-accurate regardless of what os.Stat
// reported a moment earlier. See README's filesystem-access section for the
// caveats this does and does not cover.
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
			"%q is outside ANTI_TANGENT_PLAN_ROOTS (%s)", resolved, strings.Join(roots, string(os.PathListSeparator)))
	}

	f, err := openNoFollow(resolved)
	if err != nil {
		return "", fileSource{}, fmt.Errorf("open %q: %w", resolved, err)
	}
	defer f.Close()

	// Re-stat from the open handle rather than trusting the path-based check
	// that would have run between the roots check and here — this is the
	// same file the read below actually reads.
	info, err := f.Stat()
	if err != nil {
		return "", fileSource{}, fmt.Errorf("stat %q: %w", resolved, err)
	}
	if !info.Mode().IsRegular() {
		return "", fileSource{}, fmt.Errorf("%q is not a regular file", resolved)
	}
	if info.Size() > int64(maxBytes) {
		return "", fileSource{Path: resolved, Bytes: int(info.Size())}, errTooLarge
	}

	// Read capped independently of the stat above: readCapped stops at
	// maxBytes+1, so a file that grows past the cap between the stat and
	// this read is still caught rather than silently read in full.
	b, err := readCapped(f, maxBytes)
	if errors.Is(err, errTooLarge) {
		trueBytes := len(b)
		if grown, statErr := f.Stat(); statErr == nil {
			trueBytes = int(grown.Size())
		}
		return "", fileSource{Path: resolved, Bytes: trueBytes}, errTooLarge
	}
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
// unrestricted. Containment is decided via filepath.Rel rather than a raw
// string-prefix test: a root of "/" (or a Windows drive root like "C:\") is
// its own filesystem root, so a naive r+separator prefix test degenerates to
// requiring the literal string "//" and matches nothing beneath it.
// filepath.Rel handles that case correctly while still requiring a separator
// boundary, so /home/foo does not authorize /home/foobar.
func withinRoots(p string, roots []string) bool {
	if len(roots) == 0 {
		return true
	}
	p = filepath.Clean(p)
	for _, r := range roots {
		r = filepath.Clean(r)
		rel, err := filepath.Rel(r, p)
		if err != nil {
			continue
		}
		if rel == "." {
			return true
		}
		if rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}
