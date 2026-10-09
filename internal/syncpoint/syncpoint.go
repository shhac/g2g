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
	"slices"

	localgit "github.com/shhac/g2g/internal/git"
)

// Reader reads a branch's sync point and says whether it was recorded for this
// branch rather than another that had its name.
type Reader interface {
	ReadSync(ctx context.Context, remote, branch string) (localgit.SyncPoint, bool, error)
	Describes(ctx context.Context, branch string, point localgit.SyncPoint) (bool, error)
}

// Recorder writes one.
type Recorder interface {
	RecordSync(ctx context.Context, remote, branch string, point localgit.SyncPoint) error
}

// ReadRecorder reads and writes sync points.
type ReadRecorder interface {
	Reader
	Recorder
}

// Agree records that this clone and the remote agree on a branch, unless the
// latest sync point already says exactly that. A preview runs often, and an
// agreement it found already recorded adds nothing to the history but noise.
func Agree(ctx context.Context, git ReadRecorder, remote, branch string, point localgit.SyncPoint) error {
	current, ok, err := git.ReadSync(ctx, remote, branch)
	if err != nil {
		return err
	}
	if ok && current.Tip == point.Tip && current.Local == point.Local && len(point.Dropped) == 0 {
		return nil
	}
	return git.RecordSync(ctx, remote, branch, point)
}

// Git is what classifying a branch reads, all of it local.
type Git interface {
	Commits(ctx context.Context, tip string, excluded []string) ([]string, error)
	Cherry(ctx context.Context, upstream, head, limit string) (absent, present []string, err error)
	IsAncestor(ctx context.Context, ancestor, descendant string) (bool, error)
}

// Read is a branch's sync point when it has one recorded for it. One recorded
// for a branch that has since been deleted and made again under the name is
// no sync point at all, which is the cautious answer: without one, every
// difference reads as new work on its side.
func Read(ctx context.Context, reader Reader, remote, branch string) (localgit.SyncPoint, bool, error) {
	point, ok, err := reader.ReadSync(ctx, remote, branch)
	if err != nil || !ok {
		return localgit.SyncPoint{}, false, err
	}
	describes, err := reader.Describes(ctx, branch, point)
	if err != nil || !describes {
		return localgit.SyncPoint{}, false, err
	}
	return point, true, nil
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

// Changes is what each side holds that the other does not, and why.
type Changes struct {
	// Mine are commits here the remote never had; New, commits on the
	// remote this clone never had.
	Mine, New []string
	// DroppedUpstream are commits here the remote had and no longer has;
	// DroppedHere, commits the remote has that this branch had and no longer
	// has.
	DroppedUpstream, DroppedHere []string
	// Stale reports commits that would have read as dropped here, but are
	// exactly what resetting the branch to its remote-tracking ref -- older
	// than the sync point, because pull fetched past it -- leaves out. They
	// are counted as new instead: a reset to a stale ref and a deliberate
	// drop look the same, and only one of them is safe to publish.
	Stale bool
}

// Differs reports changes on either side.
func (c Changes) Differs() bool {
	return len(c.Mine)+len(c.New)+len(c.DroppedUpstream)+len(c.DroppedHere) != 0
}

// Dropped reports a drop in either direction.
func (c Changes) Dropped() bool { return len(c.DroppedUpstream)+len(c.DroppedHere) != 0 }

// Keep reclassifies commits a person chose to keep: one dropped upstream
// becomes theirs to publish again, and one dropped here becomes one to take
// back. Anything not dropped is ignored; Unkept says which.
func (c Changes) Keep(commits []string) Changes {
	kept := c
	kept.DroppedUpstream, kept.Mine = partition(c.DroppedUpstream, commits, c.Mine)
	kept.DroppedHere, kept.New = partition(c.DroppedHere, commits, c.New)
	return kept
}

// Unkept are the named commits that are not drops of this branch.
func (c Changes) Unkept(commits []string) []string {
	unkept := make([]string, 0)
	for _, commit := range commits {
		if !slices.Contains(c.DroppedUpstream, commit) && !slices.Contains(c.DroppedHere, commit) {
			unkept = append(unkept, commit)
		}
	}
	return unkept
}

// partition moves the members of from named in chosen onto to.
func partition(from, chosen, to []string) (left, moved []string) {
	left, moved = make([]string, 0, len(from)), slices.Clone(to)
	for _, commit := range from {
		if slices.Contains(chosen, commit) {
			moved = append(moved, commit)
			continue
		}
		left = append(left, commit)
	}
	return left, moved
}

// Classify says what each side of a branch holds that the other does not.
//
// Which commits differ is asked by content, bounded at each side's own parent
// so the parent's commits stay the parent's. Why each differs is asked of the
// sync point, by id, bounded at both versions of the parent: a commit either
// parent still holds is the parent's, and a drop of it is the parent's drop.
func Classify(ctx context.Context, git Git, branch Branch) (Changes, error) {
	ours, _, err := git.Cherry(ctx, branch.Remote, branch.Local, branch.LocalParent)
	if err != nil {
		return Changes{}, err
	}
	theirsFrom := branch.LocalParent
	if branch.PublishedParent != "" {
		theirsFrom = branch.PublishedParent
	}
	theirs, _, err := git.Cherry(ctx, branch.Local, branch.Remote, theirsFrom)
	if err != nil {
		return Changes{}, err
	}
	if !branch.Synced {
		return Changes{Mine: ours, New: theirs}, nil
	}
	parents := nonEmpty(branch.LocalParent, branch.PublishedParent)
	seen, err := git.Commits(ctx, branch.Sync.Tip, parents)
	if err != nil {
		return Changes{}, err
	}
	var changes Changes
	changes.DroppedUpstream, changes.Mine = split(ours, seen)
	changes.DroppedHere, changes.New = split(theirs, seen)
	return stale(ctx, git, branch, changes)
}

// stale moves commits that read as dropped here back to new, when the branch
// was reset to a remote-tracking ref older than the sync point and those are
// exactly the commits that reset left out.
func stale(ctx context.Context, git Git, branch Branch, changes Changes) (Changes, error) {
	if len(changes.DroppedHere) == 0 || branch.Tracking == "" || branch.Tracking == branch.Sync.Tip {
		return changes, nil
	}
	older, err := git.IsAncestor(ctx, branch.Tracking, branch.Sync.Tip)
	if err != nil || !older {
		return changes, err
	}
	onIt, err := git.IsAncestor(ctx, branch.Tracking, branch.Local)
	if err != nil || !onIt {
		return changes, err
	}
	beyond, err := git.Commits(ctx, branch.Sync.Tip, []string{branch.Tracking})
	if err != nil {
		return changes, err
	}
	for _, commit := range changes.DroppedHere {
		if !slices.Contains(beyond, commit) {
			return changes, nil
		}
	}
	changes.New = append(changes.New, changes.DroppedHere...)
	changes.DroppedHere, changes.Stale = nil, true
	return changes, nil
}

// split divides commits into those seen and those not.
func split(commits, seen []string) (in, out []string) {
	in, out = make([]string, 0), make([]string, 0)
	for _, commit := range commits {
		if slices.Contains(seen, commit) {
			in = append(in, commit)
			continue
		}
		out = append(out, commit)
	}
	return in, out
}

func nonEmpty(values ...string) []string {
	kept := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" {
			kept = append(kept, value)
		}
	}
	return kept
}
