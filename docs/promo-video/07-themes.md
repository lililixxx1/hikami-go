# 宣传片风格版本说明

宣传片源码支持 **4 个主题**，通过环境变量 `HIKAMI_THEME` 切换，同一份 React 代码渲染出不同色调的成片。所有版本共享同一时间轴、同一字幕、同一动画，只是配色不同。

## 风格对照

| 主题 | `HIKAMI_THEME` | 主色 | 背景 | 适合场景 |
|------|---------------|------|------|---------|
| **深蓝（默认）** | `midnight` | `#0066cc` 蓝 | `#0a0f1a` 午夜蓝 | 科技向、稳重、B 站主流审美 |
| **明亮** ⭐ | `light` | `#0066cc` 蓝 | `#fafaf9` 暖白 | **贴近真实软件界面**、白天观看友好、演示文稿嵌入 |
| **琥珀** | `amber` | `#d97706` 暖橙 | `#1c1410` 深咖 | 温暖、人文、适合「陪伴向」叙事 |
| **极简** | `mono` | `#1a1917` 黑 | `#0a0a0a` 纯黑 | 高级感、克制、设计师向 |

> **明亮版（light）** 全片采用软件真实的浅色 UI（`#fafaf9` 暖白底 + 深色文字 + 蓝色 accent），Dashboard/Recap 段呈现为真实软件截图观感，与 Hikami-Go 实际管理界面几乎一致。适合想要「所见即所得」、向用户展示真实使用体验的场景。

## 渲染命令

```bash
cd docs/promo-video/video
CHROME="/c/Program Files/Google/Chrome/Application/chrome.exe"

# 默认深蓝
npx remotion render Main out/hikami-promo-midnight.mp4 \
  --codec=h264 --crf=20 --concurrency=3 --browser-executable="$CHROME" \
  --props='{"theme":"midnight"}'

# 明亮（贴近软件 UI）
npx remotion render Main out/hikami-promo-light.mp4 \
  --codec=h264 --crf=20 --concurrency=3 --browser-executable="$CHROME" \
  --props='{"theme":"light"}'

# 琥珀
npx remotion render Main out/hikami-promo-amber.mp4 \
  --codec=h264 --crf=20 --concurrency=3 --browser-executable="$CHROME" \
  --props='{"theme":"amber","titleSuffix":"琥珀"}'

# 极简
npx remotion render Main out/hikami-promo-mono.mp4 \
  --codec=h264 --crf=20 --concurrency=3 --browser-executable="$CHROME" \
  --props='{"theme":"mono","titleSuffix":"极简"}'
```

> **注意**：主题通过 `--props` 传入（Remotion 的 inputProps 机制），不要用 `HIKAMI_THEME` 环境变量——Remotion 在浏览器端渲染，读不到 `process.env`。
>
> `titleSuffix` 可选，会在右上角显示一个小角标（用于多版本对比时区分）。

## 怎么选

- **首发 B 站**：用 `midnight`（默认）。深色背景在 B 站播放器里最舒服，蓝色 accent 与 Hikami-Go 品牌色一致。
- **想要贴近软件真实界面**：用 `light`。全片浅色，Dashboard/Recap 段呈现真实软件 UI 观感，「所见即所得」。
- **二创/转载到小红书**：用 `amber`。暖色在小红书信息流里更吸睛，与「直播陪伴」的情感调性更搭。
- **GitHub 项目页嵌入**：用 `mono`。极简黑白与代码仓库气质一致，不抢 README 的视觉。
- **演示文稿嵌入**：用 `light`（浅色与多数演示主题协调）。

## 切换口播

口播稿（`06-narration-tts.md`）与主题**完全独立**。可以任意组合：
- 深蓝画面 + 男声沉稳
- 琥珀画面 + 女声温柔
- 极简画面 + 不配音（纯字幕版，适合社媒静音播放）

## 自定义新主题

在 `src/tokens.ts` 的 `THEMES` 里加一个新 key：

```ts
export const THEMES: Record<ThemeName, Theme> = {
  // ...existing
  neon: {
    name: 'neon',
    label: '霓虹',
    accent: '#ff00aa',
    accentDark: '#cc0088',
    accentGlow: 'rgba(255,0,170,0.4)',
    // ... 其余字段
    ...BASE_FONT,
  },
};
```

把 `ThemeName` 类型加上 `'neon'`，然后 `HIKAMI_THEME=neon` 即可渲染。
