package api

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"linux-ops-platform/backend/internal/models"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type createTaskRequest struct {
	Command        string  `json:"command"`
	WorkDir        string  `json:"work_dir"`
	TimeoutMS      int64   `json:"timeout_ms"`
	ServerIDs      []int64 `json:"server_ids"`
	IdempotencyKey *string `json:"idempotency_key"`
}

func (h *Handlers) CreateTask(c *gin.Context) {
	var req createTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	req.Command = strings.TrimSpace(req.Command)
	if req.Command == "" {
		fail(c, http.StatusBadRequest, "command is required")
		return
	}
	if len(req.Command) > h.cfg.Scheduler.MaxScriptBytes {
		fail(c, http.StatusBadRequest, fmt.Sprintf("command exceeds %d bytes", h.cfg.Scheduler.MaxScriptBytes))
		return
	}
	if len(req.ServerIDs) == 0 {
		fail(c, http.StatusBadRequest, "server_ids is required")
		return
	}
	if req.TimeoutMS <= 0 {
		req.TimeoutMS = h.cfg.Scheduler.DefaultTimeoutMS
	}
	if req.TimeoutMS > h.cfg.Scheduler.MaxTimeoutMS {
		req.TimeoutMS = h.cfg.Scheduler.MaxTimeoutMS
	}

	// Idempotency: an existing key returns the original task unchanged.
	if req.IdempotencyKey != nil && strings.TrimSpace(*req.IdempotencyKey) != "" {
		var existing models.ExecutionTask
		err := h.db.Where("idempotency_key = ?", *req.IdempotencyKey).First(&existing).Error
		if err == nil {
			h.respondTask(c, existing.ID)
			return
		}
	}

	var servers []models.Server
	if err := h.db.Where("id IN ?", req.ServerIDs).Find(&servers).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	if len(servers) != len(uniqueIDs(req.ServerIDs)) {
		fail(c, http.StatusBadRequest, "one or more servers not found")
		return
	}

	now := time.Now().UTC()
	parent := models.ExecutionTask{
		ID: uuid.NewString(), Command: req.Command, WorkDir: req.WorkDir,
		TimeoutMS: req.TimeoutMS, Status: models.StatusPending, Total: len(servers),
		IdempotencyKey: req.IdempotencyKey, CreatedBy: "anonymous", CreatedAt: now,
	}
	targets := make([]models.ExecutionTaskTarget, 0, len(servers))
	for _, srv := range servers {
		targets = append(targets, models.ExecutionTaskTarget{
			ID: uuid.NewString(), ParentTaskID: parent.ID, ServerID: srv.ID,
			ServerName: srv.Name, AgentAddr: fmt.Sprintf("%s:%d", srv.IP, srv.AgentPort),
			Status: models.StatusPending, CreatedAt: now,
		})
	}

	if err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&parent).Error; err != nil {
			return err
		}
		return tx.Create(&targets).Error
	}); err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}

	h.audit("task.create", "task", parent.ID, fmt.Sprintf("servers=%d command=%s", len(servers), truncate(req.Command, 200)))
	h.sched.Submit(targets)
	h.respondTask(c, parent.ID)
}

func (h *Handlers) respondTask(c *gin.Context, taskID string) {
	var parent models.ExecutionTask
	if err := h.db.First(&parent, "id = ?", taskID).Error; err != nil {
		fail(c, http.StatusNotFound, "task not found")
		return
	}
	var targets []models.ExecutionTaskTarget
	h.db.Where("parent_task_id = ?", taskID).Order("created_at asc").Find(&targets)
	ok(c, gin.H{"task": parent, "targets": targets})
}

func (h *Handlers) ListTasks(c *gin.Context) {
	page, size := pageParams(c)
	q := h.db.Model(&models.ExecutionTask{})

	if v := c.Query("status"); v != "" {
		q = q.Where("status = ?", v)
	}
	if v := c.Query("server_id"); v != "" {
		q = q.Joins("JOIN execution_task_targets t ON t.parent_task_id = execution_tasks.id").
			Where("t.server_id = ?", v)
	}
	if v := c.Query("from"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			q = q.Where("execution_tasks.created_at >= ?", t.UTC())
		}
	}
	if v := c.Query("to"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			q = q.Where("execution_tasks.created_at <= ?", t.UTC())
		}
	}

	var total int64
	q.Count(&total)
	var items []models.ExecutionTask
	if err := q.Order("execution_tasks.created_at desc").Offset((page - 1) * size).Limit(size).Find(&items).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, gin.H{"items": items, "total": total, "page": page, "page_size": size})
}

func (h *Handlers) GetTask(c *gin.Context) {
	taskID := c.Param("id")
	var parent models.ExecutionTask
	if err := h.db.First(&parent, "id = ?", taskID).Error; err != nil {
		fail(c, http.StatusNotFound, "task not found")
		return
	}
	var targets []models.ExecutionTaskTarget
	h.db.Where("parent_task_id = ?", taskID).Order("created_at asc").Find(&targets)
	ok(c, gin.H{"task": parent, "targets": targets})
}

func (h *Handlers) ListTaskTargets(c *gin.Context) {
	taskID := c.Param("id")
	var targets []models.ExecutionTaskTarget
	if err := h.db.Where("parent_task_id = ?", taskID).Order("created_at asc").Find(&targets).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, gin.H{"items": targets})
}

func (h *Handlers) GetTarget(c *gin.Context) {
	var target models.ExecutionTaskTarget
	if err := h.db.First(&target, "id = ?", c.Param("id")).Error; err != nil {
		fail(c, http.StatusNotFound, "target not found")
		return
	}
	ok(c, target)
}

func (h *Handlers) CancelTarget(c *gin.Context) {
	targetID := c.Param("id")
	var target models.ExecutionTaskTarget
	if err := h.db.First(&target, "id = ?", targetID).Error; err != nil {
		fail(c, http.StatusNotFound, "target not found")
		return
	}
	active := h.sched.CancelTarget(targetID)
	if !active && isTerminal(target.Status) {
		fail(c, http.StatusConflict, "target already finished")
		return
	}
	h.audit("target.cancel", "target", targetID, "")
	ok(c, gin.H{"cancelling": true})
}

func (h *Handlers) CancelTask(c *gin.Context) {
	taskID := c.Param("id")
	n := h.sched.CancelTask(taskID)
	h.audit("task.cancel", "task", taskID, fmt.Sprintf("cancelled=%d", n))
	ok(c, gin.H{"cancelled": n})
}

func (h *Handlers) RetryTarget(c *gin.Context) {
	targetID := c.Param("id")
	var target models.ExecutionTaskTarget
	if err := h.db.First(&target, "id = ?", targetID).Error; err != nil {
		fail(c, http.StatusNotFound, "target not found")
		return
	}
	var parent models.ExecutionTask
	if err := h.db.First(&parent, "id = ?", target.ParentTaskID).Error; err != nil {
		fail(c, http.StatusNotFound, "parent task not found")
		return
	}
	var server models.Server
	if err := h.db.First(&server, "id = ?", target.ServerID).Error; err != nil {
		fail(c, http.StatusNotFound, "server not found")
		return
	}
	now := time.Now().UTC()
	newParent := models.ExecutionTask{
		ID: uuid.NewString(), Command: parent.Command, WorkDir: parent.WorkDir,
		TimeoutMS: parent.TimeoutMS, Status: models.StatusPending, Total: 1,
		CreatedBy: "anonymous", CreatedAt: now,
	}
	newTarget := models.ExecutionTaskTarget{
		ID: uuid.NewString(), ParentTaskID: newParent.ID, ServerID: server.ID,
		ServerName: server.Name, AgentAddr: fmt.Sprintf("%s:%d", server.IP, server.AgentPort),
		Status: models.StatusPending, CreatedAt: now,
	}
	if err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&newParent).Error; err != nil {
			return err
		}
		return tx.Create(&newTarget).Error
	}); err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	h.audit("target.retry", "target", targetID, "new_task="+newParent.ID)
	h.sched.Submit([]models.ExecutionTaskTarget{newTarget})
	h.respondTask(c, newParent.ID)
}

// GetTargetLogs returns a base64-encoded chunk of a persisted stream.
func (h *Handlers) GetTargetLogs(c *gin.Context) {
	var target models.ExecutionTaskTarget
	if err := h.db.First(&target, "id = ?", c.Param("id")).Error; err != nil {
		fail(c, http.StatusNotFound, "target not found")
		return
	}
	stream := c.DefaultQuery("stream", "stdout")
	if stream != "stdout" && stream != "stderr" {
		fail(c, http.StatusBadRequest, "stream must be stdout or stderr")
		return
	}
	offset, _ := strconv.ParseInt(c.DefaultQuery("offset", "0"), 10, 64)
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "32768"))
	if limit <= 0 || limit > 1<<20 {
		limit = 32768
	}
	data, eof, err := h.store.Read(target.ParentTaskID, target.ID, stream, offset, limit)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	// Read the sequence after the file to give the client a resume cursor that
	// covers everything just returned.
	var seqs []int64
	h.db.Model(&models.ExecutionTaskTarget{}).Where("id = ?", target.ID).Pluck("seq", &seqs)
	var seq int64
	if len(seqs) > 0 {
		seq = seqs[0]
	}
	ok(c, gin.H{
		"stream": stream, "offset": offset, "next_offset": offset + int64(len(data)),
		"data": base64.StdEncoding.EncodeToString(data), "eof": eof, "seq": seq,
	})
}

// DownloadTargetLogs streams a persisted log file for download.
func (h *Handlers) DownloadTargetLogs(c *gin.Context) {
	var target models.ExecutionTaskTarget
	if err := h.db.First(&target, "id = ?", c.Param("id")).Error; err != nil {
		fail(c, http.StatusNotFound, "target not found")
		return
	}
	stream := c.DefaultQuery("stream", "stdout")
	if stream != "stdout" && stream != "stderr" {
		fail(c, http.StatusBadRequest, "stream must be stdout or stderr")
		return
	}
	path, err := h.store.DownloadPath(target.ParentTaskID, target.ID, stream)
	if err != nil {
		fail(c, http.StatusNotFound, "log not available")
		return
	}
	name := fmt.Sprintf("%s-%s-%s.log", sanitize(target.ServerName), target.ID[:8], stream)
	c.FileAttachment(path, name)
}

func (h *Handlers) ListAudit(c *gin.Context) {
	page, size := pageParams(c)
	q := h.db.Model(&models.AuditEvent{})
	if v := c.Query("action"); v != "" {
		q = q.Where("action = ?", v)
	}
	if v := c.Query("resource"); v != "" {
		q = q.Where("resource = ?", v)
	}
	var total int64
	q.Count(&total)
	var items []models.AuditEvent
	if err := q.Order("id desc").Offset((page - 1) * size).Limit(size).Find(&items).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, gin.H{"items": items, "total": total, "page": page, "page_size": size})
}

// ---- helpers ----

func isTerminal(status string) bool {
	switch status {
	case models.StatusSuccess, models.StatusFailed, models.StatusTimeout,
		models.StatusCancelled, models.StatusUnknown:
		return true
	}
	return false
}

func uniqueIDs(ids []int64) []int64 {
	seen := map[int64]bool{}
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func sanitize(s string) string {
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.ReplaceAll(s, "\\", "_")
	s = strings.ReplaceAll(s, " ", "_")
	if s == "" {
		return "server"
	}
	return s
}
