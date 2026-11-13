package cli

import (
	"fmt"

	"github.com/shhac/g2g/internal/retarget"
	"github.com/shhac/g2g/internal/words"
)

// retargetView names every base it would move, and where from.
//
// A base is what a merge follows, so "3 pull requests updated" is not enough
// for a reader to agree to: they need to see which merge target changes.
func retargetView(plan retarget.Plan) stackView {
	view := stackView{Operation: "github retarget", Target: plan.Target, TargetSource: plan.TargetSource,
		Nodes: treeNodes(plan.Base, plan.Target, plan.Discovery.Branches, plan.Discovery.ParentOf)}
	if len(plan.Ambiguous) != 0 {
		view = view.note(fmt.Sprintf("%s %s more than one open pull request · this leaves %s alone.",
			words.List(plan.Ambiguous), words.Pick(len(plan.Ambiguous), "has", "have"), words.Pick(len(plan.Ambiguous), "it", "them")), severityBad)
	}
	if plan.Blocked() != "" {
		return view.refusing(plan.Repair)
	}
	for _, change := range plan.Changes {
		view = view.note(fmt.Sprintf("PR #%d (%s) · base %s → %s", change.Number, change.Branch, change.From, change.To), severityWarn)
	}
	return view
}
