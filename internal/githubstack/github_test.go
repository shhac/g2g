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
if [ "$1 $2" = "repo view" ]; then printf '{"nameWithOwner":"example/fixture"}\n'; fi
if [ "$1 $2 $3" = "api graphql -f" ]; then printf '{"data":{"pr0":{"nodes":[{"number":4,"url":"https://example.test/4","headRefName":"alpha","baseRefName":"main","state":"OPEN"}]}}}\n'; fi`,
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
	if !strings.Contains(string(called), "repo view --json nameWithOwner\n") || !strings.Contains(string(called), "api graphql -f query=query { pr0: search(") || !strings.Contains(string(called), "stack link --base main alpha beta\n") {
		t.Errorf("unexpected gh calls: %q", called)
	}
}

func TestInspectRejectsInvalidJSON(t *testing.T) {
	testutil.WithFakeExecutables(t, map[string]string{"gh": "printf 'not json\\n'"})
	_, err := (Client{Runner: subprocess.ExecRunner{}}).Inspect(context.Background(), []string{"alpha"})
	if err == nil || !strings.Contains(err.Error(), "parse gh repo view JSON") {
		t.Fatalf("Inspect() error = %v", err)
	}
}

func TestParsePullRequestsRejectsGraphQLFailures(t *testing.T) {
	for _, output := range []string{
		`{"errors":[{"message":"synthetic failure"}]}`,
		`{"data":{}}`,
		`{"data":{"pr0":{"nodes":[]}}}`,
	} {
		if _, err := parsePullRequests([]byte(output), []string{"alpha", "beta"}); err == nil {
			t.Errorf("parsePullRequests(%s) error = nil", output)
		}
	}
}

func TestParsePullRequestsRejectsInvalidNode(t *testing.T) {
	output := []byte(`{"data":{"pr0":{"nodes":[{"number":0,"headRefName":"alpha","baseRefName":"main","state":"OPEN"}]}}}`)
	if _, err := parsePullRequests(output, []string{"alpha"}); err == nil {
		t.Fatal("parsePullRequests() error = nil")
	}
}

func TestBoundedMessage(t *testing.T) {
	if got := boundedMessage(strings.Repeat("x", 501)); !strings.HasSuffix(got, "…") || len(got) != 503 {
		t.Errorf("boundedMessage() = %q", got)
	}
}
