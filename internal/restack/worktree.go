package restack

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/shhac/g2g/internal/repair"
)

// WorktreeReader reports branches other worktrees have checked out.
//
// It is an optional capability rather than a method on Git: every fake in the
// tests implements Git, and a rewrite that could not ask was safe before this
// check existed and stays safe now.
type WorktreeReader interface {
	CheckedOutElsewhere(ctx context.Context) (map[string]string, error)
}

// moving is every branch whose ref will have moved by the time the rewrite is
// done.
//
// A caller's own moves count as much as the rewrite's: sync collects branches
// before replaying, and a worktree standing on one of those is stranded exactly
// as it would be by a replay. Pending is how a caller says so. Absorbing
// re-records fork points and moves no ref at all, and a collapse onto the
// commit a branch already points at moves nothing either.
func moving(steps []Step, absorb bool, pending Pending) []string {
	branches := slices.Sorted(maps.Keys(pending.Tips))
	if absorb {
		return branches
	}
	for _, step := range steps {
		if step.Collapses && step.Head == step.Base {
			continue
		}
		if !slices.Contains(branches, step.Branch) {
			branches = append(branches, step.Branch)
		}
	}
	return branches
}

// HeldElsewhere refuses to move a branch another worktree has checked out.
//
// A rewrite moves a ref without checking anything out, so nothing stopped it
// from moving a branch another worktree held. Git updated the ref; that
// worktree's index and working tree still described the old commit, so its next
// git status reported staged changes nobody made. The preview said "applies
// without touching your working tree or checked-out branch" while doing it,
// which was true of the worktree it ran in and false of the other.
//
// It is asked only of branches that will move. Asking it of the whole
// selection refused a path restack because the trunk, which it never touches,
// was checked out in another worktree. It is exported for a caller that moves
// a ref the plan does not: sync advances the trunk itself.
//
// A Git too old to list worktrees, or a failure to ask, is not a reason to
// refuse a rewrite that was fine before this check existed.
// It answers with structure rather than a sentence. A refusal here reaches a
// caller through sync and land as well as restack, and a machine reading the
// documented contract -- read repair, do not parse the prose -- was handed a
// null where the only two ways out were, on the one refusal a large checkout
// meets first.
//
// The ways out name no command, because the same refusal reaches commands that
// accept different scopes. It used to suggest g2g restack --scope path, which
// was the command that had just refused, and which sync does not accept.
func (s Service) HeldElsewhere(ctx context.Context, branches []string) (repair.Note, error) {
	if len(branches) == 0 {
		return repair.Note{}, nil
	}
	holder, ok := s.Git.(WorktreeReader)
	if !ok {
		return repair.Note{}, nil
	}
	elsewhere, err := holder.CheckedOutElsewhere(ctx)
	if err != nil || len(elsewhere) == 0 {
		return repair.Note{}, nil
	}
	held := make([]string, 0, len(branches))
	for _, branch := range branches {
		if path, taken := elsewhere[branch]; taken {
			held = append(held, fmt.Sprintf("%s (%s)", branch, path))
		}
	}
	if len(held) == 0 {
		return repair.Note{}, nil
	}
	return repair.Note{
		Reason: fmt.Sprintf("checked out in another worktree: %s · moving it would leave that worktree describing a commit it no longer has", strings.Join(held, ", ")),
		Ways: []repair.Step{
			{Effect: "switch that worktree to another branch, or close it"},
			{Effect: "select less with --branch or --scope, so nothing that has to move is checked out there"},
		},
	}, nil
}
