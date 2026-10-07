package graph

import "context"

// MergeBases is the optional part of a Git reader that says where two lines of
// history meet. A reader without it cannot place a boundary below a parent's
// tip, so a caller that needs one refuses rather than guesses.
type MergeBases interface {
	MergeBase(ctx context.Context, one, other string) (string, error)
}

// ordering answers whether one commit is an ancestor of another.
type ordering interface {
	IsAncestor(ctx context.Context, ancestor, descendant string) (bool, error)
}

// LaterFork is fork moved up to where tip and head meet, when that is further
// along: the commits below the meeting point are tip's, not the branch's own.
// It never moves a boundary backward, so a tip that was rewritten, or one head
// does not build on, leaves fork where it was.
//
// track and restack both apply it to a branch on a trunk that has moved on,
// and wrote it out apart until it was this one rule.
func LaterFork(ctx context.Context, git ordering, forks MergeBases, tip, head, fork string) (string, error) {
	shared, err := forks.MergeBase(ctx, tip, head)
	if err != nil {
		return "", err
	}
	forward, err := git.IsAncestor(ctx, fork, shared)
	if err != nil || !forward {
		return fork, err
	}
	return shared, nil
}
