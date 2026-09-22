package sync

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/shhac/g2g/internal/diagnostic"

	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/landed"
	"github.com/shhac/g2g/internal/repair"
)

// What moved, and how to say so.
//
// Every subtle rule sync has lives here — advance against supersede against
// diverge, what --take published would discard, and the wording of the refusal
// when both sides moved. Kept apart from the sequencing so the two can be read
// one at a time, the way land and restack already separate theirs.

// compare asks how the base stands against the remote without changing it.
func (s Service) compare(ctx context.Context, base, remote string, published map[string]string, take Take) (advance, supersede, diverged bool, discards []string, err error) {
	// The base is not on the remote at all, which is ordinary for a local trunk
	// that was never pushed.
	if published[base] == "" {
		return false, false, false, nil, nil
	}
	fetched := localgit.IsolatedRef(remote, base)
	local, err := s.Git.Resolve(ctx, base)
	if err != nil {
		return false, false, false, nil, err
	}
	upstream, err := s.Git.Resolve(ctx, fetched)
	if err != nil {
		return false, false, false, nil, nil
	}
	if local == upstream {
		return false, false, false, nil, nil
	}
	behind, err := s.Git.IsAncestor(ctx, base, fetched)
	if err != nil {
		return false, false, false, nil, err
	}
	if behind {
		return true, false, false, nil, nil
	}
	// A trunk with commits of its own and nothing new upstream has nothing to
	// advance to. Publishing it is not sync's business, and offering --take
	// published here offered to discard those commits for no reason at all.
	ahead, err := s.Git.IsAncestor(ctx, fetched, base)
	if err != nil {
		return false, false, false, nil, err
	}
	if ahead {
		return false, false, false, nil, nil
	}
	// Neither side is an ancestor of the other, which is what somebody
	// force-pushing the trunk looks like — usually after rebasing or squashing
	// it. If everything the local trunk has is in the published one by content,
	// nothing is lost by taking theirs, and that is the whole of the recovery:
	// remote trunk, local stack replayed onto it.
	ours, _, err := s.Git.Cherry(ctx, fetched, base, "")
	if err != nil {
		return false, false, true, nil, nil
	}
	if len(ours) == 0 {
		return false, true, false, nil, nil
	}
	if take.Published() {
		return false, true, false, ours, nil
	}
	return false, false, true, nil, nil
}

// collect works out which branches of your own the remote has moved on.
//
// Five answers, and only the last is a refusal:
//
//   - not published, or level: nothing to do.
//   - published ahead of here: fast-forward, which is a reviewer pushing a fix
//     onto your branch.
//   - published elsewhere but containing everything here by content: somebody
//     rebased or amended your branch and published it, so theirs supersedes.
//   - here containing everything published by content, ahead or rewritten:
//     unpublished work, which is push's business.
//   - genuinely diverged: you have work the published version does not, and
//     choosing between them is not something to do behind your back.
//
// Whether a branch is published at all comes from the remote, never from the
// fetched ref. Nothing prunes refs/g2g/remotes/, so a branch the remote has
// since deleted still resolves there to whatever it last held -- and reading
// that as the published version fast-forwarded a branch onto commits its
// owner had dropped, or refused it as diverged from a version nobody has.
//
// The branches both sides moved come back rather than a refusal, because the
// way out names the selection it came from and only the caller has that.
func (s Service) collect(ctx context.Context, remote, base string, branches []string, onRemote map[string]string, take Take, parents map[string]string) ([]Collection, []divergence, error) {
	collect := make([]Collection, 0, len(branches))
	stuck := make([]divergence, 0)
	for _, branch := range branches {
		if branch == base || onRemote[branch] == "" {
			// Not on the remote at all, which is ordinary for work in progress.
			continue
		}
		published, err := s.Git.Resolve(ctx, localgit.IsolatedRef(remote, branch))
		if err != nil {
			continue
		}
		local, err := s.Git.Resolve(ctx, branch)
		if err != nil {
			return nil, nil, err
		}
		if local == published {
			continue
		}
		behind, err := s.Git.IsAncestor(ctx, branch, published)
		if err != nil {
			return nil, nil, err
		}
		if behind {
			collect = append(collect, Collection{Branch: branch, To: published})
			continue
		}
		ahead, err := s.Git.IsAncestor(ctx, published, branch)
		if err != nil {
			return nil, nil, err
		}
		if ahead {
			// You have unpublished work. That is push's business, not sync's.
			continue
		}
		ours, _, err := s.Git.Cherry(ctx, published, branch, "")
		if err != nil {
			return nil, nil, err
		}
		if len(ours) == 0 {
			collect = append(collect, Collection{Branch: branch, To: published, Superseded: true})
			continue
		}
		replayed, err := landed.Into(ctx, s.Git, branch, localgit.IsolatedRef(remote, branch), "")
		if err != nil {
			return nil, nil, err
		}
		if replayed {
			// The published version is this branch before it was replayed here:
			// everything it has is here by content, and what is here and not
			// there is the trunk it was replayed onto. That is unpublished work
			// as surely as an ordinary commit is, so it is push's business too.
			//
			// Counted by commit it reads as both sides moving -- the trunk's
			// commits are "here and not published", and once a parent has been
			// squashed its original commits are "published and not here" -- so
			// a second sync before pushing, and every land of three branches
			// whose bottom one had more than one commit, refused a branch
			// nobody else had touched. Absorbed is what sees through the squash.
			continue
		}
		if take.AppliesTo(branch, parents) {
			// Asked for explicitly, and the commits it costs are carried so the
			// preview can name every one before anything happens.
			collect = append(collect, Collection{Branch: branch, To: published, Superseded: true, Discards: ours})
			continue
		}
		// Both sides moved, so say both. "You have work the remote does not" is
		// true of every ordinary commit, and a reader who has just made one has
		// no way to tell that from this.
		theirs, _, err := s.Git.Cherry(ctx, branch, localgit.IsolatedRef(remote, branch), "")
		if err != nil {
			return nil, nil, err
		}
		stuck = append(stuck, divergence{Branch: branch, Ours: len(ours), Theirs: len(theirs)})
	}
	if len(stuck) != 0 {
		return nil, stuck, nil
	}
	diagnostic.Event(ctx, "sync.collect", diagnostic.Field{Key: "branches", Value: fmt.Sprint(len(collect))})
	return collect, nil, nil
}

// fetchList is the base and the selection, each named once. The base is
// normally the first selected branch as well, and asking for it twice put the
// same refspec in the command twice.
//
// What the remote actually has is asked separately, because git fetch fails the
// whole command on one ref it cannot find — and a branch that is gone because
// it merged is the commonest reason for it not to be there.
func fetchList(base string, branches []string) []string {
	wanted := []string{base}
	for _, branch := range branches {
		if branch != base {
			wanted = append(wanted, branch)
		}
	}
	return wanted
}

// collectionsEqual compares collections, including the commits each would
// discard. A plan that would lose different work is a different plan, and
// revalidation has to see that.
func collectionsEqual(left, right []Collection) bool {
	return slices.EqualFunc(left, right, func(a, b Collection) bool {
		return a.Branch == b.Branch && a.To == b.To && a.Superseded == b.Superseded &&
			slices.Equal(a.Discards, b.Discards)
	})
}

// divergence is one branch both sides have moved, and by how much.
type divergence struct {
	Branch       string
	Ours, Theirs int
}

// divergenceWays is the choice a divergence leaves, and there is exactly one
// place it can be made: --take is the only path where sync discards work that
// exists nowhere else, and it has no "mine" value.
//
// The command keeps the selection the refusal came from. A bare
// "g2g sync --take published" told a sync of the whole trunk to run a sync of
// one stack, and told a bounded one to drop its boundary and take everything.
func divergenceWays(selection graph.Selection, take Take, parents map[string]string, stuck []divergence) []repair.Step {
	command := "g2g sync"
	if selection.Branch != "" {
		command += " --branch " + selection.Branch
	}
	if selection.Scope == graph.ScopeTrunk {
		command += " --scope " + string(graph.ScopeTrunk)
	}
	command += " --take " + string(SidePublished)
	if through := widened(take, parents, stuck); through != "" {
		command += " --through " + through
	}
	return []repair.Step{
		{Command: command, Effect: "take the published version and discard yours"},
		{Effect: "reconcile it yourself"},
	}
}

// widened is the boundary that covers what was refused as well as what was
// already decided, when one branch is stacked on all of it.
//
// Where none is -- the refusals are on different forks -- no boundary covers
// them, and the way through is to drop it: an unbounded take reaches exactly
// the diverged branches, which is these and the ones already decided.
func widened(take Take, parents map[string]string, stuck []divergence) string {
	if !take.Bounded() {
		return ""
	}
	for _, candidate := range stuck {
		if !stackedOn(candidate.Branch, take.Through, parents) {
			continue
		}
		covers := true
		for _, other := range stuck {
			covers = covers && stackedOn(candidate.Branch, other.Branch, parents)
		}
		if covers {
			return candidate.Branch
		}
	}
	return ""
}

// divergenceReason says which branches both sides moved, and what each side
// holds that the other does not.
//
// Naming only one side was the problem: "you have work the published version
// does not" is true of every ordinary commit, so a reader who had just made one
// could not tell whether that was what the message meant. Both counts make it
// unambiguous, and the counts are by content, so a commit the other side
// already has under a different id is not counted against you.
func divergenceReason(stuck []divergence) string {
	parts := make([]string, 0, len(stuck))
	for _, moved := range stuck {
		parts = append(parts, fmt.Sprintf("%s (%d here that %s not published, %d published that %s not here)",
			moved.Branch,
			moved.Ours, pick(moved.Ours, "is", "are"),
			moved.Theirs, pick(moved.Theirs, "is", "are")))
	}
	return fmt.Sprintf("both sides have moved on %s", strings.Join(parts, ", "))
}

// pick chooses the form that agrees with a count.
func pick(count int, one, many string) string {
	if count == 1 {
		return one
	}
	return many
}
