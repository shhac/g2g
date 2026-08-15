// Package graph is the branch forest gt2gh owns itself, independent of
// Graphite.
//
// The model is a forest of trees over branch names: every branch has at most
// one parent, a parent may have many children, and a repository may have
// several roots because it may have several trunks. The tree shape is the
// reason this state exists at all — GitHub native stacks are linear, so a tree
// cannot live there, and pull request bases describe only branches that have
// been published.
//
// Nothing here talks to Git, GitHub, or the filesystem. It is the shape and
// the rules; discovery, storage, and rendering live beside it.
package graph

import (
	"fmt"
	"maps"
	"slices"
	"sort"
)

// Authority records which system owns a branch's parent edge.
//
// Authority is per branch rather than per graph on purpose. A whole-graph rule
// cannot survive an edge appearing between two previously separate components,
// because that merges them through an action gt2gh never observed and would
// invalidate both. Per branch, the rule is local: an edge whose endpoints
// disagree is one conflict, reported where it is.
type Authority string

const (
	// AuthorityG2G marks an edge the user adopted into the local store.
	AuthorityG2G Authority = "g2g"
	// AuthorityGraphite marks an edge Graphite declares. gt2gh reads these and
	// never writes them back: Graphite has no supported mutation contract.
	AuthorityGraphite Authority = "graphite"
)

// Origin records how an edge was arrived at. It is read by conflict reporting
// rather than being decoration: "you chose this" and "we inferred this from
// commit ancestry" deserve different confidence when they disagree.
type Origin string

const (
	// OriginUser is an edge the user named explicitly.
	OriginUser Origin = "user"
	// OriginAncestry is an edge inferred from Git commit ancestry.
	OriginAncestry Origin = "git-ancestry"
	// OriginPullRequest is an edge observed from a pull request base.
	OriginPullRequest Origin = "pull-request"
)

// Edge is one recorded parent relationship.
//
// It deliberately holds no commit SHA. Commits and force-pushes are normal
// content movement rather than structural drift, so recording one would make
// every ordinary commit look like the graph had changed. Structure is checked
// against Git at read time instead.
type Edge struct {
	Parent    string
	Authority Authority
	Origin    Origin
}

// Graph is a forest of branches plus the trunks its roots sit on.
//
// Graph identity is deliberately absent. A graph is a connected component of
// the edge relation, which is a computation rather than a record — so there is
// no identifier to generate, no branch-to-graph index to keep consistent, and
// no merge or split event when two components join.
type Graph struct {
	Edges  map[string]Edge
	Trunks []string
}

// New returns an empty graph that is safe to mutate through its methods.
func New() Graph { return Graph{Edges: map[string]Edge{}} }

// Clone returns a deep copy. Every method that changes the graph returns a new
// one, so a preview can never be aliased by the plan that follows it.
func (g Graph) Clone() Graph {
	return Graph{Edges: maps.Clone(defaulted(g.Edges)), Trunks: slices.Clone(g.Trunks)}
}

func defaulted(edges map[string]Edge) map[string]Edge {
	if edges == nil {
		return map[string]Edge{}
	}
	return edges
}

// Parent returns the recorded parent of branch.
func (g Graph) Parent(branch string) (string, bool) {
	edge, tracked := g.Edges[branch]
	return edge.Parent, tracked
}

// Tracked reports whether the graph records an edge for branch.
func (g Graph) Tracked(branch string) bool {
	_, tracked := g.Edges[branch]
	return tracked
}

// Branches returns every tracked branch, sorted.
func (g Graph) Branches() []string {
	branches := make([]string, 0, len(g.Edges))
	for branch := range g.Edges {
		branches = append(branches, branch)
	}
	sort.Strings(branches)
	return branches
}

// IsTrunk reports whether branch is a recorded trunk.
func (g Graph) IsTrunk(branch string) bool { return slices.Contains(g.Trunks, branch) }

// Children returns the branches whose parent is branch, sorted so every
// rendering and every walk of the same graph produces the same order.
func (g Graph) Children(branch string) []string {
	children := make([]string, 0)
	for child, edge := range g.Edges {
		if edge.Parent == branch {
			children = append(children, child)
		}
	}
	sort.Strings(children)
	return children
}

// Roots returns the tracked branches whose parent is not itself tracked,
// together with any trunk that has tracked children. These are where a render
// of the whole forest starts.
func (g Graph) Roots() []string {
	seen := map[string]bool{}
	roots := make([]string, 0)
	for _, edge := range g.Edges {
		if g.Tracked(edge.Parent) || seen[edge.Parent] {
			continue
		}
		seen[edge.Parent] = true
		roots = append(roots, edge.Parent)
	}
	sort.Strings(roots)
	return roots
}

// Path returns the root-to-branch chain, inclusive of both ends. It is the
// selection a linear GitHub projection consumes.
func (g Graph) Path(branch string) ([]string, error) {
	path := []string{branch}
	seen := map[string]bool{branch: true}
	for current := branch; ; {
		edge, tracked := g.Edges[current]
		if !tracked {
			break
		}
		if seen[edge.Parent] {
			return nil, fmt.Errorf("branch %q is part of a parent cycle", branch)
		}
		seen[edge.Parent] = true
		path = append(path, edge.Parent)
		current = edge.Parent
	}
	slices.Reverse(path)
	return path, nil
}

// Subtree returns branch and every descendant in a stable pre-order: a parent
// always precedes its children and siblings come in sorted order.
func (g Graph) Subtree(branch string) []string {
	subtree := []string{branch}
	for _, child := range g.Children(branch) {
		subtree = append(subtree, g.Subtree(child)...)
	}
	return subtree
}

// Component returns the whole tree branch belongs to, starting from its root.
func (g Graph) Component(branch string) ([]string, error) {
	path, err := g.Path(branch)
	if err != nil {
		return nil, err
	}
	return g.Subtree(path[0]), nil
}

// Track records parent for branch, returning a new graph. It refuses a branch
// that is its own parent and any edge that would close a cycle, because a
// cycle is not a tree and every walk below would have to defend against it.
func (g Graph) Track(branch string, edge Edge) (Graph, error) {
	if branch == "" || edge.Parent == "" {
		return Graph{}, fmt.Errorf("both a branch and a parent are required")
	}
	if branch == edge.Parent {
		return Graph{}, fmt.Errorf("branch %q cannot be its own parent", branch)
	}
	updated := g.Clone()
	updated.Edges[branch] = edge
	if _, err := updated.Path(edge.Parent); err != nil {
		return Graph{}, fmt.Errorf("recording %q under %q would create a parent cycle", branch, edge.Parent)
	}
	return updated, nil
}

// Untrack removes the edges for the given branches, returning a new graph.
// Children of a removed branch are left pointing at it rather than being
// reparented: inventing an edge the user did not ask for is the guess this
// tool does not make, and Orphans reports the result.
func (g Graph) Untrack(branches ...string) Graph {
	updated := g.Clone()
	for _, branch := range branches {
		delete(updated.Edges, branch)
	}
	return updated
}

// Orphans returns tracked branches whose recorded parent is neither tracked
// nor a trunk. They are the visible consequence of untracking a middle branch.
func (g Graph) Orphans() []string {
	orphans := make([]string, 0)
	for _, branch := range g.Branches() {
		parent := g.Edges[branch].Parent
		if !g.Tracked(parent) && !g.IsTrunk(parent) {
			orphans = append(orphans, branch)
		}
	}
	return orphans
}

// WithTrunks returns a copy whose trunk set is exactly trunks, deduplicated
// and sorted.
func (g Graph) WithTrunks(trunks ...string) Graph {
	updated := g.Clone()
	unique := map[string]bool{}
	updated.Trunks = make([]string, 0, len(trunks))
	for _, trunk := range trunks {
		if trunk != "" && !unique[trunk] {
			unique[trunk] = true
			updated.Trunks = append(updated.Trunks, trunk)
		}
	}
	sort.Strings(updated.Trunks)
	return updated
}

// Validate reports the structural faults a graph read from disk may contain.
// It is what makes every walk below able to assume a forest.
func (g Graph) Validate() error {
	for _, branch := range g.Branches() {
		edge := g.Edges[branch]
		if edge.Parent == "" {
			return fmt.Errorf("branch %q has an empty parent", branch)
		}
		if edge.Parent == branch {
			return fmt.Errorf("branch %q is its own parent", branch)
		}
		if _, err := g.Path(branch); err != nil {
			return err
		}
	}
	return nil
}

// Equal reports whether two graphs record the same structure. Revalidation
// compares this immediately before a write.
func (g Graph) Equal(other Graph) bool {
	return maps.Equal(defaulted(g.Edges), defaulted(other.Edges)) && slices.Equal(g.Trunks, other.Trunks)
}
