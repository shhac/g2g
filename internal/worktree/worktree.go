// Package worktree decides what a command does about a branch another
// worktree has checked out: move it and bring that worktree along, leave it
// where it is, or refuse.
//
// A rewrite moves a ref without checking anything out, so the worktree holding
// it is left with an index and files describing a commit its branch no longer
// points at. For years the only answer was to refuse, which in the commonest
// layout -- a main checkout sitting on the trunk, the work in linked worktrees
// -- refused every pull that had anything to bring down. A worktree with
// nothing uncommitted loses nothing by coming along, and one with work in
// progress keeps it by being left alone; the rest of the stack need not wait
// for either.
//
// It depends on nothing but repair, so restack, sync and land can share one
// rule.
package worktree

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/shhac/g2g/internal/repair"
)

// Policy is what a command does about a branch another worktree holds.
type Policy string

const (
	// Follow moves a branch whose worktree is clean, bringing that worktree's
	// index and files along, and leaves one whose worktree has changes.
	Follow Policy = "follow"
	// Skip leaves every branch another worktree holds where it is, and does
	// the rest.
	Skip Policy = "skip"
	// Refuse does nothing when a branch that would move is held elsewhere.
	// The zero value means this too: a caller that never chose keeps the
	// behaviour every command had before there was a choice.
	Refuse Policy = "refuse"
)

// Policies are the values a caller may name, the default first.
var Policies = []Policy{Follow, Skip, Refuse}

// Parse reads a value a person typed.
func Parse(value string) (Policy, error) {
	for _, policy := range Policies {
		if string(policy) == value {
			return policy, nil
		}
	}
	names := make([]string, 0, len(Policies))
	for _, policy := range Policies {
		names = append(names, string(policy))
	}
	return "", fmt.Errorf("--worktrees %s is not one of %s", value, strings.Join(names, ", "))
}

// Holder is another worktree with a branch checked out.
type Holder struct {
	Branch string
	Path   string
	// Clean means the worktree has nothing uncommitted, untracked files
	// included, so bringing it to a new commit loses nothing.
	Clean bool
	// Busy names an operation Git is part-way through on the branch there --
	// a rebase or a bisect -- which detaches the worktree while the branch
	// stays in use. Git refuses to move such a branch, and so does this: a
	// busy holder is never clean.
	Busy string
}

// Why says what keeps a holder's branch where it is.
func (h Holder) Why() string {
	if h.Busy != "" {
		return fmt.Sprintf("%s is part-way through a %s there", h.Path, h.Busy)
	}
	return h.Path + " has uncommitted changes"
}

// Moves reports a held branch this policy moves.
func (p Policy) Moves(holder Holder) bool { return p == Follow && holder.Clean }

// Leaves reports a held branch this policy leaves where it is.
func (p Policy) Leaves(holder Holder) bool {
	return p == Skip || (p == Follow && !holder.Clean)
}

// Held maps each branch another worktree has checked out to that worktree.
type Held map[string]Holder

// Of is the holders among branches, in branch order.
func (h Held) Of(branches []string) []Holder {
	holders := make([]Holder, 0)
	for _, branch := range branches {
		if holder, held := h[branch]; held {
			holders = append(holders, holder)
		}
	}
	return holders
}

// Reader is what Git answers about other worktrees. It is optional wherever
// it is asked for: a Git that cannot answer leaves every command exactly as
// safe as it was before the question existed, because it reports nothing held.
type Reader interface {
	// HoldersElsewhere is every branch another worktree has in use, with the
	// operation Git is part-way through there, if any. The current worktree
	// is excluded: rewriting the branch you stand on is ordinary.
	HoldersElsewhere(ctx context.Context) (map[string]Holder, error)
	// WorktreeClean reports a worktree with nothing uncommitted.
	WorktreeClean(ctx context.Context, path string) (bool, error)
}

// Read is which of branches another worktree holds, and whether each of those
// worktrees is clean. Only the worktrees holding one of branches are asked,
// so a repository with a dozen worktrees pays for the ones that matter.
//
// A worktree whose cleanliness cannot be read counts as having changes: a
// directory that has gone, or a Git that fails there, is not one to move.
func Read(ctx context.Context, git any, branches []string) (Held, error) {
	reader, ok := git.(Reader)
	if !ok || len(branches) == 0 {
		return Held{}, nil
	}
	holders, err := reader.HoldersElsewhere(ctx)
	if err != nil {
		return nil, err
	}
	held := Held{}
	clean := map[string]bool{}
	for _, branch := range branches {
		holder, taken := holders[branch]
		if !taken {
			continue
		}
		holder.Branch = branch
		if holder.Busy == "" {
			known, asked := clean[holder.Path]
			if !asked {
				known, err = reader.WorktreeClean(ctx, holder.Path)
				known = known && err == nil
				clean[holder.Path] = known
			}
			holder.Clean = known
		}
		held[branch] = holder
	}
	return held, nil
}

// Outcome is what a plan does about held branches: the ones it moves along
// with their worktrees, and the ones it leaves. Both are in branch order, so
// two plans that would do the same compare equal.
type Outcome struct {
	Follow []Holder
	Leave  []Holder
}

// Empty reports a plan no other worktree is involved in.
func (o Outcome) Empty() bool { return len(o.Follow) == 0 && len(o.Leave) == 0 }

// Equal compares two outcomes, cleanliness included, which is how a worktree
// that picked up changes between a preview and its apply is caught: the
// branch it holds goes from followed to left, and the plan is not the one
// that was previewed.
func (o Outcome) Equal(other Outcome) bool {
	return slices.Equal(o.Follow, other.Follow) && slices.Equal(o.Leave, other.Leave)
}

// With is this outcome and another, a branch either names appearing once,
// in branch order. A branch one side follows and the other leaves is left:
// nothing that leaves a branch may be overruled by something that did not
// look.
func (o Outcome) With(other Outcome) Outcome {
	leave := map[string]Holder{}
	for _, holder := range slices.Concat(o.Leave, other.Leave) {
		leave[holder.Branch] = holder
	}
	follow := map[string]Holder{}
	for _, holder := range slices.Concat(o.Follow, other.Follow) {
		if _, left := leave[holder.Branch]; !left {
			follow[holder.Branch] = holder
		}
	}
	return Outcome{Follow: sorted(follow), Leave: sorted(leave)}
}

// Follows reports a branch this outcome moves along with its worktree.
func (o Outcome) Follows(branch string) (Holder, bool) {
	index := slices.IndexFunc(o.Follow, func(holder Holder) bool { return holder.Branch == branch })
	if index < 0 {
		return Holder{}, false
	}
	return o.Follow[index], true
}

// Leaves reports a branch this outcome leaves where it is.
func (o Outcome) Leaves(branch string) bool {
	return slices.ContainsFunc(o.Leave, func(holder Holder) bool { return holder.Branch == branch })
}

func sorted(holders map[string]Holder) []Holder {
	if len(holders) == 0 {
		return nil
	}
	ordered := make([]Holder, 0, len(holders))
	for _, branch := range slices.Sorted(maps.Keys(holders)) {
		ordered = append(ordered, holders[branch])
	}
	return ordered
}

// Mover brings a worktree's index and files to the commit its branch has moved
// to, as a two-tree merge that keeps anything changed since and refuses to
// overwrite it.
type Mover interface {
	SwitchTreeIn(ctx context.Context, path, from, to string) error
}

// Bring moves a followed worktree from the commit its index describes to the
// one its branch now points at. A branch that did not move needs nothing.
func Bring(ctx context.Context, git any, holder Holder, from, to string) error {
	if from == to {
		return nil
	}
	mover, ok := git.(Mover)
	if !ok {
		return &NotBrought{Holder: holder, From: from, To: to, Err: fmt.Errorf("this Git cannot update another worktree")}
	}
	if err := mover.SwitchTreeIn(ctx, holder.Path, from, to); err != nil {
		return &NotBrought{Holder: holder, From: from, To: to, Err: err}
	}
	return nil
}

// NotBrought is a followed worktree whose branch moved and whose files did
// not. The branch is where it was meant to go; the worktree reports the
// difference as changes nobody made until the command it names is run there.
type NotBrought struct {
	Holder   Holder
	From, To string
	Err      error
}

func (e *NotBrought) Error() string {
	return fmt.Sprintf("%s moved, but %s could not be brought along: %v · run %s to finish",
		e.Holder.Branch, e.Holder.Path, e.Err, e.Finish())
}

func (e *NotBrought) Unwrap() error { return e.Err }

// Finish is the command that brings the worktree along by hand.
func (e *NotBrought) Finish() string {
	return repair.Command([]string{"git", "-C", e.Holder.Path, "read-tree", "-m", "-u", e.From, e.To})
}
