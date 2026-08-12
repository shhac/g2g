package graphite

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shhac/gt2gh/internal/subprocess"
	"github.com/shhac/gt2gh/internal/testutil"
)

func TestClientUsesSupportedReadOnlyCommands(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join("testdata", "irregular-stack.txt"))
	if err != nil {
		t.Fatal(err)
	}
	arguments := filepath.Join(t.TempDir(), "gt-arguments")
	fixturePath := filepath.Join(t.TempDir(), "stack.txt")
	if err := os.WriteFile(fixturePath, fixture, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GT_FIXTURE", fixturePath)
	t.Setenv("GT_ARGUMENTS", arguments)
	testutil.WithFakeExecutables(t, map[string]string{
		"gt": `if [ "$1" = "--version" ]; then
  printf '1.8.6\n'
  exit 0
fi
printf '%s\n' "$*" >> "$GT_ARGUMENTS"
if [ "$*" = "log --all --reverse --no-interactive" ]; then
  cat "$GT_FIXTURE"
  exit 0
fi
exit 9`,
	})

	stack, err := (Client{Runner: subprocess.ExecRunner{}}).Discover(context.Background(), "beta-two-deep")
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if got, want := strings.Join(stack.Branches, ","), "alpha,beta,beta-two,beta-two-deep"; got != want {
		t.Errorf("branches = %q, want %q", got, want)
	}
	called, err := os.ReadFile(arguments)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(called), "log --all --reverse --no-interactive\n"; got != want {
		t.Errorf("gt calls = %q, want %q", got, want)
	}
}

func TestClientRejectsDifferentGraphiteVersion(t *testing.T) {
	testutil.WithFakeExecutables(t, map[string]string{"gt": "printf '1.9.0\\n'"})
	_, err := (Client{Runner: subprocess.ExecRunner{}}).TrackedBranches(context.Background())
	if err == nil || !strings.Contains(err.Error(), "unsupported Graphite CLI version") {
		t.Fatalf("TrackedBranches() error = %v", err)
	}
}
