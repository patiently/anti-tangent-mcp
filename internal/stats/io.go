package stats

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

const (
	eventsFile    = "events.jsonl"
	rollupFile    = "rollup.json"
	summaryMDFile = "summary.md"
	summariesFile = "summaries.jsonl"
)

// appendJSONL appends one JSON-marshaled value as a line to dir/name.
func appendJSONL(dir, name string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, name), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err = f.Write(append(b, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// readJSONL reads a JSONL file into a slice, skipping blank and corrupt lines
// (best-effort). A missing file is not an error (returns nil).
func readJSONL[T any](dir, name string) ([]T, error) {
	b, err := os.ReadFile(filepath.Join(dir, name))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []T
	for _, line := range bytes.Split(b, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var v T
		if err := json.Unmarshal(line, &v); err != nil {
			continue
		}
		out = append(out, v)
	}
	return out, nil
}

// rewriteJSONL atomically replaces dir/name with the given items, so readers
// never see a partial write (temp file + rename on the same filesystem).
func rewriteJSONL[T any](dir, name string, items []T) error {
	var buf bytes.Buffer
	for _, it := range items {
		b, err := json.Marshal(it)
		if err != nil {
			return err
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}
	return writeFileAtomic(filepath.Join(dir, name), buf.Bytes(), 0o644)
}

func writeJSON(dir, name string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(dir, name), b, 0o644)
}

// writeFileAtomic writes b to a unique sibling temp file then renames it over
// path (atomic on the same filesystem), so readers never see a partial write.
// The temp name is unique per call (os.CreateTemp) rather than a fixed
// "<path>.tmp": ANTI_TANGENT_STATS_DIR is designed to be shared across stdio
// server processes, and a fixed temp name lets two concurrent writers to the
// same target clobber each other's temp file — publishing the wrong payload or
// failing Rename with the temp already moved. In-process callers are already
// serialized (Recorder.mu / single-flight compaction); this hardens the
// cross-process case where neither applies.
func writeFileAtomic(path string, b []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename; cleans up on error
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
