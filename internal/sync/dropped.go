package sync

import (
	"context"
	"fmt"
	"slices"
	"strings"

	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/repair"
	"github.com/shhac/g2g/internal/syncpoint"
)

// What changed on each side since this clone and the remote last agreed.
//
// Without a sync point pull reads every commit here that the remote lacks as
// work of this clone's own, which is what it is for any commit somebody makes.
// It is also what a commit somebody else dropped and published looks like, and
// keeping it is how the drop got undone by the next push from anyone who still
// had it. With one, pull can tell the two apart: a commit this clone saw on the
// remote when they last agreed, and the remote no longer has, was dropped
// there, and pull drops it here too -- naming it, and recording it in the sync
// point's history, where it stays recoverable.

// classifier is the Git a classification reads. The Git client is all of it;
// a fake that is not makes pull behave as it did before sync points existed.
type classifier interface {
	syncpoint.Reader
	syncpoint.Git
}

// changed is what the sync point says about one branch, once decided.
type changed struct {
	collection *Collection
	refusal    *repair.Note
	drops      []Drop
	moves      []Drop
	kept       []Drop
	left       []Drop
	restored   []Drop
	// decided reports a branch the sync point settled; one it did not is
	// left to the rules pull had before sync points.
	decided bool
}

// bySyncPoint decides a branch from what changed on each side since the two
// last agreed, when there is a sync point for it and anything was dropped.
func (s Service) bySyncPoint(ctx context.Context, c collecting, branch, local, published, parent, begins string) (changed, error) {
	git, ok := s.Git.(classifier)
	if !ok {
		return changed{}, nil
	}
	point, synced, err := syncpoint.Read(ctx, git, c.remote, branch)
	if err != nil || !synced {
		return changed{}, err
	}
	tracking, err := s.Git.Resolve(ctx, "refs/remotes/"+c.remote+"/"+branch)
	if err != nil {
		tracking = ""
	}
	changes, err := syncpoint.Classify(ctx, git, syncpoint.Branch{
		Local: local, Remote: published, LocalParent: parent, PublishedParent: begins,
		Sync: point, Synced: true, Tracking: tracking,
	})
	if err != nil {
		return changed{}, err
	}
	kept := c.keeping(changes)
	changes = changes.Keep(c.keep)
	result := changed{kept: drops(branch, kept)}
	if changes.Stale {
		result.restored = drops(branch, changes.New)
	}
	if !changes.Dropped() {
		return result, nil
	}
	result.decided = true
	moved, gone, err := s.moved(ctx, c, branch, changes.DroppedUpstream)
	if err != nil {
		return changed{}, err
	}
	switch {
	case len(changes.DroppedUpstream) != 0 && len(changes.DroppedHere) != 0:
		result.refusal = droppedBothWays(c, branch, changes)
	case len(changes.DroppedUpstream) != 0 && len(changes.Mine) != 0:
		result.refusal = yoursOverTheirDrop(c, branch, changes)
	case len(changes.DroppedUpstream) != 0 && len(changes.Shared) == 0 && len(gone) != 0:
		result.refusal = emptiedUpstream(c, branch, gone)
	case len(changes.DroppedUpstream) != 0:
		result.collection = &Collection{Branch: branch, To: published, Superseded: true, Begins: begins, Dropped: gone}
		result.drops, result.moves = drops(branch, gone), moved
	case len(changes.New) != 0:
		result.refusal = droppedHereAndNew(c, branch, changes)
	default:
		// Dropped here and nothing new there: this branch is ahead of what
		// the remote holds by a drop, which is push's to publish.
		result.left = drops(branch, changes.DroppedHere)
	}
	return result, nil
}

// moved splits commits the remote dropped from this branch into those another
// selected branch's published version still holds -- moved there, not
// dropped -- and those gone from the stack.
func (s Service) moved(ctx context.Context, c collecting, branch string, commits []string) ([]Drop, []string, error) {
	moves, gone := make([]Drop, 0), make([]string, 0, len(commits))
	for _, commit := range commits {
		to := ""
		for _, other := range c.branches {
			tip := c.onRemote[other]
			if other == branch || other == c.base || tip == "" {
				continue
			}
			holds, err := s.Git.IsAncestor(ctx, commit, tip)
			if err != nil {
				return nil, nil, err
			}
			if holds {
				to = other
				break
			}
		}
		if to == "" {
			gone = append(gone, commit)
			continue
		}
		moves = append(moves, Drop{Branch: branch, Commit: commit, To: to})
	}
	return moves, gone, nil
}

func drops(branch string, commits []string) []Drop {
	named := make([]Drop, 0, len(commits))
	for _, commit := range commits {
		named = append(named, Drop{Branch: branch, Commit: commit})
	}
	return named
}

// keeping is which of the caller's --keep commits are drops of this branch.
func (c collecting) keeping(changes syncpoint.Changes) []string {
	kept := make([]string, 0)
	for _, commit := range c.keep {
		if slices.Contains(changes.DroppedUpstream, commit) || slices.Contains(changes.DroppedHere, commit) {
			kept = append(kept, commit)
		}
	}
	return kept
}

// keepCommand is the pull that keeps these commits, for the selection and
// remote this one was asked about.
func (c collecting) keepCommand(commits []string) string {
	command := c.command
	for _, commit := range commits {
		command += " --keep " + shortID(commit)
	}
	return command
}

func shortID(commit string) string {
	if len(commit) > 12 {
		return commit[:12]
	}
	return commit
}

func listed(commits []string) string {
	short := make([]string, 0, len(commits))
	for _, commit := range commits {
		short = append(short, shortID(commit))
	}
	return strings.Join(short, ", ")
}

func droppedBothWays(c collecting, branch string, changes syncpoint.Changes) *repair.Note {
	return &repair.Note{
		Reason: fmt.Sprintf("%s was dropped from on both sides since the last pull or push: %s gone from %s, and %s gone from here",
			branch, listed(changes.DroppedUpstream), c.remote, listed(changes.DroppedHere)),
		Ways: []repair.Step{
			{Command: c.keepCommand(changes.DroppedHere), Effect: "take " + c.remote + "'s version, keeping what you dropped as theirs"},
			{Effect: "reconcile it yourself"},
		},
	}
}

func yoursOverTheirDrop(c collecting, branch string, changes syncpoint.Changes) *repair.Note {
	return &repair.Note{
		Reason: fmt.Sprintf("%s dropped %s from %s, and you have committed %s on it since: taking theirs would lose yours, and keeping yours would keep what they dropped",
			c.remote, listed(changes.DroppedUpstream), branch, listed(changes.Mine)),
		Ways: []repair.Step{
			{Command: c.keepCommand(changes.DroppedUpstream), Effect: "keep what " + c.remote + " dropped, with your commits on it"},
			{Command: c.takeCommand, Effect: "take " + c.remote + "'s version and discard yours"},
			{Effect: "drop them yourself with git rebase -i, then pull"},
		},
	}
}

func emptiedUpstream(c collecting, branch string, gone []string) *repair.Note {
	return &repair.Note{
		Reason: fmt.Sprintf("%s has none of %s's own commits any more (%s), which is a branch emptied or made again rather than one a commit was dropped from",
			c.remote, branch, listed(gone)),
		Ways: []repair.Step{
			{Command: c.keepCommand(gone), Effect: "keep them"},
			{Command: c.takeCommand, Effect: "take " + c.remote + "'s version and discard them"},
		},
	}
}

func droppedHereAndNew(c collecting, branch string, changes syncpoint.Changes) *repair.Note {
	return &repair.Note{
		Reason: fmt.Sprintf("you dropped %s from %s, and %s has %s on it you do not: taking theirs would put back what you dropped",
			listed(changes.DroppedHere), branch, c.remote, listed(changes.New)),
		Ways: []repair.Step{
			{Command: c.keepCommand(changes.DroppedHere), Effect: "take theirs, keeping what you dropped"},
			{Effect: "reconcile it yourself"},
		},
	}
}

// starts says where each branch this pull does not take begins, when the
// branch below it is taken and it sits on that branch's published version
// with the commits its record says the parent had: Bob moved the parent back
// so a commit at its tip became this branch's, and published both. Without
// it, the commit the parent no longer has reads as the parent's drop, and the
// replay drops it from this branch too.
func (s Service) starts(ctx context.Context, c collecting, collections []Collection) (map[string]string, error) {
	taken := map[string]bool{}
	for _, collection := range collections {
		taken[collection.Branch] = true
	}
	starts := map[string]string{}
	for _, branch := range c.branches {
		parent := c.parents[branch]
		published, below := c.onRemote[branch], c.onRemote[parent]
		if taken[branch] || !taken[parent] || published == "" || below == "" || c.forks[branch] == "" {
			continue
		}
		answers := make([]bool, 0, 3)
		for _, pair := range [][2]string{{below, published}, {below, branch}, {c.forks[branch], published}} {
			holds, err := s.Git.IsAncestor(ctx, pair[0], pair[1])
			if err != nil {
				return nil, err
			}
			answers = append(answers, holds)
		}
		if !slices.Contains(answers, false) {
			starts[branch] = below
		}
	}
	if len(starts) == 0 {
		return nil, nil
	}
	return starts, nil
}

// describe names every commit a plan lists, when Git can say.
func (s Service) describe(ctx context.Context, plan Plan) (map[string]string, error) {
	describer, ok := s.Git.(interface {
		Describe(ctx context.Context, ids []string) ([]localgit.Commit, error)
	})
	if !ok {
		return nil, nil
	}
	ids := make([]string, 0)
	for _, list := range [][]Drop{plan.Drops, plan.Moves, plan.Kept, plan.Left, plan.Restored} {
		for _, drop := range list {
			if !slices.Contains(ids, drop.Commit) {
				ids = append(ids, drop.Commit)
			}
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
