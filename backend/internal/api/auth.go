package api

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// sessionCookie holds a stateless, HMAC-signed expiry token. The signing key is
// derived from the access password, so changing the password invalidates every
// existing session and no server-side session store is needed.
const sessionCookie = "lop_session"

type authGate struct {
	password string
	key      []byte
	ttl      time.Duration
}

func newAuthGate(password string, ttl time.Duration) *authGate {
	if ttl <= 0 {
		ttl = 12 * time.Hour
	}
	sum := sha256.Sum256([]byte("linux-ops-session-v1:" + password))
	return &authGate{password: password, key: sum[:], ttl: ttl}
}

func (a *authGate) enabled() bool { return a.password != "" }

func (a *authGate) token() string {
	exp := time.Now().Add(a.ttl).Unix()
	nonce := make([]byte, 12)
	if _, err := rand.Read(nonce); err != nil {
		// crypto/rand failure is unrecoverable for session security.
		panic("auth: cannot read random bytes: " + err.Error())
	}
	payload := fmt.Sprintf("%d.%s", exp, base64.RawURLEncoding.EncodeToString(nonce))
	mac := hmac.New(sha256.New, a.key)
	mac.Write([]byte(payload))
	return payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (a *authGate) valid(token string) bool {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return false
	}
	payload := parts[0] + "." + parts[1]
	mac := hmac.New(sha256.New, a.key)
	mac.Write([]byte(payload))
	want := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(want), []byte(parts[2])) != 1 {
		return false
	}
	exp, err := strconv.ParseInt(parts[0], 10, 64)
	return err == nil && time.Now().Unix() < exp
}

// loginLimiter throttles password guessing per client IP. It is intentionally
// in-memory: the gate protects a single shared password, not per-user accounts.
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

// AuthStatus is public: it tells the SPA whether a password is required and
// whether the current cookie is valid.
func (h *Handlers) AuthStatus(c *gin.Context) {
	authed := !h.auth.enabled()
	if !authed {
		if tok, err := c.Cookie(sessionCookie); err == nil {
			authed = h.auth.valid(tok)
		}
	}
	ok(c, gin.H{"required": h.auth.enabled(), "authenticated": authed})
}

type loginRequest struct {
	Password string `json:"password"`
}

// AuthLogin validates the shared access password and issues a session cookie.
func (h *Handlers) AuthLogin(c *gin.Context) {
	if !h.auth.enabled() {
		ok(c, gin.H{"required": false})
		return
	}
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
	if subtle.ConstantTimeCompare([]byte(req.Password), []byte(h.auth.password)) != 1 {
		h.loginLimiter.fail(ip)
		h.audit("auth.login.failed", "auth", "", "ip="+ip)
		// Small delay makes offline guessing against a short password slower.
		time.Sleep(300 * time.Millisecond)
		fail(c, http.StatusUnauthorized, "密码错误")
		return
	}
	h.loginLimiter.reset(ip)
	h.audit("auth.login", "auth", "", "ip="+ip)
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(sessionCookie, h.auth.token(), int(h.auth.ttl.Seconds()), "/", "", false, true)
	ok(c, gin.H{
		"required":   true,
		"expires_at": time.Now().Add(h.auth.ttl).UTC().Format(time.RFC3339),
	})
}

// AuthLogout clears the session cookie.
func (h *Handlers) AuthLogout(c *gin.Context) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(sessionCookie, "", -1, "/", "", false, true)
	if h.auth.enabled() {
		h.audit("auth.logout", "auth", "", "ip="+c.ClientIP())
	}
	ok(c, gin.H{"ok": true})
}

// requireAuth rejects requests without a valid session cookie. It covers REST
// and the WebSocket upgrade handshakes alike, since browsers attach cookies to
// same-origin ws:// requests.
func (h *Handlers) requireAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !h.auth.enabled() {
			c.Next()
			return
		}
		if tok, err := c.Cookie(sessionCookie); err == nil && h.auth.valid(tok) {
			c.Next()
			return
		}
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 401, "message": "请先登录"})
	}
}
