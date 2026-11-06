//go:build unix

package update

import (
	"os"
	"syscall"
)

// Restart replaces the current process image with the freshly installed
// binary. The PID and open file descriptors (stdout/stderr, nohup log) are
// preserved, so this works without a service manager.
func Restart(exe string) error {
	return syscall.Exec(exe, os.Args, os.Environ())
}
