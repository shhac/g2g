package link

import (
	"context"
	"testing"

	"github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/stack"
	"github.com/shhac/g2g/internal/subprocess"
	"github.com/shhac/g2g/internal/testutil"
)

// GitHub cannot answer this one. A squash merge lands the branch's work under a
// pull request whose head is a commit the branch never had, so the branch's own
// pull request reads as closed or missing — and the advice for that is to open
// one, for work that is already in the trunk.
//
// A fake proves nothing about it: the question is what Git makes of three
// commits combined into one, so this builds the squash for real. No remote, and
// every name invented.
func TestABranchWhoseWorkWasSquashedInIsLandedRatherThanMissing(t *testing.T) {
	repo := testutil.NewGitRepo(t, "synthetic-main")
	repo.Commit("synthetic root", "root.txt", "root")

	repo.Run("checkout", "-q", "-b", "synthetic-top")
	repo.Commit("synthetic one", "one.txt", "one")
	repo.Commit("synthetic two", "two.txt", "two")
	repo.Commit("synthetic three", "three.txt", "three")

	repo.Run("checkout", "-q", "synthetic-main")
	repo.Run("merge", "-q", "--squash", "synthetic-top")
	repo.Run("commit", "-qm", "synthetic squash of synthetic-top")

	t.Chdir(repo.Dir)
	plan := landedPlan()
	if err := (Service{Tips: git.Client{Runner: subprocess.ExecRunner{}}}).markLanded(context.Background(), plan); err != nil {
		t.Fatalf("markLanded() error = %v", err)
	}

	if got := plan.Issues[0].Kind; got != IssueLanded {
		t.Fatalf("issue kind = %q, want %q — submit would be offered for work already in the trunk", got, IssueLanded)
	}
	if want := "landed in synthetic-main"; plan.Issues[0].Reason != want {
		t.Errorf("reason = %q, want %q", plan.Issues[0].Reason, want)
	}
	if got := plan.LandedBranches(); len(got) != 1 || got[0] != "synthetic-top" {
		t.Errorf("LandedBranches() = %v, want [synthetic-top]", got)
	}
}

// The branch still has its work, so it is missing a pull request and nothing
// more. Answering "landed" here would hide the one thing submit is for.
func TestABranchWithWorkOfItsOwnStaysMissing(t *testing.T) {
	repo := testutil.NewGitRepo(t, "synthetic-main")
	repo.Commit("synthetic root", "root.txt", "root")
	repo.Run("checkout", "-q", "-b", "synthetic-top")
	repo.Commit("synthetic one", "one.txt", "one")

	t.Chdir(repo.Dir)
	plan := landedPlan()
	if err := (Service{Tips: git.Client{Runner: subprocess.ExecRunner{}}}).markLanded(context.Background(), plan); err != nil {
		t.Fatalf("markLanded() error = %v", err)
	}

	if got := plan.Issues[0].Kind; got != IssueMissing {
		t.Errorf("issue kind = %q, want %q", got, IssueMissing)
	}
}

func landedPlan() Plan {
	return Plan{
		Discovery: stack.Discovery{Snapshot: stack.Snapshot{
			Target:   "synthetic-top",
			Base:     "synthetic-main",
			Branches: []string{"synthetic-top"},
			Source:   stack.SourceG2G,
		}, PullRequests: []githubstack.PullRequest{}},
		Issues: []Issue{{Branch: "synthetic-top", Kind: IssueMissing, Reason: "no open PR"}},
	}
}

// Even a feature base that contains the merged head proves nothing about the
// trunk. A squash into that trunk, despite having no equivalent commit, does.
func TestMergedFeatureHistoryIsLandedOnlyAfterTrunkAbsorbsIt(t *testing.T) {
	repo := testutil.NewGitRepo(t, "synthetic-main")
	repo.Commit("synthetic root", "root.txt", "root")
	repo.Run("switch", "-qc", "synthetic-head")
	repo.Commit("synthetic one", "one.txt", "one")
	repo.Commit("synthetic two", "two.txt", "two")
	repo.Run("switch", "-qc", "synthetic-base")
	repo.Commit("synthetic feature", "feature.txt", "feature")
	t.Chdir(repo.Dir)
	service := Service{Tips: git.Client{Runner: subprocess.ExecRunner{}}}
	plan := mergedPlan()
	if err := service.markLanded(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if plan.Issues[0].Kind != IssueMergedUnlanded {
		t.Fatalf("feature-only content = %#v", plan.Issues[0])
	}
	// The narrow selection hangs from the feature base that now contains
	// the head, but its full ancestry still names the trunk.
	plan = mergedPlan()
	plan.Base = "synthetic-base"
	plan.Scope = stack.ScopeBranch
	if err := service.markLanded(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if plan.Issues[0].Kind != IssueMergedUnlanded {
		t.Fatalf("narrow feature-only content = %#v", plan.Issues[0])
	}

	repo.Run("switch", "-q", "synthetic-main")
	repo.Run("merge", "-q", "--squash", "synthetic-head")
	repo.Run("commit", "-qm", "synthetic squash")
	plan = mergedPlan()
	if err := service.markLanded(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if plan.Issues[0].Kind != IssueMerged {
		t.Fatalf("squashed trunk content = %#v", plan.Issues[0])
	}
}
