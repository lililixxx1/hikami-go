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
	// embedded-minimal-7.x：标识嵌入的是裁剪版 ffmpeg（基于 n7.x）。
	//   - linux-*：下载兜底用 BtbN 完整 gpl 版（裁剪超集），二者共用同一 version 缓存目录无功能问题。
	//   - windows-amd64：仅走 embedded（裁剪版 zip 顶层直接是 bin/，无 BtbN 那层
	//     ffmpeg-master-latest-... 前缀目录）。ArchiveURL 故意留空——embedded 解包失败
	//     回退到下载分支时空 URL 会立刻报错（downloadAndInstallFFmpeg 有空 URL 保护），
	//     而不是去拉 80MB 完整版（且完整版目录结构与裁剪版 zip 不同，下载了也找不到路径）。
	const version = "embedded-minimal-7.x"
	const licenseURL = "https://github.com/BtbN/FFmpeg-Builds/blob/master/LICENSE"

	return map[string]FFmpegAsset{
		"linux-amd64": {
			Version:       version,
			ArchiveURL:    "https://github.com/BtbN/FFmpeg-Builds/releases/download/latest/ffmpeg-master-latest-linux64-gpl.tar.xz",
			ArchiveFormat: "txz",
			FFmpegPath:    "ffmpeg-master-latest-linux64-gpl/bin/ffmpeg",
			FFprobePath:   "ffmpeg-master-latest-linux64-gpl/bin/ffprobe",
			LicenseURL:    licenseURL,
		},
		"linux-arm64": {
			Version:       version,
			ArchiveURL:    "https://github.com/BtbN/FFmpeg-Builds/releases/download/latest/ffmpeg-master-latest-linuxarm64-gpl.tar.xz",
			ArchiveFormat: "txz",
			FFmpegPath:    "ffmpeg-master-latest-linuxarm64-gpl/bin/ffmpeg",
			FFprobePath:   "ffmpeg-master-latest-linuxarm64-gpl/bin/ffprobe",
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
