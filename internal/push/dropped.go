package push

import (
	"context"
	"slices"

	localgit "github.com/shhac/g2g/internal/git"
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

// classifier is the Git a sync point is read through. The Git client is all
// of it; anything that is not compares as push did before sync points.
type classifier interface {
	syncpoint.Reader
	syncpoint.Git
}

// bySyncPoint is the standing a sync point gives a branch the remote holds
// commits of that it does not, or that holds commits the remote dropped. ok is
// false when there is no sync point, or nothing was dropped either way, and
// the comparison push had before stands.
func bySyncPoint(ctx context.Context, git Comparer, remote, branch, local, tip, parent, publishedParent string) (Publication, syncpoint.Changes, bool, error) {
	reader, ok := git.(classifier)
	if !ok || remote == "" {
		return Publication{}, syncpoint.Changes{}, false, nil
	}
	point, synced, err := syncpoint.Read(ctx, reader, remote, branch)
	if err != nil || !synced {
		return Publication{}, syncpoint.Changes{}, false, err
	}
	tracking, err := git.Resolve(ctx, "refs/remotes/"+remote+"/"+branch)
	if err != nil {
		tracking = ""
	}
	if publishedParent != "" {
		if built, err := reader.IsAncestor(ctx, publishedParent, tip); err != nil || !built {
			publishedParent = ""
		}
	}
	changes, err := syncpoint.Classify(ctx, reader, syncpoint.Branch{
		Local: local, Remote: tip, LocalParent: parent, PublishedParent: publishedParent,
		Sync: point, Synced: true, Tracking: tracking,
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
	default:
		return Publication{Standing: Dropping, Ours: ours, Dropped: len(changes.DroppedHere)}, changes, true, nil
	}
}

// Drop is one commit a push names, and the branch it is about. To is the
// branch a moved commit is in now.
type Drop struct {
	Branch string
	Commit string
	To     string
}

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
		Describe(ctx context.Context, ids []string) ([]localgit.Commit, error)
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
	commits, err := describer.Describe(ctx, ids)
	if err != nil {
		return nil, err
	}
	named := make(map[string]string, len(commits))
	for _, commit := range commits {
		named[commit.ID] = commit.Subject
	}
	return named, nil
}
