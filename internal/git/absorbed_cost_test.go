package git

import (
	"context"
	"fmt"
	"testing"

	"github.com/shhac/g2g/internal/subprocess"
	"github.com/shhac/g2g/internal/testutil"
)

// countingRunner counts the calls of one git subcommand, which is what a
// cost is here: a merge is the expensive one.
type countingRunner struct {
	subprocess.ExecRunner
	subcommand string
	calls      int
}

func (r *countingRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	for _, arg := range args {
		if arg == r.subcommand {
			r.calls++
			break
		}
	}
	return r.ExecRunner.Run(ctx, name, args...)
}

// A branch that conflicts with a busy trunk and never landed is the ordinary
// branch in review. It used to cost a merge at every candidate to say so; one
// of its new files never having been in the trunk says so in one walk.
func TestABranchWhoseNewFileTheTrunkNeverHeldCostsOneMerge(t *testing.T) {
	repo := testutil.NewGitRepo(t, "synthetic-trunk")
	repo.Commit("synthetic base", "shared.txt", "base")
	repo.Run("checkout", "-q", "-b", "synthetic-a")
	repo.Commit("synthetic mine", "shared.txt", "mine")
	repo.Commit("synthetic new file", "new.txt", "new")
	repo.Run("checkout", "-q", "synthetic-trunk")
	for i := range absorbedCandidates {
		repo.Commit(fmt.Sprintf("synthetic busy %d", i), "shared.txt", fmt.Sprintf("theirs %d", i))
	}

	inRepo(t, repo.Dir)
	runner := &countingRunner{subcommand: "merge-tree"}
	absorbed, err := Client{Runner: runner}.Absorbed(context.Background(), "synthetic-trunk", "synthetic-a")
	if err != nil {
		t.Fatalf("Absorbed() error = %v", err)
	}
	if absorbed {
		t.Error("a branch the trunk never took was reported as absorbed")
	}
	if runner.calls != 1 {
		t.Errorf("merges = %d, want the one at the trunk's tip", runner.calls)
	}
}

// The trunk holding a file of the same name is not the trunk holding the
// branch's file: what counts is the content the branch has.
func TestAFileTheTrunkAddedDifferentlyIsNotTheBranchs(t *testing.T) {
	repo := testutil.NewGitRepo(t, "synthetic-trunk")
	repo.Commit("synthetic base", "base.txt", "base")
	repo.Run("checkout", "-q", "-b", "synthetic-a")
	repo.Commit("synthetic mine", "new.txt", "mine")
	repo.Run("checkout", "-q", "synthetic-trunk")
	repo.Commit("synthetic theirs", "new.txt", "theirs")

	inRepo(t, repo.Dir)
	runner := &countingRunner{subcommand: "merge-tree"}
	absorbed, err := Client{Runner: runner}.Absorbed(context.Background(), "synthetic-trunk", "synthetic-a")
	if err != nil || absorbed {
		t.Fatalf("Absorbed() = %v, %v, want not absorbed", absorbed, err)
	}
	if runner.calls != 1 {
		t.Errorf("merges = %d, want the one at the trunk's tip", runner.calls)
	}
}

// A squash that reached the trunk through a merge commit, and was edited over
// there, still landed. A merge shows no changes of its own unless asked, so
// the file's arrival has to be looked for on the side that was merged in.
func TestAbsorbedSeesANewFileThatArrivedThroughAMerge(t *testing.T) {
	repo := testutil.NewGitRepo(t, "synthetic-trunk")
	repo.Commit("synthetic base", "base.txt", "base")
	repo.Run("checkout", "-q", "-b", "synthetic-a")
	repo.Commit("synthetic first", "work.txt", "one")
	repo.Commit("synthetic second", "work.txt", "one\ntwo")
	repo.Run("checkout", "-q", "-b", "synthetic-staging", "synthetic-trunk")
	repo.Run("merge", "-q", "--squash", "synthetic-a")
	repo.Run("commit", "-qm", "synthetic squash")
	repo.Run("checkout", "-q", "synthetic-trunk")
	repo.Commit("synthetic meanwhile", "other.txt", "other")
	repo.Run("merge", "-q", "--no-ff", "-m", "synthetic merge", "synthetic-staging")
	repo.Commit("synthetic follow-up", "work.txt", "one\nthree")

	inRepo(t, repo.Dir)
	absorbed, err := Client{Runner: subprocess.ExecRunner{}}.Absorbed(context.Background(), "synthetic-trunk", "synthetic-a")
	if err != nil {
		t.Fatalf("Absorbed() error = %v", err)
	}
	if !absorbed {
		t.Error("a squash merged into the trunk and edited over was not seen as absorbed")
	}
}

// A branch that deletes a file may have renamed it, and a merge follows a
// rename where a path does not, so its new files are not asked about: the
// trunk took the rename and then edited the file under its new name.
func TestAbsorbedStillSeesASquashedRenameTheTrunkEdited(t *testing.T) {
	body := "one\ntwo\nthree\nfour\nfive\nsix\nseven\neight\n"
	repo := testutil.NewGitRepo(t, "synthetic-trunk")
	repo.Commit("synthetic base", "old.txt", body)
	repo.Run("checkout", "-q", "-b", "synthetic-a")
	repo.Run("mv", "old.txt", "new.txt")
	repo.Run("commit", "-qm", "synthetic rename")
	repo.Commit("synthetic edit", "new.txt", body+"nine\n")
	repo.Run("checkout", "-q", "synthetic-trunk")
	repo.Run("merge", "-q", "--squash", "synthetic-a")
	repo.Run("commit", "-qm", "synthetic squash")
	repo.Commit("synthetic follow-up", "new.txt", body+"nine!\n")

	inRepo(t, repo.Dir)
	absorbed, err := Client{Runner: subprocess.ExecRunner{}}.Absorbed(context.Background(), "synthetic-trunk", "synthetic-a")
	if err != nil {
		t.Fatalf("Absorbed() error = %v", err)
	}
	if !absorbed {
		t.Error("a squashed rename the trunk edited over was not seen as absorbed")
	}
}

// A branch that landed and was then edited over is found where it landed: the
// trunk's first later edit to a file the branch added is where the look
// starts, however many edits to its other files have piled up since. Walking
// back from the tip met every one of them first, a merge each, and gave up
// before reaching a squash buried deeper than the bound.
func TestAbsorbedStartsBeforeTheTrunksFirstEditToANewFile(t *testing.T) {
	shared := "one\ntwo\nthree\nfour\nfive\nsix\nseven\neight\n"
	repo := testutil.NewGitRepo(t, "synthetic-trunk")
	repo.Commit("synthetic base", "shared.txt", shared)
	repo.Run("checkout", "-q", "-b", "synthetic-a")
	repo.Commit("synthetic mine", "shared.txt", "one\nTWO\nthree\nfour\nfive\nsix\nseven\neight\n")
	repo.Commit("synthetic new file", "new.txt", "new")
	repo.Run("checkout", "-q", "synthetic-trunk")
	repo.Run("merge", "-q", "--squash", "synthetic-a")
	repo.Run("commit", "-qm", "synthetic squash")
	repo.Commit("synthetic follow-up", "new.txt", "newer")
	for i := range absorbedCandidates + 8 {
		repo.Commit(fmt.Sprintf("synthetic busy %d", i), "shared.txt", fmt.Sprintf("one\ntwo %d\nthree\nfour\nfive\nsix\nseven\neight\n", i))
	}

	inRepo(t, repo.Dir)
	runner := &countingRunner{subcommand: "merge-tree"}
	absorbed, err := Client{Runner: runner}.Absorbed(context.Background(), "synthetic-trunk", "synthetic-a")
	if err != nil {
		t.Fatalf("Absorbed() error = %v", err)
	}
	if !absorbed {
		t.Error("a squash buried under later edits was not seen as absorbed")
	}
	if runner.calls != 2 {
		t.Errorf("merges = %d, want the trunk's tip and the commit before its edit", runner.calls)
	}
}

// A file the trunk edited and then put back is held again from there, so the
// newest thing that happened to it is what bounds the look.
func TestAbsorbedSeesANewFileTheTrunkEditedAndRestored(t *testing.T) {
	repo := testutil.NewGitRepo(t, "synthetic-trunk")
	repo.Commit("synthetic base", "shared.txt", "one\ntwo\nthree\n")
	repo.Run("checkout", "-q", "-b", "synthetic-a")
	repo.Commit("synthetic mine", "shared.txt", "one\nTWO\nthree\n")
	repo.Commit("synthetic new file", "new.txt", "new")
	repo.Run("checkout", "-q", "synthetic-trunk")
	repo.Run("merge", "-q", "--squash", "synthetic-a")
	repo.Run("commit", "-qm", "synthetic squash")
	repo.Commit("synthetic edit", "new.txt", "newer")
	repo.Commit("synthetic restore", "new.txt", "new")
	repo.Commit("synthetic meanwhile", "other.txt", "other")
	repo.Commit("synthetic follow-up", "shared.txt", "one\nTWO!\nthree\n")

	inRepo(t, repo.Dir)
	absorbed, err := Client{Runner: subprocess.ExecRunner{}}.Absorbed(context.Background(), "synthetic-trunk", "synthetic-a")
	if err != nil {
		t.Fatalf("Absorbed() error = %v", err)
	}
	if !absorbed {
		t.Error("a squash whose new file was edited and restored was not seen as absorbed")
	}
}
