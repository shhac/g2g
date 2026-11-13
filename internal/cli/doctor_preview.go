package cli

import (
	"fmt"

	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/push"
	"github.com/shhac/g2g/internal/repair"
	"github.com/shhac/g2g/internal/restack"
	"github.com/shhac/g2g/internal/words"
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
func diagnose(discovery graph.Discovery, restacks unfinished, publishing map[string]push.Publication, remote string) []finding {
	findings := restacks.findings()
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
		if publication.Standing == push.Conflicting {
			findings = append(findings, finding{
				Branch: branch, Problem: "conflicting local remote records for this branch or its base",
				Command: "git fetch " + repair.Quote(remote), Severity: severityWarn,
			})
			continue
		}
		if publication.Standing == push.Diverged && !discovery.Graph.IsTrunk(branch) {
			findings = append(findings, finding{
				Branch:   branch,
				Problem:  "diverged from " + remote + " · " + eachSide(publication),
				Command:  selected{branch: branch, named: true}.from(remote).aimedOr(pullCommand),
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
			view.Nodes = append(view.Nodes, stackNode{Branch: found.Branch}.labeled(found.Problem, found.Severity))
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
		return view.note(fmt.Sprintf("Nothing needs putting right across %s.", words.Count(len(discovery.Graph.Edges), "recorded branch", "recorded branches")), severityOK)
	}
	return view
}

// unfinished is every restack left part-way: whether this worktree has one,
// and every other worktree's. Each worktree journals its own, and a doctor
// that looked only here said nothing of one an agent left in another.
type unfinished struct {
	here      bool
	elsewhere []restack.Elsewhere
}

// findings is one finding per unfinished restack, each with what finishes
// it: --continue here, the same in the checkout another belongs to, and for
// one whose checkout is gone, forgetting its journal.
func (u unfinished) findings() []finding {
	findings := make([]finding, 0, len(u.elsewhere)+1)
	if u.here {
		findings = append(findings, finding{Problem: "a restack stopped part-way", Command: "g2g restack --continue", Severity: severityBad})
	}
	for _, other := range u.elsewhere {
		of := ""
		if branch := other.Started(); branch != "" {
			of = " of " + branch
		}
		if checkout := other.Checkout(); checkout != "" {
			findings = append(findings, finding{
				Problem:  "a restack" + of + " stopped part-way in " + checkout,
				Command:  "cd " + repair.Quote(checkout) + " && g2g restack --continue",
				Severity: severityBad,
			})
			continue
		}
		if other.Path == "" {
			continue
		}
		findings = append(findings, finding{
			Problem:  "a restack" + of + " stopped part-way in a worktree that no longer exists",
			Command:  repair.Command([]string{"rm", other.Path}),
			Severity: severityBad,
		})
	}
	return findings
}
