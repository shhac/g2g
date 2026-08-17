package cli

import (
	"fmt"
	"io"

	"github.com/shhac/g2g/internal/graph"
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

// driftNotes report what g2g can see and deliberately will not repair.
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
