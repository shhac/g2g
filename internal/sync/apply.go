package sync

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/shhac/g2g/internal/diagnostic"
	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/syncpoint"
)

// Apply performs the sequence and stops at the first step that cannot finish.
//
// It reports how far it got rather than unwinding: a replay that stops on a
// conflict is resumable, and undoing the fetch and the fast-forward would
// throw away work the user then has to redo.
func (s Service) Apply(ctx context.Context, plan Plan) error {
	if plan.Blocked() != "" {
		return fmt.Errorf("cannot sync: %s", plan.Blocked())
	}
	moved := make([]string, 0, len(plan.Collect)+1)
	stop := func(err error) error {
		if len(moved) == 0 {
			return err
		}
		return &Stopped{Moved: moved, Err: err}
	}
	if plan.Advance || plan.Supersede {
		diagnostic.Event(ctx, "sync.advance",
			diagnostic.Field{Key: "base", Value: plan.Base},
			diagnostic.Field{Key: "supersede", Value: fmt.Sprintf("%t", plan.Supersede)},
		)
		move := s.Git.FastForward
		if plan.Supersede {
			// The published trunk is not a descendant of this one, so this is a
			// reset. FastForward would refuse it, correctly.
			move = s.Git.ResetBranch
		}
		if err := move(ctx, plan.Base, localgit.IsolatedRef(plan.Remote, plan.Base)); err != nil {
			return err
		}
		moved = append(moved, plan.Base)
	}
	// Before the replay, because the replay works from the tips these leave
	// behind: a reviewer's commit has to be on the branch before it is moved.
	for _, collection := range plan.Collect {
		diagnostic.Event(ctx, "sync.collect_branch",
			diagnostic.Field{Key: "branch", Value: collection.Branch},
			diagnostic.Field{Key: "superseded", Value: fmt.Sprintf("%t", collection.Superseded)},
		)
		move := s.Git.FastForward
		if collection.Superseded {
			// Not a fast-forward: the published version is not a descendant of
			// this one, so FastForward would refuse it, correctly.
			move = s.Git.ResetBranch
		}
		if err := move(ctx, collection.Branch, collection.To); err != nil {
			return stop(err)
		}
		moved = append(moved, collection.Branch)
		// The version taken begins on the parent as it was published, and the
		// replay planned it from there. Recording that is what lets a replay
		// that stops on a conflict be continued: --continue plans again from
		// the record, and the old fork point is not in this version.
		if collection.Begins != "" {
			if err := s.Graph.Refork(ctx, collection.Branch, collection.Begins); err != nil {
				return stop(err)
			}
		}
	}
	// Before the replay for the same reason as a taken branch's: a branch a
	// commit moved into begins at its parent's published tip now, and a
	// replay that sits it there unchanged records no fork point of its own.
	for _, branch := range slices.Sorted(maps.Keys(plan.Starts)) {
		if err := s.Graph.Refork(ctx, branch, plan.Starts[branch]); err != nil {
			return stop(err)
		}
	}
	if len(plan.Restack.Steps) != 0 {
		if err := s.Restack.Apply(ctx, plan.Restack); err != nil {
			return stop(err)
		}
	}
	s.recordAgreed(ctx, plan)
	return nil
}

// recordAgreed notes that every selected branch the remote holds has been
// reconciled with what it held when this was planned: taken, replayed onto,
// or left ahead of it as work to push. Only a pull that finished records it.
func (s Service) recordAgreed(ctx context.Context, plan Plan) {
	recorder, ok := s.Git.(syncpoint.ReadRecorder)
	if !ok {
		return
	}
	for _, branch := range slices.Sorted(maps.Keys(plan.Published)) {
		local, err := s.Git.Resolve(ctx, branch)
		if err != nil {
			continue
		}
		dropped := make([]string, 0)
		for _, drop := range plan.Drops {
			if drop.Branch == branch {
				dropped = append(dropped, drop.Commit)
			}
		}
		s.agree(ctx, recorder, plan.Remote, branch, localgit.SyncPoint{Tip: plan.Published[branch], Local: local, Command: "pull", Dropped: dropped})
	}
}

// Stopped is a sync that moved some branches and then failed.
//
// The trunk it advanced and the branches it brought down stay where they are:
// they are what the remote holds, and putting them back would only put them
// behind again. A replay that failed and put its own refs back says "nothing
// was changed" about the replay, which was true of the replay and not of the
// run.
type Stopped struct {
	// Moved are the branches brought to their published versions, trunk
	// first, before the step that failed.
	Moved []string
	Err   error
}

func (s *Stopped) Error() string {
	return fmt.Sprintf("stopped after bringing %s up to date: %v", strings.Join(s.Moved, ", "), s.Err)
}

func (s *Stopped) Unwrap() error { return s.Err }
