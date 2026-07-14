//go:build windows && systray

package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"fyne.io/systray"
)

// shutdownCoordinator 统一管理关闭流程，保证幂等。
// 所有退出入口（托盘菜单、信号）都调 requestShutdown，不各自直接关 HTTP server。
type shutdownCoordinator struct {
	once       sync.Once
	done       chan struct{}
	err        error
	httpServer *http.Server
	logger     *slog.Logger
}

func newShutdownCoordinator(httpServer *http.Server, logger *slog.Logger) *shutdownCoordinator {
	return &shutdownCoordinator{
		done:       make(chan struct{}),
		httpServer: httpServer,
		logger:     logger,
	}
}

// requestShutdown 幂等触发优雅关闭。
// 先关 HTTP server，再调 systray.Quit() 让 systray.Run() 返回，
// 从而让 main() 返回并执行 defer 清理（worker/scheduler/DB/logCleanup）。
// 不调 os.Exit，让 defer 链自然执行。
func (sc *shutdownCoordinator) requestShutdown(reason string) {
	sc.once.Do(func() {
		sc.logger.Info("shutdown requested", "reason", reason)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := sc.httpServer.Shutdown(ctx); err != nil {
			sc.err = err
			sc.logger.Error("http server shutdown failed", "error", err)
		}
		sc.logger.Info("hikami stopped")
		close(sc.done)
		systray.Quit() // 让 systray.Run() 返回，main 继续走到 defer
	})
}

func (sc *shutdownCoordinator) Err() error {
	return sc.err
}

// runTray 阻塞主线程运行托盘（Win32 消息循环必须在主线程）。
func runTray(sc *shutdownCoordinator, serverURL string, logger *slog.Logger) {
	// 信号 goroutine：Windows GUI 下作系统关闭兜底
	go func() {
		stop := make(chan os.Signal, 1)
		signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
		defer signal.Stop(stop)
		<-stop
		sc.requestShutdown("signal")
	}()

	systray.Run(func() {
		systray.SetIcon(iconBytes)
		systray.SetTooltip("Hikami-Go · " + serverURL)
		mOpen := systray.AddMenuItem("打开管理界面", "")
		mQuit := systray.AddMenuItem("退出", "")

		go func() {
			for {
				select {
				case <-mOpen.ClickedCh:
					openBrowser(logger, serverURL)
				case <-mQuit.ClickedCh:
					sc.requestShutdown("tray_quit")
				case <-sc.done:
					return
				}
			}
		}()
	}, func() {
		// onExit: systray.Run 即将返回
	})
}

// initLogFile 返回日志 writer + cleanup 函数。
// Windows GUI 模式下 stdout 可能不可写，所以日志写文件。
// 优先写 %LOCALAPPDATA%/Hikami-Go/hikami.log（用户可写目录），
// 失败时回退到 exe 同目录 hikami.log（便携模式）。
func initLogFile() (io.Writer, func(), error) {
	// 优先：用户可写目录 %LOCALAPPDATA%/Hikami-Go/
	if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
		logDir := filepath.Join(localAppData, "Hikami-Go")
		if mkErr := os.MkdirAll(logDir, 0755); mkErr == nil {
			logPath := filepath.Join(logDir, "hikami.log")
			f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
			if err == nil {
				return f, func() { f.Close() }, nil
			}
		}
	}
	// 回退：exe 同目录（便携模式）
	logPath := filepath.Join(filepath.Dir(os.Args[0]), "hikami.log")
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, func() {}, err
	}
	return f, func() { f.Close() }, nil
}
