package push

import (
	"context"

	"github.com/shhac/g2g/internal/landed"
	"github.com/shhac/g2g/internal/parallel"
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
)

// Rejected reports a branch the lease would refuse: the remote holds something
// this push would overwrite.
func (p Publication) Rejected() bool {
	return p.Standing == Unknown || p.Standing == Behind || p.Standing == Diverged
}

// UpToDate reports a branch the remote needs nothing from: either it already
// has it exactly, or the branch has nothing left to give.
func (p Publication) UpToDate() bool {
	return p.Standing == Current || p.Standing == Landed
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
// second what a branch missing from the remote may have landed in.
//
// A remote tip this repository does not have is not an error: it is what being
// behind looks like before a fetch, and refusing to plan would be a worse
// answer than saying so.
//
// Each branch is its own few process spawns and none depends on another, so
// they are asked at once, as status's other per-branch reads are: doctor asks
// this of every recorded branch.
func Compare(ctx context.Context, git Comparer, branches []string, tips map[string]string, below func(string) (parent, trunk string)) (map[string]Publication, error) {
	// Sized before the reads start, so each owns one element and needs no lock.
	results := make([]Publication, len(branches))
	err := parallel.Each(ctx, branches, func(ctx context.Context, index int, branch string) error {
		parent, trunk := below(branch)
		publication, err := compareOne(ctx, git, branch, tips[branch], parent, trunk)
		results[index] = publication
		return err
	})
	if err != nil {
		return nil, err
	}
	publishing := make(map[string]Publication, len(branches))
	for index, branch := range branches {
		publishing[branch] = results[index]
	}
	return publishing, nil
}

// compareOne is where one branch stands against the tip the remote holds for
// it, which is empty when the remote has no such branch.
func compareOne(ctx context.Context, git Comparer, branch, tip, parent, trunk string) (Publication, error) {
	if tip == "" {
		// Absent from the remote has two meanings, and they want opposite
		// answers: work nobody has seen, or work that merged and took the
		// branch with it. Asking per commit alone got the second one wrong on
		// the commonest way a branch lands -- a squash leaves no commit with an
		// equivalent, so a branch that merged and was deleted read as new and
		// was offered for republication.
		upstream, err := landed.Into(ctx, git, trunk, branch, "")
		if err != nil || !upstream {
			return Publication{Standing: New}, err
		}
		return Publication{Standing: Landed}, nil
	}
	local, err := git.Resolve(ctx, branch)
	if err != nil {
		return Publication{}, err
	}
	if local == tip {
		return Publication{Standing: Current}, nil
	}
	if _, err := git.Resolve(ctx, tip); err != nil {
		return Publication{Standing: Unknown}, nil
	}
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
	theirs, err := landed.Missing(ctx, git, branch, tip, parent)
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
