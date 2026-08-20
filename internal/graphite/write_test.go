package graphite

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/subprocess"
	"github.com/shhac/g2g/internal/testutil"
)

// writingClient installs a gt whose every invocation is recorded, so a test can
// assert the exact argv rather than only the outcome. That is the seam an
// injected fake cannot cover and the one a Graphite release would break.
func writingClient(t *testing.T, fixture string) (Client, func() string) {
	t.Helper()

	arguments := filepath.Join(t.TempDir(), "gt-arguments")
	fixturePath := filepath.Join(t.TempDir(), "stack.txt")
	if err := os.WriteFile(fixturePath, []byte(fixture), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GT_FIXTURE", fixturePath)
	t.Setenv("GT_ARGUMENTS", arguments)
	testutil.WithFakeExecutables(t, map[string]string{
		"gt": `printf '%s\n' "$*" >> "$GT_ARGUMENTS"
case "$1" in
  --version) printf '1.8.6\n' ;;
  log) cat "$GT_FIXTURE" ;;
  track|untrack) ;;
  *) exit 9 ;;
esac`,
	})
	return Client{Runner: subprocess.ExecRunner{}}, func() string {
		recorded, err := os.ReadFile(arguments)
		if err != nil {
			return ""
		}
		return string(recorded)
	}
}

const forestFixture = "◯  synthetic-trunk\n◯  synthetic-lower\n◉  synthetic-top\n"

func TestReadForestReturnsEveryDeclaredRelationship(t *testing.T) {
	client, _ := writingClient(t, forestFixture)

	forest, err := client.ReadForest(context.Background())
	if err != nil {
		t.Fatalf("ReadForest() error = %v", err)
	}

	if got, want := strings.Join(forest.Branches(), ","), "synthetic-lower,synthetic-top,synthetic-trunk"; got != want {
		t.Errorf("Branches() = %s, want %s", got, want)
	}
	if got := forest.Parents["synthetic-top"]; got != "synthetic-lower" {
		t.Errorf("parent of synthetic-top = %q", got)
	}
	if got := forest.Parents["synthetic-trunk"]; got != "" {
		t.Errorf("parent of the root = %q, want empty", got)
	}
	if got := strings.Join(forest.Children("synthetic-lower"), ","); got != "synthetic-top" {
		t.Errorf("Children(synthetic-lower) = %s", got)
	}
}

// The branch is named on the command line. Anything else would mean checking
// out every branch in the forest to align it.
func TestTrackNamesTheBranchInsteadOfCheckingItOut(t *testing.T) {
	client, recorded := writingClient(t, forestFixture)

	if err := client.Track(context.Background(), "synthetic-top", "synthetic-lower"); err != nil {
		t.Fatalf("Track() error = %v", err)
	}

	if got, want := recorded(), "track synthetic-top --parent synthetic-lower --no-interactive"; !strings.Contains(got, want) {
		t.Errorf("recorded %q, want it to contain %q", got, want)
	}
}

func TestUntrackNamesTheBranchAndSuppressesThePrompt(t *testing.T) {
	client, recorded := writingClient(t, forestFixture)

	if err := client.Untrack(context.Background(), "synthetic-top"); err != nil {
		t.Fatalf("Untrack() error = %v", err)
	}

	if got, want := recorded(), "untrack synthetic-top --force --no-interactive"; !strings.Contains(got, want) {
		t.Errorf("recorded %q, want it to contain %q", got, want)
	}
}

// A write is gated on the same version check as a read. The cost of a changed
// contract is higher here, not lower.
func TestWritesRunTheVersionCheckFirst(t *testing.T) {
	for name, write := range map[string]func(Client) error{
		"track":   func(c Client) error { return c.Track(context.Background(), "synthetic-top", "synthetic-lower") },
		"untrack": func(c Client) error { return c.Untrack(context.Background(), "synthetic-top") },
	} {
		t.Run(name, func(t *testing.T) {
			client, recorded := writingClient(t, forestFixture)

			if err := write(client); err != nil {
				t.Fatalf("%s error = %v", name, err)
			}
			if got := recorded(); !strings.HasPrefix(got, "--version") {
				t.Errorf("recorded %q, want the version check to run first", got)
			}
		})
	}
}

// An unsupported major version must stop a write before it happens, exactly as
// it stops a read.
func TestAnUnsupportedVersionStopsAWrite(t *testing.T) {
	testutil.WithFakeExecutables(t, map[string]string{
		"gt": `if [ "$1" = "--version" ]; then printf '99.0.0\n'; exit 0; fi
exit 1`,
	})
	client := Client{Runner: subprocess.ExecRunner{}}

	if err := client.Track(context.Background(), "synthetic-top", "synthetic-lower"); err == nil {
		t.Error("Track() error = nil against an unsupported Graphite major version")
	}
}

// A name Graphite would read as an option never reaches it, and is refused
// before any process is spawned.
func TestOptionLikeNamesAreRefusedBeforeSpawning(t *testing.T) {
	client, recorded := writingClient(t, forestFixture)

	if err := client.Track(context.Background(), "-synthetic", "synthetic-lower"); err == nil {
		t.Error("Track() error = nil for an option-like branch name")
	}
	if err := client.Track(context.Background(), "synthetic-top", "--parent-injection"); err == nil {
		t.Error("Track() error = nil for an option-like parent name")
	}
	if err := client.Untrack(context.Background(), "-synthetic"); err == nil {
		t.Error("Untrack() error = nil for an option-like branch name")
	}
	if err := client.Track(context.Background(), "", "synthetic-lower"); err == nil {
		t.Error("Track() error = nil for an empty branch name")
	}
	if err := client.Untrack(context.Background(), ""); err == nil {
		t.Error("Untrack() error = nil for an empty branch name")
	}
	if got := recorded(); got != "" {
		t.Errorf("recorded %q, want no Graphite command to have run at all", got)
	}
}

func TestAnUnconfiguredClientRefusesToWrite(t *testing.T) {
	if err := (Client{}).Track(context.Background(), "synthetic-top", "synthetic-lower"); err == nil {
		t.Error("Track() error = nil on an unconfigured client")
	}
	if err := (Client{}).Untrack(context.Background(), "synthetic-top"); err == nil {
		t.Error("Untrack() error = nil on an unconfigured client")
	}
}

// A write that Graphite rejects must surface as an error naming the command,
// not be swallowed. Nothing exercised a failing gt write before this.
func TestAFailingWriteIsReportedWithItsCommand(t *testing.T) {
	testutil.WithFakeExecutables(t, map[string]string{
		"gt": `if [ "$1" = "--version" ]; then printf '1.8.6\n'; exit 0; fi
printf 'synthetic graphite refusal\n' >&2
exit 1`,
	})
	client := Client{Runner: subprocess.ExecRunner{}}

	err := client.Track(context.Background(), "synthetic-top", "synthetic-lower")
	if err == nil {
		t.Fatal("Track() error = nil when gt exited non-zero")
	}
	if !strings.Contains(err.Error(), "gt track synthetic-top") {
		t.Errorf("error = %v, want it to name the command that failed", err)
	}

	if err := client.Untrack(context.Background(), "synthetic-top"); err == nil {
		t.Error("Untrack() error = nil when gt exited non-zero")
	}
}

// The forest is parsed from another tool's display, so it can hold a shape the
// g2g store cannot: graph.Track refuses a self-parent, a rendered line does not.
// Walking it with a local copy that omitted the shared self-loop guard made a
// branch its own child, and every caller was surviving that on its own visited
// set rather than on the walk being correct.
func TestABranchDeclaredAsItsOwnParentIsNotItsOwnChild(t *testing.T) {
	forest := Forest{Parents: map[string]string{
		"synthetic-loop": "synthetic-loop",
		"synthetic-a":    "synthetic-loop",
	}}

	if children := forest.Children("synthetic-loop"); len(children) != 1 || children[0] != "synthetic-a" {
		t.Errorf("Children() = %v, want only synthetic-a", children)
	}
}

// A branch named only as somebody's parent is still a branch the forest knows.
// The local copy walked map keys alone and dropped it.
func TestAParentWithNoEntryOfItsOwnIsStillNamed(t *testing.T) {
	forest := Forest{Parents: map[string]string{"synthetic-a": "synthetic-trunk"}}

	branches := forest.Branches()
	if len(branches) != 2 || branches[0] != "synthetic-a" || branches[1] != "synthetic-trunk" {
		t.Errorf("Branches() = %v, want both the child and the parent", branches)
	}
}
