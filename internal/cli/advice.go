package cli

import (
	"fmt"

	"github.com/shhac/g2g/internal/link"
)

// advice is what to run and which branches it applies to.
//
// It exists because the same content has two readers with different needs. A
// machine wants one sentence in a single field, which is what blockedReason
// still returns and what --json and --porcelain carry unchanged. A person
// standing in a terminal wants the branch names down the left, because a stack
// of fifteen produced a sentence that wrapped mid-name and had to be read
// twice to find out which four branches it was talking about.
//
// Both are built from the same predicates on the plan, so the two can differ in
// shape and cannot differ in which command they name.
type advice struct {
	// Command is what to run, or empty when nothing g2g offers repairs this.
	Command string
	// Effect says what running it does, in the reader's terms rather than the
	// implementation's.
	Effect string
	Steps  []adviceStep
}

// adviceStep is one branch the advice covers. Note is empty for the ordinary
// case, so only the exception carries text: annotating every line with the
// reason that Effect already gave is what made the old sentence long.
type adviceStep struct {
	Branch string
	Note   string
}

// repairAdvice names the command that actually repairs the state, and the
// branches it acts on.
//
// The order is the order the cases have to be answered in, and merged comes
// first because it is the only one no g2g command fixes: the stack itself is
// stale, and Graphite has to restack around it before anything here helps.
func repairAdvice(plan link.Plan) advice {
	if merged := plan.MergedBranches(); len(merged) != 0 {
		return advice{
			Command: "gt sync",
			Effect:  "restack in Graphite first · no g2g command fixes a merged branch",
			Steps:   steps(plan, link.IssueMerged),
		}
	}
	if plan.SyncRepairable() {
		return advice{
			Command: "g2g sync",
			Effect:  "every PR is open but based on the wrong branch",
			Steps:   steps(plan, link.IssueBase),
		}
	}
	if plan.SubmitRepairable() {
		return advice{
			Command: "g2g submit",
			Effect:  pick(len(plan.Issues), "opens a new PR", fmt.Sprintf("opens a new PR for each of these %d branches", len(plan.Issues))),
			Steps:   steps(plan, link.IssueMissing, link.IssueClosed),
		}
	}
	return advice{Effect: "resolve every unresolved GitHub PR mapping first", Steps: steps(plan)}
}

// steps lists the branches this advice acts on, annotating only the ones the
// headline does not already account for.
//
// A branch whose PR was closed is the case that needs its number: "submit
// opens a new PR" is advice nobody can judge without going to read why the old
// one was closed, and the number is how they get there.
func steps(plan link.Plan, kinds ...link.IssueKind) []adviceStep {
	wanted := map[link.IssueKind]bool{}
	for _, kind := range kinds {
		wanted[kind] = true
	}
	ordinary := link.IssueKind("")
	if len(kinds) != 0 {
		ordinary = kinds[0]
	}
	listed := make([]adviceStep, 0, len(plan.Issues))
	for _, issue := range plan.Issues {
		if len(kinds) != 0 && !wanted[issue.Kind] {
			continue
		}
		step := adviceStep{Branch: issue.Branch}
		if issue.Kind != ordinary {
			step.Note = issue.Reason
			if issue.Number != 0 && issue.Kind == link.IssueClosed {
				step.Note = fmt.Sprintf("#%d was closed", issue.Number)
			}
		}
		listed = append(listed, step)
	}
	return listed
}

// lines renders the advice for a person, one branch per line so a long name
// never has to share one with another.
func (a advice) lines(heading string, p Presentation) []string {
	headline := a.Effect
	if a.Command != "" {
		headline = p.command(a.Command) + p.subdued(" · "+a.Effect)
	}
	rendered := []string{"", p.accent(heading), "  " + headline}
	for _, step := range a.Steps {
		line := "    " + p.branch(step.Branch)
		if step.Note != "" {
			line += p.subdued("  · " + step.Note)
		}
		rendered = append(rendered, line)
	}
	return rendered
}
