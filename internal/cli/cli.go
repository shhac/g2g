// Package cli defines the g2g command-line interface.
package cli

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/shhac/g2g/internal/align"
	"github.com/shhac/g2g/internal/comment"
	"github.com/shhac/g2g/internal/create"
	"github.com/shhac/g2g/internal/diagnostic"
	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/graphite"
	"github.com/shhac/g2g/internal/land"
	"github.com/shhac/g2g/internal/link"
	"github.com/shhac/g2g/internal/navigate"
	"github.com/shhac/g2g/internal/prune"
	"github.com/shhac/g2g/internal/push"
	"github.com/shhac/g2g/internal/reshape"
	"github.com/shhac/g2g/internal/restack"
	"github.com/shhac/g2g/internal/retarget"
	"github.com/shhac/g2g/internal/stack"
	"github.com/shhac/g2g/internal/submit"
	"github.com/shhac/g2g/internal/subprocess"
	syncer "github.com/shhac/g2g/internal/sync"
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
	// Land merges a stack down onto its trunk. It composes Push, Sync and
	// Prune rather than owning their rules, so it is registered only when all
	// three of its own seams are present.
	Land land.Service
	// Comment keeps a comment on each pull request listing its stack. It
	// writes conversation, never structure or code.
	Comment comment.Service
	// Align keeps the g2g graph and Graphite's in step. It is the only
	// service that writes Graphite.
	Align align.Service
	// Create starts a branch and records it in one step. It records through
	// Graph's own track plan rather than owning a second way to write an edge.
	Create create.Service
	// Reshape deletes, folds and renames branches in the g2g graph. It moves
	// and removes refs and never replays a commit, which stays restack's.
	Reshape reshape.Service
	// Navigate moves the checkout around a stack. It is the one service that
	// acts without --apply, because moving the checkout changes no ref, record
	// or remote.
	Navigate navigate.Service

	// Completions supplies branch and trunk candidates for shell completion.
	Completions stack.Completions
	// Published says how each branch stands against what the remote last held,
	// from local refs. It is optional: without it status draws no remote marks.
	Published Published

	// Unstacker performs unlink's mutation. When nil it is taken from Link's
	// GitHub client if that client provides it.
	Unstacker Unstacker
	// GraphiteConfigured reports whether this repository already uses Graphite.
	// It is one file check and never runs Graphite, which is what makes it safe
	// to consult from a command whose independence from Graphite is the point.
	GraphiteConfigured func(context.Context) (bool, error)
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
	graphService := graph.Service{Git: gitClient, Store: graph.FileStore{Git: gitClient}, Refs: gitClient, Trunks: gitClient}
	// Precedence is declared here and nowhere else. Adopting a branch into
	// g2g's own store is the user saying they want g2g to own it, so that
	// is asked first; Graphite answers for everything it still tracks.
	restackService := restack.Service{Git: gitClient, Graph: graphService, Journal: restack.FileJournal{Git: gitClient}}
	graphiteConfigured := func(ctx context.Context) (bool, error) { return graphite.Configured(ctx, gitClient) }
	selector := stack.Resolver{
		Git:    gitClient,
		Trunks: gitClient,
		Selectors: []stack.Selector{
			stack.G2GSelector{Service: graphService},
			stack.GraphiteSelector{Git: gitClient, Graphite: graphiteClient, Configured: graphiteConfigured},
		},
		// Reading pull request bases means invoking gh, and push must never do
		// that, so this source answers only when --from names it.
		OnRequest: []stack.Selector{
			stack.NewPullRequestSelector(gitClient, githubClient),
		},
	}
	// Completion draws on the same sources, in the same order, so a flag never
	// offers a branch the command would refuse — and never reaches a source the
	// command would not have reached either.
	completions := stack.Completions{
		Git: gitClient,
		Sources: []stack.Candidates{
			stack.G2GCandidates{Service: graphService},
			stack.GraphiteCandidates{Graphite: graphiteClient, Configured: graphiteConfigured},
		},
	}
	pushService := push.Service{Git: gitClient, Selector: selector}
	syncService := syncer.Service{Git: gitClient, Graph: graphService, Restack: restackService}
	pruneService := prune.Service{Git: gitClient, Graph: graphService}
	return NewWithOptions(Options{
		Version:     version,
		CommandName: commandName,
		Stdout:      stdout,
		Stderr:      stderr,
		Link:        link.Service{Git: gitClient, Selector: selector, GitHub: githubClient, Tips: gitClient},
		Push:        pushService,
		Submit:      submit.Service{Git: gitClient, Selector: selector, GitHub: githubClient, Pusher: &pushService},
		Completions: completions,
		Graph:       graphService,
		Restack:     restackService,
		Sync:        syncService,
		Prune:       pruneService,
		Align: align.Service{
			Git: gitClient, Store: graphService.Store, Refs: gitClient, Graphite: graphiteClient, Configured: graphiteConfigured,
			// Not NewPullRequestSelector: its memo would hand revalidation the
			// preview's reading of the pull requests, and re-reading them is
			// what revalidating an import from them means.
			PullRequests: stack.PullRequestSelector{Git: gitClient, GitHub: githubClient},
			Forks:        gitClient, Trunks: gitClient,
		},
		Retarget: retarget.Service{Git: gitClient, Selector: selector, GitHub: githubClient},
		Comment:  comment.Service{Selector: selector, GitHub: githubClient},
		Land: land.Service{
			Git: gitClient, Graph: graphService, Selector: selector, GitHub: githubClient,
			Pusher: &pushService, Syncer: &syncService, Pruner: &pruneService, Holds: restackService,
		},
		Published:          push.Known{Git: gitClient},
		Create:             create.Service{Git: gitClient, Graph: graphService},
		Reshape:            reshape.Service{Git: gitClient, Graph: graphService},
		Navigate:           navigate.Service{Selector: selector, Git: gitClient, Trunks: recordedChildren{service: graphService}},
		Unstacker:          githubClient,
		GraphiteConfigured: graphiteConfigured,
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
			" adopt`, which records the stack you are on in one step, then `" + options.CommandName +
			" status` to see it.",
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
	root.AddGroup(commandGroups()...)
	completions := options.Completions
	// A zero service means its command is not registered, so each is added
	// under its own condition — and a namespace only when something is in it.
	var github, graphite []*cobra.Command
	if options.Graph.Ready() {
		root.AddCommand(newGraph(options.Graph, options.Link.Selector, options.Published, presentation))
		root.AddCommand(newTrack(options.Graph, guard, options.GraphiteConfigured, presentation))
		root.AddCommand(newAdopt(options.Graph, guard, presentation))
		root.AddCommand(newUntrack(options.Graph, guard, presentation))
	}
	if options.Create.Ready() {
		root.AddCommand(newCreate(options.Create, options.Graph, guard, presentation))
	}
	if options.Reshape.Ready() {
		root.AddCommand(newReshape(options.Reshape, options.Graph, guard, presentation)...)
	}
	if options.Navigate.Ready() {
		root.AddCommand(newNavigations(options.Navigate, completions, guard, presentation)...)
	}
	if options.Restack.Ready() {
		root.AddCommand(newRestack(options.Restack, presentation))
	}
	if options.Sync.Ready() {
		root.AddCommand(newSync(options.Sync, options.Prune, guard, presentation))
	}
	if options.Prune.Ready() {
		root.AddCommand(newPrune(options.Prune, guard, presentation))
	}
	if options.Push.Ready() {
		root.AddCommand(newPush(options.Push, completions, guard, presentation))
	}
	if options.Submit.Ready() {
		root.AddCommand(newSubmit(options.Submit, options.Comment, completions, guard, presentation))
	}
	if options.Land.Ready() {
		root.AddCommand(newLand(options.Land, options.Comment, completions, guard, presentation))
	}
	if options.Link.Ready() {
		github = append(github,
			newStatus(options.Link, completions, presentation),
			newLink(options.Link, completions, guard, presentation),
			newUnlink(options.Link, options.Unstacker, completions, guard, presentation))
	}
	if options.Retarget.Ready() {
		github = append(github, newRetarget(options.Retarget, completions, guard, presentation))
	}
	if options.Comment.Ready() {
		github = append(github, newComment(options.Comment, completions, guard, presentation))
	}
	if options.Align.Ready() {
		github = append(github, newAdoptFrom(options.Align, align.FromGitHub, completions, guard, presentation))
		graphite = append(graphite,
			newAdoptFrom(options.Align, align.FromGraphite, completions, guard, presentation),
			newMirror(options.Align, guard, presentation))
	}
	if len(github) > 0 {
		root.AddCommand(namespace("github", "Work with the stack's pull requests on GitHub",
			"The stack as GitHub sees it: each branch's pull request, the base it targets, and the comment that "+
				"links the stack together. Everything here reads or writes GitHub through gh; the commands at the "+
				"top level read only git.", github...))
	}
	if len(graphite) > 0 {
		root.AddCommand(namespace("graphite", "Move a stack between Graphite and g2g",
			"For a repository that has used Graphite: adopt the stack Graphite declares into g2g's own record, "+
				"or mirror g2g's record back into Graphite. Neither contacts Graphite's service.", graphite...))
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
	// Hidden, not removed. This is run once by a shell rc or by the Homebrew
	// formula's generate_completions_from_executable, and never typed while
	// working on a stack, so it is noise in a help listing whose other entries
	// are all things a person runs. Hidden affects the listing alone: the
	// command still executes, which is what the formula depends on.
	cmd := &cobra.Command{
		Use:    "completion [bash|zsh|fish]",
		Short:  "Generate shell completion scripts",
		Args:   cobra.ExactArgs(1),
		Hidden: true,
	}
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
		// A command that stopped part-way has already reported it, in more
		// detail than a one-line error could, so all that is left is the
		// status.
		if wasStopped(err) {
			os.Exit(stoppedExitCode)
		}
		writeError(os.Stderr, err)
		os.Exit(2)
	}
}
