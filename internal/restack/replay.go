package restack

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/shhac/g2g/internal/diagnostic"
)

// applyInPlace journals a rewrite that needs no working tree before it moves
// anything, and forgets it once the rewrite is done or has been put back.
//
// The journal is what a failure that cannot be put back, or a process that
// dies between two replays, leaves for --abort. Every other command refuses
// while it exists, which is right while branches may have moved and wrong once
// they are back where they were.
func (s Service) applyInPlace(ctx context.Context, plan Plan, standing checkout) error {
	if _, err := s.begin(ctx, plan, standing); err != nil {
		return err
	}
	if err := s.rewriteInPlace(ctx, plan, standing); err != nil {
		var back putBack
		if errors.As(err, &back) {
			if clearErr := s.Journal.Clear(ctx); clearErr != nil {
				return errors.Join(err, clearErr)
			}
		}
		return err
	}
	return s.Journal.Clear(ctx)
}

// rewriteInPlace collapses and replays without touching the checkout until the
// end, and is all or nothing.
//
// Independent roots are separate replays, and the engine's atomicity covers
// one invocation, so a failure part-way would otherwise leave some roots moved
// and others not, reported as "Not applied" over refs that had moved. So it
// notes where every branch it may move points before it starts, and on any
// failure before the checkout is touched puts them back and says so.
func (s Service) rewriteInPlace(ctx context.Context, plan Plan, standing checkout) error {
	before, err := s.tips(ctx, plan)
	if err != nil {
		return err
	}
	if err := s.replay(ctx, plan); err != nil {
		return s.putBack(ctx, before, err)
	}
	// A replay or a collapse moves refs without touching the index, so a user
	// standing on a rewritten branch would otherwise see changes they never
	// made — and be unable to switch away, because git refuses to overwrite
	// them.
	if err := s.resettle(ctx, standing); err != nil {
		return err
	}
	return s.recordStructure(ctx, plan.Discovery.Branches, plan.reparenting())
}

// replay collapses what has nothing left, then replays each independent root
// onto its own base and checks the outcome.
func (s Service) replay(ctx context.Context, plan Plan) error {
	if err := s.collapse(ctx, plan); err != nil {
		return err
	}
	groups := plan.groups()
	if len(groups) == 0 {
		return nil
	}
	diagnostic.Event(ctx, "restack.replay", diagnostic.Field{Key: "branches", Value: strings.Join(plan.Replaying(), ",")})
	for _, group := range groups {
		if err := s.Git.Replay(ctx, group.onto(), group.ranges()); err != nil {
			return err
		}
	}
	return s.verify(ctx, plan)
}

// tips is where every branch a plan may move points now, which is what putting
// them back restores. It is read at apply time rather than taken from the
// plan, because a caller may have moved a branch in between: sync collects
// before it replays, and undoing a failed replay must not undo that too.
func (s Service) tips(ctx context.Context, plan Plan) (map[string]string, error) {
	tips := make(map[string]string, len(plan.Steps))
	for _, step := range plan.Steps {
		tip, err := s.Git.Resolve(ctx, step.Branch)
		if err != nil {
			return nil, err
		}
		tips[step.Branch] = tip
	}
	return tips, nil
}

// putBack is a failed in-place rewrite that restored every branch it had
// moved, so the repository is exactly as it was and saying so is true.
type putBack struct{ cause error }

func (p putBack) Error() string {
	return p.cause.Error() + " · nothing was changed: every branch it had moved was put back"
}

func (p putBack) Unwrap() error { return p.cause }

// putBack restores the tips an in-place rewrite started from. A restore that
// fails is reported as such rather than as a put back, because then some
// branches really have moved.
func (s Service) putBack(ctx context.Context, before map[string]string, cause error) error {
	diagnostic.Event(ctx, "restack.put_back", diagnostic.Field{Key: "branches", Value: strings.Join(slices.Sorted(maps.Keys(before)), ",")})
	for _, branch := range slices.Sorted(maps.Keys(before)) {
		now, err := s.Git.Resolve(ctx, branch)
		if err == nil && now == before[branch] {
			continue
		}
		if err == nil {
			err = s.Git.UpdateBranch(ctx, branch, before[branch])
		}
		if err != nil {
			return fmt.Errorf("%w · putting %s back at %s failed too, so branches may have moved: %v · run g2g restack --abort", cause, branch, before[branch], err)
		}
	}
	return putBack{cause: cause}
}
