package discover

// discover/cookie.go:发现阶段 cookie 解析(2026-07-19 新增,2026-07-25 改造)。
//
// 发现阶段调 yt-dlp --flat-playlist 时,需要给 yt-dlp 传一个它可读的 Netscape cookie 文件路径。
// 但账号池里落盘的 cookie 文件可能是加密的(HIKAMI_V1 AES-GCM,yt-dlp 读不了),
// 所以不能直接传 account.CookieFile,必须先 LoadCookie 解密到内存再写明文临时文件。
//
// 两条路径都通过 CookieAccountStore 解析(2026-07-25 起 URL 模式也走 ResolveCookie):
//
//   - URL 模式(Preview/previewCore):account_id != nil 时走 ResolveCookie case 1(指定账号),
//     case 1 GetByID 失败会 fall through 到 case 2(全局默认账号);qoder I-2 加 GetByID 预检查
//     WARN 让 fallthrough 可观测(否则用户拿到默认账号 cookie 而非所选账号却无日志线索)。
//     account_id == nil 时直接取默认账号(原 2026-07-19 行为保留)。
//
//   - 频道模式(PreviewChannel/DiscoverChannel):走 ResolveCookie 完整三级链
//     (频道账号覆盖 → 全局默认 → channel.DownloadCookieFile legacy)。
//
// 临时 cookie 文件用 os.CreateTemp 保证并发唯一,
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

// resolveURLCookie 解析 URL 模式发现阶段的 cookie 文件路径(2026-07-25 改造)。
//
// 优先级:
//  1. accountID 非 nil 且 > 0(用户选了具体账号)→ 走 ResolveCookie 三级链 case 1(指定账号),
//     case 1 GetByID 失败会 fall through 到 case 2(默认账号);
//     qoder I-2 预检查 GetByID + WARN 让 fallthrough 可观测。
//  2. accountID == nil 或 == 0(用户选「默认」)→ 显式查默认下载账号 → LoadCookie 解密 → 临时文件
//  3. 都没有 → 返回空串(yt-dlp 不带 --cookies 仍能发现公开回放)
//
// 返回 (path, cleanup):
//   - path:传给 yt-dlp --cookies 的路径(临时文件 / 空串)
//   - cleanup:临时文件清理函数;nil 表示无临时文件需清理
//
// 非 ErrNoDefaultAccount 错误(DB/LoadCookie 失败)打 WARN,不阻断发现流程。
func (m *Manager) resolveURLCookie(ctx context.Context, accountID *int64) (string, func()) {
	// 1. 用户显式选了具体账号 → 走 ResolveCookie(case 1 命中 / fallthrough 默认)
	if accountID != nil && *accountID > 0 {
		if m.cookieAccounts == nil {
			return "", nil // 未注入账号池,无法解析指定账号
		}
		// qoder I-2:预检查账号是否存在/可用。ResolveCookie case 1 GetByID 失败会静默 fall through
		// 到 case 2(默认账号),用户拿到的是默认账号 cookie 而非所选账号,若无日志用户无从得知。
		if a, err := m.cookieAccounts.GetByID(ctx, *accountID); err != nil || a == nil {
			slog.Warn("discover url mode: selected account unavailable, will try default",
				"account_id", *accountID, "error", err)
		}
		// 走 ResolveCookie 三级链(与频道模式一致);qoder M-1 用 nullInt64FromPtr helper
		cookie, err := m.cookieAccounts.ResolveCookie(
			ctx,
			nullInt64FromPtr(accountID),
			sql.NullInt64{},
			"download",
			"", // URL 模式无 legacy fallback 概念
		)
		// ResolveCookie 三级全失败才返回 ErrNoDefaultAccount(case 1 失败会 fall through,不返回 error)
		if err != nil {
			if !errors.Is(err, biliutil.ErrNoDefaultAccount) {
				slog.Warn("discover url mode: resolve cookie failed",
					"account_id", *accountID, "error", err)
			}
			return "", nil
		}
		if cookie == nil {
			return "", nil
		}
		return m.writePreviewTempCookie(cookie)
	}
	// 2. 未指定账号(默认项)→ 显式查默认下载账号(原 2026-07-19 逻辑保留)
	if m.cookieAccounts == nil {
		return "", nil
	}
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
	// 3. 加载到内存(自动解密 HIKAMI_V1 格式)
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
