package cli

import (
	"fmt"
	"io"
	"strings"

	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/push"
	"github.com/shhac/g2g/internal/shape"
)

func pushView(plan push.Plan) stackView {
	view := stackView{
		Operation:    "push",
		Target:       plan.Target,
		TargetSource: plan.TargetSource,
		Nodes:        []stackNode{{Branch: plan.Base, Trunk: true}},
		Action:       pushAction(plan),
	}
	depths := shape.Depths(append([]string{plan.Base}, plan.Branches...), plan.ParentOf)
	for _, branch := range plan.Branches {
		// A branch missing from the map reads as Uncompared, never as the
		// reassuring answer.
		state, level := publicationState(plan.Publishing[branch])
		node := stackNode{Branch: branch, Target: branch == plan.Target, Parent: plan.Parents[branch], Depth: depths[branch]}
		view.Nodes = append(view.Nodes, node.labeled(state, level))
	}
	view = pushDropNotes(view, plan)
	view = view.note("Atomic push: all selected refs advance together or none do.", severityNeutral)
	if plan.Blocked() != "" {
		return view.refusing(plan.Repair)
	}
	return view
}

// pushDropNotes list every published commit a push removes, by branch, short
// id and subject: once the remote moves, nothing else will name them.
func pushDropNotes(view stackView, plan push.Plan) stackView {
	name := func(drops []push.Drop) string {
		said := make([]string, 0, len(drops))
		for _, drop := range drops {
			entry := drop.Branch + " " + shortObject(drop.Commit)
			if subject := plan.Subjects[drop.Commit]; subject != "" {
				entry += " " + subject
			}
			if drop.To != "" {
				entry += " (" + drop.Branch + " → " + drop.To + ")"
			}
			said = append(said, entry)
		}
		return strings.Join(said, ", ")
	}
	if len(plan.Drops) != 0 {
		view = view.note(fmt.Sprintf("Drops %s from %s that you dropped here since you last pulled or pushed: %s · to keep %s, run %s first.",
			count(len(plan.Drops), "published commit", "published commits"), plan.Remote, name(plan.Drops),
			pick(len(plan.Drops), "it", "them"), runnable("g2g pull --keep <commit>")), severityWarn)
	}
	if len(plan.Moves) != 0 {
		view = view.note(fmt.Sprintf("Moves %s between branches: %s.", count(len(plan.Moves), "published commit", "published commits"), name(plan.Moves)), severityNeutral)
	}
	if len(plan.Restores) != 0 {
		view = view.note(fmt.Sprintf("%s dropped %s you still have: %s.", plan.Remote, count(len(plan.Restores), "commit", "commits"), name(plan.Restores)), severityBad)
	}
	return view
}

// pushAction is the git invocation in the shape a reader would type it: the
// leases collapse to one flag, because the pinned tips are the plan's business
// and a line of object ids hides what the command does. Everything else is the
// invocation itself, so the line cannot name a flag the push does not pass.
func pushAction(plan push.Plan) []string {
	action := []string{"git"}
	for _, argument := range localgit.PushArguments(plan.Remote, plan.Leases(), plan.Upstream) {
		if !strings.HasPrefix(argument, "--force-with-lease=") {
			action = append(action, argument)
			continue
		}
		if action[len(action)-1] != "--force-with-lease" {
			action = append(action, "--force-with-lease")
		}
	}
	return action
}

// publicationState says what pushing one branch would do. Saying nothing was
// the previous answer, and it read identically whether the branch was ahead,
// already published, or about to overwrite somebody else's commit.
func publicationState(publication push.Publication) (string, severity) {
	switch publication.Standing {
	case push.Unknown:
		return "remote is on a commit you do not have · fetch before publishing", severityBad
	case push.Diverged:
		return fmt.Sprintf("diverged · %s here, %s only on the remote · publishing would drop %s",
			count(publication.Ours, "commit", "commits"), count(publication.Theirs, "commit", "commits"), pick(publication.Theirs, "it", "them")), severityBad
	case push.Behind:
		return fmt.Sprintf("remote has %s this does not · publishing would drop %s", count(publication.Theirs, "commit", "commits"), pick(publication.Theirs, "it", "them")), severityBad
	case push.Rewritten:
		return "rewritten since it was published · replaces it, and the remote holds nothing it lacks", severityOK
	case push.Landed:
		return "already in the trunk · nothing to publish", severityNeutral
	case push.New:
		return "new branch on the remote", severityOK
	case push.Current:
		return "up to date", severityNeutral
	case push.Ahead:
		return fmt.Sprintf("%s to publish", count(publication.Ours, "commit", "commits")), severityOK
	case push.Dropping:
		return fmt.Sprintf("drops %s you dropped here", count(publication.Dropped, "published commit", "published commits")), severityWarn
	case push.Restoring:
		return fmt.Sprintf("would put back %s the remote dropped · pull first", count(publication.Dropped, "commit", "commits")), severityBad
	default:
		// Never compared, so there is nothing to say.
		return "", severityNeutral
	}
}

func writePushPlan(writer io.Writer, plan push.Plan, presentation Presentation) error {
	return writeStackView(writer, pushView(plan), presentation)
}
