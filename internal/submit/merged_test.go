package submit

import (
	"context"
	"errors"
	"slices"
	"testing"

	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/landed"
	"github.com/shhac/g2g/internal/stack"
)

type mergedGit struct {
	fakeGit
	absent   []string
	absorbed bool
	err      error
	asked    [][3]string
	byBase   map[string]landed.Evidence
	known    map[string]string
}

func (f *mergedGit) Cherry(_ context.Context, base, head, limit string) ([]string, []string, error) {
	f.asked = append(f.asked, [3]string{base, head, limit})
	if f.byBase != nil {
		switch f.byBase[base] {
		case landed.Present:
			return nil, nil, nil
		case landed.Absent:
			return []string{"synthetic-commit"}, nil, nil
		default:
			return nil, nil, errors.New("synthetic unavailable base")
		}
	}
	return f.absent, nil, f.err
}
func (f *mergedGit) Absorbed(context.Context, string, string) (bool, error) { return f.absorbed, nil }

func (f *mergedGit) KnownTips(context.Context, string, []string) (map[string]string, error) {
	return f.known, nil
}

type fixedSelector struct{ snapshot stack.Snapshot }

func (s fixedSelector) Select(context.Context, stack.Selection, string) (stack.Snapshot, error) {
	return s.snapshot, nil
}

func TestSubmissionExcludesOnlyConfirmedMergesIntoTheRecordedParent(t *testing.T) {
	for _, tc := range []struct {
		name         string
		local, known landed.Evidence
		state, base  string
		absorbed     bool
	}{
		{"local parent", landed.Present, landed.Absent, "MERGED", "synthetic-parent", true},
		{"known parent", landed.Absent, landed.Present, "MERGED", "synthetic-parent", true},
		{"absent content", landed.Absent, landed.Absent, "MERGED", "synthetic-parent", false},
		{"unknown content", landed.Unknown, landed.Unknown, "MERGED", "synthetic-parent", false},
		{"other base", landed.Present, landed.Present, "MERGED", "synthetic-other", false},
		{"closed PR", landed.Present, landed.Present, "CLOSED", "synthetic-parent", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := stack.Snapshot{Target: "synthetic-child", Base: "synthetic-main", LandingTrunk: "synthetic-main", Branches: []string{"synthetic-parent", "synthetic-child"}, Parents: map[string]string{"synthetic-parent": "synthetic-main", "synthetic-child": "synthetic-parent"}, Source: stack.SourceG2G}
			git := &mergedGit{byBase: map[string]landed.Evidence{"synthetic-main": landed.Absent, "synthetic-parent": tc.local, "synthetic-known": tc.known}, known: map[string]string{"synthetic-parent": "synthetic-known"}}
			github := &fakeGitHub{prs: []githubstack.PullRequest{{Head: "synthetic-parent", Base: "synthetic-main", State: "OPEN", Number: 41}, {Head: "synthetic-child", Base: tc.base, State: tc.state, Number: 42}}}
			service := Service{Git: git, Selector: fixedSelector{snapshot}, GitHub: github, Pusher: &git.fakeGit}
			plan, err := service.Plan(context.Background(), stack.Selection{Branch: "synthetic-child"}, "origin", localgit.SetUpstream)
			if err != nil {
				t.Fatal(err)
			}
			_, absorbed := plan.Absorbed["synthetic-child"]
			if absorbed != tc.absorbed || slices.Contains(git.planned.Branches, "synthetic-child") == tc.absorbed || slices.Contains(plan.CreationBranches(), "synthetic-child") == tc.absorbed {
				t.Fatalf("absorbed=%v, pushed=%v, created=%v", absorbed, git.planned.Branches, plan.CreationBranches())
			}
			if tc.absorbed {
				if _, ok := plan.ExistingSpec(); !ok {
					t.Fatal("absorbed child needs metadata")
				}
				if err := service.Apply(context.Background(), plan, NewSpec(nil, ""), false); err != nil {
					t.Fatal(err)
				}
				if slices.Contains(git.published.Branches, "synthetic-child") || len(github.created) != 0 {
					t.Fatal("absorbed child published or recreated")
				}
				other := plan
				other.Absorbed = nil
				if plan.Equal(other) {
					t.Fatal("revalidation ignores parent evidence")
				}
			}
		})
	}
}

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
