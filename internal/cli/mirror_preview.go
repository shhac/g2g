package cli

import (
	"fmt"

	"github.com/shhac/g2g/internal/align"
)

// mirrorView shows the reconciliation in the order it runs, so a reader can see
// where it would stop as easily as what it would do.
//
// Every line names the store it is talking about. Both tools spell the verb
// `untrack`, on opposite sides, so a bare verb here would be ambiguous about
// which record is losing an edge.
func mirrorView(plan align.MirrorPlan, prune bool) stackView {
	view := stackView{Operation: "mirror", Target: "graphite", TargetSource: "destination"}
	if plan.Blocked != "" {
		if len(plan.UnknownRoots) != 0 {
			view = view.note(fmt.Sprintf("Graphite does not track %s · track %s in Graphite first, or run %s if it has no trunk.",
				branchList(plan.UnknownRoots), pick(len(plan.UnknownRoots), "it", "them"), runnable("gt init")), severityBad)
		}
		return view.blockedBy(plan.Blocked)
	}
	// Nothing-to-do is applyFlow's line to say, not this view's: saying it here
	// too printed it twice.
	view = writeNotes(view, plan)
	return strangerNotes(view, plan, prune)
}

func writeNotes(view stackView, plan align.MirrorPlan) stackView {
	if added := plan.Added(); len(added) != 0 {
		view = view.note("Tracks "+branchList(added)+" in Graphite.", severityOK)
	}
	if moved := plan.Moved(); len(moved) != 0 {
		view = view.note("Moves "+branchList(moved)+" in Graphite to the parent the g2g graph records.", severityOK)
	}
	return view
}

// strangerNotes always names what Graphite has that g2g does not, whether or
// not a prune was asked for: a branch this command could remove is worth seeing
// before it can remove it.
func strangerNotes(view stackView, plan align.MirrorPlan, prune bool) stackView {
	if len(plan.Strangers) == 0 {
		return view
	}
	if !prune {
		return view.note(fmt.Sprintf("Graphite also tracks %s, which the g2g graph does not · --prune would untrack %s in Graphite. Nothing is removed from the g2g graph.",
			branchList(plan.Strangers), pick(len(plan.Strangers), "it", "them")), severityNeutral)
	}
	if len(plan.Prunes) != 0 {
		view = view.note("Untracks "+branchList(plan.Prunes)+" in Graphite · deepest first, because untracking takes the subtree with it. Nothing is removed from the g2g graph.", severityWarn)
	}
	if shielded := plan.Shielded(); len(shielded) != 0 {
		view = view.note(fmt.Sprintf("Keeps %s in Graphite · untracking %s would take a branch the g2g graph does know.",
			branchList(shielded), pick(len(shielded), "it", "them")), severityNeutral)
	}
	return view
}
