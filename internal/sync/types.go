package sync

import (
	"context"
	"maps"
	"slices"

	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/repair"
	"github.com/shhac/g2g/internal/restack"
)

// Git is the boundary for the steps sync performs itself.
type Git interface {
	FetchIsolated(ctx context.Context, remote string, branches []string) error
	FastForward(ctx context.Context, branch, to string) error
	ResetBranch(ctx context.Context, branch, to string) error
	Resolve(ctx context.Context, revision string) (string, error)
	IsAncestor(ctx context.Context, ancestor, descendant string) (bool, error)
	Remote(ctx context.Context, name string) error
	// RemoteTips says which of these branches the remote still has. A fetch
	// naming a ref the remote deleted fails outright, and a branch being gone
	// because it merged is the ordinary case, not an error.
	RemoteTips(ctx context.Context, remote string, branches []string) (map[string]string, error)
	// Cherry answers whether the commits on one side are present on the other
	// by content, which is how a branch somebody else rebased is recognised as
	// still being your work rather than as a divergence.
	Cherry(ctx context.Context, upstream, head, limit string) (absent, present []string, err error)
	// Absorbed answers the same of a whole branch at once, which is the only
	// way to see that a published version's commits are here after its parent
	// was squashed: the squash is equivalent to none of them individually.
	Absorbed(ctx context.Context, base, branch string) (bool, error)
}

// Restacker is the replay step. It is an interface rather than the service
// itself because sync's own job is ordering, and ordering should be testable
// without standing up a rewrite engine.
type Restacker interface {
	Plan(ctx context.Context, selection graph.Selection, onto restack.Onto, absorb bool, pending restack.Pending) (restack.Plan, error)
	Apply(ctx context.Context, plan restack.Plan) error
	InProgress(ctx context.Context) (bool, error)
}

// Service composes the steps. Each is separately testable and separately
// previewable; this decides only what runs, in what order, and when to stop.
type Service struct {
	Git     Git
	Graph   graph.Service
	Restack Restacker
}

// Plan is what a sync would do, in the order it would do it.
type Plan struct {
	Restack restack.Plan
	Remote  string
	// Base is the branch the selection is rooted on, and Advance reports that
	// the remote has moved past where it currently is.
	Base    string
	Advance bool
	// Supersede means the published trunk is not a descendant of this one but
	// contains everything it has by content — somebody rewrote the trunk and
	// pushed it. Taking theirs loses nothing, so the stack is replayed onto it
	// rather than the whole sync refusing.
	Supersede bool
	// DiscardsBase are the trunk commits taking the published trunk would lose,
	// only ever non-empty when the caller asked for that.
	DiscardsBase []string
	// Diverged means the base cannot be reconciled without losing commits.
	// Nothing is attempted in that case: choosing between them is the user's
	// call, not a side effect.
	Diverged bool
	// Collect is the branches of your own whose published version is ahead of
	// the one here, and where it is.
	//
	// Without this a reviewer's commit could not be got onto your machine at
	// all: sync fetched exactly one ref, the base, so a branch you own was
	// never brought down and push then refused because the remote was ahead.
	Collect []Collection
	// Keep are the commits the caller asked to keep rather than drop, by full
	// id. Each must be a drop this pull would otherwise make, in either
	// direction, or the plan refuses it by name.
	Keep []string
	// Drops are commits this pull takes off a branch here because the remote
	// no longer has them, measured against where the two last agreed. Moves
	// are commits that left one branch and are in another, which are not
	// drops. Kept are drops the caller kept. Left are commits dropped here and
	// still on the remote, which this pull leaves for push to publish.
	// Restored are commits a branch lacked only because it was reset to a
	// remote-tracking ref older than its sync point, which the pull puts back.
	// Every one is listed, never counted: once a ref moves nothing else will
	// name them.
	Drops, Moves, Kept, Left, Restored []Drop
	// Starts are where branches this pull does not take begin, when the
	// branch below them is taken and they are built on its published version:
	// a commit moved from the parent into the branch is the branch's now.
	Starts map[string]string
	// Subjects names every commit the lists above hold, for the preview.
	// Derived from their ids, which Equal already compares.
	Subjects map[string]string
	// Published is what the remote held for each selected branch when this
	// was planned, and is what an apply records as the point this clone and
	// the remote now agree on: never read again at write time, because
	// another worktree's pull could have fetched past it in between.
	Published map[string]string
	// Repair is why an apply would refuse and the ways out, empty when it
	// would proceed. Where sync refuses it offers a choice — take the
	// published trunk, or reconcile it yourself — and a sentence holding both
	// is where a reader loses which words belong to which.
	Repair repair.Note
}

// Blocked is why an apply would refuse, as one sentence, empty when it would
// proceed.
func (p Plan) Blocked() string { return p.Repair.Sentence() }

// Collection is one branch of yours the remote has moved on, and how.
type Collection struct {
	Branch string
	// To is the published tip this branch would be brought to.
	To string
	// Discards are the commits taking the published version would lose. It is
	// only ever non-empty when the caller asked for that with --take, and the
	// preview names every one of them: this is the only way sync loses work.
	Discards []string
	// Superseded means the published version is not a descendant of this one
	// but contains everything it has by content — somebody rebased or amended
	// your branch and published it. Taking theirs keeps their work and loses
	// none of yours, and it is a reset rather than a fast-forward, so it is
	// named rather than treated as the same thing.
	Superseded bool
	// Dropped are the commits taking the published version takes off this
	// branch because the remote dropped them, as opposed to Discards, which
	// --take costs.
	Dropped []string
	// Begins is where the published version's own commits begin, when it is
	// built on the parent as the remote holds it rather than on the parent
	// here: somebody published this branch before this clone moved its
	// parent on. Without it the replay could not tell which of the published
	// version's commits are the branch's.
	Begins string
}

// Drop is one commit a pull names, and the branch it is about. To is the
// branch a moved commit is in now.
type Drop struct {
	Branch string
	Commit string
	To     string
}

// onto names the base the replay should land on. Until the base branch is
// advanced it is still where it was, so the fetched ref is what the replay has
// to target; when it is already level, the recorded structure already says.
func (p Plan) onto() string {
	if !p.Advance && !p.Supersede {
		return ""
	}
	return localgit.IsolatedRef(p.Remote, p.Base)
}

// Nothing reports a plan with no step to take: the base is level and there is
// nothing to replay. A commit kept with --keep is a step even when nothing
// moves: the apply is what records that this clone has seen it dropped and
// kept it, without which the next push would refuse to publish it.
func (p Plan) Nothing() bool {
	return !p.Advance && !p.Supersede && len(p.Collect) == 0 && len(p.Starts) == 0 && len(p.Kept) == 0 && len(p.Restack.Steps) == 0
}

// Equal compares every fact that changes what the sync does.
func (p Plan) Equal(other Plan) bool {
	return p.Remote == other.Remote &&
		collectionsEqual(p.Collect, other.Collect) &&
		p.Base == other.Base &&
		p.Advance == other.Advance &&
		p.Diverged == other.Diverged &&
		p.Supersede == other.Supersede &&
		slices.Equal(p.DiscardsBase, other.DiscardsBase) &&
		maps.Equal(p.Published, other.Published) &&
		slices.Equal(p.Keep, other.Keep) &&
		slices.Equal(p.Drops, other.Drops) &&
		slices.Equal(p.Moves, other.Moves) &&
		slices.Equal(p.Kept, other.Kept) &&
		slices.Equal(p.Left, other.Left) &&
		slices.Equal(p.Restored, other.Restored) &&
		maps.Equal(p.Starts, other.Starts) &&
		p.Repair.Equal(other.Repair) &&
		p.Restack.Equal(other.Restack)
}

// pending is where collect will leave each branch, which is what the replay has
// to be planned against: collect runs first, so by the time the rewrite happens
// a collected branch is no longer where Git said it was when this was planned.
//
// The base is one of them when sync advances it. restack never moves a trunk
// itself, so without being told it would not ask whether another worktree has
// the trunk checked out — the ordinary layout of a checkout with several — and
// sync would move it underneath that worktree.
func (p Plan) pending() restack.Pending {
	if len(p.Collect) == 0 && len(p.Starts) == 0 && p.onto() == "" {
		return restack.Pending{}
	}
	moving := restack.Pending{Tips: make(map[string]string, len(p.Collect)+1), Begins: maps.Clone(p.Starts)}
	if moving.Begins == nil {
		moving.Begins = map[string]string{}
	}
	for _, collection := range p.Collect {
		moving.Tips[collection.Branch] = collection.To
		if collection.Begins != "" {
			moving.Begins[collection.Branch] = collection.Begins
		}
	}
	if onto := p.onto(); onto != "" {
		moving.Tips[p.Base] = onto
	}
	return moving
}
