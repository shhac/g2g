// Package align keeps g2g's graph and Graphite's in step.
//
// Source resolution decided which source answers for a branch and left them
// free to disagree. This is the other half, in both directions: mirror makes
// Graphite agree with g2g, adopt takes what Graphite declares. Neither ever
// removes a branch from the g2g graph — alignment is not ownership transfer.
package align

import (
	"context"
	"fmt"

	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/graphite"
)

// Graphite is the whole Graphite surface alignment needs. It is an interface
// rather than the client so the decision matrix can be tested without spawning
// a process per case; one path per command still runs against a real adapter.
type Graphite interface {
	ReadForest(ctx context.Context) (graphite.Forest, error)
	Track(ctx context.Context, branch, parent string) error
	Untrack(ctx context.Context, branch string) error
}

// Service compares the two graphs and reconciles one into the other.
//
// The g2g side is three narrow dependencies rather than the whole
// graph.Service, because alignment never asks that service to do anything — it
// reads ancestry, reads and writes the store, and pins a fork point. Naming
// them is what makes that legible from the type instead of from every call
// site. restack embeds the whole service because it genuinely calls Discover.
type Service struct {
	Git      graph.Ancestry
	Store    graph.Store
	Refs     graph.Pinner
	Graphite Graphite
	// Configured reports whether this repository already uses Graphite.
	//
	// Alignment is the one place with a standing excuse to skip this check —
	// the user asked for Graphite to be written, which is consent. It is asked
	// anyway, and the reason is that a *preview* must not enrol anyone: reading
	// Graphite's forest runs its discovery command, which creates state in a
	// repository that has never used it. A repository with no Graphite also has
	// no trunk, so a mirror into it would be blocked for want of a root in any
	// case. Refusing first reaches the same answer without the side effect, and
	// keeps "no g2g command enrols a repository" true without exception.
	Configured func(ctx context.Context) (bool, error)

	// PullRequests, Forks and Trunks serve only github adopt,
	// and a service without them still mirrors and adopts from Graphite.
	// PullRequests reads what open pull request bases describe, which invokes
	// gh; that is why it is asked only when that record is named.
	PullRequests PullRequestReader
	// Forks says where a branch left its base, which is the fork point an
	// adoption from pull requests records.
	Forks Forks
	// Trunks is the evidence that a base the g2g graph does not record is the
	// repository's trunk, and so may become a root.
	Trunks graph.TrunkEvidence
}

// Ready reports a service with everything it needs.
//
// Unlike its siblings this is the registration rule alone, not also the guard
// below. One service backs two commands with different needs: adopt reads
// ancestry through the Git client and mirror does not, so the rule for "may
// these commands exist" is the union and the rule for "may this call proceed"
// stays per-path.
func (s Service) Ready() bool {
	return s.Store != nil && s.Graphite != nil && s.Git != nil
}

func (s Service) both(ctx context.Context) (graph.Graph, graphite.Forest, error) {
	if s.Store == nil || s.Graphite == nil {
		return graph.Graph{}, graphite.Forest{}, fmt.Errorf("alignment service is not fully configured")
	}
	if err := s.requireGraphite(ctx); err != nil {
		return graph.Graph{}, graphite.Forest{}, err
	}
	adopted, err := s.Store.Load(ctx)
	if err != nil {
		return graph.Graph{}, graphite.Forest{}, err
	}
	forest, err := s.Graphite.ReadForest(ctx)
	if err != nil {
		return graph.Graph{}, graphite.Forest{}, err
	}
	return adopted, forest, nil
}

// requireGraphite refuses before anything reads Graphite, so a preview in a
// repository that has never used it stays a preview.
func (s Service) requireGraphite(ctx context.Context) error {
	if s.Configured == nil {
		return nil
	}
	configured, err := s.Configured(ctx)
	if err != nil {
		return err
	}
	if !configured {
		return fmt.Errorf("this repository does not use Graphite · there is nothing to align, and asking Graphite would enrol it")
	}
	return nil
}
