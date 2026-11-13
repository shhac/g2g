package restack

import (
	"errors"
	"fmt"

	"github.com/shhac/g2g/internal/repair"
	"github.com/shhac/g2g/internal/words"
)

// Elsewhere is an unfinished restack another worktree owns, the file that
// records it, and where that worktree is now.
type Elsewhere struct {
	Record
	Path string
	// GitDir is that worktree's private Git directory as it is now, and
	// Checkout its checkout, each empty when it cannot be told. Gone is
	// Git having no private directory for it any more, which removing a
	// worktree takes; nothing else is taken as its having gone.
	GitDir, Checkout string
	Gone             bool
}

// Repair is the way past another worktree's unfinished restack: it is that
// worktree's to finish, so from here the way is to wait, or to finish it
// there; and one whose worktree is gone has nobody to finish it, so the way is
// to forget it. holds names what this command and that restack share.
func (e Elsewhere) Repair(holds []string) repair.Note {
	reason := "a restack" + e.started() + " is unfinished " + e.where()
	if len(holds) != 0 {
		reason += ", which holds " + words.List(holds)
	}
	if e.Gone {
		return repair.Note{Reason: reason, Ways: []repair.Step{{
			Command: repair.Command([]string{"rm", e.Path}),
			Effect:  "forget it, if nobody will finish it, leaving its branches where they are now",
		}}}
	}
	return repair.Note{Reason: reason, Ways: []repair.Step{{
		Effect: "wait for it, or finish it there with g2g restack --continue, --skip or --abort",
	}}}
}

// where names the worktree, as well as it can be told.
func (e Elsewhere) where() string {
	switch {
	case e.Checkout != "":
		return fmt.Sprintf("in checkout %q", e.Checkout)
	case e.Gone:
		return "in a worktree that no longer exists"
	case e.GitDir != "":
		return fmt.Sprintf("in another worktree (Git directory: %s)", e.GitDir)
	}
	return "in the worktree that started it"
}

// started names the branch a restack was started from, as " for branch x".
func (r Record) started() string {
	branch := r.Branch
	if branch == "" {
		branch = r.CheckoutBranch
	}
	if branch == "" {
		return ""
	}
	return fmt.Sprintf(" for branch %q", branch)
}

// busyElsewhere is the refusal while another worktree's restack holds what
// this command touches.
func busyElsewhere(other Elsewhere, holds []string) error {
	return errors.New(other.Repair(holds).Sentence())
}
