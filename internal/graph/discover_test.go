package graph

import (
	"context"
	"errors"
	"github.com/shhac/g2g/internal/testutil"
	"strings"
	"testing"
)

// fakeAncestry is an injected Git. A decision matrix over ancestry answers is
// exactly where spawning a process per case buys nothing.
// Two other packages keep reduced copies of this — internal/cli's graphGit and
// internal/stack's g2gAncestry — for the reason recorded at
// internal/stack/g2g_test.go: a fake shared across a package boundary is a
// second interface nobody declared. Only the answers are shared, through
// internal/testutil.
type fakeAncestry struct {
	current   string
	local     []string
	ancestors map[string][]string
	// behind maps "other..target" to the commits the target has that other
	// does not. Anything unlisted defaults to one commit behind, which keeps a
	// case that is only about the candidate set free of noise.
	behind map[string]int
	// objects maps a revision to the object id it resolves to; an unlisted
	// name resolves to itself, which keeps cases that are not about object
	// ids free of noise.
	objects      map[string]string
	unresolvable map[string]bool
	err          error
}

// Resolve mirrors git rev-parse over that table.
func (f fakeAncestry) Resolve(_ context.Context, revision string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	if f.unresolvable[revision] {
		return "", errors.New("not a commit in this repository")
	}
	if resolved, listed := f.objects[revision]; listed {
		return resolved, nil
	}
	return revision, nil
}

func (f fakeAncestry) CurrentBranch(context.Context) (string, error) { return f.current, f.err }

func (f fakeAncestry) LocalBranches(context.Context) ([]string, error) { return f.local, f.err }

func (f fakeAncestry) AncestorBranches(_ context.Context, target string) ([]string, error) {
	return f.ancestors[target], f.err
}

// Divergence answers from the same ancestor map the rest of the fake uses: an
// ancestor has nothing ahead, a descendant has nothing behind.
func (f fakeAncestry) Divergence(_ context.Context, other, target string) (int, int, error) {
	if f.err != nil {
		return 0, 0, f.err
	}
	ancestor, _ := f.IsAncestor(context.Background(), other, target)
	descendant, _ := f.IsAncestor(context.Background(), target, other)
	behind, listed := f.behind[other+".."+target]
	if !listed {
		behind = 1
	}
	if descendant {
		behind = 0
	}
	ahead := 1
	if ancestor {
		ahead = 0
	}
	return ahead, behind, nil
}

func (f fakeAncestry) IsAncestor(_ context.Context, ancestor, descendant string) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	for _, candidate := range f.ancestors[descendant] {
		if candidate == ancestor {
			return true, nil
		}
	}
	return false, nil
}

func TestCandidatesOrderNearestAncestorFirst(t *testing.T) {
	git := fakeAncestry{
		local:     []string{"synthetic-auth", "synthetic-base", "synthetic-login"},
		ancestors: map[string][]string{"synthetic-login": {"synthetic-auth", "synthetic-base"}},
		behind:    map[string]int{"synthetic-auth..synthetic-login": 1, "synthetic-base..synthetic-login": 6},
	}

	candidates, err := Candidates(context.Background(), git, "synthetic-login", nil)
	if err != nil {
		t.Fatalf("Candidates() error = %v", err)
	}
	if names := branchNames(candidates); names != "synthetic-auth,synthetic-base" {
		t.Errorf("Candidates() = %s, want the nearest ancestor first", names)
	}
}

// Once a trunk moves ahead its tip stops being reachable from the branches
// built on it, so a stack's bottom branch has no ancestors at all. Offering
// trunks regardless is what stops that being a dead end.
func TestCandidatesAlwaysOfferTrunksEvenWhenTheyAreNotAncestors(t *testing.T) {
	git := fakeAncestry{
		local:     []string{"synthetic-auth", "synthetic-main"},
		ancestors: map[string][]string{"synthetic-auth": nil},
	}

	candidates, err := Candidates(context.Background(), git, "synthetic-auth", []string{"synthetic-main"})
	if err != nil {
		t.Fatalf("Candidates() error = %v", err)
	}
	if len(candidates) != 1 || candidates[0].Branch != "synthetic-main" {
		t.Fatalf("Candidates() = %#v, want the trunk offered", candidates)
	}
	if candidates[0].Ancestor {
		t.Error("a moved trunk should be offered without being reported as an ancestor")
	}
	if !candidates[0].Trunk {
		t.Error("the trunk should be marked as one")
	}
}

func TestCandidatesDoNotOfferATrunkTwice(t *testing.T) {
	git := fakeAncestry{
		local:     []string{"synthetic-auth", "synthetic-main"},
		ancestors: map[string][]string{"synthetic-auth": {"synthetic-main"}},
		behind:    map[string]int{"synthetic-main..synthetic-auth": 2},
	}

	candidates, err := Candidates(context.Background(), git, "synthetic-auth", []string{"synthetic-main"})
	if err != nil {
		t.Fatalf("Candidates() error = %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("Candidates() = %#v, want one entry", candidates)
	}
	if !candidates[0].Ancestor || !candidates[0].Trunk {
		t.Errorf("candidate = %#v, want it marked as both an ancestor and a trunk", candidates[0])
	}
}

func TestCandidatesNeverOfferTheTargetItself(t *testing.T) {
	git := fakeAncestry{
		local:     []string{"synthetic-auth", "synthetic-main"},
		ancestors: map[string][]string{"synthetic-auth": nil},
	}

	candidates, err := Candidates(context.Background(), git, "synthetic-auth", []string{"synthetic-auth", "synthetic-main"})
	if err != nil {
		t.Fatalf("Candidates() error = %v", err)
	}
	if names := branchNames(candidates); names != "synthetic-main" {
		t.Errorf("Candidates() = %s, want the target excluded", names)
	}
}

// Offering a deleted root would name a parent that could never be validated.
func TestCandidatesSkipARecordedRootThatIsNoLongerLocal(t *testing.T) {
	git := fakeAncestry{
		local:     []string{"synthetic-auth", "synthetic-main"},
		ancestors: map[string][]string{"synthetic-auth": {"synthetic-main"}},
	}

	candidates, err := Candidates(context.Background(), git, "synthetic-auth", []string{"synthetic-main", "synthetic-deleted"})
	if err != nil {
		t.Fatalf("Candidates() error = %v", err)
	}
	if names := branchNames(candidates); names != "synthetic-main" {
		t.Errorf("Candidates() = %s, want the deleted root omitted", names)
	}
}

// A descendant already contains the target, so it can never be its parent.
func TestCandidatesExcludeDescendants(t *testing.T) {
	git := fakeAncestry{
		local:     []string{"synthetic-auth", "synthetic-login", "synthetic-main"},
		ancestors: map[string][]string{"synthetic-login": {"synthetic-auth"}, "synthetic-auth": nil},
	}

	// No ancestors and no recorded roots, so every local branch is measured.
	candidates, err := Candidates(context.Background(), git, "synthetic-auth", nil)
	if err != nil {
		t.Fatalf("Candidates() error = %v", err)
	}
	for _, candidate := range candidates {
		if candidate.Branch == "synthetic-login" {
			t.Fatalf("Candidates() = %s, want the descendant excluded", branchNames(candidates))
		}
	}
	if names := branchNames(candidates); names != "synthetic-main" {
		t.Errorf("Candidates() = %s, want the fork-point fallback to find the trunk", names)
	}
}

func TestCandidatesRequireATargetAndAGit(t *testing.T) {
	if _, err := Candidates(context.Background(), fakeAncestry{}, "", nil); err == nil {
		t.Error("Candidates() error = nil without a target")
	}
	if _, err := Candidates(context.Background(), nil, "synthetic-a", nil); err == nil {
		t.Error("Candidates() error = nil without a Git boundary")
	}
}

func TestCandidatesPropagateGitFailures(t *testing.T) {
	git := fakeAncestry{err: errors.New("synthetic git failure")}

	if _, err := Candidates(context.Background(), git, "synthetic-a", nil); err == nil {
		t.Fatal("Candidates() error = nil")
	}
}

func TestAssessClassifiesEveryBranchState(t *testing.T) {
	tracked := forest()
	git := fakeAncestry{
		local: []string{"synthetic-main", "synthetic-auth", "synthetic-login", "synthetic-session", "synthetic-billing"},
		ancestors: map[string][]string{
			// login still sits on auth; session's parent moved underneath it.
			"synthetic-login":   {"synthetic-auth"},
			"synthetic-session": nil,
			"synthetic-auth":    {"synthetic-main"},
		},
	}

	states, err := Assess(context.Background(), git, tracked, []string{"synthetic-login", "synthetic-session", "synthetic-absent"})
	if err != nil {
		t.Fatalf("Assess() error = %v", err)
	}

	for branch, want := range map[string]NodeState{
		"synthetic-login":   StateAligned,
		"synthetic-session": StateNeedsRestack,
		"synthetic-absent":  StateUntracked,
	} {
		if states[branch] != want {
			t.Errorf("%s = %q, want %q", branch, states[branch], want)
		}
	}
}

// A squash-merged and deleted parent is the ordinary end of a stack's life.
// It must read as a missing parent rather than as a branch needing a restack,
// because the two have different remedies.
func TestAssessReportsAMergedAndDeletedParentAsMissing(t *testing.T) {
	git := fakeAncestry{local: []string{"synthetic-main", "synthetic-login"}}

	states, err := Assess(context.Background(), git, forest(), []string{"synthetic-login"})
	if err != nil {
		t.Fatalf("Assess() error = %v", err)
	}
	if states["synthetic-login"] != StateParentMissing {
		t.Errorf("state = %q, want %q", states["synthetic-login"], StateParentMissing)
	}
}

func TestAssessRequiresAGit(t *testing.T) {
	if _, err := Assess(context.Background(), nil, forest(), nil); err == nil {
		t.Fatal("Assess() error = nil without a Git boundary")
	}
}

func TestSelectWidensWithScope(t *testing.T) {
	for scope, want := range map[Scope]string{
		ScopeBranch:  "synthetic-auth",
		ScopePath:    "synthetic-main,synthetic-auth",
		ScopeSubtree: "synthetic-auth,synthetic-login,synthetic-session",
		ScopeTrunk:   "synthetic-main,synthetic-auth,synthetic-login,synthetic-session,synthetic-billing",
	} {
		t.Run(string(scope), func(t *testing.T) {
			got, err := forest().Select("synthetic-auth", scope)
			if err != nil {
				t.Fatalf("Select() error = %v", err)
			}
			if strings.Join(got, ",") != want {
				t.Errorf("Select(%q) = %v, want %s", scope, got, want)
			}
		})
	}
}

func TestSelectRejectsAnUnknownScope(t *testing.T) {
	if _, err := forest().Select("synthetic-auth", Scope("everything")); err == nil {
		t.Fatal("Select() error = nil")
	}
}

func branchNames(candidates []Candidate) string {
	names := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		names = append(names, candidate.Branch)
	}
	return strings.Join(names, ",")
}

// The ancestor probe belongs on the fork point, not the parent. An amended
// parent stops being an ancestor of its children even though nothing is wrong
// with them, which is exactly the case a restack repairs.
func TestAssessDistinguishesAMovedParentFromAMovedBranch(t *testing.T) {
	tracked := Graph{Edges: map[string]Edge{
		"synthetic-child": {Parent: "synthetic-parent", ForkPoint: "fork-old"},
	}}

	for name, test := range map[string]struct {
		git  fakeAncestry
		want NodeState
	}{
		"parent moved, branch intact": {
			// The fork point is still in the child's history; the parent's tip
			// has moved past it.
			git: fakeAncestry{
				local:     []string{"synthetic-parent", "synthetic-child"},
				objects:   map[string]string{"synthetic-parent": "parent-new", "fork-old": "fork-old"},
				ancestors: map[string][]string{"synthetic-child": {"fork-old"}},
			},
			want: StateNeedsRestack,
		},
		"branch rewritten off its base": {
			// Someone rebased the child by hand, so its recorded base is gone
			// from its history and the replay range would be meaningless.
			git: fakeAncestry{
				local:     []string{"synthetic-parent", "synthetic-child"},
				objects:   map[string]string{"synthetic-parent": "fork-old", "fork-old": "fork-old"},
				ancestors: map[string][]string{"synthetic-child": {}},
			},
			want: StateMovedOffParent,
		},
		"nothing moved": {
			git: fakeAncestry{
				local:     []string{"synthetic-parent", "synthetic-child"},
				objects:   map[string]string{"synthetic-parent": "fork-old", "fork-old": "fork-old"},
				ancestors: map[string][]string{"synthetic-child": {"fork-old"}},
			},
			want: StateAligned,
		},
		"fork point collected": {
			git: fakeAncestry{
				local:        []string{"synthetic-parent", "synthetic-child"},
				objects:      map[string]string{"synthetic-parent": "parent-new"},
				unresolvable: map[string]bool{"fork-old": true},
			},
			want: StateForkUnresolvable,
		},
	} {
		t.Run(name, func(t *testing.T) {
			states, err := Assess(context.Background(), test.git, tracked, []string{"synthetic-child"})
			if err != nil {
				t.Fatalf("Assess() error = %v", err)
			}
			if states["synthetic-child"] != test.want {
				t.Errorf("state = %q, want %q", states["synthetic-child"], test.want)
			}
		})
	}
}

// Only aligned and needs-restack describe a branch a rebase may act on.
func TestRestackableStates(t *testing.T) {
	for state, want := range map[NodeState]bool{
		StateAligned: true, StateNeedsRestack: true,
		StateMovedOffParent: false, StateParentMissing: false,
		StateForkUnresolvable: false, StateUntracked: false,
	} {
		if state.Restackable() != want {
			t.Errorf("%q.Restackable() = %t, want %t", state, state.Restackable(), want)
		}
	}
}

// An edge adopted before fork points existed must keep working rather than
// reporting a state its writer never saw.
func TestAssessToleratesAnEdgeWithNoForkPoint(t *testing.T) {
	legacy := Graph{Edges: map[string]Edge{"synthetic-child": {Parent: "synthetic-parent"}}}
	git := fakeAncestry{
		local:     []string{"synthetic-parent", "synthetic-child"},
		ancestors: map[string][]string{"synthetic-child": {"synthetic-parent"}},
	}

	states, err := Assess(context.Background(), git, legacy, []string{"synthetic-child"})
	if err != nil {
		t.Fatalf("Assess() error = %v", err)
	}
	if states["synthetic-child"] != StateAligned {
		t.Errorf("state = %q, want %q", states["synthetic-child"], StateAligned)
	}
}

// Cherry reports every commit as absent from the trunk unless a case says
// otherwise, so a branch reads as landed only where that is the subject.
func (f fakeAncestry) Cherry(_ context.Context, _, head, _ string) (absent, present []string, err error) {
	return testutil.OwnCommits(head), nil, nil
}
