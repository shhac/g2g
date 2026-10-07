package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/cli"
	"github.com/shhac/g2g/internal/testutil"
)

// The README promises the submission spec survives every failure — validation,
// editor, interruption, GitHub. That guarantee had no coverage at all, so a
// regression would have silently destroyed a user's hand-written titles.

func TestSubmitPreviewWithoutSpecExplainsHowToMakeOne(t *testing.T) {
	fakeRepository(t, "")

	stdout, _, err := run(t, "submit")
	if err != nil {
		t.Fatalf("submit error = %v", err)
	}
	for _, want := range []string{"No changes were made.", "--write-spec"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("preview missing %q:\n%s", want, stdout)
		}
	}
}

func TestSubmitApplyWithoutSpecRefusesWhenPullRequestsAreMissing(t *testing.T) {
	recorder := fakeRepository(t, "")
	stdout, _, err := run(t, "submit", "--apply")
	if err == nil || !strings.Contains(err.Error(), "--write-spec") {
		t.Fatalf("missing spec did not fail with a repair: %v\n%s", err, stdout)
	}
	recorder.AssertNone("git push", "gh pr create")
}

func TestSubmitApplyWithoutSpecPreservesAmbiguousPRRefusal(t *testing.T) {
	recorder := fakeRepository(t, openTopPullRequest+","+`{"number":103,"headRefName":"synthetic-top","baseRefName":"synthetic-lower","state":"OPEN"}`)
	stdout, _, err := run(t, "submit", "--apply")
	if err == nil || !strings.Contains(stdout+err.Error(), "2 open pull requests") {
		t.Fatalf("ambiguous PRs were reported as needing a spec: %v\n%s", err, stdout)
	}
	recorder.AssertNone("git push", "gh pr create")
}

func TestSubmitRejectsAnIncompleteSpecWithRepairSteps(t *testing.T) {
	fakeRepository(t, "")
	specDir := t.TempDir()
	if _, _, err := run(t, "submit", "--write-spec", specDir); err != nil {
		t.Fatal(err)
	}
	specPath := filepath.Join(specDir, "submission.json")

	// A freshly written spec has no titles, which is exactly what must be
	// reported as repairable rather than applied.
	_, _, err := run(t, "submit", "--spec", specPath)
	if err == nil {
		t.Fatal("submit --spec with no titles = nil, want a validation error")
	}
	for _, want := range []string{"missing title", "Next steps", specPath} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q:\n%v", want, err)
		}
	}
	if _, statErr := os.Stat(specPath); statErr != nil {
		t.Errorf("validation failure destroyed the spec: %v", statErr)
	}
}

func TestSubmitEditWithoutEditorExplainsTheAlternative(t *testing.T) {
	fakeRepository(t, "")
	t.Setenv("EDITOR", "")

	_, _, err := run(t, "submit", "--edit")
	if err == nil {
		t.Fatal("submit --edit without EDITOR = nil, want an error")
	}
	if !strings.Contains(err.Error(), "--write-spec") {
		t.Errorf("error does not point at the alternative: %v", err)
	}
}

// A failing editor must leave the document behind and say where it is.
func TestSubmitEditRetainsTheSpecWhenTheEditorFails(t *testing.T) {
	fakeRepository(t, "")
	t.Setenv("EDITOR", "false")

	_, _, err := run(t, "submit", "--edit")
	if err == nil {
		t.Fatal("submit --edit with a failing editor = nil, want an error")
	}
	if !strings.Contains(err.Error(), "submission spec retained at") {
		t.Fatalf("error does not report the retained spec: %v", err)
	}
	path := retainedPath(t, err.Error())
	if _, statErr := os.Stat(path); statErr != nil {
		t.Errorf("editor failure destroyed the spec at %s: %v", path, statErr)
	}
}

// A GitHub failure mid-apply must also retain it: the titles are the user's
// work, and re-running with the same spec is the documented recovery. The push
// has happened by then, so it is a stop part-way, and the retry it names is
// the one carrying the spec.
func TestSubmitRetainsTheSpecWhenGitHubFails(t *testing.T) {
	routes, _ := graphiteRoutes(t, []testutil.Route{
		{Prefix: "repo view", Output: `{"nameWithOwner":"example/synthetic"}`},
		{Prefix: "api graphql", Output: pullRequestsJSON("")},
		{Prefix: "pr create", Stderr: "synthetic pull request creation failure", Exit: 1},
	})
	testutil.FakeCLIs(t, routes)

	specDir := t.TempDir()
	if _, _, err := run(t, "submit", "--write-spec", specDir); err != nil {
		t.Fatal(err)
	}
	specPath := filepath.Join(specDir, "submission.json")
	fillSpecTitles(t, specPath)

	stdout, _, err := run(t, "submit", "--spec", specPath, "--apply")
	if err == nil {
		t.Fatal("submit --apply = nil, want the GitHub failure")
	}
	if !cli.StoppedPartWayForTest(err) {
		t.Errorf("error = %v, want the part-way status", err)
	}
	for _, want := range []string{
		"Stopped part-way: opening the pull request for synthetic-top",
		"Published the stack to origin.",
		"--spec " + specPath + " --apply to finish",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("report missing %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "Not applied") {
		t.Errorf("a submission that pushed says nothing was applied:\n%s", stdout)
	}
	if _, statErr := os.Stat(specPath); statErr != nil {
		t.Errorf("apply failure destroyed the spec: %v", statErr)
	}
	if strings.Contains(stdout, "Applied") {
		t.Errorf("failed apply claimed success:\n%s", stdout)
	}
}

func retainedPath(t *testing.T, message string) string {
	t.Helper()
	const marker = "submission spec retained at "
	index := strings.Index(message, marker)
	if index < 0 {
		t.Fatalf("no retained path in %q", message)
	}
	rest := message[index+len(marker):]
	if end := strings.IndexAny(rest, ": \n"); end >= 0 {
		return rest[:end]
	}
	return rest
}

// A flag that would be ignored is refused before anything is read, so a preview
// never names a template the spec does not use.
func TestSubmitRefusesFlagsItWouldIgnore(t *testing.T) {
	for _, args := range [][]string{
		{"submit", "--spec", "synthetic-spec.json", "--template", "synthetic"},
		{"submit", "--spec", "synthetic-spec.json", "--no-template"},
		{"submit", "--keep-spec"},
	} {
		recorder := fakeRepository(t, openTopPullRequest)
		if _, _, err := run(t, args...); err == nil {
			t.Errorf("%v was accepted", args)
		}
		recorder.AssertNone("gh ", "git push")
	}
}

// A pull request opened and then linking failed: both the push and the new
// pull request stand on GitHub, so the report names what was opened, the
// retry keeps --link, and the run exits part-way rather than "Not applied".
func TestSubmitThatStopsAtLinkNamesTheOpenedPullRequest(t *testing.T) {
	routes, _ := graphiteRoutes(t, []testutil.Route{
		{Prefix: "repo view", Output: `{"nameWithOwner":"example/synthetic"}`},
		{Prefix: "api graphql", Output: pullRequestsJSON("")},
		{Prefix: "pr create", Output: "https://example.test/synthetic/pull/102"},
		{Prefix: "stack link", Stderr: "synthetic link refusal", Exit: 1},
	})
	recorder := testutil.FakeCLIs(t, routes)

	specDir := t.TempDir()
	if _, _, err := run(t, "submit", "--write-spec", specDir); err != nil {
		t.Fatal(err)
	}
	specPath := filepath.Join(specDir, "submission.json")
	fillSpecTitles(t, specPath)

	stdout, _, err := run(t, "submit", "--spec", specPath, "--link", "--apply")
	if !cli.StoppedPartWayForTest(err) {
		t.Fatalf("error = %v, want the part-way status\n%s", err, stdout)
	}
	for _, want := range []string{"Stopped part-way: linking the stack", "Opened a pull request for synthetic-top.", "--link"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("report missing %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "Not applied") {
		t.Errorf("a submission that opened a pull request says nothing was applied:\n%s", stdout)
	}
	if got := recorder.Count("gh pr create"); got != 1 {
		t.Errorf("gh pr create ran %d times, want once for synthetic-top", got)
	}
}
