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
// moves branches, the other edits the record and deletes nothing.
package prune

import (
	"context"
	"fmt"
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

// Service reads the recorded graph and Git, and writes only the graph.
type Service struct {
	Git   Git
	Graph graph.Service
}

// Plan is what a prune would forget.
type Plan struct {
	Discovery graph.Discovery
	// Landed is the branches whose work is entirely in their parent, in the
	// order they were selected.
	Landed []string
	// Missing are recorded branches that are no longer local: deleted or
	// renamed with plain Git. Nothing can be asked of Git about them, so they
	// are not judged landed; untrack is what forgets a stale edge.
	Missing []string
	// Blocked is why an apply would refuse, empty when it would proceed.
	Blocked string
	// Repair is Blocked in the shape a caller can lay out.
	Repair repair.Note
}

// Nothing reports a plan with no branch to forget.
func (p Plan) Nothing() bool { return len(p.Landed) == 0 }

// Equal compares every fact that changes what the prune does.
func (p Plan) Equal(other Plan) bool {
	return p.Discovery.Equal(other.Discovery) &&
		p.Blocked == other.Blocked &&
		slices.Equal(p.Landed, other.Landed) &&
		slices.Equal(p.Missing, other.Missing)
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
	if !s.Ready() {
		return Plan{}, fmt.Errorf("prune service is not fully configured")
	}
	discovery, err := s.Graph.Discover(ctx, selection)
	if err != nil {
		return Plan{}, err
	}
	plan := Plan{Discovery: discovery, Landed: make([]string, 0), Missing: make([]string, 0)}
	for _, branch := range discovery.Branches {
		edge, tracked := discovery.Graph.Edges[branch]
		if !tracked {
			// A trunk is not a branch with work to land.
			continue
		}
		if discovery.States[branch] == graph.StateBranchMissing {
			plan.Missing = append(plan.Missing, branch)
			continue
		}
		landed, err := s.landed(ctx, branch, edge)
		if err != nil {
			return Plan{}, err
		}
		if landed {
			plan.Landed = append(plan.Landed, branch)
		}
	}
	// Forgetting a parent while keeping its child would strand the child, and
	// this command reports rather than reparents — the same rule untrack
	// follows, for the same reason.
	if stranded, children := s.stranded(discovery, plan.Landed); len(stranded) != 0 {
		plan.Repair = repair.Note{
			Reason: "forgetting " + strings.Join(stranded, ", ") + " would strand branches recorded under them",
			Ways:   strandedWays(discovery, plan.Landed, children),
		}
		plan.Blocked = plan.Repair.Sentence()
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

// stranded names the branches that would be forgotten while something recorded
// under them survives, and those survivors.
func (s Service) stranded(discovery graph.Discovery, landed []string) (stranded, children []string) {
	forgetting := make(map[string]bool, len(landed))
	for _, branch := range landed {
		forgetting[branch] = true
	}
	stranded, children = make([]string, 0), make([]string, 0)
	for _, branch := range landed {
		surviving := false
		for _, child := range discovery.Graph.Children(branch) {
			if !forgetting[child] {
				surviving = true
				children = append(children, child)
			}
		}
		if surviving {
			stranded = append(stranded, branch)
		}
	}
	return stranded, children
}

// strandedWays is how to get past a refusal to strand, one way per child.
//
// It used to offer widening the selection, which only helps when the child has
// landed too -- and the ordinary way to get here is a parent squash-merged and
// synced, whose child has work of its own and is exactly why it survives. That
// sent people round in a circle. Recording each child on what the landed
// branch sat on is what leaves nothing to strand. After a sync the child
// already sits there, so track records it without moving anything; before one
// it does not, track refuses a parent that is not an ancestor, and sync is
// what puts it there. Widening is still offered where a child lies outside
// the selection, because that child has not been asked about.
func strandedWays(discovery graph.Discovery, landed, children []string) []repair.Step {
	forgetting := make(map[string]bool, len(landed))
	for _, branch := range landed {
		forgetting[branch] = true
	}
	ways := make([]repair.Step, 0, len(children)+2)
	outside := false
	for _, child := range children {
		onto := survivor(discovery.Graph, discovery.Graph.Edges[child].Parent, forgetting)
		ways = append(ways, repair.Step{
			Command: fmt.Sprintf("g2g track --branch %s --parent %s", child, onto),
			Effect:  fmt.Sprintf("record %s on %s, where g2g sync leaves it, then prune again", child, onto),
		})
		outside = outside || !slices.Contains(discovery.Branches, child)
	}
	if outside {
		ways = append(ways, repair.Step{Effect: "widen the selection with --scope, if those branches have landed too"})
	}
	return append(ways, repair.Step{Command: "g2g untrack", Effect: "forget them deliberately"})
}

// survivor is the nearest branch at or below this one that is not being
// forgotten, which is where a child of a landed branch belongs.
//
// Bounded by the recorded edges, because a record naming a cycle must end the
// walk rather than the command.
func survivor(recorded graph.Graph, branch string, forgetting map[string]bool) string {
	for range len(recorded.Edges) + 1 {
		if !forgetting[branch] {
			return branch
		}
		edge, tracked := recorded.Edges[branch]
		if !tracked {
			return branch
		}
		branch = edge.Parent
	}
	return branch
}

// Revalidate repeats discovery immediately before the write and refuses if the
// answer moved.
func (s Service) Revalidate(ctx context.Context, selection graph.Selection, preview Plan) (Plan, error) {
	current, err := s.Plan(ctx, selection)
	if err != nil {
		return Plan{}, err
	}
	if err := diagnostic.Revalidated(ctx, "prune", "plan", current.Equal(preview)); err != nil {
		return Plan{}, err
	}
	return current, nil
}

// Apply forgets the landed branches. It edits the recorded graph and never
// deletes a branch: removing someone's local work is not something to do as
// the tail of another command, or as this one.
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
	diagnostic.Event(ctx, "prune.apply", diagnostic.Field{Key: "branches", Value: strings.Join(plan.Landed, ",")})
	if err := s.Graph.Store.Save(ctx, adopted.Untrack(plan.Landed...)); err != nil {
		return err
	}
	if s.Graph.Refs == nil {
		return nil
	}
	// A fork point outlives the edge it belonged to unless it is released, and
	// a stale pin keeps objects reachable that nothing refers to any more.
	for _, branch := range plan.Landed {
		if err := s.Graph.Refs.UnpinForkPoint(ctx, branch); err != nil {
			return err
		}
	}
	return nil
}
