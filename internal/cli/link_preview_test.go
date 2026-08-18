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

// The safe set is now a list rather than a triple negation, so it is worth
// asserting directly: this decides whether a rendered command can be pasted.
func TestShellQuoteLeavesSafeArgumentsAloneAndQuotesTheRest(t *testing.T) {
	for _, safe := range []string{"main", "paul/eco-1627", "a_b-c.d/e:f=g@h", "0123"} {
		if got := shellQuote(safe); got != safe {
			t.Errorf("shellQuote(%q) = %q, want it untouched", safe, got)
		}
	}
	for _, unsafe := range []string{"", "two words", "semi;colon", "dollar$sign", "back`tick`"} {
		got := shellQuote(unsafe)
		if !strings.HasPrefix(got, "'") || !strings.HasSuffix(got, "'") {
			t.Errorf("shellQuote(%q) = %q, want it quoted", unsafe, got)
		}
	}
	// An embedded quote must not end the quoting early.
	if got, want := shellQuote("it's"), `'it'\''s'`; got != want {
		t.Errorf("shellQuote(%q) = %q, want %q", "it's", got, want)
	}
}

// The sentence a machine reads and the block a person reads are built
// separately, so the thing that must not drift is which command they name.
// Naming different ones would send the two readers to different remedies for
// the same state.
func TestBothAdviceFormsNameTheSameCommand(t *testing.T) {
	for _, test := range []struct {
		name    string
		issues  []link.Issue
		command string
	}{
		{name: "merged", issues: []link.Issue{{Branch: "feat-a", Kind: link.IssueMerged, Reason: "PR merged"}}, command: "gt sync"},
		{name: "missing", issues: []link.Issue{{Branch: "feat-c", Kind: link.IssueMissing, Reason: "no open PR"}}, command: "g2g submit"},
		{name: "closed", issues: []link.Issue{{Branch: "feat-c", Kind: link.IssueClosed, Number: 7, Reason: "PR closed"}}, command: "g2g submit"},
		{name: "wrong base", issues: []link.Issue{{Branch: "feat-c", Kind: link.IssueBase, Number: 3, Reason: "PR #3 base main, want feat-b"}}, command: "g2g sync"},
		{name: "ambiguous has no command", issues: []link.Issue{{Branch: "feat-c", Kind: link.IssueAmbiguous, Reason: "2 open PRs"}}, command: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan := link.Plan{Issues: test.issues}

			structured := repairAdvice(plan)
			if structured.Command != test.command {
				t.Errorf("laid-out advice names %q, want %q", structured.Command, test.command)
			}
			sentence := blockedReason(plan)
			if test.command == "" {
				return
			}
			if !strings.Contains(sentence, test.command) {
				t.Errorf("the sentence %q does not name %q", sentence, test.command)
			}
		})
	}
}
