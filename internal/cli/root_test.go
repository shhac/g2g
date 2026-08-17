package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/diagnostic"
)

func TestBareCommandShowsHelp(t *testing.T) {
	output, err := execute(t)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, command := range []string{"link", "unlink", "status", "push", "submit", "graph", "track", "untrack", "restack"} {
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
	diagnostic.Warn(commandContext(command.Context(), command, "link", "preview", "", ""), "synthetic", "synthetic compatibility warning")
	if got, want := stderr.String(), "warning: synthetic compatibility warning\n"; got != want {
		t.Errorf("stderr = %q, want %q", got, want)
	}
}

// helpMatch is the string the release workflow tells Homebrew to assert
// against the installed binary's help. It lives in .github/workflows/release.yml
// as a durable distribution knob, so changing a command description without it
// publishes a formula whose brew test fails — which is invisible here unless
// something checks.
const helpMatch = "Link a stack to GitHub"

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

	for _, command := range root.Commands() {
		switch command.Name() {
		case "help", "completion":
			continue
		}
		mutates := command.Flags().Lookup("apply") != nil
		labelled := strings.Contains(command.Short, "(preview by default)")
		readOnly := strings.Contains(command.Short, "(read-only)")
		if mutates && !labelled {
			t.Errorf("%s takes --apply but does not say it previews by default: %q", command.Name(), command.Short)
		}
		if !mutates && !readOnly {
			t.Errorf("%s takes no --apply but does not say it is read-only: %q", command.Name(), command.Short)
		}
	}
}
