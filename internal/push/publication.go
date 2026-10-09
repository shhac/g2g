package push

import (
	"context"

	"github.com/shhac/g2g/internal/landed"
	"github.com/shhac/g2g/internal/parallel"
	"github.com/shhac/g2g/internal/syncpoint"
)

// Publication is where one branch stands against the remote's tip, and so what
// pushing it would do.
type Publication struct {
	Standing Standing
	// Ours is how many commits the local branch has that the remote's tip does
	// not, which is what the push sends. Theirs is how many of the remote's
	// commits have no equivalent in the branch, by content: the work a push
	// would drop. Counting those by commit id called every replayed commit
	// somebody else's, so a restacked stack could never be published.
	Ours, Theirs int
	// Dropped is how many commits the drop this standing is about takes off
	// one side: what Dropping removes from the remote, or what Restoring
	// would put back on it.
	Dropped int
	// CarriedBy names a branch stacked on this one that already holds the
	// remote's tip: this branch was moved back by hand past commits its child
	// still carries, so the remote being ahead is not somebody else's work to
	// pull. Compare never sets it, because it does not know the structure; a
	// caller that does says so.
	CarriedBy string
}

// Standing is one of the ways a branch and the remote's tip can differ. They
// are exclusive, which is why this is one value rather than a flag each: every
// reader of the flags had to work the exclusivity out again, in its own order,
// and got the same answer only because of how Compare happened to set them.
type Standing int

const (
	// Uncompared is the zero value, so a branch nobody compared reads as
	// exactly that. Reading as "up to date" was the one wrong answer the zero
	// value used to give, and every reader carried a flag to avoid it.
	Uncompared Standing = iota
	// Current means the remote holds this branch exactly.
	Current
	// New means the remote has no such branch yet, so there is nothing to
	// compare and nothing to overwrite.
	New
	// Landed means the branch has no work the base does not already have. A
	// branch that merged and was deleted looks exactly like a new one from the
	// remote's side, and offering to put it back is the wrong reading: it is
	// gone because it is finished.
	Landed
	// Unknown means the remote is on a commit this repository does not have,
	// so the two cannot be compared without fetching. It is treated exactly
	// like being behind, because that is what it most likely is.
	Unknown
	// Ahead means the branch has Ours commits on top of what the remote holds.
	Ahead
	// Behind means the remote has Theirs commits the branch does not.
	Behind
	// Diverged means both: Ours here, and Theirs a push would drop.
	Diverged
	// Rewritten means the published version is not an ancestor of the branch
	// and yet holds nothing the branch lacks, by content: the branch was
	// replayed since it was pushed. Publishing replaces the old version and
	// loses nothing, which is the ordinary state after a restack.
	Rewritten
	// Dropping means the remote holds commits this branch dropped since the
	// two last agreed, and nothing else it lacks: publishing removes them,
	// which is what dropping them meant.
	Dropping
	// Restoring means this branch still holds commits the remote dropped
	// since the two last agreed: publishing would put them back. pull drops
	// them here too, or keeps them as this clone's own with --keep.
	Restoring
)

// Rejected reports a branch the lease would refuse: the remote holds something
// this push would overwrite.
func (p Publication) Rejected() bool {
	return p.Standing == Unknown || p.Standing == Behind || p.Standing == Diverged || p.Standing == Restoring
}

// UpToDate reports a branch the remote needs nothing from: either it already
// has it exactly, or the branch has nothing left to give.
func (p Publication) UpToDate() bool {
	return p.Standing == Current || p.Standing == Landed
}

// Unpublished reports a branch the remote does not hold as it is here, which a
// push would publish without overwriting anything: never pushed, replayed
// since, or with work on top.
func (p Publication) Unpublished() bool {
	return p.Standing == New || p.Standing == Rewritten || p.Standing == Ahead || p.Standing == Dropping
}

// Comparer is the part of Git a comparison reads, all of it local: the tips
// are already in hand, so saying what they mean costs nothing over the network.
type Comparer interface {
	landed.Lineage
	Resolve(context.Context, string) (string, error)
	Divergence(ctx context.Context, other, target string) (ahead, behind int, err error)
}

// Compare says what publishing each branch over the given remote tips would
// do. below names the branch each one sits on and the trunk its stack stands
// on: the first is what a squashed parent's commits would have landed in, the
// second what a branch missing from the remote may have landed in. remote is
// whose sync points say what was dropped since the last agreement; empty asks
// none.
//
// A remote tip this repository does not have is not an error: it is what being
// behind looks like before a fetch, and refusing to plan would be a worse
// answer than saying so.
//
// Each branch is its own few process spawns and none depends on another, so
// they are asked at once, as status's other per-branch reads are: doctor asks
// this of every recorded branch.
func Compare(ctx context.Context, git Comparer, remote string, branches []string, tips map[string]string, below func(string) (parent, trunk string)) (map[string]Publication, error) {
	publishing, _, err := compare(ctx, git, remote, branches, tips, below)
	return publishing, err
}

// compare is Compare with what each sync point said, which only push's own
// plan reads: it lists every commit a drop concerns.
func compare(ctx context.Context, git Comparer, remote string, branches []string, tips map[string]string, below func(string) (parent, trunk string)) (map[string]Publication, map[string]syncpoint.Changes, error) {
	// Sized before the reads start, so each owns one element and needs no lock.
	results := make([]Publication, len(branches))
	changes := make([]syncpoint.Changes, len(branches))
	err := parallel.Each(ctx, branches, func(ctx context.Context, index int, branch string) error {
		parent, trunk := below(branch)
		publication, changed, err := compareOne(ctx, git, remote, branch, tips[branch], parent, trunk, tips[parent])
		results[index], changes[index] = publication, changed
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	publishing := make(map[string]Publication, len(branches))
	changed := make(map[string]syncpoint.Changes, len(branches))
	for index, branch := range branches {
		publishing[branch] = results[index]
		if changes[index].Dropped() || changes[index].Unsynced {
			changed[branch] = changes[index]
		}
	}
	return publishing, changed, nil
}

// compareOne is where one branch stands against the tip the remote holds for
// it, which is empty when the remote has no such branch.
func compareOne(ctx context.Context, git Comparer, remote, branch, tip, parent, trunk, publishedParent string) (Publication, syncpoint.Changes, error) {
	if tip == "" {
		// Absent from the remote has two meanings, and they want opposite
		// answers: work nobody has seen, or work that merged and took the
		// branch with it. Asking per commit alone got the second one wrong on
		// the commonest way a branch lands -- a squash leaves no commit with an
		// equivalent, so a branch that merged and was deleted read as new and
		// was offered for republication.
		upstream, err := landed.Into(ctx, git, trunk, branch, "")
		if err != nil || !upstream {
			return Publication{Standing: New}, syncpoint.Changes{}, err
		}
		return Publication{Standing: Landed}, syncpoint.Changes{}, nil
	}
	local, err := git.Resolve(ctx, branch)
	if err != nil {
		return Publication{}, syncpoint.Changes{}, err
	}
	if local == tip {
		return Publication{Standing: Current}, syncpoint.Changes{}, nil
	}
	if _, err := git.Resolve(ctx, tip); err != nil {
		return Publication{Standing: Unknown}, syncpoint.Changes{}, nil
	}
	// Before the content comparison: a drop on either side reads as being
	// behind or diverged by it, and what decides which is whose drop it was.
	publication, changes, ok, err := bySyncPoint(ctx, git, remote, branch, local, tip, parent, publishedParent)
	if err != nil || ok {
		return publication, changes, err
	}
	publication, err = compareApart(ctx, git, branch, tip, parent, publishedParent)
	return publication, syncpoint.Changes{Unsynced: changes.Unsynced}, err
}

// compareApart is compareOne for a branch and a remote tip that differ and no
// sync point settles: what each holds that the other does not, by content.
//
// The remote's side is bounded at the parent as the remote holds it, when the
// remote's version is built on that, as pull's is. Bounded at the parent here,
// a commit somebody published on the parent counted as this branch's: after
// one person replayed a stack over another's commit on the parent, every
// branch above it read as dropping that commit, which is the parent's.
func compareApart(ctx context.Context, git Comparer, branch, tip, parent, publishedParent string) (Publication, error) {
	behind, ours, err := git.Divergence(ctx, tip, branch)
	if err != nil {
		return Publication{}, err
	}
	if behind == 0 {
		return Publication{Standing: Ahead, Ours: ours}, nil
	}
	if ours == 0 {
		// Only behind: the branch has nothing the remote lacks, so every one of
		// the remote's commits is missing here by definition. Comparing their
		// content said the same thing at the cost of all of them — a trunk not
		// updated for a while is thousands.
		return Publication{Standing: Behind, Theirs: behind}, nil
	}
	// The remote tip is not an ancestor. Whether that loses anything is a
	// question of content, and it is the same one status asks of a pull
	// request's head, asked the same way.
	theirsFrom := parent
	if publishedParent != "" {
		built, err := git.IsAncestor(ctx, publishedParent, tip)
		if err != nil {
			return Publication{}, err
		}
		if built {
			theirsFrom = publishedParent
		}
	}
	theirs, err := landed.Missing(ctx, git, branch, tip, theirsFrom)
	if err != nil {
		return Publication{}, err
	}
	return Publication{Standing: standingApart(ours, theirs), Ours: ours, Theirs: theirs}, nil
}

// standingApart names a branch and a remote tip that are not in order, from
// what each holds that the other does not.
func standingApart(ours, theirs int) Standing {
	switch {
	case theirs == 0:
		return Rewritten
	case ours == 0:
		return Behind
	default:
		return Diverged
	}
}
