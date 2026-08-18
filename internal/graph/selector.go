package graph

import (
	"context"
	"fmt"

	"github.com/shhac/g2g/internal/stack"

	"github.com/shhac/g2g/internal/subprocess"
)

// Selector describes branches g2g's own store records, so the commands that
// project a stack onto GitHub can act on one.
//
// Adoption into the store is the authority claim, which is why this is
// consulted before Graphite: recording an edge is the user saying they want
// g2g to own the branch, and there is nothing else to say it with.
type Selector struct {
	Service Service
}

func (s Selector) Source() stack.Source { return stack.SourceG2G }

// Describes reports whether the store holds an edge for the branch. It reads
// one small file and never runs anything.
func (s Selector) Describes(ctx context.Context, branch string) (bool, error) {
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
func (s Selector) Select(ctx context.Context, selection stack.Selection, command string) (stack.Snapshot, error) {
	scope := selection.EffectiveScope()
	discovery, err := s.Service.Discover(ctx, Selection{Branch: selection.Branch, Scope: scope})
	if err != nil {
		return stack.Snapshot{}, err
	}
	if err := validatePath(discovery.Branches, command); err != nil {
		return stack.Snapshot{}, err
	}
	shape := discovery.Graph.shape()
	hangsFrom, within, err := shape.Hangs(discovery.Branches, discovery.Target, scope)
	if err != nil {
		return stack.Snapshot{}, err
	}
	// The whole line of descent, not just the selection: revalidation compares
	// it so that structure moving above the base is noticed even when the
	// acted-on branches are unchanged. Leaving it empty here would have made a
	// g2g-owned selection revalidate more weakly than any other source's.
	ancestry, err := shape.Path(discovery.Target)
	if err != nil {
		return stack.Snapshot{}, err
	}
	base, baseSource, err := selectBase(hangsFrom, discovery.Target, selection.Trunk)
	if err != nil {
		return stack.Snapshot{}, err
	}
	// A base inside the selection opens it; one outside it is the branch the
	// selection grows from and is not itself acted on.
	branches := append([]string(nil), discovery.Branches...)
	if within {
		branches = branches[1:]
	}
	return stack.Snapshot{
		Target:       discovery.Target,
		TargetSource: discovery.TargetSource,
		Ancestry:     ancestry,
		Base:         base,
		BaseSource:   baseSource,
		Branches:     branches,
		Scope:        scope,
		// Shape, restricted to the selection, so a renderer knows where this
		// selection's own roots are. A linear selection yields edges that form
		// a chain, which renders exactly as it always has.
		Parents: shape.Restrict(discovery.Branches),
	}, nil
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
type StoreCandidates struct {
	Service Service
}

// Branches names every adopted branch. Roots are deliberately absent: nothing
// is recorded above them, so a command asked to act on one has no base.
func (c StoreCandidates) Branches(ctx context.Context) ([]string, error) {
	adopted, err := c.load(ctx)
	if err != nil {
		return nil, err
	}
	return adopted.Branches(), nil
}

// Trunks names the base of the target's recorded path, which is the only base
// a g2g-owned selection has.
func (c StoreCandidates) Trunks(ctx context.Context, target string) ([]string, error) {
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

func (c StoreCandidates) load(ctx context.Context) (Graph, error) {
	if c.Service.Store == nil {
		return New(), nil
	}
	return c.Service.Store.Load(ctx)
}

// validatePath applies the same safety the Graphite path has always had: a
// selection needs a base to sit on, and no branch name may be readable as an
// option by the command it is passed to.
func validatePath(path []string, command string) error {
	if len(path) < 2 {
		return fmt.Errorf("selected branch has no recorded parent that can be used as a base")
	}
	for _, branch := range path {
		if subprocess.OptionLike(branch) {
			return fmt.Errorf("branch %q cannot be passed safely to %s", branch, command)
		}
	}
	return nil
}
