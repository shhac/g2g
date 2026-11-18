package submit

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"

	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/landed"
	"github.com/shhac/g2g/internal/stack"
)

// Only a merge whose content the landing trunk is confirmed to hold is left
// out. Lifecycle alone, a closed pull request, and a merge whose content is
// absent or unreadable all still need what they needed before.
func TestSubmissionLeavesOutOnlyParentsConfirmedInTheTrunk(t *testing.T) {
	for _, tc := range []struct {
		name, state, base string
		trunk             landed.Evidence
		landed            bool
	}{
		{"merged into the trunk and present", "MERGED", "synthetic-main", landed.Present, true},
		{"merged into a feature base and present", "MERGED", "synthetic-other", landed.Present, true},
		{"merged into the trunk, content absent", "MERGED", "synthetic-main", landed.Absent, false},
		{"merged into the trunk, content unknown", "MERGED", "synthetic-main", landed.Unknown, false},
		{"merged into a feature base, content absent", "MERGED", "synthetic-other", landed.Absent, false},
		{"closed with content present", "CLOSED", "synthetic-main", landed.Present, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := stack.Snapshot{Target: "synthetic-child", Base: "synthetic-main", LandingTrunk: "synthetic-main", Branches: []string{"synthetic-parent", "synthetic-child"}, Parents: map[string]string{"synthetic-parent": "synthetic-main", "synthetic-child": "synthetic-parent"}, Source: stack.SourceG2G}
			git := &mergedGit{byBase: map[string]landed.Evidence{"synthetic-main": tc.trunk}}
			github := &fakeGitHub{prs: []githubstack.PullRequest{{Head: "synthetic-parent", Base: tc.base, State: tc.state, Number: 41}, {Head: "synthetic-child", Base: "synthetic-parent", State: "OPEN", Number: 42}}}
			service := Service{Git: git, Selector: fixedSelector{snapshot}, GitHub: github, Pusher: &git.fakeGit}
			plan, err := service.Plan(context.Background(), stack.Selection{Branch: "synthetic-child"}, "origin", localgit.SetUpstream)
			if err != nil {
				t.Fatal(err)
			}
			left := slices.Contains(plan.Landed(), "synthetic-parent")
			if left != tc.landed || slices.Contains(plan.Push.Branches, "synthetic-parent") == tc.landed || slices.Contains(plan.CreationBranches(), "synthetic-parent") == tc.landed {
				t.Fatalf("landed=%v, pushed=%v, created=%v", plan.Landed(), plan.Push.Branches, plan.CreationBranches())
			}
			if !tc.landed {
				return
			}
			if !reflect.DeepEqual(plan.Snapshot, snapshot) || !reflect.DeepEqual(plan.Publication().Parents, snapshot.Parents) {
				t.Fatal("leaving the parent out changed the selected topology")
			}
			if _, ok := plan.ExistingSpec(); !ok || plan.Blocked() != "" {
				t.Fatalf("the child's existing pull request is held up: %q", plan.Blocked())
			}
			if on := plan.OnLanded(); len(on) != 1 || on[0].Branch != "synthetic-child" {
				t.Fatalf("pull requests still based on the landed parent: %v", on)
			}
			if err := service.Apply(context.Background(), plan, NewSpec(nil, ""), false); err != nil {
				t.Fatal(err)
			}
			if slices.Contains(git.published.Branches, "synthetic-parent") || !slices.Contains(git.published.Branches, "synthetic-child") || len(github.created) != 0 {
				t.Fatalf("published=%v, created=%v", git.published.Branches, github.created)
			}
			other := plan
			other.MergedLanded = nil
			if plan.Equal(other) {
				t.Fatal("revalidation ignores the landing evidence")
			}
		})
	}
}

// The record still names the landed branch, so opening or linking across it
// is refused with the source's cleanup, while an existing pull request keeps
// the base it has.
func TestLandedBaseRefusesCreationAndLinkingOnly(t *testing.T) {
	snapshot := stack.Snapshot{Source: stack.SourceG2G, Target: "synthetic-child", Base: "synthetic-main", Branches: []string{"synthetic-parent", "synthetic-child"}, Parents: map[string]string{"synthetic-parent": "synthetic-main", "synthetic-child": "synthetic-parent"}}
	merged := githubstack.PullRequest{Head: "synthetic-parent", Base: "synthetic-main", State: "MERGED", Number: 41}
	plan := Plan{Snapshot: snapshot, Existing: []githubstack.PullRequest{merged}, Superseded: map[string]githubstack.PullRequest{"synthetic-parent": merged}, MergedLanded: map[string]bool{"synthetic-parent": true}}
	note := plan.Refusal(false)
	if note.Reason != "synthetic-child depends on landed base synthetic-parent" || len(note.Ways) != 1 || note.Ways[0].Command != "g2g prune --branch synthetic-parent --scope branch" {
		t.Fatalf("creation across a landed base: %+v", note)
	}
	git, github := &fakeGit{}, &fakeGitHub{}
	spec := NewSpec([]string{"synthetic-child"}, "")
	spec.Pulls[0].Title = "synthetic child"
	if err := (Service{Git: git, GitHub: github, Pusher: git}).Apply(context.Background(), plan, spec, false); err == nil || !strings.Contains(err.Error(), note.Reason) || git.pushes != 0 || len(github.created) != 0 {
		t.Fatalf("apply disagrees with the refusal: %v, pushes=%d, created=%v", err, git.pushes, github.created)
	}
	plan.Existing = append(plan.Existing, githubstack.PullRequest{Head: "synthetic-child", Base: "synthetic-parent", State: "OPEN", Number: 42})
	if plan.Refusal(false).Reason != "" {
		t.Fatal("an existing pull request cannot be published to")
	}
	if !strings.Contains(plan.Refusal(true).Reason, "depends on landed base") {
		t.Fatal("linking accepted a gap where the landed branch was")
	}
	plan.Snapshot.Source = stack.SourceGraphite
	if way := plan.Refusal(true).Ways[0]; way.Command != "gt sync" {
		t.Fatalf("Graphite's record was sent to %q", way.Command)
	}
}
