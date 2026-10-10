// Package retarget points each pull request's base at the branch the resolved
// stack says it sits on.
//
// After a restack the local structure is correct and the remote bases may not
// be. Nothing repaired that: submit refuses to, deliberately, because changing
// what a merge will do is a different class of act from creating a pull request
// and wants its own preview. This is that command.
package retarget

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/shhac/g2g/internal/diagnostic"
	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/repair"
	"github.com/shhac/g2g/internal/stack"
)

// GitHub is the surface retargeting needs: read the pull requests on a path,
// and point them at different bases.
type GitHub interface {
	Inspect(ctx context.Context, branches []string) ([]githubstack.PullRequest, error)
	Retarget(ctx context.Context, changes []githubstack.BaseChange) ([]error, error)
}

// Service resolves a stack and reconciles its pull request bases with it.
type Service struct {
	Git      stack.Git
	Selector stack.PathSelector
	GitHub   GitHub
}

// Change is one pull request whose base does not match the structure.
type Change struct {
	Branch string
	Number int
	// ID is the pull request's node id, which the change addresses.
	ID string
	// From is the base GitHub currently records; To is the branch the resolved
	// stack says this one sits on.
	From string
	To   string
}

// Plan is what a retarget would do.
type Plan struct {
	stack.Discovery
	// Changes are ordered bottom-up, the order the stack itself reads in.
	Changes []Change
	// Ambiguous names branches with more than one open pull request. Nothing
	// here picks between them, so their base is left alone and the plan says so.
	Ambiguous []string
	// Repair is why an apply would refuse and the ways out, empty when it
	// would proceed.
	Repair repair.Note
}

// Blocked is why an apply would refuse, as one sentence, empty when it would
// proceed.
func (p Plan) Blocked() string { return p.Repair.Sentence() }

// NothingToRetarget reports a plan with no work.
func (p Plan) NothingToRetarget() bool { return p.Blocked() == "" && len(p.Changes) == 0 }

// Equal compares everything that changes what the write does.
func (p Plan) Equal(other Plan) bool {
	if !p.Discovery.Equal(other.Discovery) || !p.Repair.Equal(other.Repair) || len(p.Changes) != len(other.Changes) {
		return false
	}
	for index, change := range p.Changes {
		if change != other.Changes[index] {
			return false
		}
	}
	return true
}

// Ready reports a service with everything it needs.
//
// One rule, called by both the guard below and the command registration in
// internal/cli. They were two hand-written conjunctions before, and three of
// them had already drifted -- a command could be registered and then refuse on
// use, or be hidden from a build that could have run it.
func (s Service) Ready() bool {
	return s.Git != nil && s.Selector != nil && s.GitHub != nil
}

// Plan works out which pull requests point at the wrong branch.
func (s Service) Plan(ctx context.Context, selection stack.Selection) (Plan, error) {
	if !s.Ready() {
		return Plan{}, fmt.Errorf("retarget service is not fully configured")
	}
	discovery, err := stack.Discover(ctx, s.Selector, s.GitHub, selection, "g2g github retarget")
	if err != nil {
		return Plan{}, err
	}
	// Which base each pull request should have is well defined on a fork, and
	// the edges below are what answer it. The refusal is the projection rule
	// every publishing command follows, so a person always retargets exactly
	// the line they would link and push.
	if err := discovery.Snapshot.RequireLinear("retarget"); err != nil {
		return Plan{}, err
	}
	plan := Plan{Discovery: discovery, Changes: []Change{}, Ambiguous: []string{}}
	for step := range githubstack.Across(discovery.Shape().Parents, discovery.Branches, discovery.PullRequests) {
		switch step.Classify() {
		case githubstack.StepAmbiguous:
			plan.Ambiguous = append(plan.Ambiguous, step.Branch)
		case githubstack.StepBaseMismatch:
			plan.Changes = append(plan.Changes, Change{
				Branch: step.Branch,
				Number: step.Resolution.Open.Number,
				ID:     step.Resolution.Open.ID,
				From:   step.Resolution.Open.Base,
				To:     step.ExpectedBase,
			})
		}
	}
	if len(plan.Ambiguous) != 0 {
		// No command here, deliberately: which pull request a branch means is a
		// person's choice, and the way out says so rather than naming one.
		plan.Repair = repair.Note{
			Reason: "more than one open pull request for " + strings.Join(plan.Ambiguous, ", ") + ", so which one to retarget cannot be derived",
			Ways:   []repair.Step{{Effect: "close all but one open pull request for each, then rerun"}},
		}
	}
	diagnostic.Event(ctx, "retarget.plan",
		diagnostic.Field{Key: "changes", Value: fmt.Sprintf("%d", len(plan.Changes))},
		diagnostic.Field{Key: "ambiguous", Value: fmt.Sprintf("%d", len(plan.Ambiguous))},
	)
	return plan, nil
}

// Stopped is a retarget that moved some bases and not others. Those it moved
// are on GitHub and stay, so the run is neither a failure to retry from
// scratch nor a success.
type Stopped struct {
	// Moved are the changes GitHub applied, in order.
	Moved []Change
	// Failed are the changes it did not, in order, each with why.
	Failed []Failure
	// Err is what failed the run: the request that failed as a whole, or
	// every refusal joined.
	Err error
}

// Failure is one change GitHub did not apply, and why.
type Failure struct {
	Change Change
	Err    error
}

func (s *Stopped) Error() string {
	moved := make([]int, 0, len(s.Moved))
	for _, change := range s.Moved {
		moved = append(moved, change.Number)
	}
	return fmt.Sprintf("moved the base of %s but not of %s", githubstack.NamePullRequests(moved), Describe(s.Failed))
}

func (s *Stopped) Unwrap() error { return s.Err }

// Describe names every change that failed and why, a shared reason once.
func Describe(failed []Failure) string {
	unapplied := make([]githubstack.Unapplied, 0, len(failed))
	for _, failure := range failed {
		unapplied = append(unapplied, githubstack.Unapplied{Number: failure.Change.Number, Err: failure.Err})
	}
	return githubstack.DescribeUnapplied(unapplied)
}

// Execute points each pull request at its recorded parent, bottom-up, in as
// few requests as GitHub needs.
//
// GitHub applies every change in a request whatever happened to the one
// before, so a refusal does not stop the run: it moves every base it can and
// reports each one it could not. Nothing is unwound -- a base already moved is
// correct, and putting it back would undo the part that worked. Nothing moved
// is an ordinary error; some moved is a Stopped.
func (s Service) Execute(ctx context.Context, plan Plan) error {
	if plan.Blocked() != "" {
		return fmt.Errorf("cannot retarget: %s", plan.Blocked())
	}
	if err := plan.Snapshot.RequireActionable("g2g github retarget"); err != nil {
		return err
	}
	if len(plan.Changes) == 0 {
		return nil
	}
	changes := make([]githubstack.BaseChange, 0, len(plan.Changes))
	for _, change := range plan.Changes {
		changes = append(changes, githubstack.BaseChange{ID: change.ID, Number: change.Number, Base: change.To})
	}
	outcomes, whole := s.GitHub.Retarget(ctx, changes)
	stopped := &Stopped{Moved: []Change{}, Failed: []Failure{}, Err: whole}
	refusals := make([]error, 0)
	for index, change := range plan.Changes {
		if outcomes[index] == nil {
			stopped.Moved = append(stopped.Moved, change)
			continue
		}
		stopped.Failed = append(stopped.Failed, Failure{Change: change, Err: outcomes[index]})
		refusals = append(refusals, outcomes[index])
	}
	switch {
	case len(stopped.Failed) == 0:
		return nil
	case len(stopped.Moved) != 0:
		if stopped.Err == nil {
			stopped.Err = errors.Join(refusals...)
		}
		return stopped
	case whole != nil:
		return whole
	}
	return fmt.Errorf("no base was moved: %s", Describe(stopped.Failed))
}
