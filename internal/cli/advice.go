package cli

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/shhac/g2g/internal/link"
	"github.com/shhac/g2g/internal/repair"
)

// advice is what to do and which branches it applies to.
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
	// Reason is why, when the heading does not already say it. A refusal that
	// names a state — both sides moved, the graph records a different parent —
	// has to keep saying so once the sentence carrying it is replaced by this.
	Reason string
	// Ways are the ways out, in the order they should be considered. There is
	// usually one; a refusal that offers a choice — take the published trunk,
	// or reconcile it yourself — has several, and a sentence listing them is
	// where a reader loses which words belong to which option.
	Ways  []repair.Step
	Steps []adviceStep
}

// adviceStep is one branch the advice covers. Note is empty for the ordinary
// case, so only the exception carries text: annotating every line with the
// reason that Effect already gave is what made the old sentence long.
type adviceStep struct {
	Branch string
	Note   string
}

// repairAdvice lays out the repair link decided, with the branches it covers
// down the left. The decision is link's; this is only how a person reads it,
// so the sentence a machine reads and this column cannot name different
// commands.
func repairAdvice(plan link.Plan) advice {
	note, kinds := plan.Repair()
	return advice{Ways: note.Ways, Steps: steps(plan, kinds...)}
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

// lines renders the advice for a person, one way out per line and one branch
// per line, so a long name never has to share one with another.
func (a advice) lines(heading string, p Presentation) []string {
	rendered := []string{"", p.accent(heading)}
	if a.Reason != "" {
		rendered = append(rendered, "  "+p.problem(a.Reason))
	}
	for _, way := range alignedWays(a.Ways) {
		rendered = append(rendered, "  "+p.subdued(way))
	}
	for _, step := range a.Steps {
		line := "    " + p.branch(step.Branch)
		if step.Note != "" {
			line += p.subdued("  · " + step.Note)
		}
		rendered = append(rendered, line)
	}
	return rendered
}

// commands lists what this advice tells the reader to run. It is what must not
// differ from the sentence a machine reads: shape may, the command may not.
func (a advice) commands() []string {
	named := make([]string, 0, len(a.Ways))
	for _, way := range a.Ways {
		if way.Command != "" {
			named = append(named, way.Command)
		}
	}
	return named
}

// alignedWays lays the ways out in two columns: what to run, and what running
// it achieves. The commands vary in length with a branch name, so the width is
// computed rather than guessed, and the padding sits outside the mark so what
// is drawn as runnable is the command alone.
//
// A way with no command — "fetch and reconcile first", "track it in Graphite" —
// is a whole answer rather than a remark about a command, so it takes the line
// on its own rather than the column where effects sit.
func alignedWays(ways []repair.Step) []string {
	width := 0
	for _, way := range ways {
		if size := utf8.RuneCountInString(way.Command); size > width {
			width = size
		}
	}
	lines := make([]string, 0, len(ways))
	for _, way := range ways {
		if way.Command == "" {
			lines = append(lines, way.Effect)
			continue
		}
		padding := strings.Repeat(" ", width-utf8.RuneCountInString(way.Command))
		lines = append(lines, runnable(way.Command)+padding+"   "+way.Effect)
	}
	return lines
}
