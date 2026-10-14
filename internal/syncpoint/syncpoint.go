// Package syncpoint says what changed on each side of a branch since this
// clone and its remote last agreed on it.
//
// Content alone cannot tell a commit somebody dropped from one nobody had yet:
// both are a commit on one side and not the other. What tells them apart is
// whether this clone saw it on the remote when the two last agreed, so pull
// and push record that agreement — a sync point — and read it back here. A
// commit the sync point holds, and one side no longer does, was dropped by
// that side; a commit it does not hold is new.
//
// Membership in the sync point is by commit id, never by content. A change, its
// revert and the same change made again have the same content as the first,
// and asking by content would call the re-made one dropped — in the direction
// that deletes it from the remote. By id a rewritten commit reads as new,
// which keeps it or refuses, and never deletes it.
package syncpoint

import (
	"context"
	"maps"
	"slices"

	"github.com/shhac/g2g/internal/diagnostic"
	localgit "github.com/shhac/g2g/internal/git"
)

// HistoryReader reads many branches' sync points for one remote at once.
type HistoryReader interface {
	SyncHistories(ctx context.Context, remote string, branches []string) (map[string][]localgit.SyncPoint, error)
}

// Reader reads sync points and says whether one was recorded for this branch
// rather than another that had its name.
type Reader interface {
	HistoryReader
	Describes(ctx context.Context, branch string, point localgit.SyncPoint) (bool, error)
}

// Recorder writes one.
type Recorder interface {
	RecordSync(ctx context.Context, remote, branch string, point localgit.SyncPoint) error
}

// ReadRecorder reads and writes sync points.
type ReadRecorder interface {
	HistoryReader
	Recorder
}

// Latest is the newest sync point of each branch a command asked about, for
// one remote, read once for the whole selection. Asking per branch was two
// processes each, on every preview, whether or not a branch had one.
type Latest struct {
	Remote string
	points map[string]localgit.SyncPoint
	// described says, of each point, whether it was recorded for the branch
	// that has the name now; nil until Describe has asked.
	described map[string]bool
}

// LoadLatest reads the newest sync point of each branch.
func LoadLatest(ctx context.Context, git HistoryReader, remote string, branches []string) (Latest, error) {
	latest := Latest{Remote: remote, points: map[string]localgit.SyncPoint{}}
	if len(branches) == 0 {
		return latest, nil
	}
	histories, err := git.SyncHistories(ctx, remote, branches)
	if err != nil {
		return Latest{}, err
	}
	for branch, history := range histories {
		if len(history) != 0 {
			latest.points[branch] = history[0]
		}
	}
	return latest, nil
}

// Describe asks of every point at once whether it was recorded for the branch
// that has its name now, when Git can answer that in one go. Without it each
// is asked when it is assessed.
func (l Latest) Describe(ctx context.Context, git Reader) (Latest, error) {
	batch, ok := git.(interface {
		DescribesAll(ctx context.Context, points map[string]localgit.SyncPoint) (map[string]bool, error)
	})
	if !ok || len(l.points) == 0 {
		return l, nil
	}
	described, err := batch.DescribesAll(ctx, l.points)
	if err != nil {
		return Latest{}, err
	}
	l.described = described
	return l, nil
}

// Of is a branch's newest sync point, and whether it has one.
func (l Latest) Of(branch string) (localgit.SyncPoint, bool) {
	point, ok := l.points[branch]
	return point, ok
}

// Record notes that this clone and the remote agree on each branch named, at
// the point given, for a command whose own work has already happened. It
// reads what is recorded once for all of them and writes only where that
// differs: a preview runs often, and an agreement already recorded adds
// nothing to the history but noise. A recording that fails is a diagnostic
// named by event, never a failure -- the push or pull stands, and until the
// next agreement records it the cautious reading applies.
func Record(ctx context.Context, git ReadRecorder, event, remote string, points map[string]localgit.SyncPoint) {
	if len(points) == 0 {
		return
	}
	branches := slices.Sorted(maps.Keys(points))
	latest, err := LoadLatest(ctx, git, remote, branches)
	for _, branch := range branches {
		point := points[branch]
		if err == nil && agreed(latest, branch, point) {
			continue
		}
		if err != nil || git.RecordSync(ctx, remote, branch, point) != nil {
			diagnostic.Event(ctx, event, diagnostic.Field{Key: "branch", Value: branch}, diagnostic.Field{Key: "decision", Value: "not recorded"})
		}
	}
}

// RefLister lists which of the named refs exist and what they point at, only
// those containing a commit when one is given. It is how a commit dropped from
// one branch is found in another in one process rather than one per branch.
type RefLister interface {
	Refs(ctx context.Context, refs []string, contains string) (map[string]string, error)
}

// Resolver resolves one revision.
type Resolver interface {
	Resolve(ctx context.Context, revision string) (string, error)
}

// Locals is each branch's tip here, leaving out any that does not resolve --
// asked in one process when Git can answer them all at once, which the Git
// client can, and one at a time otherwise.
func Locals(ctx context.Context, git Resolver, branches []string) map[string]string {
	if batch, ok := git.(interface {
		ResolveAll(ctx context.Context, revisions []string) (map[string]string, error)
	}); ok {
		if tips, err := batch.ResolveAll(ctx, branches); err == nil {
			return tips
		}
	}
	tips := make(map[string]string, len(branches))
	for _, branch := range branches {
		if tip, err := git.Resolve(ctx, branch); err == nil {
			tips[branch] = tip
		}
	}
	return tips
}

// agreed reports a branch whose newest sync point already says exactly this.
func agreed(latest Latest, branch string, point localgit.SyncPoint) bool {
	current, ok := latest.Of(branch)
	return ok && current.Tip == point.Tip && current.Local == point.Local && len(point.Dropped) == 0
}
