// Package mcpsrv: write-path resolution for code_write.
//
// resolveFileInput cannot be reused. It EvalSymlinks the FULL path, which
// fails outright when the file does not exist yet — the normal case for a
// generated file. The parent is resolved instead, and the leaf is opened with
// O_NOFOLLOW so a symlink planted at the leaf cannot redirect the write
// outside the roots that were just checked. See design §5.3.
package mcpsrv

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// resolveWriteTarget validates target and returns an open file handle ready
// to be written. The caller closes it.
//
// Deliberately does NOT create parent directories: creating a path that does
// not exist reopens the containment problem this function exists to close,
// and a worker model acting on a typo would scatter directories.
func resolveWriteTarget(target string, roots []string, overwrite bool) (*os.File, error) {
	if strings.TrimSpace(target) == "" {
		return nil, errors.New("target_path is empty")
	}
	if !filepath.IsAbs(target) {
		return nil, fmt.Errorf("target_path must be absolute, got %q", target)
	}

	parent, leaf := filepath.Dir(target), filepath.Base(target)
	if leaf == "." || leaf == string(filepath.Separator) {
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

	f, err := openWriteNoFollow(resolved, overwrite)
	if err != nil {
		if !overwrite && errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf(
				"%q already exists; pass overwrite: true to replace it", resolved)
		}
		return nil, fmt.Errorf("open %q for writing: %w", resolved, err)
	}
	return f, nil
}
