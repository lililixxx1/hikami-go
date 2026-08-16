# Hikami-Go 宣传片 · Remotion 项目

这个目录是宣传片的**完整可渲染源码**。运行一条命令即可产出 `out/hikami-promo.mp4`（1920×1080 / 30fps / ~2 分钟）。

## 快速渲染

```bash
cd docs/promo-video/video
npm install
# 用系统 Chrome（避免 Remotion 下载 headless shell）
npx remotion render Main out/hikami-promo.mp4 \
  --codec=h264 --crf=20 --concurrency=4 \
  --browser-executable="/c/Program Files/Google/Chrome/Application/chrome.exe"
```

输出：`out/hikami-promo.mp4`

## 项目结构

```
video/
├── public/
│   └── images/
│       ├── hero.png        # 痛点→转折段背景（AI 生图：音频波形→文档）
│       └── recap-doc.png   # 回顾生成段背景（AI 生图：发光文档）
├── src/
│   ├── index.ts            # Remotion 入口
│   ├── Root.tsx            # Composition 注册（1920×1080, 30fps, 3750帧=125s）
│   ├── Main.tsx            # 主组件，按 SCENES 时间轴切场景 + 字幕层
│   ├── script.ts           # 7 场景时间轴 + 字幕数据（改时长/文案在这）
│   ├── tokens.ts           # 设计 token，对齐真实 UI（accent #0066cc 等）
│   ├── components/
│   │   └── Subtitle.tsx    # 全局字幕（毛玻璃 + 淡入）
│   └── scenes/
│       ├── Pain.tsx        # S1 痛点：飘动直播卡片堆叠，红点闪烁
│       ├── Turn.tsx        # S2 转折：hero 图 + 径向遮罩
│       ├── Reveal.tsx      # S3 亮相：Hikami-Go logo + slogan
│       ├── Dashboard.tsx   # S4 核心段：重绘管理 UI + 4 步流程依次高亮
│       ├── Recap.tsx       # S5 回顾：AI 图背景 + 重绘回顾文档
│       ├── Replay.tsx      # S6 回放：URL 输入打字 + 发现回放列表
│       └── Closing.tsx     # S7 收尾：logo + CTA + Star
├── remotion.config.ts
├── package.json
└── tsconfig.json
```

## 改动指南

| 想改 | 改哪 |
|------|------|
| 字幕文案/时长 | `src/script.ts`（每场景 subtitles 数组，at=帧偏移） |
| 配色 / 多风格 | `src/tokens.ts`（4 主题，用 `--props='{"theme":"light"}'` 切换，见 `docs/promo-video/07-themes.md`） |
| 某场景画面 | `src/scenes/<场景>.tsx` |
| AI 生成的背景图 | 替换 `public/images/hero.png` / `recap-doc.png`（2048×1152 png） |
| 加配音 | 见 `docs/promo-video/06-narration-tts.md` |

## 加配音（可选）

当前是无声版（靠字幕+节奏）。要配音：

1. 按 `docs/promo-video/01-narration-script.md` 在剪映/edge-tts 生成 mp3
2. 放到 `public/audio/narration.mp3`
3. 在 `src/Main.tsx` 顶层加：
   ```tsx
   import { Audio } from 'remotion';
   import { staticFile } from 'remotion';
   // 在 AbsoluteFill 内加：
   <Audio src={staticFile('/audio/narration.mp3')} />
   ```
4. 重新渲染

## 加 BGM

```tsx
import { Audio, interpolate, useCurrentFrame } from 'remotion';
// 在 Main 内：
<Audio src={staticFile('/audio/bgm.mp3')} volume={0.18} />
```

## 技术说明

- **UI 是重绘的，不是截图**：基于真实 Hikami-Go 实例的 DOM 与设计 token（accent #0066cc / surface #fafaf9 / success #1a7a5c 等）在 React 中重建，好处是可动画、可缓动、画面干净。重绘参照：主播「灰泽满 Hazel」的真实场次列表（`悠闲夜晚与种田` 等）。
- **AI 生图来自 yijianshengtu.com**（GPT-Image-2，2K 16:9），原始 prompt 见 `docs/promo-video/05-ai-tool-guide.md`。
- **不用 Remotion 自带的 Chrome Headless Shell**（下载慢），改用系统 Chrome，通过 `--browser-executable` 传入。
