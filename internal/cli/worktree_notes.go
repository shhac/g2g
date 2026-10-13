package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/shhac/g2g/internal/words"
	"github.com/shhac/g2g/internal/worktree"
)

// worktreeNotes says what a plan does about the branches other worktrees hold:
// which move and bring their worktrees along, and which stay where they are,
// why, and what brings them up to date later. A branch left is part of what
// the command was asked to do and did not, so it is never quiet about one.
//
// finish is the command that, run in the worktree holding a left branch,
// brings it up to date. trunk is the branch whose staying behind means the
// remote's trunk is not taken at all, empty where nothing advances one.
func worktreeNotes(view stackView, outcome worktree.Outcome, policy worktree.Policy, finish string, trunk string) stackView {
	for _, holder := range outcome.Follow {
		view = view.note(fmt.Sprintf("Moves %s in %s too, bringing that worktree's files along · it has nothing uncommitted.", holder.Branch, holder.Path), severityNeutral)
	}
	for _, holder := range outcome.Leave {
		why := holder.Why()
		if holder.Clean && holder.Busy == "" {
			why = fmt.Sprintf("it is checked out in %s, and --worktrees %s leaves it", holder.Path, policy)
		}
		then := fmt.Sprintf("run %s in %s once it is free, to bring it up to date", runnable(finish), holder.Path)
		if holder.Branch == trunk {
			then = fmt.Sprintf("nothing is taken from the remote or replayed onto the trunk it has · run %s in %s once it is free, then this again", runnable("g2g pull --trunk-only"), holder.Path)
		}
		view = view.note(fmt.Sprintf("Leaves %s where it is: %s · %s.", holder.Branch, why, then), severityWarn)
	}
	return view
}

// leftOnly is the no-op line for a plan whose only work was left in other
// worktrees: "already up to date" would be false.
func leftOnly(outcome worktree.Outcome) string {
	if len(outcome.Leave) == 0 {
		return ""
	}
	return fmt.Sprintf("Nothing else to do: %s %s left where %s.",
		words.List(branchesOf(outcome.Leave)), words.Pick(len(outcome.Leave), "is", "are"), words.Pick(len(outcome.Leave), "it is", "they are"))
}

func branchesOf(holders []worktree.Holder) []string {
	branches := make([]string, 0, len(holders))
	for _, holder := range holders {
		branches = append(branches, holder.Branch)
	}
	return branches
}

// worktreeNotBrought reports a run that did everything except bring another
// worktree's files along: its branch moved, and the worktree still describes
// the commit before. That is not "not applied", and it is not done either.
func worktreeNotBrought(cmd *cobra.Command, err error, p Presentation) (bool, error) {
	return claim(err, func(notBrought *worktree.NotBrought) error {
		if err := prose(cmd.OutOrStdout(), p, "\n"+p.problem("Done, but "+notBrought.Holder.Path+" could not be brought along with "+notBrought.Holder.Branch+".")); err != nil {
			return err
		}
		return writeWhatStands(cmd, p, "Every branch is where it was meant to go. Run "+runnable(notBrought.Finish())+" to bring that worktree's files to its branch.", notBrought)
	})
}
