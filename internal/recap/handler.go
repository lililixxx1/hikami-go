package recap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"hikami-go/internal/channel"
	"hikami-go/internal/config"
	"hikami-go/internal/glossary"
	"hikami-go/internal/notify"
	"hikami-go/internal/session"
	"hikami-go/internal/state"
	"hikami-go/internal/worker"
)

const TaskType = "recap"

const defaultSystemPrompt = `你是专业的直播内容编辑，擅长将直播转写文本和弹幕数据整理成结构清晰、内容丰富的中文直播回顾文档。你的语调生动但不煽情，像一位懂行又有趣的导播在为观众做赛后解说。

## 术语校正规则
- 回顾正文中一律使用术语校正表中的正确写法。
- 原话引用（> 格式）中保留主播原始说法；如需标注校正，只用 [应为：xxx] 标注，最终系统会提取为术语建议并清理标记。
- 组合词、惯用语同样需要校正。
- 不确定是否需要校正的，用 [?] 标记让读者自行判断。

## 人物辨析规则
- 当直播中提及多个同类型人物（如多个朋友、同学、观众），必须根据上下文、称谓、弹幕反应准确区分。
- 如果术语备注或弹幕提供了人物昵称与真实身份的对照，回顾正文中必须正确使用对应称呼。
- 不能将不同人物混淆为同一人，也不能将同一人物拆成多人。

## 内容提取优先级
- 具体数字（价格、时长、天数、人数）、食物名称、歌曲名、游戏名、角色名、活动名称、下次直播安排等，必须保留原文细节，不要概括化。
- 粉丝会传播的梗、生活细节、下播安排、社群称呼、具体名词优先于抽象总结。
- 每个主要话题至少引用 1-2 句主播原话（> 格式），宁多勿少。

## 隐私保护
- 提及非公众人物时用"某位观众""一位朋友"等模糊称呼，不使用真实人名。
- 弹幕内容引用时排除刷屏重复、广告、恶意攻击、涉及个人隐私的内容。

## 称呼统一约束
- 主播称呼必须严格使用术语校正表中标注的正确写法（通常为"正式名称"列）。
- 如果术语备注中指定了正式名与可用昵称（如"灰泽满（正式），可用昵称：小满、满神"），正文必须使用其中一种，不得混用其他变体。
- 粉丝称呼同理，必须使用术语表中标注的正确粉丝名。

## 统计真实性约束
- 所有统计数字（弹幕总数、独立用户数、直播时长、弹幕密度等）必须严格使用"基本信息"和"弹幕分析数据"中提供的数字。
- 禁止编造、估算或凭记忆填写任何统计数字。
- 如果数据缺失（如独立用户数为 0），不要自行编造，可以省略该指标或标注"数据缺失"。

## 禁止虚构约束
- 只根据转写原文和弹幕数据撰写回顾内容。
- 转写和弹幕中未出现的事件、对话、细节不得自行补写或虚构。
- 不确定的因果联系使用"据弹幕反应推测"等措辞标注，不要当作事实陈述。

## 内容要求
- 时间点一律使用相对直播时间（如 00:20-00:35），不要使用现实钟表时间（如 13:20-13:35）。
- 时间点误差控制在 ±30 秒以内，优先参考弹幕高热度时段数据校准。
- 覆盖直播的主要内容段落，不得遗漏重要话题或事件。
- 优先保留粉丝会传播的梗、生活细节、下播安排、社群称呼和具体名词，不要只写抽象总结。
- 原话引用优先于概括描述，宁可多引用也不笼统带过。
- 弹幕内容穿插在正文中，用 🔥 标注热度，配合叙事节奏自然插入。
- 每个内容段落至少引用 2-3 条相关弹幕，展示当时的直播间氛围。
- 详细内容回顾按主要话题自然分为 8-10 段；除非素材不足，不要压缩成少量大段。
- 长直播（>3 小时）应分段编号（第 1 部分、第 2 部分...），每部分包含描述性标题和时间范围。
- 保留具体细节：提到的食物、价格、数字、歌名、游戏名等不要省略。
- 若术语备注中提供了主播昵称、粉丝称呼、常用梗或写作风格，必须优先遵循。
- 禁止只做高度概括；每个主要话题必须写出具体事件、原话、数字、名词或弹幕反应。
- 除简洁摘要等明确短文模板外，完整回顾正文应保持足够篇幅；素材充足时不要压缩成短摘要。
- 详细内容回顾中单个叙事段落覆盖时间原则上不超过 15 分钟；直播节奏极慢或素材不足时才可合并，并说明合并原因。
- 如果接近输出长度限制，优先保留详细内容回顾、精彩语录和观看建议，不要用空泛总结替代具体内容。

## 情感基调分析
- 识别整场直播的核心情绪走向（开心/感动/爆笑/疲惫/平静等），不只是摘要事件。
- 「致粉丝」结尾章节必须呼应整场直播的情感基调，避免模板化。

## 专有名词处理
- 游戏角色、番剧人物、ACG 术语等基于你自身的知识库处理，使用公认的中文译名。
- 不确定准确写法的专有名词用 [?] 标记。

## 生成后自检清单
输出前检查以下要点，如发现遗漏应补全后再输出：
- 是否遗漏高光时刻或名场面？
- 是否遗漏活动信息、下播安排、粉丝梗？
- 时间点是否使用了相对直播时间（从 00:00 开始）？
- 专有名词是否使用了术语校正表中的正确写法？
- 「致{{fan_name}}」是否是全文最后一个章节？

## 输出风格参考
以下是期望的叙述风格示例（仅供参考风格，具体内容根据实际转写撰写）：

### 第3部分：常识答题的爆笑翻车现场
**00:25 - 00:27**

灰泽满在"以下哪一个是二次函数"前反复挣扎，在无理函数、反比例函数、三角函数之间犹豫不决，最终却自信地选了 D，揭晓答案后当场崩溃跳脚。🔥 弹幕瞬间刷满"灰泽满不会真没上过学吧"。

> "一眼 C，怎么可能是 C……"

连续的数学打击让直播间笑成一片，"数学已经达到小学生水平了"的调侃由此诞生。🔥 弹幕："我一直在哭"。

注意风格特点：紧凑叙事、原话引用自然穿插、弹幕配合叙事节奏、具体细节保留（道具名、台词、弹幕反应），而非空洞概括。

## 输出格式
- 使用 Markdown 格式，emoji 点缀但不堆砌。
- 语言风格粉丝友好，避免生硬的新闻稿语调。
- 内容质量 > 格式花哨，宁可文字朴实也不要空洞排比。
- 直接输出回顾文档，不要添加任何对话式开头（如"好的""没问题""以下是为您生成的"等）。`

const defaultUserFormat = `请按以下结构生成直播回顾文档：

# 🎙️ {{channel_name}}直播回顾 | {{date}} {{title}}

> 📅 {{date}} · ⏱ 约{{duration}} · 🏷 {{live_type}} · 💬 弹幕 {{danmaku_count}}条（{{unique_users}}人参与）

---

## 📋 目录
只列出以下固定主章节标题，每个标题单独一行，不带编号、emoji 或其他修饰，不展开详细内容回顾的子标题：

直播概要

高光时刻速览

详细内容回顾

精彩语录

弹幕互动精选

观看建议

## 🎬 直播概要
2-3 句话概括本场直播的情绪基调、内容走向和本场主线。
列出 6-8 个关键词标签。

**本场特色**（用列表列出 5-7 个亮点，如 🎵唱歌环节、💭深度话题、😂爆笑名场面等）

## ✨ 高光时刻速览
用符号化纯文本行列出 5-10 个高光时刻（不要用 markdown 表格），每条独占一行，格式严格为：

▶ HH:MM-HH:MM ｜ {分类emoji} {简要描述} {星级}

示例：
▶ 04:50-05:30 ｜ 😂 发现被弹劾无法合盟，绝望控诉"既想合盟又点弹劾" ⭐⭐⭐⭐

分类emoji：🎭意外事件 💝感动故事 😂爆笑时刻 🔥争议话题 🎵才艺展示 💬金句语录
热度用 1-5 颗 ⭐ 表示。注意分隔符是全角竖线 ｜，时间与分类之间、描述与星级之间各有一个空格。

## 📖 详细内容回顾
根据内容自然分 6-9 段，每 8-15 分钟为一段（不要合并过多的时间跨度），每段包含：
- 描述性标题（### 第N部分：标题）
- 相对直播时间范围（**HH:MM - HH:MM**，从开播 00:00 开始，不要写现实钟表时间）
- 内容叙述：每段必须包含具体事实、主播原话引用（> 格式）、弹幕反应和情绪/梗点
- **弹幕反应**：每段至少引用 2-3 条相关弹幕，展示当时的直播间氛围
- 保留具体细节：提到的食物、歌名、数字、游戏名等不要省略
- 保留粉丝会记住的小梗和生活碎片，例如迟到/改动态、KPI、活动、作业、下次直播安排等

重要：按话题转换自然分段，不要为了凑字数而合并不同话题的内容。每段 200-400 字即可。

如直播超过 3 小时，按主要话题自然分段（第 1 部分、第 2 部分...），每部分 500-800 字。

## 💎 精彩语录
分为两部分，每条语录独占一行（不要用列表或表格，不要加项目符号），格式为"原话引用" + 一个空格 + 时间戳括号：

**金句**
（3-5 条有深度或感人的原话，示例：）
"掌声跟嘘声总是同时到来，很正常。" （37:08-37:27）

**爆笑语录**
（3-5 条搞笑或出人意料的发言，示例：）
"庙小难留朱君，何不去全人之美，送他一票。" （16:45-16:56）

注意：弹幕统计表由系统程序化插入，你不要在回顾中生成弹幕统计表格。

## 💬 弹幕互动精选
选取 8-12 条最有代表性的弹幕，用符号化纯文本行展示（不要用 markdown 表格），每条独占一行，格式严格为：

▶ "{弹幕原文}"——{当时正在聊什么/说明} {🔥}

示例：
▶ "嗓子哑了也要赖🍟你做人真可以的"——灰泽满说薯条导致嗓子哑，被无情吐槽 🔥

说明部分紧跟在弹幕之后，用中文破折号 —— 连接；末尾可用 1-2 个 🔥 标注热度，普通弹幕也可不加。

## 🎯 观看建议
必看片段推荐（4-6 个），按推荐强度从高到低排序，每条独占一行（不要用 markdown 表格），格式严格为：

· {星级} {时间段}  {事件描述}（一句话推荐理由）

示例：
· ⭐⭐⭐⭐⭐ 1:28:00-1:33:30  满妈先斩后奏搬家安排（全场情绪最炸裂，家庭边界感讨论引发弹幕爆发）

星级用 ⭐ 表示推荐强度（3-5 颗），时间段与描述之间用两个空格分隔，推荐理由用括号包裹。

## 💌 致{{fan_name}}
一段温暖的结尾小作文（100-200 字），使用术语备注中的粉丝称呼，回顾本期直播的特别之处，感谢陪伴。
必须呼应本场直播的实际情感基调（开心/感动/爆笑/平静等），避免模板化套话。
这是全文最后一个章节，后面不要再添加免责声明、署名或其他内容。`

var (
	ErrSessionNotReady   = errors.New("session is not ready for recap")
	ErrTranscriptMissing = errors.New("recap transcript is missing")
	// ErrRecapUnavailable 在能力检查器判定回顾能力不可用时由 CreateTask 返回。
	// 设计 4.5（方案 B）：能力判断下沉到 CreateTask，自动链与手动 API 走同一套校验，
	// 消除 main.go 启动快照陈旧导致的 gate 与前端/手动 API 不一致（问题⑤）。
	ErrRecapUnavailable = errors.New("recap capability unavailable")
)

// CapabilityChecker 暴露运行时能力状态给 recap handler。
// 由 cmd/hikami 注入一个读取最新 runtime.Status 的实现（复用 server 代际刷新后的快照，
// 而非 main.go 启动时 Probe 的陈旧快照）。设计 4.5：能力判断下沉 CreateTask，避免直接依赖
// handler/server 包导致循环依赖。
type CapabilityChecker interface {
	RecapGenerate() bool
}

type sessionMetadata struct {
	DurationMs   int64  `json:"duration_ms"`
	StartedAt    string `json:"started_at"`
	EndedAt      string `json:"ended_at"`
	SourceName   string `json:"source_audio_name"`
	GeneratedAt  string `json:"generated_at"`
	DanmakuCount int    `json:"danmaku_count"`
}

type taskPayload struct {
	StartTime *float64 `json:"start_time,omitempty"`
	EndTime   *float64 `json:"end_time,omitempty"`
}

type timeRange struct {
	StartSec float64
	EndSec   float64
}

type Handler struct {
	cfg                *config.Config
	sessions           *session.Store
	states             *state.Store
	provider           Provider
	glossaryStore      *glossary.Store
	templateStore      *TemplateStore
	channels           *channel.Store
	summarizer         *TranscriptSummarizer
	onSuccess          func(ctx context.Context, task worker.Task)
	notifyMgr          *notify.Manager
	glossaryDiscoverer glossaryDiscoverer
	capabilityChecker  CapabilityChecker
}

// SetCapabilityChecker 注入运行时能力检查器（设计 4.5）。CreateTask 会据此判定回顾能力
// 是否可用，不可用时返回 ErrRecapUnavailable。未注入时不做能力校验（保持向后兼容）。
func (h *Handler) SetCapabilityChecker(c CapabilityChecker) {
	h.capabilityChecker = c
}

type glossaryDiscoverer interface {
	Discover(ctx context.Context, channelID string, sessionID string, transcript []byte, segments []glossary.TranscriptSegment, existingGlossary string) error
}

func NewHandler(cfg *config.Config, sessions *session.Store, states *state.Store, provider Provider, glossaryStore *glossary.Store, templateStore *TemplateStore, channels *channel.Store) *Handler {
	if provider == nil {
		provider = LocalProvider{}
	}
	if templateStore == nil {
		templateStore = NewTemplateStore(nil)
	}
	h := &Handler{cfg: cfg, sessions: sessions, states: states, provider: provider, glossaryStore: glossaryStore, templateStore: templateStore, channels: channels}
	// Enable summarizer if configured
	if cfg != nil && cfg.RecapAI.EnableSummarization {
		h.summarizer = NewTranscriptSummarizer(provider)
	}
	return h
}

type recapRuntimeOptions struct {
	Model            string
	MaxContinuations int
}

func (h *Handler) recapOptions(ctx context.Context, channelID string) recapRuntimeOptions {
	options := recapRuntimeOptions{}
	if h.cfg != nil {
		options.Model = h.cfg.RecapAI.Model
		options.MaxContinuations = h.cfg.RecapAI.MaxContinuations
	}
	if h.channels == nil {
		return options
	}
	ch, err := h.channels.Get(ctx, channelID)
	if err != nil {
		slog.WarnContext(ctx, "load channel recap options failed", "channel_id", channelID, "error", err)
		return options
	}
	if model := strings.TrimSpace(ch.RecapModel); model != "" {
		options.Model = model
	}
	if ch.MaxContinuations >= 0 {
		options.MaxContinuations = ch.MaxContinuations
	}
	return options
}

func (h *Handler) SetOnSuccess(fn func(ctx context.Context, task worker.Task)) {
	h.onSuccess = fn
}

func (h *Handler) SetNotifyManager(m *notify.Manager) {
	h.notifyMgr = m
}

func (h *Handler) SetGlossaryDiscoverer(d glossaryDiscoverer) {
	h.glossaryDiscoverer = d
}

func (h *Handler) Register(pool *worker.Pool) {
	pool.Register(TaskType, h.HandleTask)
}

func (h *Handler) CreateTask(ctx context.Context, pool *worker.Pool, sessionID string) (worker.Task, error) {
	sessionInfo, err := h.sessions.Get(ctx, sessionID)
	if err != nil {
		return worker.Task{}, err
	}
	if sessionInfo.Status != string(state.StatusASRDone) && sessionInfo.Status != string(state.StatusUploaded) {
		return worker.Task{}, fmt.Errorf("%w: status must be %s or %s, got %s", ErrSessionNotReady, state.StatusASRDone, state.StatusUploaded, sessionInfo.Status)
	}
	if h.capabilityChecker != nil && !h.capabilityChecker.RecapGenerate() {
		return worker.Task{}, ErrRecapUnavailable
	}
	if !sessionInfo.LocalAvailable {
		return worker.Task{}, fmt.Errorf("%w: local files removed, fetch from webdav first", ErrTranscriptMissing)
	}
	if _, err := os.Stat(h.transcriptPath(sessionInfo)); err != nil {
		if os.IsNotExist(err) {
			return worker.Task{}, fmt.Errorf("%w: %s", ErrTranscriptMissing, h.transcriptPath(sessionInfo))
		}
		return worker.Task{}, err
	}
	if _, ok, err := pool.Store().ActiveBySessionAndType(ctx, sessionInfo.ID, TaskType); err != nil {
		return worker.Task{}, err
	} else if ok {
		return worker.Task{}, fmt.Errorf("%w: active recap task already exists for session %s", worker.ErrTaskConflict, sessionInfo.ID)
	}
	return pool.Enqueue(ctx, worker.CreateInput{ChannelID: sessionInfo.ChannelID, SessionID: sessionInfo.ID, Type: TaskType, Payload: "{}"})
}

func (h *Handler) CreateTaskWithRange(ctx context.Context, pool *worker.Pool, sessionID string, startSec float64, endSec float64) (worker.Task, error) {
	if startSec < 0 || endSec <= startSec {
		return worker.Task{}, fmt.Errorf("%w: invalid recap time range", ErrSessionNotReady)
	}
	sessionInfo, err := h.sessions.Get(ctx, sessionID)
	if err != nil {
		return worker.Task{}, err
	}
	if !canCreateRangeRecap(sessionInfo.Status) {
		return worker.Task{}, fmt.Errorf("%w: status must be asr_done or later, got %s", ErrSessionNotReady, sessionInfo.Status)
	}
	if h.capabilityChecker != nil && !h.capabilityChecker.RecapGenerate() {
		return worker.Task{}, ErrRecapUnavailable
	}
	if !sessionInfo.LocalAvailable {
		return worker.Task{}, fmt.Errorf("%w: local files removed, fetch from webdav first", ErrTranscriptMissing)
	}
	if _, err := os.Stat(h.transcriptPath(sessionInfo)); err != nil {
		if os.IsNotExist(err) {
			return worker.Task{}, fmt.Errorf("%w: %s", ErrTranscriptMissing, h.transcriptPath(sessionInfo))
		}
		return worker.Task{}, err
	}
	if _, ok, err := pool.Store().ActiveBySessionAndType(ctx, sessionInfo.ID, TaskType); err != nil {
		return worker.Task{}, err
	} else if ok {
		return worker.Task{}, fmt.Errorf("%w: active recap task already exists for session %s", worker.ErrTaskConflict, sessionInfo.ID)
	}
	payload, err := json.Marshal(taskPayload{StartTime: &startSec, EndTime: &endSec})
	if err != nil {
		return worker.Task{}, err
	}
	return pool.Enqueue(ctx, worker.CreateInput{ChannelID: sessionInfo.ChannelID, SessionID: sessionInfo.ID, Type: TaskType, Payload: string(payload)})
}

func canCreateRangeRecap(status string) bool {
	return status == string(state.StatusASRDone) ||
		status == string(state.StatusRecapDone) ||
		status == string(state.StatusUploaded) ||
		status == string(state.StatusPublished)
}

func canHandleRecap(status string) bool {
	return status == string(state.StatusASRDone) || status == string(state.StatusUploaded)
}

func readSessionMetadata(dir string) *sessionMetadata {
	data, err := os.ReadFile(filepath.Join(dir, "package", "metadata.json"))
	if err != nil {
		return nil
	}
	var meta sessionMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil
	}
	return &meta
}

func (h *Handler) HandleTask(ctx context.Context, task worker.Task, reporter worker.Reporter) error {
	sessionInfo, err := h.sessions.Get(ctx, task.SessionID)
	if err != nil {
		return err
	}
	if !canHandleRecap(sessionInfo.Status) {
		return fmt.Errorf("session state %q is not valid for %s", sessionInfo.Status, TaskType)
	}
	sessionDir := h.sessionDir(sessionInfo)
	recapRange, err := parseTaskRange(task.Payload)
	if err != nil {
		return err
	}
	transcriptPath := h.transcriptPath(sessionInfo)
	// 前置产物校验（ISS-5）：状态与产物不一致时直接失败回退，避免缺产物时静默推进。
	if _, err := os.Stat(transcriptPath); err != nil {
		return err
	}
	transcript, err := os.ReadFile(transcriptPath)
	if err != nil {
		return err
	}
	if recapRange != nil {
		transcript, err = h.filteredTranscript(sessionInfo, *recapRange, transcript)
		if err != nil {
			return err
		}
	}
	transcriptForDiscovery := append([]byte(nil), transcript...)
	shouldDiscoverGlossary := recapRange == nil

	// 读取弹幕数据，部分回顾只保留指定时间段。
	var danmakuStatsVal *danmakuStats
	if danmakuData, err := os.ReadFile(filepath.Join(sessionDir, "package", "danmaku.json")); err == nil {
		var durationMs int64
		if meta := readSessionMetadata(sessionDir); meta != nil {
			durationMs = meta.DurationMs
		}
		if recapRange != nil {
			danmakuData, err = h.filteredDanmakuData(sessionInfo, *recapRange, danmakuData)
			if err != nil {
				return err
			}
			durationMs = rangeDurationMs(*recapRange)
		}
		danmakuStatsVal, _ = analyzeDanmaku(danmakuData, durationMs)
	}

	recapDir := filepath.Join(sessionDir, "recap")
	if err := os.MkdirAll(recapDir, 0o755); err != nil {
		return err
	}
	if err := reporter.Progress(ctx, 50, "generating recap"); err != nil {
		return err
	}
	transcript, _, err = h.correctedTranscriptForPrompt(ctx, sessionInfo, recapRange, transcript, recapDir)
	if err != nil {
		return err
	}

	// Read metadata
	meta := readSessionMetadata(sessionDir)

	// Resolve template
	var resolved *ResolvedTemplate
	if h.templateStore != nil && h.templateStore.db != nil {
		if r, err := h.templateStore.Resolve(ctx, sessionInfo.ChannelID, "default"); err == nil {
			resolved = r
		} else {
			slog.Warn("failed to resolve recap template, using defaults", "error", err)
		}
	}
	if resolved == nil {
		resolved = &ResolvedTemplate{
			SystemPrompt: defaultSystemPrompt,
			UserFormat:   defaultUserFormat,
		}
	}

	// Build template vars
	channelName := sessionInfo.Title // fallback to session title
	if h.channels != nil {
		if ch, err := h.channels.Get(ctx, sessionInfo.ChannelID); err == nil && ch.Name != "" {
			channelName = ch.Name
		}
	}
	vars := &TemplateVars{
		ChannelName: channelName,
		ChannelID:   sessionInfo.ChannelID,
		Slug:        sessionInfo.Slug,
		Title:       sessionInfo.Title,
		FanName:     resolved.FanName,
	}
	if sessionInfo.StartedAt != "" {
		if t, err := time.Parse(time.RFC3339, sessionInfo.StartedAt); err == nil {
			vars.Date = t.Format("2006.01.02")
			vars.DateTime = sessionInfo.StartedAt
		}
	}
	if meta != nil && meta.DurationMs > 0 {
		durMin := int(meta.DurationMs / 60000)
		durHour := durMin / 60
		durMinRem := durMin % 60
		vars.DurationMin = durMin
		vars.Duration = fmt.Sprintf("%d小时%d分钟", durHour, durMinRem)
	} else if sessionInfo.StartedAt != "" && sessionInfo.EndedAt != "" {
		if t1, err := time.Parse(time.RFC3339, sessionInfo.StartedAt); err == nil {
			if t2, err := time.Parse(time.RFC3339, sessionInfo.EndedAt); err == nil {
				durMin := int(t2.Sub(t1).Minutes())
				durHour := durMin / 60
				durMinRem := durMin % 60
				vars.DurationMin = durMin
				vars.Duration = fmt.Sprintf("%d小时%d分钟", durHour, durMinRem)
			}
		}
	}
	if danmakuStatsVal != nil {
		vars.DanmakuCount = danmakuStatsVal.TotalCount
		vars.UniqueUsers = danmakuStatsVal.UniqueUsers
		vars.AvgPerMin = fmt.Sprintf("%.1f", danmakuStatsVal.AvgPerMin)
	}

	// Attempt transcript summarization for long streams
	var transcriptForPrompt []byte
	if h.summarizer != nil {
		summarized, err := h.summarizer.Summarize(ctx, transcript, meta)
		if err != nil {
			slog.Warn("transcript summarization failed, using full transcript", "error", err)
		} else if summarized != nil {
			transcriptForPrompt = buildSummarizedTranscript(summarized)
			slog.Info("using summarized transcript", "original_len", len(transcript), "summary_len", len(transcriptForPrompt))
		}
	}
	if transcriptForPrompt == nil {
		transcriptForPrompt = transcript
	}

	// Build topic-driven segmentation suggestions for long streams
	var segSuggestions []segmentSuggestion
	if meta != nil && meta.DurationMs > 30*60*1000 {
		srtContent := h.readSRTContent(sessionInfo)
		boundaries := detectTopicBoundaries(srtContent, danmakuStatsVal, meta)
		segSuggestions = buildSegmentSuggestions(boundaries, meta.DurationMs)
		if len(segSuggestions) > 0 {
			slog.Info("detected topic segments", "boundaries", len(boundaries), "segments", len(segSuggestions))
		}
	}

	var knowledgeResult *KnowledgeLookupResult
	knowledgeOpts := parseKnowledgeOptions(resolved.ExtraVars)
	if recapRange == nil && knowledgeOpts.Enabled {
		glossaryText := ""
		if h.glossaryStore != nil {
			glossaryText, _ = h.glossaryStore.ExportForPrompt(ctx, sessionInfo.ChannelID)
		}
		lookupCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		result, err := performKnowledgeLookup(lookupCtx, string(transcriptForPrompt), glossaryText, knowledgeOpts)
		cancel()
		if err != nil {
			slog.WarnContext(ctx, "knowledge lookup failed", "error", err)
		} else {
			knowledgeResult = result
		}
	}

	var speakers *speakerStats
	if h.cfg.RecapAI.IncludeSpeakerInfo {
		speakers = h.speakerStats(sessionInfo, recapRange)
	}

	prompt := h.buildPromptWithKnowledgeAndSpeakers(ctx, sessionInfo, transcriptForPrompt, danmakuStatsVal, meta, resolved, vars, segSuggestions, knowledgeResult, speakers)
	outputSuffix := recapRangeSuffix(recapRange)
	if err := os.WriteFile(filepath.Join(recapDir, "live-recap"+outputSuffix+".prompt.md"), []byte(prompt), 0o644); err != nil {
		return err
	}
	fileBase := safeName("直播回顾_" + sessionInfo.Slug + outputSuffix)
	options := h.recapOptions(ctx, sessionInfo.ChannelID)
	providerCtx := withRecapModel(ctx, options.Model)
	result, err := h.provider.Generate(providerCtx, resolved.SystemPrompt, prompt, sessionInfo)
	if err != nil {
		return err
	}
	recap := result.Content
	rawParts := []string{result.Raw}
	finishReason := result.FinishReason

	maxCont := options.MaxContinuations
	for i := 0; i < maxCont && shouldContinueRecap(finishReason); i++ {
		slog.InfoContext(ctx, "recap auto-continuation", "attempt", i+1, "finish_reason", finishReason)

		contPrompt := buildContinuationPrompt(recap)
		contResult, err := h.provider.Generate(providerCtx, resolved.SystemPrompt, contPrompt, sessionInfo)
		if err != nil {
			slog.WarnContext(ctx, "continuation failed, using partial result", "error", err)
			break
		}

		recap = appendContinuation(recap, contResult.Content)
		rawParts = append(rawParts, contResult.Raw)
		finishReason = contResult.FinishReason
	}
	raw := combineRawResponses(rawParts)

	// Extract suggested terms from raw provider output before cleanup.
	suggestedTerms := extractSuggestedTerms(recap)
	if recapRange == nil {
		suggestedTermsPath := filepath.Join(recapDir, "suggested_terms.json")
		if len(suggestedTerms) > 0 {
			suggData, _ := json.Marshal(suggestedTerms)
			_ = os.WriteFile(suggestedTermsPath, suggData, 0o644)
		} else if err := os.Remove(suggestedTermsPath); err != nil && !os.IsNotExist(err) {
			slog.Warn("failed to remove stale suggested terms", "path", suggestedTermsPath, "error", err)
		}
	}
	recap = applyGlossaryCorrections(ctx, h.glossaryStore, sessionInfo.ChannelID, recap)
	recap = cleanSuggestedTermMarkers(recap)

	// Append programmatic danmaku statistics
	if danmakuStatsVal != nil && danmakuStatsVal.TotalCount > 0 {
		statsSection := FormatDanmakuStats(danmakuStatsVal, vars)
		recap = appendDanmakuStats(recap, statsSection)
	}
	recap = ensureFinalAddressSection(recap)
	if !hasGeneratedNotice(recap) {
		recap = strings.TrimRight(recap, " \n") + generatedNotice + "\n"
	}

	if err := os.WriteFile(filepath.Join(recapDir, "live-recap"+outputSuffix+".raw.json"), []byte(raw), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(recapDir, fileBase+".md"), []byte(recap), 0o644); err != nil {
		return err
	}
	if sessionInfo.Status == string(state.StatusASRDone) || sessionInfo.Status == string(state.StatusUploaded) {
		if _, err := h.states.Apply(ctx, task.SessionID, state.EventRecapSucceeded, task.ID, ""); err != nil {
			return err
		}
	}
	if shouldDiscoverGlossary && h.glossaryDiscoverer != nil {
		segments := h.readDiscoverySegments(sessionInfo)
		existingGlossary := ""
		if h.glossaryStore != nil {
			existingGlossary, _ = h.glossaryStore.ExportForPrompt(ctx, sessionInfo.ChannelID)
		}
		go func() {
			bg, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			if err := h.glossaryDiscoverer.Discover(
				bg,
				sessionInfo.ChannelID,
				sessionInfo.ID,
				transcriptForDiscovery,
				segments,
				existingGlossary,
			); err != nil {
				slog.Warn("glossary discovery failed",
					"channel_id", sessionInfo.ChannelID,
					"session_id", sessionInfo.ID,
					"error", err)
			}
		}()
	}
	if h.onSuccess != nil {
		h.onSuccess(ctx, task)
	}
	if h.notifyMgr != nil {
		h.notifyMgr.Send(ctx, notify.EventRecapDone, "回顾已生成",
			fmt.Sprintf("频道 %s 的回顾已生成", sessionInfo.ChannelID))
	}
	return reporter.Progress(ctx, 95, "recap completed")
}

func (h *Handler) sessionDir(sessionInfo session.Session) string {
	return filepath.Join(h.cfg.OutputRoot, sessionInfo.ChannelID, sessionInfo.Slug)
}

func (h *Handler) transcriptPath(sessionInfo session.Session) string {
	return filepath.Join(h.sessionDir(sessionInfo), "package", "transcript.txt")
}

type transcriptSegment struct {
	StartMS   int64  `json:"start_ms"`
	EndMS     int64  `json:"end_ms"`
	Text      string `json:"text"`
	SpeakerID *int64 `json:"speaker_id"`
}

const (
	speakerMinDurationRatio = 0.01
	speakerMinSegments      = 2
	speakerMinCoverage      = 0.30
)

type speakerStats struct {
	EffectiveCount    int
	Coverage          float64
	SwitchesPerMinute float64
	Speakers          []speakerStat
}

type speakerStat struct {
	ID            int64
	DurationMS    int64
	SegmentCount  int
	DurationRatio float64
}

func (h *Handler) speakerStats(sessionInfo session.Session, recapRange *timeRange) *speakerStats {
	return speakerStatsFromPackageDir(filepath.Join(h.sessionDir(sessionInfo), "package"), recapRange)
}

func speakerStatsFromPackageDir(packageDir string, recapRange *timeRange) *speakerStats {
	data, err := os.ReadFile(filepath.Join(packageDir, "segments.json"))
	if err != nil {
		slog.Warn("speaker stats unavailable", "error", err)
		return nil
	}
	var segments []transcriptSegment
	if err := json.Unmarshal(data, &segments); err != nil {
		slog.Warn("speaker stats unavailable", "error", err)
		return nil
	}
	return calculateSpeakerStats(segments, recapRange)
}

type speakerSpan struct {
	startMS   int64
	endMS     int64
	speakerID int64
}

func calculateSpeakerStats(segments []transcriptSegment, recapRange *timeRange) *speakerStats {
	var startBound, endBound int64
	hasRange := recapRange != nil
	if hasRange {
		startBound = rangeStartMs(*recapRange)
		endBound = rangeEndMs(*recapRange)
	}

	type aggregate struct {
		durationMS   int64
		segmentCount int
	}

	totalDurationMS := int64(0)
	coveredDurationMS := int64(0)
	bySpeaker := make(map[int64]aggregate)
	spans := make([]speakerSpan, 0, len(segments))

	for _, seg := range segments {
		startMS := seg.StartMS
		endMS := seg.EndMS
		if startMS < 0 || endMS <= startMS {
			continue
		}
		if hasRange {
			if endMS <= startBound || startMS >= endBound {
				continue
			}
			if startMS < startBound {
				startMS = startBound
			}
			if endMS > endBound {
				endMS = endBound
			}
		}
		durationMS := endMS - startMS
		if durationMS <= 0 {
			continue
		}

		totalDurationMS += durationMS
		if seg.SpeakerID == nil {
			continue
		}

		coveredDurationMS += durationMS
		agg := bySpeaker[*seg.SpeakerID]
		agg.durationMS += durationMS
		agg.segmentCount++
		bySpeaker[*seg.SpeakerID] = agg
		spans = append(spans, speakerSpan{startMS: startMS, endMS: endMS, speakerID: *seg.SpeakerID})
	}

	if totalDurationMS <= 0 || coveredDurationMS <= 0 {
		return nil
	}
	coverage := float64(coveredDurationMS) / float64(totalDurationMS)
	if coverage < speakerMinCoverage {
		return nil
	}

	speakers := make([]speakerStat, 0, len(bySpeaker))
	for id, agg := range bySpeaker {
		ratio := float64(agg.durationMS) / float64(coveredDurationMS)
		if ratio < speakerMinDurationRatio || agg.segmentCount < speakerMinSegments {
			continue
		}
		speakers = append(speakers, speakerStat{
			ID:            id,
			DurationMS:    agg.durationMS,
			SegmentCount:  agg.segmentCount,
			DurationRatio: ratio,
		})
	}
	if len(speakers) < 2 {
		return nil
	}

	sort.Slice(speakers, func(i, j int) bool {
		if speakers[i].DurationMS == speakers[j].DurationMS {
			return speakers[i].ID < speakers[j].ID
		}
		return speakers[i].DurationMS > speakers[j].DurationMS
	})

	effectiveIDs := make(map[int64]struct{}, len(speakers))
	for _, speaker := range speakers {
		effectiveIDs[speaker.ID] = struct{}{}
	}
	sort.Slice(spans, func(i, j int) bool {
		if spans[i].startMS == spans[j].startMS {
			return spans[i].endMS < spans[j].endMS
		}
		return spans[i].startMS < spans[j].startMS
	})

	switches := 0
	hasLast := false
	var lastID int64
	for _, span := range spans {
		if _, ok := effectiveIDs[span.speakerID]; !ok {
			continue
		}
		if hasLast && span.speakerID != lastID {
			switches++
		}
		lastID = span.speakerID
		hasLast = true
	}

	coveredMinutes := float64(coveredDurationMS) / 60000
	switchesPerMinute := 0.0
	if coveredMinutes > 0 {
		switchesPerMinute = float64(switches) / coveredMinutes
	}
	if math.IsNaN(switchesPerMinute) || math.IsInf(switchesPerMinute, 0) {
		switchesPerMinute = 0
	}

	return &speakerStats{
		EffectiveCount:    len(speakers),
		Coverage:          coverage,
		SwitchesPerMinute: switchesPerMinute,
		Speakers:          speakers,
	}
}

func (h *Handler) timedTranscript(sessionInfo session.Session) ([]byte, error) {
	return timedTranscriptFromPackageDir(filepath.Join(h.sessionDir(sessionInfo), "package"))
}

func (h *Handler) readDiscoverySegments(sessionInfo session.Session) []glossary.TranscriptSegment {
	data, err := os.ReadFile(filepath.Join(h.sessionDir(sessionInfo), "package", "segments.json"))
	if err != nil {
		return nil
	}
	var segments []glossary.TranscriptSegment
	if err := json.Unmarshal(data, &segments); err != nil {
		return nil
	}
	return segments
}

func timedTranscriptFromPackageDir(packageDir string) ([]byte, error) {
	data, err := os.ReadFile(filepath.Join(packageDir, "segments.json"))
	if err != nil {
		return nil, err
	}
	var segments []transcriptSegment
	if err := json.Unmarshal(data, &segments); err != nil {
		return nil, err
	}
	var b strings.Builder
	b.WriteString("【带时间戳转写】\n\n")
	for _, seg := range segments {
		text := strings.TrimSpace(seg.Text)
		if text == "" || seg.StartMS < 0 {
			continue
		}
		b.WriteString("[")
		b.WriteString(formatRecapTimestamp(seg.StartMS))
		b.WriteString("] ")
		b.WriteString(text)
		b.WriteString("\n")
	}
	return []byte(b.String()), nil
}

func formatRecapTimestamp(ms int64) string {
	totalSec := ms / 1000
	hour := totalSec / 3600
	minute := (totalSec % 3600) / 60
	second := totalSec % 60
	if hour > 0 {
		return fmt.Sprintf("%02d:%02d:%02d", hour, minute, second)
	}
	return fmt.Sprintf("%02d:%02d", minute, second)
}

// readSRTContent attempts to read SRT or VTT caption files for segmentation analysis.
func (h *Handler) readSRTContent(sessionInfo session.Session) string {
	dir := h.sessionDir(sessionInfo)
	for _, name := range []string{"transcript.srt", "transcript.vtt"} {
		if data, err := os.ReadFile(filepath.Join(dir, "package", name)); err == nil {
			return string(data)
		}
	}
	return ""
}

// buildSummarizedTranscript converts a SummarizedTranscript into text suitable for prompt injection.
func buildSummarizedTranscript(s *SummarizedTranscript) []byte {
	var b strings.Builder
	b.WriteString("【压缩转写摘要】\n\n")
	b.WriteString(s.Summary)
	if len(s.KeyQuotes) > 0 {
		b.WriteString("\n\n【关键原话引用】\n\n")
		for _, q := range s.KeyQuotes {
			b.WriteString("> ")
			b.WriteString(q)
			b.WriteString("\n")
		}
	}
	if len(s.Topics) > 0 {
		b.WriteString("\n【讨论话题】")
		for i, t := range s.Topics {
			if i > 0 {
				b.WriteString("、")
			}
			b.WriteString(t)
		}
		b.WriteString("\n")
	}
	return []byte(b.String())
}

func shouldContinueRecap(finishReason string) bool {
	fr := strings.TrimSpace(finishReason)
	return fr == "length" || fr == "max_tokens"
}

func buildContinuationPrompt(currentRecap string) string {
	tail := tailForContinuation(currentRecap)
	return fmt.Sprintf(`你正在续写一篇直播回顾文档。前一次输出因为长度限制中断。

要求：
- 不要重写已经完成的前文。
- 从当前内容最后未完成的位置继续写。
- 如果最后一个标题或段落已经出现，不要重复标题。
- 保持 Markdown 结构、语气、时间线和术语规则一致。
- 完成剩余章节，直到"致..."结尾章节。
- 直接输出续写正文，不要解释。

【已经生成的内容（尾部）】
%s

请继续输出后续内容：`, tail)
}

func tailForContinuation(text string) string {
	runes := []rune(text)
	if len(runes) <= 8000 {
		return text
	}
	return string(runes[len(runes)-8000:])
}

func appendContinuation(base string, continuation string) string {
	continuation = strings.TrimSpace(stripAIPreamble(continuation))
	if continuation == "" {
		return base
	}
	continuation = dropDuplicateLeadingHeading(base, continuation)
	return strings.TrimSpace(base) + "\n\n" + continuation
}

func dropDuplicateLeadingHeading(base string, continuation string) string {
	lines := strings.Split(strings.TrimSpace(continuation), "\n")
	if len(lines) == 0 {
		return continuation
	}
	firstLine := strings.TrimSpace(lines[0])
	if !strings.HasPrefix(firstLine, "#") {
		return continuation
	}
	baseLines := strings.Split(base, "\n")
	for i := len(baseLines) - 1; i >= 0; i-- {
		bl := strings.TrimSpace(baseLines[i])
		if strings.HasPrefix(bl, "#") {
			if bl == firstLine {
				return strings.Join(lines[1:], "\n")
			}
			break
		}
	}
	return continuation
}

func combineRawResponses(rawParts []string) string {
	if len(rawParts) == 1 {
		return rawParts[0]
	}
	type rawEntry struct {
		Index int    `json:"index"`
		Raw   string `json:"raw"`
	}
	entries := make([]rawEntry, len(rawParts))
	for i, r := range rawParts {
		entries[i] = rawEntry{Index: i, Raw: r}
	}
	data, _ := json.Marshal(map[string]any{"responses": entries})
	return string(data)
}
