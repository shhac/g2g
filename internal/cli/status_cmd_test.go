package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/link"
	"github.com/shhac/g2g/internal/stack"
)

func TestStatusRendersCompactAlignedAndBlockedPath(t *testing.T) {
	plan := link.Plan{Discovery: stack.Discovery{Snapshot: stack.Snapshot{Target: "synthetic/top", Base: "main", Branches: []string{"synthetic/lower", "synthetic/top"}}, PullRequests: []githubstack.PullRequest{{Head: "synthetic/lower", Number: 11, State: "OPEN"}, {Head: "synthetic/top", Number: 12, State: "OPEN"}}}, Issues: []link.Issue{{Branch: "synthetic/top", Kind: link.IssueMissing, Reason: "no open pull request"}}}
	var out bytes.Buffer
	if err := writeStatus(&out, plan, Presentation{}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Target  synthetic/top", "\u25cb main", "#11", "aligned", "blocked: no open pull request", "Safe next action: synthetic/top has no pull request. Run g2g submit"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in %q", want, out.String())
		}
	}
}

func TestStatusRendersOneNativeStackSummaryWithoutRepeatedBadges(t *testing.T) {
	plan := link.Plan{Discovery: stack.Discovery{Snapshot: stack.Snapshot{Target: "synthetic/top", Base: "main", Branches: []string{"synthetic/lower", "synthetic/top"}}, PullRequests: []githubstack.PullRequest{{Head: "synthetic/lower", Number: 11, State: "OPEN", StackNumber: 17, StackSize: 2, StackPosition: 1}, {Head: "synthetic/top", Number: 12, State: "OPEN", StackNumber: 17, StackSize: 2, StackPosition: 2}}}}
	var out bytes.Buffer
	if err := writeStatus(&out, plan, Presentation{}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "GitHub stack #17 · selected path 2/2 · aligned") {
		t.Errorf("missing aligned summary in %q", got)
	}
	if strings.Contains(got, "stack #17, position") {
		t.Errorf("healthy graph repeated native-stack badges: %q", got)
	}
}

func TestStatusMarksOnlyNativeStackExceptions(t *testing.T) {
	plan := link.Plan{Discovery: stack.Discovery{Snapshot: stack.Snapshot{Target: "synthetic/top", Base: "main", Branches: []string{"synthetic/lower", "synthetic/top"}}, PullRequests: []githubstack.PullRequest{{Head: "synthetic/lower", Number: 11, State: "OPEN", StackNumber: 17, StackSize: 2, StackPosition: 1}, {Head: "synthetic/top", Number: 12, State: "OPEN"}}}}
	var out bytes.Buffer
	if err := writeStatus(&out, plan, Presentation{}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"#12", "not linked", "GitHub stack #17 \u00b7 partial (1/2 linked)"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
}

func TestStatusMarksConflictingNativeStackMembership(t *testing.T) {
	plan := link.Plan{Discovery: stack.Discovery{Snapshot: stack.Snapshot{Target: "synthetic/top", Base: "main", Branches: []string{"synthetic/lower", "synthetic/top"}}, PullRequests: []githubstack.PullRequest{{Head: "synthetic/lower", Number: 11, State: "OPEN", StackNumber: 17, StackSize: 2, StackPosition: 2}, {Head: "synthetic/top", Number: 12, State: "OPEN", StackNumber: 17, StackSize: 2, StackPosition: 1}}}}
	var out bytes.Buffer
	if err := writeStatus(&out, plan, Presentation{}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"stack #17, position 2", "GitHub stack: conflicting membership"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
}

// The reproduction: a trunk with several children used to be refused outright,
// because full-stack resolution walked down a unique child chain and there was
// no unique child. Rendering it is the whole point of a scope that can fork.
func TestStatusRendersAForkedSelection(t *testing.T) {
	plan := link.Plan{Discovery: stack.Discovery{Snapshot: stack.Snapshot{
		Target:   "synthetic-trunk",
		Base:     "synthetic-trunk",
		Scope:    stack.ScopeSubtree,
		Source:   stack.SourceGraphite,
		Branches: []string{"synthetic-a", "synthetic-a-one", "synthetic-b"},
		Parents: map[string]string{
			"synthetic-a":     "synthetic-trunk",
			"synthetic-a-one": "synthetic-a",
			"synthetic-b":     "synthetic-trunk",
		},
	}, PullRequests: []githubstack.PullRequest{
		{Head: "synthetic-a", Number: 11, State: "OPEN", Base: "synthetic-trunk"},
		{Head: "synthetic-a-one", Number: 12, State: "OPEN", Base: "synthetic-a"},
		{Head: "synthetic-b", Number: 13, State: "OPEN", Base: "synthetic-trunk"},
	}}}
	var out bytes.Buffer
	if err := writeStatus(&out, plan, Presentation{}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	// Siblings hang off the trunk and the grandchild hangs off its own parent,
	// which is exactly what a rolling base could not have expressed.
	for _, want := range []string{"├─", "└─", "#11", "#12", "#13"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in forked status:\n%s", want, got)
		}
	}
	if strings.Contains(got, "blocked") {
		t.Errorf("a correctly based fork reported a problem:\n%s", got)
	}
}

// Each branch is assessed against its own parent. Under a rolling base the
// second sibling would be compared against the first and reported as
// misbased — the precise bug that made forked assessment meaningless.
func TestStatusAssessesSiblingsAgainstTheirParentNotEachOther(t *testing.T) {
	snapshot := stack.Snapshot{
		Target:   "synthetic-trunk",
		Base:     "synthetic-trunk",
		Scope:    stack.ScopeSubtree,
		Source:   stack.SourceGraphite,
		Branches: []string{"synthetic-a", "synthetic-b"},
		Parents:  map[string]string{"synthetic-a": "synthetic-trunk", "synthetic-b": "synthetic-trunk"},
	}
	prs := []githubstack.PullRequest{
		{Head: "synthetic-a", Number: 11, State: "OPEN", Base: "synthetic-trunk"},
		{Head: "synthetic-b", Number: 12, State: "OPEN", Base: "synthetic-trunk"},
	}
	issues := make([]string, 0)
	for step := range githubstack.Across(snapshot.Parents, snapshot.Branches, prs) {
		if step.Classify() != githubstack.StepAligned {
			issues = append(issues, step.Branch)
		}
	}
	if len(issues) != 0 {
		t.Errorf("siblings reported as misbased: %v", issues)
	}
}

// Which record described the stack is a property of the invocation, not of the
// repository, so the reader has to be told.
func TestStatusNamesTheRecordThatDescribedTheStack(t *testing.T) {
	plan := link.Plan{Discovery: stack.Discovery{Snapshot: stack.Snapshot{
		Target: "synthetic-top", Base: "synthetic-trunk", Scope: stack.ScopePath,
		Source: stack.SourceG2G, Branches: []string{"synthetic-top"},
	}}}
	var out bytes.Buffer
	if err := writeStatus(&out, plan, Presentation{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Structure from g2g") {
		t.Errorf("status did not say which record answered:\n%s", out.String())
	}
}

// A chain must keep rendering as the flat list every other command shows.
func TestStatusLeavesALinearSelectionUnindented(t *testing.T) {
	plan := link.Plan{Discovery: stack.Discovery{Snapshot: stack.Snapshot{
		Target: "synthetic-top", Base: "main", Branches: []string{"synthetic-lower", "synthetic-top"},
	}}}
	var out bytes.Buffer
	if err := writeStatus(&out, plan, Presentation{}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "├─") || strings.Contains(out.String(), "└─") {
		t.Errorf("a linear stack was rendered as a tree:\n%s", out.String())
	}
}
