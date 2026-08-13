package submit

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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
	plan := Plan{Snapshot: snapshot, Remote: remote, Existing: prs, Issues: assessExisting(prs, snapshot.Base, snapshot.Branches)}
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
	if err := s.Git.PushAtomic(ctx, plan.Remote, plan.Snapshot.Branches); err != nil {
		return err
	}
	existing := byHead(plan.Existing)
	base := plan.Snapshot.Base
	for _, pull := range spec.Pulls {
		if _, exists := existing[pull.Branch]; !exists {
			body, err := temporaryBody(pull.Body)
			if err != nil {
				return err
			}
			err = s.GitHub.Create(ctx, pull.Branch, base, pull.Title, body, spec.Draft, pull.Reviewers)
			_ = os.Remove(body)
			if err != nil {
				return err
			}
		}
		base = pull.Branch
	}
	if len(plan.Snapshot.Branches) < 2 {
		return nil
	}
	return s.GitHub.Link(ctx, plan.Snapshot.Base, plan.Snapshot.Branches)
}

func byHead(prs []githubstack.PullRequest) map[string]githubstack.PullRequest {
	out := make(map[string]githubstack.PullRequest, len(prs))
	for _, pr := range prs {
		out[pr.Head] = pr
	}
	return out
}

func assessExisting(prs []githubstack.PullRequest, base string, branches []string) map[string]string {
	byHead := make(map[string][]githubstack.PullRequest)
	for _, pr := range prs {
		byHead[pr.Head] = append(byHead[pr.Head], pr)
	}
	issues := map[string]string{}
	for _, branch := range branches {
		matches := byHead[branch]
		if len(matches) > 1 {
			issues[branch] = "multiple pull requests"
			continue
		}
		if len(matches) == 1 {
			pr := matches[0]
			if pr.State != "OPEN" {
				issues[branch] = strings.ToLower(pr.State) + " pull request"
			} else if pr.Base != base {
				issues[branch] = "PR base " + pr.Base + ", want " + base
			}
		}
		base = branch
	}
	return issues
}
func issueText(issues map[string]string) string {
	parts := make([]string, 0, len(issues))
	for branch, issue := range issues {
		parts = append(parts, branch+": "+issue)
	}
	sort.Strings(parts)
	return strings.Join(parts, "; ")
}
func temporaryBody(body string) (string, error) {
	f, err := os.CreateTemp("", "g2g-submit-body-*.md")
	if err != nil {
		return "", err
	}
	path := f.Name()
	if _, err := f.WriteString(body); err != nil {
		f.Close()
		os.Remove(path)
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return "", err
	}
	return filepath.Clean(path), nil
}

var _ Git = localgit.Client{}
