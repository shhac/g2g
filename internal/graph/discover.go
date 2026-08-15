package graph

import (
	"context"
	"fmt"
	"slices"
	"sort"
)

// Ancestry is the Git boundary a graph is discovered through. It is read-only
// and never checks a branch out.
type Ancestry interface {
	CurrentBranch(context.Context) (string, error)
	LocalBranches(context.Context) ([]string, error)
	AncestorBranches(context.Context, string) ([]string, error)
	CommitDistance(context.Context, string, string) (int, error)
	IsAncestor(context.Context, string, string) (bool, error)
}

// Candidate is one branch that could be the parent of a target.
type Candidate struct {
	Branch string
	// Distance is how many commits separate the candidate from the target.
	// The nearest ancestor is the immediate parent, so this is the ordering.
	Distance int
	// Ancestor records whether the candidate's tip is actually reachable from
	// the target. A trunk that has moved on is offered without being one.
	Ancestor bool
	Trunk    bool
}

// Candidates returns the possible parents of target, nearest ancestor first.
//
// Declared trunks are always offered even when they are not ancestors. Once a
// trunk moves ahead its tip stops being reachable from the branches built on
// it, so a stack's bottom branch would otherwise have no candidates at all.
func Candidates(ctx context.Context, git Ancestry, target string, trunks []string) ([]Candidate, error) {
	if git == nil {
		return nil, fmt.Errorf("graph discovery is not configured")
	}
	if target == "" {
		return nil, fmt.Errorf("a target branch is required")
	}
	ancestors, err := git.AncestorBranches(ctx, target)
	if err != nil {
		return nil, err
	}
	candidates := make([]Candidate, 0, len(ancestors)+len(trunks))
	seen := map[string]bool{target: true}
	for _, ancestor := range ancestors {
		distance, err := git.CommitDistance(ctx, ancestor, target)
		if err != nil {
			return nil, err
		}
		seen[ancestor] = true
		candidates = append(candidates, Candidate{Branch: ancestor, Distance: distance, Ancestor: true, Trunk: slices.Contains(trunks, ancestor)})
	}
	sortCandidates(candidates)

	detached := make([]string, 0, len(trunks))
	for _, trunk := range trunks {
		if !seen[trunk] {
			seen[trunk] = true
			detached = append(detached, trunk)
		}
	}
	sort.Strings(detached)
	for _, trunk := range detached {
		candidates = append(candidates, Candidate{Branch: trunk, Trunk: true})
	}
	return candidates, nil
}

// sortCandidates orders by distance, then by name so equal distances are
// stable rather than depending on map iteration.
func sortCandidates(candidates []Candidate) {
	sort.Slice(candidates, func(left, right int) bool {
		if candidates[left].Distance != candidates[right].Distance {
			return candidates[left].Distance < candidates[right].Distance
		}
		return candidates[left].Branch < candidates[right].Branch
	})
}

// NodeState is what the graph can say about one branch without a network call.
type NodeState string

const (
	// StateAligned means the recorded parent's tip is still reachable from the
	// branch, so the edge describes the branch as it currently is.
	StateAligned NodeState = "aligned"
	// StateNeedsRestack means the parent moved. The edge is still correct; the
	// branch's contents are not, and gt2gh does not rebase.
	StateNeedsRestack NodeState = "needs restack"
	// StateParentMissing means the recorded parent is no longer a local
	// branch, which is what a merged and deleted parent looks like.
	StateParentMissing NodeState = "parent missing"
	// StateUntracked means the graph records no parent for the branch.
	StateUntracked NodeState = "untracked"
)

// Assess reports each branch's state against Git.
//
// A parent that is no longer an ancestor is reported as needing a restack, not
// silently reparented. The distinction matters: a vanished ancestor means "the
// parent moved", not "there is no parent", and treating them alike would
// quietly reparent a stale child onto the trunk.
func Assess(ctx context.Context, git Ancestry, g Graph, branches []string) (map[string]NodeState, error) {
	if git == nil {
		return nil, fmt.Errorf("graph discovery is not configured")
	}
	local, err := git.LocalBranches(ctx)
	if err != nil {
		return nil, err
	}
	present := make(map[string]bool, len(local))
	for _, branch := range local {
		present[branch] = true
	}

	states := make(map[string]NodeState, len(branches))
	for _, branch := range branches {
		parent, tracked := g.Parent(branch)
		if !tracked {
			states[branch] = StateUntracked
			continue
		}
		if !present[parent] {
			states[branch] = StateParentMissing
			continue
		}
		aligned, err := git.IsAncestor(ctx, parent, branch)
		if err != nil {
			return nil, err
		}
		states[branch] = StateNeedsRestack
		if aligned {
			states[branch] = StateAligned
		}
	}
	return states, nil
}
