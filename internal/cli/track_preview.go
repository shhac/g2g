package cli

import (
	"fmt"
	"strings"

	"github.com/shhac/g2g/internal/graph"
)

func trackView(plan graph.TrackPlan) stackView {
	view := driftNotes(graphView(plan.Discovery, "track"), plan.Discovery)
	if plan.Blocked == "" {
		view = view.note(fmt.Sprintf("Records %s under %s.", plan.Target, plan.Parent), severityOK)
		if plan.NewTrunk != "" {
			view = view.note(fmt.Sprintf("%s becomes a root of the graph.", plan.NewTrunk), severityNeutral)
		}
		return view.note(confirmation(plan), severityFor(plan))
	}
	view = view.blockedBy(plan.Blocked)
	return view.note(candidateAdvice(plan), severityNeutral)
}

// confirmation says whether Git already agrees with the edge being recorded.
// A parent whose commits are not in the branch is not an error — it is how a
// stack looks before it is restacked — but recording it silently would hide
// the one fact that explains why.
func confirmation(plan graph.TrackPlan) string {
	if plan.Updated.Edges[plan.Target].Origin == graph.OriginAncestry {
		return "Commit ancestry confirms " + plan.Parent + " is already below " + plan.Target + "."
	}
	return plan.Parent + " is not an ancestor of " + plan.Target + " · the edge is recorded as asserted, and " + plan.Target + " will read as needing a restack."
}
func severityFor(plan graph.TrackPlan) severity {
	if plan.Updated.Edges[plan.Target].Origin == graph.OriginAncestry {
		return severityNeutral
	}
	return severityWarn
}

// candidateAdvice names the choice rather than making it. The nearest ancestor
// is usually the parent, and "usually" is not a basis for writing down a
// structure every later command trusts.
func candidateAdvice(plan graph.TrackPlan) string {
	if len(plan.Candidates) == 0 {
		return "No candidate parent found · pass --parent with a local branch to record one."
	}
	described := make([]string, 0, len(plan.Candidates))
	for _, candidate := range plan.Candidates {
		described = append(described, candidate.Branch+describeCandidate(candidate))
	}
	return "Candidate parents, nearest first: " + strings.Join(described, ", ") +
		" · rerun with --parent <branch> for this one branch, or --stack to record the whole ancestry at once."
}

// stackView renders a whole-stack adoption. It is the same graph view every
// other structure command uses; only the notes differ.
func trackStackView(plan graph.StackPlan) stackView {
	view := driftNotes(graphView(plan.Discovery, "track"), plan.Discovery)
	if plan.Blocked != "" {
		return view.blockedBy(plan.Blocked)
	}
	if len(plan.Record) == 0 {
		return view.note("The graph already records this whole ancestry.", severityNeutral)
	}
	view = view.note(fmt.Sprintf("Records %s, from %s upwards.", branchList(plan.Branches()), plan.Trunk), severityOK)
	for _, adoption := range plan.Record {
		view = view.note(fmt.Sprintf("  %s under %s", adoption.Branch, adoption.Parent), severityNeutral)
	}
	if plan.NewTrunk != "" {
		view = view.note(fmt.Sprintf("%s becomes a root of the graph.", plan.NewTrunk), severityNeutral)
	}
	if len(plan.Already) != 0 {
		view = view.note("Already recorded: "+branchList(plan.Already)+".", severityNeutral)
	}
	return view
}
func describeCandidate(candidate graph.Candidate) string {
	described := count(candidate.Distance, "commit", "commits") + " behind"
	if candidate.Trunk {
		described = "root, " + described
	}
	return " (" + described + ")"
}
