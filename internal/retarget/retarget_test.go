package retarget

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/stack"
	"github.com/shhac/g2g/internal/testutil"
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
		Parents:  map[string]string{"synthetic-lower": "synthetic-trunk", "synthetic-top": "synthetic-lower"},
	}, nil
}

// fakeGitHub answers a retarget the way GitHub answers one document: every
// change is attempted, a refused one fails alone, and a whole failure fails
// them all.
type fakeGitHub struct {
	prs []githubstack.PullRequest
	// calls holds each call's changes, as "#n:id->base".
	calls [][]string
	// refuse names the pull requests GitHub declines; whole fails the call.
	refuse map[int]error
	whole  error
}

func (f *fakeGitHub) Inspect(context.Context, []string) ([]githubstack.PullRequest, error) {
	return f.prs, nil
}

func (f *fakeGitHub) Retarget(_ context.Context, changes []githubstack.BaseChange) ([]error, error) {
	call := make([]string, 0, len(changes))
	outcomes := make([]error, len(changes))
	for index, change := range changes {
		call = append(call, fmt.Sprintf("#%d:%s->%s", change.Number, change.ID, change.Base))
		outcomes[index] = f.refuse[change.Number]
		if f.whole != nil {
			outcomes[index] = f.whole
		}
	}
	f.calls = append(f.calls, call)
	return outcomes, f.whole
}

func (f *fakeGitHub) retargeted() string {
	calls := make([]string, 0, len(f.calls))
	for _, call := range f.calls {
		calls = append(calls, strings.Join(call, ";"))
	}
	return strings.Join(calls, " | ")
}

func service(prs []githubstack.PullRequest) (Service, *fakeGitHub) {
	github := &fakeGitHub{prs: prs}
	return Service{Git: fakeGit{}, Selector: fakeSelector{}, GitHub: github}, github
}

func open(number int, head, base string) githubstack.PullRequest {
	return githubstack.PullRequest{ID: fmt.Sprintf("PR_synthetic_%d", number), Number: number, Head: head, Base: base, State: "OPEN"}
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
	moved := make([]string, 0, len(plan.Changes))
	for _, change := range plan.Changes {
		moved = append(moved, change.Branch)
	}
	if got := strings.Join(moved, ","); got != "synthetic-top" {
		t.Fatalf("moved %s, want only the branch whose base is wrong", got)
	}
	change := plan.Changes[0]
	if change.From != "synthetic-trunk" || change.To != "synthetic-lower" || change.Number != 2 {
		t.Errorf("change = %+v, want #2 moved from the trunk to synthetic-lower", change)
	}

	if err := svc.Execute(context.Background(), plan); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got := github.retargeted(); got != "#2:PR_synthetic_2->synthetic-lower" {
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
	if len(github.calls) != 0 {
		t.Errorf("retargeted %v, want nothing", github.calls)
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
	if plan.Blocked() == "" {
		t.Fatal("Blocked = empty for a branch with two open pull requests")
	}
	if got := strings.Join(plan.Ambiguous, ","); got != "synthetic-top" {
		t.Errorf("Ambiguous = %s", got)
	}
	if err := svc.Execute(context.Background(), plan); err == nil {
		t.Error("Execute() error = nil for a blocked plan")
	}
	if len(github.calls) != 0 {
		t.Errorf("a blocked plan retargeted %v", github.calls)
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

	if _, err := testutil.Replan(preview)(svc.Plan(context.Background(), stack.Selection{})); !errors.Is(err, testutil.ErrReplanned) {
		t.Error("Replan() error = nil after the bases moved underneath")
	}
}

func TestAnUnconfiguredServiceRefuses(t *testing.T) {
	if _, err := (Service{}).Plan(context.Background(), stack.Selection{}); err == nil {
		t.Error("Plan() error = nil on an unconfigured service")
	}
}

// twoMoves is a plan with both bases wrong, so what one refusal does to the
// other is observable.
func twoMoves(t *testing.T) (Service, *fakeGitHub, Plan) {
	t.Helper()
	svc, github := service([]githubstack.PullRequest{
		open(1, "synthetic-lower", "synthetic-elsewhere"),
		open(2, "synthetic-top", "synthetic-trunk"),
	})
	plan, err := svc.Plan(context.Background(), stack.Selection{})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if len(plan.Changes) != 2 {
		t.Fatalf("Changes = %+v, want two", plan.Changes)
	}
	return svc, github, plan
}

// Every base moves in one request, each addressed by the node id the plan read.
func TestExecuteMovesEveryBaseInOneRequest(t *testing.T) {
	svc, github, plan := twoMoves(t)
	if err := svc.Execute(context.Background(), plan); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got := github.retargeted(); got != "#1:PR_synthetic_1->synthetic-trunk;#2:PR_synthetic_2->synthetic-lower" {
		t.Errorf("retargeted %q, want both in one call, bottom-up", got)
	}
}

// GitHub applies the rest of a request after refusing one change, so a
// refusal no longer stops the run. The first change failing and the second
// moving is the case that used to stop before the second was tried: now it
// moved, it stays, and the run is a stop part-way naming the one that did not.
func TestExecuteReportsEveryChangeItCouldNotMake(t *testing.T) {
	svc, github, plan := twoMoves(t)
	github.refuse = map[int]error{1: fmt.Errorf("synthetic refusal of #1")}

	err := svc.Execute(context.Background(), plan)
	var stopped *Stopped
	if !errors.As(err, &stopped) {
		t.Fatalf("Execute() error = %v, want a stop part-way", err)
	}
	if len(stopped.Moved) != 1 || stopped.Moved[0].Number != 2 {
		t.Errorf("Moved = %+v, want #2", stopped.Moved)
	}
	if len(stopped.Failed) != 1 || stopped.Failed[0].Change.Number != 1 || !strings.Contains(stopped.Failed[0].Err.Error(), "synthetic refusal of #1") {
		t.Errorf("Failed = %+v, want #1 with GitHub's reason", stopped.Failed)
	}
	if !strings.Contains(err.Error(), "#1: synthetic refusal of #1") {
		t.Errorf("error = %v, want the refused change named with its reason", err)
	}
	if len(github.calls) != 1 {
		t.Errorf("made %d calls, want one", len(github.calls))
	}
}

// Nothing moved is the ordinary failure, whether GitHub refused each change
// or the request failed as a whole, and it names every change it could not
// make.
func TestExecuteThatMovesNothingIsAnOrdinaryFailure(t *testing.T) {
	whole := fmt.Errorf("synthetic request failure")
	for name, arrange := range map[string]func(*fakeGitHub){
		"each refused": func(github *fakeGitHub) {
			github.refuse = map[int]error{1: fmt.Errorf("synthetic refusal"), 2: fmt.Errorf("synthetic refusal")}
		},
		"whole request": func(github *fakeGitHub) { github.whole = whole },
	} {
		t.Run(name, func(t *testing.T) {
			svc, github, plan := twoMoves(t)
			arrange(github)
			err := svc.Execute(context.Background(), plan)
			var stopped *Stopped
			if err == nil || errors.As(err, &stopped) {
				t.Fatalf("Execute() = %v, want an ordinary failure", err)
			}
			if github.whole != nil && !errors.Is(err, whole) {
				t.Errorf("error = %v, want the request's own failure", err)
			}
			if github.whole == nil && !strings.Contains(err.Error(), "#1 and #2: synthetic refusal") {
				t.Errorf("error = %v, want both named once with their shared reason", err)
			}
		})
	}
}
