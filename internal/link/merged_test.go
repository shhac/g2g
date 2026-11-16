package link

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/stack"
)

type mergedTips struct {
	currencyTips
	absent   []string
	absorbed bool
	err      error
	asked    [][3]string
	merges   int
}

func (f *mergedTips) Cherry(_ context.Context, base, head, limit string) ([]string, []string, error) {
	f.asked = append(f.asked, [3]string{base, head, limit})
	return f.absent, nil, f.err
}
func (f *mergedTips) Absorbed(context.Context, string, string) (bool, error) {
	f.merges++
	return f.absorbed, nil
}

func mergedPlan() Plan {
	snapshot := stack.Snapshot{Target: "synthetic-head", Base: "synthetic-main", Branches: []string{"synthetic-head"}, Parents: map[string]string{"synthetic-head": "synthetic-base"}, Source: stack.SourceG2G, Ancestry: []string{"synthetic-main", "synthetic-base", "synthetic-head"}}
	prs := []githubstack.PullRequest{{Head: "synthetic-head", Base: "synthetic-base", Number: 41, State: "MERGED"}}
	return Plan{Discovery: stack.Discovery{Snapshot: snapshot, PullRequests: prs}, Issues: assessPRs(prs, snapshot)}
}

func TestMergedHistoryRequiresTrunkContent(t *testing.T) {
	for _, test := range []struct {
		name                                          string
		absent                                        []string
		absorbed, fail, absentBranch, noProbe, narrow bool
		want                                          IssueKind
		phrase, command                               string
	}{
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
			if err := service.markLanded(context.Background(), plan); err != nil {
				t.Fatal(err)
			}
			if plan.Issues[0].Kind != test.want || !strings.Contains(plan.Issues[0].Reason, test.phrase) {
				t.Fatalf("issue = %#v", plan.Issues[0])
			}
			note, _ := plan.Repair()
			if test.command != "" && (len(note.Ways) != 1 || note.Ways[0].Command != test.command) {
				t.Errorf("repair = %#v", note)
			}
			if !test.noProbe && !test.absentBranch {
				if len(probe.asked) != 1 || probe.asked[0] != [3]string{"synthetic-main", "synthetic-head", "synthetic-main"} {
					t.Errorf("content asked %v", probe.asked)
				}
			} else if len(probe.asked) != 0 {
				t.Errorf("content asked for unavailable branch: %v", probe.asked)
			}
		})
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
