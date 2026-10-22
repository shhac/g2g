package cli

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

func TestTimeoutEnvironmentIsAFloorAndFlagIsExact(t *testing.T) {
	t.Setenv("G2G_TIMEOUT", "2m")
	b := newBudgets(budgetCommand(t))
	if got := b.limit(discoveryTimeout); got != 2*time.Minute {
		t.Fatalf("discovery floor %s", got)
	}
	if got := b.limit(mutationBase + 10*landingPerBranch); got != mutationBase+10*landingPerBranch {
		t.Fatalf("environment shortened land to %s", got)
	}
	b = newBudgets(budgetCommand(t, "--timeout", "5s"))
	if got := b.limit(mutationBase + 10*landingPerBranch); got != 5*time.Second {
		t.Fatalf("explicit flag %s", got)
	}
}

func TestTimeoutDefaultsAreValidatedBeforeExecution(t *testing.T) {
	for _, test := range []struct {
		env   string
		args  []string
		valid bool
	}{
		{"invalid", []string{"synthetic"}, false},
		{"", []string{"synthetic"}, false},
		{"0s", []string{"synthetic"}, false},
		{"-1s", []string{"synthetic"}, false},
		{"3m", []string{"synthetic"}, true},
		{"invalid", []string{"synthetic", "--timeout", "4s"}, true},
		{"3m", []string{"synthetic", "--timeout", "0s"}, false},
		{"3m", []string{"synthetic", "--timeout", "-1s"}, false},
		{"invalid", []string{"synthetic", "--help"}, true},
		{"invalid", []string{"completion", "bash"}, true},
	} {
		t.Run(test.env+strings.Join(test.args, "_"), func(t *testing.T) {
			t.Setenv("G2G_TIMEOUT", test.env)
			cmd := NewWithOptions(Options{Stdout: io.Discard, Stderr: io.Discard})
			ran := false
			cmd.AddCommand(&cobra.Command{Use: "synthetic", RunE: func(*cobra.Command, []string) error { ran = true; return nil }})
			cmd.SetArgs(test.args)
			err := cmd.ExecuteContext(context.Background())
			if (err == nil) != test.valid {
				t.Fatalf("error %v, valid %v", err, test.valid)
			}
			if !test.valid && ran {
				t.Fatal("invalid timeout reached command")
			}
		})
	}
}
