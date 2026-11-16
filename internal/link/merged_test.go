package link

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/landed"
	"github.com/shhac/g2g/internal/stack"
)

type mergedTips struct {
	currencyTips
	absent   []string
	absorbed bool
	err      error
	asked    [][3]string
	merges   int
	byBase   map[string]landed.Evidence
}

func (f *mergedTips) Cherry(_ context.Context, base, head, limit string) ([]string, []string, error) {
	f.asked = append(f.asked, [3]string{base, head, limit})
	if f.byBase != nil {
		switch f.byBase[base] {
		case landed.Present:
			return nil, nil, nil
		case landed.Absent:
			return []string{"synthetic-commit"}, nil, nil
		default:
			return nil, nil, errors.New("synthetic unreadable base")
		}
	}
	return f.absent, nil, f.err
}
func (f *mergedTips) Absorbed(context.Context, string, string) (bool, error) {
	f.merges++
	return f.absorbed, nil
}

func mergedPlan() Plan {
	snapshot := stack.Snapshot{Target: "synthetic-head", Base: "synthetic-main", LandingTrunk: "synthetic-main", Branches: []string{"synthetic-head"}, Parents: map[string]string{"synthetic-head": "synthetic-base"}, Source: stack.SourceG2G, Ancestry: []string{"synthetic-main", "synthetic-base", "synthetic-head"}}
	prs := []githubstack.PullRequest{{Head: "synthetic-head", Base: "synthetic-base", Number: 41, State: "MERGED"}}
	return Plan{Discovery: stack.Discovery{Snapshot: snapshot, PullRequests: prs}, Issues: assessPRs(prs, snapshot)}
}

func TestMergedHistoryRequiresTrunkContent(t *testing.T) {
	for _, test := range []struct {
		name                                                      string
		absent                                                    []string
		absorbed, fail, absentBranch, noProbe, narrow, trunkMerge bool
		want                                                      IssueKind
		phrase, command                                           string
	}{
		{name: "stale trunk", trunkMerge: true, absent: []string{"synthetic-commit"}, want: IssueMergedStale, phrase: "refresh synthetic-main", command: "g2g pull"},
		{name: "unknown trunk content", trunkMerge: true, fail: true, want: IssueMergedStale, phrase: "refresh synthetic-main", command: "g2g pull"},
		{name: "no trunk probe", trunkMerge: true, noProbe: true, want: IssueMergedStale, phrase: "refresh synthetic-main", command: "g2g pull"},
		{name: "confirmed trunk merge", trunkMerge: true, want: IssueMerged, phrase: "landed in synthetic-main", command: "g2g pull"},
		{name: "narrow feature parent", narrow: true, absent: []string{"synthetic-commit"}, want: IssueMergedUnlanded, phrase: "not landed", command: "g2g submit"},
		{name: "feature base only", absent: []string{"synthetic-commit"}, want: IssueMergedUnlanded, phrase: "not landed", command: "g2g submit"},
		{name: "per commit landed", want: IssueMerged, phrase: "landed in synthetic-main", command: "g2g pull"},
		{name: "squash landed", absent: []string{"synthetic-commit"}, absorbed: true, want: IssueMerged, phrase: "landed in synthetic-main", command: "g2g pull"},
		{name: "unreadable content", fail: true, want: IssueMergedUnlanded, phrase: "landing unconfirmed", command: "g2g submit"},
		{name: "absent branch", absentBranch: true, want: IssueMergedUnlanded, phrase: "landing unconfirmed"},
		{name: "no probe", noProbe: true, want: IssueMergedUnlanded, phrase: "landing unconfirmed", command: "g2g submit"},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan := mergedPlan()
			if test.trunkMerge {
				plan.PullRequests[0].Base = plan.Trunk()
				plan.Issues = assessPRs(plan.PullRequests, plan.Snapshot)
			}
			if test.narrow {
				plan.Base = "synthetic-base"
			}
			probe := &mergedTips{absent: test.absent, absorbed: test.absorbed}
			if test.fail {
				probe.err = errors.New("synthetic content failure")
			}
			if test.absentBranch {
				plan.Absent = []string{"synthetic-head"}
			}
			service := Service{Tips: probe}
			if test.noProbe {
				service.Tips = nil
			}
			plan.Issues[0] = service.assessLanded(context.Background(), plan, plan.Issues[0], &plan.PullRequests[0], "")
			if plan.Issues[0].Kind != test.want || !strings.Contains(plan.Issues[0].Reason, test.phrase) {
				t.Fatalf("issue = %#v", plan.Issues[0])
			}
			note, _ := plan.Repair()
			if test.command != "" && (len(note.Ways) != 1 || note.Ways[0].Command != test.command) {
				t.Errorf("repair = %#v", note)
			}
			if !test.noProbe && !test.absentBranch {
				reads := 1
				if !test.trunkMerge && test.want == IssueMergedUnlanded {
					reads = 2
				}
				if len(probe.asked) != reads || probe.asked[0] != [3]string{"synthetic-main", "synthetic-head", "synthetic-main"} {
					t.Errorf("content asked %v", probe.asked)
				}
			} else if len(probe.asked) != 0 {
				t.Errorf("content asked for unavailable branch: %v", probe.asked)
			}
		})
	}
}

func TestMergedIntoRecordedParentRequiresAbsorbedContent(t *testing.T) {
	for _, tc := range []struct {
		name         string
		local, known landed.Evidence
		mismatched   bool
		want         IssueKind
	}{
		{"local parent", landed.Present, landed.Absent, false, IssueAbsorbed},
		{"known parent", landed.Absent, landed.Present, false, IssueAbsorbedStale},
		{"absent", landed.Absent, landed.Absent, false, IssueMergedUnlanded},
		{"unknown", landed.Unknown, landed.Unknown, false, IssueMergedUnlanded},
		{"other feature base", landed.Present, landed.Present, true, IssueMergedUnlanded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan := mergedPlan()
			if tc.mismatched {
				plan.PullRequests[0].Base = "synthetic-other"
			}
			probe := &mergedTips{byBase: map[string]landed.Evidence{"synthetic-main": landed.Absent, "synthetic-base": tc.local, "synthetic-known": tc.known}}
			plan.Issues[0] = (Service{Tips: probe}).assessLanded(context.Background(), plan, plan.Issues[0], &plan.PullRequests[0], "synthetic-known")
			if plan.Issues[0].Kind != tc.want {
				t.Fatalf("issue = %+v, want %s", plan.Issues[0], tc.want)
			}
			if tc.want == IssueAbsorbed || tc.want == IssueAbsorbedStale {
				note, _ := plan.Repair()
				if note.Ways[len(note.Ways)-1].Command != "g2g prune" {
					t.Fatalf("no graph repair: %+v", note)
				}
				if tc.want == IssueAbsorbedStale && !strings.Contains(note.Ways[0].Command, "--above synthetic-base") {
					t.Fatalf("stale parent sent straight to prune: %+v", note)
				}
			}
		})
	}
}

func TestAbsorbedParentCleanupStaysWithItsSource(t *testing.T) {
	for _, source := range []stack.Source{stack.SourceGraphite, stack.SourceGitHub} {
		plan := mergedPlan()
		plan.Source = source
		plan.Issues = []Issue{{Branch: "synthetic-head", Kind: IssueAbsorbedStale}}
		note, _ := plan.Repair()
		if len(note.Ways) != 1 {
			t.Fatalf("%s has a g2g parent collection step: %+v", source, note)
		}
		want := ""
		if source == stack.SourceGraphite {
			want = "gt sync"
		}
		if note.Ways[0].Command != want {
			t.Errorf("%s cleanup = %q, want %q", source, note.Ways[0].Command, want)
		}
	}
}

func TestMergedAssessmentDoesNotProbeOpenPullRequests(t *testing.T) {
	plan := mergedPlan()
	plan.PullRequests[0].State = "OPEN"
	plan.PullRequests[0].Base = "synthetic-base"
	plan.Issues = assessPRs(plan.PullRequests, plan.Snapshot)
	probe := &mergedTips{}
	if err := (Service{Tips: probe}).markLanded(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if len(probe.asked) != 0 {
		t.Fatalf("open PR prompted content reads: %v", probe.asked)
	}
}

func TestMergedUnlandedHistoryOffersReplacementBesideBaseMismatch(t *testing.T) {
	plan := mergedPlan()
	plan.Issues = append(plan.Issues, Issue{Branch: "synthetic-other", Kind: IssueBase, Reason: "synthetic base mismatch"})
	note, kinds := plan.Repair()
	if len(note.Ways) != 1 || note.Ways[0].Command != "g2g submit" || len(kinds) != 1 || kinds[0] != IssueMergedUnlanded {
		t.Errorf("repair = %#v, kinds = %v", note, kinds)
	}
}

func TestMergedAssessorUsesResolvedLandingTrunk(t *testing.T) {
	plan := mergedPlan()
	plan.Ancestry = append([]string{"synthetic-develop"}, plan.Ancestry...)
	plan.LandingTrunk = "synthetic-main"
	probe := &mergedTips{}
	issue := (Service{Tips: probe}).assessLanded(context.Background(), plan, plan.Issues[0], &plan.PullRequests[0], "")
	if issue.Kind != IssueMerged || len(probe.asked) != 1 || probe.asked[0][0] != "synthetic-main" {
		t.Fatalf("assessed wrong destination: %+v, %v", issue, probe.asked)
	}
}

func TestAssessLandedMissingAndClosed(t *testing.T) {
	for _, kind := range []IssueKind{IssueMissing, IssueClosed} {
		for _, outcome := range []string{"present", "absent", "unknown"} {
			t.Run(string(kind)+"/"+outcome, func(t *testing.T) {
				plan := mergedPlan()
				original := Issue{Branch: "synthetic-head", Kind: kind, Number: 41, Reason: "synthetic prior reason"}
				probe := &mergedTips{}
				if outcome != "present" {
					probe.absent = []string{"synthetic-commit"}
				}
				if outcome == "unknown" {
					probe.err = errors.New("synthetic unavailable content")
				}
				issue := (Service{Tips: probe}).assessLanded(context.Background(), plan, original, nil, "")
				if outcome == "present" {
					if issue.Kind != IssueLanded || issue.Number != 41 || issue.Reason != "landed in synthetic-base" {
						t.Fatal(issue)
					}
				} else if issue != original {
					t.Fatalf("negative or unknown evidence changed issue: %+v", issue)
				}
			})
		}
	}
}
