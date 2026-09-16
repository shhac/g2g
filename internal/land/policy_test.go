package land

import (
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/githubstack"
)

// openStep builds a PathStep with exactly one open pull request, which is the
// shape every case below varies from.
func openStep(branch, base string, number int, prBase string) githubstack.PathStep {
	pr := githubstack.PullRequest{Number: number, URL: "https://example.test/" + branch, Head: branch, Base: prBase, State: "OPEN"}
	return githubstack.PathStep{
		Branch:       branch,
		ExpectedBase: base,
		Resolution:   githubstack.Resolution{Open: &pr, Latest: &pr, OpenCount: 1},
	}
}

func ready(number int) githubstack.MergeState {
	return githubstack.MergeState{Number: number, State: "OPEN", Mergeable: "MERGEABLE", StateStatus: "CLEAN"}
}

func TestClassifyRefusesWhatItCannotMerge(t *testing.T) {
	merged := githubstack.PullRequest{Number: 9, Head: "synthetic-one", Base: "synthetic-main", State: "MERGED"}
	closed := githubstack.PullRequest{Number: 9, Head: "synthetic-one", Base: "synthetic-main", State: "CLOSED"}
	first := githubstack.PullRequest{Number: 1, Head: "synthetic-one", Base: "synthetic-main", State: "OPEN"}
	second := githubstack.PullRequest{Number: 2, Head: "synthetic-one", Base: "synthetic-main", State: "OPEN"}

	for name, testCase := range map[string]struct {
		in    facts
		want  string
		wants string
	}{
		"no pull request at all": {
			in:    facts{Step: githubstack.PathStep{Branch: "synthetic-one", ExpectedBase: "synthetic-main"}},
			want:  "has no pull request to merge",
			wants: "g2g submit",
		},
		"closed without merging": {
			in: facts{Step: githubstack.PathStep{Branch: "synthetic-one", ExpectedBase: "synthetic-main",
				Resolution: githubstack.Resolution{Latest: &closed}}},
			want:  "closed without merging",
			wants: "g2g submit",
		},
		"two open pull requests": {
			in: facts{Step: githubstack.PathStep{Branch: "synthetic-one", ExpectedBase: "synthetic-main",
				Resolution: githubstack.Resolution{Latest: &second, OpenCount: 2}}, State: ready(0)},
			want: "2 open pull requests",
		},
		"draft": {
			in:    facts{Step: openStep("synthetic-one", "synthetic-main", 41, "synthetic-main"), State: githubstack.MergeState{Number: 41, Draft: true}},
			want:  "is a draft",
			wants: "gh pr ready 41",
		},
		"conflicting": {
			in: facts{Step: openStep("synthetic-one", "synthetic-main", 41, "synthetic-main"),
				State: githubstack.MergeState{Number: 41, Mergeable: githubstack.MergeableConflicting}},
			want:  "conflicts with its base",
			wants: "g2g sync",
		},
		"blocked without admin": {
			in: facts{Step: openStep("synthetic-one", "synthetic-main", 41, "synthetic-main"),
				State: githubstack.MergeState{Number: 41, Mergeable: "MERGEABLE", StateStatus: githubstack.StatusBlocked}},
			want:  "blocked by branch protection",
			wants: "g2g land --admin",
		},
		"review required without admin": {
			in: facts{Step: openStep("synthetic-one", "synthetic-main", 41, "synthetic-main"),
				State: githubstack.MergeState{Number: 41, Mergeable: "MERGEABLE", StateStatus: "CLEAN", Review: "REVIEW_REQUIRED"}},
			want:  "not approved",
			wants: "g2g land --admin",
		},
		"changes requested without admin": {
			in: facts{Step: openStep("synthetic-one", "synthetic-main", 41, "synthetic-main"),
				State: githubstack.MergeState{Number: 41, Mergeable: "MERGEABLE", StateStatus: "CLEAN", Review: "CHANGES_REQUESTED"}},
			want: "changes requested",
		},
		"merged pull request whose branch still has work": {
			// GitHub says merged; Git says the work is not upstream. Someone
			// committed after it merged, and forgetting the branch now would
			// discard that. It is not the same event as a closed pull request
			// and must not borrow its sentence.
			in: facts{Step: githubstack.PathStep{Branch: "synthetic-one", ExpectedBase: "synthetic-main",
				Resolution: githubstack.Resolution{Latest: &merged}}, Landed: false},
			want:  "carries work that is not in synthetic-main",
			wants: "g2g submit",
		},
		"ambiguous beats everything else": {
			in: facts{Step: githubstack.PathStep{Branch: "synthetic-one", ExpectedBase: "synthetic-main",
				Resolution: githubstack.Resolution{Latest: &first, OpenCount: 2}},
				State: githubstack.MergeState{Number: 1, Draft: true}},
			want: "open pull requests",
		},
	} {
		t.Run(name, func(t *testing.T) {
			step, note := classify(testCase.in)
			if note.Reason == "" {
				t.Fatalf("classify() allowed %+v", step)
			}
			if !strings.Contains(note.Reason, testCase.want) {
				t.Errorf("reason = %q, want it to mention %q", note.Reason, testCase.want)
			}
			if testCase.wants == "" {
				return
			}
			if !strings.Contains(note.Sentence(), testCase.wants) {
				t.Errorf("sentence = %q, want a way out naming %q", note.Sentence(), testCase.wants)
			}
		})
	}
}

// Git answers "has this landed", not the pull request. A squash merge puts the
// work in under a head the branch never had, so every pull-request-shaped
// answer -- merged, closed, missing -- can be wrong in the same direction.
func TestClassifyTrustsGitOverThePullRequestAboutLanding(t *testing.T) {
	for name, step := range map[string]githubstack.PathStep{
		"no pull request":   {Branch: "synthetic-one", ExpectedBase: "synthetic-main"},
		"still open":        openStep("synthetic-one", "synthetic-main", 41, "synthetic-main"),
		"closed unmergedly": {Branch: "synthetic-one", ExpectedBase: "synthetic-main", Resolution: githubstack.Resolution{Latest: &githubstack.PullRequest{Number: 9, State: "CLOSED"}}},
	} {
		t.Run(name, func(t *testing.T) {
			got, note := classify(facts{Step: step, Landed: true})
			if note.Reason != "" {
				t.Fatalf("classify() refused a branch whose work is upstream: %s", note.Reason)
			}
			if !got.Landed || got.Merges() {
				t.Errorf("step = %+v, want landed with nothing to merge", got)
			}
		})
	}
}

func TestClassifyPlansAPushAndARetargetWhenTheyAreNeeded(t *testing.T) {
	got, note := classify(facts{
		// The pull request still points at the branch below, which has just
		// merged. Merging it there would put the work into a deleted branch.
		Step:    openStep("synthetic-two", "synthetic-main", 42, "synthetic-one"),
		State:   ready(42),
		Current: false,
	})

	if note.Reason != "" {
		t.Fatalf("classify() refused: %s", note.Reason)
	}
	if !got.Retargets() || got.From != "synthetic-one" || got.Base != "synthetic-main" {
		t.Errorf("step = %+v, want a retarget from synthetic-one to synthetic-main", got)
	}
	if !got.Push {
		t.Error("step does not push a branch the remote does not have as it is here")
	}
	if got.Number != 42 || got.URL != "https://example.test/synthetic-two" {
		t.Errorf("step = %+v, want the open pull request's identity", got)
	}
}

func TestClassifyAsksForNothingWhenEverythingAlreadyAgrees(t *testing.T) {
	got, note := classify(facts{
		Step:    openStep("synthetic-one", "synthetic-main", 41, "synthetic-main"),
		State:   ready(41),
		Current: true,
	})

	if note.Reason != "" {
		t.Fatalf("classify() refused: %s", note.Reason)
	}
	if got.Push || got.Retargets() || got.Admin || got.Landed {
		t.Errorf("step = %+v, want a plain merge", got)
	}
}

// --admin is what someone reaches for to get past checks restarted by a
// restack. It also bypasses approvals, so an unapproved pull request is named
// on its own rather than folded into the protection refusal.
func TestAdminAllowsBlockedAndRecordsThatItWasNeeded(t *testing.T) {
	got, note := classify(facts{
		Step:    openStep("synthetic-two", "synthetic-main", 42, "synthetic-main"),
		State:   githubstack.MergeState{Number: 42, Mergeable: "MERGEABLE", StateStatus: githubstack.StatusBlocked, Review: githubstack.ReviewApproved},
		Current: true,
		Admin:   true,
	})

	if note.Reason != "" {
		t.Fatalf("classify() refused with --admin: %s", note.Reason)
	}
	if !got.Admin {
		t.Error("step does not record that merging it needed --admin")
	}
}

// A repository that asks for no review reports nothing, which must not read as
// a review that has not happened.
func TestNoReviewRequiredIsNotAnUnapprovedReview(t *testing.T) {
	got, note := classify(facts{
		Step:    openStep("synthetic-one", "synthetic-main", 41, "synthetic-main"),
		State:   githubstack.MergeState{Number: 41, Mergeable: "MERGEABLE", StateStatus: "CLEAN", Review: ""},
		Current: true,
	})

	if note.Reason != "" {
		t.Fatalf("classify() refused a repository that asks for no review: %s", note.Reason)
	}
	if !got.Merges() {
		t.Errorf("step = %+v, want a merge", got)
	}
}

// An unstable pull request is one with a failing check that is not required.
// Nothing here waits for checks, so it is not a refusal.
func TestAFailingNonRequiredCheckIsNotARefusal(t *testing.T) {
	got, note := classify(facts{
		Step:    openStep("synthetic-one", "synthetic-main", 41, "synthetic-main"),
		State:   githubstack.MergeState{Number: 41, Mergeable: "MERGEABLE", StateStatus: "UNSTABLE"},
		Current: true,
	})

	if note.Reason != "" {
		t.Fatalf("classify() refused an unstable pull request: %s", note.Reason)
	}
	if got.Admin {
		t.Error("an unstable pull request was recorded as needing --admin")
	}
}
