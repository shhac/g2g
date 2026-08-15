package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/shhac/gt2gh/internal/link"
	"github.com/shhac/gt2gh/internal/submit"
)

func newSubmit(service submit.Service, linkService link.Service, presentation Presentation) *cobra.Command {
	var options submitOptions
	cmd := &cobra.Command{Use: "submit", Short: "Publish a Graphite stack and create missing draft PRs (preview by default)", Args: cobra.NoArgs}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		return options.run(cmd, service, presentation)
	}
	options.selection.register(cmd, linkService, "Graphite-tracked local branch to submit (defaults to current branch)", "Graphite-declared trunk to use as the submit base")
	cmd.Flags().StringVar(&options.remote, "remote", "origin", "Git remote to push to")
	cmd.Flags().StringVar(&options.specPath, "spec", "", "submission JSON spec to validate or apply")
	cmd.Flags().StringVar(&options.writeSpec, "write-spec", "", "write a draft spec in a private temporary directory, without applying")
	cmd.Flags().BoolVar(&options.edit, "edit", false, "create and edit one temporary submission spec document")
	cmd.Flags().BoolVar(&options.keepSpec, "keep-spec", false, "keep the temporary --edit spec after a successful apply")
	cmd.Flags().StringVar(&options.template, "template", "", "repository pull request template name to prefill generated specs")
	cmd.Flags().BoolVar(&options.noTemplate, "no-template", false, "do not prefill bodies from a repository template")
	cmd.Flags().BoolVar(&options.draft, "draft", true, "create missing pull requests as drafts")
	cmd.Flags().BoolVar(&options.ready, "ready", false, "create missing pull requests ready for review")
	cmd.MarkFlagsMutuallyExclusive("draft", "ready")
	cmd.MarkFlagsMutuallyExclusive("edit", "spec")
	cmd.MarkFlagsMutuallyExclusive("edit", "write-spec")
	cmd.Flags().BoolVar(&options.apply, "apply", false, "atomically push, create missing PRs, and link after revalidation")
	return cmd
}

type submitOptions struct {
	budgets    budgets
	root       context.Context
	selection  stackOptions
	remote     string
	specPath   string
	writeSpec  string
	template   string
	apply      bool
	draft      bool
	ready      bool
	noTemplate bool
	edit       bool
	keepSpec   bool
}

func (o *submitOptions) run(cmd *cobra.Command, service submit.Service, presentation Presentation) error {
	mode := "preview"
	if o.apply {
		mode = "apply"
	}
	o.budgets = newBudgets(cmd)
	o.root = commandContext(cmd.Context(), cmd, "submit", mode, o.selection.branch, o.selection.trunk)
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
		return o.writeDraft(cmd, plan, chosenTemplate)
	}
	if err := o.prepareSpec(ctx, cmd, plan, chosenTemplate); err != nil {
		return err
	}
	if o.specPath == "" {
		return o.previewWithoutSpec(cmd, plan, presentation, templateName)
	}
	spec, err := submit.Read(o.specPath, plan.Snapshot.Branches)
	if err != nil {
		return actionableSpecError(err, o.specPath)
	}
	spec.Draft = resolveDraft(cmd, spec.Draft, o.draft, o.ready)
	if !o.apply {
		return o.previewWithSpec(cmd, plan, presentation, templateName)
	}
	return o.applyPlan(ctx, cmd, service, plan, spec, presentation, templateName)
}

func (o *submitOptions) writeDraft(cmd *cobra.Command, plan submit.Plan, body string) error {
	path, err := submit.Write(o.writeSpec, submit.NewSpec(plan.Snapshot.Branches, body))
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Wrote draft submission spec: %s\n", path)
	_, err = fmt.Fprintln(cmd.OutOrStdout(), "Next: add a title for every PR, then run g2g submit --spec "+path+" to validate it.")
	return err
}

func (o *submitOptions) prepareSpec(ctx context.Context, cmd *cobra.Command, plan submit.Plan, body string) error {
	if !o.edit {
		return nil
	}
	dir, err := os.MkdirTemp("", "g2g-submit-")
	if err != nil {
		return err
	}
	o.specPath, err = submit.Write(dir, submit.NewSpec(plan.Snapshot.Branches, body))
	if err != nil {
		return err
	}
	if err := editSpec(ctx, o.specPath); err != nil {
		return fmt.Errorf("submission spec retained at %s: %w", o.specPath, err)
	}
	if !o.apply {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Edited submission spec: "+o.specPath)
	}
	return nil
}

func (o submitOptions) previewWithoutSpec(cmd *cobra.Command, plan submit.Plan, p Presentation, template string) error {
	if err := writeSubmitPreview(cmd.OutOrStdout(), plan, p, template); err != nil {
		return err
	}
	_, err := fmt.Fprintln(cmd.OutOrStdout(), p.notice("No changes were made. Create a spec with: g2g submit --write-spec <private-temp-dir>"))
	return err
}

func (o submitOptions) previewWithSpec(cmd *cobra.Command, plan submit.Plan, p Presentation, template string) error {
	if err := writeSubmitPreview(cmd.OutOrStdout(), plan, p, template); err != nil {
		return err
	}
	_, err := fmt.Fprintln(cmd.OutOrStdout(), p.notice("No changes were made. Re-run with --apply to atomically push, create missing PRs, and link the stack."))
	return err
}

func (o submitOptions) applyPlan(ctx context.Context, cmd *cobra.Command, service submit.Service, preview submit.Plan, spec submit.Spec, p Presentation, template string) error {
	validated, err := service.Revalidate(ctx, o.selection.Selection(), o.remote, preview)
	if err != nil {
		return writeNotApplied(cmd.OutOrStdout(), p, err)
	}
	if len(validated.Issues) != 0 {
		err := fmt.Errorf("submit preview has blocked existing pull requests; repair the marked branches and rerun")
		return writeNotApplied(cmd.OutOrStdout(), p, err)
	}
	if _, err := fmt.Fprintln(cmd.OutOrStdout(), p.accent("Ready to apply")); err != nil {
		return err
	}
	if err := writeSubmitPreview(cmd.OutOrStdout(), validated, p, template); err != nil {
		return err
	}
	if err := flushOutput(cmd.OutOrStdout()); err != nil {
		return err
	}
	// The mutation phase gets a fresh budget: it pushes, then creates one pull
	// request per missing branch, and expiring partway through would leave the
	// partial state the preview/revalidate sequence exists to avoid.
	mutateCtx, cancelMutation := o.budgets.mutation(o.root, len(validated.Snapshot.Branches))
	defer cancelMutation()
	if err := service.Apply(mutateCtx, validated, spec); err != nil {
		err = mutationTimeout(err, fmt.Sprintf("Re-running g2g submit --spec %s --apply is safe: it preserves existing pull requests and creates only the missing ones.", o.specPath))
		return fmt.Errorf("submission spec retained at %s: %w", o.specPath, writeNotApplied(cmd.OutOrStdout(), p, err))
	}
	if o.edit && !o.keepSpec {
		_ = os.RemoveAll(filepath.Dir(o.specPath))
	}
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), p.notice("Applied — stack published and missing pull requests created"))
	_, err = fmt.Fprintln(cmd.OutOrStdout(), p.subdued("Changes were made."))
	return err
}

func resolveDraft(cmd *cobra.Command, specDraft, draft, ready bool) bool {
	if ready {
		return false
	}
	if cmd.Flags().Changed("draft") {
		return draft
	}
	return specDraft
}

func editSpec(ctx context.Context, path string) error {
	editor := strings.TrimSpace(os.Getenv("EDITOR"))
	if editor == "" {
		return fmt.Errorf("EDITOR is not set; use --write-spec <private-temp-dir>, edit submission.json, then pass --spec")
	}
	parts := strings.Fields(editor)
	if len(parts) == 0 {
		return fmt.Errorf("EDITOR is empty; use --write-spec <private-temp-dir> instead")
	}
	command := exec.CommandContext(ctx, parts[0], append(parts[1:], path)...)
	command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
	return command.Run()
}

func writeSubmitPreview(w io.Writer, plan submit.Plan, p Presentation, template string) error {
	if _, err := fmt.Fprintf(w, "%s: %s\n\n", p.accent("Target"), p.branch(plan.Snapshot.Target)); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "  %s\n", p.trunk(plan.Snapshot.Base+" (trunk)")); err != nil {
		return err
	}
	for i, branch := range plan.Snapshot.Branches {
		marker := p.subdued("[create draft]")
		if reason, blocked := plan.Issues[branch]; blocked {
			marker = p.problem("[blocked: " + reason + "]")
		} else if number := existingNumber(plan, branch); number != 0 {
			marker = p.notice("[existing #" + fmt.Sprint(number) + "]")
		} else if previous, replaced := plan.Superseded[branch]; replaced {
			marker = p.subdued(fmt.Sprintf("[create draft · #%d %s]", previous.Number, strings.ToLower(previous.State)))
		}
		if _, err := fmt.Fprintf(w, "%s└─ %s %s\n", strings.Repeat("  ", i+1), p.branch(branch), marker); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	if template != "" {
		if _, err := fmt.Fprintln(w, p.subdued("PR template: "+template)); err != nil {
			return err
		}
	}
	message := "Missing PRs will be created as drafts; existing PRs are preserved."
	if len(plan.Issues) != 0 {
		message = "Apply blocked: repair the marked existing pull requests first."
	}
	_, err := fmt.Fprintln(w, p.subdued(message))
	return err
}

func existingNumber(plan submit.Plan, branch string) int {
	for _, pr := range plan.Existing {
		if pr.Head == branch && pr.State == "OPEN" {
			return pr.Number
		}
	}
	return 0
}

func actionableSpecError(err error, path string) error {
	return fmt.Errorf("%w\n\nNext steps:\n  1. Repair %s.\n  2. Validate: g2g submit --spec %s\n  3. Apply: g2g submit --spec %s --apply", err, path, path, path)
}
