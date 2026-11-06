package api

import (
	"log"
	"net/http"

	"github.com/gorilla/websocket"
)

// termUpgrader accepts interactive terminal connections. Phase 1 has no
// authentication, so the agent must stay on a private management network. The
// browser never connects here directly: the management backend proxies the
// WebSocket to /api/v1/ws/terminal.
var termUpgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

func (s *Server) terminalWS(w http.ResponseWriter, r *http.Request) {
	if !s.term.Acquire() {
		http.Error(w, "too many terminal sessions", http.StatusServiceUnavailable)
		return
	}
	defer s.term.Release()

	conn, err := termUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	conn.SetReadLimit(1 << 20)

	if err := s.term.Serve(conn); err != nil {
		log.Printf("[agent] terminal session: %v", err)
	}
}
