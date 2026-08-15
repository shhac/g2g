package githubstack

// MembershipState classifies selected PRs against one native GitHub stack.
type MembershipState string

const (
	Unlinked    MembershipState = "unlinked"
	Aligned     MembershipState = "aligned"
	Partial     MembershipState = "partial"
	Conflicting MembershipState = "conflicting"
)

// Membership describes native-stack facts for one ordered selected path.
// Branches contains the inspected PRs keyed by branch name.
type Membership struct {
	Branches    map[string]PullRequest
	State       MembershipState
	StackNumber int
	StackSize   int
	Selected    int
	Linked      int
}

// AssessMembership compares an ordered Graphite path with the native GitHub
// stack facts returned for its PRs. It does not render or mutate anything.
func AssessMembership(branches []string, prs []PullRequest) Membership {
	membership := Membership{Branches: ByHead(prs), Selected: len(branches)}
	for index, branch := range branches {
		pr := membership.Branches[branch]
		if pr.StackNumber == 0 {
			continue
		}
		membership.Linked++
		if membership.StackNumber == 0 {
			membership.StackNumber = pr.StackNumber
			membership.StackSize = pr.StackSize
		} else if pr.StackNumber != membership.StackNumber || pr.StackSize != membership.StackSize {
			membership.State = Conflicting
			return membership
		}
		if pr.StackPosition != index+1 {
			membership.State = Conflicting
			return membership
		}
	}
	if membership.Linked == 0 {
		membership.State = Unlinked
	} else if membership.Linked != membership.Selected {
		membership.State = Partial
	} else {
		membership.State = Aligned
	}
	return membership
}

// ByHead returns the pull request representing each head branch: its single
// open one, or the newest otherwise. Callers that must distinguish "none open"
// from "several open" should use ResolveHeads.
func ByHead(prs []PullRequest) map[string]PullRequest {
	indexed := make(map[string]PullRequest)
	for branch, resolution := range ResolveHeads(prs) {
		switch {
		case resolution.Open != nil:
			indexed[branch] = *resolution.Open
		case resolution.Latest != nil:
			indexed[branch] = *resolution.Latest
		}
	}
	return indexed
}

// GroupByHead preserves all PR matches so callers can enforce their own
// duplicate policy.
func GroupByHead(prs []PullRequest) map[string][]PullRequest {
	groups := make(map[string][]PullRequest, len(prs))
	for _, pr := range prs {
		groups[pr.Head] = append(groups[pr.Head], pr)
	}
	return groups
}
