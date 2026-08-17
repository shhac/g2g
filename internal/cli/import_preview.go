package cli

import (
	"fmt"

	"github.com/shhac/g2g/internal/align"
)

// importView leads with the authority claim rather than the branch count.
//
// Listing what would be adopted understates what happens: afterwards g2g
// decides for every one of those branches, and --from graphite is the only way
// to see Graphite's answer again. That is the part worth reading before typing
// --apply.
func importView(plan align.ImportPlan) stackView {
	view := stackView{Operation: "import", Target: "graphite", TargetSource: "source"}
	if plan.Blocked != "" {
		view = view.note(conflictNote(plan), severityBad)
		return view.blockedBy(plan.Blocked)
	}
	// Nothing-to-adopt is applyFlow's line to say, not this view's.
	if len(plan.Adopt) == 0 {
		return agreementNote(view, plan)
	}
	view = view.note("Adopts "+branchList(plan.Claims())+" into the g2g graph.", severityOK)
	view = view.note(fmt.Sprintf("g2g answers for %s from now on · run g2g status --from graphite to see Graphite's view of %s. Graphite keeps tracking %s.",
		pick(len(plan.Adopt), "it", "them"), pick(len(plan.Adopt), "it", "them"), pick(len(plan.Adopt), "it", "them")), severityWarn)
	if len(plan.NewTrunks) != 0 {
		view = view.note("Records "+branchList(plan.NewTrunks)+" as "+pick(len(plan.NewTrunks), "a trunk", "trunks")+" of the g2g forest.", severityNeutral)
	}
	return agreementNote(view, plan)
}

func agreementNote(view stackView, plan align.ImportPlan) stackView {
	if len(plan.Agreed) == 0 {
		return view
	}
	return view.note("Both already agree about "+branchList(plan.Agreed)+".", severityNeutral)
}

// conflictNote names each disagreement in full. "Blocked on a conflict" is not
// actionable; which parent each record holds is.
func conflictNote(plan align.ImportPlan) string {
	note := "The two records disagree about " + branchList(conflictedBranches(plan)) + ":"
	for _, conflict := range plan.Conflicts {
		note += fmt.Sprintf("\n  %s · g2g says %s, Graphite says %s", conflict.Branch, conflict.Ours, conflict.Theirs)
	}
	return note
}

func conflictedBranches(plan align.ImportPlan) []string {
	names := make([]string, 0, len(plan.Conflicts))
	for _, conflict := range plan.Conflicts {
		names = append(names, conflict.Branch)
	}
	return names
}
