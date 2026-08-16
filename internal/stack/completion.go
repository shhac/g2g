package stack

import (
	"context"
	"sort"
	"strings"

	"github.com/shhac/gt2gh/internal/diagnostic"
	"github.com/shhac/gt2gh/internal/graphite"
)

// CompletionGraphite adds the read-only lookups shell completion needs on top
// of path discovery.
type CompletionGraphite interface {
	Discover(context.Context, string) (graphite.Stack, error)
	TrackedBranches(context.Context) ([]string, error)
}

// Candidates offers shell-completion values from one structure source.
//
// It is separate from Selector because completing a flag is a question, and a
// question must not change the repository or cost what selecting costs. A
// source implementing this is promising both, which is why each implementation
// carries whatever gate that promise needs rather than leaving it to callers.
type Candidates interface {
	// Branches names the branches this source describes.
	Branches(ctx context.Context) ([]string, error)
	// Trunks names the bases this source would accept for target.
	Trunks(ctx context.Context, target string) ([]string, error)
}

// Completions supplies deterministic, checkout-free shell-completion
// candidates from every configured structure source. It lives beside discovery
// rather than inside any one command's package, so a command does not have to
// depend on another just to complete a --branch flag.
type Completions struct {
	Git Git
	// Sources are consulted in the same precedence order resolution uses. The
	// results are merged rather than taken from the first that answers: which
	// source owns a branch is decided per branch, so restricting completion to
	// one of them would hide branches the command would happily accept.
	Sources []Candidates
}

func (c Completions) configured() bool { return c.Git != nil && len(c.Sources) != 0 }

// Branches returns the locally-present branches some source describes, sorted
// for deterministic completion. It neither inspects nor changes checkout state.
func (c Completions) Branches(ctx context.Context, prefix string) ([]string, error) {
	return c.gather(ctx, prefix, func(source Candidates) ([]string, error) {
		return source.Branches(ctx)
	})
}

// Trunks returns the bases some source would accept for the selected target,
// derived without a checkout.
func (c Completions) Trunks(ctx context.Context, target, prefix string) ([]string, error) {
	if !c.configured() {
		return nil, errNotConfigured
	}
	if target == "" {
		current, err := c.Git.CurrentBranch(ctx)
		if err != nil {
			return nil, err
		}
		target = current
	}
	return c.gather(ctx, prefix, func(source Candidates) ([]string, error) {
		return source.Trunks(ctx, target)
	})
}

// gather merges every source's answer, keeping only local branches that match
// the prefix.
//
// A source that cannot answer is skipped rather than failing the completion.
// One unusable source — Graphite installed but broken, say — should cost its
// own candidates and nothing else, and the user learns what is wrong from the
// command they are completing, which can say it properly.
func (c Completions) gather(ctx context.Context, prefix string, from func(Candidates) ([]string, error)) ([]string, error) {
	if !c.configured() {
		return nil, errNotConfigured
	}
	local, err := c.Git.LocalBranches(ctx)
	if err != nil {
		return nil, err
	}
	available := branchSet(local)
	seen := map[string]bool{}
	matches := make([]string, 0)
	for _, source := range c.Sources {
		candidates, err := from(source)
		if err != nil {
			diagnostic.Event(ctx, "completion.source_skipped", diagnostic.Field{Key: "reason", Value: err.Error()})
			continue
		}
		for _, candidate := range candidates {
			if seen[candidate] || !available[candidate] || !strings.HasPrefix(candidate, prefix) {
				continue
			}
			seen[candidate] = true
			matches = append(matches, candidate)
		}
	}
	sort.Strings(matches)
	return matches, nil
}

// GraphiteCandidates completes from what Graphite tracks.
//
// Configured is the same gate GraphiteSelector applies, for the same reason:
// Graphite's discovery command creates state in a repository that has never
// used it, so completing a flag would enrol a repository whose owner chose not
// to. Pressing tab must not be how that happens.
type GraphiteCandidates struct {
	Graphite   CompletionGraphite
	Configured func(ctx context.Context) (bool, error)
}

func (c GraphiteCandidates) Branches(ctx context.Context) ([]string, error) {
	if !c.usable(ctx) {
		return nil, nil
	}
	return c.Graphite.TrackedBranches(ctx)
}

func (c GraphiteCandidates) Trunks(ctx context.Context, target string) ([]string, error) {
	if !c.usable(ctx) {
		return nil, nil
	}
	declared, err := c.Graphite.Discover(ctx, target)
	if err != nil {
		return nil, err
	}
	return declared.Trunks, nil
}

func (c GraphiteCandidates) usable(ctx context.Context) bool {
	if c.Graphite == nil {
		return false
	}
	if c.Configured == nil {
		return true
	}
	configured, err := c.Configured(ctx)
	return err == nil && configured
}
