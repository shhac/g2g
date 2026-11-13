package land

import (
	"slices"
	"testing"

	"github.com/shhac/g2g/internal/stack"
	"github.com/shhac/g2g/internal/worktree"
)

// What a descent makes of each branch another worktree holds, by what the
// branch is to it: a survivor may be left, the trunk may be left only for the
// path that does not move it, a branch it lands may not be left, and one it
// deletes may not be followed.
func TestSortHeldByWhatEachBranchIsToTheDescent(t *testing.T) {
	plan := Plan{Trunk: "main", Options: Options{DeleteLocal: true}}
	plan.Discovery = stack.Discovery{Snapshot: stack.Snapshot{Branches: []string{"synthetic-landing"}}}
	holder := func(branch string) worktree.Holder {
		return worktree.Holder{Branch: branch, Path: "/synthetic/" + branch}
	}
	sorted := plan.sortHeld(worktree.Split{
		Leave:  []worktree.Holder{holder("synthetic-above"), holder("main"), holder("synthetic-landing")},
		Follow: []worktree.Holder{holder("synthetic-landing"), holder("synthetic-other")},
		Refuse: []worktree.Holder{holder("synthetic-refused")},
	})
	branches := func(holders []worktree.Holder) []string {
		names := []string{}
		for _, holder := range holders {
			names = append(names, holder.Branch)
		}
		return names
	}
	for name, test := range map[string]struct{ got, want []string }{
		"left":    {branches(sorted.left), []string{"synthetic-above"}},
		"follow":  {branches(sorted.follow), []string{"synthetic-other"}},
		"blocked": {branches(sorted.blocked), []string{"synthetic-refused", "synthetic-landing", "synthetic-landing"}},
		"refused": {branches(sorted.refused()), []string{"synthetic-refused", "synthetic-landing", "synthetic-landing", "main"}},
	} {
		if !slices.Equal(test.got, test.want) {
			t.Errorf("%s = %v, want %v", name, test.got, test.want)
		}
	}
	if sorted.trunkLeft == nil || sorted.trunkLeft.Branch != "main" {
		t.Errorf("trunkLeft = %v, want main", sorted.trunkLeft)
	}
}
