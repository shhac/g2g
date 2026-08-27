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
		return view.refusing(plan.Blocked, plan.Repair)
	}
	view = view.note(baseNote(plan), baseSeverity(plan))
	if note := collectNote(plan); note != "" {
		view = view.note(note, severityOK)
	}
	if note := discardNote(plan); note != "" {
		view = view.note(note, severityBad)
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
	advanced, superseded, taken := make([]string, 0), make([]string, 0), make([]string, 0)
	for _, collection := range plan.Collect {
		switch {
		case len(collection.Discards) != 0:
			// Asked for. Saying it "already has everything here" would be false
			// of exactly this case, which is the one that loses work.
			taken = append(taken, collection.Branch)
		case collection.Superseded:
			superseded = append(superseded, collection.Branch)
		default:
			advanced = append(advanced, collection.Branch)
		}
	}
	notes := make([]string, 0, 3)
	if len(advanced) != 0 {
		notes = append(notes, "Brings "+branchList(advanced)+" up to what is published.")
	}
	if len(superseded) != 0 {
		notes = append(notes, "Replaces "+branchList(superseded)+" with the published version, which already has everything here.")
	}
	if len(taken) != 0 {
		notes = append(notes, "Replaces "+branchList(taken)+" with the published version.")
	}
	return strings.Join(notes, " ")
}

// discardNote names every commit --take would throw away.
//
// A count would not be enough. This is the one path where sync loses work that
// exists nowhere else, so the preview lists what dies rather than how much, and
// it is a problem rather than a notice.
func discardNote(plan syncer.Plan) string {
	losses := make([]string, 0)
	for _, commit := range plan.DiscardsBase {
		losses = append(losses, plan.Base+" "+shortObject(commit))
	}
	for _, collection := range plan.Collect {
		for _, commit := range collection.Discards {
			losses = append(losses, collection.Branch+" "+shortObject(commit))
		}
	}
	if len(losses) == 0 {
		return ""
	}
	return fmt.Sprintf("--take published discards %s that %s nowhere else: %s.",
		count(len(losses), "commit", "commits"), pick(len(losses), "exists", "exist"), strings.Join(losses, ", "))
}

// shortObject trims an object id to the length a person reads, leaving anything
// that is not one alone.
func shortObject(object string) string {
	if len(object) <= 12 {
		return object
	}
	return object[:12]
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
