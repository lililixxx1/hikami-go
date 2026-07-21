package asr

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"hikami-go/internal/config"
)

// urlCapturingTransport 捕获 HTTP 请求的 URL,返回预设响应。
// 用于验证 DashScopeTranscriber 实际请求的 URL 是否走了 Effective 兜底。
type urlCapturingTransport struct {
	capturedURL string
	captured    chan string
	response    string
}

func (t *urlCapturingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.capturedURL = req.URL.String()
	if t.captured != nil {
		t.captured <- t.capturedURL
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(t.response)),
		Header:     make(http.Header),
	}, nil
}

// TestSubmitUsesEffectiveASRURL 验证 DashScopeTranscriber.submit 在 cfg.ASRURL 为空时
// 实际 POST 到 EffectiveASRURL() 兜底的默认 URL(不是空字符串)。
// 修复 2026-07-20 BUG #1:配置备份导入会把空串持久化到 runtime_settings,覆盖 viper SetDefault,
// 导致 ASR POST 到空 URL 失败("Post \"\": unsupported protocol scheme \"\"")。
func TestSubmitUsesEffectiveASRURL(t *testing.T) {
	// 预设 DashScope submit 成功响应
	successResp := `{"output":{"task_id":"test-task-123"},"request_id":"req-1"}`
	transport := &urlCapturingTransport{response: successResp}

	transcriber := &DashScopeTranscriber{
		cfg: &config.Config{
			DashScope: config.DashScopeConfig{
				ASRURL: "", // 模拟被配置备份导入污染的 runtime_settings
			},
		},
		httpClient: &http.Client{Transport: transport},
	}

	taskID, _, err := transcriber.submit(context.Background(), "https://example.com/audio.mp3", nil)
	if err != nil {
		t.Fatalf("submit() error = %v, want nil (should POST to effective default URL)", err)
	}
	if taskID != "test-task-123" {
		t.Fatalf("submit() taskID = %q, want test-task-123", taskID)
	}

	// 关键断言:capturedURL 不为空,说明没 POST 到 ""
	if transport.capturedURL == "" {
		t.Fatal("captured URL is empty — submit() POSTed to empty string (BUG #1 not fixed)")
	}
	// 应该是 DefaultDashScopeASRURL 的 path 部分(/api/v1/services/audio/asr/transcription)
	if !strings.Contains(transport.capturedURL, "/api/v1/services/audio/asr/transcription") {
		t.Fatalf("captured URL = %q, want path containing /api/v1/services/audio/asr/transcription", transport.capturedURL)
	}
	// 应该指向 dashscope.aliyuncs.com(default 兜底)
	if !strings.Contains(transport.capturedURL, "dashscope.aliyuncs.com") {
		t.Fatalf("captured URL = %q, want host dashscope.aliyuncs.com (default fallback)", transport.capturedURL)
	}
}

// TestSubmitUsesCustomASRURL 验证 cfg.ASRURL 非空时 submit POST 到用户自定义 URL。
func TestSubmitUsesCustomASRURL(t *testing.T) {
	successResp := `{"output":{"task_id":"task-custom"},"request_id":"req-1"}`
	transport := &urlCapturingTransport{response: successResp}

	transcriber := &DashScopeTranscriber{
		cfg: &config.Config{
			DashScope: config.DashScopeConfig{
				ASRURL: "https://custom.example.com/v1/custom-asr",
			},
		},
		httpClient: &http.Client{Transport: transport},
	}

	taskID, _, err := transcriber.submit(context.Background(), "https://example.com/audio.mp3", nil)
	if err != nil {
		t.Fatalf("submit() error = %v", err)
	}
	if taskID != "task-custom" {
		t.Fatalf("taskID = %q, want task-custom", taskID)
	}
	if !strings.Contains(transport.capturedURL, "custom.example.com/v1/custom-asr") {
		t.Fatalf("captured URL = %q, want custom.example.com/v1/custom-asr", transport.capturedURL)
	}
}

// TestCheckTaskUsesEffectiveTasksURL 验证 cfg.TasksURL 为空时 checkTask 拼出的 endpoint
// 使用 EffectiveTasksURL() 兜底默认值。
// checkTask 是单次请求(不像 poll 会循环),更适合测试。
func TestCheckTaskUsesEffectiveTasksURL(t *testing.T) {
	// DashScope tasks 查询成功响应
	successResp := `{"output":{"task_id":"task-x","task_status":"SUCCEEDED","result":{"transcription_url":"https://example.com/r.json"}},"request_id":"req-1"}`
	transport := &urlCapturingTransport{response: successResp}

	transcriber := &DashScopeTranscriber{
		cfg: &config.Config{
			DashScope: config.DashScopeConfig{
				TasksURL: "", // 模拟污染
			},
		},
		httpClient: &http.Client{Transport: transport},
	}

	raw, resultURL, err := transcriber.checkTask(context.Background(), "task-x")
	if err != nil {
		t.Fatalf("checkTask() error = %v", err)
	}
	if raw == nil {
		t.Fatal("checkTask() raw = nil")
	}
	if resultURL != "https://example.com/r.json" {
		t.Fatalf("checkTask() resultURL = %q, want https://example.com/r.json", resultURL)
	}
	// 关键断言:captured URL 应包含默认 tasks path + task-x
	if !strings.Contains(transport.capturedURL, "/api/v1/tasks/task-x") {
		t.Fatalf("captured URL = %q, want containing /api/v1/tasks/task-x (default tasks URL + taskID)", transport.capturedURL)
	}
	if !strings.Contains(transport.capturedURL, "dashscope.aliyuncs.com") {
		t.Fatalf("captured URL = %q, want host dashscope.aliyuncs.com (default fallback)", transport.capturedURL)
	}
}

// TestCheckTaskUsesCustomTasksURL 验证 cfg.TasksURL 非空(带末尾 /)时,
// checkTask 拼出的 endpoint 正确 trim 末尾 /(防止双斜杠)。
// 对应 r19c LOW #2:EffectiveTasksURL 外层保留 TrimRight。
func TestCheckTaskUsesCustomTasksURL(t *testing.T) {
	successResp := `{"output":{"task_id":"t","task_status":"SUCCEEDED","result":{"transcription_url":"https://x.com/r.json"}},"request_id":"r"}`
	transport := &urlCapturingTransport{response: successResp}

	transcriber := &DashScopeTranscriber{
		cfg: &config.Config{
			DashScope: config.DashScopeConfig{
				TasksURL: "https://custom.example.com/tasks/", // 末尾带 /
			},
		},
		httpClient: &http.Client{Transport: transport},
	}

	_, _, err := transcriber.checkTask(context.Background(), "task-1")
	if err != nil {
		t.Fatalf("checkTask() error = %v", err)
	}
	// 关键断言:不应有双斜杠
	if strings.Contains(transport.capturedURL, "//tasks//") {
		t.Fatalf("captured URL = %q contains double slash (TrimRight not working)", transport.capturedURL)
	}
	if !strings.HasSuffix(transport.capturedURL, "/tasks/task-1") {
		t.Fatalf("captured URL = %q, want suffix /tasks/task-1 (trailing / trimmed)", transport.capturedURL)
	}
}
