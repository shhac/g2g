package submit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSpecRoundTripPreservesComplexMarkdown(t *testing.T) {
	dir := t.TempDir()
	markdown := "## Heading\n\n- [ ] item\n\n```go\nfmt.Println(\"synthetic\")\n```\n<!-- keep -->\n"
	path, err := Write(dir, NewSpec([]string{"synthetic/one", "synthetic/two"}, markdown))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "synthetic/one.md") {
		t.Fatal("branch name leaked into a file name")
	}
	spec, err := Read(path, []string{"synthetic/one", "synthetic/two"})
	if err == nil {
		t.Fatal("Read() = nil, want missing-title error")
	}
	spec = NewSpec([]string{"synthetic/one", "synthetic/two"}, markdown)
	spec.Pulls[0].Title, spec.Pulls[1].Title = "Synthetic one", "Synthetic two"
	path, err = Write(filepath.Join(dir, "private"), spec)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Read(path, []string{"synthetic/one", "synthetic/two"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Pulls[0].Body != markdown || !got.Draft {
		t.Fatalf("spec = %#v", got)
	}
}

func TestReadExplainsOrderingMismatch(t *testing.T) {
	path, err := Write(t.TempDir(), Spec{Version: 1, Draft: true, Pulls: []Pull{{Branch: "synthetic/two", Title: "x"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path, []string{"synthetic/one"}); err == nil || !strings.Contains(err.Error(), "want") {
		t.Fatalf("Read() error = %v", err)
	}
}
