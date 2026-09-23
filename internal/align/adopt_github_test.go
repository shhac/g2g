package align

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/repair"
	"github.com/shhac/g2g/internal/shape"
	"github.com/shhac/g2g/internal/stack"
)

// fakePullRequests answers the way GitHub does: only for the heads it is asked
// about, which is what makes a remote-only base take a second round.
type fakePullRequests struct {
	bases map[string]string
	asked [][]string
}

func (f *fakePullRequests) Inspect(_ context.Context, branches []string) ([]githubstack.PullRequest, error) {
	f.asked = append(f.asked, slices.Clone(branches))
	prs := make([]githubstack.PullRequest, 0)
	for index, branch := range branches {
		if base, open := f.bases[branch]; open {
			prs = append(prs, githubstack.PullRequest{Number: 300 + index, Head: branch, Base: base, State: "OPEN"})
		}
	}
	return prs, nil
}

// fakeForks answers a merge base per pair, so a test can see that the fork
// point recorded is the one asked for and not the parent's tip.
type fakeForks struct {
	asked []string
	err   error
}

func (f *fakeForks) MergeBase(_ context.Context, one, other string) (string, error) {
	f.asked = append(f.asked, one+".."+other)
	if f.err != nil {
		return "", f.err
	}
	return "fork-" + one, nil
}

type fakeTrunks struct{ branch string }

func (f fakeTrunks) DefaultBranch(context.Context, string) (string, error) { return f.branch, nil }

// publishedStack is somebody else's stack, fetched and here:
//
//	synthetic-trunk
//	└─ synthetic-lower  based on synthetic-trunk
//	   └─ synthetic-top  based on synthetic-lower
func publishedStack() map[string]string {
	return map[string]string{
		"synthetic-lower": "synthetic-trunk",
		"synthetic-top":   "synthetic-lower",
	}
}

type gitHubAdoption struct {
	svc   Service
	store *memoryStore
	prs   *fakePullRequests
	forks *fakeForks
}

// pullRequestService wires the real pull request selector to fake answers, so
// what a stack means here is exactly what it means to status --from
// pull-request.
func pullRequestService(adopted graph.Graph, bases map[string]string, git fakeGit, trunk string) gitHubAdoption {
	store := &memoryStore{graph: adopted}
	prs := &fakePullRequests{bases: bases}
	forks := &fakeForks{}
	svc := Service{
		Git: git, Store: store, Refs: &fakeRefs{},
		PullRequests: stack.PullRequestSelector{Git: git, GitHub: prs},
		Forks:        forks,
		Trunks:       fakeTrunks{branch: trunk},
	}
	return gitHubAdoption{svc: svc, store: store, prs: prs, forks: forks}
}

func planFromPullRequests(t *testing.T, fixture gitHubAdoption, selection stack.Selection) AdoptPlan {
	t.Helper()
	plan, err := fixture.svc.PlanAdoptFromGitHub(context.Background(), selection)
	if err != nil {
		t.Fatalf("PlanAdoptFromGitHub() error = %v", err)
	}
	return plan
}

func offersCommand(note repair.Note, command string) bool {
	return slices.ContainsFunc(note.Ways, func(step repair.Step) bool { return step.Command == command })
}

// Picking up a colleague's stack is the reason this exists: the pull requests
// name every parent, the trunk they start from becomes the root, and each fork
// point is where the branch left its base.
func TestAdoptFromGitHubAdoptsAPublishedStack(t *testing.T) {
	fixture := pullRequestService(graph.New(), publishedStack(), everyBranchLocal(), "synthetic-trunk")

	plan := planFromPullRequests(t, fixture, stack.Selection{})
	if got, want := strings.Join(plan.Claims(), ","), "synthetic-lower,synthetic-top"; got != want {
		t.Fatalf("Claims() = %s, want %s parents first", got, want)
	}
	if plan.From != FromGitHub {
		t.Errorf("From = %q, want %q", plan.From, FromGitHub)
	}
	if !slices.Equal(plan.NewTrunks, []string{"synthetic-trunk"}) {
		t.Errorf("NewTrunks = %v, want the default branch as the root", plan.NewTrunks)
	}
	if err := fixture.svc.ApplyAdopt(context.Background(), plan); err != nil {
		t.Fatalf("ApplyAdopt() error = %v", err)
	}

	for branch, parent := range publishedStack() {
		edge := fixture.store.graph.Edges[branch]
		if edge.Parent != parent {
			t.Errorf("parent of %s = %q, want %q", branch, edge.Parent, parent)
		}
		if edge.ForkPoint != "fork-"+branch {
			t.Errorf("fork point of %s = %q, want the merge base with its base", branch, edge.ForkPoint)
		}
	}
	if want := []string{"synthetic-lower..synthetic-trunk", "synthetic-top..synthetic-lower"}; !slices.Equal(fixture.forks.asked, want) {
		t.Errorf("merge bases asked = %v, want %v", fixture.forks.asked, want)
	}
}

// The graph records local branches, and creating one is not something an
// import previews. A branch the pull requests place that is only on the
// remote refuses the whole import and says how to bring it here.
func TestAdoptFromGitHubRefusesABranchThatIsNotHere(t *testing.T) {
	bases := map[string]string{
		"synthetic-mid": "synthetic-trunk",
		"synthetic-top": "synthetic-mid",
	}
	git := fakeGit{local: []string{"synthetic-trunk", "synthetic-top"}}
	fixture := pullRequestService(graph.New(), bases, git, "synthetic-trunk")

	plan := planFromPullRequests(t, fixture, stack.Selection{})
	if plan.Blocked == "" {
		t.Fatalf("plan adopts %v with synthetic-mid only on the remote", plan.Claims())
	}
	for _, command := range []string{"git fetch && git switch synthetic-mid", "git branch synthetic-mid origin/synthetic-mid"} {
		if !offersCommand(plan.Repair, command) {
			t.Errorf("repair %+v does not offer %q", plan.Repair.Ways, command)
		}
	}
	if len(plan.Adopt) != 0 || len(fixture.forks.asked) != 0 {
		t.Errorf("a refused plan still adopted %v and asked %v", plan.Claims(), fixture.forks.asked)
	}
	if err := fixture.svc.ApplyAdopt(context.Background(), plan); err == nil {
		t.Error("ApplyAdopt() error = nil for a refused plan")
	}
	if len(fixture.store.writes) != 0 {
		t.Error("a refused import wrote the graph")
	}
}

// The additive rule is the same whichever record declared the edge: a branch
// g2g records under a different parent is a disagreement, and both answers are
// named rather than one chosen.
func TestAdoptFromGitHubRefusesAConflictingRecordedParent(t *testing.T) {
	ours := graph.Graph{
		Edges:  map[string]graph.Edge{"synthetic-top": {Parent: "synthetic-trunk"}},
		Trunks: []string{"synthetic-trunk"},
	}
	fixture := pullRequestService(ours, publishedStack(), everyBranchLocal(), "synthetic-trunk")

	plan := planFromPullRequests(t, fixture, stack.Selection{})
	if len(plan.Conflicts) != 1 || plan.Conflicts[0] != (Conflict{Branch: "synthetic-top", Ours: "synthetic-trunk", Theirs: "synthetic-lower"}) {
		t.Fatalf("Conflicts = %+v, want synthetic-top named with both parents", plan.Conflicts)
	}
	if !strings.Contains(plan.Blocked, "pull request's base") {
		t.Errorf("Blocked = %q, want the way out to name the pull request's side", plan.Blocked)
	}
	if err := fixture.svc.ApplyAdopt(context.Background(), plan); err == nil {
		t.Error("ApplyAdopt() error = nil for a conflicting plan")
	}
	if fixture.store.graph.Edges["synthetic-top"].Parent != "synthetic-trunk" {
		t.Error("a refused import changed the recorded parent")
	}
}

// Re-running over a stack already recorded the same way does nothing, which is
// what makes it safe to repeat after a colleague adds a branch on top.
func TestAdoptFromGitHubIsANoOpWhereTheGraphAgrees(t *testing.T) {
	ours := graph.Graph{
		Edges: map[string]graph.Edge{
			"synthetic-lower": {Parent: "synthetic-trunk"},
			"synthetic-top":   {Parent: "synthetic-lower"},
		},
		Trunks: []string{"synthetic-trunk"},
	}
	fixture := pullRequestService(ours, publishedStack(), everyBranchLocal(), "")

	plan := planFromPullRequests(t, fixture, stack.Selection{})
	if plan.Blocked != "" || len(plan.Adopt) != 0 {
		t.Fatalf("plan = blocked %q adopting %v, want nothing to do", plan.Blocked, plan.Claims())
	}
	if got := strings.Join(plan.Agreed, ","); got != "synthetic-lower,synthetic-top" {
		t.Errorf("Agreed = %s", got)
	}
	if len(fixture.forks.asked) != 0 {
		t.Errorf("asked for merge bases %v with nothing to adopt", fixture.forks.asked)
	}
}

// A base the g2g graph already records as a root is as good as the default
// branch: adopting onto it makes nothing a trunk that was not one.
func TestAdoptFromGitHubHangsFromARecordedRoot(t *testing.T) {
	ours := graph.Graph{
		Edges:  map[string]graph.Edge{"synthetic-other": {Parent: "synthetic-trunk"}},
		Trunks: []string{"synthetic-trunk"},
	}
	git := everyBranchLocal()
	git.local = append(git.local, "synthetic-other")
	fixture := pullRequestService(ours, publishedStack(), git, "")

	plan := planFromPullRequests(t, fixture, stack.Selection{})
	if plan.Blocked != "" {
		t.Fatalf("Blocked = %q for a stack on a recorded trunk", plan.Blocked)
	}
	if len(plan.NewTrunks) != 0 {
		t.Errorf("NewTrunks = %v, want none: the trunk was already recorded", plan.NewTrunks)
	}
}

// A stack can sit on a branch of the reader's own that g2g records.
func TestAdoptFromGitHubHangsFromARecordedBranch(t *testing.T) {
	ours := graph.Graph{
		Edges:  map[string]graph.Edge{"synthetic-mine": {Parent: "synthetic-trunk"}},
		Trunks: []string{"synthetic-trunk"},
	}
	bases := map[string]string{"synthetic-top": "synthetic-mine"}
	git := fakeGit{local: []string{"synthetic-trunk", "synthetic-mine", "synthetic-top"}}
	fixture := pullRequestService(ours, bases, git, "")

	plan := planFromPullRequests(t, fixture, stack.Selection{})
	if plan.Blocked != "" {
		t.Fatalf("Blocked = %q for a stack on a recorded branch", plan.Blocked)
	}
	if got := strings.Join(plan.Claims(), ","); got != "synthetic-top" {
		t.Errorf("Claims() = %s, want synthetic-top", got)
	}
}

// Recording under a base nothing establishes would make it a trunk. That is
// the guess create refuses, and the refusal names both ways to establish it.
func TestAdoptFromGitHubRefusesABaseThatIsNotATrunk(t *testing.T) {
	fixture := pullRequestService(graph.New(), publishedStack(), everyBranchLocal(), "")

	plan := planFromPullRequests(t, fixture, stack.Selection{})
	if plan.Blocked == "" {
		t.Fatalf("plan adopts %v onto a base nothing records", plan.Claims())
	}
	for _, command := range []string{"g2g adopt --branch synthetic-trunk", "g2g track --branch synthetic-lower --parent synthetic-trunk"} {
		if !offersCommand(plan.Repair, command) {
			t.Errorf("repair %+v does not offer %q", plan.Repair.Ways, command)
		}
	}
}

// A base that has moved on since the pull request was opened is ordinary. The
// edge is still recorded, and the preview is told it will need a restack.
func TestAdoptFromGitHubReportsWhatGitDoesNotYetShow(t *testing.T) {
	git := everyBranchLocal()
	git.ancestors = map[string]string{"synthetic-lower": "synthetic-trunk"}
	fixture := pullRequestService(graph.New(), publishedStack(), git, "synthetic-trunk")

	plan := planFromPullRequests(t, fixture, stack.Selection{})
	if !slices.Equal(plan.Unconfirmed, []string{"synthetic-top"}) {
		t.Errorf("Unconfirmed = %v, want synthetic-top", plan.Unconfirmed)
	}
	if got := plan.Updated.Edges["synthetic-top"].Origin; got != graph.OriginUser {
		t.Errorf("origin = %q, want the edge recorded as asserted", got)
	}
}

// Revalidation reads GitHub again. A base retargeted between the preview and
// the write is exactly what it exists to catch.
func TestRevalidateAdoptFromGitHubRereadsThePullRequests(t *testing.T) {
	fixture := pullRequestService(graph.New(), publishedStack(), everyBranchLocal(), "synthetic-trunk")
	preview := planFromPullRequests(t, fixture, stack.Selection{})
	rounds := len(fixture.prs.asked)

	fixture.prs.bases = map[string]string{
		"synthetic-lower": "synthetic-trunk",
		"synthetic-top":   "synthetic-trunk",
	}
	if _, err := fixture.svc.RevalidateAdoptFromGitHub(context.Background(), stack.Selection{}, preview); err == nil {
		t.Error("RevalidateAdoptFromGitHub() error = nil after a base was retargeted")
	}
	if len(fixture.prs.asked) <= rounds {
		t.Error("revalidation reused the preview's reading instead of asking GitHub again")
	}
}

func TestRevalidateAdoptFromGitHubAcceptsAnUnchangedStack(t *testing.T) {
	fixture := pullRequestService(graph.New(), publishedStack(), everyBranchLocal(), "synthetic-trunk")
	preview := planFromPullRequests(t, fixture, stack.Selection{})

	if _, err := fixture.svc.RevalidateAdoptFromGitHub(context.Background(), stack.Selection{}, preview); err != nil {
		t.Errorf("RevalidateAdoptFromGitHub() error = %v for an unchanged stack", err)
	}
}

// Reading pull requests is not reading Graphite, so a repository that has
// never used Graphite imports from them without the enrolment gate refusing,
// and without Graphite being asked anything.
func TestAdoptFromGitHubNeverAsksGraphite(t *testing.T) {
	fixture := pullRequestService(graph.New(), publishedStack(), everyBranchLocal(), "synthetic-trunk")
	asked := false
	fixture.svc.Graphite = &fakeGraphite{forest: declaredChain(), asked: &asked}
	fixture.svc.Configured = func(context.Context) (bool, error) { return false, nil }

	planFromPullRequests(t, fixture, stack.Selection{})
	if asked {
		t.Error("an import from pull requests read Graphite")
	}
}

// Only a scope that opens with the base can be checked against the root rule,
// and all would reach trunks nobody named.
func TestAdoptFromGitHubRefusesAScopeItDoesNotOffer(t *testing.T) {
	fixture := pullRequestService(graph.New(), publishedStack(), everyBranchLocal(), "synthetic-trunk")

	for _, scope := range []shape.Scope{shape.ScopePath, shape.ScopeSubtree, shape.ScopeAll} {
		if _, err := fixture.svc.PlanAdoptFromGitHub(context.Background(), stack.Selection{Scope: scope}); err == nil {
			t.Errorf("scope %s: error = nil", scope)
		}
	}
}

func TestAdoptFromGitHubFailsClosedWhenGitCannotSayWhereABranchForked(t *testing.T) {
	fixture := pullRequestService(graph.New(), publishedStack(), everyBranchLocal(), "synthetic-trunk")
	fixture.forks.err = fmt.Errorf("synthetic merge-base failure")

	if _, err := fixture.svc.PlanAdoptFromGitHub(context.Background(), stack.Selection{}); err == nil {
		t.Error("PlanAdoptFromGitHub() error = nil when no fork point could be found")
	}
}

func TestAdoptFromGitHubNeedsItsOwnDependencies(t *testing.T) {
	svc := Service{Git: everyBranchLocal(), Store: &memoryStore{graph: graph.New()}, Graphite: &fakeGraphite{}}

	if _, err := svc.PlanAdoptFromGitHub(context.Background(), stack.Selection{}); err == nil {
		t.Error("PlanAdoptFromGitHub() error = nil without a pull request reader")
	}
}
