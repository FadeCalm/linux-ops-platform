package api

import (
	"net/http"
	"time"

	"linux-ops-platform/backend/internal/models"
	"linux-ops-platform/backend/internal/ws"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// CheckOrigin is permissive because phase 1 has no authentication. Access is
// expected to be restricted by network controls; M8 must add real controls.
var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 8192,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

type wsClientMessage struct {
	Type     string           `json:"type"`
	TargetID string           `json:"target_id"`
	Resume   map[string]int64 `json:"resume"`
}

// TaskWS upgrades to a WebSocket and streams live task output.
func (h *Handlers) TaskWS(c *gin.Context) {
	taskID := c.Param("id")
	var parent models.ExecutionTask
	if err := h.db.First(&parent, "id = ?", taskID).Error; err != nil {
		fail(c, http.StatusNotFound, "task not found")
		return
	}
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	client, registered := h.hub.Register(taskID)
	if !registered {
		conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseTryAgainLater, "too many connections"))
		conn.Close()
		return
	}
	defer h.hub.Unregister(client)

	// Writer: the only goroutine allowed to write to the connection.
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		defer conn.Close()
		for {
			select {
			case <-client.Done():
				return
			case f := <-client.Send:
				conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if err := conn.WriteJSON(f); err != nil {
					return
				}
			}
		}
	}()

	// Initial snapshot of the task's targets.
	var targets []models.ExecutionTaskTarget
	h.db.Where("parent_task_id = ?", taskID).Order("created_at asc").Find(&targets)
	snap := ws.Frame{Type: "snapshot", TaskID: taskID, Status: parent.Status}
	for _, t := range targets {
		snap.Targets = append(snap.Targets, ws.TargetSnapshot{
			TargetID: t.ID, ServerID: t.ServerID, ServerName: t.ServerName, Status: t.Status, Seq: t.Seq,
		})
	}
	select {
	case client.Send <- snap:
	case <-client.Done():
		return
	}

	// Reader loop on the connection goroutine.
	conn.SetReadLimit(1 << 20)
	conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})
	for {
		var msg wsClientMessage
		if err := conn.ReadJSON(&msg); err != nil {
			break
		}
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		switch msg.Type {
		case "subscribe":
			resume := msg.Resume
			go func() {
				for _, f := range h.hub.Replay(taskID, resume) {
					select {
					case client.Send <- f:
					case <-client.Done():
						return
					}
				}
			}()
		case "cancel":
			if msg.TargetID != "" {
				h.sched.CancelTarget(msg.TargetID)
				h.audit("target.cancel", "target", msg.TargetID, "via=websocket")
			}
		}
	}
	client.Close()
	conn.Close()
	<-writerDone
}
