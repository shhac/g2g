package cli

import (
	"context"
	"maps"
	"slices"

	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/prune"
	"github.com/shhac/g2g/internal/restack"
	"github.com/shhac/g2g/internal/stack"
)

// idleGuard refuses a mutation while a restack is unfinished.
//
// Three questions, named, rather than one function told apart by its
// argument: RequireIdle refuses while any worktree has an unfinished restack,
// which is every command that cannot say what it touches; RequireIdleHere
// refuses while this worktree has one, which is what a command that can say
// asks before it has discovered what; RequireApart refuses one whose
// footprint another worktree's restack moves or reads, once it has.
type idleGuard interface {
	RequireIdle(ctx context.Context) error
	RequireIdleHere(ctx context.Context) error
	RequireApart(ctx context.Context, touching restack.Footprint) error
}

var _ idleGuard = restack.Service{}

// restackGuard refuses a mutation while a restack is unfinished. A service
// that was never configured guards nothing, which is what keeps a root command
// built without one working exactly as before.
func restackGuard(service restack.Service) idleGuard {
	if service.Journal == nil {
		return nil
	}
	return service
}

// guardBefore refuses, before anything is discovered, a command that cannot
// say which branches it touches, and any command in the worktree an unfinished
// restack belongs to: a half-rewritten checkout can fail discovery before the
// refusal that names the restack is reached.
func (f applyFlow[P]) guardBefore(ctx context.Context) error {
	if f.guard == nil {
		return nil
	}
	if f.touches == nil {
		return f.guard.RequireIdle(ctx)
	}
	return f.guard.RequireIdleHere(ctx)
}

// guardAfter refuses a plan touching a branch an unfinished restack moves.
func (f applyFlow[P]) guardAfter(ctx context.Context, plan P) error {
	if f.guard == nil || f.touches == nil {
		return nil
	}
	return f.guard.RequireApart(ctx, f.touches(plan))
}

// snapshotTouches is every branch a selection acts on and the parents it
// measures them against. Publishing moves nothing a restack replays onto, so
// they are what it reads.
func snapshotTouches(snapshot stack.Snapshot) restack.Footprint {
	touching := append([]string{snapshot.Base}, snapshot.Branches...)
	for _, parent := range snapshot.Parents {
		touching = append(touching, parent)
	}
	return restack.Reads(touching...)
}

// untrackTouches is the branches whose records an untrack drops, with the
// parents they were recorded on and the children it leaves pointing at them.
// Forgetting a branch's record is a move: a restack stopped while replaying
// onto that branch plans again from the record when it is finished.
func untrackTouches(plan graph.UntrackPlan) restack.Footprint {
	reads := slices.Concat(plan.Orphaned, plan.Dependents)
	for _, branch := range plan.Removed {
		reads = append(reads, plan.Graph.Edges[branch].Parent)
	}
	return restack.Moves(slices.Concat(plan.Removed, plan.Undeclared)...).With(restack.Reads(reads...))
}

// pruneTouches is the records and branches a prune forgets, deletes or
// records on the branch below, and every branch it judged to decide that,
// with their parents. A branch a restack is part-way through moving cannot be
// judged: the graph still records where it was, so "has this landed" would be
// asked over a range that is no longer the branch's own.
func pruneTouches(plan prune.Plan) restack.Footprint {
	moves := slices.Concat(plan.Landed, plan.ForgottenMissing, slices.Collect(maps.Keys(plan.Delete)), slices.Collect(maps.Keys(plan.Rehome)))
	reads := slices.Clone(plan.Discovery.Branches)
	for _, branch := range plan.Discovery.Branches {
		reads = append(reads, plan.Discovery.Graph.Edges[branch].Parent)
	}
	for _, edge := range plan.Rehome {
		reads = append(reads, edge.Parent)
	}
	return restack.Moves(moves...).With(restack.Reads(reads...))
}

// adoptTouches is the branches an adoption records an edge for, with the
// parents it records them on and the recorded branches it measured.
func adoptTouches(plan graph.StackPlan) restack.Footprint {
	reads := slices.Concat([]string{plan.Trunk, plan.NewTrunk}, plan.Already, plan.Conflicts)
	for _, adoption := range plan.Record {
		reads = append(reads, adoption.Parent)
	}
	return restack.Moves(plan.Branches()...).With(restack.Reads(reads...))
}
