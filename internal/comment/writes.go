package comment

import (
	"fmt"

	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/shape"
)

// writes decides each comment on this stack: its own pull requests in the
// order the stack reads, then those that merged out of it.
func (k *kept) writes(forest shape.Forest, base string, members map[string]Member, read map[int]githubstack.Conversation) []Write {
	recorded, merged := k.recorded(), k.merged()
	// One pull request is not a stack, and a comment saying so is noise. One
	// already there is kept up to date rather than left saying something false.
	worthAdding := len(recorded) > 1
	writes := make([]Write, 0, len(k.own)+len(merged))
	for _, branch := range k.branches {
		m := members[branch]
		if !m.listed() {
			continue
		}
		v := view{Trunk: base, Merged: merged, Lines: linesFrom(forest, branch, members), Here: m.Number, Recorded: recorded}
		if write, ok := decide(read[m.Number], branch, v.body(), m.State == StateOpen && worthAdding); ok {
			writes = append(writes, write)
		}
	}
	for _, number := range merged {
		write, ok := k.decideMerged(read[number], forest, base, members, merged, recorded)
		if ok {
			writes = append(writes, write)
		}
	}
	return writes
}

// decideMerged is what happens to the comment on a pull request that merged
// out of the stack.
//
// It is never given a comment it did not have: nobody is reviewing it, and a
// new comment notifies everyone who did. And one that fed more than one stack
// — the branch a fork grew from, merged — is left as it is: drawn from either
// stack alone it would say the other does not exist, and a run from each would
// undo the other's.
func (k *kept) decideMerged(conversation githubstack.Conversation, forest shape.Forest, base string, members map[string]Member, merged []int, recorded []entry) (Write, bool) {
	v := view{Trunk: base, Merged: merged, Lines: whole(forest, base, k.branches, members), Here: conversation.Number, Recorded: recorded}
	write, ok := decide(conversation, conversation.Head, v.body(), false)
	if !ok {
		return Write{}, false
	}
	write.Historic = true
	if write.Changes() && k.shared(conversation.Number) {
		write.Action = ActionSkip
		write.Reason = fmt.Sprintf("#%d merged below more than one stack · its comment is left as it is", conversation.Number)
	}
	return write, true
}

// decide is what happens to one pull request's comment.
func decide(conversation githubstack.Conversation, branch, body string, create bool) (Write, bool) {
	number := conversation.Number
	write := Write{Number: number, Branch: branch, Subject: conversation.ID, Body: body}
	if len(conversation.Comments) == 0 {
		return decideNew(conversation, write, create)
	}
	existing := conversation.Comments[0]
	switch {
	case len(conversation.Comments) > 1:
		write.Action = ActionSkip
		write.Reason = fmt.Sprintf("%d stack comments on #%d · delete all but one, then rerun", len(conversation.Comments), number)
	case !existing.Editable:
		write.Action = ActionSkip
		write.Reason = fmt.Sprintf("the stack comment on #%d was written by %s and you cannot edit it", number, author(existing))
	case same(existing.Body, body):
		write.Action, write.Comment = ActionCurrent, existing.ID
	default:
		write.Action, write.Comment = ActionUpdate, existing.ID
	}
	return write, true
}

// decideNew is a pull request with no comment yet.
func decideNew(conversation githubstack.Conversation, write Write, create bool) (Write, bool) {
	if !create || conversation.ID == "" {
		return Write{}, false
	}
	if !conversation.Commentable {
		write.Action = ActionSkip
		write.Reason = fmt.Sprintf("#%d does not take new comments from you · its conversation may be locked", conversation.Number)
		return write, true
	}
	write.Action = ActionCreate
	return write, true
}

func author(found githubstack.Comment) string {
	if found.Author == "" {
		return "someone else"
	}
	return "@" + found.Author
}

// linesFrom is the stack as one branch sees it: the line down to the trunk and
// everything built on top of it. A cousin that merely shares an ancestor is
// another branch's business, and listing it would draw a fork the reader of
// this pull request is not part of.
//
// The line down is a chain, so it stays flat; only what forks above the branch
// is nested, which is the one place a reader needs the shape.
func linesFrom(forest shape.Forest, branch string, members map[string]Member) []line {
	path, err := forest.Path(branch)
	if err != nil {
		path = []string{branch}
	}
	lines := make([]line, 0, len(path))
	for index, onPath := range path {
		lines = append(lines, lineFor(onPath, index == 0, 0, members))
	}
	above := forest.Subtree(branch)
	depths := shape.Depths(above, forest.Parent)
	for _, descendant := range above[1:] {
		lines = append(lines, lineFor(descendant, false, depths[descendant], members))
	}
	return lines
}

// whole is the stack as a merged pull request sees it: everything still in it,
// because everything still in it was built on what merged.
func whole(forest shape.Forest, base string, branches []string, members map[string]Member) []line {
	ordered := append([]string{base}, branches...)
	depths := shape.Depths(ordered, forest.Parent)
	lines := make([]line, 0, len(ordered))
	for index, branch := range ordered {
		lines = append(lines, lineFor(branch, index == 0, depths[branch], members))
	}
	return lines
}

func lineFor(branch string, trunk bool, depth int, members map[string]Member) line {
	if trunk {
		return line{Branch: branch, Trunk: true, Depth: depth}
	}
	m := members[branch]
	return line{Branch: branch, State: m.State, Depth: depth, Number: m.listedNumber()}
}
