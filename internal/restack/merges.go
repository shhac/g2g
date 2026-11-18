package restack

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/repair"
	"github.com/shhac/g2g/internal/words"
)

// carriesMerge refuses a rewrite whose replay range holds a merge commit,
// naming the lowest such branch.
//
// Neither engine can carry one. git replay stops on it, which a preview met
// as a raw fatal from the prediction. git rebase is worse: it drops the merge
// and offers every commit it brought in as the branch's own, so a branch that
// merged another stack's work would come out carrying a copy of that work on
// the conflict path, and on any Git too old to replay. Rebuilding the merge
// (rebase --rebase-merges) merges again from scratch and loses whatever was
// resolved or amended in the original, silently when it happens to merge
// cleanly.
//
// So the plan refuses before anything moves, including the branches below
// that could have been replayed, and names the way that keeps the merge:
// merging the parent in. A branch that holds its parent's tip has nothing to
// replay, and recording where it forks is all that is left.
//
// A branch that collapses hands the engines nothing and is not asked, and one
// that is only recorded has no step at all. It is one process for each branch
// actually replayed, bounded by that branch's own commits.
func (s Service) carriesMerge(ctx context.Context, recorded graph.Graph, onto Onto, steps []Step) (repair.Note, error) {
	moving := make([]string, 0, len(steps))
	for _, step := range steps {
		moving = append(moving, step.Branch)
		if step.Collapses {
			continue
		}
		merges, err := s.Git.Merges(ctx, step.ForkPoint, step.start())
		if err != nil {
			return repair.Note{}, err
		}
		if len(merges) != 0 {
			return mergeInRange(step, merges, destinationOf(recorded, onto, step), slices.Contains(moving, step.Parent)), nil
		}
	}
	return repair.Note{}, nil
}

// destination is where a step's branch goes, by the two names a reader needs:
// what to merge to hold the commit it would be replayed onto, and the parent
// the graph records for it afterwards. They differ when a caller replays onto
// somewhere that is not the parent, as an --onto naming a remote's copy and
// pull both do.
type destination struct{ merge, parent string }

func destinationOf(recorded graph.Graph, onto Onto, step Step) destination {
	if onto.Object == "" || step.Parent != onto.Object {
		return destination{merge: step.Parent, parent: step.Parent}
	}
	return destination{
		merge:  cmp.Or(onto.Named, onto.Object),
		parent: cmp.Or(onto.Parent, recorded.Edges[step.Branch].Parent),
	}
}

// mergeInRange is the refusal and its way out, in the order to take it. A
// parent this rewrite would move has to be moved first: merged in as it is
// now, its replay would leave the branch holding the commits it had before.
func mergeInRange(step Step, merges []string, to destination, parentMoves bool) repair.Note {
	named := localgit.Short(merges[0])
	if others := len(merges) - 1; others != 0 {
		named += " and " + words.Count(others, "other", "others")
	}
	ways := make([]repair.Step, 0, 3)
	if parentMoves {
		ways = append(ways, repair.Step{
			Command: repair.Command([]string{"g2g", "restack", "--branch", step.Parent, "--scope", string(graph.ScopePath)}),
			Effect:  "bring " + step.Parent + " up to date first",
		})
	}
	return repair.Note{
		Reason: fmt.Sprintf("%s's own commits include merge commit %s, which a replay would flatten into the commits %s merged",
			step.Branch, named, words.Pick(len(merges), "it", "they")),
		Ways: append(ways,
			repair.Step{
				Command: repair.Command([]string{"git", "merge", to.merge}),
				Effect:  fmt.Sprintf("merge it into %s yourself, with %s checked out, which keeps %s", step.Branch, step.Branch, words.Pick(len(merges), "the merge", "the merges")),
			},
			repair.Step{
				Command: repair.Command([]string{"g2g", "track", "--branch", step.Branch, "--parent", to.parent}),
				Effect:  "record where it forks once that is merged in",
			},
		),
	}
}
