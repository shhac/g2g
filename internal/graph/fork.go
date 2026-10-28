package graph

import "context"

// MergeBases is the optional part of a Git reader that says where two lines of
// history meet. A reader without it cannot place a boundary below a parent's
// tip, so a caller that needs one refuses rather than guesses.
type MergeBases interface {
	MergeBase(ctx context.Context, one, other string) (string, error)
}

// Upstream is the optional part of a Git reader that says where a trunk's
// remote was when last fetched or pushed, from local refs alone.
type Upstream interface {
	KnownParent(ctx context.Context, trunk string) (string, error)
}

// AheadOnUpstream reports a branch on a trunk whose fork point is past the
// trunk's local tip and on the trunk's known upstream: it started where the
// remote's trunk is, as create --at starts one, and the local trunk only lags
// behind it. Nothing moved back, so the branch is not stale, and replaying it
// onto the local tip would take out trunk commits the remote has. A deliberate
// rewind of a trunk is asked for by name, with restack --onto.
//
// A reader that cannot say where the upstream is answers no, which is how
// every such branch read before this existed. The ancestry of the local tip is
// asked first because it is false for every trunk that simply moved on, so the
// ordinary case costs one process.
func AheadOnUpstream(ctx context.Context, git ordering, g Graph, parent, tip, fork string) (bool, error) {
	upstream, ok := git.(Upstream)
	if !ok || g.Tracked(parent) || fork == tip {
		return false, nil
	}
	ahead, err := git.IsAncestor(ctx, tip, fork)
	if err != nil || !ahead {
		return false, err
	}
	known, err := upstream.KnownParent(ctx, parent)
	if err != nil || known == "" {
		return false, err
	}
	return git.IsAncestor(ctx, fork, known)
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
// track applies it to a branch on a trunk that has moved on and restack to
// every branch it plans, and they wrote it out apart until it was this one
// rule.
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
