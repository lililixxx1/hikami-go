package asr

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"hikami-go/internal/config"
	"hikami-go/internal/session"
)

const asrTempPrefix = "/asr-temp/"

type TempAudioServer struct {
	cfg *config.Config
}

func NewTempAudioServer(cfg *config.Config) *TempAudioServer {
	return &TempAudioServer{cfg: cfg}
}

func (s *TempAudioServer) MountHandler() http.Handler {
	return http.StripPrefix(asrTempPrefix, http.FileServer(http.Dir(s.cfg.ASRTemp.LocalDir)))
}

// ObjectPath 是音频对象在本 server 下的相对路径,Publish 与
// DashScopeTranscriber.remotePathFor(成功后清理)共用同一构造。
func (s *TempAudioServer) ObjectPath(sessionInfo session.Session) string {
	return filepath.ToSlash(filepath.Join(sessionInfo.ChannelID, sessionInfo.ID, "audio.asr.mp3"))
}

func (s *TempAudioServer) Publish(ctx context.Context, localAudio string, sessionInfo session.Session) (publicURL string, objectPath string, err error) {
	objectPath = s.ObjectPath(sessionInfo)
	targetPath, err := s.localPath(objectPath)
	if err != nil {
		return "", "", err
	}
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return "", "", err
	}
	if err := copyFile(localAudio, targetPath); err != nil {
		return "", "", fmt.Errorf("publish asr audio failed: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	publicURL = strings.TrimRight(s.cfg.ASRTemp.PublicBaseURL, "/") + "/" + objectPath
	return publicURL, objectPath, nil
}

func (s *TempAudioServer) Delete(ctx context.Context, objectPath string) error {
	targetPath, err := s.localPath(objectPath)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Remove(targetPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	s.cleanupEmptyParents(filepath.Dir(targetPath))
	return nil
}

func (s *TempAudioServer) localPath(objectPath string) (string, error) {
	basePath, err := filepath.Abs(s.cfg.ASRTemp.LocalDir)
	if err != nil {
		return "", err
	}
	cleanObject := strings.TrimPrefix(filepath.Clean("/"+filepath.FromSlash(objectPath)), string(filepath.Separator))
	targetPath := filepath.Join(basePath, cleanObject)
	rel, err := filepath.Rel(basePath, targetPath)
	if err != nil {
		return "", err
	}
	if rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return "", fmt.Errorf("invalid asr temp object path: %s", objectPath)
	}
	return targetPath, nil
}

func (s *TempAudioServer) cleanupEmptyParents(dir string) {
	basePath, err := filepath.Abs(s.cfg.ASRTemp.LocalDir)
	if err != nil {
		return
	}
	for {
		rel, err := filepath.Rel(basePath, dir)
		if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
			return
		}
		if err := os.Remove(dir); err != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}
