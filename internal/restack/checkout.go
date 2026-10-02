package restack

import (
	"context"

	"github.com/shhac/g2g/internal/diagnostic"
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
