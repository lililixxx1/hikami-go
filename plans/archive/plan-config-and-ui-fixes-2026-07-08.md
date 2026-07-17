# 计划:配置默认值 + 设置页 UI 三处修复

> 状态:**codex 审核通过(v2)** — 待执行
> 创建:2026-07-08
> 修订:2026-07-08(v2,落实 codex 审核 v1 的 2 个必改项 + 2 个建议)
> 分支(将创建):`fix/config-and-ui-2026-07-08`
>
> **codex v1 审核结论**:NEEDS_CHANGES(2 必改 + 2 建议)。本 v2 已落实:
> - 必改 1:问题 3 措辞修正——"硬依赖"描述本身准确(`probe.go:159-164` 的 `StartupError` 对 `Required=true` 的 ffmpeg/ffprobe 缺失会 fatal 阻止启动),但"不暴露"的**理由**从"硬依赖"改为更精确的"风险控制"(改错路径导致服务无法启动,web 改动不应有此后果)。
> - 必改 2:问题 4 改用 `@click.self` 修饰符替代 `@click.stop`(更准确地拦截"仅点击遮罩才关闭")。
> - 建议 3:问题 1 grep 范围补 `web/`。
> - 建议 4:问题 3 测试补 presence-aware(nil 字段) + 空字符串两 case。

## 背景

用户提出 4 个问题,经核查全部成立(其中问题 3 需新增后端配置段)。本计划逐一列出根因与改动。

| # | 问题 | 位置 | 类型 |
|---|------|------|------|
| 1 | `output_root` 默认值 `"huizeman"` 应改为 `"hikami-go"` | `internal/config/config.go:724` + `config.full.example.yaml` | 后端默认值 |
| 2 | 设置页左侧 sidebar 不随页面滚动,总在左上角 | `web/src/features/settings/components-v10/settings-v10.css` | 前端样式 |
| 3 | yt-dlp / rclone 路径需可在 web 手动填写 | 新增 `tools` 配置段(后端)+ ToolsCardV10 可编辑(前端) | 后端新端点 + 前端表单 |
| 4 | 保存设置等确认框出现在左下角而非中间 | `web/src/components/ui/ui.css` `.dialog` / HDialog.vue 结构 | 前端样式(根因:DOM 结构与 CSS 不匹配) |

---

## 问题 1:output_root 默认值

### 根因

`internal/config/config.go:724`:
```go
v.SetDefault("output_root", "huizeman")
```
`config.full.example.yaml:1`:`output_root: "huizeman"`、`:71`:`base_path: "录播/Hikami/huizeman"`。

"huizeman" 是旧项目代号残留,应统一为 "hikami-go"。

### 改动

**`internal/config/config.go:724`**:
```go
v.SetDefault("output_root", "hikami-go")
```

**`config.full.example.yaml`**:
- `:1` `output_root: "huizeman"` → `output_root: "hikami-go"`
- `:71` `base_path: "录播/Hikami/huizeman"` → `base_path: "录播/Hikami/hikami-go"`

### 注意(不改的点)

- **`config.example.yaml` 不动**:其值为 `./data`(相对路径示例),与本次改名无关。
- **用户已有的 `config.yaml` 不受影响**:`SetDefault` 仅在 config.yaml 未设置该 key 时生效。已有 config.yaml 显式写了 `output_root` 的用户,行为不变。
- **不在 `Validate()` 加强制**:若用户显式写了 `"huizeman"`,属于用户选择,不拦截。
- **`runtime_settings` 表无 `output_root` 段**:本字段不走 runtimeconfig(它是路径而非可热更的运行参数),只改 viper 默认 + 示例文件即可。

### 测试

- 检查 `internal/config/config_test.go` 是否有断言默认值的测试(预期无,因为默认值测试很罕见);若有则同步。
- `grep -rn "huizeman" internal/ cmd/ docs/ config*.yaml web/` 确认无遗漏(预期只有上述 3 处 + recap 测试里的 fixture 路径/ChannelID `"huizeman"`——后者是测试硬编码数据,与默认值无关,**不改**;以及历史 changelog 文档,后者不改)。

---

## 问题 2:设置页 sidebar 不随滚动

### 根因

滚动容器是 `AppLayout.vue:197-201` 的 `.main-content`(`flex:1; overflow-y:auto`)。`.settings-v10`(SettingsView.vue:299)是其子元素,用 `display:flex; align-items:flex-start` 横向排布 sidebar + content。

`settings-v10.css:12-18` 的 `.sidebar` 没有 `position: sticky`:
```css
.settings-v10 .sidebar {
  width: 228px; min-width: 228px;
  background: var(--surface);
  border-right: 1px solid var(--border);
  padding: 16px 12px;
  overflow-y: auto;
}
```

flex 容器的子元素默认随容器内容流走,sidebar 高度 = 其自身内容高度,不会"钉住"。当右侧 content 很长、`.main-content` 出现滚动时,sidebar 跟着整体上滚,视觉上"消失在顶部"。

### 改动

**`web/src/features/settings/components-v10/settings-v10.css`** `.sidebar` 块,加 sticky 定位:

```css
.settings-v10 .sidebar {
  width: 228px; min-width: 228px;
  background: var(--surface);
  border-right: 1px solid var(--border);
  padding: 16px 12px;
  overflow-y: auto;
  /* sticky:让 sidebar 在 .main-content 滚动时钉在视口顶部(减去 topbar 高度) */
  position: sticky;
  top: 0;
  align-self: flex-start;  /* 配合 align-items:flex-start,防止 flex 拉伸 */
  max-height: calc(100vh - var(--topbar-h));
}
```

**关键点解释**:
- `position: sticky; top: 0`:相对最近的滚动容器(`.main-content`)吸附。因 `.main-content` 顶部紧贴 topbar 下方,`top:0` 即吸附到 topbar 下缘。
- `max-height: calc(100vh - var(--topbar-h))`:sidebar 自身内容也可能很长(13 个 section + 分组标题),限制其最大高度不超过视口减去 topbar,配合已有的 `overflow-y:auto` 实现 sidebar 内部独立滚动。
- `align-self: flex-start`:父级 `.settings-v10` 是 `align-items:flex-start`,本就允许子项不拉伸;显式写出以防未来父级改 `align-items:stretch` 时回退。此行是 belt-and-suspenders。
- `--topbar-h` 已定义(`design-tokens.css:34` = 52px)。

**响应式不动**:`@media (max-width:860px)` 下 `.sidebar { display:none }`(settings-v10.css:165)已覆盖小屏,sticky 对 `display:none` 无效,不冲突。

### 测试

- 无单测(CSS 视觉改动)。
- 手动验证:打开设置页,右侧 content 滚动时 sidebar 钉住;sidebar 内容超长时自身可滚。

---

## 问题 3:yt-dlp / rclone 路径 web 可编辑(新增 tools 配置段)

### 根因

`cfg.YTDLP` / `cfg.Rclone`(`config.go:26-27`)存在于配置,启动时由 `runtime.Probe`(`probe.go:90-91`)探测可用性。但:
- **没有 runtimeconfig 段**:无法通过 web 持久化修改。
- **没有 API 端点**:`/api/config/*` 只有 6 段(publish/recap/dashscope/asr-s3/webdav/archive)。
- **ToolsCardV10 纯展示**:只读显示 `tool.path/available/error`,无编辑能力。

用户需在 web 改路径就必须:① 新增 `tools` 配置段;② 新增 GET/PUT 端点;③ 卡片改可编辑表单;④ 保存后重新 Probe(刷新能力状态)。

### 设计决策(为何纳入 runtimeconfig 而非 secrets;为何只暴露 yt-dlp/rclone)

- yt-dlp/rclone 路径是**非敏感**字符串(可执行文件路径或命令名),无需进 secrets。
- 与现有 6 段(publish/archive 等)同构:走 `SectionDTO`(指针、presence-aware)+ `persistSectionTx` + `ApplyOverrides` case。
- **只暴露 yt-dlp/rclone,不暴露 ffmpeg/ffprobe**——理由是**风险控制**而非技术限制:
  - ffmpeg/ffprobe 在 `probe.go:88-89` 标记 `required=true`,`StartupError()`(`probe.go:159-164`)对其缺失返回 fatal 错误 → **服务启动失败**。从 web 改 ffmpeg 路径若填错,下次重启服务直接无法启动,且此时 web 也无法访问来纠正,需要回 config.yaml 手改,运维代价高。
  - yt-dlp/rclone 标记 `required=false`,填错仅降级对应能力(回放下载 / WebDAV/ASR 回退),服务正常启动,且 web 仍可用以再次修正。
  - 用户需求明确只提到 yt-dlp/rclone(问题 3 原文),ffmpeg/ffprobe 路径变更属罕见运维操作,留待后续按需扩展。

### 改动清单

#### 3.1 后端:config 层

**`internal/config/config.go`** — 新增 DTO + ApplyOverrides case:

1. 新增 `ToolsSectionDTO`(放在 `ArchiveSectionDTO` 后,`:489` 附近):
```go
// ToolsSectionDTO 对应 updateToolsConfig 管理的字段。
// 只含软依赖工具路径(yt_dlp/rclone);ffmpeg/ffprobe 不在此暴露
// (其 required=true,改错路径会导致下次启动 fatal,web 不可达无法纠正,
//  风险过高;仍只能通过 config.yaml 修改。详见计划文档问题 3 设计决策)。
type ToolsSectionDTO struct {
	YTDLP  *string `json:"yt_dlp,omitempty"`
	Rclone *string `json:"rclone,omitempty"`
}
```

2. `ApplyOverrides` 末尾(`:694` `archive` case 之后,`return cfg.Validate()` 之前)加 case:
```go
if raw, ok := overrides["tools"]; ok && len(raw) > 0 {
	var dto ToolsSectionDTO
	apply("tools", &dto)
	if dto.YTDLP != nil {
		cfg.YTDLP = *dto.YTDLP
	}
	if dto.Rclone != nil {
		cfg.Rclone = *dto.Rclone
	}
}
```

> 注:不改 `Validate()`。`runtime.Probe` 的 `probeTool` 对空命令返回 `Available=false`(降级),对找不到的命令返回 `exec.LookPath` 错误(降级),均不会 fatal。用户填错路径的反馈通过 `tools[].error + install_hint` 体现,安全。

#### 3.2 后端:handler 层

**`internal/handler/server.go`** — 新增路由 + handler(参照 `archive` 模式):

1. 路由注册(`:351` archive 路由之后):
```go
p.GET("/api/config/tools", s.getToolsConfig)
p.PUT("/api/config/tools", s.updateToolsConfig)
```

2. handler(放在 `archiveConfigToDTO` 后,`:2995` 附近):
```go
type toolsConfigResponse struct {
	YTDLP   string `json:"yt_dlp"`
	Rclone  string `json:"rclone"`
}

func newToolsConfigResponse(cfg config.Config) toolsConfigResponse {
	return toolsConfigResponse{
		YTDLP:  cfg.YTDLP,
		Rclone: cfg.Rclone,
	}
}

func (s *Server) getToolsConfig(ctx *gin.Context) {
	s.publishMu.RLock()
	resp := newToolsConfigResponse(*s.cfg)
	s.publishMu.RUnlock()
	ctx.JSON(http.StatusOK, resp)
}

func (s *Server) updateToolsConfig(ctx *gin.Context) {
	var input struct {
		YTDLP  *string `json:"yt_dlp"`
		Rclone *string `json:"rclone"`
	}
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid json body"})
		return
	}
	// trim 空白(presence-aware:nil=不改,""=清空回退默认探测)
	if input.YTDLP != nil {
		v := strings.TrimSpace(*input.YTDLP)
		input.YTDLP = &v
	}
	if input.Rclone != nil {
		v := strings.TrimSpace(*input.Rclone)
		input.Rclone = &v
	}

	s.publishMu.Lock()
	nextYTDLP := s.cfg.YTDLP
	nextRclone := s.cfg.Rclone
	if input.YTDLP != nil {
		nextYTDLP = *input.YTDLP
	}
	if input.Rclone != nil {
		nextRclone = *input.Rclone
	}
	dto := config.ToolsSectionDTO{
		YTDLP:  &nextYTDLP,
		Rclone: &nextRclone,
	}
	if err := runtimeconfig.WithTx(ctx.Request.Context(), s.runtimeCfg.DB(), func(tx *sql.Tx) error {
		return s.persistSectionTx(ctx.Request.Context(), tx, "tools", dto)
	}); err != nil {
		s.publishMu.Unlock()
		slog.Warn("persist tools config failed", "error", err)
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "failed to persist tools config"})
		return
	}
	s.cfg.YTDLP = nextYTDLP
	s.cfg.Rclone = nextRclone
	resp := newToolsConfigResponse(*s.cfg)
	cfgSnapshot := *s.cfg
	gen := s.bumpConfigGen()
	s.publishMu.Unlock()

	s.refreshRuntimeStatus(cfgSnapshot, gen)  // 重新 Probe,刷新 tools 表 + capabilities

	ctx.JSON(http.StatusOK, resp)
}
```

> **`refreshRuntimeStatus` 会重新 Probe**:`probe.go:90-91` 读 `cfg.YTDLP`/`cfg.Rclone` → `probeTool` → `exec.LookPath`。保存后前端 `onSaved` 已有 `runtimeStore.fetchRuntime(true)`(SettingsView.vue:190),自动拉到新的 tools 表 + capabilities。**无需重启服务**。

#### 3.3 后端:测试

**`internal/config/config_test.go`** — `TestApplyOverrides` 加 tools case(覆盖 4 个语义):
- **全覆盖**:写入 `overrides["tools"] = {"yt_dlp":"/custom/ytdlp","rclone":"/usr/bin/rclone"}` → 断言 `cfg.YTDLP`/`cfg.Rclone` 被覆盖。
- **presence-aware(nil 字段保留基线)**:写入 `{"yt_dlp":"/custom"}` 不传 `rclone` → 断言 `cfg.YTDLP` 被覆盖、`cfg.Rclone` 保持基线。
- **空对象保留基线**:写入 `{}` → 两者均保留基线。
- **空字符串(清空)**:写入 `{"yt_dlp":""}` → 断言 `cfg.YTDLP=""`(probe 会降级,符合"清空回退默认探测"语义,即用户显式清空则不再用配置路径,由 viper 默认或空命令降级)。

**`internal/handler/server_test.go`** — 加 2 测试:
- `TestGetToolsConfig`:`GET /api/config/tools` → 200 + 字段存在。
- `TestUpdateToolsConfigRoundTrip`:`PUT` 改路径 → 200,再 `GET` 回读断言一致;验证 `runtime_settings` 表有 `tools` section。

#### 3.4 前端:ToolsCardV10 改可编辑

**`web/src/features/settings/components-v10/ToolsCardV10.vue`** — 重写为自加载 + 可编辑卡片:

当前是纯展示(`<HCard><table>...`),改为:
1. `onMounted` 调 `getToolsConfig()`(`@/api/config` 新增)拉取当前路径。
2. 两个 `<HInput>` 分别编辑 yt_dlp / rclone(placeholder 提示"留空使用系统 PATH 探测")。
3. "保存"按钮调 `putToolsConfig({yt_dlp, rclone})`,成功后 `HMessage.success` + `emit('saved')`(壳的 `onSaved` 会重拉 runtime,刷新 tools 表 + capabilities)。
4. 保留只读的 tools 检测结果表(props.tools 仍由壳传入),放在表单**下方**,作为"当前探测状态"反馈——用户改完路径保存后,这张表会刷新显示新路径的探测结果。
5. emit 契约加 `saved`,SettingsView.vue 的 `<ToolsCardV10>` 加 `@saved="onSaved"`。

**`web/src/api/config.ts`**(新增两个函数):
```ts
export async function getToolsConfig(): Promise<{ yt_dlp: string; rclone: string }> {
  const r = await api.get('/api/config/tools')
  return r.data
}
export async function putToolsConfig(payload: { yt_dlp?: string; rclone?: string }): Promise<{ yt_dlp: string; rclone: string }> {
  const r = await api.put('/api/config/tools', payload)
  return r.data
}
```

> **types-derived.ts 不需手改**:`/api/config/tools` 是新端点,openapi-typescript 重新生成时自动派生;过渡期手写内联类型即可(见上)。

#### 3.5 文档

- `docs/api/openapi.yaml`:加 `/api/config/tools` GET/PUT path + ToolsConfig schema。
- `internal/handler/CLAUDE.md`:路由表 +2,加 tools 段说明。
- `internal/config/CLAUDE.md`:全局字段说明已含 yt_dlp/rclone,补一句"web 可改"。
- `docs/api/api-gap-analysis.md`:加 2026-07-08 update。

---

## 问题 4:确认框出现在左下角

### 根因(关键)

**DOM 结构与 CSS 不匹配**。`HDialog.vue:12-26`:
```html
<Teleport to="body">
  <template v-if="visible">
    <div class="dialog-overlay" @click="close" />   <!-- 兄弟节点 A -->
    <div class="dialog" ...>                          <!-- 兄弟节点 B -->
      ...
    </div>
  </template>
</Teleport>
```

`ui.css:151-161`:
```css
.dialog-overlay {
  position: fixed; inset: 0; ...
  display: flex; align-items: center; justify-content: center;  /* ← 这行试图居中 */
}
.dialog {
  position: relative; z-index: 121; ...  /* ← relative,不是 flex 子元素 */
}
```

**`.dialog-overlay` 的 `display:flex` 只对其**子元素**生效,但 `.dialog` 是它的**兄弟**,flex 居中完全不作用于 `.dialog`**。`.dialog` 是 `position:relative`,无定位偏移 → 跟随正常文档流(被 Teleport 到 `<body>` 末尾,排到 `#app` 之后)→ 默认停在视口左上,随页面滚动跑到左下/任意位置。

> 对比 `.drawer`(`ui.css:131-135`):`position:fixed; top:0; bottom:0`,显式定位,所以 drawer 正常。dialog 缺这一层。

### 改动(两选一,推荐方案 A)

#### 方案 A(推荐):把 `.dialog` 包进 `.dialog-overlay` 内,让 flex 生效

**`web/src/components/ui/HDialog.vue`** 模板改为嵌套:
```html
<Teleport to="body">
  <template v-if="visible">
    <div class="dialog-overlay" @click.self="close">
      <div class="dialog" :style="{ width: width ?? '480px' }" role="dialog" aria-modal="true">
        ...原内容...
      </div>
    </div>
  </template>
</Teleport>
```

关键(codex v1 必改 2 修正):
- `.dialog` 成为 `.dialog-overlay` 的子元素 → overlay 的 `display:flex; align-items:center; justify-content:center` 立即生效,dialog 水平垂直居中。
- **overlay 改用 `@click.self="close"`**(原为 `@click="close"`):`.self` 修饰符确保**只有点击 overlay 自身**(背景遮罩)才触发 close,点击任何子元素(dialog 及其内容)都不触发。这比在 `.dialog` 加 `@click.stop` 更健壮——`@click.stop` 只拦截 dialog 根元素的点击,dialog 内部子元素(body/footer 空白区)的点击仍会冒泡到 overlay 误关闭;`.self` 在源头(overlay)精确匹配 `event.target === currentTarget`,彻底杜绝冒泡误关。
- 不需要在 `.dialog` 上加任何点击修饰符。

**`ui.css` 不动**:`.dialog-overlay` 的 flex + `.dialog` 的 `position:relative` 现在配合正确(dialog 作为 flex 子元素,relative 无碍)。

> 为什么不改 CSS 让兄弟节点居中?因为 overlay 的 flex 永远不影响兄弟,只能改 DOM 结构;或者给 `.dialog` 加 `position:fixed; top:50%; left:50%; transform:translate(-50%,-50%)`(方案 B)。方案 A 更符合语义(遮罩包内容)且与 `.drawer-overlay`/`.drawer` 的目标一致(drawer 用 fixed 定位因它要贴边,dialog 要居中所以走 flex)。

#### 方案 B(备选,不推荐):纯 CSS 改 `.dialog` 定位

```css
.dialog {
  position: fixed; top: 50%; left: 50%; transform: translate(-50%, -50%);
  z-index: 121; ...
}
```
缺点:① 与 overlay 的 flex 冗余(两套居中机制);② `transform` 影响 `position:fixed` 的包含块(若有动画/transition 会出 bug);③ 未来加 dialog 进出动画时 transform 被占用。**不采用**。

### 测试

**`web/src/components/ui/__tests__/HDialog.test.ts`** — 现有测试验证 + 新增:
- `renders title and body when visible` — 不受影响(嵌套结构内仍能 query 到)。
- `emits update:visible=false on overlay click` — 保留(点遮罩关闭)。
- **新增** `does not close when clicking dialog content`(点 dialog 内 body/footer 不误关):渲染后 `dialog.querySelector('.dialog-body').click()` → 断言未 emit `update:visible`(验证 `@click.self` 生效)。
- **新增** `dialog is child of overlay`(结构断言):`overlay.querySelector('.dialog')` 非 null(验证嵌套,确保 flex 居中生效)。

> 注:`@click.self` 下,`jsdom` 的点击事件 `event.target` 即被点元素。点 `.dialog-body` 时 target=body,不等于 overlay → 不触发 close。这是 `.self` 修饰符的语义,测试能可靠验证。

---

## 执行顺序

1. **分支**:`git checkout -b fix/config-and-ui-2026-07-08`(从 main)。
2. **问题 1**(最简,先做):改 config.go 默认值 + config.full.example.yaml。`go build` 确认。
3. **问题 4**(独立,前端):改 HDialog.vue + 测试。`npm run type-check && npx vitest run`。
4. **问题 2**(独立,前端):改 settings-v10.css。无测试,视觉验证。
5. **问题 3**(最大,后端→前端):
   - 后端:config DTO + ApplyOverrides case → handler 路由 + handler + 测试 → `go test ./internal/config/... ./internal/handler/...`。
   - 前端:api/config.ts → ToolsCardV10 重写 → SettingsView.vue 加 `@saved` → type-check + vitest。
6. **文档**:openapi.yaml + CLAUDE.md × N + AGENTS.md changelog。
7. **整体验证**:`make test` + `cd web && npm run type-check && npx vitest run && npm run build`。
8. **codex 审核执行结果**。

## 提交策略

4 个 commit(每问题一个,问题 3 可拆后端/前端两个):
- `fix(config): rename output_root default huizeman → hikami-go`
- `fix(ui): center HDialog by nesting dialog inside overlay`
- `fix(ui): make settings sidebar sticky on scroll`
- `feat(config): add tools section for yt-dlp/rclone path editing`(+ 前端)

## 风险

| 风险 | 缓解 |
|------|------|
| 问题 3 新增 tools 段,ApplyOverrides 对历史无 tools section 的库兼容 | `apply()` 已处理 `!ok` → 保留基线 ✓ |
| ToolsCardV10 重写影响现有只读展示 | 保留下方 tools 检测结果表,只加表单在上方 |
| HDialog 嵌套结构改动影响其他用法 | ConfirmHost.vue/各 dialog 用法不变(都通过 slot);测试覆盖 overlay/dialog 点击 |
| 用户填错 yt-dlp 路径 | probe 降级 + error/install_hint 反馈,不 fatal;ffmpeg/ffprobe 不暴露 |

## 不在范围

- ffmpeg/ffprobe 路径 web 可编辑(硬依赖,本次不做)。
- settings 页整体重构(本次只 sticky)。
- config.yaml 已有值的用户迁移(SetDefault 不覆盖,无需迁移)。
