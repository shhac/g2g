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
	"github.com/shhac/g2g/internal/stack"
)

type Git interface {
	stack.Git
	Clean(context.Context) error
	Remote(context.Context, string) error
	RemoteTips(context.Context, string, []string) (map[string]string, error)
	PushAtomic(context.Context, string, []localgit.Lease) error
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
	// RemoteTips is what the remote held when the plan was built; the push
	// leases assert exactly these.
	RemoteTips map[string]string
}

// Leases pairs each selected branch with the tip the plan observed for it.
func (p Plan) Leases() []localgit.Lease {
	leases := make([]localgit.Lease, 0, len(p.Snapshot.Branches))
	for _, branch := range p.Snapshot.Branches {
		leases = append(leases, localgit.Lease{Branch: branch, Expected: p.RemoteTips[branch]})
	}
	return leases
}

func (s Service) Plan(ctx context.Context, selection stack.Selection, remote string) (Plan, error) {
	if s.Git == nil || s.Selector == nil || s.GitHub == nil {
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
	issues, superseded := assessExisting(discovery.PullRequests, snapshot.Base, snapshot.Branches)
	tips, err := s.Git.RemoteTips(ctx, remote, snapshot.Branches)
	if err != nil {
		return Plan{}, err
	}
	plan := Plan{Snapshot: snapshot, Remote: remote, Existing: discovery.PullRequests, Issues: issues, Superseded: superseded, RemoteTips: tips}
	decision := "ready"
	if len(plan.Issues) != 0 {
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
		maps.Equal(p.RemoteTips, other.RemoteTips)
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
	if len(plan.Issues) != 0 {
		return fmt.Errorf("submission is blocked by existing pull request state: %s", issueText(plan.Issues))
	}
	diagnostic.Event(ctx, "submit.apply", diagnostic.Field{Key: "branches", Value: strings.Join(plan.Snapshot.Branches, ",")}, diagnostic.Field{Key: "draft", Value: fmt.Sprintf("%t", spec.Draft)})
	if err := validateSpec(plan, spec); err != nil {
		return err
	}
	if err := s.Git.PushAtomic(ctx, plan.Remote, plan.Leases()); err != nil {
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

func validateSpec(plan Plan, spec Spec) error {
	if len(spec.Pulls) != len(plan.Snapshot.Branches) {
		return fmt.Errorf("submission spec does not match selected stack")
	}
	for index, branch := range plan.Snapshot.Branches {
		if spec.Pulls[index].Branch != branch || strings.TrimSpace(spec.Pulls[index].Title) == "" {
			return fmt.Errorf("submission spec is not valid for branch %q", branch)
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
