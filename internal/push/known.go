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

// Compare is Compare over the tips this repository already knows.
func (k Known) Compare(ctx context.Context, remote string, branches []string, below func(string) (parent, trunk string)) (map[string]Publication, error) {
	tips, err := k.Git.KnownTips(ctx, remote, branches)
	if err != nil {
		return nil, err
	}
	return Compare(ctx, k.Git, branches, tips, below)
}
