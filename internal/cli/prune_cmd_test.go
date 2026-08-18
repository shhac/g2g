package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/prune"
)

// pruneCLIGit answers "has this branch landed" by content, which is all prune
// asks Git.
type pruneCLIGit struct{ landed map[string]bool }

func (g pruneCLIGit) Cherry(_ context.Context, _, head, _ string) (absent, present []string, err error) {
	if g.landed[head] {
		return nil, []string{"synthetic-commit"}, nil
	}
	return []string{"synthetic-commit"}, nil, nil
}

type pruneCLIRefs struct{ unpinned []string }

func (pruneCLIRefs) PinForkPoint(context.Context, string, string) error { return nil }
func (r *pruneCLIRefs) UnpinForkPoint(_ context.Context, branch string) error {
	r.unpinned = append(r.unpinned, branch)
	return nil
}

func runPrune(t *testing.T, landed []string, args ...string) (string, *graphStore, *pruneCLIRefs, error) {
	t.Helper()

	recorded := graph.New()
	for _, edge := range []struct{ branch, parent string }{
		{"synthetic-auth", "synthetic-main"},
		{"synthetic-login", "synthetic-auth"},
	} {
		updated, err := recorded.Track(edge.branch, graph.Edge{Parent: edge.parent, ForkPoint: "0000000000000000000000000000000000000000"})
		if err != nil {
			t.Fatalf("Track(%q) error = %v", edge.branch, err)
		}
		recorded = updated
	}

	store := &graphStore{graph: recorded}
	refs := &pruneCLIRefs{}
	has := map[string]bool{}
	for _, branch := range landed {
		has[branch] = true
	}
	graphService := graph.Service{Git: graphGitFixture(), Store: store, Refs: refs}

	var stdout, stderr bytes.Buffer
	command := NewWithOptions(Options{
		Version:      "v0.1.0",
		Stdout:       &stdout,
		Stderr:       &stderr,
		Graph:        graphService,
		Prune:        prune.Service{Git: pruneCLIGit{landed: has}, Graph: graphService},
		Presentation: &Presentation{},
	})
	command.SetArgs(args)
	err := command.Execute()
	return stdout.String(), store, refs, err
}

// prune --apply panicked on a nil branches closure and shipped that way,
// because nothing exercised the command end to end. Every other mutating
// command's closures are optional and nil-guarded; this one being
// required-but-unchecked was not a distinction a caller could see.
func TestPruneApplyForgetsTheLandedBranch(t *testing.T) {
	out, store, refs, err := runPrune(t, []string{"synthetic-login"}, "prune", "--branch", "synthetic-login", "--apply")
	if err != nil {
		t.Fatalf("prune --apply error = %v\n%s", err, out)
	}
	if store.graph.Tracked("synthetic-login") {
		t.Error("the landed branch is still recorded")
	}
	if !store.graph.Tracked("synthetic-auth") {
		t.Error("prune forgot a branch that had not landed")
	}
	if got, want := strings.Join(refs.unpinned, ","), "synthetic-login"; got != want {
		t.Errorf("unpinned = %q, want %q", got, want)
	}
	if !strings.Contains(out, "No branch was deleted") {
		t.Errorf("output does not say what it did not do:\n%s", out)
	}
}

// A preview leaves no trace, which for a graph command means the store is
// never written.
func TestPrunePreviewWritesNothing(t *testing.T) {
	out, store, refs, err := runPrune(t, []string{"synthetic-login"}, "prune", "--branch", "synthetic-login")
	if err != nil {
		t.Fatalf("prune error = %v\n%s", err, out)
	}
	if store.writes != 0 || len(refs.unpinned) != 0 {
		t.Errorf("a preview wrote: %d graph writes, %v unpinned", store.writes, refs.unpinned)
	}
	if !strings.Contains(out, "No changes were made") {
		t.Errorf("preview does not say it changed nothing:\n%s", out)
	}
}

// Forgetting a branch while something recorded under it survives is a refusal,
// and a refusal must come before the ready banner rather than out of Apply.
func TestPruneRefusesToStrandAndNeverAnnouncesItself(t *testing.T) {
	out, store, _, err := runPrune(t, []string{"synthetic-auth"}, "prune", "--branch", "synthetic-auth", "--scope", "branch", "--apply")
	if err == nil {
		t.Fatalf("prune --apply error = nil; forgetting synthetic-auth strands synthetic-login\n%s", out)
	}
	if store.writes != 0 {
		t.Errorf("a refused prune wrote to the store %d times", store.writes)
	}
	if strings.Contains(out, "Ready to apply") {
		t.Errorf("a plan that cannot run was introduced as one that is about to:\n%s", out)
	}
}

// Nothing landed is an ordinary answer, not an error.
func TestPruneWithNothingLandedSaysSo(t *testing.T) {
	out, store, _, err := runPrune(t, nil, "prune", "--branch", "synthetic-login", "--apply")
	if err != nil {
		t.Fatalf("prune --apply error = %v\n%s", err, out)
	}
	if store.writes != 0 {
		t.Error("an empty prune wrote to the store")
	}
	if !strings.Contains(out, "Nothing has landed") {
		t.Errorf("output does not report an empty prune:\n%s", out)
	}
}
