package align

import (
	"context"
	"sort"

	"github.com/shhac/g2g/internal/graphite"
)

// PlanAdopt works out what Graphite declares that the g2g graph does not.
//
// It is additive by construction. Graphite declares each parent, so this is not
// the guess `track` refuses to make — but a branch g2g already records
// differently is a disagreement, not a gap, and gets refused rather than
// resolved.
func (s Service) PlanAdopt(ctx context.Context) (AdoptPlan, error) {
	adopted, forest, err := s.both(ctx)
	if err != nil {
		return AdoptPlan{}, err
	}
	local, err := s.Git.LocalBranches(ctx)
	if err != nil {
		return AdoptPlan{}, err
	}
	return s.planAdoptions(ctx, adopted, declaredEdges(forest), local, s.graphiteRecord())
}

func (s Service) graphiteRecord() record {
	return record{
		from:   FromGraphite,
		answer: "take Graphite's answer",
		forkPoint: func(ctx context.Context, adoption Adoption) (string, error) {
			return s.Git.Resolve(ctx, adoption.Parent)
		},
	}
}

// declaredEdges is every edge Graphite declares, parents first.
func declaredEdges(forest graphite.Forest) []Adoption {
	edges := make([]Adoption, 0, len(forest.Parents))
	for _, branch := range declaredOrder(forest) {
		parent := forest.Parents[branch]
		if parent == "" {
			// A Graphite root has no edge to adopt. It becomes a g2g trunk
			// only if something is adopted onto it.
			continue
		}
		edges = append(edges, Adoption{Branch: branch, Parent: parent})
	}
	return edges
}

// declaredOrder walks Graphite's forest from its roots down, so a parent is
// always considered before the branches that name it.
//
// It seeds from the roots the display named rather than from the ones parentage
// implies. Those agree today, because the parser maps a root to an empty
// parent — but which one is authoritative is a real choice, and this is reading
// Graphite's own claim rather than re-deriving it.
func declaredOrder(forest graphite.Forest) []string {
	roots := append([]string(nil), forest.Roots...)
	sort.Strings(roots)
	ordered := forest.Shape().BreadthFirst(roots)
	seen := make(map[string]bool, len(ordered))
	for _, branch := range ordered {
		seen[branch] = true
	}
	// A forest whose display named no root still has branches worth reporting.
	for _, branch := range forest.Branches() {
		if !seen[branch] {
			ordered = append(ordered, branch)
		}
	}
	return ordered
}
