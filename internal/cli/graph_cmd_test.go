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

// Divergence answers from the same ancestor map: an ancestor has nothing
// ahead, a descendant has nothing behind.
func (g graphGit) Divergence(_ context.Context, other, target string) (int, int, error) {
	ahead, behind := 1, 1
	if ancestor, _ := g.IsAncestor(context.Background(), other, target); ancestor {
		ahead = 0
	}
	if descendant, _ := g.IsAncestor(context.Background(), target, other); descendant {
		behind = 0
	}
	return ahead, behind, nil
}

func (g graphGit) Resolve(_ context.Context, revision string) (string, error) {
	return revision, nil
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
			"synthetic-auth":    {Parent: "synthetic-main", Origin: graph.OriginUser},
			"synthetic-login":   {Parent: "synthetic-auth", Origin: graph.OriginUser},
			"synthetic-session": {Parent: "synthetic-auth", Origin: graph.OriginUser},
			"synthetic-billing": {Parent: "synthetic-main", Origin: graph.OriginUser},
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

func TestBranchCompletionListsLocalBranches(t *testing.T) {
	service := graph.Service{Git: graphGitFixture(), Store: &graphStore{graph: graphFixture()}}

	completed, err := localBranchCompletions(service)(context.Background(), "")
	if err != nil {
		t.Fatalf("localBranchCompletions() error = %v", err)
	}
	if len(completed) != len(graphGitFixture().local) {
		t.Errorf("completion = %v, want every local branch", completed)
	}
}

// Completion must not fail a shell when the service is absent.
func TestBranchCompletionIsEmptyWithoutAGit(t *testing.T) {
	completed, err := localBranchCompletions(graph.Service{})(context.Background(), "")
	if err != nil || len(completed) != 0 {
		t.Errorf("localBranchCompletions() = %v, %v; want empty and no error", completed, err)
	}
}

func TestScopeCompletionOffersEveryAcceptedValue(t *testing.T) {
	completed, err := staticCompletions(graph.Scopes)(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(completed, ",") != "branch,path,subtree,graph" {
		t.Errorf("scope completion = %v", completed)
	}
	for _, value := range completed {
		if _, err := graph.ParseScope(value); err != nil {
			t.Errorf("completion offered %q, which ParseScope rejects", value)
		}
	}
}

// The three things gt2gh can see and deliberately will not repair each need to
// reach the reader, because nothing else is going to tell them.
func TestGraphReportsEveryKindOfStalenessItRefusesToRepair(t *testing.T) {
	git := graphGitFixture()
	// session was rewritten off its recorded base, and auth's parent was
	// merged and deleted, so it is no longer a local branch.
	git.ancestors["synthetic-session"] = nil
	git.local = []string{"synthetic-auth", "synthetic-billing", "synthetic-login", "synthetic-session"}

	store := &graphStore{graph: graphFixture()}
	var stdout, stderr bytes.Buffer
	command := NewWithOptions(Options{
		Version: "v0.1.0", Stdout: &stdout, Stderr: &stderr,
		Graph:        graph.Service{Git: git, Store: store},
		Presentation: &Presentation{},
	})
	command.SetArgs([]string{"graph", "--branch", "synthetic-auth", "--scope", "subtree"})
	if err := command.Execute(); err != nil {
		t.Fatalf("graph: %v\n%s", err, stdout.String())
	}

	out := stdout.String()
	for _, want := range []string{
		"synthetic-session",
		"no longer a local branch for synthetic-auth",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not mention %q:\n%s", want, out)
		}
	}
}

// An untracked middle branch leaves its children with a parent the graph does
// not know, and saying so is the whole point of not reparenting them.
func TestGraphReportsBranchesWithNoTrackedParent(t *testing.T) {
	orphaned := graphFixture().Untrack("synthetic-auth")
	store := &graphStore{graph: orphaned}
	var stdout, stderr bytes.Buffer
	command := NewWithOptions(Options{
		Version: "v0.1.0", Stdout: &stdout, Stderr: &stderr,
		Graph:        graph.Service{Git: graphGitFixture(), Store: store},
		Presentation: &Presentation{},
	})
	command.SetArgs([]string{"graph", "--branch", "synthetic-login", "--scope", "graph"})
	if err := command.Execute(); err != nil {
		t.Fatalf("graph: %v\n%s", err, stdout.String())
	}

	if out := stdout.String(); !strings.Contains(out, "No tracked parent for") {
		t.Errorf("output does not report the orphan:\n%s", out)
	}
}
