// Package atomicfile writes a file whole or not at all.
//
// g2g keeps its own state in small files under the Git directory, shared by
// every worktree and read by commands that may be running at the same
// moment. Each is written to a temporary file beside it, flushed to disk and
// renamed over the old one, so a reader sees the file as it was or as it is
// and never half of either. That was written out again for each new file,
// and two of the copies had already dropped the flush.
package atomicfile

import (
	"os"
	"path/filepath"
)

// Write replaces the file at path with content, creating its directory when
// it is not there. The file is readable only by its owner: what g2g keeps
// names branches and commits.
func Write(path string, content []byte) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	if _, err := temporary.Write(content); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporary.Name(), path)
}
