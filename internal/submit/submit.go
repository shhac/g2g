package submit

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/shhac/gt2gh/internal/diagnostic"
	localgit "github.com/shhac/gt2gh/internal/git"
	"github.com/shhac/gt2gh/internal/githubstack"
	"github.com/shhac/gt2gh/internal/stack"
)

type Git interface {
	stack.Git
	Clean(context.Context) error
	Remote(context.Context, string) error
	PushAtomic(context.Context, string, []string) error
}

type Graphite interface{ stack.Graphite }

type GitHub interface {
	Inspect(context.Context, []string) ([]githubstack.PullRequest, error)
	Create(context.Context, string, string, string, string, bool, []string) error
	Link(context.Context, string, []string) error
}

type Service struct {
	Git      Git
	Graphite Graphite
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
}

func (s Service) Plan(ctx context.Context, selection stack.Selection, remote string) (Plan, error) {
	if s.Git == nil || s.Graphite == nil || s.GitHub == nil {
		return Plan{}, fmt.Errorf("submit service is not fully configured")
	}
	if err := s.Git.Remote(ctx, remote); err != nil {
		return Plan{}, err
	}
	snapshot, err := stack.Resolve(ctx, s.Git, s.Graphite, selection, "g2g submit")
	if err != nil {
		return Plan{}, err
	}
	prs, err := s.GitHub.Inspect(ctx, snapshot.Branches)
	if err != nil {
		return Plan{}, err
	}
	issues, superseded := assessExisting(prs, snapshot.Base, snapshot.Branches)
	plan := Plan{Snapshot: snapshot, Remote: remote, Existing: prs, Issues: issues, Superseded: superseded}
	decision := "ready"
	if len(plan.Issues) != 0 {
		decision = "blocked"
	}
	diagnostic.Event(ctx, "submit.plan", diagnostic.Field{Key: "decision", Value: decision}, diagnostic.Field{Key: "target", Value: snapshot.Target}, diagnostic.Field{Key: "remote", Value: remote}, diagnostic.Field{Key: "branches", Value: strings.Join(snapshot.Branches, ",")})
	return plan, nil
}

func (p Plan) Equal(other Plan) bool {
	if p.Remote != other.Remote || p.Snapshot.Target != other.Snapshot.Target || p.Snapshot.Base != other.Snapshot.Base || len(p.Snapshot.Branches) != len(other.Snapshot.Branches) || len(p.Existing) != len(other.Existing) || len(p.Issues) != len(other.Issues) {
		return false
	}
	for i := range p.Snapshot.Branches {
		if p.Snapshot.Branches[i] != other.Snapshot.Branches[i] {
			return false
		}
	}
	for i := range p.Existing {
		if p.Existing[i] != other.Existing[i] {
			return false
		}
	}
	for branch, issue := range p.Issues {
		if other.Issues[branch] != issue {
			return false
		}
	}
	if len(p.Superseded) != len(other.Superseded) {
		return false
	}
	for branch, pr := range p.Superseded {
		if other.Superseded[branch] != pr {
			return false
		}
	}
	return true
}

func (s Service) Revalidate(ctx context.Context, selection stack.Selection, remote string, preview Plan) (Plan, error) {
	if err := s.Git.Clean(ctx); err != nil {
		return Plan{}, err
	}
	plan, err := s.Plan(ctx, selection, remote)
	if err != nil {
		return Plan{}, err
	}
	if !plan.Equal(preview) {
		diagnostic.Event(ctx, "submit.revalidation", diagnostic.Field{Key: "match", Value: "false"})
		return Plan{}, fmt.Errorf("submit plan changed during revalidation; rerun without --apply to review the new plan")
	}
	diagnostic.Event(ctx, "submit.revalidation", diagnostic.Field{Key: "match", Value: "true"})
	return plan, nil
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
	if err := s.Git.PushAtomic(ctx, plan.Remote, plan.Snapshot.Branches); err != nil {
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
	resolutions := githubstack.ResolveHeads(prs)
	issues := map[string]string{}
	superseded := map[string]githubstack.PullRequest{}
	for _, branch := range branches {
		resolution := resolutions[branch]
		switch {
		case resolution.Ambiguous():
			issues[branch] = fmt.Sprintf("%d open pull requests", resolution.OpenCount)
		case resolution.Open != nil:
			if resolution.Open.Base != base {
				issues[branch] = "PR base " + resolution.Open.Base + ", want " + base
			}
		case resolution.Superseded():
			superseded[branch] = *resolution.Latest
		}
		base = branch
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
