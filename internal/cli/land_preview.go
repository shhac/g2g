package cli

import (
	"fmt"
	"io"

	"github.com/shhac/g2g/internal/land"
)

// landView draws the descent: the stack as it is, what each branch needs, and
// the ordered recipe that would take it down.
func landView(plan land.Plan) stackView {
	view := stackView{
		Operation:    "land",
		Target:       plan.Target,
		TargetSource: plan.TargetSource,
		Nodes:        landNodes(plan),
	}
	if plan.Blocked != "" {
		// No recipe: a refused descent has no ordered set of commands that
		// would reach the end, and offering the ones decided before the
		// refusal would invite someone to run half of it.
		return view.refusing(plan.Blocked, plan.Repair)
	}
	view.Sequence = landSequence(plan)
	if len(plan.Protected) != 0 {
		// Said before the first merge rather than discovered at the second.
		// Every branch above the bottom is force-pushed by its own replay,
		// which restarts the required checks that were green when this was
		// planned, and on a protected repository that is every run.
		view = view.note(fmt.Sprintf("%s will read blocked once their replay restarts the required checks · rerun with %s to merge without waiting for them",
			branchList(plan.Protected), runnable("--admin")), severityWarn)
	}
	return view
}

// landNodes draws every selected branch, whether or not a step was decided for
// it. A refused descent keeps no steps, and a stack drawn from the steps alone
// would then be missing exactly the branch the refusal is about.
func landNodes(plan land.Plan) []stackNode {
	steps := make(map[string]land.Step, len(plan.Steps))
	for _, step := range plan.Steps {
		steps[step.Branch] = step
	}
	nodes := make([]stackNode, 0, len(plan.Branches)+1)
	if plan.Trunk != "" {
		nodes = append(nodes, stackNode{Branch: plan.Trunk, Trunk: true})
	}
	for _, branch := range plan.Branches {
		node := stackNode{Branch: branch, Target: branch == plan.Target, Depth: 1}
		step, decided := steps[branch]
		if !decided {
			nodes = append(nodes, node)
			continue
		}
		node.PRNumber, node.PRURL = step.Number, step.URL
		nodes = append(nodes, node.marked(landMarks(step)...))
	}
	return nodes
}

// landMarks says what each branch needs, one axis at a time.
//
// Separate marks rather than one sentence: a branch can want a push and a base
// move and an admin merge at once, and they carry different weight. Folding
// them into one string is how a line came to open with a reassuring word and
// go on to describe three things that had to happen first.
func landMarks(step land.Step) []stackMark {
	if step.Landed {
		return []stackMark{{Detail: "already in the trunk · forget and delete only", Severity: severityNeutral, OK: true}}
	}
	marks := make([]stackMark, 0, 3)
	if step.Push {
		marks = append(marks, stackMark{Subject: "head", Detail: "publish first", Severity: severityWarn})
	}
	if step.Retargets() {
		marks = append(marks, stackMark{Subject: "base", Detail: "→ " + step.Base, Severity: severityWarn})
	} else {
		marks = append(marks, stackMark{Subject: "base", OK: true})
	}
	detail := "merge"
	severity := severityOK
	if step.Admin {
		detail, severity = "merge with --admin", severityWarn
	}
	marks = append(marks, stackMark{Detail: detail, Severity: severity})
	return marks
}

func landSequence(plan land.Plan) []stackStep {
	commands := plan.Commands()
	steps := make([]stackStep, 0, len(commands))
	for _, command := range commands {
		steps = append(steps, stackStep{Command: command.Command, Effect: command.Effect})
	}
	return steps
}

func writeLandPlan(writer io.Writer, plan land.Plan, p Presentation) error {
	return writeStackView(writer, landView(plan), p)
}
