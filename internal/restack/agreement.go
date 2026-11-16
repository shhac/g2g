package restack

import (
	"context"
	"errors"

	"github.com/shhac/g2g/internal/diagnostic"
	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/syncpoint"
)

// recordLevel preserves agreement before rewriting a plain-pushed branch.
// Exactly equal tips are evidence even from an earlier fetch: anything the
// remote gained afterwards stays outside this agreement and protected by push.
// An ahead branch is not evidence: it can carry work the remote dropped.
func (s Service) recordLevel(ctx context.Context, plan Plan) {
	git, ok := s.Git.(interface {
		knownTips
		syncpoint.ReadRecorder
	})
	if !ok || plan.Absorb || len(plan.Steps) == 0 {
		return
	}
	branches := make([]string, 0, len(plan.Steps))
	for _, step := range plan.Steps {
		branches = append(branches, step.Branch)
	}
	tips, err := git.KnownTips(ctx, localgit.DefaultRemote, branches)
	if err != nil {
		// A repository with no origin is ordinary; restack remains offline.
		if !errors.Is(err, localgit.ErrNoSuchRemote) {
			diagnostic.Event(ctx, "restack.sync_point", diagnostic.Field{Key: "decision", Value: "not recorded"})
		}
		return
	}
	locals := make(map[string]string, len(plan.Steps))
	for _, step := range plan.Steps {
		// Use the observed local Tip, never a pending incoming Head.
		locals[step.Branch] = step.Tip
	}
	level := syncpoint.Level("restack", locals, tips)
	syncpoint.Record(ctx, git, "restack.sync_point", localgit.DefaultRemote, level)
}
