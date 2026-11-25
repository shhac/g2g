package cli

import (
	"fmt"
	"io"
	"maps"
	"slices"

	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/prune"
	"github.com/shhac/g2g/internal/repair"
	"github.com/shhac/g2g/internal/words"
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
	view = view.note("Forgets "+words.List(plan.Forgotten())+" from the recorded graph.", severityWarn)
	view = keptNotes(view, plan)
	if len(plan.Delete) != 0 {
		return view.note("Deletes local branches "+words.List(plan.Deleted())+". Remote branches are untouched.", severityWarn)
	}
	return view.note(keptBranches(plan), severityNeutral)
}

// keptNotes says which landed branches stay recorded and what would let them
// go. They are held back rather than holding the prune back: forgetting the
// rest is safe whatever happens to these, and a prune that waited for every
// branch waited for the slowest stack in the repository.
func keptNotes(view stackView, plan prune.Plan) stackView {
	for _, kept := range []struct {
		note repair.Note
		says string
	}{
		{plan.Held, "Kept as they are, record and branch: "},
		{plan.Kept, "The rest stay recorded: "},
	} {
		if kept.note.Reason == "" {
			continue
		}
		view = view.note(kept.says+kept.note.Reason+".", severityWarn)
		for _, note := range alignedCommands(kept.note.Ways...) {
			view = view.note(note.Text, note.Severity)
		}
	}
	return view
}

// keptBranches says that the forgotten branches stay, and how to remove them
// as well. It has to be said before they are forgotten: prune deletes only
// branches it records, so once one is forgotten g2g can no longer remove it.
func keptBranches(plan prune.Plan) string {
	if len(plan.Landed) == 0 {
		return "No branch is deleted."
	}
	command := selectedIn(plan.Discovery).aimedOr(pruneCommand) + " --delete-branches"
	if plan.Options.ForgetMissing {
		command += " --forget-missing"
	}
	return "No branch is deleted · run " + runnable(command) + " to remove " + words.Pick(len(plan.Landed), "it", "them") + " too, which g2g cannot do once " + words.Pick(len(plan.Landed), "it is", "they are") + " forgotten."
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

func writePrunePlan(w io.Writer, plan prunePlan, p Presentation) error {
	return writeGraphView(w, mergedNotes(pruneView(plan.Plan), plan), plan.Discovery, p)
}

// mergedNotes says why a branch whose pull request merged is still here, and
// names the command that deletes one on purpose. delete previews any commit
// that exists nowhere else, which is the question a merged pull request
// cannot answer.
func mergedNotes(view stackView, plan prunePlan) stackView {
	if len(plan.merged) == 0 {
		return view
	}
	view = view.note(fmt.Sprintf("Kept, though %s last seen merged: %s · what %s here was not found in the trunk, as when a branch here is older than the one that was merged.",
		words.Pick(len(plan.merged), "its pull request was", "their pull requests were"), words.List(plan.merged),
		words.Pick(len(plan.merged), "it holds", "they hold")), severityWarn)
	ways := make([]repair.Step, 0, len(plan.merged))
	for _, branch := range plan.merged {
		ways = append(ways, repair.Step{
			Command: repair.Command([]string{"g2g", "delete", "--branch", branch}),
			Effect:  "delete " + branch + " on purpose · its preview names any commit that exists nowhere else",
		})
	}
	for _, note := range alignedCommands(ways...) {
		view = view.note(note.Text, note.Severity)
	}
	return view
}
