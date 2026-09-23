package cli

import (
	"fmt"
	"io"

	"github.com/shhac/g2g/internal/push"
)

func pushView(plan push.Plan) stackView {
	view := stackView{
		Operation:    "push",
		Target:       plan.Target,
		TargetSource: plan.TargetSource,
		Nodes:        []stackNode{{Branch: plan.Base, Trunk: true}},
		Action:       append([]string{"git", "push", "--atomic", "--force-with-lease", plan.Remote}, plan.Branches...),
	}
	for _, branch := range plan.Branches {
		// A branch missing from the map reads as Uncompared, never as the
		// reassuring answer.
		state, level := publicationState(plan.Publishing[branch])
		view.Nodes = append(view.Nodes, stackNode{Branch: branch, Target: branch == plan.Target, State: state, Severity: level})
	}
	view = view.note("Atomic push: all selected refs advance together or none do.", severityNeutral)
	if plan.Blocked != "" {
		return view.refusing(plan.Blocked, plan.Repair)
	}
	return view
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
	default:
		// Never compared, so there is nothing to say.
		return "", severityNeutral
	}
}

func writePushPlan(writer io.Writer, plan push.Plan, presentation Presentation) error {
	return writeStackView(writer, pushView(plan), presentation)
}
