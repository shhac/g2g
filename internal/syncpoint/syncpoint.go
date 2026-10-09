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
	"cmp"
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

// DroppedOn is the commits a list of drops names on one branch, which is what
// the agreement recorded for that branch carries.
func DroppedOn(branch string, lists ...[]Drop) []string {
	named := make([]string, 0)
	for _, list := range lists {
		for _, drop := range list {
			if drop.Branch == branch {
				named = append(named, drop.Commit)
			}
		}
	}
	return named
}

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

// Drop is one commit a pull or push names one by one -- dropped, moved, kept
// -- and the branch it is about. To is the branch a moved commit is in now.
type Drop struct {
	Branch string
	Commit string
	To     string
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
	// Shared are this branch's own commits the remote also has, by content.
	// A branch with none left is one the remote emptied, or made again under
	// the name, rather than one it dropped a commit from.
	Shared []string
	// Unsynced reports a branch with no sync point to classify against.
	// Every difference then reads as new on its own side, which is the
	// cautious reading -- and not the "in sync" a caller asking for
	// strictness can rely on.
	Unsynced bool
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
// back. It returns which of them were drops of this branch; anything else
// named is not this branch's to keep.
func (c Changes) Keep(commits []string) (Changes, []string) {
	kept := make([]string, 0)
	for _, commit := range commits {
		if slices.Contains(c.DroppedUpstream, commit) || slices.Contains(c.DroppedHere, commit) {
			kept = append(kept, commit)
		}
	}
	reclassified := c
	reclassified.DroppedUpstream, reclassified.Mine = partition(c.DroppedUpstream, commits, c.Mine)
	reclassified.DroppedHere, reclassified.New = partition(c.DroppedHere, commits, c.New)
	return reclassified, kept
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
	// cherry lists only what the other side cannot reach, so what is shared
	// is counted from the branch's own commits rather than from its answer.
	own, err := git.Commits(ctx, branch.Local, nonEmpty(branch.LocalParent))
	if err != nil {
		return Changes{}, err
	}
	shared := make([]string, 0, len(own))
	for _, commit := range own {
		if !slices.Contains(ours, commit) {
			shared = append(shared, commit)
		}
	}
	theirs, _, err := git.Cherry(ctx, branch.Local, branch.Remote, cmp.Or(branch.PublishedParent, branch.LocalParent))
	if err != nil {
		return Changes{}, err
	}
	if !branch.Synced {
		return Changes{Mine: ours, New: theirs, Shared: shared}, nil
	}
	parents := nonEmpty(branch.LocalParent, branch.PublishedParent)
	seen, err := git.Commits(ctx, branch.Sync.Tip, parents)
	if err != nil {
		return Changes{}, err
	}
	changes := Changes{Shared: shared}
	changes.DroppedUpstream, changes.Mine = split(ours, seen)
	changes.DroppedHere, changes.New = split(theirs, seen)
	return stale(ctx, git, branch, changes)
}

// stale moves commits that read as dropped here back to new, when the branch
// was reset to its remote-tracking ref: pull never moves that ref, so after a
// pull it can be older than the sync point, and resetting to it leaves out
// exactly what the pull brought -- which looks like a deliberate drop.
//
// The test is what the branch added since that ref. A drop made on purpose,
// by git rebase -i say, keeps the rest of what the sync point had beyond the
// ref, under new ids; a reset to the ref keeps none of it. Asked by content,
// so it holds when somebody rewrote the branch before the pull, which leaves
// the tracking ref no ancestor of the sync point at all.
func stale(ctx context.Context, git Git, branch Branch, changes Changes) (Changes, error) {
	if len(changes.DroppedHere) == 0 || branch.Tracking == "" || branch.Tracking == branch.Sync.Tip {
		return changes, nil
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
	_, kept, err := git.Cherry(ctx, branch.Sync.Tip, branch.Local, branch.Tracking)
	if err != nil || len(kept) != 0 {
		return changes, err
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
