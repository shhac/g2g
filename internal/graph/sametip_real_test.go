package graph_test

import (
	"context"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/subprocess"
	"github.com/shhac/g2g/internal/testutil"
)

// Two branches at one commit are each an ancestor of the other, so ancestry
// says nothing about which sits on which. That is the state of every branch
// the moment it is created, which made it the ordinary case rather than an edge
// one: a candidate the target already contained was dropped as a descendant,
// so the branch someone had just switched away from was never offered, and a
// whole-stack adoption quietly skipped it and recorded the rest wrongly.
//
// The question is what Git considers reachable, so this builds a repository.
func sameTipRepository(t *testing.T) (testutil.GitRepo, graph.Service) {
	t.Helper()
	repo := testutil.NewGitRepo(t, "synthetic-main")
	repo.Commit("synthetic root", "root.txt", "root")
	repo.Run("switch", "-qc", "synthetic-one")
	repo.Commit("synthetic one", "one.txt", "one")
	t.Chdir(repo.Dir)
	client := git.Client{Runner: subprocess.ExecRunner{}}
	return repo, graph.Service{Git: client, Store: graph.FileStore{Git: client}, Refs: client}
}

// track offers the branch the target was created from, and says nothing about
// which way round they go: that is for the user to answer.
func TestTrackOffersTheBranchATargetWasCreatedFrom(t *testing.T) {
	repo, service := sameTipRepository(t)
	repo.Run("switch", "-qc", "synthetic-two")
	repo.Commit("synthetic two", "two.txt", "two")
	repo.Run("switch", "-qc", "synthetic-three")

	plan, err := service.PlanTrack(context.Background(), graph.Selection{}, "")
	if err != nil {
		t.Fatalf("PlanTrack() error = %v", err)
	}
	if len(plan.Candidates) == 0 || plan.Candidates[0].Branch != "synthetic-two" {
		t.Fatalf("Candidates = %+v, want synthetic-two first", plan.Candidates)
	}
	if !plan.Candidates[0].SameTip() || !plan.Candidates[0].Ancestor {
		t.Errorf("synthetic-two = %+v, want it offered as an ancestor at the same commit", plan.Candidates[0])
	}
	if plan.Blocked == "" {
		t.Error("track chose a parent; it must preview and block")
	}
}

// A whole-stack adoption cannot order two branches at one commit, so it must
// refuse and name both — never record one and leave the other out.
func TestTrackStackRefusesTwoBranchesAtOneCommit(t *testing.T) {
	repo, service := sameTipRepository(t)
	repo.Run("switch", "-qc", "synthetic-two")

	for _, target := range []string{"synthetic-two", "synthetic-one"} {
		t.Run(target, func(t *testing.T) {
			plan, err := service.PlanStack(context.Background(), graph.Selection{Branch: target}, "synthetic-main")
			if err != nil {
				t.Fatalf("PlanStack() error = %v", err)
			}
			if plan.Blocked == "" {
				t.Fatalf("PlanStack() records %v; synthetic-one and synthetic-two point at one commit and ancestry cannot order them", plan.Record)
			}
			for _, name := range []string{"synthetic-one", "synthetic-two"} {
				if !strings.Contains(plan.Blocked, name) {
					t.Errorf("Blocked = %q, want it to name %s", plan.Blocked, name)
				}
			}
		})
	}
}

// A branch sitting off the stack at the same commit as one on it is just as
// unorderable, and is found by the fan-out rather than the spine.
func TestTrackStackRefusesABranchAtTheSameCommitAsOneItWouldAttach(t *testing.T) {
	repo, service := sameTipRepository(t)
	repo.Run("switch", "-qc", "synthetic-side")
	repo.Commit("synthetic side", "side.txt", "side")
	repo.Run("branch", "synthetic-copy", "synthetic-side")
	repo.Run("switch", "-qc", "synthetic-two", "synthetic-one")
	repo.Commit("synthetic two", "two.txt", "two")

	plan, err := service.PlanStack(context.Background(), graph.Selection{Branch: "synthetic-two"}, "synthetic-main")
	if err != nil {
		t.Fatalf("PlanStack() error = %v", err)
	}
	if plan.Blocked == "" {
		t.Fatalf("PlanStack() records %v; synthetic-copy and synthetic-side point at one commit", plan.Record)
	}
	for _, name := range []string{"synthetic-side", "synthetic-copy"} {
		if !strings.Contains(plan.Blocked, name) {
			t.Errorf("Blocked = %q, want it to name %s", plan.Blocked, name)
		}
	}
}

// The trunk is the one branch whose place the user asserted, so a branch just
// created from it is not ambiguous: it sits on the trunk. This refused before,
// saying the trunk was not an ancestor of a branch it was the tip of.
func TestTrackStackRecordsAFreshBranchOnTheTrunkItWasCreatedFrom(t *testing.T) {
	repo, service := sameTipRepository(t)
	repo.Run("switch", "-qc", "synthetic-fresh", "synthetic-main")

	plan, err := service.PlanStack(context.Background(), graph.Selection{}, "synthetic-main")
	if err != nil {
		t.Fatalf("PlanStack() error = %v", err)
	}
	if plan.Blocked != "" {
		t.Fatalf("PlanStack() blocked: %s", plan.Blocked)
	}
	if len(plan.Record) != 1 || plan.Record[0] != (graph.Adoption{Branch: "synthetic-fresh", Parent: "synthetic-main"}) {
		t.Errorf("Record = %v, want synthetic-fresh under synthetic-main", plan.Record)
	}
}
