package subprocess

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/shhac/gt2gh/internal/diagnostic"
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

func TestObservingRunnerWritesSafeBoundedDiagnostics(t *testing.T) {
	var output bytes.Buffer
	ctx := diagnostic.WithSink(context.Background(), diagnostic.Writer{Out: &output})
	runner := ObservingRunner{Runner: runnerFunc(func(context.Context, string, ...string) ([]byte, error) {
		return []byte("Authorization: synthetic-secret\nsynthetic diagnostic"), errors.New("synthetic failure")
	})}
	if _, err := runner.Run(ctx, "gh", "api", "graphql", "-f", "query=synthetic-secret"); err == nil {
		t.Fatal("Run() error = nil")
	}
	got := output.String()
	for _, expected := range []string{"event=subprocess.start", "command=\"gh api graphql query=omitted\"", "event=subprocess.end", "status=\"error\"", "exit=\"error\"", "event=subprocess.output", "[redacted sensitive diagnostic]"} {
		if !strings.Contains(got, expected) {
			t.Errorf("diagnostics missing %q: %q", expected, got)
		}
	}
	if strings.Contains(got, "synthetic-secret") {
		t.Errorf("diagnostics leaked a secret: %q", got)
	}
}

func TestObservingRunnerReportsCanceledStatus(t *testing.T) {
	var output bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ctx = diagnostic.WithSink(ctx, diagnostic.Writer{Out: &output})
	runner := ObservingRunner{Runner: runnerFunc(func(context.Context, string, ...string) ([]byte, error) {
		return nil, context.Canceled
	})}
	if _, err := runner.Run(ctx, "gt", "--version"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(output.String(), "status=\"canceled\"") || !strings.Contains(output.String(), "exit=\"canceled\"") {
		t.Errorf("diagnostics = %q", output.String())
	}
}

type runnerFunc func(context.Context, string, ...string) ([]byte, error)

func (f runnerFunc) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return f(ctx, name, args...)
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
