// Tests for the track command, which records where a branch sits.
package cli

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/graph"
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
	if !strings.Contains(out, "Nearest ancestor: synthetic-auth") {
		t.Errorf("output does not offer the nearest ancestor:\n%s", out)
	}
	// The command that decides sits on its own line rather than trailing a
	// paragraph of candidates, because it is the thing being asked for.
	if !strings.Contains(out, "g2g track --parent synthetic-auth") {
		t.Errorf("output does not name the command that records it:\n%s", out)
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
	selection := graphOptions{scopeOptions: scopeOptions{branch: "synthetic-login"}}

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

// When another record already describes the repository, a blocked adoption
// names the command that can read it. track cannot: reading Git alone is what
// lets it work with no Graphite, no GitHub, and no network. Saying so costs
// nothing and is usually the shorter road.
func TestTrackNamesImportWhenGraphiteDescribesTheRepository(t *testing.T) {
	for _, test := range []struct {
		name    string
		uses    bool
		wantHit bool
	}{
		{"graphite in use", true, true},
		{"no graphite", false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			view := trackView(graph.TrackPlan{
				Discovery: graph.Discovery{Target: "synthetic-login", Branches: []string{"synthetic-login"}},
				Blocked:   "no parent chosen",
				Candidates: []graph.Candidate{
					{Branch: "synthetic-auth", Distance: 1},
				},
			}, test.uses)

			var mentioned bool
			for _, note := range view.Notes {
				if strings.Contains(note.Text, "g2g import") {
					mentioned = true
				}
			}
			if mentioned != test.wantHit {
				t.Errorf("mentions import = %v, want %v", mentioned, test.wantHit)
			}
		})
	}
}

// The suggestion belongs to a blocked adoption. Once a parent is chosen there
// is nothing to defer to, and offering another command would read as doubt
// about the one the user just gave.
func TestTrackDoesNotSuggestImportOnceAParentIsChosen(t *testing.T) {
	view := trackView(graph.TrackPlan{
		Discovery: graph.Discovery{Target: "synthetic-login", Branches: []string{"synthetic-login"}},
		Parent:    "synthetic-auth",
	}, true)

	for _, note := range view.Notes {
		if strings.Contains(note.Text, "g2g import") {
			t.Errorf("suggested import for an adoption that already has a parent: %q", note.Text)
		}
	}
}

// Every ancestor is a candidate, so a long-lived repository offers dozens and
// listing them all buries the two that matter.
func TestTrackCapsTheCandidateTailAndCountsTheRest(t *testing.T) {
	candidates := []graph.Candidate{{Branch: "synthetic-near", Distance: 1}}
	for i := 0; i < 9; i++ {
		candidates = append(candidates, graph.Candidate{Branch: fmt.Sprintf("synthetic-far-%d", i), Distance: 30000 + i})
	}
	view := trackView(graph.TrackPlan{
		Discovery:  graph.Discovery{Target: "synthetic-login", Branches: []string{"synthetic-login"}},
		Blocked:    "no parent chosen",
		Candidates: candidates,
	}, false)

	joined := strings.Join(noteTexts(view), "\n")
	if !strings.Contains(joined, "Nearest ancestor: synthetic-near") {
		t.Errorf("the likeliest parent is not surfaced:\n%s", joined)
	}
	if !strings.Contains(joined, "further back") {
		t.Errorf("the tail is neither shown nor counted:\n%s", joined)
	}
	if strings.Contains(joined, "synthetic-far-8") {
		t.Errorf("a candidate 30k commits behind was listed in full:\n%s", joined)
	}
}

func noteTexts(view stackView) []string {
	texts := make([]string, 0, len(view.Notes))
	for _, note := range view.Notes {
		texts = append(texts, note.Text)
	}
	return texts
}
