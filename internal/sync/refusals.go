package sync

import (
	"fmt"
	"strings"

	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/repair"
	"github.com/shhac/g2g/internal/syncpoint"
)

// The refusals a drop meets: each says which side dropped or replayed what,
// why taking either version would lose the other's work, and the ways out.

// keepWays offers keeping the commits named, when there are any; a refusal
// about replacements alone has nothing a --keep can usefully name.
func keepWays(c collecting, commits []string, effect string) []repair.Step {
	if len(commits) == 0 {
		return nil
	}
	return []repair.Step{{Command: c.keepCommand(commits), Effect: effect}}
}

func listed(commits []string) string {
	short := make([]string, 0, len(commits))
	for _, commit := range commits {
		short = append(short, localgit.Short(commit))
	}
	return strings.Join(short, ", ")
}

func droppedBothWays(c collecting, branch string, changes syncpoint.Changes) *repair.Note {
	return &repair.Note{
		Reason: fmt.Sprintf("%s was dropped from or replayed on both sides since the last pull or push: %s gone from %s, and %s gone from here",
			branch, listed(changes.LeftUpstream()), c.remote, listed(changes.LeftHere())),
		Ways: append(keepWays(c, changes.DroppedHere, "take "+c.remote+"'s version, keeping what you dropped as theirs"),
			repair.Step{Effect: "reconcile it yourself"}),
	}
}

func yoursOverTheirDrop(c collecting, branch string, changes syncpoint.Changes) *repair.Note {
	return &repair.Note{
		Reason: fmt.Sprintf("%s dropped or replayed %s on %s, and you have committed %s on it since: taking theirs would lose yours, and keeping yours would keep what they replaced",
			c.remote, listed(changes.LeftUpstream()), branch, listed(changes.Mine)),
		Ways: append(keepWays(c, changes.DroppedUpstream, "keep what "+c.remote+" dropped, with your commits on it"),
			repair.Step{Command: c.takeCommand, Effect: "take " + c.remote + "'s version and discard yours"},
			repair.Step{Effect: "rebase your commits onto theirs yourself, then pull"}),
	}
}

func emptiedUpstream(c collecting, branch string, gone []string) *repair.Note {
	return &repair.Note{
		Reason: fmt.Sprintf("%s has none of %s's own commits any more (%s), which is a branch emptied or made again rather than one a commit was dropped from",
			c.remote, branch, listed(gone)),
		Ways: []repair.Step{
			{Command: c.keepCommand(gone), Effect: "keep them"},
			{Command: c.takeCommand, Effect: "take " + c.remote + "'s version and discard them"},
		},
	}
}

func droppedHereAndNew(c collecting, branch string, changes syncpoint.Changes) *repair.Note {
	return &repair.Note{
		Reason: fmt.Sprintf("you dropped or replayed %s on %s, and %s has %s on it you do not: taking theirs would put back what you replaced",
			listed(changes.LeftHere()), branch, c.remote, listed(changes.New)),
		Ways: append(keepWays(c, changes.DroppedHere, "take theirs, keeping what you dropped"),
			repair.Step{Effect: "reconcile it yourself"}),
	}
}
