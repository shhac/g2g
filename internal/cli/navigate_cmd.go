package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/navigate"
	"github.com/shhac/g2g/internal/stack"
)

// Moving the checkout is the one thing here that happens without --apply.
//
// It changes no ref, no record and no remote, and git switch already refuses
// to carry a local change it would overwrite, so a preview in front of it would
// only be a second command to type. What it keeps is the refusal to choose: a
// fork names the branches above it and stops. --dry-run is for scripts that
// want the destination without the move.

type navigation struct {
	direction navigate.Direction
	use       string
	short     string
	long      string
	// counted directions take an optional number of steps.
	counted bool
}

var navigations = []navigation{
	{
		direction: navigate.Up, use: "up [n]", counted: true,
		short: "Switch to the branch above this one (moves the checkout)",
		long:  "Switches to the branch directly above this one, or n branches up. At a fork it refuses and names the branches above, because choosing between them is a guess.",
	},
	{
		direction: navigate.Down, use: "down [n]", counted: true,
		short: "Switch to the branch below this one (moves the checkout)",
		long:  "Switches to the branch directly below this one, or n branches down. From the bottom of a stack that is the trunk; from the trunk there is nothing below.",
	},
	{
		direction: navigate.Top, use: "top",
		short: "Switch to the top of this stack (moves the checkout)",
		long:  "Follows the branch above until there is none. At a fork it refuses and names the branches above, because choosing between them is a guess.",
	},
	{
		direction: navigate.Bottom, use: "bottom",
		short: "Switch to the bottom of this stack (moves the checkout)",
		long:  "Switches to the first branch above the trunk on the way down from this one.",
	},
}

func newNavigations(service navigate.Service, completions stack.Completions, guard func(context.Context) error, presentation Presentation) []*cobra.Command {
	commands := make([]*cobra.Command, 0, len(navigations))
	for _, each := range navigations {
		commands = append(commands, newNavigate(each, service, completions, guard, presentation))
	}
	return commands
}

func newNavigate(each navigation, service navigate.Service, completions stack.Completions, guard func(context.Context) error, presentation Presentation) *cobra.Command {
	var selection stackOptions
	var dryRun bool
	cmd := &cobra.Command{
		Use:     each.use,
		GroupID: groupMove,
		Short:   each.short,
		Long:    each.long + "\n\nThis moves the checkout directly, with no --apply: it changes no ref, no record and no remote, and git switch refuses to overwrite a local change. --dry-run prints the destination instead.",
		Args:    cobra.NoArgs,
	}
	if each.counted {
		cmd.Args = cobra.MaximumNArgs(1)
	}
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		presentation := presentation.resolve(cmd)
		if err := selection.validate(); err != nil {
			return err
		}
		steps, err := parseSteps(args)
		if err != nil {
			return err
		}
		mode := "switch"
		if dryRun {
			mode = "dry_run"
		}
		root := commandContext(cmd.Context(), cmd, mode, "", selection.trunk)
		budgets := newBudgets(cmd)
		ctx, cancel := budgets.discovery(root)
		defer cancel()
		// Mid-restack the checkout is part-way through a rebase, and switching
		// away from it is how that rebase's state gets stranded.
		if guard != nil && !dryRun {
			if err := guard(ctx); err != nil {
				return err
			}
		}
		move, err := service.Plan(ctx, navigate.Request{Direction: each.direction, Steps: steps, From: stack.Source(selection.from), Trunk: selection.trunk})
		if err != nil {
			return budgets.discoveryTimedOut(err)
		}
		if move.Blocked != "" {
			if err := writeStackView(cmd.OutOrStdout(), moveView(move), presentation); err != nil {
				return err
			}
			return errors.New(move.Blocked)
		}
		if !dryRun {
			switchCtx, cancelSwitch := budgets.mutation(root, 1)
			defer cancelSwitch()
			if err := service.Switch(switchCtx, move); err != nil {
				return mutationTimeout(err, "The checkout may or may not have moved · run git status to see where it is.")
			}
		}
		return writeMove(cmd.OutOrStdout(), move, dryRun, presentation)
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the destination and the git switch that would reach it, without switching")
	selection.registerSource(cmd, completions, stack.ReadableSources, "trunk the stack sits on, where a Graphite ancestry declares more than one")
	return cmd
}

func parseSteps(args []string) (int, error) {
	if len(args) == 0 {
		return 1, nil
	}
	steps, err := strconv.Atoi(args[0])
	if err != nil || steps < 1 {
		return 0, fmt.Errorf("%q is not a number of branches to move · pass a whole number from 1", args[0])
	}
	return steps, nil
}

// writeMove says where the checkout went. A person reads one line; a machine
// reads the same view every other command emits, with the destination as its
// target and the switch as its command.
func writeMove(writer io.Writer, move navigate.Move, dryRun bool, p Presentation) error {
	if p.machine() {
		return writeStackView(writer, moveView(move), p)
	}
	if move.Arrived() {
		return prose(writer, p, p.notice("Already there")+" · "+p.branch(move.Destination)+" "+p.subdued("is the "+string(move.Direction)+" of its stack."))
	}
	if !dryRun {
		return prose(writer, p, p.notice("Switched to ")+p.branch(move.Destination)+"  "+p.subdued("· "+describeMove(move)))
	}
	if err := prose(writer, p, p.accent("Would switch to ")+p.branch(move.Destination)+"  "+p.subdued("· "+describeMove(move))); err != nil {
		return err
	}
	return prose(writer, p, commandLine(commandText(switchCommand(move)), p))
}

func switchCommand(move navigate.Move) []string {
	return []string{"git", "switch", move.Destination}
}

// describeMove is how the destination was reached, which is what a target's
// source says everywhere else.
func describeMove(move navigate.Move) string {
	switch move.Direction {
	case navigate.Up, navigate.Down:
		return fmt.Sprintf("%s %d from %s", move.Direction, move.Steps, move.Origin)
	default:
		return fmt.Sprintf("%s of the stack from %s", move.Direction, move.Origin)
	}
}

// moveView is the walk as a stack view: the trunk, then the branches passed
// through in stack order, with the destination as the target.
func moveView(move navigate.Move) stackView {
	view := stackView{Operation: string(move.Direction), Target: move.Destination, TargetSource: describeMove(move)}
	if move.Blocked != "" {
		view.Target, view.TargetSource = move.Origin, "current Git branch"
	}
	if move.Base != "" {
		view.Nodes = append(view.Nodes, stackNode{Branch: move.Base, Trunk: true, Target: view.Target == move.Base})
	}
	walked := slices.Clone(move.Walked)
	if move.Direction == navigate.Down || move.Direction == navigate.Bottom {
		slices.Reverse(walked)
	}
	for _, branch := range walked {
		if branch == move.Base {
			continue
		}
		view.Nodes = append(view.Nodes, stackNode{Branch: branch, Parent: move.Parents[branch], Target: branch == view.Target})
	}
	if move.Blocked != "" {
		// There is no apply to block: the move itself is what was refused.
		view = view.refusing(move.Blocked, move.Repair)
		view.BlockedHeading = "Not moved"
		return view
	}
	if !move.Arrived() {
		view.Action = switchCommand(move)
	}
	return view
}

// recordedChildren answers what the g2g graph records directly on a branch,
// from one read of the store.
type recordedChildren struct{ service graph.Service }

func (r recordedChildren) Children(ctx context.Context, branch string) ([]string, error) {
	adopted, err := r.service.Store.Load(ctx)
	if err != nil {
		return nil, err
	}
	return adopted.Children(branch), nil
}
