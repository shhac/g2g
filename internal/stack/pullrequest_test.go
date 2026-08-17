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

// A base outside the checkout cannot be rendered or acted on, so the branch
// reads as a root rather than hanging from something absent.
func TestABaseThatIsNotLocalReadsAsARoot(t *testing.T) {
	selector, _ := syntheticSelector([]githubstack.PullRequest{
		{Number: 11, Head: "synthetic-a", Base: "synthetic-absent", State: "OPEN"},
	})

	snapshot, err := selector.Select(context.Background(), Selection{Branch: "synthetic-a", Scope: ScopePath}, "synthetic command")
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if snapshot.Base != "synthetic-a" {
		t.Errorf("base = %q, want the branch itself once its base is not local", snapshot.Base)
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
