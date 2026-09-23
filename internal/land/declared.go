package land

import (
	"context"
	"fmt"
	"strings"

	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/repair"
	"github.com/shhac/g2g/internal/shape"
	"github.com/shhac/g2g/internal/stack"
)

// Landing a declared trunk into what it lands into is the ordinary cycle with
// one branch in it. What differs is only what the record says about that
// branch: how it merges, that there is nothing above it to replay, and that
// forgetting it means ending a declaration rather than dropping an edge. None
// of it is a rule of land's own — the merge is still GitHub's, the advance
// still pull's, and "has it landed" still the content check prune uses.

// declared is the declaration of the branch being landed, when it is a trunk
// that lands into the base this descent merges into; zero otherwise.
//
// Only a stack g2g described can be one: another source placing the branch on
// the same base is not the declaration talking.
func declared(recorded graph.Graph, discovery stack.Discovery) graph.Declaration {
	declaration, lands := recorded.Landing(discovery.Target)
	if !lands || declaration.Into != discovery.Base || discovery.Source != stack.SourceG2G {
		return graph.Declaration{}
	}
	return declaration
}

// declared reports a descent that lands a declared trunk.
func (p Plan) declared() bool { return p.Declaration.Lands() }

// declaredMethod is how the declaration says the trunk lands. The record holds
// a plain string, because the graph depends on Git alone, so this is where a
// method that is not one is refused.
func declaredMethod(declaration graph.Declaration) (githubstack.Method, repair.Note) {
	method, err := githubstack.ParseMethod(declaration.By)
	if err != nil {
		return "", repair.Note{
			Reason: fmt.Sprintf("it is declared to land by %q, which is not a way to merge", declaration.By),
			Ways: []repair.Step{
				{Command: fmt.Sprintf("g2g track --as-trunk --into %s --by <method>", declaration.Into), Effect: "declare how it lands again"},
				{Command: "g2g land --method <method>", Effect: "or name the method for this descent"},
			},
		}
	}
	return method, repair.Note{}
}

// declaredRefusal refuses a declared trunk that still has something on it.
//
// What sits on it would have to be replayed from a shared branch's commits onto
// what it lands into, in the middle of a descent. The ordinary flow lands those
// into it first, so refusing and naming them is honest where half a design for
// carrying them across would not be. Another trunk that lands into it is
// refused for the same reason and one more: the cleanup deletes the branch it
// lands into.
func declaredRefusal(recorded graph.Graph, target string) repair.Note {
	if children := recorded.Children(target); len(children) != 0 {
		return repair.Note{
			Reason: fmt.Sprintf("%s still has %s recorded on it, which would have nowhere to land once it has gone", target, strings.Join(children, ", ")),
			Ways: []repair.Step{
				{Command: "g2g land --branch " + children[len(children)-1], Effect: fmt.Sprintf("land what sits on it into %s first", target)},
				{Command: "g2g track --branch <branch> --parent <other>", Effect: "or record it somewhere else"},
			},
		}
	}
	if dependents := recorded.Dependents(target); len(dependents) != 0 {
		return repair.Note{
			Reason: fmt.Sprintf("the cleanup deletes %s, which these trunks land into: %s", target, strings.Join(dependents, ", ")),
			Ways: []repair.Step{
				{Command: "g2g land --branch " + dependents[0], Effect: fmt.Sprintf("land it into %s first", target)},
				{Command: "g2g untrack --branch " + dependents[0], Effect: "or stop it being a trunk"},
			},
		}
	}
	return repair.Note{}
}

// pushSelection is what publishing the stack is checked against before the
// first merge. A declared trunk is a stack of one on what it lands into, which
// only a path asks for: its stack scope is the stacks above it.
func pushSelection(plan Plan) stack.Selection {
	scope := shape.ScopeStack
	if plan.declared() {
		scope = shape.ScopePath
	}
	return stack.Selection{Branch: plan.Target, Trunk: plan.Trunk, Scope: scope}
}

// syncSelection is what bringing the base up to date asks pull for. A declared
// trunk sits under nothing and has nothing above it by the time it lands, so
// the base alone is advanced: widening that to the base's stack would replay
// every stack on it in the middle of this descent.
func syncSelection(plan Plan, target string) graph.Selection {
	if plan.declared() {
		return graph.Selection{Branch: plan.Trunk, Scope: graph.ScopeBranch}
	}
	return graph.Selection{Branch: target, Scope: graph.ScopeStack}
}

// undeclare forgets a declared trunk once Git finds its work where it landed.
// It has no edge to drop and nothing sat on it, so this is ending the
// declaration, through the same untrack a person would run.
func (s Service) undeclare(ctx context.Context, landed, into string) error {
	if !s.landed(ctx, landed, into) {
		return fmt.Errorf("%s merged, but git does not find its work in %s by content, so it is left declared · run g2g status to see why", landed, into)
	}
	plan, err := s.Graph.PlanUntrack(ctx, graph.Selection{Branch: landed})
	if err != nil {
		return err
	}
	return s.Graph.ApplyUntrack(ctx, plan)
}

// declaredCleanup is the recipe's cleanup for a declared trunk: pull has no
// command that advances a base alone, so the line is Git's own, and forgetting
// it is untrack rather than prune.
func (p Plan) declaredCleanup(step Step) []Command {
	commands := []Command{{
		Command: fmt.Sprintf("git fetch %s %s:%s", p.Options.Remote, p.Trunk, p.Trunk),
		Effect:  fmt.Sprintf("advance %s to the merge · where it is checked out, git pull --ff-only there instead", p.Trunk),
	}}
	if p.Options.Forget {
		commands = append(commands, Command{
			Command: fmt.Sprintf("g2g untrack --branch %s --apply", step.Branch),
			Effect:  "stop it being a trunk, now it has landed",
		})
	}
	return commands
}
