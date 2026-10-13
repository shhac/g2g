package sync

import (
	"context"
	"errors"
	"slices"
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

// Each row is one thing a pull decides about a branch it would move itself
// while another worktree holds it. Every rule pinned here used to be the
// replay's, asked a second time about the caller's own moves.
func TestDecideHeld(t *testing.T) {
	clean := func(branch string) worktree.Holder {
		return worktree.Holder{Branch: branch, Path: "/synthetic/" + branch, Clean: true}
	}
	dirty := func(branch string) worktree.Holder {
		return worktree.Holder{Branch: branch, Path: "/synthetic/" + branch}
	}
	held := func(holders ...worktree.Holder) worktree.Held {
		found := worktree.Held{}
		for _, holder := range holders {
			found[holder.Branch] = holder
		}
		return found
	}
	collecting := func(branches ...string) collected {
		found := collected{}
		for _, branch := range branches {
			found.collections = append(found.collections, Collection{Branch: branch, To: branch + "-published"})
		}
		return found
	}
	selected := []string{"synthetic-trunk", "synthetic-a", "synthetic-b"}
	for _, tc := range []struct {
		name      string
		plan      Plan
		found     collected
		held      worktree.Held
		refusal   string
		follow    string
		leave     string
		fixed     string
		trunkLeft bool
	}{
		{name: "nothing held", plan: Plan{Policy: worktree.Follow, Base: "synthetic-trunk", Advance: true}, found: collecting("synthetic-b")},
		{
			// The trunk this pull advances, under refuse: the refusal the
			// replay used to give when it was handed the trunk as pending.
			name: "refuse a held trunk it would advance", plan: Plan{Policy: worktree.Refuse, Base: "synthetic-trunk", Advance: true},
			held: held(clean("synthetic-trunk")), refusal: "checked out in another worktree: synthetic-trunk (/synthetic/synthetic-trunk)",
		},
		{name: "refuse a held branch it would take", plan: Plan{Policy: worktree.Refuse, Base: "synthetic-trunk"}, found: collecting("synthetic-b"), held: held(clean("synthetic-b")), refusal: "synthetic-b"},
		{name: "a trunk that is level is not moved, so not decided", plan: Plan{Policy: worktree.Refuse, Base: "synthetic-trunk"}, held: held(clean("synthetic-trunk"))},
		{name: "follow a clean branch it takes", plan: Plan{Policy: worktree.Follow, Base: "synthetic-trunk"}, found: collecting("synthetic-b"), held: held(clean("synthetic-b")), follow: "synthetic-b"},
		{name: "leave a dirty branch it would take", plan: Plan{Policy: worktree.Follow, Base: "synthetic-trunk"}, found: collecting("synthetic-b"), held: held(dirty("synthetic-b")), leave: "synthetic-b", fixed: "synthetic-b"},
		{
			name: "a trunk left fixes every branch and follows none", plan: Plan{Policy: worktree.Follow, Base: "synthetic-trunk", Advance: true},
			found: collecting("synthetic-a"), held: held(dirty("synthetic-trunk"), clean("synthetic-a")),
			leave: "synthetic-trunk", fixed: "synthetic-a,synthetic-b,synthetic-trunk", trunkLeft: true,
		},
		{
			// Both sides moved and another worktree has it: leaving it is the
			// way through rather than the divergence refusal.
			name: "skip leaves a branch both sides moved", plan: Plan{Policy: worktree.Skip, Base: "synthetic-trunk"},
			found: collected{stuck: []divergence{{Branch: "synthetic-b"}}}, held: held(clean("synthetic-b")), leave: "synthetic-b", fixed: "synthetic-b",
		},
		{
			// Refused as diverged regardless, and that refusal is the one to
			// read, not one about a worktree.
			name: "refuse leaves a branch both sides moved to its own refusal", plan: Plan{Policy: worktree.Refuse, Base: "synthetic-trunk"},
			found: collected{stuck: []divergence{{Branch: "synthetic-b"}}}, held: held(clean("synthetic-b")),
		},
		{
			name: "a --take that would replace a left branch refuses", plan: Plan{Policy: worktree.Skip, Base: "synthetic-trunk"},
			found: collected{collections: []Collection{{Branch: "synthetic-b", Discards: []string{"synthetic-lost"}}}}, held: held(clean("synthetic-b")), refusal: "--take would replace synthetic-b",
		},
		{
			name: "a commit moved between a left branch and a taken one refuses", plan: Plan{Policy: worktree.Follow, Base: "synthetic-trunk"},
			found: func() collected {
				found := collecting("synthetic-a", "synthetic-b")
				found.commits = []Drop{{Kind: "moved", Branch: "synthetic-a", Commit: "synthetic-moved", To: "synthetic-b"}}
				return found
			}(),
			held: held(dirty("synthetic-b")), refusal: "a commit moved from synthetic-a to synthetic-b",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			decided := decideHeld(tc.plan, tc.found, tc.held, selected)
			if !strings.Contains(decided.refusal.Reason, tc.refusal) || (tc.refusal == "") != (decided.refusal.Reason == "") {
				t.Fatalf("refusal = %q, want %q", decided.refusal.Reason, tc.refusal)
			}
			names := func(holders []worktree.Holder) string {
				said := make([]string, 0, len(holders))
				for _, holder := range holders {
					said = append(said, holder.Branch)
				}
				return strings.Join(said, ",")
			}
			fixed := make([]string, 0, len(decided.fixed))
			for _, branch := range selected {
				if decided.fixed[branch] {
					fixed = append(fixed, branch)
				}
			}
			if got := names(decided.outcome.Follow); got != tc.follow {
				t.Errorf("follow = %q, want %q", got, tc.follow)
			}
			if got := names(decided.outcome.Leave); got != tc.leave {
				t.Errorf("leave = %q, want %q", got, tc.leave)
			}
			if got := strings.Join(slices.Sorted(slices.Values(fixed)), ","); got != tc.fixed {
				t.Errorf("fixed = %q, want %q", got, tc.fixed)
			}
			if decided.trunkLeft != tc.trunkLeft {
				t.Errorf("trunkLeft = %t, want %t", decided.trunkLeft, tc.trunkLeft)
			}
		})
	}
}
