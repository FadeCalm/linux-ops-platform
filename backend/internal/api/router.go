// Package api wires HTTP routes, handlers and middleware for the management
// backend. Access can be gated by one shared password (see auth.go); that is a
// convenience barrier, not real authentication, so the API must still only be
// reachable from a controlled, isolated network.
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
	// auth gates /api/v1 behind the shared access password; disabled when the
	// password is empty.
	auth         *authGate
	loginLimiter *loginLimiter
}

func NewHandlers(cfg *config.Config, db *gorm.DB, sched *scheduler.Scheduler, store *logstore.Store, hub *ws.Hub, agent *agentclient.Client) *Handlers {
	maxTerm := cfg.WebSocket.MaxTerminalSessions
	if maxTerm <= 0 {
		maxTerm = 16
	}
	return &Handlers{
		cfg: cfg, db: db, sched: sched, store: store, hub: hub, agent: agent,
		termSem:      make(chan struct{}, maxTerm),
		auth:         newAuthGate(cfg.Auth.AccessPassword, time.Duration(cfg.Auth.SessionHours)*time.Hour),
		loginLimiter: newLoginLimiter(10, time.Minute),
	}
}

// Register installs all routes.
func (h *Handlers) Register(r *gin.Engine) {
	r.Use(h.cors(), h.bodyLimit())
	v1 := r.Group("/api/v1")

	// Public routes: liveness plus the login endpoints themselves.
	v1.GET("/health", h.Health)
	v1.GET("/auth/status", h.AuthStatus)
	v1.POST("/auth/login", h.AuthLogin)
	v1.POST("/auth/logout", h.AuthLogout)

	// Everything below requires a valid session cookie when a password is set.
	p := v1.Group("", h.requireAuth())

	p.GET("/config/limits", h.Limits)
	p.GET("/stats/overview", h.Overview)

	p.GET("/servers", h.ListServers)
	p.POST("/servers", h.CreateServer)
	p.GET("/servers/:id", h.GetServer)
	p.PUT("/servers/:id", h.UpdateServer)
	p.DELETE("/servers/:id", h.DeleteServer)
	p.GET("/servers/:id/agent", h.GetAgent)
	p.GET("/servers/:id/agent/update/check", h.AgentUpdateCheck)
	p.POST("/servers/:id/agent/update", h.AgentUpdate)

	p.POST("/agents/check", h.BatchCheckServers)
	p.POST("/agents/update/check", h.BatchAgentUpdateCheck)
	p.POST("/agents/update", h.BatchAgentUpdate)

	p.GET("/servers/:id/fs/list", h.FSList)
	p.GET("/servers/:id/fs/stat", h.FSStat)
	p.GET("/servers/:id/fs/read", h.FSRead)
	p.GET("/servers/:id/fs/download", h.FSDownload)
	p.PUT("/servers/:id/fs/write", h.FSWrite)
	p.PUT("/servers/:id/fs/upload", h.FSUpload)
	p.POST("/servers/:id/fs/op", h.FSOp)

	p.GET("/groups", h.ListGroups)
	p.POST("/groups", h.CreateGroup)
	p.PUT("/groups/:id", h.UpdateGroup)
	p.DELETE("/groups/:id", h.DeleteGroup)

	p.GET("/tags", h.ListTags)
	p.POST("/tags", h.CreateTag)
	p.DELETE("/tags/:id", h.DeleteTag)

	p.POST("/tasks", h.CreateTask)
	p.GET("/tasks", h.ListTasks)
	p.GET("/tasks/:id", h.GetTask)
	p.GET("/tasks/:id/targets", h.ListTaskTargets)
	p.POST("/tasks/:id/cancel", h.CancelTask)

	p.GET("/targets/:id", h.GetTarget)
	p.GET("/targets/:id/logs", h.GetTargetLogs)
	p.GET("/targets/:id/logs/download", h.DownloadTargetLogs)
	p.POST("/targets/:id/cancel", h.CancelTarget)
	p.POST("/targets/:id/retry", h.RetryTarget)

	p.GET("/audit", h.ListAudit)

	p.GET("/ws/tasks/:id", h.TaskWS)
	p.GET("/ws/terminal/:id", h.TerminalWS)
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
	// Cookies can only be sent cross-origin when the origin is explicit; "*"
	// is incompatible with credentialed requests.
	allowCredentials := h.cfg.AllowOrigin != "" && h.cfg.AllowOrigin != "*"
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", h.cfg.AllowOrigin)
		if allowCredentials {
			c.Header("Access-Control-Allow-Credentials", "true")
		}
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
