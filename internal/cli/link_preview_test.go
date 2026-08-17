package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/link"
	"github.com/shhac/g2g/internal/stack"
)

// link refuses a wrong base but sync reconciles exactly that, so a path
// blocked solely on bases must name sync rather than leave the reader to
// discover it.
func TestLinkBlockedOnlyOnBasesPointsAtSync(t *testing.T) {
	plan := link.Plan{Discovery: stack.Discovery{Snapshot: stack.Snapshot{Target: "beta", Base: "main", Branches: []string{"alpha", "beta"}}, PullRequests: []githubstack.PullRequest{
		{Number: 1, Head: "alpha", Base: "main", State: "OPEN"},
		{Number: 2, Head: "beta", Base: "main", State: "OPEN"},
	}}, Issues: []link.Issue{{Branch: "beta", Kind: link.IssueBase, Reason: "PR #2 base main, want alpha"}}}

	var output bytes.Buffer
	if err := writeLinkPlan(&output, plan, Presentation{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "g2g sync") {
		t.Errorf("base-only blocker did not name sync:\n%s", output.String())
	}
}

// Anything sync also refuses must not be advertised as sync-repairable, or
// the suggestion sends the reader to a command that will block too.
func TestLinkBlockedOnAnythingElseDoesNotPointAtSync(t *testing.T) {
	for _, issue := range []link.Issue{
		{Branch: "beta", Kind: link.IssueMissing, Reason: "no open pull request"},
		{Branch: "beta", Kind: link.IssueMerged, Reason: "merged pull request"},
		{Branch: "beta", Kind: link.IssueAmbiguous, Reason: "2 open pull requests"},
	} {
		t.Run(string(issue.Kind), func(t *testing.T) {
			plan := link.Plan{Discovery: stack.Discovery{Snapshot: stack.Snapshot{Target: "beta", Base: "main", Branches: []string{"alpha", "beta"}}, PullRequests: []githubstack.PullRequest{{Number: 1, Head: "alpha", Base: "main", State: "OPEN"}}}, Issues: []link.Issue{issue}}
			var output bytes.Buffer
			if err := writeLinkPlan(&output, plan, Presentation{}); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(output.String(), "g2g sync") {
				t.Errorf("%s blocker wrongly pointed at sync:\n%s", issue.Kind, output.String())
			}
		})
	}
}

// A mixed set is not sync-repairable: sync refuses the whole path if any
// branch lacks an open pull request.
func TestLinkBlockedOnMixedCausesDoesNotPointAtSync(t *testing.T) {
	plan := link.Plan{Discovery: stack.Discovery{Snapshot: stack.Snapshot{Target: "beta", Base: "main", Branches: []string{"alpha", "beta"}}, PullRequests: []githubstack.PullRequest{{Number: 1, Head: "alpha", Base: "synthetic-other", State: "OPEN"}}}, Issues: []link.Issue{
		{Branch: "alpha", Kind: link.IssueBase, Reason: "PR #1 base synthetic-other, want main"},
		{Branch: "beta", Kind: link.IssueMissing, Reason: "no open pull request"},
	}}
	var output bytes.Buffer
	if err := writeLinkPlan(&output, plan, Presentation{}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "g2g sync") {
		t.Errorf("mixed blockers wrongly pointed at sync:\n%s", output.String())
	}
}

// A blocked preview must name the command that fixes it. Which command depends
// on why it is blocked, and getting that wrong sends the reader to a second
// command that also refuses.
func TestBlockedPreviewNamesTheCommandThatFixesIt(t *testing.T) {
	for _, test := range []struct {
		name   string
		issues []link.Issue
		want   string
	}{
		{
			name:   "merged branch needs Graphite, not g2g",
			issues: []link.Issue{{Branch: "feat-a", Kind: link.IssueMerged, Reason: "merged pull request"}},
			want:   "feat-a already merged. Run gt sync in Graphite to restack",
		},
		{
			// Merged wins over everything: nothing here helps until the stack
			// itself is restacked.
			name: "merged alongside other blockers still points at Graphite",
			issues: []link.Issue{
				{Branch: "feat-a", Kind: link.IssueMerged, Reason: "merged pull request"},
				{Branch: "feat-c", Kind: link.IssueMissing, Reason: "no open pull request"},
			},
			want: "feat-a already merged. Run gt sync in Graphite to restack",
		},
		{
			name:   "missing pull request needs submit",
			issues: []link.Issue{{Branch: "feat-c", Kind: link.IssueMissing, Reason: "no open pull request"}},
			want:   "feat-c has no pull request. Run g2g submit",
		},
		{
			// submit creates a replacement for a closed pull request, so it is
			// the right command here too.
			name:   "closed pull request also needs submit",
			issues: []link.Issue{{Branch: "feat-c", Kind: link.IssueClosed, Reason: "closed pull request"}},
			want:   "feat-c had its pull request closed. Run g2g submit",
		},
		{
			name: "several missing branches read as a list",
			issues: []link.Issue{
				{Branch: "feat-b", Kind: link.IssueMissing, Reason: "no open pull request"},
				{Branch: "feat-c", Kind: link.IssueMissing, Reason: "no open pull request"},
			},
			want: "feat-b and feat-c have no pull request. Run g2g submit",
		},
		{
			name:   "wrong base needs sync",
			issues: []link.Issue{{Branch: "feat-c", Kind: link.IssueBase, Reason: "PR #3 base main, want feat-b"}},
			want:   "Run g2g sync",
		},
		{
			// Ambiguity is the one case a person has to resolve by hand.
			name:   "ambiguity has no command to offer",
			issues: []link.Issue{{Branch: "feat-c", Kind: link.IssueAmbiguous, Reason: "2 open pull requests"}},
			want:   "resolve every unresolved GitHub PR mapping first",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan := link.Plan{Discovery: stack.Discovery{Snapshot: stack.Snapshot{Target: "feat-c", Base: "main", Branches: []string{"feat-a", "feat-b", "feat-c"}}}, Issues: test.issues}
			if got := blockedReason(plan); !strings.Contains(got, test.want) {
				t.Errorf("blockedReason() = %q, want it to contain %q", got, test.want)
			}
		})
	}
}
