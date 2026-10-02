package sync

import (
	"context"
)

// fetch asks the remote which of the wanted branches it has, and brings down
// the ones g2g's own refs do not already hold at that tip. What the remote has
// comes back, because whether a branch is published is the remote's answer
// and never the fetched ref's.
func (s Service) fetch(ctx context.Context, remote string, wanted []string) (map[string]string, error) {
	published, err := s.Git.RemoteTips(ctx, remote, wanted)
	if err != nil {
		return nil, err
	}
	stale, err := s.stale(ctx, remote, wanted, published)
	if err != nil {
		return nil, err
	}
	if len(stale) == 0 {
		return published, nil
	}
	if err := s.Git.FetchIsolated(ctx, remote, stale); err != nil {
		return nil, err
	}
	return published, nil
}

// Fetched says what g2g's own fetched refs already hold for each branch, from
// local refs. It is optional: without it every branch the remote has is
// fetched, which is what happened before it existed.
type Fetched interface {
	IsolatedTips(ctx context.Context, remote string) (map[string]string, error)
}

// stale is the branches the remote has at a tip g2g has not fetched yet.
//
// A plan fetched every branch the remote has on every run, and a land runs one
// after each merge — right after waiting for the merge by fetching the trunk,
// and again to revalidate — so most of those fetches were of refs already
// here. A ref already at the remote's tip holds every object that tip needs.
func (s Service) stale(ctx context.Context, remote string, wanted []string, published map[string]string) ([]string, error) {
	var fetched map[string]string
	if reader, ok := s.Git.(Fetched); ok {
		var err error
		if fetched, err = reader.IsolatedTips(ctx, remote); err != nil {
			return nil, err
		}
	}
	stale := make([]string, 0, len(wanted))
	for _, branch := range wanted {
		if tip := published[branch]; tip != "" && fetched[branch] != tip {
			stale = append(stale, branch)
		}
	}
	return stale, nil
}
