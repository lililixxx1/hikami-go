# hikami-windows-amd64.exe 双击闪退 — 问题与修复

> 诊断日期：2026-07-13
> 故障现象：双击 `hikami-windows-amd64.exe` 窗口一闪而过，程序退出
> 结论：**裁剪版 ffmpeg 已通过全部 7 条调用路径验证（PASS=7 FAIL=0），问题不在 ffmpeg 本身，
> 而在 exe 的 ffmpeg 路径解析逻辑与裁剪版 zip 结构不匹配。需要重新编译 exe。**

---

## 一、症状

- 双击 `hikami-windows-amd64.exe` → 控制台窗口一闪即关
- exe 同目录下出现 `hikami.db` / `hikami.db-wal` / `hikami.db-shm` / `logs/` / `hikami-go/.runtime/`（说明程序启动后走过了一段初始化）
- `logs/` 目录为空（程序没来得及写日志就崩了）

## 二、根因（已用命令行实跑 + 源码追溯双重确认）

### 2.1 命令行实跑拿到的真实报错

从 cmd 直接运行 exe（不是双击），stderr 显示：

```json
{"level":"WARN","msg":"ffmpeg auto-resolve failed",
 "error":"ffmpeg not found in archive: GetFileAttributesEx
 hikami-go\\.runtime\\ffmpeg\\windows-amd64\\.tmp-ffmpeg-374627867\\
 ffmpeg-master-latest-win64-gpl-shared\\bin\\ffmpeg.exe:
 The system cannot find the path specified."}

{"level":"ERROR","msg":"runtime dependency check failed",
 "error":"required tool ffmpeg is unavailable:
 exec: \"ffmpeg\": executable file not found in %PATH%"}
```

退出码 1，程序立即终止。**双击看到"闪退"，本质上就是这个 exit(1)，
只是控制台窗口关太快看不到报错。**

### 2.2 源码层面的根因

报错来自 `internal/runtime/ffmpeg_resolver.go` 的 `finalizeInstall`：

```go
func finalizeInstall(tmpDir, versionDir string, asset FFmpegAsset) error {
    ffmpeg := filepath.Join(tmpDir, filepath.FromSlash(asset.FFmpegPath))
    //                                 ^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^
    if err := executableFile(ffmpeg); err != nil {
        return fmt.Errorf("ffmpeg not found in archive: %w", err)   // ← 命中这行
    }
    ...
}
```

`asset.FFmpegPath` 来自 `internal/runtime/ffmpeg_manifest.go`，写死了 BtbN 完整版的目录结构：

```go
"windows-amd64": {
    Version:    "master-latest",
    ArchiveURL: "https://github.com/BtbN/FFmpeg-Builds/releases/download/latest/ffmpeg-master-latest-win64-gpl-shared.zip",
    FFmpegPath: "ffmpeg-master-latest-win64-gpl-shared/bin/ffmpeg.exe",   // ← 写死的完整版路径
    FFprobePath:"ffmpeg-master-latest-win64-gpl-shared/bin/ffprobe.exe",
    ...
}
```

### 2.3 路径不匹配示意

程序把嵌入的 zip 解到临时目录后，**死板地按 manifest 里的路径去找 ffmpeg**：

```
程序找（manifest 写死的）:
  .tmp-ffmpeg-XXX/ffmpeg-master-latest-win64-gpl-shared/bin/ffmpeg.exe   ❌ 不存在

裁剪版 zip 实际结构（顶层直接 bin/）:
  .tmp-ffmpeg-XXX/bin/ffmpeg.exe                                        ✅ 实际在这
  .tmp-ffmpeg-XXX/bin/ffprobe.exe
  .tmp-ffmpeg-XXX/LICENSE.txt
  .tmp-ffmpeg-XXX/LICENSE.lame.txt
```

完整版 BtbN zip 解压后顶层是 `ffmpeg-master-latest-win64-gpl-shared/` 这一层；
裁剪版 zip 顶层直接是 `bin/`。**打包裁剪版时只换了 `internal/runtime/assets/ffmpeg.zip`，
没同步改 `ffmpeg_manifest.go` 里的 `FFmpegPath`/`FFprobePath`**，于是 embedded
解包成功但找不到 ffmpeg → 启动检查 fatal → 闪退。

### 2.4 闪退链路（完整时序）

```
双击 exe
  → cmd/hikami/main.go 启动
  → 初始化 SQLite（hikami.db）、创建 logs/、创建 hikami-go/.runtime/
  → ResolveFFmpeg() 走 embedded 分支
  → 解包 assets/ffmpeg.zip 到 .tmp-ffmpeg-XXX/             ✅ 解包成功
  → finalizeInstall() 按 manifest 路径找 ffmpeg.exe        ❌ 找不到
  → "ffmpeg not found in archive"
  → 兜底走 downloadAndInstallFFmpeg()（要联网下 BtbN 完整版）  ❌ 服务器无对应环境/被防火墙等
  → health check: "required tool ffmpeg is unavailable"
  → logger.Error + os.Exit(1)                              💥 闪退
```

> 注：`hikami.db` 等文件时间戳是程序走过初始化的副产物，不代表 ffmpeg 解包成功。

## 三、需要修改的文件（2 处）

### 3.1 `internal/runtime/ffmpeg_manifest.go`（核心修复）

把 `windows-amd64` 的 `FFmpegPath` / `FFprobePath` / `Version` 改成裁剪版 zip 的实际结构：

```diff
 "windows-amd64": {
-    Version:       version,
-    ArchiveURL:    "https://github.com/BtbN/FFmpeg-Builds/releases/download/latest/ffmpeg-master-latest-win64-gpl-shared.zip",
+    Version:       "embedded-minimal-7.x",   // 对应交接文档里 .runtime/.../embedded-minimal-7.x 目录名
     ArchiveFormat: "zip",
-    FFmpegPath:    "ffmpeg-master-latest-win64-gpl-shared/bin/ffmpeg.exe",
-    FFprobePath:   "ffmpeg-master-latest-win64-gpl-shared/bin/ffprobe.exe",
+    ArchiveURL:    "",                        // embedded 构建不走下载；留空即可（downloadAndInstallFFmpeg 会因空 URL 直接报错而非误下完整版）
+    FFmpegPath:    "bin/ffmpeg.exe",          // ← 裁剪版 zip 实际结构
+    FFprobePath:   "bin/ffprobe.exe",
     LicenseURL:    licenseURL,
 },
```

**说明**：
- `linux-amd64` / `linux-arm64` 这两项**不需要改**（裁剪版只针对 Windows 嵌入，Linux 走系统 ffmpeg）。
- `Version` 改成 `embedded-minimal-7.x` 与交接文档"情况 2"里描述的
  `.runtime/ffmpeg/windows-amd64/embedded-minimal-7.x/bin/` 目录一致。
  （如果你裁剪版构建脚本里用的 version 标签不同，以你构建脚本实际产生的目录名为准。）
- `ArchiveURL` 留空：embedded 构建根本不走 `downloadAndInstallFFmpeg`，
  留空只是防御性——万一 embedded 解包失败回退到下载分支，空 URL 会立刻报错，
  而不是去下 80MB 完整版（那样虽然能跑但体积爆炸、且 ffmpeg 是未裁剪的）。

### 3.2 `.github/workflows/release.yml`（构建脚本，避免嵌错 ffmpeg）

当前 release.yml 的 `Download ffmpeg for Windows embed` 步骤会**从 BtbN 下载完整版** ffmpeg
塞进 `internal/runtime/assets/ffmpeg.zip`：

```yaml
- name: Download ffmpeg for Windows embed
  if: matrix.embed_ffmpeg && steps.cache-ffmpeg.outputs.cache-hit != 'true'
  run: |
    mkdir -p internal/runtime/assets
    curl -fSL -o /tmp/ffmpeg.zip "https://github.com/BtbN/FFmpeg-Builds/.../ffmpeg-master-latest-win64-gpl-shared.zip"
    unzip /tmp/ffmpeg.zip -d /tmp/ffmpeg-extract
    ...
    zip -r .../ffmpeg.zip ffmpeg-master-latest-win64-gpl-shared/   # ← 完整版，且带顶层目录
```

服务器重新编译时，**这个步骤必须换成"用裁剪版 ffmpeg.zip"**，否则即使改了 manifest，
编译出来的 exe 嵌的还是 80MB 完整版（或者 zip 结构又对不上）。

两种改法（任选其一）：

**改法 A（推荐）：在 workflow 里调用裁剪版构建脚本**

如果你的裁剪版构建脚本是 `scripts/build-ffmpeg-minimal.sh`（产出裁剪版 ffmpeg.zip），
把那个 `Download ffmpeg for Windows embed` 步骤替换成：

```yaml
- name: Build minimal ffmpeg for Windows embed
  if: matrix.embed_ffmpeg && steps.cache-ffmpeg.outputs.cache-hit != 'true'
  run: |
    bash scripts/build-ffmpeg-minimal.sh
    # 确认脚本产出的 zip 放到了 internal/runtime/assets/ffmpeg.zip
    # 且 zip 内部结构是顶层 bin/ffmpeg.exe（不带 ffmpeg-master-latest-... 那层）
    unzip -l internal/runtime/assets/ffmpeg.zip
```

**改法 B（最省事）：手动放裁剪版 zip，跳过下载步骤**

把本地已验证 PASS=7 的那个裁剪版 `ffmpeg.zip`
（`C:\Users\Administrator\Desktop\ccc\hzm\ffmpeg-verify-bundle\ffmpeg.zip`）上传到
仓库的 `internal/runtime/assets/ffmpeg.zip`，然后改 workflow：

```diff
-      key: ffmpeg-windows-shared-embed-v1
+      key: ffmpeg-windows-minimal-embed-v1   # ← 换 cache key，强制用新 zip

       ...
-      if: matrix.embed_ffmpeg && steps.cache-ffmpeg.outputs.cache-hit != 'true'
+      if: false   # ← 直接跳过下载，用仓库里已 commit 的裁剪版 zip
```

> 关键校验：构建前 `unzip -l internal/runtime/assets/ffmpeg.zip` 必须显示
> `bin/ffmpeg.exe` 和 `bin/ffprobe.exe`（**不带** `ffmpeg-master-latest-...` 前缀）。

## 四、服务器重新编译的完整步骤

```bash
# 0. 在服务器上 clone / pull 最新代码
git clone <repo> hikami-go && cd hikami-go

# 1. 改 manifest（第 3.1 节那处 diff）
#    编辑 internal/runtime/ffmpeg_manifest.go，把 windows-amd64 的
#    FFmpegPath/FFprobePath/Version/ArchiveURL 改成裁剪版结构

# 2. 准备裁剪版 ffmpeg.zip，放到 internal/runtime/assets/ffmpeg.zip
#    方式 1：跑你的裁剪编译脚本
bash scripts/build-ffmpeg-minimal.sh
#    方式 2：直接用本地已验证的那版（最快）
cp /path/to/验证过的/ffmpeg.zip internal/runtime/assets/ffmpeg.zip

# 3. 校验 zip 结构（必须顶层是 bin/，不能有 ffmpeg-master-latest-... 前缀）
unzip -l internal/runtime/assets/ffmpeg.zip
#    期望输出：
#      bin/ffmpeg.exe
#      bin/ffprobe.exe
#      LICENSE.txt
#      LICENSE.lame.txt

# 4. 构建 Windows embedded exe（Linux 上交叉编译）
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 \
  go build -tags 'embed_ffmpeg,embedded_web' -ldflags='-s -w' \
  -o dist/hikami-windows-amd64.exe ./cmd/hikami

# 5. 把 exe 拷到 Windows 测试机，从命令行实跑验证（不要双击！）
#    在 Windows cmd / Git Bash 里：
hikami-windows-amd64.exe -config config.yaml
#    期望看到日志：
#    "ffmpeg resolved ... source: embedded"
#    而不是 "ffmpeg not found in archive"
```

## 五、验证修复是否成功

修复后从 Windows 命令行运行 exe，**成功的标志**是日志里出现：

```
"msg":"ffmpeg resolved","source":"embedded",
 "path":"...\\.runtime\\ffmpeg\\windows-amd64\\embedded-minimal-7.x\\bin\\ffmpeg.exe"
```

**失败的标志**（如果还看到这两条之一，说明没改对）：

```
"ffmpeg not found in archive"   ← manifest 路径还是写死的旧路径，没改
"required tool ffmpeg is unavailable"   ← 兜底也失败
```

如果 exe 能起来并监听端口（不闪退），再用前面 3 轮验证用的
`verify-ffmpeg-minimal.sh` 跑一遍 7 条用例做最终回归即可。

## 六、附：现场证据（供复盘）

| 证据 | 值 |
|------|-----|
| exe 类型 | `PE32+ executable (console) x86-64`（console 程序，双击本就会关窗） |
| exe 大小 | 21,821,952 B（≈21 MB，符合嵌裁剪版的预期；若嵌完整版会是 80MB+） |
| 启动残留 | `hikami.db`(4KB) + `hikami.db-wal`(140KB) + `logs/`(空) + `hikami-go/.runtime/ffmpeg/windows-amd64/`(空) |
| 命令行运行退出码 | 1 |
| stderr 关键行 | `ffmpeg not found in archive: ... ffmpeg-master-latest-win64-gpl-shared\bin\ffmpeg.exe: The system cannot find the path` |
| 问题源码位置 | `internal/runtime/ffmpeg_resolver.go:finalizeInstall`（按 manifest 路径找 ffmpeg） |
| manifest 写死路径 | `internal/runtime/ffmpeg_manifest.go:44-45` |
| 裁剪版 zip 实际结构 | `bin/ffmpeg.exe`（顶层 bin/，无前缀目录） |

> 裁剪版 ffmpeg 本身已通过完整验证（`ffmpeg-verify-bundle/验证报告.md`，PASS=7 FAIL=0），
> 含 `aresample` filter 和 `ipod` muxer（.m4a 扩展名别名）。
> 只要修了 manifest 路径并重新编译，exe 即可正常启动并使用裁剪版 ffmpeg。
