// Package undo puts back what the last recorded local operation changed.
//
// It is a mutation computed from a record, so it rests on something: every
// ref and every edge it would put back has to be where the operation left
// it, or the work done since would be what is undone. See
// design-docs/undo.md.
package undo

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/oplog"
	"github.com/shhac/g2g/internal/repair"
)

// Git is what undo reads and moves.
type Git interface {
	oplog.Reader
	CommonDir(ctx context.Context) (string, error)
	MoveRef(ctx context.Context, ref, to, from string) error
	// CheckedOutElsewhere maps each branch another worktree has checked out
	// to that worktree.
	CheckedOutElsewhere(ctx context.Context) (map[string]string, error)
	Clean(ctx context.Context) error
	SwitchBranch(ctx context.Context, branch string) error
	SwitchDetached(ctx context.Context, revision string) error
	SwitchTree(ctx context.Context, from, to string) error
	// Resolve is the commit a revision names, failing when it is not one
	// this repository has.
	Resolve(ctx context.Context, revision string) (string, error)
	// OnlyOn reports a branch holding commits no other branch, tag or
	// remote's copy reaches.
	OnlyOn(ctx context.Context, branch string) (bool, error)
}

// Service puts operations back.
type Service struct {
	Git   Git
	Store graph.Store
}

// Plan is what an undo would put back.
type Plan struct {
	// Entry is the operation, and Found whether there is one.
	Entry oplog.Entry
	Found bool
	// Here is where this checkout stands, and Returns whether the undo moves
	// it back to where the operation found it.
	Here    string
	Returns bool
	// Repair is why an apply would refuse and the ways out.
	Repair repair.Note
}

// Blocked is why an apply would refuse, as one sentence.
func (p Plan) Blocked() string { return p.Repair.Sentence() }

// Nothing reports that there is no operation to put back.
func (p Plan) Nothing() bool { return !p.Found }

// Equal compares what the undo would do.
func (p Plan) Equal(other Plan) bool {
	return p.Found == other.Found && p.Entry.At.Equal(other.Entry.At) && p.Here == other.Here &&
		p.Returns == other.Returns && p.Repair.Equal(other.Repair)
}

// Branches is the local branches the undo moves, creates or deletes, sorted.
func (p Plan) Branches() []string {
	branches := make([]string, 0)
	for ref := range p.Entry.Refs {
		if branch, local := strings.CutPrefix(ref, "refs/heads/"); local {
			branches = append(branches, branch)
		}
	}
	slices.Sort(branches)
	return branches
}

// log is where this repository's operations are recorded.
func (s Service) log(ctx context.Context) (string, error) {
	common, err := s.Git.CommonDir(ctx)
	if err != nil {
		return "", err
	}
	return oplog.Path(common), nil
}

// List is the recorded operations, newest first.
func (s Service) List(ctx context.Context) ([]oplog.Entry, error) {
	path, err := s.log(ctx)
	if err != nil {
		return nil, err
	}
	entries := oplog.Load(path).Entries
	slices.Reverse(entries)
	return entries, nil
}

// Plan is the newest operation not yet undone, and whether everything it
// changed is still where it left it.
func (s Service) Plan(ctx context.Context) (Plan, error) {
	path, err := s.log(ctx)
	if err != nil {
		return Plan{}, err
	}
	entry, published, found := oplog.Load(path).Latest(time.Now())
	if !found {
		return Plan{}, nil
	}
	now, err := oplog.Read(ctx, s.Git, s.Store)
	if err != nil {
		return Plan{}, err
	}
	plan := Plan{Entry: entry, Found: true, Here: now.Here}
	plan.Returns = entry.Here != nil && now.Here == entry.Here.After
	if changed := since(entry, now); changed != "" {
		plan.Repair = repair.Note{
			Reason: changed + ", so putting back what " + entry.Command + " changed would undo that as well",
			Ways:   []repair.Step{{Effect: "what it changed is listed above, with the commits each branch was at; git branch -f <branch> <commit> puts one back by hand"}},
		}
		return plan, nil
	}
	for _, branch := range plan.Branches() {
		if slices.Contains(published, branch) {
			plan.Repair = repair.Note{
				Reason: branch + " has been published since " + entry.Command + ", and putting it back here would leave the remote holding what this checkout no longer has",
				Ways:   []repair.Step{{Effect: "undo does not reach the remote; to go back anyway, move the branch by hand with git branch -f <branch> <commit> and publish that"}},
			}
			return plan, nil
		}
	}
	held, err := s.Git.CheckedOutElsewhere(ctx)
	if err != nil {
		return Plan{}, err
	}
	for _, branch := range plan.Branches() {
		if path, elsewhere := held[branch]; elsewhere {
			plan.Repair = repair.Note{
				Reason: fmt.Sprintf("%s is checked out in %s, and putting it back would leave that worktree's files behind", branch, repair.Quote(path)),
				Ways:   []repair.Step{{Effect: "switch that worktree to another branch, then undo again"}},
			}
			return plan, nil
		}
	}
	if standing, onBranch := strings.CutPrefix(plan.after(), "refs/heads/"); onBranch {
		if move, moved := entry.Refs["refs/heads/"+standing]; moved && move.Before == "" {
			plan.Repair = repair.Note{
				Reason: standing + " is checked out here, and undoing " + entry.Command + " removes it",
				Ways:   []repair.Step{{Effect: "switch to another branch, then undo again"}},
			}
			return plan, nil
		}
	}
	plan.Repair, err = s.kept(ctx, entry, now)
	return plan, err
}

// kept refuses an undo that would lose commits, or that Git no longer has
// what it needs for.
//
// A branch the operation created is removed, and one it created with a
// commit -- create -m, which commits what was staged -- then holds work that
// is on no other branch: removing it reported everything as it was before,
// with the file gone from the working tree and its commit on nothing. A
// branch it deleted or moved is put back at a commit, and that commit has to
// still exist: asked ref by ref, the first one missing stopped an undo
// half-way, with nothing able to finish it.
func (s Service) kept(ctx context.Context, entry oplog.Entry, now oplog.Reading) (repair.Note, error) {
	for _, ref := range slices.Sorted(maps.Keys(entry.Refs)) {
		move := entry.Refs[ref]
		branch, local := strings.CutPrefix(ref, "refs/heads/")
		if move.Before == "" && local && now.Refs[ref] == move.After {
			only, err := s.Git.OnlyOn(ctx, branch)
			if err != nil {
				return repair.Note{}, err
			}
			if only {
				return repair.Note{
					Reason: fmt.Sprintf("%s was created by %s and holds commits no other branch has, so removing it would leave them on nothing", branch, entry.Command),
					Ways: []repair.Step{
						{Command: repair.Command([]string{"git", "branch", branch + "-kept", branch}), Effect: "keep those commits under another name, then undo again"},
					},
				}, nil
			}
		}
		if move.Before == "" {
			continue
		}
		if _, err := s.Git.Resolve(ctx, move.Before); err != nil {
			if ctx.Err() != nil {
				return repair.Note{}, ctx.Err()
			}
			return repair.Note{Reason: fmt.Sprintf("the commit %s was at, %s, is no longer in this repository, so it cannot be put back", ref, short(move.Before))}, nil
		}
	}
	return repair.Note{}, nil
}

// after is where this checkout will stand once the undo has moved it, if it
// does.
func (p Plan) after() string {
	if p.Returns {
		return p.Entry.Here.Before
	}
	return p.Here
}

// since names the first thing the operation changed that is no longer as it
// left it, empty when everything still is.
func since(entry oplog.Entry, now oplog.Reading) string {
	for _, ref := range slices.Sorted(maps.Keys(entry.Refs)) {
		move, is := entry.Refs[ref], now.Refs[ref]
		// Where the operation left it, or already where it is going: an undo
		// that stopped part-way has put some of these back, and has to be
		// able to finish.
		if is == move.After || is == move.Before {
			continue
		}
		name := ref
		if branch, local := strings.CutPrefix(ref, "refs/heads/"); local {
			name = branch
		}
		switch {
		case is == "":
			return name + " has been deleted since"
		case move.After == "":
			return name + " has been created again since"
		default:
			return fmt.Sprintf("%s has moved since, from %s to %s", name, short(move.After), short(is))
		}
	}
	for _, branch := range slices.Sorted(maps.Keys(entry.Tips)) {
		if _, moved := entry.Refs["refs/heads/"+branch]; moved {
			continue
		}
		if left, is := entry.Tips[branch], now.Refs["refs/heads/"+branch]; left != is {
			if is == "" {
				return branch + " has been deleted since"
			}
			return fmt.Sprintf("%s has moved since, from %s to %s", branch, short(left), short(is))
		}
	}
	for _, branch := range slices.Sorted(maps.Keys(entry.Edges)) {
		is, recorded := now.Graph.Edges[branch]
		if !same(entry.Edges[branch].After, is, recorded) && !same(entry.Edges[branch].Before, is, recorded) {
			return "what the graph records of " + branch + " has changed since"
		}
	}
	if entry.RootsAfter != nil && !rootsAre(entry.RootsAfter, now.Graph) && !rootsAre(entry.RootsBefore, now.Graph) {
		return "the recorded trunks have changed since"
	}
	return ""
}

// same reports a record that is what an entry says it was: both absent, or
// the same edge.
func same(was *graph.Edge, is graph.Edge, recorded bool) bool {
	if was == nil {
		return !recorded
	}
	return recorded && *was == is
}

func rootsAre(roots *oplog.Roots, recorded graph.Graph) bool {
	return slices.Equal(roots.Trunks, recorded.Trunks) && maps.Equal(roots.Declared, recorded.Declared)
}

func short(object string) string {
	if len(object) <= 12 {
		return object
	}
	return object[:12]
}

// Stopped is an undo that put some things back and then could not go on.
type Stopped struct {
	Restored []string
	Err      error
}

func (e *Stopped) Error() string { return e.Err.Error() }
func (e *Stopped) Unwrap() error { return e.Err }

// Apply puts the operation back: the checkout where it stood when that can
// come first, then each ref over exactly what the operation left there, then
// the record, and marks the operation undone.
//
// The refs move one at a time, each only from where the operation left it.
// Everything that could refuse has been asked before the first one moves, and
// a ref already where it is going is left, so an undo that does stop
// part-way is finished by running it again.
func (s Service) Apply(ctx context.Context, plan Plan) error {
	if plan.Blocked() != "" {
		return fmt.Errorf("cannot undo: %s", plan.Blocked())
	}
	if !plan.Found {
		return nil
	}
	entry := plan.Entry
	now, err := s.Git.OwnRefs(ctx)
	if err != nil {
		return err
	}
	standing := strings.TrimPrefix(plan.Here, "refs/heads/")
	if move, moves := entry.Refs["refs/heads/"+standing]; (moves && now["refs/heads/"+standing] != move.Before) || plan.Returns {
		// Putting back the branch that is checked out moves its files, and
		// so does returning the checkout to where it stood.
		if err := s.Git.Clean(ctx); err != nil {
			return err
		}
	}
	back, detached := "", false
	if plan.Returns {
		back, detached = entry.Here.Before, !strings.HasPrefix(entry.Here.Before, "refs/heads/")
		back = strings.TrimPrefix(back, "refs/heads/")
	}
	returned := false
	returning := func() error {
		if detached {
			return s.Git.SwitchDetached(ctx, back)
		}
		return s.Git.SwitchBranch(ctx, back)
	}
	if back != "" {
		// Before the refs when what it returns to is there already, which is
		// what lets a branch the operation created be removed.
		if _, exists := now["refs/heads/"+back]; exists || detached {
			if err := returning(); err != nil {
				return err
			}
			returned = true
			if !detached {
				standing = back
			} else {
				standing = ""
			}
		}
	}
	restored := make([]string, 0, len(entry.Refs))
	stop := func(err error) error {
		if len(restored) == 0 && !returned {
			return err
		}
		return &Stopped{Restored: restored, Err: err}
	}
	for _, ref := range slices.Sorted(maps.Keys(entry.Refs)) {
		move := entry.Refs[ref]
		if now[ref] == move.Before {
			continue
		}
		checkedOut := standing != "" && ref == "refs/heads/"+standing && move.Before != "" && move.After != ""
		if checkedOut {
			// The files first: they are what can refuse, and refusing
			// before the ref has moved is nothing having happened.
			if err := s.Git.SwitchTree(ctx, move.After, move.Before); err != nil {
				return stop(err)
			}
		}
		if err := s.Git.MoveRef(ctx, ref, move.Before, move.After); err != nil {
			if checkedOut {
				if restoreErr := s.Git.SwitchTree(ctx, move.Before, move.After); restoreErr != nil {
					err = fmt.Errorf("%w; the working tree was also left at %s and could not be put back: %v", err, short(move.Before), restoreErr)
				}
			}
			return stop(err)
		}
		restored = append(restored, ref)
	}
	if err := s.record(ctx, entry); err != nil {
		return stop(err)
	}
	if back != "" && !returned {
		if err := returning(); err != nil {
			return stop(err)
		}
	}
	path, err := s.log(ctx)
	if err != nil {
		return stop(err)
	}
	if err := oplog.MarkUndone(path, entry.At); err != nil {
		return stop(err)
	}
	return nil
}

// record puts back what the graph said of each branch the operation changed,
// in the record as it is now: branches recorded since by other work are not
// its to touch.
func (s Service) record(ctx context.Context, entry oplog.Entry) error {
	if len(entry.Edges) == 0 && entry.RootsBefore == nil {
		return nil
	}
	recorded, err := s.Store.Load(ctx)
	if err != nil {
		return err
	}
	restored := recorded.Clone()
	if restored.Edges == nil {
		restored.Edges = map[string]graph.Edge{}
	}
	for branch, was := range entry.Edges {
		if was.Before == nil {
			delete(restored.Edges, branch)
			continue
		}
		restored.Edges[branch] = *was.Before
	}
	if entry.RootsBefore != nil {
		restored.Trunks = slices.Clone(entry.RootsBefore.Trunks)
		restored.Declared = maps.Clone(entry.RootsBefore.Declared)
	}
	return s.Store.Save(ctx, restored)
}

// Record writes down what an operation changed, from a reading before it and
// one after. Nothing is written for one that changed nothing.
func Record(ctx context.Context, git interface {
	CommonDir(ctx context.Context) (string, error)
}, command, worktree string, before, after oplog.Reading, touched func(string) bool, now time.Time) error {
	entry := oplog.Between(before, after, touched)
	if entry.Empty() {
		return nil
	}
	entry.At, entry.Command, entry.Worktree = now, command, worktree
	common, err := git.CommonDir(ctx)
	if err != nil {
		return err
	}
	return oplog.Append(oplog.Path(common), entry, now)
}

// Publishing writes down that branches are about to be sent to a remote, so
// that an undo of something earlier knows they have been seen there. It is
// written before the publication, not after: one that stops part-way has
// still published, and refusing an undo that might have been safe is the
// side to err on.
func Publishing(ctx context.Context, git interface {
	CommonDir(ctx context.Context) (string, error)
}, command, worktree string, branches []string, now time.Time) error {
	if len(branches) == 0 {
		return nil
	}
	common, err := git.CommonDir(ctx)
	if err != nil {
		return err
	}
	return oplog.Append(oplog.Path(common), oplog.Entry{At: now, Command: command, Worktree: worktree, Published: branches}, now)
}
