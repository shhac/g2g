package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/shhac/g2g/internal/submit"
)

// The submission spec is a document the user edits by hand, so its lifecycle —
// writing a draft, opening an editor, deciding draft status, and reporting a
// broken one with repair steps — is kept apart from the command wiring and the
// rendering it sits between.

func (o *submitOptions) writeDraft(cmd *cobra.Command, plan submit.Plan, body string) error {
	path, err := submit.Write(o.writeSpec, submit.NewSpec(plan.Snapshot.Branches, body))
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Wrote draft submission spec: %s\n", path)
	_, err = fmt.Fprintln(cmd.OutOrStdout(), "Next: add a title for every PR, then run g2g submit --spec "+path+" to validate it.")
	return err
}

// editedSpec writes one temporary submission document and opens it, returning
// where it lives. It is retained on every failure, because the titles in it are
// the user's work.
func (o submitOptions) editedSpec(ctx context.Context, cmd *cobra.Command, plan submit.Plan, body string) (string, error) {
	dir, err := os.MkdirTemp("", "g2g-submit-")
	if err != nil {
		return "", err
	}
	path, err := submit.Write(dir, submit.NewSpec(plan.Snapshot.Branches, body))
	if err != nil {
		return "", err
	}
	if err := editSpec(ctx, path); err != nil {
		return "", fmt.Errorf("submission spec retained at %s: %w", path, err)
	}
	if !o.apply {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Edited submission spec: "+path)
	}
	return path, nil
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

func actionableSpecError(err error, path string) error {
	return fmt.Errorf("%w\n\nNext steps:\n  1. Repair %s.\n  2. Validate: g2g submit --spec %s\n  3. Apply: g2g submit --spec %s --apply", err, path, path, path)
}
