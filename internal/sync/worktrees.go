package sync

import (
	"context"
	"errors"
	"fmt"

	"github.com/shhac/g2g/internal/repair"
	"github.com/shhac/g2g/internal/syncpoint"
	"github.com/shhac/g2g/internal/worktree"
)

// held decides each branch this pull would move itself -- the base it
// advances, the branches it collects -- while another worktree holds it.
//
// What it leaves is fixed for the collection that follows, so nothing above a
// left branch is planned as though it had been taken. A trunk left behind
// fixes every branch: each one's published version is built on the trunk the
// remote has, and taking it onto the trunk here would drag that trunk's
// commits in as the branch's own. Then the pull replays only what is stale
// against the trunk as it is here.
//
// Refuse decides nothing here. The moves reach the replay as pending, which
// refuses a held one exactly as it always has.
func (s Service) held(ctx context.Context, plan *Plan, c collecting, found collected) (worktree.Outcome, map[string]bool, repair.Note, error) {
	if plan.Policy == worktree.Refuse || plan.Policy == "" {
		return worktree.Outcome{}, nil, repair.Note{}, nil
	}
	moves := make([]string, 0, len(found.collections)+len(found.stuck)+1)
	if plan.Advance || plan.Supersede {
		moves = append(moves, plan.Base)
	}
	for _, collection := range found.collections {
		moves = append(moves, collection.Branch)
	}
	// A branch both sides moved is refused; one another worktree has is not
	// this pull's to decide, so leaving it is the way through.
	for _, stuck := range found.stuck {
		moves = append(moves, stuck.Branch)
	}
	held, err := worktree.Read(ctx, s.Git, moves)
	if err != nil || len(held) == 0 {
		return worktree.Outcome{}, nil, repair.Note{}, err
	}
	follow, left := make([]worktree.Holder, 0), make([]worktree.Holder, 0)
	fixed := map[string]bool{}
	for _, holder := range held.Of(moves) {
		switch {
		case plan.Policy.Moves(holder):
			follow = append(follow, holder)
		case plan.Policy.Leaves(holder):
			left = append(left, holder)
			fixed[holder.Branch] = true
		}
	}
	if len(left) == 0 {
		return worktree.NewOutcome(follow, nil), nil, repair.Note{}, nil
	}
	if note := discardsLeft(*plan, found, left); note.Reason != "" {
		return worktree.Outcome{}, nil, note, nil
	}
	if note := movedAcross(found.commits, fixed); note.Reason != "" {
		return worktree.Outcome{}, nil, note, nil
	}
	if fixed[plan.Base] {
		plan.Advance, plan.Supersede = false, false
		for _, branch := range c.branches {
			fixed[branch] = true
		}
		follow = nil
	}
	return worktree.NewOutcome(follow, left), fixed, repair.Note{}, nil
}

// discardsLeft refuses a --take that would discard commits from a branch this
// pull leaves. It was asked for by name, and quietly not doing it reads as
// done.
func discardsLeft(plan Plan, found collected, left []worktree.Holder) repair.Note {
	for _, holder := range left {
		discards := holder.Branch == plan.Base && len(plan.DiscardsBase) != 0
		for _, collection := range found.collections {
			discards = discards || (collection.Branch == holder.Branch && len(collection.Discards) != 0)
		}
		if discards {
			return repair.Note{
				Reason: fmt.Sprintf("--take would replace %s, and %s", holder.Branch, holder.Why()),
				Ways:   []repair.Step{{Effect: "commit or put those changes away there, then run this again"}},
			}
		}
	}
	return repair.Note{}
}

// movedAcross refuses a commit that moved between a branch this pull leaves
// and one it takes. Taking one side alone either drops the commit from the
// branch it left with nowhere here to land, or lands it while the branch it
// left still has it.
func movedAcross(commits []Drop, fixed map[string]bool) repair.Note {
	for _, drop := range commits {
		if drop.Kind != syncpoint.Moved || fixed[drop.Branch] == fixed[drop.To] {
			continue
		}
		stays := drop.Branch
		if fixed[drop.To] {
			stays = drop.To
		}
		return repair.Note{
			Reason: fmt.Sprintf("a commit moved from %s to %s, and %s is held by another worktree with changes, so taking one side of the move without the other would lose it or duplicate it", drop.Branch, drop.To, stays),
			Ways:   []repair.Step{{Effect: "commit or put those changes away there, then run this again"}},
		}
	}
	return repair.Note{}
}

// moveFollowing moves a branch this pull takes or advances and, when another
// worktree holding it is followed, brings that worktree along.
//
// The tree first, as for the checkout here: it is the step that can refuse,
// and refusing before the ref moves leaves nothing having happened. A ref
// that then will not move puts the tree back.
func (s Service) moveFollowing(ctx context.Context, plan Plan, branch, to string, move func(context.Context, string, string) error) error {
	holder, follows := plan.Worktrees.Follows(branch)
	if !follows {
		return move(ctx, branch, to)
	}
	from, err := s.Git.Resolve(ctx, branch)
	if err != nil {
		return err
	}
	target, err := s.Git.Resolve(ctx, to)
	if err != nil {
		return err
	}
	if err := worktree.Bring(ctx, s.Git, holder, from, target); err != nil {
		return err
	}
	if err := move(ctx, branch, to); err != nil {
		return errors.Join(err, worktree.Bring(ctx, s.Git, holder, target, from))
	}
	return nil
}
