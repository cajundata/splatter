// Package fsio provides splatter's two write primitives. Evidence files
// (append-only JSONL) go through AppendRecord; derived, regenerable files
// (sheets, exports) go through ReplaceFile. Nothing else writes files.
package fsio

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// AppendRecord marshals v to a single JSON line and appends it to path
// with one Write call followed by fsync. O_APPEND guarantees no partial
// interleaved lines; marshal errors happen before the file is touched.
func AppendRecord(path string, v any) error {
	line, err := json.Marshal(v)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return err
	}
	return f.Sync()
}

// ReplaceFile atomically replaces path with data: write temp in the same
// directory, fsync, remove any existing destination, rename. The
// unconditional remove-before-rename works identically on darwin and
// windows, so no GOOS branch is needed.
func ReplaceFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { tmp.Close(); os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}
