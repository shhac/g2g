package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shhac/gt2gh/internal/testutil"
)

// The graph commands run through the real Git adapter here: real argv, a real
// process, real bytes parsed back. Injected fakes at the service seam cannot
// catch a malformed argument or a mishandled exit status, because they replace
// the code that has to get those right.
//
// Nothing in these tests reaches Graphite or GitHub, which is the point of a
// g2g-owned graph: it is exactly the structure that exists without either.
func graphRepository(t *testing.T, adopted string) (*testutil.Recorder, string) {
	t.Helper()

	common := t.TempDir()
	if adopted != "" {
		dir := filepath.Join(common, "g2g")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "graph.json"), []byte(adopted), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	recorder := testutil.FakeCLIs(t, map[string][]testutil.Route{
		"git": {
			{Prefix: "branch --show-current", Output: "synthetic-login"},
			{Prefix: "branch --format", Lines: []string{"synthetic-auth", "synthetic-login", "synthetic-main"}},
			{Prefix: "rev-parse --path-format=absolute --git-common-dir", Output: common},
			{Prefix: "for-each-ref", Lines: []string{"synthetic-auth", "synthetic-main"}},
			// Real git separates the two counts with a tab, so the fixture does
			// too: Lines spills to a file and can carry one.
			{Prefix: "rev-list --left-right --count", Lines: []string{"1\t1"}},
			{Prefix: "merge-base --is-ancestor"},
			{Prefix: "rev-parse --verify", Output: "1111111111111111111111111111111111111111"},
			{Prefix: "update-ref"},
		},
	})
	return recorder, common
}

const adoptedGraph = `{"storeSchemaVersion":1,"trunks":["synthetic-main"],"branches":{
	"synthetic-auth":{"parent":"synthetic-main","authority":"g2g","origin":"user"},
	"synthetic-login":{"parent":"synthetic-auth","authority":"g2g","origin":"user"}}}`

func TestGraphReadsTheStoreThroughRealAdapters(t *testing.T) {
	recorder, _ := graphRepository(t, adoptedGraph)

	stdout, _, err := run(t, "graph", "--scope", "graph")
	if err != nil {
		t.Fatalf("graph: %v\n%s", err, stdout)
	}

	for _, branch := range []string{"synthetic-main", "synthetic-auth", "synthetic-login"} {
		if !strings.Contains(stdout, branch) {
			t.Errorf("output omits %s:\n%s", branch, stdout)
		}
	}
	// A fake answers from its routes whatever it is asked, so only the
	// recorded argv proves the absolute path format was actually requested.
	recorder.Find("git rev-parse --path-format=absolute --git-common-dir")
	recorder.Find("git merge-base --is-ancestor synthetic-auth synthetic-login")
	recorder.AssertNone("gt ", "gh ")
}

func TestGraphTargetsTheCurrentBranchWithoutCheckingItOut(t *testing.T) {
	recorder, _ := graphRepository(t, adoptedGraph)

	stdout, _, err := run(t, "graph")
	if err != nil {
		t.Fatalf("graph: %v\n%s", err, stdout)
	}

	if !strings.Contains(stdout, "current Git branch") {
		t.Errorf("output does not name where the target came from:\n%s", stdout)
	}
	recorder.AssertNone("git checkout", "git switch")
}

func TestTrackBuildsItsCandidateQueryFromGitAncestry(t *testing.T) {
	recorder, _ := graphRepository(t, "")

	stdout, _, err := run(t, "track", "--branch", "synthetic-login")
	if err != nil {
		t.Fatalf("track: %v\n%s", err, stdout)
	}

	recorder.Find("git for-each-ref --format=%(refname:short) --merged synthetic-login refs/heads/")
	recorder.Find("git rev-list --left-right --count synthetic-auth...synthetic-login")
	if !strings.Contains(stdout, "Candidate parents") {
		t.Errorf("output does not offer candidates:\n%s", stdout)
	}
}

// A preview must leave no trace. The store is a file, so "did not mutate"
// means the file is still absent.
func TestTrackPreviewWritesNothing(t *testing.T) {
	_, common := graphRepository(t, "")

	if _, _, err := run(t, "track", "--branch", "synthetic-login", "--parent", "synthetic-auth"); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(common, "g2g", "graph.json")); !os.IsNotExist(err) {
		t.Fatalf("preview created the store (stat error = %v)", err)
	}
}

func TestTrackApplyWritesAStoreThatReadsBack(t *testing.T) {
	_, common := graphRepository(t, "")

	stdout, _, err := run(t, "track", "--branch", "synthetic-login", "--parent", "synthetic-auth", "--apply")
	if err != nil {
		t.Fatalf("track --apply: %v\n%s", err, stdout)
	}

	contents, err := os.ReadFile(filepath.Join(common, "g2g", "graph.json"))
	if err != nil {
		t.Fatalf("read store: %v", err)
	}
	var written struct {
		StoreSchemaVersion int      `json:"storeSchemaVersion"`
		Trunks             []string `json:"trunks"`
		Branches           map[string]struct {
			Parent    string `json:"parent"`
			Authority string `json:"authority"`
			Origin    string `json:"origin"`
		} `json:"branches"`
	}
	if err := json.Unmarshal(contents, &written); err != nil {
		t.Fatalf("parse written store: %v\n%s", err, contents)
	}
	if written.StoreSchemaVersion != 1 {
		t.Errorf("storeSchemaVersion = %d", written.StoreSchemaVersion)
	}
	if edge := written.Branches["synthetic-login"]; edge.Parent != "synthetic-auth" || edge.Authority != "g2g" || edge.Origin != "user" {
		t.Errorf("written edge = %#v", edge)
	}
	if len(written.Trunks) != 1 || written.Trunks[0] != "synthetic-auth" {
		t.Errorf("trunks = %v, want the new root recorded", written.Trunks)
	}
}

// The preview/apply sequence is the safety contract: discover, re-discover,
// render the validated plan, then write exactly once.
func TestTrackApplyRediscoversBeforeWriting(t *testing.T) {
	recorder, _ := graphRepository(t, "")

	if _, _, err := run(t, "track", "--branch", "synthetic-login", "--parent", "synthetic-auth", "--apply"); err != nil {
		t.Fatal(err)
	}

	if reads := recorder.Count("git rev-parse --path-format=absolute"); reads < 2 {
		t.Errorf("store was located %d time(s); apply must re-discover before writing", reads)
	}
}

func TestTrackApplyRendersTheValidatedPlanBeforeWriting(t *testing.T) {
	_, _ = graphRepository(t, "")

	stdout, _, err := run(t, "track", "--branch", "synthetic-login", "--parent", "synthetic-auth", "--apply")
	if err != nil {
		t.Fatal(err)
	}

	ready := strings.Index(stdout, "Ready to apply")
	recorded := strings.Index(stdout, "Recorded.")
	if ready < 0 || recorded < 0 || ready > recorded {
		t.Errorf("apply did not render the validated plan before reporting the write:\n%s", stdout)
	}
}

func TestUntrackApplyRewritesTheStore(t *testing.T) {
	_, common := graphRepository(t, adoptedGraph)

	stdout, _, err := run(t, "untrack", "--branch", "synthetic-login", "--apply")
	if err != nil {
		t.Fatalf("untrack --apply: %v\n%s", err, stdout)
	}

	contents, err := os.ReadFile(filepath.Join(common, "g2g", "graph.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(contents), "synthetic-login") {
		t.Errorf("untrack left the edge in place:\n%s", contents)
	}
	if !strings.Contains(string(contents), "synthetic-auth") {
		t.Errorf("untrack removed an edge outside the selection:\n%s", contents)
	}
}

// A store written by a newer gt2gh must not be silently rewritten by an older
// one, so an unrecognised version is an error rather than an empty graph.
func TestGraphFailsClosedOnAnUnsupportedStoreVersion(t *testing.T) {
	graphRepository(t, `{"storeSchemaVersion":99,"branches":{}}`)

	_, _, err := run(t, "graph")
	if err == nil {
		t.Fatal("graph: error = nil for a future store schema")
	}
	if !strings.Contains(err.Error(), "99") {
		t.Errorf("error = %v, want it to name the version found", err)
	}
}
