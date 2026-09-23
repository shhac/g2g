package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/shhac/g2g/internal/comment"
	"github.com/shhac/g2g/internal/shape"
	"github.com/shhac/g2g/internal/stack"
	"github.com/shhac/g2g/internal/submit"
)

func newSubmit(service submit.Service, comments comment.Service, completions stack.Completions, guard func(context.Context) error, presentation Presentation) *cobra.Command {
	options := submitOptions{guard: guard, comments: comments}
	cmd := &cobra.Command{Use: "submit", GroupID: groupPublish, Short: "Publish a stack and create missing draft PRs (preview by default)", Args: cobra.NoArgs}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		if err := options.selection.validate(); err != nil {
			return err
		}
		if err := options.validate(); err != nil {
			return err
		}
		return options.run(cmd, service, presentation.resolve(cmd))
	}
	options.selection.register(cmd, completions, stack.ReadableSources, "local branch to submit (defaults to current branch)", "trunk to use as the submit base")
	// A GitHub native stack is linear, so these are the two scopes that can
	// produce one. stack still refuses when it forks, naming the remedy.
	options.selection.registerScope(cmd, shape.ProjectScopes, shape.ScopeStack, scopeUsage("submit", shape.ProjectScopes))
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
	cmd.Flags().BoolVar(&options.noComment, "no-comment", false, "do not keep the stack comment on each pull request afterwards")
	return cmd
}

type submitOptions struct {
	budgets budgets
	// comments keeps the stack comment on each pull request once the stack
	// is published, unless noComment.
	comments  comment.Service
	noComment bool
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

// validate refuses flags that would be silently ignored. A template prefills
// the bodies of a spec being created; a spec given with --spec already has its
// bodies, and a preview naming the template it would not use is a preview of
// something else. --keep-spec keeps the document --edit creates, and there is
// no such document without it.
func (o submitOptions) validate() error {
	if o.specPath != "" && (o.template != "" || o.noTemplate) {
		return fmt.Errorf("--template and --no-template choose how a new spec is prefilled, and --spec %s already carries its bodies · drop the template flag, or use --write-spec or --edit", o.specPath)
	}
	if o.keepSpec && !o.edit {
		return fmt.Errorf("--keep-spec keeps the document --edit creates · add --edit, or drop --keep-spec")
	}
	return nil
}

// keepsComments reports whether this run keeps the stack comments afterwards.
func (o submitOptions) keepsComments() bool { return !o.noComment && o.comments.Ready() }

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
		return o.writeDraft(cmd, plan, chosenTemplate, resolveDraft(submit.DefaultDraft, o.ready, o.noReady), presentation)
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
	if err := writeSubmitPreview(cmd.OutOrStdout(), plan, p, template, draft, o.keepsComments()); err != nil {
		return err
	}
	if plan.Blocked() != "" {
		return prose(cmd.OutOrStdout(), p, "\n"+p.notice("No changes were made.")+" Apply would refuse until that is resolved.")
	}
	return prose(cmd.OutOrStdout(), p, "\n"+p.notice("No changes were made.")+" Create a spec with: "+runnable("g2g submit --write-spec <private-temp-dir>"+readyFlag(draft)))
}

func (o submitOptions) previewWithSpec(cmd *cobra.Command, plan submit.Plan, p Presentation, template string, draft bool) error {
	if err := writeSubmitPreview(cmd.OutOrStdout(), plan, p, template, draft, o.keepsComments()); err != nil {
		return err
	}
	// A preview that is already blocked must not close by inviting an apply
	// that will refuse; the rendered view names the reason.
	if plan.Blocked() != "" {
		return prose(cmd.OutOrStdout(), p, "\n"+p.notice("No changes were made.")+" Apply would refuse until that is resolved.")
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
			if validated.Push.Blocked != "" {
				return submit.Plan{}, fmt.Errorf("submit cannot publish: %s", validated.Push.Blocked)
			}
			return validated, nil
		},
		render: func(w io.Writer, plan submit.Plan, presentation Presentation) error {
			return writeSubmitPreview(w, plan, presentation, template, spec.Draft, o.keepsComments())
		},
		guard: o.guard,
		execute: func(ctx context.Context, plan submit.Plan) error {
			if err := service.Apply(ctx, plan, spec); err != nil {
				return err
			}
			if o.edit && !o.keepSpec {
				_ = os.RemoveAll(filepath.Dir(o.specPath))
			}
			selection := o.selection.Selection()
			selection.Branch = plan.Snapshot.Target
			return keepComments(ctx, o.comments, o.keepsComments(), selection)
		},
		// The pull requests exist and are linked whatever happens to their
		// comments, so a failure there is not the submission failing.
		interrupted: func(_ context.Context, err error) (bool, error) {
			return commentsNotKept(cmd, err, p)
		},
		branches: func(plan submit.Plan) int { return len(plan.Snapshot.Branches) },
		wrapMutationError: func(err error) error {
			return fmt.Errorf("submission spec retained at %s: %w", o.specPath, err)
		},
		notices: flowNotices{
			preview:       "Re-run with --apply to push, create missing PRs, and link.",
			applied:       "Applied — stack published and missing pull requests created",
			changed:       "Changes were made.",
			recovery:      fmt.Sprintf("Re-running g2g submit --spec %s --apply is safe: it preserves existing pull requests and creates only the missing ones.", o.specPath),
			suggestedNext: "g2g status",
		},
	}
	return flow.run(cmd, o.root, o.budgets, p, true)
}
