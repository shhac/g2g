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

// Replacements are the commits one side replaced, each with its replay.
type Replacements []Replacement

// Olds is the commit each pair replaced.
func (r Replacements) Olds() []string {
	olds := make([]string, 0, len(r))
	for _, pair := range r {
		olds = append(olds, pair.Old)
	}
	return olds
}

// News is the replay each pair made.
func (r Replacements) News() []string {
	news := make([]string, 0, len(r))
	for _, pair := range r {
		news = append(news, pair.New)
	}
	return news
}

// Named lists each replaced commit on one branch, with the commit it became.
func (r Replacements) Named(branch string) []Drop {
	named := make([]Drop, 0, len(r))
	for _, pair := range r {
		named = append(named, Drop{Kind: Replaced, Branch: branch, Commit: pair.Old, To: pair.New})
	}
	return named
}

// apart is dropped and made with both commits of every pair taken out: the
// pair stands for them.
func (r Replacements) apart(dropped, made []string) ([]string, []string) {
	return without(dropped, r.Olds()), without(made, r.News())
}

// rejoin is apart's inverse for the pairs whose old commit is named: those go
// back into dropped and made, and the rest stay paired.
func (r Replacements) rejoin(named, dropped, made []string) (Replacements, []string, []string) {
	kept := make(Replacements, 0, len(r))
	dropped, made = slices.Clone(dropped), slices.Clone(made)
	for _, pair := range r {
		if !slices.Contains(named, pair.Old) {
			kept = append(kept, pair)
			continue
		}
		dropped, made = append(dropped, pair.Old), append(made, pair.New)
	}
	return kept, dropped, made
}

// unpair puts each replacement whose old commit is named back as the drop and
// the new commit it was before pairing, so keeping it works on it as on any
// other drop: the version a replay made is then one more commit of its side.
func (c Changes) unpair(commits []string) Changes {
	c.ReplacedUpstream, c.DroppedUpstream, c.New = c.ReplacedUpstream.rejoin(commits, c.DroppedUpstream, c.New)
	c.ReplacedHere, c.DroppedHere, c.Mine = c.ReplacedHere.rejoin(commits, c.DroppedHere, c.Mine)
	return c
}

// Carrier reads what a replay carries over from each commit. A reader
// without it pairs nothing, and every replacement reads as a drop and a new
// commit, as it did before.
type Carrier interface {
	Carried(ctx context.Context, ids []string) (map[string]localgit.Carried, error)
}

// The real client must stay a Carrier: a signature that drifted would turn
// pairing off silently, and every replay would read as a drop again.
var _ Carrier = localgit.Client{}

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
	if !ok {
		return changes, nil
	}
	hereNew, upstreamNew := without(changes.Mine, seen), without(changes.New, seen)
	here := len(changes.DroppedHere) != 0 && len(hereNew) != 0
	upstream := len(changes.DroppedUpstream) != 0 && len(upstreamNew) != 0
	if !here && !upstream {
		return changes, nil
	}
	ids := slices.Concat(changes.DroppedHere, hereNew, changes.DroppedUpstream, upstreamNew)
	carried, err := carrier.Carried(ctx, ids)
	if err != nil {
		return Changes{}, err
	}
	changes.ReplacedHere = pairs(changes.DroppedHere, hereNew, carried)
	changes.ReplacedUpstream = pairs(changes.DroppedUpstream, upstreamNew, carried)
	changes.DroppedHere, changes.Mine = changes.ReplacedHere.apart(changes.DroppedHere, changes.Mine)
	changes.DroppedUpstream, changes.New = changes.ReplacedUpstream.apart(changes.DroppedUpstream, changes.New)
	return changes, nil
}

// pairs matches each old commit with the one new commit sharing its key, in
// the old commits' order.
func pairs(old, made []string, carried map[string]localgit.Carried) Replacements {
	byKey := func(commits []string) map[string][]string {
		keyed := map[string][]string{}
		for _, commit := range commits {
			if key := carried[commit].Key; key != "" {
				keyed[key] = append(keyed[key], commit)
			}
		}
		return keyed
	}
	olds, news := byKey(old), byKey(made)
	paired := make(Replacements, 0)
	for _, commit := range old {
		key := carried[commit].Key
		if key == "" || len(olds[key]) != 1 || len(news[key]) != 1 {
			continue
		}
		replay := news[key][0]
		if carried[replay].Committed < carried[commit].Committed {
			continue
		}
		paired = append(paired, Replacement{Old: commit, New: replay})
	}
	return paired
}

// without is commits less those named.
func without(commits, named []string) []string {
	return slices.DeleteFunc(slices.Clone(commits), func(commit string) bool { return slices.Contains(named, commit) })
}
