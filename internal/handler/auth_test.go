package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"hikami-go/internal/config"
)

func newTokenRouter(token string) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	p := r.Group("", adminTokenMiddleware(token))
	p.GET("/api/secrets", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	return r
}

// TestAdminTokenMiddleware_EmptyTokenAllows: loopback 默认（token 空）应放行（ISS-2）。
func TestAdminTokenMiddleware_EmptyTokenAllows(t *testing.T) {
	r := newTokenRouter("")
	req := httptest.NewRequest(http.MethodGet, "/api/secrets", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("空 token 期望 200, got %d", w.Code)
	}
}

// TestAdminTokenMiddleware_MissingTokenRejected: 配了 token 但请求未携带 → 401。
func TestAdminTokenMiddleware_MissingTokenRejected(t *testing.T) {
	r := newTokenRouter("s3cret")
	req := httptest.NewRequest(http.MethodGet, "/api/secrets", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("缺 token 期望 401, got %d", w.Code)
	}
}

// TestAdminTokenMiddleware_ValidHeaderAccepted: X-Admin-Token 与 Bearer 均应放行。
func TestAdminTokenMiddleware_ValidHeaderAccepted(t *testing.T) {
	r := newTokenRouter("s3cret")

	req := httptest.NewRequest(http.MethodGet, "/api/secrets", nil)
	req.Header.Set("X-Admin-Token", "s3cret")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("X-Admin-Token 正确期望 200, got %d", w.Code)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/api/secrets", nil)
	req2.Header.Set("Authorization", "Bearer s3cret")
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Errorf("Bearer 正确期望 200, got %d", w2.Code)
	}
}

// TestAdminTokenMiddleware_WrongTokenRejected: 错误 token → 401。
func TestAdminTokenMiddleware_WrongTokenRejected(t *testing.T) {
	r := newTokenRouter("s3cret")
	req := httptest.NewRequest(http.MethodGet, "/api/secrets", nil)
	req.Header.Set("X-Admin-Token", "wrong")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("错误 token 期望 401, got %d", w.Code)
	}
}

// TestServer_AdminTokenResolution 验证 admin_token_env 优先于 admin_token（ISS-2 备注1）。
func TestServer_AdminTokenResolution(t *testing.T) {
	t.Setenv("HIKAMI_TEST_TOKEN", "from-env")

	// env 优先于 yaml 直接值
	s := &Server{cfg: &config.Config{Web: config.WebConfig{AdminToken: "from-yaml", AdminTokenEnv: "HIKAMI_TEST_TOKEN"}}}
	if got := s.adminToken(); got != "from-env" {
		t.Errorf("env 优先: got %q, want from-env", got)
	}

	// env 变量值为空时回退 yaml
	t.Setenv("HIKAMI_TEST_TOKEN", "")
	if got := s.adminToken(); got != "from-yaml" {
		t.Errorf("env 空回退 yaml: got %q, want from-yaml", got)
	}

	// 未配置 env 字段时用 yaml
	s.cfg.Web.AdminTokenEnv = ""
	if got := s.adminToken(); got != "from-yaml" {
		t.Errorf("无 env 字段: got %q, want from-yaml", got)
	}
}
