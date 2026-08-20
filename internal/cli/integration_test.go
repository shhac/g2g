package cli_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/cli"
	"github.com/shhac/g2g/internal/testutil"
)

// These tests drive the real root command with fixture-backed gt, gh, and git
// executables on PATH. Nothing is injected: cobra parses real arguments, the
// production adapters build real argv, spawn real processes, and parse real
// bytes, and the renderer writes what a person would see.
//
// That covers the seams service-level fakes cannot reach — argument
// construction, response parsing, exit-status handling — which is exactly
// where a change to an external CLI's contract would first show up.

const graphiteLog = "◯  synthetic-main\n◯  synthetic-lower\n◯  synthetic-top (current)\n"

func pullRequestsJSON(top string) string {
	return `{"data":{"repository":{` +
		`"pr0":{"nodes":[{"number":101,"url":"https://example.test/101","headRefName":"synthetic-lower","baseRefName":"synthetic-main","state":"OPEN"}]},` +
		`"pr1":{"nodes":[` + top + `]}}}}`
}

// fakeRepository installs the three CLIs for a two-branch synthetic stack.
// topPullRequests is the raw nodes list for the tip branch, so a test can
// choose whether it already has a pull request.
func fakeRepository(t *testing.T, topPullRequests string) *testutil.Recorder {
	t.Helper()

	logPath := filepath.Join(t.TempDir(), "graphite-log.txt")
	if err := os.WriteFile(logPath, []byte(graphiteLog), 0o600); err != nil {
		t.Fatal(err)
	}

	return testutil.FakeCLIs(t, map[string][]testutil.Route{
		"git": {
			// The common directory serves two questions: whether a restack is
			// in flight, and whether this repository uses Graphite at all.
			{Prefix: "rev-parse --path-format=absolute --git-common-dir", Output: testutil.GraphiteRepository(t)},
			{Prefix: "branch --show-current", Output: "synthetic-top"},
			{Prefix: "branch --format", Lines: []string{"synthetic-main", "synthetic-lower", "synthetic-top"}},
			{Prefix: "status --porcelain"},
			{Prefix: "remote get-url", Output: "https://example.test/synthetic.git"},
			// push asks whether a branch has work its base does not, which is
			// how a branch that merged and was deleted is told from a new one.
			{Prefix: "cherry", Lines: []string{"+ 1111111111111111111111111111111111111111"}},
			{Prefix: "rev-parse --verify", Output: "1111111111111111111111111111111111111111"},
			{Prefix: "ls-remote"},
			{Prefix: "push"},
		},
		"gt": {
			{Prefix: "--version", Output: "1.8.6"},
			{Prefix: "log", File: logPath},
		},
		"gh": {
			{Prefix: "repo view", Output: `{"nameWithOwner":"example/synthetic"}`},
			{Prefix: "api graphql", Output: pullRequestsJSON(topPullRequests)},
			{Prefix: "pr create"},
			{Prefix: "stack link"},
		},
	})
}

func run(t *testing.T, args ...string) (string, string, error) {
	t.Helper()

	var stdout, stderr bytes.Buffer
	command := cli.New("v0.0.0-test", &stdout, &stderr)
	command.SetArgs(args)
	err := command.Execute()
	return stdout.String(), stderr.String(), err
}

const openTopPullRequest = `{"number":102,"url":"https://example.test/102","headRefName":"synthetic-top","baseRefName":"synthetic-lower","state":"OPEN"}`

func TestStatusReadsTheStackThroughRealAdapters(t *testing.T) {
	recorder := fakeRepository(t, openTopPullRequest)

	stdout, _, err := run(t, "status")
	if err != nil {
		t.Fatalf("status error = %v\n%s", err, stdout)
	}

	for _, want := range []string{"synthetic-main", "synthetic-lower", "#101", "synthetic-top", "#102"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("status output missing %q:\n%s", want, stdout)
		}
	}
	recorder.AssertNone("gh stack link", "gh pr create", "git push", "git checkout")
	recorder.AssertOrder("gt --version", "gt log", "gh repo view", "gh api graphql")

	// The fake answers any graphql call from its routes, so the response alone
	// proves nothing about the request. Assert the recorded argv: this is what
	// catches g2g asking GitHub the wrong question.
	query := recorder.Find("gh api graphql")
	for _, want := range []string{
		`repository(owner: "example", name: "synthetic")`,
		`pr0: pullRequests(headRefName: "synthetic-lower"`,
		`pr1: pullRequests(headRefName: "synthetic-top"`,
	} {
		if !strings.Contains(query, want) {
			t.Errorf("graphql request missing %q:\n%s", want, query)
		}
	}
	if strings.Contains(query, "search(") {
		t.Errorf("pull request lookup used the search index:\n%s", query)
	}

	// gt is read through its supported non-interactive surface only.
	if got := recorder.Find("gt log"); got != "gt log short --all --reverse --no-interactive" {
		t.Errorf("Graphite discovery = %q", got)
	}
	recorder.AssertNone("gt --debug", "gt submit", "gt restack", "gt track")
}

// The preview/apply split is a safety contract, so prove at the process
// boundary that a bare command touches nothing.
func TestPreviewsNeverInvokeAMutation(t *testing.T) {
	for _, command := range []string{"link", "push", "status"} {
		t.Run(command, func(t *testing.T) {
			recorder := fakeRepository(t, openTopPullRequest)

			if _, _, err := run(t, command); err != nil {
				t.Fatalf("%s error = %v", command, err)
			}
			recorder.AssertNone("gh stack link", "gh pr create", "gh stack unstack", "git push", "git checkout")
		})
	}
}

func TestLinkApplyRunsExactlyOneStackLinkAfterRediscovery(t *testing.T) {
	recorder := fakeRepository(t, openTopPullRequest)

	stdout, _, err := run(t, "link", "--apply")
	if err != nil {
		t.Fatalf("link --apply error = %v\n%s", err, stdout)
	}

	if got := recorder.Count("gh stack link --base synthetic-main synthetic-lower synthetic-top"); got != 1 {
		t.Errorf("stack link invocations = %d, want 1:\n%s", got, strings.Join(recorder.Calls(), "\n"))
	}
	// Discovery runs twice: once for the preview, once to revalidate.
	if got := recorder.Count("gh api graphql"); got != 2 {
		t.Errorf("graphql reads = %d, want 2 (preview and revalidation)", got)
	}
	recorder.AssertOrder("git status --porcelain", "gh api graphql", "gh stack link")
}

// submit performs the tool's most dangerous mutation and had no coverage at
// the command level at all, injected or otherwise.
func TestSubmitApplyPushesThenCreatesOnlyMissingPullRequestsThenLinks(t *testing.T) {
	recorder := fakeRepository(t, "")
	specDir := t.TempDir()

	if _, _, err := run(t, "submit", "--write-spec", specDir); err != nil {
		t.Fatalf("write-spec error = %v", err)
	}
	specPath := filepath.Join(specDir, "submission.json")
	fillSpecTitles(t, specPath)

	stdout, _, err := run(t, "submit", "--spec", specPath, "--apply")
	if err != nil {
		t.Fatalf("submit --apply error = %v\n%s", err, stdout)
	}

	// synthetic-lower already has #101, so only the tip is created, and the
	// atomic push must precede any pull-request creation.
	if got := recorder.Count("gh pr create"); got != 1 {
		t.Errorf("pr create invocations = %d, want 1:\n%s", got, strings.Join(recorder.Calls(), "\n"))
	}
	recorder.AssertOrder("git push --atomic --force-with-lease=", "gh pr create", "gh stack link")
	if !strings.Contains(stdout, "Applied") {
		t.Errorf("submit did not confirm success:\n%s", stdout)
	}
}

func TestSubmitPreviewWritesNothingAndMutatesNothing(t *testing.T) {
	recorder := fakeRepository(t, "")
	specDir := t.TempDir()

	if _, _, err := run(t, "submit", "--write-spec", specDir); err != nil {
		t.Fatal(err)
	}
	specPath := filepath.Join(specDir, "submission.json")
	fillSpecTitles(t, specPath)

	if _, _, err := run(t, "submit", "--spec", specPath); err != nil {
		t.Fatal(err)
	}
	recorder.AssertNone("git push", "gh pr create", "gh stack link")
}

// An external CLI failing must surface its own message, which is the whole
// point of routing the bounded diagnostic through the top-level printer.
func TestFailedGitHubCallReportsItsOwnOutput(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "graphite-log.txt")
	if err := os.WriteFile(logPath, []byte(graphiteLog), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.FakeCLIs(t, map[string][]testutil.Route{
		"git": {
			{Prefix: "rev-parse --path-format=absolute --git-common-dir", Output: testutil.GraphiteRepository(t)},
			{Prefix: "branch --show-current", Output: "synthetic-top"},
			{Prefix: "branch --format", Lines: []string{"synthetic-main", "synthetic-lower", "synthetic-top"}},
		},
		"gt": {
			{Prefix: "--version", Output: "1.8.6"},
			{Prefix: "log", File: logPath},
		},
		"gh": {
			{Prefix: "repo view", Stderr: "gh auth login required. To authenticate, run: gh auth login", Exit: 4},
		},
	})

	var stdout, stderr bytes.Buffer
	command := cli.New("v", &stdout, &stderr)
	command.SetArgs([]string{"status"})
	err := command.Execute()
	if err == nil {
		t.Fatal("status error = nil, want a GitHub failure")
	}

	cli.WriteErrorForTest(&stderr, err)
	if !strings.Contains(stderr.String(), "gh auth login") {
		t.Errorf("failure did not surface gh's own message:\n%s", stderr.String())
	}
}

func TestMachineOutputSurvivesTheRealPipeline(t *testing.T) {
	fakeRepository(t, openTopPullRequest)

	stdout, _, err := run(t, "status", "--json")
	if err != nil {
		t.Fatalf("status --json error = %v", err)
	}

	var doc struct {
		SchemaVersion int    `json:"schemaVersion"`
		Trunk         string `json:"trunk"`
		Branches      []struct {
			Branch      string `json:"branch"`
			PullRequest int    `json:"pullRequest"`
		} `json:"branches"`
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("decode: %v\n%s", err, stdout)
	}
	if doc.SchemaVersion != 1 || doc.Trunk != "synthetic-main" || len(doc.Branches) != 2 {
		t.Fatalf("document = %#v", doc)
	}
	if doc.Branches[1].Branch != "synthetic-top" || doc.Branches[1].PullRequest != 102 {
		t.Errorf("tip branch = %#v", doc.Branches[1])
	}
}

func fillSpecTitles(t *testing.T, path string) {
	t.Helper()

	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Version int `json:"version"`
		Draft   bool
		Pulls   []struct {
			Branch    string   `json:"branch"`
			Title     string   `json:"title"`
			Body      string   `json:"body"`
			Reviewers []string `json:"reviewers,omitempty"`
		} `json:"pulls"`
		Template string `json:"template,omitempty"`
	}
	if err := json.Unmarshal(contents, &spec); err != nil {
		t.Fatalf("decode spec: %v\n%s", err, contents)
	}
	for index := range spec.Pulls {
		spec.Pulls[index].Title = "Synthetic title for " + spec.Pulls[index].Branch
	}
	updated, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(updated, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}
