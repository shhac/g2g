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
	"strings"

	"github.com/shhac/g2g/internal/diagnostic"
	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/repair"
	"github.com/shhac/g2g/internal/stack"
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
	// Blocked is why an apply would refuse, and Repair the same in parts.
	Blocked string
	Repair  repair.Note
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
	discovery, err := stack.Discover(ctx, s.Selector, s.GitHub, selection, "gh pr edit")
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
	for step := range githubstack.Across(expectedParents(discovery.Snapshot), discovery.Branches, discovery.PullRequests) {
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
		// No command here, deliberately: which pull request a branch means is a
		// person's choice, and the way out says so rather than naming one.
		plan.Repair = repair.Note{
			Reason: "more than one open pull request for " + strings.Join(plan.Ambiguous, ", ") + ", so which one to retarget cannot be derived",
			Ways:   []repair.Step{{Effect: "close all but one open pull request for each, then rerun"}},
		}
		plan.Blocked = plan.Repair.Sentence()
	}
	diagnostic.Event(ctx, "retarget.plan",
		diagnostic.Field{Key: "changes", Value: fmt.Sprintf("%d", len(plan.Changes))},
		diagnostic.Field{Key: "ambiguous", Value: fmt.Sprintf("%d", len(plan.Ambiguous))},
	)
	return plan, nil
}

// expectedParents is the branch each selected one should be based on: its
// recorded parent, and the base for the selection's own roots. A rolling base
// would compare a pull request with whichever sibling happened to come first,
// so it is used only for a selection that records no edges at all, which can
// only be a line.
func expectedParents(snapshot stack.Snapshot) map[string]string {
	parents := make(map[string]string, len(snapshot.Branches))
	below := snapshot.Base
	for _, branch := range snapshot.Branches {
		parent, within := snapshot.ParentOf(branch)
		switch {
		case within:
		case len(snapshot.Parents) == 0:
			parent = below
		default:
			parent = snapshot.Base
		}
		parents[branch] = parent
		below = branch
	}
	return parents
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
	if err := plan.Snapshot.RequireActionable("g2g github retarget"); err != nil {
		return err
	}
	for _, change := range plan.Changes {
		if err := s.GitHub.Retarget(ctx, change.Number, change.To); err != nil {
			return err
		}
	}
	return nil
}
