package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/shhac/gt2gh/internal/link"
	"github.com/shhac/gt2gh/internal/submit"
)

func newSubmit(service submit.Service, linkService link.Service, presentation Presentation) *cobra.Command {
	var branch, trunk, remote, specPath, writeSpec, template string
	var noStack, apply, draft, ready, noTemplate, edit, keepSpec bool
	cmd := &cobra.Command{Use: "submit", Short: "Publish a Graphite stack and create missing draft PRs (preview by default)", Args: cobra.NoArgs}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		ctx, cancel := context.WithTimeout(cmd.Context(), linkTimeout)
		defer cancel()
		mode := "preview"
		if apply {
			mode = "apply"
		}
		ctx = commandContext(cmd, "submit", mode, branch, trunk)
		selection := link.Selection{Branch: branch, Trunk: trunk, NoStack: noStack}
		plan, err := service.Plan(ctx, selection, remote)
		if err != nil {
			return err
		}
		chosenTemplate, templateName, err := resolveTemplate(template, noTemplate)
		if err != nil {
			return err
		}
		if writeSpec != "" {
			path, err := submit.Write(writeSpec, submit.NewSpec(plan.Snapshot.Branches, chosenTemplate))
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Wrote draft submission spec: %s\n", path)
			fmt.Fprintln(cmd.OutOrStdout(), "Next: add a title for every PR, then run g2g submit --spec "+path+" to validate it.")
			return nil
		}
		if edit {
			if specPath != "" {
				return fmt.Errorf("--edit and --spec cannot be used together; use one editable submission document")
			}
			dir, err := os.MkdirTemp("", "g2g-submit-")
			if err != nil {
				return err
			}
			specPath, err = submit.Write(dir, submit.NewSpec(plan.Snapshot.Branches, chosenTemplate))
			if err != nil {
				return err
			}
			if err := editSpec(ctx, specPath); err != nil {
				return fmt.Errorf("submission spec retained at %s: %w", specPath, err)
			}
			if !apply {
				fmt.Fprintln(cmd.OutOrStdout(), "Edited submission spec: "+specPath)
			}
		}
		if specPath == "" {
			if err := writeSubmitPreview(cmd.OutOrStdout(), plan, presentation, templateName); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), presentation.notice("No changes were made. Create a spec with: g2g submit --write-spec <private-temp-dir>"))
			return nil
		}
		spec, err := submit.Read(specPath, plan.Snapshot.Branches)
		if err != nil {
			return actionableSpecError(err, specPath)
		}
		spec.Draft = resolveDraft(cmd, spec.Draft, draft, ready)
		if !apply {
			if err := writeSubmitPreview(cmd.OutOrStdout(), plan, presentation, templateName); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), presentation.notice("No changes were made. Re-run with --apply to atomically push, create missing PRs, and link the stack."))
			return nil
		}
		validated, err := service.Revalidate(ctx, selection, remote, plan)
		if err != nil {
			writeNotApplied(cmd.OutOrStdout(), presentation, err)
			return err
		}
		if len(validated.Issues) != 0 {
			err := fmt.Errorf("submit preview has blocked existing pull requests; repair the marked branches and rerun")
			writeNotApplied(cmd.OutOrStdout(), presentation, err)
			return err
		}
		if _, err := fmt.Fprintln(cmd.OutOrStdout(), presentation.accent("Ready to apply")); err != nil {
			return err
		}
		if err := writeSubmitPreview(cmd.OutOrStdout(), validated, presentation, templateName); err != nil {
			return err
		}
		if err := flushOutput(cmd.OutOrStdout()); err != nil {
			return err
		}
		if err := service.Apply(ctx, validated, spec); err != nil {
			writeNotApplied(cmd.OutOrStdout(), presentation, err)
			return fmt.Errorf("submission spec retained at %s: %w", specPath, err)
		}
		if edit && !keepSpec {
			_ = os.RemoveAll(filepath.Dir(specPath))
		}
		fmt.Fprintln(cmd.OutOrStdout(), presentation.notice("Applied — stack published and missing pull requests created"))
		fmt.Fprintln(cmd.OutOrStdout(), presentation.subdued("Changes were made."))
		return nil
	}
	cmd.Flags().StringVar(&branch, "branch", "", "Graphite-tracked local branch to submit (defaults to current branch)")
	cmd.Flags().StringVar(&trunk, "trunk", "", "Graphite-declared trunk to use as the submit base")
	cmd.Flags().StringVar(&remote, "remote", "origin", "Git remote to push to")
	cmd.Flags().BoolVar(&noStack, "no-stack", false, "stop at the selected branch instead of resolving the full linear stack")
	cmd.Flags().StringVar(&specPath, "spec", "", "submission JSON spec to validate or apply")
	cmd.Flags().StringVar(&writeSpec, "write-spec", "", "write a draft spec in a private temporary directory, without applying")
	cmd.Flags().BoolVar(&edit, "edit", false, "create and edit one temporary submission spec document")
	cmd.Flags().BoolVar(&keepSpec, "keep-spec", false, "keep the temporary --edit spec after a successful apply")
	cmd.Flags().StringVar(&template, "template", "", "repository pull request template name to prefill generated specs")
	cmd.Flags().BoolVar(&noTemplate, "no-template", false, "do not prefill bodies from a repository template")
	cmd.Flags().BoolVar(&draft, "draft", true, "create missing pull requests as drafts")
	cmd.Flags().BoolVar(&ready, "ready", false, "create missing pull requests ready for review")
	cmd.MarkFlagsMutuallyExclusive("draft", "ready")
	cmd.MarkFlagsMutuallyExclusive("edit", "spec")
	cmd.MarkFlagsMutuallyExclusive("edit", "write-spec")
	cmd.Flags().BoolVar(&apply, "apply", false, "atomically push, create missing PRs, and link after revalidation")
	_ = cmd.RegisterFlagCompletionFunc("branch", completionCallback(linkService.BranchCompletions))
	_ = cmd.RegisterFlagCompletionFunc("trunk", completionCallback(func(ctx context.Context, prefix string) ([]string, error) {
		return linkService.TrunkCompletions(ctx, branch, prefix)
	}))
	return cmd
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

func resolveTemplate(requested string, disabled bool) (string, string, error) {
	if disabled {
		return "", "", nil
	}
	templates, err := findTemplates()
	if err != nil {
		return "", "", err
	}
	if requested != "" {
		content, ok := templates[requested]
		if !ok {
			names := templateNames(templates)
			return "", "", fmt.Errorf("pull request template %q was not found; available templates: %s; or use --no-template", requested, strings.Join(names, ", "))
		}
		return content, requested, nil
	}
	if len(templates) == 0 {
		return "", "", nil
	}
	if len(templates) == 1 {
		for name, content := range templates {
			return content, name, nil
		}
	}
	return "", "", fmt.Errorf("multiple pull request templates found (%s); rerun with --template <name> or --no-template", strings.Join(templateNames(templates), ", "))
}

func findTemplates() (map[string]string, error) {
	paths := []string{".github/PULL_REQUEST_TEMPLATE.md", ".github/pull_request_template.md", "PULL_REQUEST_TEMPLATE.md", "pull_request_template.md", "docs/PULL_REQUEST_TEMPLATE.md", "docs/pull_request_template.md"}
	found := map[string]string{}
	for _, path := range paths {
		if b, err := os.ReadFile(path); err == nil {
			found[path] = string(b)
		} else if !os.IsNotExist(err) {
			return nil, err
		}
	}
	for _, dir := range []string{".github/PULL_REQUEST_TEMPLATE", "PULL_REQUEST_TEMPLATE", "docs/PULL_REQUEST_TEMPLATE"} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".md") {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			b, err := os.ReadFile(path)
			if err != nil {
				return nil, err
			}
			found[entry.Name()] = string(b)
		}
	}
	return found, nil
}
func templateNames(templates map[string]string) []string {
	names := make([]string, 0, len(templates))
	for name := range templates {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
