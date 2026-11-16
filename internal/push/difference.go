package push

import (
	"cmp"
	"context"
	"strings"

	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/repair"
	"github.com/shhac/g2g/internal/shape"
	"github.com/shhac/g2g/internal/syncpoint"
)

// explainMissingAgreement uses the assessment already made by Compare. An
// absent or unusable sync point explains why a replay cannot be distinguished
// from somebody else's work; it never makes replacing that work safe.
func (p *Plan) explainMissingAgreement(changed map[string]syncpoint.Changes) {
	missing := make([]string, 0)
	for _, branch := range p.Branches {
		if p.Publishing[branch].Rejected() && changed[branch].Unsynced {
			missing = append(missing, branch)
		}
	}
	if p.Repair.Reason != "" && len(missing) != 0 {
		p.Repair.Reason += "; g2g has no record that this checkout and the remote agreed on " + strings.Join(missing, ", ") + "; it may have been first pushed outside g2g"
	}
}

// explainDifference names the published work a refused push would overwrite.
// Only refused branches pay for this read; unknown objects remain unnamed
// until pull fetches them. No classification or permission to push changes.
func (s Service) explainDifference(ctx context.Context, plan *Plan) error {
	if plan.Repair.Reason == "" || len(syncpoint.Of(plan.Commits, syncpoint.Restoring)) != 0 {
		return nil
	}
	differing := make([]Drop, 0)
	for _, branch := range plan.Branches {
		standing := plan.Publishing[branch].Standing
		if standing != Behind && standing != Diverged {
			continue
		}
		parent := plan.SitsOn(branch)
		publishedParent, err := builtOn(ctx, s.Git, plan.RemoteTips[parent], plan.RemoteTips[branch])
		if err != nil {
			return err
		}
		commits, _, err := s.Git.Cherry(ctx, branch, plan.RemoteTips[branch], cmp.Or(publishedParent, parent))
		if err != nil {
			return err
		}
		differing = append(differing, syncpoint.Named(syncpoint.Left, branch, commits)...)
	}
	subjects, err := s.subjects(ctx, Plan{Commits: differing})
	if err != nil {
		return err
	}
	names := make([]string, 0, len(differing))
	for _, commit := range differing {
		name := syncpoint.Names([]Drop{commit})
		if subject := subjects[commit.Commit]; subject != "" {
			name += " " + subject
		}
		names = append(names, name)
	}
	if len(names) != 0 {
		plan.Repair.Reason += ": " + strings.Join(names, ", ")
	}
	args := []string{"g2g", "pull", "--branch", plan.Target}
	if plan.Scope == shape.ScopeTrunk {
		args = append(args, "--scope", string(plan.Scope))
	}
	if plan.Remote != localgit.DefaultRemote {
		args = append(args, "--remote", plan.Remote)
	}
	plan.Repair.Ways[0].Command = repair.Command(args)
	plan.Repair.Ways = append(plan.Repair.Ways, repair.Step{
		Command: repair.Command(append(args, "--take", "published", "--through", plan.Target)),
		Effect:  "preview taking the published versions through this branch; lists the local commits it would discard, then --apply only after reviewing them",
	})
	return nil
}
