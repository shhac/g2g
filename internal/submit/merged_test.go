package submit

import (
	"context"
	"errors"
	"testing"

	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/stack"
)

type mergedGit struct {
	fakeGit
	absent   []string
	absorbed bool
	err      error
	asked    [][3]string
}

func (f *mergedGit) Cherry(_ context.Context, base, head, limit string) ([]string, []string, error) {
	f.asked = append(f.asked, [3]string{base, head, limit})
	return f.absent, nil, f.err
}
func (f *mergedGit) Absorbed(context.Context, string, string) (bool, error) { return f.absorbed, nil }

func TestSupersededMergedHistoryIsAssessedByContentOnly(t *testing.T) {
	for _, test := range []struct {
		name, state                    string
		absent                         []string
		absorbed, fail, want, assessed bool
	}{
		{name: "feature only", state: "MERGED", absent: []string{"synthetic-commit"}, assessed: true},
		{name: "per commit landed", state: "MERGED", want: true, assessed: true},
		{name: "squash landed", state: "MERGED", absent: []string{"synthetic-commit"}, absorbed: true, want: true, assessed: true},
		{name: "unknown", state: "MERGED", fail: true},
		{name: "closed", state: "CLOSED"},
		{name: "open", state: "OPEN"},
	} {
		t.Run(test.name, func(t *testing.T) {
			git := &mergedGit{absent: test.absent, absorbed: test.absorbed}
			if test.fail {
				git.err = errors.New("synthetic content failure")
			}
			plan := Plan{Snapshot: stack.Snapshot{Base: "synthetic-main", Branches: []string{"synthetic-head"}}, Superseded: map[string]githubstack.PullRequest{"synthetic-head": {State: test.state}}}
			answer, assessed := (Service{Git: git}).mergedLanded(context.Background(), plan)["synthetic-head"]
			if answer != test.want || assessed != test.assessed {
				t.Errorf("landed = %v, assessed = %v", answer, assessed)
			}
			if test.state == "MERGED" {
				if len(git.asked) != 1 || git.asked[0] != [3]string{"synthetic-main", "synthetic-head", "synthetic-main"} {
					t.Errorf("asked = %v", git.asked)
				}
			} else if len(git.asked) != 0 {
				t.Errorf("probed nonmerged history: %v", git.asked)
			}
		})
	}
}

func TestMergedContentUsesResolvedLandingTrunk(t *testing.T) {
	git := &mergedGit{}
	plan := Plan{Snapshot: stack.Snapshot{Base: "synthetic-parent", LandingTrunk: "synthetic-main", Ancestry: []string{"synthetic-develop", "synthetic-main", "synthetic-parent", "synthetic-head"}, Branches: []string{"synthetic-head"}}, Superseded: map[string]githubstack.PullRequest{"synthetic-head": {State: "MERGED"}}}
	result := (Service{Git: git}).mergedLanded(context.Background(), plan)
	if !result["synthetic-head"] || len(git.asked) != 1 || git.asked[0] != [3]string{"synthetic-main", "synthetic-head", "synthetic-main"} {
		t.Fatalf("wrong landing destination: %v, %v", result, git.asked)
	}
}
