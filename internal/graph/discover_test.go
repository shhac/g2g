package graph

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeAncestry is an injected Git. A decision matrix over ancestry answers is
// exactly where spawning a process per case buys nothing.
type fakeAncestry struct {
	current   string
	local     []string
	ancestors map[string][]string
	// behind maps "other..target" to the commits the target has that other
	// does not. Anything unlisted defaults to one commit behind, which keeps a
	// case that is only about the candidate set free of noise.
	behind map[string]int
	err    error
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

func TestParseScopeAcceptsEveryValueAndDefaultsToPath(t *testing.T) {
	got, err := ParseScope("")
	if err != nil || got != ScopePath {
		t.Errorf("ParseScope(\"\") = %q, %v; want path", got, err)
	}
	for _, scope := range Scopes {
		if parsed, err := ParseScope(string(scope)); err != nil || parsed != scope {
			t.Errorf("ParseScope(%q) = %q, %v", scope, parsed, err)
		}
	}
}

func TestParseScopeRejectsAnUnknownValueAndListsTheValidOnes(t *testing.T) {
	_, err := ParseScope("tree")
	if err == nil {
		t.Fatal("ParseScope() error = nil")
	}
	for _, scope := range Scopes {
		if !strings.Contains(err.Error(), string(scope)) {
			t.Errorf("error %v does not mention %q", err, scope)
		}
	}
}

func TestSelectWidensWithScope(t *testing.T) {
	for scope, want := range map[Scope]string{
		ScopeBranch:  "synthetic-auth",
		ScopePath:    "synthetic-main,synthetic-auth",
		ScopeSubtree: "synthetic-auth,synthetic-login,synthetic-session",
		ScopeGraph:   "synthetic-main,synthetic-auth,synthetic-login,synthetic-session,synthetic-billing",
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
