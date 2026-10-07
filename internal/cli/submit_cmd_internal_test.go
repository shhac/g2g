package cli

import (
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/stack"
	"github.com/shhac/g2g/internal/submit"
)

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
