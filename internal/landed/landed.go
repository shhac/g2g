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
	absent, _, err := probe.Cherry(ctx, base, branch, limit)
	if err != nil {
		return false, err
	}
	if len(absent) == 0 {
		return true, nil
	}
	return probe.Absorbed(ctx, base, branch)
}

// Missing counts the commits of from that have no equivalent in into, by
// content: zero when into already holds everything from carries.
//
// It is Into asked from the other side, with the whole-branch question first.
// The per-commit form cannot be bounded here, because from is somebody else's
// version of into — a pull request's head, the tip a remote holds — and git
// cherry computes a patch id for every commit into holds that from does not,
// which on a stack sitting on a busy trunk is the whole trunk. The merge
// answers the common case at once, and only where it says otherwise is the
// count taken. A Git too old for merge-tree answers "not absorbed" to
// everything, which costs time here and never correctness.
//
// status and push both ask this — whether the published version holds work
// this checkout does not — and they are meant to say the same thing from
// opposite sides, which they did not while each asked it its own way.
func Missing(ctx context.Context, probe Probe, into, from string) (int, error) {
	absorbed, err := probe.Absorbed(ctx, into, from)
	if err != nil {
		return 0, err
	}
	if absorbed {
		return 0, nil
	}
	absent, _, err := probe.Cherry(ctx, into, from, "")
	if err != nil {
		return 0, err
	}
	return len(absent), nil
}
