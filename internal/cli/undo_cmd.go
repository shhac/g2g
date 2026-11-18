package cli

import (
	"context"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/shhac/g2g/internal/diagnostic"
	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/oplog"
	"github.com/shhac/g2g/internal/repair"
	"github.com/shhac/g2g/internal/restack"
	"github.com/shhac/g2g/internal/undo"
	"github.com/shhac/g2g/internal/words"
)

// recorded are the commands whose operations are written down to be undone:
// the ones that change only this repository. A command that publishes is not
// among them, because putting back the local half of something other people
// have seen is not undoing it.
var recorded = []string{"adopt", "create", "delete", "fold", "prune", "pull", "rename", "restack", "track", "untrack"}

// publishing are the commands that send branches to a remote. What they
// publish is recorded, never to be undone, so that an undo of something
// earlier knows the remote has seen those branches since.
var publishing = []string{"land", "push", "submit"}

// operations is what recording an operation needs: the repository as it is,
// before the mutation and after.
type operations struct {
	git   operationsGit
	store graph.Store
}

type operationsGit interface {
	oplog.Reader
	CommonDir(ctx context.Context) (string, error)
	WorktreeDir(ctx context.Context) (string, error)
}

// operationsOf is the recorder for a graph service, nil when its Git cannot
// be read that way, which records nothing.
func operationsOf(service graph.Service) *operations {
	git, ok := service.Git.(operationsGit)
	if !ok || service.Store == nil {
		return nil
	}
	return &operations{git: git, store: service.Store}
}

type operationsKey struct{}

// withOperations hands every command the recorder, through its context.
func withOperations(cmd *cobra.Command, recorder *operations) {
	parent := cmd.Context()
	if parent == nil {
		parent = context.Background()
	}
	cmd.SetContext(context.WithValue(parent, operationsKey{}, recorder))
}

// recording is the recorder for a command whose operations are written down,
// nil for one whose are not.
func recording(cmd *cobra.Command) *operations {
	if cmd.Parent() != cmd.Root() || !slices.Contains(slices.Concat(recorded, publishing), cmd.Name()) || cmd.Context() == nil {
		return nil
	}
	recorder, _ := cmd.Context().Value(operationsKey{}).(*operations)
	return recorder
}

// around brackets a mutation: it reads the repository before, and hands back
// what to call once the mutation has finished, which reads it again and
// writes down the difference. For a command that publishes it writes the
// marker now and hands back nothing to do. A nil recorder records nothing.
//
// names cuts the difference to the branches the operation has to do with,
// and moved to the ones it may move; a branch that came into being or went
// away is its own either way. Nil takes everything. A reading that fails
// records nothing, says so, and never fails the operation it was for.
func (o *operations) around(ctx context.Context, cmd *cobra.Command, event string, names, moved []string) func(context.Context) {
	nothing := func(context.Context) {}
	if o == nil {
		return nothing
	}
	unrecorded := func(err error) {
		diagnostic.Warn(ctx, "undo.record", fmt.Sprintf("this operation was not recorded for g2g undo: %v", err))
	}
	before, err := oplog.Read(ctx, o.git, o.store)
	if err != nil {
		unrecorded(err)
		return nothing
	}
	worktree, _ := o.git.WorktreeDir(ctx)
	asked := askedFor(cmd)
	// pull --prune is two mutations under one command, recorded one after
	// the other, and the second has to be told from the first.
	if cmd.Name() == "pull" && event == "prune" {
		asked += " · the prune"
	}
	if slices.Contains(publishing, cmd.Name()) {
		if err := undo.Publishing(ctx, o.git, asked, worktree, among(before, names), time.Now()); err != nil {
			unrecorded(err)
		}
		return nothing
	}
	return func(ctx context.Context) {
		after, err := oplog.Read(ctx, o.git, o.store)
		if err != nil {
			unrecorded(err)
			return
		}
		var touched, moves func(string) bool
		if names != nil {
			touched = func(branch string) bool {
				_, was := before.Refs["refs/heads/"+branch]
				_, is := after.Refs["refs/heads/"+branch]
				return slices.Contains(names, branch) || was != is
			}
			moves = func(branch string) bool { return slices.Contains(moved, branch) }
		}
		if err := undo.Record(ctx, o.git, asked, worktree, before, after, touched, moves, time.Now()); err != nil {
			unrecorded(err)
		}
	}
}

// among is the local branches a publication names, or every one when it
// cannot say.
func among(reading oplog.Reading, names []string) []string {
	branches := make([]string, 0)
	for ref := range reading.Refs {
		if branch, local := strings.CutPrefix(ref, "refs/heads/"); local && (names == nil || slices.Contains(names, branch)) {
			branches = append(branches, branch)
		}
	}
	slices.Sort(branches)
	return branches
}

// askedFor is the command as it was asked for, less the flags that say how
// to run it, quoted so that it can be read back as one.
func askedFor(cmd *cobra.Command) string {
	asked := append(strings.Fields(cmd.CommandPath()), cmd.Flags().Args()...)
	return repair.Command(append(asked, requestOf(cmd)...))
}

func newUndo(service undo.Service, guard idleGuard, presentation Presentation) *cobra.Command {
	var apply, list bool
	cmd := &cobra.Command{
		Use:     "undo",
		GroupID: groupUpdate,
		Short:   "Put back what the last local operation changed (preview by default)",
		Long: "Puts back what the last recorded operation made from this worktree changed: the branches it moved, created or " +
			"deleted, what the graph records, and where this checkout stood.\n\n" +
			"It is refused if anything that operation changed has changed again since, so it never undoes work done " +
			"after it. It is local: nothing pushed, merged or retargeted is touched. Operations made from other worktrees " +
			"are not offered, and --list shows them. Operations recorded are those of " +
			words.List(recorded) + ".",
		Args: cobra.NoArgs,
	}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		presentation := presentation.resolve(cmd)
		ctx := commandContext(cmd.Context(), cmd, applyMode(apply), "", "")
		if list {
			return newBudgets(cmd).reading(ctx, func(ctx context.Context) error {
				entries, worktree, err := service.List(ctx)
				if err != nil {
					return err
				}
				return writeStackView(cmd.OutOrStdout(), operationsView(entries, worktree, time.Now()), presentation)
			})
		}
		flow := applyFlow[undo.Plan]{
			plan:         service.Plan,
			revalidation: revalidation{"undo", "what there is to undo"},
			render: func(writer io.Writer, plan undo.Plan, p Presentation) error {
				return writeStackView(writer, undoView(plan, time.Now()), p)
			},
			guard: guard,
			touches: func(plan undo.Plan) restack.Footprint {
				return restack.Moves(slices.Concat(plan.Branches(), slices.Collect(maps.Keys(plan.Entry.Edges)))...)
			},
			execute:  service.Apply,
			branches: func(plan undo.Plan) int { return len(plan.Entry.Refs) },
			noOp:     undo.Plan.Nothing,
			blocked:  undo.Plan.Blocked,
			interrupted: func(_ context.Context, _ undo.Plan, cause error) (bool, error) {
				return claim(cause, func(stopped *undo.Stopped) error {
					return writeStoppedPartWay(cmd, presentation, "Undo stopped part-way: "+stopped.Err.Error(),
						"Put back before it stopped: "+words.List(stopped.Restored)+". Preview "+runnable("g2g undo")+" to see what is left.", stopped)
				})
			},
			notices: flowNotices{
				preview:  "Rerun with --apply to put it back.",
				noOp:     "Nothing to undo: no operation is recorded here, or each has been put back.",
				applied:  "Undone.",
				changed:  "Everything that operation changed here is as it was before it. Nothing published was touched.",
				recovery: "Some of it may have been put back; preview g2g undo to see what is left.",
			},
		}
		return flow.run(cmd, ctx, newBudgets(cmd), presentation, apply)
	}
	cmd.Flags().BoolVar(&apply, "apply", false, "put the operation back instead of previewing it")
	cmd.Flags().BoolVar(&list, "list", false, "list the recorded operations, newest first, instead of undoing one")
	cmd.MarkFlagsMutuallyExclusive("apply", "list")
	return cmd
}

// undoView draws what an undo puts back: each branch the operation touched,
// with where it goes, and then the record and the checkout.
func undoView(plan undo.Plan, now time.Time) stackView {
	view := stackView{Operation: "undo"}
	if !plan.Found {
		return view
	}
	entry := plan.Entry
	view.Target, view.TargetSource = entry.Command, ago(now.Sub(entry.At))
	for _, branch := range plan.Branches() {
		move := entry.Refs["refs/heads/"+branch]
		view.Nodes = append(view.Nodes, stackNode{Branch: branch}.labeled(putBack(move), severityWarn))
	}
	for _, branch := range slices.Sorted(maps.Keys(entry.Edges)) {
		view = view.note(recordedAgain(branch, entry.Edges[branch]), severityNeutral)
	}
	if entry.RootsBefore != nil {
		view = view.note("Records "+words.List(entry.RootsBefore.Trunks)+" as the trunks again.", severityNeutral)
	}
	if slices.ContainsFunc(plan.Branches(), func(branch string) bool { return entry.Refs["refs/heads/"+branch].After == "" }) {
		view = view.note("A branch that is restored does not get back the upstream it was set to track.", severityNeutral)
	}
	if others := len(entry.Refs) - len(plan.Branches()); others != 0 {
		view = view.note(fmt.Sprintf("Puts back %d of g2g's own refs: fork points and sync points.", others), severityNeutral)
	}
	if plan.Returns {
		view = view.note("Returns this checkout to "+strings.TrimPrefix(entry.Here.Before, "refs/heads/")+".", severityNeutral)
	}
	if plan.Blocked() != "" {
		return view.refusing(plan.Repair)
	}
	return view
}

// putBack says what happens to one branch.
func putBack(move oplog.Move) string {
	switch {
	case move.Before == "":
		return "removed · it was created at " + localgit.Short(move.After)
	case move.After == "":
		return "restored at " + localgit.Short(move.Before)
	default:
		return "back to " + localgit.Short(move.Before) + " from " + localgit.Short(move.After)
	}
}

// recordedAgain says what the graph goes back to saying of one branch.
func recordedAgain(branch string, was oplog.Recorded) string {
	switch {
	case was.Before == nil:
		return "Forgets the record of " + branch + "."
	case was.After == nil:
		return "Records " + branch + " on " + was.Before.Parent + " again."
	case was.Before.Parent != was.After.Parent:
		return "Records " + branch + " on " + was.Before.Parent + " again, not " + was.After.Parent + "."
	default:
		return "Records where " + branch + " starts on " + was.Before.Parent + " as it was."
	}
}

// operationsView lists the recorded operations, newest first, marking the
// one an undo here would put back.
func operationsView(entries []oplog.Entry, worktree string, now time.Time) stackView {
	view := stackView{Operation: "undo", Target: "this repository", TargetSource: "recorded operations"}
	if len(entries) == 0 {
		return view.note("No operation is recorded here.", severityNeutral)
	}
	next := true
	for _, entry := range entries {
		line := ago(now.Sub(entry.At)) + " · " + entry.Command
		switch {
		case len(entry.Published) != 0:
			line += " · published, not undone from here"
		case entry.Undone:
			line += " · undone"
		case entry.Worktree != worktree:
			// Somebody else's, as likely as not, and theirs to put back.
			line += " · from another worktree, undone from there"
		case next:
			line, next = line+" · next to undo", false
		}
		view = view.note(line, severityNeutral)
	}
	return view
}
