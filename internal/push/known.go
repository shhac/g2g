package push

import "context"

// KnownTips reads what the remote last held for each branch from local refs.
type KnownTips interface {
	Comparer
	KnownTips(ctx context.Context, remote string, branches []string) (map[string]string, error)
}

// Known compares a stack with what the remote last held here, from local refs
// alone. It is what status reads. push asks the remote itself, because a lease
// has to be pinned to what is there now; status only reports, and a report that
// needed a network would not be one to run before deciding whether to fetch.
type Known struct {
	Git KnownTips
}

// Ready reports a Known with everything it needs.
func (k Known) Ready() bool { return k.Git != nil }

// Compare is Compare over the tips this repository already knows, which it
// returns too: a caller asking more of those branches has them in hand.
func (k Known) Compare(ctx context.Context, remote string, branches []string, below func(string) (parent, trunk string)) (map[string]Publication, map[string]string, error) {
	var tips map[string]string
	var conflicts map[string]bool
	var err error
	if reader, ok := k.Git.(interface {
		KnownTipsWithConflicts(context.Context, string, []string) (map[string]string, map[string]bool, error)
	}); ok {
		tips, conflicts, err = reader.KnownTipsWithConflicts(ctx, remote, branches)
	} else {
		tips, err = k.Git.KnownTips(ctx, remote, branches)
	}
	if err != nil {
		return nil, nil, err
	}
	compared := make([]string, 0, len(branches))
	uncertain := make(map[string]bool)
	for _, branch := range branches {
		parent, trunk := below(branch)
		// A published branch's content comparison also needs its base's
		// published tip. Conflicting evidence there cannot establish counts.
		if conflicts[branch] || (tips[branch] != "" && (conflicts[parent] || conflicts[trunk])) {
			uncertain[branch] = true
		} else {
			compared = append(compared, branch)
		}
	}
	publishing, err := Compare(ctx, k.Git, remote, compared, tips, below)
	if err != nil {
		return nil, tips, err
	}
	for branch := range uncertain {
		publishing[branch] = Publication{Standing: Conflicting}
	}
	return publishing, tips, err
}
