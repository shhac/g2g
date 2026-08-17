package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/subprocess"
	"github.com/shhac/g2g/internal/testutil"
)

// syntheticRemote builds a bare repository and a clone of it. A local path is
// a perfectly good remote, so these stay offline: nothing resolves a hostname
// and no credential is ever needed.
func syntheticRemote(t *testing.T) (upstream string, client Client) {
	t.Helper()

	root := t.TempDir()
	upstream = filepath.Join(root, "upstream.git")
	clone := filepath.Join(root, "clone")
	env := syntheticEnv()
	run := func(dir string, args ...string) {
		t.Helper()
		command := exec.Command("git", args...)
		command.Dir, command.Env = dir, env
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
		}
	}
	run(root, "init", "-q", "--bare", "--initial-branch=synthetic-trunk", upstream)
	run(root, "init", "-q", "--initial-branch=synthetic-trunk", clone)
	// Background maintenance writes into .git after a commit, which races the
	// temporary directory's cleanup. Disabling it in the repository covers
	// every invocation, including the ones the code under test makes.
	// The identity has to live in the repository: the code under test spawns
	// its own git, which does not see the environment these helpers use.
	run(clone, "config", "user.name", "synthetic")
	run(clone, "config", "user.email", "synthetic@example.test")
	run(clone, "config", "gc.auto", "0")
	run(clone, "config", "maintenance.auto", "false")
	if err := os.WriteFile(filepath.Join(clone, "base.txt"), []byte("base"), 0o600); err != nil {
		t.Fatal(err)
	}
	run(clone, "add", "-A")
	run(clone, "commit", "-qm", "base")
	run(clone, "remote", "add", "origin", upstream)
	run(clone, "push", "-q", "-u", "origin", "synthetic-trunk")

	t.Chdir(clone)
	return upstream, Client{Runner: subprocess.ExecRunner{}}
}

// advanceUpstream commits on the bare repository's branch through a throwaway
// clone, which is how a teammate's push looks from here.
func advanceUpstream(t *testing.T, upstream, branch, file string) string {
	t.Helper()

	dir := t.TempDir()
	env := syntheticEnv()
	run := func(args ...string) []byte {
		t.Helper()
		command := exec.Command("git", args...)
		command.Dir, command.Env = filepath.Join(dir, "work"), env
		if args[0] == "clone" {
			command.Dir = dir
		}
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
		}
		return output
	}
	run("clone", "-q", upstream, "work")
	run("checkout", "-q", branch)
	if err := os.WriteFile(filepath.Join(dir, "work", file), []byte(file), 0o600); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-qm", "upstream "+file)
	run("push", "-q", "origin", branch)
	return strings.TrimSpace(string(run("rev-parse", "HEAD")))
}

func revision(t *testing.T, ref string) string {
	t.Helper()
	command := exec.Command("git", "rev-parse", ref)
	output, err := command.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

// Reading the remote must cost the repository nothing, so a preview can report
// that a trunk moved without having changed anything to find out.
func TestRemoteTipsWritesNothingLocally(t *testing.T) {
	upstream, client := syntheticRemote(t)
	before := revision(t, "refs/remotes/origin/synthetic-trunk")
	moved := advanceUpstream(t, upstream, "synthetic-trunk", "remote.txt")

	tips, err := client.RemoteTips(context.Background(), "origin", []string{"synthetic-trunk"})
	if err != nil {
		t.Fatalf("RemoteTips() error = %v", err)
	}

	if tips["synthetic-trunk"] != moved {
		t.Errorf("tip = %q, want %q", tips["synthetic-trunk"], moved)
	}
	if after := revision(t, "refs/remotes/origin/synthetic-trunk"); after != before {
		t.Errorf("remote-tracking ref moved from %s to %s", before, after)
	}
}

func TestRemoteTipsOmitsBranchesTheRemoteDoesNotHave(t *testing.T) {
	_, client := syntheticRemote(t)

	tips, err := client.RemoteTips(context.Background(), "origin", []string{"synthetic-trunk", "synthetic-absent"})
	if err != nil {
		t.Fatalf("RemoteTips() error = %v", err)
	}
	if _, present := tips["synthetic-absent"]; present {
		t.Error("an unpushed branch should be absent rather than an error")
	}
	if tips["synthetic-trunk"] == "" {
		t.Error("the existing branch is missing from the result")
	}
}

// The whole point of the isolated fetch: g2g gets the objects, and every ref
// the user relies on stays exactly where it was.
func TestFetchIsolatedLeavesRemoteTrackingRefsAlone(t *testing.T) {
	upstream, client := syntheticRemote(t)
	before := revision(t, "refs/remotes/origin/synthetic-trunk")
	moved := advanceUpstream(t, upstream, "synthetic-trunk", "remote.txt")

	if err := client.FetchIsolated(context.Background(), "origin", []string{"synthetic-trunk"}); err != nil {
		t.Fatalf("FetchIsolated() error = %v", err)
	}

	isolated := revision(t, IsolatedRef("origin", "synthetic-trunk"))
	if isolated != moved {
		t.Errorf("isolated ref = %q, want the remote tip %q", isolated, moved)
	}
	// A private destination refspec alone is not enough: git opportunistically
	// updates the remote-tracking ref that the configured refspec matches.
	if after := revision(t, "refs/remotes/origin/synthetic-trunk"); after != before {
		t.Fatalf("remote-tracking ref moved from %s to %s; --refmap= is not suppressing the opportunistic update", before, after)
	}
}

func TestFetchIsolatedLeavesFetchHeadAlone(t *testing.T) {
	upstream, client := syntheticRemote(t)
	advanceUpstream(t, upstream, "synthetic-trunk", "remote.txt")
	fetchHead := filepath.Join(".git", "FETCH_HEAD")
	before, _ := os.ReadFile(fetchHead)

	if err := client.FetchIsolated(context.Background(), "origin", []string{"synthetic-trunk"}); err != nil {
		t.Fatal(err)
	}

	after, _ := os.ReadFile(fetchHead)
	if string(before) != string(after) {
		t.Errorf("FETCH_HEAD changed:\nbefore %q\nafter  %q", before, after)
	}
}

// The fetched objects have to be usable as an ordinary commit-ish, because a
// restack rebases onto exactly this ref.
func TestFetchIsolatedProducesAUsableCommit(t *testing.T) {
	upstream, client := syntheticRemote(t)
	advanceUpstream(t, upstream, "synthetic-trunk", "remote.txt")

	if err := client.FetchIsolated(context.Background(), "origin", []string{"synthetic-trunk"}); err != nil {
		t.Fatal(err)
	}

	ancestors, err := client.AncestorBranches(context.Background(), IsolatedRef("origin", "synthetic-trunk"))
	if err != nil {
		t.Fatalf("the isolated ref is not usable as a commit-ish: %v", err)
	}
	if len(ancestors) == 0 {
		t.Error("expected the local trunk to be an ancestor of the fetched tip")
	}
}

func TestFetchIsolatedRequiresBranchesAndSafeNames(t *testing.T) {
	_, client := syntheticRemote(t)
	ctx := context.Background()

	if err := client.FetchIsolated(ctx, "origin", nil); err == nil {
		t.Error("FetchIsolated() error = nil with no branches")
	}
	if err := client.FetchIsolated(ctx, "origin", []string{"-synthetic"}); err == nil {
		t.Error("FetchIsolated() error = nil for an option-like name")
	}
	if _, err := client.RemoteTips(ctx, "origin", []string{"-synthetic"}); err == nil {
		t.Error("RemoteTips() error = nil for an option-like name")
	}
}

// A fake answers whatever it is asked, so only the recorded argv proves the
// two suppression flags were actually sent.
func TestFetchIsolatedSuppressesRefmapAndFetchHead(t *testing.T) {
	recorder := testutil.FakeCLIs(t, map[string][]testutil.Route{
		"git": {
			{Prefix: "remote get-url", Output: "https://example.test/synthetic.git"},
			{Prefix: "fetch"},
		},
	})

	if err := (Client{Runner: subprocess.ExecRunner{}}).FetchIsolated(context.Background(), "origin", []string{"synthetic-trunk"}); err != nil {
		t.Fatal(err)
	}

	call := recorder.Find("git fetch")
	for _, want := range []string{"--refmap=", "--no-write-fetch-head", "refs/heads/synthetic-trunk:refs/g2g/remotes/origin/synthetic-trunk"} {
		if !strings.Contains(call, want) {
			t.Errorf("fetch invocation %q is missing %q", call, want)
		}
	}
}
