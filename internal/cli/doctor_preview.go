package cli

import (
	"fmt"

	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/push"
)

// finding is one thing that is not as it should be, and the way to put it
// right. Branch is empty for one that is about the repository as a whole.
type finding struct {
	Branch   string
	Problem  string
	Command  string
	Severity severity
}

// diagnose names every finding, in the order the stacks are drawn.
//
// A branch with no commits of its own is not one: it is as likely to be a
// branch nobody has started as one that is finished, and saying either would
// be a guess.
func diagnose(discovery graph.Discovery, interrupted bool, publishing map[string]push.Publication, remote string) []finding {
	findings := make([]finding, 0)
	if interrupted {
		findings = append(findings, finding{
			Problem:  "a restack stopped part-way",
			Command:  "g2g restack --continue",
			Severity: severityBad,
		})
	}
	orphans := make(map[string]bool)
	for _, branch := range discovery.Orphans() {
		orphans[branch] = true
	}
	for _, branch := range discovery.Branches {
		if found, problem := branchFinding(discovery, branch); problem {
			findings = append(findings, found)
			continue
		}
		if orphans[branch] {
			findings = append(findings, finding{Branch: branch, Problem: "no tracked parent", Command: orphanRepair(branch), Severity: severityWarn})
			continue
		}
		publication := publishing[branch]
		if publication.Standing == push.Diverged && !discovery.Graph.IsTrunk(branch) {
			findings = append(findings, finding{
				Branch:   branch,
				Problem:  "diverged from " + remote + " · " + eachSide(publication),
				Command:  "g2g pull --branch " + branch,
				Severity: severityBad,
			})
		}
	}
	return findings
}

// branchFinding is what the graph's own state says is wrong with a branch, in
// the words status uses for the same state.
func branchFinding(discovery graph.Discovery, branch string) (finding, bool) {
	advice, known := recordedStates[discovery.States[branch]]
	if !known {
		return finding{}, false
	}
	parent, _ := discovery.Graph.Parent(branch)
	return finding{Branch: branch, Problem: advice.problem(parent), Command: advice.repair(branch, parent), Severity: advice.severity}, true
}

// doctorView lists the findings and nothing else: the branches they are about,
// each with what is wrong, and the command for each beneath.
func doctorView(discovery graph.Discovery, findings []finding) stackView {
	view := stackView{Operation: "doctor", Target: "every recorded stack", TargetSource: "repository"}
	for _, found := range findings {
		if found.Branch != "" {
			view.Nodes = append(view.Nodes, stackNode{Branch: found.Branch, State: found.Problem, Severity: found.Severity})
		}
	}
	for _, found := range findings {
		subject := found.Problem
		if found.Branch != "" {
			subject = found.Branch + ": " + found.Problem
		}
		view = view.note(subject+" · run "+runnable(found.Command)+".", found.Severity)
	}
	if len(findings) == 0 {
		return view.note(fmt.Sprintf("Nothing needs putting right across %s.", count(len(discovery.Graph.Edges), "recorded branch", "recorded branches")), severityOK)
	}
	return view
}
