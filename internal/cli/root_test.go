package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/shhac/gt2gh/internal/diagnostic"
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
	if output != "gt2gh version v0.1.0\n" {
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
			if !strings.Contains(output, "gt2gh") {
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
