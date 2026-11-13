package restack

import (
	"context"
	"errors"
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
	return busy(record)
}

// RequireIdleFor refuses work on branches an unfinished restack is part-way
// through, and lets work on any other branch go ahead from another worktree.
//
// The journal is shared, so one restack stopped on a conflict in an agent's
// worktree refused every mutation in every worktree, on stacks with nothing in
// common with it. What is untrue mid-restack is where the branches it moves
// sit: they may already have moved while the graph still records where they
// were. A command that touches none of them reads and writes nothing the
// restack will change. The branches it replays onto are not among them: a
// restack never moves those, so publishing or recording a stack beside it on
// the same trunk is no conflict.
//
// touching names every branch the command moves, publishes or records, with
// the parents it measures them against, so a stack built on a branch the
// restack is moving is refused. The worktree the restack belongs to is
// refused whatever it touches, because Git's rebase there owns the index and
// the checkout; so is a journal that does not say which worktree owns it.
func (s Service) RequireIdleFor(ctx context.Context, touching []string) error {
	if s.Journal == nil {
		return nil
	}
	record, found, err := s.Journal.Load(ctx)
	if err != nil || !found {
		return err
	}
	here, err := s.ownedHere(ctx, record)
	if err != nil || here {
		return errors.Join(err, busy(record))
	}
	moving := record.moving()
	for _, branch := range touching {
		if moving[branch] {
			return busy(record)
		}
	}
	return nil
}

// ownedHere reports a journal this worktree's rebase owns, or one that cannot
// say which worktree does.
func (s Service) ownedHere(ctx context.Context, record Record) (bool, error) {
	locator, ok := s.Git.(WorktreeLocator)
	if record.Worktree == "" || !ok {
		return true, nil
	}
	dir, err := locator.WorktreeDir(ctx)
	if err != nil {
		return true, err
	}
	return dir == record.Worktree, nil
}

// moving is every branch the restack may move, whether or not it has yet.
func (r Record) moving() map[string]bool {
	moving := map[string]bool{}
	add := func(branch string) {
		if branch != "" {
			moving[branch] = true
		}
	}
	for branch := range r.Original {
		add(branch)
	}
	for branch := range r.Reparent {
		add(branch)
	}
	for branch := range r.Structure {
		add(branch)
	}
	return moving
}

// busy is the refusal while a restack is unfinished.
func busy(record Record) error {
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
