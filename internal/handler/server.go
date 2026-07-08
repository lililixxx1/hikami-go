package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"hikami-go/internal/archive"
	"hikami-go/internal/asr"
	"hikami-go/internal/biliutil"
	"hikami-go/internal/channel"
	"hikami-go/internal/config"
	"hikami-go/internal/discover"
	"hikami-go/internal/download"
	"hikami-go/internal/glossary"
	"hikami-go/internal/importer"
	"hikami-go/internal/live_record"
	"hikami-go/internal/publisher"
	"hikami-go/internal/recap"
	"hikami-go/internal/runtime"
	"hikami-go/internal/runtimeconfig"
	"hikami-go/internal/secrets"
	"hikami-go/internal/session"
	"hikami-go/internal/upload"
	"hikami-go/internal/worker"
)

// biliCreativeReferer 是 B站创作类端点（topic 搜索 / 文集列表）的 Referer/Origin。
// 2026-07-06 加入：searchBiliTopics/listBiliSeries 原为内联裸调，缺 Referer/Origin，
// 风控收紧即挂；biliutil.biliReferer 是包内私有常量，handler 包不可见，故本地定义。
const biliCreativeReferer = "https://www.bilibili.com"

type Server struct {
	cfg                *config.Config
	runtimeStatus      *runtime.Status
	channels           *channel.Store
	identifier         *channel.Identifier
	workerPool         *worker.Pool
	liveRecords        *live_record.Manager
	discoveries        *discover.Manager
	downloads          *download.Handler
	sessions           *session.Store
	imports            *importer.Handler
	asr                *asr.Handler
	recaps             *recap.Handler
	uploads            *upload.Handler
	archives           *archive.Handler
	publisher          *publisher.Handler
	secrets            *secrets.Store
	runtimeCfg         *runtimeconfig.Store // 全局运行时配置持久化（runtime_settings 表）
	glossary           *glossary.Store
	glossaryDiscoverer *glossary.Discoverer
	biliLogin          *biliutil.QRCodeManager
	// biliCreativeClient 是 B站创作类端点（topic 搜索 / 文集列表）的共享 HTTP client。
	// 2026-07-06 加入：替代 searchBiliTopics/listBiliSeries 两处函数内联的 &http.Client{}，
	// 统一带风控对抗头（UA + Referer + Origin + Cookie）。
	biliCreativeClient *http.Client
	router             *gin.Engine
	asrTempHandler     http.Handler
	upgrader           websocket.Upgrader
	webFS              fs.FS
	recapTemplates     *recap.TemplateStore
	cookieAccounts     *biliutil.CookieAccountStore
	publishMu          sync.RWMutex
	runtimeMu          sync.RWMutex
	configGen          atomic.Uint64
	notifyMgr          interface {
		Send(ctx context.Context, eventType, title, body string)
	}
}

func NewServer(
	cfg *config.Config,
	runtimeStatus *runtime.Status,
	channels *channel.Store,
	identifier *channel.Identifier,
	workerPool *worker.Pool,
	liveRecords *live_record.Manager,
	discoveries *discover.Manager,
	downloads *download.Handler,
	sessions *session.Store,
	imports *importer.Handler,
	asrHandler *asr.Handler,
	recaps *recap.Handler,
	uploads *upload.Handler,
	archives *archive.Handler,
	publisherHandler *publisher.Handler,
	secretsStore *secrets.Store,
	runtimeCfgStore *runtimeconfig.Store,
	webFS fs.FS,
	glossaryStore *glossary.Store,
	glossaryDiscoverer *glossary.Discoverer,
	recapTemplates *recap.TemplateStore,
	cookieAccounts *biliutil.CookieAccountStore,
	notifyManagers ...interface {
		Send(ctx context.Context, eventType, title, body string)
	},
) *Server {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())

	server := &Server{
		cfg:                cfg,
		runtimeStatus:      runtimeStatus,
		channels:           channels,
		identifier:         identifier,
		workerPool:         workerPool,
		liveRecords:        liveRecords,
		discoveries:        discoveries,
		downloads:          downloads,
		sessions:           sessions,
		imports:            imports,
		asr:                asrHandler,
		recaps:             recaps,
		uploads:            uploads,
		archives:           archives,
		publisher:          publisherHandler,
		secrets:            secretsStore,
		runtimeCfg:         runtimeCfgStore,
		glossary:           glossaryStore,
		glossaryDiscoverer: glossaryDiscoverer,
		recapTemplates:     recapTemplates,
		cookieAccounts:     cookieAccounts,
		biliLogin:          biliutil.NewQRCodeManager(biliutil.NewQRLoginClient(&http.Client{Timeout: 15 * time.Second}), 180*time.Second),
		biliCreativeClient: &http.Client{Timeout: 15 * time.Second},
		router:             router,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return checkWebSocketOrigin(r)
			},
		},
		webFS: webFS,
	}
	if len(notifyManagers) > 0 {
		server.notifyMgr = notifyManagers[0]
	}
	server.routes()
	return server
}

// adminToken 解析生效的管理员 token：优先 admin_token_env 指向的环境变量，回退 admin_token 直接值。
// 支持 env 注入可避免在 config.yaml 中明文存放（ISS-2 备注1）。
func (s *Server) adminToken() string {
	if env := strings.TrimSpace(s.cfg.Web.AdminTokenEnv); env != "" {
		if v := strings.TrimSpace(os.Getenv(env)); v != "" {
			return v
		}
	}
	return s.cfg.Web.AdminToken
}

func checkWebSocketOrigin(r *http.Request) bool {
	originHeader := r.Header.Get("Origin")
	if originHeader == "" {
		return true
	}

	origin, err := url.Parse(originHeader)
	if err != nil || origin.Host == "" {
		slog.Warn("websocket origin rejected", "origin", originHeader, "host", r.Host, "error", err)
		return false
	}
	if origin.Host == r.Host {
		return true
	}
	if isLocalWebSocketHost(origin.Host) && isLocalWebSocketHost(r.Host) {
		return true
	}

	slog.Warn("websocket origin rejected", "origin", originHeader, "host", r.Host)
	return false
}

func isLocalWebSocketHost(host string) bool {
	switch websocketHostName(host) {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}

func websocketHostName(host string) string {
	host = strings.TrimSpace(host)
	if name, _, err := net.SplitHostPort(host); err == nil {
		host = name
	} else if strings.Count(host, ":") == 1 {
		host = host[:strings.LastIndex(host, ":")]
	}
	return strings.Trim(strings.ToLower(host), "[]")
}

func (s *Server) Router() http.Handler {
	return s.router
}

func (s *Server) currentRuntimeStatus() *runtime.Status {
	s.runtimeMu.RLock()
	defer s.runtimeMu.RUnlock()
	return s.runtimeStatus
}

// CurrentRuntimeStatus 导出当前运行时能力状态（经代际校验机制保护的最新快照）。
// 供 cmd/hikami 注入给各 handler 的 CapabilityChecker 使用（设计 4.5），
// 使自动链读到的是 server 刷新后的状态而非 main.go 启动时 Probe 的陈旧快照。
func (s *Server) CurrentRuntimeStatus() *runtime.Status {
	return s.currentRuntimeStatus()
}

func (s *Server) setRuntimeStatus(status *runtime.Status) {
	s.runtimeMu.Lock()
	defer s.runtimeMu.Unlock()
	s.runtimeStatus = status
}

func (s *Server) bumpConfigGen() uint64 {
	s.runtimeMu.Lock()
	defer s.runtimeMu.Unlock()
	return s.configGen.Add(1)
}

func (s *Server) refreshRuntimeStatus(cfgSnapshot config.Config, gen uint64) {
	if s.currentRuntimeStatus() == nil {
		return
	}
	status := runtime.Probe(&cfgSnapshot)
	s.runtimeMu.Lock()
	defer s.runtimeMu.Unlock()
	if s.runtimeStatus == nil {
		return
	}
	if s.configGen.Load() > gen {
		return
	}
	s.runtimeStatus = status
}

func (s *Server) configSnapshot() (config.Config, uint64) {
	s.publishMu.RLock()
	defer s.publishMu.RUnlock()
	return *s.cfg, s.configGen.Load()
}

func (s *Server) SetASRTempHandler(handler http.Handler) {
	if handler == nil {
		return
	}
	s.asrTempHandler = handler
	s.router.Handle(http.MethodGet, "/asr-temp/*filepath", gin.WrapH(handler))
	s.router.Handle(http.MethodHead, "/asr-temp/*filepath", gin.WrapH(handler))
}

func (s *Server) routes() {
	s.router.GET("/ws", s.websocket)

	// 公开路由：健康检查无需认证（ISS-2）。
	s.router.GET("/api/healthz", s.healthz)

	// 受保护路由组：token 为空时中间件放行（loopback 默认零配置可用），
	// 非空时校验 X-Admin-Token（绑外网时 Validate 已强制配置）。
	p := s.router.Group("", adminTokenMiddleware(s.adminToken()))

	p.GET("/api/health/runtime", s.runtimeHealth)

	p.GET("/api/onboarding/status", s.handleOnboardingStatus)
	p.POST("/api/onboarding/dismiss", s.handleOnboardingDismiss)

	p.GET("/api/channels", s.listChannels)
	p.GET("/api/channels/:id", s.getChannel)
	p.POST("/api/channels/identify", s.identifyChannel)
	p.POST("/api/channels/identify/save", s.saveIdentifiedChannel)
	p.POST("/api/channels", s.createChannel)
	p.PUT("/api/channels/:id", s.updateChannel)
	p.DELETE("/api/channels/:id", s.deleteChannel)

	p.POST("/api/channels/:id/copy-config", s.handleCopyChannelConfig)
	p.POST("/api/live/check", s.checkLive)
	p.GET("/api/live/status", s.liveStatus)
	p.GET("/api/live/:channel_id/status", s.liveChannelStatus)
	p.POST("/api/live/:channel_id/record/start", s.startLiveRecord)
	p.POST("/api/live/:channel_id/record/stop", s.stopLiveRecord)

	p.POST("/api/sessions/discover", s.discoverSessions)
	p.POST("/api/sessions/discover/preview", s.discoverPreviewAll)
	p.POST("/api/sessions/discover/execute", s.discoverExecute)
	p.GET("/api/sessions", s.listSessions)
	p.GET("/api/sessions/:sid", s.getSession)
	p.DELETE("/api/sessions/failed", s.deleteFailedSessions)
	p.DELETE("/api/sessions/:sid", s.deleteSession)
	p.POST("/api/sessions/download", s.downloadSession)
	p.POST("/api/sessions/download-by-url", s.downloadSessionByURL)
	p.POST("/api/sessions/import", s.importSession)
	p.POST("/api/sessions/:sid/asr/submit", s.submitASR)
	p.POST("/api/sessions/:sid/recap/generate", s.generateRecap)
	p.POST("/api/sessions/:sid/recap/regenerate", s.regenerateRecap)
	p.POST("/api/sessions/:sid/recap-partial", s.generateRecapPartial)
	p.POST("/api/sessions/:sid/recap-with-range", s.generateRecapWithRange)
	p.GET("/api/sessions/:sid/recap", s.getRecapContent)
	p.PUT("/api/sessions/:sid/recap/content", s.handleUpdateRecapContent)
	p.POST("/api/sessions/:sid/upload", s.uploadSession)
	p.POST("/api/sessions/:sid/fetch", s.fetchSession)
	p.POST("/api/sessions/:sid/publish", s.publishSession)
	p.POST("/api/sessions/:sid/archive", s.archiveSession)
	p.POST("/api/sessions/:sid/glossary/discover", s.discoverSessionGlossary)

	p.GET("/api/tasks", s.listTasks)
	p.POST("/api/tasks/batch-retry", s.handleBatchRetryTasks)
	p.GET("/api/tasks/:id", s.getTask)
	p.POST("/api/tasks/:id/retry", s.retryTask)
	p.POST("/api/tasks/:id/cancel", s.cancelTask)
	p.DELETE("/api/tasks/failed", s.deleteFailedTasks)
	p.DELETE("/api/tasks/:id", s.deleteTask)

	p.POST("/api/notify/test", s.handleNotifyTest)

	p.GET("/api/secrets", s.listSecrets)
	p.PUT("/api/secrets/:key", s.updateSecret)
	p.GET("/api/config/publish", s.getPublishConfig)
	p.PUT("/api/config/publish", s.updatePublishConfig)
	p.GET("/api/config/recap", s.getRecapConfig)
	p.PUT("/api/config/recap", s.updateRecapConfig)
	p.GET("/api/config/recap/models", s.getRecapModels)
	p.GET("/api/config/dashscope", s.getDashScopeConfig)
	p.PUT("/api/config/dashscope", s.updateDashScopeConfig)
	p.GET("/api/config/asr-s3", s.getASRS3Config)
	p.PUT("/api/config/asr-s3", s.updateASRS3Config)
	p.GET("/api/config/webdav", s.getWebDAVConfig)
	p.PUT("/api/config/webdav", s.updateWebDAVConfig)
	p.GET("/api/config/archive", s.getArchiveConfig)
	p.PUT("/api/config/archive", s.updateArchiveConfig)
	p.GET("/api/config/export", s.handleExportConfig)
	p.POST("/api/config/import", s.handleImportConfig)

	p.GET("/api/diagnostic/report", s.handleDiagnosticReport)
	p.GET("/api/stats/overview", s.handleStatsOverview)
	p.GET("/api/stats/cost", s.handleStatsCost)
	p.GET("/api/stats/dashboard", s.handleStatsDashboard)

	p.POST("/api/channels/:id/discover/preview", s.handleDiscoverPreview)
	p.GET("/api/cookies/status", s.handleCookieStatus)

	p.POST("/api/bili/login/qrcode", s.createBiliQRCodeLogin)
	p.GET("/api/bili/login/qrcode/:session_id", s.pollBiliQRCodeLogin)
	p.POST("/api/bili/login/qrcode/:session_id/save", s.saveBiliQRCodeLogin)
	p.POST("/api/bili/login/qrcode/:session_id/save-account", s.saveBiliQRCodeToAccount)
	p.DELETE("/api/bili/login/qrcode/:session_id", s.deleteBiliQRCodeLogin)
	p.GET("/api/bili/topics/search", s.searchBiliTopics)
	p.GET("/api/bili/series/list", s.listBiliSeries)

	p.GET("/api/cookie-accounts", s.listCookieAccounts)
	p.POST("/api/cookie-accounts", s.createCookieAccount)
	p.PUT("/api/cookie-accounts/:id", s.updateCookieAccount)
	p.DELETE("/api/cookie-accounts/:id", s.deleteCookieAccount)
	p.POST("/api/cookie-accounts/:id/default-download", s.setDefaultDownloadCookieAccount)
	p.POST("/api/cookie-accounts/:id/default-publish", s.setDefaultPublishCookieAccount)

	p.GET("/api/bili/accounts", s.listBiliAccounts)
	p.PUT("/api/bili/accounts/:id", s.updateBiliAccount)
	p.DELETE("/api/bili/accounts/:id", s.deleteBiliAccount)

	p.GET("/api/glossary/entries", s.listGlobalGlossary)
	p.POST("/api/glossary/entries", s.upsertGlobalGlossary)
	p.DELETE("/api/glossary/entries/:eid", s.deleteGlobalGlossary)
	p.GET("/api/glossary/note", s.getGlobalGlossaryNote)
	p.PUT("/api/glossary/note", s.updateGlobalGlossaryNote)
	p.GET("/api/glossary/candidates", s.listGlobalGlossaryCandidates)
	p.POST("/api/glossary/candidates/:cid/approve", s.approveGlossaryCandidate)
	p.POST("/api/glossary/candidates/:cid/reject", s.rejectGlossaryCandidate)

	p.GET("/api/channels/:id/glossary/entries", s.listChannelGlossary)
	p.POST("/api/channels/:id/glossary/entries", s.upsertChannelGlossary)
	p.DELETE("/api/channels/:id/glossary/entries/:eid", s.deleteChannelGlossary)
	p.GET("/api/channels/:id/glossary/note", s.getChannelGlossaryNote)
	p.PUT("/api/channels/:id/glossary/note", s.updateChannelGlossaryNote)
	p.GET("/api/channels/:id/glossary/candidates", s.listChannelGlossaryCandidates)
	p.POST("/api/channels/:id/glossary/candidates/batch-approve", s.batchApproveGlossaryCandidates)
	p.POST("/api/channels/:id/glossary/candidates/batch-reject", s.batchRejectGlossaryCandidates)

	p.POST("/api/glossary/import/markdown", s.importGlobalMarkdown)
	p.POST("/api/glossary/import/json", s.importGlobalJSON)
	p.GET("/api/glossary/export/json", s.exportGlobalJSON)
	p.POST("/api/channels/:id/glossary/import/markdown", s.importChannelMarkdown)
	p.POST("/api/channels/:id/glossary/import/json", s.importChannelJSON)
	p.GET("/api/channels/:id/glossary/export/json", s.exportChannelJSON)

	// Batch operations
	p.POST("/api/glossary/entries/batch-delete", s.batchDeleteGlobalGlossary)
	p.POST("/api/glossary/entries/batch-toggle", s.batchToggleGlobalGlossary)
	p.POST("/api/glossary/entries/:eid/toggle", s.toggleGlobalGlossary)
	p.POST("/api/channels/:id/glossary/entries/batch-delete", s.batchDeleteChannelGlossary)
	p.POST("/api/channels/:id/glossary/entries/batch-toggle", s.batchToggleChannelGlossary)
	p.POST("/api/channels/:id/glossary/entries/:eid/toggle", s.toggleChannelGlossary)

	// Recap template management
	recapPublic := p.Group("/api/recap")
	{
		recapPublic.GET("/templates", s.listGlobalRecapTemplates)
		recapPublic.PUT("/templates", s.upsertGlobalRecapTemplate)
		recapPublic.GET("/templates/export", s.exportGlobalRecapTemplates)
		recapPublic.POST("/templates/import", s.importGlobalRecapTemplates)
		recapPublic.GET("/presets", s.handleListPresets)
	}
	recapChannel := p.Group("/api/channels/:id")
	{
		recapChannel.GET("/recap-template", s.getChannelRecapTemplate)
		recapChannel.PUT("/recap-template", s.upsertChannelRecapTemplate)
		recapChannel.DELETE("/recap-template", s.deleteChannelRecapTemplate)
		recapChannel.GET("/recap-template/export", s.exportChannelRecapTemplates)
		recapChannel.POST("/recap-template/import", s.importChannelRecapTemplates)
	}

	if s.webFS != nil {
		// SPA mode: serve embedded frontend
		fileServer := http.FileServer(http.FS(s.webFS))
		s.router.NoRoute(func(ctx *gin.Context) {
			path := ctx.Request.URL.Path

			// /api and /ws paths return 404 JSON
			if strings.HasPrefix(path, "/api/") || path == "/ws" {
				ctx.JSON(http.StatusNotFound, gin.H{"error": "not found"})
				return
			}

			// Try to serve static file
			cleaned := strings.TrimPrefix(path, "/")
			if cleaned == "" {
				cleaned = "index.html"
			}
			f, err := s.webFS.Open(cleaned)
			if err == nil {
				f.Close()
				fileServer.ServeHTTP(ctx.Writer, ctx.Request)
				return
			}

			// Fallback to index.html for SPA routing
			ctx.Request.URL.Path = "/"
			fileServer.ServeHTTP(ctx.Writer, ctx.Request)
		})
	} else {
		// Pure API mode: keep the original index handler
		s.router.GET("/", s.index)
	}
}

func (s *Server) healthz(ctx *gin.Context) {
	ctx.JSON(http.StatusOK, gin.H{
		"status": "ok",
	})
}

func (s *Server) index(ctx *gin.Context) {
	ctx.Data(http.StatusOK, "text/html; charset=utf-8", []byte(`<!doctype html>
<html lang="zh-CN">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Hikami-Go</title></head>
<body><main><h1>Hikami-Go</h1><p>服务已运行。请使用 REST API 管理直播录制、回放处理和归档任务。</p></main></body>
</html>`))
}

// runtimeStatusResponse 在 runtime.Status 基础上附带账号池默认账号可用性。
// 这两个字段实时查 DB（每次 GET），不塞进 runtime.Probe 产物——避免污染
// CapabilityChecker 消费链与 Probe 的 8 处调用点，且默认账号增删立即生效。
type runtimeStatusResponse struct {
	*runtime.Status
	HasDefaultDownload bool `json:"has_default_download"`
	HasDefaultPublish  bool `json:"has_default_publish"`
}

func (s *Server) runtimeHealth(ctx *gin.Context) {
	resp := runtimeStatusResponse{Status: s.currentRuntimeStatus()}
	if s.cookieAccounts != nil {
		// GetDefault* 无默认账号时返回 ErrNoDefaultAccount（非 nil），故用 err == nil 判定存在性。
		// 对 DB 错误/ctx 取消等非 ErrNoDefaultAccount 错误记日志，避免静默误判为「无默认账号」导致前端误报。
		if _, err := s.cookieAccounts.GetDefaultDownload(ctx.Request.Context()); err == nil {
			resp.HasDefaultDownload = true
		} else if !errors.Is(err, biliutil.ErrNoDefaultAccount) {
			slog.Warn("runtime status: query default download account failed", "error", err)
		}
		if _, err := s.cookieAccounts.GetDefaultPublish(ctx.Request.Context()); err == nil {
			resp.HasDefaultPublish = true
		} else if !errors.Is(err, biliutil.ErrNoDefaultAccount) {
			slog.Warn("runtime status: query default publish account failed", "error", err)
		}
	}
	ctx.JSON(http.StatusOK, resp)
}

func (s *Server) listChannels(ctx *gin.Context) {
	channels, err := s.channels.List(ctx.Request.Context())
	if err != nil {
		writeError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"items": channels})
}

// getChannel 返回单个频道详情。序列化形态与 listChannels 的单元素一致。
func (s *Server) getChannel(ctx *gin.Context) {
	ch, err := s.channels.Get(ctx.Request.Context(), ctx.Param("id"))
	if err != nil {
		writeError(ctx, err) // ErrNotFound → 404(已有映射)
		return
	}
	ctx.JSON(http.StatusOK, ch)
}

func (s *Server) identifyChannel(ctx *gin.Context) {
	var input channel.IdentifyInput
	if err := ctx.ShouldBindJSON(&input); err != nil {
		writeBadRequest(ctx, "invalid json body")
		return
	}
	result, err := s.identifier.Identify(ctx.Request.Context(), input)
	if err != nil {
		writeError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, result)
}

func (s *Server) saveIdentifiedChannel(ctx *gin.Context) {
	var input channel.IdentifyInput
	if err := ctx.ShouldBindJSON(&input); err != nil {
		writeBadRequest(ctx, "invalid json body")
		return
	}
	result, err := s.identifier.Identify(ctx.Request.Context(), input)
	if err != nil {
		writeError(ctx, err)
		return
	}
	saved, created, err := s.channels.SaveIdentified(ctx.Request.Context(), result.Channel)
	if err != nil {
		writeError(ctx, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	ctx.JSON(status, channel.IdentifySaveResult{
		Channel: saved,
		Source:  result.Source,
		Created: created,
	})
}

func (s *Server) createChannel(ctx *gin.Context) {
	var input channel.UpsertInput
	if err := ctx.ShouldBindJSON(&input); err != nil {
		writeBadRequest(ctx, "invalid json body")
		return
	}
	created, err := s.channels.Create(ctx.Request.Context(), input)
	if err != nil {
		writeError(ctx, err)
		return
	}
	ctx.JSON(http.StatusCreated, created)
}

func (s *Server) updateChannel(ctx *gin.Context) {
	var input channel.UpsertInput
	if err := ctx.ShouldBindJSON(&input); err != nil {
		writeBadRequest(ctx, "invalid json body")
		return
	}
	updated, err := s.channels.Update(ctx.Request.Context(), ctx.Param("id"), input)
	if err != nil {
		writeError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, updated)
}

func (s *Server) deleteChannel(ctx *gin.Context) {
	channelID := ctx.Param("id")
	if err := s.channels.Delete(ctx.Request.Context(), channelID); err != nil {
		if errors.Is(err, channel.ErrInUse) {
			// 查询关联场次数量，返回更友好的错误提示
			sessionCount, _ := s.sessions.CountByChannel(ctx.Request.Context(), channelID)
			ctx.JSON(http.StatusConflict, gin.H{
				"error":         fmt.Sprintf("该主播有 %d 个关联场次，请先删除相关场次后再删除主播", sessionCount),
				"session_count": sessionCount,
			})
			return
		}
		writeError(ctx, err)
		return
	}
	ctx.Status(http.StatusNoContent)
}

func (s *Server) createBiliQRCodeLogin(ctx *gin.Context) {
	result, err := s.biliLogin.Create(ctx.Request.Context())
	if err != nil {
		writeError(ctx, err)
		return
	}
	ctx.JSON(http.StatusCreated, result)
}

func (s *Server) pollBiliQRCodeLogin(ctx *gin.Context) {
	result, err := s.biliLogin.Poll(ctx.Request.Context(), ctx.Param("session_id"))
	if err != nil {
		writeError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, result)
}

func (s *Server) saveBiliQRCodeLogin(ctx *gin.Context) {
	var input struct {
		ChannelID string `json:"channel_id"`
		Usage     string `json:"usage"`
	}
	if err := ctx.ShouldBindJSON(&input); err != nil {
		writeBadRequest(ctx, "invalid json body")
		return
	}
	if strings.TrimSpace(input.ChannelID) == "" {
		writeBadRequest(ctx, "channel_id is required")
		return
	}

	usage, err := parseCookieUsage(input.Usage)
	if err != nil {
		writeBadRequest(ctx, err.Error())
		return
	}
	session, err := s.biliLogin.GetSucceeded(ctx.Param("session_id"))
	if err != nil {
		writeError(ctx, err)
		return
	}
	uid := session.UID
	if uid == 0 {
		uid = channelUIDFromCookies(session.Cookies)
	}
	result, err := biliutil.WriteNetscapeCookieFile(session.Cookies, biliutil.CookieWriteOptions{
		Dir:   filepath.Join(s.cfg.OutputRoot, ".cookies", "bilibili"),
		UID:   uid,
		Usage: string(usage),
	})
	if err != nil {
		writeError(ctx, err)
		return
	}
	updated, err := s.channels.UpdateCookieFile(ctx.Request.Context(), input.ChannelID, usage, result.Path)
	if err != nil {
		writeError(ctx, err)
		return
	}
	s.biliLogin.Delete(ctx.Param("session_id"))

	ctx.JSON(http.StatusOK, gin.H{
		"channel":     updated,
		"usage":       usage,
		"cookie_file": result.Path,
		"uid":         result.UID,
		"expires_at":  result.ExpiresAt,
	})
}

func (s *Server) deleteBiliQRCodeLogin(ctx *gin.Context) {
	s.biliLogin.Delete(ctx.Param("session_id"))
	ctx.Status(http.StatusNoContent)
}

func parseCookieUsage(value string) (channel.CookieUsage, error) {
	switch channel.CookieUsage(value) {
	case channel.CookieUsageDownload:
		return channel.CookieUsageDownload, nil
	case channel.CookieUsagePublish:
		return channel.CookieUsagePublish, nil
	default:
		return "", errors.New("usage must be download or publish")
	}
}

func channelUIDFromCookies(cookies []*http.Cookie) int64 {
	for _, cookie := range cookies {
		if cookie != nil && cookie.Name == "DedeUserID" {
			uid, _ := strconv.ParseInt(cookie.Value, 10, 64)
			return uid
		}
	}
	return 0
}

// saveBiliQRCodeToAccount saves QR login cookies as a global account.
func (s *Server) saveBiliQRCodeToAccount(ctx *gin.Context) {
	if s.cookieAccounts == nil {
		ctx.JSON(http.StatusServiceUnavailable, gin.H{"error": "cookie account store unavailable"})
		return
	}
	var input struct {
		Nickname string `json:"nickname"`
	}
	_ = ctx.ShouldBindJSON(&input)

	session, err := s.biliLogin.GetSucceeded(ctx.Param("session_id"))
	if err != nil {
		writeError(ctx, err)
		return
	}
	uid := session.UID
	if uid == 0 {
		uid = channelUIDFromCookies(session.Cookies)
	}
	if uid == 0 {
		writeBadRequest(ctx, "cannot determine uid from login")
		return
	}

	result, err := biliutil.WriteNetscapeCookieFile(session.Cookies, biliutil.CookieWriteOptions{
		Dir:   filepath.Join(s.cfg.OutputRoot, ".cookies", "bilibili"),
		UID:   uid,
		Usage: "account",
	})
	if err != nil {
		writeError(ctx, err)
		return
	}

	// Check if account already exists for this UID
	existing, _ := s.cookieAccounts.GetByUID(ctx.Request.Context(), uid)
	if existing != nil {
		// Update cookie file path
		existing.CookieFile = result.Path
		if input.Nickname != "" {
			existing.Nickname = input.Nickname
		}
		if err := s.cookieAccounts.Update(ctx.Request.Context(), existing); err != nil {
			writeError(ctx, err)
			return
		}
		s.biliLogin.Delete(ctx.Param("session_id"))
		ctx.JSON(http.StatusOK, existing)
		return
	}

	// First account auto-becomes default for both
	accounts, _ := s.cookieAccounts.List(ctx.Request.Context())
	isFirst := len(accounts) == 0

	account := &biliutil.CookieAccount{
		UID:               uid,
		Nickname:          input.Nickname,
		CookieFile:        result.Path,
		IsDefaultDownload: isFirst,
		IsDefaultPublish:  isFirst,
	}
	id, err := s.cookieAccounts.Create(ctx.Request.Context(), account)
	if err != nil {
		writeError(ctx, err)
		return
	}
	account.ID = id

	s.biliLogin.Delete(ctx.Param("session_id"))
	ctx.JSON(http.StatusOK, account)
}

func (s *Server) listBiliAccounts(ctx *gin.Context) {
	s.listCookieAccounts(ctx)
}

func (s *Server) listCookieAccounts(ctx *gin.Context) {
	if s.cookieAccounts == nil {
		ctx.JSON(http.StatusOK, []biliutil.CookieAccount{})
		return
	}
	accounts, err := s.cookieAccounts.List(ctx.Request.Context())
	if err != nil {
		writeError(ctx, err)
		return
	}
	if accounts == nil {
		accounts = []biliutil.CookieAccount{}
	}
	ctx.JSON(http.StatusOK, accounts)
}

func (s *Server) createCookieAccount(ctx *gin.Context) {
	if s.cookieAccounts == nil {
		ctx.JSON(http.StatusServiceUnavailable, gin.H{"error": "cookie account store unavailable"})
		return
	}
	var input biliutil.CookieAccount
	if err := ctx.ShouldBindJSON(&input); err != nil {
		writeBadRequest(ctx, "invalid json body")
		return
	}
	account := &biliutil.CookieAccount{
		UID:               input.UID,
		Nickname:          input.Nickname,
		CookieFile:        input.CookieFile,
		IsDefaultDownload: input.IsDefaultDownload,
		IsDefaultPublish:  input.IsDefaultPublish,
	}
	if err := biliutil.ValidateCookiePath(account.CookieFile, s.cookieAccountAllowedDirs()); err != nil {
		writeBadRequest(ctx, err.Error())
		return
	}
	id, err := s.cookieAccounts.Create(ctx.Request.Context(), account)
	if err != nil {
		writeError(ctx, err)
		return
	}
	account.ID = id
	ctx.JSON(http.StatusCreated, account)
}

func (s *Server) updateBiliAccount(ctx *gin.Context) {
	s.updateCookieAccount(ctx)
}

func (s *Server) updateCookieAccount(ctx *gin.Context) {
	if s.cookieAccounts == nil {
		ctx.JSON(http.StatusServiceUnavailable, gin.H{"error": "cookie account store unavailable"})
		return
	}
	id, err := strconv.ParseInt(ctx.Param("id"), 10, 64)
	if err != nil {
		writeBadRequest(ctx, "invalid account id")
		return
	}
	account, err := s.cookieAccounts.GetByID(ctx.Request.Context(), id)
	if err != nil {
		writeError(ctx, err)
		return
	}

	var input struct {
		Nickname          *string `json:"nickname"`
		CookieFile        *string `json:"cookie_file"`
		IsDefaultDownload *bool   `json:"is_default_download"`
		IsDefaultPublish  *bool   `json:"is_default_publish"`
	}
	if err := ctx.ShouldBindJSON(&input); err != nil {
		writeBadRequest(ctx, "invalid json body")
		return
	}

	if input.Nickname != nil {
		account.Nickname = *input.Nickname
	}
	if input.CookieFile != nil {
		account.CookieFile = *input.CookieFile
	}
	if err := biliutil.ValidateCookiePath(account.CookieFile, s.cookieAccountAllowedDirs()); err != nil {
		writeBadRequest(ctx, err.Error())
		return
	}
	if input.IsDefaultDownload != nil && *input.IsDefaultDownload {
		if err := s.cookieAccounts.SetDefaultDownload(ctx.Request.Context(), id); err != nil {
			writeError(ctx, err)
			return
		}
		account.IsDefaultDownload = true
	}
	if input.IsDefaultPublish != nil && *input.IsDefaultPublish {
		if err := s.cookieAccounts.SetDefaultPublish(ctx.Request.Context(), id); err != nil {
			writeError(ctx, err)
			return
		}
		account.IsDefaultPublish = true
	}
	if err := s.cookieAccounts.Update(ctx.Request.Context(), account); err != nil {
		writeError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, account)
}

func (s *Server) cookieAccountAllowedDirs() []string {
	if s.cfg == nil {
		return nil
	}
	return []string{filepath.Join(s.cfg.OutputRoot, ".cookies", "bilibili")}
}

func (s *Server) deleteBiliAccount(ctx *gin.Context) {
	s.deleteCookieAccount(ctx)
}

func (s *Server) deleteCookieAccount(ctx *gin.Context) {
	if s.cookieAccounts == nil {
		ctx.JSON(http.StatusServiceUnavailable, gin.H{"error": "cookie account store unavailable"})
		return
	}
	id, err := strconv.ParseInt(ctx.Param("id"), 10, 64)
	if err != nil {
		writeBadRequest(ctx, "invalid account id")
		return
	}
	if err := s.cookieAccounts.Delete(ctx.Request.Context(), id); err != nil {
		writeError(ctx, err)
		return
	}
	ctx.Status(http.StatusNoContent)
}

func (s *Server) setDefaultDownloadCookieAccount(ctx *gin.Context) {
	s.setDefaultCookieAccount(ctx, "download")
}

func (s *Server) setDefaultPublishCookieAccount(ctx *gin.Context) {
	s.setDefaultCookieAccount(ctx, "publish")
}

func (s *Server) setDefaultCookieAccount(ctx *gin.Context, usage string) {
	if s.cookieAccounts == nil {
		ctx.JSON(http.StatusServiceUnavailable, gin.H{"error": "cookie account store unavailable"})
		return
	}
	id, err := strconv.ParseInt(ctx.Param("id"), 10, 64)
	if err != nil {
		writeBadRequest(ctx, "invalid account id")
		return
	}
	if _, err := s.cookieAccounts.GetByID(ctx.Request.Context(), id); err != nil {
		writeError(ctx, err)
		return
	}
	switch usage {
	case "download":
		err = s.cookieAccounts.SetDefaultDownload(ctx.Request.Context(), id)
	case "publish":
		err = s.cookieAccounts.SetDefaultPublish(ctx.Request.Context(), id)
	default:
		writeBadRequest(ctx, "invalid cookie usage")
		return
	}
	if err != nil {
		writeError(ctx, err)
		return
	}
	account, err := s.cookieAccounts.GetByID(ctx.Request.Context(), id)
	if err != nil {
		writeError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, account)
}

func (s *Server) checkLive(ctx *gin.Context) {
	statuses, err := s.liveRecords.CheckAndStartAll(ctx.Request.Context())
	if err != nil {
		writeError(ctx, err)
		return
	}
	ctx.JSON(http.StatusAccepted, gin.H{"items": statuses})
}

func (s *Server) liveStatus(ctx *gin.Context) {
	statuses, err := s.liveRecords.CheckAll(ctx.Request.Context())
	if err != nil {
		writeError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"items": statuses})
}

func (s *Server) liveChannelStatus(ctx *gin.Context) {
	status, err := s.liveRecords.Check(ctx.Request.Context(), ctx.Param("channel_id"))
	if err != nil {
		writeError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, status)
}

func (s *Server) startLiveRecord(ctx *gin.Context) {
	status, err := s.liveRecords.Start(ctx.Request.Context(), ctx.Param("channel_id"))
	if err != nil {
		writeError(ctx, err)
		return
	}
	ctx.JSON(http.StatusAccepted, status)
}

func (s *Server) stopLiveRecord(ctx *gin.Context) {
	if err := s.liveRecords.Stop(ctx.Param("channel_id")); err != nil {
		writeError(ctx, err)
		return
	}
	ctx.Status(http.StatusAccepted)
}

func (s *Server) discoverSessions(ctx *gin.Context) {
	results, err := s.discoveries.DiscoverAll(ctx.Request.Context())
	if err != nil {
		writeError(ctx, err)
		return
	}
	ctx.JSON(http.StatusAccepted, gin.H{"items": results})
}

// discoverPreviewAll 遍历所有频道预览回放（不建场次、不入队），供两步式发现的「第一步预览」。
// 返回的每条 Result 带 Exists 标记（是否已建过 download 场次），前端据此标记「已处理」。
func (s *Server) discoverPreviewAll(ctx *gin.Context) {
	results, err := s.discoveries.PreviewAll(ctx.Request.Context())
	if err != nil {
		writeError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"items": results})
}

// discoverExecute 按前端勾选的 entry 列表建 download 场并入队下载任务，供两步式发现的「第二步执行」。
// 不重跑 yt-dlp：复用预览阶段已拿到的 entry 信息。复用 CreateDownload 幂等性去重。
func (s *Server) discoverExecute(ctx *gin.Context) {
	var input struct {
		Items []discover.ExecuteItem `json:"items"`
	}
	if err := ctx.ShouldBindJSON(&input); err != nil {
		writeBadRequest(ctx, "invalid json body")
		return
	}
	results := s.discoveries.Execute(ctx.Request.Context(), input.Items)
	ctx.JSON(http.StatusAccepted, gin.H{"items": results})
}

func (s *Server) downloadSession(ctx *gin.Context) {
	var input struct {
		SessionID string `json:"session_id"`
	}
	if err := ctx.ShouldBindJSON(&input); err != nil {
		writeBadRequest(ctx, "invalid json body")
		return
	}
	if input.SessionID == "" {
		writeBadRequest(ctx, "session_id is required")
		return
	}
	task, err := s.downloads.Enqueue(ctx.Request.Context(), input.SessionID)
	if err != nil {
		writeError(ctx, err)
		return
	}
	ctx.JSON(http.StatusAccepted, task)
}

// downloadSessionByURL 接收用户粘贴的视频链接（如 B 站 BV 号）与主播 ID，
// 创建下载场次并入队，复用既有 download → normalize → asr → recap 管道。
func (s *Server) downloadSessionByURL(ctx *gin.Context) {
	status := s.currentRuntimeStatus()
	if status != nil && !status.Capabilities.ReplayDownload {
		ctx.JSON(http.StatusConflict, gin.H{
			"error":  "replay download capability unavailable",
			"reason": status.Capabilities.Reason,
		})
		return
	}
	var input struct {
		ChannelID string `json:"channel_id"`
		URL       string `json:"url"`
	}
	if err := ctx.ShouldBindJSON(&input); err != nil {
		writeBadRequest(ctx, "invalid json body")
		return
	}
	if input.ChannelID == "" {
		writeBadRequest(ctx, "channel_id is required")
		return
	}
	if input.URL == "" {
		writeBadRequest(ctx, "url is required")
		return
	}
	task, err := s.downloads.CreateFromURL(ctx.Request.Context(), input.ChannelID, input.URL)
	if err != nil {
		writeError(ctx, err)
		return
	}
	ctx.JSON(http.StatusAccepted, task)
}

func (s *Server) listSessions(ctx *gin.Context) {
	f := session.ListFilter{
		ChannelID: ctx.Query("channel_id"),
		Source:    ctx.Query("source"),
		Search:    ctx.Query("search"),
	}
	sessions, err := s.sessions.ListWithFilter(ctx.Request.Context(), f)
	if err != nil {
		writeError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"items": sessions})
}

func (s *Server) getSession(ctx *gin.Context) {
	sessionInfo, err := s.sessions.Get(ctx.Request.Context(), ctx.Param("sid"))
	if err != nil {
		writeError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{
		"session": sessionInfo,
		"files":   listSessionFiles(s.cfg.OutputRoot, sessionInfo),
	})
}

func (s *Server) importSession(ctx *gin.Context) {
	mediaFile, err := ctx.FormFile("media_file")
	if err != nil {
		writeBadRequest(ctx, "media_file is required")
		return
	}
	channelID := ctx.PostForm("channel_id")
	title := ctx.PostForm("title")
	if channelID == "" || title == "" {
		writeBadRequest(ctx, "channel_id and title are required")
		return
	}
	startedAt, err := parseOptionalTime(ctx.PostForm("started_at"))
	if err != nil {
		writeBadRequest(ctx, "started_at must be RFC3339")
		return
	}
	endedAt, err := parseOptionalTime(ctx.PostForm("ended_at"))
	if err != nil {
		writeBadRequest(ctx, "ended_at must be RFC3339")
		return
	}
	danmakuFile, _ := ctx.FormFile("danmaku_file")
	task, err := s.imports.CreateFromMultipart(ctx.Request.Context(), session.CreateImportInput{
		ChannelID: channelID,
		Title:     title,
		StartedAt: startedAt,
		EndedAt:   endedAt,
		SourceURL: ctx.PostForm("source_url"),
	}, mediaFile, danmakuFile)
	if err != nil {
		writeError(ctx, err)
		return
	}
	ctx.JSON(http.StatusAccepted, task)
}

func (s *Server) submitASR(ctx *gin.Context) {
	status := s.currentRuntimeStatus()
	if status != nil && !status.Capabilities.ASRSubmit {
		ctx.JSON(http.StatusConflict, gin.H{
			"error":  "asr submit capability unavailable",
			"reason": status.Capabilities.Reason,
		})
		return
	}
	task, err := s.asr.CreateTask(ctx.Request.Context(), s.workerPool, ctx.Param("sid"))
	if err != nil {
		writeError(ctx, err)
		return
	}
	ctx.JSON(http.StatusAccepted, task)
}

func (s *Server) generateRecap(ctx *gin.Context) {
	status := s.currentRuntimeStatus()
	if status != nil && !status.Capabilities.RecapGenerate {
		ctx.JSON(http.StatusConflict, gin.H{
			"error":  "recap capability unavailable",
			"reason": status.Capabilities.Reason,
		})
		return
	}
	task, err := s.recaps.CreateTask(ctx.Request.Context(), s.workerPool, ctx.Param("sid"))
	if err != nil {
		writeError(ctx, err)
		return
	}
	ctx.JSON(http.StatusAccepted, task)
}

// regenerateRecap 重新生成整场回顾（覆盖本地 md，不碰 B站）。仅 recap_done/published 状态允许。
// 任务带 BypassFailState=true：失败时仅写 last_error，不降级 published/recap_done 主状态。
func (s *Server) regenerateRecap(ctx *gin.Context) {
	status := s.currentRuntimeStatus()
	if status != nil && !status.Capabilities.RecapGenerate {
		ctx.JSON(http.StatusConflict, gin.H{
			"error":  "recap capability unavailable",
			"reason": status.Capabilities.Reason,
		})
		return
	}
	task, err := s.recaps.CreateRegenTask(ctx.Request.Context(), s.workerPool, ctx.Param("sid"))
	if err != nil {
		writeError(ctx, err)
		return
	}
	ctx.JSON(http.StatusAccepted, task)
}

func (s *Server) generateRecapWithRange(ctx *gin.Context) {
	s.generateRecapPartial(ctx)
}

func (s *Server) generateRecapPartial(ctx *gin.Context) {
	status := s.currentRuntimeStatus()
	if status != nil && !status.Capabilities.RecapGenerate {
		ctx.JSON(http.StatusConflict, gin.H{
			"error":  "recap capability unavailable",
			"reason": status.Capabilities.Reason,
		})
		return
	}
	var input struct {
		StartTime float64 `json:"start_time"`
		EndTime   float64 `json:"end_time"`
	}
	if err := ctx.ShouldBindJSON(&input); err != nil {
		writeBadRequest(ctx, "invalid json body")
		return
	}
	if input.StartTime < 0 || input.EndTime <= input.StartTime {
		writeBadRequest(ctx, "end_time must be greater than start_time")
		return
	}
	task, err := s.recaps.CreateTaskWithRange(ctx.Request.Context(), s.workerPool, ctx.Param("sid"), input.StartTime, input.EndTime)
	if err != nil {
		writeError(ctx, err)
		return
	}
	ctx.JSON(http.StatusAccepted, task)
}

func (s *Server) getRecapContent(ctx *gin.Context) {
	sid := ctx.Param("sid")
	sessionInfo, err := s.sessions.Get(ctx.Request.Context(), sid)
	if err != nil {
		writeError(ctx, err)
		return
	}
	sessionDir := filepath.Join(s.cfg.OutputRoot, sessionInfo.ChannelID, sessionInfo.Slug)
	recapDir := filepath.Join(sessionDir, "recap")

	result := gin.H{
		"available":    false,
		"markdown":     "",
		"prompt":       "",
		"raw_response": "",
	}

	// Try exact path first, then fallback to glob
	fileBase := safeRecapName("直播回顾_" + sessionInfo.Slug)
	mdPath := filepath.Join(recapDir, fileBase+".md")
	promptPath := filepath.Join(recapDir, "live-recap.prompt.md")
	rawPath := filepath.Join(recapDir, "live-recap.raw.json")

	// Read markdown - try exact, then glob
	mdContent := readFileOrNil(mdPath)
	if mdContent == nil {
		if matches, _ := filepath.Glob(filepath.Join(recapDir, "直播回顾_*.md")); len(matches) > 0 {
			mdContent = readFileOrNil(matches[0])
		}
	}

	if mdContent != nil {
		result["available"] = true
		result["markdown"] = string(mdContent)
		result["prompt"] = string(readFileOrNil(promptPath))
		result["raw_response"] = string(readFileOrNil(rawPath))
	}

	suggPath := filepath.Join(recapDir, "suggested_terms.json")
	if data, err := os.ReadFile(suggPath); err == nil {
		var terms []string
		if json.Unmarshal(data, &terms) == nil {
			result["suggested_terms"] = terms
		}
	}
	ctx.JSON(http.StatusOK, result)
}

func readFileOrNil(path string) []byte {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return data
}

func safeRecapName(value string) string {
	return strings.NewReplacer("/", "_", "\\", "_", " ", "_").Replace(value)
}

func (s *Server) recapAvailable() bool {
	status := s.currentRuntimeStatus()
	return status == nil || status.Capabilities.RecapGenerate
}

func (s *Server) uploadSession(ctx *gin.Context) {
	status := s.currentRuntimeStatus()
	if status != nil && !status.Capabilities.WebDAVUpload {
		ctx.JSON(http.StatusConflict, gin.H{
			"error":  "webdav capability unavailable",
			"reason": status.Capabilities.Reason,
		})
		return
	}
	task, err := s.uploads.CreateTask(ctx.Request.Context(), s.workerPool, ctx.Param("sid"))
	if err != nil {
		writeError(ctx, err)
		return
	}
	ctx.JSON(http.StatusAccepted, task)
}

func (s *Server) fetchSession(ctx *gin.Context) {
	status := s.currentRuntimeStatus()
	if status != nil && !status.Capabilities.WebDAVUpload {
		ctx.JSON(http.StatusConflict, gin.H{
			"error":  "webdav capability unavailable",
			"reason": status.Capabilities.Reason,
		})
		return
	}
	sessionInfo, err := s.uploads.Fetch(ctx.Request.Context(), ctx.Param("sid"))
	if err != nil {
		writeError(ctx, err)
		return
	}
	ctx.JSON(http.StatusAccepted, gin.H{"session": sessionInfo})
}

func (s *Server) webDAVAvailable() bool {
	status := s.currentRuntimeStatus()
	return status == nil || status.Capabilities.WebDAVUpload
}

// archiveSession 手动归档已发布场次到 WebDAV（自动归档失败时的手动重试入口）。
// 复用 WebDAV 能力守卫（归档与上传同一 WebDAV 后端）。
func (s *Server) archiveSession(ctx *gin.Context) {
	status := s.currentRuntimeStatus()
	if status != nil && !status.Capabilities.WebDAVUpload {
		ctx.JSON(http.StatusConflict, gin.H{
			"error":  "webdav capability unavailable",
			"reason": status.Capabilities.Reason,
		})
		return
	}
	task, err := s.archives.CreateTask(ctx.Request.Context(), s.workerPool, ctx.Param("sid"))
	if err != nil {
		writeError(ctx, err)
		return
	}
	ctx.JSON(http.StatusAccepted, task)
}

func (s *Server) publishSession(ctx *gin.Context) {
	status := s.currentRuntimeStatus()
	if status != nil && !status.Capabilities.PublishOpus {
		ctx.JSON(http.StatusConflict, gin.H{
			"error":  "publish capability unavailable",
			"reason": status.Capabilities.Reason,
		})
		return
	}
	task, err := s.publisher.CreateTask(ctx.Request.Context(), s.workerPool, ctx.Param("sid"))
	if err != nil {
		writeError(ctx, err)
		return
	}
	ctx.JSON(http.StatusAccepted, task)
}

func (s *Server) listTasks(ctx *gin.Context) {
	tasks, err := s.workerPool.Store().List(ctx.Request.Context())
	if err != nil {
		writeError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"items": tasks})
}

func (s *Server) getTask(ctx *gin.Context) {
	task, err := s.workerPool.Store().Get(ctx.Request.Context(), ctx.Param("id"))
	if err != nil {
		writeError(ctx, err)
		return
	}
	resp := gin.H{"task": task}
	if task.Status == worker.StatusFailed && task.Error != "" {
		resp["friendly_error"] = worker.GetFriendlyError(task.Type, task.Error)
	}
	if task.Status == worker.StatusFailed {
		if worker.ShouldAutoRetry(s.cfg, task.Type, task.Attempt) {
			resp["auto_retry"] = map[string]interface{}{
				"scheduled":    true,
				"attempt":      task.Attempt,
				"max_attempts": s.cfg.Worker.MaxRetryAttempts,
			}
		}
	}
	ctx.JSON(http.StatusOK, resp)
}

func (s *Server) retryTask(ctx *gin.Context) {
	task, err := s.workerPool.Retry(ctx.Request.Context(), ctx.Param("id"))
	if err != nil {
		writeError(ctx, err)
		return
	}
	ctx.JSON(http.StatusAccepted, task)
}

func (s *Server) cancelTask(ctx *gin.Context) {
	task, err := s.workerPool.Cancel(ctx.Request.Context(), ctx.Param("id"))
	if err != nil {
		writeError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, task)
}

func (s *Server) deleteTask(ctx *gin.Context) {
	if err := s.workerPool.Store().Delete(ctx.Request.Context(), ctx.Param("id")); err != nil {
		writeError(ctx, err)
		return
	}
	ctx.Status(http.StatusNoContent)
}

func (s *Server) deleteFailedTasks(ctx *gin.Context) {
	deleted, err := s.workerPool.Store().DeleteByStatus(ctx.Request.Context(), worker.StatusFailed)
	if err != nil {
		writeError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"deleted": deleted})
}

func (s *Server) deleteSession(ctx *gin.Context) {
	sid := ctx.Param("sid")
	sessionInfo, err := s.sessions.Get(ctx.Request.Context(), sid)
	if err != nil {
		writeError(ctx, err)
		return
	}
	if err := s.workerPool.Store().DeleteBySession(ctx.Request.Context(), sessionInfo.ID); err != nil {
		writeError(ctx, err)
		return
	}
	if err := s.sessions.Delete(ctx.Request.Context(), sid); err != nil {
		writeError(ctx, err)
		return
	}
	ctx.Status(http.StatusNoContent)
}

func (s *Server) deleteFailedSessions(ctx *gin.Context) {
	// 先删除失败场次关联的所有任务（不限任务状态）
	if err := s.workerPool.Store().DeleteByFailedSessions(ctx.Request.Context()); err != nil {
		writeError(ctx, err)
		return
	}
	deleted, err := s.sessions.DeleteFailed(ctx.Request.Context())
	if err != nil {
		writeError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"deleted": deleted})
}

func (s *Server) websocket(ctx *gin.Context) {
	conn, err := s.upgrader.Upgrade(ctx.Writer, ctx.Request, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	events := s.workerPool.Hub().Subscribe()
	defer s.workerPool.Hub().Unsubscribe(events)

	for {
		select {
		case event, ok := <-events:
			if !ok {
				return
			}
			if err := conn.WriteJSON(event); err != nil {
				return
			}
		case <-ctx.Request.Context().Done():
			return
		}
	}
}

func (s *Server) handleNotifyTest(ctx *gin.Context) {
	if s.notifyMgr == nil {
		ctx.JSON(http.StatusConflict, gin.H{"error": "notify not configured"})
		return
	}
	s.notifyMgr.Send(ctx.Request.Context(), "test", "Hikami-Go 通知测试", "如果你收到这条消息，说明通知配置正确")
	ctx.JSON(http.StatusOK, gin.H{"message": "测试通知已发送"})
}

func writeError(ctx *gin.Context, err error) {
	switch {
	case errors.Is(err, channel.ErrInvalid):
		writeBadRequest(ctx, err.Error())
	case errors.Is(err, channel.ErrNotFound):
		ctx.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case errors.Is(err, channel.ErrDuplicate):
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, channel.ErrInUse):
		ctx.JSON(http.StatusConflict, gin.H{"error": "该主播仍有关联数据，请先删除相关场次后再删除主播"})
	case errors.Is(err, biliutil.ErrQRLoginSessionNotFound):
		ctx.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case errors.Is(err, biliutil.ErrQRLoginSessionExpired):
		ctx.JSON(http.StatusGone, gin.H{"error": err.Error()})
	case errors.Is(err, biliutil.ErrQRLoginNotSucceeded):
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, biliutil.ErrCookieMissing):
		ctx.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
	case errors.Is(err, biliutil.ErrBiliLoginUpstream):
		ctx.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
	case errors.Is(err, biliutil.ErrAccountNotFound):
		ctx.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case errors.Is(err, biliutil.ErrAccountUIDDuplicate):
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, biliutil.ErrNoDefaultAccount):
		ctx.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case errors.Is(err, biliutil.ErrInvalidCookiePath):
		writeBadRequest(ctx, err.Error())
	case errors.Is(err, worker.ErrInvalidTask):
		writeBadRequest(ctx, err.Error())
	case errors.Is(err, worker.ErrTaskNotFound):
		ctx.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case errors.Is(err, worker.ErrTaskConflict):
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, session.ErrNotFound):
		ctx.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case errors.Is(err, session.ErrInvalid):
		writeBadRequest(ctx, err.Error())
	case errors.Is(err, glossary.ErrInvalidJSON):
		writeBadRequest(ctx, err.Error())
	case errors.Is(err, asr.ErrSessionNotReady):
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, asr.ErrAudioMissing):
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, recap.ErrSessionNotReady):
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, recap.ErrTranscriptMissing):
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, recap.ErrRecapUnavailable):
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, upload.ErrSessionNotReady):
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, upload.ErrArchiveMissing):
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, upload.ErrConfigMissing):
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, archive.ErrSessionNotReady):
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, archive.ErrArchiveMissing):
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, archive.ErrConfigMissing):
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, live_record.ErrLiveDisabled):
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, live_record.ErrChannelNotRecordable):
		writeBadRequest(ctx, err.Error())
	case errors.Is(err, live_record.ErrAlreadyRecording):
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, live_record.ErrNotRecording):
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, live_record.ErrNotLive):
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, publisher.ErrSessionNotReady):
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, publisher.ErrRecapMissing):
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, publisher.ErrChannelNoCookieFile):
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, publisher.ErrCookieMissing):
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, publisher.ErrCookieExpired):
		ctx.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
	case errors.Is(err, publisher.ErrContentRejected):
		ctx.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
	case errors.Is(err, publisher.ErrRateLimited):
		ctx.JSON(http.StatusTooManyRequests, gin.H{"error": err.Error()})
	case errors.Is(err, publisher.ErrBilibiliAPI):
		ctx.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
	case errors.Is(err, publisher.ErrPublishNotEnabled):
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, publisher.ErrNotPublished):
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, publisher.ErrNotOwner):
		ctx.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
	case errors.Is(err, glossary.ErrNotFound):
		ctx.JSON(http.StatusNotFound, gin.H{"error": "not found", "reason": err.Error()})
	case errors.Is(err, glossary.ErrDuplicate):
		ctx.JSON(http.StatusConflict, gin.H{"error": "duplicate", "reason": err.Error()})
	case errors.Is(err, glossary.ErrCandidateNotFound):
		ctx.JSON(http.StatusNotFound, gin.H{"error": "not found", "reason": err.Error()})
	case errors.Is(err, glossary.ErrInvalidCandidate):
		writeBadRequest(ctx, err.Error())
	case errors.Is(err, recap.ErrTemplateNotFound):
		ctx.JSON(http.StatusNotFound, gin.H{"error": "not found", "reason": err.Error()})
	case errors.Is(err, recap.ErrTemplateBuiltIn):
		ctx.JSON(http.StatusForbidden, gin.H{"error": "cannot delete built-in template"})
	default:
		slog.Error("unhandled error", "error", err)
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
	}
}

func writeBadRequest(ctx *gin.Context, message string) {
	ctx.JSON(http.StatusBadRequest, gin.H{"error": message})
}

func validateWebDAVURL(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	parsed, err := url.ParseRequestURI(value)
	if err != nil {
		return fmt.Errorf("webdav url must be a valid http or https url")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("webdav url must use http or https")
	}
	if parsed.Host == "" {
		return fmt.Errorf("webdav url must include host")
	}
	return nil
}

func validateWebDAVBasePath(value string) error {
	if strings.Contains(value, "..") {
		return fmt.Errorf("webdav base_path must not contain ..")
	}
	if strings.Contains(value, `\`) {
		return fmt.Errorf("webdav base_path must not contain backslash")
	}
	if containsControlCharacter(value) {
		return fmt.Errorf("webdav base_path must not contain control characters")
	}
	return nil
}

func validateWebDAVRemote(value string) error {
	if containsControlCharacter(value) {
		return fmt.Errorf("webdav remote must not contain control characters")
	}
	value = strings.TrimSpace(value)
	if value != "" && !strings.Contains(value, ":") {
		return fmt.Errorf("webdav remote must contain ':'")
	}
	return nil
}

func containsControlCharacter(value string) bool {
	for _, r := range value {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

func parseOptionalTime(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339, value)
}

type sessionFile struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

func listSessionFiles(outputRoot string, sessionInfo session.Session) []sessionFile {
	root := filepath.Join(outputRoot, sessionInfo.ChannelID, sessionInfo.Slug)
	var files []sessionFile
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		files = append(files, sessionFile{Path: filepath.ToSlash(relative), Size: info.Size()})
		return nil
	})
	if files == nil {
		return []sessionFile{}
	}
	return files
}

func (s *Server) listSecrets(ctx *gin.Context) {
	// 走 Effective* 兜底:env 名留空时 KnownKeys 用默认值,与运行时/probe 一致(codex 审核低[6])。
	knownKeys := secrets.KnownKeys(s.cfg.DashScope.EffectiveAPIKeyEnv(), s.cfg.RecapAI.EffectiveAPIKeyEnv(), s.cfg.ASRS3.EffectiveAccessKeyEnv(), s.cfg.WebDAV.EffectivePasswordEnv())
	dbSecrets, err := s.secrets.List(ctx.Request.Context())
	if err != nil {
		writeError(ctx, err)
		return
	}
	dbMap := map[string]secrets.Secret{}
	for _, sec := range dbSecrets {
		dbMap[sec.Key] = sec
	}

	var items []secrets.SecretView
	for _, key := range knownKeys {
		dbVal := ""
		updatedAt := ""
		if sec, ok := dbMap[key]; ok {
			dbVal = sec.Value
			updatedAt = sec.UpdatedAt
		}
		view := secrets.BuildView(key, dbVal)
		view.UpdatedAt = updatedAt
		items = append(items, view)
	}
	if items == nil {
		items = []secrets.SecretView{}
	}
	ctx.JSON(http.StatusOK, gin.H{"items": items})
}

// validateEnvKeyName 校验环境变量名合法(字母/数字/下划线,首字符非数字,长度 1-128)。
// 空值由 Effective* 兜底,不报错。
func validateEnvKeyName(name, field string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	if len(name) > 128 {
		return fmt.Errorf("%s too long (max 128)", field)
	}
	for i, c := range name {
		ok := c == '_' ||
			(c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') ||
			(i > 0 && c >= '0' && c <= '9')
		if !ok {
			return fmt.Errorf("%s must match [A-Za-z_][A-Za-z0-9_]*", field)
		}
	}
	return nil
}

// derefStr 安全解引用 *string,空指针返回 ""。
func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// derefBool 安全解引用 *bool,空指针返回 false。
func derefBool(p *bool) bool {
	if p == nil {
		return false
	}
	return *p
}

// applySecretEnvChange 处理"改 env 名 + 设置/清除密钥"的组合,避免孤儿 secret。
// oldEnv/newEnv 为旧/新 env 名(均已 Effective* 兜底非空)。
//
// 优先级(codex 审核修正:newSecret 最高,避免改名+输入新 key+无旧 secret 时丢失):
//   - clear=true:删 newEnv 对应 secret + unset env(env 改名时也清新旧两个)
//   - newSecret 非空(且未 clear):写 newEnv secret + setenv(无论是否有旧 secret)
//   - env 改名但未提供新 key:迁移旧 secret 到新 key(若旧有值)
//   - 其他:不动(保留语义)
func (s *Server) applySecretEnvChange(ctx context.Context, oldEnv, newEnv, newSecret string, clear bool) error {
	// env 名变化:先把旧值迁移到新 key,避免改个 env 名就导致密钥"丢失"
	if oldEnv != newEnv {
		// newSecret 优先级最高:用户输入新值就直接写新 key,不依赖旧 secret 是否存在
		if newSecret != "" && !clear {
			if err := s.secrets.Set(ctx, newEnv, newSecret); err != nil {
				return err
			}
			_ = os.Setenv(newEnv, newSecret)
		} else if oldSecret, err := s.secrets.Get(ctx, oldEnv); err == nil && oldSecret != "" && !clear {
			// 未提供新值但有旧值:迁移到新 key
			if err := s.secrets.Set(ctx, newEnv, oldSecret); err != nil {
				return err
			}
			_ = os.Setenv(newEnv, oldSecret)
		}
		// 清旧 key + 旧 env(无论是否迁移,避免孤儿 secret)
		if err := s.secrets.Delete(ctx, oldEnv); err != nil {
			return err
		}
		_ = os.Unsetenv(oldEnv)
		if clear {
			if err := s.secrets.Delete(ctx, newEnv); err != nil {
				return err
			}
			_ = os.Unsetenv(newEnv)
		}
		return nil
	}
	// env 名未变,走标准三态
	if clear {
		if err := s.secrets.Delete(ctx, newEnv); err != nil {
			return err
		}
		_ = os.Unsetenv(newEnv)
	} else if newSecret != "" {
		if err := s.secrets.Set(ctx, newEnv, newSecret); err != nil {
			return err
		}
		_ = os.Setenv(newEnv, newSecret)
	}
	return nil
}

// envMutation 描述 applySecretEnvChangeTx 在事务内对 secrets 表做的、
// 需要在事务 commit 成功后再反映到进程 env 的副作用。
// 语义:commit 成功后,对 setEnv 调 os.Setenv、对 unsetEnvs 逐个 os.Unsetenv。
type envMutation struct {
	setEnv    map[string]string // env名 -> 值(非空才 Setenv)
	unsetEnvs []string          // 需 Unsetenv 的 env名
}

// applySecretEnvChangeTx 是 applySecretEnvChange 的事务版:把 secrets 的读写全部放进
// 同一 *sql.Tx(与 runtimeconfig.SaveTx 共享),commit 后调用方再按返回的 envMutation
// 更新进程 env + 写 cfg 内存。保证「密钥写入 + 配置段写入」原子(r11 [High]),
// 且 env rename 的旧值读取也在事务内(r12 [Medium] GetTx)。
func (s *Server) applySecretEnvChangeTx(ctx context.Context, tx *sql.Tx, oldEnv, newEnv, newSecret string, clear bool) (envMutation, error) {
	mut := envMutation{setEnv: map[string]string{}}
	// env 名变化:迁移旧值到新 key(避免孤儿)
	if oldEnv != newEnv {
		if newSecret != "" && !clear {
			if err := s.secrets.SetTx(ctx, tx, newEnv, newSecret); err != nil {
				return mut, err
			}
			mut.setEnv[newEnv] = newSecret
		} else if !clear {
			// 未提供新值时迁移旧值；GetTx 已把 ErrNoRows 转成 ("",nil)，
			// 其它读取错误必须显式返回，避免静默跳过迁移后 DeleteTx 丢失 secret（codex r16 [Medium]）。
			oldSecret, err := s.secrets.GetTx(ctx, tx, oldEnv)
			if err != nil {
				return mut, err
			}
			if oldSecret != "" {
				if err := s.secrets.SetTx(ctx, tx, newEnv, oldSecret); err != nil {
					return mut, err
				}
				mut.setEnv[newEnv] = oldSecret
			}
		}
		if err := s.secrets.DeleteTx(ctx, tx, oldEnv); err != nil {
			return mut, err
		}
		mut.unsetEnvs = append(mut.unsetEnvs, oldEnv)
		if clear {
			if err := s.secrets.DeleteTx(ctx, tx, newEnv); err != nil {
				return mut, err
			}
			mut.unsetEnvs = append(mut.unsetEnvs, newEnv)
		}
		return mut, nil
	}
	// env 名未变,走标准三态
	if clear {
		if err := s.secrets.DeleteTx(ctx, tx, newEnv); err != nil {
			return mut, err
		}
		mut.unsetEnvs = append(mut.unsetEnvs, newEnv)
	} else if newSecret != "" {
		if err := s.secrets.SetTx(ctx, tx, newEnv, newSecret); err != nil {
			return mut, err
		}
		mut.setEnv[newEnv] = newSecret
	}
	return mut, nil
}

// applyEnvMutation 在事务 commit 成功后,把 envMutation 反映到进程 env。
// 失败仅记录:DB 已是真相,env 滞后可由下次保存/重启 LoadIntoEnv 自愈。
func applyEnvMutation(mut envMutation) {
	for k, v := range mut.setEnv {
		_ = os.Setenv(k, v)
	}
	for _, k := range mut.unsetEnvs {
		_ = os.Unsetenv(k)
	}
}

// persistSectionTx 在事务内写单个 section 的 DTO(JSON)。供不含密钥的 handler
// (Publish/Archive)使用:WithTx 仅包一次 SaveTx。含密钥的 handler 自行在 WithTx 内
// 同时调 applySecretEnvChangeTx + SaveTx。data 必须是可 json.Marshal 的 DTO。
func (s *Server) persistSectionTx(ctx context.Context, tx *sql.Tx, section string, dto interface{}) error {
	b, err := json.Marshal(dto)
	if err != nil {
		return fmt.Errorf("marshal %s section: %w", section, err)
	}
	return s.runtimeCfg.SaveTx(ctx, tx, section, b)
}

func (s *Server) updateSecret(ctx *gin.Context) {
	key := ctx.Param("key")
	// 走 Effective* 兜底,与 listSecrets/运行时一致(codex 审核低[6])。
	knownKeys := secrets.KnownKeys(s.cfg.DashScope.EffectiveAPIKeyEnv(), s.cfg.RecapAI.EffectiveAPIKeyEnv(), s.cfg.ASRS3.EffectiveAccessKeyEnv(), s.cfg.WebDAV.EffectivePasswordEnv())
	if err := secrets.ValidateKey(key, knownKeys); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// ASR S3 access key 与 WebDAV password 有明文兜底 + tombstone 语义：
	// 必须经各自分段配置接口（updateASRS3Config/updateWebDAVConfig）增删改，那里会在
	// 同一事务写 access_key_managed/password_managed。通用 /api/secrets/:key 不写 tombstone，
	// 若放行清除会导致 managed=false → Effective* 仍回落 config.yaml 明文（codex r16 [High]）。
	// DashScope/Recap 的 api_key 无明文字段，走本接口安全。
	if key == s.cfg.ASRS3.EffectiveAccessKeyEnv() || key == s.cfg.WebDAV.EffectivePasswordEnv() {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "this secret is managed by its section config endpoint; update it via the corresponding settings card"})
		return
	}

	var input struct {
		Value string `json:"value"`
	}
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid json body"})
		return
	}

	if input.Value == "" {
		if err := s.secrets.Delete(ctx.Request.Context(), key); err != nil {
			writeError(ctx, err)
			return
		}
		os.Unsetenv(key)
	} else {
		if err := s.secrets.Set(ctx.Request.Context(), key, input.Value); err != nil {
			writeError(ctx, err)
			return
		}
		os.Setenv(key, input.Value)
	}
	s.bumpConfigGen()

	cfgSnapshot, gen := s.configSnapshot()
	s.refreshRuntimeStatus(cfgSnapshot, gen)

	dbVal, err := s.secrets.Get(ctx.Request.Context(), key)
	if err != nil {
		writeError(ctx, err)
		return
	}
	view := secrets.BuildView(key, dbVal)
	ctx.JSON(http.StatusOK, view)
}

type publishConfigResponse struct {
	Enabled         bool   `json:"enabled"`
	Mode            string `json:"mode"`
	CategoryID      int    `json:"category_id"`
	ListID          int    `json:"list_id"`
	PrivatePub      int    `json:"private_pub"`
	SummaryLen      int    `json:"summary_len"`
	Original        int    `json:"original"`
	Aigc            int    `json:"aigc"`
	TimerPubTime    int64  `json:"timer_pub_time"`
	CoverURL        string `json:"cover_url"`
	AutoCover       bool   `json:"auto_cover"`
	Topics          string `json:"topics"`
	TopicID         int    `json:"topic_id"`
	TopicName       string `json:"topic_name"`
	CloseComment    int    `json:"close_comment"`
	UpChooseComment int    `json:"up_choose_comment"`
}

func newPublishConfigResponse(p config.PublishConfig) publishConfigResponse {
	return publishConfigResponse{
		Enabled:         p.Enabled,
		Mode:            p.Mode,
		CategoryID:      p.CategoryID,
		ListID:          p.ListID,
		PrivatePub:      p.PrivatePub,
		SummaryLen:      p.SummaryLen,
		Original:        p.Original,
		Aigc:            p.Aigc,
		TimerPubTime:    p.TimerPubTime,
		CoverURL:        p.CoverURL,
		AutoCover:       p.AutoCover,
		Topics:          p.Topics,
		TopicID:         p.TopicID,
		TopicName:       p.TopicName,
		CloseComment:    p.CloseComment,
		UpChooseComment: p.UpChooseComment,
	}
}

func (s *Server) getPublishConfig(ctx *gin.Context) {
	s.publishMu.RLock()
	resp := newPublishConfigResponse(s.cfg.Publish)
	s.publishMu.RUnlock()
	ctx.JSON(http.StatusOK, resp)
}

func (s *Server) updatePublishConfig(ctx *gin.Context) {
	var input struct {
		Enabled         *bool   `json:"enabled"`
		Mode            *string `json:"mode"`
		CategoryID      *int    `json:"category_id"`
		ListID          *int    `json:"list_id"`
		PrivatePub      *int    `json:"private_pub"`
		SummaryLen      *int    `json:"summary_len"`
		Original        *int    `json:"original"`
		Aigc            *int    `json:"aigc"`
		TimerPubTime    *int64  `json:"timer_pub_time"`
		CoverURL        *string `json:"cover_url"`
		AutoCover       *bool   `json:"auto_cover"`
		Topics          *string `json:"topics"`
		TopicID         *int    `json:"topic_id"`
		TopicName       *string `json:"topic_name"`
		CloseComment    *int    `json:"close_comment"`
		UpChooseComment *int    `json:"up_choose_comment"`
	}
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid json body"})
		return
	}

	// Validate before applying
	if input.Mode != nil && *input.Mode != "" && *input.Mode != "draft" && *input.Mode != "publish" {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "mode must be 'draft' or 'publish'"})
		return
	}
	if input.SummaryLen != nil && *input.SummaryLen < 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "summary_len must be >= 0"})
		return
	}
	// private_pub:B 站专栏可见范围,1=仅自己可见、2=公开(默认)。
	// 全局配置段没有"继承上层"的语义(区别于频道级 PublishPrivatePub 用 0 表示"继承全局"),
	// 因此全局 0 无意义 —— 规范化为 viper 默认 2(公开),保证:
	//   ① GET/PUT round-trip 幂等(GET 拿到的值原样 PUT 回去必然通过校验);
	//   ② publisher 永远不会收到 0(publisher.go:62 的 fallback 把频道级 0 回落到本全局值,
	//      若全局也是 0 会原样发给 B 站专栏 API 导致发布失败)。
	if input.PrivatePub != nil && *input.PrivatePub != 1 && *input.PrivatePub != 2 {
		if *input.PrivatePub == 0 {
			two := 2
			input.PrivatePub = &two
		} else {
			ctx.JSON(http.StatusBadRequest, gin.H{"error": "private_pub must be 1 or 2"})
			return
		}
	}
	if input.Original != nil && *input.Original != 0 && *input.Original != 1 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "original must be 0 or 1"})
		return
	}
	if input.Aigc != nil && *input.Aigc != 0 && *input.Aigc != 1 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "aigc must be 0 or 1"})
		return
	}
	if input.CloseComment != nil && *input.CloseComment != 0 && *input.CloseComment != 1 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "close_comment must be 0 or 1"})
		return
	}
	if input.UpChooseComment != nil && *input.UpChooseComment != 0 && *input.UpChooseComment != 1 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "up_choose_comment must be 0 or 1"})
		return
	}
	if input.TimerPubTime != nil && *input.TimerPubTime > 0 {
		now := time.Now().Unix()
		if *input.TimerPubTime < now+7200 || *input.TimerPubTime > now+7*86400 {
			ctx.JSON(http.StatusBadRequest, gin.H{"error": "timer_pub_time must be between 2 hours and 7 days from now"})
			return
		}
	}

	s.publishMu.Lock()
	// 基于【当前】配置 patch 出下一状态(仍持锁,无并发丢更新)。
	nextPublish := s.cfg.Publish
	if input.Enabled != nil {
		nextPublish.Enabled = *input.Enabled
	}
	if input.Mode != nil {
		nextPublish.Mode = *input.Mode
	}
	if input.CategoryID != nil {
		nextPublish.CategoryID = *input.CategoryID
	}
	if input.ListID != nil {
		nextPublish.ListID = *input.ListID
	}
	if input.PrivatePub != nil {
		nextPublish.PrivatePub = *input.PrivatePub
	}
	if input.SummaryLen != nil {
		nextPublish.SummaryLen = *input.SummaryLen
	}
	if input.Original != nil {
		nextPublish.Original = *input.Original
	}
	if input.Aigc != nil {
		nextPublish.Aigc = *input.Aigc
	}
	if input.TimerPubTime != nil {
		nextPublish.TimerPubTime = *input.TimerPubTime
	}
	if input.CoverURL != nil {
		nextPublish.CoverURL = strings.TrimSpace(*input.CoverURL)
	}
	if input.AutoCover != nil {
		nextPublish.AutoCover = *input.AutoCover
	}
	if input.Topics != nil {
		nextPublish.Topics = strings.TrimSpace(*input.Topics)
	}
	if input.TopicID != nil {
		nextPublish.TopicID = *input.TopicID
	}
	if input.TopicName != nil {
		nextPublish.TopicName = *input.TopicName
	}
	if input.CloseComment != nil {
		nextPublish.CloseComment = *input.CloseComment
	}
	if input.UpChooseComment != nil {
		nextPublish.UpChooseComment = *input.UpChooseComment
	}
	// 构造完整下一状态 DTO(presence-aware,所有字段非 nil),持久化到 runtime_settings。
	dto := publishConfigToDTO(nextPublish)
	// 持久化成功后再提交内存:WithTx 包一次 SaveTx,失败 500 不改内存(env/cfg 不变)。
	if err := runtimeconfig.WithTx(ctx.Request.Context(), s.runtimeCfg.DB(), func(tx *sql.Tx) error {
		return s.persistSectionTx(ctx.Request.Context(), tx, "publish", dto)
	}); err != nil {
		s.publishMu.Unlock()
		slog.Warn("persist publish config failed", "error", err)
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "failed to persist publish config"})
		return
	}
	s.cfg.Publish = nextPublish
	resp := newPublishConfigResponse(s.cfg.Publish)
	cfgSnapshot := *s.cfg
	gen := s.bumpConfigGen()
	s.publishMu.Unlock()

	s.refreshRuntimeStatus(cfgSnapshot, gen)

	ctx.JSON(http.StatusOK, resp)
}

// publishConfigToDTO 把 PublishConfig 转成完整下一状态 DTO(所有指针非 nil,
// 保证 runtime_settings 存的是完整快照而非 PATCH,避免 PATCH 把未传字段写成零值)。
func publishConfigToDTO(p config.PublishConfig) config.PublishSectionDTO {
	return config.PublishSectionDTO{
		Enabled:         &p.Enabled,
		Mode:            &p.Mode,
		CategoryID:      &p.CategoryID,
		ListID:          &p.ListID,
		PrivatePub:      &p.PrivatePub,
		SummaryLen:      &p.SummaryLen,
		Original:        &p.Original,
		Aigc:            &p.Aigc,
		TimerPubTime:    &p.TimerPubTime,
		CoverURL:        &p.CoverURL,
		AutoCover:       &p.AutoCover,
		Topics:          &p.Topics,
		TopicID:         &p.TopicID,
		TopicName:       &p.TopicName,
		CloseComment:    &p.CloseComment,
		UpChooseComment: &p.UpChooseComment,
	}
}

// --- ASR S3 (对象存储) config handlers ---

type asrS3ConfigResponse struct {
	Endpoint        string `json:"endpoint"`
	Bucket          string `json:"bucket"`
	AccessKeyID     string `json:"access_key_id"`
	AccessKeyEnv    string `json:"access_key_env"`
	Region          string `json:"region"`
	PublicURLPrefix string `json:"public_url_prefix"`
	UsePathStyle    bool   `json:"use_path_style"`
	AccessKeySet    bool   `json:"access_key_set"`
}

// newASRS3ConfigResponse 返回 ASR S3 配置响应。
// access_key_env 经 EffectiveAccessKeyEnv 兜底;access_key_secret 永不返回明文,只返回 access_key_set。
// access_key_set 基于 EffectiveAccessKey()(managed 时不回落 yaml 明文),与运行时一致(r12 Effective* 闭环)。
func newASRS3ConfigResponse(c config.ASRS3Config) asrS3ConfigResponse {
	return asrS3ConfigResponse{
		Endpoint:        c.Endpoint,
		Bucket:          c.Bucket,
		AccessKeyID:     c.AccessKeyID,
		AccessKeyEnv:    c.EffectiveAccessKeyEnv(),
		Region:          c.Region,
		PublicURLPrefix: c.PublicURLPrefix,
		UsePathStyle:    c.UsePathStyle,
		AccessKeySet:    c.EffectiveAccessKey() != "",
	}
}

// validateASRS3Endpoint 校验 ASR S3 endpoint 必须为合法 http(s) url 且含 host,禁含 .. 和反斜杠。
func validateASRS3Endpoint(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil // 空值允许(未配置对象存储,走其他后端)
	}
	if strings.Contains(value, "\\") || strings.Contains(value, "..") {
		return fmt.Errorf("asr_s3 endpoint must not contain backslash or ..")
	}
	u, err := url.Parse(value)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("asr_s3 endpoint must be a valid http(s) url with host")
	}
	return nil
}

// validateASRS3PublicURLPrefix 校验公网 URL 前缀必须为合法 http(s) url 且含 host。
func validateASRS3PublicURLPrefix(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if strings.Contains(value, "\\") {
		return fmt.Errorf("asr_s3 public_url_prefix must not contain backslash")
	}
	u, err := url.Parse(value)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("asr_s3 public_url_prefix must be a valid http(s) url with host")
	}
	return nil
}

func (s *Server) getASRS3Config(ctx *gin.Context) {
	s.publishMu.RLock()
	resp := newASRS3ConfigResponse(s.cfg.ASRS3)
	s.publishMu.RUnlock()
	ctx.JSON(http.StatusOK, resp)
}

func (s *Server) updateASRS3Config(ctx *gin.Context) {
	var input struct {
		Endpoint        *string `json:"endpoint"`
		Bucket          *string `json:"bucket"`
		AccessKeyID     *string `json:"access_key_id"`
		AccessKeySecret *string `json:"access_key_secret"`
		AccessKeyEnv    *string `json:"access_key_env"`
		Region          *string `json:"region"`
		PublicURLPrefix *string `json:"public_url_prefix"`
		UsePathStyle    *bool   `json:"use_path_style"`
		ClearSecret     *bool   `json:"clear_secret"`
	}
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid json body"})
		return
	}
	if input.Endpoint != nil {
		if err := validateASRS3Endpoint(*input.Endpoint); err != nil {
			ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}
	if input.PublicURLPrefix != nil {
		if err := validateASRS3PublicURLPrefix(*input.PublicURLPrefix); err != nil {
			ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}
	if input.AccessKeyEnv != nil {
		if err := validateEnvKeyName(*input.AccessKeyEnv, "access_key_env"); err != nil {
			ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}

	// 整体串行化:从读 oldEnv 到 secret 迁移再到写 cfg 全程持锁,
	// 保证 env 改名 + secret 迁移原子完成,避免并发 PUT 产生孤儿(与 DashScope/Recap 一致)。
	// 密钥走 secrets.Store:写 env + secrets.Set,而非写 cfg.AccessKeySecret 字段,
	// 这样密钥持久化到数据库,重启不丢;运行时经 EffectiveAccessKey() 读 env。
	s.publishMu.Lock()

	oldEnv := s.cfg.ASRS3.EffectiveAccessKeyEnv()
	newEnv := oldEnv
	if input.AccessKeyEnv != nil {
		trimmed := strings.TrimSpace(*input.AccessKeyEnv)
		if trimmed == "" {
			newEnv = "ASR_S3_ACCESS_KEY_SECRET" // 清空 = 用默认
		} else {
			newEnv = trimmed
		}
	}
	// tombstone 状态机(r13 [High]):显式密钥操作(设/清/改 env 名)升 managed=true;
	// 非密钥字段改动保持当前 managed(可能为 true,不回退)。
	explicitSecretOp := input.AccessKeySecret != nil || derefBool(input.ClearSecret) || input.AccessKeyEnv != nil
	nextManaged := s.cfg.ASRS3.AccessKeyManaged() || explicitSecretOp

	// 字段提交:基于【当前】配置 patch(此时仍持锁,无并发丢更新)。
	nextCfg := s.cfg.ASRS3
	if input.Endpoint != nil {
		nextCfg.Endpoint = strings.TrimSpace(*input.Endpoint)
	}
	if input.Bucket != nil {
		nextCfg.Bucket = strings.TrimSpace(*input.Bucket)
	}
	if input.AccessKeyID != nil {
		nextCfg.AccessKeyID = strings.TrimSpace(*input.AccessKeyID)
	}
	if input.Region != nil {
		nextCfg.Region = strings.TrimSpace(*input.Region)
	}
	if input.PublicURLPrefix != nil {
		nextCfg.PublicURLPrefix = strings.TrimSpace(*input.PublicURLPrefix)
	}
	if input.UsePathStyle != nil {
		nextCfg.UsePathStyle = *input.UsePathStyle
	}
	if input.AccessKeyEnv != nil {
		nextCfg.AccessKeyEnv = strings.TrimSpace(*input.AccessKeyEnv)
	}
	// 注意:不写 nextCfg.AccessKeySecret —— 密钥走 secrets + env,由 EffectiveAccessKey() 读取。
	dto := asrs3ConfigToDTO(nextCfg, nextManaged)

	// 原子:secrets 写入 + runtime_settings 写入放同一事务(r11 [High]),commit 后才改 env/cfg。
	var mut envMutation
	err := runtimeconfig.WithTx(ctx.Request.Context(), s.runtimeCfg.DB(), func(tx *sql.Tx) error {
		var secretErr error
		mut, secretErr = s.applySecretEnvChangeTx(ctx.Request.Context(), tx,
			oldEnv, newEnv, derefStr(input.AccessKeySecret), derefBool(input.ClearSecret))
		if secretErr != nil {
			return secretErr
		}
		return s.persistSectionTx(ctx.Request.Context(), tx, "asr_s3", dto)
	})
	if err != nil {
		s.publishMu.Unlock()
		writeError(ctx, err)
		return
	}
	applyEnvMutation(mut) // commit 成功后才更新进程 env

	nextCfg.SetAccessKeyManaged(nextManaged)
	s.cfg.ASRS3 = nextCfg
	resp := newASRS3ConfigResponse(s.cfg.ASRS3)
	cfgSnapshot := *s.cfg
	gen := s.bumpConfigGen()
	s.publishMu.Unlock()

	s.refreshRuntimeStatus(cfgSnapshot, gen)
	ctx.JSON(http.StatusOK, resp)
}

// asrs3ConfigToDTO 把 ASRS3Config 转成完整下一状态 DTO(含 tombstone)。
func asrs3ConfigToDTO(c config.ASRS3Config, managed bool) config.ASRS3SectionDTO {
	return config.ASRS3SectionDTO{
		Endpoint:         &c.Endpoint,
		Bucket:           &c.Bucket,
		AccessKeyID:      &c.AccessKeyID,
		AccessKeyEnv:     &c.AccessKeyEnv,
		Region:           &c.Region,
		PublicURLPrefix:  &c.PublicURLPrefix,
		UsePathStyle:     &c.UsePathStyle,
		AccessKeyManaged: &managed,
	}
}

// --- DashScope (ASR) config handlers ---

type dashscopeConfigResponse struct {
	APIKeyEnv          string `json:"api_key_env"`
	APIKeySet          bool   `json:"api_key_set"`
	ASRURL             string `json:"asr_url"`
	TasksURL           string `json:"tasks_url"`
	Model              string `json:"model"`
	Language           string `json:"language"`
	DiarizationEnabled bool   `json:"diarization_enabled"`
	SpeakerCount       int    `json:"speaker_count"`
	VocabularyID       string `json:"vocabulary_id"`
}

// newDashScopeConfigResponse 返回 DashScope 配置响应。
// api_key_env 经 EffectiveAPIKeyEnv 兜底;api_key 永不返回明文,只返回 api_key_set。
func newDashScopeConfigResponse(d config.DashScopeConfig) dashscopeConfigResponse {
	envKey := d.EffectiveAPIKeyEnv()
	return dashscopeConfigResponse{
		APIKeyEnv:          envKey,
		APIKeySet:          os.Getenv(envKey) != "",
		ASRURL:             d.ASRURL,
		TasksURL:           d.TasksURL,
		Model:              d.Model,
		Language:           d.Language,
		DiarizationEnabled: d.DiarizationEnabled,
		SpeakerCount:       d.SpeakerCount,
		VocabularyID:       d.VocabularyID,
	}
}

// validateDashScopeURL 校验 DashScope 接口地址必须为合法 http(s) url 且含 host。
func validateDashScopeURL(value, field string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil // 空值走 DashScope 官方默认
	}
	u, err := url.Parse(value)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("%s must be a valid http(s) url with host", field)
	}
	return nil
}

func (s *Server) getDashScopeConfig(ctx *gin.Context) {
	s.publishMu.RLock()
	resp := newDashScopeConfigResponse(s.cfg.DashScope)
	s.publishMu.RUnlock()
	ctx.JSON(http.StatusOK, resp)
}

func (s *Server) updateDashScopeConfig(ctx *gin.Context) {
	var input struct {
		APIKeyEnv          *string `json:"api_key_env"`
		APIKey             *string `json:"api_key"`
		ClearKey           *bool   `json:"clear_key"`
		ASRURL             *string `json:"asr_url"`
		TasksURL           *string `json:"tasks_url"`
		Model              *string `json:"model"`
		Language           *string `json:"language"`
		DiarizationEnabled *bool   `json:"diarization_enabled"`
		SpeakerCount       *int    `json:"speaker_count"`
		VocabularyID       *string `json:"vocabulary_id"`
	}
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid json body"})
		return
	}
	if input.ASRURL != nil {
		if err := validateDashScopeURL(*input.ASRURL, "asr_url"); err != nil {
			ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}
	if input.TasksURL != nil {
		if err := validateDashScopeURL(*input.TasksURL, "tasks_url"); err != nil {
			ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}
	if input.SpeakerCount != nil && *input.SpeakerCount < 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "speaker_count must be >= 0"})
		return
	}
	if input.APIKeyEnv != nil {
		if err := validateEnvKeyName(*input.APIKeyEnv, "api_key_env"); err != nil {
			ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}

	// 整体串行化:从读 oldEnv 到 secret 迁移再到写 cfg 全程持锁,
	// 保证 env 改名 + secret 迁移原子完成,避免并发 PUT 互相迁移/删除 secret 产生孤儿。
	s.publishMu.Lock()

	oldEnv := s.cfg.DashScope.EffectiveAPIKeyEnv()
	newEnv := oldEnv
	if input.APIKeyEnv != nil {
		trimmed := strings.TrimSpace(*input.APIKeyEnv)
		if trimmed == "" {
			newEnv = "DASHSCOPE_API_KEY" // 清空 = 用默认
		} else {
			newEnv = trimmed
		}
	}

	// 字段提交:基于【当前】配置 patch(此时仍持锁,无并发丢更新)。
	nextCfg := s.cfg.DashScope
	if input.ASRURL != nil {
		nextCfg.ASRURL = strings.TrimSpace(*input.ASRURL)
	}
	if input.TasksURL != nil {
		nextCfg.TasksURL = strings.TrimSpace(*input.TasksURL)
	}
	if input.Model != nil {
		nextCfg.Model = strings.TrimSpace(*input.Model)
	}
	if input.Language != nil {
		nextCfg.Language = strings.TrimSpace(*input.Language)
	}
	if input.DiarizationEnabled != nil {
		nextCfg.DiarizationEnabled = *input.DiarizationEnabled
	}
	if input.SpeakerCount != nil {
		nextCfg.SpeakerCount = *input.SpeakerCount
	}
	if input.VocabularyID != nil {
		nextCfg.VocabularyID = strings.TrimSpace(*input.VocabularyID)
	}
	if input.APIKeyEnv != nil {
		nextCfg.APIKeyEnv = strings.TrimSpace(*input.APIKeyEnv)
	}
	dto := dashscopeConfigToDTO(nextCfg)

	// 原子:secrets 写入 + runtime_settings 写入放同一事务,commit 后才改 env/cfg。
	var mut envMutation
	err := runtimeconfig.WithTx(ctx.Request.Context(), s.runtimeCfg.DB(), func(tx *sql.Tx) error {
		var secretErr error
		mut, secretErr = s.applySecretEnvChangeTx(ctx.Request.Context(), tx,
			oldEnv, newEnv, derefStr(input.APIKey), derefBool(input.ClearKey))
		if secretErr != nil {
			return secretErr
		}
		return s.persistSectionTx(ctx.Request.Context(), tx, "dashscope", dto)
	})
	if err != nil {
		s.publishMu.Unlock()
		writeError(ctx, err)
		return
	}
	applyEnvMutation(mut)

	s.cfg.DashScope = nextCfg
	resp := newDashScopeConfigResponse(s.cfg.DashScope)
	cfgSnapshot := *s.cfg
	gen := s.bumpConfigGen()
	s.publishMu.Unlock()

	s.refreshRuntimeStatus(cfgSnapshot, gen)
	ctx.JSON(http.StatusOK, resp)
}

// dashscopeConfigToDTO 把 DashScopeConfig 转成完整下一状态 DTO。APIKey 不进 DTO（走 secrets）。
func dashscopeConfigToDTO(c config.DashScopeConfig) config.DashScopeSectionDTO {
	return config.DashScopeSectionDTO{
		APIKeyEnv:          &c.APIKeyEnv,
		ASRURL:             &c.ASRURL,
		TasksURL:           &c.TasksURL,
		Model:              &c.Model,
		Language:           &c.Language,
		DiarizationEnabled: &c.DiarizationEnabled,
		SpeakerCount:       &c.SpeakerCount,
		VocabularyID:       &c.VocabularyID,
	}
}

// --- Recap AI config handlers ---

type recapConfigResponse struct {
	Enabled            bool   `json:"enabled"`
	Provider           string `json:"provider"`
	APIKeyEnv          string `json:"api_key_env"`
	APIKeySet          bool   `json:"api_key_set"`
	BaseURL            string `json:"base_url"`
	Model              string `json:"model"`
	MaxTokens          int    `json:"max_tokens"`
	MaxContinuations   int    `json:"max_continuations"`
	TimeoutSeconds     int    `json:"timeout_seconds"`
	IncludeSpeakerInfo bool   `json:"include_speaker_info"`
}

// newRecapConfigResponse 返回回顾 AI 配置响应。
// provider/base_url/model/api_key_env 经 Effective* 兜底,保证前端表单始终显示有效值;
// api_key 永不返回明文,只返回 api_key_set。
func newRecapConfigResponse(r config.RecapAIConfig) recapConfigResponse {
	envKey := r.EffectiveAPIKeyEnv()
	return recapConfigResponse{
		Enabled:            r.Enabled,
		Provider:           r.EffectiveProvider(),
		APIKeyEnv:          envKey,
		APIKeySet:          os.Getenv(envKey) != "",
		BaseURL:            r.EffectiveBaseURL(),
		Model:              r.EffectiveModel(),
		MaxTokens:          r.MaxTokens,
		MaxContinuations:   r.MaxContinuations,
		TimeoutSeconds:     r.TimeoutSeconds,
		IncludeSpeakerInfo: r.IncludeSpeakerInfo,
	}
}

func (s *Server) getRecapConfig(ctx *gin.Context) {
	s.publishMu.RLock()
	resp := newRecapConfigResponse(s.cfg.RecapAI)
	s.publishMu.RUnlock()
	ctx.JSON(http.StatusOK, resp)
}

// validRecapProviders 是回顾 AI 支持的 provider 白名单,与 provider_util.go 的 switch 对齐。
var validRecapProviders = map[string]bool{
	"openai_compatible": true,
	"anthropic":         true,
	"claude_cli":        true,
	"codex_cli":         true,
	"local":             true,
}

func (s *Server) updateRecapConfig(ctx *gin.Context) {
	var input struct {
		Enabled            *bool   `json:"enabled"`
		Provider           *string `json:"provider"`
		APIKeyEnv          *string `json:"api_key_env"`
		APIKey             *string `json:"api_key"`
		ClearKey           *bool   `json:"clear_key"`
		BaseURL            *string `json:"base_url"`
		Model              *string `json:"model"`
		MaxTokens          *int    `json:"max_tokens"`
		MaxContinuations   *int    `json:"max_continuations"`
		TimeoutSeconds     *int    `json:"timeout_seconds"`
		IncludeSpeakerInfo *bool   `json:"include_speaker_info"`
	}
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid json body"})
		return
	}

	if input.MaxTokens != nil && *input.MaxTokens < 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "max_tokens must be >= 0"})
		return
	}
	if input.MaxContinuations != nil && *input.MaxContinuations < 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "max_continuations must be >= 0"})
		return
	}
	if input.TimeoutSeconds != nil && *input.TimeoutSeconds < 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "timeout_seconds must be >= 0"})
		return
	}
	// provider 非空时校验白名单;空值允许(走 EffectiveProvider 兜底到 openai_compatible)
	if input.Provider != nil {
		if trimmed := strings.TrimSpace(*input.Provider); trimmed != "" && !validRecapProviders[trimmed] {
			ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid provider"})
			return
		}
	}
	if input.APIKeyEnv != nil {
		if err := validateEnvKeyName(*input.APIKeyEnv, "api_key_env"); err != nil {
			ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}

	s.publishMu.Lock()

	oldEnv := s.cfg.RecapAI.EffectiveAPIKeyEnv()
	newEnv := oldEnv // 未传 APIKeyEnv 字段时沿用旧值
	if input.APIKeyEnv != nil {
		trimmed := strings.TrimSpace(*input.APIKeyEnv)
		if trimmed == "" {
			// 清空 = 用默认 AI_API_KEY(与 RecapAIConfig.EffectiveAPIKeyEnv 一致)
			newEnv = "AI_API_KEY"
		} else {
			newEnv = trimmed
		}
	}

	// 字段提交:基于【当前】配置 patch(此时仍持锁,无并发丢更新)。
	nextCfg := s.cfg.RecapAI
	if input.Enabled != nil {
		nextCfg.Enabled = *input.Enabled
	}
	if input.Provider != nil {
		nextCfg.Provider = strings.TrimSpace(*input.Provider) // 允许空,运行时兜底
	}
	if input.APIKeyEnv != nil {
		nextCfg.APIKeyEnv = strings.TrimSpace(*input.APIKeyEnv)
	}
	if input.BaseURL != nil {
		nextCfg.BaseURL = strings.TrimSpace(*input.BaseURL) // 允许空,运行时兜底
	}
	if input.Model != nil {
		nextCfg.Model = strings.TrimSpace(*input.Model) // 允许空,运行时兜底
	}
	if input.MaxTokens != nil {
		nextCfg.MaxTokens = *input.MaxTokens
	}
	if input.MaxContinuations != nil {
		nextCfg.MaxContinuations = *input.MaxContinuations
	}
	if input.TimeoutSeconds != nil {
		nextCfg.TimeoutSeconds = *input.TimeoutSeconds
	}
	if input.IncludeSpeakerInfo != nil {
		nextCfg.IncludeSpeakerInfo = *input.IncludeSpeakerInfo
	}
	dto := recapConfigToDTO(nextCfg)

	// 原子:secrets 写入 + runtime_settings 写入放同一事务,commit 后才改 env/cfg。
	var mut envMutation
	err := runtimeconfig.WithTx(ctx.Request.Context(), s.runtimeCfg.DB(), func(tx *sql.Tx) error {
		var secretErr error
		mut, secretErr = s.applySecretEnvChangeTx(ctx.Request.Context(), tx,
			oldEnv, newEnv, derefStr(input.APIKey), derefBool(input.ClearKey))
		if secretErr != nil {
			return secretErr
		}
		return s.persistSectionTx(ctx.Request.Context(), tx, "recap_ai", dto)
	})
	if err != nil {
		s.publishMu.Unlock()
		writeError(ctx, err)
		return
	}
	applyEnvMutation(mut)

	s.cfg.RecapAI = nextCfg
	resp := newRecapConfigResponse(s.cfg.RecapAI)
	cfgSnapshot := *s.cfg
	gen := s.bumpConfigGen()
	s.publishMu.Unlock()

	s.refreshRuntimeStatus(cfgSnapshot, gen)
	ctx.JSON(http.StatusOK, resp)
}

// recapConfigToDTO 把 RecapAIConfig 转成完整下一状态 DTO（仅 UI 管理字段，不含 CLIPath/GlossaryFile/EnableSummarization）。
// APIKey 不进 DTO（走 secrets）。
func recapConfigToDTO(c config.RecapAIConfig) config.RecapAISectionDTO {
	return config.RecapAISectionDTO{
		Enabled:            &c.Enabled,
		Provider:           &c.Provider,
		APIKeyEnv:          &c.APIKeyEnv,
		BaseURL:            &c.BaseURL,
		Model:              &c.Model,
		MaxTokens:          &c.MaxTokens,
		MaxContinuations:   &c.MaxContinuations,
		TimeoutSeconds:     &c.TimeoutSeconds,
		IncludeSpeakerInfo: &c.IncludeSpeakerInfo,
	}
}

// RecapModelOption 是回顾模型的推荐快捷选项。
type RecapModelOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
	Group string `json:"group"`
}

// recommendedRecapModels 推荐回顾模型列表（按厂商分组），供前端下拉快捷选择。
// 模型名称仍支持自由输入，此列表仅为常用快捷选项，前后端共享以避免多处硬编码不一致。
var recommendedRecapModels = []RecapModelOption{
	{Value: "deepseek-v4-flash", Label: "deepseek-v4-flash（快速）", Group: "DeepSeek"},
	{Value: "deepseek-v4-pro", Label: "deepseek-v4-pro（默认）", Group: "DeepSeek"},
	{Value: "gpt-4o", Label: "gpt-4o", Group: "OpenAI"},
	{Value: "gpt-4o-mini", Label: "gpt-4o-mini", Group: "OpenAI"},
	{Value: "qwen-plus", Label: "qwen-plus", Group: "其他"},
	{Value: "qwen-turbo", Label: "qwen-turbo", Group: "其他"},
	{Value: "qwen-max", Label: "qwen-max", Group: "其他"},
	{Value: "claude-sonnet-4-20250514", Label: "claude-sonnet-4-20250514", Group: "其他"},
}

// getRecapModels 返回推荐回顾模型列表（只读，供前端下拉填充）。
func (s *Server) getRecapModels(ctx *gin.Context) {
	ctx.JSON(http.StatusOK, gin.H{"models": recommendedRecapModels})
}

// --- WebDAV config handlers ---

type webDAVConfigResponse struct {
	URL         string `json:"url"`
	Username    string `json:"username"`
	BasePath    string `json:"base_path"`
	Remote      string `json:"remote"`
	PasswordEnv string `json:"password_env"`
	PasswordSet bool   `json:"password_set"`
}

func newWebDAVConfigResponse(w config.WebDAVConfig) webDAVConfigResponse {
	return webDAVConfigResponse{
		URL:         w.URL,
		Username:    w.Username,
		BasePath:    w.BasePath,
		Remote:      w.Remote,
		PasswordEnv: w.PasswordEnv,
		// EffectivePassword 遵循 tombstone(managed 时不回落明文),与运行时 copier/能力探测一致。
		PasswordSet: w.EffectivePassword() != "",
	}
}

func (s *Server) getWebDAVConfig(ctx *gin.Context) {
	s.publishMu.RLock()
	resp := newWebDAVConfigResponse(s.cfg.WebDAV)
	s.publishMu.RUnlock()
	ctx.JSON(http.StatusOK, resp)
}

func (s *Server) updateWebDAVConfig(ctx *gin.Context) {
	var input struct {
		URL           *string `json:"url"`
		Username      *string `json:"username"`
		Password      *string `json:"password"`
		PasswordEnv   *string `json:"password_env"`
		BasePath      *string `json:"base_path"`
		Remote        *string `json:"remote"`
		ClearPassword *bool   `json:"clear_password"`
	}
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid json body"})
		return
	}

	if input.URL != nil {
		if err := validateWebDAVURL(*input.URL); err != nil {
			writeBadRequest(ctx, err.Error())
			return
		}
	}
	if input.BasePath != nil {
		if err := validateWebDAVBasePath(*input.BasePath); err != nil {
			writeBadRequest(ctx, err.Error())
			return
		}
	}
	if input.Remote != nil {
		if err := validateWebDAVRemote(*input.Remote); err != nil {
			writeBadRequest(ctx, err.Error())
			return
		}
	}

	s.publishMu.Lock()
	// 计算 password_env 改名的旧/新值（基于当前快照），用于密钥迁移。
	oldEnv := s.cfg.WebDAV.EffectivePasswordEnv()
	newEnv := oldEnv
	if input.PasswordEnv != nil {
		trimmed := strings.TrimSpace(*input.PasswordEnv)
		if trimmed == "" {
			newEnv = "WEBDAV_PASSWORD" // 清空 = 用默认
		} else {
			newEnv = trimmed
		}
	}
	// tombstone 状态机(r13 [High]):显式密钥操作(设/清/改 env 名)升 managed=true;
	// 非密钥字段改动保持当前 managed(可能为 true,不回退)。
	explicitSecretOp := input.Password != nil || derefBool(input.ClearPassword) || input.PasswordEnv != nil
	nextManaged := s.cfg.WebDAV.PasswordManaged() || explicitSecretOp

	// 字段提交:基于【当前】配置 patch(此时仍持锁,无并发丢更新)。
	nextCfg := s.cfg.WebDAV
	if input.URL != nil {
		nextCfg.URL = strings.TrimSpace(*input.URL)
	}
	if input.Username != nil {
		nextCfg.Username = strings.TrimSpace(*input.Username)
	}
	if input.PasswordEnv != nil {
		nextCfg.PasswordEnv = strings.TrimSpace(*input.PasswordEnv)
	}
	if input.BasePath != nil {
		nextCfg.BasePath = strings.TrimSpace(*input.BasePath)
	}
	if input.Remote != nil {
		nextCfg.Remote = strings.TrimSpace(*input.Remote)
	}
	// 注意:不写 nextCfg.Password —— 密码走 secrets + env,由 EffectivePassword() 读取,
	// managed=true 时不回落 config.yaml 明文(真正清除语义)。
	dto := webdavConfigToDTO(nextCfg, nextManaged)

	// 原子:secrets 写入 + runtime_settings 写入放同一事务,commit 后才改 env/cfg。
	var mut envMutation
	err := runtimeconfig.WithTx(ctx.Request.Context(), s.runtimeCfg.DB(), func(tx *sql.Tx) error {
		var secretErr error
		mut, secretErr = s.applySecretEnvChangeTx(ctx.Request.Context(), tx,
			oldEnv, newEnv, derefStr(input.Password), derefBool(input.ClearPassword))
		if secretErr != nil {
			return secretErr
		}
		return s.persistSectionTx(ctx.Request.Context(), tx, "webdav", dto)
	})
	if err != nil {
		s.publishMu.Unlock()
		writeError(ctx, err)
		return
	}
	applyEnvMutation(mut)

	nextCfg.SetPasswordManaged(nextManaged)
	s.cfg.WebDAV = nextCfg
	resp := newWebDAVConfigResponse(s.cfg.WebDAV)
	cfgSnapshot := *s.cfg
	gen := s.bumpConfigGen()
	s.publishMu.Unlock()

	s.refreshRuntimeStatus(cfgSnapshot, gen)
	ctx.JSON(http.StatusOK, resp)
}

// webdavConfigToDTO 把 WebDAVConfig 转成完整下一状态 DTO（含 tombstone）。Password 不进 DTO（走 secrets）。
func webdavConfigToDTO(c config.WebDAVConfig, managed bool) config.WebDAVSectionDTO {
	return config.WebDAVSectionDTO{
		URL:             &c.URL,
		Username:        &c.Username,
		PasswordEnv:     &c.PasswordEnv,
		BasePath:        &c.BasePath,
		Remote:          &c.Remote,
		PasswordManaged: &managed,
	}
}

// --- Archive config handlers (发布后自动归档) ---

type archiveConfigResponse struct {
	AutoAfterPublish bool   `json:"auto_after_publish"`
	CleanupPolicy    string `json:"cleanup_policy"`
}

func newArchiveConfigResponse(a config.ArchiveConfig) archiveConfigResponse {
	return archiveConfigResponse{
		AutoAfterPublish: a.AutoAfterPublish,
		CleanupPolicy:    a.CleanupPolicy,
	}
}

func (s *Server) getArchiveConfig(ctx *gin.Context) {
	s.publishMu.RLock()
	resp := newArchiveConfigResponse(s.cfg.Archive)
	s.publishMu.RUnlock()
	ctx.JSON(http.StatusOK, resp)
}

func (s *Server) updateArchiveConfig(ctx *gin.Context) {
	var input struct {
		AutoAfterPublish *bool   `json:"auto_after_publish"`
		CleanupPolicy    *string `json:"cleanup_policy"`
	}
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid json body"})
		return
	}

	if input.CleanupPolicy != nil {
		policy := strings.TrimSpace(*input.CleanupPolicy)
		switch policy {
		case "none", "temp", "generated", "all":
		default:
			ctx.JSON(http.StatusBadRequest, gin.H{"error": "cleanup_policy must be one of: none, temp, generated, all"})
			return
		}
		*input.CleanupPolicy = policy
	}

	s.publishMu.Lock()
	nextArchive := s.cfg.Archive
	if input.AutoAfterPublish != nil {
		nextArchive.AutoAfterPublish = *input.AutoAfterPublish
	}
	if input.CleanupPolicy != nil {
		nextArchive.CleanupPolicy = *input.CleanupPolicy
	}
	dto := archiveConfigToDTO(nextArchive)
	if err := runtimeconfig.WithTx(ctx.Request.Context(), s.runtimeCfg.DB(), func(tx *sql.Tx) error {
		return s.persistSectionTx(ctx.Request.Context(), tx, "archive", dto)
	}); err != nil {
		s.publishMu.Unlock()
		slog.Warn("persist archive config failed", "error", err)
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "failed to persist archive config"})
		return
	}
	s.cfg.Archive = nextArchive
	resp := newArchiveConfigResponse(s.cfg.Archive)
	cfgSnapshot := *s.cfg
	gen := s.bumpConfigGen()
	s.publishMu.Unlock()

	s.refreshRuntimeStatus(cfgSnapshot, gen)

	ctx.JSON(http.StatusOK, resp)
}

// archiveConfigToDTO 把 ArchiveConfig 转成完整下一状态 DTO。
func archiveConfigToDTO(a config.ArchiveConfig) config.ArchiveSectionDTO {
	return config.ArchiveSectionDTO{
		AutoAfterPublish: &a.AutoAfterPublish,
		CleanupPolicy:    &a.CleanupPolicy,
	}
}

// --- Global glossary handlers ---

func (s *Server) listGlobalGlossary(ctx *gin.Context) {
	entries, err := s.glossary.ListGlobal(ctx.Request.Context())
	if err != nil {
		writeError(ctx, err)
		return
	}
	if entries == nil {
		entries = []glossary.Entry{}
	}
	ctx.JSON(http.StatusOK, gin.H{"items": entries})
}

func (s *Server) upsertGlobalGlossary(ctx *gin.Context) {
	var input struct {
		Term      string `json:"term" binding:"required"`
		Canonical string `json:"canonical" binding:"required"`
		Category  string `json:"category"`
	}
	if err := ctx.ShouldBindJSON(&input); err != nil {
		writeBadRequest(ctx, "invalid json body")
		return
	}
	if err := s.glossary.Upsert(ctx.Request.Context(), "", input.Term, input.Canonical, input.Category); err != nil {
		writeError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"ok": true})
}

func (s *Server) deleteGlobalGlossary(ctx *gin.Context) {
	eid, err := strconv.ParseInt(ctx.Param("eid"), 10, 64)
	if err != nil {
		writeBadRequest(ctx, "invalid entry id")
		return
	}
	if err := s.glossary.Delete(ctx.Request.Context(), eid); err != nil {
		writeError(ctx, err)
		return
	}
	ctx.Status(http.StatusNoContent)
}

func (s *Server) getGlobalGlossaryNote(ctx *gin.Context) {
	note, err := s.glossary.GetNote(ctx.Request.Context(), "")
	if err != nil {
		writeError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"note": note})
}

func (s *Server) updateGlobalGlossaryNote(ctx *gin.Context) {
	var input struct {
		Note string `json:"note"`
	}
	if err := ctx.ShouldBindJSON(&input); err != nil {
		writeBadRequest(ctx, "invalid json body")
		return
	}
	if err := s.glossary.SetNote(ctx.Request.Context(), "", input.Note); err != nil {
		writeError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"ok": true})
}

// --- Channel glossary handlers ---

func (s *Server) listChannelGlossary(ctx *gin.Context) {
	channelID := ctx.Param("id")
	entries, err := s.glossary.ListByChannel(ctx.Request.Context(), channelID)
	if err != nil {
		writeError(ctx, err)
		return
	}
	if entries == nil {
		entries = []glossary.MergedEntry{}
	}
	ctx.JSON(http.StatusOK, gin.H{"items": entries})
}

func (s *Server) upsertChannelGlossary(ctx *gin.Context) {
	channelID := ctx.Param("id")
	var input struct {
		Term      string `json:"term" binding:"required"`
		Canonical string `json:"canonical" binding:"required"`
		Category  string `json:"category"`
	}
	if err := ctx.ShouldBindJSON(&input); err != nil {
		writeBadRequest(ctx, "invalid json body")
		return
	}
	if err := s.glossary.Upsert(ctx.Request.Context(), channelID, input.Term, input.Canonical, input.Category); err != nil {
		writeError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"ok": true})
}

func (s *Server) deleteChannelGlossary(ctx *gin.Context) {
	eid, err := strconv.ParseInt(ctx.Param("eid"), 10, 64)
	if err != nil {
		writeBadRequest(ctx, "invalid entry id")
		return
	}
	if err := s.glossary.Delete(ctx.Request.Context(), eid); err != nil {
		writeError(ctx, err)
		return
	}
	ctx.Status(http.StatusNoContent)
}

func (s *Server) getChannelGlossaryNote(ctx *gin.Context) {
	channelID := ctx.Param("id")
	note, err := s.glossary.GetNote(ctx.Request.Context(), channelID)
	if err != nil {
		writeError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"note": note})
}

func (s *Server) updateChannelGlossaryNote(ctx *gin.Context) {
	channelID := ctx.Param("id")
	var input struct {
		Note string `json:"note"`
	}
	if err := ctx.ShouldBindJSON(&input); err != nil {
		writeBadRequest(ctx, "invalid json body")
		return
	}
	if err := s.glossary.SetNote(ctx.Request.Context(), channelID, input.Note); err != nil {
		writeError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"ok": true})
}

// --- Glossary candidate handlers ---

func (s *Server) listGlobalGlossaryCandidates(ctx *gin.Context) {
	s.listGlossaryCandidates(ctx, "")
}

func (s *Server) listChannelGlossaryCandidates(ctx *gin.Context) {
	s.listGlossaryCandidates(ctx, ctx.Param("id"))
}

func (s *Server) listGlossaryCandidates(ctx *gin.Context, channelID string) {
	candidates, err := s.glossary.ListCandidates(ctx.Request.Context(), channelID, ctx.Query("status"))
	if err != nil {
		writeError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"items": candidates})
}

func (s *Server) approveGlossaryCandidate(ctx *gin.Context) {
	cid, err := strconv.ParseInt(ctx.Param("cid"), 10, 64)
	if err != nil || cid <= 0 {
		writeBadRequest(ctx, "invalid candidate id")
		return
	}
	var input struct {
		Term      string `json:"term"`
		Canonical string `json:"canonical"`
		Category  string `json:"category"`
	}
	if err := ctx.ShouldBindJSON(&input); err != nil && !errors.Is(err, io.EOF) {
		writeBadRequest(ctx, "invalid json body")
		return
	}
	if err := s.glossary.ApproveCandidate(ctx.Request.Context(), cid, input.Term, input.Canonical, input.Category); err != nil {
		writeError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"ok": true})
}

func (s *Server) rejectGlossaryCandidate(ctx *gin.Context) {
	cid, err := strconv.ParseInt(ctx.Param("cid"), 10, 64)
	if err != nil || cid <= 0 {
		writeBadRequest(ctx, "invalid candidate id")
		return
	}
	if err := s.glossary.RejectCandidate(ctx.Request.Context(), cid); err != nil {
		writeError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"ok": true})
}

func (s *Server) batchApproveGlossaryCandidates(ctx *gin.Context) {
	var input struct {
		IDs []int64 `json:"ids" binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&input); err != nil || len(input.IDs) == 0 {
		writeBadRequest(ctx, "invalid json body")
		return
	}
	approved, err := s.glossary.BatchApproveCandidates(ctx.Request.Context(), ctx.Param("id"), input.IDs)
	if err != nil {
		writeError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"approved": approved})
}

func (s *Server) batchRejectGlossaryCandidates(ctx *gin.Context) {
	var input struct {
		IDs []int64 `json:"ids" binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&input); err != nil || len(input.IDs) == 0 {
		writeBadRequest(ctx, "invalid json body")
		return
	}
	rejected, err := s.glossary.BatchRejectCandidates(ctx.Request.Context(), ctx.Param("id"), input.IDs)
	if err != nil {
		writeError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"rejected": rejected})
}

func (s *Server) discoverSessionGlossary(ctx *gin.Context) {
	if s.glossaryDiscoverer == nil {
		ctx.JSON(http.StatusServiceUnavailable, gin.H{"error": "glossary discovery unavailable"})
		return
	}
	sessionInfo, err := s.sessions.Get(ctx.Request.Context(), ctx.Param("sid"))
	if err != nil {
		writeError(ctx, err)
		return
	}
	if !sessionInfo.LocalAvailable {
		writeError(ctx, fmt.Errorf("%w: local files removed, fetch from webdav first", recap.ErrTranscriptMissing))
		return
	}
	packageDir := filepath.Join(s.cfg.OutputRoot, sessionInfo.ChannelID, sessionInfo.Slug, "package")
	transcript, err := os.ReadFile(filepath.Join(packageDir, "transcript.txt"))
	if err != nil {
		if os.IsNotExist(err) {
			writeError(ctx, fmt.Errorf("%w: %s", recap.ErrTranscriptMissing, filepath.Join(packageDir, "transcript.txt")))
			return
		}
		writeError(ctx, err)
		return
	}
	segments := readGlossaryDiscoverySegments(packageDir)
	existingGlossary := ""
	if s.glossary != nil {
		existingGlossary, _ = s.glossary.ExportForPrompt(ctx.Request.Context(), sessionInfo.ChannelID)
	}
	if err := s.glossaryDiscoverer.Discover(ctx.Request.Context(), sessionInfo.ChannelID, sessionInfo.ID, transcript, segments, existingGlossary); err != nil {
		writeError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"ok": true})
}

func readGlossaryDiscoverySegments(packageDir string) []glossary.TranscriptSegment {
	data, err := os.ReadFile(filepath.Join(packageDir, "segments.json"))
	if err != nil {
		return nil
	}
	var segments []glossary.TranscriptSegment
	if err := json.Unmarshal(data, &segments); err != nil {
		return nil
	}
	return segments
}

// --- Glossary import/export handlers ---

func (s *Server) importGlobalMarkdown(ctx *gin.Context) {
	s.handleImportMarkdown(ctx, "")
}

func (s *Server) importChannelMarkdown(ctx *gin.Context) {
	s.handleImportMarkdown(ctx, ctx.Param("id"))
}

func (s *Server) handleImportMarkdown(ctx *gin.Context, channelID string) {
	var input struct {
		Content string `json:"content" binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&input); err != nil {
		writeBadRequest(ctx, "invalid json body")
		return
	}
	count, err := s.glossary.ImportMarkdown(ctx.Request.Context(), channelID, input.Content)
	if err != nil {
		writeError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"imported": count})
}

func (s *Server) importGlobalJSON(ctx *gin.Context) {
	s.handleImportJSON(ctx, "")
}

func (s *Server) importChannelJSON(ctx *gin.Context) {
	s.handleImportJSON(ctx, ctx.Param("id"))
}

func (s *Server) handleImportJSON(ctx *gin.Context, channelID string) {
	body, err := io.ReadAll(ctx.Request.Body)
	if err != nil {
		writeBadRequest(ctx, "failed to read request body")
		return
	}
	count, err := s.glossary.ImportJSON(ctx.Request.Context(), channelID, body)
	if err != nil {
		writeError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"imported": count})
}

func (s *Server) exportGlobalJSON(ctx *gin.Context) {
	s.handleExportJSON(ctx, "")
}

func (s *Server) exportChannelJSON(ctx *gin.Context) {
	s.handleExportJSON(ctx, ctx.Param("id"))
}

func (s *Server) handleExportJSON(ctx *gin.Context, channelID string) {
	data, err := s.glossary.ExportJSON(ctx.Request.Context(), channelID)
	if err != nil {
		writeError(ctx, err)
		return
	}
	ctx.Header("Content-Type", "application/json")
	ctx.Header("Content-Disposition", `attachment; filename="glossary.json"`)
	ctx.Data(http.StatusOK, "application/json", data)
}

// --- Glossary batch/single toggle handlers ---

func (s *Server) batchDeleteGlobalGlossary(ctx *gin.Context) {
	s.handleBatchDeleteGlossary(ctx, "")
}

func (s *Server) batchDeleteChannelGlossary(ctx *gin.Context) {
	s.handleBatchDeleteGlossary(ctx, ctx.Param("id"))
}

func (s *Server) handleBatchDeleteGlossary(ctx *gin.Context, channelID string) {
	var input struct {
		IDs []int64 `json:"ids" binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&input); err != nil {
		writeBadRequest(ctx, "invalid json body")
		return
	}
	deleted, err := s.glossary.DeleteByIDs(ctx.Request.Context(), channelID, input.IDs)
	if err != nil {
		writeError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"deleted": deleted})
}

func (s *Server) batchToggleGlobalGlossary(ctx *gin.Context) {
	s.handleBatchToggleGlossary(ctx, "")
}

func (s *Server) batchToggleChannelGlossary(ctx *gin.Context) {
	s.handleBatchToggleGlossary(ctx, ctx.Param("id"))
}

func (s *Server) handleBatchToggleGlossary(ctx *gin.Context, channelID string) {
	var input struct {
		IDs     []int64 `json:"ids" binding:"required"`
		Enabled *bool   `json:"enabled" binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&input); err != nil {
		writeBadRequest(ctx, "invalid json body")
		return
	}
	updated, err := s.glossary.ToggleByIDs(ctx.Request.Context(), channelID, input.IDs, *input.Enabled)
	if err != nil {
		writeError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"updated": updated})
}

func (s *Server) toggleGlobalGlossary(ctx *gin.Context) {
	s.handleToggleGlossary(ctx, "")
}

func (s *Server) toggleChannelGlossary(ctx *gin.Context) {
	s.handleToggleGlossary(ctx, ctx.Param("id"))
}

func (s *Server) handleToggleGlossary(ctx *gin.Context, _ string) {
	eid, err := strconv.ParseInt(ctx.Param("eid"), 10, 64)
	if err != nil {
		writeBadRequest(ctx, "invalid entry id")
		return
	}
	var input struct {
		Enabled *bool `json:"enabled" binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&input); err != nil {
		writeBadRequest(ctx, "invalid json body")
		return
	}
	if err := s.glossary.Toggle(ctx.Request.Context(), eid, *input.Enabled); err != nil {
		writeError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"ok": true})
}

// --- Recap template handlers ---

func (s *Server) listGlobalRecapTemplates(ctx *gin.Context) {
	templates, err := s.recapTemplates.ListGlobal(ctx.Request.Context())
	if err != nil {
		writeError(ctx, err)
		return
	}
	if templates == nil {
		templates = []recap.Template{}
	}
	ctx.JSON(http.StatusOK, gin.H{"items": templates})
}

func (s *Server) upsertGlobalRecapTemplate(ctx *gin.Context) {
	var input struct {
		Name         string `json:"name"`
		SystemPrompt string `json:"system_prompt"`
		UserFormat   string `json:"user_format"`
		FanName      string `json:"fan_name"`
		ExtraVars    string `json:"extra_vars"`
		Enabled      *bool  `json:"enabled"`
	}
	if err := ctx.ShouldBindJSON(&input); err != nil {
		writeBadRequest(ctx, "invalid json body")
		return
	}
	name := input.Name
	if name == "" {
		name = "default"
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	t := &recap.Template{
		ChannelID:    "",
		Name:         name,
		SystemPrompt: input.SystemPrompt,
		UserFormat:   input.UserFormat,
		FanName:      input.FanName,
		ExtraVars:    input.ExtraVars,
		Enabled:      enabled,
	}
	if err := s.recapTemplates.Upsert(ctx.Request.Context(), t); err != nil {
		writeError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, t)
}

func (s *Server) getChannelRecapTemplate(ctx *gin.Context) {
	channelID := ctx.Param("id")

	globalTpl, _ := s.recapTemplates.GetGlobal(ctx.Request.Context(), "default")
	channelTpl, _ := s.recapTemplates.GetByChannel(ctx.Request.Context(), channelID, "default")
	resolved, err := s.recapTemplates.Resolve(ctx.Request.Context(), channelID, "default")
	if err != nil {
		writeError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{
		"global":   globalTpl,
		"channel":  channelTpl,
		"resolved": resolved,
	})
}

func (s *Server) upsertChannelRecapTemplate(ctx *gin.Context) {
	channelID := ctx.Param("id")
	var input struct {
		Name         string `json:"name"`
		SystemPrompt string `json:"system_prompt"`
		UserFormat   string `json:"user_format"`
		FanName      string `json:"fan_name"`
		ExtraVars    string `json:"extra_vars"`
		Enabled      *bool  `json:"enabled"`
	}
	if err := ctx.ShouldBindJSON(&input); err != nil {
		writeBadRequest(ctx, "invalid json body")
		return
	}
	name := input.Name
	if name == "" {
		name = "default"
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	t := &recap.Template{
		ChannelID:    channelID,
		Name:         name,
		SystemPrompt: input.SystemPrompt,
		UserFormat:   input.UserFormat,
		FanName:      input.FanName,
		ExtraVars:    input.ExtraVars,
		Enabled:      enabled,
	}
	if err := s.recapTemplates.Upsert(ctx.Request.Context(), t); err != nil {
		writeError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, t)
}

func (s *Server) deleteChannelRecapTemplate(ctx *gin.Context) {
	channelID := ctx.Param("id")
	tpl, err := s.recapTemplates.GetByChannel(ctx.Request.Context(), channelID, "default")
	if err != nil {
		writeError(ctx, err)
		return
	}
	if err := s.recapTemplates.Delete(ctx.Request.Context(), tpl.ID); err != nil {
		writeError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"ok": true})
}

func (s *Server) exportGlobalRecapTemplates(ctx *gin.Context) {
	s.handleExportRecapTemplates(ctx, "")
}

func (s *Server) exportChannelRecapTemplates(ctx *gin.Context) {
	s.handleExportRecapTemplates(ctx, ctx.Param("id"))
}

func (s *Server) handleExportRecapTemplates(ctx *gin.Context, channelID string) {
	data, err := s.recapTemplates.ExportJSON(ctx.Request.Context(), channelID)
	if err != nil {
		writeError(ctx, err)
		return
	}
	ctx.Header("Content-Type", "application/json")
	ctx.Header("Content-Disposition", `attachment; filename="recap-templates.json"`)
	ctx.Data(http.StatusOK, "application/json", data)
}

func (s *Server) importGlobalRecapTemplates(ctx *gin.Context) {
	s.handleImportRecapTemplates(ctx, "")
}

func (s *Server) importChannelRecapTemplates(ctx *gin.Context) {
	s.handleImportRecapTemplates(ctx, ctx.Param("id"))
}

func (s *Server) handleImportRecapTemplates(ctx *gin.Context, channelID string) {
	body, err := io.ReadAll(ctx.Request.Body)
	if err != nil {
		writeBadRequest(ctx, "failed to read request body")
		return
	}
	count, err := s.recapTemplates.ImportJSON(ctx.Request.Context(), channelID, body)
	if err != nil {
		writeError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"imported": count})
}

// handleBatchRetryTasks handles POST /api/tasks/batch-retry
func (s *Server) handleBatchRetryTasks(ctx *gin.Context) {
	var req struct {
		TaskIDs []string `json:"task_ids"`
	}
	if err := ctx.ShouldBindJSON(&req); err != nil || len(req.TaskIDs) == 0 {
		writeBadRequest(ctx, "task_ids is required and must be non-empty")
		return
	}
	tasks, err := s.workerPool.BatchRetry(ctx.Request.Context(), req.TaskIDs)
	if err != nil {
		writeError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{
		"retried": len(tasks),
		"tasks":   tasks,
	})
}

// handleUpdateRecapContent handles PUT /api/sessions/:sid/recap/content
func (s *Server) handleUpdateRecapContent(ctx *gin.Context) {
	sid := ctx.Param("sid")
	var req struct {
		Content string `json:"content"`
	}
	if err := ctx.ShouldBindJSON(&req); err != nil || req.Content == "" {
		writeBadRequest(ctx, "content is required")
		return
	}

	sess, err := s.sessions.Get(ctx.Request.Context(), sid)
	if err != nil {
		writeError(ctx, err)
		return
	}
	if !sess.LocalAvailable {
		writeError(ctx, fmt.Errorf("%w: local files removed, fetch from webdav first", recap.ErrTranscriptMissing))
		return
	}

	outputDir := filepath.Join(s.cfg.OutputRoot, sess.ChannelID, sess.Slug, "recap")
	markdownPath := filepath.Join(outputDir, "直播回顾_"+sess.Slug+".md")

	// Ensure output directory exists
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		writeError(ctx, err)
		return
	}

	// Write markdown
	if err := os.WriteFile(markdownPath, []byte(req.Content), 0o644); err != nil {
		writeError(ctx, err)
		return
	}

	ctx.JSON(http.StatusOK, gin.H{"message": "回顾内容已更新"})
}

// handleCopyChannelConfig handles POST /api/channels/:id/copy-config
func (s *Server) handleCopyChannelConfig(ctx *gin.Context) {
	srcID := ctx.Param("id")
	var req struct {
		TargetChannelID string `json:"target_channel_id"`
		CopyGlossary    bool   `json:"copy_glossary"`
		CopyTemplate    bool   `json:"copy_template"`
		CopyPublish     bool   `json:"copy_publish"`
		CopyAutomation  bool   `json:"copy_automation"`
		CopyRecap       bool   `json:"copy_recap"`
	}
	if err := ctx.ShouldBindJSON(&req); err != nil || req.TargetChannelID == "" {
		writeBadRequest(ctx, "target_channel_id is required")
		return
	}

	result := map[string]interface{}{}

	if req.CopyGlossary {
		count, err := s.glossary.CopyFromChannel(ctx.Request.Context(), srcID, req.TargetChannelID)
		result["glossary_copied"] = count
		if err != nil {
			slog.Warn("copy glossary failed", "error", err)
		}
	}

	if req.CopyTemplate {
		count, err := s.recapTemplates.CopyFromChannel(ctx.Request.Context(), srcID, req.TargetChannelID)
		result["template_copied"] = count
		if err != nil {
			slog.Warn("copy template failed", "error", err)
		}
	}

	if req.CopyPublish || req.CopyAutomation {
		src, err := s.channels.Get(ctx.Request.Context(), srcID)
		if err != nil {
			writeError(ctx, err)
			return
		}
		dst, err := s.channels.Get(ctx.Request.Context(), req.TargetChannelID)
		if err != nil {
			writeError(ctx, err)
			return
		}

		if req.CopyPublish {
			src.PublishEnabled = dst.PublishEnabled // will be overridden
		}

		if req.CopyAutomation {
			src.AutoASR = dst.AutoASR // will be overridden
		}

		// Build update input from source config fields
		input := channel.UpsertInput{
			Name:               dst.Name,
			UID:                dst.UID,
			LiveRoomID:         dst.LiveRoomID,
			ReplaySourceURL:    dst.ReplaySourceURL,
			SpaceURL:           dst.SpaceURL,
			TitlePrefix:        dst.TitlePrefix,
			CookieFile:         dst.CookieFile,
			DownloadCookieFile: dst.DownloadCookieFile,
			Enabled:            dst.Enabled,
			AutoRecord:         dst.AutoRecord,
			RecordDanmaku:      dst.RecordDanmaku,
			SourceMode:         dst.SourceMode,
			DiscoverLimit:      dst.DiscoverLimit,
			RecapModel:         dst.RecapModel,
			MaxContinuations:   dst.MaxContinuations,
		}

		if req.CopyPublish {
			input.PublishEnabled = src.PublishEnabled
			input.PublishMode = src.PublishMode
			input.PublishCategoryID = src.PublishCategoryID
			input.PublishListID = src.PublishListID
			input.PublishPrivatePub = src.PublishPrivatePub
			input.PublishOriginal = src.PublishOriginal
			input.PublishAigc = src.PublishAigc
			input.PublishTimerPubTime = src.PublishTimerPubTime
			input.PublishCoverURL = src.PublishCoverURL
			input.PublishTopics = src.PublishTopics
		} else {
			input.PublishEnabled = dst.PublishEnabled
			input.PublishMode = dst.PublishMode
			input.PublishCategoryID = dst.PublishCategoryID
			input.PublishListID = dst.PublishListID
			input.PublishPrivatePub = dst.PublishPrivatePub
			input.PublishOriginal = dst.PublishOriginal
			input.PublishAigc = dst.PublishAigc
			input.PublishTimerPubTime = dst.PublishTimerPubTime
			input.PublishCoverURL = dst.PublishCoverURL
			input.PublishTopics = dst.PublishTopics
		}

		if req.CopyAutomation {
			input.AutoASR = src.AutoASR
			input.AutoPublish = src.AutoPublish
			srcAutoRecap := src.AutoRecap
			input.AutoRecap = &srcAutoRecap
		} else {
			input.AutoASR = dst.AutoASR
			input.AutoPublish = dst.AutoPublish
			dstAutoRecap := dst.AutoRecap
			input.AutoRecap = &dstAutoRecap
		}

		if req.CopyRecap {
			input.RecapModel = src.RecapModel
			input.MaxContinuations = src.MaxContinuations
		} else {
			input.RecapModel = dst.RecapModel
			input.MaxContinuations = dst.MaxContinuations
		}

		if _, err := s.channels.Update(ctx.Request.Context(), req.TargetChannelID, input); err != nil {
			writeError(ctx, err)
			return
		}
		result["channel_updated"] = true
	}

	ctx.JSON(http.StatusOK, result)
}

// --- Onboarding handlers ---

func (s *Server) handleOnboardingStatus(ctx *gin.Context) {
	c := ctx.Request.Context()

	// Check if onboarding was dismissed
	_, err := s.secrets.Get(c, "_onboarding_dismissed")
	dismissed := err == nil

	// Check if channels exist
	channels, err := s.channels.List(c)
	hasChannels := err == nil && len(channels) > 0

	// Check tools status
	hasTools := true
	hasKeys := true
	status := s.currentRuntimeStatus()
	if status != nil {
		for _, tool := range status.Tools {
			if tool.Required && !tool.Available {
				hasTools = false
				break
			}
		}
		hasKeys = status.ConfigStatus.DashScopeKeySet || status.ConfigStatus.RecapKeySet
	}

	needed := !dismissed && (!hasTools || !hasKeys || !hasChannels)

	ctx.JSON(http.StatusOK, gin.H{
		"needed":       needed,
		"has_tools":    hasTools,
		"has_keys":     hasKeys,
		"has_channels": hasChannels,
	})
}

func (s *Server) handleOnboardingDismiss(ctx *gin.Context) {
	if err := s.secrets.Set(ctx.Request.Context(), "_onboarding_dismissed", "true"); err != nil {
		writeError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"message": "引导已跳过"})
}

// --- Recap preset handler ---

func (s *Server) handleListPresets(ctx *gin.Context) {
	ctx.JSON(http.StatusOK, gin.H{"presets": recap.BuiltinPresets})
}

// --- Diagnostic report ---

func (s *Server) handleDiagnosticReport(c *gin.Context) {
	ctx := c.Request.Context()
	report := map[string]interface{}{
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"runtime":   s.currentRuntimeStatus(),
		"config": map[string]interface{}{
			"output_root":  s.cfg.OutputRoot,
			"db_path":      s.cfg.DBPath,
			"web_enabled":  s.cfg.Web.Enabled,
			"web_listen":   s.cfg.Web.Listen,
			"publish_mode": s.cfg.Publish.Mode,
		},
	}

	paths := []string{s.cfg.OutputRoot}
	dbDir := filepath.Dir(s.cfg.DBPath)
	if dbDir != "." && dbDir != "" {
		paths = append(paths, dbDir)
	}
	report["disk_usage"] = runtime.CheckDiskUsage(paths)

	report["cookie_warnings"] = runtime.CheckCookieExpiry(ctx, s.channels)

	recentErrors, _ := s.workerPool.Store().RecentFailedTasks(ctx, 10)
	report["recent_errors"] = recentErrors

	taskSummary, _ := s.workerPool.Store().TaskSummary(ctx)
	report["task_summary"] = taskSummary

	c.JSON(http.StatusOK, report)
}

// --- Stats overview ---

func (s *Server) handleStatsOverview(c *gin.Context) {
	ctx := c.Request.Context()

	stats, err := s.sessions.GetStats(ctx)
	if err != nil {
		writeError(c, err)
		return
	}

	taskSummary, _ := s.workerPool.Store().TaskSummary(ctx)

	// Cost estimate: DashScope ASR ~¥0.01/sec = ¥36/hour
	const asrCostPerHour = 36.0
	asrHours := 0.0
	if stats.TotalSessions > 0 {
		// Rough estimate: ~2 hours per session average
		asrHours = float64(stats.TotalSessions) * 2.0
	}

	c.JSON(http.StatusOK, gin.H{
		"sessions":          stats,
		"task_summary":      taskSummary,
		"asr_cost_estimate": asrHours * asrCostPerHour,
		"asr_hours":         asrHours,
	})
}

func (s *Server) handleStatsDashboard(c *gin.Context) {
	data, err := s.sessions.GetDashboardStats(c.Request.Context())
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, data)
}

// --- Suggested terms extraction ---

func extractSuggestedTerms(markdown string) []map[string]string {
	re := regexp.MustCompile(`\[应为[：:]\s*([^\]]+)\]`)
	matches := re.FindAllStringSubmatch(markdown, -1)
	var terms []map[string]string
	seen := map[string]bool{}
	for _, m := range matches {
		if len(m) > 1 && !seen[m[1]] {
			seen[m[1]] = true
			terms = append(terms, map[string]string{"term": m[1]})
		}
	}
	return terms
}

// --- P2: Discover Preview ---

func (s *Server) handleDiscoverPreview(c *gin.Context) {
	ctx := c.Request.Context()
	channelID := c.Param("id")
	ch, err := s.channels.Get(ctx, channelID)
	if err != nil {
		writeError(c, err)
		return
	}
	results, err := s.discoveries.PreviewChannel(ctx, ch)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": results})
}

// --- P2: Cookie Status ---

type cookieStatus struct {
	ChannelID      string        `json:"channel_id"`
	ChannelName    string        `json:"channel_name"`
	PublishCookie  *cookieDetail `json:"publish_cookie,omitempty"`
	DownloadCookie *cookieDetail `json:"download_cookie,omitempty"`
}

type cookieDetail struct {
	File      string `json:"file"`
	Status    string `json:"status"` // valid / missing / expired
	ExpiresAt string `json:"expires_at,omitempty"`
}

func (s *Server) handleCookieStatus(c *gin.Context) {
	ctx := c.Request.Context()
	channels, err := s.channels.List(ctx)
	if err != nil {
		writeError(c, err)
		return
	}

	var statuses []cookieStatus
	for _, ch := range channels {
		cs := cookieStatus{
			ChannelID:   ch.ID,
			ChannelName: ch.Name,
		}
		if ch.CookieFile != "" {
			cs.PublishCookie = checkCookieFile(ch.CookieFile)
		}
		if ch.DownloadCookieFile != "" {
			cs.DownloadCookie = checkCookieFile(ch.DownloadCookieFile)
		}
		statuses = append(statuses, cs)
	}
	if statuses == nil {
		statuses = []cookieStatus{}
	}
	c.JSON(http.StatusOK, gin.H{"channels": statuses})
}

func checkCookieFile(path string) *cookieDetail {
	d := &cookieDetail{File: path}
	info, err := biliutil.LoadCookie(path)
	if err != nil {
		d.Status = "missing"
		return d
	}
	if info.SESSDATA == "" {
		d.Status = "missing"
		return d
	}
	isExpired, _, expiresAt := biliutil.CheckCookieExpiry(path)
	if expiresAt != "" {
		d.ExpiresAt = expiresAt
	}
	if isExpired {
		d.Status = "expired"
	} else if expiresAt != "" {
		d.Status = "valid"
	} else {
		d.Status = "valid"
	}
	return d
}

// --- P2: Cost Estimation ---

func (s *Server) handleStatsCost(c *gin.Context) {
	ctx := c.Request.Context()
	stats, err := s.sessions.GetStats(ctx)
	if err != nil {
		writeError(c, err)
		return
	}

	// Rough cost estimates
	// DashScope ASR: ~¥0.01/second = ¥36/hour
	const asrCostPerHour = 36.0
	asrHours := float64(stats.TotalSessions) * 2.0 // ~2h avg estimate

	// AI Recap: estimate based on recap count
	// ~10K tokens/recap, ¥0.01/1K tokens (varies by model)
	aiCost := float64(stats.TotalRecaps) * 0.1

	c.JSON(http.StatusOK, gin.H{
		"asr_cost_estimate":   asrHours * asrCostPerHour,
		"asr_hours_estimate":  asrHours,
		"ai_cost_estimate":    aiCost,
		"total_cost_estimate": asrHours*asrCostPerHour + aiCost,
		"monthly_breakdown":   stats.SessionsByMonth,
	})
}

// biliCreativeGet 发起带 -352 风控对抗头的 B站创作类 GET 请求（topic 搜索 / 文集列表共用）。
// 2026-07-06 加入：替代 searchBiliTopics/listBiliSeries 各自内联的 &http.Client{} 裸调，
// 统一补 Referer/Origin（原仅 UA + Accept/Cookie，无 Referer/Origin，风控收紧即挂）。
// cookie 为空时跳过 Cookie 头（适用于无账号也能调的端点，如 topic 搜索）。
// 失败（建请求/网络错误）返回 error，由调用方决定如何向用户反馈；HTTP 非 2xx 也返回 error。
func (s *Server) biliCreativeGet(ctx context.Context, endpoint, cookieHeader string, target any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", biliutil.BiliUserAgent)
	req.Header.Set("Referer", biliCreativeReferer)
	req.Header.Set("Origin", biliCreativeReferer)
	req.Header.Set("Accept", "application/json")
	if cookieHeader != "" {
		req.Header.Set("Cookie", cookieHeader)
	}
	resp, err := s.biliCreativeClient.Do(req)
	if err != nil {
		return fmt.Errorf("bili request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("bili http status %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
		return fmt.Errorf("parse bili response: %w", err)
	}
	return nil
}

func (s *Server) searchBiliTopics(ctx *gin.Context) {
	keywords := ctx.Query("keywords")
	if keywords == "" || len([]rune(keywords)) < 2 {
		ctx.JSON(http.StatusOK, gin.H{"items": []any{}})
		return
	}
	pageSize := 20
	if ps, err := strconv.Atoi(ctx.Query("page_size")); err == nil && ps > 0 && ps <= 50 {
		pageSize = ps
	}
	pageNum := 1
	if pn, err := strconv.Atoi(ctx.Query("page_num")); err == nil && pn > 0 {
		pageNum = pn
	}

	biliURL := fmt.Sprintf(
		"https://app.bilibili.com/x/topic/pub/search?keywords=%s&page_size=%d&page_num=%d",
		url.QueryEscape(keywords), pageSize, pageNum,
	)

	// topic 端点无账号也能用，但补 cookie 更稳；失败则不带 cookie 继续。
	cookieHeader, _ := s.defaultPublishCookieHeader(ctx.Request.Context())

	var result struct {
		Code int `json:"code"`
		Data struct {
			TopicItems []struct {
				ID       int    `json:"id"`
				Name     string `json:"name"`
				StatDesc string `json:"stat_desc"`
			} `json:"topic_items"`
		} `json:"data"`
	}
	if err := s.biliCreativeGet(ctx.Request.Context(), biliURL, cookieHeader, &result); err != nil {
		slog.Warn("search bili topics failed", "error", err)
		ctx.JSON(http.StatusBadGateway, gin.H{"error": "B站话题搜索请求失败"})
		return
	}

	items := make([]map[string]any, 0, len(result.Data.TopicItems))
	for _, t := range result.Data.TopicItems {
		items = append(items, map[string]any{
			"id":        t.ID,
			"name":      t.Name,
			"stat_desc": t.StatDesc,
		})
	}
	ctx.JSON(http.StatusOK, gin.H{"items": items})
}

// defaultPublishCookieHeader 解析默认发布账号的 Cookie header（searchBiliTopics/listBiliSeries 共用）。
// 未配置账号 / 无默认发布账号 / Cookie 加载失败时返回 "" + error，调用方降级为不带 cookie 继续（topic 端点）
// 或向用户反馈（listBiliSeries 由其自己做更精确的错误提示）。
func (s *Server) defaultPublishCookieHeader(ctx context.Context) (string, error) {
	if s.cookieAccounts == nil {
		return "", fmt.Errorf("cookie accounts not configured")
	}
	account, err := s.cookieAccounts.GetDefaultPublish(ctx)
	if err != nil || account == nil {
		return "", fmt.Errorf("no default publish account: %w", err)
	}
	cookie, err := biliutil.LoadCookie(account.CookieFile)
	if err != nil {
		return "", fmt.Errorf("load cookie: %w", err)
	}
	return cookie.CookieHeader(), nil
}

func (s *Server) listBiliSeries(ctx *gin.Context) {
	if s.cookieAccounts == nil {
		ctx.JSON(http.StatusOK, gin.H{
			"items": []any{},
			"error": "未配置 B站账号，请先在账号管理中添加账号",
		})
		return
	}

	account, err := s.cookieAccounts.GetDefaultPublish(ctx.Request.Context())
	if err != nil || account == nil {
		ctx.JSON(http.StatusOK, gin.H{
			"items": []any{},
			"error": "未设置默认发布账号，请先设置默认发布账号",
		})
		return
	}

	cookie, err := biliutil.LoadCookie(account.CookieFile)
	if err != nil {
		ctx.JSON(http.StatusOK, gin.H{
			"items": []any{},
			"error": "Cookie 加载失败，请重新登录",
		})
		return
	}

	biliURL := "https://api.bilibili.com/x/article/creative/list/all"
	var result struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			Lists []struct {
				ID            int    `json:"id"`
				Name          string `json:"name"`
				ArticlesCount int    `json:"articles_count"`
			} `json:"lists"`
		} `json:"data"`
	}
	// 2026-07-06 改用共享 biliCreativeGet（补 Referer/Origin 风控对抗头）；
	// 业务码（-101 登录过期 / 非 0 错误）仍由本函数判断，helper 只管 HTTP + 解码。
	if err := s.biliCreativeGet(ctx.Request.Context(), biliURL, cookie.CookieHeader(), &result); err != nil {
		slog.Warn("list bili series failed", "error", err)
		ctx.JSON(http.StatusBadGateway, gin.H{"error": "B站文集列表请求失败"})
		return
	}
	if result.Code == -101 {
		ctx.JSON(http.StatusOK, gin.H{
			"items": []any{},
			"error": "Cookie 已过期，请重新登录",
		})
		return
	}
	if result.Code != 0 {
		ctx.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("B站 API 错误: %s", result.Message)})
		return
	}

	items := make([]map[string]any, 0, len(result.Data.Lists))
	for _, l := range result.Data.Lists {
		items = append(items, map[string]any{
			"id":             l.ID,
			"name":           l.Name,
			"articles_count": l.ArticlesCount,
		})
	}
	ctx.JSON(http.StatusOK, gin.H{"items": items})
}
