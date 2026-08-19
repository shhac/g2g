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

// g2gOwnedRepository is a repository that has never used Graphite and whose
// structure g2g records itself. Every route here is a Git one: if a command
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
			{Prefix: "pr edit"},
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
// works from the structure g2g records itself.
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
			t.Errorf("running a g2g command left Graphite state behind: %s", entry.Name())
		}
	}
}

// Completing a flag must work from the structure g2g records itself. It also
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

// dualSourceRepository is described by both sources, and they disagree: g2g
// records synthetic-top under synthetic-lower, Graphite declares it directly on
// a different trunk. Precedence alone can never show the second, which is what
// --from exists to fix.
func dualSourceRepository(t *testing.T) *testutil.Recorder {
	t.Helper()

	common := testutil.GraphiteRepository(t)
	dir := filepath.Join(common, "g2g")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "graph.json"), []byte(ownedGraph), 0o600); err != nil {
		t.Fatal(err)
	}
	return testutil.FakeCLIs(t, map[string][]testutil.Route{
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
		"gt": {
			{Prefix: "--version", Output: "1.8.6"},
			{Prefix: "log", Lines: []string{"◯  synthetic-other", "◉  synthetic-top"}},
		},
		"gh": {
			{Prefix: "repo view", Output: `{"nameWithOwner":"example/synthetic"}`},
			{Prefix: "api graphql", Output: ownedPullRequests},
			{Prefix: "stack link"},
		},
	})
}

// Precedence still decides by default: g2g holds the edge, so g2g answers and
// Graphite is never asked.
func TestPrecedenceStillDecidesWithoutFrom(t *testing.T) {
	recorder := dualSourceRepository(t)

	stdout, _, err := run(t, "push")
	if err != nil {
		t.Fatalf("push: %v\n%s", err, stdout)
	}
	if !strings.Contains(stdout, "synthetic-lower") {
		t.Errorf("preview does not show the g2g-recorded path:\n%s", stdout)
	}
	recorder.AssertNone("gt ")
}

// --from graphite overrides precedence for one invocation, which is the only
// way to see Graphite's view of a branch g2g has adopted.
func TestFromPinsTheSourceAgainstPrecedence(t *testing.T) {
	recorder := dualSourceRepository(t)

	stdout, _, err := run(t, "push", "--from", "graphite")
	if err != nil {
		t.Fatalf("push --from graphite: %v\n%s", err, stdout)
	}
	if !strings.Contains(stdout, "synthetic-other") {
		t.Errorf("preview does not show the Graphite-declared base:\n%s", stdout)
	}
	if strings.Contains(stdout, "synthetic-lower") {
		t.Errorf("preview still shows the g2g path despite --from graphite:\n%s", stdout)
	}
	if recorder.Find("gt log") == "" {
		t.Error("Graphite was never consulted despite --from graphite")
	}
}

// Pinning does not force a source to answer. The refusal names the source the
// user chose, not a precedence they did not.
func TestFromReportsASourceThatDoesNotDescribeTheBranch(t *testing.T) {
	g2gOwnedRepository(t, ownedGraph)

	_, _, err := run(t, "push", "--from", "graphite")
	if err == nil {
		t.Fatal("push --from graphite: error = nil where Graphite is not configured")
	}
	if !strings.Contains(err.Error(), "graphite does not describe") {
		t.Errorf("error = %v, want it to name the pinned source", err)
	}
}

func TestFromRejectsAnUnknownSource(t *testing.T) {
	g2gOwnedRepository(t, ownedGraph)

	_, _, err := run(t, "push", "--from", "synthetic-nonsense")
	if err == nil {
		t.Fatal("push --from synthetic-nonsense: error = nil")
	}
	for _, want := range []string{"unknown source", "g2g", "graphite"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to contain %q", err, want)
		}
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
	{name: "retarget", pullRequests: ownedPullRequests},
	// graph resolves through a source too now, and reads no pull requests at
	// all: its whole point is answering without a network.
	{name: "graph", pullRequests: ownedPullRequests},
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
		// --from is what stackOptions registers and nothing else does, so it is
		// the marker for "this command resolves a stack through a source".
		// --trunk alone is not: track has one too, meaning where an adoption
		// stops rather than which base a projection sits on.
		if command.Flags().Lookup("branch") == nil || command.Flags().Lookup("from") == nil {
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
				// --trunk says which base a projection sits on, so a command
				// that projects nothing does not have one. Asserting on a flag
				// a command never registered would fail for the wrong reason.
				if !offersFlag(t, command.name, strings.TrimPrefix(flag, "--")) {
					t.Skipf("%s has no %s", command.name, flag)
				}
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

// treeRepository is a real repository with a forked stack. Ancestry is the one
// seam a PATH fake proves nothing about — the fake answers whatever it is
// asked, and the question is what Git actually considers reachable — so this
// builds a throwaway local repository with synthetic names and no remote.
func treeRepository(t *testing.T) string {
	t.Helper()

	repo := testutil.NewGitRepo(t, "synthetic-trunk")
	repo.Commit("root", "root.txt", "root\n")
	repo.Run("switch", "-qc", "synthetic-a")
	repo.Commit("a", "a.txt", "a\n")
	repo.Run("switch", "-qc", "synthetic-b")
	repo.Commit("b", "b.txt", "b\n")
	repo.Run("switch", "-q", "synthetic-a")
	repo.Run("switch", "-qc", "synthetic-c")
	repo.Commit("c", "c.txt", "c\n")
	repo.Run("switch", "-q", "synthetic-trunk")
	repo.Run("switch", "-qc", "synthetic-elsewhere")
	repo.Commit("elsewhere", "elsewhere.txt", "elsewhere\n")
	repo.Run("switch", "-q", "synthetic-b")
	return repo.Dir
}

func inRepository(t *testing.T, dir string, args ...string) (string, string, error) {
	t.Helper()

	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })
	return run(t, args...)
}

// A stack is a forest, so adopting one has to record a tree rather than a line.
func TestTrackStackRecordsAWholeTree(t *testing.T) {
	dir := treeRepository(t)

	stdout, stderr, err := inRepository(t, dir, "track", "--stack", "--trunk", "synthetic-trunk", "--apply")
	if err != nil {
		t.Fatalf("track --stack --apply: %v\n%s%s", err, stdout, stderr)
	}

	// Selected from synthetic-a, "my stack" is the trunk beneath it and both
	// branches above it. Selected from synthetic-b it would not be: synthetic-c
	// is a cousin there, which is the distinction the scope exists to make.
	graphOut, _, err := inRepository(t, dir, "graph", "--branch", "synthetic-a", "--scope", "stack")
	if err != nil {
		t.Fatalf("graph: %v", err)
	}
	for _, branch := range []string{"synthetic-a", "synthetic-b", "synthetic-c"} {
		if !strings.Contains(graphOut, branch) {
			t.Errorf("graph omits %s:\n%s", branch, graphOut)
		}
	}
	// A branch that only shares the trunk is a separate stack, not part of this
	// one, and sweeping it in would adopt half the repository.
	if strings.Contains(graphOut, "synthetic-elsewhere") {
		t.Errorf("adoption swept in a branch that only shares the trunk:\n%s", graphOut)
	}
}

// Adopting from the tip downwards must leave exactly one trunk. This is the
// shape that used to promote every branch on the way past.
func TestTrackStackLeavesOneTrunk(t *testing.T) {
	dir := treeRepository(t)

	if _, _, err := inRepository(t, dir, "track", "--stack", "--trunk", "synthetic-trunk", "--apply"); err != nil {
		t.Fatalf("track --stack: %v", err)
	}

	stored, err := os.ReadFile(filepath.Join(dir, ".git", "g2g", "graph.json"))
	if err != nil {
		t.Fatal(err)
	}
	var recorded struct {
		Trunks   []string                     `json:"trunks"`
		Branches map[string]map[string]string `json:"branches"`
	}
	if err := json.Unmarshal(stored, &recorded); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(recorded.Trunks, ","); got != "synthetic-trunk" {
		t.Errorf("trunks = %s, want only synthetic-trunk", got)
	}
	if parent := recorded.Branches["synthetic-b"]["parent"]; parent != "synthetic-a" {
		t.Errorf("parent of synthetic-b = %q, want synthetic-a", parent)
	}
}

// Re-running records nothing, which is what makes it safe to reach for.
func TestTrackStackIsRepeatable(t *testing.T) {
	dir := treeRepository(t)

	if _, _, err := inRepository(t, dir, "track", "--stack", "--trunk", "synthetic-trunk", "--apply"); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	stdout, _, err := inRepository(t, dir, "track", "--stack", "--trunk", "synthetic-trunk")
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if !strings.Contains(stdout, "Nothing to do") {
		t.Errorf("a second run does not report a no-op:\n%s", stdout)
	}
}

// The candidate list is where a newcomer first meets the tool, so it has to
// name the way out of doing this one branch at a time.
func TestCandidateAdviceOffersWholeStackAdoption(t *testing.T) {
	dir := treeRepository(t)

	stdout, _, err := inRepository(t, dir, "track")
	if err != nil {
		t.Fatalf("track: %v", err)
	}
	if !strings.Contains(stdout, "--stack") {
		t.Errorf("the candidate advice does not mention --stack:\n%s", stdout)
	}
}

// push must never invoke gh. Its whole contract is one atomic git push and no
// GitHub call, so a source that resolves by reading pull request bases has to
// be refused by the flag rather than honoured by the resolver.
//
// It was honoured: --from is registered on every stack command, and the
// resolver's on-request tier answers whoever names it, so push --from
// pull-request ran gh repo view and gh api graphql before selection began. The
// invariant was stated in the skill and in the comment directly above the field
// that broke it, and nothing asserted it — the sibling test above checks push
// reaches no Graphite and never checked GitHub.
func TestPushNeverReachesGitHubWhateverSourceIsNamed(t *testing.T) {
	for _, from := range []string{"", "g2g", "pull-request"} {
		name := from
		if name == "" {
			name = "default"
		}
		t.Run(name, func(t *testing.T) {
			recorder, _ := g2gOwnedRepository(t, ownedGraph)

			args := []string{"push", "--apply"}
			if from != "" {
				args = append(args, "--from", from)
			}
			stdout, _, err := run(t, args...)

			// pull-request is refused; the others succeed. Either way no gh.
			if from == "pull-request" {
				if err == nil {
					t.Fatalf("push --from pull-request was allowed:\n%s", stdout)
				}
				if !strings.Contains(err.Error(), "gh") {
					t.Errorf("refusal does not say why: %v", err)
				}
			} else if err != nil {
				t.Fatalf("push --from %q: %v\n%s", from, err, stdout)
			}
			recorder.AssertNone("gh ")
		})
	}
}

// offersFlag reports whether a command registered a flag, so a table covering
// several commands can assert only what each actually has.
func offersFlag(t *testing.T, command, flag string) bool {
	t.Helper()
	var stdout, stderr bytes.Buffer
	for _, candidate := range cli.New("v0.0.0-test", &stdout, &stderr).Commands() {
		if candidate.Name() == command {
			return candidate.Flags().Lookup(flag) != nil
		}
	}
	t.Fatalf("no command named %q", command)
	return false
}
