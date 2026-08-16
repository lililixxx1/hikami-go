package asr

// ISSUE-006(2026-08-16)DashScopeTranscriber 两阶段接口的 HTTP 级行为测试。
// 见 plans/plan-issue006-dashscope-taskid-persist-2026-08-16.md §4 D1-D6:
//
//   D1 远端 SUCCEEDED → 取结果返回
//   D2 checkTask HTTP 错误 → fail-closed 返回 error,零提交请求(付费安全)
//   D3 远端 FAILED → ErrDashScopeTaskDead 哨兵(errors.Is 可判)
//   D4 RUNNING → poll → SUCCEEDED 轮询走通
//   D5 remotePathFor 四后端路径重建,与 publish 侧构造一致(单一真相源)
//   D6 await 成功 + CleanupAfterSuccess → 经 remotePathFor 真实清理远端文件

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"hikami-go/internal/config"
	"hikami-go/internal/session"
)

// scriptedResponse 是 seqTransport 的一次预设响应;err 非空时返回网络错误。
type scriptedResponse struct {
	status int
	body   string
	err    error
}

// seqTransport 按序消费预设响应(记满后返回网络错误使测试显式失败),并记录全部请求。
type seqTransport struct {
	mu     sync.Mutex
	calls  []string
	script []scriptedResponse
}

func (t *seqTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.mu.Lock()
	t.calls = append(t.calls, req.Method+" "+req.URL.Path)
	if len(t.script) == 0 {
		t.mu.Unlock()
		return nil, fmt.Errorf("unexpected request %s %s (script exhausted)", req.Method, req.URL.Path)
	}
	next := t.script[0]
	t.script = t.script[1:]
	t.mu.Unlock()
	if next.err != nil {
		return nil, next.err
	}
	return &http.Response{
		StatusCode: next.status,
		Body:       io.NopCloser(strings.NewReader(next.body)),
		Header:     make(http.Header),
	}, nil
}

func (t *seqTransport) postCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	count := 0
	for _, call := range t.calls {
		if strings.HasPrefix(call, http.MethodPost) {
			count++
		}
	}
	return count
}

func awaitTestSession() session.Session {
	return session.Session{ID: "session_test", ChannelID: "ch1", Title: "Test Session"}
}

func taskJSON(status string, resultURL string) string {
	if resultURL == "" {
		return fmt.Sprintf(`{"output":{"task_id":"ds-1","task_status":%q},"request_id":"r1"}`, status)
	}
	return fmt.Sprintf(`{"output":{"task_id":"ds-1","task_status":%q,"result":{"transcription_url":%q}},"request_id":"r1"}`, status, resultURL)
}

// D1:checkTask 返回 SUCCEEDED + 结果 URL → fetch 后组出 Result,合成 submit raw 带 task_id。
func TestAwaitASRTask_SucceededFetchesResult(t *testing.T) {
	transport := &seqTransport{script: []scriptedResponse{
		{status: 200, body: taskJSON("SUCCEEDED", "https://result.example.com/r.json")},
		{status: 200, body: `{"transcripts":[{"sentences":[{"text":"你好","begin_time":0,"end_time":900,"channel_id":0}]}]}`},
	}}
	transcriber := &DashScopeTranscriber{
		cfg:        &config.Config{DashScope: config.DashScopeConfig{TasksURL: "https://dashscope.example.com/tasks/"}},
		httpClient: &http.Client{Transport: transport},
	}

	result, err := transcriber.AwaitASRTask(context.Background(), "ds-1", awaitTestSession())
	if err != nil {
		t.Fatalf("AwaitASRTask: %v", err)
	}
	if !strings.Contains(result.Transcript, "你好") {
		t.Fatalf("Transcript = %q, want parsed sentence text", result.Transcript)
	}
	if len(result.Segments) != 1 {
		t.Fatalf("Segments = %v, want 1", result.Segments)
	}
	submit, _ := result.Raw["submit"].(map[string]any)
	if submit == nil || submit["task_id"] != "ds-1" {
		t.Fatalf("Raw.submit = %v, want synthesized {task_id: ds-1}", result.Raw["submit"])
	}
	if transport.postCount() != 0 {
		t.Fatalf("POST count = %d, want 0 (await must never submit)", transport.postCount())
	}
}

// D2(付费安全):checkTask HTTP 错误 → fail-closed 返回 error,零 POST(400 不触发
// shouldRetryDashScopeError,测试快速失败;生产行为对网络错误同样 fail-closed)。
func TestAwaitASRTask_CheckErrorFailsClosed(t *testing.T) {
	transport := &seqTransport{script: []scriptedResponse{
		{status: 400, body: `{"code":"InvalidParameter"}`},
	}}
	transcriber := &DashScopeTranscriber{
		cfg:        &config.Config{DashScope: config.DashScopeConfig{TasksURL: "https://dashscope.example.com/tasks/"}},
		httpClient: &http.Client{Transport: transport},
	}

	result, err := transcriber.AwaitASRTask(context.Background(), "ds-1", awaitTestSession())
	if err == nil {
		t.Fatal("AwaitASRTask must fail on check error (fail-closed)")
	}
	if result.Transcript != "" {
		t.Fatalf("result must be empty on error, got %q", result.Transcript)
	}
	if errors.Is(err, ErrDashScopeTaskDead) {
		t.Fatalf("HTTP error must not be mistaken for dead remote: %v", err)
	}
	if !strings.Contains(err.Error(), "retry resumes await") {
		t.Fatalf("error message should guide operators to retry: %v", err)
	}
	if transport.postCount() != 0 {
		t.Fatalf("POST count = %d, want 0 (fail-closed must never silently resubmit)", transport.postCount())
	}
}

// D3:远端 FAILED → ErrDashScopeTaskDead(errors.Is 可判,供 handler 重入路径重提交)。
func TestAwaitASRTask_FailedStatusReturnsDeadSentinel(t *testing.T) {
	transport := &seqTransport{script: []scriptedResponse{
		{status: 200, body: taskJSON("FAILED", "")},
	}}
	transcriber := &DashScopeTranscriber{
		cfg:        &config.Config{DashScope: config.DashScopeConfig{TasksURL: "https://dashscope.example.com/tasks/"}},
		httpClient: &http.Client{Transport: transport},
	}

	_, err := transcriber.AwaitASRTask(context.Background(), "ds-1", awaitTestSession())
	if !errors.Is(err, ErrDashScopeTaskDead) {
		t.Fatalf("error = %v, want ErrDashScopeTaskDead", err)
	}
	if transport.postCount() != 0 {
		t.Fatalf("POST count = %d, want 0", transport.postCount())
	}
}

// D4:checkTask RUNNING → poll 首轮 SUCCEEDED → 取结果返回。
func TestAwaitASRTask_RunningThenSucceeded(t *testing.T) {
	// 缩短轮询间隔:若响应顺序异常导致真实 sleep,测试也不会卡 5s。
	restore := dashScopePollInterval
	dashScopePollInterval = 10 * time.Millisecond
	t.Cleanup(func() { dashScopePollInterval = restore })

	transport := &seqTransport{script: []scriptedResponse{
		{status: 200, body: taskJSON("RUNNING", "")},
		{status: 200, body: taskJSON("SUCCEEDED", "https://result.example.com/r.json")},
		{status: 200, body: `{"transcripts":[{"sentences":[{"text":"second poll","begin_time":0,"end_time":500,"channel_id":0}]}]}`},
	}}
	transcriber := &DashScopeTranscriber{
		cfg:        &config.Config{DashScope: config.DashScopeConfig{TasksURL: "https://dashscope.example.com/tasks/"}},
		httpClient: &http.Client{Transport: transport},
	}

	result, err := transcriber.AwaitASRTask(context.Background(), "ds-1", awaitTestSession())
	if err != nil {
		t.Fatalf("AwaitASRTask: %v", err)
	}
	if !strings.Contains(result.Transcript, "second poll") {
		t.Fatalf("Transcript = %q, want result fetched after poll", result.Transcript)
	}
	if len(transport.calls) != 3 {
		t.Fatalf("calls = %v, want 3 (check + poll + result fetch)", transport.calls)
	}
}

// D4b:checkTask RUNNING → poll 观察到 FAILED → 透传 ErrDashScopeTaskDead 哨兵
// (M-1:poll 终态与 checkTask 终态同映射,重入路径免一次人工 retry 直达重提交)。
func TestAwaitASRTask_RunningThenFailedReturnsDeadSentinel(t *testing.T) {
	restore := dashScopePollInterval
	dashScopePollInterval = 10 * time.Millisecond
	t.Cleanup(func() { dashScopePollInterval = restore })

	transport := &seqTransport{script: []scriptedResponse{
		{status: 200, body: taskJSON("RUNNING", "")},
		{status: 200, body: taskJSON("FAILED", "")},
	}}
	transcriber := &DashScopeTranscriber{
		cfg:        &config.Config{DashScope: config.DashScopeConfig{TasksURL: "https://dashscope.example.com/tasks/"}},
		httpClient: &http.Client{Transport: transport},
	}

	_, err := transcriber.AwaitASRTask(context.Background(), "ds-1", awaitTestSession())
	if !errors.Is(err, ErrDashScopeTaskDead) {
		t.Fatalf("error = %v, want ErrDashScopeTaskDead (poll-observed terminal failure)", err)
	}
	if strings.Contains(fmt.Sprint(err), "may still be running") {
		t.Fatalf("dead remote must not be described as maybe-running: %v", err)
	}
	if transport.postCount() != 0 {
		t.Fatalf("POST count = %d, want 0", transport.postCount())
	}
}

// D7:空 taskID → 直接报错,不发任何请求。
func TestAwaitASRTask_EmptyTaskID(t *testing.T) {
	transport := &seqTransport{}
	transcriber := &DashScopeTranscriber{
		cfg:        &config.Config{DashScope: config.DashScopeConfig{TasksURL: "https://dashscope.example.com/tasks/"}},
		httpClient: &http.Client{Transport: transport},
	}

	if _, err := transcriber.AwaitASRTask(context.Background(), "  ", awaitTestSession()); err == nil {
		t.Fatal("empty task id must error")
	}
	if len(transport.calls) != 0 {
		t.Fatalf("calls = %v, want none", transport.calls)
	}
}

// D5:remotePathFor 四后端路径重建正确,且与 publish 侧构造一致(单一真相源)。
func TestRemotePathFor(t *testing.T) {
	si := awaitTestSession()
	cfg := &config.Config{ASRTemp: config.ASRTempConfig{BasePath: "base/dir", RcloneRemote: "remote:"}}

	t.Run("temp_server", func(t *testing.T) {
		server := NewTempAudioServer(cfg)
		tr := &DashScopeTranscriber{cfg: cfg, tempServer: server}
		got := tr.remotePathFor(si)
		if want := server.ObjectPath(si); got != want {
			t.Fatalf("remotePathFor = %q, want ObjectPath %q", got, want)
		}
		if want := "ch1/session_test/audio.asr.mp3"; got != want {
			t.Fatalf("remotePathFor = %q, want %q", got, want)
		}
	})
	t.Run("s3", func(t *testing.T) {
		tr := &DashScopeTranscriber{cfg: cfg, s3Publisher: &S3Publisher{}}
		if got, want := tr.remotePathFor(si), s3ObjectKey(si); got != want {
			t.Fatalf("remotePathFor = %q, want s3ObjectKey %q", got, want)
		}
	})
	t.Run("dashscope_temp_storage", func(t *testing.T) {
		tr := &DashScopeTranscriber{cfg: cfg, dashScopeTempPublisher: newDashScopeTempPublisher(nil, "", "", "")}
		if got := tr.remotePathFor(si); got != "" {
			t.Fatalf("remotePathFor = %q, want empty (no-op cleanup backend)", got)
		}
	})
	t.Run("rclone", func(t *testing.T) {
		tr := &DashScopeTranscriber{cfg: cfg}
		if got, want := tr.remotePathFor(si), "remote:base/dir/ch1/session_test/audio.asr.mp3"; got != want {
			t.Fatalf("remotePathFor = %q, want %q", got, want)
		}
		if got, want := tr.rcloneRemotePath(si), cfg.ASRTemp.RcloneRemote+rcloneObjectPath(cfg, si); got != want {
			t.Fatalf("rcloneRemotePath = %q, want %q", got, want)
		}
	})
}

// D6:await 成功 + CleanupAfterSuccess → 经 remotePathFor 定位并真实删除 temp server 文件。
func TestAwaitASRTask_CleanupAfterSuccess(t *testing.T) {
	localDir := t.TempDir()
	cfg := &config.Config{
		DashScope: config.DashScopeConfig{TasksURL: "https://dashscope.example.com/tasks/"},
		ASRTemp:   config.ASRTempConfig{LocalDir: localDir, CleanupAfterSuccess: true},
	}
	server := NewTempAudioServer(cfg)
	objectPath := server.ObjectPath(awaitTestSession())
	published := filepath.Join(localDir, filepath.FromSlash(objectPath))
	if err := os.MkdirAll(filepath.Dir(published), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(published, []byte("FAKEMP3"), 0o644); err != nil {
		t.Fatal(err)
	}

	transport := &seqTransport{script: []scriptedResponse{
		{status: 200, body: taskJSON("SUCCEEDED", "")},
	}}
	transcriber := &DashScopeTranscriber{
		cfg:        cfg,
		httpClient: &http.Client{Transport: transport},
		tempServer: server,
	}

	if _, err := transcriber.AwaitASRTask(context.Background(), "ds-1", awaitTestSession()); err != nil {
		t.Fatalf("AwaitASRTask: %v", err)
	}
	if _, err := os.Stat(published); !os.IsNotExist(err) {
		t.Fatalf("published temp file should be removed after success, stat err = %v", err)
	}
}
