package cli_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shhac/gt2gh/internal/cli"
	"github.com/shhac/gt2gh/internal/testutil"
)

// g2gOwnedRepository is a repository that has never used Graphite and whose
// structure gt2gh records itself. Every route here is a Git one: if a command
// reaches for Graphite the fake will refuse, which is the point.
func g2gOwnedRepository(t *testing.T, graph string) (*testutil.Recorder, string) {
	t.Helper()
	return g2gOwnedRepositoryWithPullRequests(t, graph, ownedPullRequests)
}

func g2gOwnedRepositoryWithPullRequests(t *testing.T, graph, pullRequests string) (*testutil.Recorder, string) {
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
			{Prefix: "branch --format", Lines: []string{"synthetic-lower", "synthetic-other", "synthetic-side", "synthetic-top", "synthetic-trunk"}},
			{Prefix: "rev-parse --verify", Output: "1111111111111111111111111111111111111111"},
			{Prefix: "merge-base --is-ancestor"},
			{Prefix: "status --porcelain"},
			{Prefix: "remote get-url", Output: "https://example.test/synthetic.git"},
			{Prefix: "ls-remote"},
			{Prefix: "push"},
		},
		// Deliberately unroutable: reaching Graphite at all is the failure.
		"gt": {},
		"gh": {
			{Prefix: "repo view", Output: `{"nameWithOwner":"example/synthetic"}`},
			{Prefix: "api graphql", Output: pullRequests},
			{Prefix: "pr create"},
			{Prefix: "stack link"},
			{Prefix: "stack unstack"},
		},
	})
	return recorder, common
}

// The recorded forest has two components, so a test can tell which branch a
// command actually resolved from: the second root is reachable only by asking
// for it, never by defaulting to the checked-out branch.
const ownedGraph = `{"storeSchemaVersion":1,"trunks":["synthetic-trunk","synthetic-other"],"branches":{
	"synthetic-lower":{"parent":"synthetic-trunk","origin":"user"},
	"synthetic-top":{"parent":"synthetic-lower","origin":"user"},
	"synthetic-side":{"parent":"synthetic-other","origin":"user"}}}`

func ownedPullRequestsJSON(membership string) string {
	return `{"data":{"repository":{` +
		`"pr0":{"nodes":[{"number":201,"url":"https://example.test/201","headRefName":"synthetic-lower","baseRefName":"synthetic-trunk","state":"OPEN"` + strings.Replace(membership, "POSITION", "1", 1) + `}]},` +
		`"pr1":{"nodes":[{"number":202,"url":"https://example.test/202","headRefName":"synthetic-top","baseRefName":"synthetic-lower","state":"OPEN"` + strings.Replace(membership, "POSITION", "2", 1) + `}]}}}}`
}

// The default stack is open but not yet projected onto GitHub, which is what
// link has work to do about; the linked variant is what unlink needs.
var (
	ownedPullRequests       = ownedPullRequestsJSON("")
	ownedLinkedPullRequests = ownedPullRequestsJSON(`,"stack":{"number":42,"size":2},"stackEntry":{"position":POSITION}`)
)

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

// Completing a flag must work from the structure gt2gh records itself. It also
// must not be what enrols a repository into Graphite: completion used to run
// Graphite's discovery command, so pressing tab in a repository that had never
// used Graphite created Graphite state in it — and then failed anyway.
func TestFlagCompletionNeedsNoGraphite(t *testing.T) {
	for _, test := range []struct {
		flag string
		want []string
	}{
		{flag: "--branch", want: []string{"synthetic-lower", "synthetic-top"}},
		{flag: "--trunk", want: []string{"synthetic-trunk"}},
	} {
		t.Run(test.flag, func(t *testing.T) {
			recorder, _ := g2gOwnedRepository(t, ownedGraph)

			stdout, _, err := run(t, "__complete", "push", test.flag, "")
			if err != nil {
				t.Fatalf("__complete push %s: %v\n%s", test.flag, err, stdout)
			}
			if strings.Contains(stdout, "ShellCompDirectiveError") {
				t.Errorf("completing %s failed:\n%s", test.flag, stdout)
			}
			for _, branch := range test.want {
				if !strings.Contains(stdout, branch) {
					t.Errorf("completing %s omits %s:\n%s", test.flag, branch, stdout)
				}
			}
			recorder.AssertNone("gt ")
		})
	}
}

// graphiteRepository is a repository that does use Graphite, so the gate that
// keeps Graphite out of the previous test must let it back in here.
func graphiteRepository(t *testing.T) *testutil.Recorder {
	t.Helper()

	return testutil.FakeCLIs(t, map[string][]testutil.Route{
		"git": {
			{Prefix: "rev-parse --path-format=absolute --git-common-dir", Output: testutil.GraphiteRepository(t)},
			{Prefix: "branch --show-current", Output: "synthetic-top"},
			{Prefix: "branch --format", Lines: []string{"synthetic-lower", "synthetic-top", "synthetic-trunk"}},
		},
		"gt": {
			{Prefix: "--version", Output: "1.8.6"},
			{Prefix: "log", Lines: []string{"◯  synthetic-trunk", "◯  synthetic-lower", "◉  synthetic-top"}},
		},
	})
}

// The gate is not a way of switching Graphite off: where Graphite is in use,
// completion still offers what it declares.
func TestFlagCompletionStillReadsGraphiteWhereItIsUsed(t *testing.T) {
	recorder := graphiteRepository(t)

	stdout, _, err := run(t, "__complete", "push", "--branch", "")
	if err != nil {
		t.Fatalf("__complete: %v\n%s", err, stdout)
	}
	for _, branch := range []string{"synthetic-lower", "synthetic-top"} {
		if !strings.Contains(stdout, branch) {
			t.Errorf("completion omits Graphite-tracked %s:\n%s", branch, stdout)
		}
	}
	if recorder.Find("gt log") == "" {
		t.Error("Graphite was never consulted in a repository that uses it")
	}
}

// stackCommands are the commands that take --branch and --trunk. They share
// one registration helper, but each wires it itself, so each is checked: a
// command that forgot to pass its completions would otherwise fail silently,
// which is the failure mode nobody notices until they press tab.
// pullRequests is what that command needs to have work to do: unlink is the
// only one whose subject is a stack that is already projected onto GitHub.
var stackCommands = []struct {
	name         string
	pullRequests string
}{
	{name: "link", pullRequests: ownedPullRequests},
	{name: "status", pullRequests: ownedPullRequests},
	{name: "unlink", pullRequests: ownedLinkedPullRequests},
	{name: "push", pullRequests: ownedPullRequests},
	{name: "submit", pullRequests: ownedPullRequests},
}

// The list above is hand-maintained, because each command needs its own
// fixture. This is what stops it going stale: a new command that takes these
// flags has to be added there rather than quietly going unchecked.
func TestEveryCommandTakingTheStackFlagsIsCovered(t *testing.T) {
	var stdout, stderr bytes.Buffer
	covered := map[string]bool{}
	for _, command := range stackCommands {
		covered[command.name] = true
	}

	for _, command := range cli.New("v0.0.0-test", &stdout, &stderr).Commands() {
		if command.Flags().Lookup("branch") == nil || command.Flags().Lookup("trunk") == nil {
			continue
		}
		if !covered[command.Name()] {
			t.Errorf("%s takes --branch and --trunk but is not in stackCommands, so nothing checks that it selects or completes without Graphite", command.Name())
		}
	}
}

func TestEveryStackCommandCompletesWithoutGraphite(t *testing.T) {
	for _, command := range stackCommands {
		for flag, want := range map[string]string{"--branch": "synthetic-top", "--trunk": "synthetic-trunk"} {
			t.Run(command.name+" "+flag, func(t *testing.T) {
				recorder, _ := g2gOwnedRepository(t, ownedGraph)

				stdout, _, err := run(t, "__complete", command.name, flag, "")
				if err != nil {
					t.Fatalf("__complete %s %s: %v\n%s", command.name, flag, err, stdout)
				}
				if strings.Contains(stdout, "ShellCompDirectiveError") {
					t.Errorf("completing %s for %s failed:\n%s", flag, command.name, stdout)
				}
				if !strings.Contains(stdout, want) {
					t.Errorf("completing %s for %s omits %s:\n%s", flag, command.name, want, stdout)
				}
				recorder.AssertNone("gt ")
			})
		}
	}
}

// Trunk completion must answer for the branch being selected, not the one
// checked out. The two roots exist precisely so a wrong answer is visible
// rather than coincidentally right.
func TestTrunkCompletionFollowsAnExplicitBranch(t *testing.T) {
	recorder, _ := g2gOwnedRepository(t, ownedGraph)

	stdout, _, err := run(t, "__complete", "push", "--branch", "synthetic-side", "--trunk", "")
	if err != nil {
		t.Fatalf("__complete: %v\n%s", err, stdout)
	}
	if !strings.Contains(stdout, "synthetic-other") {
		t.Errorf("completion omits the requested branch's own root:\n%s", stdout)
	}
	if strings.Contains(stdout, "synthetic-trunk") {
		t.Errorf("completion answered for the checked-out branch instead of --branch:\n%s", stdout)
	}
	recorder.AssertNone("gt ")
}

// Every command that selects a stack must work from the recorded graph alone.
// push and link had this; the rest asserted nothing, so "works standalone" was
// only ever true for two of the five.
func TestEveryStackCommandRunsWithoutGraphite(t *testing.T) {
	for _, command := range stackCommands {
		t.Run(command.name, func(t *testing.T) {
			recorder, _ := g2gOwnedRepositoryWithPullRequests(t, ownedGraph, command.pullRequests)

			stdout, stderr, err := run(t, command.name)
			if err != nil {
				t.Fatalf("%s: %v\n%s%s", command.name, err, stdout, stderr)
			}
			for _, branch := range []string{"synthetic-lower", "synthetic-top"} {
				if !strings.Contains(stdout, branch) {
					t.Errorf("%s preview omits %s:\n%s", command.name, branch, stdout)
				}
			}
			recorder.AssertNone("gt ")
		})
	}
}

// Applying re-discovers before it mutates, so the selector runs twice. The
// second pass is the one a Graphite dependency would sneak back into.
func TestApplyingReDiscoversThroughTheSameSourceWithoutGraphite(t *testing.T) {
	recorder, _ := g2gOwnedRepository(t, ownedGraph)

	stdout, stderr, err := run(t, "link", "--apply")
	if err != nil {
		t.Fatalf("link --apply: %v\n%s%s", err, stdout, stderr)
	}

	linked := recorder.Find("gh stack link")
	if linked == "" {
		t.Fatal("no gh stack link was run")
	}
	for _, branch := range []string{"synthetic-lower", "synthetic-top"} {
		if !strings.Contains(linked, branch) {
			t.Errorf("gh stack link %q is missing %s", linked, branch)
		}
	}
	recorder.AssertNone("gt ")
}

// --trunk against a recorded path may only confirm the root. Ignoring a value
// that does not is how a stack gets projected onto the wrong base, so the
// refusal has to reach the user.
func TestTrunkThatIsNotTheRecordedBaseIsRefused(t *testing.T) {
	recorder, _ := g2gOwnedRepository(t, ownedGraph)

	stdout, _, err := run(t, "push", "--trunk", "synthetic-lower")
	if err == nil {
		t.Fatalf("push --trunk synthetic-lower: error = nil\n%s", stdout)
	}
	if !strings.Contains(err.Error(), "synthetic-trunk") {
		t.Errorf("error = %v, want it to name the base the path does have", err)
	}
	recorder.AssertNone("git push")
	recorder.AssertNone("gt ")
}

// The root is a legitimate --trunk: it is the base the path already has.
func TestTrunkMatchingTheRecordedBaseIsAccepted(t *testing.T) {
	recorder, _ := g2gOwnedRepository(t, ownedGraph)

	stdout, _, err := run(t, "push", "--trunk", "synthetic-trunk", "--apply")
	if err != nil {
		t.Fatalf("push --trunk synthetic-trunk --apply: %v\n%s", err, stdout)
	}
	if push := recorder.Find("git push --atomic"); !strings.Contains(push, "synthetic-top") {
		t.Errorf("push %q is missing the stack", push)
	}
	recorder.AssertNone("gt ")
}

// A root is not offered for --branch: nothing is recorded above it, so a
// command asked to act on one has no base and would only refuse.
func TestBranchCompletionOffersOnlySelectableBranches(t *testing.T) {
	g2gOwnedRepository(t, ownedGraph)

	stdout, _, err := run(t, "__complete", "push", "--branch", "")
	if err != nil {
		t.Fatalf("__complete: %v", err)
	}
	if strings.Contains(stdout, "synthetic-trunk") {
		t.Errorf("completion offers the root, which push cannot act on:\n%s", stdout)
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
