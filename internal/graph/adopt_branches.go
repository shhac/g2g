package graph

import (
	"context"
	"maps"
	"slices"
	"sort"

	"github.com/shhac/g2g/internal/parallel"
)

// branches grows the spine into the tree the user is working in: every local
// branch whose nearest ancestor is already selected joins, and then every
// branch hanging off those, until nothing new attaches.
//
// A branch sitting directly on the trunk never joins. It is a separate stack
// that happens to share a base, and sweeping it in because it is technically a
// descendant would adopt half the repository.
func (s Service) branches(ctx context.Context, spine []string, trunk string, adopted Graph) ([]Adoption, error) {
	local, err := s.Git.LocalBranches(ctx)
	if err != nil {
		return nil, err
	}
	selected := make([]string, 0, len(spine))
	for _, branch := range spine {
		if branch != trunk {
			selected = append(selected, branch)
		}
	}

	// Membership, ordering and the per-branch candidate list are three separate
	// jobs, and the loop previously did all three the same way: by rescanning.
	// selected stays the ordered slice attach reads; chosen answers "already
	// taken" without walking it.
	chosen := map[string]bool{trunk: true}
	for _, branch := range selected {
		chosen[branch] = true
	}
	// adopted does not change while this runs, so its roots do not either, and
	// a branch's candidates depend only on the branch and those roots. Asking
	// Git again on every pass was work proportional to passes rather than to
	// branches.
	roots := s.knownRoots(adopted)
	below, err := s.mergedBelow(ctx, trunk, selected)
	if err != nil {
		return nil, err
	}

	candidatesFor, err := s.warmCandidates(ctx, local, roots, chosen, below)
	if err != nil {
		return nil, err
	}
	return attachBranches(local, selected, chosen, candidatesFor)
}

// warmCandidates acquires each branch's ancestry answer once, with independent
// reads writing to distinct elements before the map is assembled.
func (s Service) warmCandidates(ctx context.Context, local, roots []string, chosen, below map[string]bool) (map[string][]Candidate, error) {
	candidatesFor := make(map[string][]Candidate, len(local))
	// Every branch the loop will consult, asked together. It consults all of
	// them on its first pass anyway, so this is the same work; asking for it
	// at once is what stops a repository's worth of independent process spawns
	// happening one at a time. Each answer is written to its own element and
	// folded into the map afterwards, so nothing here needs a lock.
	//
	// related, not Candidates: attach acts only on candidates that are
	// genuinely ancestors, and Candidates' fallback can only produce branches
	// that are not -- the set related measures already contains every ancestor
	// there is. Asking for the fallback measured every local branch against
	// every other one and discarded all of it.
	warmed := make([][]Candidate, len(local))
	if err := parallel.Each(ctx, local, func(ctx context.Context, index int, branch string) error {
		if chosen[branch] || below[branch] {
			return nil
		}
		candidates, err := relatedWithin(ctx, s.Git, branch, roots, local, below)
		if err != nil {
			return err
		}
		warmed[index] = candidates
		return nil
	}); err != nil {
		return nil, err
	}
	for index, candidates := range warmed {
		if candidates != nil {
			candidatesFor[local[index]] = candidates
		}
	}

	return candidatesFor, nil
}

// attachBranches grows the selection from candidate answers without reading
// Git. The working membership and order belong to this walk alone.
func attachBranches(local, selected []string, chosen map[string]bool, candidatesFor map[string][]Candidate) ([]Adoption, error) {
	selected = slices.Clone(selected)
	chosen = maps.Clone(chosen)
	edges := make([]Adoption, 0)
	for grew := true; grew; {
		grew = false
		for _, branch := range local {
			if chosen[branch] {
				continue
			}
			parent, attached, err := attach(branch, candidatesFor[branch], selected)
			if err != nil {
				return nil, err
			}
			if !attached {
				continue
			}
			edges = append(edges, Adoption{Branch: branch, Parent: parent})
			selected = append(selected, branch)
			chosen[branch] = true
			grew = true
		}
	}
	sort.Slice(edges, func(left, right int) bool { return edges[left].Branch < edges[right].Branch })
	return edges, nil
}

// mergedBelow names the branches already merged into the trunk, which a
// whole-stack adoption need neither measure nor offer.
//
// Every one of them is an ancestor of the trunk, so of every branch on it: a
// repository's old merged branches are in every branch's ancestor list, and
// measuring each against each was quadratic in them — on one of a few hundred
// branches, long enough that the preview ran out of time and refused. None can
// be chosen: a parent has to be a selected branch, and anything selected is
// above the trunk, so nearer than all of them. None can attach either, because
// nothing selected is below the trunk. That holds only while no selected branch
// has itself been merged in, so then nothing is left out.
func (s Service) mergedBelow(ctx context.Context, trunk string, selected []string) (map[string]bool, error) {
	merged, err := s.Git.AncestorBranches(ctx, trunk)
	if err != nil {
		return nil, err
	}
	below := make(map[string]bool, len(merged))
	for _, branch := range merged {
		if slices.Contains(selected, branch) {
			return nil, nil
		}
		below[branch] = branch != trunk
	}
	return below, nil
}
