package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/shhac/gt2gh/internal/githubstack"
	"github.com/shhac/gt2gh/internal/link"
)

// link refuses a wrong base but sync reconciles exactly that, so a path
// blocked solely on bases must name sync rather than leave the reader to
// discover it.
func TestLinkBlockedOnlyOnBasesPointsAtSync(t *testing.T) {
	plan := link.Plan{
		Target: "beta", Base: "main", Branches: []string{"alpha", "beta"},
		PullRequests: []githubstack.PullRequest{
			{Number: 1, Head: "alpha", Base: "main", State: "OPEN"},
			{Number: 2, Head: "beta", Base: "main", State: "OPEN"},
		},
		Issues: []link.Issue{{Branch: "beta", Kind: link.IssueBase, Reason: "PR #2 base main, want alpha"}},
	}

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
		{Branch: "beta", Kind: link.IssueNonOpen, Reason: "closed pull request"},
		{Branch: "beta", Kind: link.IssueAmbiguous, Reason: "2 open pull requests"},
	} {
		t.Run(string(issue.Kind), func(t *testing.T) {
			plan := link.Plan{
				Target: "beta", Base: "main", Branches: []string{"alpha", "beta"},
				PullRequests: []githubstack.PullRequest{{Number: 1, Head: "alpha", Base: "main", State: "OPEN"}},
				Issues:       []link.Issue{issue},
			}
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
	plan := link.Plan{
		Target: "beta", Base: "main", Branches: []string{"alpha", "beta"},
		PullRequests: []githubstack.PullRequest{{Number: 1, Head: "alpha", Base: "synthetic-other", State: "OPEN"}},
		Issues: []link.Issue{
			{Branch: "alpha", Kind: link.IssueBase, Reason: "PR #1 base synthetic-other, want main"},
			{Branch: "beta", Kind: link.IssueMissing, Reason: "no open pull request"},
		},
	}
	var output bytes.Buffer
	if err := writeLinkPlan(&output, plan, Presentation{}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "g2g sync") {
		t.Errorf("mixed blockers wrongly pointed at sync:\n%s", output.String())
	}
}
