package push

import (
	"context"
	"fmt"
	"slices"
	"strings"

	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/repair"
	"github.com/shhac/g2g/internal/syncpoint"
	"github.com/shhac/g2g/internal/words"
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
// the comparison push had before stands. begins is where the remote's own work
// on the branch starts -- the parent's remote tip when its version is built on
// that -- which bounds the remote's side.
func bySyncPoint(ctx context.Context, git Comparer, latest syncpoint.Latest, branch, local, tip, parent string, begins func() (string, error)) (Publication, syncpoint.Changes, bool, error) {
	// The Git client can assess; anything that cannot compares as push did
	// before sync points. A branch with none recorded asks nothing further.
	assessor, ok := git.(syncpoint.Assessor)
	if _, synced := latest.Of(branch); !ok || !synced {
		return Publication{}, syncpoint.Changes{Unsynced: true}, false, nil
	}
	// A parent whose ancestry cannot be asked bounds nothing: the sync point
	// is then read against the parent here, as before there was a published
	// one to ask about.
	publishedParent, err := begins()
	if err != nil {
		publishedParent = ""
	}
	changes, err := syncpoint.Assess(ctx, assessor, latest, branch, syncpoint.Branch{
		Local: local, Remote: tip, LocalParent: parent, PublishedParent: publishedParent,
	})
	if err != nil || !changes.Dropped() {
		return Publication{}, changes, false, err
	}
	publication, decided := standing(changes)
	if !decided {
		return Publication{}, syncpoint.Changes{}, false, nil
	}
	return publication, changes, true, nil
}

// standing is what a sync point's changes say of publishing a branch, from
// them and nothing else. It is false for a branch whose every commit of its
// own is gone, which is a branch emptied or made again rather than one a
// commit was dropped from: the comparison push had before refuses that,
// naming the replacement for whoever means it, and pull refuses the same
// thing from the other side.
func standing(changes syncpoint.Changes) (Publication, bool) {
	// A replay is not a drop on either side: a replaced commit here is
	// published by its replay, which Mine counts, and one replaced upstream
	// is the remote's replay in New, still to be taken.
	replaced := len(changes.ReplacedHere)
	droppedHere, droppedUpstream := changes.Unpaired(changes.DroppedHere), changes.Unpaired(changes.DroppedUpstream)
	ours := len(changes.Mine) + len(droppedUpstream)
	theirs := len(changes.New)
	switch {
	case len(droppedUpstream) != 0:
		return Publication{Standing: Restoring, Ours: ours, Theirs: theirs, Dropped: len(droppedUpstream)}, true
	case theirs != 0:
		// Somebody else's commit on top of a drop or a replay of this
		// clone's, or a replay of theirs this clone has not taken: still a
		// divergence, and still refused. pull takes a replay of theirs.
		return Publication{Standing: Diverged, Ours: ours, Theirs: theirs + len(droppedHere)}, true
	case len(changes.Shared)+replaced == 0:
		// A commit replayed is not gone: its replay is still the branch's own.
		return Publication{}, false
	case len(droppedHere) != 0:
		return Publication{Standing: Dropping, Ours: ours, Dropped: len(droppedHere), Replaced: replaced}, true
	default:
		// Only replays of commits both sides agreed on: the branch was
		// rewritten, as a restack does, with some patches changed while a
		// conflict was resolved. Nothing published is anybody else's.
		return Publication{Standing: Rewritten, Ours: ours, Replaced: replaced}, true
	}
}

// Drop is one commit a push names, and the branch it is about.
type Drop = syncpoint.Drop

// drops lists every commit the sync points said was dropped, branch by
// branch: what this push removes, what it moves between two of its
// branches, what it replaces with a replay of it, and what it would put back.
func (s Service) drops(ctx context.Context, branches []string, changed map[string]syncpoint.Changes) ([]Drop, error) {
	commits := make([]Drop, 0)
	for _, branch := range branches {
		changes := changed[branch]
		commits = append(commits, syncpoint.Named(syncpoint.Restoring, branch, changes.Unpaired(changes.DroppedUpstream))...)
		for _, commit := range changes.Unpaired(changes.DroppedHere) {
			to, err := s.holding(ctx, branches, branch, commit)
			if err != nil {
				return nil, err
			}
			kind := syncpoint.Dropped
			if to != "" {
				kind = syncpoint.Moved
			}
			commits = append(commits, Drop{Kind: kind, Branch: branch, Commit: commit, To: to})
		}
		commits = append(commits, changes.ReplacedHere.Named(branch)...)
	}
	return syncpoint.Grouped(commits), nil
}

// holding is another branch of the push that still holds a commit, which
// makes dropping it from one branch a move rather than a drop: the first in
// the push's order, as asking each in turn found it.
func (s Service) holding(ctx context.Context, branches []string, branch, commit string) (string, error) {
	if lister, ok := s.Git.(syncpoint.RefLister); ok {
		refs := make([]string, 0, len(branches))
		for _, other := range branches {
			if other != branch {
				refs = append(refs, "refs/heads/"+other)
			}
		}
		holders, err := lister.Refs(ctx, refs, commit)
		if err != nil {
			return "", err
		}
		for _, other := range branches {
			if _, holds := holders["refs/heads/"+other]; holds && other != branch {
				return other, nil
			}
		}
		return "", nil
	}
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
	for _, drop := range plan.Commits {
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
		reasons = append(reasons, "it would drop "+syncpoint.Names(drops)+" from "+remote)
	}
	unsynced := make([]string, 0)
	for _, branch := range branches {
		publication := publishing[branch]
		if publication.Unpublished() && publication.Standing != New && changed[branch].Unsynced {
			unsynced = append(unsynced, branch)
		}
	}
	if len(unsynced) != 0 {
		reasons = append(reasons, strings.Join(unsynced, ", ")+" "+words.Pick(len(unsynced), "differs", "differ")+" from "+remote+" with no record of where the two last agreed")
	}
	if len(reasons) == 0 {
		return repair.Note{}
	}
	return repair.Note{
		Reason: "--strict: " + strings.Join(reasons, "; "),
		Ways:   []repair.Step{{Effect: "run without --strict to go ahead with what the preview lists"}},
	}
}

// restoring refuses a push that would put back commits the remote dropped
// since this clone last agreed with it. The way through is pull's: drop them
// here too, or keep them on purpose, after which they are this clone's own
// and publish like any other commit.
func restoring(remote string, restores []Drop) repair.Note {
	keep := "g2g pull"
	for _, drop := range restores {
		keep += " --keep " + localgit.Short(drop.Commit)
	}
	return repair.Note{
		Reason: fmt.Sprintf("%s dropped %s since this clone last pulled or pushed, and publishing would put %s back", remote, syncpoint.Names(restores), words.Pick(len(restores), "it", "them")),
		Ways: []repair.Step{
			{Command: "g2g pull", Effect: "drop " + words.Pick(len(restores), "it", "them") + " here too"},
			{Command: keep, Effect: "keep " + words.Pick(len(restores), "it", "them") + " as yours, then push"},
		},
	}
}
