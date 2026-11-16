package cli

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/landed"
	"github.com/shhac/g2g/internal/submit"
)

// What submitting a stack would do, drawn.
//
// Wiring in _cmd.go, projection in _preview.go -- the convention every other
// command in this package follows, and which submit was already close to with
// submit_spec.go and submit_templates.go beside it.

func submitView(plan submit.Plan, template string, draft, link, comments bool) stackView {
	selected := plan.Selected
	view := stackView{
		Operation:    "submit",
		Target:       selected.Target,
		TargetSource: selected.TargetSource,
		Nodes:        treeNodes(selected.Base, selected.Target, selected.Branches, selected.ParentOf),
	}
	resolutions := githubstack.ResolveHeads(plan.Existing)
	for index, node := range view.Nodes[1:] {
		view.Nodes[index+1] = submitLabel(plan, resolutions, node, draft)
	}
	for _, branch := range plan.Selected.Branches {
		absorption, absorbed := plan.Absorbed[branch]
		if !absorbed {
			continue
		}
		for _, way := range absorptionAdvice(plan.Selected, plan.Remote, branch, absorption.Local != landed.Present) {
			text := way.Effect
			if way.Command != "" {
				text = runnable(way.Command) + " · " + text
			}
			view = view.note(text+".", severityNeutral)
		}

	}

	if template != "" {
		view = view.note("PR template: "+template, severityNeutral)
	}
	if note := plan.Refusal(link); note.Sentence() != "" {
		return view.refusing(note)
	}
	view = view.note("Publishes the selected branches through one atomic, lease-protected push; existing PR bases are preserved.", severityNeutral)
	view = offStackNote(view, plan)
	if _, existingOnly := plan.ExistingSpec(); !existingOnly {
		view = view.note(fmt.Sprintf("Missing PRs will be created %s; existing PRs are preserved.", openAsPlural(draft)), severityNeutral)
	}
	if link && len(plan.Publication().Branches) > 1 {
		view = view.note("Then links them as a GitHub stack · g2g land refuses a linked stack until it is unlinked.", severityNeutral)
	}
	switch {
	case comments && len(plan.Publication().Branches) == 1:
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

// submitLabel says what submitting does to one branch. Which pull request
// represents it goes through the shared resolution rather than a scan for the
// first open head, so the preview cannot disagree with the plan.
func submitLabel(plan submit.Plan, resolutions map[string]githubstack.Resolution, node stackNode, draft bool) stackNode {
	branch := node.Branch
	if _, absorbed := plan.Absorbed[branch]; absorbed {
		previous := plan.Superseded[branch]
		node.PRNumber = previous.Number
		return node.labeled("left out · merged into "+previous.Base+"; absorbed", severityNeutral)
	}
	if slices.Contains(plan.Waiting, branch) {
		return node.labeled("left out · no commits of its own yet", severityNeutral)
	}
	previous, replaced := plan.Superseded[branch]
	switch {
	case plan.Issues[branch] != "":
		return node.labeled("blocked: "+plan.Issues[branch], severityBad)
	case resolutions[branch].Open != nil:
		node.PRNumber = resolutions[branch].Open.Number
		return node.labeled("existing", severityOK)
	case replaced:
		assessment := landed.Unknown
		if present, assessed := plan.MergedLanded[branch]; assessed {
			assessment = landed.Absent
			if present {
				assessment = landed.Present
			}
		}
		history := supersededHistory(previous, plan.Snapshot.Trunk(), assessment)
		return node.labeled(fmt.Sprintf("create %s · #%d %s", openAs(draft), previous.Number, history), "")
	}
	return node.labeled("create "+openAs(draft), "")
}

// supersededHistory qualifies lifecycle history with local landing evidence.
func supersededHistory(previous githubstack.PullRequest, trunk string, assessment landed.Evidence) string {
	if previous.State != "MERGED" {
		return strings.ToLower(previous.State)
	}
	if assessment == landed.Present {
		return "merged · landed in " + trunk
	}
	if trunk != "" && previous.Base == trunk {
		return "merged into " + trunk + "; refresh " + trunk + " and reassess"
	}
	if assessment == landed.Absent {
		return "merged into " + previous.Base + ", not landed"
	}
	return "merged into " + previous.Base + ", landing unconfirmed"
}

// offStackNote names each pull request based on a branch the stack no longer
// has. Preserving an existing base is the right default -- a base somebody
// chose stays chosen -- and the wrong silence after the branch below was
// deleted: the pull request stayed there until somebody thought to run
// retarget. The retarget is aimed the way retarget's own retry is.
func offStackNote(view stackView, plan submit.Plan) stackView {
	misbased := plan.OffStack()
	if len(misbased) == 0 {
		return view
	}
	said := make([]string, 0, len(misbased))
	for _, step := range misbased {
		said = append(said, fmt.Sprintf("#%d (%s) is based on %s, where the stack records %s", step.Resolution.Open.Number, step.Branch, step.Resolution.Open.Base, step.ExpectedBase))
	}
	retarget := selectedFrom(plan.Snapshot).aimedOr(retargetCommand)
	return view.note(strings.Join(said, "; ")+" · submit leaves it, and "+runnable(retarget+" --apply")+" moves it.", severityWarn)
}
