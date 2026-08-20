package cli

import (
	"bytes"
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/shape"
	"github.com/shhac/g2g/internal/stack"
	"github.com/shhac/g2g/internal/testutil"
)

// graphGit answers the ancestry questions without a repository. The forest
// below is the one every case reasons about:
//
//	synthetic-main
//	├─ synthetic-auth
//	│  ├─ synthetic-login
//	│  └─ synthetic-session
//	└─ synthetic-billing
//
// It is a copy of the graph package's own fakeAncestry rather than a shared
// one, for the reason recorded at internal/stack/g2g_test.go: a fake shared
// across a package boundary is a second interface nobody declared. Only the
// answers are shared, through internal/testutil.
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
			out, _, err := runGraph(t, graphFixture(), test.color, "graph", "--branch", "synthetic-login", "--scope", "trunk")
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
	_, store, err := runGraph(t, graphFixture(), false, "graph", "--scope", "trunk")
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
	jsonOut, _, err := runGraph(t, graphFixture(), false, "graph", "--branch", "synthetic-login", "--scope", "trunk", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(jsonOut, `"parent": "synthetic-auth"`) {
		t.Errorf("JSON does not carry the parent edge:\n%s", jsonOut)
	}

	porcelain, _, err := runGraph(t, graphFixture(), false, "graph", "--branch", "synthetic-login", "--scope", "trunk", "--porcelain")
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
	completed, err := staticCompletions(shape.Scopes)(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(completed, ",") != "branch,path,subtree,stack,trunk" {
		t.Errorf("scope completion = %v", completed)
	}
	for _, value := range completed {
		if _, err := shape.ParseScope(value, shape.Scopes, graph.ScopeStack); err != nil {
			t.Errorf("completion offered %q, which ParseScope rejects", value)
		}
	}
}

// The three things g2g can see and deliberately will not repair each need to
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
	command.SetArgs([]string{"graph", "--branch", "synthetic-login", "--scope", "trunk"})
	if err := command.Execute(); err != nil {
		t.Fatalf("graph: %v\n%s", err, stdout.String())
	}

	if out := stdout.String(); !strings.Contains(out, "No tracked parent for") {
		t.Errorf("output does not report the orphan:\n%s", out)
	}
}

// A command that rewrites history or projects onto GitHub must refuse a scope
// it never offered. The services deliberately parse the wider read set, so this
// gate is the only thing standing between "restack this stack" and "restack
// every root in the repository".
func TestAScopeGateRefusesWhatItsCommandDoesNotOffer(t *testing.T) {
	for _, test := range []struct {
		name     string
		accepted []graph.Scope
		scope    string
		wantErr  bool
	}{
		{"replay refuses forest", shape.Scopes, string(graph.ScopeAll), true},
		{"replay allows graph", shape.Scopes, string(graph.ScopeTrunk), false},
		{"display allows forest", shape.ReadScopes, string(graph.ScopeAll), false},
		{"removal refuses graph", []graph.Scope{graph.ScopeBranch, graph.ScopeSubtree}, string(graph.ScopeTrunk), true},
		{"an empty scope is the default", shape.Scopes, "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			options := graphOptions{scopeOptions: scopeOptions{scope: test.scope, accepted: test.accepted}}
			err := options.validateScope()
			if test.wantErr && err == nil {
				t.Errorf("validateScope() error = nil for scope %q", test.scope)
			}
			if !test.wantErr && err != nil {
				t.Errorf("validateScope() error = %v for scope %q", err, test.scope)
			}
		})
	}
}

// Every mutating command registers the narrow set. A future command that wires
// ReadScopes into something that writes should fail here rather than in a
// repository.
func TestOnlyReadOnlyCommandsOfferForest(t *testing.T) {
	for _, scope := range shape.Scopes {
		if scope == graph.ScopeAll {
			t.Fatal("shape.Scopes contains forest; it is the set handed to commands that mutate")
		}
	}
	if len(shape.ReadScopes) != len(shape.Scopes)+1 {
		t.Errorf("ReadScopes = %v, want exactly Scopes plus forest", shape.ReadScopes)
	}
}

// A hint that names a flag value is a command the reader will type, so the
// value has to be one the command accepts.
//
// The scope vocabulary was renamed (forest became all) and this hint kept the
// old word, so following the tool's own advice produced "unsupported scope".
// Scanning the advice for scope words and checking each against the parser
// catches the next rename too, which naming one value in one assertion would
// not.
func TestSuggestedScopesAreOnesTheCommandAccepts(t *testing.T) {
	discovery := graph.Discovery{
		Target:   "synthetic-trunk",
		Branches: []string{"synthetic-trunk"},
		Scope:    graph.ScopePath,
		Graph:    forkedGraph(t),
	}

	suggested := regexp.MustCompile(`--scope ([a-z]+)`)
	found := 0
	for _, note := range graphStatusView(discovery).Notes {
		for _, match := range suggested.FindAllStringSubmatch(note.Text, -1) {
			found++
			// Checked against the set the graph command registers, not a
			// fixed one: the whole failure mode is advice drifting from what
			// the command actually takes.
			if _, err := shape.ParseScope(match[1], shape.ReadScopes, graph.ScopeStack); err != nil {
				t.Errorf("advice names --scope %s, which the command rejects: %v", match[1], err)
			}
		}
	}
	if found == 0 {
		t.Fatal("no scope advice found to check; the hint this guards has moved")
	}
}

// forkedGraph is a trunk with branches under it, so the hidden-descendants
// hint that carries the scope advice actually fires.
func forkedGraph(t *testing.T) graph.Graph {
	t.Helper()

	recorded := graph.New()
	for _, edge := range []struct{ branch, parent string }{
		{"synthetic-a", "synthetic-trunk"},
		{"synthetic-b", "synthetic-trunk"},
	} {
		updated, err := recorded.Track(edge.branch, graph.Edge{Parent: edge.parent})
		if err != nil {
			t.Fatalf("Track(%q) error = %v", edge.branch, err)
		}
		recorded = updated
	}
	return recorded
}

// The parity table compares the two records on synthetic fixtures. Rendering
// both in one format compares them on the repository in front of you, which is
// the check no fixture can stand in for.
func TestGraphReadsAnotherRecordInItsOwnFormat(t *testing.T) {
	snapshot := stack.Snapshot{
		Target:       "synthetic-b",
		TargetSource: "current Git branch",
		Base:         "synthetic-trunk",
		Branches:     []string{"synthetic-a", "synthetic-b", "synthetic-cousin"},
		Parents: map[string]string{
			"synthetic-a":      "synthetic-trunk",
			"synthetic-b":      "synthetic-a",
			"synthetic-cousin": "synthetic-trunk",
		},
		Scope:  stack.ScopeTrunk,
		Source: stack.SourceGraphite,
	}

	var out bytes.Buffer
	if err := writeStackView(&out, structureNote(sourceGraphView(snapshot), snapshot), Presentation{}); err != nil {
		t.Fatal(err)
	}

	rendered := out.String()
	for _, want := range []string{"synthetic-trunk", "synthetic-a", "synthetic-cousin", "Structure from graphite"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("output does not contain %q:\n%s", want, rendered)
		}
	}
	// The fork is drawn, which is the shape a comparison is actually about.
	if !strings.Contains(rendered, "├─") {
		t.Errorf("the fork was flattened:\n%s", rendered)
	}
	// State comes from recorded fork points, which only g2g's store has.
	// Inventing one for another record would be the drift this view exists to
	// find, reported as a fact.
	for _, absent := range []string{"needs restack", "moved off parent", "fork point lost", "untracked"} {
		if strings.Contains(rendered, absent) {
			t.Errorf("annotated %q for a record with no fork points:\n%s", absent, rendered)
		}
	}
}

// graph answers without a network, which is the whole reason it exists apart
// from status. A pull request base is read by invoking gh, so it is refused
// before any discovery runs and the refusal names the command that does read it.
func TestGraphRefusesASourceItWouldNeedTheNetworkFor(t *testing.T) {
	for _, test := range []struct {
		from string
		want string
	}{
		{from: "", want: ""},
		{from: "g2g", want: ""},
		{from: "graphite", want: ""},
		{from: "pull-request", want: "g2g status --from pull-request does read it"},
		{from: "synthetic-nonsense", want: "unknown source"},
	} {
		t.Run(test.from, func(t *testing.T) {
			err := validateOfflineSource(test.from)
			if test.want == "" {
				if err != nil {
					t.Fatalf("validateOfflineSource(%q) = %v, want accepted", test.from, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("validateOfflineSource(%q) = nil", test.from)
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Errorf("refusal %q does not contain %q", err, test.want)
			}
		})
	}
}

// Cherry reports every commit as absent from the trunk unless a case says
// otherwise, so a branch reads as landed only where that is the subject.
func (g graphGit) Cherry(_ context.Context, _, head, _ string) (absent, present []string, err error) {
	return testutil.OwnCommits(head), nil, nil
}

// Absorbed answers of a whole branch what Cherry answers per commit, which is
// what a squash merge needs. Nothing here is absorbed unless a case says so.
func (g graphGit) Absorbed(context.Context, string, string) (bool, error) { return false, nil }

// A branch the graph does not record gets one note, and which one depends on
// what is around it. Three states used to be two, and both mistakes were
// visible on an ordinary read: a trunk with a forest on it was told to record a
// parent for itself, and a target absent from the drawing was named in the
// header and explained by a note about parents.
func TestAnUntrackedTargetIsExplainedByWhatIsAroundIt(t *testing.T) {
	stacked := graph.Graph{
		Edges:  map[string]graph.Edge{"synthetic-auth": {Parent: "synthetic-main"}},
		Trunks: []string{"synthetic-main"},
	}
	for _, test := range []struct {
		name      string
		discovery graph.Discovery
		want      string
	}{
		{
			name:      "a trunk with a forest on it needs no advice",
			discovery: graph.Discovery{Graph: stacked, Target: "synthetic-main", Branches: []string{"synthetic-main", "synthetic-auth"}, DefaultTrunk: "synthetic-main"},
			want:      "",
		},
		{
			name:      "a trunk with nothing on it is where a stack starts",
			discovery: graph.Discovery{Graph: graph.New(), Target: "synthetic-main", Branches: []string{"synthetic-main"}, DefaultTrunk: "synthetic-main"},
			want:      "default branch",
		},
		{
			name:      "a target the drawing omits says so",
			discovery: graph.Discovery{Graph: stacked, Target: "synthetic-elsewhere", Branches: []string{"synthetic-main", "synthetic-auth"}},
			want:      "not drawn above",
		},
		{
			name:      "an ordinary branch is missing a parent",
			discovery: graph.Discovery{Graph: graph.New(), Target: "synthetic-feature", Branches: []string{"synthetic-feature"}},
			want:      "no recorded parent",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			note := untrackedNote(test.discovery)
			if test.want == "" {
				if note != "" {
					t.Errorf("untrackedNote() = %q, want nothing", note)
				}
				return
			}
			if !strings.Contains(note, test.want) {
				t.Errorf("untrackedNote() = %q, want it to contain %q", note, test.want)
			}
		})
	}
}
