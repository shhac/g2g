package stack

import (
	"context"
	"fmt"
	"sync"

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
	// memo holds the forest for the life of one invocation.
	//
	// Resolution asks Describes and then Select, and each needs the whole
	// structure, so without this a single command builds it twice — and since
	// building it walks remote-only bases in rounds, that doubles every round
	// as well. A pointer so a copied value shares it; nil simply means no
	// sharing, which is slower and never wrong.
	memo *forestMemo
}

// NewPullRequestSelector builds a selector that reads the structure once per
// invocation rather than once per question asked of it.
func NewPullRequestSelector(git Git, github GitHub) PullRequestSelector {
	return PullRequestSelector{Git: git, GitHub: github, memo: &forestMemo{}}
}

// forestMemo is the once-per-invocation cache. A selector is constructed for
// one command run, so there is no staleness to reason about: the structure
// cannot change underneath a single read.
type forestMemo struct {
	once   sync.Once
	forest githubstack.Forest
	err    error
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
	published, err := s.forest(ctx)
	if err != nil {
		return false, err
	}
	return Forest{Parents: published.Parents}.Knows(branch), nil
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
	published, err := s.forest(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	return selectPullRequestSnapshot(selection, target, source, local, published, command)
}

// selectPullRequestSnapshot turns already-loaded local and GitHub facts into
// one selection. Keeping that policy separate from loading makes the source's
// no-checkout contract and its boundary decisions directly readable.
func selectPullRequestSnapshot(selection Selection, target, source string, local map[string]bool, published githubstack.Forest, command string) (Snapshot, error) {
	// The shared traversal takes the edges and nothing else. Which of them are
	// on this machine is this package's concern, carried on the snapshot, so a
	// forest walk never has to ask.
	forest := Forest{Parents: published.Parents}
	if !forest.Knows(target) {
		return Snapshot{}, fmt.Errorf("no open pull request describes %q · a branch with none, and with none based on it, has no place to read", target)
	}

	scope := selection.EffectiveScope()
	selected, err := forest.Select(target, scope)
	if err != nil {
		return Snapshot{}, err
	}
	absent := branchSet(published.Absent)
	if err := validateSelectionIsSafe(selected, command); err != nil {
		return Snapshot{}, err
	}
	for _, branch := range selected {
		if !local[branch] && !absent[branch] {
			return Snapshot{}, fmt.Errorf("selected branch %q is not a local branch", branch)
		}
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
		Ancestry:     ancestry,
		Base:         base,
		BaseSource:   "pull request base",
		Branches:     branches,
		Parents:      forest.Restrict(selected),
		Scope:        scope,
		Absent:       selectedAbsent(branches, absent),
		Unfollowed:   published.Unfollowed,
	}, nil
}

// forest reads the structure GitHub holds, through the builder that owns it.
//
// The walk lives in githubstack for the same reason Graphite's parsing lives in
// graphite: this package selects within a structure and does not build one. It
// was inline here, which is what made the pull request source the only one
// whose shape was assembled inside the resolver.
func (s PullRequestSelector) forest(ctx context.Context) (githubstack.Forest, error) {
	if s.memo == nil {
		return s.build(ctx)
	}
	s.memo.once.Do(func() { s.memo.forest, s.memo.err = s.build(ctx) })
	return s.memo.forest, s.memo.err
}

func (s PullRequestSelector) build(ctx context.Context) (githubstack.Forest, error) {
	localBranches, err := s.Git.LocalBranches(ctx)
	if err != nil {
		return githubstack.Forest{}, err
	}
	return githubstack.BuildForest(ctx, s.GitHub, localBranches, githubstack.FollowRounds)
}

// selectedAbsent narrows the forest's absent branches to the ones this
// selection actually contains, because a snapshot describes its own selection
// and not the repository.
func selectedAbsent(branches []string, absent map[string]bool) []string {
	listed := make([]string, 0)
	for _, branch := range branches {
		if absent[branch] {
			listed = append(listed, branch)
		}
	}
	if len(listed) == 0 {
		return nil
	}
	return listed
}
