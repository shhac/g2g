package align

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/shhac/g2g/internal/diagnostic"
	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/repair"
	"github.com/shhac/g2g/internal/shape"
	"github.com/shhac/g2g/internal/stack"
)

// PullRequestReader selects a stack from the bases of open pull requests.
//
// It is the pull request source's own selection, so a stack means here exactly
// what it means to status --from github, remote-only branches included.
// Declared here, beside its one consumer, so a test can hand the real selector
// a GitHub that answers from a table.
type PullRequestReader interface {
	Select(ctx context.Context, selection stack.Selection, command string) (stack.Snapshot, error)
}

// Forks answers where two lines of history parted.
type Forks interface {
	MergeBase(ctx context.Context, one, other string) (string, error)
}

// GitHubAdoptScopes are how much of a stack an import from pull requests
// may adopt. Both open with the base the stack hangs from, which is what the
// root rule below needs to see; a scope rooted at the target would leave that
// base unexamined, and all would span trunks the user did not name.
var GitHubAdoptScopes = []shape.Scope{shape.ScopeStack, shape.ScopeTrunk}

// PlanAdoptFromGitHub works out what the open pull requests above a
// branch declare that the g2g graph does not.
//
// It is how a stack someone else published becomes one this checkout can
// restack. The pull requests declare each parent, so this is no more a guess
// than a Graphite import is, and it follows the same rules: additive, refused
// on any disagreement, parents recorded before children. Two refusals are its
// own, and each is about a branch the record names that the graph cannot hold:
// one that is not on this machine, and a base nothing establishes as a trunk.
func (s Service) PlanAdoptFromGitHub(ctx context.Context, selection stack.Selection) (AdoptPlan, error) {
	if s.Store == nil || s.Git == nil || s.PullRequests == nil || s.Forks == nil {
		return AdoptPlan{}, fmt.Errorf("importing from pull requests is not configured")
	}
	selection.From = stack.SourceGitHub
	selection.Scope = selection.EffectiveScope()
	if !slices.Contains(GitHubAdoptScopes, selection.Scope) {
		return AdoptPlan{}, fmt.Errorf("import --from github adopts a stack or a trunk, not scope %q", selection.Scope)
	}
	snapshot, err := s.PullRequests.Select(ctx, selection, "g2g github adopt")
	if err != nil {
		return AdoptPlan{}, err
	}
	adopted, err := s.Store.Load(ctx)
	if err != nil {
		return AdoptPlan{}, err
	}
	local, err := s.Git.LocalBranches(ctx)
	if err != nil {
		return AdoptPlan{}, err
	}

	if missing := notHere(snapshot, local); len(missing) != 0 {
		return refused(adopted, fetchFirst(missing)), nil
	}
	declared, err := pullRequestEdges(snapshot)
	if err != nil {
		return AdoptPlan{}, err
	}
	if !s.rootable(ctx, adopted, snapshot.Base) {
		return refused(adopted, unknownBase(snapshot.Base, declared)), nil
	}
	plan, err := s.planAdoptions(ctx, adopted, declared, local, s.pullRequestRecord())
	if err != nil {
		return AdoptPlan{}, err
	}
	plan.Unconfirmed = unconfirmed(plan)
	return plan, nil
}

// RevalidateAdoptFromGitHub recomputes immediately before the write.
//
// It reads the pull requests again rather than trusting the preview's reading.
// What is written is local, but it is written from what GitHub said, and a base
// someone retargeted in between is exactly the change this exists to catch.
func (s Service) RevalidateAdoptFromGitHub(ctx context.Context, selection stack.Selection, preview AdoptPlan) (AdoptPlan, error) {
	current, err := s.PlanAdoptFromGitHub(ctx, selection)
	if err != nil {
		return AdoptPlan{}, err
	}
	if err := diagnostic.Revalidated(ctx, "import", "the pull requests and the g2g graph", current.Equal(preview)); err != nil {
		return AdoptPlan{}, err
	}
	return current, nil
}

func (s Service) pullRequestRecord() record {
	return record{
		from:   FromGitHub,
		answer: "take the pull request's base",
		// The merge base, not the base's tip. Nothing else records where the
		// branch forked — a pull request has no such field — and the base may
		// have moved since the pull request was opened: a trunk that advanced,
		// a parent that took more commits. Its tip would then claim work this
		// branch never had, and a restack would replay from a commit the
		// branch does not contain. The merge base is the last commit both
		// agree on, and it is always an ancestor of the branch.
		forkPoint: func(ctx context.Context, adoption Adoption) (string, error) {
			return s.Forks.MergeBase(ctx, adoption.Branch, adoption.Parent)
		},
	}
}

// notHere lists what the selection names that this checkout lacks, base
// first. The base is checked separately because a stack's own branches are
// the snapshot's concern and the branch it hangs from is not.
func notHere(snapshot stack.Snapshot, local []string) []string {
	missing := make([]string, 0, len(snapshot.Absent)+1)
	if !slices.Contains(local, snapshot.Base) {
		missing = append(missing, snapshot.Base)
	}
	for _, branch := range snapshot.Absent {
		if !slices.Contains(missing, branch) {
			missing = append(missing, branch)
		}
	}
	return missing
}

// pullRequestEdges is each selected branch and its pull request's base, in
// the snapshot's order, which is parents first.
func pullRequestEdges(snapshot stack.Snapshot) ([]Adoption, error) {
	edges := make([]Adoption, 0, len(snapshot.Branches))
	for _, branch := range snapshot.Branches {
		parent, placed := snapshot.ParentOf(branch)
		if !placed {
			return nil, fmt.Errorf("the pull requests select %s without placing it under anything", branch)
		}
		edges = append(edges, Adoption{Branch: branch, Parent: parent})
	}
	return edges, nil
}

// rootable reports whether the stack's base may be what the adopted branches
// hang from.
//
// A base the g2g graph does not record becomes a root of it, and a root is a
// trunk. That is right for the repository's trunk and wrong for a feature
// branch whose own place nobody has recorded, so the base must be recorded
// already, or be what the remote calls its default. The default branch is used
// the way create uses it: as evidence that may only permit what would
// otherwise be refused, never to choose a parent.
func (s Service) rootable(ctx context.Context, adopted graph.Graph, base string) bool {
	if adopted.Tracked(base) || adopted.IsTrunk(base) || len(adopted.Children(base)) != 0 {
		return true
	}
	return base != "" && base == s.defaultBranch(ctx)
}

// defaultBranch treats not knowing as an ordinary answer. The ref it reads is
// written by clone and missing from a repository built by hand, and a failure
// only means the refusal stands.
func (s Service) defaultBranch(ctx context.Context) string {
	if s.Trunks == nil {
		return ""
	}
	branch, err := s.Trunks.DefaultBranch(ctx, "")
	if err != nil {
		return ""
	}
	return branch
}

// fetchFirst is the refusal for branches the pull requests name that are not
// here. Creating them is not this command's business: which remote, which
// upstream, and whether to check one out are the user's to decide, and an
// import that quietly made branches would be doing something nobody previewed.
func fetchFirst(missing []string) repair.Note {
	first := missing[0]
	reason := fmt.Sprintf("%s is only on the remote, and the g2g graph records only local branches", first)
	if len(missing) > 1 {
		reason = fmt.Sprintf("%s are only on the remote, and the g2g graph records only local branches · each needs one, starting with %s",
			strings.Join(missing, ", "), first)
	}
	return repair.Note{
		Reason: reason,
		Ways: []repair.Step{
			{Command: "git fetch && git switch " + first, Effect: "bring it here and check it out"},
			{Command: "git branch " + first + " origin/" + first, Effect: "create it without switching to it, once fetched"},
		},
	}
}

// unknownBase is the refusal for a stack that starts from a branch nothing
// establishes as a trunk.
func unknownBase(base string, declared []Adoption) repair.Note {
	ways := []repair.Step{
		{Command: "g2g adopt --branch " + base, Effect: "record the stack " + base + " is on first"},
	}
	if first := firstOnto(base, declared); first != "" {
		ways = append(ways, repair.Step{
			Command: "g2g track --branch " + first + " --parent " + base,
			Effect:  "say " + base + " is a trunk by recording its first branch yourself",
		})
	}
	return repair.Note{
		Reason: fmt.Sprintf("the pull requests start from %s, which is neither the repository's default branch nor in the g2g graph, so adopting them would make it a trunk", base),
		Ways:   ways,
	}
}

func firstOnto(base string, declared []Adoption) string {
	for _, edge := range declared {
		if edge.Parent == base {
			return edge.Branch
		}
	}
	return ""
}

func refused(adopted graph.Graph, note repair.Note) AdoptPlan {
	return AdoptPlan{From: FromGitHub, Updated: adopted, Repair: note, Blocked: note.Sentence()}
}

// unconfirmed names the adoptions Git does not yet agree with, so the preview
// can say they will read as needing a restack before anyone is surprised by it.
func unconfirmed(plan AdoptPlan) []string {
	names := make([]string, 0)
	for _, adoption := range plan.Adopt {
		if plan.Updated.Edges[adoption.Branch].Origin != graph.OriginAncestry {
			names = append(names, adoption.Branch)
		}
	}
	return names
}
