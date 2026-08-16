package cli

import (
	"fmt"

	"github.com/shhac/gt2gh/internal/retarget"
)

// retargetView names every base it would move, and where from.
//
// A base is what a merge follows, so "3 pull requests updated" is not enough
// for a reader to agree to: they need to see which merge target changes.
func retargetView(plan retarget.Plan) stackView {
	view := stackView{Operation: "retarget", Target: plan.Target, TargetSource: plan.TargetSource}
	for _, branch := range plan.Discovery.Branches {
		view.Nodes = append(view.Nodes, stackNode{Branch: branch})
	}
	if len(plan.Ambiguous) != 0 {
		view = view.note(fmt.Sprintf("%s %s more than one open pull request · this leaves %s alone.",
			branchList(plan.Ambiguous), pick(len(plan.Ambiguous), "has", "have"), pick(len(plan.Ambiguous), "it", "them")), severityBad)
	}
	if plan.Blocked != "" {
		return view.blockedBy(plan.Blocked)
	}
	for _, change := range plan.Changes {
		view = view.note(fmt.Sprintf("PR #%d (%s) · base %s → %s", change.Number, change.Branch, change.From, change.To), severityWarn)
	}
	return view
}
