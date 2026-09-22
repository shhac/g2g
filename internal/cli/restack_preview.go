package cli

import (
	"strings"

	"github.com/shhac/g2g/internal/restack"
)

func restackView(plan restack.Plan) stackView {
	view := graphView(plan.Discovery, "restack")
	if plan.Blocked != "" {
		return view.refusing(plan.Blocked, plan.Repair)
	}
	if plan.Nothing() {
		return view.note("Every selected branch already sits on its parent. Nothing to replay.", severityOK)
	}
	if len(plan.Steps) == 0 {
		return view.note("Already sits on "+plan.Onto.Parent+" · nothing to replay, only the new parent to record.", severityOK)
	}
	// A branch that collapses is not replayed, only moved, and saying it was
	// replayed would misdescribe what happened to its commits.
	if replaying := plan.Replaying(); len(replaying) != 0 {
		view = view.note("Replays "+ontoEach(plan, replaying)+".", severityOK)
	} else {
		view = view.note("Moves "+ontoEach(plan, plan.Emptied())+" · nothing needs replaying.", severityOK)
	}
	view = orphanNote(view, plan)
	view = emptiedNote(view, plan)
	return engineNote(view, plan)
}

// ontoEach says where each branch lands, grouped by the root it replays with.
// A selection can have several roots on different parents, and naming the
// first one's parent for all of them told the reader the wrong destination.
func ontoEach(plan restack.Plan, branches []string) string {
	parent := map[string]string{}
	for _, step := range plan.Steps {
		parent[step.Branch] = step.Parent
	}
	rootParent := func(branch string) string {
		for {
			above, stepped := parent[branch]
			if _, rewritten := parent[above]; !stepped || !rewritten {
				return above
			}
			branch = above
		}
	}
	order := make([]string, 0)
	grouped := map[string][]string{}
	for _, branch := range branches {
		onto := rootParent(branch)
		if _, seen := grouped[onto]; !seen {
			order = append(order, onto)
		}
		grouped[onto] = append(grouped[onto], branch)
	}
	said := make([]string, 0, len(order))
	for _, onto := range order {
		said = append(said, branchList(grouped[onto])+" onto "+onto)
	}
	return strings.Join(said, "; ")
}

// orphanNote names commits a rewritten parent dropped that a child still
// carries. Silently changing what a branch contains is the one thing this
// command must never do.
func orphanNote(view stackView, plan restack.Plan) stackView {
	orphans := plan.Orphaned()
	if len(orphans) == 0 {
		return view
	}
	dropped := count(len(orphans), "commit", "commits")
	if plan.Absorb {
		return view.note("Keeps "+dropped+" the parent dropped, by re-recording where the branch forks. Nothing is rewritten.", severityWarn)
	}
	note := "The parent dropped " + dropped + " this branch still carries; they will be dropped here too."
	if plan.Absorbable() {
		return view.note(note+" Use --absorb to keep them instead.", severityWarn)
	}
	// A rewritten commit still exists in the parent under a new object id, so
	// keeping the old copy would duplicate it.
	return view.note(note+" They cannot be absorbed: the parent rewrote rather than removed them.", severityWarn)
}

func emptiedNote(view stackView, plan restack.Plan) stackView {
	emptied := plan.Emptied()
	if len(emptied) == 0 {
		return view
	}
	return view.note("Leaves "+branchList(emptied)+" with no commits of its own · its content is already upstream, so consider "+runnable("g2g untrack")+".", severityWarn)
}

// engineNote is the informed-consent line. A rewrite that cannot apply cleanly
// has to run in the user's working tree, and they should know that before they
// ask for it rather than afterwards.
func engineNote(view stackView, plan restack.Plan) stackView {
	if plan.Absorb {
		return view
	}
	if !plan.Predicted {
		// Saying it will conflict would be a claim we have not made, and the
		// reason there is no preview is worth saying: an old Git and a parent
		// that only exists once sync has brought it down want different
		// responses.
		reason := plan.Unpredicted
		if reason == "" {
			reason = "the result cannot be previewed"
		}
		return view.note(strings.ToUpper(reason[:1])+reason[1:]+", so applying rebases in your working tree. If it stops on a conflict, resolve it and run "+runnable("g2g restack --continue")+".", severityWarn)
	}
	if plan.Clean {
		return view.note("Applies without touching your working tree or checked-out branch.", severityNeutral)
	}
	return view.note("This will not apply cleanly. Applying rebases in your working tree and stops on the conflict for you to resolve, then "+runnable("g2g restack --continue")+".", severityWarn)
}

// interruptedNote is what every other command shows while a restack is
// unfinished, because a branch may already have moved while the graph still
// records where it used to be.
func interruptedNote() string {
	return "A restack is in progress. Finish it with g2g restack --continue, or undo it with g2g restack --abort."
}
