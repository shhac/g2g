// Package cli defines the gt2gh command-line interface.
package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	localgit "github.com/shhac/gt2gh/internal/git"
	"github.com/shhac/gt2gh/internal/githubstack"
	"github.com/shhac/gt2gh/internal/graphite"
	"github.com/shhac/gt2gh/internal/link"
	"github.com/shhac/gt2gh/internal/subprocess"
	syncer "github.com/shhac/gt2gh/internal/sync"
)

const (
	linkTimeout       = 20 * time.Second
	completionTimeout = 3 * time.Second
)

// New creates the canonical gt2gh root command. version is injected by main at
// build time.
func New(version string, stdout, stderr io.Writer) *cobra.Command {
	return NewNamed(version, "gt2gh", stdout, stderr)
}

// NewNamed creates the root command for the executable name used to invoke it.
// This keeps generated shell completions correct for a package-manager alias.
func NewNamed(version, commandName string, stdout, stderr io.Writer) *cobra.Command {
	runner := subprocess.ExecRunner{}
	linkService := link.Service{
		Git:      localgit.Client{Runner: runner},
		Graphite: graphite.Client{Runner: runner},
		GitHub:   githubstack.Client{Runner: runner},
	}
	syncService := syncer.Service{Discoverer: linkService, Git: linkService.Git, GitHub: linkService.GitHub}
	return newWithServices(version, commandName, stdout, stderr, linkService, syncService)
}

// NewWithService creates the root command with injectable link dependencies.
// It keeps unit tests offline while New wires the production subprocesses.
func NewWithService(version string, stdout, stderr io.Writer, service link.Service) *cobra.Command {
	return NewWithServices(version, stdout, stderr, service, syncer.Service{Discoverer: service, Git: service.Git, GitHub: service.GitHub})
}

// NewWithServices creates the root command with injectable link and sync
// dependencies. It keeps unit tests offline while New wires subprocesses.
func NewWithServices(version string, stdout, stderr io.Writer, service link.Service, syncService syncer.Service) *cobra.Command {
	return newWithServices(version, "gt2gh", stdout, stderr, service, syncService)
}

func newWithServices(version, commandName string, stdout, stderr io.Writer, service link.Service, syncService syncer.Service) *cobra.Command {
	if commandName == "" {
		commandName = "gt2gh"
	}
	root := &cobra.Command{
		Use:               commandName,
		Short:             "Bridge Graphite-managed stacks to GitHub native stacks",
		SilenceErrors:     true,
		SilenceUsage:      true,
		Args:              cobra.NoArgs,
		RunE:              func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
		Version:           version,
		DisableAutoGenTag: true,
	}
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.AddCommand(newLink(service))
	root.AddCommand(newSync(syncService, service))
	root.AddCommand(newCompletion(root))
	return root
}

func newLink(service link.Service) *cobra.Command {
	var branch string
	var trunk string
	var apply bool
	cmd := &cobra.Command{
		Use:   "link",
		Short: "Link a linear Graphite stack to GitHub (preview by default)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), linkTimeout)
			defer cancel()
			plan, err := service.PlanWithTrunk(ctx, branch, trunk)
			if err != nil {
				return err
			}
			writePreview(cmd.OutOrStdout(), plan)
			if !apply {
				fmt.Fprintln(cmd.OutOrStdout(), "Preview only: rerun with --apply to invoke gh stack link.")
				return nil
			}
			if _, err := service.ApplyWithTrunk(ctx, branch, trunk, plan); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Applied: gh stack link completed after revalidation.")
			return nil
		},
	}
	cmd.Flags().StringVar(&branch, "branch", "", "Graphite-tracked local branch to link (defaults to current branch)")
	cmd.Flags().StringVar(&trunk, "trunk", "", "Graphite-declared trunk to use as the link base")
	cmd.Flags().BoolVar(&apply, "apply", false, "invoke gh stack link after revalidation")
	_ = cmd.RegisterFlagCompletionFunc("branch", func(command *cobra.Command, _ []string, prefix string) ([]string, cobra.ShellCompDirective) {
		ctx, cancel := context.WithTimeout(context.Background(), completionTimeout)
		defer cancel()
		branches, err := service.BranchCompletions(ctx, prefix)
		if err != nil {
			return nil, cobra.ShellCompDirectiveError | cobra.ShellCompDirectiveNoFileComp
		}
		return branches, cobra.ShellCompDirectiveNoFileComp
	})
	_ = cmd.RegisterFlagCompletionFunc("trunk", func(command *cobra.Command, _ []string, prefix string) ([]string, cobra.ShellCompDirective) {
		ctx, cancel := context.WithTimeout(context.Background(), completionTimeout)
		defer cancel()
		branches, err := service.TrunkCompletions(ctx, prefix)
		if err != nil {
			return nil, cobra.ShellCompDirectiveError | cobra.ShellCompDirectiveNoFileComp
		}
		return branches, cobra.ShellCompDirectiveNoFileComp
	})
	return cmd
}

func writePreview(writer io.Writer, plan link.Plan) {
	fmt.Fprintf(writer, "Resolved target (%s): %s\n", plan.TargetSource, plan.Target)
	fmt.Fprintf(writer, "Resolved Graphite trunk (%s): %s\n", plan.BaseSource, plan.Base)
	fmt.Fprintf(writer, "Graphite path (bottom to top): %s\n", strings.Join(plan.Branches, " -> "))
	fmt.Fprintf(writer, "Proposed command: gh stack link --base %s %s\n", plan.Base, strings.Join(plan.Branches, " "))
}

func newCompletion(root *cobra.Command) *cobra.Command {
	cmd := &cobra.Command{Use: "completion [bash|zsh|fish]", Short: "Generate shell completion scripts", Args: cobra.ExactArgs(1)}
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		switch args[0] {
		case "bash":
			return root.GenBashCompletion(cmd.OutOrStdout())
		case "zsh":
			return root.GenZshCompletion(cmd.OutOrStdout())
		case "fish":
			return root.GenFishCompletion(cmd.OutOrStdout(), true)
		default:
			return fmt.Errorf("unsupported shell %q (want bash, zsh, or fish)", args[0])
		}
	}
	return cmd
}

// Execute runs the root command with the process streams and executable name.
func Execute(version, commandName string) {
	root := NewNamed(version, commandName, os.Stdout, os.Stderr)
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}
}
