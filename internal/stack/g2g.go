package stack

import (
	"context"
	"fmt"
	"slices"

	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/shape"
)

// Selector describes branches g2g's own store records, so the commands that
// project a stack onto GitHub can act on one.
//
// Adoption into the store is the authority claim, which is why this is
// consulted before Graphite: recording an edge is the user saying they want
// g2g to own the branch, and there is nothing else to say it with.
type G2GSelector struct {
	Service graph.Service
}

func (s G2GSelector) Source() Source { return SourceG2G }

// Describes reports whether the store holds an edge for the branch. It reads
// one small file and never runs anything.
func (s G2GSelector) Describes(ctx context.Context, branch string) (bool, error) {
	if s.Service.Store == nil {
		return false, nil
	}
	adopted, err := s.Service.Store.Load(ctx)
	if err != nil {
		return false, err
	}
	return adopted.Tracked(branch), nil
}

// Select returns the selected branches as the ordered stack a command acts on:
// the first is the base, and the rest hang from it.
//
// The scope it was handed is the scope it resolves. Hardcoding one here made
// --scope and --no-stack silently do nothing for every branch this record
// describes, which is the common case because this record is consulted first.
func (s G2GSelector) Select(ctx context.Context, selection Selection, command string) (Snapshot, error) {
	scope := selection.EffectiveScope()
	// The shape and nothing else: what each branch's contents are doing is not
	// what a selection is asked, and asking it cost more than everything else.
	discovery, err := s.Service.Structure(ctx, graph.Selection{Branch: selection.Branch, Scope: scope})
	if err != nil {
		return Snapshot{}, err
	}
	forest := discovery.Graph.Shape()
	// A branch the store places nowhere has no base to sit on. It is asked of
	// the forest rather than of the selection's length: branch and subtree
	// scopes select the target alone and hang it from its parent, so counting
	// two branches refused both on every branch the store records.
	if !forest.Knows(discovery.Target) {
		return Snapshot{}, shape.ErrNoRecordedParent
	}
	if err := validateSelectionIsSafe(discovery.Branches, command); err != nil {
		return Snapshot{}, err
	}
	hangsFrom, within, err := forest.Hangs(discovery.Branches, discovery.Target, scope)
	if err != nil {
		return Snapshot{}, err
	}
	if err := s.requireLocal(ctx, discovery.Graph, append(slices.Clone(discovery.Branches), hangsFrom)); err != nil {
		return Snapshot{}, err
	}
	// The whole line of descent, not just the selection: revalidation compares
	// it so that structure moving above the base is noticed even when the
	// acted-on branches are unchanged. Leaving it empty here would have made a
	// g2g-owned selection revalidate more weakly than any other source's.
	ancestry, err := forest.Path(discovery.Target)
	if err != nil {
		return Snapshot{}, err
	}
	base, baseSource, err := selectBase(hangsFrom, discovery.Target, selection.Trunk)
	if err != nil {
		return Snapshot{}, err
	}
	// A base inside the selection opens it; one outside it is the branch the
	// selection grows from and is not itself acted on.
	branches := append([]string(nil), discovery.Branches...)
	if within {
		branches = branches[1:]
	}
	return Snapshot{
		Target:       discovery.Target,
		TargetSource: discovery.TargetSource,
		Ancestry:     ancestry,
		Base:         base,
		BaseSource:   baseSource,
		Branches:     branches,
		Scope:        scope,
		Parents:      selectionParents(forest, discovery.Branches, base),
	}, nil
}

// requireLocal refuses a selection naming a branch this checkout no longer has.
//
// The store outlives a branch deleted or renamed with plain Git, and every
// command that selects through here goes on to ask Git about what it selected.
// Graphite's selector has always refused a branch that is not local; this one
// passed the name on, so a status either failed on Git's own error or advised a
// pull request for a branch that does not exist. The two cases are repaired
// differently, which is why they are told apart: a stale edge is forgotten, and
// a stack left standing on a vanished parent is given a new one.
func (s G2GSelector) requireLocal(ctx context.Context, adopted graph.Graph, branches []string) error {
	local, err := s.Service.Git.LocalBranches(ctx)
	if err != nil {
		return err
	}
	present := branchSet(local)
	for _, branch := range branches {
		if present[branch] {
			continue
		}
		if adopted.Tracked(branch) {
			return fmt.Errorf("selected branch %q is recorded in the g2g graph but is no longer a local branch · run g2g untrack --branch %s to forget it", branch, branch)
		}
		return fmt.Errorf("the g2g graph records a stack on %q, which is no longer a local branch · record a new parent for what sits on it with g2g track --parent", branch)
	}
	return nil
}

// selectBase applies --trunk to a recorded path. A path has exactly one root,
// so the flag can only confirm the base g2g already derived; naming any other
// branch is refused rather than ignored, because silently using a different
// base than the one asked for is how a stack gets pushed at the wrong thing.
func selectBase(root, target, requested string) (string, string, error) {
	if requested == "" {
		return root, "g2g-owned graph", nil
	}
	if requested != root {
		return "", "", fmt.Errorf("requested trunk %q is not the base of %q's recorded path (%s) · run g2g track to record a different parent", requested, target, root)
	}
	return root, "--trunk", nil
}

// StoreCandidates completes from the branches g2g's own store records.
//
// It reads one small file and runs nothing, which is what makes it safe to ask
// on a keystroke — and why it can answer in a repository that has no Graphite,
// no GitHub remote, and no network.
type G2GCandidates struct {
	Service graph.Service
}

// Branches names every adopted branch. Roots are deliberately absent: nothing
// is recorded above them, so a command asked to act on one has no base.
func (c G2GCandidates) Branches(ctx context.Context) ([]string, error) {
	adopted, err := c.load(ctx)
	if err != nil {
		return nil, err
	}
	return adopted.Branches(), nil
}

// Trunks names the base of the target's recorded path, which is the only base
// a g2g-owned selection has.
func (c G2GCandidates) Trunks(ctx context.Context, target string) ([]string, error) {
	adopted, err := c.load(ctx)
	if err != nil {
		return nil, err
	}
	path, err := adopted.Path(target)
	if err != nil || len(path) < 2 {
		return nil, nil
	}
	return path[:1], nil
}

func (c G2GCandidates) load(ctx context.Context) (graph.Graph, error) {
	if c.Service.Store == nil {
		return graph.New(), nil
	}
	return c.Service.Store.Load(ctx)
}
