package cli

import (
	"errors"
	"io"
	"reflect"
	"testing"

	"github.com/shhac/g2g/internal/comment"
	"github.com/shhac/g2g/internal/restack"
	"github.com/shhac/g2g/internal/retarget"
	"github.com/shhac/g2g/internal/stack"
	"github.com/spf13/cobra"
)

// Before discovery these commands can promise a footprint, but cannot yet
// name it. They must check this worktree rather than all worktrees.
func TestGitHubRepairsUseScopedIdleGuard(t *testing.T) {
	for _, command := range []string{"retarget", "comment"} {
		t.Run(command, func(t *testing.T) {
			stop := errors.New("synthetic restack here")
			idleChecks, hereChecks := 0, 0
			guard := guardFunc(func() error { return stop })
			guard.idle = func() error { idleChecks++; return stop }
			guard.here = func() error { hereChecks++; return stop }
			var cmd *cobra.Command
			if command == "retarget" {
				cmd = newRetarget(retarget.Service{}, stack.Completions{}, guard, Presentation{})
			} else {
				cmd = newComment(comment.Service{}, stack.Completions{}, guard, Presentation{})
			}
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			cmd.SetArgs([]string{"--apply"})
			if err := cmd.Execute(); !errors.Is(err, stop) {
				t.Fatalf("error = %v, want this worktree's refusal", err)
			}
			if hereChecks != 1 || idleChecks != 0 {
				t.Errorf("guard checks: here=%d, all=%d", hereChecks, idleChecks)
			}
		})
	}
}

func TestSnapshotTouchesReadsSelectedBranchesAndParents(t *testing.T) {
	snapshot := stack.Snapshot{Base: "synthetic-main", Branches: []string{"synthetic-head"}, Parents: map[string]string{"synthetic-head": "synthetic-parent"}}
	if got, want := snapshotTouches(snapshot), restack.Reads("synthetic-main", "synthetic-head", "synthetic-parent"); !reflect.DeepEqual(got, want) {
		t.Errorf("footprint = %#v, want %#v", got, want)
	}
}
