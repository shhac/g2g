package restack

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// RequireIdle refuses other work while the shared journal records an unfinished
// restack. The owner is a private Git directory, not a checkout to cd into.
func (s Service) RequireIdle(ctx context.Context) error {
	if s.Journal == nil {
		return nil
	}
	record, found, err := s.Journal.Load(ctx)
	if err != nil || !found {
		return err
	}
	owner := "the worktree that started it"
	if checkout := journalCheckout(record.Worktree); checkout != "" {
		owner = fmt.Sprintf("checkout %q", checkout)
	} else if record.Worktree != "" {
		owner += fmt.Sprintf(" (private Git directory: %s)", record.Worktree)
	}
	branch := record.Branch
	if branch == "" {
		branch = record.CheckoutBranch
	}
	detail := ""
	if branch != "" {
		detail = fmt.Sprintf(" for branch %q", branch)
	}
	return fmt.Errorf("a restack is already in progress%s · inspect %s, then run g2g restack --continue there; g2g restack --abort there undoes that restack", detail, owner)
}

// A linked worktree's private Git directory records the checkout's .git file.
// Use that exact backlink when available; a bare directory name is not evidence
// that its parent is a checkout (core.worktree and bare repositories differ).
func journalCheckout(gitDir string) string {
	if gitDir == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(gitDir, "gitdir"))
	if err != nil {
		return ""
	}
	path := strings.TrimSpace(string(data))
	if !filepath.IsAbs(path) || filepath.Base(path) != ".git" {
		return ""
	}
	return filepath.Dir(path)
}
