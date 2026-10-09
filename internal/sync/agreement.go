package sync

import (
	"context"
	"maps"
	"slices"

	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/syncpoint"
)

// recordLevel notes the agreement a branch is already in when it is exactly
// level with the remote. That is a fact rather than a decision, like the fetch
// into g2g's own refs a preview already makes, and it is what gives a branch
// nobody has pulled or pushed through g2g a sync point to measure the next
// change from.
//
// Exactly level, and nothing looser. A branch ahead of the remote holds
// everything the remote has, which reads like agreement too -- and is also
// what a branch looks like when the remote dropped a commit it still has.
// Recording the remote's tip then would erase the one fact that tells the two
// apart, before anything had asked.
func (s Service) recordLevel(ctx context.Context, remote string, published map[string]string) {
	recorder, ok := s.Git.(syncpoint.ReadRecorder)
	if !ok {
		return
	}
	for _, branch := range slices.Sorted(maps.Keys(published)) {
		local, err := s.Git.Resolve(ctx, branch)
		if err != nil || local != published[branch] {
			continue
		}
		syncpoint.Record(ctx, recorder, "sync.sync_point", remote, branch, localgit.SyncPoint{Tip: local, Local: local, Command: "pull"})
	}
}

// ownTips is what the remote holds for each selected branch but the base. A
// trunk is never given a sync point: what pull does to one is decided by the
// rules for a rewritten trunk, and a drop there is not anybody's to publish.
func ownTips(published map[string]string, base string, branches []string) map[string]string {
	own := make(map[string]string, len(branches))
	for _, branch := range branches {
		if tip := published[branch]; tip != "" && branch != base {
			own[branch] = tip
		}
	}
	return own
}

// recordAgreed notes that every selected branch the remote holds has been
// reconciled with what it held when this was planned: taken, replayed onto,
// or left ahead of it as work to push. Only a pull that finished records it.
func (s Service) recordAgreed(ctx context.Context, plan Plan) {
	recorder, ok := s.Git.(syncpoint.ReadRecorder)
	if !ok {
		return
	}
	for _, branch := range slices.Sorted(maps.Keys(plan.Published)) {
		local, err := s.Git.Resolve(ctx, branch)
		if err != nil {
			continue
		}
		syncpoint.Record(ctx, recorder, "sync.sync_point", plan.Remote, branch, localgit.SyncPoint{
			Tip: plan.Published[branch], Local: local, Command: "pull", Dropped: syncpoint.DroppedOn(branch, plan.Drops),
		})
	}
}
