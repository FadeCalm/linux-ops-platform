package api

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"linux-ops-platform/backend/internal/models"

	"github.com/gin-gonic/gin"
)

var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{3,32}$`)

type createUserRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// ListUsers is admin-only and lists every console account plus how many
// servers each one owns.
func (h *Handlers) ListUsers(c *gin.Context) {
	var items []models.User
	err := h.db.Model(&models.User{}).
		Select("users.*, (SELECT COUNT(*) FROM servers WHERE servers.owner_id = users.id) AS server_count").
		Order("users.id asc").Find(&items).Error
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, gin.H{"items": items})
}

// CreateUser adds a sub-user. Only the admin can create accounts, and new
// accounts always get the "user" role.
func (h *Handlers) CreateUser(c *gin.Context) {
	var req createUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	username := strings.TrimSpace(req.Username)
	if !usernamePattern.MatchString(username) {
		fail(c, http.StatusBadRequest, "用户名需为 3-32 位字母、数字、下划线、点或短横线")
		return
	}
	if msg := validatePassword(req.Password); msg != "" {
		fail(c, http.StatusBadRequest, msg)
		return
	}
	var count int64
	h.db.Model(&models.User{}).Where("username = ?", username).Count(&count)
	if count > 0 {
		fail(c, http.StatusConflict, "用户名已存在")
		return
	}
	hash, err := hashPassword(req.Password)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	u := models.User{
		Username: username, PasswordHash: hash, Role: models.RoleUser,
		CreatedBy: currentUser(c).Username,
	}
	if err := h.db.Create(&u).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	h.audit(c, "user.create", "user", strconv.FormatInt(u.ID, 10), username)
	ok(c, u)
}

type resetPasswordRequest struct {
	Password string `json:"password"`
}

// ResetUserPassword lets the admin set a sub-user's password. Resetting a
// password revokes that user's sessions and long-lived API tokens (the account
// may be compromised); the admin must use change-password for their own
// account.
func (h *Handlers) ResetUserPassword(c *gin.Context) {
	id, okID := parseID(c)
	if !okID {
		return
	}
	me := currentUser(c)
	if me.ID == id {
		fail(c, http.StatusBadRequest, "请使用「修改密码」修改自己的密码")
		return
	}
	var req resetPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	if msg := validatePassword(req.Password); msg != "" {
		fail(c, http.StatusBadRequest, msg)
		return
	}
	var u models.User
	if err := h.db.First(&u, id).Error; err != nil {
		fail(c, http.StatusNotFound, "user not found")
		return
	}
	hash, err := hashPassword(req.Password)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	if err := h.db.Model(&u).Update("password_hash", hash).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	h.db.Where("user_id = ?", u.ID).Delete(&models.UserSession{})
	// A reset usually means the account may have been compromised, so long-lived
	// API tokens are revoked as well.
	h.db.Where("user_id = ?", u.ID).Delete(&models.APIToken{})
	h.audit(c, "user.password.reset", "user", strconv.FormatInt(u.ID, 10), u.Username)
	ok(c, gin.H{"reset": true})
}

// DeleteUser removes a sub-user. The admin cannot delete themselves or an
// account that still owns servers (delete those servers first), so ownership
// never silently transfers.
func (h *Handlers) DeleteUser(c *gin.Context) {
	id, okID := parseID(c)
	if !okID {
		return
	}
	me := currentUser(c)
	if me.ID == id {
		fail(c, http.StatusBadRequest, "不能删除当前登录账号")
		return
	}
	var u models.User
	if err := h.db.First(&u, id).Error; err != nil {
		fail(c, http.StatusNotFound, "user not found")
		return
	}
	if u.Role == models.RoleAdmin {
		fail(c, http.StatusBadRequest, "不能删除管理员账号")
		return
	}
	var owned int64
	h.db.Model(&models.Server{}).Where("owner_id = ?", u.ID).Count(&owned)
	if owned > 0 {
		fail(c, http.StatusConflict, "该用户仍有 "+strconv.FormatInt(owned, 10)+" 台服务器，请先删除这些服务器")
		return
	}
	h.db.Where("user_id = ?", u.ID).Delete(&models.UserSession{})
	h.db.Where("user_id = ?", u.ID).Delete(&models.APIToken{})
	if err := h.db.Delete(&models.User{}, u.ID).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	h.audit(c, "user.delete", "user", strconv.FormatInt(u.ID, 10), u.Username)
	ok(c, gin.H{"deleted": true})
}
