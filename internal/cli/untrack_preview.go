package cli

import (
	"github.com/shhac/g2g/internal/graph"
)

func untrackView(plan graph.UntrackPlan) stackView {
	view := graphView(plan.Discovery, "untrack")
	if plan.NoOp() {
		return view.note("No selected branch is tracked · nothing to remove.", severityNeutral)
	}
	if len(plan.Removed) != 0 {
		view = view.note("Removes the recorded parent of "+branchList(plan.Removed)+".", severityOK)
	}
	if len(plan.Undeclared) != 0 {
		view = view.note(branchList(plan.Undeclared)+" "+pick(len(plan.Undeclared), "stops", "stop")+" being a trunk.", severityOK)
	}
	switch len(plan.Dependents) {
	case 0:
	case 1:
		view = view.note(plan.Dependents[0]+" still lands there · untrack it too, or declare where it lands again.", severityWarn)
	default:
		view = view.note(branchList(plan.Dependents)+" still land there · untrack them too, or declare where they land again.", severityWarn)
	}
	if len(plan.Orphaned) == 0 {
		return view
	}
	// Reparenting the children onto the grandparent would invent an edge the
	// user never asked for, so the consequence is shown instead.
	return view.note("Leaves "+branchList(plan.Orphaned)+" without a tracked parent · they are not reparented.", severityWarn)
}
