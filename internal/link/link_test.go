package link

import (
	"context"
	"errors"
	"fmt"
	"github.com/shhac/g2g/internal/stack"
	"slices"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/graphite"
	"github.com/shhac/g2g/internal/testutil/forest"
)

func TestPlanUsesCurrentBranchAndSelectedForkPath(t *testing.T) {
	service := fakeService()
	plan, err := service.Plan(context.Background(), Selection{Branch: ""})
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
	plan, err := service.Plan(context.Background(), Selection{Branch: "gamma-deep"})
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
	preview, err := service.Plan(context.Background(), Selection{Branch: "beta-two"})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if err := applyPlan(t, service, Selection{Branch: "beta-two"}, preview); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if github.links != 1 {
		t.Fatalf("Link calls = %d, want 1", github.links)
	}
	if got, want := strings.Join(github.branches, ","), "alpha,beta,beta-two,beta-two-deep"; got != want {
		t.Errorf("linked branches = %q, want %q", got, want)
	}
}

func TestApplyNoopsForOneFullyMappedPullRequest(t *testing.T) {
	github := &fakeGitHub{}
	service := fakeService()
	service.GitHub = github
	// One pull request is one pull request only within a selection that holds
	// one branch; the default now reaches the stack above alpha.
	selection := Selection{Branch: "alpha", Scope: stack.ScopeBranch}
	preview, err := service.Plan(context.Background(), selection)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if !preview.NothingToLink() {
		t.Fatal("NothingToLink() = false, want true")
	}
	if err := applyPlan(t, service, selection, preview); err != nil {
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
	if err := applyPlan(t, service, Selection{Branch: "beta-two"}, Plan{}); err == nil {
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
		Selector: graphiteSelector(fakeGit{current: "beta", branches: []string{"main", "other-main", "alpha", "beta"}}, &changingGraphite{}),
		GitHub:   github,
	}
	preview, err := service.Plan(context.Background(), Selection{Branch: ""})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if err := applyPlan(t, service, Selection{Branch: ""}, preview); err == nil || !strings.Contains(err.Error(), "changed during revalidation") {
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
		{"current branch resolution", Service{Git: fakeGit{currentErr: context.Canceled}, Selector: graphiteSelector(fakeGit{currentErr: context.Canceled}, fakeGraphite{}), GitHub: &fakeGitHub{}}},
		{"local branch listing", Service{Git: fakeGit{current: "beta", branchesErr: context.Canceled}, Selector: graphiteSelector(fakeGit{current: "beta", branchesErr: context.Canceled}, fakeGraphite{}), GitHub: &fakeGitHub{}}},
		{"Graphite discovery", Service{Git: fakeGit{current: "beta", branches: []string{"main", "beta"}}, Selector: graphiteSelector(fakeGit{current: "beta", branches: []string{"main", "beta"}}, fakeGraphite{discoverErr: context.Canceled}), GitHub: &fakeGitHub{}}},
		{"missing local stack branch", Service{Git: fakeGit{current: "beta", branches: []string{"main", "beta"}}, Selector: graphiteSelector(fakeGit{current: "beta", branches: []string{"main", "beta"}}, fakeGraphite{paths: map[string]graphite.Stack{"beta": {Path: []string{"main", "missing", "beta"}, Trunks: []string{"main"}}}}), GitHub: &fakeGitHub{}}},
		{"GitHub inspection", Service{Git: fakeGit{current: "beta", branches: []string{"main", "alpha", "beta"}}, Selector: graphiteSelector(fakeGit{current: "beta", branches: []string{"main", "alpha", "beta"}}, fakeGraphite{paths: map[string]graphite.Stack{"beta": {Path: []string{"main", "alpha", "beta"}, Trunks: []string{"main"}}}}), GitHub: &fakeGitHub{inspectErr: context.Canceled}}},
		{"non-open pull request", Service{Git: fakeGit{current: "beta", branches: []string{"main", "alpha", "beta"}}, Selector: graphiteSelector(fakeGit{current: "beta", branches: []string{"main", "alpha", "beta"}}, fakeGraphite{paths: map[string]graphite.Stack{"beta": {Path: []string{"main", "alpha", "beta"}, Trunks: []string{"main"}}}}), GitHub: &fakeGitHub{prs: []githubstack.PullRequest{{Number: 2, Head: "alpha", Base: "main", State: "MERGED"}}}}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			github := test.service.GitHub.(*fakeGitHub)
			if err := applyPlan(t, test.service, Selection{Branch: ""}, Plan{}); err == nil {
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
			plan, err := service.Plan(context.Background(), Selection{Branch: "beta"})
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
		Selector: graphiteSelector(fakeGit{current: "synthetic-tip", branches: []string{"main", "-synthetic-option", "synthetic-tip"}}, fakeGraphite{paths: map[string]graphite.Stack{
			"synthetic-tip": {Path: []string{"main", "-synthetic-option", "synthetic-tip"}, Trunks: []string{"main"}},
		}}),
		GitHub: github,
	}
	if _, err := service.Plan(context.Background(), Selection{Branch: ""}); err == nil || !strings.Contains(err.Error(), "cannot be passed safely to gh stack link") {
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
	plan, err := service.Plan(context.Background(), Selection{Branch: "beta-two"})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if got, want := strings.Join(issueBranches(plan.Issues), ","), "beta,beta-two,beta-two-deep"; got != want {
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

func TestPlanWithOptionsUsesValidDeclaredAncestralOverride(t *testing.T) {
	service := Service{
		Git: fakeGit{current: "feature", branches: []string{"develop", "main", "feature"}},
		Selector: graphiteSelector(fakeGit{current: "feature", branches: []string{"develop", "main", "feature"}}, fakeGraphite{paths: map[string]graphite.Stack{
			"feature": {Path: []string{"develop", "main", "feature"}, Trunks: []string{"develop", "main"}},
		}}),
		GitHub: &fakeGitHub{prs: []githubstack.PullRequest{{Number: 9, Head: "feature", Base: "main", State: "OPEN"}}},
	}
	if _, err := service.Plan(context.Background(), Selection{Branch: "feature"}); err == nil || !strings.Contains(err.Error(), "multiple declared trunks") {
		t.Fatalf("Plan() error = %v, want ambiguity", err)
	}
	plan, err := service.Plan(context.Background(), Selection{Branch: "feature", Trunk: "main"})
	if err != nil {
		t.Fatalf("PlanWithOptions() error = %v", err)
	}
	if plan.Base != "main" || strings.Join(plan.Branches, ",") != "feature" {
		t.Errorf("plan = base %q branches %v", plan.Base, plan.Branches)
	}
}

func fakeService() Service {
	branches := []string{"main", "alpha", "beta", "beta-one", "beta-two", "beta-two-deep", "gamma", "gamma-deep"}
	return Service{
		Git: fakeGit{current: "beta-two-deep", branches: branches},
		Selector: graphiteSelector(fakeGit{current: "beta-two-deep", branches: branches}, fakeGraphite{paths: map[string]graphite.Stack{
			"alpha":         {Path: []string{"main", "alpha"}, Trunks: []string{"main"}},
			"beta":          {Path: []string{"main", "alpha", "beta"}, Trunks: []string{"main"}},
			"beta-one":      {Path: []string{"main", "alpha", "beta", "beta-one"}, Trunks: []string{"main"}},
			"beta-two":      {Path: []string{"main", "alpha", "beta", "beta-two"}, Trunks: []string{"main"}},
			"beta-two-deep": {Path: []string{"main", "alpha", "beta", "beta-two", "beta-two-deep"}, Trunks: []string{"main"}},
			"gamma":         {Path: []string{"main", "alpha", "gamma"}, Trunks: []string{"main"}},
			"gamma-deep":    {Path: []string{"main", "alpha", "gamma", "gamma-deep"}, Trunks: []string{"main"}},
		}, tracked: branches[1:]}),
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
		Selector: graphiteSelector(fakeGit{current: "middle", branches: []string{"main", "lower", "middle", "top"}}, fakeGraphite{paths: map[string]graphite.Stack{
			"middle": {Path: []string{"main", "lower", "middle"}, Trunks: []string{"main"}},
		}, stackPaths: map[string]graphite.Stack{
			"middle": {Path: []string{"main", "lower", "middle", "top"}, Trunks: []string{"main"}},
		}}),
		GitHub: &fakeGitHub{},
	}
	plan, err := service.Plan(context.Background(), Selection{})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(plan.Branches, ","), "lower,middle,top"; got != want {
		t.Errorf("branches = %q, want %q", got, want)
	}
	// branch means the branch alone, which is what it always said and never
	// did: resolving through a bool, it suppressed descendants only, so it
	// returned the whole ancestry instead.
	plan, err = service.Plan(context.Background(), Selection{Scope: stack.ScopeBranch})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(plan.Branches, ","), "middle"; got != want {
		t.Errorf("branch scope = %q, want %q", got, want)
	}
	if plan.Base != "lower" {
		t.Errorf("branch scope hangs from %q, want its parent lower", plan.Base)
	}

	// path is the trunk down to the branch, which is what --no-stack used to
	// produce and is the value it becomes.
	plan, err = service.Plan(context.Background(), Selection{Scope: stack.ScopePath})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(plan.Branches, ","), "lower,middle"; got != want {
		t.Errorf("path scope = %q, want %q", got, want)
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
	var matching []githubstack.PullRequest
	for _, pr := range f.prs {
		if slices.Contains(branches, pr.Head) {
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
	for _, issue := range assessPRs(prs, "main", branches, nil) {
		kinds[issue.Branch] = issue.Kind
	}

	for branch, want := range map[string]IssueKind{
		"wrong-base":  IssueBase,
		"closed-only": IssueClosed,
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

// applyPlan drives the sequence production actually performs: revalidate, then
// execute. The service deliberately no longer composes the two, because the
// CLI interposes the ready-to-apply render and its flush between them, so a
// composite here would describe a sequence nothing runs.
func applyPlan(t *testing.T, service Service, selection Selection, preview Plan) error {
	t.Helper()
	validated, err := service.Revalidate(context.Background(), selection, preview)
	if err != nil {
		return err
	}
	return service.Execute(context.Background(), validated)
}

// graphiteSelector wraps a Graphite fixture as the source it now is. These
// cases assert Graphite-backed behaviour, which selection becoming pluggable
// must not have changed.
func graphiteSelector(git stack.Git, graphiteClient stack.Graphite) stack.PathSelector {
	return stack.GraphiteSelector{Git: git, Graphite: graphiteClient}
}

// ReadForest states the same shape the configured paths describe. Selection
// reads the forest, so a fake answering it differently would test the code
// between them against a world that does not exist.
func (f fakeGraphite) ReadForest(context.Context) (graphite.Forest, error) {
	if f.discoverErr != nil {
		return graphite.Forest{}, f.discoverErr
	}
	return forest.OfStacks(f.paths, f.stackPaths), nil
}

// The second read describes a different shape, which is what revalidation
// exists to notice.
func (f *changingGraphite) ReadForest(context.Context) (graphite.Forest, error) {
	f.discoveries++
	if f.discoveries > 1 {
		return graphite.Forest{Parents: map[string]string{"main": "", "beta": "main"}, Roots: []string{"main"}}, nil
	}
	return graphite.Forest{Parents: map[string]string{"main": "", "alpha": "main", "beta": "alpha"}, Roots: []string{"main"}}, nil
}

// currencyTips answers the local reads that compare a branch with the commit
// its pull request is on.
//
// ours and theirs are counts of commits with no equivalent on the other side,
// by content: what the real Cherry answers, and deliberately not what counting
// commit ids either way would.
type currencyTips struct {
	local  map[string]string
	known  map[string]bool
	ours   map[string]int
	theirs map[string]int
}

func (t currencyTips) ResolveAll(_ context.Context, revisions []string) (map[string]string, error) {
	resolved := map[string]string{}
	for _, revision := range revisions {
		switch {
		case t.local[revision] != "":
			resolved[revision] = t.local[revision]
		case t.known[revision]:
			resolved[revision] = revision
		}
	}
	return resolved, nil
}

// Cherry answers whichever side is being asked about. The branch's own commits
// are asked for as Cherry(prHead, branch, parent); the pull request's as
// Cherry(branch, prHead, "") — so the head names which side the answer is for.
func (t currencyTips) Cherry(_ context.Context, upstream, head, _ string) ([]string, []string, error) {
	if _, aboutTheBranch := t.local[head]; aboutTheBranch {
		return synthetic(t.ours[head]), nil, nil
	}
	return synthetic(t.theirs[upstream]), nil, nil
}

// Absorbed is the whole-branch question. These cases are about currency rather
// than about landing, so nothing here has been absorbed into anything.
func (currencyTips) Absorbed(context.Context, string, string) (bool, error) { return false, nil }

func synthetic(commits int) []string {
	absent := make([]string, commits)
	for index := range absent {
		absent[index] = fmt.Sprintf("synthetic-commit-%d", index)
	}
	return absent
}

// "aligned" is a statement about a pull request's base, so it said nothing
// about whether the pull request has the work sitting in the branch. A branch
// with two unpushed commits read as healthy.
func TestAPlanReportsWhetherEachPullRequestHasTheBranchesWork(t *testing.T) {
	for _, test := range []struct {
		name string
		tips currencyTips
		want Currency
	}{
		{
			name: "the pull request is on the branch's commit",
			tips: currencyTips{local: map[string]string{"synthetic-top": "same"}, known: map[string]bool{"same": true}},
			want: Currency{},
		},
		{
			name: "the branch has commits the pull request does not",
			tips: currencyTips{
				local: map[string]string{"synthetic-top": "local"},
				known: map[string]bool{"pr-head": true},
				ours:  map[string]int{"synthetic-top": 2},
			},
			want: Currency{Unpushed: 2},
		},
		{
			name: "the pull request is on a commit this repository does not have",
			tips: currencyTips{local: map[string]string{"synthetic-top": "local"}},
			want: Currency{Diverged: true},
		},
		{
			// The state a restacked stack is in, and the one that used to
			// report a divergence with the trunk's commits counted as the
			// reader's own. Nothing is missing from the pull request; it is
			// showing the same work as the commits it was pushed with.
			name: "the branch was replayed since it was pushed",
			tips: currencyTips{
				local: map[string]string{"synthetic-top": "local"},
				known: map[string]bool{"pr-head": true},
			},
			want: Currency{Rewritten: true},
		},
		{
			name: "both have commits the other does not",
			tips: currencyTips{
				local:  map[string]string{"synthetic-top": "local"},
				known:  map[string]bool{"pr-head": true},
				ours:   map[string]int{"synthetic-top": 1},
				theirs: map[string]int{"synthetic-top": 3},
			},
			want: Currency{Unpushed: 1, Diverged: true},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			headOID := "pr-head"
			if test.tips.local["synthetic-top"] == "same" {
				headOID = "same"
			}
			plan := Plan{Discovery: stack.Discovery{
				Snapshot:     stack.Snapshot{Target: "synthetic-top", Base: "main", Branches: []string{"synthetic-top"}},
				PullRequests: []githubstack.PullRequest{{Number: 7, Head: "synthetic-top", HeadOID: headOID, Base: "main", State: "OPEN"}},
			}}

			currency, err := (Service{Tips: test.tips}).currency(context.Background(), plan)
			if err != nil {
				t.Fatalf("currency() error = %v", err)
			}
			if got := currency["synthetic-top"]; got != test.want {
				t.Errorf("currency = %+v, want %+v", got, test.want)
			}
		})
	}
}

// Without a reader there is nothing to say, and saying nothing is what every
// reader saw before. It must not become a claim that the pull request is
// current.
func TestCurrencyIsAbsentRatherThanAssumedWithoutAReader(t *testing.T) {
	plan := Plan{Discovery: stack.Discovery{
		Snapshot:     stack.Snapshot{Branches: []string{"synthetic-top"}},
		PullRequests: []githubstack.PullRequest{{Number: 7, Head: "synthetic-top", HeadOID: "pr-head", Base: "main", State: "OPEN"}},
	}}

	currency, err := (Service{}).currency(context.Background(), plan)
	if err != nil {
		t.Fatalf("currency() error = %v", err)
	}
	if _, reported := currency["synthetic-top"]; reported {
		t.Error("currency was reported with no reader to compute it from")
	}
}
