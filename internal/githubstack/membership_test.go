package githubstack

import "testing"

func TestAssessMembership(t *testing.T) {
	branches := []string{"synthetic/lower", "synthetic/upper"}
	for _, test := range []struct {
		name string
		prs  []PullRequest
		want MembershipState
	}{
		{name: "unlinked", prs: []PullRequest{{Head: branches[0]}, {Head: branches[1]}}, want: Unlinked},
		{name: "aligned", prs: []PullRequest{
			{Head: branches[0], StackNumber: 17, StackSize: 2, StackPosition: 1},
			{Head: branches[1], StackNumber: 17, StackSize: 2, StackPosition: 2},
		}, want: Aligned},
		{name: "partial", prs: []PullRequest{
			{Head: branches[0], StackNumber: 17, StackSize: 2, StackPosition: 1},
			{Head: branches[1]},
		}, want: Partial},
		{name: "different stacks conflict", prs: []PullRequest{
			{Head: branches[0], StackNumber: 17, StackSize: 2, StackPosition: 1},
			{Head: branches[1], StackNumber: 18, StackSize: 2, StackPosition: 2},
		}, want: Conflicting},
		{name: "unexpected position conflicts", prs: []PullRequest{
			{Head: branches[0], StackNumber: 17, StackSize: 2, StackPosition: 2},
			{Head: branches[1], StackNumber: 17, StackSize: 2, StackPosition: 1},
		}, want: Conflicting},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := AssessMembership(branches, test.prs)
			if got.State != test.want {
				t.Errorf("State = %q, want %q", got.State, test.want)
			}
		})
	}
}

func TestGroupByHeadPreservesDuplicatesAndByHeadUsesLast(t *testing.T) {
	prs := []PullRequest{{Head: "synthetic/branch", Number: 1}, {Head: "synthetic/branch", Number: 2}}
	if got := len(GroupByHead(prs)["synthetic/branch"]); got != 2 {
		t.Errorf("group length = %d, want 2", got)
	}
	if got := ByHead(prs)["synthetic/branch"].Number; got != 2 {
		t.Errorf("last number = %d, want 2", got)
	}
}
