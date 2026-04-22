package api

import (
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const (
	anonUserCookieName = "sa_uid"
	sessionCookieName  = "sa_sid"
)

func ensureAnonUserIDCookie(c *gin.Context) string {
	if c == nil {
		return ""
	}
	if v, err := c.Cookie(anonUserCookieName); err == nil && strings.TrimSpace(v) != "" {
		return v
	}
	id := uuid.NewString()
	setHTTPOnlyCookie(c, anonUserCookieName, id, cookieMaxAgeSeconds())
	return id
}

func ensureSessionIDCookie(c *gin.Context) string {
	if c == nil {
		return ""
	}
	if v, err := c.Cookie(sessionCookieName); err == nil && strings.TrimSpace(v) != "" {
		return v
	}
	id := uuid.NewString()
	setHTTPOnlyCookie(c, sessionCookieName, id, cookieMaxAgeSeconds())
	return id
}

func cookieMaxAgeSeconds() int {
	const defaultMaxAge = int((7 * 24 * time.Hour) / time.Second)
	if v := strings.TrimSpace(os.Getenv("INTENT_SESSION_COOKIE_MAX_AGE_SECONDS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return defaultMaxAge
}

func setHTTPOnlyCookie(c *gin.Context, name, value string, maxAgeSeconds int) {
	if c == nil {
		return
	}
	// 本地开发默认 Lax，避免跨域 Cookie 场景下被浏览器直接丢弃。
	c.SetSameSite(parseSameSite(os.Getenv("INTENT_COOKIE_SAMESITE")))
	c.SetCookie(name, value, maxAgeSeconds, "/", cookieDomain(), cookieSecure(c), true)
}

func cookieDomain() string {
	return strings.TrimSpace(os.Getenv("INTENT_COOKIE_DOMAIN"))
}

func cookieSecure(c *gin.Context) bool {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("INTENT_COOKIE_SECURE")), "true") {
		return true
	}
	return c != nil && c.Request != nil && c.Request.TLS != nil
}

func parseSameSite(raw string) http.SameSite {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "none":
		return http.SameSiteNoneMode
	case "strict":
		return http.SameSiteStrictMode
	default:
		return http.SameSiteLaxMode
	}
}
