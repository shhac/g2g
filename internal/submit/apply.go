package submit

import (
	"context"
	"fmt"
	"strings"

	"github.com/shhac/g2g/internal/diagnostic"
	"github.com/shhac/g2g/internal/subprocess"
)

// RequireClean refuses a submission over a working tree with changes, before
// anything is re-discovered or read from GitHub. A preview does not need it.
func (s Service) RequireClean(ctx context.Context) error { return s.Git.Clean(ctx) }

// Apply publishes all refs atomically, creates only branches with no PR, then
// links the resulting complete stack when asked to. Existing PRs are never
// retargeted.
//
// Linking is asked for rather than assumed because GitHub will not merge a
// linked pull request through gh pr merge, which is how g2g land merges each
// one; a stack submitted and then landed would stop at its first merge.
func (s Service) Apply(ctx context.Context, plan Plan, spec Spec, link bool) error {
	if blocked := plan.Blocked(); blocked != "" {
		return fmt.Errorf("submission is blocked by %s", blocked)
	}
	if link && plan.LinkBlocked() != "" {
		return fmt.Errorf("submission is blocked by %s", plan.LinkBlocked())
	}
	if err := plan.Snapshot.RequireActionable("g2g submit"); err != nil {
		return err
	}
	diagnostic.Event(ctx, "submit.apply", diagnostic.Field{Key: "branches", Value: strings.Join(plan.Snapshot.Branches, ",")}, diagnostic.Field{Key: "draft", Value: fmt.Sprintf("%t", spec.Draft)})
	requests, err := preparePulls(plan, spec)
	if err != nil {
		return err
	}
	if err := s.Pusher.Execute(ctx, plan.Push); err != nil {
		return err
	}
	pushed := !plan.Push.NothingToPublish()
	opened, err := s.createPulls(ctx, requests, spec.Draft)
	if err != nil {
		return stopped(pushed, opened, err)
	}
	if err := s.settleHeads(ctx, plan); err != nil {
		return stopped(pushed, opened, err)
	}
	if !link || len(plan.Snapshot.Branches) < 2 {
		return nil
	}
	if err := s.GitHub.Link(ctx, plan.Snapshot.Base, plan.Snapshot.Branches); err != nil {
		return stopped(pushed, opened, fmt.Errorf("linking the stack: %w", err))
	}
	return nil
}

// Stopped is a submission that failed after doing something it cannot take
// back: publishing the branches, or opening some of the pull requests. Those
// stand, so the run is neither a failure to retry from scratch nor a success.
type Stopped struct {
	// Pushed means the push published something.
	Pushed bool
	// Opened are the branches whose pull request was opened, in order.
	Opened []string
	Err    error
}

func (s *Stopped) Error() string {
	return fmt.Sprintf("stopped after publishing: %v", s.Err)
}

func (s *Stopped) Unwrap() error { return s.Err }

// stopped is err as a Stopped when anything before it happened, and as itself
// when nothing did: the push moved no ref and no pull request was opened.
func stopped(pushed bool, opened []string, err error) error {
	if !pushed && len(opened) == 0 {
		return err
	}
	return &Stopped{Pushed: pushed, Opened: opened, Err: err}
}

// validateSpec checks the same preparation Read and Apply use, before any push.
func validateSpec(plan Plan, spec Spec) error {
	_, err := preparePulls(plan, spec)
	return err
}

// creation is a validated request, with its base taken from the snapshot rather
// than the order metadata happened to appear in the submission document.
type creation struct {
	Pull
	Base string
}

// preparePulls validates all metadata and produces the ordered work to execute.
// Existing pull requests need no metadata and never become creation requests.
// Reviewers are checked here because a refusal after publishing is too late.
func preparePulls(plan Plan, spec Spec) ([]creation, error) {
	if spec.Version != 1 {
		return nil, fmt.Errorf("submission spec version must be 1")
	}
	selected := make(map[string]bool, len(plan.Snapshot.Branches))
	for _, branch := range plan.Snapshot.Branches {
		selected[branch] = true
	}
	pulls := make(map[string]Pull, len(spec.Pulls))
	for _, pull := range spec.Pulls {
		if !selected[pull.Branch] {
			return nil, fmt.Errorf("submission spec branch %q is unknown; want a branch in the selected stack", pull.Branch)
		}
		if _, duplicate := pulls[pull.Branch]; duplicate {
			return nil, fmt.Errorf("submission spec repeats branch %q", pull.Branch)
		}
		pulls[pull.Branch] = pull
	}
	requests := make([]creation, 0, len(spec.Pulls))
	for step := range plan.Snapshot.Steps(plan.Existing) {
		if step.Resolution.Open != nil {
			continue
		}
		branch := step.Branch
		pull, present := pulls[branch]
		if !present {
			return nil, fmt.Errorf("submission spec is incomplete: missing PR entry for branch %q", branch)
		}
		if strings.TrimSpace(pull.Title) == "" {
			return nil, fmt.Errorf("submission spec is incomplete: missing title for branch %q", branch)
		}
		for _, reviewer := range pull.Reviewers {
			if err := subprocess.CheckArgument("gh", "reviewer", reviewer); err != nil {
				return nil, fmt.Errorf("branch %q: %w", branch, err)
			}
		}
		requests = append(requests, creation{Pull: pull, Base: step.ExpectedBase})
	}
	return requests, nil
}

// createPulls executes only prepared requests and reports every opened branch,
// including when a later request fails. No validation or selection follows push.
func (s Service) createPulls(ctx context.Context, requests []creation, draft bool) ([]string, error) {
	opened := make([]string, 0, len(requests))
	for _, request := range requests {
		if err := s.GitHub.Create(ctx, request.Branch, request.Base, request.Title, request.Body, draft, request.Reviewers); err != nil {
			return opened, fmt.Errorf("opening the pull request for %s: %w", request.Branch, err)
		}
		opened = append(opened, request.Branch)
	}
	return opened, nil
}
