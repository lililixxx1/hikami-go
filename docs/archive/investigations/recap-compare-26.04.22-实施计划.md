# 回顾生成优化 — 实施计划

> 优先级：P0 立即修复 > P1 Prompt 优化 > P2 工程架构

---

## 阶段一：立即生效（预计 1 小时）

- [ ] **切换模型为 v4-pro** — `config.yaml` 改 `model: "deepseek-v4-pro"`
- [ ] **调大 max_tokens** — `config.yaml` 加 `max_tokens: 65536`，确保不因配额截断
- [ ] **清除术语标注残留** — `glossary_correction.go` 的 `applyGlossaryCorrections` 末尾正则清理 `\[应为[：:][^\]]+\]`
- [ ] **ASR 错误全文替换** — `glossary_correction.go` 在最终 Markdown 上做 term→canonical 全文替换（不限于引用块）

## 阶段二：Prompt 优化（预计 2-3 小时）

- [ ] **增加字数量化要求** — `handler.go` 的 `defaultUserFormat` 加入："回顾总长度不少于 12000 字符，每个话题段落不少于 500 字，完成全部章节前不得停止"
- [ ] **禁止概括指令** — `defaultSystemPrompt` 加入："禁止使用'大致意思是''他提到'等概括性描述，必须保留原话引用和具体细节"
- [ ] **自检续写指令** — `defaultSystemPrompt` 加入："输出结束前检查是否覆盖了所有重要话题且达到最低长度要求，未达到则继续补充"
- [ ] **分段要求强化** — `defaultSystemPrompt` 修改："叙事性内容（讲故事等）按情节转折自然分段，不得将超过 15 分钟的连续叙述压缩为一段"
- [ ] **预设模板同步** — `presets.go` 5 个内置预设全部加入长度引导和禁止概括指令

## 阶段三：工程优化（后续迭代）

- [ ] **自动续写机制** — `handler.go` 检测 `finish_reason: stop` 且长度 < 目标值 80% 时，自动发起新请求续写
- [ ] **流式接收** — `provider_openai.go` 支持 stream 模式，避免长生成 HTTP 超时
- [ ] **弹幕精简策略** — `danmaku.go` 按分段范围过滤弹幕，只注入峰值窗口代表性弹幕
- [ ] **摘要阈值调整** — `transcript_summarizer.go` 阈值从 30000 降到 15000
- [ ] **输出长度校验** — `handler.go` 生成后校验长度，过短自动标记"需补充"

---

## 验证方法

每完成一个阶段后：
1. 用 26.04.22 数据重新生成回顾
2. 对比指标：长度、术语残留、ASR 错误、情节保留数、时间戳格式
3. 与原文稿（18.3KB）和 v4-pro 基线（20.4KB）对比

## 当前基线

| 指标 | v4-flash | v4-pro（已验证） | 目标 |
|------|----------|----------------|------|
| 回顾长度 | 13KB | 20.4KB | ≥ 20KB |
| 术语残留 | 多处 | 0 | 0 |
| ASR 错误 | 多处 | 1 处 | 0 |
| 分段数 | 6 | 9 | 8-10 |
| 情节保留 | 3/8 | ~7/8 | ≥ 6/8 |

## 涉及文件

| 文件 | 阶段 | 改动 |
|------|------|------|
| `config.yaml` | 一 | model + max_tokens |
| `internal/recap/glossary_correction.go` | 一 | 清理标注 + ASR 替换 |
| `internal/recap/handler.go` | 二 | defaultSystemPrompt + defaultUserFormat |
| `internal/recap/presets.go` | 二 | 5 个预设同步 |
| `internal/recap/provider_openai.go` | 三 | stream 支持 + 续写 |
| `internal/recap/danmaku.go` | 三 | 弹幕精简 |
| `internal/recap/transcript_summarizer.go` | 三 | 阈值调整 |
| `internal/recap/test_recap_main_test.go` | 测试 | 更新测试模型为 v4-pro |
