package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/subprocess"
	"github.com/shhac/g2g/internal/testutil"
)

// syntheticRepo builds a throwaway local repository. Ancestry is the one thing
// a PATH fake cannot check, because the fake answers whatever it is asked and
// the question here is what Git itself considers reachable. Nothing leaves the
// machine: the repository has no remote and every name in it is invented.
func syntheticRepo(t *testing.T) (string, Client) {
	t.Helper()

	repo := testutil.NewGitRepo(t, "synthetic-main")
	dir := repo.Dir
	run := repo.Run
	commit := func(name string) {
		t.Helper()
		repo.Commit("synthetic "+name, name+".txt", name)
	}
	commit("root")
	run("checkout", "-b", "synthetic-auth")
	commit("auth")
	run("checkout", "-b", "synthetic-login")
	commit("login")
	run("checkout", "synthetic-auth")
	run("checkout", "-b", "synthetic-session")
	commit("session")
	run("checkout", "synthetic-main")
	// A trunk that has moved on is the ordinary case, and it is why the trunk
	// stops being an ancestor of the branches built from it.
	commit("trunk-moved")

	client := Client{Runner: subprocess.ExecRunner{}}
	t.Chdir(dir)
	return dir, client
}

func TestAncestorBranchesFindsCandidateParentsAndExcludesTheTarget(t *testing.T) {
	_, client := syntheticRepo(t)

	got, err := client.AncestorBranches(context.Background(), "synthetic-login")
	if err != nil {
		t.Fatalf("AncestorBranches() error = %v", err)
	}
	if want := "synthetic-auth"; strings.Join(got, ",") != want {
		t.Errorf("AncestorBranches(login) = %v, want [%s]", got, want)
	}
}

// Siblings are not ancestors of each other, so a fork must not offer one leaf
// as the other's parent.
func TestAncestorBranchesExcludesSiblings(t *testing.T) {
	_, client := syntheticRepo(t)

	got, err := client.AncestorBranches(context.Background(), "synthetic-session")
	if err != nil {
		t.Fatalf("AncestorBranches() error = %v", err)
	}
	for _, branch := range got {
		if branch == "synthetic-login" {
			t.Fatalf("AncestorBranches(session) = %v, want no sibling", got)
		}
	}
}

// A moved trunk is not an ancestor of the branches built from it. This is why
// declared trunks have to be offered as candidates regardless of ancestry.
func TestAncestorBranchesOmitsAMovedTrunk(t *testing.T) {
	_, client := syntheticRepo(t)

	got, err := client.AncestorBranches(context.Background(), "synthetic-auth")
	if err != nil {
		t.Fatalf("AncestorBranches() error = %v", err)
	}
	if len(got) != 0 {
		t.Errorf("AncestorBranches(auth) = %v, want none once the trunk moved on", got)
	}
}

// Ordering candidates needs the nearer ancestor to measure smaller, which is
// what puts the immediate parent first.
func TestDivergenceOrdersNearestAncestorFirst(t *testing.T) {
	_, client := syntheticRepo(t)
	ctx := context.Background()

	_, near, err := client.Divergence(ctx, "synthetic-auth", "synthetic-login")
	if err != nil {
		t.Fatalf("Divergence() error = %v", err)
	}
	_, far, err := client.Divergence(ctx, "synthetic-main~1", "synthetic-login")
	if err != nil {
		t.Fatalf("Divergence() error = %v", err)
	}
	if near != 1 {
		t.Errorf("distance(auth, login) = %d, want 1", near)
	}
	if far <= near {
		t.Errorf("distance from the root (%d) should exceed the nearest ancestor (%d)", far, near)
	}
}

func TestIsAncestorReportsBothAnswersWithoutError(t *testing.T) {
	_, client := syntheticRepo(t)
	ctx := context.Background()

	yes, err := client.IsAncestor(ctx, "synthetic-auth", "synthetic-login")
	if err != nil || !yes {
		t.Errorf("IsAncestor(auth, login) = %t, %v; want true, nil", yes, err)
	}
	// git reports the negative with exit status 1. Treating that as a failure
	// would turn every ordinary "no" into a broken command.
	no, err := client.IsAncestor(ctx, "synthetic-login", "synthetic-auth")
	if err != nil || no {
		t.Errorf("IsAncestor(login, auth) = %t, %v; want false, nil", no, err)
	}
}

func TestIsAncestorStillFailsOnARealError(t *testing.T) {
	_, client := syntheticRepo(t)

	if _, err := client.IsAncestor(context.Background(), "synthetic-absent", "synthetic-login"); err == nil {
		t.Fatal("IsAncestor() error = nil for an unknown ref")
	}
}

func TestCommonDirIsAbsoluteFromASubdirectory(t *testing.T) {
	dir, client := syntheticRepo(t)
	nested := filepath.Join(dir, "synthetic", "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(nested)

	common, err := client.CommonDir(context.Background())
	if err != nil {
		t.Fatalf("CommonDir() error = %v", err)
	}
	if !filepath.IsAbs(common) {
		t.Fatalf("CommonDir() = %q, want an absolute path", common)
	}
}

// The bare form is resolved against the working directory, so the absolute
// flag is what makes the store path correct from a subdirectory rather than
// only from the repository root.
func TestCommonDirRequestsTheAbsolutePathFormat(t *testing.T) {
	recorder := testutil.FakeCLIs(t, map[string][]testutil.Route{
		"git": {{Prefix: "rev-parse", Output: "/synthetic/repo/.git"}},
	})

	common, err := (Client{Runner: subprocess.ExecRunner{}}).CommonDir(context.Background())
	if err != nil {
		t.Fatalf("CommonDir() error = %v", err)
	}
	if common != "/synthetic/repo/.git" {
		t.Errorf("CommonDir() = %q", common)
	}
	recorder.Find("git rev-parse --path-format=absolute --git-common-dir")
}

func TestAncestryRejectsUnsafeRefNames(t *testing.T) {
	client := Client{Runner: subprocess.ExecRunner{}}
	ctx := context.Background()

	if _, err := client.AncestorBranches(ctx, "-synthetic"); err == nil {
		t.Error("AncestorBranches() error = nil for an option-like name")
	}
	if _, _, err := client.Divergence(ctx, "synthetic-a", "-synthetic"); err == nil {
		t.Error("Divergence() error = nil for an option-like name")
	}
	if _, err := client.IsAncestor(ctx, "-synthetic", "synthetic-a"); err == nil {
		t.Error("IsAncestor() error = nil for an option-like name")
	}
}

// Divergence is what finds a parent once the trunk has moved past being an
// ancestor: the fork point is still there, and behind counts the commits the
// target added since it.
func TestDivergenceMeasuresBothDirectionsFromTheForkPoint(t *testing.T) {
	_, client := syntheticRepo(t)
	ctx := context.Background()

	ahead, behind, err := client.Divergence(ctx, "synthetic-main", "synthetic-auth")
	if err != nil {
		t.Fatalf("Divergence() error = %v", err)
	}
	if ahead != 1 || behind != 1 {
		t.Errorf("Divergence(main, auth) = %d, %d; want 1, 1 across the fork point", ahead, behind)
	}
}

func TestDivergenceReportsAnAncestorWithNothingAhead(t *testing.T) {
	_, client := syntheticRepo(t)

	ahead, behind, err := client.Divergence(context.Background(), "synthetic-auth", "synthetic-login")
	if err != nil {
		t.Fatalf("Divergence() error = %v", err)
	}
	if ahead != 0 {
		t.Errorf("ahead = %d, want 0 for an ancestor", ahead)
	}
	if behind != 1 {
		t.Errorf("behind = %d, want 1", behind)
	}
}

// A descendant contains everything the target has, so behind is zero — which
// is how it is excluded from being offered as a parent.
func TestDivergenceReportsADescendantWithNothingBehind(t *testing.T) {
	_, client := syntheticRepo(t)

	_, behind, err := client.Divergence(context.Background(), "synthetic-login", "synthetic-auth")
	if err != nil {
		t.Fatalf("Divergence() error = %v", err)
	}
	if behind != 0 {
		t.Errorf("behind = %d, want 0 for a descendant", behind)
	}
}

// A squash merge is the commonest way a branch lands and the one git cherry
// cannot see: it combines the branch's commits into one, so that commit is
// content-equivalent to none of them and every one reads as new.
func TestAbsorbedSeesASquashCherryCannot(t *testing.T) {
	repo := testutil.NewGitRepo(t, "synthetic-trunk")
	repo.Commit("synthetic base", "base.txt", "base")
	repo.Run("checkout", "-q", "-b", "synthetic-a")
	repo.Commit("synthetic first", "work.txt", "one")
	repo.Commit("synthetic second", "work.txt", "one\ntwo")
	repo.Run("checkout", "-q", "synthetic-trunk")
	repo.Run("merge", "-q", "--squash", "synthetic-a")
	repo.Run("commit", "-qm", "synthetic squash")
	// And the trunk moves on, so a plain tree comparison would differ.
	repo.Commit("synthetic other", "other.txt", "other")

	inRepo(t, repo.Dir)
	client := Client{Runner: subprocess.ExecRunner{}}

	// Per-commit sees nothing: both are marked as having no equivalent.
	absent, _, err := client.Cherry(context.Background(), "synthetic-trunk", "synthetic-a", "")
	if err != nil {
		t.Fatalf("Cherry() error = %v", err)
	}
	if len(absent) != 2 {
		t.Errorf("Cherry reports %d commits absent, want 2 · this is the case it cannot answer", len(absent))
	}

	absorbed, err := client.Absorbed(context.Background(), "synthetic-trunk", "synthetic-a")
	if err != nil {
		t.Fatalf("Absorbed() error = %v", err)
	}
	if !absorbed {
		t.Error("a squash-merged branch was not seen as absorbed")
	}
}

// A branch with work of its own is not absorbed, however much it shares.
func TestABranchWithWorkOfItsOwnIsNotAbsorbed(t *testing.T) {
	repo := testutil.NewGitRepo(t, "synthetic-trunk")
	repo.Commit("synthetic base", "base.txt", "base")
	repo.Run("checkout", "-q", "-b", "synthetic-a")
	repo.Commit("synthetic own", "own.txt", "own")

	inRepo(t, repo.Dir)
	absorbed, err := Client{Runner: subprocess.ExecRunner{}}.Absorbed(context.Background(), "synthetic-trunk", "synthetic-a")
	if err != nil {
		t.Fatalf("Absorbed() error = %v", err)
	}
	if absorbed {
		t.Error("a branch carrying work the trunk lacks was reported as absorbed")
	}
}
