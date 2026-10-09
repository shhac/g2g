// Package sync brings a stack up to date with its remote: fetch, advance the
// base, replay, and forget what has landed.
//
// It is an orchestrator and owns no rules of its own. Each step is a service
// that already exists and is already previewable, so what this adds is the
// order and the honesty about how far it got.
package sync

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/shhac/g2g/internal/diagnostic"
	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/repair"
	"github.com/shhac/g2g/internal/restack"
	"github.com/shhac/g2g/internal/syncpoint"
)

// Ready reports a service with everything it needs.
//
// One rule, called by both the guard below and the command registration in
// internal/cli. They were two hand-written conjunctions before, and three of
// them had already drifted -- a command could be registered and then refuse on
// use, or be hidden from a build that could have run it.
//
// sync fetches, advances a base and replays, and the replay writes the
// recorded graph, so it needs all three. The registration gate asked for the
// store and the guard asked for the restacker, so a build with one and not the
// other either registered a command that fails on use or hid one that works.
func (s Service) Ready() bool {
	return s.Git != nil && s.Restack != nil && s.Graph.Store != nil
}

// Plan works out the whole sequence without performing any of it. The fetch is
// the one step that reaches the network, and it writes only into g2g's own
// ref namespace, so previewing costs the repository nothing.
func (s Service) Plan(ctx context.Context, selection graph.Selection, remote string, take Take) (Plan, error) {
	if !s.Ready() {
		return Plan{}, fmt.Errorf("sync service is not fully configured")
	}
	if err := s.Git.Remote(ctx, remote); err != nil {
		return Plan{}, err
	}
	// The stack being synced: its trunk, so there is a base to advance, and
	// everything above the target, so the replay covers what depends on it.
	// Cousins that merely share the trunk are somebody else's stack — unless
	// the caller asked for trunk, which is exactly the request to include them.
	discovery, err := s.Graph.Discover(ctx, graph.Selection{Branch: selection.Branch, Scope: syncScope(selection.Scope)})
	if err != nil {
		return Plan{}, err
	}
	if err := requireBase(selection, discovery); err != nil {
		return Plan{}, err
	}
	plan := Plan{Remote: remote, Base: discovery.Branches[0]}

	// The base and everything selected, in one fetch. Fetching only the base is
	// what left a reviewer's commit unreachable from here.
	published, err := s.fetch(ctx, remote, fetchList(plan.Base, discovery.Branches))
	if err != nil {
		return Plan{}, err
	}
	plan.Published = ownTips(published, plan.Base, discovery.Branches)
	s.recordLevel(ctx, remote, plan.Published)
	if note, outside := throughOutside(take, discovery.Branches, plan.Base); outside {
		return plan.refused(note), nil
	}
	parents := discovery.Graph.Shape().Parents
	base, err := s.compare(ctx, plan.Base, remote, published, take)
	if err != nil {
		return Plan{}, err
	}
	plan.Advance, plan.Supersede, plan.Diverged, plan.DiscardsBase = base.Advance, base.Supersede, base.Diverged, base.Discards
	if plan.Diverged {
		// Same reasoning as a branch: say that both sides moved, not that you
		// have something the remote does not, which is true of any commit.
		return plan.refused(repair.Note{
			Reason: fmt.Sprintf("both sides have moved on %s · it and %s/%s each hold commits the other does not", plan.Base, remote, plan.Base),
			Ways:   divergenceWays(selection, remote, take, parents, nil),
		}), nil
	}

	// The replay is planned against the base as it will be, which is why the
	// fetch and the fast-forward assessment come first.
	// A location, never a parent: the trunk is about to be here, and recording
	// a ref under refs/g2g/ as the parent is what broke every synced stack.
	collect, stuck, err := s.collect(ctx, remote, plan.Base, discovery.Branches, published, take, parents)
	if err != nil {
		return Plan{}, err
	}
	if len(stuck) != 0 {
		return plan.refused(repair.Note{Reason: divergenceReason(stuck), Ways: divergenceWays(selection, remote, take, parents, stuck)}), nil
	}
	plan.Collect = collect
	plan.Restack, err = s.Restack.Plan(ctx, selection, restack.ToLocation(plan.onto()), false, plan.pending())
	if err != nil {
		return Plan{}, err
	}
	// Its structure comes with it. A refusal that arrives from the step this
	// delegates to is no less actionable for having been delegated, and
	// carrying only the sentence handed every machine reader a null where the
	// ways out were.
	plan.Repair = plan.Restack.Repair
	// A fork whose replay conflicts is taken one line at a time, and from a
	// leaf sync's own stack scope is exactly that line — whereas the restack
	// command restack offers takes a scope sync does not.
	if len(plan.Restack.Lines) != 0 {
		plan = plan.refused(repair.Note{Reason: plan.Restack.Repair.Reason, Ways: lineWays(plan.Restack.Lines)})
	}
	diagnostic.Event(ctx, "sync.plan",
		diagnostic.Field{Key: "base", Value: plan.Base},
		diagnostic.Field{Key: "advance", Value: fmt.Sprintf("%t", plan.Advance)},
		diagnostic.Field{Key: "replays", Value: strings.Join(plan.Restack.Replaying(), ",")},
	)
	return plan, nil
}

// refused is this plan blocked for the reason note gives, with the sentence a
// machine reads derived from the same note a person reads.
func (p Plan) refused(note repair.Note) Plan {
	p.Repair = note
	return p
}

// throughOutside refuses a boundary naming something outside the selection. It
// resolves nothing and would look exactly like an ordinary refusal.
func throughOutside(take Take, branches []string, base string) (repair.Note, bool) {
	if !take.Bounded() || slices.Contains(branches, take.Through) || take.Through == base {
		return repair.Note{}, false
	}
	return repair.Note{
		Reason: fmt.Sprintf("--through %s is not in the stack being synced", take.Through),
		Ways: []repair.Step{
			{Effect: "name a branch this sync selects, or drop --through to take the whole stack"},
		},
	}, true
}

// lineWays is one pull per line of descent a conflicting replay was split
// into, each bringing that line up to date alone.
func lineWays(lines []string) []repair.Step {
	ways := make([]repair.Step, 0, len(lines))
	for _, leaf := range lines {
		ways = append(ways, repair.Step{Command: "g2g pull --branch " + repair.Quote(leaf), Effect: "bring the line of descent ending at " + leaf + " up to date"})
	}
	return ways
}

// requireBase refuses a selection with no base to bring up to date. A selection
// of one is the branch itself with nothing recorded under it — unless the base
// alone is what was asked for, and then it must be one.
func requireBase(selection graph.Selection, discovery graph.Discovery) error {
	if selection.Scope == graph.ScopeBranch {
		if discovery.Graph.Tracked(discovery.Target) {
			return fmt.Errorf("%q is stacked on something, and only a base is brought up to date alone", discovery.Target)
		}
		return nil
	}
	if len(discovery.Branches) < 2 {
		return fmt.Errorf("%q has no recorded parent to sync against · run g2g track to record one", discovery.Target)
	}
	return nil
}

// syncScope is the boundary this sync acts on.
//
// The default is the stack: the trunk moved, so everything above it is stale.
// trunk widens that to every stack on the same trunk, which is the whole of
// what a person means by "the trunk moved, bring everything up to date". The
// value is validated at the flag, so anything else here is a caller that did
// not go through it, and the default is the safe reading.
//
// branch is not offered at the flag. It is land's, after merging a declared
// trunk into what it lands into: that base is advanced and nothing replayed,
// because its stacks are other people's and a descent is not the moment to
// replay them.
func syncScope(scope graph.Scope) graph.Scope {
	switch scope {
	case graph.ScopeTrunk, graph.ScopeBranch:
		return scope
	}
	return graph.ScopeStack
}

// ownTips is what the remote holds for each selected branch but the base. A
// trunk is never given a sync point: what pull does to one is decided by the
// rules for a rewritten trunk, and a drop there is not anybody's to publish.
func ownTips(published map[string]string, base string, branches []string) map[string]string {
	own := make(map[string]string, len(branches))
	for _, branch := range branches {
		if tip := published[branch]; tip != "" && branch != base {
			own[branch] = tip
		}
	}
	return own
}

// recordLevel notes the agreement a branch is already in when it holds
// everything the remote has: level with it, or ahead of it with work to push.
// That is a fact rather than a decision, like the fetch into g2g's own refs a
// preview already makes, and it is what gives a branch nobody has pulled or
// pushed through g2g a sync point to measure the next change from.
func (s Service) recordLevel(ctx context.Context, remote string, published map[string]string) {
	recorder, ok := s.Git.(syncpoint.ReadRecorder)
	if !ok {
		return
	}
	for _, branch := range slices.Sorted(maps.Keys(published)) {
		local, err := s.Git.Resolve(ctx, branch)
		if err != nil {
			continue
		}
		if local != published[branch] {
			holds, err := s.Git.IsAncestor(ctx, published[branch], local)
			if err != nil || !holds {
				continue
			}
		}
		s.agree(ctx, recorder, remote, branch, localgit.SyncPoint{Tip: published[branch], Local: local, Command: "pull"})
	}
}

// agree records one agreement. pull has done or decided its work by then, so a
// recording that fails is a diagnostic: until the next one records it, the
// cautious reading applies.
func (s Service) agree(ctx context.Context, recorder syncpoint.ReadRecorder, remote, branch string, point localgit.SyncPoint) {
	if err := syncpoint.Agree(ctx, recorder, remote, branch, point); err != nil {
		diagnostic.Event(ctx, "sync.sync_point", diagnostic.Field{Key: "branch", Value: branch}, diagnostic.Field{Key: "decision", Value: "not recorded"})
	}
}
