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
	// Cherry answers whether commits are present on the other side by content,
	// which is the only way to see a cherry-pick.
	Cherry(ctx context.Context, upstream, head, limit string) (absent, present []string, err error)
	// Absorbed answers the same question of a whole branch at once, which is the
	// only way to see a squash: it combines a branch's commits into one, so the
	// result is equivalent to none of them and Cherry marks every one as new.
	Absorbed(ctx context.Context, base, branch string) (bool, error)
	LocalBranches(context.Context) ([]string, error)
	AncestorBranches(context.Context, string) ([]string, error)
	Divergence(context.Context, string, string) (ahead, behind int, err error)
	IsAncestor(context.Context, string, string) (bool, error)
	Resolve(context.Context, string) (string, error)
}

// Candidate is one branch that could be the parent of a target.
type Candidate struct {
	Branch string
	// Distance is how many commits the target has that the candidate does not.
	// The nearest such branch is the immediate parent, so this is the ordering.
	Distance int
	// Ancestor records whether the candidate's tip is actually reachable from
	// the target. A trunk that has moved on is offered without being one.
	Ancestor bool
	Trunk    bool
}

// Related returns the possible parents drawn from the target's own ancestry and
// the roots the graph already records.
//
// This is the answer in any repository that has adopted anything at all, and it
// is everything a caller that acts on ancestry can use: the set it measures
// already contains every ancestor of the target, so no branch outside it can
// come back marked as one.
func Related(ctx context.Context, git Ancestry, target string, roots []string) ([]Candidate, error) {
	if git == nil {
		return nil, fmt.Errorf("graph discovery is not configured")
	}
	local, err := git.LocalBranches(ctx)
	if err != nil {
		return nil, err
	}
	return relatedWithin(ctx, git, target, roots, local)
}

// relatedWithin is Related for a caller that already knows the local branches.
//
// They cannot change while one command runs, and a whole-stack adoption asks
// about every branch in the repository, so re-reading them per branch was one
// process spawn per branch for an answer already in hand.
func relatedWithin(ctx context.Context, git Ancestry, target string, roots, local []string) ([]Candidate, error) {
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
	// A recorded root that no longer exists locally is not a candidate: it
	// would be offered as a parent that could never be validated.
	preferred := slices.Clone(ancestors)
	for _, root := range roots {
		if slices.Contains(local, root) && !slices.Contains(preferred, root) {
			preferred = append(preferred, root)
		}
	}
	return measure(ctx, git, target, preferred, roots)
}

// Candidates returns the possible parents of target, nearest first.
//
// Related first. When that comes back empty — the first branch into an empty
// graph, whose trunk has almost always moved on since the branch left it —
// every local branch is measured instead, so that there is something to offer
// rather than nothing. Those are branches the target cannot reach, so none of
// them is an ancestor and a caller acting on ancestry should ask Related and
// skip this entirely: for one that filters on Ancestor the fallback is a Git
// call per branch whose whole result is then discarded.
func Candidates(ctx context.Context, git Ancestry, target string, roots []string) ([]Candidate, error) {
	candidates, err := Related(ctx, git, target, roots)
	if err != nil || len(candidates) != 0 {
		return candidates, err
	}
	local, err := git.LocalBranches(ctx)
	if err != nil {
		return nil, err
	}
	return measure(ctx, git, target, local, roots)
}

// measure asks Git how each branch relates to the target and keeps the ones
// that could be its parent, nearest first.
//
// One invocation per branch answers both questions at once. A branch with
// nothing behind already contains the target, so it is a descendant and never
// a parent; a branch with nothing ahead is a true ancestor.
func measure(ctx context.Context, git Ancestry, target string, branches, roots []string) ([]Candidate, error) {
	candidates := make([]Candidate, 0, len(branches))
	for _, branch := range branches {
		if branch == target {
			continue
		}
		ahead, behind, err := git.Divergence(ctx, branch, target)
		if err != nil {
			return nil, err
		}
		if behind == 0 {
			continue
		}
		candidates = append(candidates, Candidate{
			Branch:   branch,
			Distance: behind,
			Ancestor: ahead == 0,
			Trunk:    slices.Contains(roots, branch),
		})
	}
	sortCandidates(candidates)
	return candidates, nil
}

// sortCandidates orders by distance, then by name so equal distances are
// stable rather than depending on the order branches happened to arrive in.
func sortCandidates(candidates []Candidate) {
	sort.Slice(candidates, func(left, right int) bool {
		if candidates[left].Distance != candidates[right].Distance {
			return candidates[left].Distance < candidates[right].Distance
		}
		return candidates[left].Branch < candidates[right].Branch
	})
}
