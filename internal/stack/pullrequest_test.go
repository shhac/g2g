package stack

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/githubstack"
)

type prGit struct {
	current  string
	branches []string
}

func (g prGit) CurrentBranch(context.Context) (string, error) { return g.current, nil }
func (g prGit) LocalBranches(context.Context) ([]string, error) {
	return append([]string(nil), g.branches...), nil
}

type prGitHub struct {
	prs   []githubstack.PullRequest
	calls int
	err   error
}

func (g *prGitHub) Inspect(context.Context, []string) ([]githubstack.PullRequest, error) {
	g.calls++
	if g.err != nil {
		return nil, g.err
	}
	return append([]githubstack.PullRequest(nil), g.prs...), nil
}

// syntheticPRs is the shape GitHub holds for a stack somebody has published:
//
//	synthetic-trunk
//	└─ synthetic-a   #11 based on synthetic-trunk
//	   ├─ synthetic-b   #12 based on synthetic-a
//	   └─ synthetic-c   #13 based on synthetic-a
func syntheticPRs() []githubstack.PullRequest {
	return []githubstack.PullRequest{
		{Number: 11, Head: "synthetic-a", Base: "synthetic-trunk", State: "OPEN"},
		{Number: 12, Head: "synthetic-b", Base: "synthetic-a", State: "OPEN"},
		{Number: 13, Head: "synthetic-c", Base: "synthetic-a", State: "OPEN"},
	}
}

func syntheticSelector(prs []githubstack.PullRequest) (PullRequestSelector, *prGitHub) {
	github := &prGitHub{prs: prs}
	return PullRequestSelector{
		Git:    prGit{current: "synthetic-b", branches: []string{"synthetic-trunk", "synthetic-a", "synthetic-b", "synthetic-c"}},
		GitHub: github,
	}, github
}

// The structure GitHub already holds, rendered as the tree it is. This is the
// source the design named and never built, which is why a repository with an
// open stack and no Graphite had no way to see its own shape.
func TestSelectBuildsTheTreeFromPullRequestBases(t *testing.T) {
	selector, _ := syntheticSelector(syntheticPRs())

	snapshot, err := selector.Select(context.Background(), Selection{Branch: "synthetic-a", Scope: ScopeStack}, "synthetic command")
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if snapshot.Base != "synthetic-trunk" {
		t.Errorf("base = %q, want the branch its pull request is based on", snapshot.Base)
	}
	if got, want := strings.Join(snapshot.Branches, ","), "synthetic-a,synthetic-b,synthetic-c"; got != want {
		t.Errorf("branches = %q, want %q", got, want)
	}
	// The fork is carried, so a renderer can draw it rather than re-deriving it.
	for branch, want := range map[string]string{"synthetic-b": "synthetic-a", "synthetic-c": "synthetic-a"} {
		if got := snapshot.Parents[branch]; got != want {
			t.Errorf("parent of %q = %q, want %q", branch, got, want)
		}
	}
}

// Every scope means here what it means anywhere else.
func TestPullRequestSourceHonoursTheScope(t *testing.T) {
	selector, _ := syntheticSelector(syntheticPRs())

	for _, test := range []struct {
		scope      Scope
		want, base string
	}{
		{ScopeBranch, "synthetic-b", "synthetic-a"},
		{ScopePath, "synthetic-a,synthetic-b", "synthetic-trunk"},
		{ScopeSubtree, "synthetic-b", "synthetic-a"},
	} {
		t.Run(string(test.scope), func(t *testing.T) {
			snapshot, err := selector.Select(context.Background(), Selection{Branch: "synthetic-b", Scope: test.scope}, "synthetic command")
			if err != nil {
				t.Fatalf("Select() error = %v", err)
			}
			if got := strings.Join(snapshot.Branches, ","); got != test.want {
				t.Errorf("branches = %q, want %q", got, test.want)
			}
			if snapshot.Base != test.base {
				t.Errorf("base = %q, want %q", snapshot.Base, test.base)
			}
		})
	}
}

// Two open pull requests for one branch is the one ambiguity this tool refuses
// to interpret everywhere else, so it contributes no edge here either.
func TestABranchWithTwoOpenPullRequestsDescribesNothing(t *testing.T) {
	prs := append(syntheticPRs(), githubstack.PullRequest{Number: 14, Head: "synthetic-b", Base: "synthetic-trunk", State: "OPEN"})
	selector, _ := syntheticSelector(prs)

	described, err := selector.Describes(context.Background(), "synthetic-b")
	if err != nil {
		t.Fatalf("Describes() error = %v", err)
	}
	if described {
		t.Error("a branch with two open pull requests was treated as described")
	}
	if _, err := selector.Select(context.Background(), Selection{Branch: "synthetic-b"}, "synthetic command"); err == nil {
		t.Error("Select() error = nil for an ambiguous branch")
	}
}

// A branch with no open pull request is simply not published, which is the
// inherent limit of reading effect rather than intent.
func TestAnUnpublishedBranchDescribesNothing(t *testing.T) {
	selector, _ := syntheticSelector(syntheticPRs()[:1])

	described, err := selector.Describes(context.Background(), "synthetic-b")
	if err != nil {
		t.Fatalf("Describes() error = %v", err)
	}
	if described {
		t.Error("a branch with no open pull request was treated as described")
	}
}

// A base that is not on this machine is where the branch actually hangs, so it
// is placed there rather than dropped. This used to read as a root — which was
// wrong in a way nothing showed: the branch really does have a parent, it is
// just not one this checkout has.
//
// The base is not acted on locally by any command that uses it — link, submit
// and retarget all name it to GitHub, which has it — so it being absent is
// reported rather than refused.
func TestABaseThatIsNotLocalIsStillWhereTheBranchHangs(t *testing.T) {
	selector, _ := syntheticSelector([]githubstack.PullRequest{
		{Number: 11, Head: "synthetic-a", Base: "synthetic-absent", State: "OPEN"},
	})

	snapshot, err := selector.Select(context.Background(), Selection{Branch: "synthetic-a", Scope: ScopePath}, "synthetic command")
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if snapshot.Base != "synthetic-absent" {
		t.Errorf("base = %q, want the branch the pull request is actually based on", snapshot.Base)
	}
	if got := strings.Join(snapshot.Branches, ","); got != "synthetic-a" {
		t.Errorf("branches = %q, want only the branch this checkout has", got)
	}
	// Nothing selected is absent, so nothing is refused: the base is GitHub's
	// to resolve, not this machine's.
	if err := snapshot.RequireActionable("g2g link"); err != nil {
		t.Errorf("RequireActionable() = %v; only a selected branch may block a mutation", err)
	}
}

// This source is never consulted by precedence, because asking it means
// invoking gh and push must never do that. It answers only when named.
func TestThePullRequestSourceIsReachableOnlyOnRequest(t *testing.T) {
	selector, github := syntheticSelector(syntheticPRs())
	git := prGit{current: "synthetic-b", branches: []string{"synthetic-trunk", "synthetic-a", "synthetic-b", "synthetic-c"}}
	resolver := Resolver{
		Git:       git,
		Selectors: []Selector{refusingSelector{}},
		OnRequest: []Selector{selector},
	}

	// Unpinned: the on-request source is never asked, so GitHub is never called.
	if _, err := resolver.Select(context.Background(), Selection{}, "synthetic command"); err == nil {
		t.Error("Select() error = nil when no consulted source describes the branch")
	}
	if github.calls != 0 {
		t.Errorf("GitHub was called %d times without --from; a command that must not invoke gh would have", github.calls)
	}

	// Pinned: it answers.
	snapshot, err := resolver.Select(context.Background(), Selection{From: SourcePullRequest, Scope: ScopePath}, "synthetic command")
	if err != nil {
		t.Fatalf("Select(--from pull-request) error = %v", err)
	}
	if snapshot.Source != SourcePullRequest {
		t.Errorf("source = %q, want %q", snapshot.Source, SourcePullRequest)
	}
	if github.calls == 0 {
		t.Error("GitHub was never called even when the source was named")
	}
}

// An unknown source names what this build actually has, including the ones that
// only answer on request.
func TestAnUnknownSourceListsTheOnRequestOnesToo(t *testing.T) {
	resolver := Resolver{
		Git:       prGit{current: "synthetic-b", branches: []string{"synthetic-b"}},
		Selectors: []Selector{refusingSelector{}},
		OnRequest: []Selector{PullRequestSelector{}},
	}

	_, err := resolver.Select(context.Background(), Selection{From: "synthetic-nonsense"}, "synthetic command")
	if err == nil || !strings.Contains(err.Error(), string(SourcePullRequest)) {
		t.Errorf("error = %v, want it to list pull-request among this build's sources", err)
	}
}

type refusingSelector struct{}

func (refusingSelector) Source() Source                                  { return SourceG2G }
func (refusingSelector) Describes(context.Context, string) (bool, error) { return false, nil }
func (refusingSelector) Select(context.Context, Selection, string) (Snapshot, error) {
	return Snapshot{}, fmt.Errorf("synthetic selector should not have been asked")
}

// A trunk carries no pull request of its own — it only ever appears as
// somebody else's base — so asking whether the forest holds an edge *for* it
// answered no, and `g2g status --from pull-request` refused from main with
// "pull-request does not describe \"main\"".
//
// Standing on the trunk is where a person looks at what is outstanding, so it
// is the ordinary case. The question the source has to answer is whether it
// places the branch, and a root is placed: it is what the stacks hang from.
func TestThePullRequestSourceDescribesATrunkItIsTheBaseOf(t *testing.T) {
	selector, _ := syntheticSelector(syntheticPRs())

	describes, err := selector.Describes(context.Background(), "synthetic-trunk")
	if err != nil {
		t.Fatalf("Describes() error = %v", err)
	}
	if !describes {
		t.Fatal("the source does not describe the trunk every open pull request is based on")
	}

	snapshot, err := selector.Select(context.Background(), Selection{Branch: "synthetic-trunk", Scope: ScopeStack}, "synthetic command")
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if snapshot.Base != "synthetic-trunk" {
		t.Errorf("base = %q, want the trunk itself", snapshot.Base)
	}
	if got, want := strings.Join(snapshot.Branches, ","), "synthetic-a,synthetic-b,synthetic-c"; got != want {
		t.Errorf("branches = %q, want %q", got, want)
	}
}

// A branch nothing is based on, and which has no pull request of its own, is
// still not described. The widened question must not become "any branch".
func TestABranchNoPullRequestMentionsIsStillNotDescribed(t *testing.T) {
	selector, _ := syntheticSelector(syntheticPRs())

	describes, err := selector.Describes(context.Background(), "synthetic-unpublished")
	if err != nil {
		t.Fatalf("Describes() error = %v", err)
	}
	if describes {
		t.Error("a branch no pull request mentions was described")
	}
}

// A stack published from somebody else's checkout joins two local subtrees
// through a branch nobody here has. Dropping that edge made the lower subtree
// read as a root of its own, so the two looked unrelated — and looked that way
// with no indication anything had been left out.
func TestASelectionCarriesABranchThatIsNotOnThisMachine(t *testing.T) {
	prs := []githubstack.PullRequest{
		{Number: 11, Head: "synthetic-a", Base: "synthetic-trunk", State: "OPEN"},
		{Number: 12, Head: "synthetic-remote", Base: "synthetic-a", State: "OPEN"},
		{Number: 13, Head: "synthetic-c", Base: "synthetic-remote", State: "OPEN"},
	}
	selector := PullRequestSelector{
		// synthetic-remote is deliberately absent from the checkout.
		Git:    prGit{current: "synthetic-c", branches: []string{"synthetic-trunk", "synthetic-a", "synthetic-c"}},
		GitHub: &prGitHub{prs: prs},
	}

	snapshot, err := selector.Select(context.Background(), Selection{Branch: "synthetic-c", Scope: ScopePath}, "synthetic command")
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}

	if got, want := strings.Join(snapshot.Branches, ","), "synthetic-a,synthetic-remote,synthetic-c"; got != want {
		t.Errorf("branches = %q, want %q · the chain joins through the absent branch", got, want)
	}
	if got := strings.Join(snapshot.Absent, ","); got != "synthetic-remote" {
		t.Errorf("Absent = %q, want the branch this checkout does not have", got)
	}
	if snapshot.Base != "synthetic-trunk" {
		t.Errorf("base = %q, want the trunk the chain reaches", snapshot.Base)
	}
}

// Structure is not permission. A branch that is not here has no ref to push,
// rewrite, or link, so every command that mutates refuses and names it.
func TestAMutationRefusesABranchThatIsNotOnThisMachine(t *testing.T) {
	snapshot := Snapshot{Branches: []string{"synthetic-a", "synthetic-remote"}, Absent: []string{"synthetic-remote"}}

	err := snapshot.RequireActionable("g2g link")
	if err == nil {
		t.Fatal("RequireActionable() = nil for a selection containing an absent branch")
	}
	for _, want := range []string{"g2g link", "synthetic-remote", "not a local branch"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not mention %q", err, want)
		}
	}
	if err := (Snapshot{Branches: []string{"synthetic-a"}}).RequireActionable("g2g link"); err != nil {
		t.Errorf("RequireActionable() = %v for an entirely local selection", err)
	}
}

// Resolution asks Describes and then Select, and each needs the whole
// structure. Building it per question doubles every round of the walk, so a
// selector reads it once for the invocation it belongs to.
func TestTheStructureIsReadOncePerInvocation(t *testing.T) {
	github := &prGitHub{prs: syntheticPRs()}
	selector := NewPullRequestSelector(
		prGit{current: "synthetic-b", branches: []string{"synthetic-trunk", "synthetic-a", "synthetic-b", "synthetic-c"}},
		github,
	)

	if _, err := selector.Describes(context.Background(), "synthetic-b"); err != nil {
		t.Fatalf("Describes() error = %v", err)
	}
	if _, err := selector.Select(context.Background(), Selection{Branch: "synthetic-b", Scope: ScopeStack}, "synthetic command"); err != nil {
		t.Fatalf("Select() error = %v", err)
	}

	// One round covers this forest: every base is already local.
	if github.calls != 1 {
		t.Errorf("read the structure %d times for one invocation, want 1", github.calls)
	}
}

// A selector built as a plain literal still works; it simply reads more than
// once. Correctness must not depend on remembering the constructor.
func TestASelectorWithoutTheMemoStillResolves(t *testing.T) {
	selector, github := syntheticSelector(syntheticPRs())

	if _, err := selector.Describes(context.Background(), "synthetic-b"); err != nil {
		t.Fatalf("Describes() error = %v", err)
	}
	snapshot, err := selector.Select(context.Background(), Selection{Branch: "synthetic-b", Scope: ScopeStack}, "synthetic command")
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if got, want := strings.Join(snapshot.Branches, ","), "synthetic-a,synthetic-b"; got != want {
		t.Errorf("branches = %q, want %q", got, want)
	}
	if github.calls < 2 {
		t.Errorf("calls = %d; this case exists to cover the uncached path", github.calls)
	}
}
