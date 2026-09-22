package sync

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/shhac/g2g/internal/diagnostic"

	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/repair"
)

// What moved, and how to say so.
//
// Every subtle rule sync has lives here — advance against supersede against
// diverge, what --take published would discard, and the wording of the refusal
// when both sides moved. Kept apart from the sequencing so the two can be read
// one at a time, the way land and restack already separate theirs.

// compare asks how the base stands against the remote without changing it.
func (s Service) compare(ctx context.Context, base, remote string, take Take) (advance, supersede, diverged bool, discards []string, err error) {
	fetched := localgit.IsolatedRef(remote, base)
	local, err := s.Git.Resolve(ctx, base)
	if err != nil {
		return false, false, false, nil, err
	}
	upstream, err := s.Git.Resolve(ctx, fetched)
	if err != nil {
		// The base is not on the remote at all, which is ordinary for a local
		// trunk that was never pushed.
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
// Four answers, and only the last is a refusal:
//
//   - not published, or level: nothing to do.
//   - published ahead of here: fast-forward, which is a reviewer pushing a fix
//     onto your branch.
//   - published elsewhere but containing everything here by content: somebody
//     rebased or amended your branch and published it, so theirs supersedes.
//   - genuinely diverged: you have work the published version does not, and
//     choosing between them is not something to do behind your back.
func (s Service) collect(ctx context.Context, remote, base string, branches []string, take Take) ([]Collection, repair.Note, error) {
	collect := make([]Collection, 0, len(branches))
	stuck := make([]divergence, 0)
	for _, branch := range branches {
		if branch == base {
			continue
		}
		published, err := s.Git.Resolve(ctx, localgit.IsolatedRef(remote, branch))
		if err != nil {
			// Not on the remote at all, which is ordinary for work in progress.
			continue
		}
		local, err := s.Git.Resolve(ctx, branch)
		if err != nil {
			return nil, repair.Note{}, err
		}
		if local == published {
			continue
		}
		behind, err := s.Git.IsAncestor(ctx, branch, published)
		if err != nil {
			return nil, repair.Note{}, err
		}
		if behind {
			collect = append(collect, Collection{Branch: branch, To: published})
			continue
		}
		ahead, err := s.Git.IsAncestor(ctx, published, branch)
		if err != nil {
			return nil, repair.Note{}, err
		}
		if ahead {
			// You have unpublished work. That is push's business, not sync's.
			continue
		}
		ours, _, err := s.Git.Cherry(ctx, published, branch, "")
		if err != nil {
			return nil, repair.Note{}, err
		}
		if len(ours) == 0 {
			collect = append(collect, Collection{Branch: branch, To: published, Superseded: true})
			continue
		}
		if take.AppliesTo(branch, branches) {
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
			return nil, repair.Note{}, err
		}
		stuck = append(stuck, divergence{Branch: branch, Ours: len(ours), Theirs: len(theirs)})
	}
	if len(stuck) != 0 {
		return nil, repair.Note{Reason: divergenceReason(stuck), Ways: divergenceWays()}, nil
	}
	diagnostic.Event(ctx, "sync.collect", diagnostic.Field{Key: "branches", Value: fmt.Sprint(len(collect))})
	return collect, repair.Note{}, nil
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
func divergenceWays() []repair.Step {
	return []repair.Step{
		{Command: "g2g sync --take published", Effect: "take the published version and discard yours"},
		{Effect: "reconcile it yourself"},
	}
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
