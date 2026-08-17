package retarget

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/stack"
)

type fakeGit struct{}

func (fakeGit) CurrentBranch(context.Context) (string, error) { return "synthetic-top", nil }
func (fakeGit) LocalBranches(context.Context) ([]string, error) {
	return []string{"synthetic-trunk", "synthetic-lower", "synthetic-top"}, nil
}

// fakeSelector supplies a fixed resolved path, so these tests are about what
// retarget decides rather than about how a stack is selected.
type fakeSelector struct{}

func (fakeSelector) Select(context.Context, stack.Selection, string) (stack.Snapshot, error) {
	return stack.Snapshot{
		Target:   "synthetic-top",
		Base:     "synthetic-trunk",
		Branches: []string{"synthetic-lower", "synthetic-top"},
	}, nil
}

type fakeGitHub struct {
	prs       []githubstack.PullRequest
	retargets []string
	err       error
	// failAfter makes the (failAfter+1)th call fail, so a test can reach the
	// partial-success case rather than only the first-call-fails one.
	failAfter int
}

func (f *fakeGitHub) Inspect(context.Context, []string) ([]githubstack.PullRequest, error) {
	return f.prs, nil
}

func (f *fakeGitHub) Retarget(_ context.Context, number int, base string) error {
	f.retargets = append(f.retargets, fmt.Sprintf("#%d->%s", number, base))
	if f.err != nil && len(f.retargets) > f.failAfter {
		return f.err
	}
	return nil
}

func service(prs []githubstack.PullRequest) (Service, *fakeGitHub) {
	github := &fakeGitHub{prs: prs}
	return Service{Git: fakeGit{}, Selector: fakeSelector{}, GitHub: github}, github
}

func open(number int, head, base string) githubstack.PullRequest {
	return githubstack.PullRequest{Number: number, Head: head, Base: base, State: "OPEN"}
}

// The case the command exists for: a restack moved the stack and GitHub still
// records where each pull request used to sit.
func TestPlanMovesABaseThatNoLongerMatchesTheStack(t *testing.T) {
	svc, github := service([]githubstack.PullRequest{
		open(1, "synthetic-lower", "synthetic-trunk"),
		open(2, "synthetic-top", "synthetic-trunk"),
	})

	plan, err := svc.Plan(context.Background(), stack.Selection{})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if got := strings.Join(plan.Retargeting(), ","); got != "synthetic-top" {
		t.Fatalf("Retargeting() = %s, want only the branch whose base is wrong", got)
	}
	change := plan.Changes[0]
	if change.From != "synthetic-trunk" || change.To != "synthetic-lower" || change.Number != 2 {
		t.Errorf("change = %+v, want #2 moved from the trunk to synthetic-lower", change)
	}

	if err := svc.Execute(context.Background(), plan); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got := strings.Join(github.retargets, ";"); got != "#2->synthetic-lower" {
		t.Errorf("retargeted %q, want only the mismatched pull request", got)
	}
}

// A stack GitHub already agrees with is left alone, which is what makes this
// safe to run after every restack.
func TestPlanDoesNothingWhenEveryBaseAlreadyMatches(t *testing.T) {
	svc, github := service([]githubstack.PullRequest{
		open(1, "synthetic-lower", "synthetic-trunk"),
		open(2, "synthetic-top", "synthetic-lower"),
	})

	plan, err := svc.Plan(context.Background(), stack.Selection{})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if !plan.NothingToRetarget() {
		t.Errorf("plan has work: %+v", plan.Changes)
	}
	if err := svc.Execute(context.Background(), plan); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(github.retargets) != 0 {
		t.Errorf("retargeted %v, want nothing", github.retargets)
	}
}

// A branch with no pull request has no base to move. Creating one is submit's
// job, and doing it here would make a preview that said "moves a base" create
// a pull request instead.
func TestPlanIgnoresABranchWithNoPullRequest(t *testing.T) {
	svc, _ := service([]githubstack.PullRequest{open(1, "synthetic-lower", "synthetic-trunk")})

	plan, err := svc.Plan(context.Background(), stack.Selection{})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if !plan.NothingToRetarget() {
		t.Errorf("plan has work for a branch with no pull request: %+v", plan.Changes)
	}
}

// Two open pull requests for one branch is the one ambiguity nothing here
// resolves, so it blocks rather than picking.
func TestPlanBlocksOnAnAmbiguousBranch(t *testing.T) {
	svc, github := service([]githubstack.PullRequest{
		open(1, "synthetic-lower", "synthetic-trunk"),
		open(2, "synthetic-top", "synthetic-trunk"),
		open(3, "synthetic-top", "synthetic-trunk"),
	})

	plan, err := svc.Plan(context.Background(), stack.Selection{})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if plan.Blocked == "" {
		t.Fatal("Blocked = empty for a branch with two open pull requests")
	}
	if got := strings.Join(plan.Ambiguous, ","); got != "synthetic-top" {
		t.Errorf("Ambiguous = %s", got)
	}
	if err := svc.Execute(context.Background(), plan); err == nil {
		t.Error("Execute() error = nil for a blocked plan")
	}
	if len(github.retargets) != 0 {
		t.Errorf("a blocked plan retargeted %v", github.retargets)
	}
}

// A merged or closed pull request is history, not something to retarget.
func TestPlanIgnoresASupersededPullRequest(t *testing.T) {
	svc, _ := service([]githubstack.PullRequest{
		open(1, "synthetic-lower", "synthetic-trunk"),
		{Number: 2, Head: "synthetic-top", Base: "synthetic-trunk", State: "MERGED"},
	})

	plan, err := svc.Plan(context.Background(), stack.Selection{})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if !plan.NothingToRetarget() {
		t.Errorf("plan has work for a merged pull request: %+v", plan.Changes)
	}
}

// Stopping at the first refusal leaves the bases that did move where they now
// correctly are; putting them back would undo the only part that worked.
func TestExecuteStopsAtTheFirstRefusal(t *testing.T) {
	svc, github := service([]githubstack.PullRequest{
		open(1, "synthetic-lower", "synthetic-elsewhere"),
		open(2, "synthetic-top", "synthetic-trunk"),
	})
	github.err = fmt.Errorf("synthetic GitHub refusal")

	plan, err := svc.Plan(context.Background(), stack.Selection{})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if len(plan.Changes) != 2 {
		t.Fatalf("Changes = %+v, want two so a stop is observable", plan.Changes)
	}
	if err := svc.Execute(context.Background(), plan); err == nil {
		t.Fatal("Execute() error = nil when GitHub refused")
	}
	if len(github.retargets) != 1 {
		t.Errorf("made %d calls, want to stop after the first refusal", len(github.retargets))
	}
}

func TestRevalidateRefusesAChangedPlan(t *testing.T) {
	svc, github := service([]githubstack.PullRequest{
		open(1, "synthetic-lower", "synthetic-trunk"),
		open(2, "synthetic-top", "synthetic-trunk"),
	})

	preview, err := svc.Plan(context.Background(), stack.Selection{})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	github.prs = []githubstack.PullRequest{
		open(1, "synthetic-lower", "synthetic-trunk"),
		open(2, "synthetic-top", "synthetic-lower"),
	}

	if _, err := svc.Revalidate(context.Background(), stack.Selection{}, preview); err == nil {
		t.Error("Revalidate() error = nil after the bases moved underneath")
	}
}

func TestAnUnconfiguredServiceRefuses(t *testing.T) {
	if _, err := (Service{}).Plan(context.Background(), stack.Selection{}); err == nil {
		t.Error("Plan() error = nil on an unconfigured service")
	}
}

// Execute's doc says it does not unwind, because a base already moved is
// correct and putting it back would undo the only part that worked. The
// existing stop-at-first-refusal test fails on call one, so it never reaches
// the partial success that sentence is actually about.
func TestExecuteLeavesTheBasesThatAlreadyMoved(t *testing.T) {
	svc, github := service([]githubstack.PullRequest{
		open(1, "synthetic-lower", "synthetic-elsewhere"),
		open(2, "synthetic-top", "synthetic-trunk"),
	})
	github.err = fmt.Errorf("synthetic GitHub refusal")
	github.failAfter = 1

	plan, err := svc.Plan(context.Background(), stack.Selection{})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if len(plan.Changes) != 2 {
		t.Fatalf("Changes = %+v, want two so partial success is observable", plan.Changes)
	}

	if err := svc.Execute(context.Background(), plan); err == nil {
		t.Fatal("Execute() error = nil when the second call failed")
	}
	// The first move happened and stays; the second was attempted and failed.
	if got := strings.Join(github.retargets, ";"); got != "#1->synthetic-trunk;#2->synthetic-lower" {
		t.Errorf("retargeted %q, want the first to have moved and the second attempted", got)
	}
}
