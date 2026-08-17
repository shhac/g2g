package graph

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/shhac/g2g/internal/diagnostic"
	"github.com/shhac/g2g/internal/stack"
)

// Service reads and adopts g2g-owned graphs. It needs Git and a store, and
// notably not Graphite or GitHub: a g2g-owned graph is exactly the structure
// that exists without either.
// Pinner keeps recorded fork points reachable. It is separate from Ancestry
// because Ancestry is read-only by contract and this writes a ref.
type Pinner interface {
	PinForkPoint(ctx context.Context, branch, object string) error
	UnpinForkPoint(ctx context.Context, branch string) error
}

type Service struct {
	Git   Ancestry
	Store Store
	// Refs pins fork points so they survive garbage collection. A service
	// without one still works; its fork points are simply unprotected.
	Refs Pinner
}

// Selection is the no-checkout selector the graph commands share.
type Selection struct {
	Branch string
	Scope  Scope
}

// Discovery is the read-only picture every graph command starts from.
type Discovery struct {
	Graph        Graph
	Target       string
	TargetSource string
	Scope        Scope
	// Branches is the selected set in render order.
	Branches []string
	States   map[string]NodeState
	// StorePath names the file an apply would write, so a preview can be
	// specific about what it is about to change.
	StorePath string
}

// Equal compares every fact that can change what a graph command does.
func (d Discovery) Equal(other Discovery) bool {
	return d.Target == other.Target &&
		d.TargetSource == other.TargetSource &&
		d.Scope == other.Scope &&
		d.StorePath == other.StorePath &&
		d.Graph.Equal(other.Graph) &&
		slices.Equal(d.Branches, other.Branches) &&
		maps.Equal(d.States, other.States)
}

// Orphans reports selected branches whose recorded parent is neither tracked
// nor a trunk.
func (d Discovery) Orphans() []string {
	all := d.Graph.Orphans()
	orphans := make([]string, 0)
	for _, branch := range d.Branches {
		if slices.Contains(all, branch) {
			orphans = append(orphans, branch)
		}
	}
	return orphans
}

// NeedsRestack reports selected branches whose parent moved underneath them.
// g2g does not rebase, so this is reported and never repaired.
func (d Discovery) NeedsRestack() []string {
	return d.branchesInState(StateNeedsRestack)
}

// MissingParents reports selected branches whose recorded parent is no longer
// a local branch, which is what a merged and deleted parent looks like.
func (d Discovery) MissingParents() []string {
	return d.branchesInState(StateParentMissing)
}

// InState returns the selected branches in one state, in render order.
func (d Discovery) InState(want NodeState) []string { return d.branchesInState(want) }

func (d Discovery) branchesInState(want NodeState) []string {
	matching := make([]string, 0)
	for _, branch := range d.Branches {
		if d.States[branch] == want {
			matching = append(matching, branch)
		}
	}
	return matching
}

// Discover loads the adopted graph and assesses the selected branches against
// Git. It never writes and never checks a branch out.
func (s Service) Discover(ctx context.Context, selection Selection) (Discovery, error) {
	if s.Git == nil || s.Store == nil {
		return Discovery{}, fmt.Errorf("graph service is not fully configured")
	}
	target, source, err := s.target(ctx, selection.Branch)
	if err != nil {
		return Discovery{}, err
	}
	// Discover never writes, so it parses the read set and defaults to the whole
	// stack — ancestors, descendants, and where the target sits between them.
	// Which scopes a command offers is that command's own gate: all is safe to
	// display and unsafe to hand something that rewrites.
	scope, err := stack.ParseScope(string(selection.Scope), stack.ReadScopes, stack.ScopeStack)
	if err != nil {
		return Discovery{}, err
	}
	adopted, err := s.Store.Load(ctx)
	if err != nil {
		return Discovery{}, err
	}
	branches, err := adopted.Select(target, scope)
	if err != nil {
		return Discovery{}, err
	}
	states, err := Assess(ctx, s.Git, adopted, branches)
	if err != nil {
		return Discovery{}, err
	}
	path, err := s.Store.Path(ctx)
	if err != nil {
		return Discovery{}, err
	}
	diagnostic.Event(ctx, "graph.discovery",
		diagnostic.Field{Key: "target", Value: target},
		diagnostic.Field{Key: "source", Value: source},
		diagnostic.Field{Key: "scope", Value: string(scope)},
		diagnostic.Field{Key: "tracked", Value: fmt.Sprint(len(adopted.Edges))},
		diagnostic.Field{Key: "selected", Value: strings.Join(branches, ",")},
	)
	return Discovery{Graph: adopted, Target: target, TargetSource: source, Scope: scope, Branches: branches, States: states, StorePath: path}, nil
}

func (s Service) target(ctx context.Context, requested string) (string, string, error) {
	if requested != "" {
		return requested, "--branch", nil
	}
	current, err := s.Git.CurrentBranch(ctx)
	if err != nil {
		return "", "", err
	}
	return current, "current Git branch", nil
}
