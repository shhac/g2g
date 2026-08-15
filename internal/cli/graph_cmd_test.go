package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/shhac/gt2gh/internal/graph"
)

// graphGit answers the ancestry questions without a repository. The forest
// below is the one every case reasons about:
//
//	synthetic-main
//	├─ synthetic-auth
//	│  ├─ synthetic-login
//	│  └─ synthetic-session
//	└─ synthetic-billing
type graphGit struct {
	current   string
	local     []string
	ancestors map[string][]string
}

func (g graphGit) CurrentBranch(context.Context) (string, error) { return g.current, nil }

func (g graphGit) LocalBranches(context.Context) ([]string, error) { return g.local, nil }

func (g graphGit) AncestorBranches(_ context.Context, target string) ([]string, error) {
	return g.ancestors[target], nil
}

func (g graphGit) CommitDistance(_ context.Context, base, head string) (int, error) { return 1, nil }

func (g graphGit) Divergence(_ context.Context, other, target string) (int, int, error) {
	return 1, 1, nil
}

func (g graphGit) IsAncestor(_ context.Context, ancestor, descendant string) (bool, error) {
	for _, candidate := range g.ancestors[descendant] {
		if candidate == ancestor {
			return true, nil
		}
	}
	return false, nil
}

// graphStore keeps the graph in memory and reports a fixed path, so golden
// output does not depend on where the test happened to run.
type graphStore struct {
	graph  graph.Graph
	writes int
}

func (s *graphStore) Load(context.Context) (graph.Graph, error) { return s.graph.Clone(), nil }

func (s *graphStore) Save(_ context.Context, g graph.Graph) error {
	s.writes++
	s.graph = g.Clone()
	return nil
}

func (s *graphStore) Path(context.Context) (string, error) {
	return "/synthetic/repo/.git/g2g/graph.json", nil
}

func graphFixture() graph.Graph {
	return graph.Graph{
		Edges: map[string]graph.Edge{
			"synthetic-auth":    {Parent: "synthetic-main", Authority: graph.AuthorityG2G, Origin: graph.OriginUser},
			"synthetic-login":   {Parent: "synthetic-auth", Authority: graph.AuthorityG2G, Origin: graph.OriginUser},
			"synthetic-session": {Parent: "synthetic-auth", Authority: graph.AuthorityG2G, Origin: graph.OriginUser},
			"synthetic-billing": {Parent: "synthetic-main", Authority: graph.AuthorityG2G, Origin: graph.OriginUser},
		},
		Trunks: []string{"synthetic-main"},
	}
}

func graphGitFixture() graphGit {
	return graphGit{
		current: "synthetic-login",
		local:   []string{"synthetic-auth", "synthetic-billing", "synthetic-login", "synthetic-main", "synthetic-session"},
		ancestors: map[string][]string{
			"synthetic-auth":    {"synthetic-main"},
			"synthetic-login":   {"synthetic-auth", "synthetic-main"},
			"synthetic-session": {"synthetic-auth", "synthetic-main"},
			"synthetic-billing": {"synthetic-main"},
		},
	}
}

func runGraph(t *testing.T, adopted graph.Graph, color bool, args ...string) (string, *graphStore, error) {
	t.Helper()
	store := &graphStore{graph: adopted}
	var stdout, stderr bytes.Buffer
	command := NewWithOptions(Options{
		Version:      "v0.1.0",
		Stdout:       &stdout,
		Stderr:       &stderr,
		Graph:        graph.Service{Git: graphGitFixture(), Store: store},
		Presentation: &Presentation{Color: color},
	})
	command.SetArgs(args)
	err := command.Execute()
	return stdout.String(), store, err
}

func TestGraphRendersAForkedTreeWithConnectors(t *testing.T) {
	for _, test := range []struct {
		name  string
		color bool
	}{{name: "graph-tree-plain"}, {name: "graph-tree-color", color: true}} {
		t.Run(test.name, func(t *testing.T) {
			out, _, err := runGraph(t, graphFixture(), test.color, "graph", "--branch", "synthetic-login", "--scope", "graph")
			if err != nil {
				t.Fatalf("graph: %v\n%s", err, out)
			}
			assertGolden(t, test.name, out)
		})
	}
}

// A chain has no fork to draw, so it keeps the flat list every other command
// renders rather than becoming a staircase that says nothing extra.
func TestGraphRendersAChainFlat(t *testing.T) {
	out, _, err := runGraph(t, graphFixture(), false, "graph", "--branch", "synthetic-login")
	if err != nil {
		t.Fatalf("graph: %v\n%s", err, out)
	}
	assertGolden(t, "graph-path-plain", out)
	if strings.Contains(out, forkGlyph) {
		t.Error("a linear selection should not draw fork connectors")
	}
}

func TestGraphSubtreeStartsFromTheSelection(t *testing.T) {
	out, _, err := runGraph(t, graphFixture(), false, "graph", "--branch", "synthetic-auth", "--scope", "subtree")
	if err != nil {
		t.Fatalf("graph: %v\n%s", err, out)
	}
	if strings.Contains(out, "synthetic-billing") {
		t.Errorf("subtree scope included a sibling branch:\n%s", out)
	}
	for _, branch := range []string{"synthetic-auth", "synthetic-login", "synthetic-session"} {
		if !strings.Contains(out, branch) {
			t.Errorf("subtree scope omitted %s:\n%s", branch, out)
		}
	}
}

func TestGraphRejectsAnUnknownScope(t *testing.T) {
	out, _, err := runGraph(t, graphFixture(), false, "graph", "--scope", "everything")
	if err == nil {
		t.Fatalf("graph --scope everything: error = nil\n%s", out)
	}
	if !strings.Contains(err.Error(), "subtree") {
		t.Errorf("error = %v, want it to list the valid scopes", err)
	}
}

func TestGraphIsReadOnly(t *testing.T) {
	_, store, err := runGraph(t, graphFixture(), false, "graph", "--scope", "graph")
	if err != nil {
		t.Fatal(err)
	}
	if store.writes != 0 {
		t.Errorf("graph wrote to the store %d time(s)", store.writes)
	}
}

func TestGraphNamesTheStoreItReadsFrom(t *testing.T) {
	out, _, err := runGraph(t, graphFixture(), false, "graph")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "/synthetic/repo/.git/g2g/graph.json") {
		t.Errorf("output does not name the store:\n%s", out)
	}
}

// A bare track must not choose. The nearest ancestor is usually right, and
// "usually" is not a basis for writing down structure every later command
// trusts.
func TestTrackWithoutAParentPreviewsCandidatesAndBlocks(t *testing.T) {
	out, store, err := runGraph(t, graph.New(), false, "track", "--branch", "synthetic-login")
	if err != nil {
		t.Fatalf("track: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Apply blocked") {
		t.Errorf("output does not block:\n%s", out)
	}
	if !strings.Contains(out, "Candidate parents, nearest first: synthetic-auth") {
		t.Errorf("output does not offer the nearest ancestor:\n%s", out)
	}
	if store.writes != 0 {
		t.Error("a blocked preview wrote to the store")
	}
	assertGolden(t, "track-candidates-plain", out)
}

func TestTrackApplyWritesOnceAndRecordsTheNewRoot(t *testing.T) {
	out, store, err := runGraph(t, graph.New(), false, "track", "--branch", "synthetic-auth", "--parent", "synthetic-main", "--apply")
	if err != nil {
		t.Fatalf("track --apply: %v\n%s", err, out)
	}
	if store.writes != 1 {
		t.Fatalf("store writes = %d, want exactly one", store.writes)
	}
	if parent, _ := store.graph.Parent("synthetic-auth"); parent != "synthetic-main" {
		t.Errorf("recorded parent = %q", parent)
	}
	if !store.graph.IsTrunk("synthetic-main") {
		t.Error("the new root was not recorded, so the next branch up could not find it")
	}
	if !strings.Contains(out, "becomes a root") {
		t.Errorf("output does not say a root was recorded:\n%s", out)
	}
}

func TestTrackApplyRefusesABlockedPlanWithoutWriting(t *testing.T) {
	out, store, err := runGraph(t, graphFixture(), false, "track", "--branch", "synthetic-login", "--parent", "synthetic-absent", "--apply")
	if err == nil {
		t.Fatalf("track --apply: error = nil\n%s", out)
	}
	if store.writes != 0 {
		t.Error("a blocked plan was written")
	}
}

func TestTrackIsANoOpWhenTheParentIsAlreadyRecorded(t *testing.T) {
	out, store, err := runGraph(t, graphFixture(), false, "track", "--branch", "synthetic-login", "--parent", "synthetic-auth", "--apply")
	if err != nil {
		t.Fatalf("track --apply: %v\n%s", err, out)
	}
	if store.writes != 0 {
		t.Errorf("store writes = %d, want none for an unchanged edge", store.writes)
	}
	if !strings.Contains(out, "already records this parent") {
		t.Errorf("output does not report the no-op:\n%s", out)
	}
}

// Removing a middle branch must show what it strands rather than reparenting.
func TestUntrackReportsTheChildrenItStrands(t *testing.T) {
	out, store, err := runGraph(t, graphFixture(), false, "untrack", "--branch", "synthetic-auth")
	if err != nil {
		t.Fatalf("untrack: %v\n%s", err, out)
	}
	if !strings.Contains(out, "without a tracked parent") {
		t.Errorf("output does not report the orphans:\n%s", out)
	}
	if !strings.Contains(out, "not reparented") {
		t.Errorf("output does not say the children keep their parent:\n%s", out)
	}
	if store.writes != 0 {
		t.Error("a preview wrote to the store")
	}
	assertGolden(t, "untrack-orphans-plain", out)
}

func TestUntrackSubtreeRemovesDescendants(t *testing.T) {
	out, store, err := runGraph(t, graphFixture(), false, "untrack", "--branch", "synthetic-auth", "--scope", "subtree", "--apply")
	if err != nil {
		t.Fatalf("untrack --apply: %v\n%s", err, out)
	}
	if store.writes != 1 {
		t.Fatalf("store writes = %d, want exactly one", store.writes)
	}
	for _, branch := range []string{"synthetic-auth", "synthetic-login", "synthetic-session"} {
		if store.graph.Tracked(branch) {
			t.Errorf("%s is still tracked", branch)
		}
	}
	if !store.graph.Tracked("synthetic-billing") {
		t.Error("untrack --scope subtree removed a branch outside the subtree")
	}
}

func TestUntrackOfAnUntrackedBranchIsANoOp(t *testing.T) {
	out, store, err := runGraph(t, graph.New(), false, "untrack", "--branch", "synthetic-login", "--apply")
	if err != nil {
		t.Fatalf("untrack --apply: %v\n%s", err, out)
	}
	if store.writes != 0 {
		t.Errorf("store writes = %d, want none", store.writes)
	}
	if !strings.Contains(out, "Nothing to do") {
		t.Errorf("output does not report the no-op:\n%s", out)
	}
}

func TestGraphCommandsAreAbsentWithoutAConfiguredService(t *testing.T) {
	var stdout, stderr bytes.Buffer
	command := NewWithOptions(Options{Version: "v0.1.0", Stdout: &stdout, Stderr: &stderr})
	for _, name := range []string{"graph", "track", "untrack"} {
		found := false
		for _, sub := range command.Commands() {
			if sub.Name() == name {
				found = true
			}
		}
		if found {
			t.Errorf("%s was registered without a graph service", name)
		}
	}
}

func TestGraphMachineFormatsCarryTheParentEdge(t *testing.T) {
	jsonOut, _, err := runGraph(t, graphFixture(), false, "graph", "--branch", "synthetic-login", "--scope", "graph", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(jsonOut, `"parent": "synthetic-auth"`) {
		t.Errorf("JSON does not carry the parent edge:\n%s", jsonOut)
	}

	porcelain, _, err := runGraph(t, graphFixture(), false, "graph", "--branch", "synthetic-login", "--scope", "graph", "--porcelain")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(porcelain), "\n") {
		fields := strings.Split(line, "\t")
		if fields[0] != "branch" || fields[1] != "synthetic-login" {
			continue
		}
		// parent is appended after the fields that shipped before it, so an
		// existing reader keeps working.
		if fields[len(fields)-1] != "synthetic-auth" {
			t.Errorf("porcelain branch record = %q, want the parent last", line)
		}
		return
	}
	t.Errorf("no porcelain branch record for the target:\n%s", porcelain)
}

func TestTreePrefixesDeriveConnectorsFromDepthAlone(t *testing.T) {
	nodes := []stackNode{
		{Branch: "root"},
		{Branch: "first", Depth: 1},
		{Branch: "first-child", Depth: 2},
		{Branch: "first-last", Depth: 2},
		{Branch: "last", Depth: 1},
		{Branch: "last-child", Depth: 2},
	}

	got := treePrefixes(nodes)

	want := []string{"", forkGlyph, railGlyph + " " + forkGlyph, railGlyph + " " + lastGlyph, lastGlyph, "  " + lastGlyph}
	for index := range want {
		if got[index] != want[index] {
			t.Errorf("prefix[%d] (%s) = %q, want %q", index, nodes[index].Branch, got[index], want[index])
		}
	}
}

// A view carrying no depth is the linear case every other command produces,
// and it must render exactly as it always has.
func TestTreePrefixesAreEmptyForALinearView(t *testing.T) {
	got := treePrefixes([]stackNode{{Branch: "a"}, {Branch: "b"}, {Branch: "c"}})

	for index, prefix := range got {
		if prefix != "" {
			t.Errorf("prefix[%d] = %q, want empty", index, prefix)
		}
	}
}
