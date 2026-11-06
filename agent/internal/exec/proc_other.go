//go:build !unix

package exec

import (
	"os/exec"
	"time"
)

// configureProcessGroup is the non-Unix fallback used only so the agent still
// compiles on development workstations (Windows). Production agents run on
// Linux and use proc_unix.go.
func configureProcessGroup(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return cmd.Process.Kill()
	}
	cmd.WaitDelay = 5 * time.Second
}
