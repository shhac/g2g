package sync

import (
	"context"
	"errors"
	"fmt"

	"github.com/shhac/g2g/internal/repair"
	"github.com/shhac/g2g/internal/syncpoint"
	"github.com/shhac/g2g/internal/worktree"
)

// heldDecision is what a pull does about the branches it would move itself
// while another worktree holds them.
type heldDecision struct {
	// outcome is the branches this pull moves with their worktrees, and the
	// ones it leaves where they are.
	outcome worktree.Outcome
	// fixed are the branches the collection leaves alone, so nothing above
	// one is planned as though it had been taken.
	fixed map[string]bool
	// trunkLeft means the base stays where it is: it is not advanced, and
	// nothing is collected onto a trunk it does not have.
	trunkLeft bool
	refusal   repair.Note
}

// moves are the branches a pull moves itself: the base it advances and the
// branches it collects.
func moves(plan Plan, found collected) []string {
	branches := make([]string, 0, len(found.collections)+1)
	if plan.Advance || plan.Supersede {
		branches = append(branches, plan.Base)
	}
	for _, collection := range found.collections {
		branches = append(branches, collection.Branch)
	}
	return branches
}

// stuckBranches are the branches both sides moved, which a pull refuses.
func stuckBranches(found collected) []string {
	branches := make([]string, 0, len(found.stuck))
	for _, stuck := range found.stuck {
		branches = append(branches, stuck.Branch)
	}
	return branches
}

// decideHeld is what the policy makes of the held branches a pull would move
// itself, and of the ones it would refuse as diverged.
//
// Every one of them is this pull's to decide, before it asks the replay
// anything: the replay plans on top of where they will be, and deciding twice
// could only disagree. Refusing reads the same as the replay's own refusal.
// A branch both sides moved is refused anyway, so only leaving it gets
// anywhere: another worktree has it, and this pull need not touch it.
//
// A trunk left behind fixes every branch: each one's published version is
// built on the trunk the remote has, and taking it onto the trunk here would
// drag that trunk's commits in as the branch's own. Then the pull replays
// only what is stale against the trunk as it is here.
func decideHeld(plan Plan, found collected, held worktree.Held, selected []string) heldDecision {
	split := plan.Policy.Split(held, moves(plan, found))
	if len(split.Refuse) != 0 {
		return heldDecision{refusal: worktree.Refusal(split.Refuse)}
	}
	left := split.Leave
	for _, stuck := range found.stuck {
		if holder, taken := held[stuck.Branch]; taken && plan.Policy.Leaves(holder) {
			left = append(left, holder)
		}
	}
	if len(left) == 0 {
		return heldDecision{outcome: worktree.NewOutcome(split.Follow, nil)}
	}
	if note := discardsLeft(plan, found, left); note.Reason != "" {
		return heldDecision{refusal: note}
	}
	fixed := map[string]bool{}
	for _, holder := range left {
		fixed[holder.Branch] = true
	}
	if note := movedAcross(found.commits, fixed); note.Reason != "" {
		return heldDecision{refusal: note}
	}
	if !fixed[plan.Base] {
		return heldDecision{outcome: worktree.NewOutcome(split.Follow, left), fixed: fixed}
	}
	for _, branch := range selected {
		fixed[branch] = true
	}
	return heldDecision{outcome: worktree.NewOutcome(nil, left), fixed: fixed, trunkLeft: true}
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
				Ways:   []repair.Step{worktree.PutAway},
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
			Ways:   []repair.Step{worktree.PutAway},
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
		// Nothing has moved, so this is not the worktree left behind by a
		// branch that did -- which reports itself as done.
		return fmt.Errorf("%s was not moved, because %s could not be brought along with it: %w", branch, holder.Path, cause(err))
	}
	if err := move(ctx, branch, to); err != nil {
		var stranded *worktree.NotBrought
		if back := worktree.Bring(ctx, s.Git, holder, target, from); errors.As(back, &stranded) {
			return fmt.Errorf("%w · and %s could not be put back where its branch still is · run %s there", err, holder.Path, stranded.Finish())
		}
		return err
	}
	return nil
}

// cause is what stopped a worktree being brought along, without the claim
// that its branch had moved.
func cause(err error) error {
	var notBrought *worktree.NotBrought
	if errors.As(err, &notBrought) {
		return notBrought.Err
	}
	return err
}
