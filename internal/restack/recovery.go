package restack

import (
	"context"
	"errors"
	"fmt"
)

// WorktreeLocator identifies the private Git directory owning rebase state.
type WorktreeLocator interface {
	WorktreeDir(context.Context) (string, error)
}

// InProgress reports an unfinished restack in this worktree, which every
// other command here has to refuse while it lasts: a branch may already have
// moved while the graph still records where it used to be. Another
// worktree's restack is its own, and the guards ask about it separately.
func (s Service) InProgress(ctx context.Context) (bool, error) {
	here, _, err := s.unfinished(ctx)
	return here != nil, err
}

// ownedRecord is the common gate before any recovery command touches Git:
// this worktree's record, which is the journal's to tell apart from every
// other worktree's.
func (s Service) ownedRecord(ctx context.Context) (Record, error) {
	record, found, err := s.Journal.Load(ctx)
	if err != nil {
		return Record{}, err
	}
	if !found {
		return Record{}, s.noneHere(ctx)
	}
	return record, nil
}

// noneHere is the refusal of a recovery verb in a worktree with no restack
// of its own, naming the one another worktree has, since that is the restack
// the reader most likely meant.
func (s Service) noneHere(ctx context.Context) error {
	_, others, err := s.unfinished(ctx)
	if err != nil || len(others) == 0 {
		return errors.Join(err, fmt.Errorf("no restack is in progress"))
	}
	note := others[0].Repair(nil)
	note.Reason = "no restack is in progress in this worktree; " + note.Reason
	return errors.New(note.Sentence())
}

func (s Service) checkpoint(ctx context.Context, record *Record, standing checkout) error {
	record.CheckoutBranch, record.CheckoutTip = standing.Branch, standing.Tip
	return s.Journal.Save(ctx, *record)
}

// recoverCheckout repairs the gap between moving refs and updating the index.
// Only call this outside an active rebase: while rebasing, Git owns the index.
// SwitchTree is a two-tree merge, preserving local edits and refusing clobbers;
// it also accepts an index already describing the new tree.
func (s Service) recoverCheckout(ctx context.Context, record Record) error {
	if record.CheckoutBranch == "" || record.CheckoutTip == "" {
		return nil
	}
	current, err := s.Git.CurrentBranch(ctx)
	if err != nil {
		return err
	}
	if current != record.CheckoutBranch {
		return nil
	}
	return s.resettle(ctx, checkout{Branch: current, Tip: record.CheckoutTip})
}
