package api

import (
	"net/http"

	"linux-ops-platform/backend/internal/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Access model: the admin sees and manages every server and task. A normal
// user only sees the servers they created (owner_id) and tasks they started.
// Ownership is enforced server-side on every server/task/target route, not
// just in the list queries, so guessing an ID cannot cross the boundary.

func (h *Handlers) isAdmin(u *models.User) bool {
	return u != nil && u.Role == models.RoleAdmin
}

func (h *Handlers) canAccessServer(u *models.User, srv *models.Server) bool {
	if u == nil || srv == nil {
		return false
	}
	return h.isAdmin(u) || srv.OwnerID == u.ID
}

// scopedServerQuery starts a server query visible to u.
func (h *Handlers) scopedServerQuery(u *models.User) *gorm.DB {
	q := h.db.Model(&models.Server{})
	if !h.isAdmin(u) {
		q = q.Where("servers.owner_id = ?", u.ID)
	}
	return q
}

func (h *Handlers) loadServerForUser(c *gin.Context, id int64) (*models.Server, bool) {
	var srv models.Server
	if err := h.db.First(&srv, id).Error; err != nil {
		fail(c, http.StatusNotFound, "server not found")
		return nil, false
	}
	if !h.canAccessServer(currentUser(c), &srv) {
		fail(c, http.StatusForbidden, "无权访问该服务器")
		return nil, false
	}
	return &srv, true
}

func (h *Handlers) canAccessTask(u *models.User, task *models.ExecutionTask) bool {
	if u == nil || task == nil {
		return false
	}
	return h.isAdmin(u) || task.OwnerID == u.ID
}

func (h *Handlers) loadTaskForUser(c *gin.Context, taskID string) (*models.ExecutionTask, bool) {
	var task models.ExecutionTask
	if err := h.db.First(&task, "id = ?", taskID).Error; err != nil {
		fail(c, http.StatusNotFound, "task not found")
		return nil, false
	}
	if !h.canAccessTask(currentUser(c), &task) {
		fail(c, http.StatusForbidden, "无权访问该任务")
		return nil, false
	}
	return &task, true
}

// loadTargetForUser loads a child target and its parent task, enforcing task
// ownership. Handler code should use the returned parent for further checks.
func (h *Handlers) loadTargetForUser(c *gin.Context, targetID string) (*models.ExecutionTaskTarget, *models.ExecutionTask, bool) {
	var target models.ExecutionTaskTarget
	if err := h.db.First(&target, "id = ?", targetID).Error; err != nil {
		fail(c, http.StatusNotFound, "target not found")
		return nil, nil, false
	}
	var parent models.ExecutionTask
	if err := h.db.First(&parent, "id = ?", target.ParentTaskID).Error; err != nil {
		fail(c, http.StatusNotFound, "parent task not found")
		return nil, nil, false
	}
	if !h.canAccessTask(currentUser(c), &parent) {
		fail(c, http.StatusForbidden, "无权访问该任务")
		return nil, nil, false
	}
	return &target, &parent, true
}
