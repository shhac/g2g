package land

import (
	"fmt"

	"github.com/shhac/g2g/internal/githubstack"
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
	commands := make([]Command, 0, len(p.Steps)*4)
	for _, step := range p.Steps {
		if step.Merges() {
			if step.Push {
				commands = append(commands, Command{
					Command: fmt.Sprintf("g2g push --branch %s --scope path --apply", step.Branch),
					Effect:  "publish it as it is here",
				})
			}
			if step.Retargets() {
				commands = append(commands, Command{
					Command: fmt.Sprintf("gh pr edit %d --base %s", step.Number, step.Base),
					Effect:  fmt.Sprintf("merge into %s rather than %s", step.Base, step.From),
				})
			}
			commands = append(commands, Command{
				Command: mergeCommand(step, p.Options),
				Effect:  fmt.Sprintf("land %s", step.Branch),
			})
		}
		commands = append(commands, p.cleanupCommands(step)...)
	}
	commands = append(commands, p.republishCommands()...)
	return append(commands, p.commentCommands()...)
}

// republishCommands publish what the syncs replayed above the descent, once,
// and before the comments are kept, so the map is written about pull requests
// that already show where their branches now sit.
func (p Plan) republishCommands() []Command {
	commands := make([]Command, 0, len(p.Republish))
	for _, above := range p.Republish {
		commands = append(commands, Command{
			Command: fmt.Sprintf("g2g push --branch %s --scope path --apply", above.Branch),
			Effect:  fmt.Sprintf("publish it, replayed onto %s", p.Trunk),
		})
	}
	return commands
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
			Command: fmt.Sprintf("g2g github comment --branch %s --apply", above),
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
	if p.declared() {
		commands = append(commands, p.declaredCleanup(step)...)
		return append(commands, p.deletions(step)...)
	}
	commands = append(commands, Command{
		Command: "g2g pull --apply",
		Effect:  "advance the trunk and replay what is left onto it",
	})
	commands = append(commands, Command{
		Command: fmt.Sprintf("g2g prune --branch %s --scope branch --apply", step.Branch),
		Effect:  "forget it, once what sat on it has been reparented",
	})
	return append(commands, p.deletions(step)...)
}

// deletions remove the landed branch, here and on the remote.
func (p Plan) deletions(step Step) []Command {
	commands := make([]Command, 0, 2)
	if p.Options.DeleteRemote {
		commands = append(commands, Command{
			Command: fmt.Sprintf("git push %s --delete %s", p.Options.Remote, step.Branch),
			Effect:  "remove the published branch, if the merge has not already",
		})
	}
	if p.Options.DeleteLocal {
		commands = append(commands, Command{
			Command: fmt.Sprintf("git branch -D %s", step.Branch),
			Effect:  "remove it here",
		})
	}
	return commands
}

func mergeCommand(step Step, options Options) string {
	command := fmt.Sprintf("gh pr merge %d --%s", step.Number, options.Method)
	if step.Admin {
		command += " --admin"
	}
	return command
}

// MergeMethodFlag echoes the chosen method back in a command a preview
// suggests, so a rerun keeps the choice rather than silently squashing.
func MergeMethodFlag(method githubstack.Method) string {
	if method == githubstack.MethodSquash {
		return ""
	}
	return " --method " + string(method)
}
