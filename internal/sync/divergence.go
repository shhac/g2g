package sync

import (
	"fmt"
	"strings"

	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/repair"
)

// divergence is one branch both sides have moved, and by how much.
type divergence struct {
	Branch       string
	Ours, Theirs int
}

// divergenceWays is the choice a divergence leaves, and there is exactly one
// place it can be made: --take is the only path where sync discards work that
// exists nowhere else, and it has no "mine" value.
//
// The command keeps the selection the refusal came from. A bare
// "g2g pull --take published" told a sync of the whole trunk to run a sync of
// one stack, and told a bounded one to drop its boundary and take everything.
//
// The remote goes with it. "Published" is the version on the remote this pull
// read, and a suggestion without it names origin's version instead — the wrong
// side of a choice that discards commits.
func divergenceWays(selection graph.Selection, remote string, take Take, parents map[string]string, stuck []divergence) []repair.Step {
	command := "g2g pull"
	if selection.Branch != "" {
		command += " --branch " + repair.Quote(selection.Branch)
	}
	if selection.Scope == graph.ScopeTrunk {
		command += " --scope " + string(graph.ScopeTrunk)
	}
	if remote != "" && remote != localgit.DefaultRemote {
		command += " --remote " + repair.Quote(remote)
	}
	command += " --take " + string(SidePublished)
	if through := widened(take, parents, stuck); through != "" {
		command += " --through " + repair.Quote(through)
	}
	return []repair.Step{
		{Command: command, Effect: fmt.Sprintf("take the version %s has and discard yours", remoteName(remote))},
		{Effect: "reconcile it yourself"},
	}
}

// remoteName is the remote as a sentence names it. An empty one is what a
// caller that never chose one means, which is origin.
func remoteName(remote string) string {
	if remote == "" {
		return localgit.DefaultRemote
	}
	return remote
}

// widened is the boundary that covers what was refused as well as what was
// already decided, when one branch is stacked on all of it.
//
// Where none is -- the refusals are on different forks -- no boundary covers
// them, and the way through is to drop it: an unbounded take reaches exactly
// the diverged branches, which is these and the ones already decided.
func widened(take Take, parents map[string]string, stuck []divergence) string {
	if !take.Bounded() {
		return ""
	}
	for _, candidate := range stuck {
		if !stackedOn(candidate.Branch, take.Through, parents) {
			continue
		}
		covers := true
		for _, other := range stuck {
			covers = covers && stackedOn(candidate.Branch, other.Branch, parents)
		}
		if covers {
			return candidate.Branch
		}
	}
	return ""
}

// divergenceReason says which branches both sides moved, and what each side
// holds that the other does not.
//
// Naming only one side was the problem: "you have work the published version
// does not" is true of every ordinary commit, so a reader who had just made one
// could not tell whether that was what the message meant. Both counts make it
// unambiguous, and the counts are by content, so a commit the other side
// already has under a different id is not counted against you.
func divergenceReason(stuck []divergence) string {
	parts := make([]string, 0, len(stuck))
	for _, moved := range stuck {
		parts = append(parts, fmt.Sprintf("%s (%d here that %s not published, %d published that %s not here)",
			moved.Branch,
			moved.Ours, pick(moved.Ours, "is", "are"),
			moved.Theirs, pick(moved.Theirs, "is", "are")))
	}
	return fmt.Sprintf("both sides have moved on %s", strings.Join(parts, ", "))
}

// pick chooses the form that agrees with a count.
func pick(count int, one, many string) string {
	if count == 1 {
		return one
	}
	return many
}
