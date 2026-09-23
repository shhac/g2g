package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/link"
	"github.com/shhac/g2g/internal/shape"
	"github.com/shhac/g2g/internal/stack"
)

func newLink(service link.Service, completions stack.Completions, guard func(context.Context) error, presentation Presentation) *cobra.Command {
	var selection stackOptions
	var apply bool
	cmd := &cobra.Command{
		Use:     "link",
		GroupID: groupTools,
		Short:   "Link a stack to GitHub's native stacks (preview by default)",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			presentation := presentation.resolve(cmd)
			if err := selection.validate(); err != nil {
				return err
			}
			root := commandContext(cmd.Context(), cmd, applyMode(apply), selection.branch, selection.trunk)
			flow := applyFlow[link.Plan]{
				plan: func(ctx context.Context) (link.Plan, error) {
					return linkable(service.Plan(ctx, selection.Selection()))
				},
				revalidate: func(ctx context.Context, preview link.Plan) (link.Plan, error) {
					return service.Revalidate(ctx, selection.Selection(), preview)
				},
				render:   writeLinkPlan,
				guard:    guard,
				execute:  service.Execute,
				branches: func(plan link.Plan) int { return len(plan.Branches) },
				noOp:     link.Plan.NothingToLink,
				blocked: func(plan link.Plan) string {
					if len(plan.Issues) == 0 {
						return ""
					}
					return blockedReason(plan)
				},
				notices: flowNotices{
					preview:       "Re-run with --apply to link.",
					noOp:          "No changes were needed or made.",
					applied:       "Applied — GitHub stack updated",
					changed:       "Changes were made.",
					recovery:      "Run g2g github status to see whether GitHub recorded the link.",
					suggestedNext: "g2g github status",
				},
			}
			return flow.run(cmd, root, newBudgets(cmd), presentation, apply)
		},
	}
	selection.register(cmd, completions, stack.ReadableSources, "local branch to link (defaults to current branch)", "trunk to use as the link base")
	// A GitHub native stack is linear, so these are the two scopes that can
	// produce one. stack still refuses when it forks, naming the remedy.
	selection.registerScope(cmd, shape.ProjectScopes, shape.ScopeStack, scopeUsage("link", shape.ProjectScopes))
	cmd.Flags().BoolVar(&apply, "apply", false, "invoke gh stack link after revalidation")
	return cmd
}

// linkable refuses a forked selection before anything is shown as a plan. The
// plan itself is shared with status, which reads a fork happily, so the
// refusal belongs to the command that projects it.
func linkable(plan link.Plan, err error) (link.Plan, error) {
	if err != nil {
		return link.Plan{}, err
	}
	return plan, plan.Snapshot.RequireLinear("link")
}

func writeLinkPlan(writer io.Writer, plan link.Plan, presentation Presentation) error {
	return writeStackView(writer, linkView(plan), presentation)
}

// writeNotApplied renders the outcome of a failed mutation and returns the
// error marked as presented, so the top-level printer reports it without
// repeating the diagnostic block.
func writeNotApplied(writer io.Writer, presentation Presentation, err error) error {
	if presentation.machine() {
		return err
	}
	fmt.Fprintln(writer)
	fmt.Fprintln(writer, presentation.problem("Not applied"))

	summary := err.Error()
	var commandErr *githubstack.CommandError
	if errors.As(err, &commandErr) {
		summary = commandErr.Summary()
	}
	// A refusal's reason is the same sentence the preview showed, so a command
	// it names is drawn the same way here.
	fmt.Fprintln(writer, presentation.drawCommands(summary, ""))

	diagnostic := commandDiagnostic(err)
	if diagnostic == "" {
		return notAppliedError{err}
	}
	fmt.Fprintln(writer)
	fmt.Fprintln(writer, presentation.subdued("Diagnostic:"))
	for _, line := range strings.Split(diagnostic, "\n") {
		fmt.Fprintln(writer, presentation.subdued("  "+line))
	}
	return presentedError{err: notAppliedError{err}}
}

// notAppliedError is a failure writeNotApplied has already told a person
// about, so a caller composing commands knows not to say it again.
type notAppliedError struct{ err error }

func (e notAppliedError) Error() string { return e.err.Error() }
func (e notAppliedError) Unwrap() error { return e.err }

func toldNotApplied(err error) bool {
	var told notAppliedError
	return errors.As(err, &told)
}

type outputFlusher interface{ Flush() error }

func flushOutput(writer io.Writer) error {
	if flusher, ok := writer.(outputFlusher); ok {
		if err := flusher.Flush(); err != nil {
			return fmt.Errorf("flush ready-to-apply output: %w", err)
		}
	}
	return nil
}
