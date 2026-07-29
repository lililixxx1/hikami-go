# ffmpeg 重编译产物异常排查(2026-07-28)

> **背景**:在 Linux 上重编译裁剪版 ffmpeg(补 VAD filter: silencedetect/atrim/asetpts/concat),
> 产出的 `ffmpeg.zip` 放到 Windows 验证失败。本文档记录异常现象 + 根因定位 + 修复步骤。
>
> **给 AI/操作者**:带上这份文档回 Linux 机器,按「三、修复步骤」重新编译。

---

## 一、异常现象

从 Linux 编译后拷回的 `ffmpeg.zip`,在 Windows 上跑 `verify-ffmpeg-minimal.sh` 结果:

```
结果:PASS=3  FAIL=6
```

**比旧版(2026-07-13 的 PASS=7)还差**,6 个用例失败:
- 用例 4(concat demuxer 合并)失败
- 用例 5(pipe 录制)失败
- 用例 7(VAD filters)失败 —— silencedetect/atrim/asetpts/concat **全缺**
- 用例 7b(VAD atrim+concat 端到端)失败

而且**所有失败用例的 stderr 都是空的**,说明 `ffmpeg.exe` 根本没正常执行(命令静默退出,无任何输出)。

## 二、根因定位(关键证据)

对比新 zip 与项目里的旧 zip:

| 文件 | 新 zip(Linux 编的) | 旧 zip(项目里的 2026-07-13) | 问题 |
|------|---------------------|------------------------------|------|
| `bin/ffmpeg.exe` | 3,715,072 字节<br/>时间戳 **2026-07-13 14:55** | 3,692,032 字节<br/>2026-07-13 14:55 | ⚠️ **时间戳是 7-13(旧),不是编译当天 7-28**;大小与旧版不同(3.7M vs 3.69M) |
| `bin/ffprobe.exe` | 时间戳 **2026-07-28 11:12** | 2026-07-13 14:55 | ✅ 这个是新的 |
| `LICENSE.txt` | 2026-07-28 11:12 | 2026-07-13 14:55 | ✅ 新的 |
| `LICENSE.lame.txt` | 2026-07-28 11:12 | 2026-07-13 14:55 | ✅ 新的 |

### 结论

**`ffmpeg.exe` 不是 2026-07-28 编译的产物**。时间戳停留在 7-13,但大小与旧版不同 → 说明这是个**来源不明的、损坏的 ffmpeg.exe**(可能是某个中间产物、缓存残留、或编译中途失败留下的半成品),**不是 configure 白名单补全后的正确产物**。

而 `ffprobe.exe` 是新的(7-28),说明编译流程部分跑通了,但 ffmpeg.exe 这一步出了问题。

### 最可能的根因

`scripts/build-ffmpeg-minimal.sh` 的编译流程里,ffmpeg 与 ffprobe 是一起 `make` 出来的(`make -j` 编译同一个 ffmpeg 源码树,产出 ffmpeg.exe + ffprobe.exe 两个二进制)。如果只有 ffprobe 是新的,说明:

1. **make 没真正重新编译 ffmpeg.exe**(可能用了旧的 .o 目标文件,链接出问题),或
2. **staging 拷贝步骤(`cp -f ffmpeg.exe staging/bin/`)拿了错的文件**(比如 WORKDIR 里残留的旧 ffmpeg.exe),或
3. **configure 实际没重新跑**(源码树 `ffmpeg-src/` 是旧的,configure 缓存的 config.mak 还是旧的白名单)

`verify` 时 stderr 全空 + 6 用例全挂 → 这个 ffmpeg.exe 是**功能严重缺失的半成品**(可能 `--disable-everything` 关了一切但白名单没补上,连 file protocol/concat demuxer 都没有),不是简单的"缺 VAD filter"问题。

## 三、修复步骤(回 Linux 操作)

### 步骤 1:彻底清理编译缓存

```bash
cd hzm  # 仓库根目录
rm -rf .tmp/ffmpeg-build/
```

这会删掉所有缓存(源码、中间产物、staging)。**必须清理**,否则会继续复用旧的 ffmpeg.exe。

### 步骤 2:重新编译,保留完整日志

```bash
./scripts/build-ffmpeg-minimal.sh 2>&1 | tee build.log
```

**预计耗时**:首次(清理后)15-30 分钟(重新下载 ffmpeg n7.1 + lame 3.100 源码 + 完整编译)。

**观察重点**(日志里找这些关键行):
```
==> [inner] configure ffmpeg          ← 必须看到这行
==> [inner] make -jN                  ← 必须看到这行,且后面无 error
==> [inner] 校验产物存在              ← 必须看到
test -s ffmpeg.exe || die ...         ← 这行不能触发 die
test -s ffprobe.exe || die ...        ← 同上
```

如果 `configure` 或 `make` 报错,日志里会明确写出,根据错误排查(见「四、常见错误」)。

### 步骤 3:验证产物时间戳(关键!)

```bash
# A. staging 里的 ffmpeg.exe 时间戳必须是今天
ls -la .tmp/ffmpeg-build/staging/bin/
# 期望:ffmpeg.exe 和 ffprobe.exe 的时间戳都是编译当天(如 Jul 28 ...),
#      绝对不能是 Jul 13 14:55

# B. 打包后的 zip 里,ffmpeg.exe 时间戳也必须是今天
unzip -l internal/runtime/assets/ffmpeg.zip
# 期望:bin/ffmpeg.exe 和 bin/ffprobe.exe 的 Date/Time 都是今天
```

**判断标准**:
- ✅ 两个 .exe 都是今天 → 编译成功,继续步骤 4
- ❌ ffmpeg.exe 还是 Jul 13 → 编译没真正跑,回步骤 2 看日志找原因

### 步骤 4:在 Linux 上验证 filter(如果可用)

如果 Linux 装了 Wine,可以直接验证新 ffmpeg.exe 的 filter:

```bash
# 用 Wine 跑(若装了 wine)
wine .tmp/ffmpeg-build/staging/bin/ffmpeg.exe -hide_banner -filters 2>&1 | grep -i "silencedetect\|atrim\|asetpts\|concat"
# 期望:输出 4 行,每个 filter 一行
```

如果没有 Wine,跳过此步,到 Windows 上验证(步骤 5)。

### 步骤 5:拷回 Windows 验证

```bash
# Linux 上记录 SHA256
sha256sum internal/runtime/assets/ffmpeg.zip
# 拷到 Windows(git 或 scp)
```

在 Windows 上:

```bash
cd /c/Users/Administrator/Desktop/ccc/hzm

# 放到正确位置
cp /path/to/new/ffmpeg.zip internal/runtime/assets/ffmpeg.zip

# 跑验证脚本
./scripts/verify-ffmpeg-minimal.sh
```

**验收标准**:
- ✅ `PASS=8 FAIL=0`
- ✅ 用例 7(VAD filters)两个子项都 PASS:
  - `VAD filters: silencedetect + atrim + asetpts + concat 全部可用`
  - `VAD atrim+concat 端到端: 产出到 vad_trimmed.mp3`

## 四、常见错误排查

### 错误 1:`ffmpeg.exe 时间戳还是旧的`(本次的根因)

**原因**:`.tmp/ffmpeg-build/` 缓存没清理,make 复用了旧目标文件 / staging 拿了旧 ffmpeg.exe。

**解决**:
```bash
rm -rf .tmp/ffmpeg-build/
./scripts/build-ffmpeg-minimal.sh
```

### 错误 2:`configure 失败`

看 build.log 里 `configure.log` 的 tail:
```bash
tail -30 .tmp/ffmpeg-build/ffmpeg-src/configure.log
```

常见原因:
- 缺 MinGW:`apt install gcc-mingw-w64-x86-64`(Docker 模式容器内自动装)
- 缺 lame 静态库:确认 `.tmp/ffmpeg-build/lame-install/lib/libmp3lame.a` 存在

### 错误 3:`make 失败`

看 build.log 里 `make.log` 的 tail:
```bash
tail -30 .tmp/ffmpeg-build/ffmpeg-src/make.log
```

常见原因:
- 汇编错误(nasm/yasm):脚本已用 `--disable-asm` 规避,不应出现。若出现检查是不是改过脚本
- 并发冲突:试试 `JOBS=1 ./scripts/build-ffmpeg-minimal.sh`(单线程编,慢但稳)

### 错误 4:configure 白名单没生效(filter 还是缺)

**确认脚本的白名单行**:
```bash
grep "enable-filter" scripts/build-ffmpeg-minimal.sh
# 期望输出:
# --enable-filter=aresample,aformat,anull,silencedetect,atrim,asetpts,concat \
```

如果输出不含这 4 个 filter,说明代码没同步对:
```bash
git pull   # 或重新 rsync 代码
grep "enable-filter" scripts/build-ffmpeg-minimal.sh  # 再确认
```

### 错误 5:产物体积异常

- 正常体积:**7-10MB**(zip 解压后 ffmpeg.exe + ffprobe.exe 各 3-4MB)
- >15MB:裁剪失效(`--disable-everything` 没生效)
- <5MB:编译不完整

```bash
ls -lh internal/runtime/assets/ffmpeg.zip  # 期望 7-10MB
unzip -l internal/runtime/assets/ffmpeg.zip | grep ".exe"
```

## 五、给 AI 的精简指令

```
回 Linux 机器执行:

1. cd 到 hzm 仓库根目录

2. 彻底清理编译缓存(关键!上次就是缓存没清导致 ffmpeg.exe 是旧的):
   rm -rf .tmp/ffmpeg-build/

3. 重新编译,保留完整日志:
   ./scripts/build-ffmpeg-minimal.sh 2>&1 | tee build.log

4. 编译完成后,立即验证产物时间戳(必须是今天,不能是 Jul 13):
   ls -la .tmp/ffmpeg-build/staging/bin/
   unzip -l internal/runtime/assets/ffmpeg.zip
   # ffmpeg.exe 和 ffprobe.exe 的时间戳都必须是今天

5. 如果时间戳是旧的,说明编译没真跑,读 build.log 找 configure/make 的真实错误并修复

6. 确认 configure 白名单含 4 个 VAD filter:
   grep "enable-filter" scripts/build-ffmpeg-minimal.sh
   # 期望含: silencedetect,atrim,asetpts,concat

7. 报告:ffmpeg.exe 时间戳、SHA256、verify 结果(若 Linux 有 Wine 可跑 verify)

不要修改 scripts/build-ffmpeg-minimal.sh 的 configure 白名单(已正确配置)。
如果 make/configure 报错,把 build.log 里相关错误贴出来,不要凭猜测改脚本。
```