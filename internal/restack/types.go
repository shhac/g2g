package restack

import (
	"context"
	"slices"

	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/graph"
)

// The package's vocabulary: the rewrite boundary, the service, and what a plan
// is made of.
//
// plan.go's own header says it decides what a restack would do and changes
// nothing, which was true of the algorithm and misleading about this. restack.go,
// resume.go and journal.go all depend on these, so a reader looking for the Git
// interface that governs the package had to find it in a file named for one
// operation.

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
	CherryDropped(context.Context, string, string) ([]string, error)
	Cherry(ctx context.Context, upstream, head, limit string) (absent, present []string, err error)
	Absorbed(ctx context.Context, base, branch string) (bool, error)
	PinForkPoint(context.Context, string, string) error
	UpdateBranch(context.Context, string, string) error
}

// WorktreeReader reports branches other worktrees have checked out.
//
// It is an optional capability rather than a method on Git: every fake in the
// tests implements Git, and a rewrite that could not ask was safe before this
// check existed and stays safe now.
type WorktreeReader interface {
	CheckedOutElsewhere(ctx context.Context) (map[string]string, error)
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

// ranges are what the rewrite engines are given.
//
// Every range starts at the topmost step's fork point rather than at each
// branch's own. The engines replay the union of the ranges onto one base and
// update each named ref, so a chain has to be expressed as overlapping ranges
// from a single origin; per-branch origins ask them to place each branch
// directly on the base independently, which conflicts as soon as a branch
// depends on the one below it.
func (p Plan) ranges() []localgit.Range {
	rewriting := p.rewriting()
	if len(rewriting) == 0 {
		return nil
	}
	origin := rewriting[0].ForkPoint
	ranges := make([]localgit.Range, 0, len(rewriting))
	for _, step := range rewriting {
		ranges = append(ranges, localgit.Range{From: origin, To: step.Branch})
	}
	return ranges
}

// onto is the object the rewrite lands on: the base of the first step that
// still has commits, once any collapse below it has been accounted for.
func (p Plan) onto() string {
	rewriting := p.rewriting()
	if len(rewriting) == 0 {
		return ""
	}
	return rewriting[0].Base
}

// Replaying lists the branches whose commits are actually replayed, which is
// not every branch in the plan: one that collapses only has its ref moved.
func (p Plan) Replaying() []string {
	branches := make([]string, 0, len(p.Steps))
	for _, step := range p.rewriting() {
		branches = append(branches, step.Branch)
	}
	return branches
}

// rewriting is the steps that actually have commits to replay.
func (p Plan) rewriting() []Step {
	steps := make([]Step, 0, len(p.Steps))
	for _, step := range p.Steps {
		if !step.Collapses {
			steps = append(steps, step)
		}
	}
	return steps
}

// collapsing is the steps whose ref only has to move.
func (p Plan) collapsing() []Step {
	steps := make([]Step, 0)
	for _, step := range p.Steps {
		if step.Collapses {
			steps = append(steps, step)
		}
	}
	return steps
}

// reparenting is the branches this plan moves to a different parent, which is
// only ever the result of an explicit --onto.
func (p Plan) reparenting() map[string]string {
	moves := map[string]string{}
	if !p.Onto.Reparents() {
		// A rewrite that only moves contents records nothing. Deriving the move
		// from the replay target instead is what put refs/g2g/remotes/origin/main
		// in the store as a parent, on the ordinary sync path.
		return moves
	}
	for _, step := range p.Steps {
		if recorded, tracked := p.Graph.Parent(step.Branch); tracked && recorded != p.Onto.Parent {
			moves[step.Branch] = p.Onto.Parent
		}
	}
	return moves
}

// chain reports whether the steps form a single line of descent, which is the
// only shape the resumable engine can rewrite in one invocation.
func (p Plan) chain() bool {
	rewriting := p.rewriting()
	for index, step := range rewriting {
		if index == 0 {
			continue
		}
		if step.Parent != rewriting[index-1].Branch {
			return false
		}
	}
	return true
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
	// Updates is what a replay says the refs would become, and Clean reports
	// that it would apply without a conflict. Both come from a preview that
	// moves nothing.
	Updates []localgit.RefUpdate
	Clean   bool
	// Predicted records that the preview actually ran. A Git too old to
	// replay cannot say anything in advance, and "we could not look" must not
	// be reported as "we looked and it will conflict".
	Predicted bool
	// Blocked is why an apply would refuse, empty when it would proceed.
	Blocked string
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
		p.Blocked == other.Blocked &&
		p.Clean == other.Clean &&
		p.Predicted == other.Predicted &&
		slices.EqualFunc(p.Steps, other.Steps, func(left, right Step) bool {
			return left.Branch == right.Branch && left.Parent == right.Parent &&
				left.Base == right.Base && left.ForkPoint == right.ForkPoint &&
				left.Tip == right.Tip && slices.Equal(left.Orphans, right.Orphans)
		})
}
