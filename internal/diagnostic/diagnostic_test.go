package diagnostic

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestWarnWritesOnceWithoutDebugSink(t *testing.T) {
	var output bytes.Buffer
	ctx := WithWarningWriter(context.Background(), &output)
	Warn(ctx, "graphite-version", "synthetic compatibility warning")
	Warn(ctx, "graphite-version", "synthetic compatibility warning")
	if got, want := output.String(), "warning: synthetic compatibility warning\n"; got != want {
		t.Errorf("warning = %q, want %q", got, want)
	}
}

func TestSafeCommandRedactsCredentialsAndGraphQL(t *testing.T) {
	for _, test := range []struct {
		name string
		got  string
		no   string
	}{
		{"GraphQL", SafeCommand("gh", []string{"api", "graphql", "-f", "query=synthetic-secret"}), "synthetic-secret"},
		{"token", SafeCommand("gh", []string{"api", "--token", "synthetic-secret"}), "synthetic-secret"},
		{"header", SafeCommand("gh", []string{"api", "-H", "Authorization: synthetic-secret"}), "synthetic-secret"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := test.got; strings.Contains(got, test.no) {
				t.Errorf("SafeCommand() = %q, leaks %q", got, test.no)
			}
		})
	}
}

func TestBoundedOutputRedactsAndBoundsCredentialLines(t *testing.T) {
	message := "Authorization: synthetic-secret\nvisible " + strings.Repeat("x", 600)
	got := BoundedOutput([]byte(message))
	if strings.Contains(got, "synthetic-secret") || !strings.Contains(got, "[redacted sensitive diagnostic]") || !strings.Contains(got, "…") {
		t.Errorf("BoundedOutput() = %q", got)
	}
}
