package githubstack

import (
	"context"
	"fmt"
	"maps"
	"sort"
	"strconv"

	"github.com/shhac/g2g/internal/diagnostic"
)

// Inspector is the whole of what forest building needs from a client: ask for
// some branches, get their pull requests back.
//
// It is declared here, next to its consumer, so the walk can be exercised
// against a table of answers rather than a process. What it stands for at
// runtime is Client.Inspect, which resolves every branch handed to it in one
// aliased GraphQL query — so a round costs one invocation whether it is asking
// about one branch or forty.
type Inspector interface {
	Inspect(ctx context.Context, branches []string) ([]PullRequest, error)
}

// Forest is the structure GitHub holds: which branch a pull request is based
// on, for as much of the graph as was followed.
//
// It is this package's own type, the way graphite.Forest is that package's.
// Converting to the shared traversal is the selector's job, which is what keeps
// the traversal from having to know that some of these branches are not on
// this machine.
type Forest struct {
	// Parents maps a branch to the branch its open pull request is based on.
	// A branch with no open pull request, or with more than one, gets no entry
	// at all: it contributes no edge, so the forest does not claim to place it.
	// A branch that appears only as somebody else's base is a root.
	Parents map[string]string
	// Absent are branches the forest places that are not local. They are real
	// structure — a stack published from another checkout still joins two local
	// subtrees through them — and they are not somewhere a command may act.
	Absent []string
	// Unfollowed are bases the walk stopped short of because it ran out of
	// rounds. They are reported rather than dropped: a tree that silently ends
	// early reads exactly like one that is genuinely finished.
	Unfollowed []string
}

// FollowRounds is how many times BuildForest will go back to GitHub.
//
// The first round asks about the local branches; each further round asks about
// the bases the previous one turned up that are not on this machine. Every
// round is one invocation covering every unknown it has, so the bound is on
// how *deep* a remote-only chain may be, never on how wide.
//
// Four is chosen against what a stack looks like rather than what an API
// permits: a chain of four consecutive branches that are all missing locally is
// already a stack somebody else is working on, and the honest thing at that
// point is to say where the following stopped.
const FollowRounds = 4

// BuildForest reads the structure GitHub holds, following bases that are not
// checked out here.
//
// Without the following, a base that is not a local branch makes its child read
// as a root, so two local subtrees joined through a branch published from
// somebody else's checkout appear as two unrelated trees — and appear that way
// silently, which is the part that misleads.
func BuildForest(ctx context.Context, inspector Inspector, local []string, rounds int) (Forest, error) {
	if inspector == nil {
		return Forest{}, fmt.Errorf("pull request inspector is not configured")
	}
	if rounds < 1 {
		rounds = 1
	}
	onThisMachine := make(map[string]bool, len(local))
	for _, branch := range local {
		onThisMachine[branch] = true
	}

	parents := map[string]string{}
	asked := map[string]bool{}
	frontier := append([]string(nil), local...)
	absent := map[string]bool{}

	for round := 0; round < rounds && len(frontier) != 0; round++ {
		for _, branch := range frontier {
			asked[branch] = true
		}
		prs, err := inspector.Inspect(ctx, frontier)
		if err != nil {
			return Forest{}, err
		}
		roundParents, unknown := mergeInspection(prs, onThisMachine, asked)
		maps.Copy(parents, roundParents)
		maps.Copy(absent, unknown)
		diagnostic.Event(ctx, "github.forest_round",
			diagnostic.Field{Key: "round", Value: strconv.Itoa(round + 1)},
			diagnostic.Field{Key: "asked", Value: strconv.Itoa(len(frontier))},
			diagnostic.Field{Key: "unknown", Value: strconv.Itoa(len(unknown))},
		)
		frontier = sorted(unknown)
	}

	forest := Forest{Parents: parents, Absent: sorted(absent), Unfollowed: frontier}
	// A branch that was named as a base but never asked about has no entry, so
	// it reads as a root. That is right for a trunk and wrong for a chain the
	// rounds ran out on, which is why the latter is reported.
	return forest, nil
}

// mergeInspection turns one complete GitHub inspection round into edges and
// the next remote-only frontier. It is deliberately independent of the loop:
// round ordering and API calls belong to BuildForest, while interpreting one
// response has no effects and can be reasoned about on its own.
func mergeInspection(prs []PullRequest, onThisMachine, asked map[string]bool) (map[string]string, map[string]bool) {
	parents := make(map[string]string)
	unknown := make(map[string]bool)
	for branch, resolution := range ResolveHeads(prs) {
		// One open pull request is an edge. None contributes nothing, and more
		// than one is ambiguity this source does not interpret.
		if resolution.Open == nil {
			continue
		}
		base := resolution.Open.Base
		parents[branch] = base
		if !onThisMachine[base] && !asked[base] {
			unknown[base] = true
		}
	}
	return parents, unknown
}

func sorted(set map[string]bool) []string {
	if len(set) == 0 {
		return nil
	}
	values := make([]string, 0, len(set))
	for value := range set {
		values = append(values, value)
	}
	sort.Strings(values)
	return values
}
