package submit

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/stack"
)

// forked is synthetic-a, then synthetic-b, which carries synthetic-c and
// synthetic-d, and synthetic-e on synthetic-d.
func forked(target string) stack.Snapshot {
	return stack.Snapshot{
		Target:   target,
		Base:     "main",
		Branches: []string{"synthetic-a", "synthetic-b", "synthetic-c", "synthetic-d", "synthetic-e"},
		Parents: map[string]string{
			"synthetic-a": "main", "synthetic-b": "synthetic-a",
			"synthetic-c": "synthetic-b", "synthetic-d": "synthetic-b", "synthetic-e": "synthetic-d",
		},
	}
}

// Each arm of a fork is asked on its own: a branch is left out only when
// everything above it is, and what a submitted branch sits on never is.
func TestLeftOutAsksEachArmOfAForkOnItsOwn(t *testing.T) {
	for _, test := range []struct {
		name   string
		target string
		empty  []string
		want   []string
	}{
		{name: "one arm's top", target: "synthetic-a", empty: []string{"synthetic-e"}, want: []string{"synthetic-e"}},
		{name: "a whole arm", target: "synthetic-a", empty: []string{"synthetic-d", "synthetic-e"}, want: []string{"synthetic-d", "synthetic-e"}},
		{name: "an empty branch under one with work", target: "synthetic-a", empty: []string{"synthetic-d"}},
		{name: "an empty fork point under two arms with work", target: "synthetic-a", empty: []string{"synthetic-b"}},
		{name: "everything above the target", target: "synthetic-a", empty: []string{"synthetic-b", "synthetic-c", "synthetic-d", "synthetic-e"}, want: []string{"synthetic-b", "synthetic-c", "synthetic-d", "synthetic-e"}},
		{name: "never the target", target: "synthetic-d", empty: []string{"synthetic-d", "synthetic-e"}, want: []string{"synthetic-e"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			asked := []string{}
			got, err := leftOut(forked(test.target), func(branch string) (bool, error) {
				asked = append(asked, branch)
				return slices.Contains(test.empty, branch), nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, test.want) {
				t.Errorf("left out %v, want %v", got, test.want)
			}
			for _, branch := range asked {
				if branch == test.target {
					t.Errorf("asked about the target %s", branch)
				}
			}
		})
	}
}

// Each missing pull request opens on the branch it is recorded on, parents
// first. A rolling base would open synthetic-d on its sibling synthetic-c.
func TestApplyOpensEachArmOfAForkOnItsOwnParent(t *testing.T) {
	git, github := &fakeGit{}, &fakeGitHub{}
	plan := Plan{Snapshot: forked("synthetic-a"), Existing: []githubstack.PullRequest{
		{Number: 1, Head: "synthetic-a", Base: "main", State: "OPEN"},
		{Number: 2, Head: "synthetic-b", Base: "synthetic-a", State: "OPEN"},
	}}
	spec := NewSpec(plan.CreationBranches(), "")
	for index := range spec.Pulls {
		spec.Pulls[index].Title = "synthetic title"
	}
	if err := (Service{Git: git, GitHub: github, Pusher: git}).Apply(context.Background(), plan, spec, false); err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(github.created, ","), "synthetic-c<-synthetic-b,synthetic-d<-synthetic-b,synthetic-e<-synthetic-d"; got != want {
		t.Errorf("created = %q, want %q", got, want)
	}
}

// A native stack is one line, so --link refuses a fork, with the leaves and
// dropping --link as ways out, and an apply asked to link one publishes
// nothing. A fork whose second arm is left out is a line, and links.
func TestLinkingAForkIsRefusedAndALineLinks(t *testing.T) {
	plan := Plan{Snapshot: forked("synthetic-a")}
	note := plan.LinkRepair()
	if !strings.Contains(note.Sentence(), "select a leaf with --branch (synthetic-c, synthetic-e)") || !strings.Contains(note.Ways[len(note.Ways)-1].Effect, "drop --link") {
		t.Fatalf("note = %+v", note)
	}
	git, github := &fakeGit{}, &fakeGitHub{}
	if err := (Service{Git: git, GitHub: github, Pusher: git}).Apply(context.Background(), plan, NewSpec(nil, ""), true); err == nil || git.pushes != 0 {
		t.Fatalf("linking a fork: err = %v, pushes = %d", err, git.pushes)
	}

	line := without(forked("synthetic-a"), []string{"synthetic-d", "synthetic-e"})
	if blocked := (Plan{Snapshot: line}).LinkBlocked(); blocked != "" {
		t.Errorf("a line is refused: %s", blocked)
	}
}
