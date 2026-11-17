package api

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"
	"time"

	"linux-ops-platform/backend/internal/models"

	"github.com/gin-gonic/gin"
)

const (
	apiTokenBytes = 32
	// maxTokensPerUser keeps accidental token sprawl (and the blast radius of a
	// leaked account) bounded.
	maxTokensPerUser = 20
	// lastUsedUpdateInterval throttles writes caused by busy scripts.
	lastUsedUpdateInterval = 5 * time.Minute
)

// apiTokenUser resolves an API token to its owning account. The token is
// looked up by hash; expired and orphaned tokens are rejected.
func (h *Handlers) apiTokenUser(raw string) *models.User {
	var rec models.APIToken
	if err := h.db.Where("token_hash = ?", tokenHash(raw)).First(&rec).Error; err != nil {
		return nil
	}
	now := time.Now().UTC()
	if rec.ExpiresAt != nil && !rec.ExpiresAt.After(now) {
		return nil
	}
	var user models.User
	if err := h.db.First(&user, rec.UserID).Error; err != nil {
		return nil
	}
	if rec.LastUsedAt == nil || now.Sub(*rec.LastUsedAt) > lastUsedUpdateInterval {
		h.db.Model(&models.APIToken{}).Where("id = ?", rec.ID).Update("last_used_at", now)
	}
	return &user
}

// ListTokens returns the current user's API tokens. The admin may pass
// ?user_id=N to inspect another account's tokens.
func (h *Handlers) ListTokens(c *gin.Context) {
	me := currentUser(c)
	target := me.ID
	if v := strings.TrimSpace(c.Query("user_id")); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id <= 0 {
			fail(c, http.StatusBadRequest, "invalid user_id")
			return
		}
		if id != me.ID && !h.isAdmin(me) {
			fail(c, http.StatusForbidden, "无权查看其他账号的令牌")
			return
		}
		target = id
	}
	var items []models.APIToken
	if err := h.db.Where("user_id = ?", target).Order("id desc").Find(&items).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, gin.H{"items": items})
}

type createTokenRequest struct {
	Name          string `json:"name"`
	ExpiresInDays int    `json:"expires_in_days"`
}

// CreateToken mints a long-lived bearer token for the current account. The
// plaintext is returned exactly once; the database keeps only its SHA-256.
func (h *Handlers) CreateToken(c *gin.Context) {
	me := currentUser(c)
	var req createTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "未命名令牌"
	}
	if len([]rune(name)) > 64 {
		fail(c, http.StatusBadRequest, "令牌名称最长 64 个字符")
		return
	}
	if req.ExpiresInDays < 0 || req.ExpiresInDays > 3650 {
		fail(c, http.StatusBadRequest, "有效期需在 0-3650 天之间（0 表示永不过期）")
		return
	}
	var count int64
	h.db.Model(&models.APIToken{}).Where("user_id = ?", me.ID).Count(&count)
	if count >= maxTokensPerUser {
		fail(c, http.StatusBadRequest, "每个账号最多创建 "+strconv.Itoa(maxTokensPerUser)+" 个令牌，请先吊销不用的")
		return
	}

	buf := make([]byte, apiTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		fail(c, http.StatusInternalServerError, "cannot generate token")
		return
	}
	raw := apiTokenPrefix + base64.RawURLEncoding.EncodeToString(buf)
	now := time.Now().UTC()
	var expires *time.Time
	if req.ExpiresInDays > 0 {
		t := now.Add(time.Duration(req.ExpiresInDays) * 24 * time.Hour)
		expires = &t
	}
	rec := models.APIToken{
		UserID: me.ID, Username: me.Username, Name: name,
		TokenHash: tokenHash(raw), Prefix: raw[:12],
		ExpiresAt: expires, CreatedAt: now,
	}
	if err := h.db.Create(&rec).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	h.audit(c, "token.create", "api_token", strconv.FormatInt(rec.ID, 10), rec.Name)
	ok(c, gin.H{"token": raw, "item": rec})
}

// DeleteToken revokes one token. Owners can revoke their own; the admin can
// revoke anyone's.
func (h *Handlers) DeleteToken(c *gin.Context) {
	id, okID := parseID(c)
	if !okID {
		return
	}
	me := currentUser(c)
	var rec models.APIToken
	if err := h.db.First(&rec, id).Error; err != nil {
		fail(c, http.StatusNotFound, "token not found")
		return
	}
	if rec.UserID != me.ID && !h.isAdmin(me) {
		fail(c, http.StatusForbidden, "无权吊销该令牌")
		return
	}
	if err := h.db.Delete(&models.APIToken{}, rec.ID).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	h.audit(c, "token.revoke", "api_token", strconv.FormatInt(rec.ID, 10), rec.Username+"/"+rec.Name)
	ok(c, gin.H{"deleted": true})
}
