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
		// Whether this branch was compared, not whether the map exists. The
		// domain package asks it this way for a reason — push.NothingToPublish
		// does the same — and the weaker proxy was the one path where a branch
		// missing from a populated map rendered as the reassuring answer.
		publication, compared := plan.Publishing[branch]
		state, level := publicationState(publication, compared)
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
func publicationState(publication push.Publication, compared bool) (string, severity) {
	switch {
	case !compared:
		// Never compared, so there is nothing to say. The zero Publication
		// otherwise reads as "up to date", which is the one claim a plan that
		// skipped the comparison must not make.
		return "", severityNeutral
	case publication.Unknown:
		return "remote is on a commit you do not have · fetch before publishing", severityBad
	case publication.Theirs > 0 && publication.Ours > 0:
		return fmt.Sprintf("diverged · %s here, %s only on the remote · publishing would drop %s",
			count(publication.Ours, "commit", "commits"), count(publication.Theirs, "commit", "commits"), pick(publication.Theirs, "it", "them")), severityBad
	case publication.Theirs > 0:
		return fmt.Sprintf("remote has %s this does not · publishing would drop %s", count(publication.Theirs, "commit", "commits"), pick(publication.Theirs, "it", "them")), severityBad
	case publication.Rewritten:
		return "rewritten since it was published · replaces it, and the remote holds nothing it lacks", severityOK
	case publication.Landed:
		return "already in the trunk · nothing to publish", severityNeutral
	case publication.New:
		return "new branch on the remote", severityOK
	case publication.UpToDate():
		return "up to date", severityNeutral
	default:
		return fmt.Sprintf("%s to publish", count(publication.Ours, "commit", "commits")), severityOK
	}
}

func writePushPlan(writer io.Writer, plan push.Plan, presentation Presentation) error {
	return writeStackView(writer, pushView(plan), presentation)
}
