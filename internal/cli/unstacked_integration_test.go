package cli_test

import (
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/testutil"
)

// unstackedRepository is a repository nobody has stacked in yet: no g2g store,
// no Graphite, no pull requests. It is what every repository looks like before
// the first g2g track, and what this one looks like today.
func unstackedRepository(t *testing.T, defaultHead string) *testutil.Recorder {
	t.Helper()

	common := t.TempDir()
	routes := map[string][]testutil.Route{
		"git": {
			{Prefix: "rev-parse --path-format=absolute --git-common-dir", Output: common},
			{Prefix: "branch --show-current", Output: "main"},
			{Prefix: "branch --format", Lines: []string{"main"}},
			{Prefix: "rev-parse --verify", Output: "1111111111111111111111111111111111111111"},
			{Prefix: "merge-base --is-ancestor"},
			{Prefix: "cherry", Lines: []string{"+ 1111111111111111111111111111111111111111"}},
			{Prefix: "status --porcelain"},
			{Prefix: "remote get-url", Output: "https://example.test/synthetic.git"},
			{Prefix: "symbolic-ref --quiet refs/remotes/origin/HEAD", Output: defaultHead},
		},
		"gt": {},
		"gh": {
			{Prefix: "repo view", Output: `{"nameWithOwner":"example/synthetic"}`},
			{Prefix: "api graphql", Output: `{"data":{"repository":{}}}`},
		},
	}
	if defaultHead == "" {
		// An exit-non-zero route stands for the ref simply not being set, which
		// is what a repository built by hand looks like.
		routes["git"] = routes["git"][:len(routes["git"])-1]
	}
	return testutil.FakeCLIs(t, routes)
}

// status is the read-only triage entry point, so "nothing is stacked here" is
// an answer to what it was asked rather than a failure to answer it.
//
// It used to exit 2 with "no source describes \"main\" · run g2g track to
// record its parent" — advice that would break the one rule the graph has, on
// a command that would refuse it, while g2g graph rendered the same fact and
// exited 0.
func TestStatusReportsAnUnstackedTrunkInsteadOfFailing(t *testing.T) {
	unstackedRepository(t, "refs/remotes/origin/main")

	stdout, _, err := run(t, "status")
	if err != nil {
		t.Fatalf("status on an unstacked trunk returned an error: %v\n%s", err, stdout)
	}

	for _, want := range []string{"main", "trunk", "default branch", "g2g track --branch <child> --parent main"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output does not contain %q:\n%s", want, stdout)
		}
	}
	// The advice that would break the invariant must be gone, not merely
	// accompanied by better advice.
	if strings.Contains(stdout, "record its parent") {
		t.Errorf("still tells the trunk to adopt a parent:\n%s", stdout)
	}
}

// Without the ref there is no evidence either way, so the branch reads as one
// that is simply not tracked — which is the honest answer and the one that was
// always given.
func TestAnUnknownDefaultStillReportsRatherThanGuessing(t *testing.T) {
	unstackedRepository(t, "")

	stdout, _, err := run(t, "status")
	if err != nil {
		t.Fatalf("status returned an error: %v\n%s", err, stdout)
	}

	if strings.Contains(stdout, "default branch") {
		t.Errorf("claimed a default branch with no ref to read it from:\n%s", stdout)
	}
	if !strings.Contains(stdout, "no source describes") {
		t.Errorf("output does not say the branch is undescribed:\n%s", stdout)
	}
}

// Rendering the state is status's alone. A command that mutates still has
// nothing to act on, and must still refuse rather than quietly doing nothing.
func TestAMutatingCommandStillRefusesAnUnstackedTrunk(t *testing.T) {
	for _, command := range [][]string{
		{"link", "--apply"},
		{"push", "--apply"},
		{"submit", "--apply"},
	} {
		t.Run(command[0], func(t *testing.T) {
			recorder := unstackedRepository(t, "refs/remotes/origin/main")

			if _, _, err := run(t, command...); err == nil {
				t.Fatalf("%s was allowed on a branch nothing describes", command[0])
			}
			recorder.AssertNone("git push", "gh pr create", "gh stack link")
		})
	}
}

// The machine formats describe the same world: a target, no branches, and the
// reason. A consumer switching on the exit code to mean "there is a stack" was
// reading the wrong thing, and the branch list says it directly.
func TestTheUnstackedStateIsDescribedInJSONToo(t *testing.T) {
	unstackedRepository(t, "refs/remotes/origin/main")

	stdout, _, err := run(t, "status", "--json")
	if err != nil {
		t.Fatalf("status --json returned an error: %v\n%s", err, stdout)
	}

	for _, want := range []string{`"target": "main"`, `"branches": []`, `"notes"`} {
		if !strings.Contains(stdout, want) {
			t.Errorf("JSON does not contain %q:\n%s", want, stdout)
		}
	}
}

// graph answers without a network. It gained --from so the two records can be
// compared in one format, and that must not have brought gh in with it.
func TestGraphReachesNoGitHubWhateverSourceIsNamed(t *testing.T) {
	for _, from := range []string{"", "g2g", "graphite", "pull-request"} {
		name := from
		if name == "" {
			name = "default"
		}
		t.Run(name, func(t *testing.T) {
			recorder, _ := g2gOwnedRepository(t, ownedGraph)

			args := []string{"graph"}
			if from != "" {
				args = append(args, "--from", from)
			}
			_, _, err := run(t, args...)

			if from == "pull-request" && err == nil {
				t.Error("graph --from pull-request was allowed")
			}
			recorder.AssertNone("gh ")
		})
	}
}
