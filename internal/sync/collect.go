package sync

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	"github.com/shhac/g2g/internal/diagnostic"

	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/landed"
	"github.com/shhac/g2g/internal/repair"
)

// What moved, and how to say so.
//
// Every subtle rule sync has lives here — advance against supersede against
// diverge, what --take published would discard, and the wording of the refusal
// when both sides moved. Kept apart from the sequencing so the two can be read
// one at a time, the way land and restack already separate theirs.

// standing is how the base stands against the remote.
type standing struct {
	Advance, Supersede, Diverged bool
	Discards                     []string
}

// compare asks how the base stands against the remote without changing it.
func (s Service) compare(ctx context.Context, base, remote string, published map[string]string, take Take) (standing, error) {
	// The base is not on the remote at all, which is ordinary for a local trunk
	// that was never pushed.
	if published[base] == "" {
		return standing{}, nil
	}
	fetched := localgit.IsolatedRef(remote, base)
	local, err := s.Git.Resolve(ctx, base)
	if err != nil {
		return standing{}, err
	}
	upstream, err := s.Git.Resolve(ctx, fetched)
	if err != nil {
		return standing{}, nil
	}
	if local == upstream {
		return standing{}, nil
	}
	behind, err := s.Git.IsAncestor(ctx, base, fetched)
	if err != nil {
		return standing{}, err
	}
	if behind {
		return standing{Advance: true}, nil
	}
	// A trunk with commits of its own and nothing new upstream has nothing to
	// advance to. Publishing it is not sync's business, and offering --take
	// published here offered to discard those commits for no reason at all.
	ahead, err := s.Git.IsAncestor(ctx, fetched, base)
	if err != nil {
		return standing{}, err
	}
	if ahead {
		return standing{}, nil
	}
	// Neither side is an ancestor of the other, which is what somebody
	// force-pushing the trunk looks like — usually after rebasing or squashing
	// it. If everything the local trunk has is in the published one by content,
	// nothing is lost by taking theirs, and that is the whole of the recovery:
	// remote trunk, local stack replayed onto it.
	ours, _, err := s.Git.Cherry(ctx, fetched, base, "")
	if err != nil {
		return standing{Diverged: true}, nil
	}
	if len(ours) == 0 {
		return standing{Supersede: true}, nil
	}
	if take.Published() {
		return standing{Supersede: true, Discards: ours}, nil
	}
	return standing{Diverged: true}, nil
}

// collect works out which branches of your own the remote has moved on.
//
// Five answers, and only the last is a refusal:
//
//   - not published, or level: nothing to do.
//   - published ahead of here: fast-forward, which is a reviewer pushing a fix
//     onto your branch.
//   - published elsewhere but containing everything here by content: somebody
//     rebased or amended your branch and published it, so theirs supersedes.
//   - here containing everything published by content, ahead or rewritten:
//     unpublished work, which is push's business.
//   - genuinely diverged: you have work the published version does not, and
//     choosing between them is not something to do behind your back.
//
// Whether a branch is published at all comes from the remote, never from the
// fetched ref. Nothing prunes refs/g2g/remotes/, so a branch the remote has
// since deleted still resolves there to whatever it last held -- and reading
// that as the published version fast-forwarded a branch onto commits its
// owner had dropped, or refused it as diverged from a version nobody has.
//
// The branches both sides moved come back rather than a refusal, because the
// way out names the selection it came from and only the caller has that.
func (s Service) collect(ctx context.Context, c collecting) (collected, error) {
	found := collected{collections: make([]Collection, 0, len(c.branches)), stuck: make([]divergence, 0)}
	for _, branch := range c.branches {
		verdict, err := s.collectOne(ctx, c, branch)
		if err != nil {
			return collected{}, err
		}
		if verdict.collection != nil {
			found.collections = append(found.collections, *verdict.collection)
		}
		if verdict.stuck != nil {
			found.stuck = append(found.stuck, *verdict.stuck)
		}
		if verdict.refusal != nil && found.refusal == nil {
			found.refusal = verdict.refusal
		}
		found.drops = append(found.drops, verdict.drops...)
		found.moves = append(found.moves, verdict.moves...)
		found.kept = append(found.kept, verdict.kept...)
		found.left = append(found.left, verdict.left...)
		found.restored = append(found.restored, verdict.restored...)
		if verdict.unsynced {
			found.unsynced = append(found.unsynced, branch)
		}
	}
	if len(found.stuck) != 0 || found.refusal != nil {
		return found, nil
	}
	starts, err := s.starts(ctx, c, found.collections)
	if err != nil {
		return collected{}, err
	}
	found.starts = starts
	diagnostic.Event(ctx, "sync.collect", diagnostic.Field{Key: "branches", Value: fmt.Sprint(len(found.collections))})
	return found, nil
}

// collecting is what deciding each branch reads: the selection, what the
// remote holds, and what the caller chose.
type collecting struct {
	remote, base string
	branches     []string
	onRemote     map[string]string
	take         Take
	parents      map[string]string
	// forks are the recorded fork points, keep the commits the caller chose
	// to keep, and command and takeCommand the pulls a refusal offers.
	forks                map[string]string
	keep                 []string
	command, takeCommand string
}

// collected is what collect decided across the selection.
type collected struct {
	collections                        []Collection
	stuck                              []divergence
	refusal                            *repair.Note
	drops, moves, kept, left, restored []Drop
	starts                             map[string]string
	unsynced                           []string
}

// verdict is what collect decided about one branch -- a collection, a
// divergence, a refusal, or none of them -- and the commits a sync point named
// on the way.
type verdict struct {
	collection                         *Collection
	stuck                              *divergence
	refusal                            *repair.Note
	drops, moves, kept, left, restored []Drop
	// unsynced reports a branch that differs from the remote with no sync
	// point to say whose the difference is.
	unsynced bool
}

// collectOne gives one branch one of collect's five answers, each returned
// where it is decided.
func (s Service) collectOne(ctx context.Context, c collecting, branch string) (verdict, error) {
	remote, base, onRemote, parents := c.remote, c.base, c.onRemote, c.parents
	if branch == base || onRemote[branch] == "" {
		// Not on the remote at all, which is ordinary for work in progress.
		return verdict{}, nil
	}
	published, err := s.Git.Resolve(ctx, localgit.IsolatedRef(remote, branch))
	if err != nil {
		return verdict{}, nil
	}
	local, err := s.Git.Resolve(ctx, branch)
	if err != nil {
		return verdict{}, err
	}
	if local == published {
		return verdict{}, nil
	}
	// This branch's own work, bounded at its parent as currency is: what is
	// below the parent is the parent's.
	parent := parentOrBase(parents, branch, base)
	// The published version's own work is bounded at the parent as the
	// remote holds it, when it is built on that. Bounded at the parent here,
	// whatever somebody else added to the parent and published -- or this
	// clone added to it and has not -- counted as this branch's: two people
	// each moving a different branch of one stack read as both moving this
	// one, and the only way offered through discarded one of them.
	begins, err := s.publishedParent(ctx, parent, published, onRemote)
	if err != nil {
		return verdict{}, err
	}
	// Before the fast-forward below: a branch somebody reset back to drop a
	// commit is an ancestor of the published version, and fast-forwarding it
	// would put the commit back.
	sync, decided, err := s.bySyncPoint(ctx, c, branch, local, published, parent, begins)
	if err != nil || decided {
		return sync, err
	}
	found, err := s.collectUnsynced(ctx, c, branch, local, published, parent, begins)
	found.kept, found.restored, found.unsynced = sync.kept, sync.restored, sync.unsynced
	return found, err
}

// collectUnsynced is collectOne for a branch its sync point does not settle,
// which is every branch without one: the rules pull had before sync points.
func (s Service) collectUnsynced(ctx context.Context, c collecting, branch, local, published, parent, begins string) (verdict, error) {
	remote, take, parents := c.remote, c.take, c.parents
	behind, err := s.Git.IsAncestor(ctx, branch, published)
	if err != nil {
		return verdict{}, err
	}
	if behind {
		return verdict{collection: &Collection{Branch: branch, To: published}}, nil
	}
	ahead, err := s.Git.IsAncestor(ctx, published, branch)
	if err != nil {
		return verdict{}, err
	}
	if ahead {
		// You have unpublished work. That is push's business, not sync's.
		return verdict{}, nil
	}
	ours, _, err := s.Git.Cherry(ctx, published, branch, parent)
	if err != nil {
		return verdict{}, err
	}
	// The published version is this one reworded or rebuilt in place: it
	// sits on the parent as it is here, and holds everything of the
	// branch's own. Taking it is what makes the two the same commits again.
	//
	// This used to ask whether the published version held everything
	// here by content, base and all — which a squash of a one-commit
	// branch satisfies, because the squash is the same patch as the commit
	// it replaced. So a branch above a squash-merged one-commit branch,
	// replayed here and not yet pushed, read as reworded, and the stale
	// published version was taken over the replay: every descent of
	// one-commit branches stopped at its second.
	onParent, err := s.Git.IsAncestor(ctx, parent, published)
	if err != nil {
		return verdict{}, err
	}
	if len(ours) == 0 && onParent {
		return verdict{collection: &Collection{Branch: branch, To: published, Superseded: true}}, nil
	}
	theirs, err := landed.Missing(ctx, s.Git, branch, localgit.IsolatedRef(remote, branch), cmp.Or(begins, parent))
	if err != nil {
		return verdict{}, err
	}
	if theirs == 0 {
		// The published version is this branch before it was replayed here:
		// everything it has is here by content, and what is here and not
		// there is the trunk it was replayed onto. That is unpublished work
		// as surely as an ordinary commit is, so it is push's business too.
		//
		// Counted by commit it reads as both sides moving -- the trunk's
		// commits are "here and not published", and once a parent has been
		// squashed its original commits are "published and not here" -- so
		// a second sync before pushing, and every land of three branches
		// whose bottom one had more than one commit, refused a branch
		// nobody else had touched. Missing excuses exactly that squashed
		// run, and nothing else: a reviewer's revert is still theirs.
		return verdict{}, nil
	}
	// Both sides hold something the other does not. With none of the
	// branch's own work left out of the published version, it is taken,
	// and the replay decides whether it can tell which of that version's
	// commits are the branch's.
	if len(ours) == 0 {
		return verdict{collection: &Collection{Branch: branch, To: published, Superseded: true, Begins: begins}}, nil
	}
	if take.AppliesTo(branch, parents) {
		// Asked for explicitly, and the commits it costs are carried so the
		// preview can name every one before anything happens.
		return verdict{collection: &Collection{Branch: branch, To: published, Superseded: true, Discards: ours, Begins: begins}}, nil
	}
	// Both sides moved, so say both. "You have work the remote does not" is
	// true of every ordinary commit, and a reader who has just made one has
	// no way to tell that from this.
	return verdict{stuck: &divergence{Branch: branch, Ours: len(ours), Theirs: theirs}}, nil
}

// publishedParent is the parent as the remote holds it, when the published
// version of the branch is built on it, and empty otherwise. It is the
// remote's own answer from this run, never the fetched ref alone: a parent the
// remote does not hold leaves the fetched ref stale or absent, and asking Git
// about an absent ref fails the whole pull.
func (s Service) publishedParent(ctx context.Context, parent, published string, onRemote map[string]string) (string, error) {
	tip := onRemote[parent]
	if tip == "" {
		return "", nil
	}
	built, err := s.Git.IsAncestor(ctx, tip, published)
	if err != nil || !built {
		return "", err
	}
	return tip, nil
}

// fetchList is the base and the selection, each named once. The base is
// normally the first selected branch as well, and asking for it twice put the
// same refspec in the command twice.
//
// What the remote actually has is asked separately, because git fetch fails the
// whole command on one ref it cannot find — and a branch that is gone because
// it merged is the commonest reason for it not to be there.
func fetchList(base string, branches []string) []string {
	wanted := []string{base}
	for _, branch := range branches {
		if branch != base {
			wanted = append(wanted, branch)
		}
	}
	return wanted
}

// collectionsEqual compares collections, including the commits each would
// discard. A plan that would lose different work is a different plan, and
// revalidation has to see that.
func collectionsEqual(left, right []Collection) bool {
	return slices.EqualFunc(left, right, func(a, b Collection) bool {
		return a.Branch == b.Branch && a.To == b.To && a.Superseded == b.Superseded &&
			a.Begins == b.Begins && slices.Equal(a.Discards, b.Discards) && slices.Equal(a.Dropped, b.Dropped)
	})
}

// parentOrBase is what a branch sits on in the selection, the base for a root.
func parentOrBase(parents map[string]string, branch, base string) string {
	if parent := parents[branch]; parent != "" {
		return parent
	}
	return base
}
