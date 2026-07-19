package discover

// discover/cookie.go:发现阶段 cookie 解析(2026-07-19 新增)。
//
// 发现阶段调 yt-dlp --flat-playlist 时,需要给 yt-dlp 传一个它可读的 Netscape cookie 文件路径。
// 但账号池里落盘的 cookie 文件可能是加密的(HIKAMI_V1 AES-GCM,yt-dlp 读不了),
// 所以不能直接传 account.CookieFile。
//
// 两条独立路径(codex r15b HIGH #1/#2 拆分):
//
//   - URL 模式(Preview/previewCore):优先级是「用户显式 cookie → 全局默认账号 → 空串」。
//     用户显式优先级最高(URL 模式的 CookieFile 是"用户覆盖"),不走 ResolveCookie
//     (它的 legacy fallback 在默认账号之后,与本路径优先级相反,且会掩盖 DB 错误)。
//     显式调 GetDefaultDownload 以便 DB 错误单独打 WARN(codex r15b MEDIUM #4)。
//
//   - 频道模式(PreviewChannel/DiscoverChannel):走 ResolveCookie 完整三级链
//     「频道账号覆盖 → 全局默认 → channel.DownloadCookieFile(legacy)」,
//     与下载阶段 download.go:641-642 完全对齐。
//
// 临时 cookie 文件用 os.CreateTemp 保证并发唯一(codex r15b MEDIUM #5),
// 调用方负责在 Lister.List 返回后立即 os.Remove。

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"hikami-go/internal/biliutil"
)

// resolveURLCookie 解析 URL 模式发现阶段的 cookie 文件路径。
//
// 优先级:
//  1. 用户显式 explicitCookieFile 非空(含纯空白视为空)→ 原值返回(不创建临时文件)
//  2. 全局默认下载账号(cookieAccounts.GetDefaultDownload)→ 加载到内存(自动解密)+ 写明文临时文件
//  3. 都没有 → 返回空串(yt-dlp 不带 --cookies 仍能发现公开回放)
//
// 返回 (path, cleanup):
//   - path:传给 yt-dlp --cookies 的路径(用户原路径 / 临时文件 / 空串)
//   - cleanup:临时文件清理函数;nil 表示无临时文件需清理
//
// 非 ErrNoDefaultAccount 错误(DB/LoadCookie 失败)打 WARN,不阻断发现流程。
func (m *Manager) resolveURLCookie(ctx context.Context, explicitCookieFile string) (string, func()) {
	// 1. 用户显式覆盖位(URL 模式:用户填的优先级最高)
	if strings.TrimSpace(explicitCookieFile) != "" {
		return explicitCookieFile, nil
	}
	// 2. 未注入账号池,无可用 cookie
	if m.cookieAccounts == nil {
		return "", nil
	}
	// 3. 显式查默认下载账号(DB 错误单独打 WARN,不被 ResolveCookie 掩盖)
	account, err := m.cookieAccounts.GetDefaultDownload(ctx)
	if err != nil {
		if !errors.Is(err, biliutil.ErrNoDefaultAccount) {
			slog.Warn("discover url mode: get default download account failed", "error", err)
		}
		return "", nil
	}
	if account == nil {
		return "", nil
	}
	// 4. 加载到内存(自动解密 HIKAMI_V1 格式)
	cookie, err := biliutil.LoadCookie(account.CookieFile)
	if err != nil {
		slog.Warn("discover url mode: load default account cookie failed",
			"cookie_file", account.CookieFile, "error", err)
		return "", nil
	}
	return m.writePreviewTempCookie(cookie)
}

// resolveChannelCookie 解析频道模式发现阶段的 cookie 文件路径。
//
// 完全走 ResolveCookie,优先级与下载阶段 download.go:641-642 完全一致:
//  1. 频道账号覆盖(channel.DownloadAccountID)
//  2. 全局默认下载账号
//  3. channel.DownloadCookieFile(legacy fallback)
//
// helper 内不做任何"legacy 非空直返"判断(codex r15b HIGH #1),
// 全部交给 ResolveCookie 处理整个三级链。
//
// 返回 (path, cleanup):
//   - path:传给 yt-dlp --cookies 的路径(临时文件 / legacy 原路径 / 空串)
//   - cleanup:临时文件清理函数;nil 表示无临时文件需清理(走 legacy 原路径或空串时)
//
// ResolveCookie 失败时退化到 legacy 原路径(与下载链路一致:账号坏但 legacy 可用时继续)。
func (m *Manager) resolveChannelCookie(
	ctx context.Context,
	downloadAccountID *int64,
	legacyCookieFile string,
) (string, func()) {
	// 未注入账号池:退化到 legacy 文件(旧行为,零回归)
	if m.cookieAccounts == nil {
		return strings.TrimSpace(legacyCookieFile), nil
	}
	// 走 ResolveCookie 三级链(频道账号 → 全局默认 → legacy)
	cookie, err := m.cookieAccounts.ResolveCookie(
		ctx,
		nullInt64FromPtr(downloadAccountID),
		sql.NullInt64{},
		"download",
		legacyCookieFile,
	)
	if err != nil {
		// ResolveCookie 失败:DB/解密类错误打 WARN;ErrNoDefaultAccount 静默降级
		if !errors.Is(err, biliutil.ErrNoDefaultAccount) {
			slog.Warn("discover channel mode: resolve cookie failed, falling back to legacy",
				"error", err)
		}
		// 退化到 legacy 原路径(若有)
		return strings.TrimSpace(legacyCookieFile), nil
	}
	if cookie == nil {
		return strings.TrimSpace(legacyCookieFile), nil
	}
	return m.writePreviewTempCookie(cookie)
}

// writePreviewTempCookie 把内存 cookie 写成 yt-dlp 可读的明文 Netscape 临时文件。
//
// 文件名用 os.CreateTemp 保证并发唯一(codex r15b MEDIUM #5),
// 模式固定 ytdlp_preview_*.txt(下载阶段用 ytdlp_<sessionID>.txt,发现阶段无 sessionID 故用随机后缀)。
//
// 失败时打 WARN 并返回空路径 + nil cleanup(让 yt-dlp 走无 cookie 路径,不阻断发现)。
func (m *Manager) writePreviewTempCookie(cookie *biliutil.BiliCookie) (string, func()) {
	if cookie == nil {
		return "", nil
	}
	root := m.outputRoot
	if root == "" {
		root = "."
	}
	dir := filepath.Join(root, ".cookies", "bilibili")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		slog.Warn("discover: create temp cookie dir failed", "dir", dir, "error", err)
		return "", nil
	}
	f, err := os.CreateTemp(dir, "ytdlp_preview_*.txt")
	if err != nil {
		slog.Warn("discover: create temp cookie file failed", "error", err)
		return "", nil
	}
	path := f.Name()
	if _, err := f.Write(cookie.NetscapeBytes()); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		slog.Warn("discover: write temp cookie failed", "error", err)
		return "", nil
	}
	_ = f.Close()
	return path, func() { _ = os.Remove(path) }
}

// nullInt64FromPtr 把 *int64 转为 sql.NullInt64(nil → 无效)。
// 复刻自 download/nullInt64FromPtr 与 live_record/manager.go,供 ResolveCookie 接收频道账号覆盖。
// (download 包内的同名 helper 不能跨包复用,discover 包内独立定义一份。)
func nullInt64FromPtr(value *int64) sql.NullInt64 {
	if value == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Valid: true, Int64: *value}
}
