package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/submit"
)

// What submitting a stack would do, drawn.
//
// Wiring in _cmd.go, projection in _preview.go -- the convention every other
// command in this package follows, and which submit was already close to with
// submit_spec.go and submit_templates.go beside it.

func submitView(plan submit.Plan, template string, draft bool) stackView {
	view := stackView{
		Operation:    "submit",
		Target:       plan.Snapshot.Target,
		TargetSource: plan.Snapshot.TargetSource,
		Nodes:        []stackNode{{Branch: plan.Snapshot.Base, Trunk: true}},
	}
	for _, branch := range plan.Snapshot.Branches {
		node := stackNode{Branch: branch, Target: branch == plan.Snapshot.Target, State: "create " + openAs(draft)}
		previous, replaced := plan.Superseded[branch]
		existing := existingNumber(plan, branch)
		switch {
		case plan.Issues[branch] != "":
			node.State, node.Severity = "blocked: "+plan.Issues[branch], severityBad
		case existing != 0:
			node.PRNumber, node.State, node.Severity = existing, "existing", severityOK
		case replaced:
			node.State = fmt.Sprintf("create %s · #%d %s", openAs(draft), previous.Number, strings.ToLower(previous.State))
		}
		view.Nodes = append(view.Nodes, node)
	}

	if template != "" {
		view = view.note("PR template: "+template, severityNeutral)
	}
	if len(plan.Issues) != 0 {
		return view.blockedBy("repair the marked existing pull requests first.")
	}
	// Publishing is push's, refusals included, so its reason and ways out are
	// the ones push itself would show.
	if plan.Push.Blocked != "" {
		return view.refusing(plan.Push.Repair.SentenceWith(runnable), plan.Push.Repair)
	}
	return view.note(fmt.Sprintf("Missing PRs will be created %s; existing PRs are preserved.", openAsPlural(draft)), severityNeutral)
}

// openAs names what a missing pull request will be opened as. The preview is
// rendered and flushed immediately before the mutation, so saying "draft" while
// about to open ready for review is not a cosmetic error: it is the last thing
// a person reads before reviewers are notified.
func openAs(draft bool) string {
	if draft {
		return "draft"
	}
	return "ready for review"
}

func openAsPlural(draft bool) string {
	if draft {
		return "as drafts"
	}
	return "ready for review"
}

// readyFlag echoes the choice back in any command this preview suggests, so a
// copied command reproduces the run that was previewed.
func readyFlag(draft bool) string {
	if draft {
		return ""
	}
	return " --ready"
}

func writeSubmitPreview(w io.Writer, plan submit.Plan, p Presentation, template string, draft bool) error {
	return writeStackView(w, submitView(plan, template, draft), p)
}

// existingNumber routes through the shared resolution rather than scanning for
// the first open head, so the preview cannot disagree with the plan about
// which pull request represents a branch.
func existingNumber(plan submit.Plan, branch string) int {
	if resolution := githubstack.ResolveHeads(plan.Existing)[branch]; resolution.Open != nil {
		return resolution.Open.Number
	}
	return 0
}
