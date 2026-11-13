package land

import (
	"fmt"

	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/repair"
)

// Command is one line of the recipe: something a person could run, and what
// running it achieves.
type Command struct {
	Command string
	Effect  string
}

// Commands writes the descent out as the commands someone would run by hand to
// do the same thing.
//
// It is derived from the same Steps that Apply walks, in the same order, so the
// recipe a preview shows and the work an apply does cannot come to describe
// different things -- the failure push.pushArgs exists to prevent, one level up.
//
// The lines are equivalents rather than transcripts: landing composes the
// services in process rather than shelling out to itself. What is promised is
// that running these in this order reaches the same place, which is what makes
// the preview an answer to "I do not trust this yet" rather than a description
// of one.
func (p Plan) Commands() []Command {
	if p.Queue != nil && p.Options.Wait {
		return []Command{{Command: p.RetryCommand() + " --apply", Effect: "wait for confirmed queue merges before replaying and cleaning up the stack"}}
	}
	commands := make([]Command, 0, len(p.Steps)*4)
	for _, step := range p.Steps {
		commands = append(commands, p.mergeCommands(step)...)
		if step.Queue && !p.Options.Wait && step.Merges() {
			return commands
		}
		commands = append(commands, p.cleanupCommands(step)...)
	}
	commands = append(commands, p.republishCommands()...)
	return append(commands, p.commentCommands()...)
}

// mergeCommands are what lands one branch: publishing it, aiming its pull
// request at the trunk, and the merge. A branch that has already landed has
// none, only the cleanup after it.
func (p Plan) mergeCommands(step Step) []Command {
	if !step.Merges() {
		return nil
	}
	commands := make([]Command, 0, 3)
	if step.Queue && step.Pending != "" {
		return []Command{{Command: fmt.Sprintf("gh pr view %d", step.Number), Effect: "observe the existing merge request; cleanup waits for confirmed merge"}}
	}
	if step.Push {
		commands = append(commands, Command{
			Command: p.pushCommand(step.Branch),
			Effect:  "publish it as it is here",
		})
	}
	if p.retargetsAtMerge(step) {
		commands = append(commands, Command{
			Command: fmt.Sprintf("gh pr edit %d --base %s", step.Number, repair.Quote(step.Base)),
			Effect:  fmt.Sprintf("merge into %s rather than %s", step.Base, step.From),
		})
	}
	return append(commands, Command{
		Command: landedVariable(step) + "=$(" + repair.Command([]string{"git", "rev-parse", "--verify", "refs/heads/" + step.Branch}) + ")",
		Effect:  "remember the head about to merge, which the merge and the deletion after it are pinned to",
	}, Command{
		Command: p.mergeCommand(step),
		Effect:  mergeEffect(step),
	})
}

// landedVariable names the shell variable a recipe keeps a merging branch's
// head in. The head is captured just before the merge, once this branch has
// been published as it is now: every branch above the first is replayed
// before its turn, so the head the plan saw is one it no longer has. Uses
// require a nonempty value: a failed or omitted capture must not disable the
// head check on gh pr merge.
func landedVariable(step Step) string {
	return fmt.Sprintf("g2g_landed_%d", step.Number)
}

// recipeHead is the recipe's expected head for step: the variable its merge
// line captured, or for a branch already landed, the head the plan found in
// the trunk -- as Apply pins them. Empty when there is none to pin.
func recipeHead(step Step) (variable, literal string) {
	if step.Merges() {
		return landedVariable(step), ""
	}
	return "", step.LandedHead
}

// republishCommands publish what the syncs replayed above the descent, once,
// and before the comments are kept, so the map is written about pull requests
// that already show where their branches now sit.
func (p Plan) republishCommands() []Command {
	commands := make([]Command, 0, len(p.Republish))
	for _, above := range p.Republish {
		commands = append(commands, Command{
			Command: p.pushCommand(above.Branch),
			Effect:  fmt.Sprintf("publish it, replayed onto %s", p.Trunk),
		})
	}
	return commands
}

// pushCommand publishes one branch the way the descent does.
func (p Plan) pushCommand(branch string) string {
	command := fmt.Sprintf("g2g push --branch %s --scope path", repair.Quote(branch))
	if p.Options.Remote != Defaults().Remote {
		command += " --remote " + repair.Quote(p.Options.Remote)
	}
	if p.Options.Upstream == localgit.LeaveUpstream {
		command += " --no-set-upstream"
	}
	return command + " --apply"
}

// KeepsComments reports whether the descent ends by keeping the stack
// comments on what is left above it. The recipe and the command's tail both
// ask this, because they answered it separately once and disagreed: a descent
// that only tidied up after merges made in a browser kept the comments without
// the preview saying so.
func (p Plan) KeepsComments() bool {
	return p.Options.Comment && len(p.Above) != 0
}

// commentCommands keep the stack comments on what is left, once. Doing it
// after every merge would edit every comment once per branch for a map that
// is only true at the end.
func (p Plan) commentCommands() []Command {
	if !p.KeepsComments() {
		return nil
	}
	commands := make([]Command, 0, len(p.Above))
	for _, above := range p.Above {
		commands = append(commands, Command{
			Command: fmt.Sprintf("g2g github comment --branch %s --apply", repair.Quote(above)),
			Effect:  "keep the stack comments, with what landed listed as merged",
		})
	}
	return commands
}

// cleanupCommands is what follows a branch's merge, and the sync is in it for
// every branch because Apply runs it for every branch: after the last one it
// still advances the trunk here onto the merge. Leaving it out of the last
// step described a descent that ended with the trunk behind its remote.
func (p Plan) cleanupCommands(step Step) []Command {
	commands := make([]Command, 0, 4)
	if p.KeepTrunk {
		ref := localgit.IsolatedRef(p.Options.Remote, p.Trunk)
		commands = append(commands, Command{
			Command: repair.Command([]string{"git", "fetch", "--refmap=", "--no-write-fetch-head", p.Options.Remote, "+refs/heads/" + p.Trunk + ":" + ref}),
			Effect:  "fetch the merge without moving the local trunk",
		}, Command{
			Command: repair.Command([]string{"g2g", "untrack", "--branch", step.Branch, "--apply"}),
			Effect:  "forget it, after checking its work is in the fetched trunk, recording what sat on it on the trunk",
		})
		if p.Detach {
			commands = append(commands, Command{Command: repair.Command([]string{"git", "switch", "--detach", ref}), Effect: "move off the merged branch without checking out the held trunk"})
		}
		return append(commands, p.deletions(step)...)
	}
	if p.declared() {
		commands = append(commands, p.declaredCleanup(step)...)
		return append(commands, p.deletions(step)...)
	}
	commands = append(commands, Command{
		Command: "g2g pull --apply",
		Effect:  "advance the trunk and replay what is left onto it",
	})
	commands = append(commands, Command{
		Command: fmt.Sprintf("g2g prune --branch %s --scope branch --apply", repair.Quote(step.Branch)),
		Effect:  "forget it, once what sat on it has been reparented",
	})
	return append(commands, p.deletions(step)...)
}

// deletions remove the landed branch, here and on the remote.
//
// The remote one is leased on the head that landed, as Apply's is: a
// reviewer's commit pushed after the merge makes git refuse rather than take
// it. Never a bare lease, which trusts the tracking ref, nor a tip read now,
// which always matches. With no head to pin there is nothing safe to offer,
// and Apply leaves that branch too.
//
// Every pull request still sitting on the branch is pointed at the trunk
// first, because deleting a base with a push closes what sits on it.
func (p Plan) deletions(step Step) []Command {
	commands := make([]Command, 0, 2)
	for _, move := range p.moves(step) {
		commands = append(commands, Command{
			Command: fmt.Sprintf("gh pr edit %d --base %s", move.Number, repair.Quote(move.Base)),
			Effect:  fmt.Sprintf("aim %s at %s first: deleting %s with a push would close its pull request", move.Head, move.Base, move.From),
		})
	}
	variable, literal := recipeHead(step)
	if p.Options.DeleteRemote && (variable != "" || literal != "") {
		target := []string{p.Options.Remote, "--delete", "refs/heads/" + step.Branch}
		// The lease names the head the merge line captured, so the shell
		// expands it; everything else is quoted as written.
		command := "git push " + repair.Quote("--force-with-lease=refs/heads/"+step.Branch+":") + `"${` + variable + `:?}" ` + repair.Command(target)
		if variable == "" {
			command = repair.Command(append([]string{"git", "push", localgit.Lease{Branch: step.Branch, Expected: literal}.Argument()}, target...))
		}
		commands = append(commands, Command{
			Command: command,
			Effect:  "remove the published branch while it is still what landed, if the merge has not already",
		})
	}
	if p.Options.DeleteLocal {
		commands = append(commands, Command{
			Command: fmt.Sprintf("git branch -D %s", repair.Quote(step.Branch)),
			Effect:  "remove it here",
		})
	}
	return commands
}

// mergeCommand is pinned to the head its recipe captured, as Apply's merge
// is: a push to the branch between the capture and the merge makes GitHub
// refuse rather than merge something nobody published here.
func mergeCommand(step Step, options Options) string {
	pinned := ` --match-head-commit "${` + landedVariable(step) + `:?}"`
	if step.Queue {
		return fmt.Sprintf("gh pr merge %d", step.Number) + pinned
	}
	command := fmt.Sprintf("gh pr merge %d %s", step.Number, options.Method.Flag()) + pinned
	if step.Admin {
		command += " --admin"
	}
	return command
}

func mergeEffect(step Step) string {
	if step.Queue {
		return "request a merge through the queue; cleanup waits for confirmed merge"
	}
	return fmt.Sprintf("land %s", step.Branch)
}

// A ruleset queue can disallow auto-merge, which gh pr merge cannot currently
// handle. Use the same head-pinned GraphQL mutation the adapter sends, pinned
// to the captured head rather than the plan's, which a publish just replaced.
func (p Plan) mergeCommand(step Step) string {
	if !step.Queue || p.Queue.AutoMergeAllowed {
		return mergeCommand(step, p.Options)
	}
	query := `mutation Enqueue($pr: ID!, $head: GitObjectID!) { enqueuePullRequest(input: {pullRequestId: $pr, expectedHeadOid: $head}) { clientMutationId } }`
	return repair.Command([]string{"gh", "api", "graphql", "-f", "query=" + query, "-f", "pr=" + step.ID}) + ` -f "head=${` + landedVariable(step) + `:?}"`
}
