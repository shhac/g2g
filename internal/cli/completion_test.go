package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/shhac/gt2gh/internal/push"
	syncer "github.com/shhac/gt2gh/internal/sync"
)

func TestBranchCompletionUsesTrackedLocalBranchNames(t *testing.T) {
	output, err := executeWithService(t, cliService(&cliGitHub{}), "__complete", "link", "--branch", "be")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(output, "beta\n") || strings.Contains(output, "alpha\n") {
		t.Errorf("completion = %q", output)
	}
}

func TestPushBranchCompletionUsesTrackedLocalBranchNames(t *testing.T) {
	var stdout, stderr bytes.Buffer
	command := newWithPresentation("v", "gt2gh", &stdout, &stderr, cliService(&cliGitHub{}), syncer.Service{}, push.Service{Git: &cliPushGit{}, Graphite: cliPushGraphite{}}, Presentation{})
	command.SetArgs([]string{"__complete", "push", "--branch", "be"})
	err := command.Execute()
	output := stdout.String()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(output, "beta\n") || strings.Contains(output, "alpha\n") {
		t.Errorf("completion = %q", output)
	}
}

func TestTrunkCompletionUsesDeclaredLocalTrunks(t *testing.T) {
	output, err := executeWithService(t, cliService(&cliGitHub{}), "__complete", "link", "--trunk", "m")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(output, "main\n") {
		t.Errorf("completion = %q", output)
	}
}
