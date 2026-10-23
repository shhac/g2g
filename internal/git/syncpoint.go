package git

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/shhac/g2g/internal/diagnostic"
	"github.com/shhac/g2g/internal/subprocess"
)

// Sync points: where this clone and a remote last agreed on a branch.
//
// They are g2g's own refs, as refs/g2g/remotes/ is, and never the user's. Each
// is written with a reflog, so every earlier agreement — and with it every
// commit a later pull or push dropped — stays reachable through
// git reflog until git expires the entry.

const syncedPrefix = "refs/g2g/synced/"

// SyncedRef names the ref holding a branch's sync point for a remote.
func SyncedRef(remote, branch string) string {
	return syncedPrefix + remote + "/" + branch
}

// SyncPoint is one agreement: the remote's tip for the branch, and the
// branch's own tip, at the moment this clone and the remote last agreed on it.
type SyncPoint struct {
	Tip   string
	Local string
	// Dropped are commits the command that recorded this took off the branch,
	// recorded so they can be named afterwards.
	Dropped []string
	// Command is what recorded it, and At when.
	Command string
	At      time.Time
}

// RecordSync writes a branch's sync point. The message carries the local tip
// and anything dropped, which is how a later read checks the point describes
// this branch and how status names what was dropped.
func (c Client) RecordSync(ctx context.Context, remote, branch string, point SyncPoint) error {
	if err := safeRefs(remote, branch, point.Tip, point.Local); err != nil {
		return err
	}
	message := "g2g " + point.Command + " local=" + point.Local
	if len(point.Dropped) != 0 {
		message += " dropped=" + strings.Join(point.Dropped, ",")
	}
	_, err := c.run(ctx, "update-ref", "--create-reflog", "-m", message, SyncedRef(remote, branch), point.Tip)
	return err
}

// ForgetSync removes a branch's sync points for every remote, with their
// reflogs. It is for a branch that is gone: one that comes back under the same
// name is different work.
func (c Client) ForgetSync(ctx context.Context, branch string) error {
	if err := safeRef(branch); err != nil {
		return err
	}
	remotes, err := c.remotes(ctx)
	if err != nil {
		return err
	}
	for _, remote := range remotes {
		if err := c.deleteRef(ctx, SyncedRef(remote, branch)); err != nil {
			return err
		}
	}
	return nil
}

// forgetSync is ForgetSync for a branch this client has just deleted. The
// branch is gone either way, and a sync point left behind is ignored once a
// branch of that name comes back, so failing to remove it is a warning.
func (c Client) forgetSync(ctx context.Context, branch string) {
	if err := c.ForgetSync(ctx, branch); err != nil {
		diagnostic.Warn(ctx, "delete.sync_point", fmt.Sprintf("%s is deleted; its sync point could not be removed", branch))
	}
}

// MoveSync carries a branch's sync points to its new name, for every remote:
// the whole history, oldest first, since the history is what keeps every
// commit a pull or push dropped reachable. The commits are the same ones, so
// every agreement still holds. Each entry is dated by the move rather than
// when it was first recorded, which a reflog written with update-ref cannot
// say otherwise.
func (c Client) MoveSync(ctx context.Context, from, to string) error {
	if err := safeRefs(from, to); err != nil {
		return err
	}
	remotes, err := c.remotes(ctx)
	if err != nil {
		return err
	}
	for _, remote := range remotes {
		history, err := c.SyncHistory(ctx, remote, from, maxSyncHistory)
		if err != nil {
			return err
		}
		if len(history) == 0 {
			continue
		}
		for index := len(history) - 1; index >= 0; index-- {
			if err := c.RecordSync(ctx, remote, to, history[index]); err != nil {
				return err
			}
		}
		if err := c.deleteRef(ctx, SyncedRef(remote, from)); err != nil {
			return err
		}
	}
	return nil
}

// maxSyncHistory bounds how much of a sync point's history a rename carries.
// A branch pulled and pushed through g2g a few times a day stays well inside
// it for longer than git keeps the entries.
const maxSyncHistory = 1000

func (c Client) remotes(ctx context.Context) ([]string, error) {
	output, err := c.run(ctx, "remote")
	if err != nil {
		return nil, err
	}
	return outputLines(output), nil
}

func (c Client) refExists(ctx context.Context, ref string) (bool, error) {
	if _, err := c.run(ctx, "show-ref", "--verify", "--quiet", ref); err != nil {
		if _, exited := subprocess.ExitCode(err); exited {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (c Client) reflogExists(ctx context.Context, ref string) (bool, error) {
	if _, err := c.run(ctx, "reflog", "exists", ref); err != nil {
		if _, exited := subprocess.ExitCode(err); exited {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// deleteRef removes a ref and its reflog. One already gone is not a failure.
func (c Client) deleteRef(ctx context.Context, ref string) error {
	exists, err := c.refExists(ctx, ref)
	if err != nil || !exists {
		return err
	}
	if _, err := c.run(ctx, "update-ref", "-d", ref); err != nil {
		return fmt.Errorf("could not remove %s: %w", ref, err)
	}
	return nil
}
