package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shhac/gt2gh/internal/testutil"
)

// g2gOwnedRepository is a repository that has never used Graphite and whose
// structure gt2gh records itself. Every route here is a Git one: if a command
// reaches for Graphite the fake will refuse, which is the point.
func g2gOwnedRepository(t *testing.T, graph string) (*testutil.Recorder, string) {
	t.Helper()

	common := t.TempDir()
	if graph != "" {
		dir := filepath.Join(common, "g2g")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "graph.json"), []byte(graph), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	recorder := testutil.FakeCLIs(t, map[string][]testutil.Route{
		"git": {
			{Prefix: "rev-parse --path-format=absolute --git-common-dir", Output: common},
			{Prefix: "branch --show-current", Output: "synthetic-top"},
			{Prefix: "branch --format", Lines: []string{"synthetic-lower", "synthetic-top", "synthetic-trunk"}},
			{Prefix: "rev-parse --verify", Output: "1111111111111111111111111111111111111111"},
			{Prefix: "merge-base --is-ancestor"},
			{Prefix: "remote get-url", Output: "https://example.test/synthetic.git"},
			{Prefix: "ls-remote"},
			{Prefix: "push"},
		},
		// Deliberately unroutable: reaching Graphite at all is the failure.
		"gt": {},
		"gh": {
			{Prefix: "repo view", Output: `{"nameWithOwner":"example/synthetic"}`},
			{Prefix: "api graphql", Output: `{"data":{"repository":{"pr0":{"nodes":[]},"pr1":{"nodes":[]}}}}`},
		},
	})
	return recorder, common
}

const ownedGraph = `{"storeSchemaVersion":1,"trunks":["synthetic-trunk"],"branches":{
	"synthetic-lower":{"parent":"synthetic-trunk","origin":"user"},
	"synthetic-top":{"parent":"synthetic-lower","origin":"user"}}}`

// The whole point of resolution: a command that used to require Graphite now
// works from the structure gt2gh records itself.
func TestPushSelectsFromTheG2GOwnedGraph(t *testing.T) {
	recorder, _ := g2gOwnedRepository(t, ownedGraph)

	stdout, _, err := run(t, "push", "--apply")
	if err != nil {
		t.Fatalf("push --apply: %v\n%s", err, stdout)
	}

	push := recorder.Find("git push --atomic")
	for _, branch := range []string{"synthetic-lower", "synthetic-top"} {
		if !strings.Contains(push, branch) {
			t.Errorf("push %q is missing %s", push, branch)
		}
	}
	if strings.Contains(push, "synthetic-trunk") {
		t.Errorf("push %q includes the base", push)
	}
	recorder.AssertNone("gt ")
}

// Asking whether Graphite describes a branch must not be what enrols a
// repository into Graphite. Its discovery command creates state, so the
// question is answered from the repository instead.
func TestNoGraphiteCommandRunsInARepositoryThatDoesNotUseIt(t *testing.T) {
	recorder, common := g2gOwnedRepository(t, ownedGraph)

	if _, _, err := run(t, "push"); err != nil {
		t.Fatalf("push: %v", err)
	}

	recorder.AssertNone("gt ")
	entries, err := os.ReadDir(common)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(strings.ToLower(entry.Name()), "graphite") {
			t.Errorf("running a gt2gh command left Graphite state behind: %s", entry.Name())
		}
	}
}

// A branch nothing describes gets a refusal that names what to do, not a
// Graphite error from a repository that does not use Graphite.
func TestAnUndescribedBranchIsRefusedWithARemedy(t *testing.T) {
	recorder, _ := g2gOwnedRepository(t, "")

	stdout, _, err := run(t, "push")
	if err == nil {
		t.Fatalf("push: error = nil for a branch no source describes\n%s", stdout)
	}
	if !strings.Contains(err.Error(), "no source describes") {
		t.Errorf("error = %v, want it to say nothing describes the branch", err)
	}
	if !strings.Contains(err.Error(), "g2g track") {
		t.Errorf("error = %v, want it to name the remedy", err)
	}
	recorder.AssertNone("gt ")
}

// link projects whatever structure was resolved, so it works on a g2g-owned
// stack for the same reason push does.
func TestLinkSelectsFromTheG2GOwnedGraph(t *testing.T) {
	recorder, _ := g2gOwnedRepository(t, ownedGraph)

	stdout, _, err := run(t, "link")
	if err != nil {
		t.Fatalf("link: %v\n%s", err, stdout)
	}

	for _, branch := range []string{"synthetic-lower", "synthetic-top"} {
		if !strings.Contains(stdout, branch) {
			t.Errorf("preview omits %s:\n%s", branch, stdout)
		}
	}
	recorder.AssertNone("gt ")
}
