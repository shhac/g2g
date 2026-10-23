//go:build !unix

package subprocess

import "os/exec"

// Keep CommandContext's bounded cancellation on platforms without Unix groups.
func configureCancellation(_ *exec.Cmd) func() { return func() {} }
