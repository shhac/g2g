package cli

import (
	"fmt"

	syncer "github.com/shhac/gt2gh/internal/sync"
)

// syncView shows the sequence in the order it runs, so a reader can see where
// it would stop as easily as what it would do.
func syncView(plan syncer.Plan, prune bool) stackView {
	view := graphView(plan.Restack.Discovery, "sync")
	if plan.Blocked != "" {
		return view.block("Apply blocked: " + plan.Blocked)
	}
	view = view.note(baseNote(plan), baseSeverity(plan))
	view = view.note(replayNote(plan), severityNeutral)
	return pruneNote(view, plan, prune)
}

func baseNote(plan syncer.Plan) string {
	if !plan.Advance {
		return fmt.Sprintf("%s is already level with %s.", plan.Base, plan.Remote)
	}
	return fmt.Sprintf("Fast-forwards %s to %s · nothing is merged or rewritten.", plan.Base, plan.Remote)
}

func baseSeverity(plan syncer.Plan) severity {
	if plan.Advance {
		return severityOK
	}
	return severityNeutral
}

func replayNote(plan syncer.Plan) string {
	replaying := plan.Restack.Replaying()
	if len(replaying) == 0 {
		return "Nothing needs replaying."
	}
	return "Replays " + branchList(replaying) + "."
}

// pruneNote always names what would be forgotten, whether or not it will be:
// a branch leaving the recorded graph is a change to say out loud.
func pruneNote(view stackView, plan syncer.Plan, prune bool) stackView {
	if len(plan.Prunable) == 0 {
		return view
	}
	landed := branchList(plan.Prunable) + " has landed"
	if len(plan.Prunable) > 1 {
		landed = branchList(plan.Prunable) + " have landed"
	}
	if !prune {
		return view.note(landed+" · --prune would forget "+plural(len(plan.Prunable), "it", "them")+".", severityNeutral)
	}
	return view.note("Forgets "+branchList(plan.Prunable)+" · "+landed+". No branch is deleted.", severityWarn)
}

func plural(total int, one, many string) string {
	if total == 1 {
		return one
	}
	return many
}
