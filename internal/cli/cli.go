// Package cli defines the g2g command-line interface.
package cli

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/shhac/g2g/internal/align"
	"github.com/shhac/g2g/internal/diagnostic"
	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/graphite"
	"github.com/shhac/g2g/internal/link"
	"github.com/shhac/g2g/internal/prune"
	"github.com/shhac/g2g/internal/push"
	"github.com/shhac/g2g/internal/restack"
	"github.com/shhac/g2g/internal/retarget"
	"github.com/shhac/g2g/internal/stack"
	"github.com/shhac/g2g/internal/submit"
	"github.com/shhac/g2g/internal/subprocess"
	syncer "github.com/shhac/g2g/internal/sync"
)

// Command groups order the help by the job a reader is trying to do. Thirteen
// verbs listed alphabetically say nothing about where to start; three headings
// say most of it.
const (
	groupStructure = "structure"
	groupPublish   = "publish"
	groupMaintain  = "maintain"
)

// Options are the dependencies a root command is built from.
//
// A zero service means its command is not registered. That replaces four
// overlapping constructors that threaded ten positional parameters — four of
// them same-shaped service structs — through each other purely to supply
// defaults, where a transposed argument would still have compiled.
type Options struct {
	Version     string
	CommandName string
	Stdout      io.Writer
	Stderr      io.Writer

	Link   link.Service
	Push   push.Service
	Submit submit.Service
	// Graph owns the branch forest g2g keeps itself. It needs neither
	// Graphite nor GitHub, which is the whole point of it.
	Graph graph.Service
	// Restack rewrites branch contents to match that structure. It is the only
	// service allowed to change history.
	Restack restack.Service
	// Sync brings a stack up to date with its remote by composing the others.
	Sync syncer.Service
	// Prune forgets branches whose work has landed. It edits the graph and
	// deletes nothing, which is why it is not the tail of sync.
	Prune prune.Service
	// Retarget reconciles GitHub's pull request bases with the resolved stack.
	// It is the only command that changes what a merge will do.
	Retarget retarget.Service
	// Align keeps the g2g graph and Graphite's in step. It is the only
	// service that writes Graphite.
	Align align.Service

	// Completions supplies branch and trunk candidates for shell completion.
	Completions stack.Completions

	// Unstacker performs unlink's mutation. When nil it is taken from Link's
	// GitHub client if that client provides it.
	Unstacker Unstacker
	// Presentation overrides what Stdout would otherwise imply.
	Presentation *Presentation
}

// New creates the canonical g2g root command. version is injected by main at
// build time.
func New(version string, stdout, stderr io.Writer) *cobra.Command {
	return NewNamed(version, "g2g", stdout, stderr)
}

// NewNamed creates the root command for the executable name used to invoke it.
// This keeps generated shell completions correct for a package-manager alias.
func NewNamed(version, commandName string, stdout, stderr io.Writer) *cobra.Command {
	runner := subprocess.ObservingRunner{Runner: subprocess.ExecRunner{}}
	githubClient := githubstack.Client{Runner: runner}
	gitClient := localgit.Client{Runner: runner}
	graphiteClient := graphite.Client{Runner: runner}
	graphService := graph.Service{Git: gitClient, Store: graph.FileStore{Git: gitClient}, Refs: gitClient}
	// Precedence is declared here and nowhere else. Adopting a branch into
	// g2g's own store is the user saying they want g2g to own it, so that
	// is asked first; Graphite answers for everything it still tracks.
	restackService := restack.Service{Git: gitClient, Graph: graphService, Journal: restack.FileJournal{Git: gitClient}}
	graphiteConfigured := func(ctx context.Context) (bool, error) { return graphite.Configured(ctx, gitClient) }
	selector := stack.Resolver{
		Git: gitClient,
		Selectors: []stack.Selector{
			graph.Selector{Service: graphService},
			stack.GraphiteSelector{Git: gitClient, Graphite: graphiteClient, Configured: graphiteConfigured},
		},
	}
	// Completion draws on the same sources, in the same order, so a flag never
	// offers a branch the command would refuse — and never reaches a source the
	// command would not have reached either.
	completions := stack.Completions{
		Git: gitClient,
		Sources: []stack.Candidates{
			graph.StoreCandidates{Service: graphService},
			stack.GraphiteCandidates{Graphite: graphiteClient, Configured: graphiteConfigured},
		},
	}
	return NewWithOptions(Options{
		Version:     version,
		CommandName: commandName,
		Stdout:      stdout,
		Stderr:      stderr,
		Link:        link.Service{Git: gitClient, Selector: selector, GitHub: githubClient},
		Push:        push.Service{Git: gitClient, Selector: selector},
		Submit:      submit.Service{Git: gitClient, Selector: selector, GitHub: githubClient},
		Completions: completions,
		Graph:       graphService,
		Restack:     restackService,
		Sync:        syncer.Service{Git: gitClient, Graph: graphService, Restack: restackService},
		Prune:       prune.Service{Git: gitClient, Graph: graphService},
		Align:       align.Service{Git: gitClient, Store: graphService.Store, Refs: gitClient, Graphite: graphiteClient, Configured: graphiteConfigured},
		Retarget:    retarget.Service{Git: gitClient, Selector: selector, GitHub: githubClient},
		Unstacker:   githubClient,
	})
}

// NewWithOptions builds the root command from an explicit set of dependencies.
func NewWithOptions(options Options) *cobra.Command {
	if options.CommandName == "" {
		options.CommandName = "g2g"
	}
	if options.Unstacker == nil {
		if configured, ok := options.Link.GitHub.(Unstacker); ok {
			options.Unstacker = configured
		}
	}
	guard := restackGuard(options.Restack)
	presentation := detectPresentation(options.Stdout)
	if options.Presentation != nil {
		presentation = *options.Presentation
	}

	root := &cobra.Command{
		Use:   options.CommandName,
		Short: "Manage stacked branches and project them onto GitHub",
		Long: "Manage stacked branches and project them onto GitHub.\n\n" +
			"Structure is recorded locally and needs no Graphite. Start with `" + options.CommandName +
			" track --stack`, which records the stack you are on in one step, then `" + options.CommandName +
			" graph` to see it.",
		SilenceErrors:     true,
		SilenceUsage:      true,
		Args:              cobra.NoArgs,
		RunE:              func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
		Version:           options.Version,
		DisableAutoGenTag: true,
	}
	root.SetOut(options.Stdout)
	root.SetErr(options.Stderr)
	root.PersistentFlags().Bool("debug", false, "write safe diagnostic events to stderr")
	root.PersistentFlags().Duration("timeout", 0, "maximum duration for each phase, discovery and mutation separately (default 45s discovery, 60s plus 30s per branch for mutation)")
	root.PersistentFlags().Bool("json", false, "emit one JSON document instead of the human-readable preview")
	root.PersistentFlags().Bool("porcelain", false, "emit stable tab-separated records instead of the human-readable preview")
	root.MarkFlagsMutuallyExclusive("json", "porcelain")
	// Links are detected, so the flag only ever turns them off. Terminals that
	// cannot draw one already render the text unchanged; this is for the case
	// where a person would rather have plain output than a correct guess.
	root.PersistentFlags().Bool("no-links", false, "do not attach hyperlinks to pull request numbers")

	// Completion candidates come from the structure sources themselves, so no
	// command has to depend on another to complete a flag.
	root.AddGroup(
		&cobra.Group{ID: groupStructure, Title: "Recording structure:"},
		&cobra.Group{ID: groupPublish, Title: "Publishing to GitHub:"},
		&cobra.Group{ID: groupMaintain, Title: "Keeping it true:"},
	)
	completions := options.Completions
	root.AddCommand(newLink(options.Link, completions, guard, presentation))
	root.AddCommand(newStatus(options.Link, completions, presentation))
	root.AddCommand(newUnlink(options.Link, options.Unstacker, completions, guard, presentation))
	if options.Push.Git != nil && options.Push.Selector != nil {
		root.AddCommand(newPush(options.Push, completions, guard, presentation))
	}
	if options.Submit.Git != nil && options.Submit.Selector != nil && options.Submit.GitHub != nil {
		root.AddCommand(newSubmit(options.Submit, completions, guard, presentation))
	}
	if options.Graph.Git != nil && options.Graph.Store != nil {
		root.AddCommand(newGraph(options.Graph, presentation))
		root.AddCommand(newTrack(options.Graph, guard, presentation))
		root.AddCommand(newUntrack(options.Graph, guard, presentation))
	}
	if options.Restack.Git != nil && options.Restack.Journal != nil {
		root.AddCommand(newRestack(options.Restack, presentation))
	}
	if options.Sync.Git != nil && options.Sync.Graph.Store != nil {
		root.AddCommand(newSync(options.Sync, guard, presentation))
		root.AddCommand(newPrune(options.Prune, guard, presentation))
	}
	if options.Retarget.Git != nil && options.Retarget.Selector != nil && options.Retarget.GitHub != nil {
		root.AddCommand(newRetarget(options.Retarget, completions, guard, presentation))
	}
	if options.Align.Store != nil && options.Align.Git != nil && options.Align.Graphite != nil {
		root.AddCommand(newMirror(options.Align, guard, presentation))
		root.AddCommand(newImport(options.Align, guard, presentation))
	}
	root.AddCommand(newCompletion(root))
	return root
}

func commandContext(ctx context.Context, cmd *cobra.Command, operation, mode, branch, trunk string) context.Context {
	ctx = diagnostic.WithWarningWriter(ctx, cmd.ErrOrStderr())
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
		writeError(os.Stderr, err)
		os.Exit(2)
	}
}
