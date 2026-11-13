package restack

import (
	"context"
	"fmt"
	"slices"
	"strings"

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
	if remote, branch, found := strings.Cut(name, "/"); found && remote != "" && slices.Contains(local, branch) {
		if _, err := s.Git.Resolve(ctx, "refs/remotes/"+name); err == nil {
			return Onto{Object: name, Parent: branch}, nil
		}
	}
	return Onto{}, fmt.Errorf("--onto %s names no local branch, and the graph records a parent by branch · name a local branch, or a remote's copy of one", repair.Quote(name))
}
