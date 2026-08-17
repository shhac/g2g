// Tests for the track command, which records where a branch sits.
package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/shhac/gt2gh/internal/graph"
)

// A bare track must not choose. The nearest ancestor is usually right, and
// "usually" is not a basis for writing down structure every later command
// trusts.
func TestTrackWithoutAParentPreviewsCandidatesAndBlocks(t *testing.T) {
	out, store, err := runGraph(t, graph.New(), false, "track", "--branch", "synthetic-login")
	if err != nil {
		t.Fatalf("track: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Apply blocked") {
		t.Errorf("output does not block:\n%s", out)
	}
	if !strings.Contains(out, "Candidate parents, nearest first: synthetic-auth") {
		t.Errorf("output does not offer the nearest ancestor:\n%s", out)
	}
	if store.writes != 0 {
		t.Error("a blocked preview wrote to the store")
	}
	assertGolden(t, "track-candidates-plain", out)
}

func TestTrackApplyWritesOnceAndRecordsTheNewRoot(t *testing.T) {
	out, store, err := runGraph(t, graph.New(), false, "track", "--branch", "synthetic-auth", "--parent", "synthetic-main", "--apply")
	if err != nil {
		t.Fatalf("track --apply: %v\n%s", err, out)
	}
	if store.writes != 1 {
		t.Fatalf("store writes = %d, want exactly one", store.writes)
	}
	if parent, _ := store.graph.Parent("synthetic-auth"); parent != "synthetic-main" {
		t.Errorf("recorded parent = %q", parent)
	}
	if !store.graph.IsTrunk("synthetic-main") {
		t.Error("the new root was not recorded, so the next branch up could not find it")
	}
	if !strings.Contains(out, "becomes a root") {
		t.Errorf("output does not say a root was recorded:\n%s", out)
	}
}

func TestTrackApplyRefusesABlockedPlanWithoutWriting(t *testing.T) {
	out, store, err := runGraph(t, graphFixture(), false, "track", "--branch", "synthetic-login", "--parent", "synthetic-absent", "--apply")
	if err == nil {
		t.Fatalf("track --apply: error = nil\n%s", out)
	}
	if store.writes != 0 {
		t.Error("a blocked plan was written")
	}
}

func TestTrackIsANoOpWhenTheParentIsAlreadyRecorded(t *testing.T) {
	out, store, err := runGraph(t, graphFixture(), false, "track", "--branch", "synthetic-login", "--parent", "synthetic-auth", "--apply")
	if err != nil {
		t.Fatalf("track --apply: %v\n%s", err, out)
	}
	if store.writes != 0 {
		t.Errorf("store writes = %d, want none for an unchanged edge", store.writes)
	}
	if !strings.Contains(out, "already records this parent") {
		t.Errorf("output does not report the no-op:\n%s", out)
	}
}

// Completion is part of the documented interface, and the parent candidates it
// offers must be the same ones the preview would show — otherwise a shell
// suggests a branch that track then refuses.
func TestParentCompletionOffersTheSameCandidatesAsThePreview(t *testing.T) {
	service := graph.Service{Git: graphGitFixture(), Store: &graphStore{graph: graph.New()}}
	selection := graphOptions{branch: "synthetic-login"}

	completed, err := parentCompletions(service, &selection)(context.Background(), "")
	if err != nil {
		t.Fatalf("parentCompletions() error = %v", err)
	}

	plan, err := service.PlanTrack(context.Background(), selection.Selection(), "")
	if err != nil {
		t.Fatal(err)
	}
	previewed := make([]string, 0, len(plan.Candidates))
	for _, candidate := range plan.Candidates {
		previewed = append(previewed, candidate.Branch)
	}
	if strings.Join(completed, ",") != strings.Join(previewed, ",") {
		t.Errorf("completion = %v, preview = %v", completed, previewed)
	}
	if len(completed) == 0 {
		t.Error("completion offered nothing")
	}
}

// Recording a parent whose commits are not in the branch is legitimate — it is
// how a stack looks before a restack — but it must not happen silently.
func TestTrackWarnsWhenTheParentIsNotAnAncestor(t *testing.T) {
	out, _, err := runGraph(t, graph.New(), false, "track", "--branch", "synthetic-auth", "--parent", "synthetic-billing")
	if err != nil {
		t.Fatalf("track: %v\n%s", err, out)
	}
	if !strings.Contains(out, "is not an ancestor of synthetic-auth") {
		t.Errorf("output does not warn about the asserted edge:\n%s", out)
	}
	if !strings.Contains(out, "needing a restack") {
		t.Errorf("output does not say what the edge will read as:\n%s", out)
	}
}

func TestTrackConfirmsAnEdgeGitAlreadyAgreesWith(t *testing.T) {
	out, _, err := runGraph(t, graph.New(), false, "track", "--branch", "synthetic-login", "--parent", "synthetic-auth")
	if err != nil {
		t.Fatalf("track: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Commit ancestry confirms synthetic-auth is already below synthetic-login") {
		t.Errorf("output does not confirm the edge:\n%s", out)
	}
}
