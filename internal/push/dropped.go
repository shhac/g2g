package push

import (
	"context"
	"slices"
	"strings"

	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/repair"
	"github.com/shhac/g2g/internal/syncpoint"
)

// Drops, from push's side.
//
// The remote holding commits this branch does not is what being behind looks
// like, and push refuses it: publishing would drop them. It is also what a
// branch looks like after its owner dropped a commit on purpose, and refusing
// that left them only git push --force-with-lease, which is the same refusal
// with the safety taken off. A sync point tells the two apart: commits this
// clone agreed on with the remote and has since dropped are its own drop to
// publish, and anything else the remote has is somebody else's and still
// refuses. The other direction matters as much: a commit the remote dropped
// since the agreement, still here, would be published again, which is how a
// drop used to be undone by the next push from anyone who still had it.

// bySyncPoint is the standing a sync point gives a branch the remote holds
// commits of that it does not, or that holds commits the remote dropped. ok is
// false when there is no sync point, or nothing was dropped either way, and
// the comparison push had before stands. publishedParent is the parent's
// remote tip, which bounds the remote's side when its version is built on it.
func bySyncPoint(ctx context.Context, git Comparer, remote, branch, local, tip, parent, publishedParent string) (Publication, syncpoint.Changes, bool, error) {
	// The Git client can assess; anything that cannot compares as push did
	// before sync points.
	assessor, ok := git.(syncpoint.Assessor)
	if !ok || remote == "" {
		return Publication{}, syncpoint.Changes{Unsynced: true}, false, nil
	}
	// A parent whose ancestry cannot be asked bounds nothing: the sync point
	// is then read against the parent here, as before there was a published
	// one to ask about.
	begins, err := builtOn(ctx, git, publishedParent, tip)
	if err != nil {
		begins = ""
	}
	changes, err := syncpoint.Assess(ctx, assessor, remote, branch, syncpoint.Branch{
		Local: local, Remote: tip, LocalParent: parent, PublishedParent: begins,
	})
	if err != nil || !changes.Dropped() {
		return Publication{}, changes, false, err
	}
	ours := len(changes.Mine) + len(changes.DroppedUpstream)
	switch {
	case len(changes.DroppedUpstream) != 0:
		return Publication{Standing: Restoring, Ours: ours, Theirs: len(changes.New), Dropped: len(changes.DroppedUpstream)}, changes, true, nil
	case len(changes.New) != 0:
		// Somebody else's commit on top of a drop of this clone's: still a
		// divergence, and still refused.
		return Publication{Standing: Diverged, Ours: ours, Theirs: len(changes.New) + len(changes.DroppedHere)}, changes, true, nil
	case len(changes.Shared) == 0:
		// Every commit of the branch's own gone: that is a branch emptied or
		// made again, not one a commit was dropped from, and pull refuses
		// the same thing from the other side. The comparison push had before
		// refuses it, naming the replacement for whoever means it.
		return Publication{}, syncpoint.Changes{}, false, nil
	default:
		return Publication{Standing: Dropping, Ours: ours, Dropped: len(changes.DroppedHere)}, changes, true, nil
	}
}

// Drop is one commit a push names, and the branch it is about.
type Drop = syncpoint.Drop

// drops lists every commit the sync points said was dropped, in branch
// order: what this push removes, what it moves between two of its branches,
// and what it would put back.
func (s Service) drops(ctx context.Context, branches []string, changed map[string]syncpoint.Changes) (dropped, moved, restored []Drop, err error) {
	dropped, moved, restored = []Drop{}, []Drop{}, []Drop{}
	for _, branch := range branches {
		changes := changed[branch]
		for _, commit := range changes.DroppedUpstream {
			restored = append(restored, Drop{Branch: branch, Commit: commit})
		}
		for _, commit := range changes.DroppedHere {
			to, err := s.holding(ctx, branches, branch, commit)
			if err != nil {
				return nil, nil, nil, err
			}
			if to == "" {
				dropped = append(dropped, Drop{Branch: branch, Commit: commit})
				continue
			}
			moved = append(moved, Drop{Branch: branch, Commit: commit, To: to})
		}
	}
	return dropped, moved, restored, nil
}

// holding is another branch of the push that still holds a commit, which
// makes dropping it from one branch a move rather than a drop.
func (s Service) holding(ctx context.Context, branches []string, branch, commit string) (string, error) {
	for _, other := range branches {
		if other == branch {
			continue
		}
		holds, err := s.Git.IsAncestor(ctx, commit, other)
		if err != nil {
			return "", err
		}
		if holds {
			return other, nil
		}
	}
	return "", nil
}

// subjects names every commit the plan lists, when Git can say.
func (s Service) subjects(ctx context.Context, plan Plan) (map[string]string, error) {
	describer, ok := s.Git.(interface {
		Subjects(ctx context.Context, ids []string) (map[string]string, error)
	})
	if !ok {
		return nil, nil
	}
	ids := make([]string, 0)
	for _, drop := range slices.Concat(plan.Drops, plan.Moves, plan.Restores) {
		if !slices.Contains(ids, drop.Commit) {
			ids = append(ids, drop.Commit)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	return describer.Subjects(ctx, ids)
}

// strictly is what --strict refuses on top of what push refuses anyway: any
// published commit this push would drop, and any branch that differs from the
// remote with no sync point to say whose the difference is. A move keeps its
// commit in the stack and is not refused.
func strictly(remote string, branches []string, publishing map[string]Publication, changed map[string]syncpoint.Changes, drops []Drop) repair.Note {
	reasons := make([]string, 0, 2)
	if len(drops) != 0 {
		named := make([]string, 0, len(drops))
		for _, drop := range drops {
			named = append(named, drop.Branch+" "+localgit.Short(drop.Commit))
		}
		reasons = append(reasons, "it would drop "+strings.Join(named, ", ")+" from "+remote)
	}
	unsynced := make([]string, 0)
	for _, branch := range branches {
		publication := publishing[branch]
		if publication.Unpublished() && publication.Standing != New && changed[branch].Unsynced {
			unsynced = append(unsynced, branch)
		}
	}
	if len(unsynced) != 0 {
		reasons = append(reasons, strings.Join(unsynced, ", ")+" "+pickWord(len(unsynced), "differs", "differ")+" from "+remote+" with no record of where the two last agreed")
	}
	if len(reasons) == 0 {
		return repair.Note{}
	}
	return repair.Note{
		Reason: "--strict: " + strings.Join(reasons, "; "),
		Ways:   []repair.Step{{Effect: "run without --strict to go ahead with what the preview lists"}},
	}
}
