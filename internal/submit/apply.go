package submit

import (
	"context"
	"fmt"
	"strings"

	"github.com/shhac/g2g/internal/diagnostic"
	"github.com/shhac/g2g/internal/githubstack"
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
	if err := validateSpec(plan, spec); err != nil {
		return err
	}
	if err := s.Pusher.Execute(ctx, plan.Push); err != nil {
		return err
	}
	pushed := !plan.Push.NothingToPublish()
	opened, err := s.createMissingPulls(ctx, plan, spec)
	if err != nil {
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
//
// It returns the branches it opened one for, including when it stops early.
func (s Service) createMissingPulls(ctx context.Context, plan Plan, spec Spec) ([]string, error) {
	resolutions := githubstack.ResolveHeads(plan.Existing)
	base := plan.Snapshot.Base
	opened := make([]string, 0, len(spec.Pulls))
	for _, pull := range spec.Pulls {
		if resolutions[pull.Branch].Open == nil {
			if err := s.GitHub.Create(ctx, pull.Branch, base, pull.Title, pull.Body, spec.Draft, pull.Reviewers); err != nil {
				return opened, fmt.Errorf("opening the pull request for %s: %w", pull.Branch, err)
			}
			opened = append(opened, pull.Branch)
		}
		base = pull.Branch
	}
	return opened, nil
}
