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
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/shhac/g2g/internal/diagnostic"
	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/landed"
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
	// Blocked is why an apply would refuse, empty when it would proceed.
	Blocked string
	// Repair is Blocked in the shape a caller can lay out.
	Repair repair.Note
}

// Nothing reports a plan with no branch to forget.
func (p Plan) Nothing() bool { return len(p.Landed) == 0 && len(p.ForgottenMissing) == 0 }

// Equal compares every fact that changes what the prune does.
func (p Plan) Equal(other Plan) bool {
	return p.Discovery.Equal(other.Discovery) &&
		p.Options == other.Options && maps.Equal(p.Delete, other.Delete) && maps.Equal(p.Tips, other.Tips) &&
		slices.Equal(p.ForgottenMissing, other.ForgottenMissing) &&
		p.Blocked == other.Blocked &&
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

func (s Service) PlanWithOptions(ctx context.Context, selection graph.Selection, options Options) (Plan, error) {
	if !s.Ready() {
		return Plan{}, fmt.Errorf("prune service is not fully configured")
	}
	discovery, err := s.Graph.Discover(ctx, selection)
	if err != nil {
		return Plan{}, err
	}
	plan := Plan{Discovery: discovery, Options: options, Landed: make([]string, 0), Missing: make([]string, 0)}
	local, err := s.Graph.Git.LocalBranches(ctx)
	if err != nil {
		return Plan{}, err
	}
	comparisons := map[string]graph.Edge{}
	for _, branch := range discovery.Branches {
		if edge, ok := comparisonEdge(discovery.Graph, branch, local); ok {
			comparisons[branch] = edge
		}
	}
	var assessed map[string]string
	if options.DeleteBranches {
		if s.Cleaner == nil {
			return Plan{}, fmt.Errorf("local branch deletion is not configured")
		}
		needed := map[string]bool{}
		for _, branch := range discovery.Branches {
			if slices.Contains(local, branch) {
				needed[branch] = true
			}
			if edge, comparable := comparisons[branch]; comparable {
				needed[edge.Parent] = true
			}
		}
		assessed, err = s.Cleaner.ResolveAll(ctx, slices.Sorted(maps.Keys(needed)))
		if err != nil {
			return Plan{}, err
		}
		plan.Tips = assessed
	}
	for _, branch := range discovery.Branches {
		_, tracked := discovery.Graph.Edges[branch]
		if !tracked {
			// A trunk is not a branch with work to land.
			continue
		}
		if discovery.States[branch] == graph.StateBranchMissing {
			plan.Missing = append(plan.Missing, branch)
			if options.ForgetMissing {
				plan.ForgottenMissing = append(plan.ForgottenMissing, branch)
			}
			continue
		}
		edge, comparable := comparisons[branch]
		if !comparable {
			continue
		}
		head := branch
		if options.DeleteBranches {
			head = assessed[branch]
			edge.Parent = assessed[edge.Parent]
			if head == "" || edge.Parent == "" {
				return Plan{}, fmt.Errorf("a branch disappeared before cleanup · preview again")
			}
		}
		landed, err := s.landed(ctx, head, edge)
		if err != nil {
			return Plan{}, err
		}
		if landed {
			plan.Landed = append(plan.Landed, branch)
		}
	}
	// Forgetting a parent while keeping its child would strand the child. A
	// child Git already shows on the branch below is recorded there; anything
	// else is reported rather than reparented — the rule untrack follows, for
	// the same reason.
	forgotten := append(slices.Clone(plan.Landed), plan.ForgottenMissing...)
	rehome, left, err := s.rehome(ctx, placements(discovery, forgotten))
	if err != nil {
		return Plan{}, err
	}
	plan.Rehome = rehome
	if len(left) != 0 {
		plan.Repair = strandedNote(left)
		plan.Blocked = plan.Repair.Sentence()
	}
	if options.DeleteBranches && plan.Blocked == "" {
		if err := s.planDeletions(ctx, &plan, assessed); err != nil {
			return Plan{}, err
		}
	}
	diagnostic.Event(ctx, "prune.plan",
		diagnostic.Field{Key: "selected", Value: strings.Join(discovery.Branches, ",")},
		diagnostic.Field{Key: "landed", Value: strings.Join(plan.Landed, ",")},
		diagnostic.Field{Key: "blocked", Value: plan.Blocked},
	)
	return plan, nil
}

// landed reports a branch with nothing left to contribute to its parent.
//
// The per-commit question comes first because it is the cheaper one and
// answers the ordinary case. The fork point limits it to the branch's own
// work, which is what separates "this branch has nothing left to contribute"
// from "some commit below it is already upstream".
//
// A squash merge answers no to that and yes to this: its commits have no
// individual equivalent in the parent, and merging the branch in changes the
// parent not at all. A Git too old to be asked says no, which costs the
// squash-merge case and nothing else.
func (s Service) landed(ctx context.Context, branch string, edge graph.Edge) (bool, error) {
	return landed.Into(ctx, s.Git, edge.Parent, branch, edge.ForkPoint)
}

// comparisonEdge includes inherited work when a parent disappeared. Limiting
// to this child's own commits could delete the only surviving ref carrying its
// missing parent's unlanded work. The first edge above a live ancestor bounds
// the whole range that would disappear, without counting that base's commits.
func comparisonEdge(recorded graph.Graph, branch string, local []string) (graph.Edge, bool) {
	edge, tracked := recorded.Edges[branch]
	if !tracked || !slices.Contains(local, branch) {
		return graph.Edge{}, false
	}
	if slices.Contains(local, edge.Parent) {
		return edge, true
	}
	path, err := recorded.Path(branch)
	if err != nil {
		return graph.Edge{}, false
	}
	for index := len(path) - 2; index >= 0; index-- {
		if slices.Contains(local, path[index]) {
			edge.Parent = path[index]
			edge.ForkPoint = recorded.Edges[path[index+1]].ForkPoint
			return edge, true
		}
	}
	return graph.Edge{}, false
}

// Revalidate repeats discovery immediately before the write and refuses if the
// answer moved.
func (s Service) Revalidate(ctx context.Context, selection graph.Selection, preview Plan) (Plan, error) {
	current, err := s.PlanWithOptions(ctx, selection, preview.Options)
	if err != nil {
		return Plan{}, err
	}
	if err := diagnostic.Revalidated(ctx, "prune", "plan", current.Equal(preview)); err != nil {
		return Plan{}, err
	}
	return current, nil
}

// Apply forgets the selected records, recording each evidenced surviving child
// on the branch below. Explicit local deletion uses the assessed tips as leases.
func (s Service) Apply(ctx context.Context, plan Plan) error {
	if plan.Blocked != "" {
		return fmt.Errorf("%s", plan.Blocked)
	}
	if plan.Nothing() {
		return nil
	}
	if s.Graph.Store == nil {
		return fmt.Errorf("prune service has no graph store")
	}
	adopted, err := s.Graph.Store.Load(ctx)
	if err != nil {
		return err
	}
	if !adopted.Equal(plan.Discovery.Graph) {
		return fmt.Errorf("the graph changed before pruning · preview again")
	}
	if len(plan.Delete) != 0 {
		if s.Cleaner == nil {
			return fmt.Errorf("local branch deletion is not configured")
		}
		now, err := s.Cleaner.ResolveAll(ctx, slices.Sorted(maps.Keys(plan.Tips)))
		if err != nil {
			return err
		}
		if !maps.Equal(now, plan.Tips) {
			return fmt.Errorf("branches or their bases changed before cleanup · preview again")
		}
	}
	diagnostic.Event(ctx, "prune.apply", diagnostic.Field{Key: "branches", Value: strings.Join(plan.Landed, ",")})
	for _, child := range slices.Sorted(maps.Keys(plan.Rehome)) {
		if adopted, err = adopted.Track(child, plan.Rehome[child]); err != nil {
			return err
		}
	}
	// Delete first, while the graph still records the branches. A failure
	// leaves a record that --forget-missing can recover, rather than hiding
	// a surviving local branch from the next cleanup.
	deleted := []string{}
	for _, branch := range slices.Sorted(maps.Keys(plan.Delete)) {
		if s.Cleaner == nil {
			return fmt.Errorf("local branch deletion is not configured")
		}
		if err := s.Cleaner.DeleteBranchAt(ctx, branch, plan.Delete[branch]); err != nil {
			return partial(plan, deleted, nil, err)
		}
		deleted = append(deleted, branch)
	}
	forgotten := append(slices.Clone(plan.Landed), plan.ForgottenMissing...)
	if err := s.Graph.Store.Save(ctx, adopted.Untrack(forgotten...)); err != nil {
		return partial(plan, deleted, nil, err)
	}
	if s.Graph.Refs == nil {
		return nil
	}
	for _, child := range slices.Sorted(maps.Keys(plan.Rehome)) {
		if err := s.Graph.Refs.PinForkPoint(ctx, child, plan.Rehome[child].ForkPoint); err != nil {
			return partial(plan, deleted, forgotten, err)
		}
	}
	// A fork point outlives the edge it belonged to unless it is released, and
	// a stale pin keeps objects reachable that nothing refers to any more.
	for _, branch := range forgotten {
		if err := s.Graph.Refs.UnpinForkPoint(ctx, branch); err != nil {
			return partial(plan, deleted, forgotten, err)
		}
	}
	return nil
}
