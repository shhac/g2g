package restack

// The package's vocabulary: the rewrite boundary, the service, and what a plan
// is made of.
//
// plan.go's own header says it decides what a restack would do and changes
// nothing, which was true of the algorithm and misleading about this. restack.go,
// resume.go and journal.go all depend on these, so a reader looking for the Git
// interface that governs the package had to find it in a file named for one
// operation.

import (
	"context"
	"slices"

	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/repair"
)

// Git is the rewrite boundary. It is the only interface in g2g permitted to
// change commit history, and only through the two engines below.
type Git interface {
	graph.Ancestry
	Clean(context.Context) error
	SupportsReplay(context.Context) (bool, error)
	PreviewReplay(context.Context, string, []localgit.Range) ([]localgit.RefUpdate, bool, error)
	Replay(context.Context, string, []localgit.Range) error
	Rebase(context.Context, string, localgit.Range) error
	RebaseContinue(context.Context) error
	RebaseAbort(context.Context) error
	RebaseSkip(context.Context) error
	RebaseInProgress(context.Context) (bool, error)
	ConflictedPaths(context.Context) ([]string, error)
	SwitchTree(ctx context.Context, from, to string) error
	SwitchBranch(ctx context.Context, branch string) error
	CherryDropped(context.Context, string, string) ([]string, error)
	Cherry(ctx context.Context, upstream, head, limit string) (absent, present []string, err error)
	Absorbed(ctx context.Context, base, branch string) (bool, error)
	PinForkPoint(context.Context, string, string) error
	UpdateBranch(context.Context, string, string) error
}

// Describer names commits by their subjects. It is optional: without it a
// preview still says how many commits a parent dropped, as it did before it
// could say which.
type Describer interface {
	Describe(ctx context.Context, ids []string) ([]localgit.Commit, error)
}

// Service rewrites stacks so their contents match their recorded structure.
type Service struct {
	Git     Git
	Graph   graph.Service
	Journal Journal
}

// Step is one branch's rewrite: replay its own commits onto a new base.
type Step struct {
	Branch string
	Parent string
	// Base is the object the branch will sit on.
	Base string
	// ForkPoint is where the branch's own commits begin. Base..Branch would be
	// the wrong range: it includes whatever the parent dropped or rewrote.
	ForkPoint string
	// Tip is the branch's current object, recorded so an abort can restore it.
	Tip string
	// Head is where the branch will be when the rewrite runs, which is Tip
	// unless the caller moves it first. Everything the plan measures about the
	// branch is measured here.
	Head string
	// Behind means the parent is being replayed too and this branch does not
	// contain the version of it being replayed: the parent gained commits
	// after this branch forked, or the caller is bringing in a version of the
	// parent this branch never had. Sharing the parent's replay would put it
	// back on the commit it forked from, so it lands on the parent's result
	// instead, in a replay of its own after the parent's.
	Behind bool
	// Orphans are commits the parent no longer has that this branch still
	// carries, and Absorbable reports that every one of them was genuinely
	// dropped rather than rewritten.
	Orphans    []string
	Absorbable bool
	// Collapses means every commit this branch owns is already in its new
	// base by content, so it has nothing left to contribute and its ref simply
	// moves there.
	//
	// Handling this here rather than leaving it to the engines is what makes
	// them agree. Whether a rewrite drops an already-upstream commit or
	// reapplies it changed between Git 2.54 and 2.55, and it is exactly the
	// case a restack exists for, so the commits are never handed over at all.
	Collapses bool
}

// Pending is where refs will be by the time the rewrite runs.
//
// A caller that moves refs itself between planning and applying would otherwise
// have the replay planned against tips that will not exist by then. sync's
// collect does exactly that -- it takes the published version of a branch --
// and the branches stacked above it were measured against where their parent
// used to be, so a child either replayed onto a commit its parent no longer
// pointed at or was judged to need no replay at all and left stranded there.
//
// The base already had this, through Onto.ToLocation naming the ref the trunk
// is about to be at. This is the same statement for the branches above it.
type Pending map[string]string

// at answers where a branch will be, given where Git currently says it is.
func (p Pending) at(branch, resolved string) string {
	if object, moving := p[branch]; moving {
		return object
	}
	return resolved
}

// Onto is where a rewrite lands, and separately what the graph should record.
//
// The two are not the same question, and conflating them corrupted the store.
// sync replays onto a fetched ref under refs/g2g/ because that is where the
// trunk is about to be; it is a location, not a parent, and recording it left
// every synced branch hanging from an internal ref that is not a local branch.
// A user's --onto is both: they are asking for the branch to move.
type Onto struct {
	// Object is what commits are replayed onto. Empty replays onto the
	// recorded parent, which is the ordinary restack.
	Object string
	// Parent is the branch the graph should record instead of the one it has.
	// Empty keeps the recorded parent, which is what a rewrite that moves
	// contents rather than structure wants.
	Parent string
}

// Reparents reports a rewrite that changes what the graph records.
func (o Onto) Reparents() bool { return o.Parent != "" }

// ToBranch is a rewrite the user asked for by naming a branch: it is both where
// the commits land and what the graph should say afterwards.
func ToBranch(branch string) Onto {
	if branch == "" {
		return Onto{}
	}
	return Onto{Object: branch, Parent: branch}
}

// ToLocation replays onto an object without claiming it as a parent.
func ToLocation(object string) Onto { return Onto{Object: object} }

// Plan is a complete rewrite, ordered parents before children.
type Plan struct {
	graph.Discovery
	Onto   Onto
	Absorb bool
	Steps  []Step
	// Lines are the leaves of a forked selection whose rewrite conflicts,
	// one per line of descent, which is how it can be taken instead.
	Lines []string
	// Updates is what a replay says the refs would become, and Clean reports
	// that it would apply without a conflict. Both come from a preview that
	// moves nothing.
	Updates []localgit.RefUpdate
	Clean   bool
	// Predicted records that the preview actually ran. A Git too old to
	// replay cannot say anything in advance, and "we could not look" must not
	// be reported as "we looked and it will conflict".
	Predicted bool
	// Unpredicted says why there is no preview, when there is none: Git too
	// old to replay, or a branch landing on a parent the caller is bringing
	// down first, whose result only exists once it has been brought.
	Unpredicted string
	// Held reports a refusal because a branch that would move is checked out
	// in another worktree.
	Held bool
	// Subjects names each orphan by its subject, so a preview can list what
	// it is about to drop rather than count it. Derived from the orphans'
	// ids, which Equal already compares.
	Subjects map[string]string
	// Repair is why an apply would refuse and the ways out, empty when it
	// would proceed. Most of restack's refusals are states rather than
	// choices and name no way out.
	Repair repair.Note
}

// Blocked is why an apply would refuse, as one sentence, empty when it would
// proceed.
func (p Plan) Blocked() string { return p.Repair.Sentence() }

// Nothing reports a plan with nothing to do: no branch to rewrite and no edge
// to record.
//
// An empty step list is not that question. A branch already sitting on the
// --onto target has no commits to move and a recorded parent still naming
// where it used to be, and a caller that skipped Apply for want of steps left
// it recorded there while saying there was nothing to replay.
func (p Plan) Nothing() bool {
	return len(p.Steps) == 0 && len(p.reparenting()) == 0
}

// Branches lists the branches this plan rewrites.
func (p Plan) Branches() []string {
	branches := make([]string, 0, len(p.Steps))
	for _, step := range p.Steps {
		branches = append(branches, step.Branch)
	}
	return branches
}

// Orphaned lists every commit a parent dropped that a child still carries.
func (p Plan) Orphaned() []string {
	orphans := make([]string, 0)
	for _, step := range p.Steps {
		orphans = append(orphans, step.Orphans...)
	}
	return orphans
}

// Absorbable reports whether every orphan was genuinely dropped, which is the
// only case where keeping them is coherent.
func (p Plan) Absorbable() bool {
	for _, step := range p.Steps {
		if len(step.Orphans) != 0 && !step.Absorbable {
			return false
		}
	}
	return len(p.Orphaned()) != 0
}

// Emptied lists branches the rewrite would leave with no commits of their own,
// because everything they carried is already in their new base.
//
// The comparison is against where the parent ends up, not where it is now: in
// a stack every branch above the bottom one is also being rewritten, so its
// current tip says nothing about the result. It is known from the plan rather
// than inferred from a preview, so it reads the same way on every Git.
func (p Plan) Emptied() []string {
	emptied := make([]string, 0)
	for _, step := range p.collapsing() {
		emptied = append(emptied, step.Branch)
	}
	return emptied
}

// Equal compares every fact that changes what the rewrite does.
func (p Plan) Equal(other Plan) bool {
	return p.Discovery.Equal(other.Discovery) &&
		p.Onto == other.Onto &&
		p.Absorb == other.Absorb &&
		p.Repair.Equal(other.Repair) &&
		p.Clean == other.Clean &&
		p.Predicted == other.Predicted &&
		slices.EqualFunc(p.Steps, other.Steps, func(left, right Step) bool {
			return left.Branch == right.Branch && left.Parent == right.Parent &&
				left.Base == right.Base && left.ForkPoint == right.ForkPoint &&
				left.Tip == right.Tip && left.Head == right.Head && left.Behind == right.Behind &&
				slices.Equal(left.Orphans, right.Orphans)
		})
}
