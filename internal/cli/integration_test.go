package cli_test

import (
	"bytes"
	"encoding/json"
	"fmt"
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
// graphiteRoutes is the PATH-fake repository every Graphite-backed command test
// drives: one common directory, one branch set, one gt log fixture.
//
// Only the GitHub answers differ between them, so only those are a parameter.
// It was written out three times verbatim, comments and all, and the copies had
// already drifted — the one inside TestSubmitRetainsTheSpecWhenGitHubFails was
// missing the cherry route the other two carry, so a test that grew to drive
// push there would have failed for a reason that had nothing to do with it.
//
// The common directory is returned alongside because a caller that plants a
// restack journal needs to know where it goes.
// mergeabilityPrefix is the invocation land's readiness query makes. Both
// GraphQL queries go to the same endpoint and the routes match on a prefix, so
// the operation name is what tells them apart.
// mergeabilityJSON answers for the two pull requests the shared fixture has,
// both ready to merge and neither needing a bypass.
const mergeabilityJSON = `{"data":{"repository":{"squashMergeAllowed":true,"mergeCommitAllowed":true,"rebaseMergeAllowed":true,` +
	`"pr0":{"number":101,"headRefName":"synthetic-lower","headRefOid":"1111111111111111111111111111111111111111","baseRefName":"synthetic-main","state":"OPEN","isDraft":false,"mergeable":"MERGEABLE","mergeStateStatus":"CLEAN","reviewDecision":"APPROVED","mergeCommit":null},` +
	`"pr1":{"number":102,"headRefName":"synthetic-top","headRefOid":"1111111111111111111111111111111111111111","baseRefName":"synthetic-lower","state":"OPEN","isDraft":false,"mergeable":"MERGEABLE","mergeStateStatus":"CLEAN","reviewDecision":"APPROVED","mergeCommit":null}}}}`

const mergeabilityPrefix = "api graphql -F owner={owner} -F name={repo} -f query=query Mergeability("

// The stack comment's read and its two writes share the endpoint too.
const (
	stackCommentsPrefix   = "api graphql -F owner={owner} -F name={repo} -f query=query StackComments("
	commentMutationPrefix = "api graphql -f query=mutation("
)

func graphiteRoutes(t *testing.T, gh []testutil.Route) (map[string][]testutil.Route, string) {
	t.Helper()

	logPath := filepath.Join(t.TempDir(), "graphite-log.txt")
	if err := os.WriteFile(logPath, []byte(graphiteLog), 0o600); err != nil {
		t.Fatal(err)
	}
	common := testutil.GraphiteRepository(t)
	return map[string][]testutil.Route{
		"git": {
			// The common directory serves two questions: whether a restack is
			// in flight, and whether this repository uses Graphite at all.
			{Prefix: "rev-parse --path-format=absolute --git-common-dir", Output: common},
			{Prefix: "branch --show-current", Output: "synthetic-top"},
			{Prefix: "branch --format", Lines: []string{"synthetic-main", "synthetic-lower", "synthetic-top"}},
			{Prefix: "status --porcelain"},
			{Prefix: "remote get-url", Output: "https://example.test/synthetic.git"},
			// push asks whether a branch has work its base does not, which is
			// how a branch that merged and was deleted is told from a new one.
			{Prefix: "cherry", Lines: []string{"+ 1111111111111111111111111111111111111111"}},
			// Absorbed's version gate and its tree comparison. push asks the
			// whole-branch question now, not just the per-commit one.
			{Prefix: "--version", Output: "git version 2.44.0"},
			{Prefix: "merge-tree", Output: "2222222222222222222222222222222222222222"},
			{Prefix: "rev-parse --verify", Output: "1111111111111111111111111111111111111111"},
			// Absorbed's other half: the base's own tree, which differs from
			// what merge-tree answers, so these branches stay unlanded.
			{Prefix: "rev-parse", Output: "3333333333333333333333333333333333333333"},
			{Prefix: "ls-remote"},
			{Prefix: "push"},
		},
		"gt": {
			{Prefix: "--version", Output: "1.8.6"},
			{Prefix: "log", File: logPath},
		},
		// Ahead of the caller's own: the stack comment's read begins with the
		// same words as the head-ref lookup every caller answers, and its
		// writes are submit's and land's tail.
		"gh": append(commentRoutes(), gh...),
	}, common
}

// commentRoutes answer the stack comment's read for the two pull requests the
// Graphite fixtures carry, with no comments yet, and accept its writes. The
// query names the numbers it asks about, so each answer is keyed on the first
// one: an answer whose alias carries a different number is refused.
func commentRoutes() []testutil.Route {
	conversation := func(alias string, number int, head string) string {
		return fmt.Sprintf(`"%s":{"__typename":"PullRequest","id":"PR_synthetic_%d","number":%d,"headRefName":%q,"baseRefName":"synthetic-main","state":"OPEN","viewerCanComment":true,"comments":{"pageInfo":{"hasNextPage":false,"endCursor":""},"nodes":[]}}`, alias, number, number, head)
	}
	asking := func(number int) string {
		return stackCommentsPrefix + "$owner: String!, $name: String!) { repository(owner: $owner, name: $name) { c0: issueOrPullRequest(number: " + fmt.Sprint(number) + ")"
	}
	return []testutil.Route{
		{Prefix: asking(101), Output: `{"data":{"repository":{` + conversation("c0", 101, "synthetic-lower") + `,` + conversation("c1", 102, "synthetic-top") + `}}}`},
		{Prefix: asking(102), Output: `{"data":{"repository":{` + conversation("c0", 102, "synthetic-top") + `}}}`},
		{Prefix: commentMutationPrefix, Output: `{"data":{}}`},
	}
}

func fakeRepository(t *testing.T, topPullRequests string) *testutil.Recorder {
	t.Helper()

	routes, _ := graphiteRoutes(t, []testutil.Route{
		{Prefix: "repo view", Output: `{"nameWithOwner":"example/synthetic"}`},
		// Before the head-ref lookup: first match wins, and both begin with
		// the same eight words.
		{Prefix: mergeabilityPrefix, Output: mergeabilityJSON},
		{Prefix: "api graphql", Output: pullRequestsJSON(topPullRequests)},
		{Prefix: "pr merge"},
		{Prefix: "pr edit"},
		{Prefix: "pr create"},
		{Prefix: "stack link"},
	})
	return testutil.FakeCLIs(t, routes)
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

	stdout, _, err := run(t, "github", "status")
	if err != nil {
		t.Fatalf("status error = %v\n%s", err, stdout)
	}

	for _, want := range []string{"synthetic-main", "synthetic-lower", "#101", "synthetic-top", "#102"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("status output missing %q:\n%s", want, stdout)
		}
	}
	recorder.AssertNone("gh stack link", "gh pr create", "git push", "git checkout")
	// One GitHub round trip, not two: gh fills the repository in from the
	// directory it runs in, so nothing asks which one it is first.
	recorder.AssertOrder("gt --version", "gt log", "gh api graphql")
	recorder.AssertNone("gh repo view")

	// The fake answers any graphql call from its routes, so the response alone
	// proves nothing about the request. Assert the recorded argv: this is what
	// catches g2g asking GitHub the wrong question.
	query := recorder.Find("gh api graphql")
	for _, want := range []string{
		// The repository is a variable gh fills from the directory it runs in,
		// which is what removed the round trip that used to name it.
		`-F owner={owner} -F name={repo}`,
		`repository(owner: $owner, name: $name)`,
		// Heads travel as variables, so no branch name is ever part of the
		// query text GitHub parses.
		`pr0: pullRequests(headRefName: $head0,`,
		`pr1: pullRequests(headRefName: $head1,`,
		`-f head0=synthetic-lower -f head1=synthetic-top`,
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
	for _, command := range []string{"github link", "push", "github status", "status"} {
		t.Run(command, func(t *testing.T) {
			recorder := fakeRepository(t, openTopPullRequest)

			if _, _, err := run(t, strings.Fields(command)...); err != nil {
				t.Fatalf("%s error = %v", command, err)
			}
			recorder.AssertNone("gh stack link", "gh pr create", "gh stack unstack", "git push", "git checkout")
		})
	}
}

func TestLinkApplyRunsExactlyOneStackLinkAfterRediscovery(t *testing.T) {
	recorder := fakeRepository(t, openTopPullRequest)

	stdout, _, err := run(t, "github", "link", "--apply")
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

// Once the stack is published and linked, submit keeps the stack comment on
// each pull request, after everything else, and --no-comment leaves them.
func TestSubmitKeepsTheStackCommentsUnlessToldNotTo(t *testing.T) {
	for _, test := range []struct {
		name  string
		extra []string
		want  int
	}{
		{name: "by default", want: 2},
		{name: "with --no-comment", extra: []string{"--no-comment"}, want: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := fakeRepository(t, openTopPullRequest)
			specDir := t.TempDir()
			if _, _, err := run(t, "submit", "--write-spec", specDir); err != nil {
				t.Fatal(err)
			}
			specPath := filepath.Join(specDir, "submission.json")
			fillSpecTitles(t, specPath)

			preview, _, err := run(t, append([]string{"submit", "--spec", specPath}, test.extra...)...)
			if err != nil {
				t.Fatal(err)
			}
			if said := strings.Contains(preview, "keeps the stack comment"); said != (test.want != 0) {
				t.Errorf("preview mentions the comments = %t, want %t:\n%s", said, test.want != 0, preview)
			}
			stdout, _, err := run(t, append([]string{"submit", "--spec", specPath, "--apply"}, test.extra...)...)
			if err != nil {
				t.Fatalf("submit --apply: %v\n%s", err, stdout)
			}
			if got := recorder.Count("gh " + commentMutationPrefix); got != test.want {
				t.Errorf("comment writes = %d, want %d:\n%s", got, test.want, strings.Join(recorder.Calls(), "\n"))
			}
			if test.want != 0 {
				recorder.AssertOrder("gh stack link", "gh "+stackCommentsPrefix, "gh "+commentMutationPrefix)
			}
		})
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
	routes, _ := graphiteRoutes(t, []testutil.Route{
		{Prefix: "api graphql", Stderr: "gh auth login required. To authenticate, run: gh auth login", Exit: 4},
	})
	testutil.FakeCLIs(t, routes)

	var stdout, stderr bytes.Buffer
	command := cli.New("v", &stdout, &stderr)
	command.SetArgs([]string{"github", "status"})
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

	stdout, _, err := run(t, "github", "status", "--json")
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
	if doc.SchemaVersion != cli.SchemaVersion || doc.Trunk != "synthetic-main" || len(doc.Branches) != 2 {
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
