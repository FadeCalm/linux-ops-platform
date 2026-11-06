//go:build !unix

package term

import (
	"errors"

	"github.com/gorilla/websocket"
)

// Manager is a stub on platforms without PTY support. It keeps the agent
// cross-compilable (for example on Windows, where the agent is only used for
// health checks).
type Manager struct{ cfg Config }

func NewManager(cfg Config) *Manager { return &Manager{cfg: cfg} }

func (m *Manager) Acquire() bool { return true }

func (m *Manager) Release() {}

func (m *Manager) Count() int { return 0 }

func (m *Manager) Serve(conn *websocket.Conn) error {
	return errors.New("interactive terminals are not supported on this platform")
}
