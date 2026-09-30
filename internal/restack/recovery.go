package restack

import (
	"context"
	"fmt"
)

// WorktreeLocator identifies the private Git directory owning rebase state.
type WorktreeLocator interface {
	WorktreeDir(context.Context) (string, error)
}

func (s Service) requireOwner(ctx context.Context, record Record) error {
	// Older journals did not record ownership; retain their recovery path.
	if record.Worktree == "" {
		return nil
	}
	locator, ok := s.Git.(WorktreeLocator)
	if !ok {
		return fmt.Errorf("cannot identify the worktree owning this restack")
	}
	here, err := locator.WorktreeDir(ctx)
	if err != nil {
		return err
	}
	if here != record.Worktree {
		return fmt.Errorf("this restack belongs to another worktree · run g2g restack --continue, --skip, or --abort there (Git directory: %s)", record.Worktree)
	}
	return nil
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
