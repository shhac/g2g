package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestRunHelp(t *testing.T) {
	var output bytes.Buffer

	if err := Run([]string{"--help"}, &output, "test"); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if got := output.String(); !strings.Contains(got, "gt2gh link") {
		t.Errorf("help = %q, want it to document the link command", got)
	}
}

func TestRunVersion(t *testing.T) {
	var output bytes.Buffer

	if err := Run([]string{"--version"}, &output, "v0.1.0"); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if got, want := output.String(), "v0.1.0\n"; got != want {
		t.Errorf("version = %q, want %q", got, want)
	}
}

func TestRunLinkIsSafePlaceholder(t *testing.T) {
	var output bytes.Buffer

	if err := Run([]string{"link"}, &output, "test"); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if got := output.String(); !strings.Contains(got, "no commands were run") {
		t.Errorf("link output = %q, want explicit no-op message", got)
	}
}

func TestRunWithoutArgumentsShowsHelp(t *testing.T) {
	var output bytes.Buffer

	if err := Run(nil, &output, "test"); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if got := output.String(); !strings.Contains(got, "Usage:") {
		t.Errorf("bare command output = %q, want usage", got)
	}
}

func TestRunRejectsUnknownCommands(t *testing.T) {
	var output bytes.Buffer

	err := Run([]string{"unknown"}, &output, "test")
	if !errors.Is(err, errUsage) {
		t.Errorf("Run() error = %v, want usage error", err)
	}
}
