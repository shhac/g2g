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
	plan := link.Plan{Discovery: stack.Discovery{Snapshot: stack.Snapshot{Target: "synthetic/top", Base: "main", Branches: []string{"synthetic/lower", "synthetic/top"}}, PullRequests: []githubstack.PullRequest{{Head: "synthetic/lower", Number: 11, State: "OPEN"}, {Head: "synthetic/top", Number: 12, State: "OPEN"}}}, Issues: []link.Issue{{Branch: "synthetic/top", Kind: link.IssueMissing, Reason: "no open PR"}}}
	var out bytes.Buffer
	if err := writeStatus(&out, plan, Presentation{}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Target  synthetic/top", "\u25cb main", "#11", "base" + markYes, "pr" + markNo + " none open", "Safe next action", "g2g submit   opens a new PR", "  synthetic/top"} {
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

// The advice used to be one prose sentence naming every branch, which for a
// real stack wrapped mid-name across two terminal lines and had to be read
// twice to find out which branches it meant. Each branch gets its own line
// now, and only the exception carries a note: the headline already says what
// happens to the ordinary ones.
func TestStatusAdvicePutsEachBranchOnItsOwnLine(t *testing.T) {
	plan := link.Plan{
		Discovery: stack.Discovery{Snapshot: stack.Snapshot{
			Target:   "synthetic/top",
			Base:     "main",
			Branches: []string{"synthetic/lower", "synthetic/middle", "synthetic/top"},
		}},
		Issues: []link.Issue{
			{Branch: "synthetic/lower", Kind: link.IssueMissing, Reason: "no open PR"},
			{Branch: "synthetic/middle", Kind: link.IssueMissing, Reason: "no open PR"},
			{Branch: "synthetic/top", Kind: link.IssueClosed, Number: 19891, Reason: "PR closed"},
		},
	}
	var out bytes.Buffer
	if err := writeStatus(&out, plan, Presentation{}); err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(out.String(), "\n")
	index := func(want string) int {
		for at, line := range lines {
			if strings.TrimSpace(line) == want {
				return at
			}
		}
		t.Fatalf("no line %q in:\n%s", want, out.String())
		return -1
	}
	headline := index("g2g submit   opens a new PR for each of these 3 branches")
	// Every branch on its own line, in selection order, under the headline.
	for offset, want := range []string{"synthetic/lower", "synthetic/middle", "synthetic/top  · #19891 was closed"} {
		if at := index(want); at != headline+1+offset {
			t.Errorf("line %q is at %d, want %d (one per branch, in order)", want, at, headline+1+offset)
		}
	}
	// The ordinary case carries no note; repeating "no open PR" on every line
	// is what the headline exists to avoid.
	if strings.Contains(out.String(), "synthetic/lower  ·") {
		t.Errorf("the ordinary case was annotated anyway:\n%s", out.String())
	}
}

// A machine reads one field, and a porcelain record is one tab-separated row,
// so the sentence must stay a sentence however the human form is laid out.
func TestStatusAdviceStaysOneLineForMachines(t *testing.T) {
	plan := link.Plan{
		Discovery: stack.Discovery{Snapshot: stack.Snapshot{Target: "synthetic/top", Base: "main", Branches: []string{"synthetic/top"}}},
		Issues:    []link.Issue{{Branch: "synthetic/top", Kind: link.IssueMissing, Reason: "no open PR"}},
	}
	var out bytes.Buffer
	if err := writeStatus(&out, plan, Presentation{Format: formatPorcelain}); err != nil {
		t.Fatal(err)
	}

	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if !strings.HasPrefix(line, "blocked\t") {
			continue
		}
		if strings.Count(line, "\t") != 1 {
			t.Errorf("the blocked record is not one field: %q", line)
		}
		return
	}
	t.Errorf("no blocked record in:\n%s", out.String())
}

// A branch whose pull request is missing its latest work read as plain
// "aligned", because alignment is about the base. Base and head are separate
// marks for that reason, and the head one appears only where there is
// something to say — a current pull request stays unannotated so the stale
// ones stand out.
func TestStatusSaysWhenAPullRequestIsMissingTheBranchesWork(t *testing.T) {
	for _, test := range []struct {
		name     string
		currency link.Currency
		want     string
	}{
		{name: "current", currency: link.Currency{}, want: "base" + markYes},
		{name: "unpushed", currency: link.Currency{Unpushed: 2}, want: "head" + markNo + " 2 commits not pushed"},
		{name: "one unpushed reads singular", currency: link.Currency{Unpushed: 1}, want: "head" + markNo + " 1 commit not pushed"},
		{name: "diverged", currency: link.Currency{Diverged: true}, want: "head" + markNo + " PR is on a commit this branch does not have"},
		{name: "diverged with local work", currency: link.Currency{Diverged: true, Unpushed: 3}, want: "3 commits here are not on it"},
		{name: "one diverged commit agrees with its verb", currency: link.Currency{Diverged: true, Unpushed: 1}, want: "1 commit here is not on it"},
		// A restacked stack is in this state, and nothing is missing from its
		// pull requests. It used to report as a divergence, with every commit
		// the trunk had gained counted as work of the reader's own.
		{name: "replayed since it was pushed", currency: link.Currency{Rewritten: true}, want: "head" + markNo + " PR is on an older version of this branch"},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan := link.Plan{
				Discovery: stack.Discovery{
					Snapshot:     stack.Snapshot{Target: "synthetic-top", Base: "main", Branches: []string{"synthetic-top"}},
					PullRequests: []githubstack.PullRequest{{Head: "synthetic-top", Number: 12, State: "OPEN"}},
				},
				Currency: map[string]link.Currency{"synthetic-top": test.currency},
			}
			var out bytes.Buffer
			if err := writeStatus(&out, plan, Presentation{}); err != nil {
				t.Fatal(err)
			}

			if !strings.Contains(out.String(), test.want) {
				t.Errorf("output does not contain %q:\n%s", test.want, out.String())
			}
			if test.name == "current" && strings.Contains(out.String(), "head") {
				t.Errorf("a current pull request was annotated anyway:\n%s", out.String())
			}
		})
	}
}

// The complaint this answers: one line said "aligned" and then described a
// divergence, and a single colour had to choose between them — so the good
// news and the bad news were the same colour, and the first word was the good
// one. Two marks, two colours, and the reader can scan the column for ✗.
func TestBaseAndHeadAreSaidSeparatelyAndColouredSeparately(t *testing.T) {
	plan := link.Plan{
		Discovery: stack.Discovery{
			Snapshot:     stack.Snapshot{Target: "synthetic-top", Base: "main", Branches: []string{"synthetic-top"}},
			PullRequests: []githubstack.PullRequest{{Head: "synthetic-top", Number: 12, State: "OPEN"}},
		},
		Currency: map[string]link.Currency{"synthetic-top": {Diverged: true}},
	}

	var out bytes.Buffer
	if err := writeStatus(&out, plan, Presentation{Color: true}); err != nil {
		t.Fatal(err)
	}

	if want := ansiAligned + "base" + markYes + ansiReset; !strings.Contains(out.String(), want) {
		t.Errorf("the base mark is not drawn as good news:\n%q", out.String())
	}
	if want := ansiProblem + "head" + markNo; !strings.Contains(out.String(), want) {
		t.Errorf("the head mark is not drawn as bad news:\n%q", out.String())
	}
}

// Which native stack a branch belongs to used to replace the annotation rather
// than join it, so a diverged head went unsaid on exactly the branches
// something else was also wrong with.
func TestAMembershipMarkerDoesNotSwallowTheHeadMark(t *testing.T) {
	plan := link.Plan{
		Discovery: stack.Discovery{
			Snapshot: stack.Snapshot{Target: "synthetic-top", Base: "main", Branches: []string{"synthetic-lower", "synthetic-top"}},
			PullRequests: []githubstack.PullRequest{
				{Head: "synthetic-lower", Number: 11, State: "OPEN", StackNumber: 3, StackPosition: 1},
				{Head: "synthetic-top", Number: 12, State: "OPEN"},
			},
		},
		Currency: map[string]link.Currency{"synthetic-lower": {Diverged: true}},
	}

	var out bytes.Buffer
	if err := writeStatus(&out, plan, Presentation{}); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out.String(), "head"+markNo) {
		t.Errorf("the head mark was swallowed by the membership marker:\n%s", out.String())
	}
}

// A merged pull request did what it was for. It used to be grouped with a
// missing or closed one, which put a red ✗ on the successful outcome and told
// the reader something had gone wrong with it. What is left over is a branch
// that no longer belongs in the stack, which the advice block says.
func TestAMergedPullRequestIsNotMarkedAsAFault(t *testing.T) {
	for _, test := range []struct {
		name  string
		issue link.Issue
		want  string
	}{
		{name: "merged", issue: link.Issue{Branch: "synthetic-top", Kind: link.IssueMerged, Number: 7, Reason: "PR merged"}, want: "pr" + markYes + " merged as #7"},
		{name: "closed", issue: link.Issue{Branch: "synthetic-top", Kind: link.IssueClosed, Number: 7, Reason: "PR closed"}, want: "pr" + markNo + " closed without merging"},
		{name: "missing", issue: link.Issue{Branch: "synthetic-top", Kind: link.IssueMissing, Reason: "no open PR"}, want: "pr" + markNo + " none open"},
		{name: "ambiguous", issue: link.Issue{Branch: "synthetic-top", Kind: link.IssueAmbiguous, Reason: "2 open PRs"}, want: "pr" + markNo + " 2 open PRs"},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan := link.Plan{
				Discovery: stack.Discovery{Snapshot: stack.Snapshot{Target: "synthetic-top", Base: "main", Branches: []string{"synthetic-top"}}},
				Issues:    []link.Issue{test.issue},
			}
			var out bytes.Buffer
			if err := writeStatus(&out, plan, Presentation{}); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), test.want) {
				t.Errorf("output does not contain %q:\n%s", test.want, out.String())
			}
		})
	}
}

// The colour is the half a reader takes in without reading. A landed pull
// request drawn in the same red as a missing one says the opposite of what
// happened, and graph has always called an already-landed branch neutral.
func TestALandedPullRequestIsNotDrawnAsAlarming(t *testing.T) {
	plan := link.Plan{
		Discovery: stack.Discovery{Snapshot: stack.Snapshot{Target: "synthetic-top", Base: "main", Branches: []string{"synthetic-top"}}},
		Issues:    []link.Issue{{Branch: "synthetic-top", Kind: link.IssueMerged, Number: 7, Reason: "PR merged"}},
	}

	var out bytes.Buffer
	if err := writeStatus(&out, plan, Presentation{Color: true}); err != nil {
		t.Fatal(err)
	}

	if strings.Contains(out.String(), ansiProblem+"pr") {
		t.Errorf("a merged pull request was drawn as a problem:\n%q", out.String())
	}
	if want := ansiSubdued + "pr" + markYes; !strings.Contains(out.String(), want) {
		t.Errorf("a merged pull request is not drawn as the ordinary outcome it is:\n%q", out.String())
	}
}

// A branch whose work is already below it used to read as one with no pull
// request, and the advice for that is to open one — for a change that is
// already in the trunk. What it needs is forgetting, and which command does
// that depends on which record answered.
func TestALandedBranchIsNotOfferedSubmit(t *testing.T) {
	for _, test := range []struct {
		name   string
		source stack.Source
		want   string
	}{
		{name: "g2g's own graph is prunable", source: stack.SourceG2G, want: "g2g prune"},
		{name: "graphite restacks around it", source: stack.SourceGraphite, want: "gt sync"},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan := link.Plan{
				Discovery: stack.Discovery{Snapshot: stack.Snapshot{
					Target: "synthetic-top", Base: "main", Branches: []string{"synthetic-top"}, Source: test.source,
				}},
				Issues: []link.Issue{{Branch: "synthetic-top", Kind: link.IssueLanded, Reason: "landed in main"}},
			}

			var out bytes.Buffer
			if err := writeStatus(&out, plan, Presentation{}); err != nil {
				t.Fatal(err)
			}

			if !strings.Contains(out.String(), "landed in main") {
				t.Errorf("the branch is not reported as landed:\n%s", out.String())
			}
			if !strings.Contains(out.String(), test.want) {
				t.Errorf("output does not offer %q:\n%s", test.want, out.String())
			}
			if strings.Contains(out.String(), "g2g submit") {
				t.Errorf("submit was offered for work already in the trunk:\n%s", out.String())
			}
			if strings.Contains(out.String(), "pr"+markNo) {
				t.Errorf("a landed branch was marked as a pull request fault:\n%s", out.String())
			}
		})
	}
}
