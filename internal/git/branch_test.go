package git

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/subprocess"
	"github.com/shhac/g2g/internal/testutil"
)

// What Git accepts as a branch name, what it carries across a switch, and what
// it guesses when a name is not local are all questions about Git itself, so
// these run against a throwaway repository rather than a PATH fake.

func branchRepo(t *testing.T) (testutil.GitRepo, Client) {
	t.Helper()
	repo := testutil.NewGitRepo(t, "synthetic-main")
	repo.Commit("synthetic root", "root.txt", "root")
	t.Chdir(repo.Dir)
	return repo, Client{Runner: subprocess.ExecRunner{}}
}

func TestCheckBranchNameAcceptsOrdinaryNamesAndRefusesTheRest(t *testing.T) {
	repo, client := branchRepo(t)
	repo.Run("switch", "-qc", "synthetic-previous")
	repo.Run("switch", "-q", "synthetic-main")
	ctx := context.Background()

	for _, name := range []string{"synthetic-new", "synthetic/nested"} {
		if err := client.CheckBranchName(ctx, name); err != nil {
			t.Errorf("CheckBranchName(%q) = %v, want nil", name, err)
		}
	}
	// "@{-1}" is valid shorthand for the previous branch, which is exactly why
	// it must be refused: the branch it names already exists.
	for _, name := range []string{"synthetic..bad", "synthetic bad", "@{-1}", "-synthetic", ""} {
		if err := client.CheckBranchName(ctx, name); err == nil {
			t.Errorf("CheckBranchName(%q) = nil, want a refusal", name)
		}
	}
}

func TestCreateBranchStartsAtTheGivenBranchAndChecksItOut(t *testing.T) {
	repo, client := branchRepo(t)
	ctx := context.Background()
	repo.Run("switch", "-qc", "synthetic-parent")
	repo.Commit("synthetic parent", "parent.txt", "parent")
	repo.Run("switch", "-q", "synthetic-main")
	// A staged change the new start point does not touch comes along, which is
	// what lets create commit it on the new branch.
	repo.Write("staged.txt", "staged")
	repo.Run("add", "staged.txt")

	if err := client.CreateBranch(ctx, "synthetic-child", "synthetic-parent"); err != nil {
		t.Fatalf("CreateBranch() = %v", err)
	}

	if current := repo.Run("branch", "--show-current"); current != "synthetic-child" {
		t.Errorf("checked out %q, want synthetic-child", current)
	}
	if tip, parent := repo.Revision("synthetic-child"), repo.Revision("synthetic-parent"); tip != parent {
		t.Errorf("synthetic-child starts at %s, want synthetic-parent's %s", tip, parent)
	}
	if _, err := os.Stat(filepath.Join(repo.Dir, "parent.txt")); err != nil {
		t.Errorf("the working tree did not follow the switch: %v", err)
	}
	staged, err := client.StagedPaths(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(staged, []string{"staged.txt"}) {
		t.Errorf("StagedPaths() = %v, want the change that was carried over", staged)
	}
}

func TestCommitRecordsWhatIsStagedEvenWithADashLedMessage(t *testing.T) {
	repo, client := branchRepo(t)
	ctx := context.Background()
	repo.Write("work.txt", "work")
	repo.Run("add", "work.txt")

	if err := client.Commit(ctx, "-synthetic message"); err != nil {
		t.Fatalf("Commit() = %v", err)
	}

	if subject := repo.Run("log", "-1", "--format=%s"); subject != "-synthetic message" {
		t.Errorf("committed %q, want the message verbatim", subject)
	}
	if staged, _ := client.StagedPaths(ctx); len(staged) != 0 {
		t.Errorf("still staged after the commit: %v", staged)
	}
	if err := client.Commit(ctx, "  "); err == nil {
		t.Error("Commit with a blank message = nil, want a refusal")
	}
}

// A name that is not a local branch must be refused rather than recreated from
// a remote-tracking ref of the same name, which is what git switch does when
// it is allowed to guess.
func TestSwitchExistingRefusesToGuessFromARemote(t *testing.T) {
	repo, client := branchRepo(t)
	ctx := context.Background()
	remote := t.TempDir() + "/remote.git"
	repo.Run("clone", "-q", "--bare", repo.Dir, remote)
	repo.Run("remote", "add", "origin", remote)
	repo.Run("push", "-q", "origin", "synthetic-main:synthetic-remote-only")
	repo.Run("fetch", "-q", "origin")

	err := client.SwitchExisting(ctx, "synthetic-remote-only")

	if err == nil {
		t.Fatal("SwitchExisting created a branch from its remote-tracking ref")
	}
	if branches := repo.Run("branch", "--format=%(refname:short)"); strings.Contains(branches, "synthetic-remote-only") {
		t.Errorf("a local synthetic-remote-only now exists:\n%s", branches)
	}

	repo.Run("branch", "synthetic-local")
	if err := client.SwitchExisting(ctx, "synthetic-local"); err != nil {
		t.Fatalf("SwitchExisting(local) = %v", err)
	}
	if current := repo.Run("branch", "--show-current"); current != "synthetic-local" {
		t.Errorf("checked out %q, want synthetic-local", current)
	}
}
