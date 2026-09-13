package api

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"meme-bot/internal/subscription"
	"meme-bot/internal/user"
)

// 上下文键名。
const (
	ctxUserID = "auth_user_id"
	ctxRole   = "auth_role"
	ctxEmail  = "auth_email"
)

// JWTAuth 校验 Authorization: Bearer <token>。
func JWTAuth(secret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw := c.GetHeader("Authorization")
		if !strings.HasPrefix(raw, "Bearer ") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "缺少 Bearer 令牌"})
			return
		}
		claims, err := user.ParseToken(secret, strings.TrimPrefix(raw, "Bearer "))
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "令牌无效或已过期"})
			return
		}
		c.Set(ctxUserID, claims.UserID)
		c.Set(ctxRole, claims.Role)
		c.Next()
	}
}

// RequireRole 角色校验。
func RequireRole(roles ...string) gin.HandlerFunc {
	allowed := make(map[string]bool, len(roles))
	for _, r := range roles {
		allowed[r] = true
	}
	return func(c *gin.Context) {
		role, _ := c.Get(ctxRole)
		roleStr, _ := role.(string)
		if !allowed[roleStr] {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "权限不足"})
			return
		}
		c.Next()
	}
}

// APIKeyAuth 校验 X-API-Key 并扣减套餐配额。
func APIKeyAuth(users *user.Service, subs *subscription.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := strings.TrimSpace(c.GetHeader("X-API-Key"))
		if key == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "缺少 X-API-Key"})
			return
		}
		u, err := users.GetByAPIKey(c.Request.Context(), key)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "API Key 无效"})
			return
		}
		if u.Status != "active" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "账号已被禁用"})
			return
		}

		remaining, err := subs.ConsumeAPICalls(c.Request.Context(), u.ID, 1)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": err.Error()})
			return
		}
		c.Header("X-RateLimit-Remaining", itoa(remaining))
		c.Set(ctxUserID, u.ID)
		c.Set(ctxRole, string(u.Role))
		c.Set(ctxEmail, u.Email)
		c.Next()
	}
}

// currentUserID 读取当前登录用户 ID。
func currentUserID(c *gin.Context) int64 {
	v, ok := c.Get(ctxUserID)
	if !ok {
		return 0
	}
	id, _ := v.(int64)
	return id
}

// currentRole 读取当前角色。
func currentRole(c *gin.Context) string {
	v, _ := c.Get(ctxRole)
	s, _ := v.(string)
	return s
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
