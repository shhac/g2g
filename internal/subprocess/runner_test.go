package subprocess

import (
	"context"
	"strings"
	"testing"

	"github.com/shhac/gt2gh/internal/testutil"
)

func TestExecRunnerUsesFakeGraphiteCLIOnPATH(t *testing.T) {
	testutil.WithFakeExecutables(t, map[string]string{
		"gt": "printf 'fake gt: %s\\n' \"$*\"\n",
	})

	output, err := (ExecRunner{}).Run(context.Background(), "gt", "log", "short")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := string(output), "fake gt: log short\n"; got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

func TestExecRunnerUsesFakeGitHubCLIOnPATH(t *testing.T) {
	testutil.WithFakeExecutables(t, map[string]string{
		"gh": "printf 'fake gh: %s\\n' \"$*\"\n",
	})

	output, err := (ExecRunner{}).Run(context.Background(), "gh", "stack", "link", "base", "head")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := string(output), "fake gh: stack link base head\n"; got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

func TestExecRunnerIncludesCommandOutputOnFailure(t *testing.T) {
	testutil.WithFakeExecutables(t, map[string]string{
		"gt": "printf 'simulated failure\\n' >&2\nexit 7\n",
	})

	output, err := (ExecRunner{}).Run(context.Background(), "gt", "log")
	if err == nil {
		t.Fatal("Run() error = nil, want command failure")
	}
	if !strings.Contains(string(output), "simulated failure") {
		t.Errorf("output = %q, want stderr from fake executable", output)
	}
}
