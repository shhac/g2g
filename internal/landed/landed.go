// Package landed answers whether a branch's work is already in its base, by
// content.
//
// It depends on nothing but the two Git questions it asks, which is what lets
// every caller use it: internal/graph may reach Git and no further, and a
// helper arriving through another package would bring whatever that one
// reaches with it.
//
// The question has two forms and needs both, and that pairing is a rule this
// repository has already broken twice. Cherry answers per commit and cannot
// see a squash merge -- a squash combines a branch's commits into one, so the
// result is equivalent to none of them and every commit reads as new, on the
// commonest way a branch lands. Absorbed merges the branch into the base and
// checks for the base's own tree back, which answers it of the whole branch at
// once.
//
// Before this, the sequence was written out five times with four different
// error policies, and a sixth caller had only the Cherry half.
package landed

import "context"

// Probe is the pair of Git questions the answer is derived from.
//
// An interface rather than a concrete client because every caller already
// declares its own narrow Git boundary and satisfies this incidentally.
type Probe interface {
	// Cherry reports which of head's commits have no equivalent in upstream,
	// by content. limit bounds it to the branch's own work.
	Cherry(ctx context.Context, upstream, head, limit string) (absent, present []string, err error)
	// Absorbed reports a branch whose merge into base would change base not at
	// all, which is the only way to see a squash.
	Absorbed(ctx context.Context, base, branch string) (bool, error)
}

// Into reports a branch with nothing left to contribute to base.
//
// Cherry first because it is the cheaper question and answers the ordinary
// case; limit is what keeps it to the branch's own work rather than to
// everything below it, and is empty where the caller means the whole branch.
//
// Errors are returned rather than read as "no". Which of those a caller wants
// genuinely differs -- forgetting a branch on a bad answer is destructive,
// declining to report one is not -- so the choice stays at the call site where
// it is visible, and only the sequence is shared.
func Into(ctx context.Context, probe Probe, base, branch, limit string) (bool, error) {
	if untouchedBy(ctx, probe, base, branch, limit) {
		return false, nil
	}
	absent, _, err := probe.Cherry(ctx, base, branch, limit)
	if err != nil {
		return false, err
	}
	if len(absent) == 0 {
		return true, nil
	}
	return probe.Absorbed(ctx, base, branch)
}

// Untouched is the cheap question a Probe may also answer: whether base has
// touched nothing the branch touches since the branch left it, which settles
// "not landed" without comparing content.
//
// Both halves of Into compare content against every commit the base gained
// since the branch left it, which on a long-lived trunk is most of what a
// status costs. This is optional because it only ever shortcuts a "no": a
// Probe that cannot answer it is exactly as correct, and slower.
type Untouched interface {
	Untouched(ctx context.Context, base, branch, limit string) (bool, error)
}

// untouchedBy asks, where the Probe can answer. A failure to answer is
// "could not tell", never an error: the question only ever saves the full
// comparison, which is still there to ask and reports its own failures.
func untouchedBy(ctx context.Context, probe Probe, base, branch, limit string) bool {
	asker, ok := probe.(Untouched)
	if !ok {
		return false
	}
	untouched, err := asker.Untouched(ctx, base, branch, limit)
	return err == nil && untouched
}

// Lineage is Probe with the ancestry question Missing needs.
type Lineage interface {
	Probe
	IsAncestor(ctx context.Context, ancestor, descendant string) (bool, error)
}

// Missing counts the commits of from that have no equivalent in into, by
// content — the work a replacement of from by into would drop. from is
// somebody else's version of into: the tip a remote holds, a pull request's
// head.
//
// One run of such commits is excused: the commits of the branch into sat on
// when from was made, when that branch has since landed in base by squash.
// Replaying onto a squashed parent leaves the parent's original commits in the
// old version and nowhere here, each equivalent to nothing, and counting them
// read every such branch as holding somebody else's work. They are excused
// only as a run — every one an ancestor of the newest — that sits under
// commits this branch still has, and whose newest merges into base and changes
// nothing. A parent's commits are below the branch's own work; a reviewer's are
// on top of it.
//
// The whole-branch merge alone must never decide. A commit whose net effect
// cancels out against the merge base — a revert, a deleted file — vanishes
// from a three-way merge, so a reviewer's revert pushed onto a branch read as
// nothing lost and was published over.
func Missing(ctx context.Context, probe Lineage, into, from, base string) (int, error) {
	absent, present, err := probe.Cherry(ctx, into, from, "")
	if err != nil || len(absent) == 0 {
		return len(absent), err
	}
	landedBelow, err := runLandedIn(ctx, probe, absent, present, base)
	if err != nil {
		return 0, err
	}
	if landedBelow {
		return 0, nil
	}
	return len(absent), nil
}

// runLandedIn reports commits that are one run under their newest, beneath the
// newest commit this branch still has, whose work base already has.
func runLandedIn(ctx context.Context, probe Lineage, commits, kept []string, base string) (bool, error) {
	if base == "" || len(kept) == 0 {
		return false, nil
	}
	newest := commits[len(commits)-1]
	for _, commit := range commits[:len(commits)-1] {
		below, err := probe.IsAncestor(ctx, commit, newest)
		if err != nil || !below {
			return false, err
		}
	}
	under, err := probe.IsAncestor(ctx, newest, kept[len(kept)-1])
	if err != nil || !under {
		return false, err
	}
	return probe.Absorbed(ctx, base, newest)
}
