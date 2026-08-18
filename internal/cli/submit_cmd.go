package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/shape"
	"github.com/shhac/g2g/internal/stack"
	"github.com/shhac/g2g/internal/submit"
)

func newSubmit(service submit.Service, completions stack.Completions, guard func(context.Context) error, presentation Presentation) *cobra.Command {
	options := submitOptions{guard: guard}
	cmd := &cobra.Command{Use: "submit", GroupID: groupPublish, Short: "Publish a stack and create missing draft PRs (preview by default)", Args: cobra.NoArgs}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		if err := options.selection.validate(); err != nil {
			return err
		}
		return options.run(cmd, service, presentation.resolve(cmd))
	}
	options.selection.register(cmd, completions, stack.ReadableSources, "local branch to submit (defaults to current branch)", "trunk to use as the submit base")
	// A GitHub native stack is linear, so these are the two scopes that can
	// produce one. stack still refuses when it forks, naming the remedy.
	options.selection.registerScope(cmd, shape.ProjectScopes, stack.ScopeStack, scopeUsage("submit", shape.ProjectScopes))
	cmd.Flags().StringVar(&options.remote, "remote", "origin", "Git remote to push to")
	cmd.Flags().StringVar(&options.specPath, "spec", "", "submission JSON spec to validate or apply")
	cmd.Flags().StringVar(&options.writeSpec, "write-spec", "", "write a draft spec in a private temporary directory, without applying")
	cmd.Flags().BoolVar(&options.edit, "edit", false, "create and edit one temporary submission spec document")
	cmd.Flags().BoolVar(&options.keepSpec, "keep-spec", false, "keep the temporary --edit spec after a successful apply")
	cmd.Flags().StringVar(&options.template, "template", "", "repository pull request template name to prefill generated specs")
	cmd.Flags().BoolVar(&options.noTemplate, "no-template", false, "do not prefill bodies from a repository template")
	// Draft is the default and has no flag of its own. Opening a pull request
	// ready for review notifies reviewers and cannot be taken back, so it is
	// the thing a person opts into; there is nothing to opt into about a draft.
	// --no-ready exists only to overrule a spec that asked for ready.
	cmd.Flags().BoolVar(&options.ready, "ready", false, "create missing pull requests ready for review instead of as drafts")
	cmd.Flags().BoolVar(&options.noReady, "no-ready", false, "create missing pull requests as drafts even if the spec asks for ready")
	cmd.MarkFlagsMutuallyExclusive("ready", "no-ready")
	cmd.MarkFlagsMutuallyExclusive("edit", "spec")
	cmd.MarkFlagsMutuallyExclusive("edit", "write-spec")
	cmd.Flags().BoolVar(&options.apply, "apply", false, "atomically push, create missing PRs, and link after revalidation")
	return cmd
}

type submitOptions struct {
	budgets budgets
	// guard refuses the command while another operation has left the
	// repository part-way through a rewrite.
	guard      func(context.Context) error
	root       context.Context
	selection  stackOptions
	remote     string
	specPath   string
	writeSpec  string
	template   string
	apply      bool
	ready      bool
	noReady    bool
	noTemplate bool
	edit       bool
	keepSpec   bool
}

func (o *submitOptions) run(cmd *cobra.Command, service submit.Service, presentation Presentation) error {
	o.budgets = newBudgets(cmd)
	o.root = commandContext(cmd.Context(), cmd, "submit", applyMode(o.apply), o.selection.branch, o.selection.trunk)
	ctx, cancel := o.budgets.discovery(o.root)
	defer cancel()
	plan, err := service.Plan(ctx, o.selection.Selection(), o.remote)
	if err != nil {
		return err
	}
	chosenTemplate, templateName, err := resolveTemplate(o.template, o.noTemplate)
	if err != nil {
		return err
	}
	if o.writeSpec != "" {
		return o.writeDraft(cmd, plan, chosenTemplate, resolveDraft(submit.DefaultDraft, o.ready, o.noReady))
	}
	// --edit creates the document this command then reads. Returning the path
	// rather than assigning o.specPath as a side effect keeps the dispatch
	// below readable from here: previously the variable it switches on was set
	// inside a method several calls away.
	if o.edit {
		o.specPath, err = o.editedSpec(ctx, cmd, plan, chosenTemplate)
		if err != nil {
			return err
		}
	}
	if o.specPath == "" {
		// No spec has been read yet, so the choice is whatever the flags say
		// over the default a fresh spec would carry.
		return o.previewWithoutSpec(cmd, plan, presentation, templateName, resolveDraft(submit.DefaultDraft, o.ready, o.noReady))
	}
	spec, err := submit.Read(o.specPath, plan.Snapshot.Branches)
	if err != nil {
		return actionableSpecError(err, o.specPath)
	}
	spec.Draft = resolveDraft(spec.Draft, o.ready, o.noReady)
	if !o.apply {
		return o.previewWithSpec(cmd, plan, presentation, templateName, spec.Draft)
	}
	return o.applyPlan(cmd, service, plan, spec, presentation, templateName)
}

func (o submitOptions) previewWithoutSpec(cmd *cobra.Command, plan submit.Plan, p Presentation, template string, draft bool) error {
	if err := writeSubmitPreview(cmd.OutOrStdout(), plan, p, template, draft); err != nil {
		return err
	}
	return prose(cmd.OutOrStdout(), p, "\n"+p.notice("No changes were made.")+" Create a spec with: g2g submit --write-spec <private-temp-dir>"+readyFlag(draft))
}

func (o submitOptions) previewWithSpec(cmd *cobra.Command, plan submit.Plan, p Presentation, template string, draft bool) error {
	if err := writeSubmitPreview(cmd.OutOrStdout(), plan, p, template, draft); err != nil {
		return err
	}
	return prose(cmd.OutOrStdout(), p, "\n"+p.notice("No changes were made.")+" Re-run with --apply"+readyFlag(draft)+" to push, create missing PRs, and link.")
}

func (o submitOptions) applyPlan(cmd *cobra.Command, service submit.Service, preview submit.Plan, spec submit.Spec, p Presentation, template string) error {
	flow := applyFlow[submit.Plan]{
		// The preview is already in hand, so planning is a pass-through; the
		// sequence still re-discovers through revalidate before mutating.
		plan: func(context.Context) (submit.Plan, error) { return preview, nil },
		revalidate: func(ctx context.Context, preview submit.Plan) (submit.Plan, error) {
			validated, err := service.Revalidate(ctx, o.selection.Selection(), o.remote, preview)
			if err != nil {
				return submit.Plan{}, err
			}
			if len(validated.Issues) != 0 {
				return submit.Plan{}, fmt.Errorf("submit preview has blocked existing pull requests; repair the marked branches and rerun")
			}
			return validated, nil
		},
		render: func(w io.Writer, plan submit.Plan, presentation Presentation) error {
			return writeSubmitPreview(w, plan, presentation, template, spec.Draft)
		},
		guard: o.guard,
		execute: func(ctx context.Context, plan submit.Plan) error {
			if err := service.Apply(ctx, plan, spec); err != nil {
				return err
			}
			if o.edit && !o.keepSpec {
				_ = os.RemoveAll(filepath.Dir(o.specPath))
			}
			return nil
		},
		branches: func(plan submit.Plan) int { return len(plan.Snapshot.Branches) },
		wrapMutationError: func(err error) error {
			return fmt.Errorf("submission spec retained at %s: %w", o.specPath, err)
		},
		notices: flowNotices{
			preview:  "Re-run with --apply to push, create missing PRs, and link.",
			applied:  "Applied — stack published and missing pull requests created",
			changed:  "Changes were made.",
			recovery: fmt.Sprintf("Re-running g2g submit --spec %s --apply is safe: it preserves existing pull requests and creates only the missing ones.", o.specPath),
		},
	}
	return flow.run(cmd, o.root, o.budgets, p, true)
}

func submitView(plan submit.Plan, template string, draft bool) stackView {
	view := stackView{
		Operation:    "submit",
		Target:       plan.Snapshot.Target,
		TargetSource: plan.Snapshot.TargetSource,
		Nodes:        []stackNode{{Branch: plan.Snapshot.Base, Trunk: true}},
	}
	for _, branch := range plan.Snapshot.Branches {
		node := stackNode{Branch: branch, Target: branch == plan.Snapshot.Target, State: "create " + openAs(draft)}
		previous, replaced := plan.Superseded[branch]
		existing := existingNumber(plan, branch)
		switch {
		case plan.Issues[branch] != "":
			node.State, node.Severity = "blocked: "+plan.Issues[branch], severityBad
		case existing != 0:
			node.PRNumber, node.State, node.Severity = existing, "existing", severityOK
		case replaced:
			node.State = fmt.Sprintf("create %s · #%d %s", openAs(draft), previous.Number, strings.ToLower(previous.State))
		}
		view.Nodes = append(view.Nodes, node)
	}

	if template != "" {
		view = view.note("PR template: "+template, severityNeutral)
	}
	if len(plan.Issues) != 0 {
		return view.block("Apply blocked: repair the marked existing pull requests first.")
	}
	return view.note(fmt.Sprintf("Missing PRs will be created %s; existing PRs are preserved.", openAsPlural(draft)), severityNeutral)
}

// openAs names what a missing pull request will be opened as. The preview is
// rendered and flushed immediately before the mutation, so saying "draft" while
// about to open ready for review is not a cosmetic error: it is the last thing
// a person reads before reviewers are notified.
func openAs(draft bool) string {
	if draft {
		return "draft"
	}
	return "ready for review"
}

func openAsPlural(draft bool) string {
	if draft {
		return "as drafts"
	}
	return "ready for review"
}

// readyFlag echoes the choice back in any command this preview suggests, so a
// copied command reproduces the run that was previewed.
func readyFlag(draft bool) string {
	if draft {
		return ""
	}
	return " --ready"
}

func writeSubmitPreview(w io.Writer, plan submit.Plan, p Presentation, template string, draft bool) error {
	return writeStackView(w, submitView(plan, template, draft), p)
}

// existingNumber routes through the shared resolution rather than scanning for
// the first open head, so the preview cannot disagree with the plan about
// which pull request represents a branch.
func existingNumber(plan submit.Plan, branch string) int {
	if resolution := githubstack.ResolveHeads(plan.Existing)[branch]; resolution.Open != nil {
		return resolution.Open.Number
	}
	return 0
}
