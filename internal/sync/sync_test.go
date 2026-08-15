package sync

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/shhac/gt2gh/internal/githubstack"
	"github.com/shhac/gt2gh/internal/link"
)

func TestPreviewClassifiesGraphiteAuthoritativeDifferences(t *testing.T) {
	service := fakeService([]githubstack.PullRequest{
		{Number: 1, Head: "alpha", Base: "main", State: "OPEN"},
		{Number: 2, Head: "beta", Base: "main", State: "OPEN"},
		{Number: 3, Head: "gamma", Base: "beta", State: "MERGED"},
	})
	plan, err := service.Preview(context.Background(), link.Selection{Branch: ""})
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}
	states := []State{plan.Items[0].State, plan.Items[1].State, plan.Items[2].State, plan.Items[3].State}
	if got, want := strings.Join(statesToStrings(states), ","), "aligned,divergent,unsafe,missing"; got != want {
		t.Errorf("states = %q, want %q", got, want)
	}
	if plan.CanApply() {
		t.Error("CanApply() = true, want false for unsafe/missing mappings")
	}
}

func TestApplyReconcilesOnlyFullyMappedOpenPath(t *testing.T) {
	github := &fakeGitHub{}
	service := fakeService([]githubstack.PullRequest{
		{Number: 1, Head: "alpha", Base: "main", State: "OPEN"},
		{Number: 2, Head: "beta", Base: "main", State: "OPEN"},
		{Number: 3, Head: "gamma", Base: "beta", State: "OPEN"},
		{Number: 4, Head: "delta", Base: "main", State: "OPEN"},
	})
	service.GitHub = github
	preview, err := service.Preview(context.Background(), link.Selection{Branch: ""})
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}
	if !preview.CanApply() {
		t.Fatal("CanApply() = false, want true")
	}
	if err := applyPlan(t, service, preview); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if github.links != 1 {
		t.Fatalf("Link calls = %d, want 1", github.links)
	}
	if got, want := strings.Join(github.branches, ","), "alpha,beta,gamma,delta"; got != want {
		t.Errorf("branches = %q, want %q", got, want)
	}
}

func TestApplyNoopsForOneFullyMappedPullRequest(t *testing.T) {
	github := &fakeGitHub{}
	service := Service{
		Discoverer: fakeDiscoverer{plan: link.Plan{
			Target: "synthetic-feature", Base: "synthetic-main", Branches: []string{"synthetic-feature"},
			PullRequests: []githubstack.PullRequest{{Number: 1, Head: "synthetic-feature", Base: "synthetic-main", State: "OPEN"}},
		}},
		Git:    fakeGit{},
		GitHub: github,
	}
	preview, err := service.Preview(context.Background(), link.Selection{Branch: ""})
	if err != nil {
		t.Fatal(err)
	}
	if !preview.NothingToSync() {
		t.Fatal("NothingToSync() = false")
	}
	if err := applyPlan(t, service, preview); err != nil {
		t.Fatal(err)
	}
	if github.links != 0 {
		t.Errorf("Link calls = %d, want 0", github.links)
	}
}

func TestApplyFailsClosedForMissingOrUnsafeMappings(t *testing.T) {
	for _, name := range []string{"missing", "unsafe"} {
		t.Run(name, func(t *testing.T) {
			prs := []githubstack.PullRequest{{Number: 1, Head: "alpha", Base: "main", State: "OPEN"}, {Number: 2, Head: "beta", Base: "alpha", State: "OPEN"}}
			if name == "unsafe" {
				prs = append(prs, githubstack.PullRequest{Number: 3, Head: "gamma", Base: "beta", State: "CLOSED"}, githubstack.PullRequest{Number: 4, Head: "delta", Base: "gamma", State: "OPEN"})
			}
			github := &fakeGitHub{}
			service := fakeService(prs)
			service.GitHub = github
			preview, err := service.Preview(context.Background(), link.Selection{Branch: ""})
			if err != nil {
				t.Fatalf("Preview() error = %v", err)
			}
			if err := applyPlan(t, service, preview); err == nil {
				t.Fatal("Apply() error = nil")
			}
			if github.links != 0 {
				t.Errorf("Link calls = %d, want 0", github.links)
			}
		})
	}
}

func TestApplyRejectsChangedPlan(t *testing.T) {
	discoverer := &changingDiscoverer{}
	github := &fakeGitHub{}
	service := Service{Discoverer: discoverer, Git: fakeGit{}, GitHub: github}
	preview, err := service.Preview(context.Background(), link.Selection{Branch: ""})
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}
	if err := applyPlan(t, service, preview); err == nil || !strings.Contains(err.Error(), "changed during revalidation") {
		t.Fatalf("Apply() error = %v, want changed-plan error", err)
	}
	if github.links != 0 {
		t.Errorf("Link calls = %d, want 0", github.links)
	}
}

func TestApplyPropagatesCleanAndGitHubFailures(t *testing.T) {
	for _, test := range []struct {
		name    string
		gitErr  error
		github  *fakeGitHub
		wantErr error
	}{
		{"dirty worktree", errors.New("dirty"), &fakeGitHub{}, errors.New("dirty")},
		{"GitHub link", nil, &fakeGitHub{err: errors.New("link failed")}, errors.New("link failed")},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := fakeService([]githubstack.PullRequest{
				{Number: 1, Head: "alpha", Base: "main", State: "OPEN"},
				{Number: 2, Head: "beta", Base: "alpha", State: "OPEN"},
				{Number: 3, Head: "gamma", Base: "beta", State: "OPEN"},
				{Number: 4, Head: "delta", Base: "gamma", State: "OPEN"},
			})
			service.Git = fakeGit{err: test.gitErr}
			service.GitHub = test.github
			preview, err := service.Preview(context.Background(), link.Selection{Branch: ""})
			if err != nil {
				t.Fatalf("Preview() error = %v", err)
			}
			err = applyPlan(t, service, preview)
			if err == nil || err.Error() != test.wantErr.Error() {
				t.Fatalf("Apply() error = %v, want %v", err, test.wantErr)
			}
			if test.github.links != 0 {
				t.Errorf("successful links = %d, want 0", test.github.links)
			}
		})
	}
}

func TestPreviewRejectsDuplicateOpenPullRequests(t *testing.T) {
	service := fakeService([]githubstack.PullRequest{{Number: 1, Head: "alpha", Base: "main", State: "OPEN"}, {Number: 2, Head: "alpha", Base: "main", State: "OPEN"}})
	if _, err := service.Preview(context.Background(), link.Selection{Branch: ""}); err == nil || !strings.Contains(err.Error(), "2 open pull requests") {
		t.Fatalf("Preview() error = %v", err)
	}
}

// A branch reused after an earlier pull request was closed is history, not
// ambiguity. Treating the second match as a duplicate blocked stacks whose
// only fault was having been submitted before.
func TestPreviewResolvesReusedBranchWithClosedHistory(t *testing.T) {
	service := fakeService([]githubstack.PullRequest{
		{Number: 1, Head: "alpha", Base: "main", State: "CLOSED"},
		{Number: 9, Head: "alpha", Base: "main", State: "OPEN"},
	})
	plan, err := service.Preview(context.Background(), link.Selection{Branch: ""})
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}
	if plan.Items[0].State != Aligned {
		t.Fatalf("state = %q, want %q", plan.Items[0].State, Aligned)
	}
	if plan.Items[0].PullRequest.Number != 9 {
		t.Fatalf("resolved PR = #%d, want the open #9", plan.Items[0].PullRequest.Number)
	}
}

func fakeService(prs []githubstack.PullRequest) Service {
	return Service{
		Discoverer: fakeDiscoverer{plan: link.Plan{
			Target:       "delta",
			TargetSource: "current Git branch",
			Base:         "main",
			BaseSource:   "Graphite-declared ancestry",
			GraphitePath: []string{"main", "alpha", "beta", "gamma", "delta"},
			Branches:     []string{"alpha", "beta", "gamma", "delta"},
			PullRequests: prs,
		}},
		Git:    fakeGit{},
		GitHub: &fakeGitHub{},
	}
}

type fakeDiscoverer struct {
	plan link.Plan
	err  error
}

func (f fakeDiscoverer) DiscoverWithOptions(ctx context.Context, selection link.Selection) (link.Plan, error) {
	return f.plan, f.err
}

type changingDiscoverer struct{ calls int }

func (f *changingDiscoverer) DiscoverWithOptions(context.Context, link.Selection) (link.Plan, error) {
	f.calls++
	trunk := "main"
	if f.calls > 1 {
		trunk = "other-main"
	}
	return link.Plan{Target: "delta", TargetSource: "current Git branch", Base: trunk, BaseSource: "Graphite-declared ancestry", GraphitePath: []string{trunk, "alpha", "beta", "gamma", "delta"}, Branches: []string{"alpha", "beta", "gamma", "delta"}, PullRequests: []githubstack.PullRequest{{Number: 1, Head: "alpha", Base: trunk, State: "OPEN"}, {Number: 2, Head: "beta", Base: "alpha", State: "OPEN"}, {Number: 3, Head: "gamma", Base: "beta", State: "OPEN"}, {Number: 4, Head: "delta", Base: "gamma", State: "OPEN"}}}, nil
}

type fakeGit struct{ err error }

func (f fakeGit) Clean(context.Context) error { return f.err }

type fakeGitHub struct {
	links    int
	trunk    string
	branches []string
	err      error
}

func (f *fakeGitHub) Link(_ context.Context, trunk string, branches []string) error {
	if f.err != nil {
		return f.err
	}
	f.links++
	f.trunk = trunk
	f.branches = append([]string(nil), branches...)
	return nil
}

func statesToStrings(states []State) []string {
	result := make([]string, len(states))
	for index, state := range states {
		result[index] = string(state)
	}
	return result
}

// applyPlan drives the sequence production actually performs: revalidate, then
// execute. The service no longer composes the two, because the CLI interposes
// the ready-to-apply render and its flush between them.
func applyPlan(t *testing.T, service Service, preview Plan) error {
	t.Helper()
	validated, err := service.Revalidate(context.Background(), link.Selection{}, preview)
	if err != nil {
		return err
	}
	return service.Execute(context.Background(), validated)
}
