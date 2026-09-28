package land

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/shhac/g2g/internal/shape"
	"github.com/shhac/g2g/internal/stack"
)

// publish pushes exactly this branch, through push's own plan.
//
// Through the service rather than straight to git: push refuses a branch the
// remote has moved on, and a lease built here from tips read here would always
// match its own reading and overwrite whatever a reviewer had pushed -- and
// then merge it.
//
// It says whether it pushed, because a push is on the remote whatever happens
// after it.
func (s Service) publish(ctx context.Context, plan Plan, step Step) (bool, error) {
	// Landing forgets each branch as it lands, so by the time this runs the
	// path from the trunk holds exactly the branch being published. More than
	// one means an earlier cycle did not tidy up, and the extra one has already
	// merged -- pushing it would put it back.
	return s.pushPinned(ctx, plan, step.Branch, step.RemoteTip, func(branches []string) error {
		if len(branches) != 1 || branches[0] != step.Branch {
			return fmt.Errorf("publishing %s would also push %s, which has already landed", step.Branch, strings.Join(branches, ", "))
		}
		return nil
	})
}

// republish publishes a branch the descent replayed and did not land, once it
// is over. Its path from the trunk is what is left above the landed branches,
// and every branch on it must be one this descent is republishing: a parent
// that was never published is not this command's to publish.
func (s Service) republish(ctx context.Context, plan Plan, above Republish) (bool, error) {
	return s.pushPinned(ctx, plan, above.Branch, above.RemoteTip, func(branches []string) error {
		for _, branch := range branches {
			if !slices.ContainsFunc(plan.Republish, func(r Republish) bool { return r.Branch == branch }) {
				return fmt.Errorf("publishing %s would also publish %s, which is not on %s · run g2g push --branch %s --scope path --apply if you mean both", above.Branch, branch, plan.Options.Remote, above.Branch)
			}
		}
		return nil
	})
}

// pushPinned publishes branch through push, provided the remote still holds
// planned, and says whether it pushed anything.
//
// Whether a branch's published version is its own or somebody else's is the
// one question push cannot answer here. After a replay the remote holds
// commits the branch no longer has, which is exactly the shape of a reviewer's
// fix, and push refuses both. What tells them apart is whether the remote
// still holds what the plan saw: land moves these refs itself and knows what it
// left there.
func (s Service) pushPinned(ctx context.Context, plan Plan, branch, planned string, allowed func([]string) error) (bool, error) {
	tips, err := s.Git.RemoteTips(ctx, plan.Options.Remote, []string{branch})
	if err != nil {
		return false, err
	}
	if tips[branch] != planned {
		return false, fmt.Errorf("%s has moved on %s since this was planned, so it carries work this descent has not seen · fetch and reconcile it, then rerun", plan.Options.Remote, branch)
	}
	// The path, not the branch alone: push needs a base to compare against and
	// a single-branch selection has no ancestry to take one from.
	published, err := s.Pusher.Plan(ctx, stack.Selection{Branch: branch, Trunk: plan.Trunk, Scope: shape.ScopePath}, plan.Options.Remote)
	if err != nil {
		return false, err
	}
	if err := allowed(published.Branches); err != nil {
		return false, err
	}
	if published.NothingToPublish() {
		return false, nil
	}
	// push's own refusal is deliberately not consulted: it is the "the remote
	// has moved" check, which the comparison above has just answered more
	// precisely. Its lease still guards the push itself, pinned to the tips it
	// read, so a ref that moves between here and the push is still rejected.
	if err := s.Pusher.Execute(ctx, published); err != nil {
		return false, err
	}
	return true, nil
}
