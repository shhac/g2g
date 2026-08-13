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
if [ "$*" = "log short --all --reverse --no-interactive" ]; then
  cat "$GT_FIXTURE"
  exit 0
fi
exit 9`,
	})

	stack, err := (Client{Runner: subprocess.ExecRunner{}}).Discover(context.Background(), "beta-side")
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if got, want := strings.Join(stack.Path, ","), "main,alpha,beta,beta-top,beta-side"; got != want {
		t.Errorf("path = %q, want %q", got, want)
	}
	called, err := os.ReadFile(arguments)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(called), "log short --all --reverse --no-interactive\n"; got != want {
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

func TestClientKeepsMultipleTrunkComponentsSeparate(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join("testdata", "multiple-trunks.txt"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GT_FIXTURE", string(fixture))
	testutil.WithFakeExecutables(t, map[string]string{
		"gt": `if [ "$1" = "--version" ]; then printf '1.8.6\n'; exit 0; fi
if [ "$*" = "log short --all --reverse --no-interactive" ]; then printf '%s' "$GT_FIXTURE"; exit 0; fi
exit 9`,
	})
	stack, err := (Client{Runner: subprocess.ExecRunner{}}).Discover(context.Background(), "feature-two")
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if got, want := strings.Join(stack.Path, ","), "main,feature-one,feature-two"; got != want {
		t.Errorf("path = %q, want %q", got, want)
	}
	if got, want := strings.Join(stack.Trunks, ","), "main,develop,staging"; got != want {
		t.Errorf("trunks = %q, want %q", got, want)
	}
}

func TestClientDiscoversNeedsRestackBranch(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join("testdata", "needs-restack-stack.txt"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GT_FIXTURE", string(fixture))
	testutil.WithFakeExecutables(t, map[string]string{
		"gt": `if [ "$1" = "--version" ]; then printf '1.8.6\n'; exit 0; fi
if [ "$*" = "log short --all --reverse --no-interactive" ]; then printf '%s' "$GT_FIXTURE"; exit 0; fi
exit 9`,
	})
	stack, err := (Client{Runner: subprocess.ExecRunner{}}).Discover(context.Background(), "synthetic-feature")
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if got, want := strings.Join(stack.Path, ","), "trunk,foundation,synthetic-feature"; got != want {
		t.Errorf("path = %q, want %q", got, want)
	}
}

func TestClientDiscoversPathAlongsideWorktreeAnnotatedSibling(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join("testdata", "worktree-annotation-stack.txt"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GT_FIXTURE", string(fixture))
	testutil.WithFakeExecutables(t, map[string]string{
		"gt": `if [ "$1" = "--version" ]; then printf '1.8.6\n'; exit 0; fi
if [ "$*" = "log short --all --reverse --no-interactive" ]; then printf '%s' "$GT_FIXTURE"; exit 0; fi
exit 9`,
	})
	stack, err := (Client{Runner: subprocess.ExecRunner{}}).Discover(context.Background(), "synthetic-selected")
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if got, want := strings.Join(stack.Path, ","), "trunk,foundation,synthetic-selected"; got != want {
		t.Errorf("path = %q, want %q", got, want)
	}
}

func TestDiscoverStackExtendsOnlyOneLinearDescendantChain(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join("testdata", "irregular-stack.txt"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GT_FIXTURE", string(fixture))
	testutil.WithFakeExecutables(t, map[string]string{
		"gt": `if [ "$1" = "--version" ]; then printf '1.8.6\n'; exit 0; fi
if [ "$*" = "log short --all --reverse --no-interactive" ]; then printf '%s' "$GT_FIXTURE"; exit 0; fi
exit 9`,
	})
	client := Client{Runner: subprocess.ExecRunner{}}
	for _, test := range []struct{ selected, want string }{
		{"beta", "main,alpha,beta,beta-top,beta-side"},
		{"beta-top", "main,alpha,beta,beta-top,beta-side"},
		{"beta-side", "main,alpha,beta,beta-top,beta-side"},
	} {
		t.Run(test.selected, func(t *testing.T) {
			stack, err := client.DiscoverStack(context.Background(), test.selected, true)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Join(stack.Path, ","); got != test.want {
				t.Errorf("path = %q, want %q", got, test.want)
			}
		})
	}
}

func TestDiscoverStackRejectsDescendantFork(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join("testdata", "irregular-stack.txt"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GT_FIXTURE", string(fixture))
	testutil.WithFakeExecutables(t, map[string]string{
		"gt": `if [ "$1" = "--version" ]; then printf '1.8.6\n'; exit 0; fi
if [ "$*" = "log short --all --reverse --no-interactive" ]; then printf '%s' "$GT_FIXTURE"; exit 0; fi
exit 9`,
	})
	_, err = (Client{Runner: subprocess.ExecRunner{}}).DiscoverStack(context.Background(), "alpha", true)
	if err == nil || !strings.Contains(err.Error(), "multiple descendants") {
		t.Fatalf("DiscoverStack() error = %v", err)
	}
}

func TestResolveStackRejectsInvalidGraphRelationships(t *testing.T) {
	for _, test := range []struct {
		name     string
		graph    graph
		selected string
		want     string
	}{
		{
			name:     "untracked",
			graph:    graph{nodes: map[string]node{}},
			selected: "synthetic-missing",
			want:     "does not track",
		},
		{
			name: "missing parent",
			graph: graph{nodes: map[string]node{
				"synthetic-tip": {name: "synthetic-tip", parent: "synthetic-missing"},
			}},
			selected: "synthetic-tip",
			want:     "missing parent",
		},
		{
			name: "cycle",
			graph: graph{nodes: map[string]node{
				"synthetic-a": {name: "synthetic-a", parent: "synthetic-b"},
				"synthetic-b": {name: "synthetic-b", parent: "synthetic-a"},
			}},
			selected: "synthetic-a",
			want:     "ancestry cycle",
		},
		{
			name: "descendant fork",
			graph: graph{nodes: map[string]node{
				"synthetic-main": {name: "synthetic-main"},
				"synthetic-a":    {name: "synthetic-a", parent: "synthetic-main"},
				"synthetic-b":    {name: "synthetic-b", parent: "synthetic-main"},
			}},
			selected: "synthetic-main",
			want:     "multiple descendants",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := resolveStack(test.graph, test.selected, true)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("resolveStack() error = %v", err)
			}
		})
	}
}
