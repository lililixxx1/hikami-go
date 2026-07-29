# 重编 ffmpeg 补 pcm_s16le encoder

> **日期**：2026-07-28
> **目的**：修复裁剪版 ffmpeg 缺 `pcm_s16le` encoder 导致 VAD `Detect`（`-f null -`）100% 失败的生产阻断 bug。
> **方案性质**：**根本性修复**（让裁剪版 ffmpeg 支持标准 `-f null -` 惯用法，而非业务代码加 workaround 绕过）。
> **执行环境**：Linux（需 Docker 或 mingw-w64）。当前 Windows 机器无法编译，本文档供用户带去 Linux 执行。
> **预期产物**：新版 `internal/runtime/assets/ffmpeg.zip`，含 pcm_s16le encoder，verify PASS=10（原 9 + 新增 Detect 路径 case）。

---

## 一、为什么要重编（根因）

### Bug 现象

生产代码 `internal/asr/vad_processor.go:Detect()` 跑真实音频必然失败：

```
[aost#0:0] Automatic encoder selection failed
  Default encoder for format null (codec pcm_s16le) is probably disabled.
Error opening output files: Encoder not found
```

### 根因

裁剪版 ffmpeg 的 configure（`scripts/build-ffmpeg-minimal.sh:136`）：

```bash
--enable-encoder=libmp3lame,aac \      # ← 只有 2 个 encoder,缺 pcm_s16le
--enable-decoder=aac,...,pcm_s16le,pcm_s8 \  # ← decoder 有 pcm_s16le,encoder 没有
```

`-f null -` 是 ffmpeg 做"分析扫描后丢弃输出"的标准用法（`silencedetect`、`volumedetect`、`ebur128` 等都这么用），但它仍需为输出流选 encoder，默认选 `pcm_s16le` → 找不到 → 失败。

### 为什么不选代码 workaround

代码侧可加 `-c:a libmp3lame` 绕过，但：
1. **业务代码永久带着"为适配裁剪局限"的历史遗留**，换完整版 ffmpeg 后变成需解释的噪音。
2. **裁剪版 ffmpeg 连 `-f null` 都不支持，本身是裁剪清单的缺陷**——任何未来用 `-f null` 的场景都会再踩坑。
3. **体积代价极小**：`pcm.c` 是单个文件，decoder 行已启用 `pcm_s16le`（说明 pcm.c 已编译进 ffmpeg.exe），encoder 补同款只是多注册一个 encoder 结构体，增量约几 KB。

**重编是根本性修复，workaround 是永久技术债。**

---

## 二、改动清单（共 4 处）

### 改动 1：`scripts/build-ffmpeg-minimal.sh:136`（核心）

补 `pcm_s16le` 到 encoder 白名单：

```bash
# 改前
--enable-encoder=libmp3lame,aac \
# 改后
--enable-encoder=libmp3lame,aac,pcm_s16le \
```

> 可选：若想一次到位彻底免疫 `-f null` 的所有默认 encoder 场景，可补全 PCM 家族：`pcm_s16le,pcm_s24le,pcm_s32le,pcm_f32le`。但本项目只用到 `pcm_s16le`（`-f null` 默认 + wav 读写），保守起见只补这一个。

### 改动 2：`scripts/build-ffmpeg-minimal.sh` 文件头注释（可选但推荐）

在第 102 行附近的 encoder 说明段补一句，解释为什么补 pcm_s16le：

```
#    - encoder：libmp3lame(normalize，外部库)、aac(importer，原生)、pcm_s16le(-f null 惯用法必需)
#      ⚠️ pcm_s16le 不是业务编码需求，而是让 -f null（分析扫描后丢弃输出）可用——
#      silencedetect/volumedetect 等 analysis filter 都用 -f null，默认选 pcm_s16le encoder，
#      缺它会导致 VAD Detect 等功能失败（2026-07-28 实测发现）。
```

### 改动 3：`scripts/verify-ffmpeg-minimal.sh` 新增 case 7c（防回归）

在 case 7b 之后、汇总之前补 Detect 路径的验证：

```bash
# 7c. VAD Detect 路径 (-f null -，生产 vad_processor.go:Detect 用的命令)
#     这是 2026-07-28 实测发现的 bug 防线：裁剪版必须支持 -f null - 默认选 pcm_s16le encoder
if "${FFMPEG}" -y -hide_banner -loglevel warning -i "${SAMPLE}" \
    -af "silencedetect=noise=-40dB:d=0.1" \
    -f null - 2>"${ERRLOG}"; then
  ok "VAD Detect 路径 (-f null -): pcm_s16le encoder 可用（裁剪版符合标准 ffmpeg 行为）"
else
  fail "VAD Detect 路径失败(检查 build-ffmpeg-minimal.sh 的 --enable-encoder 是否含 pcm_s16le)"
fi
```

**不加 `-c:a libmp3lame`**——这正是测试点：验证裁剪版 ffmpeg **自身**支持标准 `-f null -`，而不是业务代码绕过。

### 改动 4：`internal/runtime/ffmpeg_manifest.go:42`（bump version 让旧缓存失效）

```go
// 改前
const version = "embedded-minimal-7.x-vad"
// 改后
const version = "embedded-minimal-7.x-vad2"
```

**为什么必须 bump**：用户已运行过当前 exe，`data/.runtime/ffmpeg/windows-amd64/embedded-minimal-7.x-vad/` 已有旧 ffmpeg.exe 缓存。程序启动时按 version 找目录，version 不变就直接复用旧缓存，新 zip 解不出来。bump 到 `-vad2` 强制重新解包。

> 也可以手动删 `data/.runtime/ffmpeg/` 和 `hikami-go/.runtime/ffmpeg/` 两个缓存目录代替 bump，但 bump version 更稳健（覆盖所有用户、所有缓存路径）。

---

## 三、Linux 执行步骤

### 前置：同步代码到 Linux 机器

把改动 1–4 应用后的仓库同步到 Linux 机器（git push/pull 或 rsync）。**4 处改动都要带上**，否则重编出来的 ffmpeg 还是旧的，或 verify 跑不到新 case。

### Step 1：彻底清缓存（⚠️ 关键，上次重编就栽在这）

```bash
cd /path/to/hzm
rm -rf .tmp/ffmpeg-build/
```

**为什么必须清**：`build_inner()` 第 64/84 行有"复用已编译产物"的缓存逻辑——如果 `.tmp/ffmpeg-build/` 里有上次的 `ffmpeg-src/` 和 `lame-install/`，脚本会跳过 configure + make，**直接复用旧 ffmpeg.exe**。上次（2026-07-28 第一次重编）就是这个坑：新 zip 里 ffmpeg.exe 时间戳是 Jul 13（旧缓存），ffprobe/License 才是 Jul 28（新的）。详见 `scripts/FFMPEG-BUILD-DEBUG-2026-07-28.md`。

清缓存后，脚本会重新 clone ffmpeg 源码 + 重新 configure（带新的 `pcm_s16le`）+ 重新 make，产出的 ffmpeg.exe 才含新 encoder。

### Step 2：重编

```bash
# Docker 模式（推荐，环境隔离）
./scripts/build-ffmpeg-minimal.sh

# 或 --no-docker 模式（宿主机已装 mingw-w64 + libmp3lame）
./scripts/build-ffmpeg-minimal.sh --no-docker
```

**预期输出末尾**：

```
==> [inner] 列出启用的关键能力（抽查裁剪是否生效）
ffmpeg version n7.1 Copyright (c) 2000-2024 the FFmpeg developers
--- encoders（确认 libmp3lame + aac + pcm_s16le）---
 A..... aac                  AAC (Advanced Audio Coding)
 A....D libmp3lame           libmp3lame MP3 (MPEG audio layer 3) (codec mp3)
 A..... pcm_s16le            PCM signed 16-bit little-endian          ← 必须看到这行
==> 打包完成：internal/runtime/assets/ffmpeg.zip
==> SHA256：
<新 SHA256，与基线 00fef86b... 不同>
```

**校验点**：
- encoders 列表里**必须出现 `pcm_s16le`**（上面那行）。如果没有，说明改动 1 没生效或缓存没清。
- SHA256 与旧版 `00fef86b22c1a4dbbffb597ef2606b3f4baf04779f5c4b92dfc3134896ec0ddb` **必须不同**。相同说明没真重编。

### Step 3：把新 ffmpeg.zip 拷回 Windows 机器

```bash
# 在 Linux 上
sha256sum internal/runtime/assets/ffmpeg.zip   # 记下新 SHA
# 用你习惯的方式（scp/rsync/U盘/共享目录）把 internal/runtime/assets/ffmpeg.zip 拷到 Windows 机器
```

Windows 机器上放到 `C:\Users\Administrator\Desktop\ccc\hzm\internal\runtime\assets\ffmpeg.zip`（覆盖旧的）。

### Step 4：Windows 机器上验证

```bash
cd /c/Users/Administrator/Desktop/ccc/hzm

# 4a. 确认 zip 内容时间戳是今天(2026-07-28 或之后),且两个 .exe 时间戳一致
unzip -l internal/runtime/assets/ffmpeg.zip
# 期望:bin/ffmpeg.exe 和 bin/ffprobe.exe 的 Date/Time 都是编译当天,不是 Jul 13

# 4b. 跑 verify(含新增 case 7c)
bash scripts/verify-ffmpeg-minimal.sh
# 期望:PASS=10 FAIL=0(原 9 + 新 case 7c)
#   case 7c 显示 "VAD Detect 路径 (-f null -): pcm_s16le encoder 可用"

# 4c. 直接复现 bug 命令,确认修复
data/.runtime/ffmpeg/windows-amd64/embedded-minimal-7.x-vad2/bin/ffmpeg.exe \
    -hide_banner -i data/live_20260630_230204/asr/audio.asr.mp3 \
    -af silencedetect=noise=-40dB:d=2 -f null - 2>&1 | head -5
# 期望:exit=0, stderr 含 "silence_start: 0" 等 22 行
```

### Step 5：重编 hikami.exe

```bash
# 清旧运行时缓存(bump 了 version 到 -vad2,程序会自动建新目录,但手动清更保险)
rm -rf data/.runtime/ffmpeg hikami-go/.runtime/ffmpeg

# 重编含 ffmpeg 嵌入的 Windows 产物
GOOS=windows GOARCH=amd64 go build -tags embed_ffmpeg,embedded_web -o hikami-windows-amd64-ffmpeg.exe ./cmd/hikami

# 启动验证(看 ffmpeg resolved 路径是 -vad2)
./hikami-windows-amd64-ffmpeg.exe -config config.yaml 2>&1 | head -5
# 期望日志:"ffmpeg resolved" source=embedded 路径含 embedded-minimal-7.x-vad2
```

### Step 6：跑 VAD 端到端验证（可选但推荐）

重编后，VAD 真正能跑通了。用 `plans/VAD-REALDATA-REPORT-2026-07-28.md` 第五节"未能完成"的验证程序重跑：

```bash
# 按报告重建 /tmp/vad-test/verify_real.go(调生产 Detect + BuildSilenceMap + Trim + Remap)
# 期望:
#   1. Detect 成功(不再 "Encoder not found")
#   2. BuildSilenceMap + Trim 一致性 ≤50ms 容差通过
#   3. 反向映射抽检合理(trimmed 中点 → original 中点附近)
```

---

## 四、常见问题排查

### Q1：重编后 encoders 列表还是没有 pcm_s16le

**原因**：缓存没清干净（`.tmp/ffmpeg-build/ffmpeg-src/` 复用）或改动 1 没应用。
**排查**：
```bash
ls -la .tmp/ffmpeg-build/ffmpeg-src/ffbuild/config.mtime 2>/dev/null  # 应不存在(已清)
grep "enable-encoder" .tmp/ffmpeg-build/ffmpeg-src/ffbuild/config.conf 2>/dev/null  # 查 configure 实际参数
```
**修复**：`rm -rf .tmp/ffmpeg-build/` 后重跑。

### Q2：新 zip 的 ffmpeg.exe 时间戳是 Jul 13（旧缓存）

**原因**：Step 1 缓存清理没执行，或执行后又被某步恢复。
**这是上次重编踩的坑**，详见 `scripts/FFMPEG-BUILD-DEBUG-2026-07-28.md`。
**修复**：严格按 Step 1 清缓存，确认 `.tmp/ffmpeg-build/` 不存在后再跑 build 脚本。

### Q3：verify case 7c 还是失败

**原因**：新 ffmpeg.zip 没拷到 Windows 的 `internal/runtime/assets/`，或 manifest version 没 bump 导致程序解包了旧缓存。
**排查**：
```bash
unzip -l internal/runtime/assets/ffmpeg.zip  # 确认是新 zip(时间戳今天)
grep "const version" internal/runtime/ffmpeg_manifest.go  # 确认是 -vad2
ls data/.runtime/ffmpeg/windows-amd64/  # 应只有 embedded-minimal-7.x-vad2,无旧目录
```

### Q4：build 脚本 lame 编译失败

**原因**：网络问题（sourceforge 下载 lame 源码超时）。
**修复**：重跑（脚本有"复用已编译 lame"逻辑，lame 编译成功后不会重复）。或手动下载 lame-3.100.tar.gz 放到 `.tmp/ffmpeg-build/`。

---

## 五、回滚方案

如果重编后出问题，回滚到当前版本：

```bash
# ffmpeg.zip 用 git 恢复(当前已提交版本的 assets)
git checkout HEAD -- internal/runtime/assets/ffmpeg.zip

# manifest version 回退
# internal/runtime/ffmpeg_manifest.go:42 改回 "embedded-minimal-7.x-vad"

# 清运行时缓存让旧 zip 重新解包
rm -rf data/.runtime/ffmpeg hikami-go/.runtime/ffmpeg

# 重编
GOOS=windows GOARCH=amd64 go build -tags embed_ffmpeg,embedded_web -o hikami-windows-amd64-ffmpeg.exe ./cmd/hikami
```

---

## 六、附录：本次改动汇总（给 AI 执行用的简洁版）

如果你让 AI 执行这个重编，给它这段：

---

**任务**：在 Linux 上重编 Hikami-Go 的裁剪版 ffmpeg，补 `pcm_s16le` encoder。

**仓库**：`C:\Users\Administrator\Desktop\ccc\hzm`，分支 `feat/vad-2026-07-27`。

**背景**：裁剪版 ffmpeg（`scripts/build-ffmpeg-minimal.sh`）的 `--enable-encoder=libmp3lame,aac` 缺 `pcm_s16le`，导致 `-f null -`（VAD silencedetect 扫描用的标准惯用法）找不到 encoder 失败，VAD 功能 100% 失效。`pcm.c` 已编译进 ffmpeg（decoder 行有 `pcm_s16le`），补 encoder 只多几 KB。

**4 处代码改动**（先在 Windows 改好同步到 Linux，或直接 Linux 上改）：
1. `scripts/build-ffmpeg-minimal.sh:136`：`--enable-encoder=libmp3lame,aac` → `--enable-encoder=libmp3lame,aac,pcm_s16le`
2. `scripts/build-ffmpeg-minimal.sh` 文件头注释补 pcm_s16le 说明（可选）
3. `scripts/verify-ffmpeg-minimal.sh` case 7b 后补 case 7c：测 `-f null -` 不带 `-c:a` 能否成功（防回归）
4. `internal/runtime/ffmpeg_manifest.go:42`：version `embedded-minimal-7.x-vad` → `embedded-minimal-7.x-vad2`（让旧缓存失效）

**Linux 执行**：
```bash
cd hzm
rm -rf .tmp/ffmpeg-build/                          # ⚠️ 必须清,否则复用旧缓存
./scripts/build-ffmpeg-minimal.sh                  # 产出 internal/runtime/assets/ffmpeg.zip
# 验证:encoders 列表含 pcm_s16le,SHA256 与旧版 00fef86b... 不同
```

**产物**：`internal/runtime/assets/ffmpeg.zip`，拷回 Windows 机器覆盖旧版。

**Windows 验收**：
```bash
bash scripts/verify-ffmpeg-minimal.sh              # 期望 PASS=10 FAIL=0
# case 7c 显示 "VAD Detect 路径 (-f null -): pcm_s16le encoder 可用"
```
