package cli

import (
	"fmt"
	"strings"

	"github.com/shhac/g2g/internal/comment"
	"github.com/shhac/g2g/internal/shape"
)

// commentView draws the stack with what happens to each pull request's
// comment, and the comment the requested branch's pull request would carry.
//
// One body is shown rather than all of them. They differ only in which line is
// bold and what is nested, and a person deciding whether to post them needs to
// see what one looks like, not read eight near-copies; --json carries each.
func commentView(plan comment.Plan) stackView {
	view := stackView{Operation: "comment", Target: plan.Requested, TargetSource: plan.RequestedSource}
	if plan.Blocked != "" {
		view = view.blockedBy(plan.Blocked)
	}
	writes := map[string]comment.Write{}
	for _, write := range plan.Writes {
		if !write.Historic {
			writes[write.Branch] = write
		}
	}
	numbers := map[string]int{}
	for _, pr := range plan.PullRequests {
		if pr.Number > numbers[pr.Head] {
			numbers[pr.Head] = pr.Number
		}
	}

	forest := plan.Forest()
	ordered := append([]string{plan.Base}, plan.Branches...)
	depths := shape.Depths(ordered, forest.Parent)
	view.Nodes = []stackNode{{Branch: plan.Base, Trunk: true}}
	for _, branch := range plan.Branches {
		parent, _ := forest.Parent(branch)
		node := stackNode{Branch: branch, Target: branch == plan.Requested, Parent: parent, Depth: depths[branch]}
		write, written := writes[branch]
		switch {
		case written:
			node.PRNumber = write.Number
			node = node.marked(commentMark(write))
		case numbers[branch] != 0:
			node.PRNumber = numbers[branch]
			node = node.marked(stackMark{Detail: "no comment kept", Severity: severityNeutral})
		default:
			node = node.marked(stackMark{Detail: "no pull request", Severity: severityNeutral})
		}
		view.Nodes = append(view.Nodes, node)
	}

	if len(plan.Merged) != 0 {
		merged := make([]string, 0, len(plan.Merged))
		for _, number := range plan.Merged {
			merged = append(merged, fmt.Sprintf("#%d", number))
		}
		view = view.note("Merged out of the stack and still listed: "+strings.Join(merged, ", "), severityNeutral)
	}
	if len(plan.Unread) != 0 {
		unread := make([]string, 0, len(plan.Unread))
		for _, number := range plan.Unread {
			unread = append(unread, fmt.Sprintf("#%d", number))
		}
		view = view.note("Named by a comment and not read, so not listed: "+strings.Join(unread, ", "), severityWarn)
	}
	for _, write := range plan.Writes {
		view.Comments = append(view.Comments, stackComment{PullRequest: write.Number, Branch: write.Branch, Action: string(write.Action), Reason: write.Reason, Body: write.Body})
		switch {
		case write.Action == comment.ActionSkip:
			view = view.note(write.Reason, severityBad)
		case write.Historic && write.Changes():
			view = view.note(fmt.Sprintf("#%d · merged · %s", write.Number, commentMark(write).text()), severityWarn)
		}
	}
	view.Excerpt = commentExcerpt(plan)
	return view
}

// commentMark is what happens to one comment, said as the one axis it is.
func commentMark(write comment.Write) stackMark {
	switch write.Action {
	case comment.ActionCurrent:
		return stackMark{Subject: "comment", OK: true, Severity: severityOK}
	case comment.ActionCreate:
		return stackMark{Subject: "comment", Detail: "none yet · would add", Severity: severityWarn}
	case comment.ActionUpdate:
		return stackMark{Subject: "comment", Detail: "out of date · would edit", Severity: severityWarn}
	default:
		return stackMark{Subject: "comment", Detail: "left alone", Severity: severityBad}
	}
}

// commentExcerpt is the body the requested branch's pull request would carry,
// or the first one that would change when that branch has none.
func commentExcerpt(plan comment.Plan) *stackExcerpt {
	var chosen *comment.Write
	for index := range plan.Writes {
		write := &plan.Writes[index]
		if write.Branch == plan.Requested && !write.Historic {
			chosen = write
			break
		}
		if chosen == nil && write.Changes() {
			chosen = write
		}
	}
	if chosen == nil {
		return nil
	}
	return &stackExcerpt{
		Heading: fmt.Sprintf("The comment on #%d", chosen.Number),
		Lines:   strings.Split(strings.TrimRight(chosen.Body, "\n"), "\n"),
	}
}
