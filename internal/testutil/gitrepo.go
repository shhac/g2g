package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// SyntheticGitEnv is the environment every throwaway repository runs under.
//
// It pins an identity and cuts the machine's own configuration out, because a
// test that borrows either passes or fails for reasons that have nothing to do
// with the code.
func SyntheticGitEnv() []string {
	return append(os.Environ(),
		"GIT_AUTHOR_NAME=synthetic", "GIT_AUTHOR_EMAIL=synthetic@example.test",
		"GIT_COMMITTER_NAME=synthetic", "GIT_COMMITTER_EMAIL=synthetic@example.test",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
	)
}

// GitRepo is a throwaway repository a test builds a branch shape in.
//
// Each package used to bootstrap its own, and the bootstrap had been copied
// eight times across two packages — along with the comment explaining why the
// identity has to live in the repository rather than only in the environment.
// When the reasoning gets copied too, the code should have been shared.
type GitRepo struct {
	t   *testing.T
	Dir string
}

// NewGitRepo creates an initialised repository with an identity, no inherited
// configuration, and background maintenance disabled.
//
// The identity has to live in the repository, not just in the environment these
// helpers use: code under test spawns its own git, which inherits the test
// process's environment instead. Some platforms guess an identity from the
// system and some refuse, so a rewrite that commits works in one place and
// stops half-way in another. Maintenance writes into .git after a commit and
// races the temporary directory's cleanup, so it is disabled in the repository
// where it covers every invocation, including the ones the code under test
// makes.
func NewGitRepo(t *testing.T, trunk string) GitRepo {
	t.Helper()

	repo := GitRepo{t: t, Dir: t.TempDir()}
	repo.Run("init", "-q", "--initial-branch="+trunk)
	repo.Run("config", "user.name", "synthetic")
	repo.Run("config", "user.email", "synthetic@example.test")
	repo.Run("config", "gc.auto", "0")
	repo.Run("config", "maintenance.auto", "false")
	return repo
}

// Run executes git in the repository and fails the test if it cannot.
func (r GitRepo) Run(args ...string) string {
	r.t.Helper()
	return runGit(r.t, r.Dir, args...)
}

// Try executes git and returns its error instead of failing, for the calls a
// test makes precisely to see them fail.
func (r GitRepo) Try(args ...string) error {
	r.t.Helper()
	command := exec.Command("git", args...)
	command.Dir, command.Env = r.Dir, SyntheticGitEnv()
	return command.Run()
}

// Write puts content in a file, creating it if needed.
func (r GitRepo) Write(name, content string) {
	r.t.Helper()
	if err := os.WriteFile(filepath.Join(r.Dir, name), []byte(content), 0o600); err != nil {
		r.t.Fatalf("write %s: %v", name, err)
	}
}

// Commit writes a file and commits it under the given subject.
func (r GitRepo) Commit(subject, name, content string) {
	r.t.Helper()
	r.Write(name, content)
	r.Run("add", "-A")
	r.Run("commit", "-qm", subject)
}

// Revision resolves a revision to its object id.
func (r GitRepo) Revision(revision string) string {
	r.t.Helper()
	return r.Run("rev-parse", revision)
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir, command.Env = dir, SyntheticGitEnv()
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}
