package api

import (
	"net/http"
	"strconv"
	"time"

	"linux-ops-platform/backend/internal/models"
	"linux-ops-platform/backend/internal/scheduler"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// terminalDialer connects the backend to an agent's terminal endpoint.
var terminalDialer = websocket.Dialer{
	HandshakeTimeout: 10 * time.Second,
}

// TerminalWS proxies an interactive terminal (PTY) session between the browser
// and the agent that manages the requested server. Phase 1 has no
// authentication: the endpoint must only be reachable from a controlled,
// isolated network.
func (h *Handlers) TerminalWS(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}

	var srv models.Server
	if err := h.db.First(&srv, id).Error; err != nil {
		fail(c, http.StatusNotFound, "server not found")
		return
	}

	// Reserve a backend-side slot before upgrading, so a burst of clients
	// cannot exhaust connections to agents.
	select {
	case h.termSem <- struct{}{}:
		defer func() { <-h.termSem }()
	default:
		fail(c, http.StatusServiceUnavailable, "too many terminal sessions")
		return
	}

	browser, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer browser.Close()
	browser.SetReadLimit(1 << 20)

	agentURL := "ws://" + scheduler.ServerAddr(&srv) + "/api/v1/ws/terminal"
	agentConn, resp, err := terminalDialer.Dial(agentURL, nil)
	if err != nil {
		msg := err.Error()
		if resp != nil {
			msg = resp.Status
		}
		writeJSONFrame(browser, map[string]string{
			"type": "error", "message": "connect agent: " + msg,
		})
		return
	}
	defer agentConn.Close()
	agentConn.SetReadLimit(1 << 20)

	rid := strconv.FormatInt(srv.ID, 10)
	h.audit("terminal.open", "server", rid, "")
	defer h.audit("terminal.close", "server", rid, "")

	// Keepalive. WriteControl is safe to call concurrently with the pumps,
	// unlike WriteMessage, which must stay single-writer per connection.
	stopPing := make(chan struct{})
	defer close(stopPing)
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stopPing:
				return
			case t := <-ticker.C:
				_ = browser.WriteControl(websocket.PingMessage, nil, t.Add(10*time.Second))
				_ = agentConn.WriteControl(websocket.PingMessage, nil, t.Add(10*time.Second))
			}
		}
	}()

	done := make(chan struct{}, 2)
	pump := func(src, dst *websocket.Conn) {
		defer func() { done <- struct{}{} }()
		for {
			mt, data, err := src.ReadMessage()
			if err != nil {
				return
			}
			// The backend is a transparent pipe: binary frames are terminal
			// data, text frames are control messages (resize/exit/error).
			_ = dst.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := dst.WriteMessage(mt, data); err != nil {
				return
			}
		}
	}

	go pump(agentConn, browser) // PTY output -> browser
	go pump(browser, agentConn) // keystrokes/control -> agent

	<-done
	// Closing both sides unblocks the other pump.
	_ = browser.Close()
	_ = agentConn.Close()
	<-done
}

func writeJSONFrame(conn *websocket.Conn, v interface{}) {
	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_ = conn.WriteJSON(v)
}
