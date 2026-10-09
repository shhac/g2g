package push

import (
	"context"

	"github.com/shhac/g2g/internal/diagnostic"
	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/syncpoint"
)

// recordLevel notes the agreement a branch the remote already holds exactly is
// in, which is a fact about the two rather than a decision, so a preview may
// record it. It is what gives a branch nobody has pushed through g2g yet a
// sync point to measure the next change from.
func (s Service) recordLevel(ctx context.Context, remote string, branches []string, publishing map[string]Publication, tips map[string]string) {
	recorder, ok := s.Git.(syncpoint.ReadRecorder)
	if !ok {
		return
	}
	level := map[string]localgit.SyncPoint{}
	for _, branch := range branches {
		if publishing[branch].Standing == Current {
			level[branch] = localgit.SyncPoint{Tip: tips[branch], Local: tips[branch], Command: "push"}
		}
	}
	syncpoint.Record(ctx, recorder, "push.sync_point", remote, level)
}

// recordPushed notes that this clone and the remote now agree on every branch
// the push named, which is what lets a later pull or push tell a commit
// somebody dropped from one nobody had. The push has happened by now, so a
// recording that fails is a diagnostic rather than a failure: the next
// agreement records it, and until then the cautious reading applies.
func (s Service) recordPushed(ctx context.Context, plan Plan, leases []localgit.Lease) {
	recorder, ok := s.Git.(syncpoint.ReadRecorder)
	if !ok {
		return
	}
	branches := make([]string, 0, len(leases))
	for _, lease := range leases {
		branches = append(branches, lease.Branch)
	}
	tips := syncpoint.Locals(ctx, s.Git, branches)
	pushed := map[string]localgit.SyncPoint{}
	for _, branch := range branches {
		tip, resolved := tips[branch]
		if !resolved {
			diagnostic.Event(ctx, "push.sync_point", diagnostic.Field{Key: "branch", Value: branch}, diagnostic.Field{Key: "decision", Value: "not recorded"})
			continue
		}
		pushed[branch] = localgit.SyncPoint{Tip: tip, Local: tip, Command: "push", Dropped: syncpoint.DroppedOn(branch, plan.Drops, plan.Moves)}
	}
	syncpoint.Record(ctx, recorder, "push.sync_point", plan.Remote, pushed)
}
