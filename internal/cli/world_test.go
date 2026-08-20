package cli_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/testutil"
)

// world is a real repository, a real remote, and a real second checkout.
//
// Every other integration test here drives commands against a PATH fake, which
// answers whatever it is asked. That proves argv and parsing and cannot prove
// what Git does — and the three bugs shipped in v0.19.1, v0.21.1 and before
// them were all the same shape: a ref moved and something that should have
// followed it did not. Only real Git can show that.
//
// The remote is a bare repository on disk and "somebody else" is a second
// clone of it. Nothing leaves the machine and every name is invented.
type world struct {
	t *testing.T
	// Local is the checkout commands run in. Remote is the bare repository it
	// pushes to. Other is a second clone, which is how a branch moves under
	// Local without Local doing anything.
	Local, Remote, Other string
}

func newWorld(t *testing.T) *world {
	t.Helper()

	root := t.TempDir()
	w := &world{
		t:      t,
		Remote: filepath.Join(root, "remote.git"),
		Local:  filepath.Join(root, "local"),
		Other:  filepath.Join(root, "other"),
	}

	seed := testutil.NewGitRepo(t, "main")
	seed.Commit("synthetic base", "base.txt", "base")
	runIn(t, root, "git", "clone", "-q", "--bare", seed.Dir, w.Remote)
	runIn(t, root, "git", "clone", "-q", w.Remote, w.Local)
	runIn(t, root, "git", "clone", "-q", w.Remote, w.Other)
	for _, dir := range []string{w.Local, w.Other} {
		w.git(dir, "config", "user.name", "synthetic")
		w.git(dir, "config", "user.email", "synthetic@example.test")
	}

	// g2g runs in-process, so the command under test reads this working
	// directory and spawns the real git binary in it.
	t.Chdir(w.Local)
	return w
}

func (w *world) git(dir string, args ...string) string {
	w.t.Helper()
	return runIn(w.t, dir, "git", args...)
}

// commit writes a file on a branch and commits it, leaving that branch checked
// out in the directory it was asked about.
func (w *world) commit(dir, branch, name, content string) {
	w.t.Helper()
	if w.git(dir, "branch", "--show-current") != branch {
		w.git(dir, "switch", "-q", branch)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content+"\n"), 0o600); err != nil {
		w.t.Fatal(err)
	}
	w.git(dir, "add", "-A")
	w.git(dir, "commit", "-qm", "synthetic "+name)
}

// branchOff creates a branch and gives it one commit of its own.
func (w *world) branchOff(parent, branch, name string) {
	w.t.Helper()
	w.git(w.Local, "switch", "-q", parent)
	w.git(w.Local, "switch", "-qc", branch)
	w.commit(w.Local, branch, name, name)
}

// tip resolves a revision, so a test can compare two repositories directly.
func (w *world) tip(dir, revision string) string {
	w.t.Helper()
	return w.git(dir, "rev-parse", revision)
}

// contains reports whether one revision is an ancestor of another, which is
// how "this branch was replayed onto the new trunk" is actually checked.
func (w *world) contains(dir, ancestor, descendant string) bool {
	w.t.Helper()
	command := exec.Command("git", "merge-base", "--is-ancestor", ancestor, descendant)
	command.Dir, command.Env = dir, testutil.SyntheticGitEnv()
	return command.Run() == nil
}

// assertClean is the check every one of these scenarios makes after every
// mutation, because the bug that keeps recurring is a ref moving without the
// working tree following it. A dirty tree here is changes nobody made.
func (w *world) assertClean(dir string) {
	w.t.Helper()
	if status := w.git(dir, "status", "--porcelain"); status != "" {
		w.t.Errorf("%s has changes nobody made:\n%s", filepath.Base(dir), status)
	}
}

// assertHas checks a file survived a rewrite, which is the difference between
// a stack that was replayed and one that was flattened.
func (w *world) assertHas(dir, branch, name string) {
	w.t.Helper()
	if err := exec.Command("git", "-C", dir, "cat-file", "-e", branch+":"+name).Run(); err != nil {
		w.t.Errorf("%s no longer has %s", branch, name)
	}
}

func runIn(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	command := exec.Command(name, args...)
	command.Dir, command.Env = dir, testutil.SyntheticGitEnv()
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s (in %s): %v\n%s", name, strings.Join(args, " "), dir, err, output)
	}
	return strings.TrimSpace(string(output))
}

// readStore returns the recorded graph, so a test can assert that a command
// which moves contents left the structure exactly as it found it.
func (w *world) readStore() string {
	w.t.Helper()
	contents, err := os.ReadFile(filepath.Join(w.Local, ".git", "g2g", "graph.json"))
	if err != nil {
		w.t.Fatalf("read graph store: %v", err)
	}
	return string(contents)
}

// readStructure is the recorded graph with the parts a rewrite legitimately
// changes taken out: fork points move whenever commits do, and saying so is
// not the same as saying a branch changed parents.
func (w *world) readStructure() map[string]string {
	w.t.Helper()
	var stored struct {
		Trunks   []string `json:"trunks"`
		Branches map[string]struct {
			Parent string `json:"parent"`
		} `json:"branches"`
	}
	if err := json.Unmarshal([]byte(w.readStore()), &stored); err != nil {
		w.t.Fatalf("decode graph store: %v", err)
	}
	structure := map[string]string{"trunks": strings.Join(stored.Trunks, ",")}
	for branch, edge := range stored.Branches {
		structure[branch] = edge.Parent
	}
	return structure
}
