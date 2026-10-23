package comment

import (
	"context"
	"errors"
	"fmt"

	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/stack"
)

// Stopped is a run that wrote some comments and not others.
//
// Those it wrote are correct, so reporting the run as not applied would be
// wrong about the part that worked; rerunning edits them rather than adding
// to them, and writes the rest.
type Stopped struct {
	// Written are the pull requests whose comment GitHub applied, in order.
	Written []int
	// Failed are those whose comment it did not, in order, each with why.
	Failed []githubstack.Unapplied
	// Err is what failed the run: the request that failed as a whole, or
	// every refusal joined.
	Err error
}

func (s *Stopped) Error() string {
	return fmt.Sprintf("wrote the comment on %s but not on %s", githubstack.NamePullRequests(s.Written), githubstack.DescribeUnapplied(s.Failed))
}

func (s *Stopped) Unwrap() error { return s.Err }

// Execute sends every write, in order, in as few requests as GitHub needs.
//
// GitHub applies every write in a request whatever happened to the one before,
// so a refusal does not stop the run: it writes every comment it can and
// reports each one it could not. Nothing is unwound. Nothing written is an
// ordinary error; some written is a Stopped, because some of the run happened.
func (s Service) Execute(ctx context.Context, plan Plan) error {
	if plan.Blocked() != "" {
		return fmt.Errorf("cannot keep the stack comments: %s", plan.Blocked())
	}
	sending := make([]Write, 0, plan.Changing())
	writes := make([]githubstack.CommentWrite, 0, plan.Changing())
	for _, write := range plan.Writes {
		if !write.Changes() {
			continue
		}
		sending = append(sending, write)
		if write.Action == ActionCreate {
			writes = append(writes, githubstack.CommentWrite{Subject: write.Subject, Body: write.Body})
			continue
		}
		writes = append(writes, githubstack.CommentWrite{Comment: write.Comment, Body: write.Body})
	}
	if len(writes) == 0 {
		return nil
	}
	outcomes, whole := s.GitHub.WriteComments(ctx, writes)
	stopped := &Stopped{Written: []int{}, Failed: []githubstack.Unapplied{}, Err: whole}
	refusals := make([]error, 0)
	for index, write := range sending {
		if outcomes[index] == nil {
			stopped.Written = append(stopped.Written, write.Number)
			continue
		}
		stopped.Failed = append(stopped.Failed, githubstack.Unapplied{Number: write.Number, Err: outcomes[index]})
		refusals = append(refusals, outcomes[index])
	}
	switch {
	case len(stopped.Failed) == 0:
		return nil
	case len(stopped.Written) != 0:
		if stopped.Err == nil {
			stopped.Err = errors.Join(refusals...)
		}
		return stopped
	case whole != nil:
		return whole
	}
	return fmt.Errorf("no comment was written: %s", githubstack.DescribeUnapplied(stopped.Failed))
}

// Keep plans the stack's comments and writes them in one step, for a command
// that has just changed what the stack's pull requests are — submit opening
// them, land merging some — and keeps the map in step unless told not to.
//
// There is no separate preview: the enclosing command said it would do this
// in its own preview, and every write is an edit of a comment this tool owns
// or a new one on an open pull request, so nothing here is worth a second
// confirmation. A plan that is blocked is not written, and says why.
func (s Service) Keep(ctx context.Context, selection stack.Selection) (Plan, error) {
	plan, err := s.Plan(ctx, selection)
	if err != nil {
		return Plan{}, &NotKept{Err: err}
	}
	if plan.Blocked() != "" {
		return plan, &NotKept{Err: fmt.Errorf("%s", plan.Blocked())}
	}
	if err := s.Execute(ctx, plan); err != nil {
		return plan, &NotKept{Err: err}
	}
	return plan, nil
}

// NotKept is a command that did its own work and then could not keep the
// stack comments. What it did stands; the comments can be kept later with
// g2g github comment --apply, which is what a caller says.
type NotKept struct{ Err error }

func (e *NotKept) Error() string { return "the stack comments were not kept: " + e.Err.Error() }
func (e *NotKept) Unwrap() error { return e.Err }
