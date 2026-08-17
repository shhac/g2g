package graphite

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/diagnostic"
	"github.com/shhac/g2g/internal/subprocess"
	"github.com/shhac/g2g/internal/testutil"
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

	var warnings bytes.Buffer
	ctx := diagnostic.WithWarningWriter(context.Background(), &warnings)
	stack, err := (Client{Runner: subprocess.ExecRunner{}}).Discover(ctx, "beta-side")
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
	if got := warnings.String(); got != "" {
		t.Errorf("warnings = %q, want none for known version", got)
	}
}

func TestClientWarnsForCompatibleUnknownGraphiteVersion(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join("testdata", "irregular-stack.txt"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GT_FIXTURE", string(fixture))
	testutil.WithFakeExecutables(t, map[string]string{
		"gt": `if [ "$1" = "--version" ]; then printf '1.8.7\n'; exit 0; fi
if [ "$*" = "log short --all --reverse --no-interactive" ]; then printf '%s' "$GT_FIXTURE"; exit 0; fi
exit 9`,
	})
	var warnings bytes.Buffer
	ctx := diagnostic.WithWarningWriter(context.Background(), &warnings)
	client := Client{Runner: subprocess.ExecRunner{}}
	for range 2 {
		if _, err := client.Discover(ctx, "beta-side"); err != nil {
			t.Fatalf("Discover() error = %v", err)
		}
	}
	if got := warnings.String(); !strings.Contains(got, "warning: Graphite CLI version 1.8.7 is not a known supported version") || strings.Count(got, "warning:") != 1 {
		t.Errorf("warnings = %q", got)
	}
}

func TestClientRejectsDifferentGraphiteMajorVersion(t *testing.T) {
	testutil.WithFakeExecutables(t, map[string]string{"gt": "printf '2.0.0\\n'"})
	_, err := (Client{Runner: subprocess.ExecRunner{}}).TrackedBranches(context.Background())
	if err == nil || !strings.Contains(err.Error(), "unsupported Graphite CLI major version") {
		t.Fatalf("TrackedBranches() error = %v", err)
	}
}

func TestClientRejectsUnrecognizedGraphiteVersionOutput(t *testing.T) {
	testutil.WithFakeExecutables(t, map[string]string{"gt": "printf 'graphite 1.8.7\\n'"})
	_, err := (Client{Runner: subprocess.ExecRunner{}}).TrackedBranches(context.Background())
	if err == nil || !strings.Contains(err.Error(), "unrecognized Graphite CLI version output") {
		t.Fatalf("TrackedBranches() error = %v", err)
	}
}

func TestCheckVersion(t *testing.T) {
	for _, test := range []struct {
		version string
		known   bool
		wantErr string
	}{
		{version: "1.8.6", known: true},
		{version: "1.8.7"},
		{version: "1.9.0"},
		{version: "2.0.0", wantErr: "major version"},
		{version: "graphite 1.8.7", wantErr: "unrecognized"},
		{version: "1.8", wantErr: "unrecognized"},
		{version: "01.8.7", wantErr: "unrecognized"},
	} {
		t.Run(test.version, func(t *testing.T) {
			got, known, err := checkVersion([]byte(test.version + "\n"))
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("checkVersion() error = %v", err)
				}
				return
			}
			if err != nil || got != test.version || known != test.known {
				t.Errorf("checkVersion() = (%q, %t, %v)", got, known, err)
			}
		})
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

// The forest is what a scope is applied to, so the mapping from the compact
// display to parent edges is the thing worth pinning.
//
// Discovery used to take a bool that extended through the one unambiguous
// downward chain, and refused a branch with two children outright. alpha here
// has four, which is the ordinary shape of a trunk and was the shape that could
// not be asked about at all.
func TestReadForestGivesEveryDeclaredEdge(t *testing.T) {
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

	forest, err := (Client{Runner: subprocess.ExecRunner{}}).ReadForest(context.Background())
	if err != nil {
		t.Fatalf("ReadForest() error = %v", err)
	}
	for branch, want := range map[string]string{
		"main":         "",
		"alpha":        "main",
		"beta":         "alpha",
		"beta-top":     "beta",
		"beta-side":    "beta-top",
		"gamma":        "alpha",
		"gamma-deep":   "gamma",
		"delta":        "alpha",
		"delta-deep":   "delta",
		"epsilon":      "alpha",
		"epsilon-deep": "epsilon",
	} {
		if got := forest.Parents[branch]; got != want {
			t.Errorf("parent of %q = %q, want %q", branch, got, want)
		}
	}
	if got, want := strings.Join(forest.Roots, ","), "main"; got != want {
		t.Errorf("roots = %q, want %q", got, want)
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
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := resolveStack(test.graph, test.selected)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("resolveStack() error = %v", err)
			}
		})
	}
}

// A branch with several children is an ordinary shape, not an error. Resolving
// its ancestry is all this package does now; how much of the forest a command
// acts on is a scope decided above it.
func TestResolveStackAcceptsABranchWithSeveralChildren(t *testing.T) {
	forked := graph{nodes: map[string]node{
		"synthetic-main": {name: "synthetic-main"},
		"synthetic-a":    {name: "synthetic-a", parent: "synthetic-main"},
		"synthetic-b":    {name: "synthetic-b", parent: "synthetic-main"},
	}}

	stack, err := resolveStack(forked, "synthetic-main")
	if err != nil {
		t.Fatalf("resolveStack() error = %v; a trunk with two stacks on it is the ordinary case", err)
	}
	if got, want := strings.Join(stack.Path, ","), "synthetic-main"; got != want {
		t.Errorf("path = %q, want %q", got, want)
	}
}
