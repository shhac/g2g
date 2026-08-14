// Package cli defines the gt2gh command-line interface.
package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/shhac/gt2gh/internal/diagnostic"
	localgit "github.com/shhac/gt2gh/internal/git"
	"github.com/shhac/gt2gh/internal/githubstack"
	"github.com/shhac/gt2gh/internal/graphite"
	"github.com/shhac/gt2gh/internal/link"
	"github.com/shhac/gt2gh/internal/push"
	"github.com/shhac/gt2gh/internal/submit"
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
	runner := subprocess.ObservingRunner{Runner: subprocess.ExecRunner{}}
	linkService := link.Service{
		Git:      localgit.Client{Runner: runner},
		Graphite: graphite.Client{Runner: runner},
		GitHub:   githubstack.Client{Runner: runner},
	}
	pushService := push.Service{Git: localgit.Client{Runner: runner}, Graphite: graphite.Client{Runner: runner}}
	syncService := syncer.Service{Discoverer: linkService, Git: linkService.Git, GitHub: linkService.GitHub}
	submitService := submit.Service{Git: localgit.Client{Runner: runner}, Graphite: graphite.Client{Runner: runner}, GitHub: githubstack.Client{Runner: runner}}
	return newWithServices(version, commandName, stdout, stderr, linkService, syncService, pushService, submitService)
}

// NewWithService creates the root command with injectable link dependencies.
// It keeps unit tests offline while New wires the production subprocesses.
func NewWithService(version string, stdout, stderr io.Writer, service link.Service) *cobra.Command {
	return NewWithServices(version, stdout, stderr, service, syncer.Service{Discoverer: service, Git: service.Git, GitHub: service.GitHub})
}

// NewWithServices creates the injectable link/sync command surface. It omits
// push because its mutable Git dependency is deliberately not part of this
// constructor's contract.
func NewWithServices(version string, stdout, stderr io.Writer, service link.Service, syncService syncer.Service) *cobra.Command {
	return newWithServices(version, "gt2gh", stdout, stderr, service, syncService, push.Service{}, submit.Service{})
}

func newWithServices(version, commandName string, stdout, stderr io.Writer, service link.Service, syncService syncer.Service, pushService push.Service, submitService submit.Service) *cobra.Command {
	return newWithSubmitPresentation(version, commandName, stdout, stderr, service, syncService, pushService, submitService, detectPresentation(stdout))
}
func newWithPresentation(version, commandName string, stdout, stderr io.Writer, service link.Service, syncService syncer.Service, pushService push.Service, presentation Presentation) *cobra.Command {
	return newWithSubmitPresentation(version, commandName, stdout, stderr, service, syncService, pushService, submit.Service{}, presentation)
}
func newWithSubmitPresentation(version, commandName string, stdout, stderr io.Writer, service link.Service, syncService syncer.Service, pushService push.Service, submitService submit.Service, presentation Presentation) *cobra.Command {
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
	root.PersistentFlags().Bool("debug", false, "write safe diagnostic events to stderr")
	root.AddCommand(newLink(service, presentation))
	root.AddCommand(newStatus(service, presentation))
	root.AddCommand(newUnlink(service, githubstack.Client{Runner: subprocess.ObservingRunner{Runner: subprocess.ExecRunner{}}}, presentation))
	root.AddCommand(newSync(syncService, service, presentation))
	if pushService.Git != nil && pushService.Graphite != nil {
		root.AddCommand(newPush(pushService, service, presentation))
	}
	if submitService.Git != nil && submitService.Graphite != nil && submitService.GitHub != nil {
		root.AddCommand(newSubmit(submitService, service, presentation))
	}
	root.AddCommand(newCompletion(root))
	return root
}

func commandContext(cmd *cobra.Command, operation, mode, branch, trunk string) context.Context {
	ctx := cmd.Context()
	debug, _ := cmd.Flags().GetBool("debug")
	if !debug {
		return ctx
	}
	ctx = diagnostic.WithSink(ctx, diagnostic.Writer{Out: cmd.ErrOrStderr()})
	targetSource := "current Git branch"
	if branch != "" {
		targetSource = "--branch"
	}
	fields := []diagnostic.Field{
		{Key: "operation", Value: operation},
		{Key: "mode", Value: mode},
		{Key: "target_source", Value: targetSource},
	}
	if trunk != "" {
		fields = append(fields, diagnostic.Field{Key: "trunk_override", Value: trunk})
	}
	diagnostic.Event(ctx, "operation.start", fields...)
	return ctx
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
