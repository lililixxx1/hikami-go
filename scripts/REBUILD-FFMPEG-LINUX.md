# 在 Linux 上重编译裁剪版 ffmpeg(含 VAD filter)

**目的**:把嵌入的 `internal/runtime/assets/ffmpeg.zip` 升级到含 VAD 所需 filter
(`silencedetect` / `atrim` / `asetpts` / `concat`)的版本。当前嵌入版(2026-07-13)缺这 3 个 filter,
VAD 会自动 fallback 原始音频(功能降级但不崩)。

**为什么要在 Linux 上编**:`scripts/build-ffmpeg-minimal.sh` 是 MinGW 交叉编译脚本,出的是
**Windows x64 静态产物**(ffmpeg.exe / ffprobe.exe)。Linux 是最干净的交叉编译环境
(Docker 镜像或原生 MinGW-w64 都稳定)。Windows 本机编 MinGW 容易踩 nasm/yasm/路径坑。

---

## 方案对比

| 方案 | 环境 | 体积 | 推荐度 |
|------|------|------|--------|
| **A. Docker 模式**(脚本默认) | 装了 Docker 的 Linux | ~8MB | ⭐⭐⭐ 最干净 |
| **B. --no-docker 原生 MinGW** | 装了 mingw-w64 的 Linux | ~8MB | ⭐⭐ 无 Docker 时用 |
| C. WSL | Windows 的 WSL2 | ~8MB | ⭐ 等同 Linux Docker 方案 |

**推荐方案 A(Docker)**:一行命令搞定,无需手动装 MinGW + lame。

---

## 方案 A:Docker 模式(推荐)

### 1. 准备:Linux 机器装 Docker

```bash
# Ubuntu/Debian
curl -fsSL https://get.docker.com | sudo sh
sudo usermod -aG docker $USER
newgrp docker  # 或重新登录
docker --version  # 确认可用
```

### 2. 同步代码到 Linux

把整个仓库同步到 Linux 机器(git clone 或 rsync):

```bash
git clone <repo-url> hzm
cd hzm
# 或:rsync -av --exclude='data' --exclude='*.db' /c/Users/Administrator/Desktop/ccc/hzm/ user@linux:/home/user/hzm/
```

> **注意**:`internal/runtime/assets/ffmpeg.zip` 已在 git 里(白名单放行),Linux 上 clone 后就有旧版。

### 3. 跑编译脚本

```bash
cd hzm
./scripts/build-ffmpeg-minimal.sh
```

脚本会自动:
1. `docker run gcc:13-bookworm`(Debian 13 + GCC)
2. 容器内 `apt install mingw-w64` 交叉编译器
3. 从源码交叉编译 libmp3lame(静态库,normalize 的 -f mp3 必需)
4. 从 GitHub clone ffmpeg n7.1 源码
5. configure(裁剪 + 白名单,**已含 VAD filter**,见下)
6. make 产出 ffmpeg.exe / ffprobe.exe
7. 打包成 `internal/runtime/assets/ffmpeg.zip`(约 7-8MB)

**预计耗时**:首次约 15-30 分钟(含下载源码 + 编译);二次跑(复用 .tmp/ffmpeg-build 缓存)约 5-10 分钟。

### 4. 验证产物

脚本会打印 SHA256。然后把 `ffmpeg.zip` 拷回 Windows 验证:

```bash
# Linux 上看产物
ls -lh internal/runtime/assets/ffmpeg.zip
sha256sum internal/runtime/assets/ffmpeg.zip

# 拷回 Windows(或用 git push/pull)
scp internal/runtime/assets/ffmpeg.zip user@windows:/c/Users/Administrator/Desktop/ccc/hzm/internal/runtime/assets/
```

在 Windows 上跑验证脚本(需要 .NET 或 Git Bash):

```bash
cd /c/Users/Administrator/Desktop/ccc/hzm
./scripts/verify-ffmpeg-minimal.sh
```

**关键**:用例 7(VAD)是新加的,会验证 silencedetect + atrim+concat 都可用。全过(PASS=7 FAIL=0)才算合格。

---

## 方案 B:--no-docker 原生 MinGW(无 Docker 时用)

### 1. 装依赖

```bash
# Ubuntu/Debian
sudo apt update
sudo apt install -y gcc-mingw-w64-x86-64 make wget pkg-config git zip
# 验证
x86_64-w64-mingw32-gcc --version
```

### 2. 跑脚本(--no-docker)

```bash
cd hzm
./scripts/build-ffmpeg-minimal.sh --no-docker
```

脚本会:
1. 交叉编译 libmp3lame(从源码,自动下载)
2. clone ffmpeg n7.1
3. configure + make(用系统的 x86_64-w64-mingw32-gcc)
4. 打包 ffmpeg.zip

**预计耗时**:同方案 A。

---

## 编译后的 ffmpeg.zip 包含哪些 filter

`scripts/build-ffmpeg-minimal.sh` 的 configure 白名单(2026-07-28 VAD 升级后):

```bash
--enable-filter=aresample,aformat,anull,silencedetect,atrim,asetpts,concat
```

对应代码出处:
| filter | 用途 | 代码出处 |
|--------|------|---------|
| `aresample` / `aformat` | normalize 重采样(`-ac 1 -ar 16000`) | `internal/normalize/normalize.go:40-51` |
| `anull` | ffprobe null 输出 | `internal/asr/vad_processor.go`(Detect 用 `-f null -`) |
| `silencedetect` | **VAD 静音扫描** | `internal/asr/vad_processor.go:Detect` |
| `atrim` + `asetpts` + `concat` | **VAD 按 kept 段精确裁剪含 padding 的音频** | `internal/asr/vad_processor.go:buildAtrimConcatFilter` |

**不用 `silenceremove`**:qoder C-1 审核发现它输出无 padding,与 silence_map 不一致(29 分钟累积漂移),改用 atrim+concat。

---

## 编译失败怎么办

### 常见错误 1:`lame configure 失败`
- 原因:SourceForge 下载不稳
- 解决:手动下 lame 3.100 源码放到 `.tmp/ffmpeg-build/lame-3.100/`,重跑

### 常见错误 2:`ffmpeg make 失败`(汇编相关)
- 原因:nasm/yasm 缺失
- 解决:脚本已用 `--disable-asm` 规避,不应出现。若仍出现,检查是不是改过脚本去掉了这个 flag

### 常见错误 3:`x86_64-w64-mingw32-gcc: command not found`(--no-docker 模式)
- 解决:`sudo apt install gcc-mingw-w64-x86-64`

### 常见错误 4:网络下载慢/失败
- 解决:配代理 `export https_proxy=http://your-proxy:port` 后重跑

---

## 编译成功后的善后

### 1. manifest version 已 bump(无需改)
`internal/runtime/ffmpeg_manifest.go:38` 已改为 `embedded-minimal-7.x-vad`。
旧用户升级后会重新解包新 zip(因为缓存目录 `.runtime/ffmpeg/windows-amd64/embedded-minimal-7.x-vad/` 与旧版 `embedded-minimal-7.x/` 不同)。

### 2. 重新编译 hikami.exe
把新 ffmpeg.zip 放到 `internal/runtime/assets/` 后,在 Windows 上:

```bash
cd /c/Users/Administrator/Desktop/ccc/hzm
make build-windows-amd64   # 或:go build -tags 'embed_ffmpeg,embedded_web' -o ./hikami.exe ./cmd/hikami
```

产物 `hikami.exe` 会内嵌新 ffmpeg(含 VAD filter),体积约 28MB(几乎不变,因为 filter 代码很小)。

### 3. 验证 VAD 真能用
启动 hikami,跑一场 ASR,看日志应有:
```
INFO vad: trimmed session_id=... orig_ms=... trimmed_ms=... ratio=0.92
INFO vad: remapped segments to original timeline session_id=... segment_count=...
```
若看到 `WARN vad: fallback to original audio (processing failed)` 则说明 ffmpeg 还是不支持 filter,检查嵌入版是否真升级了。

---

## 快速命令清单(Linux 上)

```bash
# 1. 装 Docker(首次)
curl -fsSL https://get.docker.com | sudo sh && sudo usermod -aG docker $USER && newgrp docker

# 2. 同步代码(假设已 clone)
cd hzm

# 3. 编译(15-30 分钟)
./scripts/build-ffmpeg-minimal.sh

# 4. 看产物
ls -lh internal/runtime/assets/ffmpeg.zip
sha256sum internal/runtime/assets/ffmpeg.zip

# 5. 拷回 Windows(git commit + push,或 scp)
git add internal/runtime/assets/ffmpeg.zip
git commit -m "chore(runtime): rebuild ffmpeg with VAD filters (silencedetect/atrim/asetpts/concat)"
git push
```

然后在 Windows 上 `git pull` + `make build-windows-amd64` 即可。

---

## 不想编译?

临时方案:让 hikami 用系统 ffmpeg(完整版)跑 VAD。
在 `config.yaml` 配:

```yaml
ffmpeg: "D:/software/ffmpeg/ffmpeg.exe"
ffprobe: "D:/software/ffmpeg/ffprobe.exe"  # 完整版需自带 ffprobe
```

**注意**:D:\software\ffmpeg\ 只有 ffmpeg.exe 没有 ffprobe.exe(见前面检查)。VAD 的 Detect 需要 ffprobe 探测时长,缺了会 fallback。要么找个 ffprobe.exe 放同目录,要么还是走编译路线。

完整版 ffmpeg(75MB)+ 配套 ffprobe(约 80MB)也可直接打包成 zip 替换嵌入版,但 exe 会从 28MB 暴涨到 ~100MB。**不推荐**,除非你不在乎体积。
