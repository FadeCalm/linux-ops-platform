package api

import (
	"net/http"
	"strconv"
	"strings"

	"linux-ops-platform/backend/internal/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func (h *Handlers) ListTags(c *gin.Context) {
	var items []models.ServerTag
	if err := h.db.Order("id asc").Find(&items).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, gin.H{"items": items})
}

func (h *Handlers) CreateTag(c *gin.Context) {
	var req struct {
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Name) == "" {
		fail(c, http.StatusBadRequest, "name is required")
		return
	}
	t := models.ServerTag{Name: strings.TrimSpace(req.Name)}
	if err := h.db.Create(&t).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	h.audit("tag.create", "tag", strconv.FormatInt(t.ID, 10), t.Name)
	ok(c, t)
}

func (h *Handlers) DeleteTag(c *gin.Context) {
	id, okID := parseID(c)
	if !okID {
		return
	}
	if err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("DELETE FROM servers_tags WHERE server_tag_id = ?", id).Error; err != nil {
			return err
		}
		return tx.Delete(&models.ServerTag{}, id).Error
	}); err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	h.audit("tag.delete", "tag", strconv.FormatInt(id, 10), "")
	ok(c, gin.H{"deleted": true})
}
