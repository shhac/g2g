package cli

import (
	"github.com/shhac/g2g/internal/graph"
)

func untrackView(plan graph.UntrackPlan) stackView {
	view := graphView(plan.Discovery, "untrack")
	if len(plan.Removed) == 0 {
		return view.note("No selected branch is tracked · nothing to remove.", severityNeutral)
	}
	view = view.note("Removes the recorded parent of "+branchList(plan.Removed)+".", severityOK)
	if len(plan.Orphaned) == 0 {
		return view
	}
	// Reparenting the children onto the grandparent would invent an edge the
	// user never asked for, so the consequence is shown instead.
	return view.note("Leaves "+branchList(plan.Orphaned)+" without a tracked parent · they are not reparented.", severityWarn)
}
