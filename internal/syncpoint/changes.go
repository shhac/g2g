package syncpoint

import (
	"cmp"
	"context"
	"slices"
)

// Changes is what each side holds that the other does not, and why.
type Changes struct {
	// Mine are commits here the remote never had; New, commits on the
	// remote this clone never had.
	Mine, New []string
	// DroppedUpstream are commits here the remote had and no longer has;
	// DroppedHere, commits the remote has that this branch had and no longer
	// has.
	DroppedUpstream, DroppedHere []string
	// ReplacedHere are commits the remote has that this branch replaced with
	// a replay of its own, each paired with the version that replaced it;
	// ReplacedUpstream, commits here the remote replaced the same way. A
	// replay that resolved a conflict changes a commit's patch, so its
	// content matches nothing on the other side, and without these it read
	// as one commit dropped and an unrelated one made. Neither commit of a
	// pair is in the four lists above.
	ReplacedHere, ReplacedUpstream []Replacement
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
	return len(c.Mine)+len(c.New)+len(c.DroppedUpstream)+len(c.DroppedHere) != 0 || c.Replaced()
}

// Dropped reports a drop in either direction.
func (c Changes) Dropped() bool { return len(c.DroppedUpstream)+len(c.DroppedHere) != 0 }

// Replaced reports a commit replaced in a replay, in either direction.
func (c Changes) Replaced() bool { return len(c.ReplacedHere)+len(c.ReplacedUpstream) != 0 }

// Decides reports changes the sync point has something to say about: a drop
// or a replacement. Without either, the comparison a caller had before sync
// points stands.
func (c Changes) Decides() bool { return c.Dropped() || c.Replaced() }

// Keep reclassifies commits a person chose to keep: one dropped upstream
// becomes theirs to publish again, and one dropped here becomes one to take
// back. It returns which of them were drops of this branch; anything else
// named is not this branch's to keep.
func (c Changes) Keep(commits []string) (Changes, []string) {
	c = c.unpair(commits)
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
	changes, err = stale(ctx, git, branch, changes)
	if err != nil {
		return Changes{}, err
	}
	return replaced(ctx, git, changes, seen)
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
