package api

import (
	"net/http"
	"strconv"
	"strings"

	"linux-ops-platform/backend/internal/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type groupRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func (h *Handlers) ListGroups(c *gin.Context) {
	var items []models.ServerGroup
	if err := h.db.Order("id asc").Find(&items).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, gin.H{"items": items})
}

func (h *Handlers) CreateGroup(c *gin.Context) {
	var req groupRequest
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Name) == "" {
		fail(c, http.StatusBadRequest, "name is required")
		return
	}
	g := models.ServerGroup{Name: strings.TrimSpace(req.Name), Description: req.Description}
	if err := h.db.Create(&g).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	h.audit("group.create", "group", strconv.FormatInt(g.ID, 10), g.Name)
	ok(c, g)
}

func (h *Handlers) UpdateGroup(c *gin.Context) {
	id, okID := parseID(c)
	if !okID {
		return
	}
	var req groupRequest
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Name) == "" {
		fail(c, http.StatusBadRequest, "name is required")
		return
	}
	res := h.db.Model(&models.ServerGroup{}).Where("id = ?", id).
		Updates(map[string]interface{}{"name": req.Name, "description": req.Description})
	if res.Error != nil {
		fail(c, http.StatusInternalServerError, res.Error.Error())
		return
	}
	if res.RowsAffected == 0 {
		fail(c, http.StatusNotFound, "group not found")
		return
	}
	h.audit("group.update", "group", strconv.FormatInt(id, 10), req.Name)
	ok(c, gin.H{"updated": true})
}

func (h *Handlers) DeleteGroup(c *gin.Context) {
	id, okID := parseID(c)
	if !okID {
		return
	}
	if err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.Server{}).Where("group_id = ?", id).Update("group_id", nil).Error; err != nil {
			return err
		}
		return tx.Delete(&models.ServerGroup{}, id).Error
	}); err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	h.audit("group.delete", "group", strconv.FormatInt(id, 10), "")
	ok(c, gin.H{"deleted": true})
}
