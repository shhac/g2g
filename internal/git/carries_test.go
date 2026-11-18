package git

import (
	"context"
	"testing"

	"github.com/shhac/g2g/internal/landed"
	"github.com/shhac/g2g/internal/subprocess"
	"github.com/shhac/g2g/internal/testutil"
)

// squashedParent is a parent squashed into the trunk with a child still on
// the parent's old tip. make gives the child its own commits.
func squashedParent(t *testing.T, child func(repo testutil.GitRepo)) Client {
	t.Helper()
	repo := testutil.NewGitRepo(t, "synthetic-trunk")
	repo.Commit("synthetic base", "base.txt", "base")
	repo.Run("checkout", "-q", "-b", "synthetic-parent")
	repo.Commit("synthetic kept", "kept.txt", "kept")
	repo.Commit("synthetic extra", "extra.txt", "one\ntwo\nthree\n")
	repo.Run("checkout", "-q", "-b", "synthetic-child")
	child(repo)
	repo.Run("checkout", "-q", "synthetic-trunk")
	repo.Run("merge", "-q", "--squash", "synthetic-parent")
	repo.Run("commit", "-qm", "synthetic squash")
	inRepo(t, repo.Dir)
	return Client{Runner: subprocess.ExecRunner{}}
}

// A change that cancels out against where the stack left the trunk is not in
// a merge of the whole branch: the child differs from the fork by less than
// its parent does. Absorbed alone called each of these landed.
func TestAChildThatTakesBackPartOfItsSquashedParentHasNotLanded(t *testing.T) {
	children := map[string]func(testutil.GitRepo){
		"deletes a file the parent added": func(repo testutil.GitRepo) {
			repo.Run("rm", "-q", "extra.txt")
			repo.Run("commit", "-qm", "synthetic removal")
		},
		"reverts a commit of the parent": func(repo testutil.GitRepo) {
			repo.Run("revert", "--no-edit", "HEAD")
		},
	}
	for name, child := range children {
		t.Run(name, func(t *testing.T) {
			client := squashedParent(t, child)
			ctx := context.Background()
			absorbed, err := client.Absorbed(ctx, "synthetic-trunk", "synthetic-child")
			if err != nil || !absorbed {
				t.Fatalf("Absorbed() = %v, %v · the whole-branch merge is expected to see nothing", absorbed, err)
			}
			carries, err := client.Carries(ctx, "synthetic-trunk", "synthetic-child", "synthetic-parent")
			if err != nil || !carries {
				t.Fatalf("Carries() = %v, %v, want the child's own commit seen", carries, err)
			}
			done, err := landed.Into(ctx, client, "synthetic-trunk", "synthetic-child", "synthetic-parent")
			if err != nil || done {
				t.Fatalf("landed.Into() = %v, %v, want not landed", done, err)
			}
		})
	}
}

// A child squashed in together with its parent has its own commits in the
// trunk, so replaying them changes nothing and it is still landed.
func TestAChildSquashedWithItsParentStillLands(t *testing.T) {
	repo := testutil.NewGitRepo(t, "synthetic-trunk")
	repo.Commit("synthetic base", "base.txt", "base")
	repo.Run("checkout", "-q", "-b", "synthetic-parent")
	repo.Commit("synthetic parent", "parent.txt", "parent")
	repo.Run("checkout", "-q", "-b", "synthetic-child")
	repo.Commit("synthetic child", "child.txt", "child")
	repo.Commit("synthetic child again", "parent.txt", "parent, edited")
	repo.Run("checkout", "-q", "synthetic-trunk")
	repo.Run("merge", "-q", "--squash", "synthetic-child")
	repo.Run("commit", "-qm", "synthetic squash of both")
	inRepo(t, repo.Dir)
	client := Client{Runner: subprocess.ExecRunner{}}

	carries, err := client.Carries(context.Background(), "synthetic-trunk", "synthetic-child", "synthetic-parent")
	if err != nil || carries {
		t.Fatalf("Carries() = %v, %v, want nothing left to carry", carries, err)
	}
	done, err := landed.Into(context.Background(), client, "synthetic-trunk", "synthetic-child", "synthetic-parent")
	if err != nil || !done {
		t.Fatalf("landed.Into() = %v, %v, want landed", done, err)
	}
}

// What cannot be asked says nothing: a point the branch does not contain is
// not where its own work starts.
func TestCarriesSaysNothingOfAPointTheBranchDoesNotContain(t *testing.T) {
	client := squashedParent(t, func(repo testutil.GitRepo) {
		repo.Run("rm", "-q", "extra.txt")
		repo.Run("commit", "-qm", "synthetic removal")
	})
	for _, since := range []string{"", "synthetic-trunk"} {
		carries, err := client.Carries(context.Background(), "synthetic-trunk", "synthetic-child", since)
		if err != nil || carries {
			t.Errorf("Carries(since %q) = %v, %v, want no answer", since, carries, err)
		}
	}
}

// A commit made after the branch's pull request merged is the branch's own
// work whatever it does, and one that reverts what merged cancels out against
// the fork: the whole branch then equals where it started, and merges into the
// trunk without changing it. Measured from the head that merged, it is there.
func TestAFollowUpThatRevertsWhatMergedHasNotLanded(t *testing.T) {
	repo := testutil.NewGitRepo(t, "synthetic-trunk")
	repo.Commit("synthetic base", "base.txt", "base")
	repo.Run("checkout", "-q", "-b", "synthetic-a")
	repo.Commit("synthetic work", "work.txt", "work")
	merged := repo.Revision("synthetic-a")
	repo.Run("checkout", "-q", "synthetic-trunk")
	repo.Run("merge", "-q", "--squash", "synthetic-a")
	repo.Run("commit", "-qm", "synthetic squash")
	repo.Run("checkout", "-q", "synthetic-a")
	repo.Run("revert", "--no-edit", "HEAD")
	inRepo(t, repo.Dir)
	client := Client{Runner: subprocess.ExecRunner{}}
	ctx := context.Background()

	if got := landed.Assess(ctx, client, "synthetic-trunk", "synthetic-a", "synthetic-trunk"); got != landed.Present {
		t.Fatalf("Assess() = %v · bounded at the trunk, the revert is expected to go unseen", got)
	}
	if got := landed.AssessOwn(ctx, client, "synthetic-trunk", "synthetic-a", "synthetic-trunk", "synthetic-trunk", merged); got != landed.Absent {
		t.Errorf("AssessOwn() from the merged head = %v, want the follow-up seen", got)
	}
	done, err := landed.Whole(ctx, client, "synthetic-trunk", "synthetic-a", merged)
	if err != nil || done {
		t.Errorf("Whole() from the merged head = %v, %v, want not landed", done, err)
	}
	if done, err := landed.Whole(ctx, client, "synthetic-trunk", merged, merged); err != nil || !done {
		t.Errorf("Whole() of the merged head itself = %v, %v, want landed", done, err)
	}
}

// The branch a child sits on can be rewritten after the child left it, and is
// then no part of the child. The fork point the graph recorded still is, so
// it is what says where the child's own work starts.
func TestWholeBelievesTheForkPointWhenTheParentWasRewritten(t *testing.T) {
	client := squashedParent(t, func(repo testutil.GitRepo) {
		fork := repo.Revision("synthetic-parent")
		repo.Run("rm", "-q", "extra.txt")
		repo.Run("commit", "-qm", "synthetic removal")
		repo.Run("update-ref", "refs/heads/synthetic-fork", fork)
		repo.Run("checkout", "-q", "synthetic-parent")
		repo.Run("commit", "-q", "--amend", "-m", "synthetic extra, reworded")
	})
	ctx := context.Background()
	if done, err := landed.Whole(ctx, client, "synthetic-trunk", "synthetic-child", "synthetic-parent"); err != nil || !done {
		t.Fatalf("Whole() from the rewritten parent = %v, %v · it is expected to say nothing", done, err)
	}
	if done, err := landed.Whole(ctx, client, "synthetic-trunk", "synthetic-child", "synthetic-fork", "synthetic-parent"); err != nil || done {
		t.Errorf("Whole() from the fork point = %v, %v, want not landed", done, err)
	}
}
