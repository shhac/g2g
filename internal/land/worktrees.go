package land

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/repair"
	syncer "github.com/shhac/g2g/internal/sync"
	"github.com/shhac/g2g/internal/worktree"
)

// What a descent does about the branches other worktrees hold. The rule is
// internal/worktree's; what is land's is that a descent cannot leave part of
// itself behind, and deletes the branches it lands.

// heldElsewhere refuses a descent that would move a branch another worktree
// has checked out and cannot bring along: the trunk it advances, the branches
// it merges and deletes, and those above it that it replays.
//
// Each step's own sync refuses the same thing, but only once there is
// something to move — after the first merge, which does not come back. Asking
// sync up front found nothing while the trunk was level, so a descent with the
// trunk open in another worktree merged its bottom branch and then stopped.
//
// A clean worktree the policy follows is no obstacle: each step's pull brings
// it along. A branch the descent deletes is, because Git will not delete a
// branch checked out anywhere, and so is one the policy would leave, because
// a descent cannot leave part of itself behind.
func (s Service) heldElsewhere(ctx context.Context, recorded graph.Graph, plan Plan) (keeping, repair.Note) {
	cannotTell := func(err error) repair.Note {
		return repair.Note{Reason: "cannot tell whether another worktree has a branch this would move: " + err.Error()}
	}
	branches, err := moving(recorded, plan.Target, plan.Trunk)
	if err != nil {
		return keeping{}, cannotTell(err)
	}
	held, err := worktree.Read(ctx, s.Git, plan.Options.Worktrees, branches)
	if err != nil {
		return keeping{}, cannotTell(err)
	}
	split := plan.Options.Worktrees.Split(held, branches)
	blocked, follow, left := slices.Clone(split.Refuse), make([]worktree.Holder, 0, len(split.Follow)), make([]worktree.Holder, 0, len(split.Leave))
	for _, holder := range split.Leave {
		if plan.survives(holder.Branch) {
			left = append(left, holder)
			continue
		}
		blocked = append(blocked, holder)
	}
	for _, holder := range split.Follow {
		if plan.deletes(holder.Branch) {
			blocked = append(blocked, holder)
			continue
		}
		follow = append(follow, holder)
	}
	if len(blocked) == 0 {
		// A branch the descent deletes is stepped off first, and the trunk
		// cannot be checked out here while another worktree has it.
		trunkThere := slices.ContainsFunc(follow, func(holder worktree.Holder) bool { return holder.Branch == plan.Trunk })
		return keeping{follow: worktree.NewOutcome(follow, left), detach: trunkThere && plan.Options.DeleteLocal}, repair.Note{}
	}
	kept, err := s.keepsTrunk(ctx, recorded, plan, blocked, left)
	if err != nil {
		return keeping{}, cannotTell(err)
	}
	if kept.trunk {
		kept.follow = worktree.NewOutcome(follow, left)
		return kept, repair.Note{}
	}
	// Narrowing the selection is no way out here: a descent moves the whole
	// stack whatever was selected, because the replay after each merge takes
	// everything above it.
	return keeping{}, heldRefusal(plan, blocked)
}

// survives reports a branch above the descent: one it replays rather than
// lands. The policy may leave such a branch where its worktree has it --
// what the descent lands does not depend on it, and its own replay waits for
// that worktree, as a restack's would. Leaving one it lands, or the trunk it
// lands onto, would leave part of the descent itself.
func (p Plan) survives(branch string) bool {
	return branch != p.Trunk && !slices.Contains(p.Branches, branch)
}

// deletes reports a branch this descent deletes locally once it has landed.
func (p Plan) deletes(branch string) bool {
	return p.Options.DeleteLocal && slices.Contains(p.Branches, branch)
}

// heldRefusal says, for each branch in the way, why it cannot be moved, and
// the ways past them all.
func heldRefusal(plan Plan, blocked []worktree.Holder) repair.Note {
	why := make([]string, 0, len(blocked))
	ways := []repair.Step{worktree.SwitchAway}
	follows := false
	for _, holder := range blocked {
		switch {
		case plan.deletes(holder.Branch):
			why = append(why, fmt.Sprintf("%s is checked out in %s, and landing deletes it", holder.Branch, holder.Path))
		case holder.Clean:
			why = append(why, fmt.Sprintf("%s is checked out in %s", holder.Branch, holder.Path))
			follows = true
		default:
			why = append(why, fmt.Sprintf("%s is checked out in another worktree, and %s", holder.Branch, holder.Why()))
		}
	}
	if follows {
		ways = append(ways, repair.Step{Effect: "run it with --worktrees follow, which brings a clean worktree along"})
	}
	return repair.Note{Reason: strings.Join(why, "; "), Ways: ways}
}

// keeping is a descent leaving a trunk another worktree holds where it is,
// and whether this checkout steps off the branch it deletes onto the merge.
type keeping struct {
	trunk, detach bool
	follow        worktree.Outcome
}

// keepsTrunk is the one way past a trunk that cannot be moved. A single branch
// can merge without advancing the trunk here, provided no survivor would need
// replaying afterwards -- it has none, or every one is left where its own
// worktree has it -- and nothing but the trunk is in the way, and the policy
// would leave the trunk rather than refuse it. That is --worktrees skip with
// a clean trunk checkout and an agent's child branch in another.
func (s Service) keepsTrunk(ctx context.Context, recorded graph.Graph, plan Plan, blocked, left []worktree.Holder) (keeping, error) {
	if len(plan.Branches) != 1 {
		return keeping{}, nil
	}
	for _, child := range recorded.Children(plan.Target) {
		if !slices.ContainsFunc(left, func(holder worktree.Holder) bool { return holder.Branch == child }) {
			return keeping{}, nil
		}
	}
	if len(blocked) != 1 || blocked[0].Branch != plan.Trunk || !plan.Options.Worktrees.Leaves(blocked[0]) {
		return keeping{}, nil
	}
	// A detached checkout stands on no branch the descent deletes.
	current, _ := s.Git.CurrentBranch(ctx)
	return keeping{trunk: true, detach: plan.Options.DeleteLocal && current == plan.Target}, nil
}

// unplannedLeaves is what a pull would leave that the descent did not plan
// to: a branch whose worktree picked up changes since, or one the descent
// cannot do without.
func (p Plan) unplannedLeaves(synced syncer.Plan) []worktree.Holder {
	unplanned := make([]worktree.Holder, 0, len(synced.Worktrees.Leave))
	for _, holder := range synced.Worktrees.Leave {
		if !p.Worktrees.Leaves(holder.Branch) {
			unplanned = append(unplanned, holder)
		}
	}
	return unplanned
}

// leftBehind refuses a descent whose pull would leave a branch where it is
// that the descent did not plan to leave. It cannot: the branch above the
// next merge has to be on the trunk the merge produced.
func leftBehind(left []worktree.Holder) repair.Note {
	why := make([]string, 0, len(left))
	for _, holder := range left {
		why = append(why, holder.Branch+": "+holder.Why())
	}
	return repair.Note{
		Reason: "landing has to move branches another worktree holds and cannot bring along · " + strings.Join(why, "; "),
		Ways:   []repair.Step{worktree.PutAway},
	}
}
