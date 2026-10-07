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

func submitView(plan submit.Plan, template string, draft, link, comments bool) stackView {
	view := stackView{
		Operation:    "submit",
		Target:       plan.Snapshot.Target,
		TargetSource: plan.Snapshot.TargetSource,
		Nodes:        []stackNode{{Branch: plan.Snapshot.Base, Trunk: true}},
	}
	for _, branch := range plan.Snapshot.Branches {
		node := stackNode{Branch: branch, Target: branch == plan.Snapshot.Target}
		state, level := "create "+openAs(draft), severity("")
		previous, replaced := plan.Superseded[branch]
		existing := existingNumber(plan, branch)
		switch {
		case plan.Issues[branch] != "":
			state, level = "blocked: "+plan.Issues[branch], severityBad
		case existing != 0:
			node.PRNumber = existing
			state, level = "existing", severityOK
		case replaced:
			state = fmt.Sprintf("create %s · #%d %s", openAs(draft), previous.Number, strings.ToLower(previous.State))
		}
		view.Nodes = append(view.Nodes, node.labeled(state, level))
	}

	if template != "" {
		view = view.note("PR template: "+template, severityNeutral)
	}
	if len(plan.Issues) != 0 {
		return view.blockedBy("repair the marked existing pull requests first.")
	}
	if link && plan.LinkBlocked() != "" {
		return view.refusing(plan.LinkRepair())
	}
	// Publishing is push's, refusals included, so its reason and ways out are
	// the ones push itself would show.
	if plan.Push.Blocked() != "" {
		return view.refusing(plan.Push.Repair)
	}
	view = view.note("Publishes the selected branches through one atomic, lease-protected push; existing PR bases are preserved.", severityNeutral)
	if _, existingOnly := plan.ExistingSpec(); !existingOnly {
		view = view.note(fmt.Sprintf("Missing PRs will be created %s; existing PRs are preserved.", openAsPlural(draft)), severityNeutral)
	}
	if link && len(plan.Snapshot.Branches) > 1 {
		view = view.note("Then links them as a GitHub stack · g2g land refuses a linked stack until it is unlinked.", severityNeutral)
	}
	switch {
	case comments && len(plan.Snapshot.Branches) == 1:
		view = view.note("Then keeps the stack comment, which a stack listing only one pull request does not get · --no-comment skips it.", severityNeutral)
	case comments:
		view = view.note("Then keeps the stack comment on each pull request · --no-comment skips it.", severityNeutral)
	}
	return view
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

// linkFlag echoes --link back, as readyFlag does --ready.
func linkFlag(link bool) string {
	if link {
		return " --link"
	}
	return ""
}

// readyFlag echoes the choice back in any command this preview suggests, so a
// copied command reproduces the run that was previewed.
func readyFlag(draft bool) string {
	if draft {
		return ""
	}
	return " --ready"
}

func writeSubmitPreview(w io.Writer, plan submit.Plan, p Presentation, template string, draft, link, comments bool) error {
	return writeStackView(w, submitView(plan, template, draft, link, comments), p)
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
