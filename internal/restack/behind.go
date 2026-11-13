package restack

import (
	"context"

	"github.com/shhac/g2g/internal/graph"
)

// trunksBehind is each trunk a step replays onto whose remote's copy, as this
// checkout last saw it, is ahead of it: only ahead, never diverged, by the
// rule graph.UpstreamAhead keeps for every caller.
//
// Local refs only. A restack never fetches, and the question is whether what
// is already here says the replay is onto a stale trunk. It only ever adds a
// note, so a question Git cannot answer says nothing rather than failing the
// plan.
func (s Service) trunksBehind(ctx context.Context, discovery graph.Discovery, steps []Step) []TrunkBehind {
	found := []TrunkBehind{}
	asked := map[string]bool{}
	for _, step := range steps {
		trunk := step.Parent
		if asked[trunk] || discovery.Graph.Tracked(trunk) {
			continue
		}
		asked[trunk] = true
		known, err := graph.UpstreamAhead(ctx, s.Git, trunk)
		if err != nil || known == "" || known == step.Base {
			continue
		}
		_, commits, err := s.Git.Divergence(ctx, step.Base, known)
		if err != nil {
			continue
		}
		found = append(found, TrunkBehind{Trunk: trunk, Commits: commits})
	}
	return found
}
