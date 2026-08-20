// Package stack resolves the read-only picture every stack-oriented command
// starts from: a safe, local Graphite path and the GitHub pull requests on it.
//
// This lives here rather than in any one command's package so that link, sync,
// status, unlink and submit can share it without depending on each other.
package stack

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"

	"github.com/shhac/g2g/internal/diagnostic"
	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/graphite"

	"github.com/shhac/g2g/internal/subprocess"
)

// Git supplies local repository facts without changing checkout state.
type Git interface {
	CurrentBranch(context.Context) (string, error)
	LocalBranches(context.Context) ([]string, error)
}

// Graphite reads declared structure without checking out a branch.
//
// One read, because there is one question: what does Graphite declare. How much
// of that a command acts on is a scope, applied here, rather than a shape
// Graphite is asked to produce.
type Graphite interface {
	ReadForest(context.Context) (graphite.Forest, error)
}

// GitHub reads the pull requests on a discovered path. Inspect is read-only.
type GitHub interface {
	Inspect(context.Context, []string) ([]githubstack.PullRequest, error)
}

// Discovery is the complete read-only picture: the Graphite-declared path plus
// the GitHub pull requests on it. Commands add their own policy on top; none
// of them need to re-derive these facts.
type Discovery struct {
	Snapshot
	PullRequests []githubstack.PullRequest
}

// Equal reports whether two snapshots describe the same Graphite path.
// Revalidation compares this immediately before a mutation, so every fact that
// could change what the command does belongs here — including the declared
// ancestry above the base, which can move without altering Branches.
func (s Snapshot) Equal(other Snapshot) bool {
	return s.Target == other.Target &&
		s.TargetSource == other.TargetSource &&
		s.Source == other.Source &&
		s.Base == other.Base &&
		s.BaseSource == other.BaseSource &&
		s.Scope == other.Scope &&
		maps.Equal(s.Parents, other.Parents) &&
		slices.Equal(s.Absent, other.Absent) &&
		slices.Equal(s.Unfollowed, other.Unfollowed) &&
		slices.Equal(s.Ancestry, other.Ancestry) &&
		slices.Equal(s.Branches, other.Branches)
}

// Equal reports whether two discoveries describe the same world.
func (d Discovery) Equal(other Discovery) bool {
	return d.Snapshot.Equal(other.Snapshot) && slices.Equal(d.PullRequests, other.PullRequests)
}

// PathSelector produces the ordered path a command acts on. Resolver is the
// production implementation; a command needs only this much of it.
type PathSelector interface {
	Select(ctx context.Context, selection Selection, command string) (Snapshot, error)
}

// Discover resolves the selected path through whichever source describes it,
// and reads its pull requests, without checking out a branch or mutating
// anything.
func Discover(ctx context.Context, selector PathSelector, github GitHub, selection Selection, command string) (Discovery, error) {
	if selector == nil || github == nil {
		return Discovery{}, fmt.Errorf("stack discovery is not fully configured")
	}
	snapshot, err := selector.Select(ctx, selection, command)
	if err != nil {
		return Discovery{}, err
	}
	diagnostic.Event(ctx, "discovery.target", diagnostic.Field{Key: "target", Value: snapshot.Target}, diagnostic.Field{Key: "source", Value: snapshot.TargetSource})
	diagnostic.Event(ctx, "discovery.trunk", diagnostic.Field{Key: "trunk", Value: snapshot.Base}, diagnostic.Field{Key: "source", Value: snapshot.BaseSource}, diagnostic.Field{Key: "structure", Value: string(snapshot.Source)}, diagnostic.Field{Key: "path_branches", Value: strings.Join(snapshot.Branches, ",")})
	prs, err := github.Inspect(ctx, snapshot.Branches)
	if err != nil {
		return Discovery{}, err
	}
	diagnostic.Event(ctx, "github.native_stack_membership", diagnostic.Field{Key: "observation", Value: "per_pull_request"})
	return Discovery{Snapshot: snapshot, PullRequests: prs}, nil
}

// Selection captures every no-checkout path selector shared by stack commands.
type Selection struct {
	Branch string
	Trunk  string
	// Scope is how much of the structure to select. Empty means the whole
	// stack: the trunk beneath the target, the target, and everything above it.
	Scope Scope
	// From pins which source answers, for this invocation only. Empty means
	// precedence decides, which is the normal case. Nothing is recorded: once
	// a branch is in the g2g store there is otherwise no way to ask Graphite
	// what it thinks, and comparing the two views is the whole point.
	From Source
}

// Snapshot is the validated ordered path a command acts on, whichever source
// described it.
type Snapshot struct {
	Target       string
	TargetSource string
	// Ancestry is the whole line of descent to the target, base included,
	// whether or not the selection acts on all of it. It exists for
	// revalidation, which has to notice the structure above the base moving
	// even when Branches is unchanged — so every source fills it, and one that
	// did not would revalidate more weakly than the others without saying so.
	Ancestry   []string
	Base       string
	BaseSource string
	Branches   []string
	// Parents carries the shape that Branches alone cannot express, restricted
	// to the selection: a branch whose parent lies outside it is absent, which
	// is how a renderer knows the selection's own roots.
	//
	// A linear selection leaves this nil. Every consumer that predates forked
	// selection reads Branches and is unaffected.
	Parents map[string]string
	// Scope is what was actually selected, so a renderer can say how much of
	// the structure it is showing.
	Scope Scope
	// Absent are selected branches that are not on this machine. Only the pull
	// request source produces them: a stack published from another checkout can
	// join two local subtrees through a branch nobody here has, and dropping
	// that edge makes the two look unrelated.
	//
	// They are structure and they are not somewhere a command may act, so every
	// command that mutates refuses on them rather than quietly skipping them.
	Absent []string
	// Unfollowed are bases the structure stops short of, because following them
	// ran out of rounds. Reported so a tree that ends early cannot be mistaken
	// for one that is finished.
	Unfollowed []string
	// Source names where the structure came from, so a preview can say.
	Source Source
}

// ParentOf reports the selected parent of a branch. A linear selection records
// no edges, so it answers false for everything and any renderer asking about
// shape gets the same answer it did before scopes existed.
func (s Snapshot) ParentOf(branch string) (string, bool) {
	parent, within := s.Parents[branch]
	if !within || parent == "" {
		return "", false
	}
	return parent, true
}

// Forks reports whether any selected branch has two selected children.
//
// A GitHub native stack is linear, so this is the question every projecting
// command has to ask before it can act. It is deliberately not asked during
// selection: reading a fork is fine and is the ordinary case on a trunk, and
// refusing it there was what made status unusable from one.
func (s Snapshot) Forks() bool {
	children := make(map[string]int, len(s.Parents))
	for _, parent := range s.Parents {
		children[parent]++
		if children[parent] > 1 {
			return true
		}
	}
	return false
}

// RequireLinear refuses a selection a linear projection cannot represent, and
// names the remedy: a leaf has no descendants, so selecting one collapses the
// scope to an ordered path without needing a different scope at all.
func (s Snapshot) RequireLinear(what string) error {
	if !s.Forks() {
		return nil
	}
	return fmt.Errorf("%s needs one ordered path and %q has more than one branch above it · select a leaf with --branch, or narrow with --scope path", what, s.forkedAt())
}

// forkedAt names a branch the selection divides at, so the message points at
// something the reader can act on.
func (s Snapshot) forkedAt() string {
	children := make(map[string]int, len(s.Parents))
	forks := make([]string, 0)
	for _, parent := range s.Parents {
		children[parent]++
		if children[parent] == 2 {
			forks = append(forks, parent)
		}
	}
	sort.Strings(forks)
	if len(forks) == 0 {
		return s.Target
	}
	return forks[0]
}

// Resolve selects a local Graphite path without checkout. command names the
// consumer's action in an option-like branch safety error.
var errNotConfigured = fmt.Errorf("stack resolver is not fully configured")

// EffectiveScope is the scope this selection means.
//
// It is exported because every selector has to honour it. A selector that
// resolves its own scope, or ignores the one it was handed, makes a flag mean
// something different depending on which record answered.
//
// An unset scope is the whole stack rather than the ancestry, because that is
// what someone standing mid-stack means: where am I, what is under me, what is
// above me. A command that wants something narrower says so.
func (s Selection) EffectiveScope() Scope {
	if s.Scope != "" {
		return s.Scope
	}
	return ScopeStack
}

func resolveTarget(ctx context.Context, git Git, requestedBranch string) (string, string, error) {
	if requestedBranch != "" {
		return requestedBranch, "--branch", nil
	}
	target, err := git.CurrentBranch(ctx)
	if err != nil {
		return "", "", err
	}
	return target, "current Git branch", nil
}

func branchSet(branches []string) map[string]bool {
	set := make(map[string]bool, len(branches))
	for _, branch := range branches {
		set[branch] = true
	}
	return set
}

// validateSelectionIsSafe refuses a name a process would read as an option.
//
// It is separate from the locality check because the pull request source has a
// third answer: a branch can be legitimately absent and still be structure. The
// safety rule applies to every name regardless, which is why it splits out
// rather than being repeated.
func validateSelectionIsSafe(branches []string, command string) error {
	for _, branch := range branches {
		if subprocess.OptionLike(branch) {
			return fmt.Errorf("selected branch %q cannot be passed safely to %s", branch, command)
		}
	}
	return nil
}

// RequireActionable refuses a selection containing branches this machine does
// not have.
//
// Reading a structure through pull request bases can place a branch nobody here
// has checked out, which is the point: it is what joins two local subtrees
// published from somebody else's machine. Acting on one is a different matter —
// there is no ref to push, rewrite, or link — so every command that mutates
// asks this first, and says which branches rather than failing at the tool.
func (s Snapshot) RequireActionable(command string) error {
	if len(s.Absent) == 0 {
		return nil
	}
	return fmt.Errorf("%s cannot act on %s: not %s on this machine · the structure came from pull request bases, which describe branches this checkout does not have",
		command, strings.Join(s.Absent, ", "), pluralBranches(len(s.Absent)))
}

func pluralBranches(count int) string {
	if count == 1 {
		return "a local branch"
	}
	return "local branches"
}
