# Hikami-Go ·你好神！Golang直播AI回顾生成工具

> 中文名「你好神」取自 **Hi-kami**（Hi=你好，kami=神）的双关。

**帮你把 B 站主播的直播音频录下来、转成文字、再自动写成一篇回顾文章，发到 B 站专栏。**

从直播结束到专栏发布，全流程在一个本地网页里点几下就能完成——不用手动剪辑、不用手写回顾、不用多个软件来回倒腾。

## 它能帮你做什么

- 🎙️ **录直播** — 直播开始自动录制音频和弹幕，错过也不怕
- 📥 **存回放** — 自动发现并下载 B 站回放（单 P / 多 P 都行），本地音频文件也能手动导入
- 🔤 **转文字** — 把录音转成带时间轴的字幕，方便后续生成回顾
- ✍️ **写回顾** — 用 AI（支持 DeepSeek / openai / 通义等）结合弹幕氛围，自动写出一篇直播回顾
- 📤 **发专栏** — 一键发布到 B 站专栏，支持草稿、自定义封面和分区
- 🗂️ **存网盘** — 录音文件自动归档到 WebDAV 网盘，不占本地空间

> 还能：按主播维护专属的术语表（人名/游戏术语不再转错）；下载、发布使用不同B站账号。

## 谁适合用

- 经常看直播、不想错过精彩内容 **B站主播粉丝**
- 想快速了解直播，快速定位精彩时刻的 **直播切片员**

## 快速开始

### 1. 准备环境

需要先装好这些（程序会调用它们）：

- **必须**：`ffmpeg`、`ffprobe`（音视频处理，缺失会启动失败）
- **可选（按需启用）**：`yt-dlp`（回放下载/发现/多 P 回退）、`rclone`（WebDAV/ASR 临时目录未配内置后端时的后备）

> 项目已用 Go 实现了下载（native）、WebDAV 上传、ASR 临时文件上传的内置后端，`yt-dlp`/`rclone` 仅在不支持的场景作 fallback。缺失只降级对应能力（启动时探测并在健康检查暴露），不影响其他功能。

### 2. 构建 & 运行

```bash
make build        # 构建前端 + 编译程序
cp config.example.yaml config.yaml
# 编辑 config.yaml，至少填 output_root（存录播的目录）
make run          # 启动
```

启动后浏览器打开 **http://127.0.0.1:6334** 就是管理界面。

### 3. 填好 AI 能力（可选但推荐）

回顾和转写需要 AI 服务，在网页「设置」里填 API Key 即可：

- **转写**：阿里云 DashScope（通义听悟）
- **写回顾**：任意 OpenAI 兼容接口 / 本地 CLI

> 默认只监听本机（127.0.0.1）。如果要从别的电脑访问，务必在 `config.yaml` 里设置 `web.admin_token` 加密码，否则任何人都能操作你的后台。

## 界面长什么样

打开网页就是一个管理后台，主要几个页面：

- **首页** — 直播状态总览、最近场次、任务进度
- **主播** — 添加 B 站主播，配置自动录制/转写/发布开关
- **场次** — 每场直播的完整生命周期：录音 → 转写 → 回顾 → 发布，点点点走完
- **回顾** — 查看/编辑 AI 写好的回顾，支持只回顾某段时间
- **设置** — AI 配置、B 站登录、术语表、网盘、发布参数

操作上就是「选中一场直播 → 点按钮一步步往下走」，全程实时进度，不用盯着命令行。

## 文档

- 📋 [完整 API 路由清单](./CLAUDE-detail/api-routes.md) — 给二次开发/接入用
- 🏗️ [前端架构说明](./docs/FRONTEND_ARCHITECTURE.md)
- 🔧 [开发指南](./CLAUDE-detail/development.md)

---

<details>
<summary><b>🛠️ 开发者信息（技术栈 / 项目结构）</b></summary>

### 技术栈

| 组件 | 选型 |
|------|------|
| 后端 | Go 1.25.0 + Gin + gorilla/websocket |
| 数据库 | SQLite（纯 Go，无需 CGO） |
| 配置 | Viper (YAML) |
| 前端 | Vue 3 + Element Plus + Vite |
| 外部工具 | ffmpeg / ffprobe（必需）；yt-dlp / rclone（可选，按需启用） |
| AI | DashScope ASR + OpenAI 兼容 / Anthropic 回顾 |

### 数据流

```
录制/下载/导入 --> 标准化 --> 转写(ASR) --> AI回顾 --> 网盘归档 --> 专栏发布
```

### 项目结构

```
cmd/hikami/           程序入口
internal/             后端各模块（config/db/handler/worker/asr/recap/publisher 等）
web/                  Vue 3 前端源码
CLAUDE-detail/        API 路由、开发、测试等详细文档
docs/                 架构与设计文档
```

### 开发命令

```bash
make web-dev       # 前端热更新开发模式
make test          # go test ./...
make fmt           # gofmt
make tidy          # go mod tidy
```

</details>

## 许可证

GPL-3.0（详见 [LICENSE](./LICENSE)）
