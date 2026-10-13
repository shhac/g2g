package restack

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/shhac/g2g/internal/diagnostic"
	"github.com/shhac/g2g/internal/repair"
	"github.com/shhac/g2g/internal/worktree"
)

// A rewrite moves a ref without checking anything out, so nothing stopped it
// from moving a branch another worktree held. Git updated the ref; that
// worktree's index and working tree still described the old commit, and its
// next git status reported staged changes nobody made. The preview said
// "applies without touching your working tree or checked-out branch" while
// doing it, which was true of the worktree it ran in and false of the other.
// What a rewrite does about such a branch is the caller's --worktrees policy,
// decided here once per branch.

// own is every branch this rewrite moves itself, which is what it decides
// about.
//
// Asking of the whole selection refused a path restack because the trunk,
// which it never touches, was checked out elsewhere. A branch the caller moves
// is the caller's to decide -- sync advances the trunk and takes published
// versions, and has decided about each before it asks -- and deciding again
// here could only disagree with it. Absorbing re-records fork points and moves
// no ref at all, and a collapse onto the commit a branch already points at
// moves nothing either.
func own(steps []Step, absorb bool, pending Pending) []string {
	branches := make([]string, 0, len(steps))
	if absorb {
		return branches
	}
	for _, step := range steps {
		if _, callers := pending.Tips[step.Branch]; callers || (step.Collapses && step.Head == step.Base) {
			continue
		}
		branches = append(branches, step.Branch)
	}
	return branches
}

// leaving is what the policy makes of the held branches this rewrite would
// move: the ones it leaves where they are, or why it refuses.
func leaving(plan Plan, split worktree.Split) ([]worktree.Holder, repair.Note) {
	if len(split.Refuse) != 0 {
		return nil, worktree.Refusal(split.Refuse)
	}
	if len(split.Leave) != 0 && plan.Onto.Reparents() {
		return nil, ontoLeaves(split.Leave, plan.Onto.Parent)
	}
	return split.Leave, repair.Note{}
}

// following is every branch this rewrite moves along with another worktree:
// its own that the policy follows, and the caller's it is told to.
func following(plan Plan, held worktree.Held, steps []Step, pending Pending) []worktree.Holder {
	follow := plan.Policy.Split(held, own(steps, plan.Absorb, pending)).Follow
	for _, holder := range pending.Follow {
		if !plan.Absorb && slices.ContainsFunc(steps, func(step Step) bool { return step.Branch == holder.Branch }) {
			follow = append(follow, holder)
		}
	}
	return follow
}

// ontoLeaves refuses an --onto that would leave a branch it moves. Recording
// the new parent for a branch still sitting on the old one describes a stack
// that does not exist.
func ontoLeaves(left []worktree.Holder, parent string) repair.Note {
	return repair.Note{
		Reason: fmt.Sprintf("--onto %s has to move %s, and %s", parent, left[0].Branch, left[0].Why()),
		Ways:   []repair.Step{worktree.PutAway, {Effect: "run it from that worktree instead"}},
	}
}

// followedHere refuses a rewrite that would have to check out a branch
// another worktree holds.
//
// A rewrite that applies cleanly moves refs and brings the worktree along
// afterwards. One that conflicts, or cannot be previewed, rebases in this
// working tree, branch by branch, and Git will not check out a branch another
// worktree has.
func (p Plan) followedHere() repair.Note {
	if !p.NeedsWorkingTree() {
		return repair.Note{}
	}
	here := make([]string, 0)
	for _, step := range p.Steps {
		if holder, follows := p.Worktrees.Follows(step.Branch); follows {
			here = append(here, fmt.Sprintf("%s (%s)", holder.Branch, holder.Path))
		}
	}
	if len(here) == 0 {
		return repair.Note{}
	}
	return repair.Note{
		Reason: "this rewrite has to run in a working tree, a branch at a time, and another worktree has " + strings.Join(here, ", ") + " checked out",
		Ways: []repair.Step{
			{Effect: "run it from that worktree, where it can be checked out and a conflict resolved"},
			{Effect: "run it again with --worktrees skip, which leaves those branches where they are and rewrites the rest"},
		},
	}
}

// journalFollowed notes each worktree this plan brings along, with the commit
// its index describes now, before anything moves. A worktree already noted
// keeps what it has: that is still what its index describes.
func (s Service) journalFollowed(ctx context.Context, record *Record, plan Plan) error {
	for _, holder := range plan.Worktrees.Follow {
		if _, noted := record.Followed[holder.Branch]; noted {
			continue
		}
		tip, err := s.Git.Resolve(ctx, holder.Branch)
		if err != nil {
			return err
		}
		if record.Followed == nil {
			record.Followed = map[string]FollowedTree{}
		}
		record.Followed[holder.Branch] = FollowedTree{Path: holder.Path, Tip: tip}
	}
	return nil
}

// settleFollowed brings each noted worktree to where its branch points now,
// noting each one as it goes, so a failure part-way leaves the journal saying
// which are still behind.
func (s Service) settleFollowed(ctx context.Context, record *Record) error {
	for _, branch := range slices.Sorted(maps.Keys(record.Followed)) {
		tree := record.Followed[branch]
		now, err := s.Git.Resolve(ctx, branch)
		if err != nil {
			return err
		}
		if now == tree.Tip {
			continue
		}
		diagnostic.Event(ctx, "restack.follow",
			diagnostic.Field{Key: "branch", Value: branch},
			diagnostic.Field{Key: "from", Value: tree.Tip},
			diagnostic.Field{Key: "to", Value: now},
		)
		if err := worktree.Bring(ctx, s.Git, worktree.Holder{Branch: branch, Path: tree.Path}, tree.Tip, now); err != nil {
			return err
		}
		tree.Tip = now
		record.Followed[branch] = tree
		if err := s.Journal.Save(ctx, *record); err != nil {
			return err
		}
	}
	return nil
}
