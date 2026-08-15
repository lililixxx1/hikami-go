package fsutil

import (
	"errors"
	"os"
	"path/filepath"
)

// RemoveTempCookieFiles 清扫 yt-dlp 明文 cookie 临时文件(L4,2026-08-15)。
//
// 下载(download.writeTempCookieFile)/发现(discover.writePreviewTempCookie)链路
// 会把账号池 cookie 解成 <outputRoot>/.cookies/bilibili/ytdlp_*.txt 明文文件
// 供 yt-dlp 读取,正常路径用完即删;进程崩溃/断电会残留——明文登录凭证落盘。
// 启动时调用(此时必然没有在用任务),幂等,目录/文件不存在不算错误。
// 模式 ytdlp_*.txt 同时覆盖 ytdlp_<sessionID>.txt 与 ytdlp_preview_*.txt。
// 返回删除的文件数。
func RemoveTempCookieFiles(outputRoot string) (int, error) {
	if outputRoot == "" {
		return 0, nil
	}
	matches, err := filepath.Glob(filepath.Join(outputRoot, ".cookies", "bilibili", "ytdlp_*.txt"))
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, path := range matches {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return removed, err
		}
		removed++
	}
	return removed, nil
}
