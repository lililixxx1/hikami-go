package handler

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"mime/multipart"
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

// TestGetChannelByID 回归 bug 报告 #4:GET /api/channels/:id 之前未注册(404)。
// 验证单频道详情路由可返回 200 + 字段,不存在 id 返回 404。
func TestGetChannelByID(t *testing.T) {
	server := newTestServer(t)

	// 先创建一个频道
	createBody := `{"id":"huize","name":"灰泽满Hikami","uid":1298779265,"enabled":true}`
	create := performRequest(server, http.MethodPost, "/api/channels", createBody)
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", create.Code, create.Body.String())
	}

	// GET 单频道详情
	get := performRequest(server, http.MethodGet, "/api/channels/huize", "")
	if get.Code != http.StatusOK {
		t.Fatalf("get by id status = %d, body = %s", get.Code, get.Body.String())
	}
	if !strings.Contains(get.Body.String(), `"id":"huize"`) || !strings.Contains(get.Body.String(), `"uid":1298779265`) {
		t.Fatalf("get by id body missing fields: %s", get.Body.String())
	}

	// 不存在的 id → 404
	notFound := performRequest(server, http.MethodGet, "/api/channels/does_not_exist", "")
	if notFound.Code != http.StatusNotFound {
		t.Fatalf("get non-existent id expected 404, got %d", notFound.Code)
	}
}

// TestImportGlossaryJSONAcceptsArrayBody 回归 bug 报告 #1:
// 前端 importGlobalJSON 发裸数组 body,后端原只接受 GlossaryExport 对象 → 导入永久失败。
// 验证数组 body 被接受(200 + imported 计数),非法 JSON 返回 400(回归原 500)。
func TestImportGlossaryJSONAcceptsArrayBody(t *testing.T) {
	server := newTestServer(t)

	arrayBody := `[{"term":"AI","canonical":"人工智能"},{"term":"LLM","canonical":"大语言模型"}]`
	rec := performRequest(server, http.MethodPost, "/api/glossary/import/json", arrayBody)
	if rec.Code != http.StatusOK {
		t.Fatalf("array body import status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var result map[string]int
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal response: %v, body=%s", err, rec.Body.String())
	}
	if result["imported"] != 2 {
		t.Fatalf("expected imported=2, got %d", result["imported"])
	}

	// 对象格式仍兼容(ExportJSON 产出的形态)
	objectBody := `{"entries":[{"term":"GPU","canonical":"图形处理器"}],"note":""}`
	rec2 := performRequest(server, http.MethodPost, "/api/glossary/import/json", objectBody)
	if rec2.Code != http.StatusOK {
		t.Fatalf("object body import status = %d, body = %s", rec2.Code, rec2.Body.String())
	}
}

// TestImportGlossaryJSONInvalidReturns400 回归 bug 报告 #1 次要问题:
// 非 JSON 输入应返回 400(客户端错误),而非原走通用 writeError 的 500。
func TestImportGlossaryJSONInvalidReturns400(t *testing.T) {
	server := newTestServer(t)

	rec := performRequest(server, http.MethodPost, "/api/glossary/import/json", "not json at all")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid JSON expected 400, got %d (body=%s)", rec.Code, rec.Body.String())
	}
}

// TestPublishConfigRoundTripNormalizesZeroPrivatePub 回归 bug 报告 #2:
// 原先 GET 默认/未配置状态返回 private_pub=0,但 PUT 校验拒绝 0 → round-trip 失败。
// 修复:全局段 0 无"继承"语义(区别于频道级),PUT 把 0 规范化为 viper 默认 2(公开),
// 既保证 round-trip 幂等,又避免 publisher 收到 0 原样发给 B 站专栏 API。
func TestPublishConfigRoundTripNormalizesZeroPrivatePub(t *testing.T) {
	server := newTestServer(t)

	// 1. PUT 一个 private_pub=0 的 body → 应被规范化为 2 后接受(200)
	zeroBody := `{"private_pub":0}`
	putRec := performRequest(server, http.MethodPut, "/api/config/publish", zeroBody)
	if putRec.Code != http.StatusOK {
		t.Fatalf("PUT with private_pub=0 expected 200 (normalized to 2), got %d (body=%s)", putRec.Code, putRec.Body.String())
	}
	if !strings.Contains(putRec.Body.String(), `"private_pub":2`) {
		t.Fatalf("expected response private_pub=2 after normalization, got: %s", putRec.Body.String())
	}

	// 2. GET 现在 returns 2(规范化后持久化),round-trip 幂等
	getRec := performRequest(server, http.MethodGet, "/api/config/publish", "")
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET status = %d", getRec.Code)
	}
	if !strings.Contains(getRec.Body.String(), `"private_pub":2`) {
		t.Fatalf("expected GET to return private_pub=2 after normalization, got: %s", getRec.Body.String())
	}
	// 原样 PUT 回去必然通过
	putRec2 := performRequest(server, http.MethodPut, "/api/config/publish", getRec.Body.String())
	if putRec2.Code != http.StatusOK {
		t.Fatalf("round-trip PUT after normalization expected 200, got %d", putRec2.Code)
	}

	// 3. 合法值 1/2 直接接受
	for _, v := range []string{`{"private_pub":1}`, `{"private_pub":2}`} {
		rec := performRequest(server, http.MethodPut, "/api/config/publish", v)
		if rec.Code != http.StatusOK {
			t.Fatalf("PUT %s expected 200, got %d", v, rec.Code)
		}
	}

	// 4. 非法值(如 3)仍拒绝
	bad := performRequest(server, http.MethodPut, "/api/config/publish", `{"private_pub":3}`)
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("PUT private_pub=3 expected 400, got %d", bad.Code)
	}
}

// TestToolsConfigRoundTrip 验证 yt-dlp/rclone 路径的 GET/PUT 往返。
// PUT 后持久化到 runtime_settings,GET 回读一致;保存后 cfg.YTDLP/Rclone 已更新
// (refreshRuntimeStatus 会重新 Probe,这里只验证配置写入,不验证 Probe 副作用)。
func TestToolsConfigRoundTrip(t *testing.T) {
	server := newTestServer(t)

	// 1. GET 初始值(newTestServer 用 viper 默认,通常解析到 "yt-dlp"/"rclone" 命令名)
	getRec := performRequest(server, http.MethodGet, "/api/config/tools", "")
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET tools status = %d, body=%s", getRec.Code, getRec.Body.String())
	}

	// 2. PUT 自定义路径 → 200 + 持久化
	putBody := `{"yt_dlp":"/custom/yt-dlp","rclone":"/usr/local/bin/rclone"}`
	putRec := performRequest(server, http.MethodPut, "/api/config/tools", putBody)
	if putRec.Code != http.StatusOK {
		t.Fatalf("PUT tools status = %d, body=%s", putRec.Code, putRec.Body.String())
	}
	if !strings.Contains(putRec.Body.String(), `"yt_dlp":"/custom/yt-dlp"`) {
		t.Fatalf("expected PUT response yt_dlp=/custom/yt-dlp, got: %s", putRec.Body.String())
	}
	if !strings.Contains(putRec.Body.String(), `"rclone":"/usr/local/bin/rclone"`) {
		t.Fatalf("expected PUT response rclone=/usr/local/bin/rclone, got: %s", putRec.Body.String())
	}

	// 3. GET 回读一致(round-trip 幂等)
	getRec2 := performRequest(server, http.MethodGet, "/api/config/tools", "")
	if getRec2.Code != http.StatusOK {
		t.Fatalf("GET tools after PUT status = %d", getRec2.Code)
	}
	if !strings.Contains(getRec2.Body.String(), `"yt_dlp":"/custom/yt-dlp"`) {
		t.Fatalf("GET after PUT should reflect persisted yt_dlp, got: %s", getRec2.Body.String())
	}

	// 4. presence-aware:只传 yt_dlp,rclone 保持上次值(不被清空)
	putPartial := `{"yt_dlp":"/other/ytdlp"}`
	putRec3 := performRequest(server, http.MethodPut, "/api/config/tools", putPartial)
	if putRec3.Code != http.StatusOK {
		t.Fatalf("PUT tools partial status = %d", putRec3.Code)
	}
	if !strings.Contains(putRec3.Body.String(), `"rclone":"/usr/local/bin/rclone"`) {
		t.Fatalf("partial PUT should retain rclone, got: %s", putRec3.Body.String())
	}

	// 5. runtime_settings 表持久化了 tools section
	var section string
	err := server.runtimeCfg.DB().QueryRow("SELECT section FROM runtime_settings WHERE section='tools'").Scan(&section)
	if err != nil {
		t.Fatalf("tools section not persisted in runtime_settings: %v", err)
	}
}

// TestMCPConfigRoundTrip 验证 MCP 搜索工具配置的 GET/PUT 往返 + 密钥只写。
func TestMCPConfigRoundTrip(t *testing.T) {
	server := newTestServer(t)

	// 1. GET 初始(默认 mcp.enabled=false)
	getRec := performRequest(server, http.MethodGet, "/api/config/mcp", "")
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET mcp status = %d, body=%s", getRec.Code, getRec.Body.String())
	}
	if !strings.Contains(getRec.Body.String(), `"enabled":false`) {
		t.Fatalf("初始 mcp.enabled 应为 false, got: %s", getRec.Body.String())
	}

	// 2. PUT 开启 + 配置 server(含 headers) + 密钥
	putBody := `{"enabled":true,"max_tool_rounds":7,"servers":[{"name":"srv1","transport":"http","url":"http://localhost:9090","enabled":true,"headers":{"Authorization":"Bearer y"}}],"builtin":{"brave_api_key":"secret123"}}`
	putRec := performRequest(server, http.MethodPut, "/api/config/mcp", putBody)
	if putRec.Code != http.StatusOK {
		t.Fatalf("PUT mcp status = %d, body=%s", putRec.Code, putRec.Body.String())
	}
	// 3. 密钥只写:响应只含 brave_api_key_set,不含明文
	if !strings.Contains(putRec.Body.String(), `"brave_api_key_set":true`) {
		t.Fatalf("响应应含 brave_api_key_set:true, got: %s", putRec.Body.String())
	}
	if strings.Contains(putRec.Body.String(), "secret123") {
		t.Fatalf("密钥明文不应出现在响应中(只写模式), got: %s", putRec.Body.String())
	}
	// enabled 和 server 回读
	if !strings.Contains(putRec.Body.String(), `"enabled":true`) {
		t.Fatalf("响应应含 enabled:true, got: %s", putRec.Body.String())
	}
	if !strings.Contains(putRec.Body.String(), `"srv1"`) {
		t.Fatalf("响应应含 server srv1, got: %s", putRec.Body.String())
	}
	// headers 注入往返:响应应含提交的 Authorization 头
	if !strings.Contains(putRec.Body.String(), `"Authorization":"Bearer y"`) {
		t.Fatalf("响应应含 server headers, got: %s", putRec.Body.String())
	}

	// 4. GET 回读一致(round-trip)
	getRec2 := performRequest(server, http.MethodGet, "/api/config/mcp", "")
	if getRec2.Code != http.StatusOK {
		t.Fatalf("GET mcp after PUT status = %d", getRec2.Code)
	}
	if !strings.Contains(getRec2.Body.String(), `"brave_api_key_set":true`) {
		t.Fatalf("GET 回读应反映已设置, got: %s", getRec2.Body.String())
	}
	if !strings.Contains(getRec2.Body.String(), `"Authorization":"Bearer y"`) {
		t.Fatalf("GET 回读应含 server headers, got: %s", getRec2.Body.String())
	}

	// 5. runtime_settings 表持久化了 mcp section
	var mcpSection string
	err := server.runtimeCfg.DB().QueryRow("SELECT section FROM runtime_settings WHERE section='mcp'").Scan(&mcpSection)
	if err != nil {
		t.Fatalf("mcp section not persisted in runtime_settings: %v", err)
	}

	// 6. presence-aware:只传 enabled=false,max_tool_rounds 保持 7
	putPartial := `{"enabled":false}`
	putRec3 := performRequest(server, http.MethodPut, "/api/config/mcp", putPartial)
	if putRec3.Code != http.StatusOK {
		t.Fatalf("PUT mcp partial status = %d", putRec3.Code)
	}
	if !strings.Contains(putRec3.Body.String(), `"max_tool_rounds":7`) {
		t.Fatalf("partial PUT 应保留 max_tool_rounds=7, got: %s", putRec3.Body.String())
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

	// 预设只保留 DeepSeek 两个快捷选项，锁死精确集合与顺序，防止未来误加回多余预设。
	want := []RecapModelOption{
		{Value: "deepseek-v4-flash", Label: "deepseek-v4-flash（快速）", Group: "DeepSeek"},
		{Value: "deepseek-v4-pro", Label: "deepseek-v4-pro（默认）", Group: "DeepSeek"},
	}
	if len(result.Models) != len(want) {
		t.Fatalf("expected %d models, got %d (%+v)", len(want), len(result.Models), result.Models)
	}
	for i, m := range result.Models {
		if m != want[i] {
			t.Fatalf("models[%d] = %+v, want %+v", i, m, want[i])
		}
	}

	// 确认精简生效：已移除的厂商模型不再出现。
	byValue := make(map[string]RecapModelOption, len(result.Models))
	for _, m := range result.Models {
		byValue[m.Value] = m
	}
	for _, gone := range []string{"gpt-4o", "gpt-4o-mini", "qwen-plus", "qwen-turbo", "qwen-max", "claude-sonnet-4-20250514"} {
		if _, ok := byValue[gone]; ok {
			t.Fatalf("removed model %q still present in models list", gone)
		}
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

func TestListSessionsFilter(t *testing.T) {
	server := newTestServer(t)
	ctx := context.Background()

	// Seed 2 channels.
	for _, ch := range []channel.UpsertInput{
		{ID: "chan_a", Name: "alice", UID: 1001, LiveRoomID: 100, Enabled: true},
		{ID: "chan_b", Name: "bob", UID: 1002, LiveRoomID: 200, Enabled: true},
	} {
		if _, err := server.channels.Create(ctx, ch); err != nil {
			t.Fatalf("create channel %s: %v", ch.ID, err)
		}
	}

	// chan_a: one live_record "abc live", one download "xyz dl".
	if _, err := server.sessions.CreateLive(ctx, session.CreateLiveInput{
		ChannelID: "chan_a",
		Title:     "abc live",
		RoomID:    100,
		StartedAt: time.Date(2026, 7, 7, 10, 0, 0, 0, time.Local),
	}); err != nil {
		t.Fatalf("create live chan_a: %v", err)
	}
	if _, _, err := server.sessions.CreateDownload(ctx, session.CreateDownloadInput{
		ChannelID: "chan_a",
		SourceID:  "xyz_dl",
		Title:     "xyz dl",
		SourceURL: "https://example.com/xyz",
		StartedAt: time.Date(2026, 7, 7, 11, 0, 0, 0, time.Local),
	}); err != nil {
		t.Fatalf("create download chan_a: %v", err)
	}
	// chan_b: one live_record "abc live 2".
	if _, err := server.sessions.CreateLive(ctx, session.CreateLiveInput{
		ChannelID: "chan_b",
		Title:     "abc live 2",
		RoomID:    200,
		StartedAt: time.Date(2026, 7, 7, 12, 0, 0, 0, time.Local),
	}); err != nil {
		t.Fatalf("create live chan_b: %v", err)
	}

	cases := []struct {
		name    string
		query   string
		wantLen int
	}{
		{"no filter", "", 3},
		{"channel_id chan_a", "?channel_id=chan_a", 2},
		{"source live_record", "?source=live_record", 2},
		{"search abc", "?search=abc", 2},
		{"channel_id+source download", "?channel_id=chan_a&source=download", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := "/api/sessions" + tc.query
			resp := performRequest(server, http.MethodGet, path, "")
			if resp.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", resp.Code, resp.Body.String())
			}
			var got struct {
				Items []struct {
					ChannelName string `json:"channel_name"`
					Title       string `json:"title"`
				} `json:"items"`
			}
			if err := json.Unmarshal(resp.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode: %v, body=%s", err, resp.Body.String())
			}
			if len(got.Items) != tc.wantLen {
				t.Fatalf("len(items) = %d, want %d (body=%s)", len(got.Items), tc.wantLen, resp.Body.String())
			}
			// channel_name must still flow through with the filter applied.
			if len(got.Items) > 0 && got.Items[0].ChannelName == "" {
				t.Fatalf("items[0].ChannelName empty, expected channel join (body=%s)", resp.Body.String())
			}
		})
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

// TestStatsDashboardCostCalculation 验证 dashboard 成本趋势用正确的 ASR 单价（¥0.792/小时）
// 和 AI 单价（¥0.1/回顾）计算。创建一条 asr_done 状态、1 小时时长的 session + 一条 recap_done。
func TestStatsDashboardCostCalculation(t *testing.T) {
	server := newTestServer(t)
	ctx := context.Background()

	if _, err := server.channels.Create(ctx, channel.UpsertInput{
		ID: "test", Name: "Test", UID: 1, Enabled: true,
	}); err != nil {
		t.Fatalf("create channel: %v", err)
	}

	// 创建 live session（media_ready 状态，有 started_at）
	sess, err := server.sessions.CreateLive(ctx, session.CreateLiveInput{
		ChannelID: "test",
		Title:     "Cost Test",
		RoomID:    1,
		StartedAt: time.Date(2026, 7, 11, 12, 0, 0, 0, time.Local),
	})
	if err != nil {
		t.Fatalf("create live session: %v", err)
	}

	// 直接设 ended_at = 13:00（1 小时后）
	if err := server.sessions.UpdateEndedAt(ctx, sess.ID, time.Date(2026, 7, 11, 13, 0, 0, 0, time.Local)); err != nil {
		t.Fatalf("set ended_at: %v", err)
	}

	// 通过状态机把状态推进到 asr_done
	// discovered → download_started → downloading → normalize_succeeded → media_ready → asr_submitted → asr_done
	stateStore := state.NewStore(server.sessions.DB())
	if _, err := stateStore.Apply(ctx, sess.ID, state.EventDownloadStarted, "t1", ""); err != nil {
		t.Fatalf("apply download_started: %v", err)
	}
	if _, err := stateStore.Apply(ctx, sess.ID, state.EventNormalizeSucceeded, "t2", ""); err != nil {
		t.Fatalf("apply normalize_succeeded: %v", err)
	}
	if _, err := stateStore.Apply(ctx, sess.ID, state.EventASRSubmitted, "t3", ""); err != nil {
		t.Fatalf("apply asr_submitted: %v", err)
	}
	if _, err := stateStore.Apply(ctx, sess.ID, state.EventASRSucceeded, "t4", ""); err != nil {
		t.Fatalf("apply asr_succeeded: %v", err)
	}

	// 再创建一条 recap_done session，验证 AI 成本
	sess2, err := server.sessions.CreateLive(ctx, session.CreateLiveInput{
		ChannelID: "test",
		Title:     "Recap Test",
		RoomID:    2,
		StartedAt: time.Date(2026, 7, 11, 14, 0, 0, 0, time.Local),
	})
	if err != nil {
		t.Fatalf("create live session 2: %v", err)
	}
	if err := server.sessions.UpdateEndedAt(ctx, sess2.ID, time.Date(2026, 7, 11, 15, 0, 0, 0, time.Local)); err != nil {
		t.Fatalf("set ended_at 2: %v", err)
	}
	if _, err := stateStore.Apply(ctx, sess2.ID, state.EventDownloadStarted, "t5", ""); err != nil {
		t.Fatalf("apply download_started 2: %v", err)
	}
	if _, err := stateStore.Apply(ctx, sess2.ID, state.EventNormalizeSucceeded, "t6", ""); err != nil {
		t.Fatalf("apply normalize_succeeded 2: %v", err)
	}
	if _, err := stateStore.Apply(ctx, sess2.ID, state.EventASRSubmitted, "t7", ""); err != nil {
		t.Fatalf("apply asr_submitted 2: %v", err)
	}
	if _, err := stateStore.Apply(ctx, sess2.ID, state.EventASRSucceeded, "t8", ""); err != nil {
		t.Fatalf("apply asr_succeeded 2: %v", err)
	}
	if _, err := stateStore.Apply(ctx, sess2.ID, state.EventRecapSucceeded, "t9", ""); err != nil {
		t.Fatalf("apply recap_succeeded: %v", err)
	}

	// 请求 dashboard
	reqCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/stats/dashboard", nil).WithContext(reqCtx)
	rec := httptest.NewRecorder()
	server.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var data session.DashboardData
	if err := json.Unmarshal(rec.Body.Bytes(), &data); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(data.CostTrend) == 0 {
		t.Fatal("expected cost trend entries")
	}

	// 两条 session 都在同一个月，asr_hours = 2.0 (1h + 1h)
	// asr_cost = 2.0 * 0.792 = 1.584
	// recap_count = 1 (只有 sess2 到了 recap_done)
	// ai_cost = 1 * 0.1 = 0.1
	// total_cost = 1.584 + 0.1 = 1.684
	row := data.CostTrend[0]
	if math.Abs(row.ASRHours-2.0) > 0.01 {
		t.Errorf("asr_hours = %v, want ~2.0", row.ASRHours)
	}
	if math.Abs(row.ASRCost-1.584) > 0.01 {
		t.Errorf("asr_cost = %v, want ~1.584", row.ASRCost)
	}
	if math.Abs(row.AICost-0.1) > 0.001 {
		t.Errorf("ai_cost = %v, want ~0.1", row.AICost)
	}
	expectedTotal := row.ASRCost + row.AICost
	if math.Abs(row.TotalCost-expectedTotal) > 0.01 {
		t.Errorf("total_cost = %v, want ~%v", row.TotalCost, expectedTotal)
	}

	// 确认 JSON 中没有 recap_count 内部字段泄漏
	var raw map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}
	costTrend, ok := raw["cost_trend"].([]any)
	if !ok || len(costTrend) == 0 {
		t.Fatal("cost_trend missing or empty")
	}
	firstRow, ok := costTrend[0].(map[string]any)
	if !ok {
		t.Fatal("cost_trend[0] not an object")
	}
	if _, exists := firstRow["recap_count"]; exists {
		t.Error("recap_count leaked into JSON response")
	}
}

// TestStatsOverviewAndCostUseCorrectPrice 验证 /api/stats/overview 和 /api/stats/cost
// 使用正确的 ASR 单价（¥0.792/小时）而非旧的 ¥36/小时。
func TestStatsOverviewAndCostUseCorrectPrice(t *testing.T) {
	server := newTestServer(t)
	ctx := context.Background()

	if _, err := server.channels.Create(ctx, channel.UpsertInput{
		ID: "test", Name: "Test", UID: 1, Enabled: true,
	}); err != nil {
		t.Fatalf("create channel: %v", err)
	}

	// 创建一条 session
	if _, err := server.sessions.CreateLive(ctx, session.CreateLiveInput{
		ChannelID: "test",
		Title:     "Price Test",
		RoomID:    1,
		StartedAt: time.Date(2026, 7, 11, 12, 0, 0, 0, time.Local),
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	// /api/stats/overview: 1 session × 2h × 0.792 = 1.584
	reqCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/stats/overview", nil).WithContext(reqCtx)
	rec := httptest.NewRecorder()
	server.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("overview status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var overview map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &overview); err != nil {
		t.Fatalf("unmarshal overview: %v", err)
	}
	asrCostEst, ok := overview["asr_cost_estimate"].(float64)
	if !ok {
		t.Fatalf("asr_cost_estimate not float64: %T", overview["asr_cost_estimate"])
	}
	// 1 session × 2h × 0.792 = 1.584; 旧值会是 1 × 2 × 36 = 72
	if math.Abs(asrCostEst-1.584) > 0.01 {
		t.Errorf("overview asr_cost_estimate = %v, want ~1.584 (old wrong value would be 72)", asrCostEst)
	}

	// /api/stats/cost: 同样 1 session × 2h × 0.792 = 1.584
	req2 := httptest.NewRequest(http.MethodGet, "/api/stats/cost", nil).WithContext(reqCtx)
	rec2 := httptest.NewRecorder()
	server.Router().ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Fatalf("cost status = %d, body = %s", rec2.Code, rec2.Body.String())
	}

	var costResp map[string]any
	if err := json.Unmarshal(rec2.Body.Bytes(), &costResp); err != nil {
		t.Fatalf("unmarshal cost: %v", err)
	}
	asrCostEst2, ok := costResp["asr_cost_estimate"].(float64)
	if !ok {
		t.Fatalf("cost asr_cost_estimate not float64: %T", costResp["asr_cost_estimate"])
	}
	if math.Abs(asrCostEst2-1.584) > 0.01 {
		t.Errorf("cost asr_cost_estimate = %v, want ~1.584", asrCostEst2)
	}
	totalEst, ok := costResp["total_cost_estimate"].(float64)
	if !ok {
		t.Fatalf("total_cost_estimate not float64: %T", costResp["total_cost_estimate"])
	}
	// total = asr_cost + ai_cost = 1.584 + 0 (no recaps) = 1.584
	if math.Abs(totalEst-1.584) > 0.01 {
		t.Errorf("cost total_cost_estimate = %v, want ~1.584", totalEst)
	}
}

// TestRecapContentRoundTrip 验证 PUT recap/content 写入后 GET 能读到更新内容（slug 清洗一致性）。
// 使用含空格的 slug 验证 safeRecapName 在 GET 和 PUT 路径一致生效。
func TestRecapContentRoundTrip(t *testing.T) {
	server := newTestServer(t)

	if _, err := server.channels.Create(context.Background(), channel.UpsertInput{
		ID: "test", Name: "Test", UID: 1, Enabled: true,
	}); err != nil {
		t.Fatalf("create channel: %v", err)
	}

	sess, _, err := server.sessions.CreateDownload(context.Background(), session.CreateDownloadInput{
		ChannelID: "test",
		SourceID:  "BV1",
		Title:     "Test Replay",
	})
	if err != nil {
		t.Fatalf("create download session: %v", err)
	}

	// 直接在 DB 中设置含空格的 slug，模拟 sanitizeSlug 之前的异常历史数据。
	// safeRecapName 会把空格替换为下划线：GET 和 PUT 必须使用相同的清洗后文件名。
	if _, err := server.sessions.DB().Exec(
		`UPDATE sessions SET slug = 'test slug with space' WHERE id = ?`, sess.ID,
	); err != nil {
		t.Fatalf("update slug: %v", err)
	}

	// Mark local_available = true（PUT handler 检查 LocalAvailable）
	if err := server.sessions.SetLocalAvailable(context.Background(), sess.ID, true); err != nil {
		t.Fatalf("set local available: %v", err)
	}

	// Create recap directory to ensure file can be written
	recapDir := filepath.Join(server.cfg.OutputRoot, sess.ChannelID, "test slug with space", "recap")
	if err := os.MkdirAll(recapDir, 0o755); err != nil {
		t.Fatalf("mkdir recap dir: %v", err)
	}

	// PUT new content
	newContent := "# Updated Title\n\nThis is updated content."
	putResp := performRequest(server, http.MethodPut, "/api/sessions/"+sess.ID+"/recap/content",
		fmt.Sprintf(`{"content":%q}`, newContent))
	if putResp.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, body = %s", putResp.Code, putResp.Body.String())
	}

	// GET content back
	getResp := performRequest(server, http.MethodGet, "/api/sessions/"+sess.ID+"/recap", "")
	if getResp.Code != http.StatusOK {
		t.Fatalf("GET status = %d, body = %s", getResp.Code, getResp.Body.String())
	}

	var result map[string]any
	if err := json.Unmarshal(getResp.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if result["available"] != true {
		t.Fatalf("expected available=true, got %v", result["available"])
	}
	markdown, ok := result["markdown"].(string)
	if !ok {
		t.Fatalf("markdown not string: %T", result["markdown"])
	}
	if markdown != newContent {
		t.Errorf("markdown = %q, want %q (slug cleaning mismatch between GET and PUT?)", markdown, newContent)
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
	server, _ := newTestServerWithDB(t)
	return server
}

// newTestServerWithDB 返回 Server 与其底层 *sql.DB(供需要直接操作 DB 的测试用,
// 如 cookie account 插入)。2026-07-20 新增,供 listBiliSeries 测试注入 cookieAccounts。
func newTestServerWithDB(t *testing.T) (*Server, *sql.DB) {
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
	server := NewServer(
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
	return server, database
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

// ---------------------------------------------------------------------------
// 解耦改动测试 (2026-07-19): 下载/导入空 channel_id 兜底 + preview-by-url 新端点
// ---------------------------------------------------------------------------

// TestDownloadByURLNoChannelFallsBackToUnassigned:
// 回放页「下载」抽屉不选主播时,后端应自动挂到系统占位 channel _unassigned。
// 不再返回 400「channel_id is required」(2026-07-19 解耦)。
func TestDownloadByURLNoChannelFallsBackToUnassigned(t *testing.T) {
	server := newTestServer(t)
	// 配置 replay_download 能力为 true(handler 在 download-by-url 入口检查此能力)
	server.setRuntimeStatus(&runtime.Status{Capabilities: runtime.Capabilities{ReplayDownload: true}})
	// 关键:测试 DB 必须先有占位 channel,否则 FK 约束失败(Handler 兜底填 _unassigned 但 DB 里没记录)
	if err := server.channels.EnsureUnassigned(context.Background()); err != nil {
		t.Fatalf("ensure unassigned: %v", err)
	}

	body := strings.NewReader(`{"url":"https://www.bilibili.com/video/BV1xx"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/download-by-url", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	server.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202, body = %s", rec.Code, rec.Body.String())
	}

	var task worker.Task
	if err := json.Unmarshal(rec.Body.Bytes(), &task); err != nil {
		t.Fatalf("unmarshal task: %v", err)
	}
	if task.ChannelID != channel.UnassignedID {
		t.Errorf("task.ChannelID = %q, want %q (空 channel_id 应兜底到占位)", task.ChannelID, channel.UnassignedID)
	}
}

// TestImportSessionNoChannelFallsBackToUnassigned:
// 回放页「导入」抽屉不选主播时,后端应自动挂到系统占位 channel _unassigned。
// 仅 title 必填(原行为:channel_id+title 都必填,现在 channel_id 可空)。
func TestImportSessionNoChannelFallsBackToUnassigned(t *testing.T) {
	server := newTestServer(t)
	// 关键:测试 DB 必须先有占位 channel,否则 FK 约束失败(Handler 兜底填 _unassigned 但 DB 里没记录)
	if err := server.channels.EnsureUnassigned(context.Background()); err != nil {
		t.Fatalf("ensure unassigned: %v", err)
	}

	// multipart:不传 channel_id,只传 title + media_file(1 字节文件)
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	_ = writer.WriteField("title", "未分类测试")
	part, _ := writer.CreateFormFile("media_file", "test.m4a")
	_, _ = part.Write([]byte("x"))
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/sessions/import", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()
	server.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202, body = %s", rec.Code, rec.Body.String())
	}

	var task worker.Task
	if err := json.Unmarshal(rec.Body.Bytes(), &task); err != nil {
		t.Fatalf("unmarshal task: %v", err)
	}
	if task.ChannelID != channel.UnassignedID {
		t.Errorf("task.ChannelID = %q, want %q (不选主播应兜底到占位)", task.ChannelID, channel.UnassignedID)
	}
}

// TestImportSessionStillRejectsEmptyTitle:
// 解耦改动只放开 channel_id,title 仍必填。
func TestImportSessionStillRejectsEmptyTitle(t *testing.T) {
	server := newTestServer(t)
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	_ = writer.WriteField("channel_id", "") // 不选主播
	// 不传 title
	part, _ := writer.CreateFormFile("media_file", "test.m4a")
	_, _ = part.Write([]byte("x"))
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/sessions/import", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()
	server.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (title 仍必填), body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "title is required") {
		t.Errorf("body = %s, want contains 'title is required'", rec.Body.String())
	}
}

// TestDiscoverPreviewByURL:
// 新端点 POST /api/sessions/discover/preview-by-url 接受 JSON body {url, cookie_file?, title_prefix?},
// 返回 200 + {items: [DiscoverResult]},ChannelID 默认占位 _unassigned。
func TestDiscoverPreviewByURL(t *testing.T) {
	server := newTestServer(t)

	// 1. 缺 url → 400
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/discover/preview-by-url",
		strings.NewReader(`{"cookie_file":""}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	server.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("[empty url] status = %d, want 400, body = %s", rec.Code, rec.Body.String())
	}

	// 2. 正常请求 → 200 + items(fakeLister 返回 BV1)
	req = httptest.NewRequest(http.MethodPost, "/api/sessions/discover/preview-by-url",
		strings.NewReader(`{"url":"https://space.bilibili.com/1/lists/1","title_prefix":"【直播回放】"}`))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	server.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("[normal] status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Items []discover.Result `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Items) != 1 || resp.Items[0].SourceID != "BV1" {
		t.Fatalf("items = %+v, want only BV1 (fakeLister + title_prefix 匹配)", resp.Items)
	}
	if resp.Items[0].ChannelID != channel.UnassignedID {
		t.Errorf("items[0].ChannelID = %q, want %q (默认占位)", resp.Items[0].ChannelID, channel.UnassignedID)
	}
}

// TestListChannelsExcludesUnassigned:
// 主播管理页 GET /api/channels 不应返回占位 _unassigned(三重保险的第三重,避免误删/误改)。
func TestListChannelsExcludesUnassigned(t *testing.T) {
	server := newTestServer(t)
	// 先 ensure 占位存在(handler 路径走 _unassigned 时 Get 才能找到)
	if err := server.channels.EnsureUnassigned(context.Background()); err != nil {
		t.Fatalf("ensure unassigned: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/channels", nil)
	rec := httptest.NewRecorder()
	server.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var resp struct {
		Items []channel.Channel `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, c := range resp.Items {
		if c.ID == channel.UnassignedID {
			t.Errorf("GET /api/channels 返回了占位 channel _unassigned: %+v", c)
		}
	}
}

// ==================== listBiliSeries:per-channel publish account 选择(2026-07-20) ====================

// cookieCapturingRT 是测试用的 http.RoundTripper,截获请求并返回预设响应。
// server.go:listBiliSeries 的目标 URL 硬编码为 B 站 API,
// httptest.Server.Client() 不会自动接管,必须用 RoundTripper 直接注入 server.biliCreativeClient。
type cookieCapturingRT struct {
	gotCookieHeader string
	respBody        []byte
	respStatus      int
}

func (rt *cookieCapturingRT) RoundTrip(req *http.Request) (*http.Response, error) {
	rt.gotCookieHeader = req.Header.Get("Cookie")
	return &http.Response{
		StatusCode: rt.respStatus,
		Body:       io.NopCloser(bytes.NewReader(rt.respBody)),
		Header:     make(http.Header),
	}, nil
}

// newTestServerWithCookieAccounts 在 newTestServer 基础上注入 cookieAccounts store。
// 用于测试 listBiliSeries 等 cookie 账号依赖端点(codex r17c MEDIUM #5:
// newTestServer 第 22 参传 nil,本 helper 显式覆盖)。
// 返回 Server 与底层 DB(供测试直接插入 bili_cookie_accounts / channels 数据)。
func newTestServerWithCookieAccounts(t *testing.T, cookieDir string) (*Server, *sql.DB) {
	t.Helper()
	server, database := newTestServerWithDB(t)
	server.cookieAccounts = biliutil.NewCookieAccountStore(database, cookieDir)
	return server, database
}

// TestListBiliSeries_WithChannelID_UsesChannelPublishAccount:
// channel 有 publish_account_id → 请求 B 站时 Cookie 含该账号的 SESSDATA。
func TestListBiliSeries_WithChannelID_UsesChannelPublishAccount(t *testing.T) {
	ctx := context.Background()
	cookieDir := t.TempDir()
	server, database := newTestServerWithCookieAccounts(t, cookieDir)

	// 插入一个账号(非默认 is_default_publish=0),cookie 含全部 3 个必填字段
	accountPath := filepath.Join(cookieDir, "acct.txt")
	if err := os.WriteFile(accountPath, []byte("# Netscape\n"+
		".bilibili.com\tTRUE\t/\tTRUE\t9999999999\tSESSDATA\taccount-sess\n"+
		".bilibili.com\tTRUE\t/\tTRUE\t9999999999\tbili_jct\tcsrf-acct\n"+
		".bilibili.com\tTRUE\t/\tTRUE\t9999999999\tDedeUserID\t777\n"), 0600); err != nil {
		t.Fatalf("write account cookie: %v", err)
	}
	var accountID int64
	if err := database.QueryRowContext(ctx, `
		INSERT INTO bili_cookie_accounts (uid, nickname, cookie_file, is_default_download, is_default_publish, created_at, updated_at)
		VALUES (?, ?, ?, 0, 0, ?, ?)
		RETURNING id`,
		777, "acct", accountPath, time.Now().Format(time.RFC3339), time.Now().Format(time.RFC3339),
	).Scan(&accountID); err != nil {
		t.Fatalf("insert account: %v", err)
	}

	// 创建 channel 绑定该 publish_account_id
	if _, err := database.ExecContext(ctx, `
		INSERT INTO channels (id, name, uid, live_room_id, enabled, auto_record, auto_asr, auto_recap, record_danmaku,
			source_mode, discover_limit, publish_enabled, publish_mode, publish_category_id, publish_list_id,
			publish_private_pub, publish_original, auto_publish, publish_aigc, publish_timer_pub_time,
			publish_cover_url, publish_topics, publish_account_id, recap_model, max_continuations, created_at, updated_at)
		VALUES ('ch1','test',1,0,1,0,0,0,0,'both',0,0,'',0,-1,0,-1,0,-1,0,'','',NULL,'',-1,?,?)`,
		time.Now().Format(time.RFC3339), time.Now().Format(time.RFC3339)); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	if _, err := database.ExecContext(ctx, `UPDATE channels SET publish_account_id = ? WHERE id = 'ch1'`, accountID); err != nil {
		t.Fatalf("update channel: %v", err)
	}

	// 注入 RoundTripper 截获 Cookie(codex r17d:必须覆盖 biliCreativeClient)
	rt := &cookieCapturingRT{
		respBody:   []byte(`{"code":0,"message":"","data":{"lists":[]}}`),
		respStatus: 200,
	}
	server.biliCreativeClient = &http.Client{Transport: rt}

	// 调用端点
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/bili/series/list?channel_id=ch1", nil)
	server.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(rt.gotCookieHeader, "account-sess") {
		t.Errorf("Cookie header = %q, want contains %q (channel publish_account_id 应优先)", rt.gotCookieHeader, "account-sess")
	}
}

// TestListBiliSeries_WithChannelID_NoAccount_FallsBackToDefault:
// channel 无 publish_account_id → Cookie 含全局默认账号的 SESSDATA。
func TestListBiliSeries_WithChannelID_NoAccount_FallsBackToDefault(t *testing.T) {
	ctx := context.Background()
	cookieDir := t.TempDir()
	server, database := newTestServerWithCookieAccounts(t, cookieDir)

	// 插入默认发布账号(cookie 含全部 3 个必填字段)
	defaultPath := filepath.Join(cookieDir, "default.txt")
	if err := os.WriteFile(defaultPath, []byte("# Netscape\n"+
		".bilibili.com\tTRUE\t/\tTRUE\t9999999999\tSESSDATA\tdefault-sess\n"+
		".bilibili.com\tTRUE\t/\tTRUE\t9999999999\tbili_jct\tcsrf-default\n"+
		".bilibili.com\tTRUE\t/\tTRUE\t9999999999\tDedeUserID\t888\n"), 0600); err != nil {
		t.Fatalf("write default cookie: %v", err)
	}
	if _, err := database.ExecContext(ctx, `
		INSERT INTO bili_cookie_accounts (uid, nickname, cookie_file, is_default_download, is_default_publish, created_at, updated_at)
		VALUES (?, ?, ?, 0, 1, ?, ?)`,
		888, "default", defaultPath, time.Now().Format(time.RFC3339), time.Now().Format(time.RFC3339)); err != nil {
		t.Fatalf("insert default: %v", err)
	}

	// 创建 channel 无 publish_account_id(回退全局默认)
	if _, err := database.ExecContext(ctx, `
		INSERT INTO channels (id, name, uid, live_room_id, enabled, auto_record, auto_asr, auto_recap, record_danmaku,
			source_mode, discover_limit, publish_enabled, publish_mode, publish_category_id, publish_list_id,
			publish_private_pub, publish_original, auto_publish, publish_aigc, publish_timer_pub_time,
			publish_cover_url, publish_topics, publish_account_id, recap_model, max_continuations, created_at, updated_at)
		VALUES ('ch1','test',1,0,1,0,0,0,0,'both',0,0,'',0,-1,0,-1,0,-1,0,'','',NULL,'',-1,?,?)`,
		time.Now().Format(time.RFC3339), time.Now().Format(time.RFC3339)); err != nil {
		t.Fatalf("insert channel: %v", err)
	}

	rt := &cookieCapturingRT{
		respBody:   []byte(`{"code":0,"message":"","data":{"lists":[]}}`),
		respStatus: 200,
	}
	server.biliCreativeClient = &http.Client{Transport: rt}

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/bili/series/list?channel_id=ch1", nil)
	server.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(rt.gotCookieHeader, "default-sess") {
		t.Errorf("Cookie header = %q, want contains %q (无 channel account 应回退默认账号)", rt.gotCookieHeader, "default-sess")
	}
}

// TestListBiliSeries_WithoutChannelID_UnchangedBehavior:
// 不传 channel_id → 完全等价旧行为(用全局默认账号),回归保护。
func TestListBiliSeries_WithoutChannelID_UnchangedBehavior(t *testing.T) {
	ctx := context.Background()
	cookieDir := t.TempDir()
	server, database := newTestServerWithCookieAccounts(t, cookieDir)

	// 仅插入默认账号,不插任何 channel(也不传 channel_id query)
	defaultPath := filepath.Join(cookieDir, "default.txt")
	if err := os.WriteFile(defaultPath, []byte("# Netscape\n"+
		".bilibili.com\tTRUE\t/\tTRUE\t9999999999\tSESSDATA\tdefault-sess\n"+
		".bilibili.com\tTRUE\t/\tTRUE\t9999999999\tbili_jct\tcsrf-default\n"+
		".bilibili.com\tTRUE\t/\tTRUE\t9999999999\tDedeUserID\t999\n"), 0600); err != nil {
		t.Fatalf("write default cookie: %v", err)
	}
	if _, err := database.ExecContext(ctx, `
		INSERT INTO bili_cookie_accounts (uid, nickname, cookie_file, is_default_download, is_default_publish, created_at, updated_at)
		VALUES (?, ?, ?, 0, 1, ?, ?)`,
		999, "default", defaultPath, time.Now().Format(time.RFC3339), time.Now().Format(time.RFC3339)); err != nil {
		t.Fatalf("insert default: %v", err)
	}

	rt := &cookieCapturingRT{
		respBody:   []byte(`{"code":0,"message":"","data":{"lists":[{"id":42,"name":"test","articles_count":3}]}}`),
		respStatus: 200,
	}
	server.biliCreativeClient = &http.Client{Transport: rt}

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/bili/series/list", nil)
	server.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(rt.gotCookieHeader, "default-sess") {
		t.Errorf("Cookie header = %q, want contains %q (无 channel_id 应走默认账号)", rt.gotCookieHeader, "default-sess")
	}
	if !strings.Contains(w.Body.String(), `"id":42`) {
		t.Errorf("response body = %s, want contains 文集 ID 42", w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// v6 新增:resetSession 端点测试(修复 2026-07-20 BUG #2)
// ---------------------------------------------------------------------------

// TestResetSession_Success 验证 ASR 失败场次 reset 成功,返回 SessionDetail。
func TestResetSession_Success(t *testing.T) {
	server, database := newTestServerWithDB(t)
	ctx := context.Background()

	// 插入测试数据
	_, err := database.Exec(`INSERT INTO channels (id, name, uid, enabled) VALUES ('test_ch', 'Test', 1, 1)`)
	if err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	_, err = database.Exec(`
		INSERT INTO sessions (id, slug, channel_id, source_type, source_id, title, source_url, status, current_task_id, local_available)
		VALUES ('sess_reset', 'reset_slug', 'test_ch', 'live_record', 'src_1', 'Reset Test', '', 'failed', 'task_asr_1', 1)
	`)
	if err != nil {
		t.Fatalf("insert session: %v", err)
	}
	_, err = database.Exec(`
		INSERT INTO tasks (id, channel_id, session_id, type, status, attempt, payload, progress, message, created_at, updated_at)
		VALUES ('task_asr_1', 'test_ch', 'sess_reset', 'asr', 'failed', 1, '{}', 0, '', '2026-07-20T00:00:00+08:00', '2026-07-20T00:00:00+08:00')
	`)
	if err != nil {
		t.Fatalf("insert task: %v", err)
	}

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/sessions/sess_reset/reset", nil)
	server.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}

	// 验证返回 SessionDetail 结构
	var resp struct {
		Session map[string]any `json:"session"`
		Files   []any          `json:"files"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Session == nil {
		t.Fatal("session field is nil, want non-nil")
	}
	if resp.Session["status"] != "media_ready" {
		t.Errorf("session.status = %v, want media_ready", resp.Session["status"])
	}

	// 验证 DB session 状态已更新
	var status string
	err = database.QueryRowContext(ctx, `SELECT status FROM sessions WHERE id = 'sess_reset'`).Scan(&status)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if status != "media_ready" {
		t.Errorf("DB status = %q, want media_ready", status)
	}
}

// TestResetSession_NotFound 验证 404。
func TestResetSession_NotFound(t *testing.T) {
	server, _ := newTestServerWithDB(t)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/sessions/nonexistent/reset", nil)
	server.router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body = %s", w.Code, w.Body.String())
	}
}

// TestResetSession_StatusNotFailed 验证非 failed 状态 → 409。
func TestResetSession_StatusNotFailed(t *testing.T) {
	server, database := newTestServerWithDB(t)

	_, err := database.Exec(`INSERT INTO channels (id, name, uid, enabled) VALUES ('test_ch', 'Test', 1, 1)`)
	if err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	// status = media_ready(不是 failed)
	_, err = database.Exec(`
		INSERT INTO sessions (id, slug, channel_id, source_type, source_id, title, status, local_available)
		VALUES ('sess_media', 'slug', 'test_ch', 'live_record', 'src', 'Test', 'media_ready', 1)
	`)
	if err != nil {
		t.Fatalf("insert session: %v", err)
	}

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/sessions/sess_media/reset", nil)
	server.router.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body = %s", w.Code, w.Body.String())
	}
}

// TestResetSession_NonASRFailure 验证非 ASR 任务失败 → 409。
func TestResetSession_NonASRFailure(t *testing.T) {
	server, database := newTestServerWithDB(t)

	_, err := database.Exec(`INSERT INTO channels (id, name, uid, enabled) VALUES ('test_ch', 'Test', 1, 1)`)
	if err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	_, err = database.Exec(`
		INSERT INTO sessions (id, slug, channel_id, source_type, source_id, title, status, current_task_id, local_available)
		VALUES ('sess_recap', 'slug', 'test_ch', 'live_record', 'src', 'Test', 'failed', 'task_recap_1', 1)
	`)
	if err != nil {
		t.Fatalf("insert session: %v", err)
	}
	// task 类型 recap(非 asr)
	_, err = database.Exec(`
		INSERT INTO tasks (id, channel_id, session_id, type, status, attempt, payload, progress, message, created_at, updated_at)
		VALUES ('task_recap_1', 'test_ch', 'sess_recap', 'recap', 'failed', 1, '{}', 0, '', '2026-07-20T00:00:00+08:00', '2026-07-20T00:00:00+08:00')
	`)
	if err != nil {
		t.Fatalf("insert task: %v", err)
	}

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/sessions/sess_recap/reset", nil)
	server.router.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body = %s", w.Code, w.Body.String())
	}
}
