package sync

import (
	"context"
	"fmt"

	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/repair"
)

// boundaryFork preserves a collected boundary's structural range even though
// the replay deliberately excludes it. An incoming version can lose the old
// fork when its publisher rewrites it onto an older or different trunk.
func (s Service) boundaryFork(ctx context.Context, discovery graph.Discovery, above string, found *collected) (repair.Note, error) {
	if above == "" {
		return repair.Note{}, nil
	}
	for _, collection := range found.collections {
		if collection.Branch != above {
			continue
		}
		edge := discovery.Graph.Edges[above]
		if found.begins[above] != "" {
			return repair.Note{}, nil
		}
		if edge.ForkPoint != "" {
			retained, err := s.Git.IsAncestor(ctx, edge.ForkPoint, collection.To)
			if err != nil {
				return repair.Note{}, err
			}
			if retained {
				return repair.Note{}, nil
			}
		}
		trunk := discovery.Graph.IsTrunk(edge.Parent)
		if !trunk {
			built, err := s.Git.IsAncestor(ctx, edge.Parent, collection.To)
			if err != nil {
				return repair.Note{}, err
			}
			if !built {
				return repair.Note{Reason: fmt.Sprintf("the published version of %s contains neither its recorded fork nor a verified tip of feature parent %s, so its own replay range cannot be established", above, edge.Parent), Ways: []repair.Step{{Effect: "reconcile the parent boundary explicitly before following its published version"}}}, nil
			}
		}
		fork, err := s.Graph.ForkPoint(ctx, edge.Parent, collection.To, trunk)
		if err != nil {
			return repair.Note{}, err
		}
		if found.begins == nil {
			found.begins = map[string]string{}
		}
		found.begins[above] = fork
		return repair.Note{}, nil
	}
	return repair.Note{}, nil
}
