package runtime

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"hikami-go/internal/config"
)

type Resolution struct {
	FFmpegPath  string
	FFprobePath string
	Source      string
}

var (
	lastFFmpegResolutionMu sync.RWMutex
	lastFFmpegResolution   *Resolution
)

func ResolveFFmpeg(ctx context.Context, cfg *config.Config) (*Resolution, error) {
	systemResolution, err := resolveSystemFFmpeg(cfg)
	if err == nil {
		setLastFFmpegResolution(systemResolution)
		return systemResolution, nil
	}

	platform := PlatformKey()
	asset, ok := CurrentManifest()[platform]
	if !ok {
		return nil, fmt.Errorf("unsupported ffmpeg platform: %s", platform)
	}
	if strings.TrimSpace(cfg.OutputRoot) == "" {
		return nil, errors.New("output_root is required for ffmpeg auto-resolve")
	}

	versionDir := ffmpegVersionDir(cfg.OutputRoot, platform, asset.Version)
	if resolution, err := cachedResolution(versionDir, asset, "cached"); err == nil {
		setLastFFmpegResolution(resolution)
		return resolution, nil
	}

	if data, ok := embedAssets(); ok {
		if err := installEmbeddedFFmpeg(data, versionDir, asset); err != nil {
			return nil, err
		}
		resolution, err := cachedResolution(versionDir, asset, "embedded")
		if err != nil {
			return nil, err
		}
		setLastFFmpegResolution(resolution)
		return resolution, nil
	}

	if err := downloadAndInstallFFmpeg(ctx, versionDir, asset); err != nil {
		return nil, err
	}
	resolution, err := cachedResolution(versionDir, asset, "downloaded")
	if err != nil {
		return nil, err
	}
	setLastFFmpegResolution(resolution)
	return resolution, nil
}

func resolveSystemFFmpeg(cfg *config.Config) (*Resolution, error) {
	ffmpeg, err := exec.LookPath(strings.TrimSpace(cfg.FFmpeg))
	if err != nil {
		return nil, err
	}
	ffprobe, err := exec.LookPath(strings.TrimSpace(cfg.FFprobe))
	if err != nil {
		return nil, err
	}
	return &Resolution{FFmpegPath: ffmpeg, FFprobePath: ffprobe, Source: "system"}, nil
}

func ffmpegVersionDir(outputRoot, platform, version string) string {
	return filepath.Join(outputRoot, ".runtime", "ffmpeg", platform, version)
}

func cachedResolution(versionDir string, asset FFmpegAsset, source string) (*Resolution, error) {
	ffmpeg := filepath.Join(versionDir, filepath.FromSlash(asset.FFmpegPath))
	ffprobe := filepath.Join(versionDir, filepath.FromSlash(asset.FFprobePath))
	if err := executableFile(ffmpeg); err != nil {
		return nil, err
	}
	if err := executableFile(ffprobe); err != nil {
		return nil, err
	}
	return &Resolution{FFmpegPath: ffmpeg, FFprobePath: ffprobe, Source: source}, nil
}

func executableFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("%s is a directory", path)
	}
	return nil
}

func installEmbeddedFFmpeg(data []byte, versionDir string, asset FFmpegAsset) error {
	tmpDir, err := prepareTempInstallDir(versionDir)
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	if err := extractArchive(bytes.NewReader(data), int64(len(data)), "zip", tmpDir); err != nil {
		return err
	}
	return finalizeInstall(tmpDir, versionDir, asset)
}

func downloadAndInstallFFmpeg(ctx context.Context, versionDir string, asset FFmpegAsset) error {
	if strings.TrimSpace(asset.ArchiveURL) == "" {
		return errors.New("ffmpeg archive url is empty")
	}
	tmpDir, err := prepareTempInstallDir(versionDir)
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	archivePath := filepath.Join(tmpDir, "ffmpeg.tmp")
	if err := downloadFile(ctx, asset.ArchiveURL, archivePath, asset.ArchiveSHA256); err != nil {
		_ = os.Remove(archivePath)
		return err
	}
	if err := extractArchiveFile(archivePath, asset.ArchiveFormat, tmpDir); err != nil {
		return err
	}
	return finalizeInstall(tmpDir, versionDir, asset)
}

func prepareTempInstallDir(versionDir string) (string, error) {
	parent := filepath.Dir(versionDir)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", err
	}
	return os.MkdirTemp(parent, ".tmp-ffmpeg-")
}

func downloadFile(ctx context.Context, url, destPath, expectedSHA256 string) error {
	client := &http.Client{Timeout: 5 * time.Minute}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("download ffmpeg archive failed: %s", resp.Status)
	}

	out, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer out.Close()

	hash := sha256.New()
	writer := io.MultiWriter(out, hash)
	buf := make([]byte, 128*1024)
	var downloaded int64
	var nextLog int64 = 1024 * 1024
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, err := writer.Write(buf[:n]); err != nil {
				return err
			}
			downloaded += int64(n)
			if downloaded >= nextLog {
				slog.Info("ffmpeg download progress", "bytes", downloaded)
				nextLog += 1024 * 1024
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}

	if strings.TrimSpace(expectedSHA256) == "" {
		return nil
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if !strings.EqualFold(actual, expectedSHA256) {
		return fmt.Errorf("ffmpeg archive sha256 mismatch: expected %s, got %s", expectedSHA256, actual)
	}
	return nil
}

func extractArchiveFile(path, format, destDir string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	return extractArchive(file, info.Size(), format, destDir)
}

func extractArchive(reader io.ReaderAt, size int64, format, destDir string) error {
	switch format {
	case "zip":
		return extractZip(reader, size, destDir)
	case "tgz":
		readSeeker, ok := reader.(io.ReadSeeker)
		if !ok {
			return errors.New("tgz archive reader must support seek")
		}
		return extractTgz(readSeeker, destDir)
	default:
		return fmt.Errorf("unsupported ffmpeg archive format: %s", format)
	}
}

func extractZip(reader io.ReaderAt, size int64, destDir string) error {
	zr, err := zip.NewReader(reader, size)
	if err != nil {
		return err
	}
	for _, file := range zr.File {
		if err := extractZipFile(file, destDir); err != nil {
			return err
		}
	}
	return nil
}

func extractZipFile(file *zip.File, destDir string) error {
	target, err := safeJoin(destDir, file.Name)
	if err != nil {
		return err
	}
	if file.FileInfo().IsDir() {
		return os.MkdirAll(target, 0o755)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	src, err := file.Open()
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, file.Mode())
	if err != nil {
		return err
	}
	defer dst.Close()
	_, err = io.Copy(dst, src)
	return err
}

func extractTgz(reader io.Reader, destDir string) error {
	gz, err := gzip.NewReader(reader)
	if err != nil {
		return err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if err := extractTarEntry(tr, header, destDir); err != nil {
			return err
		}
	}
	return nil
}

func extractTarEntry(reader io.Reader, header *tar.Header, destDir string) error {
	target, err := safeJoin(destDir, header.Name)
	if err != nil {
		return err
	}
	switch header.Typeflag {
	case tar.TypeDir:
		return os.MkdirAll(target, 0o755)
	case tar.TypeReg, tar.TypeRegA:
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		mode := os.FileMode(header.Mode)
		dst, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
		if err != nil {
			return err
		}
		defer dst.Close()
		_, err = io.Copy(dst, reader)
		return err
	default:
		return nil
	}
}

func safeJoin(root, name string) (string, error) {
	cleanName := filepath.Clean(filepath.FromSlash(name))
	target := filepath.Join(root, cleanName)
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("archive entry escapes destination: %s", name)
	}
	return target, nil
}

func finalizeInstall(tmpDir, versionDir string, asset FFmpegAsset) error {
	ffmpeg := filepath.Join(tmpDir, filepath.FromSlash(asset.FFmpegPath))
	ffprobe := filepath.Join(tmpDir, filepath.FromSlash(asset.FFprobePath))
	if err := executableFile(ffmpeg); err != nil {
		return fmt.Errorf("ffmpeg not found in archive: %w", err)
	}
	if err := executableFile(ffprobe); err != nil {
		return fmt.Errorf("ffprobe not found in archive: %w", err)
	}
	if err := os.Chmod(ffmpeg, 0o755); err != nil {
		return err
	}
	if err := os.Chmod(ffprobe, 0o755); err != nil {
		return err
	}
	if err := os.RemoveAll(versionDir); err != nil {
		return err
	}
	return os.Rename(tmpDir, versionDir)
}

func setLastFFmpegResolution(resolution *Resolution) {
	lastFFmpegResolutionMu.Lock()
	defer lastFFmpegResolutionMu.Unlock()
	lastFFmpegResolution = resolution
}

func getLastFFmpegResolution() *Resolution {
	lastFFmpegResolutionMu.RLock()
	defer lastFFmpegResolutionMu.RUnlock()
	if lastFFmpegResolution == nil {
		return nil
	}
	return &Resolution{
		FFmpegPath:  lastFFmpegResolution.FFmpegPath,
		FFprobePath: lastFFmpegResolution.FFprobePath,
		Source:      lastFFmpegResolution.Source,
	}
}
