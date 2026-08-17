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

// A subtree selection that happens to be a chain must render flat, exactly as
// the graph views render one. Both commands used to compute depth themselves
// and only one suppressed the staircase, so the same shape rendered two ways
// depending on which command was asked.
func TestStatusRendersAChainFlatEvenWhenTheScopeCouldFork(t *testing.T) {
	plan := link.Plan{Discovery: stack.Discovery{Snapshot: stack.Snapshot{
		Target: "synthetic-a", Base: "synthetic-a", Scope: stack.ScopeSubtree, Source: stack.SourceGraphite,
		Branches: []string{"synthetic-b", "synthetic-c"},
		Parents:  map[string]string{"synthetic-b": "synthetic-a", "synthetic-c": "synthetic-b"},
	}}}
	var out bytes.Buffer
	if err := writeStatus(&out, plan, Presentation{}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "├─") || strings.Contains(out.String(), "└─") {
		t.Errorf("a chain was rendered as a staircase:\n%s", out.String())
	}
}

// treeDepths is the one place either command asks how deep a node sits, so its
// contract is worth stating directly rather than only through two renderers.
func TestTreeDepthsSuppressesAChainAndMeasuresAFork(t *testing.T) {
	chain := map[string]string{"synthetic-b": "synthetic-a", "synthetic-c": "synthetic-b"}
	if depths := treeDepths([]string{"synthetic-a", "synthetic-b", "synthetic-c"}, lookup(chain)); len(depths) != 0 {
		t.Errorf("treeDepths(chain) = %v, want none", depths)
	}

	fork := map[string]string{"synthetic-a": "synthetic-trunk", "synthetic-a-one": "synthetic-a", "synthetic-b": "synthetic-trunk"}
	depths := treeDepths([]string{"synthetic-trunk", "synthetic-a", "synthetic-a-one", "synthetic-b"}, lookup(fork))
	for branch, want := range map[string]int{"synthetic-trunk": 0, "synthetic-a": 1, "synthetic-a-one": 2, "synthetic-b": 1} {
		if depths[branch] != want {
			t.Errorf("depth of %q = %d, want %d", branch, depths[branch], want)
		}
	}
}

// A parent outside the selection does not count: the selection's own roots sit
// at depth zero rather than hanging from something not on screen.
func TestTreeDepthsIgnoresAParentOutsideTheSelection(t *testing.T) {
	edges := map[string]string{"synthetic-a": "synthetic-elsewhere", "synthetic-b": "synthetic-a", "synthetic-c": "synthetic-a"}
	depths := treeDepths([]string{"synthetic-a", "synthetic-b", "synthetic-c"}, lookup(edges))
	if depths["synthetic-a"] != 0 {
		t.Errorf("selection root depth = %d, want 0", depths["synthetic-a"])
	}
}

func lookup(edges map[string]string) func(string) (string, bool) {
	return func(branch string) (string, bool) {
		parent, ok := edges[branch]
		return parent, ok && parent != ""
	}
}

// A GitHub stack is linear and a selection need not be. Where they overlap the
// members are marked inside the tree, so the reader can see which part of the
// shape the stack number covers rather than being shown a shape and a number
// and left to work out the relationship.
func TestStatusMarksTheNativeStackInsideAForkedTree(t *testing.T) {
	plan := link.Plan{Discovery: stack.Discovery{Snapshot: stack.Snapshot{
		Target: "synthetic-trunk", Base: "synthetic-trunk", Scope: stack.ScopeStack, Source: stack.SourceGraphite,
		Branches: []string{"synthetic-a", "synthetic-a-one", "synthetic-b"},
		Parents: map[string]string{
			"synthetic-a":     "synthetic-trunk",
			"synthetic-a-one": "synthetic-a",
			"synthetic-b":     "synthetic-trunk",
		},
	}, PullRequests: []githubstack.PullRequest{
		{Head: "synthetic-a", Number: 11, State: "OPEN", Base: "synthetic-trunk", StackNumber: 7, StackSize: 2, StackPosition: 1},
		{Head: "synthetic-a-one", Number: 12, State: "OPEN", Base: "synthetic-a", StackNumber: 7, StackSize: 2, StackPosition: 2},
		{Head: "synthetic-b", Number: 13, State: "OPEN", Base: "synthetic-trunk"},
	}}}
	var out bytes.Buffer
	if err := writeStatus(&out, plan, Presentation{}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"stack #7 · 1/2", "stack #7 · 2/2", "├─", "└─"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in the forked status:\n%s", want, got)
		}
	}
	// The branch outside the native stack must not claim membership in it.
	for _, line := range strings.Split(got, "\n") {
		if strings.Contains(line, "synthetic-b") && strings.Contains(line, "stack #7") {
			t.Errorf("a branch outside the native stack was marked as in it: %q", line)
		}
	}
}

// A linear selection keeps the compact summary it always had: the whole path is
// the stack, so marking every node with the same number says nothing.
func TestStatusLeavesALinearSelectionUnmarked(t *testing.T) {
	plan := link.Plan{Discovery: stack.Discovery{Snapshot: stack.Snapshot{
		Target: "synthetic-top", Base: "synthetic-trunk", Scope: stack.ScopePath,
		Branches: []string{"synthetic-lower", "synthetic-top"},
	}, PullRequests: []githubstack.PullRequest{
		{Head: "synthetic-lower", Number: 11, State: "OPEN", StackNumber: 7, StackSize: 2, StackPosition: 1},
		{Head: "synthetic-top", Number: 12, State: "OPEN", StackNumber: 7, StackSize: 2, StackPosition: 2},
	}}}
	var out bytes.Buffer
	if err := writeStatus(&out, plan, Presentation{}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "stack #7 · 1/2") {
		t.Errorf("a linear selection repeated the stack number per node:\n%s", out.String())
	}
}
