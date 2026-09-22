package link

import (
	"context"
	"fmt"

	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/parallel"
)

// Whether a branch and the commit its pull request is on are the same work.
//
// An optional capability, and separable by construction: without a Tips reader
// currency is simply not reported. Its tests have lived apart in
// currency_real_test.go since it was written, against a real repository,
// because what Git considers equivalent is not something a fake can vouch for.

// Tips compares a branch with the commit its pull request is on.
//
// It is optional. Without it currency is simply not reported, which is what
// every reader saw before: a pull request described as aligned while it did not
// contain the work sitting in the branch, because alignment is a statement
// about the base and never about the contents.
type Tips interface {
	// ResolveAll answers for every revision in one process, and omits the ones
	// this repository does not have. Asking per revision cost a process each,
	// and a status asks about every branch and the commit every pull request
	// is on.
	ResolveAll(ctx context.Context, revisions []string) (map[string]string, error)
	// Cherry compares by content. Counting commit ids answered this question
	// wrongly in the ordinary case: a branch replayed onto a newer base carries
	// the same work under new ids, so every commit the base had gained was
	// reported as work of the reader's own — 1532 of them on one real stack,
	// none of which were theirs.
	Cherry(ctx context.Context, upstream, head, limit string) (absent, present []string, err error)
	// Absorbed answers the same question of a whole branch at once, which is
	// what a squash merge needs: it combines the branch's commits into one, so
	// that commit is equivalent to none of them and Cherry marks every one as
	// new while the branch as a whole contributes nothing.
	Absorbed(ctx context.Context, base, branch string) (bool, error)
}

// Currency is how a branch stands against the commit its pull request is on.
type Currency struct {
	// Unpushed is how many of this branch's own commits have no equivalent on
	// its pull request. It counts by content and stops at the branch's parent,
	// because a stacked branch's work is what sits above the branch below it —
	// not everything above the trunk, which is what made the count grow by the
	// size of the trunk every time somebody replayed onto it.
	Unpushed int
	// Diverged means the pull request carries work, by content, that this
	// branch does not: somebody pushed to it, or commits were dropped here.
	Diverged bool
	// Rewritten means the pull request has every one of this branch's commits
	// by content, but not as these commits — it was replayed or amended since
	// it was last pushed, so the pull request shows an older rendering of the
	// same work.
	//
	// This is the state a restacked stack is in, and it used to report as a
	// divergence with the trunk's commits counted as the reader's own. Nothing
	// is missing from it; it needs pushing.
	Rewritten bool
}

// Current reports a pull request that already has everything the branch does,
// as the commits the branch has.
func (c Currency) Current() bool { return c.Unpushed == 0 && !c.Diverged && !c.Rewritten }

// currency compares each branch with the commit its open pull request is on.
//
// A pull request whose head this repository does not have is reported as
// diverged rather than counted: the commits are not here to count, and
// fetching them to say how many would turn a read into a network write.
func (s Service) currency(ctx context.Context, plan Plan) (map[string]Currency, error) {
	if s.Tips == nil {
		return nil, nil
	}
	open := map[string]githubstack.PullRequest{}
	for branch, resolution := range githubstack.ResolveHeads(plan.PullRequests) {
		if resolution.Open != nil {
			open[branch] = *resolution.Open
		}
	}
	// Every revision this needs, in one process: each branch, and the commit
	// each pull request is on.
	asking := make([]string, 0, 2*len(plan.Branches))
	for _, branch := range plan.Branches {
		if pr, published := open[branch]; published && pr.HeadOID != "" {
			asking = append(asking, branch, pr.HeadOID)
		}
	}
	tips, err := s.Tips.ResolveAll(ctx, asking)
	if err != nil {
		return nil, err
	}

	// What is left is per branch and cannot be batched — git compares one pair
	// at a time — so the branches are asked at once instead. The results land
	// in a slice sized first, which is what makes that safe without a lock.
	states := make([]*Currency, len(plan.Branches))
	err = parallel.Each(ctx, plan.Branches, func(ctx context.Context, index int, branch string) error {
		pr, published := open[branch]
		if !published || pr.HeadOID == "" {
			return nil
		}
		state, err := s.compareWithPullRequest(ctx, plan, branch, pr.HeadOID, tips)
		if err != nil {
			return err
		}
		states[index] = &state
		return nil
	})
	if err != nil {
		return nil, err
	}
	currency := make(map[string]Currency, len(plan.Branches))
	for index, state := range states {
		if state != nil {
			currency[plan.Branches[index]] = *state
		}
	}
	return currency, nil
}

// compareWithPullRequest is one branch's whole answer, and the unit that runs
// alongside the others.
func (s Service) compareWithPullRequest(ctx context.Context, plan Plan, branch, head string, tips map[string]string) (Currency, error) {
	local, known := tips[branch]
	if !known {
		return Currency{}, fmt.Errorf("branch %q is not a commit in this repository", branch)
	}
	if local == head {
		return Currency{}, nil
	}
	if _, here := tips[head]; !here {
		// On a commit this repository has never seen, so there is nothing here
		// to compare it with.
		return Currency{Diverged: true}, nil
	}
	return s.compare(ctx, plan, branch, head)
}

// compare asks what each side holds that the other does not, by content.
//
// The two calls are not symmetric and cannot be. This branch's side is limited
// to its own commits, because everything below them belongs to the branch it
// is stacked on. The pull request's side needs no limit: branch..head already
// excludes everything the branch can reach, so what is left is the pull
// request's own commits and never the base's.
func (s Service) compare(ctx context.Context, plan Plan, branch, head string) (Currency, error) {
	ours, _, err := s.Tips.Cherry(ctx, head, branch, ownCommitsFrom(plan, branch))
	if err != nil {
		return Currency{}, err
	}
	diverged, err := s.diverged(ctx, branch, head)
	if err != nil {
		return Currency{}, err
	}
	return Currency{Unpushed: len(ours), Diverged: diverged, Rewritten: len(ours) == 0 && !diverged}, nil
}

// diverged reports a pull request carrying content this branch does not.
//
// The whole-branch merge is asked first because the per-commit comparison
// cannot be bounded on this side: it has to compute a patch id for every commit
// the branch holds that the pull request does not, which on a stack sitting on
// a busy trunk is the whole trunk, and it was most of what a status spent.
//
// That makes this a reading, not a decision. A pull request head whose only
// extra commit cancels out against the merge base — a revert — merges away,
// so status can call it rewritten. push and sync, which act on the answer,
// ask landed.Missing, which counts per commit and is not fooled.
func (s Service) diverged(ctx context.Context, branch, head string) (bool, error) {
	absorbed, err := s.Tips.Absorbed(ctx, branch, head)
	if err != nil || absorbed {
		return false, err
	}
	theirs, _, err := s.Tips.Cherry(ctx, branch, head, "")
	if err != nil {
		return false, err
	}
	return len(theirs) != 0, nil
}
