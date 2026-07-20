package handler

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"hikami-go/internal/biliutil"
	"hikami-go/internal/channel"
	"hikami-go/internal/config"
	"hikami-go/internal/glossary"
	"hikami-go/internal/recap"
	"hikami-go/internal/runtimeconfig"

	"github.com/gin-gonic/gin"
)

// ConfigExportBundle is the top-level JSON structure for full config export.
//
// 全部 6 个全局配置段（recap_ai/publish/webdav/asr_s3/dashscope/archive）均以指针形式参与
// 备份，缺失段反序列化后为 nil，导入侧据此用「段是否存在」判断是否覆盖（统一 presence 语义）。
//
// WebDAV / ASR S3 使用专用 DTO（WebDAVExportSection / ASRS3ExportSection）而非直接嵌入
// config 包的结构体，原因是后者含明文密钥字段（WebDAVConfig.Password、ASRS3Config.AccessKeySecret），
// 会被 encoding/json 直接序列化进导出文件，违背项目「密钥字段不进配置 DTO，统一走 secrets 表」的设计
// （见 internal/config/config.go 中 ASRS3SectionDTO 的注释）。这里只导出非密钥字段。
// dashscope/archive 不含明文密钥，直接嵌 config 结构体。
type ConfigExportBundle struct {
	Version      string                  `json:"version"`
	ExportedAt   string                  `json:"exported_at"`
	RecapAI      *config.RecapAIConfig   `json:"recap_ai,omitempty"`
	Publish      *config.PublishConfig   `json:"publish,omitempty"`
	WebDAV       *WebDAVExportSection    `json:"webdav,omitempty"`
	ASRS3        *ASRS3ExportSection     `json:"asr_s3,omitempty"`
	DashScope    *config.DashScopeConfig `json:"dashscope,omitempty"`
	Archive      *config.ArchiveConfig   `json:"archive,omitempty"`
	Secrets      map[string]string       `json:"secrets"`
	Channels     []channel.UpsertInput   `json:"channels"`
	Glossary     GlossaryExportSection   `json:"glossary"`
	Templates    TemplateExportSection   `json:"templates"`
	BiliAccounts []BiliAccountExportItem `json:"bili_accounts"`
}

// WebDAVExportSection 是 WebDAV 配置的导出投影：剔除 Password 明文，密钥随 Secrets 段走。
type WebDAVExportSection struct {
	Remote      string `json:"remote"`
	BasePath    string `json:"base_path"`
	URL         string `json:"url"`
	Username    string `json:"username"`
	PasswordEnv string `json:"password_env"`
}

func webdavToExport(c config.WebDAVConfig) *WebDAVExportSection {
	return &WebDAVExportSection{
		Remote:      c.Remote,
		BasePath:    c.BasePath,
		URL:         c.URL,
		Username:    c.Username,
		PasswordEnv: c.PasswordEnv,
	}
}

// ASRS3ExportSection 是 ASR S3（对象存储）配置的导出投影：剔除 AccessKeySecret 明文，
// 密钥随 Secrets 段走。
type ASRS3ExportSection struct {
	Endpoint        string `json:"endpoint"`
	Bucket          string `json:"bucket"`
	AccessKeyID     string `json:"access_key_id"`
	AccessKeyEnv    string `json:"access_key_env"`
	Region          string `json:"region"`
	PublicURLPrefix string `json:"public_url_prefix"`
	UsePathStyle    bool   `json:"use_path_style"`
}

func asrs3ToExport(c config.ASRS3Config) *ASRS3ExportSection {
	return &ASRS3ExportSection{
		Endpoint:        c.Endpoint,
		Bucket:          c.Bucket,
		AccessKeyID:     c.AccessKeyID,
		AccessKeyEnv:    c.AccessKeyEnv,
		Region:          c.Region,
		PublicURLPrefix: c.PublicURLPrefix,
		UsePathStyle:    c.UsePathStyle,
	}
}

type GlossaryExportSection struct {
	Global  *glossary.GlossaryExport            `json:"global,omitempty"`
	Channel map[string]*glossary.GlossaryExport `json:"channels,omitempty"`
}

type TemplateExportSection struct {
	Global  *recap.TemplateExport            `json:"global,omitempty"`
	Channel map[string]*recap.TemplateExport `json:"channels,omitempty"`
}

type BiliAccountExportItem struct {
	UID               int64  `json:"uid"`
	Nickname          string `json:"nickname"`
	IsDefaultDownload bool   `json:"is_default_download"`
	IsDefaultPublish  bool   `json:"is_default_publish"`
}

type importResult struct {
	Imported bool     `json:"imported"`
	Strategy string   `json:"strategy"`
	Warnings []string `json:"warnings,omitempty"`
	Details  struct {
		SecretsCount        int `json:"secrets_count"`
		ChannelsCount       int `json:"channels_count"`
		GlossaryCount       int `json:"glossary_count"`
		TemplatesCount      int `json:"templates_count"`
		BiliAccountsCount   int `json:"bili_accounts_count"`
		ConfigSectionsCount int `json:"config_sections_count"`
	} `json:"details"`
}

func (s *Server) handleExportConfig(ctx *gin.Context) {
	bundle := ConfigExportBundle{
		Version:    "1",
		ExportedAt: time.Now().UTC().Format(time.RFC3339),
		Secrets:    make(map[string]string),
	}

	// In-memory config（指针投影；dashscope/archive 无密钥直接取地址）。
	// 注意：局部变量名避开 recap（会遮蔽 internal/recap 包导入，导致 recap.TemplateExport 报 "not a type"）。
	s.publishMu.RLock()
	recapAI := s.cfg.RecapAI
	publish := s.cfg.Publish
	dashscope := s.cfg.DashScope
	archive := s.cfg.Archive
	bundle.RecapAI = &recapAI
	bundle.Publish = &publish
	bundle.DashScope = &dashscope
	bundle.Archive = &archive
	bundle.WebDAV = webdavToExport(s.cfg.WebDAV)
	bundle.ASRS3 = asrs3ToExport(s.cfg.ASRS3)
	s.publishMu.RUnlock()

	// Secrets (actual values)
	secretList, err := s.secrets.List(ctx.Request.Context())
	if err != nil {
		writeError(ctx, err)
		return
	}
	for _, sec := range secretList {
		if sec.Value != "" {
			bundle.Secrets[sec.Key] = sec.Value
		}
	}

	// Channels
	chList, err := s.channels.ListVisible(ctx.Request.Context())
	if err != nil {
		writeError(ctx, err)
		return
	}
	bundle.Channels = make([]channel.UpsertInput, 0, len(chList))
	for _, ch := range chList {
		bundle.Channels = append(bundle.Channels, channelToUpsertInput(ch))
	}

	// Glossary: global + per-channel
	glossaryGlobal, err := s.glossary.ExportJSON(ctx.Request.Context(), "")
	if err == nil && glossaryGlobal != nil {
		var ge glossary.GlossaryExport
		if json.Unmarshal(glossaryGlobal, &ge) == nil {
			bundle.Glossary.Global = &ge
		}
	}
	if len(chList) > 0 {
		bundle.Glossary.Channel = make(map[string]*glossary.GlossaryExport)
		for _, ch := range chList {
			data, err := s.glossary.ExportJSON(ctx.Request.Context(), ch.ID)
			if err != nil || data == nil {
				continue
			}
			var ge glossary.GlossaryExport
			if json.Unmarshal(data, &ge) == nil && len(ge.Entries) > 0 {
				bundle.Glossary.Channel[ch.ID] = &ge
			}
		}
	}

	// Templates: global + per-channel
	tplGlobal, err := s.recapTemplates.ExportJSON(ctx.Request.Context(), "")
	if err == nil && tplGlobal != nil {
		var te recap.TemplateExport
		if json.Unmarshal(tplGlobal, &te) == nil {
			bundle.Templates.Global = &te
		}
	}
	if len(chList) > 0 {
		bundle.Templates.Channel = make(map[string]*recap.TemplateExport)
		for _, ch := range chList {
			data, err := s.recapTemplates.ExportJSON(ctx.Request.Context(), ch.ID)
			if err != nil || data == nil {
				continue
			}
			var te recap.TemplateExport
			if json.Unmarshal(data, &te) == nil && len(te.Templates) > 0 {
				bundle.Templates.Channel[ch.ID] = &te
			}
		}
	}

	// Bili accounts (metadata only, no cookie files)
	accounts, err := s.cookieAccounts.List(ctx.Request.Context())
	if err != nil {
		writeError(ctx, err)
		return
	}
	bundle.BiliAccounts = make([]BiliAccountExportItem, 0, len(accounts))
	for _, a := range accounts {
		bundle.BiliAccounts = append(bundle.BiliAccounts, BiliAccountExportItem{
			UID:               a.UID,
			Nickname:          a.Nickname,
			IsDefaultDownload: a.IsDefaultDownload,
			IsDefaultPublish:  a.IsDefaultPublish,
		})
	}

	data, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		writeError(ctx, err)
		return
	}
	ctx.Header("Content-Type", "application/json")
	ctx.Header("Content-Disposition", `attachment; filename="hikami-config-export.json"`)
	ctx.Data(http.StatusOK, "application/json", data)
}

func (s *Server) handleImportConfig(ctx *gin.Context) {
	strategy := ctx.DefaultQuery("strategy", "merge")
	if strategy != "merge" && strategy != "overwrite" {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "strategy must be 'merge' or 'overwrite'"})
		return
	}

	body, err := io.ReadAll(io.LimitReader(ctx.Request.Body, 10<<20))
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "failed to read request body"})
		return
	}

	var bundle ConfigExportBundle
	if err := json.Unmarshal(body, &bundle); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON: " + err.Error()})
		return
	}
	if bundle.Version != "1" {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "unsupported export version: " + bundle.Version})
		return
	}

	result := importResult{Imported: true, Strategy: strategy}
	cctx := ctx.Request.Context()

	// === 阶段一：核心数据（全局配置段 + secrets）原子持久化 ===
	// 设计依据 codex 审核：
	//   1. 配置段 + secrets 放进同一 WithTx 事务，任一失败回滚、返回 500、内存 cfg 与进程 env 不变。
	//   2. overwrite 的 secrets 清理用 ClearTx 进事务（不再放事务外，避免 Clear 成功事务失败→密钥全丢）。
	//   3. 全 6 段用指针 != nil 判断「段是否存在」，统一 presence 语义（兼容旧备份缺段）。
	//   4. 先在内存算 next cfg + 构造各段 DTO → 事务内 persistSectionTx×N + secrets ClearTx/SetTx
	//      → commit 成功后才提交内存 cfg + 进程 env（与正式 update handler 的顺序范式一致）。
	//   5. WebDAV/ASR S3 的 managed tombstone：overwrite 清了 secrets 但 bundle 无对应 env key 时
	//      置 managed=true，防止 Effective* 回落 config.yaml 明文（等于 overwrite 没清干净）。
	//   6. overwrite 的非配置清理（glossary/templates/cookies）推迟到核心事务成功之后（阶段二），
	//      避免核心持久化失败时这些数据已被清。

	// 收集旧 secrets keys（用于 commit 后清理进程 env），必须在事务前读。
	oldSecretKeys := map[string]struct{}{}
	if oldList, err := s.secrets.List(cctx); err == nil {
		for _, sc := range oldList {
			oldSecretKeys[sc.Key] = struct{}{}
		}
	}

	// 计算各段 next 状态（基于 bundle 回填非密钥字段），并构造持久化 DTO。
	s.publishMu.Lock()
	nextRecap := s.cfg.RecapAI
	nextPublish := s.cfg.Publish
	nextDashscope := s.cfg.DashScope
	nextArchive := s.cfg.Archive
	nextWebDAV := s.cfg.WebDAV
	nextASRS3 := s.cfg.ASRS3

	// tombstone 判定的 hasSecret helper：bundle.Secrets 是否含某 env key。
	hasSecret := func(envKey string) bool {
		_, ok := bundle.Secrets[envKey]
		return ok
	}

	// 收集待持久化的 section DTO（仅 bundle 携带的段才写）。
	type sectionDTO struct {
		name string
		dto  interface{}
	}
	var sections []sectionDTO
	if bundle.RecapAI != nil {
		nextRecap = *bundle.RecapAI
		sections = append(sections, sectionDTO{"recap_ai", recapConfigToDTO(nextRecap)})
	}
	if bundle.Publish != nil {
		nextPublish = *bundle.Publish
		sections = append(sections, sectionDTO{"publish", publishConfigToDTO(nextPublish)})
	}
	if bundle.DashScope != nil {
		nextDashscope = *bundle.DashScope
		sections = append(sections, sectionDTO{"dashscope", dashscopeConfigToDTO(nextDashscope)})
	}
	if bundle.Archive != nil {
		nextArchive = *bundle.Archive
		sections = append(sections, sectionDTO{"archive", archiveConfigToDTO(nextArchive)})
	}
	if bundle.WebDAV != nil {
		// 先回填非密钥字段（含 PasswordEnv），再基于「导入后的 effective env」判 managed
		// （codex 复审 #2：env 名改名时，必须用新 env 名查 bundle.Secrets，否则 OLD→NEW 改名
		// 且 bundle 只带 OLD 时会误判 managed=false，导致 EffectivePassword 回落 yaml 明文）。
		nextWebDAV.Remote = bundle.WebDAV.Remote
		nextWebDAV.BasePath = bundle.WebDAV.BasePath
		nextWebDAV.URL = bundle.WebDAV.URL
		nextWebDAV.Username = bundle.WebDAV.Username
		nextWebDAV.PasswordEnv = bundle.WebDAV.PasswordEnv
		webdavManaged := s.cfg.WebDAV.PasswordManaged()
		if strategy == "overwrite" && !hasSecret(nextWebDAV.EffectivePasswordEnv()) {
			webdavManaged = true
		}
		nextWebDAV.SetPasswordManaged(webdavManaged)
		sections = append(sections, sectionDTO{"webdav", webdavConfigToDTO(nextWebDAV, webdavManaged)})
	}
	if bundle.ASRS3 != nil {
		// 同 WebDAV：先回填 AccessKeyEnv，再用新 effective env 判 managed。
		nextASRS3.Endpoint = bundle.ASRS3.Endpoint
		nextASRS3.Bucket = bundle.ASRS3.Bucket
		nextASRS3.AccessKeyID = bundle.ASRS3.AccessKeyID
		nextASRS3.AccessKeyEnv = bundle.ASRS3.AccessKeyEnv
		nextASRS3.Region = bundle.ASRS3.Region
		nextASRS3.PublicURLPrefix = bundle.ASRS3.PublicURLPrefix
		nextASRS3.UsePathStyle = bundle.ASRS3.UsePathStyle
		asrs3Managed := s.cfg.ASRS3.AccessKeyManaged()
		if strategy == "overwrite" && !hasSecret(nextASRS3.EffectiveAccessKeyEnv()) {
			asrs3Managed = true
		}
		nextASRS3.SetAccessKeyManaged(asrs3Managed)
		sections = append(sections, sectionDTO{"asr_s3", asrs3ConfigToDTO(nextASRS3, asrs3Managed)})
	}

	// 待写入的 secrets（剔除空值）。
	type secretKV struct{ k, v string }
	var newSecrets []secretKV
	for k, v := range bundle.Secrets {
		if v == "" {
			continue
		}
		newSecrets = append(newSecrets, secretKV{k, v})
	}

	// 持久化前校验（codex 审核 #4）：导入能写 runtime_settings，若写入非法值（如
	// publish.mode="column"）会导致下次启动 ApplyOverrides→Validate 失败、进程起不来。
	// 这里只校验「与 6 个可导入段相关」的约束（Config.Validate 里也有同样检查，但那个需要
	// 完整有效 cfg，不适用于「只覆盖部分段」的 import 场景，故在此局部校验）。
	// 校验失败返回 400、不落盘、不改内存。
	if err := validateImportedSections(nextRecap, nextPublish, nextDashscope, nextArchive, nextWebDAV, nextASRS3); err != nil {
		s.publishMu.Unlock()
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "imported config would be invalid: " + err.Error()})
		return
	}

	// 单事务：overwrite 清 secrets（ClearTx）→ 写各配置段 DTO → 写新 secrets。
	persistErr := runtimeconfig.WithTx(cctx, s.runtimeCfg.DB(), func(tx *sql.Tx) error {
		if strategy == "overwrite" {
			if err := s.secrets.ClearTx(cctx, tx); err != nil {
				return fmt.Errorf("clear secrets: %w", err)
			}
		}
		for _, sec := range sections {
			if err := s.persistSectionTx(cctx, tx, sec.name, sec.dto); err != nil {
				return fmt.Errorf("persist %s: %w", sec.name, err)
			}
		}
		for _, kv := range newSecrets {
			if err := s.secrets.SetTx(cctx, tx, kv.k, kv.v); err != nil {
				return fmt.Errorf("set secret %s: %w", kv.k, err)
			}
		}
		return nil
	})
	if persistErr != nil {
		s.publishMu.Unlock()
		slog.Warn("import persist core data failed, rolled back", "error", persistErr, "strategy", strategy)
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "failed to persist imported config: " + persistErr.Error()})
		return
	}

	// 事务 commit 成功 → 提交内存 cfg + 进程 env（与正式 handler 顺序一致）。
	s.cfg.RecapAI = nextRecap
	s.cfg.Publish = nextPublish
	s.cfg.DashScope = nextDashscope
	s.cfg.Archive = nextArchive
	s.cfg.WebDAV = nextWebDAV
	s.cfg.ASRS3 = nextASRS3
	// 先清理旧 env keys（overwrite 下避免残留旧密钥被读到），再 set 新值。
	if strategy == "overwrite" {
		for k := range oldSecretKeys {
			os.Unsetenv(k)
		}
	}
	for _, kv := range newSecrets {
		os.Setenv(kv.k, kv.v)
	}
	result.Details.ConfigSectionsCount = len(sections)
	result.Details.SecretsCount = len(newSecrets)
	cfgSnapshot := *s.cfg // 直接拷贝，避免持锁调 configSnapshot()（它会 RLock，与当前 Lock 互斥/冗余）。
	gen := s.bumpConfigGen()
	s.publishMu.Unlock()

	// === 阶段二：非配置数据（overwrite 清理 + channels/glossary/templates/bili_accounts）===
	// 仅在核心事务成功后执行；这些 store 无 *Tx 接口，失败记 warning 继续（与原行为一致）。
	// overwrite 在此清 glossary/templates/cookies（secrets 已在阶段一事务内清理）。
	// 各 store 做 nil 防护（测试 fixture 或部分裁剪部署可能未注入）。
	if strategy == "overwrite" {
		if s.glossary != nil {
			_ = s.glossary.ClearAll(cctx)
		}
		if s.recapTemplates != nil {
			_ = s.recapTemplates.ClearCustom(cctx)
		}
		if s.cookieAccounts != nil {
			_ = s.cookieAccounts.ClearAll(cctx)
		}
	}

	// Channels (upsert)
	for _, input := range bundle.Channels {
		if input.ID == "" {
			continue
		}
		_, err := s.channels.Get(cctx, input.ID)
		if err == nil {
			_, err = s.channels.Update(cctx, input.ID, input)
		} else {
			_, err = s.channels.Create(cctx, input)
		}
		if err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("channel %s: %v", input.ID, err))
			continue
		}
		result.Details.ChannelsCount++
	}

	// Glossary
	if bundle.Glossary.Global != nil {
		data, _ := json.Marshal(bundle.Glossary.Global)
		if count, err := s.glossary.ImportJSON(cctx, "", data); err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("glossary global: %v", err))
		} else {
			result.Details.GlossaryCount += count
		}
	}
	for chID, ge := range bundle.Glossary.Channel {
		data, _ := json.Marshal(ge)
		if count, err := s.glossary.ImportJSON(cctx, chID, data); err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("glossary channel %s: %v", chID, err))
		} else {
			result.Details.GlossaryCount += count
		}
	}

	// Templates
	if bundle.Templates.Global != nil {
		data, _ := json.Marshal(bundle.Templates.Global)
		if count, err := s.recapTemplates.ImportJSON(cctx, "", data); err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("templates global: %v", err))
		} else {
			result.Details.TemplatesCount += count
		}
	}
	for chID, te := range bundle.Templates.Channel {
		data, _ := json.Marshal(te)
		if count, err := s.recapTemplates.ImportJSON(cctx, chID, data); err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("templates channel %s: %v", chID, err))
		} else {
			result.Details.TemplatesCount += count
		}
	}

	// Bili accounts
	for _, item := range bundle.BiliAccounts {
		if item.UID == 0 {
			continue
		}
		// merge: skip if UID already exists
		if strategy == "merge" {
			if existing, _ := s.cookieAccounts.GetByUID(cctx, item.UID); existing != nil {
				continue
			}
		}
		_, err := s.cookieAccounts.CreateImported(cctx, &biliutil.CookieAccount{
			UID:               item.UID,
			Nickname:          item.Nickname,
			IsDefaultDownload: item.IsDefaultDownload,
			IsDefaultPublish:  item.IsDefaultPublish,
		})
		if err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("bili account uid %d: %v", item.UID, err))
			continue
		}
		result.Details.BiliAccountsCount++
	}

	if len(bundle.BiliAccounts) > 0 {
		result.Warnings = append(result.Warnings, "B站账号已导入元数据，需重新扫码登录恢复 Cookie")
	}

	// 用阶段一已算好的 cfgSnapshot/gen 刷新运行时状态（配置段在阶段一已提交，此处统一刷新一次）。
	s.refreshRuntimeStatus(cfgSnapshot, gen)

	ctx.JSON(http.StatusOK, result)
}

// validateImportedSections 复用各正式 update handler 的段内校验，避免 import 绕过 API 层
// 既有安全/格式约束（codex 复审 #1）。只覆盖 6 个可导入段，不要求完整有效 cfg（output_root/
// db_path 等启动时已校验、import 不改）。校验失败 → 调用方返回 400、不落盘。
func validateImportedSections(recap config.RecapAIConfig, publish config.PublishConfig,
	dashscope config.DashScopeConfig, archive config.ArchiveConfig,
	webdav config.WebDAVConfig, asrs3 config.ASRS3Config) error {
	// publish（复用 updatePublishConfig 的校验：mode/summary_len + 枚举 + timer 范围）
	if publish.Mode != "" && publish.Mode != "draft" && publish.Mode != "publish" {
		return fmt.Errorf("publish.mode must be 'draft' or 'publish', got %s", publish.Mode)
	}
	if publish.SummaryLen < 0 {
		return fmt.Errorf("publish.summary_len must be >= 0")
	}
	// private_pub 仅允许 1/2（非零时校验；0 视为未设置，与正式 handler 的 *int!=nil 语义对齐）
	if publish.PrivatePub != 0 && publish.PrivatePub != 1 && publish.PrivatePub != 2 {
		return fmt.Errorf("publish.private_pub must be 1 or 2, got %d", publish.PrivatePub)
	}
	// original/aigc/close_comment/up_choose_comment 仅允许 0/1
	for _, f := range []struct {
		val  int
		name string
	}{
		{publish.Original, "publish.original"},
		{publish.Aigc, "publish.aigc"},
		{publish.CloseComment, "publish.close_comment"},
		{publish.UpChooseComment, "publish.up_choose_comment"},
	} {
		if f.val != 0 && f.val != 1 {
			return fmt.Errorf("%s must be 0 or 1, got %d", f.name, f.val)
		}
	}
	// timer_pub_time > 0 时必须落在当前时间后 2 小时到 7 天内（与 updatePublishConfig 一致）
	if publish.TimerPubTime > 0 {
		now := time.Now().Unix()
		if publish.TimerPubTime < now+7200 || publish.TimerPubTime > now+7*86400 {
			return fmt.Errorf("publish.timer_pub_time must be between 2 hours and 7 days from now")
		}
	}
	// archive（Config.Validate 同源）
	switch archive.CleanupPolicy {
	case "", "none", "temp", "generated", "all":
	default:
		return fmt.Errorf("archive.cleanup_policy must be one of: none, temp, generated, all, got %s", archive.CleanupPolicy)
	}
	// recap_ai（复用 updateRecapConfig 的校验：provider 白名单 + 负数 + env 名）
	if p := strings.TrimSpace(recap.Provider); p != "" && !validRecapProviders[p] {
		return fmt.Errorf("recap_ai.provider invalid: %s", p)
	}
	if recap.MaxTokens < 0 {
		return fmt.Errorf("recap_ai.max_tokens must be >= 0")
	}
	if recap.MaxContinuations < 0 {
		return fmt.Errorf("recap_ai.max_continuations must be >= 0")
	}
	if recap.TimeoutSeconds < 0 {
		return fmt.Errorf("recap_ai.timeout_seconds must be >= 0")
	}
	if err := validateEnvKeyName(recap.APIKeyEnv, "recap_ai.api_key_env"); err != nil {
		return err
	}
	// dashscope（复用 updateDashScopeConfig 的校验：URL + 负数 + env 名）
	if err := validateDashScopeURL(dashscope.ASRURL, "dashscope.asr_url"); err != nil {
		return err
	}
	if err := validateDashScopeURL(dashscope.TasksURL, "dashscope.tasks_url"); err != nil {
		return err
	}
	if dashscope.SpeakerCount < 0 {
		return fmt.Errorf("dashscope.speaker_count must be >= 0")
	}
	if err := validateEnvKeyName(dashscope.APIKeyEnv, "dashscope.api_key_env"); err != nil {
		return err
	}
	// webdav（复用 updateWebDAVConfig 的校验：url/base_path/remote）
	if err := validateWebDAVURL(webdav.URL); err != nil {
		return err
	}
	if err := validateWebDAVBasePath(webdav.BasePath); err != nil {
		return err
	}
	if err := validateWebDAVRemote(webdav.Remote); err != nil {
		return err
	}
	if err := validateEnvKeyName(webdav.PasswordEnv, "webdav.password_env"); err != nil {
		return err
	}
	// asr_s3（复用 updateASRS3Config 的校验：endpoint/public_url_prefix/env 名）
	if err := validateASRS3Endpoint(asrs3.Endpoint); err != nil {
		return err
	}
	if err := validateASRS3PublicURLPrefix(asrs3.PublicURLPrefix); err != nil {
		return err
	}
	if err := validateEnvKeyName(asrs3.AccessKeyEnv, "asr_s3.access_key_env"); err != nil {
		return err
	}
	return nil
}

func channelToUpsertInput(ch channel.Channel) channel.UpsertInput {
	autoRecap := ch.AutoRecap
	return channel.UpsertInput{
		ID:                  ch.ID,
		Name:                ch.Name,
		UID:                 ch.UID,
		LiveRoomID:          ch.LiveRoomID,
		ReplaySourceURL:     ch.ReplaySourceURL,
		SpaceURL:            ch.SpaceURL,
		TitlePrefix:         ch.TitlePrefix,
		CookieFile:          ch.CookieFile,
		DownloadCookieFile:  ch.DownloadCookieFile,
		DownloadAccountID:   ch.DownloadAccountID,
		Enabled:             ch.Enabled,
		AutoRecord:          ch.AutoRecord,
		AutoASR:             ch.AutoASR,
		AutoRecap:           &autoRecap,
		RecordDanmaku:       ch.RecordDanmaku,
		SourceMode:          ch.SourceMode,
		DiscoverLimit:       ch.DiscoverLimit,
		PublishEnabled:      ch.PublishEnabled,
		PublishMode:         ch.PublishMode,
		PublishCategoryID:   ch.PublishCategoryID,
		PublishListID:       ch.PublishListID,
		PublishPrivatePub:   ch.PublishPrivatePub,
		PublishOriginal:     ch.PublishOriginal,
		AutoPublish:         ch.AutoPublish,
		PublishAigc:         ch.PublishAigc,
		PublishTimerPubTime: ch.PublishTimerPubTime,
		PublishCoverURL:     ch.PublishCoverURL,
		PublishTopics:       ch.PublishTopics,
		PublishAccountID:    ch.PublishAccountID,
		RecapModel:          ch.RecapModel,
		MaxContinuations:    ch.MaxContinuations,
	}
}
