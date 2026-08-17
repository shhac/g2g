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

// Graphite discovers declared structure without checking out a branch.
//
// Two reads rather than one, because the questions genuinely differ: a linear
// scope wants the one ancestry a projection consumes, and a forked scope wants
// the shape, which no ordered path can express.
type Graphite interface {
	DiscoverStack(context.Context, string, bool) (graphite.Stack, error)
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
		slices.Equal(s.GraphitePath, other.GraphitePath) &&
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
	// NoStack is the original binary form of Scope, kept because it is a
	// published flag. It means ScopeBranch and nothing else.
	NoStack bool
	// Scope is how much of the structure to select. Empty means the default,
	// which is the linear path every projection consumes, so a command that
	// never sets it behaves exactly as it did before scopes existed.
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
	// GraphitePath is the full declared ancestry including the base. Only a
	// Graphite selection fills it; it exists for revalidation, which must
	// notice ancestry moving even when Branches does not change.
	GraphitePath []string
	Base         string
	BaseSource   string
	Branches     []string
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
	// Source names where the structure came from, so a preview can say.
	Source Source
}

// Forked reports whether the selection is a shape rather than a path.
func (s Snapshot) Forked() bool { return len(s.Parents) != 0 }

// Resolve selects a local Graphite path without checkout. command names the
// consumer's action in an option-like branch safety error.
var errNotConfigured = fmt.Errorf("stack resolver is not fully configured")

func Resolve(ctx context.Context, git Git, graphiteClient Graphite, selection Selection, command string) (Snapshot, error) {
	if git == nil || graphiteClient == nil {
		return Snapshot{}, errNotConfigured
	}
	target, source, err := resolveTarget(ctx, git, selection.Branch)
	if err != nil {
		return Snapshot{}, err
	}
	localBranches, err := git.LocalBranches(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	local := branchSet(localBranches)
	if !local[target] {
		return Snapshot{}, fmt.Errorf("selected branch %q is not a local branch", target)
	}

	scope := selection.scope()
	if !scope.Linear() {
		return resolveForked(ctx, graphiteClient, target, source, scope, local, command)
	}

	declared, err := graphiteClient.DiscoverStack(ctx, target, scope != ScopeBranch)
	if err != nil {
		return Snapshot{}, err
	}
	if err := validatePathLocalAndSafe(local, declared.Path, command); err != nil {
		return Snapshot{}, err
	}
	base, baseSource, branches, err := SelectBoundary(declared.Path, declared.Trunks, selection.Trunk)
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{
		Target:       target,
		TargetSource: source,
		GraphitePath: append([]string(nil), declared.Path...),
		Base:         base,
		BaseSource:   baseSource,
		Branches:     branches,
		Scope:        scope,
	}, nil
}

// scope reconciles the scope flag with the older boolean. --no-stack predates
// scopes and means exactly ScopeBranch, so it is translated rather than
// consulted separately; nothing downstream should have to know both exist.
func (s Selection) scope() Scope {
	if s.Scope != "" {
		return s.Scope
	}
	if s.NoStack {
		return ScopeBranch
	}
	return ScopePath
}

// resolveForked answers a scope that can fork, from Graphite's whole declared
// forest rather than one walk down it.
//
// The base here is the selection's own root, not a declared trunk. A subtree
// taken from halfway up a stack legitimately hangs from a branch that is nobody's
// trunk, and demanding a declared trunk would refuse exactly the selection the
// user asked for.
func resolveForked(ctx context.Context, graphiteClient Graphite, target, source string, scope Scope, local map[string]bool, command string) (Snapshot, error) {
	declared, err := graphiteClient.ReadForest(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	if _, known := declared.Parents[target]; !known {
		return Snapshot{}, fmt.Errorf("Graphite does not track local branch %q", target)
	}
	forest := Forest{Parents: declared.Parents}
	selected, err := forest.Select(target, scope)
	if err != nil {
		return Snapshot{}, err
	}
	if err := validateBranchesLocalAndSafe(local, selected, command); err != nil {
		return Snapshot{}, err
	}
	ancestry, err := forest.Path(target)
	if err != nil {
		return Snapshot{}, err
	}
	base, branches := selected[0], append([]string(nil), selected[1:]...)
	return Snapshot{
		Target:       target,
		TargetSource: source,
		GraphitePath: ancestry,
		Base:         base,
		BaseSource:   "selection root",
		Branches:     branches,
		Parents:      forest.Restrict(selected),
		Scope:        scope,
	}, nil
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

func validatePathLocalAndSafe(local map[string]bool, path []string, command string) error {
	// A path needs a branch above its base to be a link base at all. A forked
	// selection has no such requirement: one branch with no descendants is a
	// legitimate subtree, and refusing it here would refuse the honest answer.
	if len(path) < 2 {
		return fmt.Errorf("selected branch has no Graphite ancestor that can be used as a link base")
	}
	return validateBranchesLocalAndSafe(local, path, command)
}

func validateBranchesLocalAndSafe(local map[string]bool, branches []string, command string) error {
	for _, branch := range branches {
		if !local[branch] {
			return fmt.Errorf("Graphite ancestry branch %q is not a local branch", branch)
		}
		if subprocess.OptionLike(branch) {
			return fmt.Errorf("Graphite ancestry branch %q cannot be passed safely to %s", branch, command)
		}
	}
	return nil
}

// SelectBoundary chooses only among declared trunk candidates on the selected
// ancestry. It never guesses by branch name.
func SelectBoundary(path, trunks []string, requestedTrunk string) (string, string, []string, error) {
	if len(path) < 2 {
		return "", "", nil, fmt.Errorf("selected branch has no Graphite ancestor that can be used as a link base")
	}
	declared := make(map[string]bool, len(trunks))
	for _, trunk := range trunks {
		declared[trunk] = true
	}
	indices := make(map[string]int)
	for index, branch := range path[:len(path)-1] {
		if declared[branch] {
			indices[branch] = index
		}
	}
	// Guard clauses in the order a reader asks the questions: was one chosen,
	// is there none, is there more than one, and only then the single case.
	if requestedTrunk != "" {
		index, valid := indices[requestedTrunk]
		if !valid {
			if !declared[requestedTrunk] {
				return "", "", nil, fmt.Errorf("requested trunk %q is not a Graphite-declared trunk", requestedTrunk)
			}
			return "", "", nil, fmt.Errorf("requested trunk %q is not an ancestor of selected branch %q", requestedTrunk, path[len(path)-1])
		}
		return requestedTrunk, "--trunk", append([]string(nil), path[index+1:]...), nil
	}
	if len(indices) == 0 {
		return "", "", nil, fmt.Errorf("selected Graphite ancestry %q has no declared trunk; use supported Graphite configuration to resolve it", strings.Join(path, " -> "))
	}
	if len(indices) > 1 {
		candidates := slices.Sorted(maps.Keys(indices))
		return "", "", nil, fmt.Errorf("selected Graphite ancestry has multiple declared trunks (%s); rerun with --trunk <branch>", strings.Join(candidates, ", "))
	}
	trunk := slices.Collect(maps.Keys(indices))[0]
	return trunk, "Graphite-declared ancestry", append([]string(nil), path[indices[trunk]+1:]...), nil
}
