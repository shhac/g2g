package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/shhac/gt2gh/internal/graph"
)

// graphNodes projects a discovery onto the shared view.
//
// Depth is only populated when the selection actually forks. A selection where
// no branch has two selected children is a chain, and a chain reads better as
// the flat list every other command shows than as a staircase that pushes each
// branch further right for no information.
func graphNodes(discovery graph.Discovery) []stackNode {
	depths := renderDepths(discovery)
	nodes := make([]stackNode, 0, len(discovery.Branches))
	for index, branch := range discovery.Branches {
		parent, _ := discovery.Graph.Parent(branch)
		node := stackNode{
			Branch: branch,
			Parent: parent,
			Depth:  depths[index],
			Target: branch == discovery.Target,
			// The root of a path or component is the base the graph hangs
			// from. A lone untracked branch is not a base, it is the branch
			// being asked about, so a single-node view has no trunk.
			Trunk: discovery.Graph.IsTrunk(branch) || (index == 0 && len(discovery.Branches) > 1 && !discovery.Graph.Tracked(branch)),
		}
		node.State, node.Severity = nodeState(discovery, branch)
		nodes = append(nodes, node)
	}
	return nodes
}

// nodeState says what the graph knows about one branch without a network call.
func nodeState(discovery graph.Discovery, branch string) (string, severity) {
	switch discovery.States[branch] {
	case graph.StateNeedsRestack:
		return "needs restack", severityWarn
	case graph.StateMovedOffParent:
		return "moved off parent", severityWarn
	case graph.StateForkUnresolvable:
		return "fork point lost", severityBad
	case graph.StateParentMissing:
		return "parent missing", severityWarn
	case graph.StateUntracked:
		if discovery.Graph.IsTrunk(branch) {
			return "trunk", severityNeutral
		}
		return "untracked", severityNeutral
	default:
		return "", severityNeutral
	}
}

// renderDepths returns the indentation depth of each selected branch, or all
// zeros when the selection is a chain.
func renderDepths(discovery graph.Discovery) []int {
	positions := selectedPositions(discovery)
	depths := make([]int, len(discovery.Branches))
	if !forks(discovery, positions) {
		return depths
	}
	for index, branch := range discovery.Branches {
		if parentIndex, inSelection := selectedParent(discovery, positions, branch); inSelection {
			depths[index] = depths[parentIndex] + 1
		}
	}
	return depths
}

func selectedPositions(discovery graph.Discovery) map[string]int {
	positions := make(map[string]int, len(discovery.Branches))
	for index, branch := range discovery.Branches {
		positions[branch] = index
	}
	return positions
}

// selectedParent locates a branch's parent within the selection. A parent
// outside it is not a parent for rendering: the selection's own root hangs
// from nothing.
func selectedParent(discovery graph.Discovery, positions map[string]int, branch string) (int, bool) {
	parent, tracked := discovery.Graph.Parent(branch)
	if !tracked {
		return 0, false
	}
	index, inSelection := positions[parent]
	return index, inSelection
}

// forks reports whether any selected branch has two selected children. A
// selection without one is a chain, and a chain reads better as the flat list
// every other command shows.
func forks(discovery graph.Discovery, positions map[string]int) bool {
	children := make(map[int]int, len(discovery.Branches))
	for _, branch := range discovery.Branches {
		parentIndex, inSelection := selectedParent(discovery, positions, branch)
		if !inSelection {
			continue
		}
		children[parentIndex]++
		if children[parentIndex] > 1 {
			return true
		}
	}
	return false
}

func graphView(discovery graph.Discovery, operation string) stackView {
	return stackView{
		Operation:    operation,
		Target:       discovery.Target,
		TargetSource: discovery.TargetSource,
		Nodes:        graphNodes(discovery),
	}
}

// writeGraphView renders a graph view and appends the store line to it.
//
// That line goes last in every one of these views — it says where the graph
// lives, which is worth being able to find and is not what the reader came
// for. Adding it here rather than at each view's return means no branch of any
// of them can forget it.
func writeGraphView(writer io.Writer, view stackView, discovery graph.Discovery, p Presentation) error {
	summary := fmt.Sprintf("Scope %s · %s · %s", discovery.Scope, count(len(discovery.Branches), "branch", "branches"), discovery.StorePath)
	return writeStackView(writer, view.note(summary, severityNeutral), p)
}

func count(total int, singular, plural string) string {
	return fmt.Sprintf("%d %s", total, pick(total, singular, plural))
}

// pick chooses the form that agrees with a count. Two helpers were doing this,
// one counting and one not, which is one idea wearing two names.
func pick(total int, one, many string) string {
	if total == 1 {
		return one
	}
	return many
}

// driftNotes report what gt2gh can see and deliberately will not repair.
func driftNotes(view stackView, discovery graph.Discovery) stackView {
	if stale := discovery.NeedsRestack(); len(stale) != 0 {
		view = view.note("Parent moved under "+branchList(stale)+" · run g2g restack.", severityWarn)
	}
	if moved := discovery.InState(graph.StateMovedOffParent); len(moved) != 0 {
		view = view.note("No longer built on the recorded parent: "+branchList(moved)+" · retrack before restacking, the replay range would be wrong.", severityWarn)
	}
	if lost := discovery.InState(graph.StateForkUnresolvable); len(lost) != 0 {
		view = view.note("Recorded fork point is gone for "+branchList(lost)+" · retrack to record it again.", severityBad)
	}
	if missing := discovery.MissingParents(); len(missing) != 0 {
		view = view.note("Recorded parent is no longer a local branch for "+branchList(missing)+" · retrack onto its new parent.", severityWarn)
	}
	if orphans := discovery.Orphans(); len(orphans) != 0 {
		view = view.note("No tracked parent for "+branchList(orphans)+".", severityWarn)
	}
	return view
}

func graphStatusView(discovery graph.Discovery) stackView {
	view := graphView(discovery, "graph")
	if !discovery.Graph.Tracked(discovery.Target) {
		view = view.note("This branch has no recorded parent · run g2g track to adopt one.", severityNeutral)
	}
	return driftNotes(view, discovery)
}

func trackView(plan graph.TrackPlan) stackView {
	view := driftNotes(graphView(plan.Discovery, "track"), plan.Discovery)
	if plan.Blocked == "" {
		view = view.note(fmt.Sprintf("Records %s under %s.", plan.Target, plan.Parent), severityOK)
		if plan.NewTrunk != "" {
			view = view.note(fmt.Sprintf("%s becomes a root of the graph.", plan.NewTrunk), severityNeutral)
		}
		return view.note(confirmation(plan), severityFor(plan))
	}
	view = view.block("Apply blocked: " + plan.Blocked)
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
	return "Candidate parents, nearest first: " + strings.Join(described, ", ") + " · rerun with --parent <branch>."
}

func describeCandidate(candidate graph.Candidate) string {
	described := count(candidate.Distance, "commit", "commits") + " behind"
	if candidate.Trunk {
		described = "root, " + described
	}
	return " (" + described + ")"
}

func untrackView(plan graph.UntrackPlan) stackView {
	view := graphView(plan.Discovery, "untrack")
	if len(plan.Removed) == 0 {
		return view.note("No selected branch is tracked · nothing to remove.", severityNeutral)
	}
	view = view.note("Removes the recorded parent of "+branchList(plan.Removed)+".", severityOK)
	if len(plan.Orphaned) == 0 {
		return view
	}
	// Reparenting the children onto the grandparent would invent an edge the
	// user never asked for, so the consequence is shown instead.
	return view.note("Leaves "+branchList(plan.Orphaned)+" without a tracked parent · they are not reparented.", severityWarn)
}
