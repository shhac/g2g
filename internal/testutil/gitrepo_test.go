package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A helper's explicit Env hid this leak: commands the adapters spawned still
// inherited personal signing settings. A clone also loses repository-local
// overrides, so isolate the test process rather than disabling signing in one
// repository.
func TestGitRepoIsolatesAdapterSubprocessesAndClones(t *testing.T) {
	root := t.TempDir()
	global, system := filepath.Join(root, "synthetic-global"), filepath.Join(root, "synthetic-system")
	config := "[commit]\n\tgpgSign = true\n[gpg]\n\tprogram = synthetic-missing-signer\n"
	for _, path := range []string{global, system} {
		if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	t.Setenv("GIT_CONFIG_SYSTEM", system)
	t.Run("isolated", func(t *testing.T) {
		repo := NewGitRepo(t, "synthetic-main")
		repo.Commit("synthetic root", "root.txt", "root")
		run := func(dir string, args ...string) {
			t.Helper()
			command := exec.Command("git", args...)
			command.Dir = dir
			// Deliberately inherit the process environment, as the adapters do.
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v\n%s", args, err, output)
			}
		}
		run(repo.Dir, "commit", "--quiet", "--allow-empty", "--message=synthetic adapter commit")
		clone := filepath.Join(t.TempDir(), "synthetic-clone")
		run(root, "clone", "--quiet", repo.Dir, clone)
		run(clone, "config", "user.name", "synthetic")
		run(clone, "config", "user.email", "synthetic@example.test")
		run(clone, "commit", "--quiet", "--allow-empty", "--message=synthetic clone commit")
	})
	if os.Getenv("GIT_CONFIG_GLOBAL") != global || os.Getenv("GIT_CONFIG_SYSTEM") != system {
		t.Fatal("the isolated test did not restore its caller's environment")
	}
	for _, path := range []string{global, system} {
		if data, err := os.ReadFile(path); err != nil || string(data) != config {
			t.Fatalf("the caller's Git configuration changed: %v", err)
		}
	}
}
