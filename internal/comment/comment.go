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
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/shhac/g2g/internal/diagnostic"
	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/repair"
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
	// Members is each branch's pull request, as the open-is-identity rule
	// decided it. A renderer reads the number from here rather than working
	// it out again, which is how a preview came to name a pull request the
	// comment was not written for.
	Members map[string]Member
	// Ambiguous names branches with more than one open pull request.
	Ambiguous []string
	// Blocked is why an apply would refuse, and Repair the same in parts.
	Blocked string
	Repair  repair.Note
}

// NothingToDo reports a plan with no write to send.
func (p Plan) NothingToDo() bool { return p.Blocked == "" && p.Changing() == 0 }

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
		slices.Equal(p.Writes, other.Writes) &&
		maps.Equal(p.Members, other.Members)
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
	members := classify(discovery)
	plan := Plan{Discovery: discovery, Requested: requested.Target, RequestedSource: requested.TargetSource, Merged: []int{}, Unread: []int{}, Writes: []Write{}, Members: members, Ambiguous: ambiguous(discovery.Branches, members)}
	if err := discovery.Snapshot.RequireActionable(command); err != nil {
		plan.Blocked = err.Error()
		return plan, nil
	}
	if len(plan.Ambiguous) != 0 {
		// No command, deliberately: which pull request a branch means is a
		// person's choice, and the way out says so rather than naming one.
		plan.Repair = repair.Note{
			Reason: "more than one open pull request for " + strings.Join(plan.Ambiguous, ", ") + ", so which one the stack means cannot be derived",
			Ways:   []repair.Step{{Effect: "close all but one open pull request for each, then rerun"}},
		}
		plan.Blocked = plan.Repair.Sentence()
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
	written := map[int]bool{}
	for _, kept := range stacks {
		plan.Merged = append(plan.Merged, kept.merged()...)
		plan.Unread = append(plan.Unread, kept.unread()...)
		for _, write := range kept.writes(forest, discovery.Base, members, read) {
			if write.Historic && written[write.Number] {
				continue
			}
			written[write.Number] = true
			plan.Writes = append(plan.Writes, write)
		}
	}
	// From a trunk, two stacks can both record a pull request whose fork point
	// merged; it is one pull request, listed once.
	slices.Sort(plan.Merged)
	plan.Merged = slices.Compact(plan.Merged)
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
	path.Scope = shape.ScopePath
	snapshot, err := s.Selector.Select(ctx, path, command)
	if err != nil {
		return stack.Selection{}, stack.Snapshot{}, err
	}
	whole := stack.Selection{Branch: snapshot.Target, Trunk: selection.Trunk, Scope: shape.ScopeStack, From: selection.From}
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

// Stopped is a run that wrote some comments and then failed on one.
//
// Those comments are written and correct, so reporting the run as not applied
// would be wrong about the part that worked; rerunning edits the rest rather
// than adding to them.
type Stopped struct {
	// Written are the pull requests whose comment was sent, in order.
	Written []int
	// Failed is the pull request the run stopped on.
	Failed int
	Err    error
}

func (s *Stopped) Error() string {
	return fmt.Sprintf("stopped at #%d after writing %d: %v", s.Failed, len(s.Written), s.Err)
}

func (s *Stopped) Unwrap() error { return s.Err }

// Execute sends each write in order and stops at the first that fails.
//
// Nothing is unwound. A failure before anything was sent is an ordinary error;
// one after is a Stopped, because some of the run happened.
func (s Service) Execute(ctx context.Context, plan Plan) error {
	if plan.Blocked != "" {
		return fmt.Errorf("cannot keep the stack comments: %s", plan.Blocked)
	}
	written := make([]int, 0, plan.Changing())
	for _, write := range plan.Writes {
		if !write.Changes() {
			continue
		}
		if err := s.send(ctx, write); err != nil {
			if len(written) == 0 {
				return fmt.Errorf("#%d: %w", write.Number, err)
			}
			return &Stopped{Written: written, Failed: write.Number, Err: err}
		}
		written = append(written, write.Number)
	}
	return nil
}

func (s Service) send(ctx context.Context, write Write) error {
	if write.Action == ActionCreate {
		return s.GitHub.AddComment(ctx, write.Subject, write.Body)
	}
	return s.GitHub.UpdateComment(ctx, write.Comment, write.Body)
}
