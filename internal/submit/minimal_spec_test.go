package submit

import (
	"context"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/githubstack"
)

func TestMinimalSpecUsesSnapshotBasesAndIgnoresExistingMetadata(t *testing.T) {
	for _, full := range []bool{false, true} {
		t.Run(map[bool]string{false: "minimal", true: "legacy"}[full], func(t *testing.T) {
			git := &fakeGit{}
			github := &fakeGitHub{prs: []githubstack.PullRequest{{Head: "synthetic/lower", State: "OPEN", Number: 11}}}
			plan := Plan{Snapshot: snapshot(), Remote: "origin", Existing: github.prs}
			// Deliberately reversed: entry order must never choose a PR's base.
			spec := Spec{Version: 1, Draft: true, Pulls: []Pull{{Branch: "synthetic/top", Title: "top"}, {Branch: "synthetic/middle", Title: "middle"}}}
			if full {
				spec.Pulls = append(spec.Pulls, Pull{Branch: "synthetic/lower", Reviewers: []string{"--ignored-existing-metadata"}})
			}
			path, err := Write(t.TempDir(), spec)
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := Read(path, plan)
			if err != nil {
				t.Fatal(err)
			}
			if err := (Service{Git: git, GitHub: github, Pusher: git}).Apply(context.Background(), plan, parsed, false); err != nil {
				t.Fatal(err)
			}
			if got := strings.Join(github.created, ","); got != "synthetic/middle<-synthetic/lower,synthetic/top<-synthetic/middle" {
				t.Fatalf("created %s", got)
			}
		})
	}
}

func TestMinimalSpecValidationBeforePublication(t *testing.T) {
	for _, test := range []struct {
		name  string
		pulls []Pull
		want  string
	}{
		{"missing", []Pull{{Branch: "synthetic/middle", Title: "middle"}}, "missing PR entry"},
		{"duplicate", []Pull{{Branch: "synthetic/middle", Title: "middle"}, {Branch: "synthetic/middle", Title: "again"}}, "repeats"},
		{"unknown", []Pull{{Branch: "synthetic/unknown", Title: "unknown"}}, "unknown"},
		{"title", []Pull{{Branch: "synthetic/middle"}, {Branch: "synthetic/top", Title: "top"}}, "missing title"},
		{"reviewer", []Pull{{Branch: "synthetic/middle", Title: "middle", Reviewers: []string{"--flag"}}, {Branch: "synthetic/top", Title: "top"}}, "reviewer"},
	} {
		t.Run(test.name, func(t *testing.T) {
			git := &fakeGit{}
			github := &fakeGitHub{prs: []githubstack.PullRequest{{Head: "synthetic/lower", State: "OPEN"}}}
			plan := Plan{Snapshot: snapshot(), Remote: "origin", Existing: github.prs}
			spec := Spec{Version: 1, Pulls: test.pulls}
			path, err := Write(t.TempDir(), spec)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Read(path, plan); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Read error %v", err)
			}
			if err := (Service{Git: git, GitHub: github, Pusher: git}).Apply(context.Background(), plan, spec, false); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Apply error %v", err)
			}
			if git.pushes != 0 || len(github.created) != 0 {
				t.Fatal("invalid spec mutated remote")
			}
		})
	}
}

func TestExistingOnlySpecNeedsNoEntries(t *testing.T) {
	plan := Plan{Snapshot: snapshot()}
	for _, branch := range plan.Snapshot.Branches {
		plan.Existing = append(plan.Existing, githubstack.PullRequest{Head: branch, State: "OPEN"})
	}
	spec, ok := plan.ExistingSpec()
	if !ok || len(spec.Pulls) != 0 {
		t.Fatalf("spec %#v existing %v", spec, ok)
	}
	if err := validateSpec(plan, spec); err != nil {
		t.Fatal(err)
	}
	plan.Existing[0].State = "CLOSED"
	if err := validateSpec(plan, spec); err == nil {
		t.Fatal("closed existing PR no longer required metadata")
	}
}
