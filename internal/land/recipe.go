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
	for index, step := range p.Steps {
		if step.Merges() {
			if step.Push {
				commands = append(commands, Command{
					Command: fmt.Sprintf("g2g push --branch %s --scope branch --apply", step.Branch),
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
		commands = append(commands, p.cleanupCommands(step, index)...)
	}
	return commands
}

func (p Plan) cleanupCommands(step Step, index int) []Command {
	commands := make([]Command, 0, 4)
	if index != len(p.Steps)-1 {
		commands = append(commands, Command{
			Command: "g2g sync --apply",
			Effect:  "advance the trunk and replay what is left onto it",
		})
	}
	if p.Options.Forget {
		commands = append(commands, Command{
			Command: fmt.Sprintf("g2g prune --branch %s --scope branch --apply", step.Branch),
			Effect:  "forget it, once what sat on it has been reparented",
		})
	}
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
