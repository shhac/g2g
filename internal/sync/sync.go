// Package sync implements Graphite-authoritative, conservative GitHub stack
// reconciliation.
package sync

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/shhac/gt2gh/internal/diagnostic"
	"github.com/shhac/gt2gh/internal/githubstack"
	"github.com/shhac/gt2gh/internal/stack"
)

// Git supplies local repository facts and the clean-worktree precondition.
type Git interface {
	stack.Git
	Clean(context.Context) error
}

// GitHub reads pull requests and performs the one reconciliation mutation.
type GitHub interface {
	stack.GitHub
	Link(context.Context, string, []string) error
}

// Service reconciles only a fully mapped, open GitHub PR path. It never
// creates mappings for Graphite-only branches or repairs closed PRs.
type Service struct {
	Git      Git
	Graphite stack.Graphite
	GitHub   GitHub
}

// State describes a single Graphite branch's existing GitHub relationship.
type State string

const (
	Aligned   State = "aligned"
	Divergent State = "divergent"
	Missing   State = "missing"
	Unsafe    State = "unsafe"
)

// Item compares one Graphite branch to the expected GitHub PR base.
type Item struct {
	Branch       string
	ExpectedBase string
	State        State
	PullRequest  *githubstack.PullRequest
}

// Plan is a Graphite-authoritative reconciliation preview.
type Plan struct {
	Discovery stack.Discovery
	Items     []Item
}

// Preview discovers the selected path and classifies GitHub's existing PR
// relationship. It is entirely read-only.
func (s Service) Preview(ctx context.Context, selection stack.Selection) (Plan, error) {
	if s.Git == nil || s.Graphite == nil || s.GitHub == nil {
		return Plan{}, fmt.Errorf("sync service is not fully configured")
	}
	discovery, err := stack.Discover(ctx, s.Git, s.Graphite, s.GitHub, selection, "gh stack link")
	if err != nil {
		return Plan{}, err
	}
	items, err := classify(discovery)
	if err != nil {
		return Plan{}, err
	}
	result := Plan{Discovery: discovery, Items: items}
	diagnostic.Event(ctx, "sync.plan", diagnostic.Field{Key: "decision", Value: syncDecision(result)}, diagnostic.Field{Key: "summary", Value: result.Summary()}, diagnostic.Field{Key: "states", Value: syncStates(result.Items)})
	return result, nil
}

// Revalidate repeats discovery immediately before a mutation and refuses if
// the result differs from the rendered preview. Callers run Execute
// themselves, matching the CLI's render-and-flush-between sequence.
func (s Service) Revalidate(ctx context.Context, selection stack.Selection, preview Plan) (Plan, error) {
	if s.Git == nil || s.Graphite == nil || s.GitHub == nil {
		return Plan{}, fmt.Errorf("sync service is not fully configured")
	}
	if err := s.Git.Clean(ctx); err != nil {
		return Plan{}, err
	}
	plan, err := s.Preview(ctx, selection)
	if err != nil {
		return Plan{}, err
	}
	if !samePlan(plan, preview) {
		diagnostic.Event(ctx, "sync.revalidation", diagnostic.Field{Key: "match", Value: "false"})
		return Plan{}, fmt.Errorf("sync plan changed during revalidation; rerun without --apply to review the new plan")
	}
	diagnostic.Event(ctx, "sync.revalidation", diagnostic.Field{Key: "match", Value: "true"})
	if err := plan.applyBlocker(); err != nil {
		return Plan{}, err
	}
	return plan, nil
}

// Execute invokes the one permitted reconciliation after a caller has
// revalidated and presented the exact plan.
func (s Service) Execute(ctx context.Context, plan Plan) error {
	if s.GitHub == nil {
		return fmt.Errorf("sync service is not fully configured")
	}
	if err := plan.applyBlocker(); err != nil {
		return err
	}
	if plan.NothingToSync() {
		diagnostic.Event(ctx, "sync.apply", diagnostic.Field{Key: "decision", Value: "skipped"}, diagnostic.Field{Key: "reason", Value: "fewer_than_two_pr_branches"})
		return nil
	}
	diagnostic.Event(ctx, "sync.apply", diagnostic.Field{Key: "decision", Value: "run"}, diagnostic.Field{Key: "base", Value: plan.Discovery.Base}, diagnostic.Field{Key: "branches", Value: strings.Join(plan.Discovery.Branches, ",")})
	return s.GitHub.Link(ctx, plan.Discovery.Base, plan.Discovery.Branches)
}

func syncDecision(plan Plan) string {
	if !plan.CanApply() {
		return "blocked"
	}
	if plan.NothingToSync() {
		return "no_op"
	}
	return "ready"
}

func syncStates(items []Item) string {
	parts := make([]string, len(items))
	for index, item := range items {
		parts[index] = item.Branch + ": " + string(item.State)
	}
	return strings.Join(parts, "; ")
}

func classify(discovery stack.Discovery) ([]Item, error) {
	resolutions := githubstack.ResolveHeads(discovery.PullRequests)
	for _, branch := range discovery.Branches {
		if resolutions[branch].Ambiguous() {
			return nil, fmt.Errorf("GitHub has %d open pull requests for branch %q; refusing ambiguous sync", resolutions[branch].OpenCount, branch)
		}
	}
	items := make([]Item, 0, len(discovery.Branches))
	for step := range githubstack.Along(discovery.Base, discovery.Branches, discovery.PullRequests) {
		resolution := step.Resolution
		item := Item{Branch: step.Branch, ExpectedBase: step.ExpectedBase, State: Missing}
		switch {
		case resolution.Open != nil:
			item.PullRequest = resolution.Open
			if resolution.Open.Base == step.ExpectedBase {
				item.State = Aligned
			} else {
				item.State = Divergent
			}
		case resolution.Superseded():
			// The branch's pull requests are all closed or merged. Reconciling
			// one is out of scope, so this stays an explicit refusal.
			item.PullRequest = resolution.Latest
			item.State = Unsafe
		}
		items = append(items, item)
	}
	return items, nil
}

func samePlan(left, right Plan) bool {
	return left.Discovery.Equal(right.Discovery) && slices.EqualFunc(left.Items, right.Items, sameItem)
}

func sameItem(left, right Item) bool {
	if left.Branch != right.Branch || left.ExpectedBase != right.ExpectedBase || left.State != right.State {
		return false
	}
	if left.PullRequest == nil || right.PullRequest == nil {
		return left.PullRequest == right.PullRequest
	}
	return *left.PullRequest == *right.PullRequest
}

// Summary returns concise state counts for a preview.
func (p Plan) Summary() string {
	counts := make(map[State]int)
	for _, item := range p.Items {
		counts[item.State]++
	}
	return fmt.Sprintf("aligned=%d divergent=%d missing=%d unsafe=%d", counts[Aligned], counts[Divergent], counts[Missing], counts[Unsafe])
}

// CanApply explains whether the conservative v0.2 scope permits apply.
func (p Plan) CanApply() bool {
	return p.applyBlocker() == nil
}

// NothingToSync reports an apply-eligible path too short for gh stack link.
// It is a successful no-op, never an invitation to execute an invalid command.
func (p Plan) NothingToSync() bool {
	return p.CanApply() && len(p.Discovery.Branches) < 2
}

func (p Plan) applyBlocker() error {
	for _, item := range p.Items {
		switch item.State {
		case Missing:
			return fmt.Errorf("branch %q has no GitHub pull request; refusing to create a mapping during sync", item.Branch)
		case Unsafe:
			return fmt.Errorf("branch %q has a non-open GitHub pull request; refusing automatic repair", item.Branch)
		}
	}
	return nil
}
