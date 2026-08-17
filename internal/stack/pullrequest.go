package stack

import (
	"context"
	"fmt"

	"github.com/shhac/g2g/internal/diagnostic"
	"github.com/shhac/g2g/internal/githubstack"
)

// PullRequestSelector describes branches by the base of their open pull
// request.
//
// This is the third source source-resolution.md names, and the only one that
// reports *effect* rather than intent: Graphite and the g2g store say what was
// meant, a pull request base says what a merge will actually do. For a branch
// that has one, it is the strongest evidence available — somebody chose that
// base, it lives on the server rather than in local metadata, and it is what
// GitHub will act on.
//
// Two limits are inherent rather than incidental, and both argue for it being
// asked for rather than assumed:
//
//   - It can only describe published branches. No pull request, no edge, so a
//     tree read this way is partial by construction.
//   - GitHub retargets a child when its base branch is deleted on merge, so
//     immediately after a parent lands its children point at the trunk. The
//     structure is not wrong about what GitHub will do; it is no longer a
//     record of what the stack was.
type PullRequestSelector struct {
	Git    Git
	GitHub GitHub
}

func (s PullRequestSelector) Source() Source { return SourcePullRequest }

// Describes reports whether this branch has exactly one open pull request.
//
// Unlike the other sources this cannot be answered without asking GitHub, which
// is precisely why it is never consulted by precedence: a command that must not
// invoke gh would do so merely by resolving a branch. It answers only when the
// caller named it.
func (s PullRequestSelector) Describes(ctx context.Context, branch string) (bool, error) {
	if s.Git == nil || s.GitHub == nil {
		return false, nil
	}
	forest, err := s.forest(ctx)
	if err != nil {
		return false, err
	}
	_, described := forest.Parents[branch]
	return described, nil
}

// Select builds the structure GitHub already holds and applies the scope.
func (s PullRequestSelector) Select(ctx context.Context, selection Selection, command string) (Snapshot, error) {
	if s.Git == nil || s.GitHub == nil {
		return Snapshot{}, fmt.Errorf("pull request selector is not fully configured")
	}
	target, source, err := resolveTarget(ctx, s.Git, selection.Branch)
	if err != nil {
		return Snapshot{}, err
	}
	localBranches, err := s.Git.LocalBranches(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	local := branchSet(localBranches)
	if !local[target] {
		return Snapshot{}, fmt.Errorf("selected branch %q is not a local branch", target)
	}
	forest, err := s.forest(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	if _, described := forest.Parents[target]; !described {
		return Snapshot{}, fmt.Errorf("no open pull request describes %q · a branch with none, or with more than one, has no base to read", target)
	}

	scope := selection.EffectiveScope()
	selected, err := forest.Select(target, scope)
	if err != nil {
		return Snapshot{}, err
	}
	if err := validateBranchesLocalAndSafe(local, selected, command); err != nil {
		return Snapshot{}, err
	}
	base, within, err := forest.Hangs(selected, target, scope)
	if err != nil {
		return Snapshot{}, err
	}
	if selection.Trunk != "" && selection.Trunk != base {
		return Snapshot{}, fmt.Errorf("requested trunk %q is not what %q's pull request is based on (%s)", selection.Trunk, target, base)
	}
	branches := append([]string(nil), selected...)
	if within {
		branches = branches[1:]
	}
	ancestry, err := forest.Path(target)
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{
		Target:       target,
		TargetSource: source,
		GraphitePath: ancestry,
		Base:         base,
		BaseSource:   "pull request base",
		Branches:     branches,
		Parents:      forest.Restrict(selected),
		Scope:        scope,
	}, nil
}

// forest reads every local branch's open pull request and keeps the base as the
// parent.
//
// A branch with no open pull request, or with more than one, contributes no
// edge: the first has nothing to read and the second is the one ambiguity this
// tool refuses to interpret anywhere else, so interpreting it here would be
// inconsistent as well as a guess.
func (s PullRequestSelector) forest(ctx context.Context) (Forest, error) {
	localBranches, err := s.Git.LocalBranches(ctx)
	if err != nil {
		return Forest{}, err
	}
	prs, err := s.GitHub.Inspect(ctx, localBranches)
	if err != nil {
		return Forest{}, err
	}
	local := branchSet(localBranches)
	parents := make(map[string]string, len(prs))
	for branch, resolution := range githubstack.ResolveHeads(prs) {
		if resolution.Open == nil || !local[branch] {
			continue
		}
		// A base outside the checkout cannot be rendered or acted on, so the
		// branch reads as a root rather than hanging from something absent.
		if base := resolution.Open.Base; local[base] {
			parents[branch] = base
			continue
		}
		parents[branch] = ""
	}
	diagnostic.Event(ctx, "pullrequest.forest", diagnostic.Field{Key: "edges", Value: fmt.Sprint(len(parents))})
	return Forest{Parents: parents}, nil
}
