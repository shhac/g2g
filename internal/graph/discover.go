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
	// which is the only way to see a squash or a cherry-pick.
	Cherry(ctx context.Context, upstream, head, limit string) (absent, present []string, err error)
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

// Candidates returns the possible parents of target, nearest first.
//
// Two sets are tried in turn. The preferred set is the target's ancestors plus
// the roots the graph already records, which is the answer in any repository
// that has adopted anything at all. When that comes back empty — the first
// branch into an empty graph, whose trunk has almost always moved on since the
// branch left it — every local branch is measured instead. That fallback costs
// one Git call per branch and runs once per repository, not once per command.
func Candidates(ctx context.Context, git Ancestry, target string, roots []string) ([]Candidate, error) {
	if git == nil {
		return nil, fmt.Errorf("graph discovery is not configured")
	}
	if target == "" {
		return nil, fmt.Errorf("a target branch is required")
	}
	local, err := git.LocalBranches(ctx)
	if err != nil {
		return nil, err
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
	candidates, err := measure(ctx, git, target, preferred, roots)
	if err != nil || len(candidates) != 0 {
		return candidates, err
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

// NodeState is what the graph can say about one branch without a network call.
type NodeState string

const (
	// StateAligned means the recorded parent's tip is still reachable from the
	// branch, so the edge describes the branch as it currently is.
	StateAligned NodeState = "aligned"
	// StateNeedsRestack means the parent moved. The edge is still correct; the
	// branch's contents are not.
	StateNeedsRestack NodeState = "needs restack"
	// StateMovedOffParent means the branch is no longer built on its recorded
	// parent at all, which is what a manual rebase looks like. The recorded
	// fork point is meaningless from here, so nothing may be replayed until
	// the edge is recorded again.
	StateMovedOffParent NodeState = "moved off parent"
	// StateParentMissing means the recorded parent is no longer a local
	// branch, which is what a merged and deleted parent looks like.
	StateParentMissing NodeState = "parent missing"
	// StateLanded means this branch's own work is already in a trunk, by
	// content rather than by object id, so there is nothing left for it to
	// contribute.
	//
	// It is distinguished from a missing parent because the remedy is the
	// opposite. "Retrack onto its new parent" tells someone to repair a branch
	// that has already served its purpose; what they want is to forget it.
	StateLanded NodeState = "landed"
	// StateForkUnresolvable means the recorded fork point is no longer an
	// object in this repository, usually because it was collected after the
	// parent branch went away.
	StateForkUnresolvable NodeState = "fork point unresolvable"
	// StateUntracked means the graph records no parent for the branch.
	StateUntracked NodeState = "untracked"
)

// Restackable reports whether a state describes a branch a rebase may act on.
//
// A landed branch is very much one of them: a stack whose base has landed is
// the recovery restack exists for, and the branch collapses onto its new base
// rather than being replayed. Leaving it out refused exactly the case the tool
// is for.
func (s NodeState) Restackable() bool {
	return s == StateAligned || s == StateNeedsRestack || s == StateLanded
}

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
		state, err := classify(ctx, git, g, present, branch)
		if err != nil {
			return nil, err
		}
		states[branch] = state
	}
	return states, nil
}

// classify answers what the graph knows about one branch.
//
// Two probes are needed and neither is sufficient alone.
//
// The ancestor probe asks whether the branch still contains its own recorded
// base. It is asked of the fork point rather than of the parent, because an
// amended or rebased parent is no longer an ancestor of its children even
// though nothing is wrong with them — that is precisely the ordinary case a
// restack repairs. A fork point that has stopped being an ancestor means the
// branch itself was rewritten, and the recorded range is then meaningless.
//
// The equality probe asks whether the parent has moved since the edge was
// written, which is what makes the contents stale.
func classify(ctx context.Context, git Ancestry, g Graph, present map[string]bool, branch string) (NodeState, error) {
	edge, tracked := g.Edges[branch]
	if !tracked {
		return StateUntracked, nil
	}
	if !present[edge.Parent] {
		return drifted(ctx, git, g, present, branch, StateParentMissing)
	}
	parentTip, err := git.Resolve(ctx, edge.Parent)
	if err != nil {
		return "", err
	}
	// An edge written before fork points were recorded can only be checked the
	// way it used to be. It fails closed once it drifts, because the restack
	// guard refuses a fork point it cannot verify.
	if edge.ForkPoint == "" {
		built, err := git.IsAncestor(ctx, edge.Parent, branch)
		if err != nil {
			return "", err
		}
		if built {
			return StateAligned, nil
		}
		return drifted(ctx, git, g, present, branch, StateNeedsRestack)
	}
	forkPoint, err := git.Resolve(ctx, edge.ForkPoint)
	if err != nil {
		return StateForkUnresolvable, nil
	}
	intact, err := git.IsAncestor(ctx, forkPoint, branch)
	if err != nil {
		return "", err
	}
	if !intact {
		return drifted(ctx, git, g, present, branch, StateMovedOffParent)
	}
	if forkPoint == parentTip {
		return StateAligned, nil
	}
	return drifted(ctx, git, g, present, branch, StateNeedsRestack)
}

// drifted reports a branch that no longer sits where it was recorded, and
// distinguishes the two reasons that look identical from the graph's side.
//
// A branch drifts because the world moved and it needs repairing, or because
// its work landed and it is finished. The remedies are opposite — restack or
// retrack, against prune — so telling someone to repair a branch that has
// already served its purpose sends them to fix something that is not broken.
//
// Only a drifted branch is asked, which is what bounds the cost: a branch
// sitting exactly where it was recorded cannot have landed without also having
// no work of its own.
func drifted(ctx context.Context, git Ancestry, g Graph, present map[string]bool, branch string, otherwise NodeState) (NodeState, error) {
	landed, err := landedInATrunk(ctx, git, g, present, branch)
	if err != nil {
		return "", err
	}
	if landed {
		return StateLanded, nil
	}
	return otherwise, nil
}

// landedInATrunk reports a branch whose own work is already in a trunk by
// content, which is what a squash merge or a cherry-picked series leaves
// behind: the same change under a different object id.
//
// Every trunk the graph knows is asked, because the branch's own trunk may be
// exactly the one that has gone. A trunk that is not a local branch is skipped
// rather than failing the read.
func landedInATrunk(ctx context.Context, git Ancestry, g Graph, present map[string]bool, branch string) (bool, error) {
	for _, trunk := range g.Trunks {
		if !present[trunk] || trunk == branch {
			continue
		}
		absent, _, err := git.Cherry(ctx, trunk, branch, "")
		if err != nil {
			// A branch the trunk shares no history with cannot be compared,
			// which is an answer rather than a failure.
			return false, nil
		}
		if len(absent) == 0 {
			return true, nil
		}
	}
	return false, nil
}
