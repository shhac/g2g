package comment

import (
	"cmp"
	"fmt"
	"slices"

	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/shape"
)

// writes decides each comment on this stack: its own pull requests in the
// order the stack reads, then those that merged out of it.
func (k *kept) writes(forest shape.Forest, base line, members map[string]Member, read map[int]githubstack.Conversation, version string) []Write {
	recorded, merged := k.recorded(), k.merged()
	history := historyLines(merged, read)
	// One already there is kept up to date rather than left saying something
	// false.
	worthAdding := !k.single()
	writes := make([]Write, 0, len(k.own)+len(merged))
	for _, branch := range k.branches {
		m := members[branch]
		if !m.listed() {
			continue
		}
		v := view{Lines: withHistory(linesFrom(forest, base, branch, members), history), Here: m.Number, Recorded: recorded, Version: version}
		if write, ok := decide(read[m.Number], branch, v.body(), m.State == StateOpen && worthAdding); ok {
			writes = append(writes, write)
		}
	}
	for _, number := range merged {
		write, ok := k.decideMerged(read[number], forest, base, members, history, recorded, version)
		if ok {
			writes = append(writes, write)
		}
	}
	return writes
}

// single reports a stack that lists one pull request at most. One pull request
// is not a stack, and a new comment saying so is noise.
func (k *kept) single() bool { return len(k.recorded()) < 2 }

// alone are the open pull requests that get no comment because their stack
// lists nothing else, which a preview says rather than leaving a reader to
// wonder whether a write failed.
func (k *kept) alone(members map[string]Member, writes []Write) []int {
	if !k.single() {
		return nil
	}
	alone := make([]int, 0, 1)
	for _, branch := range k.branches {
		m := members[branch]
		if m.State == StateOpen && !slices.ContainsFunc(writes, func(w Write) bool { return w.Number == m.Number }) {
			alone = append(alone, m.Number)
		}
	}
	return alone
}

// decideMerged is what happens to the comment on a pull request that merged
// out of the stack.
//
// It is never given a comment it did not have: nobody is reviewing it, and a
// new comment notifies everyone who did. And one that fed more than one stack
// — the branch a fork grew from, merged — is left as it is: drawn from either
// stack alone it would say the other does not exist, and a run from each would
// undo the other's.
func (k *kept) decideMerged(conversation githubstack.Conversation, forest shape.Forest, base line, members map[string]Member, history []line, recorded []entry, version string) (Write, bool) {
	v := view{Lines: withHistory(whole(forest, base, k.branches, members), history), Here: conversation.Number, Recorded: recorded, Version: version}
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
	// A comment someone else wrote does not stand in the way of one you can
	// keep: anyone who can comment can open one with the marker, and letting
	// it count would let them stop the map being kept at all.
	comments := conversation.Comments
	if own := editable(comments); len(own) != 0 {
		comments = own
	}
	existing := comments[0]
	switch {
	case len(comments) > 1:
		write.Action = ActionSkip
		write.Reason = fmt.Sprintf("%d stack comments on #%d · delete all but one, then rerun", len(comments), number)
	case !existing.Editable:
		write.Action = ActionSkip
		write.Reason = fmt.Sprintf("the stack comment on #%d was written by %s and you cannot edit it", number, author(existing))
	case !readable(existing.Body):
		write.Action = ActionSkip
		write.Reason = fmt.Sprintf("the stack comment on #%d was written by %s · upgrade g2g to keep it", number, writer(existing.Body))
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
		write.Reason = fmt.Sprintf("#%d does not take new comments from you · its conversation is locked", conversation.Number)
		return write, true
	}
	write.Action = ActionCreate
	return write, true
}

// writer names the g2g a comment says wrote it, as far as it can be trusted to
// say: anyone who can edit the comment can change the field.
func writer(body string) string {
	version := markerFields(body)["version"]
	if !versionShape.MatchString(version) {
		return "a newer g2g"
	}
	return "g2g@" + version + ", a newer g2g"
}

func author(found githubstack.Comment) string {
	if found.Author == "" {
		return "someone else"
	}
	return "@" + found.Author
}

// linesFrom is the stack as the pull request on branch sees it.
//
// The path from the base to it is one flat column, since that is what it is
// built on. Whatever else grew from a branch on that path hangs one level
// under it, as a single line saying how much more sits above: a reviewer
// learns it is there, and where it splits off, without reading work that is
// not on their way. Everything above this pull request is drawn in full,
// nested only where it forks, because all of it is built on this one.
func linesFrom(forest shape.Forest, base line, branch string, members map[string]Member) []line {
	path, err := forest.Path(branch)
	if err != nil {
		path = []string{base.Branch, branch}
	}
	lines := []line{base}
	for index, onPath := range path[1 : len(path)-1] {
		lines = append(lines, lineFor(onPath, 0, members))
		next := path[index+2]
		for _, child := range forest.Children(onPath) {
			if child == next {
				continue
			}
			beside := lineFor(child, 1, members)
			beside.Above = len(forest.Subtree(child)) - 1
			lines = append(lines, beside)
		}
	}
	above := forest.Subtree(branch)
	depths := shape.Depths(above, forest.Parent)
	lines = append(lines, lineFor(branch, 0, members))
	for _, descendant := range above[1:] {
		lines = append(lines, lineFor(descendant, depths[descendant], members))
	}
	return lines
}

// whole is the stack as a merged pull request sees it: everything still in it,
// because everything still in it was built on what merged.
func whole(forest shape.Forest, base line, branches []string, members map[string]Member) []line {
	depths := shape.Depths(append([]string{base.Branch}, branches...), forest.Parent)
	lines := []line{base}
	for _, branch := range branches {
		lines = append(lines, lineFor(branch, depths[branch], members))
	}
	return lines
}

// historyLines are the pull requests that merged out of the stack, in the order
// they merged.
//
// Where each one sat is not recorded in a way that survives: once the one below
// lands, the one above is put on the trunk and recorded there. A stack comes
// down from the bottom, so the order things merged in is the order they sat in.
func historyLines(merged []int, read map[int]githubstack.Conversation) []line {
	lines := make([]line, 0, len(merged))
	for _, number := range merged {
		lines = append(lines, line{Branch: read[number].Head, Number: number, State: StateMerged})
	}
	slices.SortStableFunc(lines, func(left, right line) int {
		return cmp.Or(read[left.Number].MergedAt.Compare(read[right.Number].MergedAt), left.Number-right.Number)
	})
	return lines
}

// withHistory puts what merged between the trunk and what is still open,
// which is where it sat: the list keeps the stack's shape rather than moving
// landed work to a line of its own.
func withHistory(lines, history []line) []line {
	if len(history) == 0 || len(lines) == 0 {
		return lines
	}
	return slices.Concat(lines[:1], history, lines[1:])
}

func lineFor(branch string, depth int, members map[string]Member) line {
	m := members[branch]
	return line{Branch: branch, State: m.State, Depth: depth, Number: m.listedNumber()}
}
