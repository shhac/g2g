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

// WorktreeReader reports branches other worktrees have checked out.
//
// It is an optional capability rather than a method on Git: every fake in the
// tests implements Git, and a rewrite that could not ask was safe before this
// check existed and stays safe now.
type WorktreeReader interface {
	CheckedOutElsewhere(ctx context.Context) (map[string]string, error)
}

// moving is every branch whose ref will have moved by the time the rewrite is
// done.
//
// A caller's own moves count as much as the rewrite's: sync collects branches
// before replaying, and a worktree standing on one of those is stranded exactly
// as it would be by a replay. Pending is how a caller says so. Absorbing
// re-records fork points and moves no ref at all, and a collapse onto the
// commit a branch already points at moves nothing either.
func moving(steps []Step, absorb bool, pending Pending) []string {
	branches := slices.Sorted(maps.Keys(pending.Tips))
	if absorb {
		return branches
	}
	for _, step := range steps {
		if step.Collapses && step.Head == step.Base {
			continue
		}
		if !slices.Contains(branches, step.Branch) {
			branches = append(branches, step.Branch)
		}
	}
	return branches
}

// HeldElsewhere refuses to move a branch another worktree has checked out.
//
// A rewrite moves a ref without checking anything out, so nothing stopped it
// from moving a branch another worktree held. Git updated the ref; that
// worktree's index and working tree still described the old commit, so its next
// git status reported staged changes nobody made. The preview said "applies
// without touching your working tree or checked-out branch" while doing it,
// which was true of the worktree it ran in and false of the other.
//
// It is asked only of branches that will move. Asking it of the whole
// selection refused a path restack because the trunk, which it never touches,
// was checked out in another worktree. It is exported for a caller that moves
// a ref the plan does not: sync advances the trunk itself.
//
// A Git too old to list worktrees, or a failure to ask, is not a reason to
// refuse a rewrite that was fine before this check existed.
// It answers with structure rather than a sentence. A refusal here reaches a
// caller through sync and land as well as restack, and a machine reading the
// documented contract -- read repair, do not parse the prose -- was handed a
// null where the only two ways out were, on the one refusal a large checkout
// meets first.
//
// The ways out name no command, because the same refusal reaches commands that
// accept different scopes. It used to suggest g2g restack --scope path, which
// was the command that had just refused, and which sync does not accept.
func (s Service) HeldElsewhere(ctx context.Context, branches []string) (repair.Note, error) {
	if len(branches) == 0 {
		return repair.Note{}, nil
	}
	holder, ok := s.Git.(WorktreeReader)
	if !ok {
		return repair.Note{}, nil
	}
	elsewhere, err := holder.CheckedOutElsewhere(ctx)
	if err != nil || len(elsewhere) == 0 {
		return repair.Note{}, nil
	}
	held := make([]string, 0, len(branches))
	for _, branch := range branches {
		if path, taken := elsewhere[branch]; taken {
			held = append(held, fmt.Sprintf("%s (%s)", branch, path))
		}
	}
	if len(held) == 0 {
		return repair.Note{}, nil
	}
	return repair.Note{
		Reason: fmt.Sprintf("checked out in another worktree: %s · moving it would leave that worktree describing a commit it no longer has", strings.Join(held, ", ")),
		Ways: []repair.Step{
			{Effect: "switch that worktree to another branch, or close it"},
			{Effect: "select less with --branch or --scope, so nothing that has to move is checked out there"},
		},
	}, nil
}

// holding is what a plan does about the branches other worktrees hold: the
// outcome, or why it refuses.
type holding struct {
	outcome worktree.Outcome
	refusal repair.Note
}

// held decides each branch that would move while another worktree holds it,
// and replans without the ones the policy leaves where they are.
//
// Leaving a branch changes what sits on it: a child that was moving only
// because its parent was is now measured against where the parent actually
// is, and moves only if it is not already there. So the steps are worked out
// again with those branches fixed. The set that moves can only shrink, so
// nothing held that was not asked about can appear the second time.
//
// A Git that can say which branches are held but not whether their worktrees
// are clean gets the refusal every rewrite used to give, whatever was asked:
// following needs to know a worktree has nothing to lose.
func (s Service) held(ctx context.Context, plan Plan, steps []Step, pending Pending) ([]Step, holding, error) {
	branches := moving(steps, plan.Absorb, pending)
	if _, answers := s.Git.(worktree.Reader); !answers || plan.Policy == worktree.Refuse || plan.Policy == "" {
		note, err := s.HeldElsewhere(ctx, branches)
		return steps, holding{refusal: note}, err
	}
	held, err := worktree.Read(ctx, s.Git, branches)
	if err != nil || len(held) == 0 {
		return steps, holding{}, err
	}
	left := make([]worktree.Holder, 0)
	fixed := map[string]bool{}
	for _, holder := range held.Of(branches) {
		if !plan.Policy.Leaves(holder) {
			continue
		}
		// The caller moves these itself, so leaving them is the caller's
		// decision to make before asking: it has to know the branch stays
		// put to plan anything on top of it.
		if _, callers := pending.Tips[holder.Branch]; callers {
			note, err := s.HeldElsewhere(ctx, []string{holder.Branch})
			return steps, holding{refusal: note}, err
		}
		left = append(left, holder)
		fixed[holder.Branch] = true
	}
	if len(left) != 0 && plan.Onto.Reparents() {
		return steps, holding{refusal: ontoLeaves(left, plan.Onto.Parent)}, nil
	}
	if len(left) != 0 {
		if steps, err = s.steps(ctx, plan.Discovery, plan.Onto, pending, fixed); err != nil {
			return nil, holding{}, err
		}
		branches = moving(steps, plan.Absorb, pending)
	}
	follow := make([]worktree.Holder, 0)
	for _, holder := range held.Of(branches) {
		if plan.Policy.Moves(holder) {
			follow = append(follow, holder)
		}
	}
	return steps, holding{outcome: worktree.NewOutcome(follow, left)}, nil
}

// ontoLeaves refuses an --onto that would leave a branch it moves. Recording
// the new parent for a branch still sitting on the old one describes a stack
// that does not exist.
func ontoLeaves(left []worktree.Holder, parent string) repair.Note {
	return repair.Note{
		Reason: fmt.Sprintf("--onto %s has to move %s, and %s", parent, left[0].Branch, left[0].Why()),
		Ways: []repair.Step{
			{Effect: "commit or put those changes away there, then run this again"},
			{Effect: "run it from that worktree instead"},
		},
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
		if !slices.ContainsFunc(plan.Steps, func(step Step) bool { return step.Branch == holder.Branch }) {
			// The caller moves it, and brings it along itself.
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

// Holders is which of branches another worktree holds, and whether each of
// those worktrees is clean, for a caller deciding for itself what to move.
// A Git that can name the holders and not their state answers that none is
// clean, which is the answer that moves nothing.
func (s Service) Holders(ctx context.Context, branches []string) (worktree.Held, error) {
	if _, answers := s.Git.(worktree.Reader); answers {
		return worktree.Read(ctx, s.Git, branches)
	}
	reader, ok := s.Git.(WorktreeReader)
	if !ok || len(branches) == 0 {
		return worktree.Held{}, nil
	}
	elsewhere, err := reader.CheckedOutElsewhere(ctx)
	if err != nil {
		return nil, err
	}
	held := worktree.Held{}
	for _, branch := range branches {
		if path, taken := elsewhere[branch]; taken {
			held[branch] = worktree.Holder{Branch: branch, Path: path}
		}
	}
	return held, nil
}
