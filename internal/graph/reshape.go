// Changing the record when a branch goes or changes name.
//
// These live apart from Track and Untrack because they answer a different
// request. Untrack forgets an edge and must never reparent what it strands:
// nobody asked where those children belong. Removing a branch that is being
// deleted or folded is the user asking for it to go, and what it sat on is the
// only place its children can mean — so here it is stated, not guessed.
package graph

import (
	"fmt"
	"slices"
)

// Remove forgets branch and records each branch that sat on it under its
// parent instead, returning the new graph and those children.
//
// The children keep their fork points. That is what makes the next restack
// replay only their own commits onto the new parent: the recorded fork point
// is where the removed branch's work ended, so its commits are excluded from
// every child's range rather than carried along with it.
func (g Graph) Remove(branch string) (Graph, []string, error) {
	edge, tracked := g.Edges[branch]
	if !tracked {
		return Graph{}, nil, fmt.Errorf("%s is not recorded under a parent, so there is nowhere to put what sits on it", branch)
	}
	updated := g.Clone()
	delete(updated.Edges, branch)
	children := g.Children(branch)
	for _, child := range children {
		moved := updated.Edges[child]
		moved.Parent = edge.Parent
		updated.Edges[child] = moved
	}
	if err := updated.Validate(); err != nil {
		return Graph{}, nil, err
	}
	return updated, children, nil
}

// Rename moves every record that names from to to: its own edge, the edges of
// the branches recorded under it, its place among the trunks, its declaration,
// and every declaration that lands into it.
//
// Graph identity is derived rather than stored, so this is a key rewrite and
// nothing more; there is no index elsewhere to keep in step.
func (g Graph) Rename(from, to string) (Graph, error) {
	if from == "" || to == "" {
		return Graph{}, fmt.Errorf("both the old and the new name are required")
	}
	if from == to {
		return Graph{}, fmt.Errorf("%s already has that name", from)
	}
	if g.Records(to) {
		return Graph{}, fmt.Errorf("the graph already records %s", to)
	}
	updated := g.Clone()
	if edge, tracked := updated.Edges[from]; tracked {
		delete(updated.Edges, from)
		updated.Edges[to] = edge
	}
	for branch, edge := range updated.Edges {
		if edge.Parent == from {
			edge.Parent = to
			updated.Edges[branch] = edge
		}
	}
	if g.IsTrunk(from) {
		trunks := slices.DeleteFunc(slices.Clone(updated.Trunks), func(trunk string) bool { return trunk == from })
		updated = updated.withTrunks(append(trunks, to)...)
	}
	if declaration, declared := updated.Declared[from]; declared {
		delete(updated.Declared, from)
		updated.Declared[to] = declaration
	}
	for trunk, declaration := range updated.Declared {
		if declaration.Into == from {
			declaration.Into = to
			updated.Declared[trunk] = declaration
		}
	}
	if err := updated.Validate(); err != nil {
		return Graph{}, err
	}
	return updated, nil
}
