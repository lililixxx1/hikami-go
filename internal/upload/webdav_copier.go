package upload

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"hikami-go/internal/config"

	"github.com/studio-b12/gowebdav"
)

type WebDAVCopier struct {
	client   *gowebdav.Client
	basePath string
}

func NewWebDAVCopier(cfg *config.WebDAVConfig) *WebDAVCopier {
	return &WebDAVCopier{
		// EffectivePassword 遵循 tombstone（managed 时不回落 config.yaml 明文），
		// 与 GET 响应/能力探测一致（r12 Effective* 闭环）。
		client:   gowebdav.NewClient(cfg.URL, cfg.Username, cfg.EffectivePassword()),
		basePath: strings.Trim(cfg.BasePath, "/"),
	}
}

func (c *WebDAVCopier) Copy(ctx context.Context, source string, target string) error {
	target = c.relativeTarget(target)
	return filepath.Walk(source, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		remotePath := joinWebDAVPath(c.basePath, target, filepath.ToSlash(rel))
		if err := c.client.MkdirAll(pathDir(remotePath), 0o755); err != nil {
			return fmt.Errorf("webdav mkdir failed: %w", err)
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		if err := c.client.WriteStreamWithLength(remotePath, file, info.Size(), 0o644); err != nil {
			return fmt.Errorf("webdav write failed: %w", err)
		}
		return nil
	})
}

func (c *WebDAVCopier) Fetch(ctx context.Context, source string, target string) error {
	source = joinWebDAVPath(c.basePath, c.relativeTarget(source))
	return c.fetchDir(ctx, source, target)
}

func (c *WebDAVCopier) Delete(ctx context.Context, target string) error {
	remotePath := joinWebDAVPath(c.basePath, c.relativeTarget(target))
	if err := c.deleteRecursive(ctx, remotePath); err != nil && !isWebDAVNotExist(err) {
		return err
	}
	return nil
}

func (c *WebDAVCopier) fetchDir(ctx context.Context, remoteDir string, localDir string) error {
	infos, err := c.client.ReadDir(remoteDir)
	if err != nil {
		if isWebDAVNotExist(err) {
			return nil
		}
		return fmt.Errorf("webdav readdir failed: %w", err)
	}
	if err := os.MkdirAll(localDir, 0o755); err != nil {
		return err
	}
	for _, info := range infos {
		if err := ctx.Err(); err != nil {
			return err
		}
		remotePath := joinWebDAVPath(remoteDir, info.Name())
		localPath := filepath.Join(localDir, info.Name())
		if info.IsDir() {
			if err := c.fetchDir(ctx, remotePath, localPath); err != nil {
				return err
			}
			continue
		}
		if err := c.fetchFile(remotePath, localPath); err != nil {
			return err
		}
	}
	return nil
}

func (c *WebDAVCopier) fetchFile(remotePath string, localPath string) error {
	if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
		return err
	}
	reader, err := c.client.ReadStream(remotePath)
	if err != nil {
		return fmt.Errorf("webdav read failed: %w", err)
	}
	defer reader.Close()

	file, err := os.Create(localPath)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := io.Copy(file, reader); err != nil {
		return err
	}
	return file.Close()
}

func (c *WebDAVCopier) deleteRecursive(ctx context.Context, remotePath string) error {
	infos, err := c.client.ReadDir(remotePath)
	if err != nil {
		if isWebDAVNotExist(err) {
			return nil
		}
		return fmt.Errorf("webdav readdir failed: %w", err)
	}
	for _, info := range infos {
		if err := ctx.Err(); err != nil {
			return err
		}
		child := joinWebDAVPath(remotePath, info.Name())
		if info.IsDir() {
			if err := c.deleteRecursive(ctx, child); err != nil {
				return err
			}
			continue
		}
		if err := c.client.Remove(child); err != nil && !isWebDAVNotExist(err) {
			return fmt.Errorf("webdav remove failed: %w", err)
		}
	}
	if err := c.client.Remove(remotePath); err != nil && !isWebDAVNotExist(err) {
		return fmt.Errorf("webdav remove failed: %w", err)
	}
	return nil
}

func (c *WebDAVCopier) relativeTarget(target string) string {
	target = strings.Trim(target, "/")
	if c.basePath == "" {
		return target
	}
	if target == c.basePath {
		return ""
	}
	return strings.TrimPrefix(target, c.basePath+"/")
}

func joinWebDAVPath(parts ...string) string {
	cleaned := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.Trim(part, "/")
		if part != "" {
			cleaned = append(cleaned, part)
		}
	}
	return strings.Join(cleaned, "/")
}

func pathDir(path string) string {
	if idx := strings.LastIndex(path, "/"); idx >= 0 {
		return path[:idx]
	}
	return ""
}

func isWebDAVNotExist(err error) bool {
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	var pathErr *os.PathError
	return errors.As(err, &pathErr) && errors.Is(pathErr.Err, os.ErrNotExist)
}
