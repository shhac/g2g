// Package prune forgets branches whose work has landed.
//
// It was the tail of sync, which made it the one part of that command reading
// a different selection from the rest of it, and the one part no test ever
// executed: sync's tests built a graph service with no ref writer, so the
// fork-point unpin returned early every time.
//
// It is a separate command because it answers a different question. sync asks
// what the remote has that this stack does not; prune asks what this stack has
// that the trunk already contains. They share a boundary and nothing else: one
// advances branches, the other edits the record and optionally deletes local
// branches whose assessed work is already upstream.
package prune

import (
	"context"
	"maps"
	"slices"

	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/repair"
)

// Git compares branches by content, and has to do it two ways.
//
// Cherry answers per commit, which covers a branch rebased or cherry-picked
// into its parent. It cannot see a squash merge: the squash combines the
// branch's commits into one, so that commit is equivalent to none of them
// individually and every one reads as new — while the branch as a whole
// contributes nothing. Absorbed merges the branch in and looks for the
// parent's own tree back, which asks it of the whole branch at once.
//
// This command exists to forget branches whose work has landed, and it had
// only the half that misses the commonest way they land. graph, which has both,
// said "already in the trunk · run g2g prune to forget them" about branches
// prune then found nothing to forget.
type Git interface {
	Cherry(ctx context.Context, upstream, head, limit string) (absent, present []string, err error)
	Absorbed(ctx context.Context, base, branch string) (bool, error)
}

// Service reads the recorded graph and Git. It writes the graph and, only
// when explicitly asked, deletes assessed local branch tips.
type Service struct {
	Git   Git
	Graph graph.Service
	// Cleaner is required only for explicit local branch deletion.
	Cleaner Cleaner
}

type Cleaner interface {
	ResolveAll(context.Context, []string) (map[string]string, error)
	BranchHolders(context.Context) (map[string]string, error)
	DeleteBranchAt(context.Context, string, string) error
}

type Options struct {
	DeleteBranches bool
	ForgetMissing  bool
}

// Plan is what a prune would forget.
type Plan struct {
	Discovery graph.Discovery
	// Landed is the branches whose work is entirely in their parent, in the
	// order they were selected.
	Landed []string
	// Missing are recorded branches that are no longer local: deleted or
	// renamed with plain Git. Nothing can be asked of Git about them, so they
	// are not judged landed; --forget-missing or untrack forgets a stale edge.
	Missing []string
	Options Options
	// Delete pins each deletion to the tip whose content was assessed.
	Delete map[string]string
	// Tips also pins the bases whose content justified each deletion.
	Tips             map[string]string
	ForgottenMissing []string
	// Rehome is the edge each surviving child of a forgotten branch is
	// recorded with instead, keyed by the child. Only a child Git already shows
	// sitting on the branch below is in it: see rehome.
	Rehome map[string]graph.Edge
	// Repair is why an apply would refuse and the ways out, empty when it
	// would proceed.
	Repair repair.Note
}

// Blocked is why an apply would refuse, as one sentence, empty when it would
// proceed.
func (p Plan) Blocked() string { return p.Repair.Sentence() }

// Nothing reports a plan with no branch to forget.
func (p Plan) Nothing() bool { return len(p.Landed) == 0 && len(p.ForgottenMissing) == 0 }

// Equal compares every fact that changes what the prune does.
func (p Plan) Equal(other Plan) bool {
	return p.Discovery.Equal(other.Discovery) &&
		p.Options == other.Options && maps.Equal(p.Delete, other.Delete) && maps.Equal(p.Tips, other.Tips) &&
		slices.Equal(p.ForgottenMissing, other.ForgottenMissing) &&
		p.Repair.Equal(other.Repair) &&
		slices.Equal(p.Landed, other.Landed) &&
		slices.Equal(p.Missing, other.Missing) &&
		maps.Equal(p.Rehome, other.Rehome)
}

// Ready reports a service with everything it needs.
//
// One rule, called by both the guard below and the command registration in
// internal/cli. They were two hand-written conjunctions before, and three of
// them had already drifted -- a command could be registered and then refuse on
// use, or be hidden from a build that could have run it.
//
// Apply loads and saves the graph, so the store is as required as the Git
// client the guard used to ask for alone.
func (s Service) Ready() bool {
	return s.Git != nil && s.Graph.Store != nil
}

// Plan works out what has landed without changing anything.
func (s Service) Plan(ctx context.Context, selection graph.Selection) (Plan, error) {
	return s.PlanWithOptions(ctx, selection, Options{})
}
