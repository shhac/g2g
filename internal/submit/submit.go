package submit

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"

	"github.com/shhac/g2g/internal/diagnostic"
	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/push"
	"github.com/shhac/g2g/internal/stack"
	"github.com/shhac/g2g/internal/subprocess"
)

type Git interface {
	stack.Git
	Clean(context.Context) error
	// Remote is checked before anything is read, so a mistyped remote costs
	// nothing on GitHub. push checks it again when it plans; that is one
	// local read.
	Remote(context.Context, string) error
}

// Pusher publishes the stack, and refuses a remote that holds work the stack
// does not have.
//
// It is push's own planning rather than a copy of its lease. submit used to
// build its leases from the tips it read and push them itself, and a lease
// pinned to the tip you just read always matches: a reviewer's commit sitting
// on the remote was force-pushed over, and then a pull request opened for
// what was left. push refuses exactly that, and land already went through it.
type Pusher interface {
	Plan(ctx context.Context, selection stack.Selection, remote string) (push.Plan, error)
	Execute(ctx context.Context, plan push.Plan) error
}

type GitHub interface {
	stack.GitHub
	Create(context.Context, string, string, string, string, bool, []string) error
	Link(context.Context, string, []string) error
}

type Service struct {
	Git Git
	// Selector supplies the ordered path, from whichever source describes the
	// branch.
	Selector stack.PathSelector
	GitHub   GitHub
	Pusher   Pusher
}

type Plan struct {
	Snapshot stack.Snapshot
	Remote   string
	Existing []githubstack.PullRequest
	Issues   map[string]string
	// Superseded records branches whose only pull requests are closed or
	// merged, so a preview can show that a new one will be created rather than
	// silently reusing a branch name that has history.
	Superseded map[string]githubstack.PullRequest
	// Push is how the stack is published, planned by push with its refusals.
	Push push.Plan
}

// Ready reports a service with everything it needs.
//
// One rule, called by both the guard below and the command registration in
// internal/cli. They were two hand-written conjunctions before, and three of
// them had already drifted -- a command could be registered and then refuse on
// use, or be hidden from a build that could have run it.
func (s Service) Ready() bool {
	return s.Git != nil && s.Selector != nil && s.GitHub != nil && s.Pusher != nil
}

// Blocked is why an apply would refuse, empty when it would proceed.
func (p Plan) Blocked() string {
	if len(p.Issues) != 0 {
		return "existing pull request state: " + issueText(p.Issues)
	}
	return p.Push.Blocked
}

func (s Service) Plan(ctx context.Context, selection stack.Selection, remote string) (Plan, error) {
	if !s.Ready() {
		return Plan{}, fmt.Errorf("submit service is not fully configured")
	}
	if err := s.Git.Remote(ctx, remote); err != nil {
		return Plan{}, err
	}
	discovery, err := stack.Discover(ctx, s.Selector, s.GitHub, selection, "g2g submit")
	if err != nil {
		return Plan{}, err
	}
	snapshot := discovery.Snapshot
	// Missing pull requests are created each on the one before it, and the
	// result is linked as one list, so a fork would open a pull request on its
	// sibling and link the two as a line.
	if err := snapshot.RequireLinear("submit"); err != nil {
		return Plan{}, err
	}
	issues, superseded := assessExisting(discovery.PullRequests, snapshot.Base, snapshot.Branches)
	published, err := s.Pusher.Plan(ctx, selection, remote)
	if err != nil {
		return Plan{}, err
	}
	plan := Plan{Snapshot: snapshot, Remote: remote, Existing: discovery.PullRequests, Issues: issues, Superseded: superseded, Push: published}
	decision := "ready"
	if plan.Blocked() != "" {
		decision = "blocked"
	}
	diagnostic.Event(ctx, "submit.plan", diagnostic.Field{Key: "decision", Value: decision}, diagnostic.Field{Key: "target", Value: snapshot.Target}, diagnostic.Field{Key: "remote", Value: remote}, diagnostic.Field{Key: "branches", Value: strings.Join(snapshot.Branches, ",")})
	return plan, nil
}

// Equal compares every fact that can change what the submission does.
func (p Plan) Equal(other Plan) bool {
	return p.Remote == other.Remote &&
		p.Snapshot.Equal(other.Snapshot) &&
		slices.Equal(p.Existing, other.Existing) &&
		maps.Equal(p.Issues, other.Issues) &&
		maps.Equal(p.Superseded, other.Superseded) &&
		p.Push.Equal(other.Push)
}

func (s Service) Revalidate(ctx context.Context, selection stack.Selection, remote string, preview Plan) (Plan, error) {
	if err := s.Git.Clean(ctx); err != nil {
		return Plan{}, err
	}
	plan, err := s.Plan(ctx, selection, remote)
	if err != nil {
		return Plan{}, err
	}
	return plan, diagnostic.Revalidated(ctx, "submit", "submit plan", plan.Equal(preview))
}

// Apply publishes all refs atomically, creates only branches with no PR, then
// links the resulting complete stack. Existing PRs are never retargeted.
func (s Service) Apply(ctx context.Context, plan Plan, spec Spec) error {
	if blocked := plan.Blocked(); blocked != "" {
		return fmt.Errorf("submission is blocked by %s", blocked)
	}
	if err := plan.Snapshot.RequireActionable("g2g submit"); err != nil {
		return err
	}
	diagnostic.Event(ctx, "submit.apply", diagnostic.Field{Key: "branches", Value: strings.Join(plan.Snapshot.Branches, ",")}, diagnostic.Field{Key: "draft", Value: fmt.Sprintf("%t", spec.Draft)})
	if err := validateSpec(plan, spec); err != nil {
		return err
	}
	if err := s.Pusher.Execute(ctx, plan.Push); err != nil {
		return err
	}
	if err := s.createMissingPulls(ctx, plan, spec); err != nil {
		return err
	}
	if len(plan.Snapshot.Branches) < 2 {
		return nil
	}
	return s.GitHub.Link(ctx, plan.Snapshot.Base, plan.Snapshot.Branches)
}

// validateSpec checks everything the spec contributes to a mutation, and runs
// before the push because the push cannot be taken back.
//
// Reviewers reach gh as "--reviewer <value>", so a value gh would read as an
// option is the shared CheckArgument case. Leaving it to gh meant the refusal
// arrived after the refs were already published, which is the one ordering
// this command exists to get right.
func validateSpec(plan Plan, spec Spec) error {
	if len(spec.Pulls) != len(plan.Snapshot.Branches) {
		return fmt.Errorf("submission spec does not match selected stack")
	}
	for index, branch := range plan.Snapshot.Branches {
		if spec.Pulls[index].Branch != branch || strings.TrimSpace(spec.Pulls[index].Title) == "" {
			return fmt.Errorf("submission spec is not valid for branch %q", branch)
		}
		for _, reviewer := range spec.Pulls[index].Reviewers {
			if err := subprocess.CheckArgument("gh", "reviewer", reviewer); err != nil {
				return fmt.Errorf("branch %q: %w", branch, err)
			}
		}
	}
	return nil
}

// createMissingPulls creates one pull request per branch that has no open one.
// Keying off open pull requests rather than any match is what lets a branch
// with a closed predecessor be re-submitted instead of silently skipped and
// then failing at the link step.
func (s Service) createMissingPulls(ctx context.Context, plan Plan, spec Spec) error {
	resolutions := githubstack.ResolveHeads(plan.Existing)
	base := plan.Snapshot.Base
	for _, pull := range spec.Pulls {
		if resolutions[pull.Branch].Open == nil {
			if err := s.GitHub.Create(ctx, pull.Branch, base, pull.Title, pull.Body, spec.Draft, pull.Reviewers); err != nil {
				return err
			}
		}
		base = pull.Branch
	}
	return nil
}

// assessExisting reports only what blocks submission. A branch whose pull
// requests are all closed or merged is not blocked: re-submitting a stack
// whose branch names were used before is the recovery this command exists for,
// so that history is recorded as superseded and a new pull request is created.
func assessExisting(prs []githubstack.PullRequest, base string, branches []string) (map[string]string, map[string]githubstack.PullRequest) {
	issues := map[string]string{}
	superseded := map[string]githubstack.PullRequest{}
	for step := range githubstack.Along(base, branches, prs) {
		// A missing pull request is not an issue for submit: creating it is the
		// job. That is the whole of submit's policy difference from link.
		switch step.Classify() {
		case githubstack.StepAmbiguous:
			issues[step.Branch] = fmt.Sprintf("%d open pull requests", step.Resolution.OpenCount)
		case githubstack.StepBaseMismatch:
			issues[step.Branch] = "PR base " + step.Resolution.Open.Base + ", want " + step.ExpectedBase
		case githubstack.StepSuperseded:
			superseded[step.Branch] = *step.Resolution.Latest
		}
	}
	return issues, superseded
}
func issueText(issues map[string]string) string {
	parts := make([]string, 0, len(issues))
	for branch, issue := range issues {
		parts = append(parts, branch+": "+issue)
	}
	sort.Strings(parts)
	return strings.Join(parts, "; ")
}

var _ Git = localgit.Client{}
