# 计划：4 个调查问题修复（2026-07-15）

> **分支**：`fix/investigations-2026-07-15`
> **创建日期**：2026-07-15
> **状态**：已实施 + codex 执行审核通过（Issue 1/2/3 APPROVED，Issue 4 实现正确、测试覆盖受限于注入点，与现有 `fetchCidMapForMultiP` 同模式）

## codex 执行审核结论（2026-07-15）

- **Issue 1 — APPROVED**：`--ffmpeg-location` 注入逻辑、裸命令名降级和参数顺序均正确；测试组合充分。
- **Issue 2 — APPROVED**：补齐 `drawer rtl open` 恢复通用抽屉定位/打开态样式，保留 `recap-drawer-panel`，无明显副作用。
- **Issue 3 — APPROVED**：初值 `false` 时 immediate 执行 `cleanupSession()` 是安全空操作（计时器停止、session 为 null，不调取消接口）；`nextTick()` 覆盖初值 `true` 时 canvas 未挂载。存在一个非本次引入的轻微竞态（创建请求未完成时关闭弹窗，响应回来后可能保留 session 空转轮询），建议后续补可见性检查——**非本次范围**。
- **Issue 4 — 实现正确，测试层面 NEEDS_CHANGES（可接受）**：实现路径正确，`Pages[0]` 降级行为可接受；新增测试只覆盖"无 BV 返回 0"，核心弹幕写入/失败不阻断行为未受回归测试保护。**原因**：`biliutil.VideoClient{}.Fetch` 和 `fetchDanmakuShared` 无注入点，无法在不重构的情况下 mock，与现有 `fetchCidMapForMultiP`（同样只有 `TestFetchCidMapForMultiPNoBvid`）一致——核心行为靠集成/端到端验证覆盖。
- `singlePCid` 与 `fetchCidMapForMultiP` 有少量重复，但职责和返回形态不同，当前规模保持独立更符合 KISS。

## 目标

修复 `/home/lioi/文档/investigations/` 下记录的 4 个问题。经代码核实 + codex 只读确认，4 个问题诊断总体成立，其中 **Issue 3 的根因需修正**（调查文档归因为 `flush:'pre'` 竞态，codex + 代码核验确认真实根因是 `watch` 缺 `immediate:true`）。

## 核实结论总览

| Issue | 调查文档诊断 | 代码核实 | codex 确认 | 实际状态 |
|-------|-------------|---------|-----------|---------|
| 1. yt-dlp 缺 --ffmpeg-location | 成立 | `ytDlpArgs` 无注入 ✅ | 成立 | **未修复**（文档声称已修复，实际代码无改动） |
| 2. RecapDrawer z-index 缺失 | 成立 | `recap-drawer-panel` 无 CSS ✅ | 成立 | **未修复**（文档声称已修复，实际代码无改动） |
| 3. 扫码二维码首次不显示 | 根因有误 | `watch` 缺 `immediate:true` | 根因修正确认 | **未修复** |
| 4. yt-dlp 单 P 不抓弹幕 | 成立 | `downloadSingleP` 无弹幕抓取 ✅ | 成立 | **未修复** |

**重要说明**：Issue 1、2 的调查文档标注"已修复 + 端到端验证通过"，但当前仓库（`main` 分支，HEAD=`797a8e4`，工作区干净）**实际不含这些修复**。可能是文档先行撰写但代码未提交、或在其他环境修复未同步。本次按"未修复"处理，统一实施。

---

## Issue 1：yt-dlp 回放下载缺 --ffmpeg-location

### 问题

`internal/download/download.go:67-72` 的 `ytDlpArgs()` 只处理 `--cookies`，没有把 `YTDLPDownloader.FFmpeg` 字段转为 `--ffmpeg-location` 参数。yt-dlp 后处理（`-x` 提取音频为 m4a）找不到 ffmpeg，回放下载失败。

### 修复方案

**文件**：`internal/download/download.go`

**(1) 重写 `ytDlpArgs()` 注入 `--ffmpeg-location`**

```go
// ytDlpArgs 构造 yt-dlp 调用参数前缀:当 FFmpeg 指向真实路径(非裸命令名)时注入
// --ffmpeg-location <dir>,使 yt-dlp 后处理(-x 音频提取/转码)能找到 hikami 解析的 ffmpeg。
func (d YTDLPDownloader) ytDlpArgs(cookieFile string, baseArgs ...string) []string {
	prefix := make([]string, 0, 4)
	if dir := ffmpegLocationDir(d.FFmpeg); dir != "" {
		prefix = append(prefix, "--ffmpeg-location", dir)
	}
	if cookieFile != "" {
		prefix = append(prefix, "--cookies", cookieFile)
	}
	if len(prefix) == 0 {
		return baseArgs
	}
	return append(prefix, baseArgs...)
}

// ffmpegLocationDir 从 ffmpeg 可执行文件路径推导 yt-dlp --ffmpeg-location 所需的目录。
// 对空值和裸命令名(如 "ffmpeg")返回空(此时不注入,让 yt-dlp 回退自身 PATH 查找)。
func ffmpegLocationDir(ffmpegPath string) string {
	if ffmpegPath == "" {
		return ""
	}
	if filepath.Base(ffmpegPath) == ffmpegPath {
		return "" // 裸命令名,无目录信息
	}
	return filepath.Dir(ffmpegPath)
}
```

**设计要点**：
1. 统一注入点——所有 yt-dlp 调用（single-P/multi-P/listPlaylist）都经 `ytDlpArgs`，一处修复覆盖全部。
2. `--ffmpeg-location` 接受目录，yt-dlp 到该目录找 ffmpeg+ffprobe，故取 `filepath.Dir(d.FFmpeg)`。
3. 裸命令名守卫：`filepath.Base(path) == path` 判定裸命令名，不注入无效路径，保持原 PATH 回退行为。
4. codex 补充：系统 PATH 已有 ffmpeg 时不报错，嵌入/缓存 ffmpeg 不在 PATH 时稳定复现——本修复覆盖两种场景。

**(2) 新增单元测试**（`internal/download/download_test.go`）

| 测试 | 验证内容 |
|------|---------|
| `TestFFmpegLocationDir` | 空值/裸命令名返回空；绝对/相对/Windows 路径正确取目录 |
| `TestYTDLPArgsInjectsFFmpegLocation` | 真实路径注入 `--ffmpeg-location` 且保留 baseArgs |
| `TestYTDLPArgsNoFFmpegLocationForBareName` | 裸命令名不注入，避免传无效目录 |
| `TestYTDLPArgsCombinesCookiesAndFFmpegLocation` | `--ffmpeg-location` 与 `--cookies` 正确组合 |
| `TestYTDLPArgsNoPrefixForEmptyAll` | FFmpeg 空 + cookie 空 → 原样返回 baseArgs（无前缀） |

### 风险评估

低。修改集中在参数构造，裸命令名保持原回退行为，不影响 `TestAutoDownloaderFallbackOnNativeUnsupported` 等回退测试。

---

## Issue 2：RecapDrawerV10 面板 z-index 缺失

### 问题

`web/src/features/recaps/components/RecapDrawerV10.vue:172` 面板用 `class="recap-drawer-panel"`，但该 class 全前端无 CSS 定义 → `position:static; z-index:auto(=0)` → 被 `z-index:100` 的遮罩盖住，回顾文档不可见、按钮点不到、点一下就关。

### 修复方案

**文件**：`web/src/features/recaps/components/RecapDrawerV10.vue:172`

```diff
   <template v-if="visible">
     <div class="drawer-overlay" @click="emit('update:visible', false)" />
-    <div class="recap-drawer-panel">
+    <div class="drawer rtl open recap-drawer-panel" style="width: 600px;">
```

**改动说明**：
- `drawer`：复用 `ui.css:131` 的 `position:fixed; z-index:101`（**核心修复**，提升层级到遮罩之上）。
- `rtl`：`ui.css:136` 右侧滑出定位（`right:0`），与 HDrawer 默认方向一致。
- `open`：`ui.css:137` 的 `transform: translateX(0)`，让面板停在视口内。`RecapDrawerV10` 模板被 `v-if="visible"` 守卫，渲染即打开，故直接写死 `open`。
- `recap-drawer-panel`：保留原 class（向后兼容，现作为语义标记）。
- `style="width: 600px"`：内联宽度（回顾文档内容较多，比 HDrawer 默认 520px 更舒展）。

**codex 补充确认**：单独加 `.drawer` 不够完整（缺 rtl/open 定位），必须 `drawer rtl open` 三者齐全才能正确右侧滑出 + 归位 + 高 z-index。本方案已包含三者。

### 验证

- 现有 `RecapDrawerV10.test.ts` 6 用例基于 `.md-preview`/`.suggested-term-btn`/文本/textarea 选择器，不依赖 `.recap-drawer-panel` class，不受影响。
- `cd web && npm run build`（含 vue-tsc）验证类型 + 构建。

### 风险评估

低。单行模板改动，复用已测样式体系，不新增 CSS 规则。

---

## Issue 3：B站扫码二维码首次不显示

### 根因修正（与调查文档不同）

**调查文档归因**：`watch(visible, flush:'pre')` 在 DOM 挂载前触发 `startLogin → renderQRCode`，canvas 还没挂载 → 静默 return。

**codex + 代码核实确认的真实根因**：`watch(visible, ...)` **缺少 `immediate: true`**。

证据链：
1. `StreamersView.vue:242-245`：`<BiliQRCodeLoginDialog v-if="showQRDialog" v-model:visible="showQRDialog">`——组件用 `v-if` 条件渲染。
2. `StreamersView.vue:123`：`showQRDialog.value = true` 先置 true，再触发 `v-if` 挂载组件。
3. 组件挂载时 `props.visible` **已经是 `true`**（父级传入即 true）。
4. `useBiliQRCodeLogin.ts:84`：`watch(visible, (v) => { if (v) void startLogin() ... })`——**无 `immediate: true`**。
5. Vue 的 `watch` 默认只在值**变化**时触发。组件挂载时 `visible` 从初始值 `true` 开始，没有 `false → true` 的变化，**watch 永不触发** → `startLogin()` 永不调用 → 二维码不显示。
6. 点「刷新二维码」按钮直接调 `startLogin`（模板 footer 按钮），此时组件已挂载、canvas 已就绪 → 正常显示。这就是"第二次就好"的真正原因。

**为什么调查文档的竞态分析不成立**：即便 `flush:'pre'` 且 API 响应极快，只要 `startLogin` 被调用了，`await createQRCodeSession()` 的网络往返至少一个 macro/microtask，而 Vue 的 DOM flush 在同一同步代码块后的 microtask 执行——但实际上 `startLogin` 根本没被调用（watch 没触发），所以连竞态都谈不上。

### 修复方案

**文件**：`web/src/features/channel/useBiliQRCodeLogin.ts:84`

**(1) 主修复：watch 加 `{ immediate: true }`**

```diff
- watch(visible, (v) => {
-   if (v) void startLogin()
-   else void cleanupSession()
- })
+ watch(visible, (v) => {
+   if (v) void startLogin()
+   else void cleanupSession()
+ }, { immediate: true })
```

`immediate: true` 让 watch 在组件挂载时立即用当前值（`true`）执行一次回调 → `startLogin()` 被调用 → 创建 session → 渲染二维码。

**(2) 补充防御：`renderQRCode` 加 `await nextTick()`**

**文件**：`web/src/components/channel/BiliQRCodeLoginDialog.vue:31-37`

```diff
-async function renderQRCode(text: string): Promise<void> {
-  if (!canvasRef.value) return
-  await QRCode.toCanvas(canvasRef.value, text, {
+async function renderQRCode(text: string): Promise<void> {
+  // immediate watch 在组件挂载极早期触发 startLogin,HDialog 的 v-if 可能还未完成 canvas 挂载。
+  // await nextTick 确保 canvasRef 就绪,避免静默 return 导致二维码不显示。
+  await nextTick()
+  if (!canvasRef.value) return // nextTick 后仍无 canvas 属异常(组件已卸载),此时放弃
+  await QRCode.toCanvas(canvasRef.value, text, {
     width: 220,
     margin: 1,
     errorCorrectionLevel: 'M',
   })
+}
```

import 改动：`import { onBeforeUnmount, ref } from 'vue'` → `import { nextTick, onBeforeUnmount, ref } from 'vue'`。

**为何两者都要**：
- `immediate: true` 是**根治**（让首次 startLogin 真正执行）。
- `nextTick` 是**防御**（immediate watch 极早触发时，确保 canvas 挂载完成再画；即便未来 flush 语义变化也鲁棒）。两者组合形成完整修复。

### 验证

- `BiliQRCodeLoginDialog` 无独立单测（canvas 渲染依赖真实 DOM）。以 `cd web && npm run build`（vue-tsc 类型检查）验证编译。
- 手动/chrome-devtools 实测：首次打开弹窗二维码立即显示。

### 风险评估

低。`immediate: true` 在 `visible` 初值为 `false` 时会触发 `cleanupSession()`（else 分支），但 `cleanupSession` 对无 session 状态是 no-op（安全）。`nextTick` 延迟一个 microtask，用户无感。

---

## Issue 4：yt-dlp 单 P 回放不抓弹幕

### 问题

`internal/download/download.go:128-145` 的 `downloadSingleP` 只做音频下载 + info-json + thumbnail，无 `fetchDanmakuShared` 调用，不写 `raw/danmaku.xml`。导致 normalize 阶段弹幕为空（`package/danmaku.json = []`），回顾显示"弹幕 0 条"。

### 修复方案

**文件**：`internal/download/download.go`

**(1) `downloadSingleP` 补弹幕抓取（在 yt-dlp 下载成功后、return 前）**

```go
func (d YTDLPDownloader) downloadSingleP(ctx context.Context, command, sourceURL, rawDir string, cookieFile string) error {
	// ... 现有 yt-dlp 下载 + normalizeMetadataName + normalizeCoverName ...

	// 补弹幕抓取(与 native 单 P 的 native.go:132 对齐,与 multi-P 的 download.go:215 同函数)
	if cid := singlePCid(ctx, sourceURL, cookieFile); cid != 0 {
		cookieHeader, _ := cookieHeaderFromCookieFile(cookieFile)
		xml := fetchDanmakuShared(ctx, nil, "", "", cid, cookieHeader)
		if err := fsutil.WriteFileAtomic(filepath.Join(rawDir, "danmaku.xml"), xml, 0o644); err != nil {
			// 弹幕抓取失败不阻断主流程(音频已下载成功),仅告警——与 multi-P 容错策略一致
			slog.Warn("write danmaku xml failed for single-P", "source_url", sourceURL, "error", err)
		}
	} else {
		slog.Warn("single-P: no cid resolved, skip danmaku", "source_url", sourceURL)
	}
	return nil
}
```

**(2) 新增 helper `singlePCid`**

复用已有的 `extractNativeBVID`（download.go 已有）和 `biliutil.VideoClient.Fetch`（`fetchCidMapForMultiP` 已用）：

```go
// singlePCid 解析单 P 视频的 cid(弹幕抓取需要)。从 sourceURL 提取 bvid,
// 调 B 站 view API 取 Pages[0].CID。失败返回 0(调用方跳过弹幕,不阻断下载)。
func singlePCid(ctx context.Context, sourceURL, cookieFile string) int64 {
	bvid := extractNativeBVID(sourceURL)
	if bvid == "" {
		return 0
	}
	cookieHeader, _ := cookieHeaderFromCookieFile(cookieFile)
	info, err := (&biliutil.VideoClient{}).Fetch(ctx, bvid, cookieHeader)
	if err != nil || len(info.Pages) == 0 {
		return 0
	}
	return info.Pages[0].CID
}
```

**设计要点**：
- 弹幕抓取失败不阻断下载（音频已落地是主目标），与 multi-P 容错一致。
- 复用 `fetchDanmakuShared`（seg.so 优先 + XML 回退 + 双失败写 `<i></i>`），不另造轮子。
- cid 查询复用 view API（`fetchCidMapForMultiP` 同链路），单 P 取 `Pages[0].CID`。
- 产物 `raw/danmaku.xml`，与 native 单 P 一致，normalize 优先级 2 正确识别，无需改 normalize。

**需先核实的依赖**：`cookieHeaderFromCookieFile` 函数名（multi-P 路径用的 helper，需确认存在性与签名）、`extractNativeBVID` 存在性、`fsutil.WriteFileAtomic`/`slog` import 状态。这些在执行阶段逐一确认。

**(3) 新增单元测试**（`internal/download/download_test.go`）

| 测试 | 验证内容 |
|------|---------|
| `TestSinglePCid` | 有效 bvid URL → 返回正确 cid；无效 URL → 返回 0（mock view API） |
| `TestDownloadSinglePWritesDanmakuXML` | mock view API 返回 cid → mock fetchDanmakuShared → 断言 `danmaku.xml` 写出 |

### 风险评估

中。涉及外部 API 调用（view API），但失败降级为跳过弹幕（不阻断下载）。需确认 `fetchDanmakuShared`/`cookieHeaderFromCookieFile` 等函数的包可见性与签名。

---

## 执行顺序

1. **Issue 1**（download.go 参数构造）—— 独立，先做。
2. **Issue 4**（download.go 弹幕抓取）—— 同文件，紧接 Issue 1 后做，合并测试。
3. **Issue 2**（RecapDrawerV10.vue）—— 前端独立。
4. **Issue 3**（useBiliQRCodeLogin.ts + BiliQRCodeLoginDialog.vue）—— 前端独立。

## 验证

- 后端：`go test ./internal/download/...`（Issue 1+4 新测试 + 无回归）+ `go build ./...`
- 前端：`cd web && npm run build`（vue-tsc + vite，Issue 2+3）+ `cd web && npx vitest run`（无回归）
- 全量：`go test ./...`
