package runtime

import goruntime "runtime"

// FFmpegAsset 描述一个平台的 ffmpeg 发行物。嵌入与下载两条解析路径共用本结构：
//   - 嵌入路径（embed_ffmpeg build tag）读取编译期打进去的 assets/ffmpeg.zip，由
//     internal/runtime/assets/ffmpeg.zip 提供——该 zip 是裁剪版（仅含本项目用到的
//     flv/concat/mov/mp3 demuxer/muxer + mp3/aac encoder），由 scripts/build-ffmpeg-minimal.sh
//     产出，体积约 8-12MB，zip 顶层直接是 bin/（无 BtbN 完整版那层
//     ffmpeg-master-latest-... 前缀目录）。详见 scripts/README-ffmpeg-build.md。
//   - 下载路径（embedAssets() 返回 nil 时）按 ArchiveURL 在线拉取。linux-* 指向 BtbN 完整
//     gpl 版作为兜底（约 80MB，是裁剪版的超集，功能上能跑）；windows-amd64 不走下载
//     （ArchiveURL 留空），因裁剪版 zip 与 BtbN 完整版的目录结构不同，下载了也找不到路径。
//
// Version 决定缓存目录 .runtime/ffmpeg/<platform>/<version>/，改 Version 会让旧用户升级后
// 重新解包，避免用到旧缓存。
type FFmpegAsset struct {
	Version       string
	FFmpegURL     string
	FFprobeURL    string
	FFmpegSHA256  string
	FFprobeSHA256 string
	ArchiveURL    string
	ArchiveSHA256 string
	ArchiveFormat string
	FFmpegPath    string
	FFprobePath   string
	LicenseURL    string
}

func CurrentManifest() map[string]FFmpegAsset {
	// 仅覆盖项目部署目标平台(windows-amd64 / linux-amd64 / linux-arm64);
	// 其他平台(如 darwin-arm64、linux-386)不在 manifest → ResolveFFmpeg 回退到系统 ffmpeg。
	//
	// embedded-minimal-7.x-vad：标识嵌入的裁剪版 ffmpeg（基于 n7.x，2026-07-27 起含 VAD 用 filter）。
	//   - linux-*：下载兜底用 BtbN 完整 gpl 版（裁剪超集），二者共用同一 version 缓存目录无功能问题。
	//   - windows-amd64：仅走 embedded（裁剪版 zip 顶层直接是 bin/，无 BtbN 那层
	//     ffmpeg-master-latest-... 前缀目录）。ArchiveURL 故意留空——embedded 解包失败
	//     回退到下载分支时空 URL 会立刻报错（downloadAndInstallFFmpeg 有空 URL 保护），
	//     而不是去拉 80MB 完整版（且完整版目录结构与裁剪版 zip 不同，下载了也找不到路径）。
	//
	// 2026-07-27:vad 后缀让旧用户升级时重新解包,因为旧 embedded-minimal-7.x 不含
	// silencedetect/atrim/asetpts/concat filter(VAD 会自动 fallback 原始音频,功能降级但不崩)。
	// 见 plans/plan-vad-2026-07-27.md Phase 6 + scripts/build-ffmpeg-minimal.sh。
	//
	// 2026-07-28:vad2 后缀:补 pcm_s16le encoder,修复 -f null - (VAD Detect 路径) 失败。
	// 旧 -vad 版缺 pcm_s16le encoder,silencedetect 的 -f null - 找不到 encoder 100% 失败,
	// 导致 VAD 功能完全失效。见 scripts/FFMPEG-REBUILD-PCM-S16LE-2026-07-28.md。
	const version = "embedded-minimal-7.x-vad2"
	const licenseURL = "https://github.com/BtbN/FFmpeg-Builds/blob/master/LICENSE"

	return map[string]FFmpegAsset{
		// 2026-08-15(M4):linux 下载兜底从可变的 latest tag 钉到具体 autobuild
		// (autobuild-2026-08-13-17-03)。latest tag 文件名稳定但内容每日重写,不钉版本
		// 无 SHA 校验可言,且 BtbN 改内部目录前缀时会静默找不到路径。钉版后补上
		// ArchiveSHA256(SHA256 取自该 release 的 checksums.sha256 资产)。
		// Version 保持不变:txz 解包此前 100% 失败,不存在已缓存的 linux 下载产物,无需
		// bump 失效缓存(windows 嵌入 zip 共用该 version,bump 会触发无谓重解包)。
		"linux-amd64": {
			Version:       version,
			ArchiveURL:    "https://github.com/BtbN/FFmpeg-Builds/releases/download/autobuild-2026-08-13-17-03/ffmpeg-N-126122-gca821e458a-linux64-gpl.tar.xz",
			ArchiveSHA256: "d9d80d1e161338b304ac4a5dab8cf7cd0e572b284b7ffbd17c12bdd517651d3e",
			ArchiveFormat: "txz",
			FFmpegPath:    "ffmpeg-N-126122-gca821e458a-linux64-gpl/bin/ffmpeg",
			FFprobePath:   "ffmpeg-N-126122-gca821e458a-linux64-gpl/bin/ffprobe",
			LicenseURL:    licenseURL,
		},
		"linux-arm64": {
			Version:       version,
			ArchiveURL:    "https://github.com/BtbN/FFmpeg-Builds/releases/download/autobuild-2026-08-13-17-03/ffmpeg-N-126122-gca821e458a-linuxarm64-gpl.tar.xz",
			ArchiveSHA256: "870529c905a63ef48c94adb67cc3751dfd42961de80fbe6b36dc87a56502e363",
			ArchiveFormat: "txz",
			FFmpegPath:    "ffmpeg-N-126122-gca821e458a-linuxarm64-gpl/bin/ffmpeg",
			FFprobePath:   "ffmpeg-N-126122-gca821e458a-linuxarm64-gpl/bin/ffprobe",
			LicenseURL:    licenseURL,
		},
		"windows-amd64": {
			Version:       version,
			ArchiveFormat: "zip",
			FFmpegPath:    "bin/ffmpeg.exe",
			FFprobePath:   "bin/ffprobe.exe",
			LicenseURL:    licenseURL,
		},
	}
}

func PlatformKey() string {
	return goruntime.GOOS + "-" + goruntime.GOARCH
}
