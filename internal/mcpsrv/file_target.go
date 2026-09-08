// Package mcpsrv: write-path resolution for code_write.
//
// resolveFileInput cannot be reused. It EvalSymlinks the FULL path, which
// fails outright when the file does not exist yet — the normal case for a
// generated file. The parent is resolved instead, and the leaf is guarded
// against a planted symlink so it cannot redirect the write outside the roots
// that were just checked. See design §5.3.
//
// # Two write shapes, and why they differ
//
// overwrite: false creates the leaf directly with O_CREAT|O_EXCL|O_NOFOLLOW.
// That is the right primitive there and is deliberately left alone: O_EXCL is
// the atomic "refuse if anything is already here", nothing pre-existing can
// be lost, and routing the create through a temp file would trade that real
// guarantee for a window in which the name is unclaimed.
//
// overwrite: true writes a temp file in the target's OWN directory and
// renames it over the target. The old O_TRUNC open emptied the caller's file
// at open(2) — before a single byte of new content had been written — so any
// later failure left the caller with an empty or half-written file and no way
// back. With the rename, the target holds its original bytes until the
// instant it holds the complete new ones.
//
// # Residual exposure: an ancestor directory swapped mid-flight
//
// Between the EvalSymlinks(parent) below and the open (or rename) that
// follows, an attacker could swap an ANCESTOR directory for a symlink. The
// leaf guard does not catch that — O_NOFOLLOW only ever inspects the last
// component of the path handed to open(2), and os.Lstat likewise only
// describes the leaf.
//
// This window is knowingly left open. Closing it needs openat2(2) with
// RESOLVE_NO_SYMLINKS, or a hand-rolled per-component O_NOFOLLOW walk holding
// a directory fd at every step — Linux-specific machinery, and a rewrite of
// this resolution path rather than an addition to it.
//
// Be precise about what is being accepted, because the easy version of this
// argument is wrong. It is tempting to say the race grants nothing, since
// swapping an ancestor requires write permission on a directory inside the
// roots and anyone holding that could write the target file directly. That
// does not follow. Winning this race redirects the write OUTSIDE the roots,
// which is the one thing writing the target directly cannot achieve, and the
// static version of the same attack — planting the symlink and leaving it
// there — IS caught, by EvalSymlinks resolving through it and withinRoots
// then refusing. So the race is not a no-op.
//
// What actually makes it acceptable is who can run it, and that splits:
//
//   - A racer running as the SAME uid as this server — the usual case, where
//     the roots are a checkout owned by the user whose agent launched the
//     server — gains nothing, because same-uid is not a privilege boundary.
//     Such a process can already ptrace this one, or simply write the file
//     itself with these exact permissions.
//   - A racer running as a DIFFERENT uid needs write permission on a
//     directory inside the roots to plant the swap, which requires a group-
//     or world-writable directory inside ANTI_TANGENT_PLAN_ROOTS. Then the
//     race is a real escalation and this code does not stop it. Do not point
//     the roots at a tree that is writable by users you do not trust; that
//     configuration is outside what this containment check claims to cover,
//     the same way it is for the read path in file_source.go.
//
// The two write paths are not equally exposed either, which bounds the damage
// further. The overwrite path renames a temp file it created inside the
// resolved parent, so an ancestor swap invalidates the rename's SOURCE as
// well as its destination: both operands re-resolve through the swapped
// ancestor, the temp is not there, and the rename fails closed with ENOENT
// instead of landing outside. That is a bound, not a guarantee — a racer
// could still defeat it by discovering the temp's randomly generated name and
// hardlinking it into the redirected directory within the same window — but
// it is a far narrower target than the create path, which has no such
// accident. What bounds the create path instead is O_EXCL: it can only ever
// create a file where none exists, never clobber an existing one.
//
// Note that all of this covers the ANCESTOR only. It does NOT extend to the
// leaf, and the leaf guard is not optional: a symlink at the leaf can be
// planted with write permission on the target's own directory, needs no race
// at all, and points outside the roots. That is why it is refused under both
// overwrite values.
package mcpsrv

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// resolveWriteTarget validates target and returns an in-flight write the
// caller finishes with Commit or abandons with Discard.
//
// Deliberately does NOT create parent directories: creating a path that does
// not exist reopens the containment problem this function exists to close,
// and a worker model acting on a typo would scatter directories.
func resolveWriteTarget(target string, roots []string, overwrite bool) (*writeTx, error) {
	if strings.TrimSpace(target) == "" {
		return nil, errors.New("target_path is empty")
	}
	if !filepath.IsAbs(target) {
		return nil, fmt.Errorf("target_path must be absolute, got %q", target)
	}

	parent, leaf := filepath.Dir(target), filepath.Base(target)
	if leaf == "." || leaf == ".." || leaf == string(filepath.Separator) {
		return nil, fmt.Errorf("target_path %q does not name a file", target)
	}

	parentResolved, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return nil, fmt.Errorf(
			"target_path parent directory %q must already exist (code_write never creates directories): %w", parent, err)
	}

	resolved := filepath.Join(parentResolved, leaf)
	if err := rejectControlChars(resolved); err != nil {
		return nil, err
	}
	if !withinRoots(parentResolved, roots) {
		return nil, fmt.Errorf(
			"%q is outside ANTI_TANGENT_PLAN_ROOTS (%s)", resolved, strings.Join(roots, string(os.PathListSeparator)))
	}

	// The platform gate sits here, after everything that can be decided from
	// the path alone and before the first call that changes the filesystem.
	// A caller whose path is also malformed gets the specific complaint about
	// the path first, which is the more actionable of the two; a caller whose
	// path is fine gets told the platform is the problem, once, before
	// anything has been created.
	if err := writeTargetSupported(); err != nil {
		return nil, err
	}

	if overwrite {
		return beginOverwrite(parentResolved, resolved)
	}
	return beginCreate(resolved)
}

// beginCreate claims the leaf itself. Unchanged from the original design and
// deliberately so — see the package comment.
func beginCreate(resolved string) (*writeTx, error) {
	f, err := openCreateNoFollow(resolved)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("%q already exists; pass overwrite: true to replace it", resolved)
		}
		return nil, fmt.Errorf("open %q for writing: %w", resolved, err)
	}
	// pending is the target itself: O_EXCL proves this call created it, so
	// removing it on failure destroys nothing that was there before.
	return &writeTx{f: f, final: resolved, pending: resolved}, nil
}

// beginOverwrite opens a temp file alongside the target, to be renamed over
// it by Commit.
func beginOverwrite(dir, resolved string) (*writeTx, error) {
	mode, err := inspectOverwriteTarget(resolved)
	if err != nil {
		return nil, err
	}

	// Same directory, not os.TempDir(): rename(2) is atomic only within one
	// filesystem, and across filesystems it is not even permitted (EXDEV).
	// os.CreateTemp opens with O_CREATE|O_EXCL, so the temp's own name cannot
	// be hijacked by a planted symlink either.
	f, err := os.CreateTemp(dir, tempPattern(filepath.Base(resolved)))
	if err != nil {
		return nil, fmt.Errorf("create a temporary file next to %q: %w", resolved, err)
	}
	tx := &writeTx{f: f, final: resolved, tmp: f.Name(), pending: f.Name()}

	// os.CreateTemp opens 0600. Renaming that over the target would silently
	// tighten the file's permissions, so carry the mode across — the old
	// O_TRUNC open preserved it for free by keeping the same inode, and a
	// caller should not have to notice which mechanism ran.
	if err := f.Chmod(mode); err != nil {
		_ = tx.Discard()
		return nil, fmt.Errorf("set mode %v on the temporary file for %q: %w", mode, resolved, err)
	}
	return tx, nil
}

// inspectOverwriteTarget refuses to overwrite anything that is not a plain
// regular file and returns the mode a replacement should carry.
//
// This check is load-bearing, not cosmetic. The old implementation got the
// leaf-symlink refusal for free from O_NOFOLLOW, which fails the open. A
// rename has the opposite default: rename(2) replaces a symlink AT the
// destination rather than following it or refusing it, so switching to
// rename without this Lstat would have quietly converted a hard refusal into
// a silent "the link is gone now". The refusal is a security property (see
// the package comment on why the leaf differs from an ancestor), so it is
// restated explicitly here rather than dropped.
//
// The Lstat is not itself a race guard and does not claim to be — a symlink
// planted between it and the rename wins the race. That costs nothing: the
// rename still does not follow the link, so the symlink's target is never
// written. The worst outcome is that a link inside the roots is replaced by
// a regular file, which the only attacker who could set it up could have
// done directly.
func inspectOverwriteTarget(resolved string) (os.FileMode, error) {
	info, err := os.Lstat(resolved)
	switch {
	case errors.Is(err, os.ErrNotExist):
		// overwrite against a path that does not exist yet is a plain create.
		return 0o644, nil
	case err != nil:
		return 0, fmt.Errorf("stat %q: %w", resolved, err)
	case info.Mode()&os.ModeSymlink != 0:
		return 0, fmt.Errorf(
			"%q is a symlink; code_write never writes through one, because the link points outside the "+
				"directory that was just containment-checked", resolved)
	case !info.Mode().IsRegular():
		return 0, fmt.Errorf(
			"%q is not a regular file (%s); code_write only overwrites regular files", resolved, info.Mode().Type())
	}
	return info.Mode().Perm(), nil
}

// tempPattern builds the os.CreateTemp pattern for a temp file destined for
// leaf.
//
// The leading dot matters twice over: it keeps a stranded temp out of the Go
// toolchain's sight (cmd/go ignores files beginning with "." or "_", so a
// leftover in a package directory cannot break a build) and out of most
// listings. The leaf is embedded so a human who does find one knows what it
// was for, and truncated so a long generated filename cannot push the temp
// past NAME_MAX and turn every overwrite into an ENAMETOOLONG failure.
func tempPattern(leaf string) string {
	const maxLeaf = 100
	if len(leaf) > maxLeaf {
		leaf = leaf[:maxLeaf]
	}
	return ".anti-tangent-" + leaf + ".*.tmp"
}

// writeTx is one in-flight write to a target_path.
//
// It is a transaction, not a file handle. Commit is the only thing that makes
// the bytes visible at target_path and Discard is the only thing that cleans
// up; there is deliberately no Close, because on the overwrite path a plain
// close is ambiguous — it would either commit a half-written file or strand
// the temp, depending on which one the caller meant.
type writeTx struct {
	f     *os.File
	final string // the path the caller asked for
	tmp   string // the temp file's path; empty on the create path

	// pending is the path holding this call's bytes while the call is still
	// in flight: the temp file on the overwrite path, the target itself on
	// the create path. Emptied once those bytes are renamed into place or
	// removed, which is what makes Discard idempotent.
	pending string

	// stranded records a removal that failed, so Discard can name the file it
	// could not clean up instead of silently claiming it did.
	stranded    string
	strandedErr error

	committed bool
}

// Name reports the path the caller asked for, never the temp file's — it is
// what gets reported back as `written` and what appears in error messages,
// and neither should ever mention an implementation detail the caller cannot
// act on.
func (w *writeTx) Name() string { return w.final }

func (w *writeTx) WriteString(s string) (int, error) { return w.f.WriteString(s) }

// Commit finalizes the write.
//
// On the overwrite path the sequence is Sync, then close, then rename, and it
// is the rename that publishes the new content: every failure before it
// leaves the target byte-identical to what it held when this call started.
// Sync comes first so the rename does not publish a name pointing at data the
// kernel has not accepted yet.
//
// Any failure removes the temp file before returning, so a caller that
// reports the error and forgets to Discard still leaves nothing behind.
func (w *writeTx) Commit() error {
	if w.tmp == "" {
		// Create path: closing the file IS the commit. Its error is returned
		// rather than swallowed — a failed flush surfaces at close, and
		// reporting success for a file that did not close cleanly is a lie.
		if err := w.f.Close(); err != nil {
			return err
		}
		w.pending, w.committed = "", true
		return nil
	}

	if err := w.f.Sync(); err != nil {
		_ = w.f.Close()
		w.discard()
		return fmt.Errorf("flush the temporary file for %q: %w", w.final, err)
	}
	if err := w.f.Close(); err != nil {
		w.discard()
		return fmt.Errorf("close the temporary file for %q: %w", w.final, err)
	}
	if err := os.Rename(w.tmp, w.final); err != nil {
		w.discard()
		return fmt.Errorf("replace %q with the newly written file: %w", w.final, err)
	}
	w.pending, w.committed = "", true
	return nil
}

// Discard removes whatever this call put on disk and returns a sentence
// describing the state target_path was left in.
//
// It returns a sentence rather than an error because its only consumer is the
// message on an already-failing path: a removal that itself fails is
// information the caller needs inside that message, not a second error to
// weigh against the first. Idempotent, so Commit's own cleanup and the
// caller's can both run.
func (w *writeTx) Discard() string {
	w.discard()
	switch {
	case w.committed:
		return fmt.Sprintf("%q already holds the newly written content", w.final)
	case w.stranded != "" && w.tmp != "":
		return fmt.Sprintf(
			"the original file at %q is unchanged, but the temporary file could NOT be removed and may still be at %q: %v",
			w.final, w.stranded, w.strandedErr)
	case w.stranded != "":
		return fmt.Sprintf("the partial file could NOT be removed and may still be at %q: %v", w.stranded, w.strandedErr)
	case w.tmp != "":
		return fmt.Sprintf("the original file at %q is unchanged", w.final)
	default:
		return fmt.Sprintf("the partial file at %q was removed", w.final)
	}
}

// discard closes the handle and removes this call's in-flight bytes.
func (w *writeTx) discard() {
	if w.pending == "" {
		return
	}
	path := w.pending
	w.pending = ""
	_ = w.f.Close() // best effort: the file is about to be removed anyway
	if err := os.Remove(path); err != nil {
		w.stranded, w.strandedErr = path, err
	}
}
