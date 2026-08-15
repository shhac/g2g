// Package stack resolves the read-only picture every stack-oriented command
// starts from: a safe, local Graphite path and the GitHub pull requests on it.
//
// This lives here rather than in any one command's package so that link, sync,
// status, unlink and submit can share it without depending on each other.
package stack

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/shhac/gt2gh/internal/diagnostic"
	"github.com/shhac/gt2gh/internal/githubstack"
	"github.com/shhac/gt2gh/internal/graphite"
)

// Git supplies local repository facts without changing checkout state.
type Git interface {
	CurrentBranch(context.Context) (string, error)
	LocalBranches(context.Context) ([]string, error)
}

// Graphite discovers a declared path without checking out a branch.
type Graphite interface {
	DiscoverStack(context.Context, string, bool) (graphite.Stack, error)
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

// Discover resolves the selected path and reads its pull requests, without
// checking out a branch or mutating anything.
func Discover(ctx context.Context, git Git, graphiteClient Graphite, github GitHub, selection Selection, command string) (Discovery, error) {
	if github == nil {
		return Discovery{}, fmt.Errorf("stack discovery is not fully configured")
	}
	snapshot, err := Resolve(ctx, git, graphiteClient, selection, command)
	if err != nil {
		return Discovery{}, err
	}
	diagnostic.Event(ctx, "discovery.target", diagnostic.Field{Key: "target", Value: snapshot.Target}, diagnostic.Field{Key: "source", Value: snapshot.TargetSource})
	diagnostic.Event(ctx, "discovery.trunk", diagnostic.Field{Key: "trunk", Value: snapshot.Base}, diagnostic.Field{Key: "source", Value: snapshot.BaseSource}, diagnostic.Field{Key: "path_branches", Value: strings.Join(snapshot.Branches, ",")})
	prs, err := github.Inspect(ctx, snapshot.Branches)
	if err != nil {
		return Discovery{}, err
	}
	diagnostic.Event(ctx, "github.native_stack_membership", diagnostic.Field{Key: "observation", Value: "per_pull_request"})
	return Discovery{Snapshot: snapshot, PullRequests: prs}, nil
}

// Selection captures every no-checkout path selector shared by stack commands.
type Selection struct {
	Branch  string
	Trunk   string
	NoStack bool
}

// Snapshot contains the validated Graphite facts common to link, sync, and push.
type Snapshot struct {
	Target       string
	TargetSource string
	GraphitePath []string
	Base         string
	BaseSource   string
	Branches     []string
}

// Resolve selects a local Graphite path without checkout. command names the
// consumer's action in an option-like branch safety error.
func Resolve(ctx context.Context, git Git, graphiteClient Graphite, selection Selection, command string) (Snapshot, error) {
	if git == nil || graphiteClient == nil {
		return Snapshot{}, fmt.Errorf("stack resolver is not fully configured")
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

	declared, err := graphiteClient.DiscoverStack(ctx, target, !selection.NoStack)
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
	if len(path) < 2 {
		return fmt.Errorf("selected branch has no Graphite ancestor that can be used as a link base")
	}
	for _, branch := range path {
		if !local[branch] {
			return fmt.Errorf("Graphite ancestry branch %q is not a local branch", branch)
		}
		if strings.HasPrefix(branch, "-") {
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
	if len(indices) == 1 {
		for trunk, index := range indices {
			return trunk, "Graphite-declared ancestry", append([]string(nil), path[index+1:]...), nil
		}
	}
	if len(indices) == 0 {
		return "", "", nil, fmt.Errorf("selected Graphite ancestry %q has no declared trunk; use supported Graphite configuration to resolve it", strings.Join(path, " -> "))
	}
	candidates := make([]string, 0, len(indices))
	for trunk := range indices {
		candidates = append(candidates, trunk)
	}
	sort.Strings(candidates)
	return "", "", nil, fmt.Errorf("selected Graphite ancestry has multiple declared trunks (%s); rerun with --trunk <branch>", strings.Join(candidates, ", "))
}
