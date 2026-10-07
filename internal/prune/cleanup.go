package prune

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/shhac/g2g/internal/repair"
)

func (s Service) planDeletions(ctx context.Context, plan *Plan, assessed map[string]string) error {
	if len(plan.Landed) == 0 {
		return nil
	}
	if s.Cleaner == nil {
		return fmt.Errorf("local branch deletion is not configured")
	}
	holders, err := s.Cleaner.BranchHolders(ctx)
	if err != nil {
		return err
	}
	for _, branch := range plan.Landed {
		if path, held := holders[branch]; held {
			plan.Repair = repair.Note{Reason: branch + " is checked out in " + path, Ways: []repair.Step{{Effect: "switch that worktree to another branch, then preview the prune again"}}}
			return nil
		}
	}
	plan.Delete, err = s.Cleaner.ResolveAll(ctx, plan.Landed)
	if err != nil {
		return err
	}
	for _, branch := range plan.Landed {
		if plan.Delete[branch] == "" || plan.Delete[branch] != assessed[branch] {
			return fmt.Errorf("%s changed during cleanup planning · preview again", branch)
		}
	}
	return nil
}

// Stopped records irreversible work, including a graph write followed by a
// failed fork-point release. The CLI must report it as part-way, never unapplied.
type Stopped struct {
	Deleted   []string
	Forgotten []string
	Err       error
	Retry     string
}

func (e *Stopped) Error() string { return e.Err.Error() }
func (e *Stopped) Unwrap() error { return e.Err }

func partial(plan Plan, deleted, forgotten []string, err error) error {
	if len(deleted) == 0 && len(forgotten) == 0 {
		return err
	}
	retry := "g2g prune --branch " + repair.Quote(plan.Discovery.Target) + " --scope " + string(plan.Discovery.Scope) + " --forget-missing"
	if plan.Options.DeleteBranches {
		retry += " --delete-branches"
	}
	return &Stopped{Deleted: slices.Clone(deleted), Forgotten: slices.Clone(forgotten), Err: err, Retry: retry}
}

// Forgotten lists the graph records this plan removes, separately from the
// content verdict on branches that still exist.
func (p Plan) Forgotten() []string {
	return append(slices.Clone(p.Landed), p.ForgottenMissing...)
}

func (p Plan) Deleted() []string { return slices.Sorted(maps.Keys(p.Delete)) }

func (e *Stopped) WhatStands() string {
	parts := []string{}
	if len(e.Deleted) != 0 {
		parts = append(parts, "Deleted local branches "+strings.Join(e.Deleted, ", "))
	}
	if len(e.Forgotten) != 0 {
		parts = append(parts, "Forgot graph records for "+strings.Join(e.Forgotten, ", "))
	}
	return strings.Join(parts, ". ") + "."
}
