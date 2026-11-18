package restack

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/shhac/g2g/internal/diagnostic"
	localgit "github.com/shhac/g2g/internal/git"
)

// rebase runs the resumable engine and journals enough to undo the whole
// operation, which git cannot do because it only restores the invocation it is
// running.
//
// The journal comes first: a collapse moves refs too, and a branch moved with
// no record of where it was is one --abort cannot put back.
func (s Service) rebase(ctx context.Context, plan Plan, standing checkout) error {
	record, err := s.begin(ctx, plan, standing)
	if err != nil {
		return err
	}
	diagnostic.Event(ctx, "restack.rebase", diagnostic.Field{Key: "branches", Value: strings.Join(plan.Branches(), ",")})
	if err := s.collapseAndRebase(ctx, plan, standing); err != nil {
		return s.neverStarted(ctx, record, standing, err)
	}
	return s.finish(ctx, record)
}

// neverStarted forgets the journal of a rebase that failed before it moved
// anything, and says so.
//
// Stopping on a conflict leaves Git part-way through a rebase, which is what
// the journal is for. A rebase Git could not start at all -- its index
// locked, its directory not writable -- leaves every branch and the checkout
// where they were, and a journal over that refused every other command for a
// restack with nothing to continue and nothing to abort, under advice to
// resolve files when none were unmerged. Anything this cannot confirm keeps
// the journal: a cancelled run may have killed Git mid-rewrite, and a branch
// or a checkout that has moved is exactly what --abort puts back.
func (s Service) neverStarted(ctx context.Context, record Record, standing checkout, cause error) error {
	if ctx.Err() != nil {
		return cause
	}
	if inProgress, err := s.Git.RebaseInProgress(ctx); err != nil || inProgress {
		return cause
	}
	if now, err := s.standingOn(ctx); err != nil || now != standing {
		return cause
	}
	for branch, original := range record.Original {
		if tip, err := s.Git.Resolve(ctx, branch); err != nil || tip != original {
			return cause
		}
	}
	if err := s.Journal.Clear(ctx); err != nil {
		return errors.Join(cause, err)
	}
	return fmt.Errorf("%w · nothing was replayed: every branch is where it was, and no restack is left in progress", cause)
}

// collapseAndRebase moves what has nothing left, brings the checkout along,
// and rebases the rest.
//
// The resumable engine checks out as it goes, and it refuses to start over an
// index describing a commit its branch no longer points at -- which is exactly
// what collapsing the checked-out branch leaves. Running it straight after the
// collapse stopped every such restack on "your index contains uncommitted
// changes", with the phantom changes staged and the journal left behind.
func (s Service) collapseAndRebase(ctx context.Context, plan Plan, standing checkout) error {
	if err := s.collapse(ctx, plan); err != nil {
		return err
	}
	if err := s.resettle(ctx, standing); err != nil {
		return err
	}
	return s.rebaseEach(ctx, plan)
}

// rebaseEach replays one branch at a time, bottom-up.
//
// The engines model the work differently and are given it differently. Replay
// takes a root and everything above it at once and needs one shared origin.
// Rebase moves a
// single line of descent, so each branch is rebased onto the parent it now
// has, re-resolved after that parent has itself moved. Handing rebase the
// whole chain and asking --update-refs to carry the intermediate branches
// works on some versions and not others, and buys nothing that sequencing
// does not.
//
// Stopping part-way is the expected outcome, not a failure: the journal is
// already written and --continue re-derives what is left.
func (s Service) rebaseEach(ctx context.Context, plan Plan) error {
	for _, step := range plan.rewriting() {
		base, err := s.Git.Resolve(ctx, step.Parent)
		if err != nil {
			return err
		}
		if err := s.Git.Rebase(ctx, base, localgit.Range{From: step.ForkPoint, To: step.Branch}); err != nil {
			return err
		}
		if err := s.collapseAfter(ctx, plan, []string{step.Branch}); err != nil {
			return err
		}
	}
	return nil
}
