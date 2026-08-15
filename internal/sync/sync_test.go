package sync

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/shhac/gt2gh/internal/githubstack"
	"github.com/shhac/gt2gh/internal/graphite"
	"github.com/shhac/gt2gh/internal/stack"
)

func TestPreviewClassifiesGraphiteAuthoritativeDifferences(t *testing.T) {
	service := fakeService([]githubstack.PullRequest{
		{Number: 1, Head: "alpha", Base: "main", State: "OPEN"},
		{Number: 2, Head: "beta", Base: "main", State: "OPEN"},
		{Number: 3, Head: "gamma", Base: "beta", State: "MERGED"},
	})
	plan, err := service.Preview(context.Background(), stack.Selection{})
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
	service := fakeService([]githubstack.PullRequest{
		{Number: 1, Head: "alpha", Base: "main", State: "OPEN"},
		{Number: 2, Head: "beta", Base: "alpha", State: "OPEN"},
		{Number: 3, Head: "gamma", Base: "beta", State: "OPEN"},
		{Number: 4, Head: "delta", Base: "gamma", State: "OPEN"},
	})
	github := service.GitHub.(*fakeGitHub)
	preview, err := service.Preview(context.Background(), stack.Selection{})
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
	github.prs = []githubstack.PullRequest{{Number: 1, Head: "synthetic-feature", Base: "synthetic-main", State: "OPEN"}}
	service := Service{Git: singleBranchGit{}, Graphite: singleBranchGraphite{}, GitHub: github}
	preview, err := service.Preview(context.Background(), stack.Selection{})
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
			preview, err := service.Preview(context.Background(), stack.Selection{})
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
	calls := 0
	github := &fakeGitHub{prs: []githubstack.PullRequest{
		{Number: 1, Head: "alpha", Base: "main", State: "OPEN"},
		{Number: 2, Head: "beta", Base: "alpha", State: "OPEN"},
		{Number: 3, Head: "gamma", Base: "beta", State: "OPEN"},
		{Number: 4, Head: "delta", Base: "gamma", State: "OPEN"},
	}}
	service := Service{Git: fakeGit{}, Graphite: fakeGraphite{shiftTrunk: &calls}, GitHub: github}
	preview, err := service.Preview(context.Background(), stack.Selection{})
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
		linkErr error
		wantErr string
	}{
		{name: "dirty worktree", gitErr: errors.New("dirty"), wantErr: "dirty"},
		{name: "GitHub link", linkErr: errors.New("link failed"), wantErr: "link failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := fakeService([]githubstack.PullRequest{
				{Number: 1, Head: "alpha", Base: "main", State: "OPEN"},
				{Number: 2, Head: "beta", Base: "alpha", State: "OPEN"},
				{Number: 3, Head: "gamma", Base: "beta", State: "OPEN"},
				{Number: 4, Head: "delta", Base: "gamma", State: "OPEN"},
			})
			service.Git = fakeGit{err: test.gitErr}
			github := service.GitHub.(*fakeGitHub)
			github.err = test.linkErr

			preview, err := service.Preview(context.Background(), stack.Selection{})
			if err != nil {
				t.Fatalf("Preview() error = %v", err)
			}
			if err := applyPlan(t, service, preview); err == nil || err.Error() != test.wantErr {
				t.Fatalf("apply error = %v, want %q", err, test.wantErr)
			}
			if github.links != 0 {
				t.Errorf("successful links = %d, want 0", github.links)
			}
		})
	}
}

func TestPreviewRejectsDuplicateOpenPullRequests(t *testing.T) {
	service := fakeService([]githubstack.PullRequest{{Number: 1, Head: "alpha", Base: "main", State: "OPEN"}, {Number: 2, Head: "alpha", Base: "main", State: "OPEN"}})
	if _, err := service.Preview(context.Background(), stack.Selection{}); err == nil || !strings.Contains(err.Error(), "2 open pull requests") {
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
	plan, err := service.Preview(context.Background(), stack.Selection{})
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
		Git:      fakeGit{},
		Graphite: fakeGraphite{},
		GitHub:   &fakeGitHub{prs: prs},
	}
}

type fakeGit struct{ err error }

func (f fakeGit) Clean(context.Context) error { return f.err }
func (fakeGit) CurrentBranch(context.Context) (string, error) {
	return "delta", nil
}
func (fakeGit) LocalBranches(context.Context) ([]string, error) {
	return []string{"main", "other-main", "alpha", "beta", "gamma", "delta"}, nil
}

// fakeGraphite declares the trunk-to-delta path every case in this file works
// from. shiftTrunk moves the declared base on the second call, which is how a
// revalidation observes Graphite changing under a rendered preview.
type fakeGraphite struct{ shiftTrunk *int }

func (f fakeGraphite) DiscoverStack(context.Context, string, bool) (graphite.Stack, error) {
	trunk := "main"
	if f.shiftTrunk != nil {
		*f.shiftTrunk++
		if *f.shiftTrunk > 1 {
			trunk = "other-main"
		}
	}
	return graphite.Stack{
		Path:   []string{trunk, "alpha", "beta", "gamma", "delta"},
		Trunks: []string{trunk},
	}, nil
}

type fakeGitHub struct {
	prs      []githubstack.PullRequest
	links    int
	trunk    string
	branches []string
	err      error
}

func (f *fakeGitHub) Inspect(_ context.Context, branches []string) ([]githubstack.PullRequest, error) {
	var matching []githubstack.PullRequest
	for _, pr := range f.prs {
		if slices.Contains(branches, pr.Head) {
			matching = append(matching, pr)
		}
	}
	return matching, nil
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
	validated, err := service.Revalidate(context.Background(), stack.Selection{}, preview)
	if err != nil {
		return err
	}
	return service.Execute(context.Background(), validated)
}

// singleBranch* model a stack with nothing above its trunk, which is the
// successful no-op gh stack link cannot express.
type singleBranchGit struct{}

func (singleBranchGit) Clean(context.Context) error { return nil }
func (singleBranchGit) CurrentBranch(context.Context) (string, error) {
	return "synthetic-feature", nil
}
func (singleBranchGit) LocalBranches(context.Context) ([]string, error) {
	return []string{"synthetic-main", "synthetic-feature"}, nil
}

type singleBranchGraphite struct{}

func (singleBranchGraphite) DiscoverStack(context.Context, string, bool) (graphite.Stack, error) {
	return graphite.Stack{Path: []string{"synthetic-main", "synthetic-feature"}, Trunks: []string{"synthetic-main"}}, nil
}
