// Package push implements the explicit Git-only stack-ref escape hatch.
package push

import (
	"context"
	"fmt"
	"strings"

	"github.com/shhac/gt2gh/internal/diagnostic"
	localgit "github.com/shhac/gt2gh/internal/git"
	"github.com/shhac/gt2gh/internal/stack"
)

type Git interface {
	stack.Git
	Remote(context.Context, string) error
	PushAtomic(context.Context, string, []string) error
}

type Graphite interface {
	stack.Graphite
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

func (s Service) Plan(ctx context.Context, selection stack.Selection, remote string) (Plan, error) {
	if s.Git == nil || s.Graphite == nil {
		return Plan{}, fmt.Errorf("push service is not fully configured")
	}
	if err := s.Git.Remote(ctx, remote); err != nil {
		return Plan{}, err
	}
	snapshot, err := stack.Resolve(ctx, s.Git, s.Graphite, selection, "git push")
	if err != nil {
		return Plan{}, err
	}
	if len(snapshot.Branches) == 0 {
		return Plan{}, fmt.Errorf("selected Graphite path has no non-trunk branches to push")
	}
	plan := Plan{Target: snapshot.Target, TargetSource: snapshot.TargetSource, Base: snapshot.Base, BaseSource: snapshot.BaseSource, Branches: snapshot.Branches, Remote: remote}
	diagnostic.Event(ctx, "push.plan",
		diagnostic.Field{Key: "decision", Value: "ready"},
		diagnostic.Field{Key: "target", Value: snapshot.Target},
		diagnostic.Field{Key: "target_source", Value: snapshot.TargetSource},
		diagnostic.Field{Key: "full_stack", Value: fmt.Sprintf("%t", !selection.NoStack)},
		diagnostic.Field{Key: "base", Value: snapshot.Base},
		diagnostic.Field{Key: "remote", Value: remote},
		diagnostic.Field{Key: "branches", Value: strings.Join(snapshot.Branches, ",")},
		diagnostic.Field{Key: "command", Value: diagnostic.SafeCommand("git", append([]string{"push", "--atomic", "--force-with-lease", remote}, snapshot.Branches...))},
	)
	return plan, nil
}

func (s Service) Revalidate(ctx context.Context, selection stack.Selection, remote string, preview Plan) (Plan, error) {
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
