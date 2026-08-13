// Package push implements the explicit Git-only stack-ref escape hatch.
package push

import (
	"context"
	"fmt"
	"strings"

	"github.com/shhac/gt2gh/internal/diagnostic"
	localgit "github.com/shhac/gt2gh/internal/git"
	"github.com/shhac/gt2gh/internal/graphite"
	"github.com/shhac/gt2gh/internal/link"
)

type Git interface {
	CurrentBranch(context.Context) (string, error)
	LocalBranches(context.Context) ([]string, error)
	Remote(context.Context, string) error
	PushAtomic(context.Context, string, []string) error
}

type Graphite interface {
	DiscoverStack(context.Context, string, bool) (graphite.Stack, error)
}

type Service struct {
	Git      Git
	Graphite Graphite
}

type Plan struct {
	Target       string
	TargetSource string
	Base         string
	BaseSource   string
	Branches     []string
	Remote       string
}

func (s Service) Plan(ctx context.Context, selection link.Selection, remote string) (Plan, error) {
	if s.Git == nil || s.Graphite == nil {
		return Plan{}, fmt.Errorf("push service is not fully configured")
	}
	if err := s.Git.Remote(ctx, remote); err != nil {
		return Plan{}, err
	}
	target, source, err := target(ctx, s.Git, selection.Branch)
	if err != nil {
		return Plan{}, err
	}
	localBranches, err := s.Git.LocalBranches(ctx)
	if err != nil {
		return Plan{}, err
	}
	local := make(map[string]bool, len(localBranches))
	for _, branch := range localBranches {
		local[branch] = true
	}
	if !local[target] {
		return Plan{}, fmt.Errorf("selected branch %q is not a local branch", target)
	}
	stack, err := s.Graphite.DiscoverStack(ctx, target, selection.Stack)
	if err != nil {
		return Plan{}, err
	}
	for _, branch := range stack.Path {
		if !local[branch] {
			return Plan{}, fmt.Errorf("Graphite ancestry branch %q is not a local branch", branch)
		}
		if strings.HasPrefix(branch, "-") {
			return Plan{}, fmt.Errorf("Graphite ancestry branch %q cannot be passed safely to git push", branch)
		}
	}
	base, baseSource, branches, err := link.SelectBoundary(stack.Path, stack.Trunks, selection.Trunk)
	if err != nil {
		return Plan{}, err
	}
	if len(branches) == 0 {
		return Plan{}, fmt.Errorf("selected Graphite path has no non-trunk branches to push")
	}
	plan := Plan{Target: target, TargetSource: source, Base: base, BaseSource: baseSource, Branches: branches, Remote: remote}
	diagnostic.Event(ctx, "push.plan",
		diagnostic.Field{Key: "decision", Value: "ready"},
		diagnostic.Field{Key: "target", Value: target},
		diagnostic.Field{Key: "target_source", Value: source},
		diagnostic.Field{Key: "stack", Value: fmt.Sprintf("%t", selection.Stack)},
		diagnostic.Field{Key: "base", Value: base},
		diagnostic.Field{Key: "remote", Value: remote},
		diagnostic.Field{Key: "branches", Value: strings.Join(branches, ",")},
		diagnostic.Field{Key: "command", Value: diagnostic.SafeCommand("git", append([]string{"push", "--atomic", "--force-with-lease", remote}, branches...))},
	)
	return plan, nil
}

func (s Service) Revalidate(ctx context.Context, selection link.Selection, remote string, preview Plan) (Plan, error) {
	plan, err := s.Plan(ctx, selection, remote)
	if err != nil {
		return Plan{}, err
	}
	if !plan.Equal(preview) {
		diagnostic.Event(ctx, "push.revalidation", diagnostic.Field{Key: "match", Value: "false"})
		return Plan{}, fmt.Errorf("push plan changed during revalidation; rerun without --apply to review the new plan")
	}
	diagnostic.Event(ctx, "push.revalidation", diagnostic.Field{Key: "match", Value: "true"})
	return plan, nil
}

func (s Service) Execute(ctx context.Context, plan Plan) error {
	if s.Git == nil {
		return fmt.Errorf("push service is not fully configured")
	}
	diagnostic.Event(ctx, "push.apply",
		diagnostic.Field{Key: "decision", Value: "run"},
		diagnostic.Field{Key: "remote", Value: plan.Remote},
		diagnostic.Field{Key: "branches", Value: strings.Join(plan.Branches, ",")},
		diagnostic.Field{Key: "command", Value: diagnostic.SafeCommand("git", append([]string{"push", "--atomic", "--force-with-lease", plan.Remote}, plan.Branches...))},
	)
	return s.Git.PushAtomic(ctx, plan.Remote, plan.Branches)
}

func (p Plan) Equal(other Plan) bool {
	return p.Target == other.Target && p.TargetSource == other.TargetSource && p.Base == other.Base && p.BaseSource == other.BaseSource && p.Remote == other.Remote && sameBranches(p.Branches, other.Branches)
}

func target(ctx context.Context, git Git, requested string) (string, string, error) {
	if requested != "" {
		return requested, "--branch", nil
	}
	branch, err := git.CurrentBranch(ctx)
	if err != nil {
		return "", "", err
	}
	return branch, "current Git branch", nil
}

func sameBranches(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

var _ Git = localgit.Client{}
