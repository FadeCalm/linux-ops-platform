//go:build unix

package exec

import (
	"os/exec"
	"syscall"
	"time"
)

// configureProcessGroup places the command in its own process group and makes
// cancellation kill the whole group, so child processes are not orphaned.
func configureProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 5 * time.Second
}
