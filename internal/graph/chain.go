package graph

import (
	"fmt"
	"strings"
)

// Chain orders the branches lying between a trunk and the target, trunk first.
//
// It is the one thing a whole-stack adoption needs that a single `track` does
// not, and it is pure: given the measured candidates it returns an order or an
// error, with no repository, no Git, and nothing to stub.
//
// This is not the guess `track` refuses to make. There, the user has said
// nothing and the tool would be choosing a parent on their behalf. Here the user
// has named the trunk, and everything between it and the target follows from
// ancestry — one assertion, then arithmetic. Where the arithmetic is ambiguous
// this refuses rather than picking, for exactly the reason `track` does.
func Chain(candidates []Candidate, trunk string) ([]string, error) {
	reachable := make([]Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		// A branch the target cannot reach is not on the way to anywhere.
		if candidate.Ancestor {
			reachable = append(reachable, candidate)
		}
	}

	end := -1
	for index, candidate := range reachable {
		if candidate.Branch == trunk {
			end = index
			break
		}
	}
	if end < 0 {
		return nil, fmt.Errorf("%q is not an ancestor of the selected branch", trunk)
	}

	// candidates arrive nearest first, so the chain up to the trunk is the
	// prefix, reversed.
	chain := make([]string, 0, end+1)
	for index := end; index >= 0; index-- {
		chain = append(chain, reachable[index].Branch)
	}
	if ambiguous := tied(reachable[:end+1]); ambiguous != "" {
		return nil, fmt.Errorf("%s are the same distance from the selected branch, so their order cannot be derived · record them with g2g track --parent instead", ambiguous)
	}
	return chain, nil
}

// tied names two branches an ancestry ordering cannot separate. Equal distance
// means neither is above the other, which is a fork rather than a stack.
func tied(candidates []Candidate) string {
	for index := 1; index < len(candidates); index++ {
		if candidates[index].Distance == candidates[index-1].Distance {
			return fmt.Sprintf("%q and %q", candidates[index-1].Branch, candidates[index].Branch)
		}
	}
	return ""
}

// TrunkFor picks the trunk a whole-stack adoption should stop at when the user
// has not named one.
//
// Only a branch the graph already treats as a root qualifies. Anything else
// would be this tool deciding where someone's stack begins, which is the
// decision it exists to not make.
func TrunkFor(candidates []Candidate, known []string) (string, error) {
	roots := make([]string, 0)
	for _, candidate := range candidates {
		if candidate.Ancestor && contains(known, candidate.Branch) {
			roots = append(roots, candidate.Branch)
		}
	}
	switch len(roots) {
	case 1:
		return roots[0], nil
	case 0:
		return "", fmt.Errorf("no recorded trunk is an ancestor of the selected branch · name one with --trunk <branch>")
	default:
		return "", fmt.Errorf("several recorded trunks are ancestors of the selected branch (%s) · choose one with --trunk <branch>", strings.Join(roots, ", "))
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// Attach picks the parent for a branch that hangs off an already-selected one.
//
// This is what makes a whole-stack adoption forest-shaped rather than linear.
// The chain gives the spine; every branch whose nearest ancestor is on that
// spine joins the tree, and so does every branch that then hangs off those.
//
// selected excludes the trunk deliberately. A branch that sits directly on the
// trunk is somebody else's stack that happens to share a base, and sweeping it
// in because it is technically a descendant would adopt half the repository.
func Attach(candidates []Candidate, selected []string) (string, bool, error) {
	for index, candidate := range candidates {
		if !candidate.Ancestor || !contains(selected, candidate.Branch) {
			continue
		}
		// A tie at the nearest position means two possible parents and no way
		// to choose, which is the guess this refuses to make.
		if index+1 < len(candidates) && candidates[index+1].Ancestor &&
			candidates[index+1].Distance == candidate.Distance && contains(selected, candidates[index+1].Branch) {
			return "", false, fmt.Errorf("%q and %q are the same distance below this branch, so its parent cannot be derived · record it with g2g track --parent", candidate.Branch, candidates[index+1].Branch)
		}
		return candidate.Branch, true, nil
	}
	return "", false, nil
}
