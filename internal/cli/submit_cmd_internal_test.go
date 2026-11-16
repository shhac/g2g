package cli

import (
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/repair"
	"github.com/shhac/g2g/internal/shape"
	"github.com/shhac/g2g/internal/stack"
	"github.com/shhac/g2g/internal/submit"
)

func TestSubmitLinkRepairPrecedesPreservedBaseRepairInPreviewAndApply(t *testing.T) {
	snapshot := stack.Snapshot{
		Target: "synthetic-lower", Base: "synthetic-trunk",
		Branches: []string{"synthetic-lower", "synthetic-top", "synthetic-side"},
		Parents:  map[string]string{"synthetic-lower": "synthetic-trunk", "synthetic-top": "synthetic-lower", "synthetic-side": "synthetic-lower"},
	}
	plan := submit.Plan{Snapshot: snapshot, Selected: snapshot, Repair: repair.Note{Reason: "synthetic preserved base refusal"}}
	for _, link := range []bool{false, true} {
		want := plan.Repair.Sentence()
		if link {
			want = plan.LinkBlocked()
		}
		view := submitView(plan, "", true, link, false)
		flow := (submitOptions{link: link}).flow(nil, submit.Service{}, plan, submit.Spec{}, Presentation{}, "", "")
		if got := flow.blocked(plan); got != want {
			t.Errorf("apply link=%t: refusal = %q, want %q", link, got, want)
		}
		if got := view.Repair.Sentence(); got != want {
			t.Errorf("preview link=%t: refusal = %q, want %q", link, got, want)
		}
	}
}

// With no spec, what a submission does depends on what exists: pull requests
// that all exist need none, a blocked apply goes through to say why it
// refuses, an apply that would create one fails naming how to write a spec,
// and anything else previews whatever was asked.
func TestASubmissionWithoutASpecDependsOnWhatExists(t *testing.T) {
	branches := stack.Snapshot{Base: "synthetic-main", Branches: []string{"synthetic-a"}}
	missing := submit.Plan{Snapshot: branches}
	existing := submit.Plan{Snapshot: branches, Existing: []githubstack.PullRequest{{Number: 1, Head: "synthetic-a", Base: "synthetic-main", State: "OPEN"}}}
	blocked := submit.Plan{Snapshot: branches, Issues: map[string]string{"synthetic-a": "synthetic refusal"}}
	for _, test := range []struct {
		name       string
		plan       submit.Plan
		apply      bool
		invitation string
		applies    bool
		fails      string
	}{
		{name: "existing, preview", plan: existing, invitation: "publish commits to the existing PRs"},
		{name: "existing, apply", plan: existing, apply: true, invitation: "publish commits to the existing PRs", applies: true},
		{name: "blocked apply", plan: blocked, apply: true, applies: true},
		{name: "missing, apply", plan: missing, apply: true, fails: "missing PRs require a submission spec"},
		{name: "missing, preview", plan: missing, invitation: "Create a spec with:"},
	} {
		t.Run(test.name, func(t *testing.T) {
			chosen, err := submitOptions{apply: test.apply}.submission(test.plan)
			if test.fails != "" {
				if err == nil || !strings.Contains(err.Error(), test.fails) {
					t.Fatalf("submission() error = %v, want %q", err, test.fails)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if chosen.apply != test.applies || !strings.Contains(chosen.invitation, test.invitation) {
				t.Errorf("submission() = %+v, want apply %t and an invitation containing %q", chosen, test.applies, test.invitation)
			}
		})
	}
}

// After the branch below was deleted, the pull request above still points at
// it. submit leaves an existing base alone, and its preview says so by name,
// with the retarget that moves it.
func TestSubmitNamesAPullRequestBasedOffTheStack(t *testing.T) {
	for _, test := range []struct {
		name, source, want string
	}{
		{name: "standing on it", want: "g2g github retarget --apply"},
		{name: "named", source: shape.TargetNamed, want: "g2g github retarget --branch synthetic-b --apply"},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan := submit.Plan{
				Snapshot: stack.Snapshot{Target: "synthetic-b", TargetSource: test.source, Base: "synthetic-main", Branches: []string{"synthetic-b"}, Scope: shape.ScopeStack},
				Existing: []githubstack.PullRequest{{Number: 7, Head: "synthetic-b", Base: "synthetic-deleted", State: "OPEN"}},
			}
			plan.Selected = plan.Snapshot
			view := submitView(plan, "", false, false, false)
			said := ""
			for _, note := range view.Notes {
				said += note.Text + "\n"
			}
			for _, want := range []string{"#7 (synthetic-b) is based on synthetic-deleted, where the stack records synthetic-main", test.want} {
				if !strings.Contains(said, want) {
					t.Errorf("the preview does not say %q:\n%s", want, said)
				}
			}
		})
	}
}

// A base somebody chose inside the stack -- every pull request aimed at the
// trunk -- is preserved without comment, as it always was.
func TestSubmitSaysNothingOfABaseChosenInsideTheStack(t *testing.T) {
	plan := submit.Plan{
		Snapshot: stack.Snapshot{Target: "synthetic-b", Base: "synthetic-main", Branches: []string{"synthetic-a", "synthetic-b"},
			Parents: map[string]string{"synthetic-a": "synthetic-main", "synthetic-b": "synthetic-a"}},
		Existing: []githubstack.PullRequest{
			{Number: 6, Head: "synthetic-a", Base: "synthetic-main", State: "OPEN"},
			{Number: 7, Head: "synthetic-b", Base: "synthetic-main", State: "OPEN"},
		},
	}
	// The base differs from the recorded parent, which is what the preview
	// has to stay quiet about; without the edge there would be nothing to
	// stay quiet about, and the test would pass for no reason.
	if plan.LinkBlocked() == "" {
		t.Fatal("synthetic-b's pull request reads as based on its recorded parent")
	}
	plan.Selected = plan.Snapshot
	for _, note := range submitView(plan, "", false, false, false).Notes {
		if strings.Contains(note.Text, "retarget") {
			t.Errorf("the preview flags a base inside the stack: %q", note.Text)
		}
	}
}

func TestSubmitMergedHistoryNamesLandingEvidence(t *testing.T) {
	for _, test := range []struct {
		name        string
		assessments map[string]bool
		want        string
	}{
		{name: "feature only", assessments: map[string]bool{"synthetic-head": false}, want: "merged into synthetic-base, not landed"},
		{name: "confirmed trunk", assessments: map[string]bool{"synthetic-head": true}, want: "merged · landed in synthetic-main"},
		{name: "unconfirmed", want: "merged into synthetic-base, landing unconfirmed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan := submit.Plan{Snapshot: stack.Snapshot{Base: "synthetic-main"}, Superseded: map[string]githubstack.PullRequest{"synthetic-head": {Number: 41, Base: "synthetic-base", State: "MERGED"}}, MergedLanded: test.assessments}
			node := submitLabel(plan, nil, stackNode{Branch: "synthetic-head"}, true)
			if !strings.Contains(node.state(), test.want) {
				t.Errorf("label = %q, want %q", node.state(), test.want)
			}
		})
	}
}
