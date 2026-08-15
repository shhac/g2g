package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestDebugIsPersistentStderrOnlyAndDoesNotChangeLinkMutation(t *testing.T) {
	for _, args := range [][]string{{"--debug", "link", "--branch", "beta"}, {"link", "--debug", "--branch", "beta"}} {
		var stdout, stderr bytes.Buffer
		github := &cliGitHub{}
		command := NewWithService("v0.4.0", &stdout, &stderr, cliService(github))
		command.SetArgs(args)
		if err := command.Execute(); err != nil {
			t.Fatalf("Execute(%v) error = %v", args, err)
		}
		if github.links != 0 || strings.Contains(stdout.String(), "debug event=") {
			t.Errorf("args=%v stdout=%q links=%d", args, stdout.String(), github.links)
		}
		for _, expected := range []string{"event=operation.start", "operation=\"link\"", "target_source=\"--branch\"", "event=discovery.target", "event=discovery.trunk", "event=github.native_stack_membership", "observation=\"per_pull_request\"", "event=link.plan", "decision=\"ready\""} {
			if !strings.Contains(stderr.String(), expected) {
				t.Errorf("args=%v debug missing %q: %q", args, expected, stderr.String())
			}
		}
	}
}

func TestNormalLinkLeavesStderrQuiet(t *testing.T) {
	var stdout, stderr bytes.Buffer
	command := NewWithService("v0.4.0", &stdout, &stderr, cliService(&cliGitHub{}))
	command.SetArgs([]string{"link"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if got := stderr.String(); got != "" {
		t.Errorf("stderr = %q, want empty without --debug", got)
	}
}
