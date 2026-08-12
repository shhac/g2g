package link

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/shhac/gt2gh/internal/githubstack"
	"github.com/shhac/gt2gh/internal/graphite"
)

func TestPlanUsesCurrentBranchAndSelectedForkPath(t *testing.T) {
	service := fakeService()
	plan, err := service.Plan(context.Background(), "")
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if got, want := plan.Target, "beta-two-deep"; got != want {
		t.Errorf("target = %q, want %q", got, want)
	}
	if got, want := strings.Join(plan.Branches, ","), "alpha,beta,beta-two,beta-two-deep"; got != want {
		t.Errorf("branches = %q, want %q", got, want)
	}
}

func TestPlanUsesExplicitBranchWithoutCheckout(t *testing.T) {
	service := fakeService()
	plan, err := service.Plan(context.Background(), "gamma-deep")
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if got, want := strings.Join(plan.Branches, ","), "alpha,gamma,gamma-deep"; got != want {
		t.Errorf("branches = %q, want %q", got, want)
	}
}

func TestApplyRevalidatesBeforeGitHubMutation(t *testing.T) {
	github := &fakeGitHub{}
	service := fakeService()
	service.GitHub = github
	preview, err := service.Plan(context.Background(), "beta-two")
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if _, err := service.Apply(context.Background(), "beta-two", preview); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if github.links != 1 {
		t.Fatalf("Link calls = %d, want 1", github.links)
	}
	if got, want := strings.Join(github.branches, ","), "alpha,beta,beta-two"; got != want {
		t.Errorf("linked branches = %q, want %q", got, want)
	}
}

func TestApplyStopsBeforeMutationWhenDirty(t *testing.T) {
	github := &fakeGitHub{}
	service := fakeService()
	service.Git = fakeGit{dirty: errors.New("dirty")}
	service.GitHub = github
	if _, err := service.Apply(context.Background(), "beta-two", Plan{}); err == nil {
		t.Fatal("Apply() error = nil")
	}
	if github.links != 0 {
		t.Errorf("Link calls = %d, want 0", github.links)
	}
}

func TestApplyRejectsPlanChangedDuringRevalidation(t *testing.T) {
	github := &fakeGitHub{}
	service := Service{
		Git:      fakeGit{current: "beta", branches: []string{"main", "other-main", "alpha", "beta"}},
		Graphite: &changingGraphite{},
		GitHub:   github,
	}
	preview, err := service.Plan(context.Background(), "")
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if _, err := service.Apply(context.Background(), "", preview); err == nil || !strings.Contains(err.Error(), "changed during revalidation") {
		t.Fatalf("Apply() error = %v, want plan-change error", err)
	}
	if github.links != 0 {
		t.Errorf("Link calls = %d, want 0", github.links)
	}
}

func TestApplyFailsClosedBeforeGitHubMutation(t *testing.T) {
	cases := []struct {
		name    string
		service Service
	}{
		{"current branch resolution", Service{Git: fakeGit{currentErr: context.Canceled}, Graphite: fakeGraphite{}, GitHub: &fakeGitHub{}}},
		{"local branch listing", Service{Git: fakeGit{current: "beta", branchesErr: context.Canceled}, Graphite: fakeGraphite{}, GitHub: &fakeGitHub{}}},
		{"Graphite discovery", Service{Git: fakeGit{current: "beta", branches: []string{"main", "beta"}}, Graphite: fakeGraphite{discoverErr: context.Canceled}, GitHub: &fakeGitHub{}}},
		{"missing local stack branch", Service{Git: fakeGit{current: "beta", branches: []string{"main", "beta"}}, Graphite: fakeGraphite{paths: map[string]graphite.Stack{"beta": {Trunk: "main", Branches: []string{"missing", "beta"}}}}, GitHub: &fakeGitHub{}}},
		{"GitHub inspection", Service{Git: fakeGit{current: "beta", branches: []string{"main", "alpha", "beta"}}, Graphite: fakeGraphite{paths: map[string]graphite.Stack{"beta": {Trunk: "main", Branches: []string{"alpha", "beta"}}}}, GitHub: &fakeGitHub{inspectErr: context.Canceled}}},
		{"non-open pull request", Service{Git: fakeGit{current: "beta", branches: []string{"main", "alpha", "beta"}}, Graphite: fakeGraphite{paths: map[string]graphite.Stack{"beta": {Trunk: "main", Branches: []string{"alpha", "beta"}}}}, GitHub: &fakeGitHub{prs: []githubstack.PullRequest{{Number: 2, Head: "alpha", Base: "main", State: "MERGED"}}}}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			github := test.service.GitHub.(*fakeGitHub)
			if _, err := test.service.Apply(context.Background(), "", Plan{}); err == nil {
				t.Fatal("Apply() error = nil")
			}
			if github.links != 0 {
				t.Errorf("Link calls = %d, want 0", github.links)
			}
		})
	}
}

func TestPlanRejectsUnsafeOrDivergentGitHubState(t *testing.T) {
	cases := []struct {
		name string
		prs  []githubstack.PullRequest
	}{
		{"duplicate head", []githubstack.PullRequest{{Number: 2, Head: "alpha", Base: "main", State: "OPEN"}, {Number: 3, Head: "alpha", Base: "main", State: "OPEN"}}},
		{"unexpected pull request state", []githubstack.PullRequest{{Number: 2, Head: "alpha", Base: "main", State: "DRAFT"}}},
		{"divergent pull request base", []githubstack.PullRequest{{Number: 2, Head: "beta", Base: "main", State: "OPEN"}}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			service := fakeService()
			service.GitHub = &fakeGitHub{prs: test.prs}
			if _, err := service.Plan(context.Background(), "beta"); err == nil {
				t.Fatal("Plan() error = nil")
			}
		})
	}
}

func TestBranchCompletionsAreLocalTrackedAndSorted(t *testing.T) {
	service := fakeService()
	branches, err := service.BranchCompletions(context.Background(), "beta")
	if err != nil {
		t.Fatalf("BranchCompletions() error = %v", err)
	}
	if got, want := strings.Join(branches, ","), "beta,beta-one,beta-two,beta-two-deep"; got != want {
		t.Errorf("branches = %q, want %q", got, want)
	}
}

func fakeService() Service {
	branches := []string{"main", "alpha", "beta", "beta-one", "beta-two", "beta-two-deep", "gamma", "gamma-deep"}
	return Service{
		Git: fakeGit{current: "beta-two-deep", branches: branches},
		Graphite: fakeGraphite{paths: map[string]graphite.Stack{
			"alpha":         {Trunk: "main", Branches: []string{"alpha"}},
			"beta":          {Trunk: "main", Branches: []string{"alpha", "beta"}},
			"beta-one":      {Trunk: "main", Branches: []string{"alpha", "beta", "beta-one"}},
			"beta-two":      {Trunk: "main", Branches: []string{"alpha", "beta", "beta-two"}},
			"beta-two-deep": {Trunk: "main", Branches: []string{"alpha", "beta", "beta-two", "beta-two-deep"}},
			"gamma":         {Trunk: "main", Branches: []string{"alpha", "gamma"}},
			"gamma-deep":    {Trunk: "main", Branches: []string{"alpha", "gamma", "gamma-deep"}},
		}, tracked: branches[1:]},
		GitHub: &fakeGitHub{},
	}
}

type fakeGit struct {
	current     string
	currentErr  error
	branches    []string
	branchesErr error
	dirty       error
}

func (f fakeGit) CurrentBranch(context.Context) (string, error)   { return f.current, f.currentErr }
func (f fakeGit) LocalBranches(context.Context) ([]string, error) { return f.branches, f.branchesErr }
func (f fakeGit) Clean(context.Context) error                     { return f.dirty }

type fakeGraphite struct {
	paths       map[string]graphite.Stack
	tracked     []string
	discoverErr error
}

func (f fakeGraphite) Discover(_ context.Context, branch string) (graphite.Stack, error) {
	if f.discoverErr != nil {
		return graphite.Stack{}, f.discoverErr
	}
	stack, ok := f.paths[branch]
	if !ok {
		return graphite.Stack{}, errors.New("untracked")
	}
	return stack, nil
}
func (f fakeGraphite) TrackedBranches(context.Context) ([]string, error) { return f.tracked, nil }

type changingGraphite struct{ discoveries int }

func (f *changingGraphite) Discover(context.Context, string) (graphite.Stack, error) {
	f.discoveries++
	trunk := "main"
	if f.discoveries > 1 {
		trunk = "other-main"
	}
	return graphite.Stack{Trunk: trunk, Branches: []string{"alpha", "beta"}}, nil
}
func (*changingGraphite) TrackedBranches(context.Context) ([]string, error) {
	return []string{"alpha", "beta"}, nil
}

type fakeGitHub struct {
	links      int
	trunk      string
	branches   []string
	prs        []githubstack.PullRequest
	inspectErr error
}

func (f *fakeGitHub) Inspect(context.Context, []string) ([]githubstack.PullRequest, error) {
	return f.prs, f.inspectErr
}
func (f *fakeGitHub) Link(_ context.Context, trunk string, branches []string) error {
	f.links++
	f.trunk = trunk
	f.branches = append([]string(nil), branches...)
	return nil
}
