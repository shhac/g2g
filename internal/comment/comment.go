// Package comment keeps one comment on every pull request in a stack, listing
// the stack so a reviewer can move through it.
//
// GitHub shows a pull request in isolation. A reviewer landing on the third of
// five has no way to find the other four short of reading bases one at a time,
// and no way at all to find the ones that have already merged. The comment is
// that map, drawn from where the pull request it sits on stands.
//
// It is kept rather than posted: a later run edits the comment it finds, so the
// map follows the stack as branches land, move and are added.
package comment

import (
	"context"
	"fmt"
	"slices"
	"strconv"

	"github.com/shhac/g2g/internal/diagnostic"
	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/shape"
	"github.com/shhac/g2g/internal/stack"
)

// command is how this command names itself where a selector or a refusal asks.
const command = "g2g comment"

// historyRounds bounds how many times a run goes back to GitHub for pull
// requests its comments named. Every comment of a stack records the same
// numbers, so the ordinary run reads the stack and then its history, and a
// round beyond that follows a comment that knew of something the others did
// not.
const historyRounds = 6

// GitHub is what keeping the comment needs: the pull requests on the stack,
// their conversations, and the two writes.
type GitHub interface {
	Inspect(ctx context.Context, branches []string) ([]githubstack.PullRequest, error)
	Conversations(ctx context.Context, numbers []int, marker string) ([]githubstack.Conversation, error)
	AddComment(ctx context.Context, subject, body string) error
	UpdateComment(ctx context.Context, id, body string) error
}

// Service resolves a stack and keeps the comment on each of its pull requests.
type Service struct {
	Selector stack.PathSelector
	GitHub   GitHub
}

// Ready reports a service with everything it needs.
func (s Service) Ready() bool { return s.Selector != nil && s.GitHub != nil }

// Action is what a run does to one pull request's comment.
type Action string

const (
	// ActionCreate adds the comment to an open pull request that has none.
	ActionCreate Action = "create"
	// ActionUpdate replaces a comment that no longer says what the stack is.
	ActionUpdate Action = "update"
	// ActionCurrent is a comment that already says it.
	ActionCurrent Action = "current"
	// ActionSkip is a comment this run will not touch, and Reason says why.
	ActionSkip Action = "skip"
)

// Write is one pull request's comment and what happens to it.
type Write struct {
	Number int
	Branch string
	Action Action
	// Subject is the pull request's node id, where a new comment is added.
	// Comment is the node id of the one an update replaces.
	Subject string
	Comment string
	Body    string
	Reason  string
	// Historic marks a pull request that merged out of the stack. Its branch
	// may be gone, or reused by a branch the stack carries now.
	Historic bool
}

// Changes reports a write that would send something to GitHub.
func (w Write) Changes() bool { return w.Action == ActionCreate || w.Action == ActionUpdate }

// Plan is what keeping the comments would do.
type Plan struct {
	stack.Discovery
	// Requested is the branch the run was asked about, and RequestedSource
	// how it was named. Discovery's target is the bottom of its stack, because
	// the whole stack is what is kept, and it was always named by --branch.
	Requested       string
	RequestedSource string
	// Merged are pull requests that landed out of the stack and are still
	// listed, oldest first.
	Merged []int
	// Unread are pull requests the comments named that the run stopped short
	// of reading. They are neither listed nor dropped from GitHub; the next run
	// starts from what this one writes, so the preview says so.
	Unread []int
	// Writes are ordered as the stack reads, then the merged pull requests.
	Writes []Write
	// Ambiguous names branches with more than one open pull request.
	Ambiguous []string
	Blocked   string
}

// NothingToDo reports a plan with no write to send.
func (p Plan) NothingToDo() bool {
	if p.Blocked != "" {
		return false
	}
	for _, write := range p.Writes {
		if write.Changes() {
			return false
		}
	}
	return true
}

// Changing counts the writes that would reach GitHub, which is what sizes the
// time a run is given.
func (p Plan) Changing() int {
	changing := 0
	for _, write := range p.Writes {
		if write.Changes() {
			changing++
		}
	}
	return changing
}

// Forest is the planned stack's shape, rooted at its base.
func (p Plan) Forest() shape.Forest { return forestOf(p.Snapshot) }

// Equal compares everything that changes what the writes do.
func (p Plan) Equal(other Plan) bool {
	return p.Discovery.Equal(other.Discovery) &&
		p.Requested == other.Requested &&
		p.RequestedSource == other.RequestedSource &&
		p.Blocked == other.Blocked &&
		slices.Equal(p.Merged, other.Merged) &&
		slices.Equal(p.Unread, other.Unread) &&
		slices.Equal(p.Ambiguous, other.Ambiguous) &&
		slices.Equal(p.Writes, other.Writes)
}

// Plan works out what every comment on the stack should say, and which of them
// already do.
func (s Service) Plan(ctx context.Context, selection stack.Selection) (Plan, error) {
	if !s.Ready() {
		return Plan{}, fmt.Errorf("comment service is not fully configured")
	}
	whole, requested, err := s.wholeStack(ctx, selection)
	if err != nil {
		return Plan{}, err
	}
	discovery, err := stack.Discover(ctx, s.Selector, s.GitHub, whole, command)
	if err != nil {
		return Plan{}, err
	}
	plan := Plan{Discovery: discovery, Requested: requested.Target, RequestedSource: requested.TargetSource, Merged: []int{}, Unread: []int{}, Writes: []Write{}, Ambiguous: []string{}}
	if err := discovery.Snapshot.RequireActionable(command); err != nil {
		plan.Blocked = err.Error()
		return plan, nil
	}

	members := classify(discovery)
	for _, branch := range discovery.Branches {
		if members[branch].ambiguous {
			plan.Ambiguous = append(plan.Ambiguous, branch)
		}
	}
	if len(plan.Ambiguous) != 0 {
		plan.Blocked = "more than one open pull request for a branch, so which one the stack means cannot be derived"
		return plan, nil
	}

	forest := forestOf(discovery.Snapshot)
	stacks := make([]*kept, 0)
	for _, root := range forest.Children(discovery.Base) {
		stacks = append(stacks, newKept(forest, root, members))
	}
	read, err := s.readHistory(ctx, stacks)
	if err != nil {
		return Plan{}, err
	}
	for _, kept := range stacks {
		plan.Merged = append(plan.Merged, kept.merged()...)
		plan.Unread = append(plan.Unread, kept.unread...)
		plan.Writes = append(plan.Writes, kept.writes(forest, discovery.Base, members, read)...)
	}
	diagnostic.Event(ctx, "comment.plan",
		diagnostic.Field{Key: "stacks", Value: strconv.Itoa(len(stacks))},
		diagnostic.Field{Key: "writes", Value: strconv.Itoa(plan.Changing())},
		diagnostic.Field{Key: "merged", Value: strconv.Itoa(len(plan.Merged))},
	)
	return plan, nil
}

// wholeStack widens a selection to the stack its branch belongs to.
//
// Every comment lists its own pull request's ancestors and descendants, so a
// fork below it shows up in some comments and not others. Keeping only the
// part of a stack a scope reached would leave the rest describing a different
// stack, and running from a different branch would rewrite them again; taking
// the whole stack from its bottom is what makes every run from anywhere on it
// write the same comments. From a trunk, that is every stack on it.
func (s Service) wholeStack(ctx context.Context, selection stack.Selection) (stack.Selection, stack.Snapshot, error) {
	path := selection
	path.Scope = stack.ScopePath
	snapshot, err := s.Selector.Select(ctx, path, command)
	if err != nil {
		return stack.Selection{}, stack.Snapshot{}, err
	}
	whole := stack.Selection{Branch: snapshot.Target, Trunk: selection.Trunk, Scope: stack.ScopeStack, From: selection.From}
	if len(snapshot.Branches) != 0 {
		whole.Branch = snapshot.Branches[0]
	}
	return whole, snapshot, nil
}

// Revalidate re-reads the world and refuses if anything moved since preview.
func (s Service) Revalidate(ctx context.Context, selection stack.Selection, preview Plan) (Plan, error) {
	plan, err := s.Plan(ctx, selection)
	if err != nil {
		return Plan{}, err
	}
	return plan, diagnostic.Revalidated(ctx, "comment", "comment plan", plan.Equal(preview))
}

// Execute sends each write in order and stops at the first that fails.
//
// Nothing is unwound. A comment already written is correct, and rerunning
// edits the rest rather than adding to them.
func (s Service) Execute(ctx context.Context, plan Plan) error {
	if plan.Blocked != "" {
		return fmt.Errorf("cannot keep the stack comments: %s", plan.Blocked)
	}
	for _, write := range plan.Writes {
		var err error
		switch write.Action {
		case ActionCreate:
			err = s.GitHub.AddComment(ctx, write.Subject, write.Body)
		case ActionUpdate:
			err = s.GitHub.UpdateComment(ctx, write.Comment, write.Body)
		default:
			continue
		}
		if err != nil {
			return fmt.Errorf("#%d: %w", write.Number, err)
		}
	}
	return nil
}

// Member states, as a comment names them.
const (
	stateOpen    = "open"
	stateMerged  = "merged"
	stateClosed  = "closed"
	stateMissing = "missing"
)

// member is one branch's pull request, once the open-is-identity rule has said
// which one that is.
type member struct {
	number    int
	state     string
	ambiguous bool
}

// listed reports a member the stack's comments name by number. A closed pull
// request is not: nothing will merge it, and its branch is waiting for one.
func (m member) listed() bool {
	return m.number != 0 && (m.state == stateOpen || m.state == stateMerged)
}

func classify(discovery stack.Discovery) map[string]member {
	members := make(map[string]member, len(discovery.Branches))
	for step := range githubstack.Across(discovery.Parents, discovery.Branches, discovery.PullRequests) {
		switch step.Classify() {
		case githubstack.StepAmbiguous:
			members[step.Branch] = member{ambiguous: true}
		case githubstack.StepAligned, githubstack.StepBaseMismatch:
			members[step.Branch] = member{number: step.Resolution.Open.Number, state: stateOpen}
		case githubstack.StepSuperseded:
			state := stateClosed
			if step.Merged() {
				state = stateMerged
			}
			members[step.Branch] = member{number: step.Resolution.Latest.Number, state: state}
		default:
			members[step.Branch] = member{state: stateMissing}
		}
	}
	return members
}

// forestOf is the selection's shape with its base as the root. Every selector
// fills Parents, and a branch with no selected parent hangs from the base.
func forestOf(snapshot stack.Snapshot) shape.Forest {
	parents := make(map[string]string, len(snapshot.Branches)+1)
	parents[snapshot.Base] = ""
	for _, branch := range snapshot.Branches {
		parent, ok := snapshot.ParentOf(branch)
		if !ok {
			parent = snapshot.Base
		}
		parents[branch] = parent
	}
	return shape.Forest{Parents: parents}
}

// writes decides each comment on this stack: its own pull requests in the
// order the stack reads, then those that merged out of it.
func (k *kept) writes(forest shape.Forest, base string, members map[string]member, read map[int]githubstack.Conversation) []Write {
	recorded := k.recorded()
	writes := make([]Write, 0, len(k.own)+len(k.history))
	for _, branch := range k.branches {
		m := members[branch]
		if !m.listed() {
			continue
		}
		v := view{Trunk: base, Merged: k.merged(), Lines: k.linesFrom(forest, branch, members), Here: m.number, Recorded: recorded}
		if write, ok := decide(read[m.number], branch, v.body(), m.state == stateOpen && len(recorded) > 1); ok {
			writes = append(writes, write)
		}
	}
	for _, number := range k.merged() {
		v := view{Trunk: base, Merged: k.merged(), Lines: k.whole(forest, base, members), Here: number, Recorded: recorded}
		// A merged pull request is never given a comment it did not have.
		// Nobody is reviewing it, and a new comment notifies everyone who did.
		if write, ok := decide(read[number], read[number].Head, v.body(), false); ok {
			write.Historic = true
			writes = append(writes, write)
		}
	}
	return writes
}

// decide is what happens to one pull request's comment.
func decide(conversation githubstack.Conversation, branch, body string, create bool) (Write, bool) {
	number := conversation.Number
	write := Write{Number: number, Branch: branch, Subject: conversation.ID, Body: body}
	switch comments := conversation.Comments; {
	case len(comments) == 0:
		if !create || conversation.ID == "" {
			return Write{}, false
		}
		if !conversation.Commentable {
			write.Action = ActionSkip
			write.Reason = fmt.Sprintf("#%d does not take new comments from you · its conversation may be locked", number)
			return write, true
		}
		write.Action = ActionCreate
	case len(comments) > 1:
		write.Action = ActionSkip
		write.Reason = fmt.Sprintf("%d stack comments on #%d · delete all but one, then rerun", len(comments), number)
	case !comments[0].Editable:
		write.Action = ActionSkip
		write.Reason = fmt.Sprintf("the stack comment on #%d was written by %s and you cannot edit it", number, author(comments[0]))
	case same(comments[0].Body, body):
		write.Action = ActionCurrent
		write.Comment = comments[0].ID
	default:
		write.Action = ActionUpdate
		write.Comment = comments[0].ID
	}
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
func (k *kept) linesFrom(forest shape.Forest, branch string, members map[string]member) []line {
	path, err := forest.Path(branch)
	if err != nil {
		path = []string{branch}
	}
	lines := make([]line, 0, len(path))
	for index, onPath := range path {
		lines = append(lines, k.line(onPath, index == 0, 0, members))
	}
	above := forest.Subtree(branch)
	depths := shape.Depths(above, forest.Parent)
	for _, descendant := range above[1:] {
		lines = append(lines, k.line(descendant, false, depths[descendant], members))
	}
	return lines
}

// whole is the stack as a merged pull request sees it: everything still in it,
// because everything still in it was built on what merged.
func (k *kept) whole(forest shape.Forest, base string, members map[string]member) []line {
	ordered := append([]string{base}, k.branches...)
	depths := shape.Depths(ordered, forest.Parent)
	lines := make([]line, 0, len(ordered))
	for index, branch := range ordered {
		lines = append(lines, k.line(branch, index == 0, depths[branch], members))
	}
	return lines
}

func (k *kept) line(branch string, trunk bool, depth int, members map[string]member) line {
	if trunk {
		return line{Branch: branch, Trunk: true, Depth: depth}
	}
	m := members[branch]
	entry := line{Branch: branch, State: m.state, Depth: depth}
	if m.listed() {
		entry.Number = m.number
	}
	return entry
}
