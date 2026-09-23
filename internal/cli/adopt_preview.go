package cli

import (
	"fmt"

	"github.com/shhac/g2g/internal/graph"
)

// gitAdoptView renders a whole-stack adoption. It is the same graph view every
// other structure command uses; only the notes differ.
func gitAdoptView(plan graph.StackPlan) stackView {
	view := driftNotes(graphView(plan.Discovery, "adopt"), plan.Discovery)
	if plan.Blocked != "" {
		return view.refusing(plan.Blocked, plan.Repair)
	}
	if len(plan.Record) == 0 {
		return view.note("The graph already records this whole ancestry.", severityNeutral)
	}
	view = view.note(fmt.Sprintf("Records %s, from %s upwards.", branchList(plan.Branches()), plan.Trunk), severityOK)
	for _, adoption := range plan.Record {
		view = view.note(fmt.Sprintf("  %s under %s", adoption.Branch, adoption.Parent), severityNeutral)
	}
	if plan.NewTrunk != "" {
		view = view.note(fmt.Sprintf("%s becomes a root of the graph.", plan.NewTrunk), severityNeutral)
	}
	if len(plan.Already) != 0 {
		view = view.note("Already recorded: "+branchList(plan.Already)+".", severityNeutral)
	}
	return view
}
