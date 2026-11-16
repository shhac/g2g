package stack

import "testing"

func TestTrunkSurvivesANarrowSelection(t *testing.T) {
	for _, test := range []struct {
		name     string
		snapshot Snapshot
		want     string
	}{
		{name: "full stack", snapshot: Snapshot{Base: "synthetic-main", Ancestry: []string{"synthetic-main", "synthetic-parent", "synthetic-head"}}, want: "synthetic-main"},
		{name: "branch on feature", snapshot: Snapshot{Base: "synthetic-parent", Ancestry: []string{"synthetic-main", "synthetic-parent", "synthetic-head"}}, want: "synthetic-main"},
		{name: "without ancestry", snapshot: Snapshot{Base: "synthetic-main"}, want: "synthetic-main"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := test.snapshot.Trunk(); got != test.want {
				t.Errorf("Trunk = %q, want %q", got, test.want)
			}
		})
	}
}
