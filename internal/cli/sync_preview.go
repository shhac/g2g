package cli

import (
	"fmt"
	"strings"

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
	if note := collectNote(plan); note != "" {
		view = view.note(note, severityOK)
	}
	view = view.note(replayNote(plan), severityNeutral)
	return view
}

// collectNote says which of your branches the remote has moved on, and how.
//
// Bringing somebody else's commit onto a branch of yours is a change to your
// work, so it is named in the preview rather than folded into "synced". A
// supersede is called out separately because it replaces what you have rather
// than adding to it.
func collectNote(plan syncer.Plan) string {
	advanced, superseded := make([]string, 0), make([]string, 0)
	for _, collection := range plan.Collect {
		if collection.Superseded {
			superseded = append(superseded, collection.Branch)
			continue
		}
		advanced = append(advanced, collection.Branch)
	}
	notes := make([]string, 0, 2)
	if len(advanced) != 0 {
		notes = append(notes, "Brings "+branchList(advanced)+" up to what is published.")
	}
	if len(superseded) != 0 {
		notes = append(notes, "Replaces "+branchList(superseded)+" with the published version, which already has everything here.")
	}
	return strings.Join(notes, " ")
}

func baseNote(plan syncer.Plan) string {
	switch {
	case plan.Supersede:
		// Worth spelling out: this is the one place sync discards commits, and
		// it only does so because the published trunk already has their content
		// under different ids.
		return fmt.Sprintf("Replaces %s with %s/%s, which was rewritten and already has everything here · your stack is replayed onto it.", plan.Base, plan.Remote, plan.Base)
	case plan.Advance:
		return fmt.Sprintf("Fast-forwards %s to %s · nothing is merged or rewritten.", plan.Base, plan.Remote)
	}
	return fmt.Sprintf("%s is already level with %s.", plan.Base, plan.Remote)
}

func baseSeverity(plan syncer.Plan) severity {
	if plan.Supersede {
		return severityWarn
	}
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
