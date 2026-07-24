package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"hikami-go/internal/config"
)

// TestExportBundleOmitsPlaintextSecrets 防回归：构造一个含明文密钥的内存 cfg，
// 按 handleExportConfig 的方式投影到 ConfigExportBundle，断言序列化后绝不包含
// WebDAV.Password / ASRS3.AccessKeySecret 的明文与字段名 —— 密钥统一走 Secrets 段。
// 这是 codex 审核指出的核心安全问题：直接嵌 config.WebDAVConfig / config.ASRS3Config
// 会被 encoding/json 序列化出明文密钥，故改用专用 DTO（WebDAVExportSection / ASRS3ExportSection）。
func TestExportBundleOmitsPlaintextSecrets(t *testing.T) {
	wd := config.WebDAVConfig{Remote: "r:remote", URL: "https://wd.example.com", Password: "WEBDAV_PLAINTEXT_SECRET"}
	s3 := config.ASRS3Config{
		Endpoint:        "https://oss.example.com",
		Bucket:          "bucket-x",
		AccessKeySecret: "S3_PLAINTEXT_SECRET",
	}

	bundle := ConfigExportBundle{
		Version: "1",
		WebDAV:  webdavToExport(wd),
		ASRS3:   asrs3ToExport(s3),
	}
	data, err := json.Marshal(bundle)
	if err != nil {
		t.Fatalf("marshal bundle: %v", err)
	}
	body := string(data)

	for _, leak := range []string{"WEBDAV_PLAINTEXT_SECRET", "S3_PLAINTEXT_SECRET"} {
		if strings.Contains(body, leak) {
			t.Errorf("导出 JSON 泄漏明文密钥 %q: %s", leak, body)
		}
	}
	// DTO 不投影这两个字段，故字段名也不应出现。
	if strings.Contains(strings.ToLower(body), "accesskeysecret") || strings.Contains(body, `"password"`) {
		t.Errorf("导出 JSON 含密钥字段名: %s", body)
	}

	// 反序列化确认结构：asr_s3 / webdav 段存在且只含非密钥字段。
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := m["asr_s3"]; !ok {
		t.Errorf("导出缺少 asr_s3 段")
	}
	if _, ok := m["webdav"]; !ok {
		t.Errorf("导出缺少 webdav 段")
	}
	var asrSection ASRS3ExportSection
	if err := json.Unmarshal(m["asr_s3"], &asrSection); err != nil {
		t.Fatalf("unmarshal asr_s3: %v", err)
	}
	if asrSection.Endpoint != "https://oss.example.com" || asrSection.Bucket != "bucket-x" {
		t.Errorf("asr_s3 非密钥字段未正确投影: %+v", asrSection)
	}
}

// TestExportBundleWebDAVAndASRS3AreOmittable 指针字段在为空时应整体省略（omitempty），
// 这样旧备份（缺该段）反序列化后指针为 nil，导入侧可据此判断「字段是否存在」。
func TestExportBundleWebDAVAndASRS3AreOmittable(t *testing.T) {
	bundle := ConfigExportBundle{Version: "1"} // WebDAV / ASRS3 均为 nil
	data, err := json.Marshal(bundle)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	body := string(data)
	if strings.Contains(body, "webdav") || strings.Contains(body, "asr_s3") {
		t.Errorf("空指针段未被 omitempty 省略: %s", body)
	}

	// 反序列化回来，指针应为 nil。
	var back ConfigExportBundle
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.WebDAV != nil || back.ASRS3 != nil {
		t.Errorf("缺失段反序列化后指针非 nil: WebDAV=%v ASRS3=%v", back.WebDAV, back.ASRS3)
	}
}

// TestImportConfigDoesNotWriteBackPlaintextSecrets 防回归：导入夹带明文密钥的恶意/旧备份时，
// 不能把明文写回内存 cfg（否则会绕过 updateASRS3Config/updateWebDAVConfig 的 tombstone 语义，
// 使 EffectiveAccessKey/EffectivePassword 回落明文）。密钥应只随 Secrets 段恢复。
func TestImportConfigDoesNotWriteBackPlaintextSecrets(t *testing.T) {
	server := newTestServer(t)

	// 构造一个 asr_s3 / webdav 段夹带明文密钥的 bundle。
	malicious := `{
		"version":"1",
		"webdav":{"remote":"r:remote","url":"https://wd.example.com","password":"LEAKED_WD_PASSWORD"},
		"asr_s3":{"endpoint":"https://oss2.example.com","bucket":"b2","access_key_secret":"LEAKED_S3_SECRET"}
	}`
	resp := performRequest(server, http.MethodPost, "/api/config/import?strategy=merge", malicious)
	if resp.Code != http.StatusOK {
		t.Fatalf("import status = %d, body = %s", resp.Code, resp.Body.String())
	}

	server.publishMu.RLock()
	defer server.publishMu.RUnlock()
	if server.cfg.WebDAV.Password == "LEAKED_WD_PASSWORD" {
		t.Errorf("导入把 WebDAV 明文密码写回内存 cfg，绕过 tombstone")
	}
	if server.cfg.ASRS3.AccessKeySecret == "LEAKED_S3_SECRET" {
		t.Errorf("导入把 ASR S3 明文密钥写回内存 cfg，绕过 tombstone")
	}
	// 非密钥字段应正常恢复。
	if server.cfg.ASRS3.Endpoint != "https://oss2.example.com" || server.cfg.ASRS3.Bucket != "b2" {
		t.Errorf("asr_s3 非密钥字段未恢复: endpoint=%q bucket=%q", server.cfg.ASRS3.Endpoint, server.cfg.ASRS3.Bucket)
	}
	if server.cfg.WebDAV.Remote != "r:remote" {
		t.Errorf("webdav 非密钥字段未恢复: remote=%q", server.cfg.WebDAV.Remote)
	}
}

// TestImportConfigMissingASRS3LeavesConfigUntouched 旧备份（无 asr_s3 段）导入不应清空现有配置。
func TestImportConfigMissingASRS3LeavesConfigUntouched(t *testing.T) {
	server := newTestServer(t)
	server.publishMu.Lock()
	server.cfg.ASRS3.Endpoint = "https://keep.example.com"
	server.cfg.ASRS3.Bucket = "keep-bucket"
	server.publishMu.Unlock()

	// 不含 asr_s3 / webdav 段的最小 bundle。
	minimal := `{"version":"1"}`
	resp := performRequest(server, http.MethodPost, "/api/config/import?strategy=merge", minimal)
	if resp.Code != http.StatusOK {
		t.Fatalf("import status = %d, body = %s", resp.Code, resp.Body.String())
	}

	server.publishMu.RLock()
	defer server.publishMu.RUnlock()
	if server.cfg.ASRS3.Endpoint != "https://keep.example.com" || server.cfg.ASRS3.Bucket != "keep-bucket" {
		t.Errorf("缺失 asr_s3 段的导入清空了现有配置: endpoint=%q bucket=%q",
			server.cfg.ASRS3.Endpoint, server.cfg.ASRS3.Bucket)
	}
}

// runtimeSettingsSection 查询 runtime_settings 表里某 section 的 data，不存在返回空串。
func runtimeSettingsSection(t *testing.T, server *Server, section string) string {
	t.Helper()
	var data string
	err := server.runtimeCfg.DB().QueryRowContext(
		context.Background(),
		"SELECT data FROM runtime_settings WHERE section = ?", section,
	).Scan(&data)
	if err == sql.ErrNoRows {
		return ""
	}
	if err != nil {
		t.Fatalf("query runtime_settings[%s]: %v", section, err)
	}
	return data
}

// TestImportConfigPersistsSectionsToDB 核心回归：导入带 publish/asr_s3/dashscope/archive 的 bundle 后，
// 这些配置段必须写进 runtime_settings 表（重启时由 ApplyOverrides 覆盖基线），不再只改内存。
func TestImportConfigPersistsSectionsToDB(t *testing.T) {
	server := newTestServer(t)

	bundle := `{
		"version":"1",
		"publish":{"Enabled":true,"Mode":"draft","CategoryID":21},
		"asr_s3":{"endpoint":"https://oss.db.example.com","bucket":"db-bkt","region":"cn-hangzhou"},
		"dashscope":{"Model":"paraformer-v2","Language":"zh"},
		"archive":{"AutoAfterPublish":true,"CleanupPolicy":"temp"}
	}`
	resp := performRequest(server, http.MethodPost, "/api/config/import?strategy=merge", bundle)
	if resp.Code != http.StatusOK {
		t.Fatalf("import status = %d, body = %s", resp.Code, resp.Body.String())
	}

	// 各 section 应在 runtime_settings 里有记录。
	for _, sec := range []string{"publish", "asr_s3", "dashscope", "archive"} {
		if got := runtimeSettingsSection(t, server, sec); got == "" {
			t.Errorf("section %s 未持久化到 runtime_settings", sec)
		} else if !json.Valid([]byte(got)) {
			t.Errorf("section %s 的 data 不是合法 JSON: %s", sec, got)
		}
	}

	// 验证 publish 段的实际值落盘：直接解析 runtime_settings 里的 publish JSON。
	// （不走 ApplyOverrides 是因为它末尾会 Validate，需要完整的有效 cfg，与「断言持久化值」无关。）
	publishData := runtimeSettingsSection(t, server, "publish")
	var publishSection config.PublishSectionDTO
	if err := json.Unmarshal([]byte(publishData), &publishSection); err != nil {
		t.Fatalf("unmarshal publish section: %v", err)
	}
	if publishSection.Mode == nil || *publishSection.Mode != "draft" {
		t.Errorf("publish 段持久化 Mode 不对: %+v", publishSection)
	}
	if publishSection.CategoryID == nil || *publishSection.CategoryID != 21 {
		t.Errorf("publish 段持久化 CategoryID 不对: %+v", publishSection)
	}

	// 内存 cfg 也应同步。
	server.publishMu.RLock()
	if server.cfg.Publish.Mode != "draft" {
		t.Errorf("内存 cfg.Publish.Mode 未更新: %q", server.cfg.Publish.Mode)
	}
	if server.cfg.ASRS3.Endpoint != "https://oss.db.example.com" {
		t.Errorf("内存 cfg.ASRS3.Endpoint 未更新: %q", server.cfg.ASRS3.Endpoint)
	}
	server.publishMu.RUnlock()

	// ConfigSectionsCount 应反映实际写入的段数（4 段）。
	var res importResult
	if err := json.Unmarshal(resp.Body.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal import result: %v", err)
	}
	if res.Details.ConfigSectionsCount != 4 {
		t.Errorf("ConfigSectionsCount = %d, 期望 4", res.Details.ConfigSectionsCount)
	}
}

// TestImportConfigRejectsInvalidValues 防回归（codex #4）：导入能写 runtime_settings 前必须校验，
// 否则非法值（如 publish.mode="column"）落盘后会导致下次启动 ApplyOverrides→Validate 失败、进程起不来。
// 校验失败应返回 400 且不落盘、不改内存。
func TestImportConfigRejectsInvalidValues(t *testing.T) {
	server := newTestServer(t)

	server.publishMu.Lock()
	server.cfg.Publish.Mode = "draft" // 基线合法值
	server.publishMu.Unlock()

	// publish.mode 非法（只允许 draft/publish/空）。
	invalid := `{"version":"1","publish":{"Mode":"column"}}`
	resp := performRequest(server, http.MethodPost, "/api/config/import?strategy=merge", invalid)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("期望非法值被拒返回 400, 实际 %d, body=%s", resp.Code, resp.Body.String())
	}

	// 内存 cfg 保持基线，未被污染。
	server.publishMu.RLock()
	if server.cfg.Publish.Mode != "draft" {
		t.Errorf("非法导入改了内存 Publish.Mode: %q (期望 draft)", server.cfg.Publish.Mode)
	}
	server.publishMu.RUnlock()

	// 没落盘到 runtime_settings。
	if got := runtimeSettingsSection(t, server, "publish"); got != "" {
		t.Errorf("非法值竟落盘到 runtime_settings: %s", got)
	}

	// archive.cleanup_policy 非法也应被拒。
	invalid2 := `{"version":"1","archive":{"CleanupPolicy":"bogus"}}`
	resp2 := performRequest(server, http.MethodPost, "/api/config/import?strategy=merge", invalid2)
	if resp2.Code != http.StatusBadRequest {
		t.Errorf("期望非法 archive.cleanup_policy 被拒返回 400, 实际 %d", resp2.Code)
	}
}

// TestImportConfigRejectsInvalidSectionValues 覆盖 codex 复审 #1：import 复用正式 handler 的段内校验。
// 各段非法值（URL 格式、provider 白名单、env 名、负数）应被拒返回 400，不落盘。
func TestImportConfigRejectsInvalidSectionValues(t *testing.T) {
	server := newTestServer(t)

	cases := []struct {
		name   string
		bundle string
	}{
		{"asr_s3 非法 endpoint", `{"version":"1","asr_s3":{"endpoint":"ftp://bad.example.com"}}`},
		{"asr_s3 endpoint 非 URL", `{"version":"1","asr_s3":{"endpoint":"not-a-url"}}`},
		{"asr_s3 非法 public_url_prefix", `{"version":"1","asr_s3":{"public_url_prefix":"no-scheme"}}`},
		{"asr_s3 非法 access_key_env", `{"version":"1","asr_s3":{"access_key_env":"BAD-NAME"}}`},
		{"dashscope 非法 asr_url", `{"version":"1","dashscope":{"ASRURL":"ftp://x"}}`},
		{"dashscope speaker_count 负数", `{"version":"1","dashscope":{"SpeakerCount":-1}}`},
		{"recap_ai 非法 provider", `{"version":"1","recap_ai":{"Provider":"bogus_provider"}}`},
		{"recap_ai max_tokens 负数", `{"version":"1","recap_ai":{"MaxTokens":-5}}`},
		{"recap_ai 非法 api_key_env", `{"version":"1","recap_ai":{"APIKeyEnv":"1INVALID"}}`},
		{"webdav 非法 url", `{"version":"1","webdav":{"url":"ftp://x"}}`},
		{"webdav base_path 含 ..", `{"version":"1","webdav":{"base_path":"../escape"}}`},
		{"webdav remote 无冒号", `{"version":"1","webdav":{"remote":"nocolon"}}`},
		{"publish private_pub 非法值", `{"version":"1","publish":{"PrivatePub":9}}`},
		{"publish original 非法值", `{"version":"1","publish":{"Original":5}}`},
		{"publish aigc 非法值", `{"version":"1","publish":{"Aigc":7}}`},
		{"publish close_comment 非法值", `{"version":"1","publish":{"CloseComment":3}}`},
		{"publish up_choose_comment 非法值", `{"version":"1","publish":{"UpChooseComment":8}}`},
		{"publish timer_pub_time 过去", `{"version":"1","publish":{"TimerPubTime":1000}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := performRequest(server, http.MethodPost, "/api/config/import?strategy=merge", tc.bundle)
			if resp.Code != http.StatusBadRequest {
				t.Errorf("%s: 期望 400, 实际 %d, body=%s", tc.name, resp.Code, resp.Body.String())
			}
		})
	}
}

// TestImportConfigTombstoneOnEnvRename 覆盖 codex 复审 #2：overwrite 导入 webdav 段把 password_env
// 从 OLD 改成 NEW，但 bundle.Secrets 只带 OLD 不带 NEW 时，managed 必须置 true（否则 EffectivePassword
// 回落 config.yaml 明文 = 没清干净）。校验计算用的是「导入后的 effective env」（NEW）。
func TestImportConfigTombstoneOnEnvRename(t *testing.T) {
	server := newTestServer(t)

	// 预置：webdav 用 OLD env，managed=false（模拟密钥走 yaml 明文的旧状态）。
	server.publishMu.Lock()
	server.cfg.WebDAV.PasswordEnv = "WD_OLD"
	server.cfg.WebDAV.SetPasswordManaged(false)
	server.publishMu.Unlock()

	// overwrite 导入：把 password_env 改成 WD_NEW，但 secrets 只带 WD_OLD（旧名），不带 WD_NEW。
	// 此时按新 effective env（WD_NEW）查 bundle.Secrets 找不到 → managed 必须置 true。
	bundle := `{
		"version":"1",
		"webdav":{"url":"https://wd.example.com","password_env":"WD_NEW"},
		"secrets":{"WD_OLD":"some-value"}
	}`
	resp := performRequest(server, http.MethodPost, "/api/config/import?strategy=overwrite", bundle)
	if resp.Code != http.StatusOK {
		t.Fatalf("import status = %d, body = %s", resp.Code, resp.Body.String())
	}

	server.publishMu.RLock()
	if !server.cfg.WebDAV.PasswordManaged() {
		t.Errorf("env 改名 OLD→NEW 且 bundle 无 NEW secret 时, managed 应为 true (实际 false)")
	}
	if server.cfg.WebDAV.PasswordEnv != "WD_NEW" {
		t.Errorf("PasswordEnv 未更新为 WD_NEW: %q", server.cfg.WebDAV.PasswordEnv)
	}
	server.publishMu.RUnlock()
}

// TestImportConfigRollsBackOnDBFailure 持久化失败时：返回 500、内存 cfg 不变、secrets 不变。
// 通过关闭底层 DB 让事务失败来触发回滚。
func TestImportConfigRollsBackOnDBFailure(t *testing.T) {
	server := newTestServer(t)

	// 记录导入前的基线值。
	server.publishMu.Lock()
	server.cfg.Publish.Mode = "baseline-mode"
	server.cfg.ASRS3.Endpoint = "baseline-endpoint"
	server.publishMu.Unlock()

	bundle := `{
		"version":"1",
		"publish":{"Mode":"draft"},
		"asr_s3":{"endpoint":"https://new.example.com","bucket":"new-bucket"}
	}`

	// 关闭底层 DB，使事务 begin/commit 失败 → 走回滚分支返回 500。
	db := server.runtimeCfg.DB()
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	resp := performRequest(server, http.MethodPost, "/api/config/import?strategy=merge", bundle)
	if resp.Code != http.StatusInternalServerError {
		t.Fatalf("期望持久化失败返回 500, 实际 %d, body=%s", resp.Code, resp.Body.String())
	}

	// 内存 cfg 应保持基线不变（未提交）。
	server.publishMu.RLock()
	if server.cfg.Publish.Mode != "baseline-mode" {
		t.Errorf("回滚后内存 Publish.Mode 被改: %q (期望 baseline-mode)", server.cfg.Publish.Mode)
	}
	if server.cfg.ASRS3.Endpoint != "baseline-endpoint" {
		t.Errorf("回滚后内存 ASRS3.Endpoint 被改: %q (期望 baseline-endpoint)", server.cfg.ASRS3.Endpoint)
	}
	server.publishMu.RUnlock()
}

// TestImportConfigOverwriteClearsSecretsAtomically overwrite 策略下 secrets 清理应进事务：
// 事务失败时旧 secrets 不丢。这里验证正常路径——overwrite 后旧 secret 被新 secret 替换。
func TestImportConfigOverwriteReplacesSecrets(t *testing.T) {
	server := newTestServer(t)
	ctx := context.Background()

	// 预置一个旧 secret。
	if err := server.secrets.Set(ctx, "OLD_KEY", "old-value"); err != nil {
		t.Fatalf("preset secret: %v", err)
	}

	// overwrite 导入：带新的 secret，不含 OLD_KEY。
	bundle := `{"version":"1","secrets":{"NEW_KEY":"new-value"}}`
	resp := performRequest(server, http.MethodPost, "/api/config/import?strategy=overwrite", bundle)
	if resp.Code != http.StatusOK {
		t.Fatalf("import status = %d, body = %s", resp.Code, resp.Body.String())
	}

	// OLD_KEY 应被清（overwrite + ClearTx），NEW_KEY 应存在。
	if old, _ := server.secrets.Get(ctx, "OLD_KEY"); old != "" {
		t.Errorf("overwrite 后旧 secret OLD_KEY 仍存在: %q", old)
	}
	if got, _ := server.secrets.Get(ctx, "NEW_KEY"); got != "new-value" {
		t.Errorf("overwrite 后新 secret NEW_KEY 未写入: %q", got)
	}
}

// TestImportConfigOldBundleCompatibility 旧格式备份兼容：recap_ai/publish 在旧备份里是值对象（非指针），
// 新代码用指针接收，反序列化后只要段在 JSON 里，指针就非 nil，导入应正常工作。
//
// 注意：RecapAIConfig/PublishConfig 只有 mapstructure tag 无 json tag，故 export 产物里这俩段
// 用 PascalCase 字段名（如 BaseURL/Mode），与 webdav/asr_s3 的 snake_case 不一致 —— 这是既有契约，
// 本测试用实际产物格式（PascalCase）断言。
func TestImportConfigOldBundleCompatibility(t *testing.T) {
	server := newTestServer(t)

	// 模拟旧格式（recap_ai/publish 值对象，字段名按 export 实际产物 PascalCase）。
	// publish.Mode 用合法值（draft），避免被持久化前校验拒绝。
	oldBundle := `{
		"version":"1",
		"recap_ai":{"BaseURL":"https://old.example.com","Model":"old-model"},
		"publish":{"Mode":"draft"}
	}`
	resp := performRequest(server, http.MethodPost, "/api/config/import?strategy=merge", oldBundle)
	if resp.Code != http.StatusOK {
		t.Fatalf("旧格式导入失败 status = %d, body = %s", resp.Code, resp.Body.String())
	}

	server.publishMu.RLock()
	if server.cfg.RecapAI.BaseURL != "https://old.example.com" {
		t.Errorf("旧格式 recap_ai 未导入: BaseURL=%q", server.cfg.RecapAI.BaseURL)
	}
	if server.cfg.Publish.Mode != "draft" {
		t.Errorf("旧格式 publish 未导入: Mode=%q", server.cfg.Publish.Mode)
	}
	server.publishMu.RUnlock()

	// 旧格式段也应持久化。
	if got := runtimeSettingsSection(t, server, "recap_ai"); got == "" {
		t.Errorf("旧格式 recap_ai 未持久化到 runtime_settings")
	}
}

// --- MCP 段导入导出(2026-07-23 新增) ---

// TestExportBundleOmitsMCPPlaintextSecrets 防回归:含明文密钥(Authorization 鉴权头、
// Brave/Tavily 明文 key)的 MCPConfig 经 mcpToExport 投影后,导出 JSON 绝不含明文,
// 密钥随 Secrets 段走(与 WebDAV/ASRS3 范式一致)。仿 TestExportBundleOmitsPlaintextSecrets。
func TestExportBundleOmitsMCPPlaintextSecrets(t *testing.T) {
	cfg := config.MCPConfig{
		Enabled: true,
		Servers: []config.MCPServerConfig{{
			Name:      "stepfun",
			Transport: "http",
			URL:       "https://mcp.example.com/sse",
			Enabled:   true,
			Headers: map[string]string{
				"Authorization": "Bearer STEPFUN_SECRET_TOKEN",
				"User-Agent":    "hikami/1.0", // 非鉴权头应留在投影里
			},
		}},
		Builtin: config.MCPBuiltinConfig{
			BraveAPIKey:  "BRAVE_PLAINTEXT_KEY",
			TavilyAPIKey: "TAVILY_PLAINTEXT_KEY",
		},
		MaxToolRounds: 7,
	}

	secrets := make(map[string]string)
	section := mcpToExport(cfg, secrets)

	// 只 marshal mcp 投影段(不含 secrets),断言明文密钥绝不进配置段。
	// 注意:不能 marshal 整个 bundle —— bundle.Secrets 按设计含明文(密钥的归宿)。
	data, err := json.Marshal(section)
	if err != nil {
		t.Fatalf("marshal mcp section: %v", err)
	}
	body := string(data)

	// 明文密钥绝不进 mcp 配置段 JSON。
	for _, leak := range []string{"STEPFUN_SECRET_TOKEN", "BRAVE_PLAINTEXT_KEY", "TAVILY_PLAINTEXT_KEY"} {
		if strings.Contains(body, leak) {
			t.Errorf("mcp 段 JSON 泄漏明文密钥 %q: %s", leak, body)
		}
	}
	// mcp 段的 servers[].headers 不应含 Authorization 键(无论大小写)。
	if strings.Contains(strings.ToLower(body), "authorization") {
		t.Errorf("mcp 段 JSON 的 servers headers 含 authorization: %s", body)
	}
	// 非鉴权头应保留在投影里。
	if !strings.Contains(body, "hikami/1.0") {
		t.Errorf("mcp 段 JSON 丢失非鉴权头 User-Agent: %s", body)
	}

	// 密钥应进 secrets map(密钥的归宿)。
	if got := secrets[mcpServerSecretKey(0, "stepfun")]; got != "Bearer STEPFUN_SECRET_TOKEN" {
		t.Errorf("鉴权头未进 secrets: key=%q got=%q", mcpServerSecretKey(0, "stepfun"), got)
	}
	if secrets["MCP_BRAVE_API_KEY"] != "BRAVE_PLAINTEXT_KEY" {
		t.Errorf("Brave key 未进 secrets: %v", secrets)
	}
	if secrets["MCP_TAVILY_API_KEY"] != "TAVILY_PLAINTEXT_KEY" {
		t.Errorf("Tavily key 未进 secrets: %v", secrets)
	}
}

// TestExportBundleMCPIsOmittable: MCP 为 nil 时整体省略(omitempty),旧备份缺段为 nil。
// 仿 TestExportBundleWebDAVAndASRS3AreOmittable。
func TestExportBundleMCPIsOmittable(t *testing.T) {
	bundle := ConfigExportBundle{Version: "1"} // MCP 为 nil
	data, err := json.Marshal(bundle)
	if err != nil {
		t.Fatalf("marshal bundle: %v", err)
	}
	body := string(data)
	if strings.Contains(body, "\"mcp\"") {
		t.Errorf("空 MCP 指针未被 omitempty 省略: %s", body)
	}

	var back ConfigExportBundle
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.MCP != nil {
		t.Errorf("缺失 MCP 段反序列化后指针非 nil: %v", back.MCP)
	}
}

// TestMCPExportImportRoundTrip 验证 mcpToExport → mcpFromExport 的完全可逆性(qoder
// Important#2):这是「密钥与配置段分离」设计的核心用户保证。任一字段映射错误都会在此暴露。
// 同时覆盖 qoder Important#1 的同名/归一化碰撞场景(双键防碰撞)。
func TestMCPExportImportRoundTrip(t *testing.T) {
	original := config.MCPConfig{
		Enabled: true,
		Servers: []config.MCPServerConfig{
			{
				Name:       "stepfun",
				Transport:  "http",
				URL:        "https://mcp.example.com/sse",
				Command:    "",
				Args:       []string{"--port", "8080"},
				Env:        []string{"FOO=bar"},
				Enabled:    true,
				TimeoutSec: 45,
				Headers: map[string]string{
					"Authorization": "Bearer STEPFUN_TOKEN",
					"X-Custom":      "keep-me",
				},
			},
			{
				// server[1] 无任何头(验证 nil headers 语义:导出不分配 map)。
				Name:       "local-stdio",
				Transport:  "stdio",
				Command:    "/usr/bin/mcp-server",
				Args:       nil,
				Enabled:    false,
				TimeoutSec: 0,
			},
		},
		Builtin: config.MCPBuiltinConfig{
			BraveAPIKey:     "BRAVE_KEY",
			TavilyAPIKey:    "TAVILY_KEY",
			BraveAPIKeyEnv:  "MY_BRAVE_ENV",
			TavilyAPIKeyEnv: "MY_TAVILY_ENV",
		},
		MaxToolRounds: 12,
	}

	// 1) 导出:密钥进 secrets,投影不含明文。
	secrets := make(map[string]string)
	exportDTO := mcpToExport(original, secrets)

	// 2) 模拟落盘往返:marshal export DTO → unmarshal 回新 DTO(验证 JSON tag 正确)。
	dtoBytes, err := json.Marshal(exportDTO)
	if err != nil {
		t.Fatalf("marshal export DTO: %v", err)
	}
	if strings.Contains(string(dtoBytes), "STEPFUN_TOKEN") || strings.Contains(string(dtoBytes), "BRAVE_KEY") {
		t.Fatalf("导出 DTO 含明文密钥: %s", dtoBytes)
	}
	var roundtrippedDTO MCPExportSection
	if err := json.Unmarshal(dtoBytes, &roundtrippedDTO); err != nil {
		t.Fatalf("unmarshal export DTO: %v", err)
	}

	// 3) 导入:从 DTO + secrets 还原,断言完全可逆。
	restored := mcpFromExport(&roundtrippedDTO, secrets)
	if !reflect.DeepEqual(restored, original) {
		t.Errorf("round-trip 不完全可逆:\n got = %#v\nwant = %#v", restored, original)
	}
}

// TestMCPExportImportRoundTrip_NameCollision 覆盖 qoder Important#1:两个 server name
// 归一化后相同(my-server / my_server)各自带不同 Authorization,round-trip 后应各自正确。
// 验证「下标+名」双键彻底防碰撞。
func TestMCPExportImportRoundTrip_NameCollision(t *testing.T) {
	original := config.MCPConfig{
		Enabled: true,
		Servers: []config.MCPServerConfig{
			{
				Name:      "my-server", // 归一化后 MCP_SERVER_0_MY_SERVER_AUTHORIZATION
				Transport: "http",
				URL:       "https://a.example.com",
				Headers:   map[string]string{"Authorization": "Bearer TOKEN_A"},
			},
			{
				Name:      "my_server", // 归一化后 MCP_SERVER_1_MY_SERVER_AUTHORIZATION(下标不同)
				Transport: "http",
				URL:       "https://b.example.com",
				Headers:   map[string]string{"Authorization": "Bearer TOKEN_B"},
			},
		},
	}

	secrets := make(map[string]string)
	exportDTO := mcpToExport(original, secrets)

	// 两个 secrets key 必须不同(下标区分),各含各的 token。
	keyA := mcpServerSecretKey(0, "my-server")
	keyB := mcpServerSecretKey(1, "my_server")
	if keyA == keyB {
		t.Fatalf("同名归一化碰撞: keyA==keyB==%q(应不同)", keyA)
	}
	if secrets[keyA] != "Bearer TOKEN_A" || secrets[keyB] != "Bearer TOKEN_B" {
		t.Errorf("碰撞场景下 token 回填错误: secrets=%v", secrets)
	}

	restored := mcpFromExport(exportDTO, secrets)
	if !reflect.DeepEqual(restored, original) {
		t.Errorf("碰撞场景 round-trip 不完全可逆:\n got = %#v\nwant = %#v", restored, original)
	}
	// 显式断言每个 server 的 Authorization 各自正确(防静默串台)。
	if restored.Servers[0].Headers["Authorization"] != "Bearer TOKEN_A" {
		t.Errorf("server[0] Authorization 串台: got %q", restored.Servers[0].Headers["Authorization"])
	}
	if restored.Servers[1].Headers["Authorization"] != "Bearer TOKEN_B" {
		t.Errorf("server[1] Authorization 串台: got %q", restored.Servers[1].Headers["Authorization"])
	}
}

// TestImportConfigPersistsMCPSection:含 mcp 段 + secrets 的 bundle 经 merge 导入后,
// runtime_settings 有 mcp section、内存 cfg.MCP 恢复(含从 secrets 回填的密钥)。
// 仿 TestImportConfigPersistsSectionsToDB。
func TestImportConfigPersistsMCPSection(t *testing.T) {
	server := newTestServer(t)

	// bundle 含 mcp 段(投影 DTO,不含明文密钥)+ secrets(含密钥)。
	bundle := `{
		"version":"1",
		"mcp":{
			"enabled":true,
			"servers":[
				{"name":"stepfun","transport":"http","url":"https://mcp.example.com","enabled":true,"timeout_sec":30,"headers":{"X-Trace":"keep"}}
			],
			"builtin":{"brave_api_key_env":"MY_BRAVE_ENV","tavily_api_key_env":""},
			"max_tool_rounds":8
		},
		"secrets":{
			"MCP_SERVER_0_STEPFUN_AUTHORIZATION":"Bearer STEPFUN_TOKEN",
			"MCP_BRAVE_API_KEY":"BRAVE_RESTORED"
		}
	}`
	resp := performRequest(server, http.MethodPost, "/api/config/import?strategy=merge", bundle)
	if resp.Code != http.StatusOK {
		t.Fatalf("import status = %d, body = %s", resp.Code, resp.Body.String())
	}

	// mcp section 应在 runtime_settings 里有记录。
	mcpData := runtimeSettingsSection(t, server, "mcp")
	if mcpData == "" {
		t.Fatalf("mcp section 未持久化到 runtime_settings")
	}
	if !json.Valid([]byte(mcpData)) {
		t.Errorf("mcp section 的 data 不是合法 JSON: %s", mcpData)
	}
	// 持久化的 MCPSectionDTO 应含预期字段(明文密钥在 DB 里是设计预期,
	// 与 PUT /api/config/mcp 一致;导出文件才是唯一需剔除明文的产物)。
	var mcpSection config.MCPSectionDTO
	if err := json.Unmarshal([]byte(mcpData), &mcpSection); err != nil {
		t.Fatalf("unmarshal mcp section: %v", err)
	}
	if mcpSection.Enabled == nil || !*mcpSection.Enabled {
		t.Errorf("mcp 段持久化 enabled 不对: %+v", mcpSection)
	}
	if mcpSection.MaxToolRounds == nil || *mcpSection.MaxToolRounds != 8 {
		t.Errorf("mcp 段持久化 max_tool_rounds 不对: %+v", mcpSection)
	}
	if mcpSection.Servers == nil || len(*mcpSection.Servers) != 1 {
		t.Errorf("mcp 段持久化 servers 不对: %+v", mcpSection)
	} else {
		sv := (*mcpSection.Servers)[0]
		// 从 secrets 回填的 Authorization 应在持久化数据里。
		if sv.Headers["Authorization"] != "Bearer STEPFUN_TOKEN" {
			t.Errorf("server Authorization 未从 secrets 回填: %+v", sv.Headers)
		}
		if sv.Headers["X-Trace"] != "keep" {
			t.Errorf("非鉴权头丢失: %+v", sv.Headers)
		}
	}

	// 内存 cfg.MCP 应同步恢复。
	server.publishMu.RLock()
	if !server.cfg.MCP.Enabled {
		t.Errorf("内存 cfg.MCP.Enabled 未更新")
	}
	if server.cfg.MCP.MaxToolRounds != 8 {
		t.Errorf("内存 cfg.MCP.MaxToolRounds 未更新: %d", server.cfg.MCP.MaxToolRounds)
	}
	if len(server.cfg.MCP.Servers) != 1 {
		t.Errorf("内存 cfg.MCP.Servers 未更新: %d", len(server.cfg.MCP.Servers))
	} else {
		if server.cfg.MCP.Servers[0].Headers["Authorization"] != "Bearer STEPFUN_TOKEN" {
			t.Errorf("内存 server Authorization 未恢复: %+v", server.cfg.MCP.Servers[0].Headers)
		}
	}
	if server.cfg.MCP.Builtin.BraveAPIKey != "BRAVE_RESTORED" {
		t.Errorf("内存 cfg.MCP.Builtin.BraveAPIKey 未恢复: %q", server.cfg.MCP.Builtin.BraveAPIKey)
	}
	if server.cfg.MCP.Builtin.BraveAPIKeyEnv != "MY_BRAVE_ENV" {
		t.Errorf("内存 cfg.MCP.Builtin.BraveAPIKeyEnv 未恢复: %q", server.cfg.MCP.Builtin.BraveAPIKeyEnv)
	}
	server.publishMu.RUnlock()

	// ConfigSectionsCount 应反映含 mcp(本 bundle 只导 mcp 段 = 1)。
	var res importResult
	if err := json.Unmarshal(resp.Body.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal import result: %v", err)
	}
	if res.Details.ConfigSectionsCount != 1 {
		t.Errorf("ConfigSectionsCount = %d, 期望 1(只导 mcp 段)", res.Details.ConfigSectionsCount)
	}
}

// TestImportConfigOldBundleLeavesMCPUntouched 钉死保护性副作用(分析文档第二节):
// 旧 bundle(无 mcp 段)导入后,内存 MCP 配置**不被破坏**(zero regression)。
func TestImportConfigOldBundleLeavesMCPUntouched(t *testing.T) {
	server := newTestServer(t)

	// 预设现有 MCP 配置(模拟生产已配置 MCP 的机器导入旧备份)。
	server.publishMu.Lock()
	server.cfg.MCP = config.MCPConfig{
		Enabled: true,
		Servers: []config.MCPServerConfig{{
			Name: "existing-server", Transport: "http", URL: "https://existing.example.com",
			Enabled: true, Headers: map[string]string{"Authorization": "Bearer EXISTING"},
		}},
		MaxToolRounds: 5,
	}
	server.publishMu.Unlock()

	// 旧 bundle:只有 publish,无 mcp 段。
	oldBundle := `{"version":"1","publish":{"Mode":"draft"}}`
	resp := performRequest(server, http.MethodPost, "/api/config/import?strategy=merge", oldBundle)
	if resp.Code != http.StatusOK {
		t.Fatalf("import status = %d, body = %s", resp.Code, resp.Body.String())
	}

	// 内存 MCP 配置应保持不变。
	server.publishMu.RLock()
	if !server.cfg.MCP.Enabled {
		t.Errorf("MCP.Enabled 被旧 bundle 破坏: false(应保持 true)")
	}
	if len(server.cfg.MCP.Servers) != 1 || server.cfg.MCP.Servers[0].Name != "existing-server" {
		t.Errorf("MCP.Servers 被旧 bundle 破坏: %+v", server.cfg.MCP.Servers)
	}
	if server.cfg.MCP.Servers[0].Headers["Authorization"] != "Bearer EXISTING" {
		t.Errorf("MCP server Authorization 被旧 bundle 破坏: %+v", server.cfg.MCP.Servers[0].Headers)
	}
	if server.cfg.MCP.MaxToolRounds != 5 {
		t.Errorf("MCP.MaxToolRounds 被旧 bundle 破坏: %d", server.cfg.MCP.MaxToolRounds)
	}
	server.publishMu.RUnlock()

	// runtime_settings 不应有 mcp section(旧 bundle 不碰 MCP)。
	if got := runtimeSettingsSection(t, server, "mcp"); got != "" {
		t.Errorf("旧 bundle 竟写入 mcp section(应零回归): %s", got)
	}

	// ConfigSectionsCount 应为 1(只 publish),不含 mcp。
	var res importResult
	if err := json.Unmarshal(resp.Body.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal import result: %v", err)
	}
	if res.Details.ConfigSectionsCount != 1 {
		t.Errorf("ConfigSectionsCount = %d, 期望 1(旧 bundle 只有 publish)", res.Details.ConfigSectionsCount)
	}
}
