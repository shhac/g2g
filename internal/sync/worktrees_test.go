package sync

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/worktree"
)

// heldGit is the sync fake with another worktree holding branches, which
// brings that worktree along by recording each switch it is asked for.
type heldGit struct {
	*fakeGit
	holders  map[string]worktree.Holder
	switched []string
	refuse   bool
}

func (g *heldGit) HoldersElsewhere(context.Context) (map[string]worktree.Holder, error) {
	return g.holders, nil
}

func (g *heldGit) WorktreeClean(context.Context, string) (bool, error) { return true, nil }

func (g *heldGit) SwitchTreeIn(_ context.Context, _ string, from, to string) error {
	g.switched = append(g.switched, from+">"+to)
	if g.refuse {
		return errors.New("synthetic: untracked file would be overwritten")
	}
	return nil
}

func followingPlan(branch string) Plan {
	holder := worktree.Holder{Branch: branch, Path: "/synthetic/second", Clean: true}
	return Plan{Worktrees: worktree.NewOutcome([]worktree.Holder{holder}, nil)}
}

// A worktree that will not come along stops the move before the branch moves,
// and says so -- not that the branch moved and its worktree did not, which a
// pull reports as done.
func TestAWorktreeThatWillNotComeAlongStopsTheMoveBeforeIt(t *testing.T) {
	git := &heldGit{
		fakeGit: &fakeGit{objects: map[string]string{"synthetic-trunk": "old", "refs/synthetic/new": "new"}},
		holders: map[string]worktree.Holder{"synthetic-trunk": {Path: "/synthetic/second"}},
		refuse:  true,
	}
	service, _ := newService(git.fakeGit, nil)
	service.Git = git
	moved := false
	err := service.moveFollowing(context.Background(), followingPlan("synthetic-trunk"), "synthetic-trunk", "refs/synthetic/new", func(context.Context, string, string) error {
		moved = true
		return nil
	})
	if err == nil || moved {
		t.Fatalf("moveFollowing() = %v, moved = %t, want a refusal before the move", err, moved)
	}
	var notBrought *worktree.NotBrought
	if errors.As(err, &notBrought) {
		t.Errorf("error %q reads as a branch that moved", err)
	}
	if !strings.Contains(err.Error(), "was not moved") {
		t.Errorf("error %q does not say the branch stayed", err)
	}
}

// A ref that will not move after its worktree came along puts the worktree
// back, so nothing has happened.
func TestARefThatWillNotMovePutsItsWorktreeBack(t *testing.T) {
	git := &heldGit{
		fakeGit: &fakeGit{objects: map[string]string{"synthetic-trunk": "old", "refs/synthetic/new": "new"}},
		holders: map[string]worktree.Holder{"synthetic-trunk": {Path: "/synthetic/second"}},
	}
	service, _ := newService(git.fakeGit, nil)
	service.Git = git
	err := service.moveFollowing(context.Background(), followingPlan("synthetic-trunk"), "synthetic-trunk", "refs/synthetic/new", func(context.Context, string, string) error {
		return errors.New("synthetic: cannot lock ref")
	})
	if err == nil || !strings.Contains(err.Error(), "cannot lock ref") {
		t.Fatalf("moveFollowing() = %v, want the move's failure", err)
	}
	if strings.Join(git.switched, ",") != "old>new,new>old" {
		t.Errorf("switched %v, want the worktree brought forward and then back", git.switched)
	}
}
