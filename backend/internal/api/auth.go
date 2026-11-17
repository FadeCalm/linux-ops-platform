package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"linux-ops-platform/backend/internal/models"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

// sessionCookie holds an opaque random token. The database stores only its
// SHA-256, so sessions can be revoked and a leaked database does not yield
// usable cookies.
const sessionCookie = "lop_session"

const ctxUserKey = "lops.user"

type authGate struct {
	ttl time.Duration
}

func newAuthGate(ttl time.Duration) *authGate {
	if ttl <= 0 {
		ttl = 12 * time.Hour
	}
	return &authGate{ttl: ttl}
}

// ---- password + token primitives ----

func hashPassword(password string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func checkPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func newSessionToken() (token, hash string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", "", err
	}
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, tokenHash(token), nil
}

// ---- session lookup ----

func (h *Handlers) sessionUser(c *gin.Context) (*models.User, *models.UserSession) {
	tok, err := c.Cookie(sessionCookie)
	if err != nil || strings.TrimSpace(tok) == "" {
		return nil, nil
	}
	var sess models.UserSession
	if err := h.db.Where("token_hash = ? AND expires_at > ?", tokenHash(tok), time.Now().UTC()).
		First(&sess).Error; err != nil {
		return nil, nil
	}
	var user models.User
	if err := h.db.First(&user, sess.UserID).Error; err != nil {
		return nil, nil
	}
	return &user, &sess
}

// ---- bearer credentials (JWT / API token) ----

const (
	apiTokenPrefix = "lop_"
	bearerPrefix   = "bearer "
)

func bearerToken(c *gin.Context) string {
	hdr := strings.TrimSpace(c.GetHeader("Authorization"))
	if len(hdr) <= len(bearerPrefix) || !strings.EqualFold(hdr[:len(bearerPrefix)], bearerPrefix) {
		return ""
	}
	return strings.TrimSpace(hdr[len(bearerPrefix):])
}

// requestUser resolves the caller from an Authorization: Bearer credential
// first (a JWT or an API token), then from the browser session cookie.
func (h *Handlers) requestUser(c *gin.Context) *models.User {
	if tok := bearerToken(c); tok != "" {
		return h.bearerUser(tok)
	}
	user, _ := h.sessionUser(c)
	return user
}

func (h *Handlers) bearerUser(raw string) *models.User {
	if strings.HasPrefix(raw, apiTokenPrefix) {
		return h.apiTokenUser(raw)
	}
	claims, err := parseJWT(h.jwtSecret, raw)
	if err != nil {
		return nil
	}
	id, err := strconv.ParseInt(claims.Subject, 10, 64)
	if err != nil || id <= 0 {
		return nil
	}
	var user models.User
	if err := h.db.First(&user, id).Error; err != nil {
		return nil
	}
	// A password change rotates the fingerprint and silently invalidates old
	// JWTs instead of letting them live until expiry.
	if claims.PasswordVersion != passwordVersion(user.PasswordHash) {
		return nil
	}
	return &user
}

func currentUser(c *gin.Context) *models.User {
	v, exists := c.Get(ctxUserKey)
	if !exists {
		return nil
	}
	u, _ := v.(*models.User)
	return u
}

func publicUser(u *models.User) gin.H {
	return gin.H{"id": u.ID, "username": u.Username, "role": u.Role}
}

func (h *Handlers) setSessionCookie(c *gin.Context, token string) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(sessionCookie, token, int(h.auth.ttl.Seconds()), "/", "", false, true)
}

// ---- middleware ----

// requireAuth accepts either an Authorization: Bearer credential (JWT or API
// token) or the browser session cookie, and rejects unauthenticated requests.
// It also covers WebSocket upgrade handshakes (browsers send the cookie there).
func (h *Handlers) requireAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		user := h.requestUser(c)
		if user == nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 401, "message": "未认证：请先登录或提供有效令牌"})
			return
		}
		c.Set(ctxUserKey, user)
		c.Next()
	}
}

// requireAdmin must be registered after requireAuth.
func (h *Handlers) requireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		user := currentUser(c)
		if user == nil || user.Role != models.RoleAdmin {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 403, "message": "需要管理员权限"})
			return
		}
		c.Next()
	}
}

// ---- handlers ----

// AuthStatus tells the caller whether the current cookie/bearer credential is
// valid and who it belongs to.
func (h *Handlers) AuthStatus(c *gin.Context) {
	user := h.requestUser(c)
	if user == nil {
		ok(c, gin.H{"required": true, "authenticated": false})
		return
	}
	ok(c, gin.H{"required": true, "authenticated": true, "user": publicUser(user)})
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// AuthLogin validates a username/password pair and creates a session.
func (h *Handlers) AuthLogin(c *gin.Context) {
	ip := c.ClientIP()
	if !h.loginLimiter.allow(ip) {
		fail(c, http.StatusTooManyRequests, "尝试次数过多，请稍后再试")
		return
	}
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	username := strings.TrimSpace(req.Username)
	if username == "" || req.Password == "" {
		fail(c, http.StatusBadRequest, "请输入用户名和密码")
		return
	}
	var user models.User
	err := h.db.Where("username = ?", username).First(&user).Error
	if err != nil || !checkPassword(user.PasswordHash, req.Password) {
		h.loginLimiter.fail(ip)
		h.auditAs("anonymous", "auth.login.failed", "auth", "",
			"username="+truncate(username, 64)+" ip="+ip)
		// Slow down online guessing without revealing which field was wrong.
		time.Sleep(300 * time.Millisecond)
		fail(c, http.StatusUnauthorized, "用户名或密码错误")
		return
	}
	h.loginLimiter.reset(ip)

	token, hash, err := newSessionToken()
	if err != nil {
		fail(c, http.StatusInternalServerError, "cannot create session")
		return
	}
	now := time.Now().UTC()
	h.db.Where("expires_at < ?", now).Delete(&models.UserSession{})
	sess := models.UserSession{TokenHash: hash, UserID: user.ID, ExpiresAt: now.Add(h.auth.ttl), CreatedAt: now}
	if err := h.db.Create(&sess).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	h.setSessionCookie(c, token)
	h.auditAs(user.Username, "auth.login", "auth", strconv.FormatInt(user.ID, 10), "ip="+ip)
	ok(c, gin.H{
		"required":   true,
		"user":       publicUser(&user),
		"expires_at": sess.ExpiresAt.Format(time.RFC3339),
	})
}

// AuthIssueToken exchanges a username/password pair for a short-lived JWT that
// API clients send as "Authorization: Bearer <jwt>". It shares the login
// limiter and failure delay with the form-login endpoint.
func (h *Handlers) AuthIssueToken(c *gin.Context) {
	ip := c.ClientIP()
	if !h.loginLimiter.allow(ip) {
		fail(c, http.StatusTooManyRequests, "尝试次数过多，请稍后再试")
		return
	}
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	username := strings.TrimSpace(req.Username)
	if username == "" || req.Password == "" {
		fail(c, http.StatusBadRequest, "请输入用户名和密码")
		return
	}
	var user models.User
	err := h.db.Where("username = ?", username).First(&user).Error
	if err != nil || !checkPassword(user.PasswordHash, req.Password) {
		h.loginLimiter.fail(ip)
		h.auditAs("anonymous", "auth.token.failed", "auth", "",
			"username="+truncate(username, 64)+" ip="+ip)
		time.Sleep(300 * time.Millisecond)
		fail(c, http.StatusUnauthorized, "用户名或密码错误")
		return
	}
	h.loginLimiter.reset(ip)

	ttl := time.Duration(h.cfg.Auth.JWTMinutes) * time.Minute
	now := time.Now().UTC()
	token, err := signJWT(h.jwtSecret, jwtClaims{
		Subject:         strconv.FormatInt(user.ID, 10),
		Username:        user.Username,
		Role:            user.Role,
		PasswordVersion: passwordVersion(user.PasswordHash),
		Issuer:          jwtIssuer,
		IssuedAt:        now.Unix(),
		ExpiresAt:       now.Add(ttl).Unix(),
	})
	if err != nil {
		fail(c, http.StatusServiceUnavailable, "JWT 签发不可用")
		return
	}
	h.auditAs(user.Username, "auth.token.issue", "user", strconv.FormatInt(user.ID, 10), "ip="+ip)
	ok(c, gin.H{
		"access_token": token,
		"token_type":   "Bearer",
		"expires_in":   int(ttl.Seconds()),
		"expires_at":   now.Add(ttl).Format(time.RFC3339),
		"user":         publicUser(&user),
	})
}

// AuthLogout revokes the current session and clears the cookie.
func (h *Handlers) AuthLogout(c *gin.Context) {
	user, sess := h.sessionUser(c)
	if sess != nil {
		h.db.Delete(&models.UserSession{}, sess.ID)
	}
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(sessionCookie, "", -1, "/", "", false, true)
	if user != nil {
		h.auditAs(user.Username, "auth.logout", "auth", strconv.FormatInt(user.ID, 10), "")
	}
	ok(c, gin.H{"ok": true})
}

type changePasswordRequest struct {
	OldPassword string `json:"old_password"`
	NewPassword string `json:"new_password"`
}

// AuthChangePassword lets the logged-in user change their own password.
func (h *Handlers) AuthChangePassword(c *gin.Context) {
	user := currentUser(c)
	var req changePasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	if !checkPassword(user.PasswordHash, req.OldPassword) {
		fail(c, http.StatusBadRequest, "原密码错误")
		return
	}
	msg := validatePassword(req.NewPassword)
	if msg != "" {
		fail(c, http.StatusBadRequest, msg)
		return
	}
	hash, err := hashPassword(req.NewPassword)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	if err := h.db.Model(&models.User{}).Where("id = ?", user.ID).
		Update("password_hash", hash).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	// Revoke every old session, then issue a fresh one for this browser. Old
	// JWTs die automatically because the "pv" claim no longer matches; API
	// tokens are intentionally left alone (separate credentials).
	h.db.Where("user_id = ?", user.ID).Delete(&models.UserSession{})
	token, th, err := newSessionToken()
	if err == nil {
		now := time.Now().UTC()
		h.db.Create(&models.UserSession{
			TokenHash: th, UserID: user.ID, ExpiresAt: now.Add(h.auth.ttl), CreatedAt: now,
		})
		h.setSessionCookie(c, token)
	}
	h.auditAs(user.Username, "auth.password.change", "user", strconv.FormatInt(user.ID, 10), "")
	ok(c, gin.H{"changed": true})
}

// validatePassword returns an error message, or "" when acceptable.
func validatePassword(pw string) string {
	if len(pw) < 6 {
		return "新密码至少 6 位"
	}
	if len(pw) > 128 {
		return "新密码过长"
	}
	return ""
}

// ---- login throttling ----

// loginLimiter throttles password guessing per client IP. Failures are keyed
// by IP only so a lockout cannot be used to enumerate usernames.
type loginLimiter struct {
	mu       sync.Mutex
	failures map[string][]time.Time
	max      int
	window   time.Duration
}

func newLoginLimiter(max int, window time.Duration) *loginLimiter {
	return &loginLimiter{failures: make(map[string][]time.Time), max: max, window: window}
}

func (l *loginLimiter) recent(ip string) []time.Time {
	cutoff := time.Now().Add(-l.window)
	out := l.failures[ip][:0]
	for _, t := range l.failures[ip] {
		if t.After(cutoff) {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		delete(l.failures, ip)
	} else {
		l.failures[ip] = out
	}
	return out
}

func (l *loginLimiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.recent(ip)) < l.max
}

func (l *loginLimiter) fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.failures[ip] = append(l.recent(ip), time.Now())
}

func (l *loginLimiter) reset(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.failures, ip)
}
