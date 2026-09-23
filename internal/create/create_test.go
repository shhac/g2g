package create

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/repair"
)

// fakeGit answers from a table and records every call that changes something,
// so the order an apply acts in is directly assertable.
type fakeGit struct {
	current    string
	detached   bool
	local      []string
	tips       map[string]string
	invalid    map[string]bool
	staged     []string
	failCreate error
	failCommit error
	calls      []string
}

func (g *fakeGit) CurrentBranch(context.Context) (string, error) {
	if g.detached {
		return "", errors.New("HEAD is detached")
	}
	return g.current, nil
}
func (g *fakeGit) LocalBranches(context.Context) ([]string, error) { return g.local, nil }
func (g *fakeGit) Resolve(_ context.Context, revision string) (string, error) {
	return g.tips[revision], nil
}
func (g *fakeGit) CheckBranchName(_ context.Context, name string) error {
	g.calls = append(g.calls, "check-ref-format "+name)
	if g.invalid[name] {
		return errors.New("synthetic invalid name")
	}
	return nil
}
func (g *fakeGit) StagedPaths(context.Context) ([]string, error) { return g.staged, nil }
func (g *fakeGit) CreateBranch(_ context.Context, name, start string) error {
	g.calls = append(g.calls, "switch -c "+name+" "+start)
	return g.failCreate
}
func (g *fakeGit) SwitchExisting(_ context.Context, branch string) error {
	g.calls = append(g.calls, "switch "+branch)
	return nil
}
func (g *fakeGit) DeleteBranch(_ context.Context, branch string) error {
	g.calls = append(g.calls, "branch -D "+branch)
	return nil
}
func (g *fakeGit) Commit(_ context.Context, message string) error {
	g.calls = append(g.calls, "commit "+message)
	return g.failCommit
}

// fakeGraph is the graph service's three calls, over a fixed graph.
type fakeGraph struct {
	git          *fakeGit
	adopted      graph.Graph
	defaultTrunk string
	trackBlocked string
	forkPoint    string
	failApply    error
}

func (f *fakeGraph) Discover(_ context.Context, selection graph.Selection) (graph.Discovery, error) {
	return graph.Discovery{Graph: f.adopted, Target: selection.Branch, Scope: selection.Scope, Branches: []string{selection.Branch}, DefaultTrunk: f.defaultTrunk}, nil
}

func (f *fakeGraph) PlanTrack(_ context.Context, selection graph.Selection, parent string) (graph.TrackPlan, error) {
	f.git.calls = append(f.git.calls, "plan-track "+selection.Branch+" "+parent)
	forkPoint := f.forkPoint
	if forkPoint == "" {
		forkPoint = f.git.tips[parent]
	}
	updated, _, err := f.adopted.Adopt(selection.Branch, graph.Edge{Parent: parent, ForkPoint: forkPoint})
	if err != nil {
		return graph.TrackPlan{}, err
	}
	return graph.TrackPlan{Parent: parent, Updated: updated, Blocked: f.trackBlocked}, nil
}

func (f *fakeGraph) ApplyTrack(_ context.Context, plan graph.TrackPlan) error {
	f.git.calls = append(f.git.calls, "apply-track "+plan.Parent)
	return f.failApply
}

// The forest every case reasons about: one recorded stack on synthetic-main,
// plus a feature branch nobody has recorded.
func fixture() (*fakeGit, *fakeGraph) {
	git := &fakeGit{
		current: "synthetic-lower",
		local:   []string{"synthetic-lower", "synthetic-main", "synthetic-stray"},
		tips:    map[string]string{"synthetic-lower": "lower-tip", "synthetic-main": "main-tip", "synthetic-stray": "stray-tip"},
		staged:  []string{"synthetic.txt"},
	}
	adopted := graph.Graph{
		Edges:  map[string]graph.Edge{"synthetic-lower": {Parent: "synthetic-main"}},
		Trunks: []string{"synthetic-main"},
	}
	return git, &fakeGraph{git: git, adopted: adopted, defaultTrunk: "synthetic-main"}
}

func TestPlanDecidesWhetherABranchCanBeCreatedAndRecorded(t *testing.T) {
	for _, test := range []struct {
		name    string
		request Request
		arrange func(*fakeGit, *fakeGraph)
		// blocked is a fragment of the refusal, empty when the plan proceeds.
		blocked string
		// way is a command the refusal must offer.
		way      string
		parent   string
		newTrunk string
	}{
		{name: "on the branch you stand on", request: Request{Name: "synthetic-new"}, parent: "synthetic-lower"},
		{name: "under a recorded trunk", request: Request{Name: "synthetic-new", Parent: "synthetic-main"}, parent: "synthetic-main"},
		{
			// The default branch is a trunk whether or not anything is
			// recorded yet, which is the first branch in an empty graph.
			name:    "under the default branch an empty graph has never recorded",
			request: Request{Name: "synthetic-new", Parent: "synthetic-main"},
			arrange: func(_ *fakeGit, f *fakeGraph) { f.adopted = graph.New() },
			parent:  "synthetic-main", newTrunk: "synthetic-main",
		},
		{
			name:    "under a branch the graph does not know",
			request: Request{Name: "synthetic-new", Parent: "synthetic-stray"},
			blocked: "would make synthetic-stray a trunk", way: "g2g adopt --branch synthetic-stray",
		},
		{
			// Standing on it is not a different claim: an unrecorded parent
			// becomes a trunk however it was named.
			name:    "standing on a branch the graph does not know",
			request: Request{Name: "synthetic-new"},
			arrange: func(g *fakeGit, _ *fakeGraph) { g.current = "synthetic-stray" },
			blocked: "would make synthetic-stray a trunk",
		},
		{
			name:    "a name that already exists",
			request: Request{Name: "synthetic-stray"},
			blocked: "already exists", way: "g2g track --branch synthetic-stray --parent synthetic-lower",
		},
		{name: "a name git refuses", request: Request{Name: "synthetic..bad"}, arrange: func(g *fakeGit, _ *fakeGraph) { g.invalid = map[string]bool{"synthetic..bad": true} }, blocked: "synthetic invalid name"},
		{name: "an option-like name", request: Request{Name: "-synthetic"}, blocked: "cannot be passed safely"},
		{name: "a parent that is not local", request: Request{Name: "synthetic-new", Parent: "synthetic-gone"}, blocked: "not a local branch"},
		{
			name:    "a name the graph still records from a deleted branch",
			request: Request{Name: "synthetic-deleted"},
			arrange: func(_ *fakeGit, f *fakeGraph) {
				f.adopted.Edges["synthetic-deleted"] = graph.Edge{Parent: "synthetic-lower"}
			},
			blocked: "already records synthetic-deleted",
		},
		{name: "committing with nothing staged", request: Request{Name: "synthetic-new", Commit: true, Message: "synthetic"}, arrange: func(g *fakeGit, _ *fakeGraph) { g.staged = nil }, blocked: "nothing is staged", way: "g2g create synthetic-new"},
		{name: "committing with an empty message", request: Request{Name: "synthetic-new", Commit: true}, blocked: "message is empty"},
		{name: "committing what is staged", request: Request{Name: "synthetic-new", Commit: true, Message: "synthetic"}, parent: "synthetic-lower"},
		{name: "a detached HEAD", request: Request{Name: "synthetic-new", Parent: "synthetic-main"}, arrange: func(g *fakeGit, _ *fakeGraph) { g.detached = true }, blocked: "detached"},
	} {
		t.Run(test.name, func(t *testing.T) {
			git, graphs := fixture()
			if test.arrange != nil {
				test.arrange(git, graphs)
			}

			plan, err := Service{Git: git, Graph: graphs}.Plan(context.Background(), test.request)
			if err != nil {
				t.Fatalf("Plan() error = %v", err)
			}

			if test.blocked == "" {
				if plan.Blocked != "" {
					t.Fatalf("Blocked = %q, want the plan to proceed", plan.Blocked)
				}
				if plan.Parent != test.parent || plan.NewTrunk != test.newTrunk {
					t.Errorf("parent %q new trunk %q, want %q and %q", plan.Parent, plan.NewTrunk, test.parent, test.newTrunk)
				}
				if plan.At != git.tips[test.parent] {
					t.Errorf("At = %q, want the parent's tip", plan.At)
				}
				return
			}
			if !strings.Contains(plan.Blocked, test.blocked) {
				t.Errorf("Blocked = %q, want it to contain %q", plan.Blocked, test.blocked)
			}
			// The sentence a machine reads is built from the note a person
			// reads, so the two cannot name different commands.
			if plan.Blocked != plan.Repair.Sentence() {
				t.Errorf("Blocked %q is not the repair's sentence %q", plan.Blocked, plan.Repair.Sentence())
			}
			if test.way != "" && !slices.ContainsFunc(plan.Repair.Ways, func(way repair.Step) bool { return way.Command == test.way }) {
				t.Errorf("repair offers %+v, want %q among them", plan.Repair.Ways, test.way)
			}
		})
	}
}

// An option-like name must never reach git at all, not even to be validated:
// check-ref-format would read it as one of its own flags.
func TestAnOptionLikeNameNeverReachesGit(t *testing.T) {
	git, graphs := fixture()
	if _, err := (Service{Git: git, Graph: graphs}).Plan(context.Background(), Request{Name: "--synthetic"}); err != nil {
		t.Fatal(err)
	}
	if len(git.calls) != 0 {
		t.Errorf("git was asked %v about an option-like name", git.calls)
	}
}

func TestApplyCreatesThenRecordsThenCommits(t *testing.T) {
	git, graphs := fixture()
	service := Service{Git: git, Graph: graphs}
	request := Request{Name: "synthetic-new", Commit: true, Message: "synthetic first"}
	plan, err := service.Plan(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	git.calls = nil

	if err := service.Apply(context.Background(), plan); err != nil {
		t.Fatalf("Apply() = %v", err)
	}

	want := []string{
		"switch -c synthetic-new synthetic-lower",
		"plan-track synthetic-new synthetic-lower",
		"apply-track synthetic-lower",
		"commit synthetic first",
	}
	if !slices.Equal(git.calls, want) {
		t.Errorf("calls = %v, want %v", git.calls, want)
	}
}

// A recording that fails leaves nothing behind: the branch has no commits of
// its own yet, so going back and deleting it loses nothing.
func TestAFailedRecordingIsRolledBack(t *testing.T) {
	for _, test := range []struct {
		name    string
		arrange func(*fakeGraph)
	}{
		{name: "the track plan refuses", arrange: func(f *fakeGraph) { f.trackBlocked = "synthetic refusal" }},
		{name: "the store write fails", arrange: func(f *fakeGraph) { f.failApply = errors.New("synthetic write failure") }},
		{name: "the parent moved underneath", arrange: func(f *fakeGraph) { f.forkPoint = "moved-tip" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			git, graphs := fixture()
			test.arrange(graphs)
			service := Service{Git: git, Graph: graphs}
			plan, err := service.Plan(context.Background(), Request{Name: "synthetic-new", Commit: true, Message: "synthetic"})
			if err != nil {
				t.Fatal(err)
			}
			git.calls = nil

			err = service.Apply(context.Background(), plan)

			var rolledBack *RolledBack
			if !errors.As(err, &rolledBack) || rolledBack.Left != nil {
				t.Fatalf("Apply() = %v, want a complete rollback", err)
			}
			tail := git.calls[len(git.calls)-2:]
			if !slices.Equal(tail, []string{"switch synthetic-lower", "branch -D synthetic-new"}) {
				t.Errorf("rollback ran %v, want a switch back and a delete", git.calls)
			}
			if slices.Contains(git.calls, "commit synthetic") {
				t.Error("committed after a failed recording")
			}
		})
	}
}

// A commit that fails after the branch is recorded is reported as part-done,
// not rolled back: the branch and its edge are what was asked for, and the
// staged changes are still staged on it.
func TestAFailedCommitLeavesTheBranchAndItsRecord(t *testing.T) {
	git, graphs := fixture()
	git.failCommit = errors.New("synthetic hook refused")
	service := Service{Git: git, Graph: graphs}
	plan, err := service.Plan(context.Background(), Request{Name: "synthetic-new", Commit: true, Message: "synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	git.calls = nil

	err = service.Apply(context.Background(), plan)

	var partial *Partial
	if !errors.As(err, &partial) || partial.Branch != "synthetic-new" {
		t.Fatalf("Apply() = %v, want a partial apply naming synthetic-new", err)
	}
	for _, call := range git.calls {
		if strings.HasPrefix(call, "branch -D") || call == "switch synthetic-lower" {
			t.Errorf("a failed commit was rolled back: %v", git.calls)
		}
	}
}

func TestABlockedPlanIsNeverApplied(t *testing.T) {
	git, graphs := fixture()
	service := Service{Git: git, Graph: graphs}
	plan, err := service.Plan(context.Background(), Request{Name: "synthetic-new", Parent: "synthetic-stray"})
	if err != nil {
		t.Fatal(err)
	}
	git.calls = nil

	if err := service.Apply(context.Background(), plan); err == nil {
		t.Fatal("Apply() of a blocked plan = nil")
	}
	if len(git.calls) != 0 {
		t.Errorf("a blocked plan ran %v", git.calls)
	}
}

func TestRevalidationRefusesAParentThatMoved(t *testing.T) {
	git, graphs := fixture()
	service := Service{Git: git, Graph: graphs}
	request := Request{Name: "synthetic-new"}
	preview, err := service.Plan(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	git.tips["synthetic-lower"] = "moved-tip"

	if _, err := service.Revalidate(context.Background(), request, preview); err == nil {
		t.Fatal("Revalidate() = nil after the parent moved")
	}
}
