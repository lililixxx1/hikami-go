package handler

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// adminTokenMiddleware 保护敏感 REST API（ISS-2）。
// token 为空时直接放行（loopback 默认场景，配置 Validate 已保证绑外网时 token 非空）；
// 否则校验请求凭据，不匹配返回 401。
func adminTokenMiddleware(token string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if token == "" {
			c.Next()
			return
		}
		if subtle.ConstantTimeCompare([]byte(extractAdminToken(c)), []byte(token)) == 1 {
			c.Next()
			return
		}
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"error": "missing or invalid admin token",
		})
	}
}

// extractAdminToken 从 X-Admin-Token 头提取，兼容 "Authorization: Bearer <token>"。
func extractAdminToken(c *gin.Context) string {
	if t := strings.TrimSpace(c.GetHeader("X-Admin-Token")); t != "" {
		return t
	}
	if auth := c.GetHeader("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
	}
	return ""
}
