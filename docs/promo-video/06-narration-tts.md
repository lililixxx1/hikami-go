# 口播稿 · 对齐 Remotion 时间轴

> **用途**：直接复制进 TTS 工具（剪映文本朗读 / edge-tts / 火山 TTS）生成 mp3，放到 `video/public/audio/narration.mp3`，在 `Main.tsx` 加 `<Audio src={staticFile('/audio/narration.mp3')} />` 即可合成有声版。
>
> **时间轴**：与 `video/src/script.ts` 的 7 个 SceneSpec 严格对齐。每段开头标注 `[场景 起始秒 → 结束秒]`，括号内是给配音的语气提示，不读。
>
> **总时长**：约 2 分 05 秒（125s）。字数约 460 字，按 3.7 字/秒（中等播报语速）。

---

## 推荐配音参数

| 项 | 值 |
|----|-----|
| 音色 | 男声「沉稳解说 / 知性青年」；女声「温柔电台 / 朗读学姐」 |
| 语速 | 0.95x（略慢，给观众消化画面的时间） |
| 风格 | 科技向、克制、不煽情。像在跟朋友安利一个工具，不是带货 |
| 句间停顿 | 句号自动停 0.4s；段落间留 0.8s（在文本里加「，」可延长） |

---

## 正文（复制以下内容进 TTS）

[S1 痛点 · 0:00 → 0:15]（语气：共鸣，略疲惫）
你关注的直播，是不是越来越多？
一场两小时，根本看不过来。
错过了，只能翻切片。东拼西凑，拼不出完整的剧情。

[S2 转折 · 0:15 → 0:25]（语气：亮起来，带期待）
但如果——
有一款工具，能把两小时直播，变成五分钟读完的文字，
还自动发到 B 站。

[S3 亮相 · 0:25 → 0:35]（语气：清晰，自信）
Hikami-Go。
开源、免费、单文件部署。

[S4 演示 · 0:35 → 1:03]（语气：演示，平稳）
这是它的管理界面。
主播一开播，自动开始录制，弹幕也一起存下来。
录完，自动送去语音转写，AI 把它变成带时间轴的文字稿。
再交给大模型，生成一篇结构清晰的回顾。
最后，自动发布。
全自动，不用你点一下。

[S5 回顾 · 1:03 → 1:25]（语气：放慢，让观众感受）
一篇完整的直播回顾。
分段、小标题、重点高亮。
五分钟，读完两小时。
像一份，认真的直播笔记。

[S6 回放 · 1:25 → 1:43]（语气：补充，轻快）
不只是直播录像。
你丢一个视频链接，或一个收藏夹地址给它，
它也能转成回顾。
做切片素材、做学习笔记，都好用。

[S7 收尾 · 1:43 → 2:05]（语气：真诚，号召）
Hikami-Go。
Go 后端，内嵌 Web 界面。
Windows 双击即用，Linux 一条命令部署。
把追的直播，变成可以慢慢读的文字。
GitHub 搜 Hikami-Go，欢迎 Star。

---

## TTS 工具推荐（按优先级）

1. **剪映「文本朗读」**（最方便）：粘进文本框 → 选音色 → 生成 → 右键导出 mp3。免费、无字数限制、中文音色多。
2. **edge-tts**（免费、命令行、质量高）：
   ```bash
   # 需要 Python：pip install edge-tts
   edge-tts --voice zh-CN-YunxiNeural --rate=-5% --text "$(cat narration.txt)" --write-media video/public/audio/narration.mp3
   ```
   推荐音色：`zh-CN-YunxiNeural`（男，年轻沉稳）/ `zh-CN-YunjianNeural`（男，运动）/ `zh-CN-XiaoyiNeural`（女，温柔）/ `zh-CN-YunyangNeural`（男，新闻）
3. **火山引擎 TTS**（付费，质量最高）：大模型音色，自然度最好，适合商业视频。

## 合成有声版

```bash
# 1. 把 TTS 生成的 mp3 放到：
#    docs/promo-video/video/public/audio/narration.mp3

# 2. 在 src/Main.tsx 的 <AbsoluteFill> 内最外层加（在 Sequence 之前）：
#    import { Audio, staticFile } from 'remotion';
#    <Audio src={staticFile('/audio/narration.mp3')} />

# 3. 重新渲染：
cd docs/promo-video/video
npx remotion render Main out/hikami-promo-voiced.mp4 \
  --codec=h264 --crf=20 --concurrency=4 \
  --browser-executable="/c/Program Files/Google/Chrome/Application/chrome.exe"
```

## 备注

- 口播稿与字幕文案**不完全相同**：字幕更短（视觉单位），口播更完整（听觉单位）。两者时间轴对齐但文字量不同，这是正常的——观众看字幕 + 听配音时，字幕是配音的「摘要」。
- 如果想配音和字幕完全一致，把 `script.ts` 的 subtitles 改成口播稿的对应句即可。
