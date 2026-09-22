package reshape

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/graph"
)

// fakeGit answers from tables and records every call that changes something,
// so the order an apply acts in is directly assertable. It serves both the
// graph service's reads and this package's, because one repository answers
// both.
type fakeGit struct {
	current   string
	local     []string
	tips      map[string]string
	ancestors map[string]bool // "ancestor descendant"
	// absent is what Cherry reports as having no equivalent upstream, keyed by
	// "upstream head".
	absent      map[string][]string
	absorbed    bool
	unpublished []git.Commit
	remote      map[string][]string
	elsewhere   map[string]string
	invalid     map[string]bool
	fail        map[string]error // keyed by the recorded call
	calls       []string
}

func (g *fakeGit) record(call string) error {
	g.calls = append(g.calls, call)
	return g.fail[call]
}

func (g *fakeGit) CurrentBranch(context.Context) (string, error) {
	if g.current == "" {
		return "", errors.New("HEAD is detached")
	}
	return g.current, nil
}
func (g *fakeGit) LocalBranches(context.Context) ([]string, error) { return g.local, nil }
func (g *fakeGit) Resolve(_ context.Context, revision string) (string, error) {
	if tip, ok := g.tips[revision]; ok {
		return tip, nil
	}
	return revision, nil
}
func (g *fakeGit) IsAncestor(_ context.Context, ancestor, descendant string) (bool, error) {
	return g.ancestors[ancestor+" "+descendant], nil
}
func (g *fakeGit) Cherry(_ context.Context, upstream, head, _ string) ([]string, []string, error) {
	return g.absent[upstream+" "+head], nil, nil
}
func (g *fakeGit) Absorbed(context.Context, string, string) (bool, error) { return g.absorbed, nil }
func (g *fakeGit) AncestorBranches(context.Context, string) ([]string, error) {
	return nil, nil
}
func (g *fakeGit) Divergence(context.Context, string, string) (int, int, error) { return 0, 0, nil }
func (g *fakeGit) Unpublished(context.Context, string, string) ([]git.Commit, error) {
	return g.unpublished, nil
}
func (g *fakeGit) RemoteTracking(_ context.Context, branch string) ([]string, error) {
	return g.remote[branch], nil
}
func (g *fakeGit) CheckedOutElsewhere(context.Context) (map[string]string, error) {
	return g.elsewhere, nil
}
func (g *fakeGit) CheckBranchName(_ context.Context, name string) error {
	g.calls = append(g.calls, "check-ref-format "+name)
	if g.invalid[name] {
		return errors.New("synthetic invalid name")
	}
	return nil
}
func (g *fakeGit) SwitchExisting(_ context.Context, branch string) error {
	return g.record("switch " + branch)
}
func (g *fakeGit) SwitchTree(_ context.Context, from, to string) error {
	return g.record("read-tree " + from + " " + to)
}
func (g *fakeGit) MoveBranch(_ context.Context, branch, from, to string) error {
	return g.record(fmt.Sprintf("update-ref %s %s %s", branch, to, from))
}
func (g *fakeGit) RenameBranch(_ context.Context, from, to string) error {
	return g.record("branch -m " + from + " " + to)
}
func (g *fakeGit) DeleteBranch(_ context.Context, branch string) error {
	return g.record("branch -D " + branch)
}

// fakeStore keeps the graph in memory and records writes into the same call
// list as Git, so a test sees one ordered history.
type fakeStore struct {
	git     *fakeGit
	adopted graph.Graph
	failAt  int // the save, counting from one, that fails; zero for none
	saves   int
}

func (s *fakeStore) Load(context.Context) (graph.Graph, error) { return s.adopted.Clone(), nil }
func (s *fakeStore) Path(context.Context) (string, error)      { return "/synthetic/graph.json", nil }
func (s *fakeStore) Save(_ context.Context, g graph.Graph) error {
	s.saves++
	s.git.calls = append(s.git.calls, "save")
	if s.saves == s.failAt {
		return errors.New("synthetic write failure")
	}
	s.adopted = g.Clone()
	return nil
}

type fakePins struct {
	git  *fakeGit
	fail map[string]error
}

func (p *fakePins) PinForkPoint(_ context.Context, branch, object string) error {
	p.git.calls = append(p.git.calls, "pin "+branch+" "+object)
	return p.fail["pin "+branch]
}
func (p *fakePins) UnpinForkPoint(_ context.Context, branch string) error {
	p.git.calls = append(p.git.calls, "unpin "+branch)
	return p.fail["unpin "+branch]
}

// world is the forest every case reasons about:
//
//	synthetic-main
//	└─ synthetic-lower
//	   ├─ synthetic-middle
//	   │  └─ synthetic-top
//	   └─ synthetic-side
//
// synthetic-middle's commits sit directly on synthetic-lower, so it can be
// folded; synthetic-top and synthetic-side fork where their parents are now.
type world struct {
	git   *fakeGit
	store *fakeStore
	pins  *fakePins
}

func newWorld() world {
	fake := &fakeGit{
		current: "synthetic-middle",
		local:   []string{"synthetic-main", "synthetic-lower", "synthetic-middle", "synthetic-top", "synthetic-side"},
		tips: map[string]string{
			"synthetic-main": "main-tip", "synthetic-lower": "lower-tip", "synthetic-middle": "middle-tip",
			"synthetic-top": "top-tip", "synthetic-side": "side-tip",
		},
		ancestors: map[string]bool{
			"synthetic-lower synthetic-middle": true,
			"synthetic-main synthetic-lower":   true,
		},
		absent: map[string][]string{"synthetic-lower synthetic-middle": {"middle-own", "middle-pushed"}},
		unpublished: []git.Commit{
			{ID: "middle-own", Subject: "synthetic middle work"},
			{ID: "middle-merged", Subject: "synthetic already upstream"},
		},
		remote: map[string][]string{},
	}
	adopted := graph.Graph{
		Edges: map[string]graph.Edge{
			"synthetic-lower":  {Parent: "synthetic-main", ForkPoint: "main-tip"},
			"synthetic-middle": {Parent: "synthetic-lower", ForkPoint: "lower-tip"},
			"synthetic-top":    {Parent: "synthetic-middle", ForkPoint: "middle-tip"},
			"synthetic-side":   {Parent: "synthetic-lower", ForkPoint: "lower-tip"},
		},
		Trunks: []string{"synthetic-main"},
	}
	store := &fakeStore{git: fake, adopted: adopted}
	return world{git: fake, store: store, pins: &fakePins{git: fake}}
}

func (w world) service() Service {
	return Service{Git: w.git, Graph: graph.Service{Git: w.git, Store: w.store, Refs: w.pins}}
}

// mutations are the recorded calls that change something, which is every call
// but the name check.
func (w world) mutations() []string {
	return slices.DeleteFunc(slices.Clone(w.git.calls), func(call string) bool {
		return strings.HasPrefix(call, "check-ref-format")
	})
}
