package asr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"hikami-go/internal/config"
	"hikami-go/internal/executil"
	"hikami-go/internal/session"
)

type DashScopeTranscriber struct {
	cfg         *config.Config
	httpClient  *http.Client
	rclone      string
	tempServer  *TempAudioServer
	s3Publisher *S3Publisher
}

type dashScopeLogContextKey string

const (
	dashScopeChannelIDKey dashScopeLogContextKey = "channel_id"
	dashScopeSessionIDKey dashScopeLogContextKey = "session_id"
)

func NewConfiguredTranscriber(cfg *config.Config) Transcriber {
	// 走 EffectiveAPIKeyEnv 兜底,与 probe/handler 一致:空 env 名视为 DASHSCOPE_API_KEY(codex 审核高)。
	if cfg == nil || os.Getenv(cfg.DashScope.EffectiveAPIKeyEnv()) == "" {
		return LocalTranscriber{}
	}
	hasPublishBackend := cfg.ASRTemp.NativeConfigured() ||
		cfg.ASRS3.Configured() ||
		(cfg.ASRTemp.RcloneConfigured() && cfg.ASRTemp.PublicBaseURL != "")
	if !hasPublishBackend {
		return LocalTranscriber{}
	}
	transcriber := &DashScopeTranscriber{
		cfg:        cfg,
		httpClient: &http.Client{Timeout: 60 * time.Second},
	}
	if cfg.ASRTemp.NativeConfigured() {
		transcriber.tempServer = NewTempAudioServer(cfg)
	} else if cfg.ASRS3.Configured() {
		s3p, err := NewS3Publisher(cfg)
		if err != nil {
			slog.Error("asr_s3: init failed, falling back to local", "error", err)
			return LocalTranscriber{}
		}
		transcriber.s3Publisher = s3p
	} else {
		transcriber.rclone = cfg.Rclone
	}
	return transcriber
}

func (t *DashScopeTranscriber) Transcribe(ctx context.Context, audioPath string, sessionInfo session.Session) (Result, error) {
	return t.transcribe(ctx, audioPath, sessionInfo, nil)
}

func (t *DashScopeTranscriber) TranscribeWithVocabulary(ctx context.Context, audioPath string, sessionInfo session.Session, vocabulary map[string]int) (Result, error) {
	return t.transcribe(ctx, audioPath, sessionInfo, vocabulary)
}

func (t *DashScopeTranscriber) transcribe(ctx context.Context, audioPath string, sessionInfo session.Session, vocabulary map[string]int) (Result, error) {
	startedAt := time.Now()
	model := NormalizeDashScopeASRModel(t.cfg.DashScope.Model)
	logCtx := context.WithValue(ctx, dashScopeChannelIDKey, sessionInfo.ChannelID)
	logCtx = context.WithValue(logCtx, dashScopeSessionIDKey, sessionInfo.ID)
	slog.Info("dashscope asr transcribe started",
		"channel_id", sessionInfo.ChannelID,
		"session_id", sessionInfo.ID,
		"audio_path", filepath.ToSlash(audioPath),
		"model", model,
		"request_mode", DashScopeRequestMode(model))

	publicURL, remotePath, err := t.publishAudio(ctx, audioPath, sessionInfo)
	if err != nil {
		return Result{}, err
	}
	if t.cfg.ASRTemp.CleanupAfterSuccess {
		defer t.cleanupRemote(ctx, remotePath)
	}

	taskID, submitRaw, err := t.submit(logCtx, publicURL, vocabulary)
	if err != nil {
		return Result{}, err
	}
	slog.Info("dashscope asr task submitted",
		"channel_id", sessionInfo.ChannelID,
		"session_id", sessionInfo.ID,
		"task_id", taskID)
	taskRaw, resultURL, err := t.poll(logCtx, taskID)
	if err != nil {
		return Result{}, err
	}
	resultRaw := map[string]any{}
	if resultURL != "" {
		resultRaw, err = t.fetchResult(logCtx, resultURL)
		if err != nil {
			return Result{}, err
		}
	}
	result := buildResultFromDashScope(sessionInfo, submitRaw, taskRaw, resultRaw)
	slog.Info("dashscope asr transcribe completed",
		"channel_id", sessionInfo.ChannelID,
		"session_id", sessionInfo.ID,
		"segments", len(result.Segments),
		"transcript_len", len(result.Transcript),
		"duration", time.Since(startedAt).String())
	return result, nil
}

func (t *DashScopeTranscriber) TranscribeWithTaskID(ctx context.Context, audioPath string, sessionInfo session.Session, taskID string) (Result, error) {
	return t.TranscribeWithTaskIDAndVocabulary(ctx, audioPath, sessionInfo, taskID, nil)
}

func (t *DashScopeTranscriber) TranscribeWithTaskIDAndVocabulary(ctx context.Context, audioPath string, sessionInfo session.Session, taskID string, vocabulary map[string]int) (Result, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return t.transcribe(ctx, audioPath, sessionInfo, vocabulary)
	}
	startedAt := time.Now()
	logCtx := context.WithValue(ctx, dashScopeChannelIDKey, sessionInfo.ChannelID)
	logCtx = context.WithValue(logCtx, dashScopeSessionIDKey, sessionInfo.ID)
	slog.Info("dashscope asr task recovered",
		"channel_id", sessionInfo.ChannelID,
		"session_id", sessionInfo.ID,
		"task_id", taskID)
	taskRaw, resultURL, err := t.checkTask(logCtx, taskID)
	if err == nil {
		status := dashScopeTaskStatus(taskRaw)
		if status == "SUCCEEDED" {
			resultRaw := map[string]any{}
			if resultURL != "" {
				resultRaw, err = t.fetchResult(logCtx, resultURL)
				if err != nil {
					return Result{}, err
				}
			}
			result := buildResultFromDashScope(sessionInfo, map[string]any{"task_id": taskID, "recovered": true}, taskRaw, resultRaw)
			slog.Info("dashscope asr transcribe completed",
				"channel_id", sessionInfo.ChannelID,
				"session_id", sessionInfo.ID,
				"segments", len(result.Segments),
				"transcript_len", len(result.Transcript),
				"duration", time.Since(startedAt).String())
			return result, nil
		}
		if status != "FAILED" && status != "CANCELED" && status != "CANCELLED" {
			taskRaw, resultURL, err = t.poll(logCtx, taskID)
			if err == nil {
				resultRaw := map[string]any{}
				if resultURL != "" {
					resultRaw, err = t.fetchResult(logCtx, resultURL)
					if err != nil {
						return Result{}, err
					}
				}
				result := buildResultFromDashScope(sessionInfo, map[string]any{"task_id": taskID, "recovered": true}, taskRaw, resultRaw)
				slog.Info("dashscope asr transcribe completed",
					"channel_id", sessionInfo.ChannelID,
					"session_id", sessionInfo.ID,
					"segments", len(result.Segments),
					"transcript_len", len(result.Transcript),
					"duration", time.Since(startedAt).String())
				return result, nil
			}
		}
	}
	return t.transcribe(ctx, audioPath, sessionInfo, vocabulary)
}

func (t *DashScopeTranscriber) publishAudio(ctx context.Context, audioPath string, sessionInfo session.Session) (string, string, error) {
	if t.tempServer != nil {
		return t.tempServer.Publish(ctx, audioPath, sessionInfo)
	}
	if t.s3Publisher != nil {
		return t.s3Publisher.Publish(ctx, audioPath, sessionInfo)
	}
	remoteObject := filepath.ToSlash(filepath.Join(t.cfg.ASRTemp.BasePath, sessionInfo.ChannelID, sessionInfo.ID, "audio.asr.mp3"))
	remotePath := t.cfg.ASRTemp.RcloneRemote + remoteObject
	command := t.rclone
	if command == "" {
		command = "rclone"
	}
	cmd := exec.CommandContext(ctx, command, "copyto", audioPath, remotePath)
	executil.HideWindow(cmd)
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", "", fmt.Errorf("publish asr audio failed: %w: %s", err, string(output))
	}
	publicURL := strings.TrimRight(t.cfg.ASRTemp.PublicBaseURL, "/") + "/" + remoteObject
	return publicURL, remotePath, nil
}

func (t *DashScopeTranscriber) cleanupRemote(ctx context.Context, remotePath string) {
	if t.tempServer != nil {
		_ = t.tempServer.Delete(ctx, remotePath)
		return
	}
	if t.s3Publisher != nil {
		_ = t.s3Publisher.Delete(ctx, remotePath)
		return
	}
	command := t.rclone
	if command == "" {
		command = "rclone"
	}
	delCmd := exec.CommandContext(ctx, command, "deletefile", remotePath)
	executil.HideWindow(delCmd)
	_ = delCmd.Run()
}

func (t *DashScopeTranscriber) submit(ctx context.Context, publicURL string, vocabulary map[string]int) (string, map[string]any, error) {
	body := buildDashScopeSubmitBody(&t.cfg.DashScope, publicURL, vocabulary)
	raw, err := t.doJSONWithRetry(ctx, http.MethodPost, t.cfg.DashScope.ASRURL, body)
	if err != nil {
		return "", nil, err
	}
	taskID := lookupString(raw, "output", "task_id")
	if taskID == "" {
		taskID = lookupString(raw, "task_id")
	}
	if taskID == "" {
		return "", raw, fmt.Errorf("dashscope submit response missing task_id")
	}
	return taskID, raw, nil
}

func buildDashScopeSubmitBody(cfg *config.DashScopeConfig, publicURL string, vocabulary map[string]int) map[string]any {
	normalizedModel := NormalizeDashScopeASRModel(cfg.Model)
	body := map[string]any{
		"model": normalizedModel,
		"input": map[string]any{},
		"parameters": map[string]any{
			"channel_id": []int{0},
		},
	}

	input := body["input"].(map[string]any)
	parameters := body["parameters"].(map[string]any)
	if IsQwenFileTransModel(normalizedModel) {
		input["file_url"] = publicURL
		if cfg.Language != "" {
			parameters["language"] = cfg.Language
		}
		parameters["enable_itn"] = false
		return body
	}

	// 非qwen模型使用 file_urls 模式
	input["file_urls"] = []string{publicURL}
	if cfg.Language != "" {
		parameters["language_hints"] = []string{cfg.Language}
	}
	if strings.EqualFold(normalizedModel, "fun-asr") && len(vocabulary) > 0 {
		parameters["vocabulary"] = vocabulary
	}

	// 说话人分离
	if cfg.DiarizationEnabled {
		parameters["diarization_enabled"] = true
		if cfg.SpeakerCount > 0 {
			parameters["speaker_count"] = cfg.SpeakerCount
		}
	}

	// 热词
	if cfg.VocabularyID != "" {
		body["vocabulary_id"] = cfg.VocabularyID
	}

	return body
}

func NormalizeDashScopeASRModel(model string) string {
	trimmed := strings.TrimSpace(strings.ToLower(model))
	switch trimmed {
	case "":
		return "fun-asr"
	case "qwen-asr":
		return "qwen3-asr-flash-filetrans"
	default:
		return trimmed
	}
}

func IsQwenFileTransModel(model string) bool {
	return strings.EqualFold(model, "qwen3-asr-flash-filetrans")
}

func DashScopeRequestMode(model string) string {
	if IsQwenFileTransModel(NormalizeDashScopeASRModel(model)) {
		return "file_url"
	}
	return "file_urls"
}

func (t *DashScopeTranscriber) poll(ctx context.Context, taskID string) (map[string]any, string, error) {
	endpoint := strings.TrimRight(t.cfg.DashScope.TasksURL, "/") + "/" + taskID
	var last map[string]any
	lastStatus := ""
	consecutiveFailures := 0
	for i := 0; i < 120; i++ {
		raw, err := t.doJSONWithRetry(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			consecutiveFailures++
			if consecutiveFailures > 10 {
				return nil, "", err
			}
			select {
			case <-ctx.Done():
				return nil, "", ctx.Err()
			case <-time.After(5 * time.Second):
			}
			continue
		}
		consecutiveFailures = 0
		last = raw
		status := dashScopeTaskStatus(raw)
		if status != "" && status != lastStatus {
			slog.Info("dashscope asr task status changed",
				"channel_id", ctx.Value(dashScopeChannelIDKey),
				"session_id", ctx.Value(dashScopeSessionIDKey),
				"task_id", taskID,
				"status", status)
			lastStatus = status
		}
		if status == "SUCCEEDED" {
			return raw, findResultURL(raw), nil
		}
		if status == "FAILED" || status == "CANCELED" || status == "CANCELLED" {
			return raw, "", fmt.Errorf("dashscope task %s ended with status %s", taskID, status)
		}
		select {
		case <-ctx.Done():
			return nil, "", ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
	return last, "", fmt.Errorf("dashscope task %s polling timeout", taskID)
}

func (t *DashScopeTranscriber) checkTask(ctx context.Context, taskID string) (map[string]any, string, error) {
	endpoint := strings.TrimRight(t.cfg.DashScope.TasksURL, "/") + "/" + taskID
	raw, err := t.doJSONWithRetry(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, "", err
	}
	return raw, findResultURL(raw), nil
}

func dashScopeTaskStatus(raw map[string]any) string {
	status := strings.ToUpper(lookupString(raw, "output", "task_status"))
	if status == "" {
		status = strings.ToUpper(lookupString(raw, "task_status"))
	}
	return status
}

func (t *DashScopeTranscriber) fetchResult(ctx context.Context, resultURL string) (map[string]any, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, resultURL, nil)
	if err != nil {
		return nil, err
	}
	response, err := t.httpClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("dashscope result http status %d", response.StatusCode)
	}
	var raw map[string]any
	return raw, json.NewDecoder(response.Body).Decode(&raw)
}

type dashScopeHTTPError struct {
	statusCode int
	body       string
}

func (e *dashScopeHTTPError) Error() string {
	return fmt.Sprintf("dashscope http status %d: %s", e.statusCode, e.body)
}

type dashScopeNetworkError struct {
	err error
}

func (e *dashScopeNetworkError) Error() string {
	return e.err.Error()
}

func (e *dashScopeNetworkError) Unwrap() error {
	return e.err
}

func (t *DashScopeTranscriber) doJSONWithRetry(ctx context.Context, method string, endpoint string, body any) (map[string]any, error) {
	delays := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}
	var lastErr error
	for attempt := 0; attempt <= len(delays); attempt++ {
		raw, err := t.doJSON(ctx, method, endpoint, body)
		if err == nil {
			return raw, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		lastErr = err
		if attempt == len(delays) || !shouldRetryDashScopeError(err) {
			break
		}
		delay := delays[attempt]
		slog.Info("dashscope request retrying",
			"channel_id", ctx.Value(dashScopeChannelIDKey),
			"session_id", ctx.Value(dashScopeSessionIDKey),
			"attempt", attempt+1,
			"reason", err.Error())
		if delay > 10*time.Second {
			delay = 10 * time.Second
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
	}
	return nil, lastErr
}

func shouldRetryDashScopeError(err error) bool {
	var httpErr *dashScopeHTTPError
	if errors.As(err, &httpErr) {
		return httpErr.statusCode == http.StatusTooManyRequests || httpErr.statusCode >= 500
	}
	var networkErr *dashScopeNetworkError
	return errors.As(err, &networkErr)
}

func (t *DashScopeTranscriber) doJSON(ctx context.Context, method string, endpoint string, body any) (map[string]any, error) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(data)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+os.Getenv(t.cfg.DashScope.EffectiveAPIKeyEnv()))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-DashScope-Async", "enable")
	response, err := t.httpClient.Do(request)
	if err != nil {
		return nil, &dashScopeNetworkError{err: err}
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(response.Body)
		return nil, &dashScopeHTTPError{statusCode: response.StatusCode, body: string(body)}
	}
	var raw map[string]any
	return raw, json.NewDecoder(response.Body).Decode(&raw)
}

func buildResultFromDashScope(sessionInfo session.Session, submitRaw map[string]any, taskRaw map[string]any, resultRaw map[string]any) Result {
	transcript := extractTranscript(resultRaw)
	segments := extractSegments(resultRaw)
	if transcript == "" {
		transcript = fmt.Sprintf("# %s\n\n（DashScope 任务完成，但结果中未解析到文本。）\n", sessionInfo.Title)
	}
	return Result{
		Transcript: transcript,
		SRT:        buildSRT(segments),
		Segments:   segments,
		Raw: map[string]any{
			"provider":  "dashscope",
			"submit":    submitRaw,
			"task":      taskRaw,
			"result":    resultRaw,
			"generated": time.Now().Format(time.RFC3339),
		},
	}
}

func extractTranscript(raw map[string]any) string {
	if value := lookupString(raw, "transcription"); value != "" {
		return value
	}
	if value := lookupString(raw, "text"); value != "" {
		return value
	}
	if value := lookupString(raw, "output", "text"); value != "" {
		return value
	}
	if value := extractTranscriptList(raw["transcripts"]); value != "" {
		return value
	}
	return ""
}

func extractTranscriptList(value any) string {
	items, ok := value.([]any)
	if !ok {
		return ""
	}
	var builder strings.Builder
	for _, item := range items {
		object, ok := item.(map[string]any)
		if !ok {
			continue
		}
		text, _ := object["text"].(string)
		if text == "" {
			text = joinSentenceText(object["sentences"])
		}
		if text == "" {
			continue
		}
		if builder.Len() > 0 {
			builder.WriteString("\n")
		}
		builder.WriteString(text)
	}
	return builder.String()
}

func joinSentenceText(value any) string {
	items, ok := value.([]any)
	if !ok {
		return ""
	}
	var builder strings.Builder
	for _, item := range items {
		object, ok := item.(map[string]any)
		if !ok {
			continue
		}
		text, _ := object["text"].(string)
		if text == "" {
			continue
		}
		if builder.Len() > 0 {
			builder.WriteString("\n")
		}
		builder.WriteString(text)
	}
	return builder.String()
}

func extractSegments(raw map[string]any) []map[string]any {
	transcripts, ok := raw["transcripts"].([]any)
	if !ok {
		return []map[string]any{}
	}
	var segments []map[string]any
	for _, transcript := range transcripts {
		transcriptObject, ok := transcript.(map[string]any)
		if !ok {
			continue
		}
		channelID, hasChannelID := numberToInt(transcriptObject["channel_id"])
		sentences, ok := transcriptObject["sentences"].([]any)
		if !ok {
			continue
		}
		for _, sentence := range sentences {
			sentenceObject, ok := sentence.(map[string]any)
			if !ok {
				continue
			}
			text, _ := sentenceObject["text"].(string)
			if text == "" {
				continue
			}
			startMS, hasStart := numberToInt(sentenceObject["begin_time"])
			endMS, hasEnd := numberToInt(sentenceObject["end_time"])
			if !hasStart || !hasEnd || endMS < startMS {
				continue
			}
			segment := map[string]any{
				"start_ms": startMS,
				"end_ms":   endMS,
				"text":     text,
			}
			if hasChannelID {
				segment["channel_id"] = channelID
			}
			if sentenceID, ok := numberToInt(sentenceObject["sentence_id"]); ok {
				segment["sentence_id"] = sentenceID
			}
			if speakerID, ok := numberToInt(sentenceObject["speaker_id"]); ok {
				segment["speaker_id"] = speakerID
			}
			segments = append(segments, segment)
		}
	}
	return segments
}

func buildSRT(segments []map[string]any) string {
	var builder strings.Builder
	index := 1
	for _, segment := range segments {
		startMS, ok := numberToInt(segment["start_ms"])
		if !ok {
			continue
		}
		endMS, ok := numberToInt(segment["end_ms"])
		if !ok {
			continue
		}
		text, _ := segment["text"].(string)
		if text == "" {
			continue
		}
		if builder.Len() > 0 {
			builder.WriteString("\n")
		}
		builder.WriteString(fmt.Sprintf("%d\n%s --> %s\n%s\n", index, formatSRTTime(startMS), formatSRTTime(endMS), normalizeSRTText(text)))
		index++
	}
	return builder.String()
}

func formatSRTTime(ms int64) string {
	if ms < 0 {
		ms = 0
	}
	hours := ms / 3600000
	ms %= 3600000
	minutes := ms / 60000
	ms %= 60000
	seconds := ms / 1000
	milliseconds := ms % 1000
	return fmt.Sprintf("%02d:%02d:%02d,%03d", hours, minutes, seconds, milliseconds)
}

func normalizeSRTText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}
	return strings.Join(lines, "\n")
}

func numberToInt(value any) (int64, bool) {
	switch typed := value.(type) {
	case int:
		return int64(typed), true
	case int64:
		return typed, true
	case float64:
		return int64(typed), true
	case json.Number:
		number, err := typed.Int64()
		return number, err == nil
	default:
		return 0, false
	}
}

func lookupString(raw map[string]any, path ...string) string {
	var current any = raw
	for _, key := range path {
		object, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current = object[key]
	}
	value, _ := current.(string)
	return value
}

func findResultURL(raw map[string]any) string {
	for _, path := range [][]string{
		{"output", "results", "0", "transcription_url"},
		{"output", "result", "transcription_url"},
		{"output", "transcription_url"},
		{"transcription_url"},
		{"url"},
	} {
		if value := lookupLooseString(raw, path...); value != "" {
			return value
		}
	}
	return ""
}

func lookupLooseString(raw any, path ...string) string {
	current := raw
	for _, key := range path {
		switch value := current.(type) {
		case map[string]any:
			current = value[key]
		case []any:
			if key != "0" || len(value) == 0 {
				return ""
			}
			current = value[0]
		default:
			return ""
		}
	}
	result, _ := current.(string)
	return result
}
