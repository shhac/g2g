package restack

import "context"

// Each worktree journals its own restack. The guards ask two questions of
// them: is one unfinished here, and does one elsewhere move what a command
// reads, or read what it moves.

// RequireIdle refuses other work while any worktree has an unfinished
// restack. It is for a command that cannot say what it touches.
func (s Service) RequireIdle(ctx context.Context) error {
	here, others, err := s.unfinished(ctx)
	switch {
	case err != nil:
		return err
	case here != nil:
		return busy(*here)
	case len(others) != 0:
		return busyElsewhere(others[0], nil)
	}
	return nil
}

// RequireIdleHere refuses while this worktree has an unfinished restack,
// whatever is touched, because Git's rebase here owns the index and the
// checkout. It is what a command asks before it has discovered what it will
// touch, and asks RequireApart afterwards.
func (s Service) RequireIdleHere(ctx context.Context) error {
	here, _, err := s.unfinished(ctx)
	if err != nil || here == nil {
		return err
	}
	return busy(*here)
}

// RequireApart refuses work while this worktree has an unfinished restack, or
// while another worktree's moves a branch it reads or reads a branch it moves,
// and lets anything else go ahead from another worktree.
//
// One restack stopped on a conflict in an agent's worktree used to refuse
// every mutation and every other restack in every worktree, on stacks with
// nothing in common with it. What is untrue mid-restack is where the branches
// it moves sit: they may already have moved while the graph still records
// where they were. Both directions matter: a restack paused part-way through
// replaying a child onto its parent is undone by another moving that parent,
// though neither moves what the other moves. The branches a restack replays
// onto it never moves, so a stack beside it on the same trunk is no conflict.
func (s Service) RequireApart(ctx context.Context, mine Footprint) error {
	here, others, err := s.unfinished(ctx)
	if err != nil {
		return err
	}
	if here != nil {
		return busy(*here)
	}
	return apart(mine, others)
}

// apart refuses when any other restack moves what mine reads, or reads what
// mine moves.
func apart(mine Footprint, others []Elsewhere) error {
	for _, other := range others {
		if holds := mine.meets(other.footprint()); len(holds) != 0 {
			return busyElsewhere(other, holds)
		}
	}
	return nil
}

// unfinished is this worktree's unfinished restack and every other's.
func (s Service) unfinished(ctx context.Context) (*Record, []Elsewhere, error) {
	if s.Journal == nil {
		return nil, nil, nil
	}
	return s.Journal.Unfinished(ctx)
}

// Unfinished is whether this worktree has an unfinished restack, and every
// other worktree's, for a report that covers the whole repository.
func (s Service) Unfinished(ctx context.Context) (bool, []Elsewhere, error) {
	here, others, err := s.unfinished(ctx)
	return here != nil, others, err
}
