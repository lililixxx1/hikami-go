# 裁剪版 ffmpeg 构建说明

## 为什么需要

Hikami-Go 的 `build-windows-amd64` 会把 ffmpeg 嵌入二进制，做成"单文件开箱即用"的 Windows
分发版。但完整 ffmpeg（BtbN gpl 版）解压后约 **80MB**，绝大部分是本项目用不到的视频编解码器
（h264/h265/vp9/av1…）和无关协议（rtsp/rtmp/srt…），导致 exe 体积暴涨。

本项目对 ffmpeg 的实际用量非常窄：

| 调用路径 | 命令 | 用到的模块 |
|---------|------|-----------|
| 直播录制 `live_record` | `-i pipe:0 -vn -c:a copy out.m4a`（FLV 流抽音轨） | flv demuxer + mov muxer，**零编码器** |
| 录制分段合并 `manager.go` | `-f concat -safe 0 -i list -c copy` | concat demuxer + mov muxer |
| download 多P合并 | 同上 + ffprobe 时长探测 | concat demuxer |
| normalize 转码 | `-vn -ac 1 -ar 16000 -b:a 64k -f mp3` | **mp3 encoder** + mp3 muxer |
| importer 导入转码 | `-vn -c:a aac out.m4a` | **aac encoder** + mov muxer |

**关键洞察**：录制和合并全是流复制（`-c:a copy`），**完全不需要任何编码器**；
仅 normalize 需要 mp3 编码器、importer 需要 aac 编码器。所以裁剪空间极大——
把 80MB 砍到约 **8-12MB**（≈ 1/8）。

## 怎么构建

### 前置
- **Docker**（推荐，零环境依赖）：能 `docker run` 即可。
- 或 `--no-docker` 模式：Linux 宿主机装 `apt install gcc-mingw-w64-x86-64 make pkg-config git`。

### 一键构建
```bash
make build-ffmpeg-minimal
# 或直接：./scripts/build-ffmpeg-minimal.sh
```
产物：`internal/runtime/assets/ffmpeg.zip`（含 `bin/ffmpeg.exe` + `bin/ffprobe.exe` + `LICENSE.txt`）。

脚本会：
1. 用 `gcc:13-bookworm` 容器装 MinGW-w64 交叉工具链；
2. 从 GitHub 克隆指定 tag 的 ffmpeg 源码（默认 `n7.1`，可用 `FFMPEG_TAG=...` 改）；
3. `./configure --disable-everything` 后只白名单启用本项目所需模块；
4. `make` 编译，产物打进 `internal/runtime/assets/ffmpeg.zip`；
5. 打印 SHA256（可选填入 manifest）。

中间产物缓存在 `.tmp/ffmpeg-build/`（已 gitignore），重复运行会复用源码，只重跑 configure+make。

### 验证产物
裁剪过度会漏掉某个 demuxer/encoder，运行时报 `Unknown decoder` 之类。验证脚本逐条复刻代码里真实的
ffmpeg/ffprobe 调用参数，全绿才算产物合格。脚本自带测试素材 `scripts/sample.m4a`（1 秒正弦波 AAC，
5KB），不依赖 lavfi——因为裁剪版 ffmpeg 用 `--disable-everything` 本身不含 lavfi，无法用它合成素材。

```bash
# 在 Windows 上跑（git-bash 或 WSL）——产物是 .exe
./scripts/verify-ffmpeg-minimal.sh

# 或在 Linux 上用一份带全功能的 ffmpeg 先自测（FFMPEG_BIN 指向任意 ffmpeg 目录）
FFMPEG_BIN=/usr/bin ./scripts/verify-ffmpeg-minimal.sh
```

覆盖 7 条用例：ffprobe 时长探测、normalize 转码（⭐验证 libmp3lame 链入）、importer 转码、concat 合并、
pipe 录制、FLV 录制。其中 normalize 用例最关键——它验证裁剪版是否正确链入了 libmp3lame（ffmpeg 无
原生 mp3 编码器，写 mp3 必须靠 libmp3lame）。

## 构建产物进版本库吗？

**进。** 裁剪版很小（8-12MB），值得入库，让 `make build-windows-amd64` 开箱即用——
贡献者 clone 仓库即可直接编出 Windows 单文件版，无需本地跑 Docker。
`.gitignore` 已配置为：忽略 `internal/runtime/assets/` 下的临时文件，但放行 `ffmpeg.zip` 和 `LICENSE.txt`。

更新裁剪版时，跑一次 `make build-ffmpeg-minimal`，`git add` 新的 zip 并提交即可。

## 许可证

ffmpeg 是 **GPL** 项目（`--enable-gpl`）。本项目嵌入 ffmpeg 二进制后，分发时须遵守 GPL 条款：
- `LICENSE.txt`（ffmpeg 的 GPLv3 全文）已打包进 `ffmpeg.zip`，随二进制分发；
- 项目根目录应在分发说明里注明"包含 ffmpeg（GPLv3）"，并指向 https://ffmpeg.org/legal.html。

若需要更宽松的许可（LGPL/可闭源商用），把脚本里的 `--enable-gpl` 去掉即可——但会失去
`--enable-encoder=mp3,aac` 中的 native mp3/aac encoder 启用？**不会**：ffmpeg 原生的
mp3（mp3float）和 aac encoder 属于 LGPL 部分，不依赖 GPL 组件。去掉 `--enable-gpl` 仍可编译，
只是丢失 GPL-only 的少数编解码器（本项目用不到）。如需 LGPL 版本，编辑脚本去掉 `--enable-gpl`。

## 裁剪清单（configure 选项）

完整选项见 `build-ffmpeg-minimal.sh`，核心白名单：

```
--disable-everything
--enable-protocol=pipe
--enable-demuxer=flv,concat,mov,mp3,aac,wav,flac,ogg,matroska,asf
--enable-muxer=mp4,mov,mp3,null,matroska
--enable-encoder=mp3,aac
--enable-decoder=aac,mp3,mp3float,flac,vorbis,opus,pcm_s16le,pcm_s8
--enable-parser=aac,mpegaudio
--enable-bsf=aac_adtstoasc
```

各选项对应代码出处：
- `flv` demuxer ← `internal/live_record/ffmpeg.go:87`（`-f flv`）
- `concat` demuxer ← `internal/download/download.go:310` + `internal/live_record/manager.go:1265`
- `mov` muxer ← m4a 输出（`manager.go:769`、`importer.go:121`、`download.go:261`）
- `mp3` muxer/encoder ← `internal/normalize/normalize.go:48`（`-f mp3 -b:a 64k`）
- `aac` encoder ← `internal/importer/importer.go:42`（`-c:a aac`）
- `aac_adtstoasc` bsf ← FLV（ADTS AAC）→ m4a（ASC AAC）remux 隐式调用
- `pipe` protocol ← `internal/live_record/ffmpeg.go:90`（`-i pipe:0`）

## 不支持什么

裁剪版**不含**视频编解码器、不含网络协议（rtmp/http output 等）、不含多数无关容器。
因此：
- **不能**用它做视频转码、视频剪辑——但本项目从不做这些。
- **导入路径**（importer）若用户上传的是裁剪版不支持的格式（如 .amr、.wma 某些变体）会转码失败；
  normalize 回退路径 `findRawAudio` 同理。这些是手动触发的非关键路径，失败时用户可换格式重传，
  不影响直播录制主线。如需严格覆盖，编辑脚本的 `--enable-demuxer=` 清单后重编。

## 何时重新构建

- 想升级 ffmpeg 版本：改 `FFMPEG_TAG` 重跑，验证，提交新 zip。
- 项目新增了 ffmpeg 调用路径（用了新 demuxer/encoder）：更新 `verify-ffmpeg-minimal.sh` 加用例，
  再按需补 configure 选项重编。
- 平时**不需要**重跑——ffmpeg 是稳定工具，裁剪清单不变则产物不变。
