//go:build unix

package subprocess

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// Give Git a chance to remove its locks before forcing termination. Each
// invocation gets its own process group so a wrapper's children stop too,
// without signalling the agent or shell that started g2g.
func configureCancellation(command *exec.Cmd) func() {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var force *time.Timer
	signal := func(sig syscall.Signal) error {
		err := syscall.Kill(-command.Process.Pid, sig)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	command.Cancel = func() error {
		if err := signal(syscall.SIGTERM); err != nil {
			return err
		}
		force = time.AfterFunc(waitDelay/2, func() { _ = signal(syscall.SIGKILL) })
		return nil
	}
	return func() {
		// Wait joins exec's cancellation worker before this runs. Stop the
		// watchdog and remove any descendants that outlived the direct child.
		if force != nil {
			force.Stop()
			_ = signal(syscall.SIGKILL)
		}
	}
}
