#!/usr/bin/env bash
#
# build-ffmpeg-minimal.sh — 为 Hikami-Go 交叉编译裁剪版 ffmpeg/ffprobe（Windows x64 静态）
#
# 目的：完整 BtbN gpl 版 ffmpeg 解压后约 80MB，绝大部分是本项目用不到的视频编解码器
# （h264/h265/vp9/av1…）和无关协议。本项目对 ffmpeg 的实际用量是：
#   - 直播录制 (live_record)：FLV 流经 stdin → -c:a copy 抽音轨 → m4a   【零编码器】
#   - 录制分段合并 (manager.go)：-f concat -c copy                       【零编码器】
#   - download 多P合并：-f concat -c copy + ffprobe 时长探测              【零编码器】
#   - normalize 转码：-vn -ac 1 -ar 16000 -b:a 64k -f mp3                 【libmp3lame encoder】
#   - importer 导入转码：-vn -c:a aac → m4a                               【aac encoder（原生）】
# 裁剪后只保留上述 demuxer/muxer/encoder/decoder，预计 8-12MB（约完整版的 1/8）。
#
# 注意：ffmpeg 没有原生 MP3 编码器，normalize 的 -f mp3 必须依赖 libmp3lame。
# 本脚本在容器内从源码交叉编译 lame 静态库后链入 ffmpeg（lame 是 LGPL，兼容）。
#
# 用法：
#   ./scripts/build-ffmpeg-minimal.sh            # 用默认 ffmpeg tag
#   FFMPEG_TAG=n7.1 ./scripts/build-ffmpeg-minimal.sh
#   ./scripts/build-ffmpeg-minimal.sh --no-docker  # 在 Linux 宿主机直接编（需装 mingw-w64 + lame-dev）
#
# 依赖（默认 Docker 模式）：本机有 docker 且当前用户能访问 docker daemon。
# 依赖（--no-docker 模式）：apt install gcc-mingw-w64-x86-64 make wget pkg-config git
#                          + 已交叉编译好的 libmp3lame（指向 LAME_PREFIX 环境变量）
#
# 产出：internal/runtime/assets/ffmpeg.zip（含 bin/ffmpeg.exe + bin/ffprobe.exe + LICENSE.txt）
# 末尾打印 SHA256，供 ffmpeg_manifest.go 的 ArchiveSHA256 填写（当前嵌入路径不校验，可选）。

set -uo pipefail   # 注意：不设 -e，容器内各步骤自行检查退出码（见 die）

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
ASSETS_DIR="${REPO_ROOT}/internal/runtime/assets"
OUT_ZIP="${ASSETS_DIR}/ffmpeg.zip"

FFMPEG_TAG="${FFMPEG_TAG:-n7.1}"            # ffmpeg 源码 tag（见 https://github.com/FFmpeg/FFmpeg/tags）
LAME_TAG="${LAME_TAG:-3.100}"               # lame 源码版本
JOBS="${JOBS:-$(nproc 2>/dev/null || echo 4)}"

USE_DOCKER=1
if [[ "${1:-}" == "--no-docker" ]]; then
  USE_DOCKER=0
fi

die() { echo "ERROR: $*" >&2; exit 1; }

echo "==> 配置：FFMPEG_TAG=${FFMPEG_TAG}  LAME_TAG=${LAME_TAG}  JOBS=${JOBS}  USE_DOCKER=${USE_DOCKER}"

# ---------- 内部：实际编译逻辑（在容器或宿主机里执行）----------
# 参数：$1=FFMPEG_TAG  $2=LAME_TAG  $3=JOBS
build_inner() {
  local tag="$1"
  local lame_tag="$2"
  local jobs="$3"
  local workdir="${WORKDIR:-/tmp/ffmpeg-build}"
  local prefix="${workdir}/install"
  local lame_prefix="${workdir}/lame-install"

  echo "==> [inner] 工作目录 ${workdir}"
  mkdir -p "${workdir}"
  cd "${workdir}"

  # 1. 交叉编译 libmp3lame 静态库（normalize 的 -f mp3 必需）
  if [[ ! -f "${lame_prefix}/lib/libmp3lame.a" ]]; then
    echo "==> [inner] 交叉编译 libmp3lame ${lame_tag}"
    if [[ ! -d "lame-${lame_tag}/.git" ]] && [[ ! -d "lame-${lame_tag}/configure" ]]; then
      wget -q "https://downloads.sourceforge.net/project/lame/lame/${lame_tag}/lame-${lame_tag}.tar.gz" -O lame.tgz
      tar xzf lame.tgz
    fi
    cd "lame-${lame_tag}"
    ./configure --host=x86_64-w64-mingw32 --enable-static --disable-shared \
                --disable-frontend --prefix="${lame_prefix}" >/dev/null 2>&1 \
      || die "lame configure 失败"
    make -j"${jobs}" >/dev/null 2>&1 || die "lame make 失败"
    make install >/dev/null 2>&1 || die "lame install 失败"
    cd "${workdir}"
    echo "    libmp3lame.a: $(ls -l ${lame_prefix}/lib/libmp3lame.a | awk '{print $5}') bytes"
  else
    echo "==> [inner] 复用已编译 libmp3lame"
  fi

  # 2. 取 ffmpeg 源码
  local src="ffmpeg-src"
  if [[ ! -d "${src}/.git" ]]; then
    echo "==> [inner] 克隆 ffmpeg ${tag}"
    git clone --depth 1 --branch "${tag}" https://github.com/FFmpeg/FFmpeg.git "${src}"
  else
    echo "==> [inner] 复用已有源码 ${src}"
  fi
  cd "${src}"

  # 3. configure —— 裁剪核心
  #    设计依据见文件头注释。关键点：
  #    - --disable-everything 关掉全部，再按白名单逐个 enable，杜绝冗余。
  #    - --disable-autodetect 不自动探测系统库，保证静态 + 无外部依赖（libmp3lame 除外，显式指定）。
  #    - --disable-asm：MinGW 交叉编译下 x86inc 汇编常踩坑（nasm/yasm 配置差异），
  #      先关汇编保证能一次编通；体积代价可接受（纯 C 实现）。如需更小体积，
  #      可装 nasm 后去掉 --disable-asm。
  #    - demuxer：flv(直播)、concat(合并)、mov/aac/mp3/wav/flac/ogg/matroska/asf(导入/探测兜底)
  #    - muxer：mp4+mov(m4a)、mp3(normalize)、null(ffprobe)、matroska(兜底)
  #    - encoder：libmp3lame(normalize，外部库)、aac(importer，原生)
  #      ⚠️ ffmpeg 无原生 mp3 编码器，写 mp3 必须用 libmp3lame（注册名 ff_libmp3lame_encoder）
  #    - decoder：覆盖录制/导入可能遇到的音频编码（AAC/MP3/FLAC/Vorbis/Opus/PCM）
  #    - protocol：file（读写本地音频文件，必需！）、pipe（直播录制 stdin）
  #      ⚠️ --disable-everything 会关掉 file 协议，必须显式补回，否则 ffmpeg 无法
  #      读写任何本地文件（2026-07-13 Windows 验证发现的 bug，见 docs/验证报告.md）。
  #    - parser/bsf：AAC 的 ADTS↔ASC 转换（flv→m4a remux 必需 aac_adtstoasc）
  #    - filter：aresample/aformat/anull。normalize 的 -ac 1 -ar 16000 会让 ffmpeg 自动
  #      插入 aresample（重采样）+ aformat（格式协商）filter，--disable-everything 关掉了
  #      所有 filter，必须显式补这几个（swresample 库本身默认编入，但它的 filter 包装接口
  #      需单独 enable）。见 docs/验证报告.md 缺陷 A。
  #    - muxer 里的 ipod：注册扩展名 m4v,m4a,m4b——ipod 是 mov 家族里唯一声明 .m4a 别名的
  #      muxer（mp4 只认 .mp4、mov 只认 .mov）。启用它让 importer/record/concat 的 .m4a
  #      输出能被 ffmpeg 自动推断到 mov 家族，否则报 "Unable to choose an output format"。
  #      见 docs/验证报告.md 缺陷 B。
  echo "==> [inner] configure ffmpeg"
  ./configure \
    --target-os=mingw64 \
    --arch=x86_64 \
    --cross-prefix=x86_64-w64-mingw32- \
    --prefix="${prefix}" \
    --disable-everything \
    --disable-doc \
    --disable-debug \
    --disable-autodetect \
    --disable-network \
    --disable-iconv \
    --disable-asm \
    --enable-gpl \
    --enable-ffmpeg \
    --enable-ffprobe \
    --enable-libmp3lame \
    --enable-protocol=file,pipe \
    --enable-demuxer=flv,concat,mov,mp3,aac,wav,flac,ogg,matroska,asf \
    --enable-muxer=mp4,mov,ipod,mp3,null,matroska \
    --enable-encoder=libmp3lame,aac \
    --enable-decoder=aac,mp3,mp3float,flac,vorbis,opus,pcm_s16le,pcm_s8 \
    --enable-parser=aac,mpegaudio \
    --enable-bsf=aac_adtstoasc \
    --enable-filter=aresample,aformat,anull \
    --extra-cflags="-I${lame_prefix}/include" \
    --extra-ldflags="-L${lame_prefix}/lib" \
    >configure.log 2>&1 || { tail -30 configure.log; die "ffmpeg configure 失败"; }

  echo "==> [inner] make -j${jobs}"
  make -j"${jobs}" >make.log 2>&1 || { tail -30 make.log; die "ffmpeg make 失败"; }

  echo "==> [inner] 校验产物存在"
  test -s ffmpeg.exe || die "ffmpeg.exe 未产出"
  test -s ffprobe.exe || die "ffprobe.exe 未产出"

  echo "==> [inner] 列出启用的关键能力（抽查裁剪是否生效）"
  ./ffprobe.exe -version 2>&1 | head -2 || true
  echo "--- encoders（确认 libmp3lame + aac）---"
  ./ffprobe.exe -encoders 2>&1 | grep -iE 'mp3|aac' || true

  # 把产物挪到 workdir 下的稳定路径，供外层打包
  mkdir -p "${workdir}/staging/bin"
  cp -f ffmpeg.exe  "${workdir}/staging/bin/"
  cp -f ffprobe.exe "${workdir}/staging/bin/"
  # 许可证：ffmpeg GPLv3 + lame LGPL
  cp -f LICENSE.txt "${workdir}/staging/" 2>/dev/null \
    || cp -f COPYING.GPLv3 "${workdir}/staging/LICENSE.txt" 2>/dev/null || true
  cp -f "${workdir}/lame-${lame_tag}/COPYING" "${workdir}/staging/LICENSE.lame.txt" 2>/dev/null || true
  echo "==> [inner] 完成，staging 在 ${workdir}/staging"
}

# ---------- 打包（宿主机侧）----------
pack() {
  local staging="$1"
  command -v zip >/dev/null 2>&1 || die "宿主机未装 zip（apt install zip）"
  mkdir -p "${ASSETS_DIR}"
  rm -f "${OUT_ZIP}"

  # zip 内部路径布局须与 ffmpeg_manifest.go 的 FFmpegPath/FFprobePath 一致：
  # 当前 manifest 用 bin/ffmpeg.exe、bin/ffprobe.exe。
  ( cd "${staging}" && zip -r -X "${OUT_ZIP}" bin LICENSE.txt LICENSE.lame.txt >/dev/null )

  echo "==> 打包完成：${OUT_ZIP}"
  ls -lh "${OUT_ZIP}"
  echo "==> SHA256："
  sha256sum "${OUT_ZIP}"
}

# ---------- 入口 ----------
if [[ "${USE_DOCKER}" == "1" ]]; then
  command -v docker >/dev/null 2>&1 || die "未找到 docker。要么安装 docker，要么用 --no-docker（需 mingw-w64 + libmp3lame）。"
  HOST_WORKDIR="${REPO_ROOT}/.tmp/ffmpeg-build"
  mkdir -p "${HOST_WORKDIR}"
  echo "==> Docker 模式，构建产物挂载在 ${HOST_WORKDIR}"

  docker run --rm \
    -e WORKDIR=/work \
    -e FFMPEG_TAG="${FFMPEG_TAG}" \
    -e LAME_TAG="${LAME_TAG}" \
    -v "${HOST_WORKDIR}:/work" \
    gcc:13-bookworm bash -c '
      set -uo pipefail
      echo "[docker] 安装 mingw-w64 + wget"
      apt-get update -qq >/dev/null 2>&1 && apt-get install -y -qq make mingw-w64 wget pkg-config git >/dev/null 2>&1
      '"$(declare -f build_inner die)"'
      build_inner "${FFMPEG_TAG}" "${LAME_TAG}" "'"${JOBS}"'"
    '
  STAGING="${HOST_WORKDIR}/staging"
else
  # --no-docker：宿主机需已装 mingw-w64 + 预编译 lame
  command -v x86_64-w64-mingw32-gcc >/dev/null 2>&1 || die "--no-docker 模式需要 x86_64-w64-mingw32-gcc（apt install gcc-mingw-w64-x86-64）。"
  HOST_WORKDIR="${REPO_ROOT}/.tmp/ffmpeg-build"
  export WORKDIR="${HOST_WORKDIR}"
  build_inner "${FFMPEG_TAG}" "${LAME_TAG}" "${JOBS}"
  STAGING="${HOST_WORKDIR}/staging"
fi

[[ -f "${STAGING}/bin/ffmpeg.exe" ]] || die "编译未产出 ffmpeg.exe，staging=${STAGING}"

pack "${STAGING}"

echo ""
echo "==> 全部完成。"
echo "    产物：${OUT_ZIP}"
echo "    下一步：运行 ./scripts/verify-ffmpeg-minimal.sh 在 Windows 上验证（或交由 CI 验证）。"
echo "    提示：把上面打印的 SHA256 记到 internal/runtime/ffmpeg_manifest.go（可选，嵌入路径不校验）。"
