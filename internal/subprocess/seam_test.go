package subprocess

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// This package is the only place a process is started, which is what lets a
// test put fakes on PATH and a diagnostic record every run. The rule had no
// check behind it, and an editor launch had quietly become the exception.
func TestNothingElseStartsAProcess(t *testing.T) {
	root := filepath.Join("..", "..")
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && (entry.Name() == ".git" || entry.Name() == ".claude" || strings.HasPrefix(entry.Name(), ".")) && path != root {
			return filepath.SkipDir
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		slashed := filepath.ToSlash(path)
		if strings.Contains(slashed, "internal/subprocess/") || strings.Contains(slashed, "internal/testutil/") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, spec := range file.Imports {
			if imported, _ := strconv.Unquote(spec.Path.Value); imported == "os/exec" {
				t.Errorf("%s imports os/exec; start processes through internal/subprocess", slashed)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
