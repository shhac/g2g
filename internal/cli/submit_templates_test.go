package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveTemplateSelectsOneAndRejectsAmbiguityActionably(t *testing.T) {
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	if err := os.MkdirAll(filepath.Join(".github", "PULL_REQUEST_TEMPLATE"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(".github", "PULL_REQUEST_TEMPLATE", "synthetic-a.md"), []byte("A"), 0o600); err != nil {
		t.Fatal(err)
	}
	content, name, err := resolveTemplate("", false)
	if err != nil || content != "A" || name != "synthetic-a.md" {
		t.Fatalf("resolve = %q, %q, %v", content, name, err)
	}
	if err := os.WriteFile(filepath.Join(".github", "PULL_REQUEST_TEMPLATE", "synthetic-b.md"), []byte("B"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := resolveTemplate("", false); err == nil || !strings.Contains(err.Error(), "--template <name>") {
		t.Fatalf("ambiguity = %v", err)
	}
	content, name, err = resolveTemplate("synthetic-b.md", false)
	if err != nil || content != "B" || name != "synthetic-b.md" {
		t.Fatalf("explicit = %q, %q, %v", content, name, err)
	}
}
