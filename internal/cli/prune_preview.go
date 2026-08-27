package cli

import (
	"io"

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
			view.Nodes[index].State, view.Nodes[index].Severity = "landed · forget", severityWarn
		}
	}
	if plan.Blocked != "" {
		return view.refusing(plan.Blocked, plan.Repair)
	}
	if plan.Nothing() {
		return view
	}
	return view.note("Forgets "+branchList(plan.Landed)+" from the recorded graph. No branch is deleted.", severityWarn)
}

func writePrunePlan(w io.Writer, plan prune.Plan, p Presentation) error {
	return writeGraphView(w, pruneView(plan), plan.Discovery, p)
}
