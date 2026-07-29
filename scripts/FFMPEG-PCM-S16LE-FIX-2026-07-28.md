# 修复方案：裁剪版 ffmpeg 缺 pcm_s16le encoder 导致 VAD Detect 失败

> **日期**：2026-07-28
> **触发**：VAD 真实数据实测（`plans/VAD-REALDATA-REPORT-2026-07-28.md`）发现生产 `VADProcessor.Detect()` 在裁剪版 ffmpeg 上 100% 失败。
> **结论先行**：**推荐方案 A（代码侧 1 行改动），无需重编 ffmpeg**。方案 B（重编 ffmpeg）作为备选，仅在不愿改代码时用。

---

## 一、Bug 现象

直接调用生产代码 `VADProcessor.Detect()` 跑真实音频 `data/live_20260630_230204/asr/audio.asr.mp3`：

```
vad detect: ffmpeg failed: exit status 0xbcb1ba08: ...
[aost#0:0 @ 0000024bcb9043e0] Automatic encoder selection failed
  Default encoder for format null (codec pcm_s16le) is probably disabled.
Error opening output files: Encoder not found
```

ffmpeg 在"打开输出"阶段就 Error 退出（非零退出码），`CombinedOutput()` 返回 err → `HandleTask` fallback 决策树跳过整个 VAD 链路 → **VAD 功能完全失效**。

## 二、根因

`internal/asr/vad_processor.go:66-71` 的 Detect 命令：

```go
cmd := exec.CommandContext(ctx, p.ffmpeg,
    "-hide_banner",
    "-i", audioPath,
    "-af", fmt.Sprintf("silencedetect=noise=%s:d=%s", threshold, duration),
    "-f", "null", "-",    // ← 问题在这
)
```

`-f null` 是 ffmpeg 的"丢弃所有输出"伪 muxer，但它仍需为输出流**选一个 encoder**，默认选 `pcm_s16le`。而裁剪版 ffmpeg（`scripts/build-ffmpeg-minimal.sh`）的 configure：

```bash
# 第 136 行
--enable-encoder=libmp3lame,aac \
# 第 137 行
--enable-decoder=aac,mp3,mp3float,flac,vorbis,opus,pcm_s16le,pcm_s8 \
```

**只 enable 了 `aac` + `libmp3lame` 两个 encoder，`pcm_s16le` 仅作为 decoder 启用**（ffprobe 解析 PCM 文件用），所以 `-f null` 找不到可用 encoder 报错。

> 注：`silencedetect` 是 analysis filter，不需要真输出音频，只跑完 filter 链即可。问题纯粹是 ffmpeg muxer 选 encoder 的机制——`-f null` 仍走 encoder 协商。

## 三、影响范围

`vad_processor.go` 三个 ffmpeg/ffprobe 调用点：

| 调用点 | 行号 | 命令 | 是否受影响 |
|--------|------|------|-----------|
| **Detect** | 66 | `-af silencedetect=... -f null -` | ❌ **失败**（缺 pcm_s16le encoder） |
| probeDurationMS | 123 | `ffprobe ...` | ✓ 不调 ffmpeg |
| **Trim** | 159 | `-filter_complex ... -c:a libmp3lame -f mp3` | ✓ 已显式指定 libmp3lame |

**只有 Detect 受影响**。Trim 因为生产代码本来就写了 `-c:a libmp3lame`（看 `vad_processor.go:162` 附近），完全正常（`verify-ffmpeg-minimal.sh` case 7b 已验证）。

## 四、修复方案

### ✅ 方案 A（推荐）：代码侧加 `-c:a libmp3lame`

**改动**：`internal/asr/vad_processor.go:66-71` 加两行：

```go
cmd := exec.CommandContext(ctx, p.ffmpeg,
    "-hide_banner",
    "-i", audioPath,
    "-af", fmt.Sprintf("silencedetect=noise=%s:d=%s", threshold, duration),
    "-c:a", "libmp3lame",   // ← 新增:裁剪版无 pcm_s16le encoder,必须显式指定
    "-f", "null", "-",
)
```

**已实测验证**：
```bash
ffmpeg -hide_banner -i audio.asr.mp3 -af silencedetect=noise=-40dB:d=2 -c:a libmp3lame -f null -
# exit=0, 正确扫到 22 个静音区间
```

**为什么选 libmp3lame 而不是补 pcm_s16le 到 build 脚本**：
1. **零重编成本**：代码 1 行改动 + 跑测试，不需要用户再上 Linux 重编 ffmpeg。
2. **零体积增加**：libmp3lame 已是裁剪版必装 encoder（normalize 的 `-f mp3` 依赖它），不增体积。
3. **生产代码已用同款**：Trim 方法（line 159）就是用 `-c:a libmp3lame`，Detect 跟它对齐，风格统一。
4. **pcm_s16le 是体积大头**：补它会带进一组 PCM encoder 族，违背裁剪初衷。

**测试影响**：
- `vad_processor_test.go`：**无影响**。该测试用 mock ffmpeg（不真调 ffmpeg），没有命令参数顺序断言。已 grep 确认无 `null`/`libmp3lame`/`exec.Command` 相关断言。
- `vad_integration_test.go`：**无影响**。集成测试用真 ffmpeg 但跑的是 `-f null -c:a pcm_s16le`？需确认——见下文"实施步骤"。

**需同步改的配套**：
- `scripts/verify-ffmpeg-minimal.sh`：case 7 当前只覆盖 Trim 路径（7b 用 `-f mp3`）。**建议补 case 7c**：`-c:a libmp3lame -f null -`（Detect 路径），防止未来 build 脚本又裁错 encoder 时回归无报警。

### 备选方案 B：重编 ffmpeg 补 pcm_s16le encoder

如果不愿改业务代码，可在 `scripts/build-ffmpeg-minimal.sh:136` 补 encoder：

```bash
# 第 136 行,从
--enable-encoder=libmp3lame,aac \
# 改为
--enable-encoder=libmp3lame,aac,pcm_s16le \
```

然后用户上 Linux 重编：
```bash
rm -rf .tmp/ffmpeg-build/    # 清缓存,见 scripts/FFMPEG-BUILD-DEBUG-2026-07-28.md 的坑
./scripts/build-ffmpeg-minimal.sh
```

**缺点**：
- 需要用户再上 Linux 重编一次（上次重编踩了缓存坑，见 `scripts/FFMPEG-BUILD-DEBUG-2026-07-28.md`）。
- `pcm_s16le` encoder 会带进 PCM encoder 族（通常 +0.5–1MB 体积，PCM encoder 实现简单但 codec 注册会拉关联）。
- 只解决 ffmpeg 侧，不解决"裁剪版 muxer 默认选 encoder 的脆弱性"——未来加新 filter 用 `-f null` 还会踩同样的坑。

**不推荐 B**，除非有强理由不愿动 Go 代码。

---

## 五、实施步骤（方案 A）

### Step 1：改 vad_processor.go

`internal/asr/vad_processor.go:66-71`，加 `-c:a libmp3lame`（见第四节代码）。

同步改注释：第 64 行 `// ffmpeg -af silencedetect=noise=-40dB:d=2 -f null -` → `// ffmpeg -af silencedetect=noise=-40dB:d=2 -c:a libmp3lame -f null -`，并加一行说明"裁剪版无 pcm_s16le encoder，必须显式指定 libmp3lame"。

### Step 2：确认 integration 测试不受影响

读 `internal/asr/vad_integration_test.go`，看它跑 Detect 时用的是什么 ffmpeg 命令：
- 如果它用的是**完整版 ffmpeg**（系统 PATH 的、或 testdata 自带的），不受影响（完整版有 pcm_s16le）。
- 如果它用的是**裁剪版 ffmpeg**（`embedded-minimal-7.x-vad`），改前也会失败——需确认这个测试当前是 skip 还是 fail。

### Step 3：补 verify 脚本 case 7c

`scripts/verify-ffmpeg-minimal.sh`，在 case 7b 后补：

```bash
# 7c. Detect 路径(生产 vad_processor.go:Detect 用的命令)
#     必须显式 -c:a libmp3lame,因为裁剪版无 pcm_s16le encoder,-f null 默认选它必失败
if "${FFMPEG}" -y -hide_banner -loglevel warning -i "${SAMPLE}" \
    -af "silencedetect=noise=-40dB:d=0.1" \
    -c:a libmp3lame -f null - 2>"${ERRLOG}"; then
  ok "VAD Detect 路径 (-c:a libmp3lame -f null): 裁剪版可用"
else
  fail "VAD Detect 路径失败(检查 libmp3lame encoder 是否启用)"
fi
```

### Step 4：重跑实测验证

修完后重跑 `plans/VAD-REALDATA-REPORT-2026-07-28.md` 第五节"端到端验证未能完成"的部分：
- 用 `/tmp/vad-test/verify_real.go`（实测时写的程序，已删，按报告重建）跑 BuildSilenceMap + Trim + Remap，确认在真实 62 分钟音频上的一致性（≤50ms 容差）。

### Step 5：跑测试 + 重编 exe

```bash
go test ./internal/asr/...                    # 确认测试全绿
go vet ./internal/asr/...
# 重编含 ffmpeg 的 Windows 产物（VAD 真正要用的）
GOOS=windows GOARCH=amd64 go build -tags embed_ffmpeg,embedded_web -o hikami-windows-amd64-ffmpeg.exe ./cmd/hikami
```

---

## 六、为什么不一开始就发现？

- `verify-ffmpeg-minimal.sh` case 7 原本设计时只测了"filter 是否在列表里"（7a）+ "Trim 端到端"（7b），**漏了 Detect 路径的 `-f null`**。这是验证用例覆盖不全的教训。
- 单元测试 `vad_processor_test.go` 用 mock ffmpeg，**不真调 ffmpeg**，所以测不出 encoder 缺失。
- 集成测试 `vad_integration_test.go` 用合成音频 + 可能用完整版 ffmpeg，**没复现裁剪版的 encoder 缺失场景**。

**防线改进**：方案 A Step 3 补 case 7c 后，未来重编 ffmpeg 时 verify 会立刻发现 Detect 路径是否还能跑。

---

## 七、附录：实测命令复现

```bash
# 复现 bug（当前裁剪版,失败）
data/.runtime/ffmpeg/windows-amd64/embedded-minimal-7.x-vad/bin/ffmpeg.exe \
    -hide_banner -i data/live_20260630_230204/asr/audio.asr.mp3 \
    -af silencedetect=noise=-40dB:d=2 -f null -
# → exit 非零, "Encoder not found"

# 修复后（方案 A,成功）
data/.runtime/ffmpeg/windows-amd64/embedded-minimal-7.x-vad/bin/ffmpeg.exe \
    -hide_banner -i data/live_20260630_230204/asr/audio.asr.mp3 \
    -af silencedetect=noise=-40dB:d=2 -c:a libmp3lame -f null -
# → exit=0, 22 个 silence_start
```
