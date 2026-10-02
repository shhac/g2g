package prune

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/shhac/g2g/internal/diagnostic"
)

// Apply forgets the selected records, recording each evidenced surviving child
// on the branch below. Explicit local deletion uses the assessed tips as leases.
func (s Service) Apply(ctx context.Context, plan Plan) error {
	if plan.Blocked != "" {
		return fmt.Errorf("%s", plan.Blocked)
	}
	if plan.Nothing() {
		return nil
	}
	if s.Graph.Store == nil {
		return fmt.Errorf("prune service has no graph store")
	}
	adopted, err := s.Graph.Store.Load(ctx)
	if err != nil {
		return err
	}
	if !adopted.Equal(plan.Discovery.Graph) {
		return fmt.Errorf("the graph changed before pruning · preview again")
	}
	if len(plan.Delete) != 0 {
		if s.Cleaner == nil {
			return fmt.Errorf("local branch deletion is not configured")
		}
		now, err := s.Cleaner.ResolveAll(ctx, slices.Sorted(maps.Keys(plan.Tips)))
		if err != nil {
			return err
		}
		if !maps.Equal(now, plan.Tips) {
			return fmt.Errorf("branches or their bases changed before cleanup · preview again")
		}
	}
	diagnostic.Event(ctx, "prune.apply", diagnostic.Field{Key: "branches", Value: strings.Join(plan.Landed, ",")})
	for _, child := range slices.Sorted(maps.Keys(plan.Rehome)) {
		if adopted, err = adopted.Track(child, plan.Rehome[child]); err != nil {
			return err
		}
	}
	// Delete first, while the graph still records the branches. A failure
	// leaves a record that --forget-missing can recover, rather than hiding
	// a surviving local branch from the next cleanup.
	deleted := []string{}
	for _, branch := range slices.Sorted(maps.Keys(plan.Delete)) {
		if s.Cleaner == nil {
			return fmt.Errorf("local branch deletion is not configured")
		}
		if err := s.Cleaner.DeleteBranchAt(ctx, branch, plan.Delete[branch]); err != nil {
			return partial(plan, deleted, nil, err)
		}
		deleted = append(deleted, branch)
	}
	forgotten := append(slices.Clone(plan.Landed), plan.ForgottenMissing...)
	if err := s.Graph.Store.Save(ctx, adopted.Untrack(forgotten...)); err != nil {
		return partial(plan, deleted, nil, err)
	}
	if s.Graph.Refs == nil {
		return nil
	}
	for _, child := range slices.Sorted(maps.Keys(plan.Rehome)) {
		if err := s.Graph.Refs.PinForkPoint(ctx, child, plan.Rehome[child].ForkPoint); err != nil {
			return partial(plan, deleted, forgotten, err)
		}
	}
	// A fork point outlives the edge it belonged to unless it is released, and
	// a stale pin keeps objects reachable that nothing refers to any more.
	for _, branch := range forgotten {
		if err := s.Graph.Refs.UnpinForkPoint(ctx, branch); err != nil {
			return partial(plan, deleted, forgotten, err)
		}
	}
	return nil
}
