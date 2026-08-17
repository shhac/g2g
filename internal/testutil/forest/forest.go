// Package forest builds Graphite forests for tests.
//
// It is a subpackage rather than part of testutil because testutil is imported
// by graphite's own tests, and a helper that constructs a graphite.Forest would
// close that loop into an import cycle.
package forest

import (
	"sort"

	"github.com/shhac/g2g/internal/graphite"
)

// Of turns ordered chains into the forest a Graphite read returns.
//
// Selection reads the forest, so a fake that describes one shape through a path
// and a different one through the forest is asserting itself rather than the
// code between them. Stating the shape once, here, is what stops the two
// answers drifting apart per package.
func Of(chains ...[]string) graphite.Forest {
	forest := graphite.Forest{Parents: map[string]string{}}
	roots := map[string]bool{}
	for _, chain := range chains {
		for index, branch := range chain {
			if index == 0 {
				if _, seen := forest.Parents[branch]; !seen {
					forest.Parents[branch] = ""
				}
				roots[branch] = true
				continue
			}
			forest.Parents[branch] = chain[index-1]
		}
	}
	for root := range roots {
		forest.Roots = append(forest.Roots, root)
	}
	sort.Strings(forest.Roots)
	return forest
}

// OfStacks is Of for callers holding declared stacks
// rather than bare chains, which is the shape a Graphite fake usually carries.
func OfStacks(sets ...map[string]graphite.Stack) graphite.Forest {
	chains := make([][]string, 0)
	roots := map[string]bool{}
	for _, set := range sets {
		for _, declared := range set {
			chains = append(chains, declared.Path)
			for _, trunk := range declared.Trunks {
				roots[trunk] = true
			}
		}
	}
	forest := Of(chains...)
	forest.Roots = forest.Roots[:0]
	for trunk := range roots {
		forest.Roots = append(forest.Roots, trunk)
	}
	sort.Strings(forest.Roots)
	return forest
}
