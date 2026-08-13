// Package sync implements Graphite-authoritative, conservative GitHub stack
// reconciliation.
package sync

import (
	"context"
	"fmt"
	"strings"

	"github.com/shhac/gt2gh/internal/diagnostic"
	"github.com/shhac/gt2gh/internal/githubstack"
	"github.com/shhac/gt2gh/internal/link"
)

// Discoverer supplies the common, read-only Graphite and GitHub facts.
type Discoverer interface {
	DiscoverWithTrunk(context.Context, string, string) (link.Plan, error)
	DiscoverWithOptions(context.Context, link.Selection) (link.Plan, error)
}

// Git supplies the local precondition needed before an explicit apply.
type Git interface {
	Clean(context.Context) error
}

// GitHub provides the one deliberate reconciliation mutation.
type GitHub interface {
	Link(context.Context, string, []string) error
}

// Service reconciles only a fully mapped, open GitHub PR path. It never
// creates mappings for Graphite-only branches or repairs closed PRs.
type Service struct {
	Discoverer Discoverer
	Git        Git
	GitHub     GitHub
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
	Link  link.Plan
	Items []Item
}

// Preview discovers the selected path and classifies GitHub's existing PR
// relationship. It is entirely read-only.
func (s Service) Preview(ctx context.Context, requestedBranch string) (Plan, error) {
	return s.PreviewWithTrunk(ctx, requestedBranch, "")
}

func (s Service) PreviewWithTrunk(ctx context.Context, requestedBranch, requestedTrunk string) (Plan, error) {
	return s.PreviewWithOptions(ctx, link.Selection{Branch: requestedBranch, Trunk: requestedTrunk})
}

func (s Service) PreviewWithOptions(ctx context.Context, selection link.Selection) (Plan, error) {
	if s.Discoverer == nil || s.Git == nil || s.GitHub == nil {
		return Plan{}, fmt.Errorf("sync service is not fully configured")
	}
	plan, err := s.Discoverer.DiscoverWithOptions(ctx, selection)
	if err != nil {
		return Plan{}, err
	}
	items, err := classify(plan)
	if err != nil {
		return Plan{}, err
	}
	result := Plan{Link: plan, Items: items}
	diagnostic.Event(ctx, "sync.plan", diagnostic.Field{Key: "decision", Value: syncDecision(result)}, diagnostic.Field{Key: "summary", Value: result.Summary()}, diagnostic.Field{Key: "states", Value: syncStates(result.Items)})
	return result, nil
}

// Apply revalidates the preview, then asks gh to update the native stack only
// when every Graphite branch is already represented by an open PR. This avoids
// silently creating mappings or repairing closed/ambiguous state.
func (s Service) Apply(ctx context.Context, requestedBranch string, preview Plan) (Plan, error) {
	return s.ApplyWithTrunk(ctx, requestedBranch, "", preview)
}

func (s Service) ApplyWithTrunk(ctx context.Context, requestedBranch, requestedTrunk string, preview Plan) (Plan, error) {
	return s.ApplyWithOptions(ctx, link.Selection{Branch: requestedBranch, Trunk: requestedTrunk}, preview)
}

func (s Service) ApplyWithOptions(ctx context.Context, selection link.Selection, preview Plan) (Plan, error) {
	plan, err := s.RevalidateWithOptions(ctx, selection, preview)
	if err != nil {
		return Plan{}, err
	}
	if err := s.Execute(ctx, plan); err != nil {
		return Plan{}, err
	}
	return plan, nil
}

// RevalidateWithTrunk confirms that the preview remains eligible immediately
// before the caller renders its final plan and invokes the sole mutation.
func (s Service) RevalidateWithTrunk(ctx context.Context, requestedBranch, requestedTrunk string, preview Plan) (Plan, error) {
	return s.RevalidateWithOptions(ctx, link.Selection{Branch: requestedBranch, Trunk: requestedTrunk}, preview)
}

func (s Service) RevalidateWithOptions(ctx context.Context, selection link.Selection, preview Plan) (Plan, error) {
	if s.Discoverer == nil || s.Git == nil || s.GitHub == nil {
		return Plan{}, fmt.Errorf("sync service is not fully configured")
	}
	if err := s.Git.Clean(ctx); err != nil {
		return Plan{}, err
	}
	plan, err := s.PreviewWithOptions(ctx, selection)
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
	diagnostic.Event(ctx, "sync.apply", diagnostic.Field{Key: "decision", Value: "run"}, diagnostic.Field{Key: "base", Value: plan.Link.Base}, diagnostic.Field{Key: "branches", Value: strings.Join(plan.Link.Branches, ",")})
	return s.GitHub.Link(ctx, plan.Link.Base, plan.Link.Branches)
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

func classify(plan link.Plan) ([]Item, error) {
	byBranch := make(map[string]githubstack.PullRequest, len(plan.PullRequests))
	for _, pr := range plan.PullRequests {
		if _, exists := byBranch[pr.Head]; exists {
			return nil, fmt.Errorf("GitHub returned multiple pull requests for branch %q; refusing ambiguous sync", pr.Head)
		}
		byBranch[pr.Head] = pr
	}
	base := plan.Base
	items := make([]Item, 0, len(plan.Branches))
	for _, branch := range plan.Branches {
		item := Item{Branch: branch, ExpectedBase: base, State: Missing}
		if pr, exists := byBranch[branch]; exists {
			item.PullRequest = &pr
			switch {
			case pr.State != "OPEN":
				item.State = Unsafe
			case pr.Base == base:
				item.State = Aligned
			default:
				item.State = Divergent
			}
		}
		items = append(items, item)
		base = branch
	}
	return items, nil
}

func samePlan(left, right Plan) bool {
	if !left.Link.Equal(right.Link) || len(left.Items) != len(right.Items) {
		return false
	}
	for index := range left.Items {
		if left.Items[index].Branch != right.Items[index].Branch || left.Items[index].ExpectedBase != right.Items[index].ExpectedBase || left.Items[index].State != right.Items[index].State || !samePullRequest(left.Items[index].PullRequest, right.Items[index].PullRequest) {
			return false
		}
	}
	return true
}

func samePullRequest(left, right *githubstack.PullRequest) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
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
	return p.CanApply() && p.Link.NothingToLink()
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
