package restack

import (
	"context"
	"fmt"
	"slices"
	"strings"

	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/repair"
)

// OntoNamed is what a user's --onto asks for.
//
// A local branch is both where the commits land and what the graph records.
// A remote's copy of a local branch is where they land, and the local branch
// is what the graph records: the graph's parents are branches, and recording
// origin/main left the branch reading "parent missing" on the very next
// status, after a replay that had done what was asked. That is create --at's
// answer to the same request, and a fork point on the remote's trunk is one
// status already reads as a trunk this checkout lags. Anything else names no
// branch to record and is refused before it is replayed onto.
func (s Service) OntoNamed(ctx context.Context, name string) (Onto, error) {
	if name == "" {
		return Onto{}, nil
	}
	local, err := s.Git.LocalBranches(ctx)
	if err != nil {
		return Onto{}, err
	}
	if slices.Contains(local, name) {
		return ToBranch(name), nil
	}
	remote, branch, found := strings.Cut(name, "/")
	if found && remote != "" && slices.Contains(local, branch) {
		if tip := s.remoteCopy(ctx, remote, branch); tip != "" {
			return Onto{Object: tip, Parent: branch, Named: name}, nil
		}
	}
	return Onto{}, fmt.Errorf("--onto %s names no local branch, and the graph records a parent by branch · name a local branch, or a remote's copy of one", repair.Quote(name))
}

// knownTips is the optional part of a reader that says where a remote's copy
// of a branch is from every local record of it: the remote-tracking ref and
// the one g2g fetches into, whichever is further along.
type knownTips interface {
	KnownTips(ctx context.Context, remote string, branches []string) (map[string]string, error)
}

// remoteCopy is the commit a remote's copy of branch is at as this checkout
// knows it, empty when there is none.
//
// The same answer the note saying a trunk is behind its remote counts from.
// Resolving only the remote-tracking ref replayed onto it after a pull had
// fetched further into g2g's own, and the next restack then found nothing to
// do while the branch still lacked what the pull had brought.
func (s Service) remoteCopy(ctx context.Context, remote, branch string) string {
	if known, ok := s.Git.(knownTips); ok {
		if tips, err := known.KnownTips(ctx, remote, []string{branch}); err == nil {
			return tips[branch]
		}
	}
	tip, err := s.Git.Resolve(ctx, localgit.TrackingRef(remote, branch))
	if err != nil {
		return ""
	}
	return tip
}
