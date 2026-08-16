package subprocess

import (
	"strings"
	"testing"
)

// The rule lived in five places across four packages in four wordings before
// this, so the exhaustive cases belong here now — one place to harden.
func TestOptionLikeRejectsAnythingAProcessWouldMisread(t *testing.T) {
	for value, want := range map[string]bool{
		"synthetic-branch":        false,
		"feature/synthetic":       false,
		"":                        false,
		"-synthetic":              true,
		"--upload-pack=synthetic": true,
		"--":                      true,
		"-":                       true,
	} {
		if got := OptionLike(value); got != want {
			t.Errorf("OptionLike(%q) = %t, want %t", value, got, want)
		}
	}
}

// The caller's tool name reaches the message, because "git refused this" and
// "gt refused this" are different things for a reader to act on.
func TestCheckArgumentNamesTheToolAndTheKind(t *testing.T) {
	err := CheckArgument("gt", "branch", "-synthetic")
	if err == nil {
		t.Fatal("CheckArgument() = nil for an option-like value")
	}
	for _, want := range []string{"gt", "branch", "-synthetic"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to contain %q", err, want)
		}
	}
}

func TestCheckArgumentRejectsEmptyAndAcceptsOrdinaryNames(t *testing.T) {
	if err := CheckArgument("git", "branch name", ""); err == nil {
		t.Error("CheckArgument() = nil for an empty value")
	}
	if err := CheckArgument("git", "branch name", "synthetic-branch"); err != nil {
		t.Errorf("CheckArgument() = %v for an ordinary name", err)
	}
}
