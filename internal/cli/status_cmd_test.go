package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/shhac/gt2gh/internal/githubstack"
	"github.com/shhac/gt2gh/internal/link"
)

func TestStatusRendersCompactAlignedAndBlockedPath(t *testing.T) {
	plan := link.Plan{Target: "synthetic/top", Base: "main", Branches: []string{"synthetic/lower", "synthetic/top"}, PullRequests: []githubstack.PullRequest{{Head: "synthetic/lower", Number: 11, State: "OPEN"}, {Head: "synthetic/top", Number: 12, State: "OPEN"}}, Issues: []link.Issue{{Branch: "synthetic/top", Reason: "no open pull request"}}}
	var out bytes.Buffer
	if err := writeStatus(&out, plan, Presentation{}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Target: synthetic/top", "main (trunk)", "synthetic/lower (#11) [aligned]", "synthetic/top [blocked: no open pull request]", "Safe next action: repair"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in %q", want, out.String())
		}
	}
}

func TestStatusRendersOneNativeStackSummaryWithoutRepeatedBadges(t *testing.T) {
	plan := link.Plan{Target: "synthetic/top", Base: "main", Branches: []string{"synthetic/lower", "synthetic/top"}, PullRequests: []githubstack.PullRequest{{Head: "synthetic/lower", Number: 11, State: "OPEN", StackNumber: 17, StackSize: 2, StackPosition: 1}, {Head: "synthetic/top", Number: 12, State: "OPEN", StackNumber: 17, StackSize: 2, StackPosition: 2}}}
	var out bytes.Buffer
	if err := writeStatus(&out, plan, Presentation{}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "GitHub stack #17 · selected path 2/2 · aligned") {
		t.Errorf("missing aligned summary in %q", got)
	}
	if strings.Contains(got, "[stack #") {
		t.Errorf("healthy graph repeated native-stack badges: %q", got)
	}
}

func TestStatusMarksOnlyNativeStackExceptions(t *testing.T) {
	plan := link.Plan{Target: "synthetic/top", Base: "main", Branches: []string{"synthetic/lower", "synthetic/top"}, PullRequests: []githubstack.PullRequest{{Head: "synthetic/lower", Number: 11, State: "OPEN", StackNumber: 17, StackSize: 2, StackPosition: 1}, {Head: "synthetic/top", Number: 12, State: "OPEN"}}}
	var out bytes.Buffer
	if err := writeStatus(&out, plan, Presentation{}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"synthetic/top (#12) [aligned] [not linked]", "GitHub stack #17 · partial (1/2 linked)"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
}

func TestStatusMarksConflictingNativeStackMembership(t *testing.T) {
	plan := link.Plan{Target: "synthetic/top", Base: "main", Branches: []string{"synthetic/lower", "synthetic/top"}, PullRequests: []githubstack.PullRequest{{Head: "synthetic/lower", Number: 11, State: "OPEN", StackNumber: 17, StackSize: 2, StackPosition: 2}, {Head: "synthetic/top", Number: 12, State: "OPEN", StackNumber: 17, StackSize: 2, StackPosition: 1}}}
	var out bytes.Buffer
	if err := writeStatus(&out, plan, Presentation{}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"[stack #17, position 2]", "GitHub stack: conflicting membership"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
}
