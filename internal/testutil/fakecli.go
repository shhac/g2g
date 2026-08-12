// Package testutil contains helpers shared by tests in internal packages.
package testutil

import (
	"os"
	"path/filepath"
	"testing"
)

// WithFakeExecutables places small executable scripts named after external
// CLIs at the front of PATH. It keeps tests offline and independent of local
// Graphite or GitHub CLI installations.
func WithFakeExecutables(t *testing.T, scripts map[string]string) {
	t.Helper()

	dir := t.TempDir()
	for name, body := range scripts {
		path := filepath.Join(dir, name)
		contents := "#!/bin/sh\nset -eu\n" + body
		if err := os.WriteFile(path, []byte(contents), 0o755); err != nil {
			t.Fatalf("write fake %s: %v", name, err)
		}
	}

	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}
