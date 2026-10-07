package cli

import (
	"io"
	"maps"
	"slices"

	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/prune"
)

// pruneView marks what would be forgotten inside the graph it belongs to, so a
// reader sees the branches in context rather than as a bare list. Record removal
// and explicit local branch deletion are listed separately, so the preview says
// exactly which acts were requested.
func pruneView(plan prune.Plan) stackView {
	forgetting := make(map[string]bool, len(plan.Landed))
	for _, branch := range plan.Forgotten() {
		forgetting[branch] = true
	}

	view := graphView(plan.Discovery, "prune")
	for index, node := range view.Nodes {
		if forgetting[node.Branch] {
			state := forgetState(plan, node.Branch)
			if _, deleting := plan.Delete[node.Branch]; deleting {
				state += " · delete local branch"
			}
			view.Nodes[index] = node.labeled(state, severityWarn)
		}
	}
	if !plan.Options.ForgetMissing {
		for _, note := range missingNotes(plan.Discovery) {
			view = view.note(note, severityWarn)
		}
	}
	if plan.Blocked() != "" {
		return view.refusing(plan.Repair)
	}
	if plan.Nothing() {
		return view
	}
	for _, child := range slices.Sorted(maps.Keys(plan.Rehome)) {
		view = view.note("Records "+child+" on "+plan.Rehome[child].Parent+", where it already sits.", severityOK)
	}
	view = view.note("Forgets "+branchList(plan.Forgotten())+" from the recorded graph.", severityWarn)
	if len(plan.Delete) != 0 {
		return view.note("Deletes local branches "+branchList(plan.Deleted())+". Remote branches are untouched.", severityWarn)
	}
	return view.note(keptBranches(plan), severityNeutral)
}

// keptBranches says that the forgotten branches stay, and how to remove them
// as well. It has to be said before they are forgotten: prune deletes only
// branches it records, so once one is forgotten g2g can no longer remove it.
func keptBranches(plan prune.Plan) string {
	if len(plan.Landed) == 0 {
		return "No branch is deleted."
	}
	command := selectedIn(plan.Discovery).next(pruneCommand) + " --delete-branches"
	if plan.Options.ForgetMissing {
		command += " --forget-missing"
	}
	return "No branch is deleted · run " + runnable(command) + " to remove " + pick(len(plan.Landed), "it", "them") + " too, which g2g cannot do once " + pick(len(plan.Landed), "it is", "they are") + " forgotten."
}

// forgetState says why a branch is forgotten in the words status uses for it. A
// branch with no commits of its own is not called landed: it may be one nobody
// has committed to yet, and the two are identical from the recorded state.
func forgetState(plan prune.Plan, branch string) string {
	if plan.Discovery.States[branch] == graph.StateBranchMissing {
		return "branch missing · forget record"
	}
	if plan.Discovery.States[branch] == graph.StateEmpty {
		return "no commits of its own · forget"
	}
	return "landed · forget"
}

func writePrunePlan(w io.Writer, plan prune.Plan, p Presentation) error {
	return writeGraphView(w, pruneView(plan), plan.Discovery, p)
}
