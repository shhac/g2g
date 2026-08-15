package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shhac/gt2gh/internal/subprocess"
	"github.com/shhac/gt2gh/internal/testutil"
)

// syntheticRepo builds a throwaway local repository. Ancestry is the one thing
// a PATH fake cannot check, because the fake answers whatever it is asked and
// the question here is what Git itself considers reachable. Nothing leaves the
// machine: the repository has no remote and every name in it is invented.
func syntheticRepo(t *testing.T) (string, Client) {
	t.Helper()

	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		command := exec.Command("git", args...)
		command.Dir = dir
		command.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=synthetic", "GIT_AUTHOR_EMAIL=synthetic@example.test",
			"GIT_COMMITTER_NAME=synthetic", "GIT_COMMITTER_EMAIL=synthetic@example.test",
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
		}
	}
	commit := func(name string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name+".txt"), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
		run("add", "-A")
		run("commit", "-m", "synthetic "+name)
	}

	run("init", "--initial-branch=synthetic-main")
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

func TestCommitDistanceOrdersNearestAncestorFirst(t *testing.T) {
	_, client := syntheticRepo(t)
	ctx := context.Background()

	near, err := client.CommitDistance(ctx, "synthetic-auth", "synthetic-login")
	if err != nil {
		t.Fatalf("CommitDistance() error = %v", err)
	}
	far, err := client.CommitDistance(ctx, "synthetic-main~1", "synthetic-login")
	if err != nil {
		t.Fatalf("CommitDistance() error = %v", err)
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
	if _, err := client.CommitDistance(ctx, "synthetic-a", "-synthetic"); err == nil {
		t.Error("CommitDistance() error = nil for an option-like name")
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

func TestDivergenceRejectsUnsafeRefNames(t *testing.T) {
	if _, _, err := (Client{Runner: subprocess.ExecRunner{}}).Divergence(context.Background(), "-synthetic", "synthetic-a"); err == nil {
		t.Error("Divergence() error = nil for an option-like name")
	}
}
