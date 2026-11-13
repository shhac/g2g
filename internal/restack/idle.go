package restack

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/shhac/g2g/internal/repair"
	"github.com/shhac/g2g/internal/words"
)

// Each worktree journals its own restack, and the guards below ask two
// questions of every journal: is one unfinished here, and does one elsewhere
// move what this command touches, or replay onto what it moves.

// RequireIdle refuses other work while any worktree has an unfinished
// restack. It is for a command that cannot say what it touches.
func (s Service) RequireIdle(ctx context.Context) error {
	here, others, err := s.journals(ctx)
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

// RequireIdleFor refuses work on branches an unfinished restack is part-way
// through, and lets work on any other branch go ahead from another worktree.
//
// One restack stopped on a conflict in an agent's worktree used to refuse
// every mutation in every worktree, on stacks with nothing in common with it.
// What is untrue mid-restack is where the branches it moves sit: they may
// already have moved while the graph still records where they were. A command
// that touches none of them reads and writes nothing the restack will change.
// The branches it replays onto are not among them: a restack never moves
// those, so publishing or recording a stack beside it on the same trunk is no
// conflict.
//
// touching names every branch the command moves, publishes or records, with
// the parents it measures them against, so a stack built on a branch the
// restack is moving is refused. This worktree's own restack refuses whatever
// is touched, because Git's rebase here owns the index and the checkout; so
// does a journal that does not say which worktree owns it. Touching nothing
// asks only that, which is what a command asks before it has discovered what
// it will touch.
func (s Service) RequireIdleFor(ctx context.Context, touching []string) error {
	return s.requireApart(ctx, footprint{reads: set(touching)})
}

// requireApart refuses a restack here while another worktree's unfinished
// restack moves a branch this one reads, or reads a branch this one moves.
// Both directions matter: a restack paused part-way through replaying a
// child onto its parent is undone by another moving that parent.
func (s Service) requireApart(ctx context.Context, mine footprint) error {
	here, others, err := s.journals(ctx)
	if err != nil {
		return err
	}
	if here != nil {
		return busy(*here)
	}
	return apart(mine, others)
}

// apart refuses when any other restack moves what mine reads, or reads what
// mine moves, or does not say which worktree owns it.
func apart(mine footprint, others []Elsewhere) error {
	for _, other := range others {
		if other.Worktree == "" {
			return busy(other.Record)
		}
		if shared := mine.meets(other.footprint()); len(shared) != 0 {
			return busyElsewhere(other, shared)
		}
	}
	return nil
}

// journals is this worktree's unfinished restack, if it has one, and every
// other worktree's. A journal shared by every worktree holds at most one,
// and it is this worktree's unless it names another.
func (s Service) journals(ctx context.Context) (*Record, []Elsewhere, error) {
	if s.Journal == nil {
		return nil, nil, nil
	}
	record, found, err := s.Journal.Load(ctx)
	if err != nil {
		return nil, nil, err
	}
	var here *Record
	others := []Elsewhere{}
	if found {
		owned, err := s.ownedHere(ctx, record)
		if err != nil {
			return nil, nil, err
		}
		if owned {
			here = &record
		} else {
			others = append(others, Elsewhere{Record: record})
		}
	}
	if journal, ok := s.Journal.(othersJournal); ok {
		elsewhere, err := journal.Others(ctx)
		if err != nil {
			return nil, nil, err
		}
		others = append(others, elsewhere...)
	}
	return here, others, nil
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

// Unfinished is whether this worktree has an unfinished restack, and every
// other worktree's, for a report that covers the whole repository.
func (s Service) Unfinished(ctx context.Context) (bool, []Elsewhere, error) {
	here, others, err := s.journals(ctx)
	return here != nil, others, err
}

// footprint is what a restack moves and what it reads: every branch it moves,
// and every branch those are measured against or replayed onto.
type footprint struct {
	moves, reads map[string]bool
}

// meets is every branch one footprint moves that the other reads, either way
// round, sorted.
func (f footprint) meets(other footprint) []string {
	shared := map[string]bool{}
	for branch := range f.moves {
		if other.reads[branch] {
			shared[branch] = true
		}
	}
	for branch := range other.moves {
		if f.reads[branch] {
			shared[branch] = true
		}
	}
	return slices.Sorted(maps.Keys(shared))
}

func set(branches []string) map[string]bool {
	members := make(map[string]bool, len(branches))
	for _, branch := range branches {
		if branch != "" {
			members[branch] = true
		}
	}
	return members
}

// busy is the refusal while this worktree's restack is unfinished, or one
// whose journal does not say which worktree owns it.
func busy(record Record) error {
	owner := "the worktree that started it"
	if checkout := journalCheckout(record.Worktree); checkout != "" {
		owner = fmt.Sprintf("checkout %q", checkout)
	} else if record.Worktree != "" {
		owner += fmt.Sprintf(" (private Git directory: %s)", record.Worktree)
	}
	return fmt.Errorf("a restack is already in progress%s · inspect %s, then run g2g restack --continue there; g2g restack --abort there undoes that restack", restackOf(record), owner)
}

// busyElsewhere is the refusal while another worktree's restack holds what
// this command touches. Its --continue and --abort are whoever started it's
// to run, so the way through from here is to wait. A record whose checkout
// is gone has nobody to finish it, and forgetting it is the way through.
func busyElsewhere(other Elsewhere, shared []string) error {
	holds := ""
	if len(shared) != 0 {
		holds = ", which holds " + words.List(shared)
	}
	checkout := journalCheckout(other.Worktree)
	switch {
	case checkout == "" && other.Path != "":
		return fmt.Errorf("a restack%s is unfinished in a worktree that no longer exists%s · if nobody will finish it, %s forgets it and leaves its branches where they are now", restackOf(other.Record), holds,
			repair.Command([]string{"rm", other.Path}))
	case checkout == "":
		return busy(other.Record)
	}
	return fmt.Errorf("a restack%s is unfinished in checkout %q%s · it is that checkout's to finish, with g2g restack --continue or --abort there; wait for it, then rerun this", restackOf(other.Record), checkout, holds)
}

// Checkout is the checkout the restack belongs to, empty when it no longer
// exists or cannot be told from its private Git directory.
func (e Elsewhere) Checkout() string { return journalCheckout(e.Worktree) }

// Started is the branch the restack was started from, if it says.
func (e Elsewhere) Started() string {
	if e.Branch != "" {
		return e.Branch
	}
	return e.CheckoutBranch
}

// restackOf names the branch a restack was started from.
func restackOf(record Record) string {
	branch := record.Branch
	if branch == "" {
		branch = record.CheckoutBranch
	}
	if branch == "" {
		return ""
	}
	return fmt.Sprintf(" for branch %q", branch)
}

// A linked worktree's private Git directory records the checkout's .git file.
// Use that exact backlink when available; a bare directory name is not evidence
// that its parent is a checkout (core.worktree and bare repositories differ).
// A backlink to a checkout that is gone is no checkout.
func journalCheckout(gitDir string) string {
	if gitDir == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(gitDir, "gitdir"))
	if err != nil {
		return mainCheckout(gitDir)
	}
	path := strings.TrimSpace(string(data))
	if !filepath.IsAbs(path) || filepath.Base(path) != ".git" {
		return ""
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return ""
	}
	return filepath.Dir(path)
}

// mainCheckout is the checkout a main worktree's .git directory belongs to.
func mainCheckout(gitDir string) string {
	if filepath.Base(gitDir) != ".git" {
		return ""
	}
	if info, err := os.Stat(gitDir); err != nil || !info.IsDir() {
		return ""
	}
	return filepath.Dir(gitDir)
}
