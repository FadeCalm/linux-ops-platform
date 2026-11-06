package api

import (
	"net/http"
	"strconv"
	"strings"

	"linux-ops-platform/backend/internal/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type serverRequest struct {
	Name      string   `json:"name"`
	IP        string   `json:"ip"`
	AgentPort int      `json:"agent_port"`
	GroupID   *int64   `json:"group_id"`
	Tags      []string `json:"tags"`
}

func (h *Handlers) ListServers(c *gin.Context) {
	page, size := pageParams(c)
	q := h.db.Model(&models.Server{}).Preload("Tags")

	if v := strings.TrimSpace(c.Query("q")); v != "" {
		like := "%" + v + "%"
		q = q.Where("servers.name LIKE ? OR servers.ip LIKE ? OR servers.hostname LIKE ?", like, like, like)
	}
	if v := c.Query("status"); v != "" {
		q = q.Where("servers.status = ?", v)
	}
	if v := c.Query("group_id"); v != "" {
		q = q.Where("servers.group_id = ?", v)
	}
	if v := c.Query("tag"); v != "" {
		q = q.Joins("JOIN servers_tags st ON st.server_id = servers.id").
			Joins("JOIN server_tags t ON t.id = st.server_tag_id").
			Where("t.name = ?", v)
	}

	var total int64
	q.Count(&total)
	var items []models.Server
	if err := q.Order("servers.id desc").Offset((page - 1) * size).Limit(size).Find(&items).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, gin.H{"items": items, "total": total, "page": page, "page_size": size})
}

func (h *Handlers) GetServer(c *gin.Context) {
	id, okID := parseID(c)
	if !okID {
		return
	}
	var srv models.Server
	if err := h.db.Preload("Tags").First(&srv, id).Error; err != nil {
		fail(c, http.StatusNotFound, "server not found")
		return
	}
	ok(c, srv)
}

func (h *Handlers) CreateServer(c *gin.Context) {
	var req serverRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	if strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.IP) == "" {
		fail(c, http.StatusBadRequest, "name and ip are required")
		return
	}
	if req.AgentPort == 0 {
		req.AgentPort = 19090
	}
	if req.AgentPort < 1 || req.AgentPort > 65535 {
		fail(c, http.StatusBadRequest, "invalid agent_port")
		return
	}
	srv := models.Server{
		Name: req.Name, IP: req.IP, AgentPort: req.AgentPort,
		GroupID: req.GroupID, Status: models.StatusUnknownAgent,
	}
	tags, err := h.resolveTags(c, req.Tags)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	if err := h.db.Create(&srv).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	if len(tags) > 0 {
		h.db.Model(&srv).Association("Tags").Replace(tags)
	}
	h.audit("server.create", "server", strconv.FormatInt(srv.ID, 10), req.Name+" "+req.IP)
	h.db.Preload("Tags").First(&srv, srv.ID)
	ok(c, srv)
}

func (h *Handlers) UpdateServer(c *gin.Context) {
	id, okID := parseID(c)
	if !okID {
		return
	}
	var srv models.Server
	if err := h.db.First(&srv, id).Error; err != nil {
		fail(c, http.StatusNotFound, "server not found")
		return
	}
	var req serverRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	if strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.IP) == "" {
		fail(c, http.StatusBadRequest, "name and ip are required")
		return
	}
	if req.AgentPort == 0 {
		req.AgentPort = srv.AgentPort
	}
	if req.AgentPort < 1 || req.AgentPort > 65535 {
		fail(c, http.StatusBadRequest, "invalid agent_port")
		return
	}
	updates := map[string]interface{}{
		"name": req.Name, "ip": req.IP, "agent_port": req.AgentPort, "group_id": req.GroupID,
	}
	if err := h.db.Model(&srv).Updates(updates).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	tags, err := h.resolveTags(c, req.Tags)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	h.db.Model(&srv).Association("Tags").Replace(tags)
	h.audit("server.update", "server", strconv.FormatInt(id, 10), req.Name+" "+req.IP)
	h.db.Preload("Tags").First(&srv, id)
	ok(c, srv)
}

func (h *Handlers) DeleteServer(c *gin.Context) {
	id, okID := parseID(c)
	if !okID {
		return
	}
	var active int64
	h.db.Model(&models.ExecutionTaskTarget{}).
		Where("server_id = ? AND status IN ?", id,
			[]string{models.StatusPending, models.StatusDispatching, models.StatusRunning}).Count(&active)
	if active > 0 {
		fail(c, http.StatusConflict, "server has active tasks; cancel them first")
		return
	}
	if err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("server_id = ?", id).Delete(&models.Agent{}).Error; err != nil {
			return err
		}
		var srv models.Server
		if err := tx.First(&srv, id).Error; err != nil {
			return err
		}
		if err := tx.Model(&srv).Association("Tags").Clear(); err != nil {
			return err
		}
		return tx.Delete(&models.Server{}, id).Error
	}); err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	h.audit("server.delete", "server", strconv.FormatInt(id, 10), "")
	ok(c, gin.H{"deleted": true})
}

func (h *Handlers) CheckServer(c *gin.Context) {
	id, okID := parseID(c)
	if !okID {
		return
	}
	var srv models.Server
	if err := h.db.First(&srv, id).Error; err != nil {
		fail(c, http.StatusNotFound, "server not found")
		return
	}
	info, err := h.sched.CheckServer(c.Request.Context(), &srv)
	if err != nil {
		ok(c, gin.H{"online": false, "error": err.Error()})
		return
	}
	ok(c, gin.H{"online": true, "info": info})
}

func (h *Handlers) GetAgent(c *gin.Context) {
	id, okID := parseID(c)
	if !okID {
		return
	}
	var ag models.Agent
	if err := h.db.Where("server_id = ?", id).First(&ag).Error; err != nil {
		fail(c, http.StatusNotFound, "agent info not available yet")
		return
	}
	ok(c, ag)
}

// resolveTags finds existing tags by name or creates them.
func (h *Handlers) resolveTags(c *gin.Context, names []string) ([]models.ServerTag, error) {
	var tags []models.ServerTag
	seen := map[string]bool{}
	for _, raw := range names {
		name := strings.TrimSpace(raw)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		var tag models.ServerTag
		err := h.db.Where("name = ?", name).First(&tag).Error
		if err == gorm.ErrRecordNotFound {
			tag = models.ServerTag{Name: name}
			if err := h.db.Create(&tag).Error; err != nil {
				return nil, err
			}
		} else if err != nil {
			return nil, err
		}
		tags = append(tags, tag)
	}
	return tags, nil
}
