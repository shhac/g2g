package repair

import (
	"os/exec"
	"strings"
	"testing"
)

func TestCommandPreservesArgumentsWithoutShellExpansion(t *testing.T) {
	args := []string{"synthetic-safe", "synthetic space", "synthetic'quote", "synthetic$variable", "synthetic;separator", "", "synthetic\nline"}
	command := "printf '%s\\000' " + Command(args)
	output, err := exec.Command("sh", "-c", command).Output()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(output), strings.Join(args, "\x00")+"\x00"; got != want {
		t.Fatalf("shell changed arguments: %q, want %q", got, want)
	}
}

// Quote decides whether a pasted command can be trusted, so the safe set is
// asserted directly: names the shell reads as written stay bare, and anything
// else is quoted without an embedded quote ending the quoting early.
func TestQuoteLeavesSafeArgumentsAloneAndQuotesTheRest(t *testing.T) {
	for _, safe := range []string{"main", "synthetic/feature-1", "a_b-c.d/e:f=g@h", "0123"} {
		if got := Quote(safe); got != safe {
			t.Errorf("Quote(%q) = %q, want it untouched", safe, got)
		}
	}
	for _, unsafe := range []string{"", "two words", "semi;colon", "dollar$sign", "back`tick`"} {
		got := Quote(unsafe)
		if !strings.HasPrefix(got, "'") || !strings.HasSuffix(got, "'") {
			t.Errorf("Quote(%q) = %q, want it quoted", unsafe, got)
		}
	}
	if got, want := Quote("it's"), `'it'\''s'`; got != want {
		t.Errorf("Quote(%q) = %q, want %q", "it's", got, want)
	}
}
