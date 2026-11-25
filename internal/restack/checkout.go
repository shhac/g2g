package restack

import (
	"context"
	"errors"
	"slices"

	"github.com/shhac/g2g/internal/diagnostic"
	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/repair"
)

// standingOn records the branch the checkout is on and where it points.
//
// A detached HEAD has no branch for a rewrite to move underneath it, and an
// unreadable one is not worth failing a rewrite for, so both answer with an
// empty branch and nothing to reconcile later.
func (s Service) standingOn(ctx context.Context) (checkout, error) {
	branch, err := s.Git.CurrentBranch(ctx)
	if err != nil || branch == "" {
		return checkout{}, nil
	}
	tip, err := s.Git.Resolve(ctx, branch)
	if err != nil {
		return checkout{}, nil
	}
	return checkout{Branch: branch, Tip: tip}, nil
}

// checkout is where the working tree stood before a rewrite.
type checkout struct {
	Branch string
	Tip    string
}

// resettle brings the index and working tree to the branch's new tip.
//
// The rebase engine checks out as it goes, so this is only for the paths that
// move a ref without one: the replay engine, and a collapse, which is a bare
// ref move and could strand the checkout just as easily.
func (s Service) resettle(ctx context.Context, standing checkout) error {
	if standing.Branch == "" {
		return nil
	}
	tip, err := s.Git.Resolve(ctx, standing.Branch)
	if err != nil || tip == standing.Tip {
		return err
	}
	diagnostic.Event(ctx, "restack.resettle",
		diagnostic.Field{Key: "branch", Value: standing.Branch},
		diagnostic.Field{Key: "from", Value: standing.Tip},
		diagnostic.Field{Key: "to", Value: tip},
	)
	return s.Git.SwitchTree(ctx, standing.Tip, tip)
}

// here is the branch checked out in this worktree when plan moves it: one of
// its own, or one the caller moves and has it replay. An unreadable or
// detached checkout has no branch to move.
func (s Service) here(ctx context.Context, plan Plan, pending Pending) string {
	branch, err := s.Git.CurrentBranch(ctx)
	if err != nil || branch == "" || plan.Absorb {
		return ""
	}
	if slices.Contains(own(plan.Steps, plan.Absorb, pending), branch) {
		return branch
	}
	if _, callers := pending.Tips[branch]; callers && slices.Contains(plan.Branches(), branch) {
		return branch
	}
	return ""
}

// Uncommitted refuses plan where applying it would meet uncommitted work in
// this worktree, and nowhere else.
//
// A replay that applies cleanly moves refs and never opens the working tree,
// so changes on a branch it does not move are nothing to it. Refusing every
// tree with changes stopped someone replaying a child while its parent held
// work in progress, under a preview that said the working tree would not be
// touched, and the refusal came only from the apply. The two places a rewrite
// does meet the tree are the branch checked out here, whose files move with
// it, and a replay that does not apply cleanly, which becomes a rebase here.
// It is part of the plan so that the preview refuses in the apply's words. A
// status Git could not give is an error and never a refusal: a read the
// budget cut off would otherwise tell someone with a clean tree to commit.
func (s Service) Uncommitted(ctx context.Context, plan Plan) (Plan, error) {
	why := plan.meetsWorkingTree()
	if why == "" || plan.Blocked() != "" {
		return plan, nil
	}
	err := s.Git.Clean(ctx)
	if !errors.Is(err, localgit.ErrNotClean) {
		return plan, err
	}
	return plan.refused(repair.Note{
		Reason: why + ", and this working tree has uncommitted changes",
		Ways:   []repair.Step{{Effect: "commit the changes, or restack from a worktree that has none"}},
	}), nil
}

// meetsWorkingTree says why applying the plan needs this working tree, empty
// when it does not.
func (p Plan) meetsWorkingTree() string {
	switch {
	case p.NeedsWorkingTree():
		return "the replay does not apply cleanly, so it is a rebase in this working tree"
	case p.Here != "":
		return p.Here + " is checked out here, so its files move with it"
	}
	return ""
}
