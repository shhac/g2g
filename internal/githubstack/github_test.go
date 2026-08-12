package githubstack

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shhac/gt2gh/internal/subprocess"
	"github.com/shhac/gt2gh/internal/testutil"
)

func TestClientUsesFakeGitHubCLIOnPATH(t *testing.T) {
	arguments := filepath.Join(t.TempDir(), "gh-arguments")
	t.Setenv("GH_ARGUMENTS", arguments)
	testutil.WithFakeExecutables(t, map[string]string{
		"gh": `printf '%s\n' "$*" >> "$GH_ARGUMENTS"
if [ "$1 $2 $3" = "pr list --state" ]; then
  printf '[{"number":4,"url":"https://example.test/4","headRefName":"alpha","baseRefName":"main","state":"OPEN"}]\n'
fi`,
	})
	client := Client{Runner: subprocess.ExecRunner{}}
	prs, err := client.Inspect(context.Background(), []string{"alpha"})
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if len(prs) != 1 || prs[0].Head != "alpha" {
		t.Fatalf("Inspect() = %#v", prs)
	}
	if err := client.Link(context.Background(), "main", []string{"alpha", "beta"}); err != nil {
		t.Fatalf("Link() error = %v", err)
	}
	called, err := os.ReadFile(arguments)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(called), "pr list --state all --limit 1000 --json number,url,headRefName,baseRefName,state\nstack link --base main alpha beta\n"; got != want {
		t.Errorf("gh calls = %q, want %q", got, want)
	}
}

func TestInspectRejectsInvalidJSON(t *testing.T) {
	testutil.WithFakeExecutables(t, map[string]string{"gh": "printf 'not json\\n'"})
	_, err := (Client{Runner: subprocess.ExecRunner{}}).Inspect(context.Background(), []string{"alpha"})
	if err == nil || !strings.Contains(err.Error(), "parse gh pr list JSON") {
		t.Fatalf("Inspect() error = %v", err)
	}
}
