// Package push implements the explicit Git-only stack-ref escape hatch.
package push

import (
	"context"
	"fmt"
	"maps"
	"strings"

	"github.com/shhac/gt2gh/internal/diagnostic"
	localgit "github.com/shhac/gt2gh/internal/git"
	"github.com/shhac/gt2gh/internal/stack"
)

type Git interface {
	stack.Git
	Remote(context.Context, string) error
	RemoteTips(context.Context, string, []string) (map[string]string, error)
	PushAtomic(context.Context, string, []localgit.Lease) error
}

type Graphite interface {
	stack.Graphite
}

type Service struct {
	Git      Git
	Graphite Graphite
}

type Plan struct {
	stack.Snapshot
	Remote string
	// RemoteTips is what the remote held when the plan was built. It is the
	// lease the push asserts, so a branch that moved in between is rejected
	// rather than overwritten.
	RemoteTips map[string]string
}

// pushArgs is the exact invocation Execute makes, so a diagnostic never
// advertises a command that differs from the one that runs.
func (p Plan) pushArgs() []string {
	args := []string{"push", "--atomic"}
	for _, lease := range p.Leases() {
		args = append(args, lease.Argument())
	}
	args = append(args, p.Remote)
	return append(args, p.Branches...)
}

// Leases pairs each selected branch with the tip the plan observed for it.
func (p Plan) Leases() []localgit.Lease {
	leases := make([]localgit.Lease, 0, len(p.Branches))
	for _, branch := range p.Branches {
		leases = append(leases, localgit.Lease{Branch: branch, Expected: p.RemoteTips[branch]})
	}
	return leases
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
	tips, err := s.Git.RemoteTips(ctx, remote, snapshot.Branches)
	if err != nil {
		return Plan{}, err
	}
	plan := Plan{Snapshot: snapshot, Remote: remote, RemoteTips: tips}
	diagnostic.Event(ctx, "push.plan",
		diagnostic.Field{Key: "decision", Value: "ready"},
		diagnostic.Field{Key: "target", Value: snapshot.Target},
		diagnostic.Field{Key: "target_source", Value: snapshot.TargetSource},
		diagnostic.Field{Key: "full_stack", Value: fmt.Sprintf("%t", !selection.NoStack)},
		diagnostic.Field{Key: "base", Value: snapshot.Base},
		diagnostic.Field{Key: "remote", Value: remote},
		diagnostic.Field{Key: "branches", Value: strings.Join(snapshot.Branches, ",")},
		diagnostic.Field{Key: "command", Value: diagnostic.SafeCommand("git", plan.pushArgs())},
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
		diagnostic.Field{Key: "command", Value: diagnostic.SafeCommand("git", plan.pushArgs())},
	)
	return s.Git.PushAtomic(ctx, plan.Remote, plan.Leases())
}

// Equal compares every fact that can change which refs are pushed where.
// Equal compares every fact that changes what the push does, including the
// remote tips the leases assert: a branch that moved on the remote between
// preview and apply must stop the push, not be overwritten by it.
func (p Plan) Equal(other Plan) bool {
	return p.Snapshot.Equal(other.Snapshot) &&
		p.Remote == other.Remote &&
		maps.Equal(p.RemoteTips, other.RemoteTips)
}

var _ Git = localgit.Client{}
