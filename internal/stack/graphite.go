package stack

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// This file is the Graphite source: everything that turns what Graphite
// declares into a Snapshot, and nothing else.
//
// It sits beside g2g.go and pullrequest.go, which own their sources the same
// way. It was in stack.go, the file holding the vocabulary every command
// shares, because Graphite was the original source and the siblings grew up
// around it — so the one file named for the package was the one file describing
// a particular record.

// GraphiteSelector describes branches Graphite declares.
type GraphiteSelector struct {
	Git      Git
	Graphite Graphite
	// Configured reports whether this repository already uses Graphite. It is
	// asked before anything else, because Graphite's discovery command creates
	// state in a repository that has never used it — so a repository that
	// deliberately has no Graphite would be enrolled into it merely by being
	// asked a question.
	Configured func(ctx context.Context) (bool, error)
}

func (s GraphiteSelector) Source() Source { return SourceGraphite }

// Describes reports whether Graphite is in use here at all. Whether it knows
// this particular branch is left to Select, which is the call that has to run
// Graphite anyway.
func (s GraphiteSelector) Describes(ctx context.Context, _ string) (bool, error) {
	if s.Git == nil || s.Graphite == nil {
		return false, nil
	}
	if s.Configured == nil {
		return true, nil
	}
	return s.Configured(ctx)
}

func (s GraphiteSelector) Select(ctx context.Context, selection Selection, command string) (Snapshot, error) {
	return resolveGraphiteSelection(ctx, s.Git, s.Graphite, selection, command)
}

func resolveGraphiteSelection(ctx context.Context, git Git, graphiteClient Graphite, selection Selection, command string) (Snapshot, error) {
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

	scope := selection.EffectiveScope()
	return resolveGraphite(ctx, graphiteClient, target, source, scope, selection.Trunk, local, command)
}

// resolveGraphite answers any scope from Graphite's whole declared forest.
//
// Selection used to go through DiscoverStack(target, includeTip bool), and one
// bool cannot carry six scopes. branch mapped to includeTip=false, which only
// suppresses descendants and therefore returned the whole ancestry — so branch
// and path both meant something other than their own documentation whenever
// Graphite answered. The forest was always parsed; only a linear walk over it
// was ever exposed.
func resolveGraphite(ctx context.Context, graphiteClient Graphite, target, source string, scope Scope, requestedTrunk string, local map[string]bool, command string) (Snapshot, error) {
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

	base, baseSource, branches, err := graphiteBoundary(forest, declared.Roots, ancestry, selected, target, scope, requestedTrunk)
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{
		Target:       target,
		TargetSource: source,
		Ancestry:     ancestry,
		Base:         base,
		BaseSource:   baseSource,
		Branches:     branches,
		Parents:      forest.Restrict(selected),
		Scope:        scope,
	}, nil
}

// graphiteBoundary decides what the selection hangs from.
//
// A scope rooted at the target hangs from the target's own parent. A scope that
// reaches the trunk hangs from a declared trunk, which is where --trunk applies
// and where Graphite's ambiguity lives: a declared trunk can sit part-way up an
// ancestry, so which one is the base is a real question and not one to guess.
func graphiteBoundary(forest Forest, declaredTrunks, ancestry, selected []string, target string, scope Scope, requestedTrunk string) (string, string, []string, error) {
	// Hangs fails for an empty selection as well as for a target-rooted scope
	// with no parent, and both are refusals rather than something to carry on
	// from. The earlier form tested the scope again here, which was already
	// implied by !within, and let an empty selection fall through to indexing
	// it.
	base, within, err := forest.Hangs(selected, target, scope)
	if err != nil {
		return "", "", nil, err
	}
	if !within {
		if requestedTrunk != "" && requestedTrunk != base {
			return "", "", nil, fmt.Errorf("requested trunk %q is not %q's parent (%s); a scope rooted at the branch hangs from its parent", requestedTrunk, target, base)
		}
		return base, "Graphite-declared parent", append([]string(nil), selected...), nil
	}
	if scope == ScopeAll {
		// Several trunks, so there is no single one to hang from: the first
		// root renders as the base and the rest render as trunks in their own
		// right.
		return selected[0], "Graphite-declared roots", append([]string(nil), selected[1:]...), nil
	}
	if len(ancestry) == 1 {
		// The target is itself a trunk, so there is nothing above it to choose
		// between and Hangs has already given the answer. SelectBoundary
		// cannot: it scans the ancestry excluding the target precisely because
		// it exists to find the trunk *under* one, so asked from a trunk it can
		// only report that there is no ancestor to use.
		if !slices.Contains(declaredTrunks, target) {
			return "", "", nil, fmt.Errorf("selected branch %q is a root of the Graphite forest but is not a declared trunk; use supported Graphite configuration to resolve it", target)
		}
		if requestedTrunk != "" && requestedTrunk != target {
			return "", "", nil, fmt.Errorf("requested trunk %q is not %q, which is the trunk this selection starts from", requestedTrunk, target)
		}
		return base, "Graphite-declared trunk", append([]string(nil), selected[1:]...), nil
	}

	base, baseSource, _, err := SelectBoundary(ancestry, declaredTrunks, requestedTrunk)
	if err != nil {
		return "", "", nil, err
	}
	// The base is on the ancestry and the ancestry opens the selection, so
	// everything the command acts on is what follows it.
	for index, branch := range selected {
		if branch == base {
			return base, baseSource, append([]string(nil), selected[index+1:]...), nil
		}
	}
	return "", "", nil, fmt.Errorf("selected base %q is not part of the selection", base)
}

// validateBranchesLocalAndSafe refuses a selection a command could not act on.
//
// It names no source, because more than one reaches it: a pull request base
// can put a branch here just as a Graphite ancestry can, and telling someone
// Graphite selected a branch it did not sends them to the wrong record.
func validateBranchesLocalAndSafe(local map[string]bool, branches []string, command string) error {
	for _, branch := range branches {
		if !local[branch] {
			return fmt.Errorf("selected branch %q is not a local branch", branch)
		}
	}
	return validateSelectionIsSafe(branches, command)
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
