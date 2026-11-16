package push

import (
	"fmt"
	"strings"

	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/repair"
	"github.com/shhac/g2g/internal/shape"
	"github.com/shhac/g2g/internal/syncpoint"
)

// differenceRepair builds the refusal solely from collected observations.
// Unavailable difference evidence leaves commits unnamed, never changes the
// refusal or removes its ways out.
func differenceRepair(plan Plan, changed map[string]syncpoint.Changes, differing []Drop, subjects map[string]string) repair.Note {
	rejected := make([]string, 0)
	leases := make([]localgit.Lease, 0)
	reconcile := "fetch and reconcile first"
	for _, branch := range plan.Branches {
		if plan.Publishing[branch].Rejected() {
			rejected = append(rejected, branch)
			leases = append(leases, localgit.Lease{Branch: branch, Expected: plan.RemoteTips[branch]})
		}
	}
	missing := make([]string, 0)
	for _, branch := range rejected {
		if changed[branch].Unsynced {
			missing = append(missing, branch)
		}
	}
	agreement := ""
	if len(missing) != 0 {
		agreement = "; g2g has no record that this checkout and the remote agreed on " + strings.Join(missing, ", ") + "; it may have been first pushed outside g2g"
	}
	if restores := syncpoint.Of(plan.Commits, syncpoint.Restoring); len(restores) != 0 {
		note := restoring(plan.Remote, restores)
		note.Reason += agreement
		return note
	}
	if len(rejected) == 0 {
		return repair.Note{}
	}
	reason := fmt.Sprintf("the published version differs on %s", strings.Join(rejected, ", ")) + agreement
	for _, branch := range rejected {
		if plan.Publishing[branch].Standing == Diverged {
			reconcile += "; a conflict-resolved replay can also change the published patch, so check the differences before replacing it"
			break
		}
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
		reason += ": " + strings.Join(names, ", ")
	}
	args := []string{"g2g", "pull", "--branch", plan.Target}
	if plan.Scope == shape.ScopeTrunk {
		args = append(args, "--scope", string(plan.Scope))
	}
	if plan.Remote != localgit.DefaultRemote {
		args = append(args, "--remote", plan.Remote)
	}
	replace := repair.Command(append([]string{"git"}, localgit.PushArguments(plan.Remote, leases, localgit.LeaveUpstream)...))
	return repair.Note{Reason: reason, Ways: []repair.Step{
		{Command: repair.Command(args), Effect: reconcile},
		{Command: replace, Effect: "replace what is published, dropping what the remote has"},
		{Command: repair.Command(append(args, "--take", "published", "--through", plan.Target)), Effect: "preview taking the published versions through this branch; lists the local commits it would discard, then --apply only after reviewing them"},
	}}
}
