package cli

import (
	"io"
	"maps"
	"slices"

	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/prune"
)

// pruneView marks what would be forgotten inside the graph it belongs to, so a
// reader sees the branches in context rather than as a bare list. Nothing is
// deleted, and the note says so: forgetting a branch and removing someone's
// work are different acts and must not read alike.
func pruneView(plan prune.Plan) stackView {
	forgetting := make(map[string]bool, len(plan.Landed))
	for _, branch := range plan.Landed {
		forgetting[branch] = true
	}

	view := graphView(plan.Discovery, "prune")
	for index, node := range view.Nodes {
		if forgetting[node.Branch] {
			view.Nodes[index].State, view.Nodes[index].Severity = forgetState(plan, node.Branch), severityWarn
		}
	}
	for _, branch := range plan.Missing {
		view = view.note(branch+" is recorded and no longer a local branch · run "+runnable("g2g untrack --branch "+branch)+" to forget it", severityWarn)
	}
	if plan.Blocked != "" {
		return view.refusing(plan.Blocked, plan.Repair)
	}
	if plan.Nothing() {
		return view
	}
	for _, child := range slices.Sorted(maps.Keys(plan.Rehome)) {
		view = view.note("Records "+child+" on "+plan.Rehome[child].Parent+", where it already sits.", severityOK)
	}
	return view.note("Forgets "+branchList(plan.Landed)+" from the recorded graph. No branch is deleted.", severityWarn)
}

// forgetState says why a branch is forgotten in the words graph uses for it. A
// branch with no commits of its own is not called landed: it may be one nobody
// has committed to yet, and the two are identical from the recorded state.
func forgetState(plan prune.Plan, branch string) string {
	if plan.Discovery.States[branch] == graph.StateEmpty {
		return "no commits of its own · forget"
	}
	return "landed · forget"
}

func writePrunePlan(w io.Writer, plan prune.Plan, p Presentation) error {
	return writeGraphView(w, pruneView(plan), plan.Discovery, p)
}
