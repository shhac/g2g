package cli

import (
	"fmt"
	"io"

	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/shape"
)

// graphNodes projects a discovery onto the shared view.
//
// Depth is only populated when the selection actually forks. A selection where
// no branch has two selected children is a chain, and a chain reads better as
// the flat list every other command shows than as a staircase that pushes each
// branch further right for no information.
func graphNodes(discovery graph.Discovery) []stackNode {
	depths := shape.Depths(discovery.Branches, discovery.Graph.Parent)
	nodes := make([]stackNode, 0, len(discovery.Branches))
	for index, branch := range discovery.Branches {
		parent, _ := discovery.Graph.Parent(branch)
		node := stackNode{
			Branch: branch,
			Parent: parent,
			Depth:  depths[branch],
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

// stateAdvice is one recorded state that wants something done: the word a
// branch's line shows, what doctor calls the problem, and the command that
// puts it right.
type stateAdvice struct {
	label    string
	severity severity
	problem  func(parent string) string
	repair   func(branch, parent string) string
}

// recordedStates holds each of them once. status's notes and doctor's findings
// were written separately and had already drifted: status said "retrack" where
// doctor named a command, and a landed branch was a different colour in each.
var recordedStates = map[graph.NodeState]stateAdvice{
	graph.StateNeedsRestack: {"needs restack", severityWarn,
		said("its parent moved underneath it"),
		func(branch, _ string) string { return "g2g restack --branch " + branch }},
	graph.StateMovedOffParent: {"moved off parent", severityWarn,
		func(parent string) string { return "no longer built on " + parent },
		retrack},
	graph.StateForkUnresolvable: {"fork point lost", severityBad,
		said("its recorded fork point is gone"),
		retrack},
	graph.StateParentMissing: {"parent missing", severityWarn,
		func(parent string) string { return parent + " is no longer a local branch" },
		func(branch, _ string) string { return "g2g track --branch " + branch }},
	graph.StateBranchMissing: {"branch missing", severityWarn,
		said("recorded, and no longer a local branch"),
		func(branch, _ string) string { return "g2g untrack --branch " + branch }},
	graph.StateLanded: {"landed", severityOK,
		func(parent string) string { return "already landed in " + parent },
		func(branch, _ string) string { return "g2g prune --branch " + branch }},
}

// retrack records the fork point again on the parent already recorded, which
// track does only where that parent's tip is in the branch.
func retrack(branch, parent string) string {
	return "g2g track --branch " + branch + " --parent " + parent
}

func said(problem string) func(string) string { return func(string) string { return problem } }

// orphanRepair is a branch whose recorded parent is recorded nowhere.
func orphanRepair(branch string) string { return "g2g track --branch " + branch }

// nodeState says what the graph knows about one branch without a network call.
func nodeState(discovery graph.Discovery, branch string) (string, severity) {
	state := discovery.States[branch]
	if advice, known := recordedStates[state]; known {
		return advice.label, advice.severity
	}
	switch state {
	case graph.StateEmpty:
		return "no commits of its own", severityNeutral
	case graph.StateUntracked:
		if discovery.Graph.IsTrunk(branch) {
			return "trunk", severityNeutral
		}
		return "untracked", severityNeutral
	default:
		return "", severityNeutral
	}
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

// driftNotes report what g2g can see and deliberately will not repair.
func driftNotes(view stackView, discovery graph.Discovery) stackView {
	if stale := discovery.NeedsRestack(); len(stale) != 0 {
		view = view.note("Parent moved under "+branchList(stale)+" · run "+runnable("g2g restack")+".", severityWarn)
	}
	// What is wrong with one branch's edge is put right one branch at a time,
	// so these are a line each, naming the command doctor names.
	for _, each := range []struct {
		state  graph.NodeState
		prefix string
		then   string
	}{
		{graph.StateMovedOffParent, "No longer built on the recorded parent: ", "to record where it sits now, before restacking"},
		{graph.StateForkUnresolvable, "Recorded fork point is gone for ", "to record it again"},
		{graph.StateBranchMissing, "Recorded but no longer a local branch: ", "to forget it"},
		{graph.StateParentMissing, "Recorded parent is no longer a local branch for ", "to choose where it sits now"},
	} {
		advice := recordedStates[each.state]
		for _, branch := range discovery.InState(each.state) {
			parent, _ := discovery.Graph.Parent(branch)
			view = view.note(each.prefix+branch+" · run "+runnable(advice.repair(branch, parent))+" "+each.then+".", advice.severity)
		}
	}
	if landed := discovery.InState(graph.StateLanded); len(landed) != 0 {
		view = view.note("Already in the trunk: "+branchList(landed)+" · run "+runnable("g2g prune")+" to forget them.", severityNeutral)
	}
	if empty := discovery.InState(graph.StateEmpty); len(empty) != 0 {
		view = view.note("Nothing of their own on "+branchList(empty)+" · either finished, or not started yet.", severityNeutral)
	}
	for _, orphan := range discovery.Orphans() {
		view = view.note("No tracked parent for "+orphan+" · run "+runnable(orphanRepair(orphan))+" to choose one.", severityWarn)
	}
	return view
}
