package sync

import (
	"context"
	"fmt"

	"github.com/shhac/g2g/internal/diagnostic"
	"github.com/shhac/g2g/internal/graph"
)

// PlanTrunk advances only the selected stack's recorded trunk. A branch with
// no recorded ancestry must itself be an evidenced trunk; it is never guessed.
func (s Service) PlanTrunk(ctx context.Context, selection graph.Selection, remote string) (Plan, error) {
	discovery, err := s.Graph.Structure(ctx, graph.Selection{Branch: selection.Branch, Scope: graph.ScopePath})
	if err != nil {
		return Plan{}, err
	}
	base := discovery.Branches[0]
	known := discovery.Graph.IsTrunk(base) || len(discovery.Graph.Children(base)) != 0
	if !known && s.Graph.Trunks != nil {
		defaultBranch, err := s.Graph.Trunks.DefaultBranch(ctx, remote)
		if err != nil {
			return Plan{}, err
		}
		known = defaultBranch == base
	}
	if !known {
		return Plan{}, fmt.Errorf("%q has no recorded trunk · name one with --branch, or declare it with g2g track --branch %s --as-trunk", discovery.Target, base)
	}
	return s.Plan(ctx, graph.Selection{Branch: base, Scope: graph.ScopeBranch}, remote, TakeNothing)
}

// RevalidateTrunk rediscovers the selected trunk and checks its remote before
// applying the preview, using the same boundary as an ordinary pull.
func (s Service) RevalidateTrunk(ctx context.Context, selection graph.Selection, remote string, preview Plan) (Plan, error) {
	current, err := s.PlanTrunk(ctx, selection, remote)
	if err != nil {
		return Plan{}, err
	}
	if err := diagnostic.Revalidated(ctx, "sync", "plan", current.Equal(preview)); err != nil {
		return Plan{}, err
	}
	return current, nil
}
