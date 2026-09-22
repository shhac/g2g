package cli_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/testutil"
)

// The preview/apply sequence is this tool's core safety contract: discover,
// re-discover, and only then mutate. It is currently guaranteed by each
// command implementing it separately, which is exactly the shape that drifts.
// These tests assert the contract at the process boundary, per command, so any
// consolidation of that flow has something to hold it in place.

const stackedPullRequests = `{"number":102,"url":"https://example.test/102","headRefName":"synthetic-top","baseRefName":"synthetic-lower","state":"OPEN","stack":{"number":42,"size":2},"stackEntry":{"position":2}}`

func lifecycleRepository(t *testing.T, topPullRequest string) *testutil.Recorder {
	t.Helper()
	recorder, _ := lifecycleRepositoryIn(t, topPullRequest)
	return recorder
}

// lifecycleRepositoryIn also returns the Git common directory, which is where
// an interrupted restack leaves its journal. A test that needs to stand one up
// needs to know where it goes.
func lifecycleRepositoryIn(t *testing.T, topPullRequest string) (*testutil.Recorder, string) {
	t.Helper()

	lower := `{"number":101,"url":"https://example.test/101","headRefName":"synthetic-lower","baseRefName":"synthetic-main","state":"OPEN","stack":{"number":42,"size":2},"stackEntry":{"position":1}}`
	routes, common := graphiteRoutes(t, []testutil.Route{
		{Prefix: "repo view", Output: `{"nameWithOwner":"example/synthetic"}`},
		{Prefix: "api graphql", Output: `{"data":{"repository":{"pr0":{"nodes":[` + lower + `]},"pr1":{"nodes":[` + topPullRequest + `]}}}}`},
		{Prefix: "pr create"},
		{Prefix: "stack link"},
		{Prefix: "stack unstack"},
	})
	return testutil.FakeCLIs(t, routes), common
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

// An unfinished restack holds the only record of where the branches were, and
// its journal is overwritten rather than merged. A second mutation while it
// exists therefore does not merely interleave — it destroys the tips
// --abort would have restored.
//
// This is a table over every mutating command rather than a spot check on one,
// because the failure it exists to catch is a command registered without the
// guard. Checking one command cannot see that; sync was wired without it and
// nothing noticed.
func TestNoMutatingCommandProceedsDuringAnInterruptedRestack(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		// mutation is the recorded call the command would make, led by its
		// tool the way the recorder writes it. A prefix without the tool
		// matches nothing, so the assertion it guards passes whatever runs.
		mutation string
		// store marks a command whose mutation is the graph store, which is a
		// file write rather than a process, so no recorded call can show it.
		store bool
		// spec means the command needs a submission spec before --apply does
		// anything at all; without one it only previews, so a row that omits
		// it would assert nothing.
		spec bool
	}{
		{name: "link", args: []string{"link", "--apply"}, mutation: "gh stack link"},
		{name: "unlink", args: []string{"unlink", "--apply"}, mutation: "gh stack unstack"},
		{name: "push", args: []string{"push", "--apply"}, mutation: "git push"},
		{name: "track", args: []string{"track", "--branch", "synthetic-top", "--parent", "synthetic-lower", "--apply"}, store: true},
		{name: "untrack", args: []string{"untrack", "--branch", "synthetic-top", "--apply"}, store: true},
		{name: "retarget", args: []string{"retarget", "--apply"}, mutation: "gh pr edit"},
		{name: "mirror", args: []string{"mirror", "--apply"}, mutation: "gt track"},
		{name: "import", args: []string{"import", "--apply"}, store: true},
		{name: "sync", args: []string{"sync", "--apply"}, mutation: "git fetch"},
		{name: "submit", args: []string{"submit", "--apply"}, mutation: "git push", spec: true},
		{name: "prune", args: []string{"prune", "--apply"}, store: true},
		{name: "land", args: []string{"land", "--apply"}, mutation: "gh pr merge"},
		{name: "comment", args: []string{"comment", "--apply"}, mutation: "gh " + commentMutationPrefix},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder, common := lifecycleRepositoryIn(t, stackedPullRequests)

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
			plantRestackJournal(t, common)

			_, _, err := run(t, args...)
			if err == nil {
				t.Fatalf("%s --apply was allowed during an interrupted restack", test.name)
			}
			if !strings.Contains(err.Error(), "restack") {
				t.Errorf("refusal does not mention the restack that caused it: %v", err)
			}
			if test.mutation != "" {
				recorder.AssertNone(test.mutation)
			}
			if _, statErr := os.Stat(filepath.Join(common, "g2g", "graph.json")); !errors.Is(statErr, os.ErrNotExist) {
				t.Errorf("%s --apply wrote the graph store during an interrupted restack: %v", test.name, statErr)
			}
		})
	}
}

// plantRestackJournal leaves the record an interrupted restack would have left.
// Its exact contents do not matter to the guard, which refuses on the file
// existing at all — that is the point, since a half-written record is exactly
// the state worth refusing on.
func plantRestackJournal(t *testing.T, common string) {
	t.Helper()

	dir := filepath.Join(common, "g2g")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	journal := `{"schemaVersion":1,"branch":"synthetic-top","scope":"path","original":{"synthetic-top":"0000000000000000000000000000000000000000"}}`
	if err := os.WriteFile(filepath.Join(dir, "restack.json"), []byte(journal), 0o600); err != nil {
		t.Fatal(err)
	}
}
