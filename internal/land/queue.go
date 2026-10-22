package land

import (
	"context"
	"errors"
	"fmt"
	"strings"

	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/repair"
)

// Pending reports a merge request still owned by GitHub. It is not a merge,
// including when this invocation merely observed a request somebody made earlier.
type queueRefusal struct{ err error }

func (e *queueRefusal) Error() string { return e.err.Error() }
func (e *queueRefusal) Unwrap() error { return e.err }

type Pending struct {
	Branch string
	Number int
	State  string
	Retry  string
	Cause  error
}

func (p *Pending) Error() string {
	text := fmt.Sprintf("%s (#%d) is %s; no merge is confirmed", p.Branch, p.Number, p.State)
	if p.Cause != nil {
		text += ": " + p.Cause.Error()
	}
	return text
}
func (p *Pending) Unwrap() error { return p.Cause }

func pendingKind(state githubstack.MergeState) string {
	if state.QueueEntry != nil {
		return "queued"
	}
	if state.AutoMerge {
		return "waiting"
	}
	return ""
}

func pendingState(state githubstack.MergeState) string {
	if state.QueueEntry != nil {
		return "queued (" + strings.ToLower(state.QueueEntry.State) + ")"
	}
	if state.AutoMerge {
		return "waiting for merge requirements"
	}
	return "merge requested"
}
func queuePolicy(q *githubstack.Queue) string {
	if q == nil {
		return ""
	}
	return fmt.Sprintf("%s:%s:auto=%t", q.ID, q.Method(), q.AutoMergeAllowed)
}

// RetryCommand preserves the invocation's selection and important choices.
func (p Plan) RetryCommand() string {
	args := []string{"g2g", "land", "--branch", p.Target, "--scope", string(p.Scope), "--from", string(p.Source)}
	if p.Trunk != "" {
		args = append(args, "--trunk", p.Trunk)
	}
	if p.Options.Remote != Defaults().Remote {
		args = append(args, "--remote", p.Options.Remote)
	}
	if p.Options.Wait {
		args = append(args, "--wait")
	}
	if !p.Options.DeleteLocal {
		args = append(args, "--no-delete-local")
	}
	if !p.Options.DeleteRemote {
		args = append(args, "--no-delete-remote")
	}
	if !p.Options.Comment {
		args = append(args, "--no-comment")
	}
	if p.Options.Upstream == localgit.LeaveUpstream {
		args = append(args, "--no-set-upstream")
	}
	if p.Options.Strict {
		args = append(args, "--strict")
	}
	if p.Options.Worktrees != "" && p.Options.Worktrees != Defaults().Worktrees {
		args = append(args, "--worktrees", string(p.Options.Worktrees))
	}
	return repair.Command(args)
}

// pendingSurvivors protects every branch the replay can move, including those
// above a target that selected only the bottom of a stack.
func (s Service) pendingSurvivors(ctx context.Context, plan Plan, recorded graph.Graph, readiness githubstack.Mergeability, allowPendingRoot bool) (repair.Note, error) {
	if len(plan.Branches) == 0 {
		return repair.Note{}, nil
	}
	root := ""
	for _, branch := range plan.Branches {
		if recorded.Records(branch) {
			root = branch
			break
		}
	}
	if root == "" {
		return repair.Note{}, nil
	}
	affected := recorded.Shape().Subtree(root)
	var extras []string
	for _, branch := range affected {
		found := false
		for _, selected := range plan.Branches {
			found = found || branch == selected
		}
		if !found {
			extras = append(extras, branch)
		}
	}
	prs := plan.PullRequests
	if len(extras) != 0 {
		more, err := s.GitHub.Inspect(ctx, extras)
		if err != nil {
			return repair.Note{}, err
		}
		prs = append([]githubstack.PullRequest{}, prs...)
		for _, pr := range more {
			for _, branch := range extras {
				if pr.Head == branch {
					known := false
					for _, existing := range prs {
						known = known || existing.Number == pr.Number
					}
					if !known {
						prs = append(prs, pr)
					}
					break
				}
			}
		}
	}
	resolutions := githubstack.ResolveHeads(prs)
	var numbers []int
	for _, branch := range affected {
		if open := resolutions[branch].Open; open != nil {
			if _, known := readiness.States[open.Number]; known {
				continue
			}
			numbers = append(numbers, open.Number)
		}
	}
	states := make(map[int]githubstack.MergeState, len(readiness.States))
	for number, state := range readiness.States {
		states[number] = state
	}
	if len(numbers) != 0 {
		more, err := s.GitHub.Mergeability(ctx, numbers, plan.Trunk)
		if err != nil {
			return repair.Note{}, err
		}
		for number, state := range more.States {
			states[number] = state
		}
	}
	for _, branch := range affected {
		open := resolutions[branch].Open
		if open == nil {
			continue
		}
		state := states[open.Number]
		if state.Pending() && (branch != root || plan.Queue == nil || !allowPendingRoot) {
			return repair.Note{Reason: fmt.Sprintf("%s (#%d) has a pending merge request and this descent would change its head or base", branch, open.Number), Ways: []repair.Step{{Effect: "finish or remove that merge request before landing its ancestors"}}}, nil
		}
	}
	return repair.Note{}, nil
}

// mergeQueued observes existing requests before any head/base mutation. A new
// request pins its head and is never retried blindly after transport failure.
func (s Service) mergeQueued(ctx context.Context, plan Plan, step Step, changed *[]string) (bool, error) {
	readiness, err := s.GitHub.Mergeability(ctx, []int{step.Number}, plan.Trunk)
	if err != nil {
		return false, err
	}
	if queuePolicy(readiness.Queue) != queuePolicy(plan.Queue) {
		return false, fmt.Errorf("the target merge queue policy changed; rerun %s", plan.RetryCommand())
	}
	recorded, err := s.Graph.Store.Load(ctx)
	if err != nil {
		return false, err
	}
	if note, err := s.pendingSurvivors(ctx, plan, recorded, readiness, true); err != nil {
		return false, err
	} else if note.Reason != "" {
		return false, errors.New(note.Sentence())
	}
	state := readiness.States[step.Number]
	tip, err := s.Git.Resolve(ctx, step.Branch)
	if err != nil {
		return false, err
	}
	if !state.Pending() && !state.Merged() {
		pushed, err := s.publish(ctx, plan, step)
		if err != nil {
			return false, err
		}
		if pushed {
			*changed = append(*changed, "published "+step.Branch)
		}
		if step.Retargets() {
			if err := s.retarget(ctx, step); err != nil {
				return false, err
			}
			*changed = append(*changed, fmt.Sprintf("pointed #%d at %s", step.Number, step.Base))
		}
		tip, err = s.Git.Resolve(ctx, step.Branch)
		if err != nil {
			return false, err
		}
		state, err = s.settlePush(ctx, step, tip, plan.Options.Remote)
		if err != nil {
			return false, err
		}
		// Retargeting may change which queue applies. Read the target policy again,
		// immediately before the head-pinned irreversible request.
		readiness, err = s.GitHub.Mergeability(ctx, []int{step.Number}, plan.Trunk)
		if err != nil {
			return false, err
		}
		if queuePolicy(readiness.Queue) != queuePolicy(plan.Queue) {
			return false, fmt.Errorf("the target merge queue policy changed; rerun %s", plan.RetryCommand())
		}
		state = readiness.States[step.Number]
		if !state.Merged() && !state.QueueEnabled {
			return false, fmt.Errorf("#%d does not report a required merge queue at %s; refusing to request an ordinary auto-merge", step.Number, step.Base)
		}
		if !state.Merged() {
			if _, err := s.recheck(ctx, plan, step, tip, state); err != nil {
				return false, err
			}
		}
		if !state.Pending() && !state.Merged() {
			requestErr := s.GitHub.RequestQueue(ctx, step.Number, step.ID, tip, plan.Queue.AutoMergeAllowed)
			if requestErr == nil {
				*changed = append(*changed, fmt.Sprintf("requested merge of #%d through the queue", step.Number))
			}
			observe, cancel := context.WithTimeout(context.WithoutCancel(ctx), diagnoseBudget)
			after, readErr := s.GitHub.Mergeability(observe, []int{step.Number}, plan.Trunk)
			cancel()
			if readErr != nil {
				if requestErr != nil {
					return false, &Pending{Branch: step.Branch, Number: step.Number, State: "merge request outcome unknown", Cause: fmt.Errorf("inspect #%d before retrying: %w", step.Number, requestErr)}
				}
				return false, &Pending{Branch: step.Branch, Number: step.Number, State: "merge requested", Cause: readErr}
			}
			state = after.States[step.Number]
			if requestErr != nil && !state.Pending() && !state.Merged() {
				return false, requestErr
			}
		}
	}
	return s.observeQueue(ctx, plan, step, tip, state)
}

func (s Service) observeQueue(ctx context.Context, plan Plan, step Step, tip string, state githubstack.MergeState) (bool, error) {
	observed := false
	seenPending := state.Pending()
	ask := func(ctx context.Context) (bool, error) {
		if state.HeadOID != tip || state.Base != step.Base || (state.QueueEntry != nil && state.QueueEntry.HeadCommit != nil && state.QueueEntry.HeadCommit.OID != tip) {
			return false, &queueRefusal{fmt.Errorf("#%d changed its head or base while pending; inspect it before rerunning %s", step.Number, plan.RetryCommand())}
		}
		if state.Merged() {
			observed = true
			return true, nil
		}
		if state.State == "OPEN" && !state.QueueEnabled {
			return false, &queueRefusal{fmt.Errorf("#%d no longer reports a required merge queue at %s", step.Number, step.Base)}
		}
		if state.State != "OPEN" {
			return false, &queueRefusal{fmt.Errorf("#%d closed without merging", step.Number)}
		}
		if seenPending && !state.Pending() {
			return false, &queueRefusal{fmt.Errorf("#%d was removed from the merge queue or its merge request was cancelled; inspect it before rerunning %s", step.Number, plan.RetryCommand())}
		}
		if len(state.Checks.Failed) != 0 || len(state.Checks.Cancelled) != 0 {
			cause := errors.New(checkReason(step.Branch, step.Number, state.Checks))
			if state.Pending() {
				return false, &Pending{Branch: step.Branch, Number: step.Number, State: pendingState(state), Cause: cause}
			}
			return false, cause
		}
		seenPending = seenPending || state.Pending()
		return false, nil
	}
	settled, err := ask(ctx)
	if err != nil {
		return false, err
	}
	if !settled && !plan.Options.Wait {
		var cause error
		if len(state.Checks.Failed) != 0 || len(state.Checks.Cancelled) != 0 {
			cause = errors.New(checkReason(step.Branch, step.Number, state.Checks))
		}
		return false, &Pending{Branch: step.Branch, Number: step.Number, State: pendingState(state), Cause: cause}
	}
	if !settled {
		err = settle(ctx, s.pause, fmt.Sprintf("#%d to merge through the queue", step.Number), func(ctx context.Context) (bool, error) {
			readiness, err := s.GitHub.Mergeability(ctx, []int{step.Number}, plan.Trunk)
			if err != nil {
				return false, err
			}
			if queuePolicy(readiness.Queue) != queuePolicy(plan.Queue) {
				return false, &queueRefusal{fmt.Errorf("the target merge queue policy changed while waiting")}
			}
			state = readiness.States[step.Number]
			return ask(ctx)
		})
		if err != nil {
			var refused *queueRefusal
			var pending *Pending
			if errors.As(err, &refused) || errors.As(err, &pending) {
				return observed, err
			}
			return observed, &Pending{Branch: step.Branch, Number: step.Number, State: pendingState(state), Cause: err}
		}
	}
	err = s.settleMergeObserved(ctx, plan, step, &observed)
	return observed, err
}
