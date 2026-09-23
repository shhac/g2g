// Package reshape changes which branches a stack is made of: deleting one,
// folding one into the branch below it, and renaming one.
//
// It rewrites no commit. Deleting removes a ref, folding fast-forwards one,
// and renaming moves one; replaying commits onto a new parent is restack's
// alone, which is why a delete that drops a branch's commits from the
// branches above it says so and leaves the replay to restack.
//
// Each command acts on the g2g graph and refuses a branch it does not record.
// The graph is what says where a removed branch's children belong, and a
// branch nothing records has no such answer here.
package reshape

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/repair"
)

// Git is what reshaping needs from the repository. Every read is local: a
// remote-tracking ref says what this clone last saw of a remote, and nothing
// here asks the remote itself.
type Git interface {
	CurrentBranch(ctx context.Context) (string, error)
	LocalBranches(ctx context.Context) ([]string, error)
	Resolve(ctx context.Context, revision string) (string, error)
	IsAncestor(ctx context.Context, ancestor, descendant string) (bool, error)
	Cherry(ctx context.Context, upstream, head, limit string) (absent, present []string, err error)
	Absorbed(ctx context.Context, base, branch string) (bool, error)
	Unpublished(ctx context.Context, branch, since string) ([]git.Commit, error)
	RemoteTracking(ctx context.Context, branch string) ([]string, error)
	CheckedOutElsewhere(ctx context.Context) (map[string]string, error)
	CheckBranchName(ctx context.Context, name string) error

	SwitchExisting(ctx context.Context, branch string) error
	SwitchTree(ctx context.Context, from, to string) error
	MoveBranch(ctx context.Context, branch, from, to string) error
	RenameBranch(ctx context.Context, from, to string) error
	DeleteBranch(ctx context.Context, branch string) error
}

// Service reads the recorded graph through the graph service and writes it
// through the same store and fork-point pins that service uses.
type Service struct {
	Git   Git
	Graph graph.Service
}

// Ready reports a service with everything it needs, and is the rule the
// commands' registration uses.
func (s Service) Ready() bool {
	return s.Git != nil && s.Graph.Ready()
}

// unrecorded is the refusal every reshape shares: the g2g graph is what says
// where a branch sits, and it says nothing of this one.
func unrecorded(branch string, ways ...repair.Step) repair.Note {
	return repair.Note{
		Reason: fmt.Sprintf("the g2g graph does not record %s, so nothing says where it sits", branch),
		Ways:   append([]repair.Step{{Command: "g2g track --branch " + branch, Effect: "record it first"}}, ways...),
	}
}

func notLocal(branch string, ways ...repair.Step) repair.Note {
	return repair.Note{Reason: fmt.Sprintf("%s is not a local branch", branch), Ways: ways}
}

// heldElsewhere names the branches another worktree has checked out, among
// those an apply would move or remove.
func (s Service) heldElsewhere(ctx context.Context, branches ...string) ([]string, error) {
	elsewhere, err := s.Git.CheckedOutElsewhere(ctx)
	if err != nil {
		return nil, err
	}
	held := make([]string, 0)
	for _, branch := range branches {
		if path, taken := elsewhere[branch]; taken {
			held = append(held, fmt.Sprintf("%s (%s)", branch, path))
		}
	}
	return held, nil
}

func heldNote(held []string, consequence string) repair.Note {
	return repair.Note{
		Reason: "checked out in another worktree: " + strings.Join(held, ", ") + " · " + consequence,
		Ways:   []repair.Step{{Effect: "switch that worktree to another branch, or close it"}},
	}
}

// currentBranch is the branch checked out here, empty on a detached HEAD: an
// apply only needs it to know whether it is about to act on the checkout.
func (s Service) currentBranch(ctx context.Context) string {
	current, err := s.Git.CurrentBranch(ctx)
	if err != nil {
		return ""
	}
	return current
}

// undo is the steps that put back what an apply has done so far, run newest
// first.
type undo []func(context.Context) error

func (u undo) run(ctx context.Context) error {
	for _, step := range slices.Backward(u) {
		if err := step(ctx); err != nil {
			return err
		}
	}
	return nil
}

// RolledBack is an apply that failed part-way and put back what it had done.
// Left is what the rollback could not undo, nil when it undid everything.
type RolledBack struct {
	Operation string
	Branch    string
	Cause     error
	Left      error
}

func (r *RolledBack) Error() string {
	if r.Left != nil {
		return fmt.Sprintf("%s %s failed (%v), and putting it back did not finish: %v", r.Operation, r.Branch, r.Cause, r.Left)
	}
	return fmt.Sprintf("%s %s failed, so everything it had done was put back: %v", r.Operation, r.Branch, r.Cause)
}
func (r *RolledBack) Unwrap() error { return r.Cause }

// Stuck reports a rollback that could not finish, which leaves the repository
// part-way and is a different outcome from a clean failure.
func (r *RolledBack) Stuck() bool { return r.Left != nil }

// Partial is an apply that did what it was asked and then could not tidy up
// after itself. What it did stays; Left says what is still to do by hand.
type Partial struct {
	Done string
	Left string
	Err  error
}

func (p *Partial) Error() string { return fmt.Sprintf("%s, but %s: %v", p.Done, p.Left, p.Err) }
func (p *Partial) Unwrap() error { return p.Err }
