package syncpoint

import (
	"cmp"
	"context"
	"slices"

	localgit "github.com/shhac/g2g/internal/git"
)

// Git is what classifying a branch reads, all of it local.
type Git interface {
	Commits(ctx context.Context, tip string, excluded []string) ([]string, error)
	Cherry(ctx context.Context, upstream, head, limit string) (absent, present []string, err error)
	IsAncestor(ctx context.Context, ancestor, descendant string) (bool, error)
}

// Assessor is what assessing a branch reads: its sync point, the commits on
// each side, and the remote-tracking ref a reset may have gone back to.
type Assessor interface {
	Reader
	Git
	Resolve(ctx context.Context, revision string) (string, error)
}

// Assess says what changed on each side of a branch since this clone and the
// remote last agreed on it. sides names the branch here and on the remote and
// each side's parent; latest holds its sync point, and Assess supplies the
// rest. A branch with no sync point recorded for it reports Unsynced and
// nothing else, and the caller compares it as it did before there were sync
// points. One nothing was dropped from reports no changes at all, whatever
// else differs.
//
// It is the one place a sync point is read for a decision, so pull and push
// cannot come to mean different things by having one.
func Assess(ctx context.Context, git Assessor, latest Latest, branch string, sides Branch) (Changes, error) {
	point, synced, err := describing(ctx, git, latest, branch)
	if err != nil {
		return Changes{}, err
	}
	if !synced {
		return Changes{Unsynced: true}, nil
	}
	sides.Sync, sides.Synced = point, true
	droppable, err := mayHaveDropped(ctx, git, sides)
	if err != nil || !droppable {
		return Changes{}, err
	}
	tracking, err := git.Resolve(ctx, localgit.TrackingRef(latest.Remote, branch))
	if err != nil {
		tracking = ""
	}
	sides.Tracking = tracking
	return Classify(ctx, git, sides)
}

// describing is a branch's sync point when it has one recorded for it. One
// recorded for a branch that has since been deleted and made again under the
// name is no sync point at all, which is the cautious answer: without one,
// every difference reads as new work on its side.
func describing(ctx context.Context, git Reader, latest Latest, branch string) (localgit.SyncPoint, bool, error) {
	point, ok := latest.Of(branch)
	if !ok {
		return localgit.SyncPoint{}, false, nil
	}
	if latest.described != nil {
		return point, latest.described[branch], nil
	}
	describes, err := git.Describes(ctx, branch, point)
	if err != nil || !describes {
		return localgit.SyncPoint{}, false, err
	}
	return point, true, nil
}

// mayHaveDropped reports whether Classify could find a drop on either side.
// Every commit it calls dropped is one side's own, held by the sync point, and
// without an equivalent on the other side; a branch with no such commit has
// nothing to classify, and every caller asks why a commit differs only when
// one was dropped. A false answer is exact. A true one may be a commit with
// an equivalent below the parents, which Classify then settles.
//
// It exists for the cost. Classify compares by content, and after a restack a
// branch's version here and on the remote sit on different trunks: its
// content comparison then reads every commit the trunk gained since, on every
// branch of a stack waiting to be pushed. These walks are bounded by the
// branch's own commits.
func mayHaveDropped(ctx context.Context, git Git, branch Branch) (bool, error) {
	seen, err := git.Commits(ctx, branch.Sync.Tip, nonEmpty(branch.LocalParent, branch.PublishedParent))
	if err != nil || len(seen) == 0 {
		return false, err
	}
	held := make(map[string]bool, len(seen))
	for _, commit := range seen {
		held[commit] = true
	}
	isHeld := func(commit string) bool { return held[commit] }
	comparer, ok := git.(Comparer)
	if !ok {
		// Without the bounded comparison, ask by id alone: a commit held by
		// the sync point and on one side only may have been dropped.
		ours, err := git.Commits(ctx, branch.Local, nonEmpty(branch.Remote, branch.LocalParent))
		if err != nil || slices.ContainsFunc(ours, isHeld) {
			return true, err
		}
		theirs, err := git.Commits(ctx, branch.Remote, nonEmpty(branch.Local, cmp.Or(branch.PublishedParent, branch.LocalParent)))
		return slices.ContainsFunc(theirs, isHeld), err
	}
	// After a restack every commit of the remote's version is one the sync
	// point holds and this branch has only under a new id, so the id alone
	// cannot settle it; content bounded at both parents can, for both sides
	// at once.
	ours, theirs, err := comparer.Inequivalent(ctx, branch.Local, branch.Remote, nonEmpty(branch.LocalParent, branch.PublishedParent))
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(ours, isHeld) || slices.ContainsFunc(theirs, isHeld), nil
}

// Comparer compares by content with both sides bounded, which lets Assess
// settle the commonest case in one process.
type Comparer interface {
	Inequivalent(ctx context.Context, left, right string, bounds []string) (leftOnly, rightOnly []string, err error)
}

// Branch is one branch on both sides and where they last agreed.
type Branch struct {
	// Local and Remote are the branch's tips here and on the remote.
	Local, Remote string
	// LocalParent is the parent here; PublishedParent the parent as the
	// remote holds it, when the remote's version is built on it, and empty
	// otherwise. Each side's own commits are bounded at its own parent.
	LocalParent, PublishedParent string
	// Sync is where the two last agreed, and Synced whether there is one.
	Sync   localgit.SyncPoint
	Synced bool
	// Tracking is the remote-tracking ref's tip for the branch, empty when
	// there is none. pull never moves it, so it can be older than Sync.
	Tracking string
}
