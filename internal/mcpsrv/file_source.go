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
	"runtime"
	"strings"
	"unicode"
	"unicode/utf8"
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
	return fmt.Sprintf("%s (%d B, sha256 %s…)", s.Path, s.Bytes, shortHash(s.SHA256))
}

// shortHash is the 8-hex-digit display prefix of a sha256. One helper rather
// than two copies, so the summary block's provenance line and the reviewer
// prompt's BEGIN FILE delimiter can never drift to different widths and name
// the same file by two different identities. A hash shorter than 8 digits
// (only the empty string in practice) is returned unchanged.
func shortHash(sum string) string {
	if len(sum) > 8 {
		return sum[:8]
	}
	return sum
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
	if err := rejectControlChars(resolved); err != nil {
		return "", fileSource{}, err
	}
	if !withinRoots(resolved, roots) {
		return "", fileSource{}, fmt.Errorf(
			"%q is outside ANTI_TANGENT_PLAN_ROOTS (%s)", resolved, strings.Join(roots, string(os.PathListSeparator)))
	}

	f, err := openNoFollow(resolved)
	if err != nil {
		return "", fileSource{}, fmt.Errorf("open %q: %w", resolved, err)
	}
	defer func() { _ = f.Close() }()

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

// rejectControlChars refuses a resolved path containing any character that
// can forge structure in the rendered prompt: C0 controls (< 0x20), DEL
// (0x7f), the Unicode line/paragraph separators U+2028 and U+2029, and every
// Unicode format character (category Cf) — which covers U+202E RIGHT-TO-LEFT
// OVERRIDE (the Trojan-Source class, CVE-2021-42574) and U+200B ZERO WIDTH
// SPACE.
//
// This is a PROMPT-INTEGRITY guard, not a filesystem one. Linux permits every
// byte but '/' and NUL in a file name, EvalSymlinks returns whatever the link
// target says verbatim, and text/template escapes nothing — so a file (or a
// symlink target) named with an embedded newline injects a line break into
// two places that are structurally trusted:
//
//   - the enumerated attached-paths list rendered INSIDE the reviewer's
//     ground rules (plan_rules.tmpl), which sits ABOVE and OUTSIDE the
//     nonce-guarded BEGIN/END FILE region. Injected lines land in the
//     instruction region the nonce deliberately does not protect, so a
//     hostile repo could append its own ground rules and make the plan gate
//     pass anything;
//   - the `context:` provenance list in the summary block (summary.go),
//     where the same bytes forge lines a human reads as server output.
//
// One rejection covers both. It is a plain error (a transport error), not a
// too-large envelope: unlike a cap breach there is no caller-tunable dial
// and no partial result to return — the path is simply not renderable.
//
// A `r < 0x20` predicate alone was not enough: U+2028 and U+2029 ARE line
// breaks to a model reading the prompt, U+202E reverses the apparent
// direction of everything after it (so a path can render as text it does not
// contain), and U+200B is invisible padding that defeats an eyeball
// comparison of two paths. All of them survive a C0-only test.
//
// Rejecting the whole Cf category fails CLOSED on the rare legitimate
// filename that carries a format character, which is the right trade here:
// the alternative is rendering an unreviewable path into the instruction
// region, and the error names the path so the operator can rename it.
//
// %q escapes the offending bytes, so naming the path in the error cannot
// re-inject them into whatever reads the error. The code point is also named
// as U+XXXX: %q renders U+200B as an escape the operator then has to decode,
// and "control character" was a misnomer once the predicate grew to cover all
// of unicode.Cf — a soft hyphen is a FORMAT character, not a control one, and
// an operator searching their filenames for "a control character" would not
// find it.
func rejectControlChars(resolved string) error {
	if i := strings.IndexFunc(resolved, forgesPromptStructure); i >= 0 {
		r, _ := utf8.DecodeRuneInString(resolved[i:])
		return fmt.Errorf(
			"resolved path %q contains a disallowed control or format character U+%04X at byte %d; refusing to render it into the reviewer prompt (rename the file, or drop it from context_paths)",
			resolved, r, i)
	}
	return nil
}

// forgesPromptStructure reports whether r can forge prompt structure. Split
// out of rejectControlChars so the predicate can be stated once and read
// without the surrounding error plumbing.
func forgesPromptStructure(r rune) bool {
	return r < 0x20 || r == 0x7f || r == '\u2028' || r == '\u2029' || unicode.Is(unicode.Cf, r)
}

// withinRoots reports whether p sits inside any root. Empty roots means
// unrestricted. Containment is decided via filepath.Rel rather than a raw
// string-prefix test: a root of "/" (or a Windows drive root like "C:\") is
// its own filesystem root, so a naive r+separator prefix test degenerates to
// requiring the literal string "//" and matches nothing beneath it.
// filepath.Rel handles that case correctly while still requiring a separator
// boundary, so /home/foo does not authorize /home/foobar.
//
// FIX 3: both sides are passed through withinRootsFold first, which is a
// no-op everywhere except a real Windows build (runtime.GOOS ==
// "windows"), where it lower-cases both strings before filepath.Rel ever
// sees them. Windows filesystems are case-insensitive, so an operator
// writing ANTI_TANGENT_PLAN_ROOTS=C:\Repo while EvalSymlinks returns the
// on-disk casing (c:\repo\...) would otherwise see filepath.Rel treat the
// volume names / path elements as distinct and refuse every legitimate
// path. Unix stays byte-for-byte case-sensitive deliberately: folding case
// there would WIDEN the roots allowlist — a security setting — rather than
// merely tolerate a cosmetic difference.
func withinRoots(p string, roots []string) bool {
	if len(roots) == 0 {
		return true
	}
	p = filepath.Clean(p)
	pFold := withinRootsFold(p, runtime.GOOS)
	for _, r := range roots {
		r = filepath.Clean(r)
		rel, err := filepath.Rel(withinRootsFold(r, runtime.GOOS), pFold)
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

// withinRootsFold returns s unchanged unless goos is "windows", in which
// case it is lower-cased for a case-insensitive comparison. goos is a
// parameter — rather than this function reading runtime.GOOS itself —
// purely so the Windows-shaped folding decision is unit-testable on any
// host (including this project's Linux CI) without requiring an actual
// Windows build: path/filepath's own volume-name and separator handling is
// selected at Go's build time per GOOS, so a runtime-only "pretend we're on
// Windows" flag could never exercise filepath.Rel's real Windows behavior
// from a non-Windows test binary anyway. What CAN be verified everywhere is
// this folding decision itself, which is exactly what TestWithinRootsFold
// does.
func withinRootsFold(s, goos string) string {
	if goos != "windows" {
		return s
	}
	return strings.ToLower(s)
}
