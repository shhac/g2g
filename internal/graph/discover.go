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

// SameTip reports a candidate at the target's own commit. Each is then an
// ancestor of the other, so ancestry cannot say which sits on which: it is a
// question to put to the user, never one to answer for them.
func (c Candidate) SameTip() bool { return c.Ancestor && c.Distance == 0 }

// related returns the possible parents drawn from the target's own ancestry and
// the roots the graph already records.
//
// This is the answer in any repository that has adopted anything at all, and it
// is everything a caller that acts on ancestry can use: the set it measures
// already contains every ancestor of the target, so no branch outside it can
// come back marked as one.
func related(ctx context.Context, git Ancestry, target string, roots []string) ([]Candidate, error) {
	if git == nil {
		return nil, fmt.Errorf("graph discovery is not configured")
	}
	local, err := git.LocalBranches(ctx)
	if err != nil {
		return nil, err
	}
	return relatedWithin(ctx, git, target, roots, local, nil)
}

// relatedWithin is related for a caller that already knows the local branches.
//
// They cannot change while one command runs, and a whole-stack adoption asks
// about every branch in the repository, so re-reading them per branch was one
// process spawn per branch for an answer already in hand.
//
// below names branches already merged into the trunk that the caller knows
// cannot be the answer, so they are not measured: see Service.branches.
func relatedWithin(ctx context.Context, git Ancestry, target string, roots, local []string, below map[string]bool) ([]Candidate, error) {
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
	preferred := make([]string, 0, len(ancestors))
	for _, ancestor := range ancestors {
		if !below[ancestor] {
			preferred = append(preferred, ancestor)
		}
	}
	for _, root := range roots {
		if slices.Contains(local, root) && !slices.Contains(preferred, root) {
			preferred = append(preferred, root)
		}
	}
	return measure(ctx, git, target, preferred, roots)
}

// Candidates returns the possible parents of target, nearest first.
//
// related first. When that comes back empty — the first branch into an empty
// graph, whose trunk has almost always moved on since the branch left it —
// every local branch is measured instead, so that there is something to offer
// rather than nothing. Those are branches the target cannot reach, so none of
// them is an ancestor and a caller acting on ancestry should ask related and
// skip this entirely: for one that filters on Ancestor the fallback is a Git
// call per branch whose whole result is then discarded.
func Candidates(ctx context.Context, git Ancestry, target string, roots []string) ([]Candidate, error) {
	candidates, err := related(ctx, git, target, roots)
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
// nothing ahead is a true ancestor. A branch with nothing behind and something
// ahead contains the target and more, so it is a descendant and never a parent.
//
// A branch with nothing either way is at the target's own commit, and is kept.
// It is both an ancestor and a descendant, which ancestry cannot order — and
// it is exactly the branch a new one was just created from. Dropping it as a
// descendant hid that branch from track and let a whole-stack adoption record
// the stack as though it were not there.
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
		if behind == 0 && ahead != 0 {
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
