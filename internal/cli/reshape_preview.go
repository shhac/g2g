package cli

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/shhac/g2g/internal/reshape"
)

func writeRemovalPlan(writer io.Writer, plan reshape.Plan, p Presentation) error {
	return writeStackView(writer, removalView(plan), p)
}

// removalView draws the stack as it is, with each branch the removal touches
// saying what happens to it, so the preview shows the change in place.
func removalView(plan reshape.Plan) stackView {
	view := graphView(plan.Discovery, string(plan.Operation))
	if plan.Blocked != "" {
		return view.refusing(plan.Blocked, plan.Repair)
	}
	view.Nodes = removalNodes(plan, view.Nodes)
	if plan.Operation == reshape.Fold {
		view = foldNotes(view, plan)
	} else {
		view = deleteNotes(view, plan)
	}
	view = view.note(remoteNote(plan.Branch, plan.Remote, "deletes the local branch only"), severityNeutral)
	return view.note("Graph store · "+plan.Discovery.StorePath, severityNeutral)
}

func removalNodes(plan reshape.Plan, nodes []stackNode) []stackNode {
	for index, node := range nodes {
		switch {
		case node.Branch == plan.Branch && plan.Operation == reshape.Fold:
			node.State, node.Severity = "folds into "+plan.Parent, severityWarn
		case node.Branch == plan.Branch:
			node.State, node.Severity = "deleted", severityBad
		case slices.Contains(plan.Children, node.Branch) && plan.Operation == reshape.Fold:
			node.State, node.Severity = "moves onto "+plan.Parent, severityOK
		case slices.Contains(plan.Children, node.Branch):
			node.State, node.Severity = "moves onto "+plan.Parent+" · needs restack", severityWarn
		case slices.Contains(plan.Siblings, node.Branch):
			node.State, node.Severity = "needs restack", severityWarn
		default:
			continue
		}
		nodes[index] = node
	}
	return nodes
}

func deleteNotes(view stackView, plan reshape.Plan) stackView {
	view = view.note(fmt.Sprintf("Deletes %s, at %s.", plan.Branch, shortObject(plan.Tip)), severityOK)
	if plan.Current == plan.Branch {
		view = view.note(switchNote(plan), severityNeutral)
	}
	if len(plan.Children) != 0 {
		view = view.note(fmt.Sprintf("Records %s on %s, where %s sat. %s its fork point, so the next restack replays only its own commits onto %s and %s's commits leave %s%s.",
			branchList(plan.Children), plan.Parent, plan.Branch, pick(len(plan.Children), "It keeps", "Each keeps"),
			plan.Parent, plan.Branch, pick(len(plan.Children), "it", "them"), restackHint(plan)), severityWarn)
	}
	if len(plan.Unique) == 0 {
		return view.note(fmt.Sprintf("Everything on %s is already in %s or on a remote-tracking ref, so deleting it loses no commit.", plan.Branch, plan.Parent), severityOK)
	}
	listed := make([]string, 0, len(plan.Unique))
	for _, commit := range plan.Unique {
		listed = append(listed, shortObject(commit.ID)+" "+commit.Subject)
	}
	// Named one by one rather than counted: this is work that exists nowhere
	// else, and once the branch is gone nothing here will name it again.
	return view.note(fmt.Sprintf("%s %s that %s nowhere else, not in %s and on no remote-tracking ref, and %s gone once %s is: %s.",
		plan.Branch, pick(len(plan.Unique), "has 1 commit", fmt.Sprintf("has %d commits", len(plan.Unique))),
		pick(len(plan.Unique), "exists", "exist"), plan.Parent, pick(len(plan.Unique), "it is", "they are"), plan.Branch,
		strings.Join(listed, "; ")), severityBad)
}

func foldNotes(view stackView, plan reshape.Plan) stackView {
	if plan.Moves() {
		view = view.note(fmt.Sprintf("Fast-forwards %s from %s to %s, so %s's commits become %s's.",
			plan.Parent, shortObject(plan.ParentTip), shortObject(plan.Tip), plan.Branch, plan.Parent), severityOK)
	} else {
		view = view.note(fmt.Sprintf("%s has no commits of its own, so %s does not move.", plan.Branch, plan.Parent), severityNeutral)
	}
	if plan.Moves() && plan.Current == plan.Parent {
		view = view.note(plan.Parent+" is checked out, so the working tree moves with it · a local change it would overwrite stops the fold and puts "+plan.Parent+" back.", severityNeutral)
	}
	view = view.note("Deletes "+plan.Branch+".", severityNeutral)
	if plan.Current == plan.Branch {
		view = view.note("Switches to "+plan.Parent+" first, which by then is the same commit.", severityNeutral)
	}
	if len(plan.Children) != 0 {
		view = view.note(fmt.Sprintf("Records %s on %s, where %s's commits now are.", branchList(plan.Children), plan.Parent, plan.Branch), severityNeutral)
	}
	if len(plan.Siblings) != 0 && plan.Moves() {
		view = view.note(fmt.Sprintf("%s also %s on %s and will need a restack once it moves%s.", branchList(plan.Siblings), pick(len(plan.Siblings), "sits", "sit"), plan.Parent, restackHint(plan)), severityWarn)
	}
	return view
}

// restackHint names the restack that finishes the job, when one command does.
func restackHint(plan reshape.Plan) string {
	next := removalNext(plan)
	if !strings.HasPrefix(next, "g2g restack") {
		return ""
	}
	return " · run " + runnable(next) + " afterwards"
}

// switchNote says why the checkout moves and what stops it: git will not
// delete the branch it is on, and git switch will not overwrite a change.
func switchNote(plan reshape.Plan) string {
	return fmt.Sprintf("Switches to %s first, because git will not delete the branch it is on · a local change that switch would overwrite stops the delete before anything changes.", plan.Parent)
}

// remoteNote says what a local change leaves on every remote, naming the
// remote-tracking refs this clone knows carry the branch.
func remoteNote(branch string, remote []string, action string) string {
	if len(remote) == 0 {
		return "This " + action + " · nothing on any remote changes."
	}
	return fmt.Sprintf("This %s · %s %s untouched, and so is any pull request for %s.", action, branchList(remote), pick(len(remote), "is", "are"), branch)
}

func writeRenamePlan(writer io.Writer, plan reshape.RenamePlan, p Presentation) error {
	return writeStackView(writer, renameView(plan), p)
}

func renameView(plan reshape.RenamePlan) stackView {
	view := graphView(plan.Discovery, "rename")
	if plan.Blocked != "" {
		return view.refusing(plan.Blocked, plan.Repair)
	}
	for index, node := range view.Nodes {
		if node.Branch == plan.From {
			view.Nodes[index].State, view.Nodes[index].Severity = "becomes "+plan.To, severityOK
		}
	}
	view = view.note(fmt.Sprintf("Renames %s to %s with git branch -m, which carries its configuration and reflog.", plan.From, plan.To), severityOK)
	switch {
	case plan.Current == plan.From:
		view = view.note("It is checked out here, and stays checked out under the new name.", severityNeutral)
	case plan.Elsewhere != "":
		view = view.note("It is checked out in "+plan.Elsewhere+", and git moves that worktree to the new name with it.", severityNeutral)
	}
	if plan.Trunk {
		view = view.note(fmt.Sprintf("%s is a trunk, and the graph records it as one under the new name.", plan.From), severityNeutral)
	}
	if len(plan.Children) != 0 {
		view = view.note(fmt.Sprintf("Records %s on %s.", branchList(plan.Children), plan.To), severityNeutral)
	}
	if len(plan.Remote) != 0 {
		view = view.note(fmt.Sprintf("%s %s the old name, and the published branch and any pull request for it stay under that name · %s publishes %s as a new branch.",
			branchList(plan.Remote), pick(len(plan.Remote), "still carries", "still carry"), runnable("g2g push"), plan.To), severityWarn)
	}
	return view.note("Graph store · "+plan.Discovery.StorePath, severityNeutral)
}
