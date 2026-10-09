package cli

import (
	"context"

	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/syncpoint"
)

// levelRecorder is what recording an adoption's agreement with the remote
// needs. The Git client is all of it; a fake that is not records nothing.
type levelRecorder interface {
	syncpoint.ReadRecorder
	KnownTips(ctx context.Context, remote string, branches []string) (map[string]string, error)
	Resolve(ctx context.Context, revision string) (string, error)
}

// recordAdopted notes the agreement each adopted branch is in with the remote
// as this clone last saw it, when it is exactly level with that.
//
// Adopting a stack somebody else published means agreeing with it at that
// moment. Recording it is what lets the first pull afterwards tell a commit
// they have since dropped from work of this clone's own, which is the whole of
// what a later pull needs to drop it here too rather than publish it again.
// Read from local refs alone, as status does; adoption asks nothing of the
// network. A recording that fails is a diagnostic, left for the next pull or
// push to make.
func recordAdopted(ctx context.Context, git graph.Ancestry, branches []string) {
	recorder, ok := git.(levelRecorder)
	if !ok || len(branches) == 0 {
		return
	}
	tips, err := recorder.KnownTips(ctx, localgit.DefaultRemote, branches)
	if err != nil {
		return
	}
	for _, branch := range branches {
		local, err := recorder.Resolve(ctx, branch)
		if err != nil || tips[branch] == "" || tips[branch] != local {
			continue
		}
		syncpoint.Record(ctx, recorder, "adopt.sync_point", localgit.DefaultRemote, branch, localgit.SyncPoint{Tip: local, Local: local, Command: "adopt"})
	}
}
