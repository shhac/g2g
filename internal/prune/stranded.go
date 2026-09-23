package prune

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/repair"
)

// placement is one child a prune would strand: the recorded parent it would
// lose, the branch it belongs on instead, and whether the selection asked
// about it. Where a rehome records it and what a refusal suggests are both
// read from here, so the two cannot name different branches.
type placement struct {
	child, parent, onto string
	selected            bool
}

// placements names each child that survives a landed parent, once each.
func placements(discovery graph.Discovery, landed []string) []placement {
	forgetting := make(map[string]bool, len(landed))
	for _, branch := range landed {
		forgetting[branch] = true
	}
	placed := make([]placement, 0)
	for _, branch := range landed {
		for _, child := range discovery.Graph.Children(branch) {
			if forgetting[child] {
				continue
			}
			placed = append(placed, placement{
				child:    child,
				parent:   branch,
				onto:     survivor(discovery.Graph, branch, forgetting),
				selected: slices.Contains(discovery.Branches, child),
			})
		}
	}
	return placed
}

// rehome decides, for each child a prune would strand, whether Git already
// answers where it belongs.
//
// It is the question track asks, answered the same way. After a pull, a child
// of a squash-merged branch has been replayed onto the trunk, so the trunk is
// an ancestor of it and recording it there moves nothing and guesses nothing —
// refusing sent everyone to run track by hand, on the commonest way a branch
// lands. Before a pull the trunk is not an ancestor, and that child is left to
// the refusal. So is one outside the selection, which nobody asked about.
func (s Service) rehome(ctx context.Context, placed []placement) (map[string]graph.Edge, []placement, error) {
	rehome := map[string]graph.Edge{}
	left := make([]placement, 0)
	for _, each := range placed {
		sits, err := s.sits(ctx, each)
		if err != nil {
			return nil, nil, err
		}
		if !sits {
			left = append(left, each)
			continue
		}
		// The fork point is where the branch below ends, as track records it:
		// everything under it is that branch's, and the child owns the rest.
		forkPoint, err := s.Graph.Git.Resolve(ctx, each.onto)
		if err != nil {
			return nil, nil, err
		}
		rehome[each.child] = graph.Edge{Parent: each.onto, ForkPoint: forkPoint, Origin: graph.OriginAncestry}
	}
	return rehome, left, nil
}

// sits reports a selected child Git shows already built on where it belongs.
func (s Service) sits(ctx context.Context, each placement) (bool, error) {
	if !each.selected || s.Graph.Git == nil {
		return false, nil
	}
	return s.Graph.Git.IsAncestor(ctx, each.onto, each.child)
}

// strandedNote is the refusal for the children nothing could place, and the
// ways past it.
//
// It used to offer widening the selection, which only helps when the child has
// landed too -- and the ordinary way to get here is a parent squash-merged,
// whose child has work of its own and is exactly why it survives. That sent
// people round in a circle. Recording each child on what the landed branch sat
// on is what leaves nothing to strand. After a pull the child already sits
// there and prune records it itself, so a child only reaches this refusal
// before one: pulling is the way out, and track is the way to say so by hand.
// Widening is still offered where a child lies outside the selection, because
// that child has not been asked about.
func strandedNote(left []placement) repair.Note {
	parents := make([]string, 0, len(left))
	ways := make([]repair.Step, 0, len(left)+3)
	if slices.ContainsFunc(left, func(each placement) bool { return each.selected }) {
		ways = append(ways, repair.Step{Command: "g2g pull --prune", Effect: "replay them onto the branch below, where prune then records them"})
	}
	for _, each := range left {
		if !slices.Contains(parents, each.parent) {
			parents = append(parents, each.parent)
		}
		ways = append(ways, repair.Step{
			Command: fmt.Sprintf("g2g track --branch %s --parent %s", each.child, each.onto),
			Effect:  fmt.Sprintf("record %s on %s, where g2g pull leaves it, then prune again", each.child, each.onto),
		})
	}
	if slices.ContainsFunc(left, func(each placement) bool { return !each.selected }) {
		ways = append(ways, repair.Step{Effect: "widen the selection with --scope, if those branches have landed too"})
	}
	return repair.Note{
		Reason: "forgetting " + strings.Join(parents, ", ") + " would strand branches recorded under them",
		Ways:   append(ways, repair.Step{Command: "g2g untrack", Effect: "forget them deliberately"}),
	}
}

// survivor is the nearest branch at or below this one that is not being
// forgotten, which is where a child of a landed branch belongs.
//
// Bounded by the recorded edges, because a record naming a cycle must end the
// walk rather than the command.
func survivor(recorded graph.Graph, branch string, forgetting map[string]bool) string {
	for range len(recorded.Edges) + 1 {
		if !forgetting[branch] {
			return branch
		}
		edge, tracked := recorded.Edges[branch]
		if !tracked {
			return branch
		}
		branch = edge.Parent
	}
	return branch
}
