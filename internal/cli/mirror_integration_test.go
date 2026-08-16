package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shhac/gt2gh/internal/testutil"
)

// mirrorRepository is described by both sources. The gt2gh graph is fixed and
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

// Graphite has the two branches in the opposite order to the gt2gh graph, which
// is a disagreement about both of them and needs no fork to express.
var invertedGraphiteLog = []string{
	"◯  synthetic-trunk",
	"◯  synthetic-top",
	"◉  synthetic-lower",
}

// Graphite agrees about the recorded stack and carries one extra branch on top
// that the gt2gh graph says nothing about.
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

// The whole point: Graphite ends up recording what gt2gh records.
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

// Without --prune, a branch gt2gh does not record is reported and left alone.
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
