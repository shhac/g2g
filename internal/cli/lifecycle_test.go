package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shhac/gt2gh/internal/testutil"
)

// The preview/apply sequence is this tool's core safety contract: discover,
// re-discover, and only then mutate. It is currently guaranteed by each
// command implementing it separately, which is exactly the shape that drifts.
// These tests assert the contract at the process boundary, per command, so any
// consolidation of that flow has something to hold it in place.

const stackedPullRequests = `{"number":102,"url":"https://example.test/102","headRefName":"synthetic-top","baseRefName":"synthetic-lower","state":"OPEN","stack":{"number":42,"size":2},"stackEntry":{"position":2}}`

func lifecycleRepository(t *testing.T, topPullRequest string) *testutil.Recorder {
	t.Helper()

	logPath := filepath.Join(t.TempDir(), "graphite-log.txt")
	if err := os.WriteFile(logPath, []byte(graphiteLog), 0o600); err != nil {
		t.Fatal(err)
	}
	lower := `{"number":101,"url":"https://example.test/101","headRefName":"synthetic-lower","baseRefName":"synthetic-main","state":"OPEN","stack":{"number":42,"size":2},"stackEntry":{"position":1}}`
	return testutil.FakeCLIs(t, map[string][]testutil.Route{
		"git": {
			// The restack guard looks for an in-flight journal, which needs
			// the common directory. An empty one means nothing is in flight.
			{Prefix: "rev-parse --path-format=absolute --git-common-dir", Output: t.TempDir()},
			{Prefix: "branch --show-current", Output: "synthetic-top"},
			{Prefix: "branch --format", Lines: []string{"synthetic-main", "synthetic-lower", "synthetic-top"}},
			{Prefix: "status --porcelain"},
			{Prefix: "remote get-url", Output: "https://example.test/synthetic.git"},
			{Prefix: "ls-remote"},
			{Prefix: "push"},
		},
		"gt": {
			{Prefix: "--version", Output: "1.8.6"},
			{Prefix: "log", File: logPath},
		},
		"gh": {
			{Prefix: "repo view", Output: `{"nameWithOwner":"example/synthetic"}`},
			{Prefix: "api graphql", Output: `{"data":{"repository":{"pr0":{"nodes":[` + lower + `]},"pr1":{"nodes":[` + topPullRequest + `]}}}}`},
			{Prefix: "pr create"},
			{Prefix: "stack link"},
			{Prefix: "stack unstack"},
		},
	})
}

// Every mutating command must discover twice — once for the preview it renders
// and once to revalidate — before it touches anything.
func TestEveryApplyRediscoversBeforeMutating(t *testing.T) {
	for _, test := range []struct {
		name     string
		args     []string
		mutation string
		spec     bool
	}{
		{name: "link", args: []string{"link", "--apply"}, mutation: "gh stack link"},
		{name: "sync", args: []string{"sync", "--apply"}, mutation: "gh stack link"},
		{name: "push", args: []string{"push", "--apply"}, mutation: "git push --atomic"},
		{name: "unlink", args: []string{"unlink", "--apply"}, mutation: "gh stack unstack"},
		{name: "submit", args: []string{"submit", "--apply"}, mutation: "git push --atomic", spec: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := lifecycleRepository(t, stackedPullRequests)

			args := test.args
			if test.spec {
				specDir := t.TempDir()
				if _, _, err := run(t, "submit", "--write-spec", specDir); err != nil {
					t.Fatal(err)
				}
				specPath := filepath.Join(specDir, "submission.json")
				fillSpecTitles(t, specPath)
				args = []string{"submit", "--spec", specPath, "--apply"}
			}

			if _, _, err := run(t, args...); err != nil {
				t.Fatalf("%s error = %v", test.name, err)
			}

			// Graphite is read once per discovery pass, so two reads before the
			// mutation is what proves revalidation actually happened.
			calls := recorder.Calls()
			mutationAt := indexOfPrefix(t, calls, test.mutation)
			discoveries := 0
			for _, call := range calls[:mutationAt] {
				if strings.HasPrefix(call, "gt log") {
					discoveries++
				}
			}
			if discoveries < 2 {
				t.Errorf("%s discovered %d times before mutating, want 2 (preview and revalidation):\n%s",
					test.name, discoveries, strings.Join(calls, "\n"))
			}
		})
	}
}

// A mutation must happen exactly once. Twice would mean a revalidation path
// that re-executes; zero would mean a command reporting success having done
// nothing.
func TestEveryApplyMutatesExactlyOnce(t *testing.T) {
	for _, test := range []struct {
		name     string
		args     []string
		mutation string
	}{
		{name: "link", args: []string{"link", "--apply"}, mutation: "gh stack link --base synthetic-main synthetic-lower synthetic-top"},
		{name: "sync", args: []string{"sync", "--apply"}, mutation: "gh stack link --base synthetic-main synthetic-lower synthetic-top"},
		{name: "push", args: []string{"push", "--apply"}, mutation: "git push --atomic --force-with-lease="},
		{name: "unlink", args: []string{"unlink", "--apply"}, mutation: "gh stack unstack 42"},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := lifecycleRepository(t, stackedPullRequests)
			if _, _, err := run(t, test.args...); err != nil {
				t.Fatalf("%s error = %v", test.name, err)
			}
			if got := recorder.Count(test.mutation); got != 1 {
				t.Errorf("%s ran %q %d times, want 1:\n%s", test.name, test.mutation, got, strings.Join(recorder.Calls(), "\n"))
			}
		})
	}
}

// The commands that mutate committed state require a clean worktree, and must
// check it before doing so. push deliberately does not, because uncommitted
// files cannot change which refs advance.
func TestWorktreeIsCheckedBeforeCommittedStateChanges(t *testing.T) {
	for _, test := range []struct {
		name     string
		args     []string
		mutation string
	}{
		{name: "link", args: []string{"link", "--apply"}, mutation: "gh stack link"},
		{name: "sync", args: []string{"sync", "--apply"}, mutation: "gh stack link"},
		{name: "unlink", args: []string{"unlink", "--apply"}, mutation: "gh stack unstack"},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := lifecycleRepository(t, stackedPullRequests)
			if _, _, err := run(t, test.args...); err != nil {
				t.Fatalf("%s error = %v", test.name, err)
			}
			recorder.AssertOrder("git status --porcelain", test.mutation)
		})
	}
}

// A blocked or refused apply must reach no mutation at all.
func TestBlockedApplyNeverMutates(t *testing.T) {
	recorder := lifecycleRepository(t, "")

	if _, _, err := run(t, "link", "--apply"); err == nil {
		t.Fatal("link --apply on an unmapped path = nil, want a refusal")
	}
	recorder.AssertNone("gh stack link", "gh pr create", "git push")
}

func indexOfPrefix(t *testing.T, calls []string, prefix string) int {
	t.Helper()
	for index, call := range calls {
		if strings.HasPrefix(call, prefix) {
			return index
		}
	}
	t.Fatalf("no %q in:\n%s", prefix, strings.Join(calls, "\n"))
	return -1
}
