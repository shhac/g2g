package git

import (
	"context"
	"testing"

	"github.com/shhac/g2g/internal/subprocess"
	"github.com/shhac/g2g/internal/testutil"
)

// What an operation here can change is the local branches and g2g's own
// refs, and not its fetched copies of a remote, which record the remote.
func TestOwnRefsLeavesOutTheFetchedCopiesOfARemote(t *testing.T) {
	repo := testutil.NewGitRepo(t, "synthetic-trunk")
	repo.Commit("synthetic base", "base.txt", "base")
	tip := repo.Revision("synthetic-trunk")
	for _, ref := range []string{"refs/g2g/forkpoints/synthetic/deep", "refs/g2g/synced/origin/synthetic-a", "refs/g2g/remotes/origin/synthetic-trunk", "refs/remotes/origin/synthetic-trunk", "refs/tags/synthetic"} {
		repo.Run("update-ref", ref, "HEAD")
	}
	inRepo(t, repo.Dir)
	refs, err := Client{Runner: subprocess.ExecRunner{}}.OwnRefs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"refs/heads/synthetic-trunk": tip, "refs/g2g/forkpoints/synthetic/deep": tip, "refs/g2g/synced/origin/synthetic-a": tip}
	if len(refs) != len(want) {
		t.Fatalf("OwnRefs() = %v, want %v", refs, want)
	}
	for ref, object := range want {
		if refs[ref] != object {
			t.Errorf("%s = %q, want %q", ref, refs[ref], object)
		}
	}
}

// A ref is moved only from the commit it was expected to be at, created only
// where there is none, and deleted only from where it was left: what is put
// back is put back over exactly what was there.
func TestMoveRefMovesOnlyFromWhereItWasExpected(t *testing.T) {
	repo := testutil.NewGitRepo(t, "synthetic-trunk")
	repo.Commit("synthetic base", "base.txt", "base")
	first := repo.Revision("HEAD")
	repo.Commit("synthetic more", "more.txt", "more")
	second := repo.Revision("HEAD")
	inRepo(t, repo.Dir)
	client := Client{Runner: subprocess.ExecRunner{}}
	ctx := context.Background()
	const ref = "refs/heads/synthetic-a"

	if err := client.MoveRef(ctx, ref, first, ""); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := client.MoveRef(ctx, ref, second, ""); err == nil {
		t.Error("a ref that exists was created over")
	}
	if err := client.MoveRef(ctx, ref, second, second); err == nil {
		t.Error("a ref was moved from a commit it was not at")
	}
	if err := client.MoveRef(ctx, ref, second, first); err != nil || repo.Revision(ref) != second {
		t.Fatalf("move: %v", err)
	}
	if err := client.MoveRef(ctx, ref, "", first); err == nil {
		t.Error("a ref was deleted from a commit it was not at")
	}
	if err := client.MoveRef(ctx, ref, "", second); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if refs, _ := client.OwnRefs(ctx); refs[ref] != "" {
		t.Error("the ref survived its deletion")
	}
}

// A branch holds the only copy of a commit when no other branch, tag or
// remote's copy reaches it. Its own name does not count.
func TestOnlyOnDoesNotCountTheBranchItself(t *testing.T) {
	repo := testutil.NewGitRepo(t, "synthetic-trunk")
	repo.Commit("synthetic base", "base.txt", "base")
	repo.Run("checkout", "-q", "-b", "synthetic-a")
	inRepo(t, repo.Dir)
	client := Client{Runner: subprocess.ExecRunner{}}
	ctx := context.Background()
	if only, err := client.OnlyOn(ctx, "synthetic-a"); err != nil || only {
		t.Fatalf("OnlyOn() = %v, %v for a branch on its parent's tip", only, err)
	}
	repo.Commit("synthetic own", "own.txt", "own")
	if only, err := client.OnlyOn(ctx, "synthetic-a"); err != nil || !only {
		t.Fatalf("OnlyOn() = %v, %v for a branch with a commit of its own", only, err)
	}
	for _, ref := range []string{"refs/heads/synthetic-b", "refs/tags/synthetic", "refs/remotes/origin/synthetic-a", "refs/g2g/remotes/origin/synthetic-a"} {
		repo.Run("update-ref", ref, "synthetic-a")
		if only, err := client.OnlyOn(ctx, "synthetic-a"); err != nil || only {
			t.Errorf("OnlyOn() = %v, %v with %s holding the commit too", only, err, ref)
		}
		repo.Run("update-ref", "-d", ref)
	}
}
