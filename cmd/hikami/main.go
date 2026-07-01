package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"hikami-go/internal/archive"
	"hikami-go/internal/asr"
	"hikami-go/internal/biliutil"
	"hikami-go/internal/channel"
	"hikami-go/internal/config"
	"hikami-go/internal/db"
	"hikami-go/internal/discover"
	"hikami-go/internal/download"
	"hikami-go/internal/glossary"
	"hikami-go/internal/handler"
	"hikami-go/internal/importer"
	"hikami-go/internal/live_record"
	"hikami-go/internal/normalize"
	"hikami-go/internal/notify"
	"hikami-go/internal/publisher"
	"hikami-go/internal/recap"
	hzruntime "hikami-go/internal/runtime"
	"hikami-go/internal/runtimeconfig"
	"hikami-go/internal/scheduler"
	"hikami-go/internal/secrets"
	"hikami-go/internal/session"
	"hikami-go/internal/state"
	"hikami-go/internal/upload"
	"hikami-go/internal/worker"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Error("load config failed", "error", err)
		os.Exit(1)
	}

	logOptions := &slog.HandlerOptions{Level: cfg.LogLevel()}
	logFormat := cfg.LogFormat
	if logFormat == "" {
		logFormat = cfg.Logs.Format
	}
	var logHandler slog.Handler
	if strings.EqualFold(logFormat, "text") {
		logHandler = slog.NewTextHandler(os.Stdout, logOptions)
	} else {
		logHandler = slog.NewJSONHandler(os.Stdout, logOptions)
	}
	logger := slog.New(logHandler)
	slog.SetDefault(logger)

	if err := biliutil.SetCookieEncryptionKey(cfg.CookieEncryptionKey); err != nil {
		logger.Error("invalid cookie encryption key", "error", err)
		os.Exit(1)
	}
	if biliutil.CookieEncryptionEnabled() {
		logger.Info("cookie encryption enabled (AES-256-GCM)")
	}

	if err := cfg.EnsureDirs(); err != nil {
		logger.Error("prepare directories failed", "error", err)
		os.Exit(1)
	}

	database, err := db.Open(cfg.DBPath)
	if err != nil {
		logger.Error("open database failed", "path", cfg.DBPath, "error", err)
		os.Exit(1)
	}
	defer database.Close()

	if err := db.Migrate(database); err != nil {
		logger.Error("migrate database failed", "error", err)
		os.Exit(1)
	}

	secretsStore := secrets.NewStore(database)
	if err := secretsStore.LoadIntoEnv(context.Background()); err != nil {
		logger.Warn("load secrets into env failed", "error", err)
	}

	// 全局运行时配置覆盖：config.yaml 是只读基线，UI 改动存 runtime_settings 表。
	// 启动时把 DB 的 per-section 覆盖应用到内存 cfg，得到最终生效配置。
	// Load 失败视为启动 fatal（r10 [Medium]）；section JSON 损坏由 ApplyOverrides 跳过+告警。
	rtStore := runtimeconfig.NewStore(database)
	overrides, err := rtStore.Load(context.Background())
	if err != nil {
		logger.Error("load runtime settings failed", "error", err)
		os.Exit(1)
	}
	if len(overrides) > 0 {
		if err := config.ApplyOverrides(cfg, overrides); err != nil {
			logger.Error("apply runtime overrides failed", "error", err)
			os.Exit(1)
		}
		slog.Info("applied runtime config overrides", "sections", len(overrides))
	}

	glossaryStore := glossary.NewStore(database)
	if cfg.RecapAI.GlossaryFile != "" {
		if data, err := os.ReadFile(cfg.RecapAI.GlossaryFile); err == nil && len(data) > 0 {
			count, _ := glossaryStore.CountGlobal(context.Background())
			if count == 0 {
				imported, err := glossaryStore.ImportMarkdown(context.Background(), "", string(data))
				if err != nil {
					slog.Warn("failed to parse legacy glossary file", "error", err)
				} else {
					slog.Info("imported legacy glossary entries", "path", cfg.RecapAI.GlossaryFile, "count", imported)
				}
			}
		}
	}

	recapTemplateStore := recap.NewTemplateStore(database)
	cookieAccountStore := biliutil.NewCookieAccountStore(database, filepath.Join(cfg.OutputRoot, ".cookies", "bilibili"))

	// 初始化通知管理器
	var notifyMgr *notify.Manager
	if cfg.Notify.Enabled {
		notifier := notify.NewNotifierFromConfig(
			cfg.Notify.Type,
			cfg.Notify.WebhookURL,
			cfg.Notify.BarkURL,
			cfg.Notify.BarkKey,
			cfg.Notify.ServerChanKey,
		)
		if notifier != nil {
			notifyMgr = notify.NewManager(notifier, cfg.Notify.Events)
			slog.Info("notify system initialized", "type", cfg.Notify.Type, "events", cfg.Notify.Events)
		}
	}
	if notifyMgr == nil {
		notifyMgr = notify.NoopManager
	}

	resolution, err := hzruntime.ResolveFFmpeg(context.Background(), cfg)
	if err != nil {
		logger.Warn("ffmpeg auto-resolve failed", "error", err)
	}
	if resolution != nil {
		if resolution.FFmpegPath != "" {
			cfg.FFmpeg = resolution.FFmpegPath
		}
		if resolution.FFprobePath != "" {
			cfg.FFprobe = resolution.FFprobePath
		}
		slog.Info("ffmpeg resolved", "ffmpeg", cfg.FFmpeg, "ffprobe", cfg.FFprobe, "source", resolution.Source)
	}

	// Auto-detect public IP for ASR temp audio server if enabled but no public_base_url configured.
	if cfg.ASRTemp.Enabled && strings.TrimSpace(cfg.ASRTemp.LocalDir) != "" && strings.TrimSpace(cfg.ASRTemp.PublicBaseURL) == "" {
		if publicIP := asr.DetectPublicIP(context.Background()); publicIP != "" {
			_, port, _ := net.SplitHostPort(cfg.Web.Listen)
			if port == "" {
				port = "6334"
			}
			cfg.ASRTemp.PublicBaseURL = "http://" + net.JoinHostPort(publicIP, port) + "/asr-temp"
			logger.Info("auto-detected public IP for ASR temp server", "ip", publicIP, "url", cfg.ASRTemp.PublicBaseURL)
		} else {
			logger.Warn("asr_temp enabled but no public IP detected, skipping local temp server")
		}
	}

	runtimeStatus := hzruntime.Probe(cfg)
	if err := runtimeStatus.StartupError(); err != nil {
		logger.Error("runtime dependency check failed", "error", err)
		os.Exit(1)
	}

	channelStore := channel.NewStore(database)
	if err := channelStore.Bootstrap(context.Background(), cfg.BootstrapChannels); err != nil {
		logger.Error("bootstrap channels failed", "error", err)
		os.Exit(1)
	}

	taskStore := worker.NewStore(database)
	taskHub := worker.NewHub()
	workerPool := worker.NewPool(taskStore, taskHub, cfg.Worker.Num, cfg)
	workerPool.SetNotifyManager(notifyMgr)
	sessionStore := session.NewStore(database)
	stateStore := state.NewStore(database)
	normalizeHandler := normalize.NewHandler(
		cfg,
		sessionStore,
		stateStore,
		normalize.FFmpegConverter{Command: cfg.FFmpeg},
	)
	asrHandler := asr.NewHandler(cfg, sessionStore, stateStore, asr.NewConfiguredTranscriber(cfg), glossaryStore)
	normalizeHandler.SetOnSuccess(func(ctx context.Context, task worker.Task) {
		ch, err := channelStore.Get(ctx, task.ChannelID)
		if err != nil || !ch.AutoASR {
			return
		}
		if runtimeStatus != nil && !runtimeStatus.Capabilities.ASRSubmit {
			slog.Warn("auto ASR skipped: ASR capability unavailable", "channel_id", task.ChannelID, "session_id", task.SessionID)
			return
		}
		if _, err := asrHandler.CreateTask(ctx, workerPool, task.SessionID); err != nil {
			slog.Warn("auto ASR failed", "channel_id", task.ChannelID, "session_id", task.SessionID, "error", err)
			_, _ = stateStore.Apply(ctx, task.SessionID, state.EventTaskFailed, "", fmt.Sprintf("auto %s task creation failed: %v", asr.TaskType, err))
		} else {
			slog.Info("auto ASR submitted", "channel_id", task.ChannelID, "session_id", task.SessionID)
		}
	})
	downloadHandler := download.NewHandler(
		cfg,
		sessionStore,
		stateStore,
		workerPool,
		download.NewConfiguredDownloader(cfg),
		channelStore,
	)
	downloadHandler.SetCookieAccountStore(cookieAccountStore)
	importHandler := importer.NewHandler(
		cfg,
		sessionStore,
		stateStore,
		workerPool,
		importer.FFmpegConverter{Command: cfg.FFmpeg},
	)
	recapProvider := recap.NewConfiguredProvider(cfg)
	recapHandler := recap.NewHandler(cfg, sessionStore, stateStore, recapProvider, glossaryStore, recapTemplateStore, channelStore)
	glossaryDiscoverer := glossary.NewDiscoverer(glossaryStore, recapProvider, sessionStore,
		glossary.WithDiscoveryTimeout(15*time.Minute),
	)
	recapHandler.SetGlossaryDiscoverer(glossaryDiscoverer)
	recapHandler.SetNotifyManager(notifyMgr)
	asrHandler.SetOnSuccess(func(ctx context.Context, task worker.Task) {
		ch, err := channelStore.Get(ctx, task.ChannelID)
		if err != nil || !ch.AutoRecap {
			return
		}
		// 回顾能力判断已下沉到 recap.CreateTask（设计 4.5），读取 server 最新运行时状态，
		// 不再在此处用启动时 Probe 的陈旧 runtimeStatus 做 gate（避免问题⑤）。
		if _, err := recapHandler.CreateTask(ctx, workerPool, task.SessionID); err != nil {
			// 能力不可用是外部配置/运行时条件，不是 ASR 成果失败：不降级 asr_done，
			// 仅日志/通知，保留用户补齐 key 后手动回顾的入口（codex 审核指出的降级 bug）。
			if errors.Is(err, recap.ErrRecapUnavailable) {
				slog.Warn("auto recap skipped: recap capability unavailable", "channel_id", task.ChannelID, "session_id", task.SessionID)
				return
			}
			slog.Warn("auto recap failed", "channel_id", task.ChannelID, "session_id", task.SessionID, "error", err)
			_, _ = stateStore.Apply(ctx, task.SessionID, state.EventTaskFailed, "", fmt.Sprintf("auto %s task creation failed: %v", recap.TaskType, err))
		} else {
			slog.Info("auto recap submitted", "channel_id", task.ChannelID, "session_id", task.SessionID)
		}
	})
	uploadHandler := upload.NewHandler(cfg, sessionStore, stateStore, upload.NewConfiguredCopier(cfg))
	archiveHandler := archive.NewHandler(cfg, sessionStore, stateStore,
		upload.NewConfiguredCopier(cfg), upload.NewConfiguredDeleter(cfg))
	wbiSigner := biliutil.NewWBISigner("")
	publisherHandler := publisher.NewHandler(cfg, sessionStore, stateStore, channelStore, publisher.NewBiliOpusClientWithSigner(wbiSigner))
	publisherHandler.SetCookieAccountStore(cookieAccountStore)
	publisherHandler.SetNotifyManager(notifyMgr)
	recapHandler.SetOnSuccess(func(ctx context.Context, task worker.Task) {
		ch, err := channelStore.Get(ctx, task.ChannelID)
		if err != nil || !ch.AutoPublish {
			return
		}
		if runtimeStatus != nil && !runtimeStatus.Capabilities.PublishOpus {
			slog.Warn("auto publish skipped: publish capability unavailable")
			return
		}
		// 回放类(回放下载/导入)的回顾不自动发布到B站(仅录播自动发布)。
		// 手动 API POST /api/sessions/:sid/publish 不受此限制，由前端隐藏动作覆盖。
		if sess, err := sessionStore.Get(ctx, task.SessionID); err == nil &&
			(sess.SourceType == "download" || sess.SourceType == "import") {
			return
		}
		if _, err := publisherHandler.CreateTask(ctx, workerPool, task.SessionID); err != nil {
			slog.Warn("auto publish failed", "error", err, "session_id", task.SessionID)
			_, _ = stateStore.Apply(ctx, task.SessionID, state.EventTaskFailed, "", fmt.Sprintf("auto %s task creation failed: %v", publisher.TaskType, err))
		} else {
			slog.Info("auto publish submitted", "session_id", task.SessionID)
		}
	})
	// 发布成功后按 archive.auto_after_publish 决定是否自动归档到 WebDAV。
	// 范本同 recap→publish 链：能力 gate + 失败仅日志/通知，不调 EventTaskFailed
	// （published 无 failed 入边语义，强行 Apply 会经 Next 全局特判把 published 降为 failed）。
	publisherHandler.SetOnSuccess(func(ctx context.Context, task worker.Task) {
		if !cfg.Archive.AutoAfterPublish {
			return
		}
		if runtimeStatus != nil && !runtimeStatus.Capabilities.WebDAVUpload {
			slog.Warn("auto archive skipped: webdav capability unavailable", "session_id", task.SessionID)
			return
		}
		if _, err := archiveHandler.CreateTask(ctx, workerPool, task.SessionID); err != nil {
			slog.Warn("auto archive failed", "session_id", task.SessionID, "error", err)
			if notifyMgr != nil {
				notifyMgr.Send(ctx, notify.EventTaskFailed, "自动归档失败",
					fmt.Sprintf("场次 %s 自动归档未启动：%v", task.SessionID, err))
			}
		} else {
			slog.Info("auto archive submitted", "session_id", task.SessionID)
		}
	})
	discoverManager := discover.NewManager(
		channelStore,
		sessionStore,
		workerPool,
		discover.YTDLPLister{Command: cfg.YTDLP},
	)
	liveManager := live_record.NewManager(
		cfg,
		channelStore,
		sessionStore,
		stateStore,
		workerPool,
		live_record.NewBilibiliClient(),
		&live_record.FFmpegRecorder{
			Command:         cfg.FFmpeg,
			StopGracePeriod: time.Duration(cfg.LiveRecord.StopGraceSeconds) * time.Second,
		},
		live_record.NewBilibiliDanmakuRecorder(),
		cookieAccountStore,
	)
	liveManager.SetNotifyManager(notifyMgr)
	normalizeHandler.Register(workerPool)
	downloadHandler.Register(workerPool)
	importHandler.Register(workerPool)
	asrHandler.Register(workerPool)
	recapHandler.Register(workerPool)
	uploadHandler.Register(workerPool)
	archiveHandler.Register(workerPool)
	publisherHandler.Register(workerPool)
	liveManager.Register(workerPool)
	liveManager.StartHealthCheck(context.Background(), 0) // 使用默认 60s 间隔
	workerPool.SetFailSessionStateFn(func(ctx context.Context, task worker.Task, event, taskID, errorMessage string, bypassState bool) error {
		if errorMessage == "" {
			errorMessage = "task failed"
		}
		// 设计 4.3：状态旁路任务（由 worker 注册时的 WithBypassFailState 声明，替代原先对
		// upload/archive TaskType 的硬编码特判）失败时仅写 last_error，不降级主状态。
		if bypassState && event == string(state.EventTaskFailed) {
			_, err := database.ExecContext(ctx, `
					UPDATE sessions
					SET last_error = COALESCE(NULLIF(last_error, ''), ?), updated_at = datetime('now')
					WHERE id = ?
				`, errorMessage, task.SessionID)
			return err
		}
		var existingError sql.NullString
		err := database.QueryRowContext(ctx, "SELECT last_error FROM sessions WHERE id = ?", task.SessionID).Scan(&existingError)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if existingError.Valid && existingError.String != "" {
			errorMessage = existingError.String
		}
		_, err = stateStore.Apply(ctx, task.SessionID, state.Event(event), taskID, errorMessage)
		return err
	})
	// 注入 live_record 进程接管回调（ISS-6）：重启后存活的 ffmpeg 进程由 Manager.Adopt 重建 activeRecord，
	// 使其可被前端"停止录制"接管。
	workerPool.SetAdoptLiveRecordFn(func(ctx context.Context, task worker.Task, pid int) {
		liveManager.Adopt(task.ChannelID, task.ID, task.SessionID, pid)
	})

	// Determine whether embedded web dist is available（提前计算，供 server 创建）
	var webFS fs.FS
	if _, statErr := fs.Stat(webDistFS, "webdist"); statErr == nil {
		sub, subErr := fs.Sub(webDistFS, "webdist")
		if subErr == nil {
			webFS = sub
			logger.Info("embedded web frontend loaded")
		} else {
			logger.Warn("web frontend embed dir present but unreadable, serving API-only fallback page", "error", subErr)
		}
	} else {
		logger.Warn("embedded web frontend is NOT loaded (binary built without -tags embedded_web); serving API-only fallback page at /")
	}

	server := handler.NewServer(
		cfg,
		runtimeStatus,
		channelStore,
		channel.NewIdentifierWithChannelStoreAndBootstrap(channelStore, cfg.BootstrapChannels),
		workerPool,
		liveManager,
		discoverManager,
		downloadHandler,
		sessionStore,
		importHandler,
		asrHandler,
		recapHandler,
		uploadHandler,
		archiveHandler,
		publisherHandler,
		secretsStore,
		rtStore,
		webFS,
		glossaryStore,
		glossaryDiscoverer,
		recapTemplateStore,
		cookieAccountStore,
		notifyMgr,
	)
	// 设计 4.5：把能力判断下沉到 recap.CreateTask，注入一个读取 server 最新运行时状态的
	// CapabilityChecker（而非 main.go 启动时 Probe 的陈旧快照）。必须在 workerPool.Start() 之前注入：
	// 否则 recoverRunning 重新入队的 ASR 任务可能在 checker 注入前完成，触发回调时 CreateTask
	// 因 checker 为 nil 绕过能力校验（codex 审核指出的注入时机窗口 + 未同步读写）。
	recapHandler.SetCapabilityChecker(runtimeCapabilityAdapter{server: server})
	if cfg.ASRTemp.NativeConfigured() {
		tempServer := asr.NewTempAudioServer(cfg)
		server.SetASRTempHandler(tempServer.MountHandler())
	}

	if err := workerPool.Start(context.Background(), cfg.Worker.Num); err != nil {
		logger.Error("start worker pool failed", "error", err)
		os.Exit(1)
	}
	defer workerPool.Stop()

	// Start cron scheduler
	sched := scheduler.New(cfg, discoverManager, liveManager, channelStore, notifyMgr)
	sched.Start()
	go func() {
		if _, err := liveManager.CheckAndStartAll(context.Background()); err != nil {
			logger.Error("initial live record check failed", "error", err)
		}
	}()
	defer sched.Stop()

	httpServer := &http.Server{
		Addr:              cfg.Web.Listen,
		Handler:           server.Router(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	listener, err := net.Listen("tcp", cfg.Web.Listen)
	if err != nil {
		logger.Error("http server listen failed", "listen", cfg.Web.Listen, "error", err)
		os.Exit(1)
	}

	serverURL := webURL(listener.Addr().String())
	logger.Info("hikami started", "listen", listener.Addr().String(), "url", serverURL)
	if cfg.Web.AutoOpenBrowser {
		openBrowser(logger, serverURL)
	}

	go func() {
		if err := httpServer.Serve(listener); err != nil && err != http.ErrServerClosed {
			logger.Error("http server failed", "error", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		logger.Error("http server shutdown failed", "error", err)
		os.Exit(1)
	}
	logger.Info("hikami stopped")
}

func webURL(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "http://" + addr
	}
	if host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
		host = "localhost"
	}
	return "http://" + net.JoinHostPort(host, port)
}

func openBrowser(logger *slog.Logger, url string) {
	if !hasDesktopSession() {
		logger.Debug("skip opening browser in headless environment", "url", url)
		return
	}

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		logger.Warn("open browser failed", "url", url, "error", err)
		return
	}
	logger.Info("opening browser", "url", url)
}

func hasDesktopSession() bool {
	switch runtime.GOOS {
	case "darwin":
		return os.Getenv("SSH_TTY") == "" && os.Getenv("SSH_CONNECTION") == ""
	case "windows":
		return os.Getenv("SESSIONNAME") != "" || os.Getenv("USERPROFILE") != ""
	default:
		return os.Getenv("DISPLAY") != "" ||
			os.Getenv("WAYLAND_DISPLAY") != "" ||
			os.Getenv("MIR_SOCKET") != ""
	}
}

// runtimeCapabilityAdapter 把 server 的最新运行时状态适配为 recap.CapabilityChecker。
// 设计 4.5：自动链/手动 API 的能力判断统一走 CreateTask，读取 server 代际刷新后的快照，
// 避免 main.go 启动时 Probe 的陈旧 runtimeStatus 导致 gate 与实际配置不一致。
type runtimeCapabilityAdapter struct {
	server *handler.Server
}

func (a runtimeCapabilityAdapter) RecapGenerate() bool {
	if a.server == nil {
		return false
	}
	status := a.server.CurrentRuntimeStatus()
	return status != nil && status.Capabilities.RecapGenerate
}
