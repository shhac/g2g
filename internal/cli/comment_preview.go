package cli

import (
	"fmt"
	"slices"
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
	view := stackView{Operation: "github comment", Target: plan.Requested, TargetSource: plan.RequestedSource}
	if plan.Blocked != "" {
		view = view.refusing(plan.Blocked, plan.Repair)
	}
	view.Nodes = commentNodes(plan)
	if len(plan.Merged) != 0 {
		view = view.note("Merged out of the stack and still listed: "+pullRequestList(plan.Merged), severityNeutral)
	}
	if len(plan.Unread) != 0 {
		view = view.note("Named by a comment and not read yet, so not listed this time: "+pullRequestList(plan.Unread), severityWarn)
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

// commentNodes is the stack, each branch with its pull request and what
// happens to that pull request's comment. The number is the one the plan
// decided the branch means; a merged pull request's write is looked up by its
// number rather than its branch, because its branch may be gone or reused.
func commentNodes(plan comment.Plan) []stackNode {
	writes := map[int]comment.Write{}
	for _, write := range plan.Writes {
		if !write.Historic {
			writes[write.Number] = write
		}
	}
	forest := plan.Forest()
	depths := shape.Depths(append([]string{plan.Base}, plan.Branches...), forest.Parent)
	nodes := []stackNode{{Branch: plan.Base, Trunk: true}}
	for _, branch := range plan.Branches {
		parent, _ := forest.Parent(branch)
		node := stackNode{Branch: branch, Target: branch == plan.Requested, Parent: parent, Depth: depths[branch], PRNumber: plan.Members[branch].Number}
		write, written := writes[node.PRNumber]
		switch {
		case written && node.PRNumber != 0:
			node = node.marked(commentMark(write))
		case node.PRNumber != 0:
			node = node.marked(stackMark{Detail: "no comment kept", Severity: severityNeutral})
		case plan.Members[branch].State == comment.StateAmbiguous:
			node = node.marked(stackMark{Detail: "more than one open pull request", Severity: severityBad})
		default:
			node = node.marked(stackMark{Detail: "no pull request", Severity: severityNeutral})
		}
		nodes = append(nodes, node)
	}
	return nodes
}

// pullRequestList names pull requests the way every other list here is read.
func pullRequestList(numbers []int) string {
	named := make([]string, 0, len(numbers))
	for _, number := range numbers {
		named = append(named, fmt.Sprintf("#%d", number))
	}
	return branchList(named)
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
	chosen := slices.IndexFunc(plan.Writes, func(write comment.Write) bool {
		return write.Branch == plan.Requested && !write.Historic
	})
	if chosen < 0 {
		chosen = slices.IndexFunc(plan.Writes, comment.Write.Changes)
	}
	if chosen < 0 {
		return nil
	}
	write := plan.Writes[chosen]
	return &stackExcerpt{
		Heading: fmt.Sprintf("The comment on #%d", write.Number),
		Lines:   strings.Split(strings.TrimRight(write.Body, "\n"), "\n"),
	}
}
