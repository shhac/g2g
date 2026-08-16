package graph

import (
	"context"
	"fmt"
	"strings"

	"github.com/shhac/gt2gh/internal/stack"
)

// Selector describes branches gt2gh's own store records, so the commands that
// project a stack onto GitHub can act on one.
//
// Adoption into the store is the authority claim, which is why this is
// consulted before Graphite: recording an edge is the user saying they want
// gt2gh to own the branch, and there is nothing else to say it with.
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

// Select returns the root-to-target path as the ordered stack a projection
// consumes: the root is the base, and everything above it is the stack.
//
// NoStack narrows to the selected branch and its ancestry, which is what the
// path scope already means, so the flag needs no separate handling here.
func (s Selector) Select(ctx context.Context, selection stack.Selection, command string) (stack.Snapshot, error) {
	discovery, err := s.Service.Discover(ctx, Selection{Branch: selection.Branch, Scope: ScopePath})
	if err != nil {
		return stack.Snapshot{}, err
	}
	if err := validatePath(discovery.Branches, command); err != nil {
		return stack.Snapshot{}, err
	}
	return stack.Snapshot{
		Target:       discovery.Target,
		TargetSource: discovery.TargetSource,
		Base:         discovery.Branches[0],
		BaseSource:   "gt2gh-owned graph",
		Branches:     append([]string(nil), discovery.Branches[1:]...),
	}, nil
}

// validatePath applies the same safety the Graphite path has always had: a
// selection needs a base to sit on, and no branch name may be readable as an
// option by the command it is passed to.
func validatePath(path []string, command string) error {
	if len(path) < 2 {
		return fmt.Errorf("selected branch has no recorded parent that can be used as a base")
	}
	for _, branch := range path {
		if strings.HasPrefix(branch, "-") {
			return fmt.Errorf("branch %q cannot be passed safely to %s", branch, command)
		}
	}
	return nil
}
