package submit

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/shhac/gt2gh/internal/githubstack"
	"github.com/shhac/gt2gh/internal/graphite"
	"github.com/shhac/gt2gh/internal/stack"
)

type fakeGraphite struct{ err error }

func (f fakeGraphite) DiscoverStack(context.Context, string, bool) (graphite.Stack, error) {
	return graphite.Stack{
		Path:   []string{"main", "synthetic/lower", "synthetic/middle", "synthetic/top"},
		Trunks: []string{"main"},
	}, f.err
}

func planService(github *fakeGitHub) (Service, *fakeGit) {
	git := &fakeGit{}
	return Service{Git: git, Graphite: fakeGraphite{}, GitHub: github}, git
}

func planFor(t *testing.T, prs []githubstack.PullRequest) Plan {
	t.Helper()
	service, _ := planService(&fakeGitHub{prs: prs})
	plan, err := service.Plan(context.Background(), stack.Selection{}, "origin")
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	return plan
}

func TestPlanResolvesTheSelectedStackAndRemote(t *testing.T) {
	plan := planFor(t, nil)

	if plan.Remote != "origin" || plan.Snapshot.Base != "main" || plan.Snapshot.Target != "synthetic/top" {
		t.Errorf("plan = %#v", plan.Snapshot)
	}
	if got, want := strings.Join(plan.Snapshot.Branches, ","), "synthetic/lower,synthetic/middle,synthetic/top"; got != want {
		t.Errorf("branches = %q, want %q", got, want)
	}
	if len(plan.Issues) != 0 || len(plan.Superseded) != 0 {
		t.Errorf("clean stack reported issues=%v superseded=%v", plan.Issues, plan.Superseded)
	}
}

// assessExisting decides what blocks a submission. Only an ambiguous or
// wrongly-based open pull request does; closed and merged history on a reused
// branch name is recorded as superseded so a replacement is created.
func TestPlanAssessesExistingPullRequests(t *testing.T) {
	tests := []struct {
		name           string
		prs            []githubstack.PullRequest
		wantIssue      string
		wantSuperseded int
	}{
		{
			name: "open pull request on the expected base",
			prs:  []githubstack.PullRequest{{Head: "synthetic/lower", Base: "main", State: "OPEN", Number: 11}},
		},
		{
			name:      "open pull request on the wrong base",
			prs:       []githubstack.PullRequest{{Head: "synthetic/lower", Base: "synthetic/other", State: "OPEN", Number: 11}},
			wantIssue: "PR base synthetic/other, want main",
		},
		{
			name:      "two open pull requests are ambiguous",
			prs:       []githubstack.PullRequest{{Head: "synthetic/lower", Base: "main", State: "OPEN", Number: 11}, {Head: "synthetic/lower", Base: "main", State: "OPEN", Number: 12}},
			wantIssue: "2 open pull requests",
		},
		{
			name:           "closed history does not block",
			prs:            []githubstack.PullRequest{{Head: "synthetic/lower", Base: "main", State: "CLOSED", Number: 9}},
			wantSuperseded: 9,
		},
		{
			name:           "merged history does not block",
			prs:            []githubstack.PullRequest{{Head: "synthetic/lower", Base: "main", State: "MERGED", Number: 8}},
			wantSuperseded: 8,
		},
		{
			name: "closed history alongside the open pull request",
			prs:  []githubstack.PullRequest{{Head: "synthetic/lower", Base: "main", State: "CLOSED", Number: 9}, {Head: "synthetic/lower", Base: "main", State: "OPEN", Number: 21}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := planFor(t, test.prs)

			if got := plan.Issues["synthetic/lower"]; got != test.wantIssue {
				t.Errorf("issue = %q, want %q", got, test.wantIssue)
			}
			superseded, recorded := plan.Superseded["synthetic/lower"]
			if (test.wantSuperseded != 0) != recorded {
				t.Fatalf("superseded recorded = %t, want %t", recorded, test.wantSuperseded != 0)
			}
			if recorded && superseded.Number != test.wantSuperseded {
				t.Errorf("superseded = #%d, want #%d", superseded.Number, test.wantSuperseded)
			}
		})
	}
}

// The expected base walks up the stack, so a branch is judged against its
// predecessor rather than against the trunk.
func TestPlanExpectsEachBranchToSitOnItsPredecessor(t *testing.T) {
	plan := planFor(t, []githubstack.PullRequest{
		{Head: "synthetic/lower", Base: "main", State: "OPEN", Number: 11},
		{Head: "synthetic/middle", Base: "main", State: "OPEN", Number: 12},
	})

	if plan.Issues["synthetic/lower"] != "" {
		t.Errorf("bottom branch blocked: %q", plan.Issues["synthetic/lower"])
	}
	if got, want := plan.Issues["synthetic/middle"], "PR base main, want synthetic/lower"; got != want {
		t.Errorf("issue = %q, want %q", got, want)
	}
}

// A branch whose only pull request is closed must get a replacement. Keying
// creation off any match rather than an open one skipped it, and the stack
// then failed at the link step.
func TestSupersededBranchIsSubmittedAgain(t *testing.T) {
	github := &fakeGitHub{prs: []githubstack.PullRequest{{Head: "synthetic/lower", Base: "main", State: "CLOSED", Number: 9}}}
	service, git := planService(github)
	plan, err := service.Plan(context.Background(), stack.Selection{}, "origin")
	if err != nil {
		t.Fatal(err)
	}
	spec := Spec{Version: 1, Draft: true, Pulls: []Pull{
		{Branch: "synthetic/lower", Title: "lower"},
		{Branch: "synthetic/middle", Title: "middle"},
		{Branch: "synthetic/top", Title: "top"},
	}}

	if err := service.Apply(context.Background(), plan, spec); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if got, want := strings.Join(github.created, ","), "synthetic/lower<-main,synthetic/middle<-synthetic/lower,synthetic/top<-synthetic/middle"; got != want {
		t.Errorf("created = %q, want %q", got, want)
	}
	if git.pushes != 1 || github.links != 1 {
		t.Errorf("pushes=%d links=%d", git.pushes, github.links)
	}
}

func TestRevalidateRequiresACleanWorktreeBeforeReadingAnything(t *testing.T) {
	github := &fakeGitHub{}
	service, git := planService(github)
	git.cleanErr = errors.New("working tree is not clean")

	if _, err := service.Revalidate(context.Background(), stack.Selection{}, "origin", Plan{}); err == nil {
		t.Fatal("Revalidate() = nil, want error")
	}
	if github.inspections != 0 {
		t.Errorf("GitHub was read for a dirty worktree: %d inspections", github.inspections)
	}
}

func TestRevalidateAcceptsAnUnchangedPlan(t *testing.T) {
	github := &fakeGitHub{prs: []githubstack.PullRequest{{Head: "synthetic/lower", Base: "main", State: "OPEN", Number: 11}}}
	service, _ := planService(github)
	preview, err := service.Plan(context.Background(), stack.Selection{}, "origin")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := service.Revalidate(context.Background(), stack.Selection{}, "origin", preview); err != nil {
		t.Fatalf("Revalidate() error = %v", err)
	}
}

// Revalidation is the gate immediately before the most dangerous mutation in
// the tool, so GitHub changing under a preview must abort rather than apply a
// plan the user never saw.
func TestRevalidateRejectsAPlanThatChangedUnderneath(t *testing.T) {
	github := &fakeGitHub{
		prs:      []githubstack.PullRequest{{Head: "synthetic/lower", Base: "main", State: "OPEN", Number: 11}},
		later:    []githubstack.PullRequest{{Head: "synthetic/lower", Base: "synthetic/other", State: "OPEN", Number: 11}},
		laterSet: true,
	}
	service, _ := planService(github)
	preview, err := service.Plan(context.Background(), stack.Selection{}, "origin")
	if err != nil {
		t.Fatal(err)
	}

	_, err = service.Revalidate(context.Background(), stack.Selection{}, "origin", preview)
	if err == nil || !strings.Contains(err.Error(), "changed during revalidation") {
		t.Fatalf("Revalidate() error = %v, want a revalidation mismatch", err)
	}
}

func TestPlanEqualComparesEveryFactThatAffectsTheMutation(t *testing.T) {
	base := Plan{
		Snapshot:   stack.Snapshot{Target: "synthetic/top", Base: "main", Branches: []string{"synthetic/lower"}},
		Remote:     "origin",
		Existing:   []githubstack.PullRequest{{Head: "synthetic/lower", Number: 11, State: "OPEN"}},
		Issues:     map[string]string{},
		Superseded: map[string]githubstack.PullRequest{},
	}
	if !base.Equal(base) {
		t.Fatal("Equal() = false for an identical plan")
	}

	for name, mutate := range map[string]func(Plan) Plan{
		"remote":    func(p Plan) Plan { p.Remote = "upstream"; return p },
		"target":    func(p Plan) Plan { p.Snapshot.Target = "synthetic/other"; return p },
		"base":      func(p Plan) Plan { p.Snapshot.Base = "synthetic/other"; return p },
		"branches":  func(p Plan) Plan { p.Snapshot.Branches = []string{"synthetic/other"}; return p },
		"branchLen": func(p Plan) Plan { p.Snapshot.Branches = nil; return p },
		"existing": func(p Plan) Plan {
			p.Existing = []githubstack.PullRequest{{Head: "synthetic/lower", Number: 12}}
			return p
		},
		"issues": func(p Plan) Plan { p.Issues = map[string]string{"synthetic/lower": "2 open pull requests"}; return p },
		"superseded": func(p Plan) Plan {
			p.Superseded = map[string]githubstack.PullRequest{"synthetic/lower": {Number: 9}}
			return p
		},
	} {
		t.Run(name, func(t *testing.T) {
			if base.Equal(mutate(base)) {
				t.Errorf("Equal() = true despite a differing %s", name)
			}
		})
	}
}

func TestPlanRejectsAnUnconfiguredServiceAndUnknownRemote(t *testing.T) {
	if _, err := (Service{}).Plan(context.Background(), stack.Selection{}, "origin"); err == nil {
		t.Error("Plan() on an unconfigured service = nil, want error")
	}

	github := &fakeGitHub{}
	service, git := planService(github)
	git.remoteErr = errors.New("no such remote")
	if _, err := service.Plan(context.Background(), stack.Selection{}, "synthetic"); err == nil {
		t.Error("Plan() with an unknown remote = nil, want error")
	}
	if github.inspections != 0 {
		t.Errorf("GitHub was read before the remote was validated: %d inspections", github.inspections)
	}
}
