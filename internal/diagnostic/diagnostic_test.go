package diagnostic

import (
	"strings"
	"testing"
)

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
