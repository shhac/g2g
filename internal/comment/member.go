package comment

import (
	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/shape"
	"github.com/shhac/g2g/internal/stack"
)

// State is what one branch's pull request is, once the open-is-identity rule
// has said which pull request that is.
type State string

const (
	StateOpen      State = "open"
	StateMerged    State = "merged"
	StateClosed    State = "closed"
	StateMissing   State = "missing"
	StateAmbiguous State = "ambiguous"
)

// Member is one branch's pull request. Number is zero for a branch with none,
// and for one with more than one open, where nothing says which it means.
type Member struct {
	Number int
	State  State
}

// listed reports a member the stack's comments name by number. A closed pull
// request is not: nothing will merge it, and its branch is waiting for one.
func (m Member) listed() bool {
	return m.Number != 0 && (m.State == StateOpen || m.State == StateMerged)
}

// listedNumber is the number a comment names for this member, zero for none.
func (m Member) listedNumber() int {
	if m.listed() {
		return m.Number
	}
	return 0
}

// classify applies githubstack's step classification to every branch. The
// rule exists once, there; this only names the answer in the comment's terms.
func classify(discovery stack.Discovery) map[string]Member {
	members := make(map[string]Member, len(discovery.Branches))
	for step := range githubstack.Across(discovery.Parents, discovery.Branches, discovery.PullRequests) {
		switch step.Classify() {
		case githubstack.StepAmbiguous:
			members[step.Branch] = Member{State: StateAmbiguous}
		case githubstack.StepAligned, githubstack.StepBaseMismatch:
			members[step.Branch] = Member{Number: step.Resolution.Open.Number, State: StateOpen}
		case githubstack.StepSuperseded:
			state := StateClosed
			if step.Merged() {
				state = StateMerged
			}
			members[step.Branch] = Member{Number: step.Resolution.Latest.Number, State: state}
		default:
			members[step.Branch] = Member{State: StateMissing}
		}
	}
	return members
}

// ambiguous names the branches a stack cannot be kept for, in stack order.
func ambiguous(branches []string, members map[string]Member) []string {
	named := make([]string, 0)
	for _, branch := range branches {
		if members[branch].State == StateAmbiguous {
			named = append(named, branch)
		}
	}
	return named
}

// forestOf is the selection's shape with its base as the root. Every selector
// fills Parents, and a branch with no selected parent hangs from the base.
func forestOf(snapshot stack.Snapshot) shape.Forest {
	parents := make(map[string]string, len(snapshot.Branches)+1)
	parents[snapshot.Base] = ""
	for _, branch := range snapshot.Branches {
		parent, ok := snapshot.ParentOf(branch)
		if !ok {
			parent = snapshot.Base
		}
		parents[branch] = parent
	}
	return shape.Forest{Parents: parents}
}
