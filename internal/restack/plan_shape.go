package restack

// Replaying lists the branches whose commits are actually replayed, which is
// not every branch in the plan: one that collapses only has its ref moved.
func (p Plan) Replaying() []string {
	branches := make([]string, 0, len(p.Steps))
	for _, step := range p.rewriting() {
		branches = append(branches, step.Branch)
	}
	return branches
}

// rewriting is the steps that actually have commits to replay.
func (p Plan) rewriting() []Step {
	steps := make([]Step, 0, len(p.Steps))
	for _, step := range p.Steps {
		if !step.Collapses {
			steps = append(steps, step)
		}
	}
	return steps
}

// collapsing is the steps whose ref only has to move.
func (p Plan) collapsing() []Step {
	steps := make([]Step, 0)
	for _, step := range p.Steps {
		if step.Collapses {
			steps = append(steps, step)
		}
	}
	return steps
}

// reparenting is the branches this plan moves to a different parent, which is
// only ever the result of an explicit --onto.
func (p Plan) reparenting() map[string]string {
	moves := map[string]string{}
	if !p.Onto.Reparents() {
		// A rewrite that only moves contents records nothing. Deriving the move
		// from the replay target instead is what put refs/g2g/remotes/origin/main
		// in the store as a parent, on the ordinary sync path.
		return moves
	}
	// Only the selection's root moves. Every branch above it is being rewritten
	// because its parent is, not because its parent changed, and recording the
	// same new parent for all of them flattened the stack into a fan: a
	// subtree's children came to record the --onto target rather than the
	// branch they are stacked on, and the fork point refreshed alongside then
	// widened each one's replay range to swallow its parent's commits.
	// Plan refuses an --onto over more than one root, so there is only one.
	roots := selectionRoots(p.Discovery)
	if len(roots) != 1 {
		return moves
	}
	if recorded, tracked := p.Graph.Parent(roots[0]); tracked && recorded != p.Onto.Parent {
		moves[roots[0]] = p.Onto.Parent
	}
	return moves
}

// ParentAfter is the parent the graph records for branch once this plan has
// run: the --onto target for the root it moves, and otherwise the parent
// already recorded. A rewrite onto a location, as pull's is, records no
// parent, so where it replays is never the answer: that is a ref under
// refs/g2g/, which no reader knows by name.
func (p Plan) ParentAfter(branch string) string {
	if moved, ok := p.reparenting()[branch]; ok {
		return moved
	}
	parent, _ := p.Graph.Parent(branch)
	return parent
}
