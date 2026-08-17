package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/testutil"
)

// mirrorRepository is described by both sources. The g2g graph is fixed and
// the Graphite display varies per test, because what a mirror does is entirely
// a function of how the two disagree.
func mirrorRepository(t *testing.T, graphJSON string, graphiteLog []string) *testutil.Recorder {
	t.Helper()

	common := testutil.GraphiteRepository(t)
	if graphJSON != "" {
		dir := filepath.Join(common, "g2g")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "graph.json"), []byte(graphJSON), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return testutil.FakeCLIs(t, map[string][]testutil.Route{
		"git": {
			{Prefix: "rev-parse --path-format=absolute --git-common-dir", Output: common},
			{Prefix: "branch --show-current", Output: "synthetic-top"},
			{Prefix: "branch --format", Lines: []string{"synthetic-lower", "synthetic-stale", "synthetic-top", "synthetic-trunk"}},
			// import resolves a fork point per adopted edge and asks Git whether
			// it confirms each declared relationship.
			{Prefix: "rev-parse --verify", Output: "1111111111111111111111111111111111111111"},
			{Prefix: "merge-base --is-ancestor"},
			{Prefix: "update-ref"},
		},
		"gt": {
			{Prefix: "--version", Output: "1.8.6"},
			{Prefix: "log", Lines: graphiteLog},
			{Prefix: "track"},
			{Prefix: "untrack"},
		},
	})
}

const mirrorGraph = `{"storeSchemaVersion":1,"trunks":["synthetic-trunk"],"branches":{
	"synthetic-lower":{"parent":"synthetic-trunk","origin":"user"},
	"synthetic-top":{"parent":"synthetic-lower","origin":"user"}}}`

// Graphite has the two branches in the opposite order to the g2g graph, which
// is a disagreement about both of them and needs no fork to express.
var invertedGraphiteLog = []string{
	"◯  synthetic-trunk",
	"◯  synthetic-top",
	"◉  synthetic-lower",
}

// Graphite agrees about the recorded stack and carries one extra branch on top
// that the g2g graph says nothing about.
var strangerGraphiteLog = []string{
	"◯  synthetic-trunk",
	"◯  synthetic-lower",
	"◯  synthetic-top",
	"◉  synthetic-stale",
}

func TestMirrorPreviewWritesNothing(t *testing.T) {
	recorder := mirrorRepository(t, mirrorGraph, invertedGraphiteLog)

	stdout, _, err := run(t, "mirror")
	if err != nil {
		t.Fatalf("mirror: %v\n%s", err, stdout)
	}

	if !strings.Contains(stdout, "synthetic-top") {
		t.Errorf("preview does not name the branch it would move:\n%s", stdout)
	}
	recorder.AssertNone("gt track")
	recorder.AssertNone("gt untrack")
}

// The whole point: Graphite ends up recording what g2g records.
func TestMirrorApplyMovesTheDisagreeingBranch(t *testing.T) {
	recorder := mirrorRepository(t, mirrorGraph, invertedGraphiteLog)

	stdout, stderr, err := run(t, "mirror", "--apply")
	if err != nil {
		t.Fatalf("mirror --apply: %v\n%s%s", err, stdout, stderr)
	}

	tracked := recorder.Find("gt track synthetic-top")
	if !strings.Contains(tracked, "--parent synthetic-lower") {
		t.Errorf("recorded %q, want synthetic-top moved under synthetic-lower", tracked)
	}
	if !strings.Contains(tracked, "--no-interactive") {
		t.Errorf("recorded %q, want it noninteractive", tracked)
	}
	recorder.AssertNone("gt untrack")
}

// Without --prune, a branch g2g does not record is reported and left alone.
func TestMirrorLeavesStrangersAloneWithoutPrune(t *testing.T) {
	recorder := mirrorRepository(t, mirrorGraph, strangerGraphiteLog)

	stdout, _, err := run(t, "mirror")
	if err != nil {
		t.Fatalf("mirror: %v\n%s", err, stdout)
	}
	if !strings.Contains(stdout, "synthetic-stale") {
		t.Errorf("preview does not name the branch only Graphite has:\n%s", stdout)
	}
	if !strings.Contains(stdout, "--prune") {
		t.Errorf("preview does not say what would remove it:\n%s", stdout)
	}
	recorder.AssertNone("gt untrack")
}

func TestMirrorPruneUntracksOnlyInGraphite(t *testing.T) {
	recorder := mirrorRepository(t, mirrorGraph, strangerGraphiteLog)

	stdout, stderr, err := run(t, "mirror", "--prune", "--apply")
	if err != nil {
		t.Fatalf("mirror --prune --apply: %v\n%s%s", err, stdout, stderr)
	}

	if untracked := recorder.Find("gt untrack synthetic-stale"); untracked == "" {
		t.Error("the stale branch was not untracked in Graphite")
	}
	for _, kept := range []string{"gt untrack synthetic-top", "gt untrack synthetic-lower"} {
		recorder.AssertNone(kept)
	}
}

// A mirror must not be what enrols a repository. Reading Graphite's forest runs
// its discovery command, so even the preview has to refuse first.
func TestMirrorRefusesWithoutTouchingAGraphiteFreeRepository(t *testing.T) {
	recorder, common := g2gOwnedRepository(t, ownedGraph)

	_, _, err := run(t, "mirror")
	if err == nil {
		t.Fatal("mirror: error = nil in a repository that does not use Graphite")
	}
	if !strings.Contains(err.Error(), "does not use Graphite") {
		t.Errorf("error = %v", err)
	}

	recorder.AssertNone("gt ")
	entries, err := os.ReadDir(common)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(strings.ToLower(entry.Name()), "graphite") {
			t.Errorf("previewing a mirror enrolled the repository: %s", entry.Name())
		}
	}
}

// import is the other direction: it writes the g2g graph and never Graphite.
func TestImportAdoptsIntoTheG2GGraphOnly(t *testing.T) {
	recorder := mirrorRepository(t, "", strangerGraphiteLog)

	stdout, stderr, err := run(t, "import", "--apply")
	if err != nil {
		t.Fatalf("import --apply: %v\n%s%s", err, stdout, stderr)
	}

	for _, branch := range []string{"synthetic-lower", "synthetic-top", "synthetic-stale"} {
		if !strings.Contains(stdout, branch) {
			t.Errorf("preview omits %s:\n%s", branch, stdout)
		}
	}
	recorder.AssertNone("gt track")
	recorder.AssertNone("gt untrack")
}

// Adoption is the authority claim, so the preview has to say so rather than
// only listing branches.
func TestImportPreviewNamesTheAuthorityShift(t *testing.T) {
	mirrorRepository(t, "", strangerGraphiteLog)

	stdout, _, err := run(t, "import")
	if err != nil {
		t.Fatalf("import: %v\n%s", err, stdout)
	}
	if !strings.Contains(stdout, "answers for") {
		t.Errorf("preview does not say g2g takes over answering:\n%s", stdout)
	}
	if !strings.Contains(stdout, "--from graphite") {
		t.Errorf("preview does not say how to see Graphite's view afterwards:\n%s", stdout)
	}
}

// A disagreement is refused rather than resolved, and both answers are named.
func TestImportBlocksAndNamesBothRecords(t *testing.T) {
	mirrorRepository(t, mirrorGraph, invertedGraphiteLog)

	stdout, _, err := run(t, "import")
	if err != nil {
		t.Fatalf("import: %v\n%s", err, stdout)
	}
	if !strings.Contains(stdout, "Apply blocked") {
		t.Errorf("preview is not blocked by the disagreement:\n%s", stdout)
	}
	if !strings.Contains(stdout, "g2g says") || !strings.Contains(stdout, "Graphite says") {
		t.Errorf("preview does not name both records:\n%s", stdout)
	}

	_, _, applyErr := run(t, "import", "--apply")
	if applyErr == nil {
		t.Error("import --apply: error = nil for a blocked plan")
	}
}

// A Graphite write that fails must be reported, not swallowed into a success
// message. Nothing exercised a failing gt write end to end before this.
func TestMirrorReportsAFailedGraphiteWrite(t *testing.T) {
	common := testutil.GraphiteRepository(t)
	dir := filepath.Join(common, "g2g")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "graph.json"), []byte(mirrorGraph), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.FakeCLIs(t, map[string][]testutil.Route{
		"git": {
			{Prefix: "rev-parse --path-format=absolute --git-common-dir", Output: common},
			{Prefix: "branch --show-current", Output: "synthetic-top"},
			{Prefix: "branch --format", Lines: []string{"synthetic-lower", "synthetic-top", "synthetic-trunk"}},
		},
		"gt": {
			{Prefix: "--version", Output: "1.8.6"},
			{Prefix: "log", Lines: invertedGraphiteLog},
			{Prefix: "track", Stderr: "synthetic graphite refusal", Exit: 1},
		},
	})

	stdout, _, err := run(t, "mirror", "--apply")
	if err == nil {
		t.Fatalf("mirror --apply: error = nil when gt track failed\n%s", stdout)
	}
	if strings.Contains(stdout, "Graphite is aligned.") {
		t.Errorf("a failed mirror claimed success:\n%s", stdout)
	}
}

// import must refuse a Graphite-free repository for the same reason mirror
// does: reading the forest is what enrols it.
func TestImportRefusesWithoutTouchingAGraphiteFreeRepository(t *testing.T) {
	recorder, common := g2gOwnedRepository(t, ownedGraph)

	_, _, err := run(t, "import")
	if err == nil {
		t.Fatal("import: error = nil in a repository that does not use Graphite")
	}
	if !strings.Contains(err.Error(), "does not use Graphite") {
		t.Errorf("error = %v", err)
	}

	recorder.AssertNone("gt ")
	entries, err := os.ReadDir(common)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(strings.ToLower(entry.Name()), "graphite") {
			t.Errorf("previewing an import enrolled the repository: %s", entry.Name())
		}
	}
}

// Both commands must say "nothing to do" rather than claim work they did not
// do. The hand-rolled flow they started with printed a ready-to-apply banner
// and then reported success for an empty plan.
func TestAlignedMirrorReportsNothingToDo(t *testing.T) {
	mirrorRepository(t, mirrorGraph, []string{"◯  synthetic-trunk", "◯  synthetic-lower", "◉  synthetic-top"})

	for _, arguments := range [][]string{{"mirror"}, {"mirror", "--apply"}} {
		stdout, _, err := run(t, arguments...)
		if err != nil {
			t.Fatalf("%v: %v\n%s", arguments, err, stdout)
		}
		if !strings.Contains(stdout, "Nothing to do") {
			t.Errorf("%v does not report a no-op:\n%s", arguments, stdout)
		}
		if strings.Contains(stdout, "Ready to apply") {
			t.Errorf("%v shows a ready banner with nothing to do:\n%s", arguments, stdout)
		}
		if strings.Contains(stdout, "Rerun with --apply") {
			t.Errorf("%v invites an apply with nothing to apply:\n%s", arguments, stdout)
		}
	}
}

// A blocked plan also has no writes, so it must not be reported as agreement.
// "Nothing to do" and "this cannot be done" are opposite answers that happen to
// produce the same empty list.
func TestBlockedMirrorIsNotReportedAsNothingToDo(t *testing.T) {
	// Graphite has never heard of the g2g graph's root, and cannot be told
	// about it without being given a parent.
	mirrorRepository(t, mirrorGraph, []string{"◯  synthetic-other", "◉  synthetic-elsewhere"})

	stdout, _, err := run(t, "mirror")
	if err != nil {
		t.Fatalf("mirror: %v\n%s", err, stdout)
	}
	if !strings.Contains(stdout, "Apply blocked") {
		t.Errorf("a blocked mirror is not reported as blocked:\n%s", stdout)
	}
	if strings.Contains(stdout, "Nothing to do") {
		t.Errorf("a blocked mirror claimed there was nothing to do:\n%s", stdout)
	}
	if !strings.Contains(stdout, "synthetic-trunk") {
		t.Errorf("the refusal does not name the root Graphite lacks:\n%s", stdout)
	}

	if _, _, applyErr := run(t, "mirror", "--apply"); applyErr == nil {
		t.Error("mirror --apply: error = nil for a blocked plan")
	}
}

// retarget is the one command that changes what a merge will do, so the exact
// gh invocation matters as much as the decision behind it.
func TestRetargetMovesOnlyTheMismatchedBase(t *testing.T) {
	// synthetic-lower sits correctly on the trunk; synthetic-top still records
	// the trunk as its base though the stack says it sits on synthetic-lower.
	stale := `{"data":{"repository":{` +
		`"pr0":{"nodes":[{"number":201,"url":"https://example.test/201","headRefName":"synthetic-lower","baseRefName":"synthetic-trunk","state":"OPEN"}]},` +
		`"pr1":{"nodes":[{"number":202,"url":"https://example.test/202","headRefName":"synthetic-top","baseRefName":"synthetic-trunk","state":"OPEN"}]}}}}`
	recorder, _ := g2gOwnedRepositoryWithPullRequests(t, ownedGraph, stale)

	stdout, _, err := run(t, "retarget")
	if err != nil {
		t.Fatalf("retarget: %v\n%s", err, stdout)
	}
	if !strings.Contains(stdout, "base synthetic-trunk → synthetic-lower") {
		t.Errorf("preview does not name the base it would move:\n%s", stdout)
	}
	recorder.AssertNone("gh pr edit")

	if _, _, err := run(t, "retarget", "--apply"); err != nil {
		t.Fatalf("retarget --apply: %v", err)
	}
	edited := recorder.Find("gh pr edit")
	if !strings.Contains(edited, "202") || !strings.Contains(edited, "--base synthetic-lower") {
		t.Errorf("recorded %q, want #202 pointed at synthetic-lower", edited)
	}
	// The pull request already sitting where it belongs is left alone.
	if strings.Contains(edited, "201") {
		t.Errorf("recorded %q, want the correct pull request untouched", edited)
	}
}

// A stack GitHub already agrees with is a no-op, which is what makes this safe
// to run after every restack.
func TestRetargetIsANoOpWhenBasesAlreadyMatch(t *testing.T) {
	aligned := `{"data":{"repository":{` +
		`"pr0":{"nodes":[{"number":201,"url":"https://example.test/201","headRefName":"synthetic-lower","baseRefName":"synthetic-trunk","state":"OPEN"}]},` +
		`"pr1":{"nodes":[{"number":202,"url":"https://example.test/202","headRefName":"synthetic-top","baseRefName":"synthetic-lower","state":"OPEN"}]}}}}`
	recorder, _ := g2gOwnedRepositoryWithPullRequests(t, ownedGraph, aligned)

	stdout, _, err := run(t, "retarget", "--apply")
	if err != nil {
		t.Fatalf("retarget --apply: %v\n%s", err, stdout)
	}
	if !strings.Contains(stdout, "Nothing to do") {
		t.Errorf("an aligned stack does not report a no-op:\n%s", stdout)
	}
	recorder.AssertNone("gh pr edit")
}
