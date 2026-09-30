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
