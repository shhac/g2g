package push

import (
	"context"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/shape"
	"github.com/shhac/g2g/internal/stack"
	"github.com/shhac/g2g/internal/syncpoint"
)

func TestDifferenceRepairPreviewsTakingOnlyThroughTheTarget(t *testing.T) {
	for _, scope := range []shape.Scope{shape.ScopeBranch, shape.ScopePath, shape.ScopeStack, shape.ScopeTrunk} {
		t.Run(string(scope), func(t *testing.T) {
			branches := []string{"synthetic-$target"}
			publishing := map[string]Publication{branches[0]: {Standing: Unknown}}
			plan := Plan{
				Snapshot: stack.Snapshot{Target: branches[0], Scope: scope, Branches: branches},
				Remote:   "synthetic-remote", Publishing: publishing,
				Repair: blockedBy("synthetic-remote", branches, publishing, nil, nil),
			}
			if err := (Service{}).explainDifference(context.Background(), &plan); err != nil {
				t.Fatal(err)
			}
			want := "g2g pull --branch 'synthetic-$target'"
			if scope == shape.ScopeTrunk {
				want += " --scope trunk"
			}
			want += " --remote synthetic-remote"
			if plan.Repair.Ways[0].Command != want {
				t.Errorf("pull = %q, want %q", plan.Repair.Ways[0].Command, want)
			}
			take := plan.Repair.Ways[len(plan.Repair.Ways)-1]
			if take.Command != want+" --take published --through 'synthetic-$target'" || !strings.Contains(take.Effect, "discard") {
				t.Errorf("take hides its boundary or destructive effect: %+v", take)
			}
		})
	}
}

func TestReplacementAdvicePinsOnlyRejectedBranchesToObservedTips(t *testing.T) {
	note := blockedBy("synthetic-remote", []string{"synthetic-a", "synthetic-b", "synthetic-c"},
		map[string]Publication{"synthetic-a": {Standing: Behind}, "synthetic-b": {Standing: Current}, "synthetic-c": {Standing: Diverged}},
		map[string]string{"synthetic-a": strings.Repeat("a", 40), "synthetic-b": strings.Repeat("b", 40), "synthetic-c": strings.Repeat("c", 40)}, nil)
	want := "git push --atomic --force-with-lease=refs/heads/synthetic-a:" + strings.Repeat("a", 40) + " --force-with-lease=refs/heads/synthetic-c:" + strings.Repeat("c", 40) + " synthetic-remote synthetic-a synthetic-c"
	if got := note.Ways[1].Command; got != want {
		t.Fatalf("replacement command = %s, want %s", got, want)
	}
	if !strings.Contains(note.Ways[1].Effect, "dropping what the remote has") {
		t.Fatal("replacement advice hid its destructive effect")
	}
}

func TestMissingAgreementExplainedOnlyForRejectedUnsyncedBranches(t *testing.T) {
	for _, test := range []struct {
		name           string
		standing       Standing
		unsynced, want bool
	}{
		{"diverged without agreement", Diverged, true, true},
		{"behind without agreement", Behind, true, true},
		{"unavailable objects without agreement", Unknown, true, true},
		{"usable agreement", Diverged, false, false},
		{"safe unpublished work", Ahead, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			branches := []string{"synthetic-top"}
			publishing := map[string]Publication{branches[0]: {Standing: test.standing}}
			p := Plan{Snapshot: stack.Snapshot{Branches: branches}, Publishing: publishing, Repair: blockedBy("origin", branches, publishing, nil, nil)}
			p.explainMissingAgreement(map[string]syncpoint.Changes{branches[0]: {Unsynced: test.unsynced}})
			got := strings.Contains(p.Repair.Reason, "g2g has no record that this checkout and the remote agreed on synthetic-top")
			if got != test.want {
				t.Fatalf("reason=%s, want missing agreement=%t", p.Repair.Reason, test.want)
			}
		})
	}
}
