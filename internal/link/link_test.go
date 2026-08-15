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

func TestApplyNoopsForOneFullyMappedPullRequest(t *testing.T) {
	github := &fakeGitHub{}
	service := fakeService()
	service.GitHub = github
	preview, err := service.Plan(context.Background(), "alpha")
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if !preview.NothingToLink() {
		t.Fatal("NothingToLink() = false, want true")
	}
	if _, err := service.Apply(context.Background(), "alpha", preview); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if github.links != 0 {
		t.Errorf("Link calls = %d, want 0", github.links)
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
		{"missing local stack branch", Service{Git: fakeGit{current: "beta", branches: []string{"main", "beta"}}, Graphite: fakeGraphite{paths: map[string]graphite.Stack{"beta": {Path: []string{"main", "missing", "beta"}, Trunks: []string{"main"}}}}, GitHub: &fakeGitHub{}}},
		{"GitHub inspection", Service{Git: fakeGit{current: "beta", branches: []string{"main", "alpha", "beta"}}, Graphite: fakeGraphite{paths: map[string]graphite.Stack{"beta": {Path: []string{"main", "alpha", "beta"}, Trunks: []string{"main"}}}}, GitHub: &fakeGitHub{inspectErr: context.Canceled}}},
		{"non-open pull request", Service{Git: fakeGit{current: "beta", branches: []string{"main", "alpha", "beta"}}, Graphite: fakeGraphite{paths: map[string]graphite.Stack{"beta": {Path: []string{"main", "alpha", "beta"}, Trunks: []string{"main"}}}}, GitHub: &fakeGitHub{prs: []githubstack.PullRequest{{Number: 2, Head: "alpha", Base: "main", State: "MERGED"}}}}},
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
			plan, err := service.Plan(context.Background(), "beta")
			if err != nil || len(plan.Issues) == 0 {
				t.Fatalf("Plan() = (%v, %v), want unresolved issue", plan, err)
			}
		})
	}
}

func TestPlanRejectsOptionLikeGraphiteBranch(t *testing.T) {
	github := &fakeGitHub{}
	service := Service{
		Git: fakeGit{current: "synthetic-tip", branches: []string{"main", "-synthetic-option", "synthetic-tip"}},
		Graphite: fakeGraphite{paths: map[string]graphite.Stack{
			"synthetic-tip": {Path: []string{"main", "-synthetic-option", "synthetic-tip"}, Trunks: []string{"main"}},
		}},
		GitHub: github,
	}
	if _, err := service.Plan(context.Background(), ""); err == nil || !strings.Contains(err.Error(), "cannot be passed safely to gh stack link") {
		t.Fatalf("Plan() error = %v", err)
	}
	if github.links != 0 {
		t.Errorf("links=%d, want 0", github.links)
	}
}

func TestPlanAccumulatesEveryUnresolvedBranch(t *testing.T) {
	service := fakeService()
	service.GitHub = &fakeGitHub{prs: []githubstack.PullRequest{
		{Number: 1, Head: "alpha", Base: "main", State: "OPEN"},
		{Number: 2, Head: "beta", Base: "alpha", State: "CLOSED"},
	}}
	plan, err := service.Plan(context.Background(), "beta-two")
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if got, want := strings.Join(issueBranches(plan.Issues), ","), "beta,beta-two"; got != want {
		t.Errorf("issues = %q, want %q", got, want)
	}
}

func issueBranches(issues []Issue) []string {
	result := make([]string, len(issues))
	for i := range issues {
		result[i] = issues[i].Branch
	}
	return result
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

func TestPlanWithOptionsUsesValidDeclaredAncestralOverride(t *testing.T) {
	service := Service{
		Git: fakeGit{current: "feature", branches: []string{"develop", "main", "feature"}},
		Graphite: fakeGraphite{paths: map[string]graphite.Stack{
			"feature": {Path: []string{"develop", "main", "feature"}, Trunks: []string{"develop", "main"}},
		}},
		GitHub: &fakeGitHub{prs: []githubstack.PullRequest{{Number: 9, Head: "feature", Base: "main", State: "OPEN"}}},
	}
	if _, err := service.Plan(context.Background(), "feature"); err == nil || !strings.Contains(err.Error(), "multiple declared trunks") {
		t.Fatalf("Plan() error = %v, want ambiguity", err)
	}
	plan, err := service.PlanWithOptions(context.Background(), Selection{Branch: "feature", Trunk: "main"})
	if err != nil {
		t.Fatalf("PlanWithOptions() error = %v", err)
	}
	if plan.Base != "main" || strings.Join(plan.Branches, ",") != "feature" {
		t.Errorf("plan = base %q branches %v", plan.Base, plan.Branches)
	}
}

func TestTrunkCompletionsAreLocalAndSorted(t *testing.T) {
	service := fakeService()
	service.Git = fakeGit{current: "beta", branches: []string{"main", "develop", "staging", "alpha", "beta"}}
	service.Graphite = fakeGraphite{paths: map[string]graphite.Stack{"beta": {Path: []string{"main", "alpha", "beta"}, Trunks: []string{"staging", "main", "develop"}}}}
	branches, err := service.TrunkCompletions(context.Background(), "", "")
	if err != nil {
		t.Fatalf("TrunkCompletions() error = %v", err)
	}
	if got, want := strings.Join(branches, ","), "develop,main,staging"; got != want {
		t.Errorf("branches = %q, want %q", got, want)
	}
}

func TestTrunkCompletionsUseExplicitTargetWithoutCheckout(t *testing.T) {
	service := Service{
		Git: fakeGit{current: "current", branches: []string{"main", "develop", "current", "chosen"}},
		Graphite: fakeGraphite{paths: map[string]graphite.Stack{
			"current": {Path: []string{"main", "current"}, Trunks: []string{"main"}},
			"chosen":  {Path: []string{"develop", "chosen"}, Trunks: []string{"develop"}},
		}},
	}
	branches, err := service.TrunkCompletions(context.Background(), "chosen", "d")
	if err != nil {
		t.Fatalf("TrunkCompletions() error = %v", err)
	}
	if got, want := strings.Join(branches, ","), "develop"; got != want {
		t.Errorf("branches = %q, want %q", got, want)
	}
}

func fakeService() Service {
	branches := []string{"main", "alpha", "beta", "beta-one", "beta-two", "beta-two-deep", "gamma", "gamma-deep"}
	return Service{
		Git: fakeGit{current: "beta-two-deep", branches: branches},
		Graphite: fakeGraphite{paths: map[string]graphite.Stack{
			"alpha":         {Path: []string{"main", "alpha"}, Trunks: []string{"main"}},
			"beta":          {Path: []string{"main", "alpha", "beta"}, Trunks: []string{"main"}},
			"beta-one":      {Path: []string{"main", "alpha", "beta", "beta-one"}, Trunks: []string{"main"}},
			"beta-two":      {Path: []string{"main", "alpha", "beta", "beta-two"}, Trunks: []string{"main"}},
			"beta-two-deep": {Path: []string{"main", "alpha", "beta", "beta-two", "beta-two-deep"}, Trunks: []string{"main"}},
			"gamma":         {Path: []string{"main", "alpha", "gamma"}, Trunks: []string{"main"}},
			"gamma-deep":    {Path: []string{"main", "alpha", "gamma", "gamma-deep"}, Trunks: []string{"main"}},
		}, tracked: branches[1:]},
		GitHub: &fakeGitHub{prs: []githubstack.PullRequest{
			{Number: 1, Head: "alpha", Base: "main", State: "OPEN"},
			{Number: 2, Head: "beta", Base: "alpha", State: "OPEN"},
			{Number: 3, Head: "beta-one", Base: "beta", State: "OPEN"},
			{Number: 4, Head: "beta-two", Base: "beta", State: "OPEN"},
			{Number: 5, Head: "beta-two-deep", Base: "beta-two", State: "OPEN"},
			{Number: 6, Head: "gamma", Base: "alpha", State: "OPEN"},
			{Number: 7, Head: "gamma-deep", Base: "gamma", State: "OPEN"},
		}},
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
	stackPaths  map[string]graphite.Stack
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
func (f fakeGraphite) DiscoverStack(ctx context.Context, branch string, includeTip bool) (graphite.Stack, error) {
	if includeTip && f.stackPaths != nil {
		stack, ok := f.stackPaths[branch]
		if !ok {
			return graphite.Stack{}, errors.New("untracked")
		}
		return stack, nil
	}
	return f.Discover(ctx, branch)
}
func (f fakeGraphite) TrackedBranches(context.Context) ([]string, error) { return f.tracked, nil }

type changingGraphite struct{ discoveries int }

func (f *changingGraphite) Discover(context.Context, string) (graphite.Stack, error) {
	f.discoveries++
	path := []string{"main", "alpha", "beta"}
	if f.discoveries > 1 {
		path = []string{"main", "beta"}
	}
	return graphite.Stack{Path: path, Trunks: []string{"main"}}, nil
}
func (f *changingGraphite) DiscoverStack(ctx context.Context, branch string, _ bool) (graphite.Stack, error) {
	return f.Discover(ctx, branch)
}

func TestPlanDefaultsToFullStackAndNoStackStopsAtPivotWithoutCheckout(t *testing.T) {
	service := Service{
		Git: fakeGit{current: "middle", branches: []string{"main", "lower", "middle", "top"}},
		Graphite: fakeGraphite{paths: map[string]graphite.Stack{
			"middle": {Path: []string{"main", "lower", "middle"}, Trunks: []string{"main"}},
		}, stackPaths: map[string]graphite.Stack{
			"middle": {Path: []string{"main", "lower", "middle", "top"}, Trunks: []string{"main"}},
		}},
		GitHub: &fakeGitHub{},
	}
	plan, err := service.PlanWithOptions(context.Background(), Selection{})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(plan.Branches, ","), "lower,middle,top"; got != want {
		t.Errorf("branches = %q, want %q", got, want)
	}
	plan, err = service.PlanWithOptions(context.Background(), Selection{NoStack: true})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(plan.Branches, ","), "lower,middle"; got != want {
		t.Errorf("no-stack branches = %q, want %q", got, want)
	}
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

func (f *fakeGitHub) Inspect(_ context.Context, branches []string) ([]githubstack.PullRequest, error) {
	if f.prs == nil {
		prs := make([]githubstack.PullRequest, 0, len(branches))
		base := "main"
		for index, branch := range branches {
			prs = append(prs, githubstack.PullRequest{Number: index + 1, Head: branch, Base: base, State: "OPEN"})
			base = branch
		}
		return prs, f.inspectErr
	}
	wanted := branchSet(branches)
	var matching []githubstack.PullRequest
	for _, pr := range f.prs {
		if wanted[pr.Head] {
			matching = append(matching, pr)
		}
	}
	return matching, f.inspectErr
}
func (f *fakeGitHub) Link(_ context.Context, trunk string, branches []string) error {
	f.links++
	f.trunk = trunk
	f.branches = append([]string(nil), branches...)
	return nil
}

// The suggestion in preview is only safe if the kinds come out of real
// assessment correctly, so pin them at the source rather than only where they
// are rendered.
func TestAssessedIssuesCarryTheirKind(t *testing.T) {
	prs := []githubstack.PullRequest{
		{Number: 1, Head: "wrong-base", Base: "synthetic-other", State: "OPEN"},
		{Number: 2, Head: "closed-only", Base: "wrong-base", State: "CLOSED"},
		{Number: 3, Head: "ambiguous", Base: "closed-only", State: "OPEN"},
		{Number: 4, Head: "ambiguous", Base: "closed-only", State: "OPEN"},
	}
	branches := []string{"wrong-base", "closed-only", "ambiguous", "missing"}

	kinds := map[string]IssueKind{}
	for _, issue := range assessPRs(prs, "main", branches) {
		kinds[issue.Branch] = issue.Kind
	}

	for branch, want := range map[string]IssueKind{
		"wrong-base":  IssueBase,
		"closed-only": IssueNonOpen,
		"ambiguous":   IssueAmbiguous,
		"missing":     IssueMissing,
	} {
		if kinds[branch] != want {
			t.Errorf("%s kind = %q, want %q", branch, kinds[branch], want)
		}
	}
}

func TestSyncRepairableRequiresEveryIssueToBeABase(t *testing.T) {
	base := Issue{Branch: "a", Kind: IssueBase}
	if !(Plan{Issues: []Issue{base, {Branch: "b", Kind: IssueBase}}}).SyncRepairable() {
		t.Error("all-base plan = false, want true")
	}
	if (Plan{Issues: []Issue{base, {Branch: "b", Kind: IssueMissing}}}).SyncRepairable() {
		t.Error("mixed plan = true, want false")
	}
	if (Plan{}).SyncRepairable() {
		t.Error("clean plan = true, want false")
	}
}
