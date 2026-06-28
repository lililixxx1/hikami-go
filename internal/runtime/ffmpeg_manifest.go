package runtime

import goruntime "runtime"

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
	const version = "master-latest"
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
			ArchiveURL:    "https://github.com/BtbN/FFmpeg-Builds/releases/download/latest/ffmpeg-master-latest-win64-gpl-shared.zip",
			ArchiveFormat: "zip",
			FFmpegPath:    "ffmpeg-master-latest-win64-gpl-shared/bin/ffmpeg.exe",
			FFprobePath:   "ffmpeg-master-latest-win64-gpl-shared/bin/ffprobe.exe",
			LicenseURL:    licenseURL,
		},
	}
}

func PlatformKey() string {
	return goruntime.GOOS + "-" + goruntime.GOARCH
}
