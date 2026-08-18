package githubstack

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// recordingInspector answers from a table and records what each round asked
// for, which is the only way to check that a round costs one invocation
// however many unknowns it covers.
type recordingInspector struct {
	bases  map[string]string
	rounds [][]string
	err    error
}

func (r *recordingInspector) Inspect(_ context.Context, branches []string) ([]PullRequest, error) {
	if r.err != nil {
		return nil, r.err
	}
	r.rounds = append(r.rounds, append([]string(nil), branches...))
	prs := make([]PullRequest, 0, len(branches))
	number := 100 + len(r.rounds)*10
	for _, branch := range branches {
		base, published := r.bases[branch]
		if !published {
			continue
		}
		number++
		prs = append(prs, PullRequest{Number: number, Head: branch, Base: base, State: "OPEN"})
	}
	return prs, nil
}

// Two local subtrees joined through a branch published from somebody else's
// checkout. Without following the base, synthetic-local-c reads as a root and
// the two look unrelated — silently, which is the part that misleads.
//
//	synthetic-trunk
//	└─ synthetic-local-a        local
//	   └─ synthetic-remote-b    not on this machine
//	      └─ synthetic-local-c  local
func joinedByARemoteBranch() *recordingInspector {
	return &recordingInspector{bases: map[string]string{
		"synthetic-local-a":  "synthetic-trunk",
		"synthetic-remote-b": "synthetic-local-a",
		"synthetic-local-c":  "synthetic-remote-b",
	}}
}

func TestBuildForestFollowsABaseThatIsNotOnThisMachine(t *testing.T) {
	inspector := joinedByARemoteBranch()
	local := []string{"synthetic-trunk", "synthetic-local-a", "synthetic-local-c"}

	forest, err := BuildForest(context.Background(), inspector, local, FollowRounds)
	if err != nil {
		t.Fatalf("BuildForest() error = %v", err)
	}

	for branch, want := range map[string]string{
		"synthetic-local-a":  "synthetic-trunk",
		"synthetic-remote-b": "synthetic-local-a",
		"synthetic-local-c":  "synthetic-remote-b",
	} {
		if got := forest.Parents[branch]; got != want {
			t.Errorf("parent of %q = %q, want %q", branch, got, want)
		}
	}
	if strings.Join(forest.Absent, ",") != "synthetic-remote-b" {
		t.Errorf("Absent = %v, want the branch that is not local", forest.Absent)
	}
	if len(forest.Unfollowed) != 0 {
		t.Errorf("Unfollowed = %v, want none: the walk finished", forest.Unfollowed)
	}
}

// A round resolves every unknown it has in one invocation, so the cost is the
// depth of the remote-only chain and never the number of branches.
func TestEachRoundIsOneInvocationHoweverManyUnknownsItCovers(t *testing.T) {
	bases := map[string]string{}
	local := []string{"synthetic-trunk"}
	// Five local branches, each based on a different branch nobody here has.
	for _, suffix := range []string{"a", "b", "c", "d", "e"} {
		bases["synthetic-local-"+suffix] = "synthetic-remote-" + suffix
		bases["synthetic-remote-"+suffix] = "synthetic-trunk"
		local = append(local, "synthetic-local-"+suffix)
	}
	inspector := &recordingInspector{bases: bases}

	forest, err := BuildForest(context.Background(), inspector, local, FollowRounds)
	if err != nil {
		t.Fatalf("BuildForest() error = %v", err)
	}

	// Five unknowns, resolved together: one round for the local branches and
	// one for everything they turned up.
	if len(inspector.rounds) != 2 {
		t.Fatalf("took %d rounds for 5 unknowns, want 2:\n%v", len(inspector.rounds), inspector.rounds)
	}
	if len(inspector.rounds[1]) != 5 {
		t.Errorf("the second round asked about %d branches, want all 5 at once: %v", len(inspector.rounds[1]), inspector.rounds[1])
	}
	if len(forest.Absent) != 5 {
		t.Errorf("Absent = %v, want all five remote-only branches", forest.Absent)
	}
}

// Following is bounded by depth. A chain longer than the bound stops, and says
// where: a tree that silently ends early reads exactly like a finished one.
func TestFollowingIsBoundedAndReportsWhereItStopped(t *testing.T) {
	// synthetic-local-0 → synthetic-remote-1 → … → synthetic-remote-9
	bases := map[string]string{"synthetic-local-0": "synthetic-remote-1"}
	for depth := 1; depth < 9; depth++ {
		bases["synthetic-remote-"+string(rune('0'+depth))] = "synthetic-remote-" + string(rune('0'+depth+1))
	}
	inspector := &recordingInspector{bases: bases}

	forest, err := BuildForest(context.Background(), inspector, []string{"synthetic-local-0"}, 3)
	if err != nil {
		t.Fatalf("BuildForest() error = %v", err)
	}

	if len(inspector.rounds) != 3 {
		t.Errorf("took %d rounds, want the bound of 3", len(inspector.rounds))
	}
	if len(forest.Unfollowed) == 0 {
		t.Error("the walk stopped short and did not say so")
	}
}

// A branch with no open pull request is a root, not an unknown to chase.
func TestABranchWithNoOpenPullRequestEndsTheWalk(t *testing.T) {
	inspector := &recordingInspector{bases: map[string]string{"synthetic-local-a": "synthetic-trunk"}}

	forest, err := BuildForest(context.Background(), inspector, []string{"synthetic-trunk", "synthetic-local-a"}, FollowRounds)
	if err != nil {
		t.Fatalf("BuildForest() error = %v", err)
	}

	if len(inspector.rounds) != 1 {
		t.Errorf("took %d rounds, want 1: the only base is already local", len(inspector.rounds))
	}
	if len(forest.Absent) != 0 {
		t.Errorf("Absent = %v, want none", forest.Absent)
	}
}

func TestBuildForestReportsAnInspectorFailure(t *testing.T) {
	inspector := &recordingInspector{err: errors.New("synthetic GitHub failure")}

	if _, err := BuildForest(context.Background(), inspector, []string{"synthetic-a"}, FollowRounds); err == nil {
		t.Error("BuildForest() error = nil for a failing inspector")
	}
	if _, err := BuildForest(context.Background(), nil, []string{"synthetic-a"}, FollowRounds); err == nil {
		t.Error("BuildForest() error = nil without an inspector")
	}
}
