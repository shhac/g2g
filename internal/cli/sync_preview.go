package cli

import (
	"fmt"

	syncer "github.com/shhac/g2g/internal/sync"
)

// syncView shows the sequence in the order it runs, so a reader can see where
// it would stop as easily as what it would do.
func syncView(plan syncer.Plan) stackView {
	view := graphView(plan.Restack.Discovery, "sync")
	if plan.Blocked != "" {
		return view.blockedBy(plan.Blocked)
	}
	view = view.note(baseNote(plan), baseSeverity(plan))
	view = view.note(replayNote(plan), severityNeutral)
	return view
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
