package syncpoint

import (
	"context"
	"slices"

	localgit "github.com/shhac/g2g/internal/git"
)

// Replacement is a commit and the version of it a replay made.
type Replacement struct {
	Old, New string
}

// unpair puts each replacement whose old commit is named back as the drop and
// the new commit it was before pairing, so keeping it works on it as on any
// other drop: the version a replay made is then one more commit of its side.
func (c Changes) unpair(commits []string) Changes {
	split := func(pairs []Replacement, dropped, made []string) ([]Replacement, []string, []string) {
		kept := make([]Replacement, 0, len(pairs))
		for _, pair := range pairs {
			if !slices.Contains(commits, pair.Old) {
				kept = append(kept, pair)
				continue
			}
			dropped, made = append(dropped, pair.Old), append(made, pair.New)
		}
		return kept, dropped, made
	}
	c.DroppedUpstream, c.Mine = slices.Clone(c.DroppedUpstream), slices.Clone(c.Mine)
	c.DroppedHere, c.New = slices.Clone(c.DroppedHere), slices.Clone(c.New)
	c.ReplacedUpstream, c.DroppedUpstream, c.New = split(c.ReplacedUpstream, c.DroppedUpstream, c.New)
	c.ReplacedHere, c.DroppedHere, c.Mine = split(c.ReplacedHere, c.DroppedHere, c.Mine)
	return c
}

// Carrier reads what a replay carries over from each commit. A reader
// without it pairs nothing, and every replacement reads as a drop and a new
// commit, as it did before.
type Carrier interface {
	Carried(ctx context.Context, ids []string) (map[string]localgit.Carried, error)
}

// replaced pairs each commit a side dropped with the commit a replay made of
// it on the other side, when there is exactly one: the same author, author
// date and subject, the old commit held by the sync point and the new one
// not, and the new one committed no earlier than the old. A replay that had
// to resolve a conflict changes the patch, and a patch is all a content
// comparison has to go on.
//
// The sync point is what keeps this from hiding anybody's work. The old
// commit of every pair is one this clone and the remote agreed on, so
// replacing it loses nothing either side had not already seen; a commit
// somebody added since is in neither drop list and stays new. The new commit
// has to be outside the sync point, or a stale reset's commits, which are in
// it, could pair. And it has to be the later of the two, or going back to the
// version before a colleague's replay would read as replaying theirs.
//
// A key two commits share on either side pairs nothing: that is ambiguity,
// and ambiguity stays a drop.
func replaced(ctx context.Context, git Git, changes Changes, seen []string) (Changes, error) {
	carrier, ok := git.(Carrier)
	unseen := func(commits []string) []string {
		return slices.DeleteFunc(slices.Clone(commits), func(commit string) bool { return slices.Contains(seen, commit) })
	}
	hereNew, upstreamNew := unseen(changes.Mine), unseen(changes.New)
	pairable := len(changes.DroppedHere) != 0 && len(hereNew) != 0 || len(changes.DroppedUpstream) != 0 && len(upstreamNew) != 0
	if !ok || !pairable {
		return changes, nil
	}
	ids := slices.Concat(changes.DroppedHere, hereNew, changes.DroppedUpstream, upstreamNew)
	carried, err := carrier.Carried(ctx, ids)
	if err != nil {
		return Changes{}, err
	}
	changes.ReplacedHere = pairs(changes.DroppedHere, hereNew, carried)
	changes.ReplacedUpstream = pairs(changes.DroppedUpstream, upstreamNew, carried)
	changes.DroppedHere, changes.Mine = without(changes.DroppedHere, changes.ReplacedHere, true), without(changes.Mine, changes.ReplacedHere, false)
	changes.DroppedUpstream, changes.New = without(changes.DroppedUpstream, changes.ReplacedUpstream, true), without(changes.New, changes.ReplacedUpstream, false)
	return changes, nil
}

// pairs matches each old commit with the one new commit sharing its key, in
// the old commits' order.
func pairs(old, made []string, carried map[string]localgit.Carried) []Replacement {
	counted := func(commits []string) map[string]int {
		count := map[string]int{}
		for _, commit := range commits {
			if key := carried[commit].Key; key != "" {
				count[key]++
			}
		}
		return count
	}
	olds, news := counted(old), counted(made)
	paired := make([]Replacement, 0)
	for _, commit := range old {
		key := carried[commit].Key
		if key == "" || olds[key] != 1 || news[key] != 1 {
			continue
		}
		index := slices.IndexFunc(made, func(candidate string) bool { return carried[candidate].Key == key })
		if carried[made[index]].Committed < carried[commit].Committed {
			continue
		}
		paired = append(paired, Replacement{Old: commit, New: made[index]})
	}
	return paired
}

// without is commits less those a replacement names, on its old side or its
// new one.
func without(commits []string, replacements []Replacement, old bool) []string {
	return slices.DeleteFunc(slices.Clone(commits), func(commit string) bool {
		return slices.ContainsFunc(replacements, func(replacement Replacement) bool {
			if old {
				return replacement.Old == commit
			}
			return replacement.New == commit
		})
	})
}
