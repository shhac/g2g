package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shhac/gt2gh/internal/testutil"
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
// work, and re-running with the same spec is the documented recovery.
func TestSubmitRetainsTheSpecWhenGitHubFails(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "graphite-log.txt")
	if err := os.WriteFile(logPath, []byte(graphiteLog), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.FakeCLIs(t, map[string][]testutil.Route{
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
			{Prefix: "api graphql", Output: pullRequestsJSON("")},
			{Prefix: "pr create", Stderr: "synthetic pull request creation failure", Exit: 1},
		},
	})

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
	if !strings.Contains(err.Error(), specPath) {
		t.Errorf("error does not name the retained spec: %v", err)
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
