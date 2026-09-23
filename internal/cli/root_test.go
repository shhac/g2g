package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/shhac/g2g/internal/diagnostic"
)

func TestBareCommandShowsHelp(t *testing.T) {
	output, err := execute(t)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, command := range []string{"status", "adopt", "track", "untrack", "create", "restack", "pull", "push", "submit", "land", "github", "graphite"} {
		if !strings.Contains(output, "  "+command) {
			t.Errorf("help does not list %q:\n%s", command, output)
		}
	}
}

func TestVersion(t *testing.T) {
	output, err := execute(t, "--version")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if output != "g2g version v0.1.0\n" {
		t.Errorf("version = %q", output)
	}
}

func TestCompletionScripts(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		t.Run(shell, func(t *testing.T) {
			output, err := execute(t, "completion", shell)
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if !strings.Contains(output, "g2g") {
				t.Errorf("completion script does not name command")
			}
		})
	}
}

func TestNamedExecutableGeneratesMatchingZshCompletion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	command := NewNamed("v0.2.1", "g2g", &stdout, &stderr)
	command.SetArgs([]string{"completion", "zsh"})
	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(stdout.String(), "#compdef g2g") {
		t.Errorf("zsh completion = %q", stdout.String())
	}
}

func TestCompletionRejectsUnknownShell(t *testing.T) {
	if _, err := execute(t, "completion", "powershell"); err == nil {
		t.Fatal("Execute() error = nil, want error")
	}
}

func TestCommandContextWritesCompatibilityWarningsToStderrWithoutDebug(t *testing.T) {
	var stdout, stderr bytes.Buffer
	command := New("v", &stdout, &stderr)
	command.SetContext(context.Background())
	diagnostic.Warn(commandContext(command.Context(), command, "preview", "", ""), "synthetic", "synthetic compatibility warning")
	if got, want := stderr.String(), "warning: synthetic compatibility warning\n"; got != want {
		t.Errorf("stderr = %q, want %q", got, want)
	}
}

// helpMatch is the string the release workflow tells Homebrew to assert
// against the installed binary's help. It lives in .github/workflows/release.yml
// as a durable distribution knob, so changing a command description without it
// publishes a formula whose brew test fails — which is invisible here unless
// something checks.
const helpMatch = "Manage stacked branches"

func TestHelpContainsWhatTheFormulaAsserts(t *testing.T) {
	output, err := execute(t)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(output, helpMatch) {
		t.Errorf("help does not contain %q, which the published formula asserts:\n%s", helpMatch, output)
	}

	workflow, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatalf("read release workflow: %v", err)
	}
	if !strings.Contains(string(workflow), `help_match: "`+helpMatch+`"`) {
		t.Errorf("release.yml no longer asks for %q; the constant above and the workflow have to agree", helpMatch)
	}
}

// The help is the first thing a newcomer reads, and thirteen verbs listed
// alphabetically say nothing about where to start. Every command belongs to a
// group, so a command added without one is visible rather than quietly landing
// in "Additional Commands".
func TestEveryCommandIsGroupedExceptTheBuiltIns(t *testing.T) {
	var stdout, stderr bytes.Buffer
	root := New("v0.0.0-test", &stdout, &stderr)

	// A namespace's own commands are listed under it, not under a group.
	ungrouped := make([]string, 0)
	for _, command := range root.Commands() {
		switch command.Name() {
		case "help", "completion":
			continue
		}
		if command.GroupID == "" {
			ungrouped = append(ungrouped, command.Name())
		}
	}
	if len(ungrouped) != 0 {
		t.Errorf("commands with no help group: %v", ungrouped)
	}
}

// A mutating command previews by default and says so; a read-only one says
// that instead. Getting this wrong is how a reader learns to distrust the
// labels entirely.
func TestCommandsSayWhetherTheyPreviewOrRead(t *testing.T) {
	var stdout, stderr bytes.Buffer
	root := New("v0.0.0-test", &stdout, &stderr)

	for _, command := range everyCommand(root) {
		switch command.Name() {
		case "help", "completion":
			continue
		}
		// A namespace does nothing but hold the commands that are checked here.
		if command.HasSubCommands() {
			continue
		}
		mutates := command.Flags().Lookup("apply") != nil
		labelled := strings.Contains(command.Short, "(preview by default)")
		readOnly := strings.Contains(command.Short, "(read-only")
		// The navigation commands are the one deliberate exception to preview
		// first: they move the checkout and nothing else. They must say so, and
		// they must offer the dry run a preview would otherwise have been.
		moves := strings.Contains(command.Short, "(moves the checkout)")
		if mutates && !labelled {
			t.Errorf("%s takes --apply but does not say it previews by default: %q", command.Name(), command.Short)
		}
		if moves && command.Flags().Lookup("dry-run") == nil {
			t.Errorf("%s moves the checkout without --apply but offers no --dry-run", command.Name())
		}
		if !mutates && !readOnly && !moves {
			t.Errorf("%s takes no --apply but does not say it is read-only: %q", command.Name(), command.Short)
		}
	}
}

// A command must not be registered by a rule different from the one its
// service enforces. The two were hand-written conjunctions in separate files
// and three had already drifted: sync's gate asked for the graph store while
// its guard asked for the restacker, so a build with one and not the other
// either advertised a command that refuses on use or hid one that works.
func TestRegistrationAgreesWithWhatEachServiceRequires(t *testing.T) {
	var stdout, stderr bytes.Buffer
	// Every service left zero: nothing but the always-available commands.
	bare := NewWithOptions(Options{
		Version: "v0.0.0-test", CommandName: "g2g", Stdout: &stdout, Stderr: &stderr,
	})
	for _, command := range everyCommand(bare) {
		switch command.Name() {
		case "help", "completion":
			continue
		}
		t.Errorf("%s was registered on a build with no services configured", command.CommandPath())
	}
}

// everyCommand is every command below root, namespaces and what they hold.
func everyCommand(root *cobra.Command) []*cobra.Command {
	var all []*cobra.Command
	for _, command := range root.Commands() {
		all = append(all, command)
		all = append(all, everyCommand(command)...)
	}
	return all
}
