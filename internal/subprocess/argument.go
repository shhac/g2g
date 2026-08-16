package subprocess

import (
	"fmt"
	"strings"
)

// OptionLike reports whether a value would be read as an option rather than as
// the argument it is meant to be.
//
// This is the whole of the check, in one place, because it is a security
// property and it was previously written out five times across four packages
// in four different wordings. A branch called `--upload-pack=...` handed to a
// subprocess is not a branch; it is an instruction. Nothing here spawns
// anything — this package owns the process seam, so what is safe to hand a
// process belongs with it.
//
// Callers keep their own error wording, because "cannot be passed safely to
// git" and "…to gt" are genuinely different sentences and the reader needs to
// know which tool refused. Only the rule is shared.
func OptionLike(value string) bool { return strings.HasPrefix(value, "-") }

// CheckArgument rejects a value a process would misread, naming the tool it was
// destined for.
func CheckArgument(tool, kind, value string) error {
	if value == "" {
		return fmt.Errorf("%s must not be empty", kind)
	}
	if OptionLike(value) {
		return fmt.Errorf("%s %q cannot be passed safely to %s", kind, value, tool)
	}
	return nil
}
