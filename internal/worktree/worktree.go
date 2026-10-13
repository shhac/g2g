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
	return "", fmt.Errorf("%q is not one of %s", value, strings.Join(names(), ", "))
}

// names are the values a caller may name, as they are typed.
func names() []string {
	names := make([]string, 0, len(Policies))
	for _, policy := range Policies {
		names = append(names, string(policy))
	}
	return names
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

// Refuses reports a policy that refuses rather than moving or leaving
// anything, which is what a caller that never chose gets.
func (p Policy) Refuses() bool { return p != Follow && p != Skip }

// Split is held branches sorted by what a policy does with each: moved with
// its worktree, left where it is, or refused. Every held branch is in exactly
// one, in the order branches gives.
type Split struct {
	Follow, Leave, Refuse []Holder
}

// Split sorts the holders among branches by what this policy does with each.
// A caller with a rule of its own -- a branch it deletes, one another caller
// moves -- applies it to the answer.
func (p Policy) Split(held Held, branches []string) Split {
	var split Split
	for _, holder := range held.Of(branches) {
		switch {
		case p.Moves(holder):
			split.Follow = append(split.Follow, holder)
		case p.Leaves(holder):
			split.Leave = append(split.Leave, holder)
		default:
			split.Refuse = append(split.Refuse, holder)
		}
	}
	return split
}

// The ways out of a held branch, worded once for every command that offers
// them.
var (
	// SwitchAway frees the branch.
	SwitchAway = repair.Step{Effect: "switch that worktree to another branch, or close it"}
	// PutAway makes the worktree clean, so it can be brought along.
	PutAway = repair.Step{Effect: "commit or put those changes away there, then run this again"}
)

// Refusal is the refusal for branches a command will not move because other
// worktrees hold them, naming each and where.
//
// The ways out name no command, because the same refusal reaches commands that
// accept different scopes. It used to suggest g2g restack --scope path, which
// was the command that had just refused, and which pull does not accept.
func Refusal(holders []Holder) repair.Note {
	held := make([]string, 0, len(holders))
	for _, holder := range holders {
		held = append(held, fmt.Sprintf("%s (%s)", holder.Branch, holder.Path))
	}
	return repair.Note{
		Reason: fmt.Sprintf("checked out in another worktree: %s · moving it would leave that worktree describing a commit it no longer has", strings.Join(held, ", ")),
		Ways: []repair.Step{
			SwitchAway,
			{Effect: "select less with --branch or --scope, so nothing that has to move is checked out there"},
		},
	}
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
// so a repository with a dozen worktrees pays for the ones that matter, and a
// policy that refuses whatever it finds does not ask at all.
//
// A worktree whose cleanliness cannot be read counts as having changes: a
// directory that has gone, or a Git that fails there, is not one to move.
func Read(ctx context.Context, git any, policy Policy, branches []string) (Held, error) {
	reader, ok := git.(Reader)
	if !ok || len(branches) == 0 {
		return Held{}, nil
	}
	holders, err := reader.HoldersElsewhere(ctx)
	if err != nil {
		return nil, err
	}
	clean := cleanliness(ctx, reader)
	held := Held{}
	for _, branch := range branches {
		holder, taken := holders[branch]
		if !taken {
			continue
		}
		holder.Branch = branch
		holder.Clean = holder.Busy == "" && !policy.Refuses() && clean(holder.Path)
		held[branch] = holder
	}
	return held, nil
}

// cleanliness asks each worktree once, however many branches it holds, and
// answers false for one it could not ask.
func cleanliness(ctx context.Context, reader Reader) func(string) bool {
	known := map[string]bool{}
	return func(path string) bool {
		if clean, asked := known[path]; asked {
			return clean
		}
		clean, err := reader.WorktreeClean(ctx, path)
		if err != nil {
			clean = false
		}
		known[path] = clean
		return clean
	}
}

// Outcome is what a plan does about held branches: the ones it moves along
// with their worktrees, and the ones it leaves. Both are in branch order, so
// two plans that would do the same compare equal.
type Outcome struct {
	Follow []Holder
	Leave  []Holder
}

// NewOutcome orders what it is given, a branch named twice appearing once, so
// two plans that would do the same compare equal however they were assembled.
func NewOutcome(follow, leave []Holder) Outcome {
	return Outcome{Follow: sorted(follow), Leave: sorted(leave)}
}

// Equal compares two outcomes, cleanliness included, which is how a worktree
// that picked up changes between a preview and its apply is caught: the
// branch it holds goes from followed to left, and the plan is not the one
// that was previewed.
func (o Outcome) Equal(other Outcome) bool {
	return slices.Equal(o.Follow, other.Follow) && slices.Equal(o.Leave, other.Leave)
}

// With is this outcome and another. Each comes from a caller that decides
// only the branches it moves itself, so no branch is followed by one and left
// by the other; one followed by both is the same holder, named once.
func (o Outcome) With(other Outcome) Outcome {
	return NewOutcome(slices.Concat(o.Follow, other.Follow), slices.Concat(o.Leave, other.Leave))
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

func sorted(holders []Holder) []Holder {
	if len(holders) == 0 {
		return nil
	}
	byBranch := make(map[string]Holder, len(holders))
	for _, holder := range holders {
		byBranch[holder.Branch] = holder
	}
	ordered := make([]Holder, 0, len(byBranch))
	for _, branch := range slices.Sorted(maps.Keys(byBranch)) {
		ordered = append(ordered, byBranch[branch])
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
	// The other worktree may have switched branch since it was asked about,
	// and bringing it along then would lay this branch's change over
	// whatever it is on now.
	if reader, ok := git.(Reader); ok {
		holders, err := reader.HoldersElsewhere(ctx)
		if err != nil {
			return &NotBrought{Holder: holder, From: from, To: to, Err: err}
		}
		if holders[holder.Branch].Path != holder.Path {
			return nil
		}
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
