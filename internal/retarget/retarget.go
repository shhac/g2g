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
	"fmt"

	"github.com/shhac/gt2gh/internal/diagnostic"
	"github.com/shhac/gt2gh/internal/githubstack"
	"github.com/shhac/gt2gh/internal/stack"
)

// GitHub is the surface retargeting needs: read the pull requests on a path,
// and point one at a different base.
type GitHub interface {
	Inspect(ctx context.Context, branches []string) ([]githubstack.PullRequest, error)
	Retarget(ctx context.Context, number int, base string) error
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
	Blocked   string
}

// NothingToRetarget reports a plan with no work.
func (p Plan) NothingToRetarget() bool { return p.Blocked == "" && len(p.Changes) == 0 }

// Retargeting names the branches whose base this plan would move. It is not
// called Branches: Discovery already has that field, and it means the whole
// resolved path rather than the part being changed.
func (p Plan) Retargeting() []string {
	names := make([]string, 0, len(p.Changes))
	for _, change := range p.Changes {
		names = append(names, change.Branch)
	}
	return names
}

// Equal compares everything that changes what the write does.
func (p Plan) Equal(other Plan) bool {
	if !p.Discovery.Equal(other.Discovery) || p.Blocked != other.Blocked || len(p.Changes) != len(other.Changes) {
		return false
	}
	for index, change := range p.Changes {
		if change != other.Changes[index] {
			return false
		}
	}
	return true
}

// Plan works out which pull requests point at the wrong branch.
func (s Service) Plan(ctx context.Context, selection stack.Selection) (Plan, error) {
	if s.Git == nil || s.Selector == nil || s.GitHub == nil {
		return Plan{}, fmt.Errorf("retarget service is not fully configured")
	}
	discovery, err := stack.Discover(ctx, s.Selector, s.GitHub, selection, "gh pr edit")
	if err != nil {
		return Plan{}, err
	}
	plan := Plan{Discovery: discovery, Changes: []Change{}, Ambiguous: []string{}}
	for step := range githubstack.Along(discovery.Base, discovery.Branches, discovery.PullRequests) {
		switch step.Classify() {
		case githubstack.StepAmbiguous:
			plan.Ambiguous = append(plan.Ambiguous, step.Branch)
		case githubstack.StepBaseMismatch:
			plan.Changes = append(plan.Changes, Change{
				Branch: step.Branch,
				Number: step.Resolution.Open.Number,
				From:   step.Resolution.Open.Base,
				To:     step.ExpectedBase,
			})
		}
	}
	if len(plan.Ambiguous) != 0 {
		plan.Blocked = "more than one open pull request for a branch, so which one to retarget cannot be derived"
	}
	diagnostic.Event(ctx, "retarget.plan",
		diagnostic.Field{Key: "changes", Value: fmt.Sprintf("%d", len(plan.Changes))},
		diagnostic.Field{Key: "ambiguous", Value: fmt.Sprintf("%d", len(plan.Ambiguous))},
	)
	return plan, nil
}

// Revalidate re-reads the world and refuses if anything moved since preview.
func (s Service) Revalidate(ctx context.Context, selection stack.Selection, preview Plan) (Plan, error) {
	plan, err := s.Plan(ctx, selection)
	if err != nil {
		return Plan{}, err
	}
	return plan, diagnostic.Revalidated(ctx, "retarget", "retarget plan", plan.Equal(preview))
}

// Execute points each pull request at its recorded parent, bottom-up.
//
// It stops at the first refusal rather than unwinding. A base already moved is
// correct, and putting it back would undo the only part that worked.
func (s Service) Execute(ctx context.Context, plan Plan) error {
	if plan.Blocked != "" {
		return fmt.Errorf("cannot retarget: %s", plan.Blocked)
	}
	for _, change := range plan.Changes {
		if err := s.GitHub.Retarget(ctx, change.Number, change.To); err != nil {
			return err
		}
	}
	return nil
}
