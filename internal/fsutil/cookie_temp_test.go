package fsutil

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRemoveTempCookieFiles L4(2026-08-15):清扫 ytdlp_*.txt 明文 cookie 临时文件
// (下载链 ytdlp_<sessionID>.txt 与发现链 ytdlp_preview_*.txt 都命中),
// 不碰无关文件;目录不存在/空 outputRoot 幂等返回 0。
func TestRemoveTempCookieFiles(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".cookies", "bilibili")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for _, name := range []string{"ytdlp_sess_1.txt", "ytdlp_preview_abc.txt", "other.txt", "ytdlp_notes.md"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("cookie"), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	removed, err := RemoveTempCookieFiles(root)
	if err != nil {
		t.Fatalf("RemoveTempCookieFiles: %v", err)
	}
	if removed != 2 {
		t.Fatalf("removed = %d, want 2 (ytdlp_*.txt only)", removed)
	}
	for _, gone := range []string{"ytdlp_sess_1.txt", "ytdlp_preview_abc.txt"} {
		if _, err := os.Stat(filepath.Join(dir, gone)); !os.IsNotExist(err) {
			t.Fatalf("%s should be removed", gone)
		}
	}
	for _, keep := range []string{"other.txt", "ytdlp_notes.md"} {
		if _, err := os.Stat(filepath.Join(dir, keep)); err != nil {
			t.Fatalf("%s should be kept: %v", keep, err)
		}
	}

	// 幂等:再跑一次 0 删除;空 outputRoot 与不存在目录都安全。
	if removed, err := RemoveTempCookieFiles(root); err != nil || removed != 0 {
		t.Fatalf("second run: removed=%d err=%v, want 0/nil", removed, err)
	}
	if removed, err := RemoveTempCookieFiles(""); err != nil || removed != 0 {
		t.Fatalf("empty root: removed=%d err=%v, want 0/nil", removed, err)
	}
	if removed, err := RemoveTempCookieFiles(filepath.Join(root, "nope")); err != nil || removed != 0 {
		t.Fatalf("missing dir: removed=%d err=%v, want 0/nil", removed, err)
	}
}
