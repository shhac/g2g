package git

import (
	"context"
	"testing"

	"github.com/shhac/g2g/internal/landed"
	"github.com/shhac/g2g/internal/subprocess"
	"github.com/shhac/g2g/internal/testutil"
)

// untouchedRepo is a trunk and a branch off it, with the trunk moved on by a
// commit that touches only its own file.
func untouchedRepo(t *testing.T, branchFile string) (testutil.GitRepo, Client) {
	t.Helper()
	repo := testutil.NewGitRepo(t, "synthetic-trunk")
	repo.Commit("synthetic root", "root.txt", "root")
	repo.Run("checkout", "-q", "-b", "synthetic-branch")
	repo.Commit("synthetic branch work", branchFile, "branch work")
	repo.Run("checkout", "-q", "synthetic-trunk")
	repo.Commit("synthetic trunk work", "trunk.txt", "trunk work")
	t.Chdir(repo.Dir)
	return repo, Client{Runner: subprocess.ExecRunner{}}
}

func untouched(t *testing.T, client Client) bool {
	t.Helper()
	answer, err := client.Untouched(context.Background(), "synthetic-trunk", "synthetic-branch", "")
	if err != nil {
		t.Fatalf("Untouched() error = %v", err)
	}
	return answer
}

func landedInto(t *testing.T, client Client) bool {
	t.Helper()
	answer, err := landed.Into(context.Background(), client, "synthetic-trunk", "synthetic-branch", "")
	if err != nil {
		t.Fatalf("landed.Into() error = %v", err)
	}
	return answer
}

// The ordinary case, and the one it exists for: the trunk moved on elsewhere,
// so the branch has not landed, and nothing's content had to be compared.
func TestABranchTheTrunkNeverTouchedIsUntouched(t *testing.T) {
	_, client := untouchedRepo(t, "branch.txt")
	if !untouched(t, client) {
		t.Error("Untouched() = false for a branch whose files the trunk never touched")
	}
	if landedInto(t, client) {
		t.Error("landed.Into() = true for a branch that has not landed")
	}
}

// Every way a branch lands touches its paths on the trunk, so none of them may
// read as untouched — the answer has to come from the content.
func TestEveryWayOfLandingIsLeftToTheContent(t *testing.T) {
	for name, land := range map[string]func(repo testutil.GitRepo){
		"squashed": func(repo testutil.GitRepo) {
			repo.Run("merge", "-q", "--squash", "synthetic-branch")
			repo.Run("commit", "-q", "-m", "synthetic squash")
		},
		"cherry-picked": func(repo testutil.GitRepo) {
			repo.Run("cherry-pick", "synthetic-branch")
		},
	} {
		t.Run(name, func(t *testing.T) {
			repo, client := untouchedRepo(t, "branch.txt")
			land(repo)
			if untouched(t, client) {
				t.Fatal("Untouched() = true for a branch the trunk has taken in")
			}
			if !landedInto(t, client) {
				t.Error("landed.Into() = false for a branch that has landed")
			}
		})
	}
}

// A name that means something to a pathspec — a glob, a space — has to be
// matched as itself, or the trunk's change to it would go unseen.
func TestAPathspecLookingNameIsMatchedLiterally(t *testing.T) {
	repo, client := untouchedRepo(t, "synthetic [a]*.txt")
	repo.Commit("synthetic trunk edits it too", "synthetic [a]*.txt", "trunk version")
	if untouched(t, client) {
		t.Error("Untouched() = true though the trunk touched the branch's file")
	}
}

// A branch whose commits cancel out changes nothing as a whole, so whether
// merging it would change the trunk cannot be read from paths.
func TestABranchThatCancelsOutCannotBeToldFromPaths(t *testing.T) {
	repo, client := untouchedRepo(t, "branch.txt")
	repo.Run("checkout", "-q", "synthetic-branch")
	repo.Run("rm", "-q", "branch.txt")
	repo.Run("commit", "-q", "-m", "synthetic undo")
	if untouched(t, client) {
		t.Error("Untouched() = true for a branch that changes nothing as a whole")
	}
}
