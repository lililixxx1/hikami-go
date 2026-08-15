package runtime

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ulikunitz/xz"
)

func writeZipArchive(t *testing.T, entries map[string]string) *bytes.Reader {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, content := range entries {
		file, err := writer.Create(name)
		if err != nil {
			t.Fatalf("create zip entry: %v", err)
		}
		if _, err := file.Write([]byte(content)); err != nil {
			t.Fatalf("write zip entry: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return bytes.NewReader(buffer.Bytes())
}

func writeTgzArchive(t *testing.T, entries map[string]string) *bytes.Reader {
	t.Helper()
	var buffer bytes.Buffer
	gz := gzip.NewWriter(&buffer)
	writer := tar.NewWriter(gz)
	for name, content := range entries {
		header := &tar.Header{
			Name: name,
			Mode: 0o644,
			Size: int64(len(content)),
		}
		if err := writer.WriteHeader(header); err != nil {
			t.Fatalf("write tar header: %v", err)
		}
		if _, err := writer.Write([]byte(content)); err != nil {
			t.Fatalf("write tar entry: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return bytes.NewReader(buffer.Bytes())
}

func writeTxzArchive(t *testing.T, entries map[string]string) *bytes.Reader {
	t.Helper()
	var buffer bytes.Buffer
	xw, err := xz.NewWriter(&buffer)
	if err != nil {
		t.Fatalf("new xz writer: %v", err)
	}
	writer := tar.NewWriter(xw)
	for name, content := range entries {
		header := &tar.Header{
			Name: name,
			Mode: 0o644,
			Size: int64(len(content)),
		}
		if err := writer.WriteHeader(header); err != nil {
			t.Fatalf("write tar header: %v", err)
		}
		if _, err := writer.Write([]byte(content)); err != nil {
			t.Fatalf("write tar entry: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := xw.Close(); err != nil {
		t.Fatalf("close xz: %v", err)
	}
	return bytes.NewReader(buffer.Bytes())
}

func TestSafeJoin_NormalPath(t *testing.T) {
	// 正常路径拼接成功
	root := t.TempDir()
	got, err := safeJoin(root, "bin/ffmpeg")
	if err != nil {
		t.Fatalf("safeJoin: %v", err)
	}
	want := filepath.Join(root, "bin", "ffmpeg")
	if got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
}

func TestSafeJoin_DirectoryTraversal(t *testing.T) {
	// ../ 路径穿越应返回错误
	if _, err := safeJoin(t.TempDir(), "../ffmpeg"); err == nil {
		t.Fatalf("expected error for directory traversal")
	}
}

func TestSafeJoin_AbsolutePath(t *testing.T) {
	// 绝对路径被 filepath.Join 规范化到 root 下，不穿越
	root := t.TempDir()
	got, err := safeJoin(root, filepath.Join(string(os.PathSeparator), "tmp", "ffmpeg"))
	if err != nil {
		t.Fatalf("safeJoin: %v", err)
	}
	// Go 1.20+ filepath.Join 不再丢弃前序元素，绝对路径被拼接到 root 下
	if !strings.HasPrefix(got, root) {
		t.Fatalf("path = %q, want under %q", got, root)
	}
}

func TestSafeJoint_MultipleDots(t *testing.T) {
	// 多层 ../ 穿越应返回错误
	if _, err := safeJoin(t.TempDir(), "bin/../../ffmpeg"); err == nil {
		t.Fatalf("expected error for multiple dot traversal")
	}
}

func TestExecutableFile_RegularFile(t *testing.T) {
	// 普通文件不返回错误
	path := filepath.Join(t.TempDir(), "ffmpeg")
	if err := os.WriteFile(path, []byte("test"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := executableFile(path); err != nil {
		t.Fatalf("executableFile: %v", err)
	}
}

func TestExecutableFile_Directory(t *testing.T) {
	// 目录返回错误
	if err := executableFile(t.TempDir()); err == nil {
		t.Fatalf("expected error for directory")
	}
}

func TestExecutableFile_NotExist(t *testing.T) {
	// 不存在返回错误
	if err := executableFile(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatalf("expected error for missing file")
	}
}

func TestExtractArchive_ZipFormat(t *testing.T) {
	// zip 数据应成功解压到目标目录
	reader := writeZipArchive(t, map[string]string{"bin/ffmpeg": "binary"})
	destDir := t.TempDir()
	if err := extractArchive(reader, int64(reader.Len()), "zip", destDir); err != nil {
		t.Fatalf("extractArchive: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(destDir, "bin", "ffmpeg"))
	if err != nil {
		t.Fatalf("read extracted file: %v", err)
	}
	if string(content) != "binary" {
		t.Fatalf("content = %q, want %q", string(content), "binary")
	}
}

func TestExtractArchive_UnsupportedFormat(t *testing.T) {
	// 不支持的格式返回错误
	reader := bytes.NewReader([]byte("test"))
	if err := extractArchive(reader, int64(reader.Len()), "rar", t.TempDir()); err == nil {
		t.Fatalf("expected error for unsupported format")
	}
}

// TestExtractArchive_TxzFormat M4(2026-08-15):linux 兜底资产是 BtbN 的 .tar.xz,
// txz 分支持有性回归——修复前该调用返回 "unsupported ffmpeg archive format"。
func TestExtractArchive_TxzFormat(t *testing.T) {
	reader := writeTxzArchive(t, map[string]string{
		"pkg/bin/ffmpeg":  "binary",
		"pkg/bin/ffprobe": "probe",
	})
	destDir := t.TempDir()
	if err := extractArchive(reader, int64(reader.Len()), "txz", destDir); err != nil {
		t.Fatalf("extractArchive: %v", err)
	}
	for name, want := range map[string]string{
		"pkg/bin/ffmpeg":  "binary",
		"pkg/bin/ffprobe": "probe",
	} {
		content, err := os.ReadFile(filepath.Join(destDir, filepath.FromSlash(name)))
		if err != nil {
			t.Fatalf("read extracted %s: %v", name, err)
		}
		if string(content) != want {
			t.Fatalf("%s content = %q, want %q", name, string(content), want)
		}
	}
}

func TestExtractTxz_SkipsPathTraversal(t *testing.T) {
	// tar.xz 内含 ../ 穿越条目应返回错误(与 tgz/zip 路径安全对称)
	reader := writeTxzArchive(t, map[string]string{"../ffmpeg": "binary"})
	if err := extractTxz(reader, t.TempDir()); err == nil {
		t.Fatalf("expected error for path traversal")
	}
}

func TestFFmpegVersionDir(t *testing.T) {
	// ffmpeg 版本目录应包含 outputRoot/.runtime/ffmpeg/platform/version
	outputRoot := t.TempDir()
	got := ffmpegVersionDir(outputRoot, "linux-amd64", "v1")
	want := filepath.Join(outputRoot, ".runtime", "ffmpeg", "linux-amd64", "v1")
	if got != want {
		t.Fatalf("dir = %q, want %q", got, want)
	}
}

func TestExtractZip_SkipsPathTraversal(t *testing.T) {
	// zip 内含 ../ 穿越条目应返回错误
	reader := writeZipArchive(t, map[string]string{"../ffmpeg": "binary"})
	if err := extractZip(reader, int64(reader.Len()), t.TempDir()); err == nil {
		t.Fatalf("expected error for path traversal")
	}
}

func TestExtractTgz_SkipsPathTraversal(t *testing.T) {
	// tgz 内含 ../ 穿越条目应返回错误
	reader := writeTgzArchive(t, map[string]string{"../ffmpeg": "binary"})
	if err := extractTgz(reader, t.TempDir()); err == nil {
		t.Fatalf("expected error for path traversal")
	}
}

func TestCachedResolution_MissingFile(t *testing.T) {
	// 缓存二进制不存在应返回错误
	asset := FFmpegAsset{FFmpegPath: "bin/ffmpeg", FFprobePath: "bin/ffprobe"}
	if _, err := cachedResolution(t.TempDir(), asset, "cached"); err == nil {
		t.Fatalf("expected error for missing cached files")
	}
}

func TestCachedResolution_Success(t *testing.T) {
	// 缓存二进制存在应解析成功
	versionDir := t.TempDir()
	binDir := filepath.Join(versionDir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	for _, name := range []string{"ffmpeg", "ffprobe"} {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte("test"), 0o755); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	asset := FFmpegAsset{FFmpegPath: "bin/ffmpeg", FFprobePath: "bin/ffprobe"}
	resolution, err := cachedResolution(versionDir, asset, "cached")
	if err != nil {
		t.Fatalf("cachedResolution: %v", err)
	}
	if resolution.FFmpegPath != filepath.Join(binDir, "ffmpeg") {
		t.Fatalf("FFmpegPath = %q", resolution.FFmpegPath)
	}
	if resolution.FFprobePath != filepath.Join(binDir, "ffprobe") {
		t.Fatalf("FFprobePath = %q", resolution.FFprobePath)
	}
	if resolution.Source != "cached" {
		t.Fatalf("Source = %q, want cached", resolution.Source)
	}
}

func TestLastFFmpegResolution_ConcurrentAccess(t *testing.T) {
	// 并发读写不应 panic
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			setLastFFmpegResolution(&Resolution{FFmpegPath: "ffmpeg", FFprobePath: "ffprobe", Source: "test"})
		}()
	}
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = getLastFFmpegResolution()
		}()
	}
	wg.Wait()
}

// TestCurrentManifest_LinuxAssetsPinned M4(2026-08-15):linux 下载兜底钉到具体 BtbN
// autobuild,不再用内容每日重写的 latest tag。校验三件事:① URL 指向具体 autobuild
// (含 /download/autobuild-,非 latest);② SHA256 已钉(64 位十六进制),下载后必经校验;
// ③ FFmpegPath/FFprobePath 顶层目录前缀与 URL 内归档名一致(BtbN autobuild 顶层目录是
// ffmpeg-N-xxxx-gpl,与 latest 的固定名不同,前缀错了解包后就找不到二进制)。
func TestCurrentManifest_LinuxAssetsPinned(t *testing.T) {
	manifest := CurrentManifest()
	for _, platform := range []string{"linux-amd64", "linux-arm64"} {
		asset, ok := manifest[platform]
		if !ok {
			t.Fatalf("%s missing from manifest", platform)
		}
		if !strings.Contains(asset.ArchiveURL, "/download/autobuild-") {
			t.Errorf("%s ArchiveURL 未钉到具体 autobuild(仍指向可变 latest?): %s", platform, asset.ArchiveURL)
		}
		if len(asset.ArchiveSHA256) != 64 || strings.Trim(asset.ArchiveSHA256, "0123456789abcdef") != "" {
			t.Errorf("%s ArchiveSHA256 不是 64 位小写十六进制: %q", platform, asset.ArchiveSHA256)
		}
		archive := asset.ArchiveURL[strings.LastIndex(asset.ArchiveURL, "/")+1:]
		prefix := strings.TrimSuffix(archive, ".tar.xz") + "/"
		if !strings.HasPrefix(asset.FFmpegPath, prefix) || !strings.HasPrefix(asset.FFprobePath, prefix) {
			t.Errorf("%s 路径前缀与归档名不一致: want prefix %q, got ffmpeg=%q ffprobe=%q",
				platform, prefix, asset.FFmpegPath, asset.FFprobePath)
		}
	}
	// windows 仍只走 embedded,不提供下载兜底(既有约定)。
	if manifest["windows-amd64"].ArchiveURL != "" {
		t.Errorf("windows-amd64 ArchiveURL 应留空(不走下载兜底)")
	}
}
