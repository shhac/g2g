package cli

import (
	"context"
	"errors"

	"github.com/spf13/cobra"

	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/push"
	"github.com/shhac/g2g/internal/restack"
)

// newDoctor is status narrowed to what went wrong.
//
// status draws the stack you are on and everything about it; doctor reads every
// recorded stack and says only what is not as it should be, each with the
// command that puts it right. Most of what it finds broke outside this tool —
// a branch deleted with plain git, a parent rebased by hand, a force push from
// somewhere else — which is why it is a command of its own rather than a mode:
// it is what to run when something feels off, and its exit status answers
// whether anything is.
func newDoctor(service graph.Service, restacker restack.Service, published push.Known, presentation Presentation) *cobra.Command {
	var remote string
	cmd := &cobra.Command{
		Use:     "doctor",
		GroupID: groupLook,
		Short:   "Find what needs putting right, across every recorded stack (read-only, offline)",
		Long: "Reads every recorded stack and reports only what is not as it should be, each with the command " +
			"that puts it right: a branch whose parent moved, one deleted or renamed with plain git, a parent " +
			"that is gone, work that has already landed, a restack that stopped part-way, a branch that has " +
			"diverged from its remote.\n\n" +
			"It asks nothing of the network. It exits 0 when it finds nothing, and 1 when it finds something.",
		Args: cobra.NoArgs,
	}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		presentation := presentation.resolve(cmd)
		ctx, cancel := newBudgets(cmd).discovery(commandContext(cmd.Context(), cmd, "read_only", "", ""))
		defer cancel()
		from, err := doctorStart(ctx, service)
		if err != nil {
			return err
		}
		discovery, err := service.Discover(ctx, graph.Selection{Branch: from, Scope: graph.ScopeAll})
		if err != nil {
			return err
		}
		interrupted, err := restacker.InProgress(ctx)
		if err != nil {
			return err
		}
		publishing, err := readPublished(ctx, published, remote, cmd.Flags().Changed("remote"), discovery)
		if err != nil {
			return err
		}
		findings := diagnose(discovery, interrupted, publishing, remote)
		if err := writeStackView(cmd.OutOrStdout(), doctorView(discovery, findings), presentation); err != nil {
			return err
		}
		if len(findings) != 0 {
			return foundProblems(len(findings))
		}
		return nil
	}
	cmd.Flags().StringVar(&remote, "remote", "origin", "the remote whose last-known branches each one is compared with")
	return cmd
}

// doctorStart is the branch a whole-repository read is anchored on. Every
// scope is chosen relative to one, and "all" answers the same from any of them
// — but a detached HEAD, which is where someone mid-rebase stands, has none,
// and that is exactly when doctor is wanted. A recorded root answers instead.
func doctorStart(ctx context.Context, service graph.Service) (string, error) {
	if current, err := service.Git.CurrentBranch(ctx); err == nil {
		return current, nil
	}
	recorded, err := service.Store.Load(ctx)
	if err != nil {
		return "", err
	}
	roots := recorded.Roots()
	if len(roots) == 0 {
		return "", errors.New("HEAD is detached and nothing is recorded, so there is nothing to examine")
	}
	return roots[0], nil
}
