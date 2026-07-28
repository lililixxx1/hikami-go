# 任务:在 Linux 上重编译裁剪版 ffmpeg(含 VAD filter)

> 这是一份**自包含的任务说明书**,可直接丢给 AI agent(或人)在 Linux 机器上执行。
> 包含完整前因后果、环境要求、精确步骤、验收标准、失败排查。

---

## 一、前因后果(为什么要做这件事)

### 项目背景
Hikami-Go 是一个 Go 后端 + Vue 前端的 B 站直播回顾自动生成服务。流水线:
**直播录制 → normalize(转 mp3) → ASR(语音转写,DashScope) → AI 回顾生成 → 发布**。

其中 **ASR 阶段调用阿里云 DashScope**,按**音频总时长计费**(¥1.2-1.5/小时)。直播录音含大量
静音段(主播离开/BGM 间奏/换 P/中场休息),实测占 3-10%。

### VAD 功能(已实现,代码已合入)
2026-07-27 实现了 **VAD(Voice Activity Detection)静音裁剪**:上传 ASR 前先用 ffmpeg 裁掉
长静音段,减少计费时长。实测 -40dB/2s 参数:电台直播省 10%、游戏直播省 2.8%,**零真实内容损失**
(6 轮真实数据验证,4333 段反向映射 0ms 漂移)。

架构(详见 `plans/plan-vad-2026-07-27.md`):
- `audio.asr.mp3` 保持原始时间线不变(单一真相源)
- 产出 `audio.asr.trimmed.mp3` + `silence_map.json`(静音映射表)
- ASR 返回后,用 silence_map 把 segments 从 trimmed 时间线**反向映射**回原始时间线
- 所有下游(recap/glossary/danmaku)零改动

VAD 用 ffmpeg 两个能力:
1. `silencedetect` filter:扫描静音区间
2. `atrim` + `asetpts` + `concat` filter:按静音区间精确裁剪,保留 padding 缓冲

### 问题:当前嵌入的 ffmpeg 缺这些 filter
本项目内嵌一个**裁剪版 ffmpeg**(用 `--disable-everything` + 白名单编译,只含项目用到的
demuxer/muxer/encoder,体积 7-8MB,而非完整版 80MB)。当前嵌入版是 **2026-07-13 编译的**,
白名单只有 `aresample,aformat,anull` 三个 filter,**缺 silencedetect/asetpts/concat**
(atrim 已有)。

**后果**:VAD 功能虽已实现,但嵌入版 ffmpeg 跑不了 → VAD 自动 fallback 原始音频
(写 WARN 日志,功能降级但不崩)→ **降本效果归零**。

### 为什么不在 Windows 上编
`scripts/build-ffmpeg-minimal.sh` 是 **MinGW 交叉编译脚本**(产 Windows x64 静态 .exe)。
- Windows 本机编 MinGW 容易踩 nasm/yasm/路径坑
- 开发机(Windows)没装 Docker,也没装 gcc/MinGW
- **Linux 是最干净的交叉编译环境**(Docker 镜像或原生 MinGW-w64 都稳定)

### 本次任务
在 Linux 机器上重编译裁剪版 ffmpeg,把 silencedetect/atrim/asetpts/concat 四个 filter 编进去,
产出新的 `internal/runtime/assets/ffmpeg.zip`,拷回 Windows 替换旧版。

---

## 二、环境要求

### Linux 机器(任选一种)

**方案 A:Docker 模式(推荐,最干净)**
- 任意 Linux 发行版(Ubuntu/Debian/CentOS 均可)
- Docker 已安装,当前用户能访问 docker daemon(`docker ps` 不报错)
- 网络:能拉 `gcc:13-bookworm` 镜像 + 能访问 GitHub(SourceForge 下 lame 源码)

**方案 B:--no-docker 模式(无 Docker 时)**
- Debian/Ubuntu 系:`sudo apt install gcc-mingw-w64-x86-64 make wget pkg-config git zip`
- RHEL/CentOS 系:`sudo dnf install mingw64-gcc make wget pkgconf git zip`
- 网络:能访问 GitHub + SourceForge

### 代码
- 整个 hzm 仓库(已包含改好的编译脚本 + 验证脚本)
- 同步方式:git clone,或从 Windows rsync

---

## 三、精确执行步骤

### 步骤 1:在 Linux 机器上拿到代码

```bash
# 方式一:git clone(推荐,如果仓库已 push)
git clone <仓库地址> hzm
cd hzm

# 方式二:从 Windows rsync(如果还没 push)
# 在 Windows 上跑:
# rsync -av --exclude='data' --exclude='*.db' --exclude='.runtime' \
#   --exclude='hikami-go' --exclude='node_modules' \
#   /c/Users/Administrator/Desktop/ccc/hzm/ user@linux:/home/user/hzm/
# 然后在 Linux 上:cd /home/user/hzm
```

**确认关键文件存在**:
```bash
ls -la scripts/build-ffmpeg-minimal.sh scripts/verify-ffmpeg-minimal.sh
ls -la internal/runtime/assets/ffmpeg.zip   # 旧版,7.3MB,将被替换
ls -la scripts/sample.m4a                    # 验证脚本依赖的测试音频(5KB)
```

### 步骤 2:确认编译脚本的白名单已含 VAD filter

**这一步是检查,不是修改**。脚本应该已经改好了,确认即可:

```bash
grep "enable-filter" scripts/build-ffmpeg-minimal.sh
```

**期望输出**(必须含 silencedetect/atrim/asetpts/concat):
```
    --enable-filter=aresample,aformat,anull,silencedetect,atrim,asetpts,concat \
```

如果输出**不含**这 4 个 filter,说明代码没同步对,先 `git pull` 或重新 rsync。

### 步骤 3:跑编译脚本

**方案 A(Docker 模式,推荐)**:
```bash
./scripts/build-ffmpeg-minimal.sh
```

**方案 B(--no-docker 模式)**:
```bash
# 先装依赖(Debian/Ubuntu)
sudo apt update && sudo apt install -y gcc-mingw-w64-x86-64 make wget pkg-config git zip

# 跑编译
./scripts/build-ffmpeg-minimal.sh --no-docker
```

**预计耗时**:首次 15-30 分钟(下载 ffmpeg n7.1 源码 + lame 3.100 源码 + 编译);
二次跑(复用 `.tmp/ffmpeg-build/` 缓存)5-10 分钟。

**脚本会自动**:
1. (Docker 模式)拉 `gcc:13-bookworm` 镜像,容器内装 mingw-w64
2. 交叉编译 libmp3lame 静态库(normalize 的 `-f mp3` 必需)
3. clone ffmpeg n7.1 源码
4. configure(`--disable-everything` + 白名单,**含 VAD 4 个 filter**)
5. make 产出 `ffmpeg.exe` + `ffprobe.exe`(Windows x64 静态)
6. 打包成 `internal/runtime/assets/ffmpeg.zip`(约 7-8MB)
7. 打印 SHA256

### 步骤 4:验证产物合格

**在 Linux 上跑验证脚本**(脚本支持 Linux,会自动用 Wine 或直接检查 zip 内容):

```bash
./scripts/verify-ffmpeg-minimal.sh
```

> 注:验证脚本的 ffmpeg 命令是在 Windows 上跑的(.exe),Linux 上跑可能因无法执行 .exe 而失败。
> **替代方案**:用 `unzip -l` 看 zip 内容 + 在 Windows 上跑验证(见步骤 6)。

**最低限度检查**(Linux 上必做):
```bash
# 1. zip 存在且非空
ls -lh internal/runtime/assets/ffmpeg.zip
# 期望:约 7-8MB(旧版 7.3MB,新版含 filter 略大但不应超过 10MB)

# 2. zip 内含 ffmpeg.exe + ffprobe.exe + LICENSE
unzip -l internal/runtime/assets/ffmpeg.zip
# 期望:bin/ffmpeg.exe + bin/ffprobe.exe + LICENSE.txt + LICENSE.lame.txt

# 3. 打印 SHA256(记录下来,可选填到 manifest)
sha256sum internal/runtime/assets/ffmpeg.zip
```

### 步骤 5:把新 ffmpeg.zip 拷回 Windows

**方式一:git(推荐)**
```bash
git add internal/runtime/assets/ffmpeg.zip
git commit -m "chore(runtime): rebuild ffmpeg with VAD filters (silencedetect/atrim/asetpts/concat)

Recompiled on Linux to add 4 filters required by ASR VAD (plans/plan-vad-2026-07-27.md):
- silencedetect: scan silence intervals
- atrim + asetpts + concat: precise cut with padding (replaces silenceremove per qoder C-1)

Old embedded version (2026-07-13) lacked these, causing VAD to fallback to original audio.
Manifest version bumped to embedded-minimal-7.x-vad for cache invalidation."
git push
```

然后在 Windows 上:`git pull`

**方式二:scp**
```bash
scp internal/runtime/assets/ffmpeg.zip user@windows-machine:/c/Users/Administrator/Desktop/ccc/hzm/internal/runtime/assets/
```

### 步骤 6:在 Windows 上验证 + 重编 hikami.exe

```bash
cd /c/Users/Administrator/Desktop/ccc/hzm

# 1. 验证新 ffmpeg.zip(用例 7 是 VAD,必须 PASS)
./scripts/verify-ffmpeg-minimal.sh
# 期望:PASS=8 FAIL=0,特别是:
#   ✅ VAD filters: silencedetect + atrim + asetpts + concat 全部可用
#   ✅ VAD atrim+concat 端到端: 产出到 vad_trimmed.mp3

# 2. 清理旧 ffmpeg 缓存(强制重新解包新版)
rm -rf data/.runtime/ffmpeg/windows-amd64/embedded-minimal-7.x
rm -rf hikami-go/.runtime/ffmpeg/windows-amd64/embedded-minimal-7.x
# (新缓存目录是 embedded-minimal-7.x-vad,由 manifest version 决定)

# 3. 重编 hikami.exe(内嵌新 ffmpeg)
make build-windows-amd64
# 或:go build -tags 'embed_ffmpeg,embedded_web' -o ./hikami.exe ./cmd/hikami

# 4. 启动验证 VAD 真能用
./hikami.exe -config config.yaml
# 另开终端跑一场 ASR,看日志应有:
#   INFO vad: trimmed session_id=... orig_ms=... trimmed_ms=... ratio=0.92
#   INFO vad: remapped segments to original timeline ...
# 若看到 WARN vad: fallback to original audio 则说明 ffmpeg 还是不支持,检查步骤 5
```

---

## 四、验收标准

### 必须 100% 满足才算完成

1. ✅ `internal/runtime/assets/ffmpeg.zip` 体积 7-10MB(远小于完整版 80MB)
2. ✅ zip 内含 `bin/ffmpeg.exe` + `bin/ffprobe.exe` + LICENSE
3. ✅ Windows 上跑 `./scripts/verify-ffmpeg-minimal.sh` → **PASS=8 FAIL=0**
4. ✅ 特别是用例 7(VAD filters)两个子项都 PASS:
   - `VAD filters: silencedetect + atrim + asetpts + concat 全部可用`
   - `VAD atrim+concat 端到端: 产出到 vad_trimmed.mp3`
5. ✅ `hikami.exe` 重编成功(约 28MB),启动正常
6. ✅ 跑一场 ASR,日志出现 `INFO vad: trimmed` 而非 `WARN vad: fallback`

---

## 五、失败排查

### 问题 1:`docker: command not found`
→ 装 Docker:`curl -fsSL https://get.docker.com | sudo sh`,或改用 `--no-docker` 模式。

### 问题 2:`x86_64-w64-mingw32-gcc: command not found`(--no-docker 模式)
→ `sudo apt install gcc-mingw-w64-x86-64`(Debian/Ubuntu)。

### 问题 3:`lame configure 失败` / 下载卡住
→ SourceForge 下载不稳。手动下 lame 3.100:
```bash
cd .tmp/ffmpeg-build
wget https://downloads.sourceforge.net/project/lame/lame/3.100/lame-3.100.tar.gz
tar xzf lame-3.100.tar.gz
```
然后重跑脚本(会复用已下载的源码)。

### 问题 4:`ffmpeg make 失败`(汇编/nasm 错误)
→ 脚本已用 `--disable-asm` 规避。若仍出现,检查是不是有人改过脚本去掉了这个 flag。
正常不应出现。

### 问题 5:网络慢/超时
→ 配代理:
```bash
export https_proxy=http://your-proxy:port
export http_proxy=http://your-proxy:port
./scripts/build-ffmpeg-minimal.sh
```

### 问题 6:产物体积异常(>15MB 或 <5MB)
→ >15MB 说明裁剪失效(可能 --disable-everything 没生效);<5MB 说明编译不完整。
重新清理 `.tmp/ffmpeg-build/` 后重跑:
```bash
rm -rf .tmp/ffmpeg-build
./scripts/build-ffmpeg-minimal.sh
```

### 问题 7:Windows 上验证脚本用例 7 仍失败
→ 说明 zip 没替换成功,或缓存没清理。
```bash
# 确认 zip 是新的(看修改时间 + SHA256)
ls -la internal/runtime/assets/ffmpeg.zip
# 清理所有 ffmpeg 缓存
rm -rf data/.runtime/ffmpeg hikami-go/.runtime/ffmpeg .runtime
# 重跑验证
./scripts/verify-ffmpeg-minimal.sh
```

---

## 六、关键文件索引

| 文件 | 作用 |
|------|------|
| `scripts/build-ffmpeg-minimal.sh` | 编译脚本(Docker / --no-docker 双模式) |
| `scripts/verify-ffmpeg-minimal.sh` | 验证脚本(8 个用例,含 VAD) |
| `scripts/sample.m4a` | 验证用的测试音频(5KB,1s 正弦波 AAC) |
| `scripts/README-ffmpeg-build.md` | 裁剪清单 + 代码出处对应表 |
| `internal/runtime/assets/ffmpeg.zip` | 编译产物(将被替换) |
| `internal/runtime/ffmpeg_manifest.go` | manifest(version 已 bump 为 `-vad`) |
| `plans/plan-vad-2026-07-27.md` | VAD 功能完整设计(含两轮审核记录) |

---

## 七、给 AI agent 的精简指令(可直接复制)

```
在 Linux 机器上执行以下任务:

1. 拿到 hzm 仓库代码(git clone 或已同步),cd 进仓库根目录
2. 确认 scripts/build-ffmpeg-minimal.sh 的 --enable-filter 行含 silencedetect,atrim,asetpts,concat
   (grep "enable-filter" scripts/build-ffmpeg-minimal.sh 验证)
3. 跑 ./scripts/build-ffmpeg-minimal.sh(Docker 模式,若失败改 --no-docker)
4. 编译完成后,验证产物:
   - ls -lh internal/runtime/assets/ffmpeg.zip (期望 7-10MB)
   - unzip -l internal/runtime/assets/ffmpeg.zip (期望含 bin/ffmpeg.exe + bin/ffprobe.exe)
   - sha256sum internal/runtime/assets/ffmpeg.zip (记录输出)
5. 若 Linux 能跑 .exe(Wine/WSL),跑 ./scripts/verify-ffmpeg-minimal.sh 期望 PASS=8 FAIL=0
6. 把产出的 ffmpeg.zip 拷回 Windows 仓库同路径(或 git commit + push)
7. 报告:产物 SHA256、体积、verify 结果

如果编译失败,读取 scripts/build-ffmpeg-minimal.sh 的报错日志,排查并重试。
不要修改 scripts/build-ffmpeg-minimal.sh 的 configure 白名单(已正确配置)。
不要修改 internal/runtime/ffmpeg_manifest.go(version 已 bump)。
```

---

## 八、不做的事(明确边界)

- ❌ **不要**改 `scripts/build-ffmpeg-minimal.sh` 的 configure 选项(已正确)
- ❌ **不要**改 `internal/runtime/ffmpeg_manifest.go`(version 已 bump)
- ❌ **不要**改任何 Go 代码 / 前端代码(VAD 功能已实现 + 审核通过)
- ❌ **不要**用完整版 ffmpeg 替换(80MB 会让 exe 暴涨到 100MB+,失去裁剪意义)
- ❌ **不要**在 Windows 上编 MinGW(踩坑多,Linux 更稳)

本次任务**只产出一个新的 ffmpeg.zip**,不涉及任何代码改动。
