package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/shhac/g2g/internal/create"
)

// stagedShown is how many staged paths are named before the rest are counted.
const stagedShown = 5

func writeCreatePlan(writer io.Writer, plan create.Plan, p Presentation) error {
	return writeStackView(writer, createView(plan), p)
}

// createView draws the path down to the parent with the new branch above it,
// so the preview shows where the branch will sit rather than describing it.
func createView(plan create.Plan) stackView {
	view := stackView{Operation: "create", Target: plan.Name, TargetSource: "new branch"}
	for _, node := range graphNodes(plan.Discovery) {
		node.Target = false
		if node.Branch == plan.NewTrunk {
			// It becomes a root by this write, which is what it is drawn as.
			node.Trunk, node.State, node.Severity = true, "", severityNeutral
		}
		view.Nodes = append(view.Nodes, node)
	}
	if plan.At != "" {
		view.Nodes = append(view.Nodes, stackNode{Branch: plan.Name, Parent: plan.Parent, Target: true, State: "new", Severity: severityOK})
	}
	if plan.Blocked != "" {
		return view.refusing(plan.Blocked, plan.Repair)
	}

	view = view.note(fmt.Sprintf("Creates %s at %s, the tip of %s, and switches to it.", plan.Name, shortObject(plan.At), plan.Parent), severityOK)
	view = view.note(fmt.Sprintf("Records %s under %s.", plan.Name, plan.Parent), severityNeutral)
	if plan.NewTrunk != "" {
		view = view.note(fmt.Sprintf("%s becomes a root of the graph, as the repository's default branch.", plan.NewTrunk), severityNeutral)
	}
	if plan.Commit {
		view = view.note("Commits "+stagedSummary(plan.Staged)+" onto it.", severityNeutral)
	}
	view.Sequence = createSequence(plan)
	if plan.Discovery.StorePath != "" {
		view = view.note("Graph store · "+plan.Discovery.StorePath, severityNeutral)
	}
	return view
}

// createSequence is what a person would run by hand to reach the same place,
// in the order this runs it. The record is the same track plan
// `track --parent` writes, so that is the command named for it.
func createSequence(plan create.Plan) []stackStep {
	steps := []stackStep{
		{Command: commandText([]string{"git", "switch", "-c", plan.Name, plan.Parent}), Effect: "start " + plan.Name + " at " + plan.Parent + " and check it out"},
		{Command: commandText([]string{"g2g", "track", "--branch", plan.Name, "--parent", plan.Parent, "--apply"}), Effect: "record it under " + plan.Parent},
	}
	if plan.Commit {
		steps = append(steps, stackStep{Command: commandText([]string{"git", "commit", "-m", plan.Message}), Effect: "commit what is staged"})
	}
	return steps
}

func stagedSummary(paths []string) string {
	shown := paths
	if len(shown) > stagedShown {
		shown = shown[:stagedShown]
	}
	summary := count(len(paths), "staged file", "staged files") + " (" + strings.Join(shown, ", ")
	if rest := len(paths) - len(shown); rest > 0 {
		summary += fmt.Sprintf(", and %d more", rest)
	}
	return summary + ")"
}
