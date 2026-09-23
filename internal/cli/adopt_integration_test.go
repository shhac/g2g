package cli_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/testutil"
)

// publishedForkPoint is what the fake merge-base answers. It is not any
// branch's tip, so a fork point recorded from a tip would not match it.
const publishedForkPoint = "4444444444444444444444444444444444444444"

// publishedStackJSON is a stack somebody else published, as GitHub answers for
// the branches fetched here, in the order they are asked about: lower, then top,
// then the trunk, which has no pull request of its own.
const publishedStackJSON = `{"data":{"repository":{"nameWithOwner":"example/synthetic",` +
	`"pr0":{"nodes":[{"number":501,"url":"https://example.test/501","headRefName":"synthetic-lower","baseRefName":"synthetic-trunk","state":"OPEN"}]},` +
	`"pr1":{"nodes":[{"number":502,"url":"https://example.test/502","headRefName":"synthetic-top","baseRefName":"synthetic-lower","state":"OPEN"}]},` +
	`"pr2":{"nodes":[]}}}}`

// publishedRepository has never used Graphite and records nothing yet. The
// branches of a colleague's stack have been fetched and are here; the only
// place their structure lives is their pull requests.
func publishedRepository(t *testing.T, local []string, pullRequests string) (*testutil.Recorder, string) {
	t.Helper()

	common := t.TempDir()
	recorder := testutil.FakeCLIs(t, map[string][]testutil.Route{
		"git": {
			{Prefix: "rev-parse --path-format=absolute --git-common-dir", Output: common},
			{Prefix: "branch --show-current", Output: "synthetic-top"},
			{Prefix: "branch --format", Lines: local},
			{Prefix: "symbolic-ref --quiet refs/remotes/origin/HEAD", Output: "refs/remotes/origin/synthetic-trunk"},
			{Prefix: "merge-base --is-ancestor"},
			{Prefix: "merge-base", Output: publishedForkPoint},
			{Prefix: "update-ref"},
		},
		// Deliberately unroutable: this mode reads GitHub and never Graphite.
		"gt": {},
		"gh": {
			{Prefix: "api graphql", Output: pullRequests},
		},
	})
	return recorder, common
}

func storedGraph(t *testing.T, common string) (map[string]map[string]string, []string) {
	t.Helper()

	stored, err := os.ReadFile(filepath.Join(common, "g2g", "graph.json"))
	if err != nil {
		t.Fatalf("graph store: %v", err)
	}
	var recorded struct {
		Trunks   []string                     `json:"trunks"`
		Branches map[string]map[string]string `json:"branches"`
	}
	if err := json.Unmarshal(stored, &recorded); err != nil {
		t.Fatal(err)
	}
	return recorded.Branches, recorded.Trunks
}

// The whole journey end to end: gh is asked for the pull requests, Git for
// where each branch forked, and the graph store is written only on --apply.
func TestAdoptFromGitHubAdoptsAColleaguesStack(t *testing.T) {
	recorder, common := publishedRepository(t, []string{"synthetic-lower", "synthetic-top", "synthetic-trunk"}, publishedStackJSON)

	stdout, stderr, err := run(t, "github", "adopt")
	if err != nil {
		t.Fatalf("github adopt: %v\n%s%s", err, stdout, stderr)
	}
	for _, want := range []string{"synthetic-lower", "synthetic-top", "g2g github status --from github"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("preview omits %q:\n%s", want, stdout)
		}
	}
	if _, statErr := os.Stat(filepath.Join(common, "g2g", "graph.json")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("a preview wrote the graph store: %v", statErr)
	}

	stdout, stderr, err = run(t, "github", "adopt", "--apply")
	if err != nil {
		t.Fatalf("github adopt --apply: %v\n%s%s", err, stdout, stderr)
	}
	branches, trunks := storedGraph(t, common)
	for branch, parent := range map[string]string{"synthetic-lower": "synthetic-trunk", "synthetic-top": "synthetic-lower"} {
		if got := branches[branch]["parent"]; got != parent {
			t.Errorf("parent of %s = %q, want %q", branch, got, parent)
		}
		if got := branches[branch]["forkPoint"]; got != publishedForkPoint {
			t.Errorf("fork point of %s = %q, want the merge base", branch, got)
		}
	}
	if strings.Join(trunks, ",") != "synthetic-trunk" {
		t.Errorf("trunks = %v, want the default branch", trunks)
	}

	// The requests, not only the result: the fake answers whatever it is asked.
	if query := recorder.Find("gh api graphql"); !strings.Contains(query, "head0=synthetic-lower") || !strings.Contains(query, "head1=synthetic-top") {
		t.Errorf("gh was asked %q, want the local branches' pull requests", query)
	}
	for _, want := range []string{"git merge-base synthetic-lower synthetic-trunk", "git merge-base synthetic-top synthetic-lower"} {
		if recorder.Find(want) == "" {
			t.Errorf("no %q among:\n%s", want, strings.Join(recorder.Calls(), "\n"))
		}
	}
	if recorder.Find("git update-ref refs/g2g/forkpoints/synthetic-top "+publishedForkPoint) == "" {
		t.Errorf("synthetic-top's fork point was not pinned:\n%s", strings.Join(recorder.Calls(), "\n"))
	}
	// Preview, then revalidation: GitHub is read again before the write.
	if count := recorder.Count("gh api graphql"); count < 3 {
		t.Errorf("gh api graphql ran %d times across a preview and an apply, want the apply to re-read", count)
	}
	recorder.AssertNone("gt ", "git switch", "git branch synthetic", "git fetch", "gh pr")
}

// A branch the pull requests place that is not here is refused by name, with
// the way to bring it here, and nothing is written or created.
func TestAdoptFromGitHubRefusesARemoteOnlyBranch(t *testing.T) {
	onlyTop := `{"data":{"repository":{"nameWithOwner":"example/synthetic",` +
		`"pr0":{"nodes":[{"number":502,"url":"https://example.test/502","headRefName":"synthetic-top","baseRefName":"synthetic-mid","state":"OPEN"}]},` +
		`"pr1":{"nodes":[]}}}}`
	recorder, common := publishedRepository(t, []string{"synthetic-top", "synthetic-trunk"}, onlyTop)

	stdout, _, err := run(t, "github", "adopt", "--apply")
	if err == nil {
		t.Fatalf("github adopt --apply: error = nil with synthetic-mid not here\n%s", stdout)
	}
	for _, want := range []string{"synthetic-mid", "git fetch && git switch synthetic-mid", "git branch synthetic-mid origin/synthetic-mid"} {
		if !strings.Contains(stdout+err.Error(), want) {
			t.Errorf("refusal omits %q:\n%s\n%v", want, stdout, err)
		}
	}
	if _, statErr := os.Stat(filepath.Join(common, "g2g", "graph.json")); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("a refused adoption wrote the graph store: %v", statErr)
	}
	recorder.AssertNone("git switch", "git branch synthetic", "git update-ref", "gt ")
}

// Choosing a branch and a scope only means something for pull requests, which
// describe many stacks; Graphite's adoption takes the whole record, so it has
// neither flag rather than one it ignores.
func TestAdoptRefusesWhatItCannotSelect(t *testing.T) {
	for _, test := range []struct {
		args []string
		want string
	}{
		{args: []string{"graphite", "adopt", "--branch", "synthetic-top"}, want: "unknown flag"},
		{args: []string{"graphite", "adopt", "--scope", "trunk"}, want: "unknown flag"},
		{args: []string{"github", "adopt", "--scope", "all"}, want: "scope"},
	} {
		t.Run(strings.Join(test.args, " "), func(t *testing.T) {
			recorder, _ := publishedRepository(t, []string{"synthetic-lower", "synthetic-top", "synthetic-trunk"}, publishedStackJSON)

			_, _, err := run(t, test.args...)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Errorf("error = %v, want it to mention %q", err, test.want)
			}
			recorder.AssertNone("gh ", "gt ")
		})
	}
}
