package cli

import (
	"fmt"

	"github.com/shhac/g2g/internal/align"
	"github.com/shhac/g2g/internal/words"
)

// mirrorView shows the reconciliation in the order it runs, so a reader can see
// where it would stop as easily as what it would do.
//
// Every line names the store it is talking about. Both tools spell the verb
// `untrack`, on opposite sides, so a bare verb here would be ambiguous about
// which record is losing an edge.
func mirrorView(plan align.MirrorPlan, prune bool) stackView {
	view := stackView{Operation: "graphite mirror", Target: "graphite", TargetSource: "destination"}
	if plan.Blocked() != "" {
		return view.refusing(plan.Repair)
	}
	// Nothing-to-do is applyFlow's line to say, not this view's: saying it here
	// too printed it twice.
	view = writeNotes(view, plan)
	return strangerNotes(view, plan, prune)
}

func writeNotes(view stackView, plan align.MirrorPlan) stackView {
	if added := plan.Added(); len(added) != 0 {
		view = view.note("Tracks "+words.List(added)+" in Graphite.", severityOK)
	}
	if moved := plan.Moved(); len(moved) != 0 {
		view = view.note("Moves "+words.List(moved)+" in Graphite to the parent the g2g graph records.", severityOK)
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
			words.List(plan.Strangers), words.Pick(len(plan.Strangers), "it", "them")), severityNeutral)
	}
	if len(plan.Prunes) != 0 {
		view = view.note("Untracks "+words.List(plan.Prunes)+" in Graphite · deepest first, because untracking takes the subtree with it. Nothing is removed from the g2g graph.", severityWarn)
	}
	if shielded := plan.Shielded(); len(shielded) != 0 {
		view = view.note(fmt.Sprintf("Keeps %s in Graphite · untracking %s would take a branch the g2g graph does know.",
			words.List(shielded), words.Pick(len(shielded), "it", "them")), severityNeutral)
	}
	return view
}
