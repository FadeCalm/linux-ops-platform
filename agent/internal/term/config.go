// Package term implements interactive shell sessions backed by a pseudo
// terminal (PTY). The browser never talks to this package directly: the
// management backend proxies a WebSocket to the agent's
// /api/v1/ws/terminal endpoint.
//
// On platforms without PTY support (for example Windows) the package still
// compiles and serves a stub that reports the feature as unavailable, so the
// agent remains cross-compilable.
package term

// Config controls how interactive sessions are started.
type Config struct {
	// Shell is executed inside the PTY. Defaults to /bin/bash.
	Shell string
	// WorkDir is the initial directory. Defaults to "/".
	WorkDir string
	// User is empty to inherit the agent process user, or "root" to run the
	// shell with UID/GID 0. The latter only works when the agent itself runs
	// with enough privilege (usually as root).
	User string
	// MaxSessions caps concurrent terminal sessions on this agent.
	MaxSessions int
	// IdleTimeoutSec closes a session that has seen no input or output for
	// this long. Zero or negative disables the check.
	IdleTimeoutSec int
	// MaxDurationSec closes a session after this long in total, regardless of
	// activity. Zero or negative disables the check.
	MaxDurationSec int
}
