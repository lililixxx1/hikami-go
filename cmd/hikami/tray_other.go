//go:build !windows || !systray

package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

// shutdownCoordinator 统一管理关闭流程，保证幂等。
// 非 Windows/非托盘构建：与 tray_windows.go 结构体字段一致，
// requestShutdown 逻辑一致但不调 systray.Quit（无托盘）。
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
	})
}

func (sc *shutdownCoordinator) Err() error {
	return sc.err
}

// runTray 非 Windows/非托盘构建：保持现有 signal.Notify 阻塞行为。
// 信号监听只在此处（main 不重复监听）。
func runTray(sc *shutdownCoordinator, serverURL string, logger *slog.Logger) {
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(stop)

	select {
	case <-stop:
		sc.requestShutdown("signal")
	case <-sc.done:
	}
}

// initLogFile 非 Windows：返回 stdout，不写文件（保持现有行为）。
func initLogFile() (io.Writer, func(), error) {
	return os.Stdout, func() {}, nil
}
