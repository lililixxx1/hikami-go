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
	cfg                    *config.Config
	httpClient             *http.Client
	rclone                 string
	tempServer             *TempAudioServer
	s3Publisher            *S3Publisher
	dashScopeTempPublisher *DashScopeTempPublisher
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
		(cfg.ASRTemp.RcloneConfigured() && cfg.ASRTemp.PublicBaseURL != "") ||
		cfg.DashScope.TemporaryStorageEnabled
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
	} else if cfg.ASRTemp.RcloneConfigured() && cfg.ASRTemp.PublicBaseURL != "" {
		transcriber.rclone = cfg.Rclone
	} else {
		transcriber.dashScopeTempPublisher = newDashScopeTempPublisher(
			&http.Client{Timeout: 30 * time.Minute},
			defaultDashScopeUploadsURL,
			cfg.DashScope.EffectiveAPIKeyEnv(),
			cfg.DashScope.Model,
		)
	}
	return transcriber
}

// ErrDashScopeTaskDead 表示远端 DashScope 任务已进入终态失败(FAILED/CANCELED),
// 继续轮询同一 taskID 不可能出结果。调用方(asr.Handler)仅在「恢复重入」场景
// 据此重新提交;新提交路径的 await 返回它时直接失败(防同 attempt 内无限重提交)。
var ErrDashScopeTaskDead = errors.New("dashscope remote task reached terminal failure state")

// SubmitASRTask 发布音频并提交 DashScope 转写任务,返回远端任务 ID。
// 与 AwaitASRTask 拆分的目的:submit 成功后调用方立即把 taskID 持久化进
// worker 任务 payload,崩溃恢复重入时走 await 轮询既有远端任务而非重新提交
// (ISSUE-006,见 plans/plan-issue006-dashscope-taskid-persist-2026-08-16.md)。
func (t *DashScopeTranscriber) SubmitASRTask(ctx context.Context, audioPath string, sessionInfo session.Session, vocabulary map[string]int) (string, error) {
	model := NormalizeDashScopeASRModel(t.cfg.DashScope.Model)
	logCtx := context.WithValue(ctx, dashScopeChannelIDKey, sessionInfo.ChannelID)
	logCtx = context.WithValue(logCtx, dashScopeSessionIDKey, sessionInfo.ID)
	slog.Info("dashscope asr transcribe started",
		"channel_id", sessionInfo.ChannelID,
		"session_id", sessionInfo.ID,
		"audio_path", filepath.ToSlash(audioPath),
		"model", model,
		"request_mode", DashScopeRequestMode(model))

	publicURL, _, err := t.publishAudio(ctx, audioPath, sessionInfo)
	if err != nil {
		return "", err
	}
	taskID, _, err := t.submit(logCtx, publicURL, vocabulary)
	if err != nil {
		return "", err
	}
	slog.Info("dashscope asr task submitted",
		"channel_id", sessionInfo.ChannelID,
		"session_id", sessionInfo.ID,
		"task_id", taskID)
	return taskID, nil
}

// AwaitASRTask 轮询既有远端任务直至完成并取回结果(零提交,零计费)。
//   - 远端 SUCCEEDED → 取结果返回;
//   - 远端 FAILED/CANCELED → 返回 ErrDashScopeTaskDead(旧任务已终态,重提交合法);
//   - checkTask/poll 网络错误或轮询超时 → 返回 error(fail-closed,绝不静默重提交:
//     远端任务可能仍在运行,人工 retry 携带同一 taskID 重新进入本方法继续等待)。
//
// 成功且 CleanupAfterSuccess 时经 remotePathFor(与 publishAudio 同一路径构造)清理远端音频;
// 失败路径有意保留远端文件(远端任务可能仍需该 URL),由后续 retry 成功后最终回收。
func (t *DashScopeTranscriber) AwaitASRTask(ctx context.Context, taskID string, sessionInfo session.Session) (Result, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return Result{}, fmt.Errorf("dashscope await: empty task id")
	}
	startedAt := time.Now()
	logCtx := context.WithValue(ctx, dashScopeChannelIDKey, sessionInfo.ChannelID)
	logCtx = context.WithValue(logCtx, dashScopeSessionIDKey, sessionInfo.ID)
	slog.Info("dashscope asr task await started",
		"channel_id", sessionInfo.ChannelID,
		"session_id", sessionInfo.ID,
		"task_id", taskID)
	taskRaw, resultURL, err := t.checkTask(logCtx, taskID)
	if err != nil {
		return Result{}, fmt.Errorf("dashscope task %s check failed (remote task may still be running; retry resumes await): %w", taskID, err)
	}
	status := dashScopeTaskStatus(taskRaw)
	if status == "FAILED" || status == "CANCELED" || status == "CANCELLED" {
		return Result{}, fmt.Errorf("%w: task %s status %s", ErrDashScopeTaskDead, taskID, status)
	}
	if status != "SUCCEEDED" {
		taskRaw, resultURL, err = t.poll(logCtx, taskID)
		if err != nil {
			if errors.Is(err, ErrDashScopeTaskDead) {
				// poll 观察到终态失败(哨兵已含 taskID/状态),直接透传供 errors.Is 判定。
				return Result{}, err
			}
			return Result{}, fmt.Errorf("dashscope task %s poll failed (remote task may still be running; retry resumes await): %w", taskID, err)
		}
	}
	resultRaw := map[string]any{}
	if resultURL != "" {
		resultRaw, err = t.fetchResult(logCtx, resultURL)
		if err != nil {
			return Result{}, err
		}
	}
	if t.cfg.ASRTemp.CleanupAfterSuccess {
		t.cleanupRemote(ctx, t.remotePathFor(sessionInfo))
	}
	result := buildResultFromDashScope(sessionInfo, map[string]any{"task_id": taskID}, taskRaw, resultRaw)
	slog.Info("dashscope asr transcribe completed",
		"channel_id", sessionInfo.ChannelID,
		"session_id", sessionInfo.ID,
		"task_id", taskID,
		"segments", len(result.Segments),
		"transcript_len", len(result.Transcript),
		"duration", time.Since(startedAt).String())
	return result, nil
}

func (t *DashScopeTranscriber) Transcribe(ctx context.Context, audioPath string, sessionInfo session.Session) (Result, error) {
	return t.TranscribeWithVocabulary(ctx, audioPath, sessionInfo, nil)
}

// TranscribeWithVocabulary 一次性「提交+等待」组合。生产路径由 asr.Handler 编排
// SubmitASRTask→持久化 taskID→AwaitASRTask(带崩溃恢复锚点);本组合方法无持久化
// 能力,仅服务未接入编排的调用面(测试/外部复用)。行为差异:失败路径不再清理
// 远端音频(fail-closed 时远端任务可能仍需该 URL),成功路径经 remotePathFor 清理。
func (t *DashScopeTranscriber) TranscribeWithVocabulary(ctx context.Context, audioPath string, sessionInfo session.Session, vocabulary map[string]int) (Result, error) {
	taskID, err := t.SubmitASRTask(ctx, audioPath, sessionInfo, vocabulary)
	if err != nil {
		return Result{}, err
	}
	return t.AwaitASRTask(ctx, taskID, sessionInfo)
}

// remotePathFor 重建远端音频对象路径,与 publishAudio 的实际发布路径保持一致
// (单一真相源:tempServer/s3/rclone 三后端的路径构造两侧共用同一 helper)。
// DashScope 临时存储返回空串——该后端对象不支持主动删除(48h 自动过期),
// cleanupRemote 对其本就是 no-op。AwaitASRTask 成功后的 CleanupAfterSuccess 用它定位远端文件。
func (t *DashScopeTranscriber) remotePathFor(sessionInfo session.Session) string {
	switch {
	case t.tempServer != nil:
		return t.tempServer.ObjectPath(sessionInfo)
	case t.s3Publisher != nil:
		return s3ObjectKey(sessionInfo)
	case t.dashScopeTempPublisher != nil:
		return ""
	default:
		return t.rcloneRemotePath(sessionInfo)
	}
}

// rcloneObjectPath 是 rclone 后端的对象路径(不含 remote 前缀),publishAudio 与 remotePathFor 共用。
func rcloneObjectPath(cfg *config.Config, sessionInfo session.Session) string {
	return filepath.ToSlash(filepath.Join(cfg.ASRTemp.BasePath, sessionInfo.ChannelID, sessionInfo.ID, "audio.asr.mp3"))
}

// rcloneRemotePath 是 rclone 后端的完整远端路径(remote 前缀 + 对象路径)。
func (t *DashScopeTranscriber) rcloneRemotePath(sessionInfo session.Session) string {
	return t.cfg.ASRTemp.RcloneRemote + rcloneObjectPath(t.cfg, sessionInfo)
}

func (t *DashScopeTranscriber) publishAudio(ctx context.Context, audioPath string, sessionInfo session.Session) (string, string, error) {
	if t.tempServer != nil {
		return t.tempServer.Publish(ctx, audioPath, sessionInfo)
	}
	if t.s3Publisher != nil {
		return t.s3Publisher.Publish(ctx, audioPath, sessionInfo)
	}
	if t.dashScopeTempPublisher != nil {
		return t.dashScopeTempPublisher.Publish(ctx, audioPath, sessionInfo)
	}
	remoteObject := rcloneObjectPath(t.cfg, sessionInfo)
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
	if t.dashScopeTempPublisher != nil {
		// DashScope 临时对象不支持查询或主动删除，会在 48 小时后自动过期。
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
	headers := make(http.Header)
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(publicURL)), "oss://") {
		headers.Set("X-DashScope-OssResourceResolve", "enable")
	}
	raw, err := t.doJSONWithRetryHeaders(ctx, http.MethodPost, t.cfg.DashScope.EffectiveASRURL(), body, headers)
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

// dashScopePollInterval 是任务轮询间隔。包级 var 便于测试缩短(生产 5s)。
var dashScopePollInterval = 5 * time.Second

func (t *DashScopeTranscriber) poll(ctx context.Context, taskID string) (map[string]any, string, error) {
	endpoint := strings.TrimRight(t.cfg.DashScope.EffectiveTasksURL(), "/") + "/" + taskID
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
			case <-time.After(dashScopePollInterval):
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
			// 终态失败映射哨兵(与 checkTask 观察到的终态一致):重入路径据此直接重提交,
			// 而非 fail-closed 多等一次人工 retry。AwaitASRTask 的 %w 包装保留 errors.Is 链。
			return raw, "", fmt.Errorf("%w: dashscope task %s ended with status %s", ErrDashScopeTaskDead, taskID, status)
		}
		select {
		case <-ctx.Done():
			return nil, "", ctx.Err()
		case <-time.After(dashScopePollInterval):
		}
	}
	return last, "", fmt.Errorf("dashscope task %s polling timeout", taskID)
}

func (t *DashScopeTranscriber) checkTask(ctx context.Context, taskID string) (map[string]any, string, error) {
	endpoint := strings.TrimRight(t.cfg.DashScope.EffectiveTasksURL(), "/") + "/" + taskID
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
	return t.doJSONWithRetryHeaders(ctx, method, endpoint, body, nil)
}

func (t *DashScopeTranscriber) doJSONWithRetryHeaders(ctx context.Context, method string, endpoint string, body any, headers http.Header) (map[string]any, error) {
	delays := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}
	var lastErr error
	for attempt := 0; attempt <= len(delays); attempt++ {
		raw, err := t.doJSON(ctx, method, endpoint, body, headers)
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

func (t *DashScopeTranscriber) doJSON(ctx context.Context, method string, endpoint string, body any, headers http.Header) (map[string]any, error) {
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
	for name, values := range headers {
		for _, value := range values {
			request.Header.Add(name, value)
		}
	}
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
