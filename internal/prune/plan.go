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
)

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
	comparisons := comparisonEdges(discovery, local)
	if options.DeleteBranches {
		plan.Tips, err = s.captureTips(ctx, discovery.Branches, local, comparisons)
		if err != nil {
			return Plan{}, err
		}
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
		landed, err := s.assessBranch(ctx, branch, edge, options.DeleteBranches, plan.Tips)
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
	}
	if options.DeleteBranches && plan.Blocked() == "" {
		if err := s.planDeletions(ctx, &plan, plan.Tips); err != nil {
			return Plan{}, err
		}
	}
	diagnostic.Event(ctx, "prune.plan",
		diagnostic.Field{Key: "selected", Value: strings.Join(discovery.Branches, ",")},
		diagnostic.Field{Key: "landed", Value: strings.Join(plan.Landed, ",")},
		diagnostic.Field{Key: "blocked", Value: plan.Blocked()},
	)
	return plan, nil
}

// comparisonEdges bounds each assessment at its first surviving parent.
func comparisonEdges(discovery graph.Discovery, local []string) map[string]graph.Edge {
	comparisons := map[string]graph.Edge{}
	for _, branch := range discovery.Branches {
		if edge, ok := comparisonEdge(discovery.Graph, branch, local); ok {
			comparisons[branch] = edge
		}
	}
	return comparisons
}

// captureTips pins both sides before any content assessment for local deletion.
func (s Service) captureTips(ctx context.Context, branches, local []string, comparisons map[string]graph.Edge) (map[string]string, error) {
	if s.Cleaner == nil {
		return nil, fmt.Errorf("local branch deletion is not configured")
	}
	needed := map[string]bool{}
	for _, branch := range branches {
		if slices.Contains(local, branch) {
			needed[branch] = true
		}
		if edge, comparable := comparisons[branch]; comparable {
			needed[edge.Parent] = true
		}
	}
	return s.Cleaner.ResolveAll(ctx, slices.Sorted(maps.Keys(needed)))
}

// assessBranch uses names for graph-only pruning and captured objects for
// deletion. A stale range can never justify removing a local branch.
func (s Service) assessBranch(ctx context.Context, branch string, edge graph.Edge, deleting bool, tips map[string]string) (bool, error) {
	if !deleting {
		return s.landed(ctx, branch, edge)
	}
	head := tips[branch]
	edge.Parent = tips[edge.Parent]
	if head == "" || edge.Parent == "" {
		return false, fmt.Errorf("a branch disappeared before cleanup · preview again")
	}
	retained, err := s.retainsRange(ctx, head, edge)
	if err != nil || !retained {
		return false, err
	}
	return s.landed(ctx, head, edge)
}

// retainsRange proves both that the range belongs to this head and that work
// inherited below it still exists in the base. Cherry alone cannot prove either.
func (s Service) retainsRange(ctx context.Context, head string, edge graph.Edge) (bool, error) {
	if edge.ForkPoint == "" {
		return true, nil
	}
	// A manual reset or rebase can leave the stored range pointing beyond
	// this head. Cherry would then exclude work it must assess.
	intact, err := s.Graph.Git.IsAncestor(ctx, edge.ForkPoint, head)
	if err != nil || !intact {
		return false, err
	}
	// A live parent can have been rewound too. The child's own range says
	// nothing about inherited work below its fork.
	retained, err := s.Graph.Git.IsAncestor(ctx, edge.ForkPoint, edge.Parent)
	if err != nil || retained {
		return retained, err
	}
	return s.Git.Absorbed(ctx, edge.Parent, edge.ForkPoint)
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
