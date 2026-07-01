package handler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"hikami-go/internal/archive"
	"hikami-go/internal/asr"
	"hikami-go/internal/biliutil"
	"hikami-go/internal/channel"
	"hikami-go/internal/config"
	"hikami-go/internal/db"
	"hikami-go/internal/discover"
	"hikami-go/internal/download"
	"hikami-go/internal/glossary"
	"hikami-go/internal/importer"
	"hikami-go/internal/live_record"
	"hikami-go/internal/publisher"
	"hikami-go/internal/recap"
	"hikami-go/internal/runtime"
	"hikami-go/internal/runtimeconfig"
	"hikami-go/internal/secrets"
	"hikami-go/internal/session"
	"hikami-go/internal/state"
	"hikami-go/internal/upload"
	"hikami-go/internal/worker"
)

func TestChannelRoutes(t *testing.T) {
	server := newTestServer(t)

	createBody := `{
			"id":"huize",
			"name":"灰泽满Hikami",
			"uid":1298779265,
			"live_room_id":0,
			"enabled":true,
			"recap_model":"v4-pro",
			"max_continuations":2
		}`
	create := performRequest(server, http.MethodPost, "/api/channels", createBody)
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", create.Code, create.Body.String())
	}

	list := performRequest(server, http.MethodGet, "/api/channels", "")
	if list.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", list.Code, list.Body.String())
	}
	if !strings.Contains(list.Body.String(), `"id":"huize"`) {
		t.Fatalf("list body missing channel: %s", list.Body.String())
	}
	if !strings.Contains(list.Body.String(), `"recap_model":"v4-pro"`) || !strings.Contains(list.Body.String(), `"max_continuations":2`) {
		t.Fatalf("list body missing recap config: %s", list.Body.String())
	}

	updateBody := `{
		"name":"Hikami",
		"uid":1298779265,
		"live_room_id":123,
		"enabled":false
	}`
	update := performRequest(server, http.MethodPut, "/api/channels/huize", updateBody)
	if update.Code != http.StatusOK {
		t.Fatalf("update status = %d, body = %s", update.Code, update.Body.String())
	}
	if !strings.Contains(update.Body.String(), `"live_room_id":123`) {
		t.Fatalf("update body missing live room id: %s", update.Body.String())
	}

	deleteResponse := performRequest(server, http.MethodDelete, "/api/channels/huize", "")
	if deleteResponse.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, body = %s", deleteResponse.Code, deleteResponse.Body.String())
	}
}

func TestGetRecapModels(t *testing.T) {
	server := newTestServer(t)

	resp := performRequest(server, http.MethodGet, "/api/config/recap/models", "")
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.Code, resp.Body.String())
	}

	var result struct {
		Models []RecapModelOption `json:"models"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal models: %v, body=%s", err, resp.Body.String())
	}
	if len(result.Models) == 0 {
		t.Fatal("expected non-empty models list")
	}

	byValue := make(map[string]RecapModelOption)
	for _, m := range result.Models {
		byValue[m.Value] = m
	}
	if m, ok := byValue["deepseek-v4-pro"]; !ok {
		t.Fatal("expected deepseek-v4-pro in models")
	} else if m.Group != "DeepSeek" {
		t.Fatalf("expected group DeepSeek for deepseek-v4-pro, got %q", m.Group)
	}
	if _, ok := byValue["qwen-max"]; !ok {
		t.Fatal("expected qwen-max in models")
	}
}

func TestCreateChannelRejectsInvalidBody(t *testing.T) {
	server := newTestServer(t)
	response := performRequest(server, http.MethodPost, "/api/channels", `{"name":"Bad"}`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.Code)
	}
}

func TestIdentifyChannelRouteRejectsInvalidInput(t *testing.T) {
	server := newTestServer(t)
	response := performRequest(server, http.MethodPost, "/api/channels/identify", `{"input":""}`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestSaveIdentifiedChannelRouteCreatesChannel(t *testing.T) {
	server := newTestServer(t)
	biliServer := newBilibiliIdentifyServer(t)
	defer biliServer.Close()
	server.identifier = channel.NewIdentifierWithBaseURL(biliServer.URL)

	create := performRequest(server, http.MethodPost, "/api/channels/identify/save", `{"input":"https://live.bilibili.com/123"}`)
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", create.Code, create.Body.String())
	}
	if !strings.Contains(create.Body.String(), `"created":true`) || !strings.Contains(create.Body.String(), `"id":"bili_456"`) {
		t.Fatalf("create body missing saved channel: %s", create.Body.String())
	}

	retry := performRequest(server, http.MethodPost, "/api/channels/identify/save", `{"input":"https://live.bilibili.com/123"}`)
	if retry.Code != http.StatusOK {
		t.Fatalf("retry status = %d, body = %s", retry.Code, retry.Body.String())
	}
	if !strings.Contains(retry.Body.String(), `"created":false`) {
		t.Fatalf("retry body should be idempotent: %s", retry.Body.String())
	}
}

func TestSaveBiliQRCodeLoginRouteUpdatesChannelCookie(t *testing.T) {
	server := newTestServer(t)
	if _, err := server.channels.Create(context.Background(), channel.UpsertInput{
		ID:      "huize",
		Name:    "Hikami",
		UID:     42,
		Enabled: true,
	}); err != nil {
		t.Fatalf("create channel: %v", err)
	}
	now := time.Date(2026, 5, 12, 10, 0, 0, 0, time.UTC)
	client := biliutil.NewQRLoginClient(&http.Client{Transport: qrLoginRoundTripper{t: t, now: now}})
	client.BaseURL = "https://passport.test"
	client.Now = func() time.Time { return now }
	server.biliLogin = biliutil.NewQRLoginSessionStore(client, 180*time.Second)

	create := performRequest(server, http.MethodPost, "/api/bili/login/qrcode", "")
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", create.Code, create.Body.String())
	}
	var created struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	poll := performRequest(server, http.MethodGet, "/api/bili/login/qrcode/"+created.SessionID, "")
	if poll.Code != http.StatusOK {
		t.Fatalf("poll status = %d, body = %s", poll.Code, poll.Body.String())
	}

	response := performRequest(server, http.MethodPost, "/api/bili/login/qrcode/"+created.SessionID+"/save", `{"channel_id":"huize","usage":"download"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if !strings.Contains(body, `"usage":"download"`) || !strings.Contains(body, `"download_cookie_file":`) {
		t.Fatalf("body missing cookie save result: %s", body)
	}
}

func TestLiveChannelStatusRouteChecksSingleChannel(t *testing.T) {
	server := newTestServer(t)
	if _, err := server.channels.Create(context.Background(), channel.UpsertInput{
		ID:         "huize",
		Name:       "Hikami",
		UID:        1,
		LiveRoomID: 123,
		Enabled:    true,
	}); err != nil {
		t.Fatalf("create channel: %v", err)
	}

	response := performRequest(server, http.MethodGet, "/api/live/huize/status", "")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if !strings.Contains(body, `"channel_id":"huize"`) || !strings.Contains(body, `"live":true`) {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestStartLiveRecordRouteCreatesSessionAndTask(t *testing.T) {
	server := newTestServer(t)
	if _, err := server.channels.Create(context.Background(), channel.UpsertInput{
		ID:         "huize",
		Name:       "Hikami",
		UID:        1,
		LiveRoomID: 123,
		Enabled:    true,
	}); err != nil {
		t.Fatalf("create channel: %v", err)
	}

	response := performRequest(server, http.MethodPost, "/api/live/huize/record/start", "")
	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, expected := range []string{`"channel_id":"huize"`, `"recording":true`, `"session_id":`, `"task_id":`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("response missing %s: %s", expected, body)
		}
	}
}

func TestStartLiveRecordRouteRejectsDuplicateActiveSession(t *testing.T) {
	server := newTestServer(t)
	if _, err := server.channels.Create(context.Background(), channel.UpsertInput{
		ID:         "huize",
		Name:       "Hikami",
		UID:        1,
		LiveRoomID: 123,
		Enabled:    true,
	}); err != nil {
		t.Fatalf("create channel: %v", err)
	}
	if _, err := server.sessions.CreateLive(context.Background(), session.CreateLiveInput{
		ChannelID: "huize",
		Title:     "Live",
		RoomID:    123,
		StartedAt: time.Date(2026, 4, 28, 12, 0, 0, 0, time.Local),
	}); err != nil {
		t.Fatalf("create active live session: %v", err)
	}

	response := performRequest(server, http.MethodPost, "/api/live/huize/record/start", "")
	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), live_record.ErrAlreadyRecording.Error()) {
		t.Fatalf("body missing duplicate error: %s", response.Body.String())
	}
}

func TestRuntimeHealthIncludesASRDetails(t *testing.T) {
	server := newTestServer(t)
	server.runtimeStatus = &runtime.Status{
		Capabilities: runtime.Capabilities{
			ASRSubmit:      true,
			ASRModel:       "qwen3-asr-flash-filetrans",
			ASRRequestMode: "file_url",
		},
	}

	response := performRequest(server, http.MethodGet, "/api/health/runtime", "")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, expected := range []string{`"asr_submit":true`, `"asr_model":"qwen3-asr-flash-filetrans"`, `"asr_request_mode":"file_url"`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("response missing %s: %s", expected, body)
		}
	}
}

func TestImportConfigRefreshesRuntimeStatus(t *testing.T) {
	server := newTestServer(t)
	server.cfg.DashScope.APIKeyEnv = "HIKAMI_TEST_DASHSCOPE_KEY"
	t.Setenv("HIKAMI_TEST_DASHSCOPE_KEY", "")

	initialCheckedAt := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	server.setRuntimeStatus(&runtime.Status{
		CheckedAt: initialCheckedAt,
		ConfigStatus: runtime.ConfigStatus{
			DashScopeKeyEnv: "HIKAMI_TEST_DASHSCOPE_KEY",
		},
	})

	body := `{
		"version":"1",
		"recap_ai":{},
		"publish":{},
		"webdav":{},
		"secrets":{"HIKAMI_TEST_DASHSCOPE_KEY":"test-key"},
		"channels":[],
		"glossary":{},
		"templates":{},
		"bili_accounts":[]
	}`
	response := performRequest(server, http.MethodPost, "/api/config/import", body)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}

	status := server.currentRuntimeStatus()
	if status == nil {
		t.Fatal("expected runtime status")
	}
	if !status.ConfigStatus.DashScopeKeySet {
		t.Fatalf("expected dashscope key to be set, status = %+v", status.ConfigStatus)
	}
	if !status.CheckedAt.After(initialCheckedAt) {
		t.Fatalf("expected checked_at to refresh, got %s", status.CheckedAt)
	}
}

func TestRefreshRuntimeStatusAllowsNilStatus(t *testing.T) {
	server := newTestServer(t)
	server.setRuntimeStatus(nil)

	server.refreshRuntimeStatus(*server.cfg, server.configGen.Load())

	if status := server.currentRuntimeStatus(); status != nil {
		t.Fatalf("expected nil runtime status, got %+v", status)
	}
}

func TestRefreshRuntimeStatusDiscardsStaleGeneration(t *testing.T) {
	server := newTestServer(t)
	server.cfg.RecapAI.Enabled = true
	server.cfg.RecapAI.APIKeyEnv = "HIKAMI_TEST_STALE_RECAP_KEY"
	t.Setenv("HIKAMI_TEST_STALE_RECAP_KEY", "")
	server.setRuntimeStatus(&runtime.Status{
		ConfigStatus: runtime.ConfigStatus{
			RecapKeySet: true,
			RecapKeyEnv: "HIKAMI_TEST_STALE_RECAP_KEY",
		},
	})

	staleSnapshot, staleGen := server.configSnapshot()
	server.bumpConfigGen()

	server.refreshRuntimeStatus(staleSnapshot, staleGen)

	status := server.currentRuntimeStatus()
	if status == nil {
		t.Fatal("expected runtime status")
	}
	if !status.ConfigStatus.RecapKeySet {
		t.Fatalf("stale refresh should not overwrite runtime status, got %+v", status.ConfigStatus)
	}
}

func TestUpdatePublishConfigRefreshesRuntimeStatusWithProbe(t *testing.T) {
	server := newTestServer(t)
	server.cfg.RecapAI.Enabled = false
	server.cfg.RecapAI.APIKeyEnv = "HIKAMI_TEST_RECAP_KEY"
	server.setRuntimeStatus(&runtime.Status{
		Capabilities: runtime.Capabilities{
			PublishOpus: false,
			Reason:      "recap api key not configured; publish not enabled",
		},
		ConfigStatus: runtime.ConfigStatus{
			PublishEnabled: false,
			RecapKeyEnv:    "HIKAMI_TEST_RECAP_KEY",
			RecapKeySet:    false,
		},
	})

	response := performRequest(server, http.MethodPut, "/api/config/publish", `{"enabled":true}`)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}

	status := server.currentRuntimeStatus()
	if status == nil {
		t.Fatal("expected runtime status")
	}
	if !status.Capabilities.PublishOpus {
		t.Fatalf("expected publish capability enabled, status = %+v", status.Capabilities)
	}
	if !status.ConfigStatus.PublishEnabled {
		t.Fatalf("expected publish config enabled, status = %+v", status.ConfigStatus)
	}
	if strings.Contains(status.Capabilities.Reason, "publish not enabled") {
		t.Fatalf("reason should not contain publish not enabled: %q", status.Capabilities.Reason)
	}
	if status.ConfigStatus.RecapKeySet {
		t.Fatalf("expected probe to preserve current recap key state from env, status = %+v", status.ConfigStatus)
	}
}

func TestConcurrentConfigUpdatesRefreshLatestRuntimeStatus(t *testing.T) {
	server := newTestServer(t)
	server.cfg.RecapAI.Enabled = false
	server.cfg.RecapAI.APIKeyEnv = "HIKAMI_TEST_CONCURRENT_RECAP_KEY"
	server.cfg.Publish.Enabled = false
	t.Setenv("HIKAMI_TEST_CONCURRENT_RECAP_KEY", "")
	server.setRuntimeStatus(runtime.Probe(server.cfg))

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		response := performRequest(server, http.MethodPut, "/api/config/publish", `{"enabled":true}`)
		if response.Code != http.StatusOK {
			t.Errorf("publish status = %d, body = %s", response.Code, response.Body.String())
		}
	}()
	go func() {
		defer wg.Done()
		response := performRequest(server, http.MethodPut, "/api/config/recap", `{"enabled":true}`)
		if response.Code != http.StatusOK {
			t.Errorf("recap status = %d, body = %s", response.Code, response.Body.String())
		}
	}()
	wg.Wait()

	server.publishMu.RLock()
	wantPublishEnabled := server.cfg.Publish.Enabled
	wantRecapEnabled := server.cfg.RecapAI.Enabled
	server.publishMu.RUnlock()

	status := server.currentRuntimeStatus()
	if status == nil {
		t.Fatal("expected runtime status")
	}
	if status.ConfigStatus.PublishEnabled != wantPublishEnabled {
		t.Fatalf("publish status = %v, want %v", status.ConfigStatus.PublishEnabled, wantPublishEnabled)
	}
	if status.Capabilities.PublishOpus != wantPublishEnabled {
		t.Fatalf("publish capability = %v, want %v", status.Capabilities.PublishOpus, wantPublishEnabled)
	}
	if !wantRecapEnabled {
		t.Fatal("expected recap config to be enabled")
	}
	if !strings.Contains(status.Capabilities.Reason, "recap api key not configured") {
		t.Fatalf("expected latest recap config to be reflected in reason, got %q", status.Capabilities.Reason)
	}
}

func TestSubmitASRRouteRejectsUnavailableCapability(t *testing.T) {
	server := newTestServer(t)
	server.runtimeStatus = &runtime.Status{
		Capabilities: runtime.Capabilities{
			ASRSubmit: false,
			Reason:    "asr_temp or dashscope api key not configured",
		},
	}

	response := performRequest(server, http.MethodPost, "/api/sessions/session_1/asr/submit", "")
	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "asr submit capability unavailable") {
		t.Fatalf("body missing asr capability error: %s", response.Body.String())
	}
}

func TestGenerateRecapRouteRejectsUnavailableCapability(t *testing.T) {
	server := newTestServer(t)
	server.runtimeStatus = &runtime.Status{
		Capabilities: runtime.Capabilities{
			RecapGenerate: false,
			Reason:        "recap provider unavailable",
		},
	}

	response := performRequest(server, http.MethodPost, "/api/sessions/session_1/recap/generate", "")
	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "recap capability unavailable") {
		t.Fatalf("body missing recap capability error: %s", response.Body.String())
	}
}

func TestUploadAndFetchRoutesRejectUnavailableWebDAVCapability(t *testing.T) {
	server := newTestServer(t)
	server.runtimeStatus = &runtime.Status{
		Capabilities: runtime.Capabilities{
			WebDAVUpload: false,
			Reason:       "webdav remote or rclone unavailable",
		},
	}

	uploadResponse := performRequest(server, http.MethodPost, "/api/sessions/session_1/upload", "")
	if uploadResponse.Code != http.StatusConflict {
		t.Fatalf("upload status = %d, body = %s", uploadResponse.Code, uploadResponse.Body.String())
	}
	if !strings.Contains(uploadResponse.Body.String(), "webdav capability unavailable") {
		t.Fatalf("upload body missing capability error: %s", uploadResponse.Body.String())
	}

	fetchResponse := performRequest(server, http.MethodPost, "/api/sessions/session_1/fetch", "")
	if fetchResponse.Code != http.StatusConflict {
		t.Fatalf("fetch status = %d, body = %s", fetchResponse.Code, fetchResponse.Body.String())
	}
	if !strings.Contains(fetchResponse.Body.String(), "webdav capability unavailable") {
		t.Fatalf("fetch body missing capability error: %s", fetchResponse.Body.String())
	}
}

// TestStatsDashboardRouteDoesNotDeadlock 复现并防止 stats/dashboard 的自死锁回归：
// 旧 handleStatsDashboard 在 rows.Next() 循环内调用 s.channels.Get（二次 QueryRowContext），
// 与 SetMaxOpenConns(1) 的唯一连接共用导致永久等待直至 ctx 超时。
// 现复用 session.GetDashboardStats（LEFT JOIN，循环内不查库）后应在超时内返回。
func TestStatsDashboardRouteDoesNotDeadlock(t *testing.T) {
	server := newTestServer(t)

	if _, err := server.channels.Create(context.Background(), channel.UpsertInput{
		ID:      "huize",
		Name:    "Hikami",
		UID:     1,
		Enabled: true,
	}); err != nil {
		t.Fatalf("create channel: %v", err)
	}
	// 至少一条 session 才会让 sessions 聚合查询返回行，从而进入需要补频道名的循环。
	if _, err := server.sessions.CreateLive(context.Background(), session.CreateLiveInput{
		ChannelID: "huize",
		Title:     "Live",
		RoomID:    123,
		StartedAt: time.Date(2026, 6, 24, 12, 0, 0, 0, time.Local),
	}); err != nil {
		t.Fatalf("create live session: %v", err)
	}

	// 不复用 performRequest（无法注入超时 context）；直接构造带 2s 超时的请求，
	// 若发生自死锁，ctx 超时后返回 5xx/空，测试失败。
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	request := httptest.NewRequest(http.MethodGet, "/api/stats/dashboard", nil).WithContext(ctx)
	response := httptest.NewRecorder()
	server.Router().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("dashboard status = %d, body = %s (deadlock regression?)", response.Code, response.Body.String())
	}

	var data session.DashboardData
	if err := json.Unmarshal(response.Body.Bytes(), &data); err != nil {
		t.Fatalf("unmarshal dashboard: %v", err)
	}

	// 频道名经 LEFT JOIN 正确补全。
	if len(data.SessionsByChannel) == 0 {
		t.Fatalf("expected at least one channel row, got 0")
	}
	ch := data.SessionsByChannel[0]
	if ch.ChannelID != "huize" {
		t.Fatalf("channel_id = %q, want huize", ch.ChannelID)
	}
	if ch.ChannelName == "" {
		t.Fatalf("channel_name empty for huize (JOIN not applied)")
	}
	if ch.SessionCount != 1 {
		t.Fatalf("session_count = %d, want 1", ch.SessionCount)
	}

	// 当月场次聚合正确。
	currentMonth := time.Date(2026, 6, 24, 12, 0, 0, 0, time.Local).Format("2006-01")
	var monthRow *session.DashboardMonth
	for i := range data.SessionsByMonth {
		if data.SessionsByMonth[i].Month == currentMonth {
			monthRow = &data.SessionsByMonth[i]
			break
		}
	}
	if monthRow == nil {
		t.Fatalf("current month %q not found in sessions_by_month: %+v", currentMonth, data.SessionsByMonth)
	}
	if monthRow.SessionCount != 1 {
		t.Fatalf("month session_count = %d, want 1", monthRow.SessionCount)
	}
	// ASRHours 字段存在且可解析（session 处于 discovered 非终态，按 GetDashboardStats 口径计 0）。
	if monthRow.ASRHours != 0 {
		t.Fatalf("month asr_hours = %v, want 0 for non-asr session", monthRow.ASRHours)
	}

	// CostTrend 契约完整（每项含 ASRHours/ASRCost/AICost/TotalCost 字段）。
	// 直接断言 JSON key 存在，防止契约字段被静默删除。
	var raw map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &raw); err != nil {
		t.Fatalf("unmarshal dashboard to map: %v", err)
	}
	costTrend, ok := raw["cost_trend"].([]any)
	if !ok {
		t.Fatalf("cost_trend not an array: %T", raw["cost_trend"])
	}
	for i, item := range costTrend {
		row, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("cost_trend[%d] not an object: %T", i, item)
		}
		for _, key := range []string{"month", "asr_hours", "asr_cost", "ai_cost", "total_cost"} {
			if _, exists := row[key]; !exists {
				t.Fatalf("cost_trend[%d] missing field %q", i, key)
			}
		}
	}

	if data.RecapCount != 0 {
		t.Fatalf("recap_count = %d, want 0", data.RecapCount)
	}
	if data.PublishCount != 0 {
		t.Fatalf("publish_count = %d, want 0", data.PublishCount)
	}
}

// TestStatsDashboardRouteEmptyDatabase 确认空库返回 200 且各切片为 []（非 null），
// 契合前端类型与渲染期望。
func TestStatsDashboardRouteEmptyDatabase(t *testing.T) {
	server := newTestServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	request := httptest.NewRequest(http.MethodGet, "/api/stats/dashboard", nil).WithContext(ctx)
	response := httptest.NewRecorder()
	server.Router().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("dashboard status = %d, body = %s", response.Code, response.Body.String())
	}

	var data session.DashboardData
	if err := json.Unmarshal(response.Body.Bytes(), &data); err != nil {
		t.Fatalf("unmarshal dashboard: %v", err)
	}
	if data.SessionsByMonth == nil {
		t.Fatalf("sessions_by_month is null, want []")
	}
	if data.SessionsByChannel == nil {
		t.Fatalf("sessions_by_channel is null, want []")
	}
	if data.CostTrend == nil {
		t.Fatalf("cost_trend is null, want []")
	}
	if data.DanmakuTop == nil {
		t.Fatalf("danmaku_top is null, want []")
	}
}

func TestTaskRoutes(t *testing.T) {
	server := newTestServer(t)

	if _, err := server.channels.Create(context.Background(), channel.UpsertInput{
		ID:      "huize",
		Name:    "Hikami",
		UID:     1,
		Enabled: true,
	}); err != nil {
		t.Fatalf("create channel: %v", err)
	}

	task, err := server.workerPool.Store().Create(context.Background(), worker.CreateInput{
		ChannelID: "huize",
		Type:      "discover",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	get := performRequest(server, http.MethodGet, "/api/tasks/"+task.ID, "")
	if get.Code != http.StatusOK {
		t.Fatalf("get task status = %d, body = %s", get.Code, get.Body.String())
	}

	cancel := performRequest(server, http.MethodPost, "/api/tasks/"+task.ID+"/cancel", "")
	if cancel.Code != http.StatusOK {
		t.Fatalf("cancel status = %d, body = %s", cancel.Code, cancel.Body.String())
	}

	list := performRequest(server, http.MethodGet, "/api/tasks", "")
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `"status":"cancelled"`) {
		t.Fatalf("list status = %d, body = %s", list.Code, list.Body.String())
	}
}

func newTestServer(t *testing.T) *Server {
	t.Helper()

	database, err := db.Open(filepath.Join(t.TempDir(), "hikami.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() {
		_ = database.Close()
	})
	if err := db.Migrate(database); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	cfg := &config.Config{
		OutputRoot: t.TempDir(),
		LiveRecord: config.LiveRecordConfig{
			Enabled:              true,
			AudioContainer:       "m4a",
			RequireAudioStream:   true,
			FallbackExtractAudio: false,
			RecordDanmaku:        false,
		},
	}
	status := &runtime.Status{}
	taskStore := worker.NewStore(database)
	hub := worker.NewHub()
	pool := worker.NewPool(taskStore, hub, 1, nil)
	liveManager := live_record.NewManager(
		cfg,
		channel.NewStore(database),
		session.NewStore(database),
		state.NewStore(database),
		pool,
		&fakeLiveClient{},
		fakeAudioRecorder{},
		live_record.NoopDanmakuRecorder{},
	)
	liveManager.Register(pool)
	downloadHandler := download.NewHandler(cfg, session.NewStore(database), state.NewStore(database), pool, fakeDownloader{}, channel.NewStore(database))
	discoverManager := discover.NewManager(channel.NewStore(database), session.NewStore(database), pool, fakeLister{})
	importHandler := importer.NewHandler(cfg, session.NewStore(database), state.NewStore(database), pool, fakeImportConverter{})
	asrHandler := asr.NewHandler(cfg, session.NewStore(database), state.NewStore(database), asr.LocalTranscriber{}, glossary.NewStore(database))
	recapHandler := recap.NewHandler(cfg, session.NewStore(database), state.NewStore(database), recap.LocalProvider{}, glossary.NewStore(database), nil, channel.NewStore(database))
	uploadHandler := upload.NewHandler(cfg, session.NewStore(database), state.NewStore(database), fakeCopier{})
	archiveHandler := archive.NewHandler(cfg, session.NewStore(database), state.NewStore(database), fakeCopier{}, upload.RcloneCopier{})
	publisherHandler := publisher.NewHandler(cfg, session.NewStore(database), state.NewStore(database), channel.NewStore(database))
	downloadHandler.Register(pool)
	importHandler.Register(pool)
	asrHandler.Register(pool)
	recapHandler.Register(pool)
	uploadHandler.Register(pool)
	archiveHandler.Register(pool)
	publisherHandler.Register(pool)
	if err := pool.Start(context.Background(), 1); err != nil {
		t.Fatalf("start worker pool: %v", err)
	}
	t.Cleanup(pool.Stop)
	return NewServer(
		cfg,
		status,
		channel.NewStore(database),
		channel.NewIdentifierWithBaseURL("http://127.0.0.1"),
		pool,
		liveManager,
		discoverManager,
		downloadHandler,
		session.NewStore(database),
		importHandler,
		asrHandler,
		recapHandler,
		uploadHandler,
		archiveHandler,
		publisherHandler,
		secrets.NewStore(database),
		runtimeconfig.NewStore(database),
		nil,
		glossary.NewStore(database),
		nil,
		nil,
		nil,
	)
}

type fakeLiveClient struct{}

func (fakeLiveClient) CheckLive(ctx context.Context, roomID int64, cookieHeader string) (live_record.LiveInfo, error) {
	return live_record.LiveInfo{
		RoomID:    roomID,
		Live:      true,
		Title:     "Live",
		StartedAt: time.Now(),
	}, nil
}

func (fakeLiveClient) GetStream(ctx context.Context, roomID int64, audioOnly bool, cookieHeader string) (live_record.StreamInfo, error) {
	return live_record.StreamInfo{URL: "http://example.com/live.flv", AudioOnly: true}, nil
}

type fakeAudioRecorder struct{}

func (fakeAudioRecorder) Record(ctx context.Context, stream live_record.StreamInfo, outputPath string) error {
	return nil
}

type fakeLister struct{}

func (fakeLister) List(ctx context.Context, sourceURL string, cookieFile string) ([]discover.Entry, error) {
	return []discover.Entry{
		{ID: "BV1", Title: "【直播回放】测试", WebpageURL: "https://www.bilibili.com/video/BV1"},
	}, nil
}

type fakeDownloader struct{}

func (fakeDownloader) Download(ctx context.Context, sourceURL string, rawDir string, cookieFile string) error {
	return nil
}

type fakeImportConverter struct{}

func (fakeImportConverter) Convert(ctx context.Context, inputPath string, outputPath string) error {
	return nil
}

type fakeCopier struct{}

func (fakeCopier) Copy(ctx context.Context, source string, target string) error {
	return nil
}

func performRequest(server *Server, method, path, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	server.Router().ServeHTTP(response, request)
	return response
}

func newBilibiliIdentifyServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/room/v1/Room/getRoomInfoOld":
			_, _ = w.Write([]byte(`{"code":0,"message":"0","data":{"roomid":123}}`))
		case "/xlive/web-room/v1/index/getInfoByRoom":
			_, _ = w.Write([]byte(`{
				"code":0,
				"message":"0",
				"data":{
					"room_info":{"uid":456,"room_id":123,"title":"直播标题"},
					"anchor_info":{"base_info":{"uid":456,"uname":"主播名"}}
				}
			}`))
		default:
			http.NotFound(w, r)
		}
	}))
}

type qrLoginRoundTripper struct {
	t   *testing.T
	now time.Time
}

func (rt qrLoginRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	switch r.URL.Path {
	case "/x/passport-login/web/qrcode/generate":
		return handlerJSONResponse(200, nil, `{"code":0,"message":"0","data":{"url":"https://passport.bilibili.com/scan","qrcode_key":"key_1"}}`), nil
	case "/x/passport-login/web/qrcode/poll":
		if got := r.URL.Query().Get("qrcode_key"); got != "key_1" {
			rt.t.Fatalf("qrcode_key = %q", got)
		}
		headers := http.Header{}
		expires := rt.now.Add(time.Hour).Format(http.TimeFormat)
		headers.Add("Set-Cookie", "SESSDATA=sess; Domain=.bilibili.com; Path=/; Secure; HttpOnly; Expires="+expires)
		headers.Add("Set-Cookie", "bili_jct=csrf; Domain=.bilibili.com; Path=/; Secure; Expires="+expires)
		headers.Add("Set-Cookie", "DedeUserID=42; Domain=.bilibili.com; Path=/; Secure; Expires="+expires)
		return handlerJSONResponse(200, headers, `{"code":0,"message":"0","data":{"url":"https://www.bilibili.com/","refresh_token":"refresh","code":0,"message":"登录成功"}}`), nil
	default:
		return handlerJSONResponse(404, nil, `{}`), nil
	}
}

func handlerJSONResponse(status int, headers http.Header, body string) *http.Response {
	if headers == nil {
		headers = http.Header{}
	}
	headers.Set("Content-Type", "application/json")
	return &http.Response{
		StatusCode: status,
		Header:     headers,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestPublishSessionRouteRejectsUnavailableCapability(t *testing.T) {
	server := newTestServer(t)
	server.runtimeStatus = &runtime.Status{
		Capabilities: runtime.Capabilities{
			PublishOpus: false,
			Reason:      "publish not enabled",
		},
	}

	response := performRequest(server, http.MethodPost, "/api/sessions/session_1/publish", "")
	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "publish capability unavailable") {
		t.Fatalf("body missing publish capability error: %s", response.Body.String())
	}
}

func TestEditOpusRouteRejectsUnavailableCapability(t *testing.T) {
	server := newTestServer(t)
	server.runtimeStatus = &runtime.Status{
		Capabilities: runtime.Capabilities{
			PublishOpus: false,
			Reason:      "publish not enabled",
		},
	}

	response := performRequest(server, http.MethodPost, "/api/sessions/session_1/opus/edit", "")
	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "publish capability unavailable") {
		t.Fatalf("body missing publish capability error: %s", response.Body.String())
	}
}

func TestRemoveOpusRouteRejectsUnavailableCapability(t *testing.T) {
	server := newTestServer(t)
	server.runtimeStatus = &runtime.Status{
		Capabilities: runtime.Capabilities{
			PublishOpus: false,
			Reason:      "publish not enabled",
		},
	}

	response := performRequest(server, http.MethodDelete, "/api/sessions/session_1/opus", "")
	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "publish capability unavailable") {
		t.Fatalf("body missing publish capability error: %s", response.Body.String())
	}
}

// decodeRecapResponse 解析回顾配置响应。
func decodeRecapResponse(t *testing.T, body io.Reader) recapConfigResponse {
	t.Helper()
	var resp recapConfigResponse
	if err := json.NewDecoder(body).Decode(&resp); err != nil {
		t.Fatalf("decode recap config response: %v", err)
	}
	return resp
}

// TestGetRecapConfigReturnsResponse 验证响应含新字段(provider/api_key_env/api_key_set),
// 且 provider/base_url/model 经 Effective* 兜底为默认值(空配置时回填 DeepSeek 默认)。
func TestGetRecapConfigReturnsResponse(t *testing.T) {
	server := newTestServer(t)

	resp := performRequest(server, http.MethodGet, "/api/config/recap", "")
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.Code, resp.Body.String())
	}
	got := decodeRecapResponse(t, resp.Body)
	// 默认配置兜底:newTestServer 的 cfg.RecapAI 为零值,响应应回落到 DeepSeek 默认
	if got.Provider != config.DefaultRecapProvider {
		t.Fatalf("provider = %q, want %q (EffectiveProvider fallback)", got.Provider, config.DefaultRecapProvider)
	}
	if got.BaseURL != config.DefaultRecapBaseURL {
		t.Fatalf("base_url = %q, want %q (EffectiveBaseURL fallback)", got.BaseURL, config.DefaultRecapBaseURL)
	}
	if got.Model != config.DefaultRecapModel {
		t.Fatalf("model = %q, want %q (EffectiveModel fallback)", got.Model, config.DefaultRecapModel)
	}
	if got.APIKeyEnv != "AI_API_KEY" {
		t.Fatalf("api_key_env = %q, want AI_API_KEY (EffectiveAPIKeyEnv fallback)", got.APIKeyEnv)
	}
	// 响应永不返回明文密钥(无 api_key 字段),只有 api_key_set 布尔
	body := resp.Body.String()
	if strings.Contains(body, "\"api_key\":") {
		t.Fatalf("response must not contain plaintext api_key: %s", body)
	}
}

// TestUpdateRecapConfigProvider 验证 provider 切换生效,非法值 400。
func TestUpdateRecapConfigProvider(t *testing.T) {
	server := newTestServer(t)

	// 合法 provider
	ok := performRequest(server, http.MethodPut, "/api/config/recap", `{"provider":"anthropic"}`)
	if ok.Code != http.StatusOK {
		t.Fatalf("update provider anthropic status = %d, body = %s", ok.Code, ok.Body.String())
	}
	got := decodeRecapResponse(t, ok.Body)
	if got.Provider != "anthropic" {
		t.Fatalf("provider = %q, want anthropic", got.Provider)
	}

	// 非法 provider → 400
	bad := performRequest(server, http.MethodPut, "/api/config/recap", `{"provider":"nonexistent"}`)
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("invalid provider status = %d, want 400, body = %s", bad.Code, bad.Body.String())
	}
}

// TestUpdateRecapConfigEmptyProviderFallback 验证 provider 留空可存入,
// 响应层回落到 openai_compatible(DeepSeek 默认)。
func TestUpdateRecapConfigEmptyProviderFallback(t *testing.T) {
	server := newTestServer(t)

	// 先设非默认 provider,再清空
	_ = performRequest(server, http.MethodPut, "/api/config/recap", `{"provider":"anthropic"}`)
	resp := performRequest(server, http.MethodPut, "/api/config/recap", `{"provider":""}`)
	if resp.Code != http.StatusOK {
		t.Fatalf("empty provider status = %d, body = %s", resp.Code, resp.Body.String())
	}
	got := decodeRecapResponse(t, resp.Body)
	if got.Provider != config.DefaultRecapProvider {
		t.Fatalf("empty provider should fall back to %q, got %q", config.DefaultRecapProvider, got.Provider)
	}
}

// TestUpdateRecapConfigEmptyBaseURLModelFallback 验证 base_url/model 留空存入后,
// 响应回落到 DeepSeek 官方默认。
func TestUpdateRecapConfigEmptyBaseURLModelFallback(t *testing.T) {
	server := newTestServer(t)

	resp := performRequest(server, http.MethodPut, "/api/config/recap", `{"base_url":"","model":""}`)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.Code, resp.Body.String())
	}
	got := decodeRecapResponse(t, resp.Body)
	if got.BaseURL != config.DefaultRecapBaseURL {
		t.Fatalf("empty base_url should fall back to %q, got %q", config.DefaultRecapBaseURL, got.BaseURL)
	}
	if got.Model != config.DefaultRecapModel {
		t.Fatalf("empty model should fall back to %q, got %q", config.DefaultRecapModel, got.Model)
	}
}

// TestUpdateRecapConfigKeyLifecycle 验证密钥三态:留空保留、更新、clear_key 清除。
func TestUpdateRecapConfigKeyLifecycle(t *testing.T) {
	server := newTestServer(t)
	t.Cleanup(func() { os.Unsetenv("AI_API_KEY") })

	// 1. 设置密钥
	set := performRequest(server, http.MethodPut, "/api/config/recap", `{"api_key":"sk-test-123"}`)
	if set.Code != http.StatusOK {
		t.Fatalf("set key status = %d, body = %s", set.Code, set.Body.String())
	}
	if got := decodeRecapResponse(t, set.Body); !got.APIKeySet {
		t.Fatalf("after setting key, api_key_set should be true")
	}
	if v := os.Getenv("AI_API_KEY"); v != "sk-test-123" {
		t.Fatalf("AI_API_KEY env = %q, want sk-test-123", v)
	}

	// 2. 留空保存(不传 api_key),密钥保留
	keep := performRequest(server, http.MethodPut, "/api/config/recap", `{"max_tokens":8192}`)
	if keep.Code != http.StatusOK {
		t.Fatalf("keep key status = %d, body = %s", keep.Code, keep.Body.String())
	}
	if got := decodeRecapResponse(t, keep.Body); !got.APIKeySet {
		t.Fatalf("empty api_key should preserve existing key, api_key_set should stay true")
	}
	if v := os.Getenv("AI_API_KEY"); v != "sk-test-123" {
		t.Fatalf("AI_API_KEY env = %q, want sk-test-123 (preserved)", v)
	}

	// 3. clear_key 清除
	clear := performRequest(server, http.MethodPut, "/api/config/recap", `{"clear_key":true}`)
	if clear.Code != http.StatusOK {
		t.Fatalf("clear key status = %d, body = %s", clear.Code, clear.Body.String())
	}
	if got := decodeRecapResponse(t, clear.Body); got.APIKeySet {
		t.Fatalf("after clear_key, api_key_set should be false")
	}
	if v := os.Getenv("AI_API_KEY"); v != "" {
		t.Fatalf("AI_API_KEY env = %q, want empty after clear", v)
	}
}

// TestUpdateRecapConfigEnvRenameMigratesSecret 验证改 api_key_env 名时,
// 旧密钥迁移到新 key(用户未输入新值时复用旧值),不留孤儿 secret。
func TestUpdateRecapConfigEnvRenameMigratesSecret(t *testing.T) {
	server := newTestServer(t)
	t.Cleanup(func() {
		os.Unsetenv("AI_API_KEY")
		os.Unsetenv("MY_RECAP_KEY")
	})

	// 先设密钥到默认 AI_API_KEY
	set := performRequest(server, http.MethodPut, "/api/config/recap", `{"api_key":"sk-migrate"}`)
	if set.Code != http.StatusOK {
		t.Fatalf("set key status = %d, body = %s", set.Code, set.Body.String())
	}

	// 改 env 名,不输入新密钥 → 应迁移旧值到新 key
	rename := performRequest(server, http.MethodPut, "/api/config/recap", `{"api_key_env":"MY_RECAP_KEY"}`)
	if rename.Code != http.StatusOK {
		t.Fatalf("rename env status = %d, body = %s", rename.Code, rename.Body.String())
	}
	got := decodeRecapResponse(t, rename.Body)
	if got.APIKeyEnv != "MY_RECAP_KEY" {
		t.Fatalf("api_key_env = %q, want MY_RECAP_KEY", got.APIKeyEnv)
	}
	if !got.APIKeySet {
		t.Fatalf("after rename, api_key_set should stay true (migrated)")
	}
	if v := os.Getenv("MY_RECAP_KEY"); v != "sk-migrate" {
		t.Fatalf("MY_RECAP_KEY env = %q, want sk-migrate (migrated)", v)
	}
	if v := os.Getenv("AI_API_KEY"); v != "" {
		t.Fatalf("AI_API_KEY env should be unset after rename, got %q", v)
	}
}

// TestUpdateRecapConfigRejectsBadEnvName 验证非法环境变量名 → 400。
func TestUpdateRecapConfigRejectsBadEnvName(t *testing.T) {
	server := newTestServer(t)

	cases := []string{
		`{"api_key_env":"1INVALID"}`,  // 首字符数字
		`{"api_key_env":"BAD-NAME"}`,  // 含连字符
		`{"api_key_env":"has space"}`, // 含空格
	}
	for _, body := range cases {
		resp := performRequest(server, http.MethodPut, "/api/config/recap", body)
		if resp.Code != http.StatusBadRequest {
			t.Fatalf("bad env name %q status = %d, want 400, body = %s", body, resp.Code, resp.Body.String())
		}
	}

	// 合法名(含下划线和数字,首字符字母)应通过
	ok := performRequest(server, http.MethodPut, "/api/config/recap", `{"api_key_env":"RECAP_KEY_V2"}`)
	if ok.Code != http.StatusOK {
		t.Fatalf("valid env name status = %d, want 200, body = %s", ok.Code, ok.Body.String())
	}
}

// TestUpdateRecapConfigEnvRenameWithNewSecretNoOldSecret 验证首次配置路径:
// 改 env 名 + 输入新密钥 + 旧 secret 不存在时,新密钥必须写入新 key(codex 审核高[2] 回归)。
func TestUpdateRecapConfigEnvRenameWithNewSecretNoOldSecret(t *testing.T) {
	server := newTestServer(t)
	t.Cleanup(func() {
		os.Unsetenv("AI_API_KEY")
		os.Unsetenv("FIRST_RECAP_KEY")
	})
	// 确保旧 key 无值(首次配置场景)
	os.Unsetenv("AI_API_KEY")

	resp := performRequest(server, http.MethodPut, "/api/config/recap",
		`{"api_key_env":"FIRST_RECAP_KEY","api_key":"sk-first"}`)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.Code, resp.Body.String())
	}
	got := decodeRecapResponse(t, resp.Body)
	if !got.APIKeySet {
		t.Fatalf("first-time config with new key should set api_key_set=true")
	}
	if v := os.Getenv("FIRST_RECAP_KEY"); v != "sk-first" {
		t.Fatalf("FIRST_RECAP_KEY env = %q, want sk-first (new key must be saved even without old secret)", v)
	}
}

// TestUpdateRecapConfigConcurrentFieldUpdates 验证并发 PUT 改不同字段不会互相覆盖:
// 一个改 max_tokens,另一个改 model,最终两个字段都生效(codex 审核高[1] 回归)。
func TestUpdateRecapConfigConcurrentFieldUpdates(t *testing.T) {
	server := newTestServer(t)

	const goroutines = 30
	var wg sync.WaitGroup
	wg.Add(goroutines)
	// 偶数下标改 max_tokens,奇数下标改 model,二者并发
	for i := 0; i < goroutines; i++ {
		i := i
		go func() {
			defer wg.Done()
			var body string
			if i%2 == 0 {
				body = `{"max_tokens":4096}`
			} else {
				body = `{"model":"concurrent-model"}`
			}
			resp := performRequest(server, http.MethodPut, "/api/config/recap", body)
			if resp.Code != http.StatusOK {
				t.Errorf("goroutine %d status = %d, body = %s", i, resp.Code, resp.Body.String())
			}
		}()
	}
	wg.Wait()

	// 最终两个字段都必须是最后一次写入的值(未被对方覆盖)
	resp := performRequest(server, http.MethodGet, "/api/config/recap", "")
	if resp.Code != http.StatusOK {
		t.Fatalf("final get status = %d", resp.Code)
	}
	got := decodeRecapResponse(t, resp.Body)
	if got.MaxTokens != 4096 {
		t.Fatalf("max_tokens = %d, want 4096 (concurrent update lost)", got.MaxTokens)
	}
	if got.Model != "concurrent-model" {
		t.Fatalf("model = %q, want concurrent-model (concurrent update lost)", got.Model)
	}
}

// TestUpdateRecapConfigEnvClearedToDefaultMigratesSecret 验证 api_key_env 从自定义值清空回默认时,
// secret 从旧自定义 key 迁移到默认 AI_API_KEY,不留孤儿(codex 复审发现的高优回归)。
func TestUpdateRecapConfigEnvClearedToDefaultMigratesSecret(t *testing.T) {
	server := newTestServer(t)
	t.Cleanup(func() {
		os.Unsetenv("AI_API_KEY")
		os.Unsetenv("CUSTOM_RECAP_KEY")
	})

	// 1. 先设自定义 env 名 + 密钥
	set := performRequest(server, http.MethodPut, "/api/config/recap",
		`{"api_key_env":"CUSTOM_RECAP_KEY","api_key":"sk-custom"}`)
	if set.Code != http.StatusOK {
		t.Fatalf("set custom env status = %d, body = %s", set.Code, set.Body.String())
	}
	if v := os.Getenv("CUSTOM_RECAP_KEY"); v != "sk-custom" {
		t.Fatalf("CUSTOM_RECAP_KEY = %q, want sk-custom", v)
	}

	// 2. 清空 api_key_env(留空 = 用默认),同时输入新密钥
	clear := performRequest(server, http.MethodPut, "/api/config/recap",
		`{"api_key_env":"","api_key":"sk-migrated"}`)
	if clear.Code != http.StatusOK {
		t.Fatalf("clear env status = %d, body = %s", clear.Code, clear.Body.String())
	}
	got := decodeRecapResponse(t, clear.Body)
	if got.APIKeyEnv != "AI_API_KEY" {
		t.Fatalf("after clearing, api_key_env = %q, want AI_API_KEY (default fallback)", got.APIKeyEnv)
	}
	if !got.APIKeySet {
		t.Fatalf("after clearing env with new key, api_key_set should be true")
	}
	// 新密钥必须落到默认 AI_API_KEY
	if v := os.Getenv("AI_API_KEY"); v != "sk-migrated" {
		t.Fatalf("AI_API_KEY = %q, want sk-migrated (secret should migrate to default key)", v)
	}
	// 旧自定义 key 必须被清除(无孤儿 secret)
	if v := os.Getenv("CUSTOM_RECAP_KEY"); v != "" {
		t.Fatalf("CUSTOM_RECAP_KEY = %q, want empty (old key should be cleared, no orphan)", v)
	}
}

// decodeDashScopeResponse 解析 DashScope 配置响应。
func decodeDashScopeResponse(t *testing.T, body io.Reader) dashscopeConfigResponse {
	t.Helper()
	var resp dashscopeConfigResponse
	if err := json.NewDecoder(body).Decode(&resp); err != nil {
		t.Fatalf("decode dashscope config response: %v", err)
	}
	return resp
}

// TestGetDashScopeConfigReturnsResponse 验证响应含新字段(api_key_env/api_key_set),
// 且空配置时 api_key_env 兜底为 DASHSCOPE_API_KEY,响应永不回明文密钥。
func TestGetDashScopeConfigReturnsResponse(t *testing.T) {
	server := newTestServer(t)

	resp := performRequest(server, http.MethodGet, "/api/config/dashscope", "")
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.Code, resp.Body.String())
	}
	got := decodeDashScopeResponse(t, resp.Body)
	if got.APIKeyEnv != "DASHSCOPE_API_KEY" {
		t.Fatalf("api_key_env = %q, want DASHSCOPE_API_KEY (EffectiveAPIKeyEnv fallback)", got.APIKeyEnv)
	}
	body := resp.Body.String()
	if strings.Contains(body, "\"api_key\":") {
		t.Fatalf("response must not contain plaintext api_key: %s", body)
	}
}

// TestUpdateDashScopeConfigFields 验证各非密钥字段更新生效。
func TestUpdateDashScopeConfigFields(t *testing.T) {
	server := newTestServer(t)

	body := `{"model":"paraformer-v2","language":"en","diarization_enabled":true,"speaker_count":3,"vocabulary_id":"vocab-1","asr_url":"https://dashscope.aliyuncs.com/api/v1/services/audio/asr/transcription","tasks_url":"https://dashscope.aliyuncs.com/api/v1/tasks"}`
	resp := performRequest(server, http.MethodPut, "/api/config/dashscope", body)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.Code, resp.Body.String())
	}
	got := decodeDashScopeResponse(t, resp.Body)
	if got.Model != "paraformer-v2" {
		t.Fatalf("model = %q, want paraformer-v2", got.Model)
	}
	if got.Language != "en" {
		t.Fatalf("language = %q, want en", got.Language)
	}
	if !got.DiarizationEnabled {
		t.Fatalf("diarization_enabled = false, want true")
	}
	if got.SpeakerCount != 3 {
		t.Fatalf("speaker_count = %d, want 3", got.SpeakerCount)
	}
	if got.VocabularyID != "vocab-1" {
		t.Fatalf("vocabulary_id = %q, want vocab-1", got.VocabularyID)
	}
}

// TestUpdateDashScopeConfigKeyLifecycle 验证密钥三态:设置、留空保留、clear_key 清除。
func TestUpdateDashScopeConfigKeyLifecycle(t *testing.T) {
	server := newTestServer(t)
	t.Cleanup(func() { os.Unsetenv("DASHSCOPE_API_KEY") })

	// 1. 设置密钥
	set := performRequest(server, http.MethodPut, "/api/config/dashscope", `{"api_key":"sk-dash"}`)
	if set.Code != http.StatusOK {
		t.Fatalf("set key status = %d, body = %s", set.Code, set.Body.String())
	}
	if got := decodeDashScopeResponse(t, set.Body); !got.APIKeySet {
		t.Fatalf("after setting key, api_key_set should be true")
	}
	if v := os.Getenv("DASHSCOPE_API_KEY"); v != "sk-dash" {
		t.Fatalf("DASHSCOPE_API_KEY env = %q, want sk-dash", v)
	}

	// 2. 留空保存(不传 api_key),密钥保留
	keep := performRequest(server, http.MethodPut, "/api/config/dashscope", `{"model":"fun-asr"}`)
	if keep.Code != http.StatusOK {
		t.Fatalf("keep key status = %d, body = %s", keep.Code, keep.Body.String())
	}
	if got := decodeDashScopeResponse(t, keep.Body); !got.APIKeySet {
		t.Fatalf("empty api_key should preserve existing key")
	}
	if v := os.Getenv("DASHSCOPE_API_KEY"); v != "sk-dash" {
		t.Fatalf("DASHSCOPE_API_KEY env = %q, want sk-dash (preserved)", v)
	}

	// 3. clear_key 清除
	clear := performRequest(server, http.MethodPut, "/api/config/dashscope", `{"clear_key":true}`)
	if clear.Code != http.StatusOK {
		t.Fatalf("clear key status = %d, body = %s", clear.Code, clear.Body.String())
	}
	if got := decodeDashScopeResponse(t, clear.Body); got.APIKeySet {
		t.Fatalf("after clear_key, api_key_set should be false")
	}
	if v := os.Getenv("DASHSCOPE_API_KEY"); v != "" {
		t.Fatalf("DASHSCOPE_API_KEY env = %q, want empty after clear", v)
	}
}

// TestUpdateDashScopeConfigRejectsBadURL 验证 asr_url/tasks_url 非 http(s) 或无 host → 400。
func TestUpdateDashScopeConfigRejectsBadURL(t *testing.T) {
	server := newTestServer(t)

	cases := map[string]string{
		`{"asr_url":"ftp://x.example.com"}`:   "asr_url",
		`{"asr_url":"not-a-url"}`:             "asr_url",
		`{"tasks_url":"https://"}`:            "tasks_url",
		`{"tasks_url":"javascript:alert(1)"}`: "tasks_url",
	}
	for body, field := range cases {
		resp := performRequest(server, http.MethodPut, "/api/config/dashscope", body)
		if resp.Code != http.StatusBadRequest {
			t.Fatalf("bad %s status = %d, want 400, body = %s", field, resp.Code, resp.Body.String())
		}
	}

	// 合法 http(s) 应通过
	ok := performRequest(server, http.MethodPut, "/api/config/dashscope", `{"asr_url":"https://dashscope.aliyuncs.com/x"}`)
	if ok.Code != http.StatusOK {
		t.Fatalf("valid asr_url status = %d, want 200, body = %s", ok.Code, ok.Body.String())
	}
}

// TestUpdateDashScopeConfigRejectsNegativeSpeakerCount 验证 speaker_count < 0 → 400。
func TestUpdateDashScopeConfigRejectsNegativeSpeakerCount(t *testing.T) {
	server := newTestServer(t)

	resp := performRequest(server, http.MethodPut, "/api/config/dashscope", `{"speaker_count":-1}`)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("negative speaker_count status = %d, want 400", resp.Code)
	}

	// 0 合法(自动检测)
	ok := performRequest(server, http.MethodPut, "/api/config/dashscope", `{"speaker_count":0}`)
	if ok.Code != http.StatusOK {
		t.Fatalf("speaker_count=0 status = %d, want 200", ok.Code)
	}
}

// TestUpdateDashScopeConfigRejectsBadEnvName 验证非法密钥环境变量名 → 400。
func TestUpdateDashScopeConfigRejectsBadEnvName(t *testing.T) {
	server := newTestServer(t)

	resp := performRequest(server, http.MethodPut, "/api/config/dashscope", `{"api_key_env":"BAD-NAME"}`)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("bad env name status = %d, want 400", resp.Code)
	}
}

// TestUpdateDashScopeConfigEnvRenameMigratesSecret 验证改 api_key_env 名时,
// 旧密钥迁移到新 key,不留孤儿 secret。
func TestUpdateDashScopeConfigEnvRenameMigratesSecret(t *testing.T) {
	server := newTestServer(t)
	t.Cleanup(func() {
		os.Unsetenv("DASHSCOPE_API_KEY")
		os.Unsetenv("MY_DASH_KEY")
	})

	set := performRequest(server, http.MethodPut, "/api/config/dashscope", `{"api_key":"sk-migrate"}`)
	if set.Code != http.StatusOK {
		t.Fatalf("set key status = %d", set.Code)
	}

	// 改 env 名,不输入新密钥 → 迁移旧值到新 key
	rename := performRequest(server, http.MethodPut, "/api/config/dashscope", `{"api_key_env":"MY_DASH_KEY"}`)
	if rename.Code != http.StatusOK {
		t.Fatalf("rename env status = %d, body = %s", rename.Code, rename.Body.String())
	}
	got := decodeDashScopeResponse(t, rename.Body)
	if got.APIKeyEnv != "MY_DASH_KEY" {
		t.Fatalf("api_key_env = %q, want MY_DASH_KEY", got.APIKeyEnv)
	}
	if !got.APIKeySet {
		t.Fatalf("after rename, api_key_set should stay true (migrated)")
	}
	if v := os.Getenv("MY_DASH_KEY"); v != "sk-migrate" {
		t.Fatalf("MY_DASH_KEY = %q, want sk-migrate (migrated)", v)
	}
	if v := os.Getenv("DASHSCOPE_API_KEY"); v != "" {
		t.Fatalf("DASHSCOPE_API_KEY should be unset after rename, got %q", v)
	}
}

// TestUpdateDashScopeConfigEnvClearedToDefaultMigratesSecret 验证 api_key_env 从自定义值清空回默认时,
// secret 从旧自定义 key 迁移到默认 DASHSCOPE_API_KEY,不留孤儿(codex 审核低[3],对应 Recap 同款测试)。
func TestUpdateDashScopeConfigEnvClearedToDefaultMigratesSecret(t *testing.T) {
	server := newTestServer(t)
	t.Cleanup(func() {
		os.Unsetenv("DASHSCOPE_API_KEY")
		os.Unsetenv("CUSTOM_DASH_KEY")
	})

	// 1. 先设自定义 env 名 + 密钥
	set := performRequest(server, http.MethodPut, "/api/config/dashscope",
		`{"api_key_env":"CUSTOM_DASH_KEY","api_key":"sk-custom"}`)
	if set.Code != http.StatusOK {
		t.Fatalf("set custom env status = %d, body = %s", set.Code, set.Body.String())
	}
	if v := os.Getenv("CUSTOM_DASH_KEY"); v != "sk-custom" {
		t.Fatalf("CUSTOM_DASH_KEY = %q, want sk-custom", v)
	}

	// 2. 清空 api_key_env(留空 = 用默认),同时输入新密钥
	clear := performRequest(server, http.MethodPut, "/api/config/dashscope",
		`{"api_key_env":"","api_key":"sk-migrated"}`)
	if clear.Code != http.StatusOK {
		t.Fatalf("clear env status = %d, body = %s", clear.Code, clear.Body.String())
	}
	got := decodeDashScopeResponse(t, clear.Body)
	if got.APIKeyEnv != "DASHSCOPE_API_KEY" {
		t.Fatalf("after clearing, api_key_env = %q, want DASHSCOPE_API_KEY (default fallback)", got.APIKeyEnv)
	}
	if !got.APIKeySet {
		t.Fatalf("after clearing env with new key, api_key_set should be true")
	}
	// 新密钥必须落到默认 DASHSCOPE_API_KEY
	if v := os.Getenv("DASHSCOPE_API_KEY"); v != "sk-migrated" {
		t.Fatalf("DASHSCOPE_API_KEY = %q, want sk-migrated (secret should migrate to default key)", v)
	}
	// 旧自定义 key 必须被清除(无孤儿 secret)
	if v := os.Getenv("CUSTOM_DASH_KEY"); v != "" {
		t.Fatalf("CUSTOM_DASH_KEY = %q, want empty (old key should be cleared, no orphan)", v)
	}
}

// TestUpdateDashScopeConfigConcurrentEnvRename 验证并发 env 改名不会产生孤儿 secret:
// 最终生效的 api_key_env 对应的 secret 必须存在(codex 审核中[2] 回归)。
func TestUpdateDashScopeConfigConcurrentEnvRename(t *testing.T) {
	server := newTestServer(t)
	t.Cleanup(func() {
		os.Unsetenv("DASHSCOPE_API_KEY")
		os.Unsetenv("CONCURRENT_DASH_KEY")
	})

	// 先在默认 key 设密钥
	if resp := performRequest(server, http.MethodPut, "/api/config/dashscope", `{"api_key":"sk-base"}`); resp.Code != http.StatusOK {
		t.Fatalf("set base key status = %d", resp.Code)
	}

	// 并发多个请求改 env 名到同一个新 key
	const n = 20
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			resp := performRequest(server, http.MethodPut, "/api/config/dashscope", `{"api_key_env":"CONCURRENT_DASH_KEY"}`)
			if resp.Code != http.StatusOK {
				t.Errorf("concurrent rename status = %d, body = %s", resp.Code, resp.Body.String())
			}
		}()
	}
	wg.Wait()

	// 最终:CONCURRENT_DASH_KEY 必须有值(secret 迁移成功),DASHSCOPE_API_KEY 应被清空
	if v := os.Getenv("CONCURRENT_DASH_KEY"); v == "" {
		t.Fatalf("CONCURRENT_DASH_KEY should have migrated secret (non-empty), got empty (orphan/lost)")
	}
}

// decodeASRS3Response 解析 ASR S3 配置响应。
func decodeASRS3Response(t *testing.T, body io.Reader) asrS3ConfigResponse {
	t.Helper()
	var resp asrS3ConfigResponse
	if err := json.NewDecoder(body).Decode(&resp); err != nil {
		t.Fatalf("decode asr s3 config response: %v", err)
	}
	return resp
}

// TestGetASRS3ConfigReturnsResponse 验证响应含新字段,空配置时 access_key_env 兜底,
// 且响应永不回明文 access_key_secret。
func TestGetASRS3ConfigReturnsResponse(t *testing.T) {
	server := newTestServer(t)

	resp := performRequest(server, http.MethodGet, "/api/config/asr-s3", "")
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.Code, resp.Body.String())
	}
	got := decodeASRS3Response(t, resp.Body)
	if got.AccessKeyEnv != "ASR_S3_ACCESS_KEY_SECRET" {
		t.Fatalf("access_key_env = %q, want ASR_S3_ACCESS_KEY_SECRET (EffectiveAccessKeyEnv fallback)", got.AccessKeyEnv)
	}
	body := resp.Body.String()
	if strings.Contains(body, "\"access_key_secret\":") {
		t.Fatalf("response must not contain plaintext access_key_secret: %s", body)
	}
}

// TestUpdateASRS3ConfigFields 验证各非密钥字段更新生效。
func TestUpdateASRS3ConfigFields(t *testing.T) {
	server := newTestServer(t)

	body := `{"endpoint":"https://oss-cn-hangzhou.aliyuncs.com","bucket":"my-bucket","access_key_id":"LTAI","region":"oss-cn-hangzhou","public_url_prefix":"https://my-bucket.oss-cn-hangzhou.aliyuncs.com/asr","use_path_style":false}`
	resp := performRequest(server, http.MethodPut, "/api/config/asr-s3", body)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.Code, resp.Body.String())
	}
	got := decodeASRS3Response(t, resp.Body)
	if got.Endpoint != "https://oss-cn-hangzhou.aliyuncs.com" {
		t.Fatalf("endpoint = %q", got.Endpoint)
	}
	if got.Bucket != "my-bucket" {
		t.Fatalf("bucket = %q", got.Bucket)
	}
	if got.AccessKeyID != "LTAI" {
		t.Fatalf("access_key_id = %q", got.AccessKeyID)
	}
	if got.UsePathStyle {
		t.Fatalf("use_path_style = true, want false")
	}
}

// TestUpdateASRS3ConfigSecretInStore 验证密钥写入 secrets.Store + env,
// 不进 cfg.AccessKeySecret 字段(codex 审核高[4] 核心要求)。
func TestUpdateASRS3ConfigSecretInStore(t *testing.T) {
	server := newTestServer(t)
	t.Cleanup(func() { os.Unsetenv("ASR_S3_ACCESS_KEY_SECRET") })

	resp := performRequest(server, http.MethodPut, "/api/config/asr-s3", `{"access_key_secret":"sk-s3-secret"}`)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.Code, resp.Body.String())
	}
	got := decodeASRS3Response(t, resp.Body)
	if !got.AccessKeySet {
		t.Fatalf("after setting secret, access_key_set should be true")
	}
	// 密钥写入 env(运行时经 SecretResolved() 读取)
	if v := os.Getenv("ASR_S3_ACCESS_KEY_SECRET"); v != "sk-s3-secret" {
		t.Fatalf("ASR_S3_ACCESS_KEY_SECRET env = %q, want sk-s3-secret", v)
	}
	// cfg.AccessKeySecret 字段不应被写入(密钥走 secrets store,不进 cfg 字段)
	server.publishMu.RLock()
	fieldSecret := server.cfg.ASRS3.AccessKeySecret
	server.publishMu.RUnlock()
	if fieldSecret != "" {
		t.Fatalf("cfg.ASRS3.AccessKeySecret = %q, want empty (secret must live in secrets store, not cfg field)", fieldSecret)
	}
	// 直接验证 secrets.Store 落盘(codex 复审低[1]):避免未来误改成只 os.Setenv 也能通过
	stored, err := server.secrets.Get(context.Background(), "ASR_S3_ACCESS_KEY_SECRET")
	if err != nil {
		t.Fatalf("secrets.Get: %v", err)
	}
	if stored != "sk-s3-secret" {
		t.Fatalf("secrets store value = %q, want sk-s3-secret (secret must persist in DB store)", stored)
	}
}

// TestUpdateASRS3ConfigKeyLifecycle 验证密钥三态:设置、留空保留、clear_secret 清除。
func TestUpdateASRS3ConfigKeyLifecycle(t *testing.T) {
	server := newTestServer(t)
	t.Cleanup(func() { os.Unsetenv("ASR_S3_ACCESS_KEY_SECRET") })

	// 1. 设置
	set := performRequest(server, http.MethodPut, "/api/config/asr-s3", `{"access_key_secret":"sk-s3"}`)
	if set.Code != http.StatusOK {
		t.Fatalf("set status = %d", set.Code)
	}
	if got := decodeASRS3Response(t, set.Body); !got.AccessKeySet {
		t.Fatalf("after setting, access_key_set should be true")
	}

	// 2. 留空保留
	keep := performRequest(server, http.MethodPut, "/api/config/asr-s3", `{"bucket":"b"}`)
	if keep.Code != http.StatusOK {
		t.Fatalf("keep status = %d", keep.Code)
	}
	if got := decodeASRS3Response(t, keep.Body); !got.AccessKeySet {
		t.Fatalf("empty secret should preserve existing, access_key_set should stay true")
	}
	if v := os.Getenv("ASR_S3_ACCESS_KEY_SECRET"); v != "sk-s3" {
		t.Fatalf("env = %q, want sk-s3 (preserved)", v)
	}

	// 3. clear_secret 清除
	clear := performRequest(server, http.MethodPut, "/api/config/asr-s3", `{"clear_secret":true}`)
	if clear.Code != http.StatusOK {
		t.Fatalf("clear status = %d", clear.Code)
	}
	if got := decodeASRS3Response(t, clear.Body); got.AccessKeySet {
		t.Fatalf("after clear_secret, access_key_set should be false")
	}
	if v := os.Getenv("ASR_S3_ACCESS_KEY_SECRET"); v != "" {
		t.Fatalf("env = %q, want empty after clear", v)
	}
}

// TestUpdateASRS3ConfigRejectsBadEndpoint 验证 endpoint 非 http(s) 或含 .. / 反斜杠 → 400。
func TestUpdateASRS3ConfigRejectsBadEndpoint(t *testing.T) {
	server := newTestServer(t)

	cases := []string{
		`{"endpoint":"ftp://x.example.com"}`,
		`{"endpoint":"not-a-url"}`,
		`{"endpoint":"https://"}`,
		`{"endpoint":"https://x/../escape"}`,
		`{"endpoint":"https://oss.example.com\\bad"}`,
	}
	for _, body := range cases {
		resp := performRequest(server, http.MethodPut, "/api/config/asr-s3", body)
		if resp.Code != http.StatusBadRequest {
			t.Fatalf("bad endpoint %q status = %d, want 400", body, resp.Code)
		}
	}

	// 合法 endpoint 通过
	ok := performRequest(server, http.MethodPut, "/api/config/asr-s3", `{"endpoint":"https://oss.example.com"}`)
	if ok.Code != http.StatusOK {
		t.Fatalf("valid endpoint status = %d, want 200", ok.Code)
	}
}

// TestUpdateASRS3ConfigRejectsBadPublicURLPrefix 验证 public_url_prefix 非 http(s) 或无 host → 400。
func TestUpdateASRS3ConfigRejectsBadPublicURLPrefix(t *testing.T) {
	server := newTestServer(t)

	cases := []string{
		`{"public_url_prefix":"ftp://x"}`,
		`{"public_url_prefix":"https://"}`,
		`{"public_url_prefix":"no-scheme"}`,
	}
	for _, body := range cases {
		resp := performRequest(server, http.MethodPut, "/api/config/asr-s3", body)
		if resp.Code != http.StatusBadRequest {
			t.Fatalf("bad prefix %q status = %d, want 400", body, resp.Code)
		}
	}
}

// TestUpdateASRS3ConfigRejectsBadEnvName 验证非法密钥环境变量名 → 400。
func TestUpdateASRS3ConfigRejectsBadEnvName(t *testing.T) {
	server := newTestServer(t)

	resp := performRequest(server, http.MethodPut, "/api/config/asr-s3", `{"access_key_env":"BAD-NAME"}`)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("bad env name status = %d, want 400", resp.Code)
	}
}

// TestUpdateASRS3ConfigEnvRenameMigratesSecret 验证改 access_key_env 名时旧密钥迁移到新 key。
func TestUpdateASRS3ConfigEnvRenameMigratesSecret(t *testing.T) {
	server := newTestServer(t)
	t.Cleanup(func() {
		os.Unsetenv("ASR_S3_ACCESS_KEY_SECRET")
		os.Unsetenv("MY_S3_KEY")
	})

	set := performRequest(server, http.MethodPut, "/api/config/asr-s3", `{"access_key_secret":"sk-migrate"}`)
	if set.Code != http.StatusOK {
		t.Fatalf("set status = %d", set.Code)
	}

	rename := performRequest(server, http.MethodPut, "/api/config/asr-s3", `{"access_key_env":"MY_S3_KEY"}`)
	if rename.Code != http.StatusOK {
		t.Fatalf("rename status = %d, body = %s", rename.Code, rename.Body.String())
	}
	got := decodeASRS3Response(t, rename.Body)
	if got.AccessKeyEnv != "MY_S3_KEY" {
		t.Fatalf("access_key_env = %q, want MY_S3_KEY", got.AccessKeyEnv)
	}
	if !got.AccessKeySet {
		t.Fatalf("after rename, access_key_set should stay true (migrated)")
	}
	if v := os.Getenv("MY_S3_KEY"); v != "sk-migrate" {
		t.Fatalf("MY_S3_KEY = %q, want sk-migrate (migrated)", v)
	}
	if v := os.Getenv("ASR_S3_ACCESS_KEY_SECRET"); v != "" {
		t.Fatalf("ASR_S3_ACCESS_KEY_SECRET should be unset after rename, got %q", v)
	}
}

// TestUpdateASRS3ConfigEnvClearedToDefaultMigratesSecret 验证 access_key_env 从自定义清空回默认时,
// secret 迁移到默认 ASR_S3_ACCESS_KEY_SECRET,不留孤儿。
func TestUpdateASRS3ConfigEnvClearedToDefaultMigratesSecret(t *testing.T) {
	server := newTestServer(t)
	t.Cleanup(func() {
		os.Unsetenv("ASR_S3_ACCESS_KEY_SECRET")
		os.Unsetenv("CUSTOM_S3_KEY")
	})

	set := performRequest(server, http.MethodPut, "/api/config/asr-s3",
		`{"access_key_env":"CUSTOM_S3_KEY","access_key_secret":"sk-custom"}`)
	if set.Code != http.StatusOK {
		t.Fatalf("set custom env status = %d", set.Code)
	}

	clear := performRequest(server, http.MethodPut, "/api/config/asr-s3",
		`{"access_key_env":"","access_key_secret":"sk-migrated"}`)
	if clear.Code != http.StatusOK {
		t.Fatalf("clear env status = %d, body = %s", clear.Code, clear.Body.String())
	}
	got := decodeASRS3Response(t, clear.Body)
	if got.AccessKeyEnv != "ASR_S3_ACCESS_KEY_SECRET" {
		t.Fatalf("after clearing, access_key_env = %q, want ASR_S3_ACCESS_KEY_SECRET", got.AccessKeyEnv)
	}
	if !got.AccessKeySet {
		t.Fatalf("after clearing env with new secret, access_key_set should be true")
	}
	if v := os.Getenv("ASR_S3_ACCESS_KEY_SECRET"); v != "sk-migrated" {
		t.Fatalf("ASR_S3_ACCESS_KEY_SECRET = %q, want sk-migrated (migrated to default)", v)
	}
	if v := os.Getenv("CUSTOM_S3_KEY"); v != "" {
		t.Fatalf("CUSTOM_S3_KEY = %q, want empty (no orphan)", v)
	}
}
