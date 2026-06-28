package importer

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"hikami-go/internal/config"
	"hikami-go/internal/normalize"
	"hikami-go/internal/session"
	"hikami-go/internal/state"
	"hikami-go/internal/worker"
)

const TaskType = "import"

type MediaConverter interface {
	Convert(ctx context.Context, inputPath string, outputPath string) error
}

type FFmpegConverter struct {
	Command string
}

func (c FFmpegConverter) Convert(ctx context.Context, inputPath string, outputPath string) error {
	command := c.Command
	if command == "" {
		command = "ffmpeg"
	}
	cmd := exec.CommandContext(ctx, command,
		"-y",
		"-hide_banner",
		"-loglevel", "warning",
		"-i", inputPath,
		"-vn",
		"-c:a", "aac",
		outputPath,
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg import failed: %w: %s", err, string(output))
	}
	return nil
}

type Handler struct {
	cfg       *config.Config
	sessions  *session.Store
	states    *state.Store
	workers   *worker.Pool
	converter MediaConverter
}

func NewHandler(cfg *config.Config, sessions *session.Store, states *state.Store, workers *worker.Pool, converter MediaConverter) *Handler {
	return &Handler{cfg: cfg, sessions: sessions, states: states, workers: workers, converter: converter}
}

func (h *Handler) Register(pool *worker.Pool) {
	pool.Register(TaskType, h.HandleTask)
}

func (h *Handler) CreateFromMultipart(ctx context.Context, input session.CreateImportInput, mediaHeader *multipart.FileHeader, danmakuHeader *multipart.FileHeader) (worker.Task, error) {
	sessionInfo, err := h.sessions.CreateImport(ctx, input)
	if err != nil {
		return worker.Task{}, err
	}
	rawDir := filepath.Join(h.cfg.OutputRoot, sessionInfo.ChannelID, sessionInfo.Slug, "raw")
	if err := os.MkdirAll(rawDir, 0o755); err != nil {
		return worker.Task{}, err
	}
	mediaName := "import.source" + filepath.Ext(mediaHeader.Filename)
	if err := saveMultipartFile(mediaHeader, filepath.Join(rawDir, mediaName)); err != nil {
		return worker.Task{}, err
	}
	if danmakuHeader != nil {
		if err := saveMultipartFile(danmakuHeader, filepath.Join(rawDir, "danmaku.jsonl")); err != nil {
			return worker.Task{}, err
		}
	}
	if err := writeJSON(filepath.Join(rawDir, "import.raw.json"), map[string]any{
		"media_file": mediaHeader.Filename,
		"danmaku_file": func() string {
			if danmakuHeader == nil {
				return ""
			}
			return danmakuHeader.Filename
		}(),
	}); err != nil {
		return worker.Task{}, err
	}
	return h.workers.Enqueue(ctx, worker.CreateInput{
		ChannelID: sessionInfo.ChannelID,
		SessionID: sessionInfo.ID,
		Type:      TaskType,
		Payload:   "{}",
	})
}

func (h *Handler) HandleTask(ctx context.Context, task worker.Task, reporter worker.Reporter) error {
	sessionInfo, err := h.sessions.Get(ctx, task.SessionID)
	if err != nil {
		return err
	}
	if _, err := h.states.Apply(ctx, task.SessionID, state.EventImportStarted, task.ID, ""); err != nil {
		return err
	}

	rawDir := filepath.Join(h.cfg.OutputRoot, task.ChannelID, sessionInfo.Slug, "raw")
	sourcePath, err := findImportSource(rawDir)
	if err != nil {
		return err
	}
	if err := reporter.Progress(ctx, 20, "converting import media"); err != nil {
		return err
	}
	audioPath := filepath.Join(rawDir, "audio.m4a")
	if err := h.converter.Convert(ctx, sourcePath, audioPath+".tmp"); err != nil {
		return err
	}
	if err := os.Rename(audioPath+".tmp", audioPath); err != nil {
		return err
	}
	if _, err := h.states.Apply(ctx, task.SessionID, state.EventImportSucceeded, task.ID, ""); err != nil {
		return err
	}
	_, err = h.workers.Enqueue(ctx, worker.CreateInput{
		ChannelID: task.ChannelID,
		SessionID: task.SessionID,
		Type:      normalize.TaskType,
		Payload:   "{}",
	})
	return err
}

func saveMultipartFile(header *multipart.FileHeader, target string) error {
	source, err := header.Open()
	if err != nil {
		return err
	}
	defer source.Close()
	destination, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer destination.Close()
	_, err = io.Copy(destination, source)
	return err
}

func findImportSource(rawDir string) (string, error) {
	entries, err := os.ReadDir(rawDir)
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if strings.HasPrefix(entry.Name(), "import.source") {
			return filepath.Join(rawDir, entry.Name()), nil
		}
	}
	return "", fmt.Errorf("import source file not found")
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}
