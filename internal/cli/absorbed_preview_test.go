package cli

import (
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/landed"
	"github.com/shhac/g2g/internal/repair"
	"github.com/shhac/g2g/internal/shape"
	"github.com/shhac/g2g/internal/stack"
	"github.com/shhac/g2g/internal/submit"
)

func TestSubmitAbsorbedAdviceKeepsRemoteAndSource(t *testing.T) {
	for _, source := range []stack.Source{stack.SourceG2G, stack.SourceGraphite, stack.SourceGitHub} {
		t.Run(string(source), func(t *testing.T) {
			snapshot := stack.Snapshot{Target: "synthetic-child", TargetSource: shape.TargetNamed, Base: "synthetic-main", Branches: []string{"synthetic-parent", "synthetic-child"}, Parents: map[string]string{"synthetic-parent": "synthetic-main", "synthetic-child": "synthetic-parent"}, Source: source, Scope: shape.ScopePath}
			plan := submit.Plan{Selected: snapshot, Snapshot: snapshot, Remote: "synthetic-upstream", Absorbed: map[string]landed.ParentAbsorption{"synthetic-child": {Local: landed.Absent, Known: landed.Present}}}
			view := submitView(plan, "", true, false, false)
			said := ""
			for _, note := range view.Notes {
				said += note.Text + "\n"
			}
			switch source {
			case stack.SourceG2G:
				if !strings.Contains(said, "g2g pull --branch synthetic-child --remote synthetic-upstream --above synthetic-parent") {
					t.Errorf("remote lost: %s", said)
				}
			case stack.SourceGraphite:
				if !strings.Contains(said, "gt sync") || strings.Contains(said, "g2g prune") {
					t.Errorf("wrong source cleanup: %s", said)
				}
			case stack.SourceGitHub:
				if strings.Contains(said, "g2g prune") || strings.Contains(said, "gt sync") {
					t.Errorf("invented cleanup command: %s", said)
				}
			}
		})
	}
}

func TestSubmitPathScopeRepairKeepsNamedTargetAndAcceptedScope(t *testing.T) {
	snapshot := stack.Snapshot{Target: "synthetic-child", TargetSource: shape.TargetNamed, Scope: shape.ScopePath}
	plan := (submitOptions{}).planned(submit.Plan{Snapshot: snapshot, Repair: repair.Note{Reason: "synthetic preserved base", Ways: []repair.Step{{Command: "g2g github retarget"}}}})
	if got := plan.Repair.Ways[0].Command; got != "g2g github retarget --branch synthetic-child --scope path" {
		t.Fatalf("path submit gave unusable retarget advice: %s", got)
	}
}
