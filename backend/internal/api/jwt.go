package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// Minimal HS256 JWT implementation (RFC 7519 subset) kept in-tree so the
// backend does not need an extra dependency. Only the fields this service
// needs are modelled; anything else is ignored.
//
// JWTs are short-lived bearer credentials for API clients. They are not used
// by the browser (which keeps using the HttpOnly session cookie) and carry no
// refresh token. The "pv" claim is derived from the password hash, so a
// password change or admin reset invalidates every previously issued JWT.

const jwtIssuer = "linux-ops-platform"

var errInvalidJWT = errors.New("invalid token")

type jwtHeader struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
}

type jwtClaims struct {
	Subject         string `json:"sub"`
	Username        string `json:"usr"`
	Role            string `json:"role"`
	PasswordVersion string `json:"pv"`
	Issuer          string `json:"iss"`
	IssuedAt        int64  `json:"iat"`
	ExpiresAt       int64  `json:"exp"`
}

func signJWT(secret []byte, claims jwtClaims) (string, error) {
	if len(secret) == 0 {
		return "", errors.New("jwt signing key is not configured")
	}
	head, err := json.Marshal(jwtHeader{Alg: "HS256", Typ: "JWT"})
	if err != nil {
		return "", err
	}
	body, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	input := base64.RawURLEncoding.EncodeToString(head) + "." +
		base64.RawURLEncoding.EncodeToString(body)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(input))
	return input + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func parseJWT(secret []byte, raw string) (*jwtClaims, error) {
	if len(secret) == 0 {
		return nil, errInvalidJWT
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return nil, errInvalidJWT
	}
	headJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, errInvalidJWT
	}
	var head jwtHeader
	if err := json.Unmarshal(headJSON, &head); err != nil || head.Alg != "HS256" {
		return nil, errInvalidJWT
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, errInvalidJWT
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(parts[0] + "." + parts[1]))
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return nil, errInvalidJWT
	}
	bodyJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, errInvalidJWT
	}
	var claims jwtClaims
	if err := json.Unmarshal(bodyJSON, &claims); err != nil {
		return nil, errInvalidJWT
	}
	now := time.Now().Unix()
	if claims.Issuer != jwtIssuer || claims.Subject == "" ||
		claims.ExpiresAt <= now || claims.IssuedAt > now+300 {
		return nil, errInvalidJWT
	}
	return &claims, nil
}

// passwordVersion is a non-reversible fingerprint of the stored bcrypt hash.
// It changes whenever the password changes without exposing the hash itself.
func passwordVersion(hash string) string {
	sum := sha256.Sum256([]byte(hash))
	return hex.EncodeToString(sum[:])[:16]
}
