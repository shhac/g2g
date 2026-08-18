// Package graph is the branch forest g2g owns itself, independent of
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

	"github.com/shhac/g2g/internal/shape"
)

// Origin records how much Git agrees with an edge at the moment it was
// recorded, which is not decoration: a parent that is already an ancestor is
// confirmed, and one that is not is an assertion the user made about branches
// whose commits do not yet line up.
type Origin string

const (
	// OriginUser is an edge the user named explicitly.
	OriginUser Origin = "user"
	// OriginAncestry is an edge Git confirms: the parent's tip is reachable
	// from the branch.
	OriginAncestry Origin = "git-ancestry"
)

// Edge is one recorded parent relationship.
//
// A branch's own tip is deliberately absent: it moves with every ordinary
// commit, so recording it would make routine work look like the graph had
// changed. ForkPoint is different — see its own comment.
type Edge struct {
	Parent string
	Origin Origin
	// ForkPoint is the parent's tip when this edge was written. It is
	// structural rather than drift state: it answers which commits belong to
	// the branch, namely ForkPoint..branch, and that range is what a restack
	// replays. It changes only when an edge is adopted or restacked.
	//
	// It cannot be derived after the fact. Once a merged parent's branch is
	// deleted, the merge base with the trunk points before the parent's work
	// and replaying from there would reapply it.
	ForkPoint string
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

// shape is this graph with the edge payload removed.
//
// Fork points and origins matter to restack and to nothing that merely walks
// the structure, so the walking lives once in shape.Forest and both records
// answer a scope the same way. Keeping a second copy here is how the two came
// to differ in the small ways that only show up under an unusual shape.
func (g Graph) Shape() shape.Forest {
	parents := make(map[string]string, len(g.Edges))
	for branch, edge := range g.Edges {
		parents[branch] = edge.Parent
	}
	return shape.Forest{Parents: parents}
}

// Children returns the branches whose parent is branch, sorted so every
// rendering and every walk of the same graph produces the same order.
func (g Graph) Children(branch string) []string { return g.Shape().Children(branch) }

// Roots returns the tracked branches whose parent is not itself tracked,
// together with any trunk that has tracked children. These are where a render
// of the whole forest starts.
func (g Graph) Roots() []string { return g.Shape().Roots() }

// Path returns the root-to-branch chain, inclusive of both ends. It is the
// selection a linear GitHub projection consumes.
func (g Graph) Path(branch string) ([]string, error) { return g.Shape().Path(branch) }

// Subtree returns branch and every descendant in a stable pre-order: a parent
// always precedes its children and siblings come in sorted order.
func (g Graph) Subtree(branch string) []string { return g.Shape().Subtree(branch) }

// Component returns the whole tree branch belongs to, starting from its root.
func (g Graph) Component(branch string) ([]string, error) { return g.Shape().Component(branch) }

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
	// A trunk is a branch nothing sits under. Recording a parent for one ends
	// that, so the entry goes with it — otherwise adopting a stack from its tip
	// downwards, which is what being on the tip branch leads you to do, promotes
	// each branch to a trunk on the way past and never takes it back.
	return updated.withoutTrunk(branch), nil
}

// Adopt records an edge and keeps the trunk set true either side of it: the
// branch stops being a trunk because something now sits above it, and the
// parent becomes one if nothing else records it. newTrunk names the parent when
// it was promoted, which is what a preview says out loud.
//
// Track alone is not enough for a caller, and three of them were each pairing it
// with their own promotion step. Two took the trunk list from the graph before
// the edge was recorded, which quietly put back the entry Track had just
// removed. The rule belongs here, once, with the type that owns the invariant.
func (g Graph) Adopt(branch string, edge Edge) (Graph, string, error) {
	updated, err := g.Track(branch, edge)
	if err != nil {
		return Graph{}, "", err
	}
	if g.Tracked(edge.Parent) || updated.IsTrunk(edge.Parent) {
		return updated, "", nil
	}
	return updated.WithTrunks(append(slices.Clone(updated.Trunks), edge.Parent)...), edge.Parent, nil
}

// withoutTrunk drops branch from the trunk set, leaving the rest as they were.
func (g Graph) withoutTrunk(branch string) Graph {
	if !g.IsTrunk(branch) {
		return g
	}
	remaining := make([]string, 0, len(g.Trunks))
	for _, trunk := range g.Trunks {
		if trunk != branch {
			remaining = append(remaining, trunk)
		}
	}
	return g.WithTrunks(remaining...)
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
