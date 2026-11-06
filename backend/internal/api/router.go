// Package api wires HTTP routes, handlers and middleware for the management
// backend. Phase 1 has no authentication: the API must only be reachable from
// a controlled, isolated network.
package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"linux-ops-platform/backend/internal/agentclient"
	"linux-ops-platform/backend/internal/config"
	"linux-ops-platform/backend/internal/logstore"
	"linux-ops-platform/backend/internal/models"
	"linux-ops-platform/backend/internal/scheduler"
	"linux-ops-platform/backend/internal/ws"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Handlers bundles dependencies shared by all HTTP handlers.
type Handlers struct {
	cfg   *config.Config
	db    *gorm.DB
	sched *scheduler.Scheduler
	store *logstore.Store
	hub   *ws.Hub
	agent *agentclient.Client
	// termSem bounds the number of interactive terminal WebSockets proxied by
	// this backend.
	termSem chan struct{}
}

func NewHandlers(cfg *config.Config, db *gorm.DB, sched *scheduler.Scheduler, store *logstore.Store, hub *ws.Hub, agent *agentclient.Client) *Handlers {
	maxTerm := cfg.WebSocket.MaxTerminalSessions
	if maxTerm <= 0 {
		maxTerm = 16
	}
	return &Handlers{
		cfg: cfg, db: db, sched: sched, store: store, hub: hub, agent: agent,
		termSem: make(chan struct{}, maxTerm),
	}
}

// Register installs all routes.
func (h *Handlers) Register(r *gin.Engine) {
	r.Use(h.cors(), h.bodyLimit())
	v1 := r.Group("/api/v1")

	v1.GET("/health", h.Health)
	v1.GET("/config/limits", h.Limits)
	v1.GET("/stats/overview", h.Overview)

	v1.GET("/servers", h.ListServers)
	v1.POST("/servers", h.CreateServer)
	v1.GET("/servers/:id", h.GetServer)
	v1.PUT("/servers/:id", h.UpdateServer)
	v1.DELETE("/servers/:id", h.DeleteServer)
	v1.POST("/servers/:id/check", h.CheckServer)
	v1.GET("/servers/:id/agent", h.GetAgent)
	v1.GET("/servers/:id/agent/update/check", h.AgentUpdateCheck)
	v1.POST("/servers/:id/agent/update", h.AgentUpdate)

	v1.POST("/agents/update/check", h.BatchAgentUpdateCheck)
	v1.POST("/agents/update", h.BatchAgentUpdate)

	v1.GET("/servers/:id/fs/list", h.FSList)
	v1.GET("/servers/:id/fs/stat", h.FSStat)
	v1.GET("/servers/:id/fs/read", h.FSRead)
	v1.GET("/servers/:id/fs/download", h.FSDownload)
	v1.PUT("/servers/:id/fs/write", h.FSWrite)
	v1.PUT("/servers/:id/fs/upload", h.FSUpload)
	v1.POST("/servers/:id/fs/op", h.FSOp)

	v1.GET("/groups", h.ListGroups)
	v1.POST("/groups", h.CreateGroup)
	v1.PUT("/groups/:id", h.UpdateGroup)
	v1.DELETE("/groups/:id", h.DeleteGroup)

	v1.GET("/tags", h.ListTags)
	v1.POST("/tags", h.CreateTag)
	v1.DELETE("/tags/:id", h.DeleteTag)

	v1.POST("/tasks", h.CreateTask)
	v1.GET("/tasks", h.ListTasks)
	v1.GET("/tasks/:id", h.GetTask)
	v1.GET("/tasks/:id/targets", h.ListTaskTargets)
	v1.POST("/tasks/:id/cancel", h.CancelTask)

	v1.GET("/targets/:id", h.GetTarget)
	v1.GET("/targets/:id/logs", h.GetTargetLogs)
	v1.GET("/targets/:id/logs/download", h.DownloadTargetLogs)
	v1.POST("/targets/:id/cancel", h.CancelTarget)
	v1.POST("/targets/:id/retry", h.RetryTarget)

	v1.GET("/audit", h.ListAudit)

	v1.GET("/ws/tasks/:id", h.TaskWS)
	v1.GET("/ws/terminal/:id", h.TerminalWS)
}

// ---- response helpers ----

func ok(c *gin.Context, data interface{}) {
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": data})
}

func fail(c *gin.Context, status int, msg string) {
	c.JSON(status, gin.H{"code": status, "message": msg})
}

func parseID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		fail(c, http.StatusBadRequest, "invalid id")
		return 0, false
	}
	return id, true
}

func pageParams(c *gin.Context) (page, size int) {
	page, _ = strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ = strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 20
	}
	if size > 200 {
		size = 200
	}
	return page, size
}

// ---- middleware ----

func (h *Handlers) cors() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", h.cfg.AllowOrigin)
		c.Header("Access-Control-Allow-Methods", "GET,POST,PUT,DELETE,OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type,Authorization")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

func (h *Handlers) bodyLimit() gin.HandlerFunc {
	max := int64(h.cfg.Scheduler.MaxScriptBytes)*2 + 1<<20
	return func(c *gin.Context) {
		// Uploads are streamed and have their own per-chunk limit.
		if strings.Contains(c.Request.URL.Path, "/fs/upload") {
			c.Next()
			return
		}
		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, max)
		}
		c.Next()
	}
}

// ---- audit ----

func (h *Handlers) audit(action, resource, resourceID, detail string) {
	h.db.Create(&models.AuditEvent{
		Action: action, Resource: resource, ResourceID: resourceID,
		Operator: "anonymous", DetailJSON: detail, CreatedAt: time.Now().UTC(),
	})
}

// Health reports backend liveness.
func (h *Handlers) Health(c *gin.Context) {
	ok(c, gin.H{"status": "ok", "time": time.Now().UTC().Format(time.RFC3339)})
}

// Limits exposes the effective operational limits (read-only).
func (h *Handlers) Limits(c *gin.Context) {
	ok(c, gin.H{
		"dispatch_concurrency":  h.cfg.Scheduler.DispatchConcurrency,
		"default_timeout_ms":    h.cfg.Scheduler.DefaultTimeoutMS,
		"max_timeout_ms":        h.cfg.Scheduler.MaxTimeoutMS,
		"max_script_bytes":      h.cfg.Scheduler.MaxScriptBytes,
		"max_output_bytes":      h.cfg.Scheduler.MaxOutputBytes,
		"poll_interval_ms":      h.cfg.Scheduler.PollIntervalMS,
		"heartbeat_sec":         h.cfg.Scheduler.HeartbeatIntervalSec,
		"ws_max_connections":    h.cfg.WebSocket.MaxConnections,
		"max_terminal_sessions": h.cfg.WebSocket.MaxTerminalSessions,
	})
}

// Overview powers the dashboard.
func (h *Handlers) Overview(c *gin.Context) {
	var total, online, offline int64
	h.db.Model(&models.Server{}).Count(&total)
	h.db.Model(&models.Server{}).Where("status = ?", models.StatusOnline).Count(&online)
	// Treat "unknown" (never checked yet) as not online so the dashboard
	// numbers add up: total = online + offline.
	h.db.Model(&models.Server{}).Where("status <> ?", models.StatusOnline).Count(&offline)

	type versionCount struct {
		AgentVersion string `json:"version"`
		Count        int64  `json:"count"`
	}
	var versions []versionCount
	h.db.Model(&models.Server{}).Select("agent_version as agent_version, count(*) as count").
		Group("agent_version").Scan(&versions)

	var recent []models.ExecutionTask
	h.db.Order("created_at desc").Limit(10).Find(&recent)

	var taskTotal, taskSuccess int64
	h.db.Model(&models.ExecutionTask{}).Count(&taskTotal)
	h.db.Model(&models.ExecutionTask{}).Where("status = ?", models.StatusSuccess).Count(&taskSuccess)
	rate := 0.0
	if taskTotal > 0 {
		rate = float64(taskSuccess) / float64(taskTotal) * 100
	}

	ok(c, gin.H{
		"servers_total": total, "servers_online": online, "servers_offline": offline,
		"agent_versions": versions, "recent_tasks": recent,
		"tasks_total": taskTotal, "tasks_success": taskSuccess, "success_rate": rate,
		"ws_connections": h.hub.ConnCount(),
	})
}
