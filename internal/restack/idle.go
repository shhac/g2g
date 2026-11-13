package restack

import (
	"context"
	"errors"

	"github.com/shhac/g2g/internal/repair"
)

// Each worktree journals its own restack. The guards ask two questions of
// them: is one unfinished here, and does one elsewhere move what a command
// reads, or read what it moves.

// RequireIdle refuses other work while any worktree has an unfinished
// restack. It is for a command that cannot say what it touches.
func (s Service) RequireIdle(ctx context.Context) error {
	if err := s.RequireIdleHere(ctx); err != nil {
		return err
	}
	others, err := s.elsewhere(ctx)
	if err != nil || len(others) == 0 {
		return err
	}
	return busyElsewhere(others[0], nil)
}

// RequireIdleHere refuses while this worktree has an unfinished restack,
// whatever is touched, because Git's rebase here owns the index and the
// checkout. It is what a command asks before it has discovered what it will
// touch, and asks RequireApart afterwards.
func (s Service) RequireIdleHere(ctx context.Context) error {
	if s.Journal == nil {
		return nil
	}
	record, found, err := s.Journal.Load(ctx)
	if err != nil || !found {
		return err
	}
	return busy(record)
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
	if err := s.RequireIdleHere(ctx); err != nil {
		return err
	}
	others, err := s.elsewhere(ctx)
	if err != nil {
		return err
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

// InProgress reports an unfinished restack in this worktree, which every
// other command here has to refuse while it lasts: a branch may already have
// moved while the graph still records where it used to be. Another
// worktree's restack is its own, and the guards ask about it separately.
func (s Service) InProgress(ctx context.Context) (bool, error) {
	if s.Journal == nil {
		return false, nil
	}
	_, found, err := s.Journal.Load(ctx)
	return found, err
}

// Unfinished is this worktree's unfinished restack, as the way past it, and
// every other worktree's, for a report that covers the whole repository.
func (s Service) Unfinished(ctx context.Context) (repair.Note, []Elsewhere, error) {
	if s.Journal == nil {
		return repair.Note{}, nil, nil
	}
	record, found, err := s.Journal.Load(ctx)
	if err != nil {
		return repair.Note{}, nil, err
	}
	here := repair.Note{}
	if found {
		here = record.Repair()
	}
	others, err := s.elsewhere(ctx)
	return here, others, err
}

// elsewhere is every other worktree's unfinished restack.
func (s Service) elsewhere(ctx context.Context) ([]Elsewhere, error) {
	if s.Journal == nil {
		return nil, nil
	}
	return s.Journal.Elsewhere(ctx)
}

// Repair is the way past this worktree's own unfinished restack: finish it,
// or undo it.
func (r Record) Repair() repair.Note {
	return repair.Note{
		Reason: "a restack" + r.started() + " stopped part-way",
		Ways: []repair.Step{
			{Command: "g2g restack --continue", Effect: "finish it once its conflicts are resolved"},
			{Command: "g2g restack --abort", Effect: "undo it"},
		},
	}
}

// busy is the refusal while this worktree's own restack is unfinished, or a
// restack recorded before any said which worktree it belongs to.
func busy(record Record) error {
	note := record.Repair()
	note.Reason = "a restack" + record.started() + " is already in progress"
	return errors.New(note.Sentence())
}
