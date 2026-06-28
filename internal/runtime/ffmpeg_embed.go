//go:build embed_ffmpeg

package runtime

import _ "embed"

//go:embed assets/ffmpeg.zip
var embedFFmpegZip []byte

func embedAssets() ([]byte, bool) {
	if len(embedFFmpegZip) == 0 {
		return nil, false
	}
	return embedFFmpegZip, true
}
